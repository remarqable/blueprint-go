# Observability

> One question to answer: when a user says it is broken, can you find out what
> happened without guessing?

Three signals, in the order you should build them: **structured logs** with a
correlation ID, **metrics** on the few things users feel, **traces** when the
work spans processes and the first two stop being enough.

## Table of Contents

- [Correlation](#correlation)
- [Logging](#logging)
- [Metrics](#metrics)
- [Cardinality](#cardinality)
- [Cost as a Signal](#cost-as-a-signal)
- [Tracing](#tracing)
- [Health Checks](#health-checks)
- [Alerting](#alerting)
- [Never Log](#never-log)

---

## Correlation

Work that begins as an HTTP request continues in a job, which calls a model,
which writes rows that a gateway pushes to a client. **One identifier follows all
of it.** Without that, a user complaint is unanswerable — you have five systems
of logs with no way to line them up.

```go
// internal/platform/obs/ctx.go
package obs

type ctxKey struct{}

type Trace struct {
  RequestID string  // one per inbound request
  TraceID   string  // survives across processes; == RequestID at the origin
  TenantID  int64
  UserID    int64
}
```

Generated in middleware, echoed in the response header, **persisted on the job
row**, and restored by the worker:

```go
func RequestID() gin.HandlerFunc {
  return func(c *gin.Context) {
    id := c.GetHeader("X-Request-ID")
    if id == "" {
      id = ulid.Make().String()
    }
    c.Set("trace", obs.Trace{RequestID: id, TraceID: id})
    c.Header("X-Request-ID", id)
    c.Next()
  }
}
```

```sql
ALTER TABLE job ADD COLUMN trace_id TEXT;
ALTER TABLE ai_call ADD COLUMN trace_id TEXT;
```

That column is the single highest-value line in this document. It turns "a user
in tenant 40 says their message never got interpreted" from an afternoon into
one query.

Return the request ID in error responses and show it in the UI's error state.
Users paste it into support tickets and you skip the reconstruction entirely.

---

## Logging

Structured, JSON in production, one event per line. The blueprint uses `zerolog`
(see [claude.md § Logging](../claude.md#logging)).

```go
log := obs.From(ctx)   // carries trace_id, tenant_id, user_id automatically

log.Info().
  Str("event", "message.created").
  Int64("message_id", msg.ID).
  Int64("channel_id", msg.ChannelID).
  Dur("took", time.Since(start)).
  Msg("")
```

Conventions that make logs queryable rather than merely voluminous:

- **`event` is a stable dotted name**, not prose. `message.created`,
  `job.failed`, `ai.rejected`. You will group by it.
- **Message text is optional.** The fields are the data; `Msg("")` is fine.
- **One log line per unit of work**, at completion, carrying duration and
  outcome. Not one at the start and one at the end.
- **Log the decision, not the narration.** `Str("skipped_reason", "too_short")`
  answers a question. "Processing message..." answers none.
- **Errors log once, where they are handled.** An error logged at every level of
  the stack produces five lines for one failure and makes the error rate a lie.

Levels: `Error` is something a human must look at. `Warn` is degraded but
handled — a retry, a fallback, a rejected extraction. `Info` is the business
event stream. `Debug` is off in production.

---

## Metrics

Prometheus via `/metrics` (see [deployment.md](deployment.md)). Track the RED
signals per surface, then the few domain numbers that matter.

```go
var (
  HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
    Name: "http_requests_total",
  }, []string{"route", "method", "status"})

  HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
    Name:    "http_request_duration_seconds",
    Buckets: prometheus.DefBuckets,
  }, []string{"route", "method"})

  JobsPending = promauto.NewGaugeVec(prometheus.GaugeOpts{
    Name: "jobs_pending",
  }, []string{"queue"})

  JobsOldestPending = promauto.NewGaugeVec(prometheus.GaugeOpts{
    Name: "jobs_oldest_pending_seconds",
  }, []string{"queue"})

  AICost = promauto.NewCounterVec(prometheus.CounterOpts{
    Name: "ai_cost_micros_total",
  }, []string{"model", "purpose"})
)
```

The minimum set, by layer:

| Layer | Metrics |
|---|---|
| HTTP | requests by route/status, duration histogram, in-flight |
| Jobs (`jobs: true`) | pending, **oldest pending age**, duration by kind, dead total, reclaimed |
| Realtime (`realtime: true`) | active connections, evictions, fanout latency, gap refetches |
| AI (`ai: true`) | calls by model/purpose, tokens, cost, rejections, latency |
| Database | pool in-use/idle/waiting, query duration, deadlocks |

**Oldest pending age beats queue depth** for anything queue-shaped. Depth spikes
harmlessly during bursts; age only rises when you are genuinely not keeping up.

---

## Cardinality

**Never put a tenant ID, user ID, message ID, or URL path parameter in a
Prometheus label.** Each distinct value creates a time series that lives in
memory forever. A `tenant_id` label on three metrics across a thousand tenants
is how a monitoring system takes down the thing it monitors.

```go
// Wrong — unbounded series
HTTPRequests.WithLabelValues("/channels/8412/messages", "GET", "200").Inc()

// Right — the route template
HTTPRequests.WithLabelValues("/channels/:id/messages", "GET", "200").Inc()
```

Per-tenant numbers belong in the database, where they are rows and can be
aggregated on demand:

```sql
SELECT tenant_id, SUM(cost_micros)/1e6 AS dollars
  FROM ai_call
 WHERE created_at >= NOW() - INTERVAL '30 days'
 GROUP BY tenant_id ORDER BY 2 DESC LIMIT 20;
```

Rule of thumb: a label is safe if you can name every value it will ever take.

---

## Cost as a Signal

> `ai: true` only. Skip this section if the project does not call a model.

For anything model-backed, cost is an operational signal with the same standing
as latency, and it degrades in ways latency does not — silently, and with a
month's delay before the invoice tells you.

Track it two ways, deliberately:

- **Metrics** for aggregate rate and alerting, labelled by `model` and `purpose`
  only.
- **The `ai_call` table** ([ai.md](ai.md#cost-accounting)) for per-tenant
  attribution, which is unbounded and therefore not a metric.

The number that decides whether the business works is **cost per active user per
month** against price. Put it on a dashboard before launch, not after the first
invoice.

---

## Tracing

Worth adding when a single user action spans processes and you have stopped being
able to say which hop was slow. With an API, a gateway, and workers, that
threshold arrives earlier than in a single-process app.

OpenTelemetry, span per hop, `trace_id` shared with the logging context so a
trace links to its logs:

```go
ctx, span := tracer.Start(ctx, "interpret_message",
  trace.WithAttributes(attribute.Int64("message_id", id)))
defer span.End()
```

Sample aggressively — 1–5% of successful requests, 100% of errors and of
anything past a latency threshold. Full-fidelity tracing costs more than it
returns at this size.

Until then, `trace_id` in structured logs answers most of the same questions for
a fraction of the effort. **Do not build tracing before you have the correlation
ID in the logs**; it is the same value at ten times the cost.

---

## Health Checks

Two endpoints, different jobs. Conflating them causes outages.

```go
// Liveness: is this process wedged? No dependencies.
r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

// Readiness: can it serve? Checks dependencies.
r.GET("/readyz", func(c *gin.Context) {
  ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
  defer cancel()

  sqlDB, err := db.Get().DB()
  if err == nil {
    err = sqlDB.PingContext(ctx)
  }
  if err != nil {
    c.JSON(503, gin.H{"status": "degraded", "db": err.Error()})
    return
  }
  c.JSON(200, gin.H{"status": "ok"})
})
```

**Liveness must not check the database.** A brief database blip then fails
liveness on every instance, the orchestrator restarts them all at once, and a
recoverable incident becomes a full outage with a cold cache.

Workers need a health check too — a process that stopped claiming jobs is often
still happily alive. Report last-claim time and fail readiness if nothing has
been claimed while the queue is non-empty.

---

## Alerting

Alert on **symptoms users experience**, not on causes. Cause-based alerts fire
constantly during normal operation and train people to ignore the channel.

Worth waking someone:

| Alert | Condition |
|---|---|
| Error rate | 5xx above 1% of requests for 5 min |
| Latency | p95 above the route's budget for 10 min |
| Queue falling behind | `jobs_oldest_pending_seconds` past the queue's budget |
| Dead jobs | Any increase in `jobs_dead_total` |
| Fanout broken | `ws_gap_refetch_total` rate up sharply |
| Cost | Daily spend above 2× the trailing 7-day mean |
| Disk | Above 80% |

Not worth waking someone: CPU, memory, connection counts, individual job
failures, a single slow query. Those are dashboard material — what you consult
after an alert, not what generates one.

Every alert needs a written response. An alert with no action is noise, and it
should be deleted rather than tolerated.

---

## Never Log

- Passwords, tokens, session IDs, API keys — including inside request bodies and
  error strings from third parties, which frequently echo the request back.
- Full message bodies or user content. Log IDs and lengths; the content is in
  the database, where access is governed.
- Model prompts and completions containing user content. Log the prompt hash and
  token counts. If you need a sample for debugging, gate it behind an explicit
  flag, sample at a low rate, and expire it.
- Email addresses in metric labels or anywhere unbounded.

A logging pipeline is usually the least access-controlled copy of your data,
shipped to a third party, retained by default, and searchable by everyone in
engineering. Treat every line as though it will be read by someone who should
not see the underlying record — because in a multi-tenant system, that is the
realistic failure.
