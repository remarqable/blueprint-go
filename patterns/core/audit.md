# Record Audit: created_by and updated_by

> Who created this row, and who last changed it. Two nullable columns, filled by
> GORM from the user acting in the request. This is **not** an audit log; see
> [This is not an audit log](#this-is-not-an-audit-log).

---

## Table of Contents

- [Overview](#overview)
- [Schema](#schema)
- [Model](#model)
- [The Actor in Context](#the-actor-in-context)
- [Filling the Columns](#filling-the-columns)
- [Jobs and System Actors](#jobs-and-system-actors)
- [Tenancy and RLS](#tenancy-and-rls)
- [Raw SQL](#raw-sql)
- [Showing Names](#showing-names)
- [Testing](#testing)
- [This is not an audit log](#this-is-not-an-audit-log)
- [Checklist](#checklist)

---

## Overview

Two system-managed columns on business tables:

- **`created_by_user_id`**: the user who created the row. Set once, never
  changed.
- **`updated_by_user_id`**: the user who made the last change. Rewritten on
  every update.

The names follow [database.md](database.md#naming-style): a foreign key is
`<entity>_id`, and `created_by_user_id` is that document's own example. Code
and UI call them "created by" and "updated by".

Users never set them. They come from the authenticated user in the request
context, through two GORM callbacks registered once at startup, so a model or
controller cannot forget them and a form cannot forge them.

Add them to tables people edit: posts, contacts, invoices, settings. Skip them
on tables only the system writes (jobs, `ai_call`, derived data), where they
would always be NULL.

---

## Schema

```sql
-- migrations/00X_post.sql
-- +goose Up
CREATE TABLE post (
  id                 BIGSERIAL PRIMARY KEY,
  tenant_id          BIGINT NOT NULL REFERENCES tenant(id),
  user_id            BIGINT NOT NULL REFERENCES "user"(id),
  title              TEXT NOT NULL,
  content            TEXT,
  created_by_user_id BIGINT REFERENCES "user"(id) ON DELETE SET NULL,
  updated_by_user_id BIGINT REFERENCES "user"(id) ON DELETE SET NULL,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS post;
```

Adding them to an existing table:

```sql
-- migrations/00Y_post_audit_columns.sql
-- +goose Up
ALTER TABLE post
  ADD COLUMN created_by_user_id BIGINT REFERENCES "user"(id) ON DELETE SET NULL,
  ADD COLUMN updated_by_user_id BIGINT REFERENCES "user"(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE post
  DROP COLUMN IF EXISTS updated_by_user_id,
  DROP COLUMN IF EXISTS created_by_user_id;
```

Why each choice:

- **Nullable.** Existing rows have no known creator, anonymous submissions
  (a public contact form) have no user, and system work is done by no one. NULL
  means "not a person", never "unknown bug". Do not backfill a guess.
- **`ON DELETE SET NULL`.** The row belongs to the tenant, not to whoever typed
  it. Deleting a user must neither delete their colleagues' records (`CASCADE`)
  nor be blocked by them (the default `NO ACTION`). It is also what legal
  erasure needs; see [data-modeling.md](data-modeling.md#deletion).
- **Separate from `user_id`.** `user_id` is ownership or assignment, a business
  fact users can change ("reassign to Bob"). `created_by_user_id` is history and
  cannot change.
- **No index by default.** PostgreSQL does not index foreign keys, so deleting a
  user scans each table that references it. User deletion is rare; on small
  tables that is fine. Index when you list "created by me"
  (`(tenant_id, created_by_user_id)`, tenant first, per
  [tenancy.md](../tenancy.md#performance)), or when the scan on the dominant
  table gets slow (a partial index on the column alone,
  `WHERE created_by_user_id IS NOT NULL`).

On SQLite the same DDL works, and the `ON DELETE` action only runs with foreign
keys on (`_foreign_keys=on` in the DSN, as the blueprint's SQLite URL has).

---

## Model

```go
// internal/models/audited.go
package models

// Audited is embedded in models whose tables carry the two audit columns.
// The columns are system-managed: the binding tags stop a form or JSON body
// from setting them. Expose them in API responses through a response struct.
type Audited struct {
  CreatedByUserID *int64 `gorm:"column:created_by_user_id" json:"-" form:"-"`
  UpdatedByUserID *int64 `gorm:"column:updated_by_user_id" json:"-" form:"-"`
}

// CreatedBy and UpdatedBy return display names from a map loaded once per page.
// See § Showing Names.
func (a Audited) CreatedBy(names map[int64]string) string { return nameOf(a.CreatedByUserID, names) }
func (a Audited) UpdatedBy(names map[int64]string) string { return nameOf(a.UpdatedByUserID, names) }

func nameOf(id *int64, names map[int64]string) string {
  if id == nil {
    return ""
  }
  return names[*id]
}
```

```go
// internal/models/post.go
type Post struct {
  ID       int64  `gorm:"primaryKey"`
  TenantID int64  `gorm:"not null"`
  UserID   int64  `gorm:"not null"`
  Title    string `gorm:"not null"`
  Content  string
  Audited
  CreatedAt time.Time
  UpdatedAt time.Time
}

func (Post) TableName() string { return "post" }
```

Pointers, because the columns are nullable: a plain `int64` would write `0`,
which violates the foreign key.

---

## The Actor in Context

GORM callbacks see the statement's `context.Context`, not the `*gin.Context`.
So the acting user goes into the request context, set by the authentication
middleware and by nothing else:

```go
// internal/platform/actor/actor.go
package actor

import "context"

type ctxKey struct{}

// With returns ctx carrying the user responsible for writes made under it.
// Only authentication middleware and the job worker call this.
func With(ctx context.Context, userID int64) context.Context {
  return context.WithValue(ctx, ctxKey{}, userID)
}

// From returns the acting user. No actor means system or anonymous.
func From(ctx context.Context) (int64, bool) {
  id, ok := ctx.Value(ctxKey{}).(int64)
  return id, ok && id != 0
}
```

In the auth middleware ([auth.md](auth.md)), next to the existing `c.Set`
calls:

```go
c.Set("user_id", session.UserID)
c.Set("user_email", session.Email)
c.Request = c.Request.WithContext(actor.With(c.Request.Context(), session.UserID))
```

`obs.Trace` also carries a user ID, for log lines
([observability.md](observability.md#correlation)). The actor is a separate key
on purpose: it is written to the database, so only authentication may set it.

Controllers already pass `c.Request.Context()` to `db.WithTenant` and
`WithContext`, which is how the actor reaches the callbacks. A query run
without the request context is a query run by no one.

---

## Filling the Columns

```go
// internal/platform/actor/gorm.go
package actor

import (
  "errors"
  "slices"

  "gorm.io/gorm"
)

const (
  createdBy = "created_by_user_id"
  updatedBy = "updated_by_user_id"
)

// RegisterGORM fills the audit columns on every model that has them.
// Call once at startup, on every handle (runtime and owner).
func RegisterGORM(g *gorm.DB) error {
  cb := g.Callback()
  return errors.Join(
    cb.Create().Before("gorm:create").Register("actor:created_by", setCreatedBy),
    cb.Update().Before("gorm:update").Register("actor:updated_by", setUpdatedBy),
  )
}

func audited(tx *gorm.DB, column string) bool {
  return tx.Statement.Schema != nil && tx.Statement.Schema.LookUpField(column) != nil
}

func setCreatedBy(tx *gorm.DB) {
  if !audited(tx, createdBy) {
    return
  }
  id, ok := From(tx.Statement.Context)
  if !ok {
    return // system or anonymous: leave whatever the caller set (NULL, or an import's original author)
  }
  // true: on a batch Create, set every element, not just the current one.
  tx.Statement.SetColumn(createdBy, id, true)
}

func setUpdatedBy(tx *gorm.DB) {
  if !audited(tx, updatedBy) {
    return
  }
  // created_by is written once. Omitting it from every update means a Save of
  // a struct bound from a form cannot rewrite who created the row.
  tx.Statement.Omits = append(tx.Statement.Omits, createdBy)

  // UpdateColumn(s) skips hooks and leaves updated_at alone; it leaves
  // updated_by alone too. Use it for counters and system touches.
  if tx.Statement.SkipHooks {
    return
  }

  // With an explicit Select, GORM writes only the selected columns.
  if len(tx.Statement.Selects) > 0 && !slices.Contains(tx.Statement.Selects, "*") {
    tx.Statement.Selects = append(tx.Statement.Selects, updatedBy)
  }

  if id, ok := From(tx.Statement.Context); ok {
    tx.Statement.SetColumn(updatedBy, id, true)
  } else {
    // A change made by no one is credited to no one, not to the last person.
    tx.Statement.SetColumn(updatedBy, nil, true)
  }
}
```

```go
// cmd/api/main.go, after db.SetDB / db.SetOwnerDB
if err := actor.RegisterGORM(database); err != nil {
  log.Fatal().Err(err).Msg("audit callbacks")
}
```

What this covers, and what it does not:

| Call | created_by | updated_by |
|---|---|---|
| `Create(&post)`, `Create(&posts)` | actor | untouched (NULL) |
| `Save(&post)` on an existing row | kept (omitted) | actor |
| `Model(&post).Update("title", v)` / `Updates(map or struct)` | kept | actor |
| `Model(&post).Select("title").Updates(...)` | kept | actor (added to the select) |
| Any of the above with no actor | caller's value / kept | NULL |
| `UpdateColumn` / `UpdateColumns` | kept | untouched |
| `Table("post").Updates(map)` (no model) | not filled | not filled |
| `Raw` / `Exec` | not filled | not filled |

`updated_by_user_id` stays NULL until the first edit, so "Last updated by"
appears only on rows someone has changed.

The last two rows are the same limit the SQLite tenant callbacks have
([tenancy.md](../tenancy.md#sqlite-scoping)): callbacks run on GORM's query
builder with a model, not on SQL you wrote. See [Raw SQL](#raw-sql).

---

## Jobs and System Actors

Work outside a request has no session. Two cases:

**Work done on behalf of a user** (an import the user uploaded, an invoice they
asked to send). Attribute it to them. Record the actor when enqueuing, the same
way the [trace ID](observability.md#correlation) is persisted, and restore it in
the worker before calling the handler:

```sql
ALTER TABLE job ADD COLUMN actor_user_id BIGINT REFERENCES "user"(id) ON DELETE SET NULL;
```

```go
// enqueue, inside the request's transaction
if id, ok := actor.From(ctx); ok {
  job.ActorUserID = &id
}

// worker, before running the handler (see jobs.md § Tenant Scoping in Workers)
if j.ActorUserID != nil {
  ctx = actor.With(ctx, *j.ActorUserID)
}
```

**Work nobody asked for** (scheduled cleanup, model-derived fields, sync from a
third party). No actor: `created_by_user_id` stays NULL and an update sets
`updated_by_user_id` to NULL. Do not create a "system" row in `user` to point
at. It needs an email, can show up in member lists and pickers, and one day
someone signs in as it. If you need to know *which* subsystem wrote a row,
record it in the data (a `source` column, as in
[data-modeling.md](data-modeling.md#superseding-not-overwriting)) or in an audit
log, not in a fake user.

Workers connect as the owner ([jobs.md](../jobs.md#tenant-scoping-in-workers)),
so register the callbacks on the owner handle too.

**Platform staff acting inside a tenant** (support fixing a record) are
credited as themselves, never as the user they are helping. If you build
impersonation, the actor stays the real staff member, and the impersonation is
an audit log entry.

---

## Tenancy and RLS

The audit columns do not change isolation, and isolation does not get in their
way:

- **`"user"` is not tenant-scoped**, so these are ordinary foreign keys to a
  global table. The tenant policy (`USING` / `WITH CHECK` on `tenant_id`) is
  untouched; writing `updated_by_user_id` cannot move a row between tenants.
- **Foreign-key checks and `ON DELETE SET NULL` bypass row security.**
  PostgreSQL runs referential actions without RLS, so deleting a user clears
  their references in every tenant's rows, even though no tenant-scoped
  connection could see those rows. That is the behaviour you want.
- **The database does not check that the actor belongs to the tenant.** It does
  not need to: the actor is the authenticated user that `TenantMiddleware`
  resolved the tenant for, and the binding tags keep request input out of these
  columns. Never accept an actor ID from a form, header, or job payload a client
  can influence.
- **Former members stay credited.** A user who leaves a tenant still created
  what they created. Show their name; that is history, not a leak.

---

## Raw SQL

`Raw` and `Exec` bypass the callbacks. A raw write to an audited table sets the
columns itself:

```go
id, _ := actor.From(ctx)
err := tx.WithContext(ctx).Exec(
  `UPDATE post SET title = ?, updated_by_user_id = NULLIF(?::bigint, 0) WHERE id = ?`,
  title, id, postID,
).Error
```

If raw writes are common, the alternative on PostgreSQL is a trigger, which
covers every write path. `WithTenant` then also sets the actor, next to the
tenant:

```go
// in db.WithTenant, after setting app.tenant_id
if id, ok := actor.From(ctx); ok {
  if err := tx.Exec(`SELECT set_config('app.user_id', ?::text, true)`, id).Error; err != nil {
    return err
  }
}
```

```sql
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION set_audit_users() RETURNS TRIGGER AS $$
DECLARE
  -- NULLIF: once a pooled connection has run set_config, the setting reads
  -- as '' rather than NULL after the transaction ends.
  actor BIGINT := NULLIF(current_setting('app.user_id', true), '')::BIGINT;
BEGIN
  IF TG_OP = 'INSERT' THEN
    NEW.created_by_user_id := COALESCE(actor, NEW.created_by_user_id);
  ELSE
    NEW.created_by_user_id := OLD.created_by_user_id;  -- set once
    NEW.updated_by_user_id := actor;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER set_post_audit_users BEFORE INSERT OR UPDATE ON post
FOR EACH ROW EXECUTE FUNCTION set_audit_users();
```

Pick one mechanism per project, callbacks or triggers, not both. Callbacks work
on SQLite and keep the logic in Go; triggers cover raw SQL and the owner
connection, at the cost of one extra statement per transaction and a trigger per
table. Note the trigger fires on `UpdateColumn` too.

---

## Showing Names

Load the names for the page in one query, not one per row
([scale.md § N+1](../scale.md#n1)):

```go
// internal/models/user.go
func UserNamesByIDs(ctx context.Context, tx *gorm.DB, ids []int64) (map[int64]string, error) {
  names := make(map[int64]string, len(ids))
  if len(ids) == 0 {
    return names, nil
  }
  var rows []struct {
    ID   int64
    Name string
  }
  if err := tx.WithContext(ctx).Model(&User{}).
    Select("id", "name").Where("id IN ?", ids).Scan(&rows).Error; err != nil {
    return nil, err
  }
  for _, r := range rows {
    names[r.ID] = r.Name
  }
  return names, nil
}
```

```go
// controller: collect the IDs on the page, then one lookup
var ids []int64
for _, p := range posts {
  for _, id := range []*int64{p.CreatedByUserID, p.UpdatedByUserID} {
    if id != nil {
      ids = append(ids, *id)
    }
  }
}
slices.Sort(ids)
names, err := models.UserNamesByIDs(ctx, tx, slices.Compact(ids))
```

```html
<p class="text-sm text-gray-500">
  {{ t .lang "audit.created" "date" (.Post.CreatedAt.Format "2006-01-02") }}
  {{ with .Post.CreatedBy $.Names }}{{ t $.lang "audit.by" "name" . }}{{ end }}
  {{ with .Post.UpdatedBy $.Names }}· {{ t $.lang "audit.updated_by" "name" . }}{{ end }}
</p>
```

A name map rather than a GORM `Preload` of the user: it fetches two columns
instead of whole user rows (emails included) into a model that may later be
serialized. A NULL or deleted user renders nothing.

---

## Testing

Test through the same scope the application uses ([testing.md](testing.md)),
with fixture users 1 and 2:

```go
func TestAudit_CreatedAndUpdatedBy(t *testing.T) {
  db.SetupTestData(t)

  db.TestTenantTx(t, 1, func(t *testing.T, tx *gorm.DB) {
    alice := actor.With(context.Background(), 1)
    bob := actor.With(context.Background(), 2)
    reload := func(id int64) models.Post {
      var p models.Post
      require.NoError(t, tx.First(&p, id).Error)
      return p
    }

    p := models.Post{TenantID: 1, UserID: 1, Title: "first"}
    require.NoError(t, tx.WithContext(alice).Create(&p).Error)
    got := reload(p.ID)
    require.NotNil(t, got.CreatedByUserID)
    assert.Equal(t, int64(1), *got.CreatedByUserID)
    assert.Nil(t, got.UpdatedByUserID, "not edited yet")

    require.NoError(t, tx.WithContext(bob).Model(&got).Update("title", "second").Error)
    got = reload(p.ID)
    assert.Equal(t, int64(1), *got.CreatedByUserID, "created_by never changes")
    assert.Equal(t, int64(2), *got.UpdatedByUserID)

    // A forged creator on Save is ignored.
    forged := int64(2)
    got.CreatedByUserID, got.Title = &forged, "third"
    require.NoError(t, tx.WithContext(bob).Save(&got).Error)
    assert.Equal(t, int64(1), *reload(p.ID).CreatedByUserID)

    // No actor (system work) clears updated_by rather than crediting Bob.
    require.NoError(t, tx.WithContext(context.Background()).
      Model(&models.Post{ID: p.ID}).Update("title", "fourth").Error)
    assert.Nil(t, reload(p.ID).UpdatedByUserID)
  })
}
```

Add, per audited resource:

- **One handler test through the real auth middleware** that creates and edits
  a row and asserts both columns. The callbacks are only as good as the
  middleware that puts the actor in the context; a route mounted outside it
  writes NULL, silently.
- **A user-deletion test** that deletes a fixture user and asserts their rows
  survive with the columns set to NULL, on PostgreSQL, as the runtime role.

---

## This is not an audit log

The columns answer "who made this row, and who touched it last". They keep no
history: each update overwrites the previous editor, deletes leave nothing
behind, and they say nothing about *what* changed. That is enough for a
"Created by Alice · Last updated by Bob" line, and for nothing else.

An **audit log** is a different thing: an append-only table with one row per
significant action, which answers "who did what, when, to what, and from what
value". Build one when a customer, an auditor, or your own support needs it:
role and permission changes, deletions, exports, billing changes, sign-ins,
impersonation. The minimum shape:

```sql
CREATE TABLE audit_log (
  id            BIGSERIAL PRIMARY KEY,
  tenant_id     BIGINT REFERENCES tenant(id),   -- NULL = platform-level action
  actor_user_id BIGINT REFERENCES "user"(id) ON DELETE SET NULL,
  action        TEXT NOT NULL,                  -- stable dotted name: 'member.role_changed'
  entity        TEXT NOT NULL,                  -- 'member', 'post'
  entity_id     BIGINT,
  changes       JSONB NOT NULL DEFAULT '{}'::jsonb,  -- {"role": ["viewer", "admin"]}: IDs and values, not content
  trace_id      TEXT,                           -- joins the entry to the request's logs
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_audit_log_tenant_created ON audit_log (tenant_id, created_at DESC);

-- Append-only for the application: it may insert and read, never rewrite.
REVOKE UPDATE, DELETE ON audit_log FROM app_user;
```

The rules that make it trustworthy:

- **Written in the same transaction as the change**, like a job enqueue, so the
  action and its record commit or roll back together.
- **Tenant-scoped with the usual RLS policy** ([tenancy.md](../tenancy.md#policy)),
  so tenants read their own trail; platform-level rows (`tenant_id` NULL) are
  visible only through the owner connection.
- **Not your application logs.** Logs are sampled, rotated, shipped to a
  third party, and readable by all of engineering
  ([observability.md](observability.md#never-log)); an audit trail people rely
  on lives in the database, where access is governed. The two share `trace_id`.
- **Likely to become the dominant table** in an audit-heavy product: plan its
  retention and partitioning early ([scale.md](../scale.md#the-dominant-table)).

If what you need is the history of a *value* as part of the product (prices,
facts, corrections a user can see), model it in the data instead:
[superseding rows](data-modeling.md#superseding-not-overwriting), not an audit
log.

Many products want both: the columns for the everyday "who touched this", the
log for the questions asked after something went wrong.

---

## Checklist

- [ ] `created_by_user_id` / `updated_by_user_id`: `BIGINT`, nullable,
      `REFERENCES "user"(id) ON DELETE SET NULL`, in a goose migration
- [ ] Model embeds `models.Audited`; fields are `*int64` with `json:"-" form:"-"`
- [ ] Auth middleware puts the user in the request context (`actor.With`)
- [ ] `actor.RegisterGORM` runs at startup on every handle, including the owner
- [ ] Every DB call passes the request context
- [ ] Jobs on behalf of a user carry `actor_user_id` and restore it; system jobs
      run with no actor; no "system" user row
- [ ] Raw writes to audited tables set the columns themselves (or the project
      uses the trigger instead of callbacks, not both)
- [ ] Names shown from one lookup per page, not per row
- [ ] Tests: create/update/forged-save/no-actor, one handler test per resource,
      user deletion leaves rows with NULL
- [ ] Anything that must answer "what changed, and when" goes to an audit log,
      not these columns

---

**Next:** [Database](database.md) | [Tenancy](../tenancy.md) | back to [claude.md](../../claude.md)
