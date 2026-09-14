# Scale

> The handful of decisions that are cheap now and expensive once the table is
> large.

Most performance work is premature. These are the exceptions: shapes that must
be right early because changing them later means a migration against your
biggest table while it is serving traffic.

Applies when one table will hold hundreds of millions of rows, or when
`SELECT COUNT(*)` on it has already stopped being instant.

## Table of Contents

- [The Dominant Table](#the-dominant-table)
- [Cursor Pagination](#cursor-pagination)
- [Partitioning](#partitioning)
- [Retention](#retention)
- [Connection Pooling](#connection-pooling)
- [N+1](#n1)
- [Index Discipline](#index-discipline)
- [Counters](#counters)
- [Caching](#caching)
- [Read Replicas](#read-replicas)
- [What Not to Do Yet](#what-not-to-do-yet)

---

## The Dominant Table

Most applications have one table that eventually dwarfs everything else by two
orders of magnitude — messages, events, log lines, readings. Every other table
is measured in thousands; this one is measured in hundreds of millions.

Identify it on day one, because it is the only table where these patterns are
mandatory and the only one worth designing around. Applying them everywhere is
its own kind of premature optimisation; applying them nowhere means retrofitting
them under load.

For a chat product it is `message`. For a metrics product, `sample`. For an
audit-heavy product, `audit_log`.

---

## Cursor Pagination

`OFFSET` reads and discards every row it skips. At offset 100,000 the database
does 100,000 rows of work to return 50. It gets worse linearly, and it is wrong
as well as slow: rows inserted while a user pages cause items to shift between
pages, so some are shown twice and some never.

Page by the last key you saw:

```sql
-- First page
SELECT * FROM message
 WHERE channel_id = $1
 ORDER BY seq DESC
 LIMIT 50;

-- Next page: cursor is the last seq returned
SELECT * FROM message
 WHERE channel_id = $1 AND seq < $2
 ORDER BY seq DESC
 LIMIT 50;
```

Constant time at any depth, given `(channel_id, seq DESC)` as an index, and
stable under concurrent inserts.

The cursor must be on a **unique, monotonic** column. `created_at` alone is
neither: two rows can share a timestamp, and the boundary row is then either
skipped or repeated. Use the sequence, or `(created_at, id)` as a compound
cursor:

```sql
WHERE (created_at, id) < ($2, $3)   -- row comparison; needs a matching index
ORDER BY created_at DESC, id DESC
```

Encode cursors opaquely (base64 the tuple) so clients cannot construct them and
you can change the shape later.

**Do not return a total count with a paginated list.** `COUNT(*)` on a large
table is a full scan of the index. If the UI needs a sense of scale, use
`reltuples` from `pg_class` for an estimate, or ask for one more row than the
page size and report "more available".

---

## Partitioning

Partition the dominant table by time once it is large. Two benefits, one of which
is the real one:

- Queries with a time predicate touch fewer partitions.
- **Deleting old data becomes `DROP TABLE` instead of `DELETE`.** This is why
  you do it. A `DELETE` of 200 million rows produces 200 million dead tuples,
  hours of vacuum, table bloat, and replication lag. Dropping a partition is
  instant and reclaims the disk immediately.

```sql
CREATE TABLE message (
  id         BIGSERIAL,
  tenant_id  BIGINT NOT NULL,
  channel_id BIGINT NOT NULL,
  seq        BIGINT NOT NULL,
  body       TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (id, created_at)          -- partition key must be in the PK
) PARTITION BY RANGE (created_at);

CREATE TABLE message_2026_09 PARTITION OF message
  FOR VALUES FROM ('2026-09-01') TO ('2026-10-01');
```

Three consequences to accept up front:

- **The partition key joins every unique constraint.** `PRIMARY KEY (id)` is not
  possible; it becomes `(id, created_at)`. Same for every `UNIQUE`. This is the
  main reason to decide early — changing a primary key later is not a small
  migration.
- **Queries without the partition key scan every partition.** Fetching a message
  by id alone touches all of them. Carry the timestamp in the identifier the
  client holds, or accept the scan on a rare path.
- **Partitions must be created ahead of time.** A row with no matching partition
  is an insert error. Run a job that keeps three months ahead, and alert if it
  has not run.

Partition by month for chat-like volume, by day for metrics-like volume. Aim for
partitions in the low tens of gigabytes; too many small partitions slows
planning.

---

## Retention

Decide what you keep before you are storing it. Retention is a product decision
with a schema consequence, and adding it later means the first deletion is a
migration rather than a routine.

| Data | Typical | Mechanism |
|---|---|---|
| Messages | Forever, or per plan | Drop partitions |
| Derived facts | Rebuildable — keep while useful | Delete freely |
| Job rows | 7–30 days after completion | Drop partitions |
| `ai_call` | 12 months (billing evidence) | Drop partitions |
| Logs | 14–30 days | Log pipeline retention |
| Sessions | On expiry | TTL in Redis |

Partition anything with a retention policy, including `job` and `ai_call`. They
grow faster than people expect, and they are the tables where `DELETE` bloat
bites first because the churn is constant.

---

## Connection Pooling

Postgres allocates roughly 5–10 MB per connection and degrades past a few
hundred. A Go service with `MaxOpenConns: 25` across four API instances, two
gateways, and eight workers is already at 350.

Budget across all processes, not per process:

```go
// GORM does not expose pool settings directly -- reach the *sql.DB underneath.
sqlDB, err := database.DB()
if err != nil {
  return err
}
sqlDB.SetMaxOpenConns(20)                      // per process, budgeted below
sqlDB.SetMaxIdleConns(5)
sqlDB.SetConnMaxLifetime(30 * time.Minute)     // survives failover/DNS changes
sqlDB.SetConnMaxIdleTime(5 * time.Minute)
```

**This is the blueprint's canonical pool configuration.** Other docs show a
connection being opened; the numbers come from here, and they are a budget
across every process, not a per-process default to copy.

When the total exceeds ~200, add pgbouncer in **transaction** mode.

**This interacts with RLS, and getting it wrong leaks data across tenants.** In
transaction mode a connection is returned to the pool at the end of every
transaction, so session-scoped state does not survive. The
[`WithTenant`](tenancy.md#setting-the-tenant) helper is already correct for
this — it sets the tenant with `set_config(..., true)` inside the transaction
that uses it. Any code path that sets a session variable outside a transaction
is silently broken under pgbouncer.

Transaction mode also disables session-level features: `LISTEN/NOTIFY`,
`SET SESSION`, and cursors held across transactions. The realtime listener
therefore needs a **direct connection**, bypassing pgbouncer. Two URLs: one
pooled for request work, one direct for the listener.

---

## N+1

The most common real performance bug, and it is invisible in development where
every table has twelve rows.

```go
// One query, then one per row.
for _, m := range messages {
  m.Author, _ = models.FindUser(ctx, tx, m.UserID)
}

// One query, then one more.
ids := lo.Uniq(lo.Map(messages, func(m Message, _ int) int64 { return m.UserID }))
authors, _ := models.UsersByIDs(ctx, tx, ids)  // WHERE id = ANY($1)
for i := range messages {
  messages[i].Author = authors[messages[i].UserID]
}
```

Catch it mechanically rather than by review: count queries per request in
development and fail the test above a threshold.

```go
func TestChannelViewQueryCount(t *testing.T) {
  n := db.CountQueries(t, func() { getChannel(t, channelID) })
  assert.LessOrEqual(t, n, 5, "channel view should not scale with message count")
}
```

Seed the test with 50 messages. A test with three rows passes whether or not the
bug exists.

---

## Index Discipline

- **`tenant_id` leads every composite index** on a tenant-scoped table. It is in
  the predicate of every query, RLS or not.
- **Index for the query, not the column.** `(channel_id, seq DESC)` serves the
  paginated read; separate indexes on each column do not.
- **Partial indexes for skewed predicates.** `WHERE status = 'pending'` on a
  table that is 99% done keeps the index small enough to stay cached.
- **Covering indexes for hot reads.** `INCLUDE (body)` avoids the heap fetch.
  Costs write throughput and disk; measure before adding.
- **Every index costs every write.** Six indexes means six B-tree updates per
  insert. On the dominant table this is the difference between a fast write path
  and a slow one.

Find the unused ones — they are pure cost:

```sql
SELECT relname, indexrelname, idx_scan, pg_size_pretty(pg_relation_size(indexrelid))
  FROM pg_stat_user_indexes
 WHERE idx_scan < 50
 ORDER BY pg_relation_size(indexrelid) DESC;
```

And find the expensive queries, which is nearly always a different list from the
one you would guess:

```sql
SELECT calls, round(mean_exec_time::numeric, 2) AS avg_ms,
       round(total_exec_time::numeric/1000, 1) AS total_s, query
  FROM pg_stat_statements
 ORDER BY total_exec_time DESC LIMIT 20;
```

Order by **total** time, not mean. A 3 ms query run 40,000 times a minute costs
more than a 900 ms report run hourly.

---

## Counters

`SELECT COUNT(*) FROM message WHERE channel_id = $1` is a scan. Rendered in a
sidebar for 50 channels, it is 50 scans per page load.

Keep a counter row, updated in the same transaction as the insert:

```sql
INSERT INTO channel_stat (channel_id, message_count) VALUES ($1, 1)
ON CONFLICT (channel_id) DO UPDATE SET message_count = channel_stat.message_count + 1;
```

This serialises writes per channel, which is correct for chat and wrong for a
global counter every write touches — there, accumulate per-shard rows and sum,
or accept an approximation refreshed on a schedule.

Unread counts specifically: store a per-user **cursor** (last seen `seq`) and
derive the count as `channel.max_seq - cursor`. No per-user-per-message state,
one row per user per channel, and it is exact.

---

## Caching

In order of value:

1. **Do less work.** A removed N+1 beats any cache.
2. **Postgres' own cache.** Right indexes and a `shared_buffers` sized for the
   working set. Most "we need Redis" problems are a missing index.
3. **Process-local cache** for small, hot, slow-changing data — feature flags,
   plan limits, tenant config. A `sync.Map` with a TTL. No network hop, and
   staleness is bounded by the TTL.
4. **Redis** for state shared across processes: sessions, rate limits, presence,
   expensive derived results.

Rules for any shared cache: **the key includes `tenant_id`** (a cross-tenant
cache hit is a data breach), the TTL is short enough that stale data is
tolerable, and the application is correct with the cache empty — because it will
be, after every restart.

Prefer expiry to invalidation. Explicit invalidation is correct roughly until
the third writer, and the bugs it produces are intermittent and unreproducible.

---

## Read Replicas

Later than you think. One Postgres instance on modern hardware handles a great
deal, and replication adds a correctness problem: **replica lag means
read-your-own-writes breaks**. A user posts a message, the next request reads a
replica 200 ms behind, and the message is not there.

When you do add one, route deliberately: analytics, reports, exports, and search
backfills to the replica; anything a user might have just written to the primary.
Never route by "is this a SELECT".

Before a replica, exhaust: indexes, N+1, caching, and moving heavy read work into
jobs that write summary tables.

---

## What Not to Do Yet

Each of these solves a problem you probably do not have, and creates several you
definitely will:

- **Sharding.** Partition first, replicate second, shard only when one machine
  genuinely cannot hold the data.
- **Microservices.** Separate processes for API, gateway, and worker is about
  resource profiles, not service boundaries. They share a database and a
  codebase on purpose.
- **A separate search cluster.** Postgres FTS plus `pgvector` covers a great deal.
- **A message broker.** The [jobs](jobs.md) table is enough well past the point
  where the product's other problems dominate.
- **Multi-region.** Doubles every operational problem and introduces consistency
  as a product concern.

The honest signal to act on is a measurement: a specific query in
`pg_stat_statements`, a p95 past its budget, a queue whose oldest job keeps
ageing. Not a projection, and not an architecture diagram.
