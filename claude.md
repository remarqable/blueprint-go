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
2. Read **all of `patterns/core/`** — it applies to every project.
3. Read a **layer doc only when its condition is met.**

**Never read both branches of the same decision in one session.** If
`tenancy: personal`, do not open `patterns/tenancy.md` at all — not for
reference, not for context.

| Layer doc | Read only when |
|-----------|----------------|
| `patterns/tenancy.md` | `tenancy: shared` |
| `patterns/jobs.md` | `jobs: true` |
| `patterns/realtime.md` | `realtime: true` |
| `patterns/ai.md` | `ai: true` (requires `jobs: true`) |
| `patterns/frontend.md`, `patterns/htmx.md`, `patterns/mobile-navigation.md` | `frontend: server` |
| `patterns/embed.md` | `deploy: binary` |
| `patterns/scale.md` | the dominant table is large |

`skills/` is not a layer and has no condition. It holds procedures rather than
patterns, and [`skills/blueprint-go-audit`](skills/blueprint-go-audit/SKILL.md)
runs on every project — see [Verification](#verification-every-change-is-audited).

---

## Project Configuration

> **AI agents:** ask the questions below before creating any files. Record the
> answers in the **project's** root `claude.md` — the block below holds upstream
> defaults, and this file is a shared submodule, so editing it here changes every
> project that mounts the blueprint. The project's recorded answers are
> authoritative for everything that follows, and are what the
> [blueprint audit](#verification-every-change-is-audited) reviews against.

```yaml
tenancy:   shared     # shared | personal
database:  postgres   # postgres | sqlite
realtime:  false      # true | false
jobs:      false      # true | false
ai:        false      # true | false
frontend:  server     # server (Gin templates + HTMX + Tailwind/Alpine) | api (JSON only)
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
mkdir -p internal/assets/{views/{layouts,partials,pages},static/{css,js,img}}   # frontend: server
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

With `frontend: server`, the frontend assets come next. There is no npm: Tailwind
is a standalone binary the Makefile downloads, and htmx and Alpine are vendored
files.

```bash
make css      # downloads the Tailwind standalone binary, builds app.css
make assets   # vendors htmx, @alpinejs/csp, @alpinejs/focus
echo ".bin/" >> .gitignore
```

`internal/assets/input.css` holds the design tokens and the component layer —
see [frontend.md](patterns/frontend.md#design-tokens). Commit the built
`internal/assets/static/css/app.css`; `go build` embeds it.

### Step 3: Database and roles

The application must **not** connect as the role that owns the tables. Table
owners bypass Row-Level Security, so an app connecting as the owner has no
tenant isolation at all, silently. See
[tenancy.md](patterns/tenancy.md).

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
| `internal/platform/db/db.go` | [database.md](patterns/core/database.md#connection-management) |
| `internal/platform/db/tenant.go` | [tenancy.md](patterns/tenancy.md#setting-the-tenant) — `tenancy: shared` |
| `internal/platform/logger/logger.go` | [§ Logging](#logging) |
| `internal/platform/errors/errors.go` | [§ Error Handling](#error-handling) |
| `internal/platform/obs/ctx.go` | [observability.md](patterns/core/observability.md#correlation) |
| `internal/platform/i18n/i18n.go` | [i18n.md](patterns/core/i18n.md) |
| `internal/platform/auth/session.go` | [auth.md](patterns/core/auth.md) |
| `internal/platform/jobs/` | [jobs.md](patterns/jobs.md) — `jobs: true` |
| `internal/platform/ai/` | [ai.md](patterns/ai.md) — `ai: true` |

### Step 6: First model, controller, view

- `internal/models/user.go` → [mvc.md](patterns/core/mvc.md#models-fat-models)
- `internal/controllers/router.go` → [mvc.md](patterns/core/mvc.md#controllers-thin-controllers)
- `internal/assets/views/layouts/base.html` → [mvc.md](patterns/core/mvc.md#views-templates), [frontend.md](patterns/frontend.md)
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
- **Server-rendered HTML + HTMX, Alpine for behaviour.** No SPA, no npm. The
  only build step is `make css`, and its output is committed.
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
| Frontend | Tailwind CSS v4 + Alpine.js (CSP build) + HTMX | Standalone Tailwind binary, no npm; strict CSP; progressive enhancement |
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
  See [tenancy.md](patterns/tenancy.md#enforcing-the-boundary).
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
- Tailwind utilities in the markup, plus a thin component layer in
  `input.css` (buttons, inputs, cards); see [frontend.md](patterns/frontend.md)
- HTMX for server interaction, Alpine (CSP build) for behaviour; Alpine
  components live in `static/js/app.js`
- No npm, webpack, or vite; `make css` after touching a template

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
[observability.md](patterns/core/observability.md#logging).

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
  // boot. They run once, at deploy, as app_owner. See patterns/core/deployment.md.

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

### Core — read all of these (`patterns/core/`)

| Doc | Covers |
|-----|--------|
| [mvc.md](patterns/core/mvc.md) | Models, views, controllers in detail |
| [database.md](patterns/core/database.md) | Conventions, migrations, JSONB, FTS, indexes |
| [data-modeling.md](patterns/core/data-modeling.md) | Evidence vs derivation, temporal data, graph shapes |
| [auth.md](patterns/core/auth.md) | Magic links, OAuth, sessions |
| [security.md](patterns/core/security.md) | CSRF, rate limiting, checklist |
| [testing.md](patterns/core/testing.md) | Unit, integration, isolation tests |
| [observability.md](patterns/core/observability.md) | Correlation IDs, logging, metrics, alerting |
| [i18n.md](patterns/core/i18n.md) | Internationalization |
| [deployment.md](patterns/core/deployment.md) | Production deployment, graceful shutdown, the "be right back" page, request profiling (admin Info box) |
| [audit.md](patterns/core/audit.md) | Record audit: `created_by` / `updated_by` |

### Layers — read only when the condition holds

| Doc | Condition | Covers |
|-----|-----------|--------|
| [tenancy.md](patterns/tenancy.md) | `tenancy: shared` | Row-Level Security, roles, scoping, isolation tests |
| [jobs.md](patterns/jobs.md) | `jobs: true` | Durable queue, retries, idempotency, workers |
| [realtime.md](patterns/realtime.md) | `realtime: true` | Connections, fanout, ordering, resume |
| [ai.md](patterns/ai.md) | `ai: true` | Providers, structured output, provenance, cost, evals |
| [frontend.md](patterns/frontend.md) | `frontend: server` | Tailwind v4 + Alpine.js, component layer, visual rules, accessibility |
| [htmx.md](patterns/htmx.md) | `frontend: server` | Interactive patterns |
| [mobile-navigation.md](patterns/mobile-navigation.md) | `frontend: server` | Responsive navigation |
| [embed.md](patterns/embed.md) | `deploy: binary` | Single-binary asset embedding |
| [scale.md](patterns/scale.md) | The dominant table is large | Pagination, partitioning, pooling, caching |

### Skills — procedures to run, not patterns to read

| Skill | Purpose | When |
|-------|---------|------|
| [blueprint-go-audit](skills/blueprint-go-audit/SKILL.md) | Independent adversarial review of the changed files, with a scored conformance table | **Mandatory** after every implementation, before reporting the work complete |

See [skills/README.md](skills/README.md) for how to add one.

---

## Verification: every change is audited

**All code generated against this blueprint must conform to it, and conformance
is checked rather than assumed.** After implementing anything — a new project, a
feature, a bug fix, a refactor — and **before reporting the work complete**, run
[`skills/blueprint-go-audit`](skills/blueprint-go-audit/SKILL.md).

`gofmt`, `go vet` and `go test -race` run first and are not the same thing. Read
the framing of the non-negotiables below: **each fails silently.** A handler on
`db.Get()` instead of `db.WithTenant` compiles, vets clean, passes, and leaks in
production. The toolchain proves the code runs; the audit proves it conforms.

The audit is run by a separate reviewer agent that never sees the goal or the
plan, because the agent that drifted is the one being asked whether it drifted.
The skill holds the procedure and the reasoning; this section is only the
requirement.

Read `blueprint/skills/blueprint-go-audit/SKILL.md` and follow it — no install,
so the gate holds in a fresh clone. (`make skills` also exposes it as
`/blueprint-go-audit` for humans; the agent never needs it.)

| Result | What to do |
|--------|-----------|
| **FAIL** | Fix the Critical and High violations, re-run the audit. Do not commit, do not report the work complete. |
| **WARN** | Report complete **with the violations table**, naming the ones you left. |
| **PASS** | Report complete, include the conformance table. |

Print what the reviewer returned rather than your summary of it. A finding you
chose not to fix is a decision when the user can see it and a regression when you
quietly drop it.

The reviewer is told which layers are enabled and is forbidden to open the rest.
It takes that list from the **project's** root `claude.md`, not from the [Project
Configuration](#project-configuration) block above — this file is a shared
submodule, so that block holds upstream defaults. Record the real answers there
and the audit does not have to infer them.

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
- Every interactive component carries its own ARIA; Tailwind ships none.
- Run `make css` after touching any template and commit `app.css`; CI runs
  `make css-check`. An uncompiled class renders unstyled in production and
  nowhere else.
- Logical properties (`ms-`/`me-`/`ps-`/`pe-`/`text-start`), never
  `ml-`/`mr-`/`text-left`. Physical properties silently break RTL.
- Alpine's CSP build: every Alpine value is a name defined with `Alpine.data`.
  An inline expression silently does nothing.
- No inline `<script>`, `style=`, `on*=` or `hx-on`. The CSP blocks them in
  production only.
- Validation errors return 422 and are swapped; htmx 2 discards the body of
  every other 4xx/5xx, so those failures become a toast.

**Deployment**
- Migrations run before the restart and must work with the previous release
  still running: a rollback restores the binary, not the schema.
- `Server-Timing` and the request-profiling Info box go to platform admins only,
  never to tenant admins or users.
- `created_by_user_id` / `updated_by_user_id` are set from the authenticated
  actor only, never from request input. See
  [audit.md](patterns/core/audit.md).

**Verification**
- Every implementation ends with the
  [blueprint audit](#verification-every-change-is-audited), run by a separate
  agent, before the work is reported complete. A green build is the
  precondition, not the result — everything on this list compiles and vets
  clean.

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
2. Record the answers in the **project's own** root `claude.md`, not in this
   submodule — this file is shared, and editing it changes every project.
3. Read the core patterns. Read layer docs **only** where the condition holds.
4. Execute Quick Start steps 1–7 in order.
5. Verify `make run` serves `/healthz`.
6. Run the [blueprint audit](#verification-every-change-is-audited) over
   everything you generated — by reading its `SKILL.md`, which needs no install.
   Fix any FAIL before handing the project over.

**Adding a feature**
1. Find the relevant pattern doc in the index and follow it. Do not invent a
   second way to do something the blueprint already covers.
2. Implement.
3. Run the [blueprint audit](#verification-every-change-is-audited) over the
   files you changed, and act on the result, **before** reporting the work
   complete. This is not conditional on the size of the change.

**Before committing**
```bash
gofmt -l .
go vet ./...
go build ./...
go test ./... -race -cover
make css-check   # frontend: server — the committed app.css matches the templates
```
Then the [blueprint audit](#verification-every-change-is-audited). These commands
prove the code compiles and passes; they cannot prove it conforms, because every
non-negotiable above compiles and vets clean.

**Verifying the blueprint itself.** `examples/` is a compiling skeleton of the
platform code in these documents, with tests asserting what they claim —
tenant isolation in four directions, job durability, evidence validation. CI
builds it. If a pattern here stops compiling, that build fails.

---

## Makefile

```makefile
APP=yourapp

.PHONY: run build test fmt migrate migrate-status worker gateway skills css css-watch css-check assets

# frontend: server — see patterns/frontend.md § The Build Step
ASSETS           := internal/assets
TAILWIND_VERSION := v4.1.14
TAILWIND_BIN     := .bin/tailwindcss
HTMX_VERSION     := 2.0.4
ALPINE_VERSION   := 3.14.9

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

$(TAILWIND_BIN):
	@mkdir -p .bin
	@case "$$(uname -s)-$$(uname -m)" in \
	  Darwin-arm64)  F=tailwindcss-macos-arm64 ;; \
	  Darwin-x86_64) F=tailwindcss-macos-x64 ;; \
	  Linux-aarch64) F=tailwindcss-linux-arm64 ;; \
	  Linux-x86_64)  F=tailwindcss-linux-x64 ;; \
	  *) echo "Unsupported platform"; exit 1 ;; \
	esac; \
	curl -fsSL -o $(TAILWIND_BIN) \
	  https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/$$F
	@chmod +x $(TAILWIND_BIN)

css: $(TAILWIND_BIN)
	$(TAILWIND_BIN) -i $(ASSETS)/input.css -o $(ASSETS)/static/css/app.css --minify

css-watch: $(TAILWIND_BIN)
	$(TAILWIND_BIN) -i $(ASSETS)/input.css -o $(ASSETS)/static/css/app.css --watch

css-check: css
	git diff --exit-code $(ASSETS)/static/css/app.css

assets:
	@mkdir -p $(ASSETS)/static/js
	curl -fsSL -o $(ASSETS)/static/js/htmx.min.js \
	  https://unpkg.com/htmx.org@$(HTMX_VERSION)/dist/htmx.min.js
	curl -fsSL -o $(ASSETS)/static/js/alpine.min.js \
	  https://unpkg.com/@alpinejs/csp@$(ALPINE_VERSION)/dist/cdn.min.js
	curl -fsSL -o $(ASSETS)/static/js/alpine-focus.min.js \
	  https://unpkg.com/@alpinejs/focus@$(ALPINE_VERSION)/dist/cdn.min.js

skills:
	@set -e; \
	found=0; \
	for BP in . $$(git config -f .gitmodules --get-regexp '\.path$$' 2>/dev/null | awk '{print $$2}'); do \
	  [ -d "$$BP/skills" ] || continue; \
	  mkdir -p .claude/skills; \
	  for d in "$$BP"/skills/*/; do \
	    [ -f "$$d/SKILL.md" ] || continue; \
	    n=$$(basename "$$d"); \
	    rm -rf ".claude/skills/$$n"; \
	    ln -s "$$(cd "$$d" && pwd)" ".claude/skills/$$n"; \
	    echo "  linked /$$n"; \
	    found=1; \
	  done; \
	done; \
	if [ "$$found" = "0" ]; then \
	  echo "No blueprint skills found."; \
	  echo "The blueprint submodule is missing or not initialized. Run:"; \
	  echo "  git submodule update --init --recursive"; \
	  exit 1; \
	fi; \
	if [ -f .gitignore ] && ! grep -qxF '.claude/skills/' .gitignore; then \
	  echo '.claude/skills/' >> .gitignore; \
	fi; \
	echo "Skills linked. Restart Claude Code to pick them up."
```

---

## License

Copyright (c) Your Organization.
