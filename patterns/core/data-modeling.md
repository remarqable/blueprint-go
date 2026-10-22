# Data Modeling

> How to shape data for a domain, as distinct from how to write the code that
> touches it.

Most decisions in this blueprint are reversible in an afternoon. Data shape is
not. A schema that conflates what happened with what you concluded, or that
overwrites rather than supersedes, cannot be fixed later by better code — the
information you would need was never written down.

This document covers the shapes that are expensive to get wrong.

## Table of Contents

- [Evidence and Derivation](#evidence-and-derivation)
- [Temporal Data](#temporal-data)
- [Superseding, Not Overwriting](#superseding-not-overwriting)
- [Entities, Edges, Evidence](#entities-edges-evidence)
- [Identity Resolution](#identity-resolution)
- [Closed Vocabularies First](#closed-vocabularies-first)
- [Querying a Graph in Postgres](#querying-a-graph-in-postgres)
- [Deletion](#deletion)
- [When You Need a Graph Database](#when-you-need-a-graph-database)

---

## Evidence and Derivation

Split every domain into two kinds of table and never mix them.

**Evidence** is what actually happened, as recorded at the time. A message
someone sent, a file they uploaded, a click, a payment. It is append-only and
immutable. If it turns out to be wrong, it is still what happened.

**Derivation** is what you concluded from evidence. A summary, an extracted
request, a computed score, a category. It is revisable, rebuildable, and always
traceable to the evidence that produced it.

```
message          ← evidence: immutable, user-authored
message_edit     ← evidence: the edit is itself an event, not a mutation
fact             ← derivation: revisable, cites evidence
fact_evidence    ← the citation
```

The test: **can you delete every derivation table and rebuild it from
evidence?** If not, something irreplaceable is stored in a table you treat as
disposable, and a backfill will destroy it.

This is why an edit is a new row rather than an `UPDATE`. The moment you
overwrite the body of a message, you lose the ability to explain why a
conclusion drawn last week no longer follows from the text in front of you.

---

## Temporal Data

Two different times, routinely confused, and the confusion is unrecoverable
later:

- **Valid time** — when the fact was true in the world. The decision was made on
  Tuesday.
- **Transaction time** — when the system learned it. You recorded it on Friday.

You need both whenever the system can learn things late or be corrected, which
is any system that interprets rather than merely records.

```sql
CREATE TABLE fact (
  id          BIGSERIAL PRIMARY KEY,
  tenant_id   BIGINT NOT NULL REFERENCES tenant(id),
  kind        TEXT   NOT NULL,
  body        JSONB  NOT NULL,

  valid_from  TIMESTAMPTZ NOT NULL,   -- true in the world from
  valid_to    TIMESTAMPTZ,            -- NULL = still true
  recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),  -- system learned it
  retracted_at TIMESTAMPTZ,           -- system stopped believing it

  superseded_by BIGINT REFERENCES fact(id)
);

-- Current beliefs about current reality.
CREATE INDEX idx_fact_current ON fact (tenant_id, kind)
  WHERE valid_to IS NULL AND retracted_at IS NULL;
```

Two clauses, two questions:

```sql
-- What is true now, as far as we currently believe?
WHERE valid_to IS NULL AND retracted_at IS NULL

-- What did we believe on 1 March about the state of the world on 1 February?
WHERE valid_from <= '2026-02-01' AND (valid_to   IS NULL OR valid_to   > '2026-02-01')
  AND recorded_at <= '2026-03-01' AND (retracted_at IS NULL OR retracted_at > '2026-03-01')
```

The second query is the one that justifies the whole design. "What did we think
the pricing was before we changed it" and "why did the report say that last
month" are unanswerable without both axes, and no amount of later engineering
recovers them.

Full bitemporality is not free — every read grows two predicates. Add valid time
to tables where the world changes under you, and transaction time to tables
where your beliefs change. Most tables need neither; `created_at`/`updated_at`
is right for a user's display name.

---

## Superseding, Not Overwriting

A correction closes the old row and opens a new one. It never edits in place.

```go
func Supersede(ctx context.Context, tx *gorm.DB, oldID int64, next Fact) (int64, error) {
  now := time.Now()

  var newID int64
  if err := tx.WithContext(ctx).Raw(`
    INSERT INTO fact (tenant_id, kind, body, valid_from, recorded_at, source)
    VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
    next.TenantID, next.Kind, next.Body, now, now, next.Source,
  ).Scan(&newID).Error; err != nil {
    return 0, err
  }

  res := tx.WithContext(ctx).Exec(`
    UPDATE fact SET valid_to = ?, superseded_by = ?
     WHERE id = ? AND valid_to IS NULL`, now, newID, oldID)
  if res.Error != nil {
    return 0, res.Error
  }
  if res.RowsAffected == 0 {
    return 0, errors.New("fact already superseded")  // lost the race; retry
  }
  return newID, nil
}
```

Both statements in one transaction, and the `AND valid_to IS NULL` guard makes a
concurrent double-supersede fail rather than producing two live chains.

Rules that hold across any domain doing this:

- **Record who superseded and why.** A `source` column (`'user'`, `'model'`,
  `'rule'`, `'import'`) is the minimum. Without it you cannot tell a human
  correction from an automated re-derivation.
- **Human corrections are sticky.** If a person corrected a fact, an automated
  process must not supersede it back. Otherwise the next reprocessing reasserts
  the original error and the user learns their correction is decorative.
- **Chains, not trees.** One live fact per subject. `superseded_by` forms a
  linked list you can walk backwards to reconstruct history.

---

## Entities, Edges, Evidence

When the domain is a set of things and relationships between them — not rows in
independent tables — three tables carry it:

```sql
-- The things.
CREATE TABLE entity (
  id           BIGSERIAL PRIMARY KEY,
  tenant_id    BIGINT NOT NULL REFERENCES tenant(id),
  kind         TEXT   NOT NULL,        -- person | project | topic | resource
  canonical_id BIGINT REFERENCES entity(id),  -- set when merged into another
  name         TEXT   NOT NULL,
  attrs        JSONB  NOT NULL DEFAULT '{}'::jsonb,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The relationships. Typed, directed, temporal.
CREATE TABLE edge (
  id          BIGSERIAL PRIMARY KEY,
  tenant_id   BIGINT NOT NULL REFERENCES tenant(id),
  src_id      BIGINT NOT NULL REFERENCES entity(id),
  dst_id      BIGINT NOT NULL REFERENCES entity(id),
  kind        TEXT   NOT NULL,         -- owns | requested_from | affects
  attrs       JSONB  NOT NULL DEFAULT '{}'::jsonb,
  confidence  REAL   NOT NULL DEFAULT 1.0,

  valid_from  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  valid_to    TIMESTAMPTZ,
  superseded_by BIGINT REFERENCES edge(id)
);

-- Why you believe any of it.
CREATE TABLE edge_evidence (
  edge_id    BIGINT NOT NULL REFERENCES edge(id) ON DELETE CASCADE,
  message_id BIGINT NOT NULL REFERENCES message(id) ON DELETE CASCADE,
  PRIMARY KEY (edge_id, message_id)
);

CREATE INDEX idx_edge_out ON edge (tenant_id, src_id, kind) WHERE valid_to IS NULL;
CREATE INDEX idx_edge_in  ON edge (tenant_id, dst_id, kind) WHERE valid_to IS NULL;
CREATE INDEX idx_entity_kind ON entity (tenant_id, kind) WHERE canonical_id IS NULL;
```

Both directional indexes are required. Traversals go both ways ("what does Aidan
own", "who owns this"), and an index on one direction makes the other a
sequential scan of the whole table.

**`attrs` is for attributes you only display. Anything you filter or join on
gets a column.** JSONB with a GIN index is genuinely fast, but you cannot
constrain it, cannot foreign-key it, and cannot see it in a schema diff. Start
narrow: promote a key to a column the first time you write a `WHERE` against it.

---

## Identity Resolution

"Aidan", "@aidan", "Aidan K", and `aidan@example.com` are one person. Resolving
that is the hardest part of building a graph from unstructured input, and the
part people underestimate.

Two rules:

**Merge by pointer, never by deletion.** `canonical_id` marks an entity as
merged into another. Nothing is deleted, edges are not rewritten, and reads
follow the pointer. A wrong merge is then a one-row `UPDATE` to undo, rather
than an unrecoverable loss.

```sql
CREATE VIEW entity_resolved AS
SELECT e.id, COALESCE(c.id, e.id) AS canonical, COALESCE(c.name, e.name) AS name
  FROM entity e LEFT JOIN entity c ON c.id = e.canonical_id;
```

**Keep aliases as evidence.** An `entity_alias(entity_id, alias, source)` table
lets you show why a match was made, and lets the next extraction resolve the
same surface form without re-deciding.

Bias toward under-merging. Two rows for one person is a visible annoyance a user
will point out. Two people merged into one silently attributes someone's work,
requests, and words to a colleague, and is discovered late and badly.

---

## Closed Vocabularies First

`kind` on entities and edges is an enum enforced in application code, and it is
a short list you wrote deliberately.

The temptation — especially with a model producing the data — is to let kinds be
free text so new concepts emerge on their own. What actually emerges is
`decision`, `Decision`, `decison`, `decision_made`, and `possible_decision`,
each with a handful of rows, and no query that returns all decisions.

```go
var EntityKinds = map[string]bool{
  "person": true, "project": true, "topic": true,
  "resource": true, "request": true, "decision": true,
}
```

Anything outside the set is rejected and counted. When the rejection log shows
the same unrecognised kind repeatedly, that is real evidence for adding it —
deliberately, with a migration and a display name and a query that uses it.

Open vocabularies are a feature to earn, not a starting position. Adding a kind
later is a one-line change; un-fragmenting a year of free-text kinds is a
migration you will not enjoy.

---

## Querying a Graph in Postgres

One hop is a join. Several hops is a recursive CTE:

```sql
WITH RECURSIVE reachable AS (
  SELECT dst_id AS id, 1 AS depth
    FROM edge
   WHERE src_id = $1 AND valid_to IS NULL

  UNION                                    -- UNION, not UNION ALL: cycles exist
  SELECT e.dst_id, r.depth + 1
    FROM edge e JOIN reachable r ON e.src_id = r.id
   WHERE e.valid_to IS NULL AND r.depth < 4
)
SELECT * FROM reachable;
```

Two non-negotiables: `UNION` rather than `UNION ALL` so cycles terminate, and an
explicit depth cap. A graph built from real conversation is densely connected
and a four-hop traversal without a cap will attempt to return most of it.

In practice the useful queries are one and two hops — "what is attached to this
topic", "who is waiting on whom" — and those are plain joins. If you find
yourself needing six-hop traversals to answer product questions, the question is
usually wrong before the database is.

---

## Deletion

Evidence and derivation delete differently, and a single "delete" verb across
both is a bug.

- **Derivation**: hard delete freely. It rebuilds from evidence.
- **Evidence**: soft delete (`deleted_at`), because something downstream cites it
  and a dangling citation is worse than a tombstone.
- **Legal erasure** (GDPR and similar): actually destroy the content, keep the
  row. Null the body, keep the id, timestamps, and structural links. Citations
  stay resolvable and render as "deleted", and the graph does not develop holes.

```sql
UPDATE message SET body = NULL, deleted_at = NOW(), redacted = true WHERE id = $1;
```

Decide this before launch. Retrofitting erasure onto a schema where derived
tables copied content rather than citing it means hunting the same text through
a dozen tables under a deadline.

---

## When You Need a Graph Database

Almost certainly not.

Postgres handles graph workloads well into the tens of millions of edges with
the indexes above. A dedicated graph database earns its place when traversal
depth is genuinely unbounded and central to the product — fraud rings, network
topology, pathfinding — and it costs you transactions with the rest of your
data, a second operational system, and joins to your relational tables becoming
application-level work.

The realistic trigger is a specific recursive CTE that is too slow after
indexing and denormalisation, not the observation that the domain is
graph-shaped. Most graph-shaped domains have relational access patterns.
