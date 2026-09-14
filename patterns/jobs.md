# Background Jobs

> Durable work that outlives the request that created it.

Anything slow, external, or expensive belongs here: sending mail, calling a
model, generating a thumbnail, fanning out a notification. If a user is waiting
on it and it can fail, it is a job.

## Table of Contents

- [Why Postgres](#why-postgres)
- [Schema](#schema)
- [Enqueue](#enqueue)
- [Claiming Work](#claiming-work)
- [The Worker](#the-worker)
- [Handlers](#handlers)
- [Retries and Failure](#retries-and-failure)
- [Idempotency](#idempotency)
- [Tenant Scoping in Workers](#tenant-scoping-in-workers)
- [Scheduled and Delayed Jobs](#scheduled-and-delayed-jobs)
- [Queues and Priority](#queues-and-priority)
- [Graceful Shutdown](#graceful-shutdown)
- [Testing](#testing)
- [Operating It](#operating-it)
- [When to Graduate](#when-to-graduate)

---

## Why Postgres

Use the database you already have. A Postgres-backed queue gives you the one
property a separate broker cannot: **the job is enqueued in the same transaction
as the write that caused it.** Either the message row and its interpretation job
both exist, or neither does. With Redis or SQS you get a window where the row
committed and the enqueue failed, and you need an outbox table to close it — at
which point you have rebuilt this, plus a broker.

`FOR UPDATE SKIP LOCKED` handles low thousands of jobs per second on modest
hardware. That is far past the point where the rest of the product needs
attention. See [When to Graduate](#when-to-graduate).

**Rule: a job is enqueued inside the transaction that produced its cause, or it
is not enqueued.**

---

## Schema

```sql
CREATE TYPE job_status AS ENUM ('pending', 'running', 'done', 'failed', 'dead');

CREATE TABLE job (
  id              BIGSERIAL PRIMARY KEY,
  tenant_id       BIGINT REFERENCES tenant(id),  -- NULL = platform-level work
  queue           TEXT        NOT NULL DEFAULT 'default',
  kind            TEXT        NOT NULL,          -- handler name
  payload         JSONB       NOT NULL DEFAULT '{}'::jsonb,
  idempotency_key TEXT,

  status          job_status  NOT NULL DEFAULT 'pending',
  priority        SMALLINT    NOT NULL DEFAULT 0,  -- higher runs first
  attempts        SMALLINT    NOT NULL DEFAULT 0,
  max_attempts    SMALLINT    NOT NULL DEFAULT 5,

  run_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  leased_until    TIMESTAMPTZ,
  leased_by       TEXT,

  last_error      TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The claim query's index. Partial, so it stays small as done rows accumulate.
CREATE INDEX idx_job_claim ON job (queue, priority DESC, run_at)
  WHERE status = 'pending';

-- Reaper: find leases that expired.
CREATE INDEX idx_job_lease ON job (leased_until)
  WHERE status = 'running';

-- Idempotency. Partial so retired jobs do not block a legitimate re-enqueue.
CREATE UNIQUE INDEX uq_job_idem ON job (tenant_id, kind, idempotency_key)
  WHERE idempotency_key IS NOT NULL AND status IN ('pending', 'running', 'done');
```

`job` is deliberately **not** RLS-protected. Workers run as the owner role and
re-enter the tenant scope per job — see
[Tenant Scoping in Workers](#tenant-scoping-in-workers). Putting RLS on the queue
itself means a worker cannot see the jobs it is supposed to claim.

---

## Enqueue

```go
// internal/platform/jobs/enqueue.go
package jobs

import (
  "context"
  "encoding/json"
  "time"

  "gorm.io/gorm"
)

type Options struct {
  Queue          string
  Priority       int16
  MaxAttempts    int16
  RunAt          *time.Time
  IdempotencyKey string
}

// Enqueue writes a job inside the caller's transaction. tx is the *gorm.DB
// bound to an open transaction -- never the global handle from db.Get().
// A job that can commit independently of the work that caused it will
// eventually do exactly that.
func Enqueue(ctx context.Context, tx *gorm.DB, tenantID int64, kind string,
  payload any, opt Options) (int64, error) {

  body, err := json.Marshal(payload)
  if err != nil {
    return 0, err
  }
  if opt.Queue == "" {
    opt.Queue = "default"
  }
  if opt.MaxAttempts == 0 {
    opt.MaxAttempts = 5
  }
  runAt := time.Now()
  if opt.RunAt != nil {
    runAt = *opt.RunAt
  }

  var id int64
  res := tx.WithContext(ctx).Raw(`
    INSERT INTO job (tenant_id, queue, kind, payload, idempotency_key,
                     priority, max_attempts, run_at)
    VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?)
    ON CONFLICT DO NOTHING
    RETURNING id`,
    nullableTenant(tenantID), opt.Queue, kind, body, opt.IdempotencyKey,
    opt.Priority, opt.MaxAttempts, runAt).Scan(&id)

  if res.Error != nil {
    return 0, res.Error
  }
  if res.RowsAffected == 0 {
    return 0, nil // idempotency key already present; not an error
  }
  return id, nil
}

// nullableTenant keeps platform-level jobs (tenant_id NULL) expressible.
func nullableTenant(id int64) any {
  if id == 0 {
    return nil
  }
  return id
}
```

Call it alongside the write it belongs to:

```go
err := db.WithTenant(ctx, tenantID, func(tx *gorm.DB) error {
  msg, err := models.CreateMessage(ctx, tx, input)
  if err != nil {
    return err
  }
  _, err = jobs.Enqueue(ctx, tx, tenantID, "interpret_message",
    interpretPayload{MessageID: msg.ID},
    jobs.Options{Queue: "interpret", IdempotencyKey: fmt.Sprint(msg.ID)})
  return err
})
```

If the transaction rolls back, so does the job. That is the whole point.

---

## Claiming Work

One statement claims, leases, and returns the job. `SKIP LOCKED` lets N workers
poll the same table without blocking each other.

```sql
UPDATE job SET
  status       = 'running',
  attempts     = attempts + 1,
  leased_until = NOW() + $2::interval,
  leased_by    = $3,
  updated_at   = NOW()
WHERE id = (
  SELECT id FROM job
  WHERE status = 'pending'
    AND queue  = $1
    AND run_at <= NOW()
  ORDER BY priority DESC, run_at
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
RETURNING *;
```

The lease is what makes this survive a worker being killed. A crashed worker
leaves a `running` row whose `leased_until` passes; the reaper returns it to
`pending`:

```sql
UPDATE job
   SET status = 'pending', leased_until = NULL, leased_by = NULL
 WHERE status = 'running' AND leased_until < NOW();
```

Run the reaper on a ticker in every worker. It is idempotent and cheap.

**Set the lease longer than the slowest plausible run of the handler.** Too
short and a live worker's job is reclaimed and run concurrently by another,
which is how at-least-once quietly becomes at-least-twice under load.

---

## The Worker

```go
// cmd/worker/main.go — a separate binary from cmd/api
func main() {
  cfg := mustLoadConfig()
  pool := mustConnect(cfg.DatabaseOwnerURL) // owner role: sees all tenants

  w := jobs.NewWorker(pool, jobs.WorkerConfig{
    Queues:      []string{"default", "interpret"},
    Concurrency: 8,
    Lease:       5 * time.Minute,
    PollEvery:   time.Second,
    ID:          hostname() + "/" + shortID(),
  })

  ctx, stop := signal.NotifyContext(context.Background(),
    os.Interrupt, syscall.SIGTERM)
  defer stop()

  if err := w.Run(ctx); err != nil {
    log.Fatal().Err(err).Msg("worker exited")
  }
}
```

Polling every second is fine and costs one indexed query per queue. Reach for
`LISTEN/NOTIFY` to cut latency only once you have measured that the second
matters — and keep the poll as a backstop, because a `NOTIFY` delivered while no
worker is listening is simply lost.

---

## Handlers

Register at import time, keyed by `kind`:

```go
// internal/platform/jobs/registry.go
type Handler func(ctx context.Context, j *Job) error

var registry = map[string]Handler{}

func Register(kind string, h Handler) {
  if _, dup := registry[kind]; dup {
    panic("jobs: duplicate handler " + kind)
  }
  registry[kind] = h
}
```

```go
// internal/jobs/interpret.go
func init() {
  jobs.Register("interpret_message", interpretMessage)
}
```

An unknown `kind` is a **dead** job, not a retry. It means a deploy removed a
handler while jobs were still queued; retrying cannot fix it and will burn
attempts for hours.

**Deploy ordering:** add the handler, deploy, *then* start enqueuing that kind.
Remove in reverse: stop enqueuing, drain, then delete the handler.

---

## Retries and Failure

Exponential backoff with jitter. Without jitter, a downstream outage produces a
thundering herd the moment it recovers.

```go
func backoff(attempt int16) time.Duration {
  base := time.Duration(math.Pow(2, float64(attempt))) * time.Second // 2,4,8,16…
  if base > 10*time.Minute {
    base = 10 * time.Minute
  }
  jitter := time.Duration(rand.Int63n(int64(base / 2)))
  return base/2 + jitter
}
```

On handler error:

```sql
UPDATE job SET
  status     = CASE WHEN attempts >= max_attempts THEN 'dead' ELSE 'pending' END,
  run_at     = NOW() + $2::interval,
  last_error = $3,
  leased_until = NULL, leased_by = NULL, updated_at = NOW()
WHERE id = $1;
```

Distinguish two failure classes in handler code:

- **Retryable** — timeout, 429, 5xx, deadlock. Return the error; let backoff work.
- **Terminal** — malformed payload, deleted referent, 400, unknown handler.
  Return `jobs.Fatal(err)` and the worker marks it `dead` immediately rather than
  retrying five times against a certainty.

`dead` rows are never deleted automatically. They are your bug report. Alert on
the count.

---

## Idempotency

At-least-once delivery is the contract. A handler **will** run twice: leases
expire, workers are killed between the side effect and the status update,
networks partition. Design for it.

Three techniques, in order of preference:

1. **Natural idempotency.** Write with `ON CONFLICT DO NOTHING` against a unique
   key derived from the input. Re-running produces the same row.
2. **Guard on state.** `UPDATE ... WHERE status = 'pending'` and check rows
   affected; a second run affects zero rows and returns cleanly.
3. **Explicit ledger.** For side effects you cannot make idempotent (charging a
   card, sending mail), insert into a `side_effect(idempotency_key)` table inside
   the same transaction as the effect, and let the unique violation stop the
   second run.

**Never** make the effect and its record separate transactions. That is the same
bug as enqueueing outside the caller's transaction, one layer down.

---

## Tenant Scoping in Workers

Workers connect as the owner role, which bypasses RLS — otherwise they could not
see the queue. The tenant scope is re-entered per job:

```go
func (w *Worker) run(ctx context.Context, j *Job) error {
  h, ok := registry[j.Kind]
  if !ok {
    return Fatal(fmt.Errorf("no handler for kind %q", j.Kind))
  }

  // Platform-level job: no tenant, runs unscoped. Keep these rare and audited.
  if j.TenantID == nil {
    return h(ctx, j)
  }

  // Tenant job: same isolation guarantees as an HTTP request.
  return db.WithTenant(ctx, *j.TenantID, func(tx *gorm.DB) error {
    return h(WithTx(ctx, tx), j)
  })
}
```

**A handler never opens its own connection.** It takes the transaction from
context. A handler that reaches for `db.Unscoped()` is on the owner connection
and sees every tenant's data — that is the dangerous one, and it looks like
ordinary code. `db.Get()` is less bad but still wrong: under RLS it fails closed
and the handler silently processes nothing.

Lint for it: `db.Unscoped()` outside `internal/platform` and the few named
platform jobs is a build failure.

---

## Scheduled and Delayed Jobs

Delayed work is `RunAt` in the future — no extra machinery.

Recurring work is a job that enqueues its own successor as its last act, guarded
by an idempotency key on the period:

```go
func dailyDigest(ctx context.Context, j *Job) error {
  if err := sendDigest(ctx, j); err != nil {
    return err
  }
  tomorrow := time.Now().Add(24 * time.Hour).Truncate(24 * time.Hour)
  _, err := jobs.Enqueue(ctx, TxFrom(ctx), *j.TenantID, "daily_digest", nil,
    jobs.Options{
      RunAt:          &tomorrow,
      IdempotencyKey: "digest-" + tomorrow.Format("2006-01-02"),
    })
  return err
}
```

The idempotency key means a retried digest does not schedule two tomorrows. Do
not run cron in the container as well; two schedulers is how you send every
customer two emails.

---

## Queues and Priority

Separate queues by **latency expectation and failure blast radius**, not by
feature:

| Queue | Holds | Why separate |
|---|---|---|
| `default` | mail, webhooks, thumbnails | Baseline |
| `interpret` | model calls | Slow, expensive, rate-limited upstream |
| `fanout` | notification delivery | High volume, must stay responsive |

A queue exists so that one slow class cannot starve another. Ten thousand
queued model calls must not delay a password-reset email. Give each queue its
own worker pool with its own concurrency, and size `interpret` against your
provider's rate limit rather than your CPU count.

---

## Graceful Shutdown

On SIGTERM: stop claiming, let in-flight jobs finish, then exit.

```go
func (w *Worker) Run(ctx context.Context) error {
  var wg sync.WaitGroup
  sem := make(chan struct{}, w.cfg.Concurrency)

  for {
    select {
    case <-ctx.Done():
      wg.Wait()            // drain in-flight work
      return nil
    case sem <- struct{}{}:
    }

    j, err := w.claim(ctx)
    if err != nil || j == nil {
      <-sem
      w.sleep(ctx, w.cfg.PollEvery)
      continue
    }

    wg.Add(1)
    go func() {
      defer wg.Done()
      defer func() { <-sem }()
      w.execute(context.WithoutCancel(ctx), j)
    }()
  }
}
```

`context.WithoutCancel` for the execution context: the job should finish its
work and record its outcome rather than being torn off mid-write. Set the
deployment's termination grace period above your longest handler, or the
orchestrator will `SIGKILL` through the drain you just wrote.

---

## Testing

Handlers are ordinary functions — test them directly, with a transaction.

For controller tests, drain the queue synchronously rather than running a worker:

```go
// jobs.WorkOff runs every pending job once, in order, and fails the test on
// any handler error. Test helper only.
func TestPostingAMessageQueuesInterpretation(t *testing.T) {
  resp := postMessage(t, "ship it on friday")
  require.Equal(t, 200, resp.Code)

  require.Equal(t, 1, jobs.Pending(t, "interpret"))
  jobs.WorkOff(t)

  assert.Equal(t, 0, jobs.Pending(t, "interpret"))
}
```

Tests worth writing once, at the queue level, not per handler:

- A rolled-back transaction leaves no job.
- The same idempotency key enqueues once.
- A handler that panics marks the job failed, not `running` forever.
- An expired lease is reclaimed by the reaper.
- Exceeding `max_attempts` moves the job to `dead`, not back to `pending`.
- A handler running under tenant A cannot read tenant B's rows.

---

## Operating It

Export these from the worker (see [observability.md](observability.md)):

| Metric | Alert when |
|---|---|
| `jobs_pending{queue}` | Growing monotonically for 15 min — consumption is behind production |
| `jobs_oldest_pending_seconds{queue}` | Past the queue's latency budget |
| `jobs_dead_total{kind}` | Any increase |
| `jobs_duration_seconds{kind}` | p95 approaching the lease duration |
| `jobs_reclaimed_total` | Non-zero and rising — workers are dying mid-job |

Queue depth alone is a bad alarm; a burst is normal. **Oldest pending age** is
the honest signal.

---

## When to Graduate

Stay on Postgres until one of these is true:

- Sustained throughput above a few thousand jobs/sec, and the claim query shows
  up in `pg_stat_statements` as a top cost.
- You need fanout to many independent consumers of the same event, which is a
  log (Kafka, Redis Streams), not a work queue.
- Jobs must survive the database being down — rare, and usually a sign the work
  belongs to a different service.

Growth in *job count* is not a reason. Partition `job` by month and drop old
partitions; see [scale.md](scale.md#partitioning).
