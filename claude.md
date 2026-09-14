# claude.md — Go + Gin SaaS Blueprint

> Production Go services: MVC, multi-tenancy, background jobs, realtime, AI,
> and deployment. Optimized for fast iteration, clean code, and AI agent
> execution.

---

## Read this first: scoped reading

This blueprint describes **one architecture with optional layers** — never a
menu of alternatives to blend. Reading a layer that does not apply produces code
that does not compile, or worse, compiles and is wrong.

1. Establish the [Project Configuration](#project-configuration) below.
2. Read **all of the core patterns** — they apply to every project.
3. Read a **layer doc only when its condition is met.**

**Never read both branches of the same decision in one session.** If
`tenancy: personal`, do not open the multi-tenancy section at all — not for
reference, not for context.

---

## Project Configuration

> **AI agents:** ask the questions below before creating any files, then record
> the answers here. This block is authoritative for everything that follows.

```yaml
tenancy:   shared     # shared | personal
database:  postgres   # postgres | sqlite
realtime:  false      # true | false
jobs:      false      # true | false
ai:        false      # true | false
frontend:  server     # server (Gin + HTMX) | api (JSON only)
deploy:    binary     # binary (systemd) | docker
```

### The questions to ask

**1. Will data ever be shared between users?**

- **Yes → `tenancy: shared`** (default). Users belong to tenants; business
  tables carry `tenant_id` and are protected by RLS. Every B2B app, and every
  B2C app that might one day add teams or sharing.
- **No → `tenancy: personal`.** Data belongs to one user and never to a group.

Ask it this way rather than "B2C or B2B?" — that asks about the go-to-market
label, not the data model. Retrofitting `tenant_id` means backfilling a tenant
per user and rewriting every query and policy. Carrying it from day one costs
one indexed column. **When unsure, choose `shared`.**

**2. SQLite or PostgreSQL?** → `database`

SQLite needs no server and is right for development, single-tenant installs, and
low-concurrency apps. PostgreSQL is required for anything with real concurrency,
and for **multi-tenant isolation**: SQLite has no Row-Level Security, so with
`tenancy: shared` the tenant predicate is added by application-layer callbacks
that raw SQL bypasses. Develop on either; **anything holding more than one
tenant's data runs on PostgreSQL.**

**3. Do clients need to see changes without asking?** → `realtime`
Chat, presence, live counters. Adds a second binary; see [realtime.md](patterns/realtime.md).

**4. Is there work that outlives the request?** → `jobs`
Mail, webhooks, image processing, anything calling a third party. Adds a third
binary. If `ai: true`, this is **required** — inference never runs in a handler.

**5. Does the product call a language model?** → `ai`
Requires `jobs: true`. See [ai.md](patterns/ai.md).

**6. Server-rendered HTML or JSON API?** → `frontend`
`server` gives Gin templates + HTMX with no build step. `api` drops the view
layer entirely; skip [frontend.md](patterns/frontend.md) and [htmx.md](patterns/htmx.md).

---

## Quick Start

### Prerequisites
- Go 1.21+
- PostgreSQL 15+
- Make

### Step 1: Structure

```bash
go mod init github.com/yourorg/yourapp

mkdir -p cmd/api
mkdir -p internal/{platform,models,controllers,middleware}
mkdir -p views/{layouts,partials,users,settings} static/{css,js,img}
mkdir -p lang migrations config

# jobs: true
mkdir -p cmd/worker internal/jobs
# realtime: true
mkdir -p cmd/gateway internal/gateway
# ai: true
mkdir -p internal/platform/ai/prompts evals/cases
```

### Step 2: Dependencies

```bash
go get github.com/gin-gonic/gin gorm.io/gorm github.com/rs/zerolog
go get gorm.io/driver/postgres   # database: postgres
go get gorm.io/driver/sqlite     # database: sqlite
go install github.com/pressly/goose/v3/cmd/goose@latest
```

### Step 3: Database and roles

The application must **not** connect as the role that owns the tables. Table
owners bypass Row-Level Security, so an app connecting as the owner has no
tenant isolation at all, silently. See
[database.md § Multi-Tenancy](patterns/database.md#multi-tenancy).

```bash
docker run --name app-db -e POSTGRES_USER=app_owner -e POSTGRES_PASSWORD=app \
  -e POSTGRES_DB=app -p 5432:5432 -d postgres:15-alpine
```

```sql
CREATE ROLE app_user LOGIN PASSWORD 'app' NOBYPASSRLS;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO app_user;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO app_user;
ALTER DEFAULT PRIVILEGES FOR ROLE app_owner IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_user;
```

Two URLs from here on: migrations use `app_owner`, the application uses
`app_user`.

### Step 4: Environment

```bash
cat > config/local.env <<'EOF'
APP_ENV=dev
PORT=8000

# database: postgres
DATABASE_URL=postgres://app_user:app@localhost:5432/app?sslmode=disable
DATABASE_OWNER_URL=postgres://app_owner:app@localhost:5432/app?sslmode=disable

# database: sqlite -- one URL, no roles
# DATABASE_URL=file:data/app.db?_foreign_keys=on&_journal_mode=WAL
EOF
```

### Step 5: Platform layer

| File | Source |
|------|--------|
| `internal/platform/config/config.go` | [§ Configuration](#configuration) |
| `internal/platform/db/db.go` | [database.md](patterns/database.md#connection-management) |
| `internal/platform/db/tenant.go` | [database.md](patterns/database.md#setting-the-tenant) — `tenancy: shared` |
| `internal/platform/logger/logger.go` | [§ Logging](#logging) |
| `internal/platform/errors/errors.go` | [§ Error Handling](#error-handling) |
| `internal/platform/obs/ctx.go` | [observability.md](patterns/observability.md#correlation) |
| `internal/platform/i18n/i18n.go` | [i18n.md](patterns/i18n.md) |
| `internal/platform/auth/session.go` | [auth.md](patterns/auth.md) |
| `internal/platform/jobs/` | [jobs.md](patterns/jobs.md) — `jobs: true` |
| `internal/platform/ai/` | [ai.md](patterns/ai.md) — `ai: true` |

### Step 6: First model, controller, view

- `internal/models/user.go` → [mvc.md](patterns/mvc.md#models-fat-models)
- `internal/controllers/router.go` → [mvc.md](patterns/mvc.md#controllers-thin-controllers)
- `views/layouts/base.html` → [mvc.md](patterns/mvc.md#views-templates)
- `cmd/api/main.go` → [§ Application Startup](#application-startup)

### Step 7: Run

```bash
make migrate && make run
```

Verify: `curl localhost:8000/healthz` returns `{"status":"ok"}`.

---

## Philosophy

- **Keep it simple, explicit, and local.** No magic, no over-engineering.
- **MVC**: fat models, thin controllers, dumb templates.
- **Server-rendered HTML + HTMX.** No SPA, no build pipeline.
- **Safe by default.** Tenant isolation is enforced in one place, by the
  database. Security that depends on remembering is not security.
- **Slow work is a job.** Nothing that calls a third party blocks a request.
- **The database is the truth; sockets are hints.**
- **i18n from day 1.**
- **Zero yak-shaving dev loop.** `make run` boots a working app.

### Fat models, thin controllers

**Models**: business logic, validation, queries, domain rules.
**Controllers**: parse input → call model → render or return JSON.
**Views**: `html/template`, minimal logic, HTMX attributes.

---

## Process Shape

A service in this blueprint is **one codebase, one database, and up to three
binaries**. They are separate processes because their resource profiles differ,
not because they are separate services.

```
cmd/
├── api/       # HTTP. Stateless, scales on request volume.
├── gateway/   # realtime: true — long-lived connections. Memory and fd bound.
└── worker/    # jobs: true — background work. Scales on queue depth.
```

| | Connects as | Scales on | Restart cost |
|---|---|---|---|
| `api` | `app_user` (RLS enforced) | Requests/sec | Brief 502s |
| `gateway` | `app_user` | Concurrent connections | Every client reconnects |
| `worker` | `app_owner` (sees all tenants; re-enters scope per job) | Queue depth | In-flight jobs drain |

Rules:

- **Only `api` accepts writes.** The gateway pushes notifications outward and
  never mutates domain data; a write path over a socket means duplicating
  authorization and validation with different failure semantics.
- **Only `worker` connects as the owner**, and it re-enters the tenant scope for
  every tenant-scoped job. See [jobs.md](patterns/jobs.md#tenant-scoping-in-workers).
- **They share `internal/`.** Same models, same validation, one schema. This is
  not a microservice boundary and should not become one.

With `jobs: false` and `realtime: false` there is one binary and none of this
applies.

---

## Tech Stack

| Layer | Technology | Rationale |
|-------|-----------|-----------|
| Backend | Go 1.21+ | Predictable performance, single binary, good at long-lived connections |
| Web framework | Gin | Fast, minimal, good DX |
| Database | PostgreSQL 15+, or SQLite | JSONB, FTS, RLS, `pgvector`, `SKIP LOCKED` on Postgres; SQLite for dev and single-tenant |
| ORM | GORM | Type-safe, one API across both engines |
| Templates | html/template | Auto-escaping, embedded |
| Frontend | Bootstrap 5 + HTMX | No build step, progressive enhancement |
| i18n | JSON catalogs | Simple, runtime-loaded |
| Logging | zerolog | Structured, fast |
| Migrations | goose | Simple, SQL-based, runs at deploy |
| Metrics | Prometheus | `/metrics`, standard tooling |

Postgres carries the queue, the vector index, the full-text index, and pub/sub.
Each of those has a dedicated alternative; none earns its operational cost until
measurements say so. See [scale.md § What Not to Do Yet](patterns/scale.md#what-not-to-do-yet).

---

## Conventions

### Go
- **Pointers for models**: `func (u *User) ...`
- **Always pass context** to DB calls (`tx.WithContext(ctx)`), with a timeout
- **No global singletons** outside `platform/db`
- **Tenant-scoped queries go through `db.WithTenant`**, never the shared
  `db.Get()` handle. Under RLS the shared handle fails closed and returns zero
  rows, so the symptom is an empty list rather than a leak — still a bug.
  `db.Unscoped()` is the owner connection and is platform-only.
  See [database.md](patterns/database.md#enforcing-the-boundary).
- **Migrations are SQL in goose, not `AutoMigrate`.** AutoMigrate cannot express
  RLS policies, partial indexes, or partitions, and versions nothing.
- **Files ~300 lines**: split by concern (`user.go`, `user_validation.go`)

### HTTP
- Controllers parse input, call models, render
- `gin.Recovery()` and request logging always on
- Request ID middleware first in the chain

### Templates
- `html/template` with auto-escaping; no raw HTML unless trusted
- Partials prefixed `_`
- All user-visible strings in i18n catalogs

### Frontend
- Bootstrap 5; do not invent custom classes
- HTMX for interactivity; one `app.css` for brand overrides
- No npm, webpack, or vite

---

## Configuration

```go
// internal/platform/config/config.go
package config

type Config struct {
  AppEnv           string
  Port             string
  Driver           string // "postgres" | "sqlite"
  DatabaseURL      string // postgres: app_user — RLS enforced
  DatabaseOwnerURL string // postgres: app_owner — migrations and workers only
}

func Load() (*Config, error) {
  cfg := &Config{
    AppEnv:           getEnv("APP_ENV", "dev"),
    Port:             getEnv("PORT", "8000"),
    Driver:           getEnv("DB_DRIVER", "postgres"),
    DatabaseURL:      os.Getenv("DATABASE_URL"),
    DatabaseOwnerURL: os.Getenv("DATABASE_OWNER_URL"),
  }
  if cfg.DatabaseURL == "" {
    return nil, fmt.Errorf("DATABASE_URL is required")
  }
  return cfg, nil
}

func getEnv(key, fallback string) string {
  if v := os.Getenv(key); v != "" {
    return v
  }
  return fallback
}
```

---

## Logging

```go
// internal/platform/logger/logger.go
package logger

var logger zerolog.Logger

func Init(env string) {
  zerolog.TimeFieldFormat = time.RFC3339
  if env == "dev" {
    logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}).
      With().Timestamp().Caller().Logger()
  } else {
    logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
  }
  log.Logger = logger
}

func Get() *zerolog.Logger { return &logger }
```

Bind the trace context rather than reading fields ad hoc — see
[observability.md](patterns/observability.md#logging).

---

## Error Handling

```go
// internal/platform/errors/errors.go
package errors

const (
  CodeUnknown      = "E_UNKNOWN"
  CodeNotFound     = "E_NOT_FOUND"
  CodeInvalidInput = "E_INVALID_INPUT"
  CodeUnauthorized = "E_UNAUTHORIZED"
  CodeForbidden    = "E_FORBIDDEN"
  CodeConflict     = "E_CONFLICT"
)

type AppError struct {
  Code    string
  Message string
  Err     error
}

func (e *AppError) Error() string {
  if e.Err != nil {
    return fmt.Sprintf("%s: %s (%v)", e.Code, e.Message, e.Err)
  }
  return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error { return e.Err }

func New(code, message string) *AppError { return &AppError{Code: code, Message: message} }

func (e *AppError) HTTPStatus() int {
  switch e.Code {
  case CodeNotFound:
    return 404
  case CodeInvalidInput, CodeConflict:
    return 400
  case CodeUnauthorized:
    return 401
  case CodeForbidden:
    return 403
  default:
    return 500
  }
}
```

---

## Application Startup

```go
// cmd/api/main.go
func main() {
  logger.Init(os.Getenv("APP_ENV"))
  log := logger.Get()

  cfg, err := config.Load()
  if err != nil {
    log.Fatal().Err(err).Msg("config")
  }

  db.SetDriver(cfg.Driver)

  database, err := db.Connect(cfg.Driver, cfg.DatabaseURL)
  if err != nil {
    log.Fatal().Err(err).Msg("database")
  }
  db.SetDB(database)

  // tenancy: shared on postgres -- the owner handle bypasses RLS and is used
  // only by migrations, workers and platform tooling.
  if cfg.DatabaseOwnerURL != "" {
    owner, err := db.Connect(cfg.Driver, cfg.DatabaseOwnerURL)
    if err != nil {
      log.Fatal().Err(err).Msg("owner database")
    }
    db.SetOwnerDB(owner)
  }

  // Budget connections across ALL processes, not per process.
  // See patterns/scale.md#connection-pooling
  if sqlDB, err := database.DB(); err == nil {
    sqlDB.SetMaxOpenConns(20)
    sqlDB.SetMaxIdleConns(5)
    sqlDB.SetConnMaxLifetime(30 * time.Minute)
    defer sqlDB.Close()
  }

  // Migrations are NOT run here: N instances would race the same upgrade on
  // boot. They run once, at deploy, as app_owner. See patterns/deployment.md.

  i18n.Preload("en")
  srv := &http.Server{
    Addr:    "0.0.0.0:" + cfg.Port,
    Handler: controllers.SetupRouter(),
  }

  go func() {
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
      log.Fatal().Err(err).Msg("listen")
    }
  }()
  log.Info().Str("addr", srv.Addr).Msg("server listening")

  stop := make(chan os.Signal, 1)
  signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
  <-stop

  shutdown, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
  defer cancel2()
  _ = srv.Shutdown(shutdown)
}
```

---

## Pattern Index

### Core — read all of these

| Doc | Covers |
|-----|--------|
| [mvc.md](patterns/mvc.md) | Models, views, controllers in detail |
| [database.md](patterns/database.md) | Conventions, migrations, JSONB, FTS, indexes, **RLS** |
| [data-modeling.md](patterns/data-modeling.md) | Evidence vs derivation, temporal data, graph shapes |
| [auth.md](patterns/auth.md) | Magic links, OAuth, sessions |
| [security.md](patterns/security.md) | CSRF, rate limiting, checklist |
| [testing.md](patterns/testing.md) | Unit, integration, isolation tests |
| [observability.md](patterns/observability.md) | Correlation IDs, logging, metrics, alerting |
| [i18n.md](patterns/i18n.md) | Internationalization |
| [deployment.md](patterns/deployment.md) | Production deployment, graceful shutdown |

### Layers — read only when the condition holds

| Doc | Condition | Covers |
|-----|-----------|--------|
| [jobs.md](patterns/jobs.md) | `jobs: true` | Durable queue, retries, idempotency, workers |
| [realtime.md](patterns/realtime.md) | `realtime: true` | Connections, fanout, ordering, resume |
| [ai.md](patterns/ai.md) | `ai: true` | Providers, structured output, provenance, cost, evals |
| [frontend.md](patterns/frontend.md) | `frontend: server` | Bootstrap + HTMX, accessibility |
| [htmx.md](patterns/htmx.md) | `frontend: server` | Interactive patterns |
| [embed.md](patterns/embed.md) | `deploy: binary` | Single-binary asset embedding |
| [scale.md](patterns/scale.md) | The dominant table is large | Pagination, partitioning, pooling, caching |

---

## Non-negotiables

Each of these fails **silently** when ignored. That is why they are on this
list: nothing errors, nothing logs, and the damage is discovered late.

**Tenancy** (`tenancy: shared`)
- The application connects as a **non-owner, `NOBYPASSRLS`** role. A table owner
  bypasses RLS entirely — policies exist, `pg_policies` lists them, and every
  query sees every tenant.
- Every tenant-scoped table has `FORCE ROW LEVEL SECURITY`, not just `ENABLE`.
- Every policy has **both `USING` and `WITH CHECK`**. `USING` alone permits
  cross-tenant `INSERT`.
- Tenant is set with `set_config(..., true)` **inside a transaction**.
  `SET LOCAL` outside a transaction is a no-op with only a warning, and setting
  it on the shared handle applies to an arbitrary connection.
- Tenant-scoped queries go through `WithTenant`. The shared handle fails closed
  under RLS (zero rows, not a leak), and `db.Unscoped()` — the owner connection
  — is platform-only, checked in CI.
- **SQLite has no RLS.** With `database: sqlite` the tenant predicate comes from
  GORM callbacks, which `Raw` and `Exec` bypass. Isolation tests run on
  PostgreSQL regardless of what development uses.

**Database**
- Migrations run once at deploy, as the owner — never inside application
  startup, where N instances race the same upgrade. SQL in goose, never
  `AutoMigrate`, which cannot express policies, partial indexes, or partitions.
- `tenant_id` leads every composite index on a tenant-scoped table.
- Cursor pagination on the dominant table; `OFFSET` degrades linearly and skips
  rows under concurrent inserts.

**Jobs** (`jobs: true`)
- A job is enqueued **inside the transaction that produced its cause**. Enqueuing
  outside means the row commits and the job does not.
- Every handler is idempotent. Delivery is at-least-once, always.
- A handler takes its transaction from context and never opens its own. One that
  calls `db.Get()` has left the tenant scope and reads every tenant's data.

**Realtime** (`realtime: true`)
- The database is the source of truth; a dropped frame is a latency problem, not
  data loss.
- Every event carries a server-assigned per-channel sequence. Timestamps are not
  monotonic and do not let a client detect a gap.
- Per-connection send buffers are bounded, and a full buffer closes the
  connection. An unbounded buffer turns one stalled client into an OOM.

**AI** (`ai: true`)
- Inference runs in a job, never in a handler.
- Model output is validated against a schema before it is stored, and every
  citation is verified against the input actually supplied.
- Nothing derived is shown without provenance; corrections supersede rather than
  overwrite, and human corrections outrank automated ones.
- Every call is written to `ai_call` with tokens and cost, and per-tenant
  budgets are enforced.

**Frontend** (`frontend: server`)
- All user-visible strings in i18n catalogs.
- Every interactive component carries its own ARIA.

**Observability**
- One `trace_id` spans request → job → model call, and is a column on `job` and
  `ai_call`.
- No tenant, user, or entity ID in a Prometheus label — unbounded cardinality
  takes down the monitoring before it takes down the app.
- Never log credentials, user content, or model completions containing it.

---

## AI Agent Instructions

**Bootstrapping a new project**
1. Ask the [configuration questions](#the-questions-to-ask). Do not guess.
2. Record answers in [Project Configuration](#project-configuration).
3. Read the core patterns. Read layer docs **only** where the condition holds.
4. Execute Quick Start steps 1–7 in order.
5. Verify `make run` serves `/healthz`.

**Adding a feature**
Find the relevant pattern doc in the index and follow it. Do not invent a second
way to do something the blueprint already covers.

**Before committing**
```bash
go build ./...
go test ./... -race -cover
go vet ./...
gofmt -l .
```

---

## Makefile

```makefile
APP=yourapp

.PHONY: run build test fmt migrate worker gateway

run:
	export $$(cat config/local.env | xargs) && go run ./cmd/${APP:-api}

worker:
	export $$(cat config/local.env | xargs) && go run ./cmd/worker

gateway:
	export $$(cat config/local.env | xargs) && go run ./cmd/gateway

build:
	go build -o bin/ ./cmd/...

test:
	go test ./... -race -cover

fmt:
	go fmt ./... && go vet ./...

migrate:
	goose -dir migrations postgres "$${DATABASE_OWNER_URL}" up

migrate-status:
	goose -dir migrations postgres "$${DATABASE_OWNER_URL}" status
```

---

## License

Copyright (c) Your Organization.
