---
name: blueprint-go-audit
description: Audit Go code against the Go + Gin SaaS blueprint. Launches an independent adversarial reviewer agent over the changed files, then reports violations and a scored conformance table across MVC, database, data modeling, auth, security, testing, observability, i18n, deployment, record audit, Go conventions, and the enabled layers (tenancy, jobs, realtime, AI, frontend, HTMX, mobile navigation, embed, scale). Runs gofmt, go vet, go test -race and the stylesheet build check first. Respects the blueprint's scoped-reading rule. This is the mandatory verification gate — run it after every implementation, before reporting the work complete.
---

# Blueprint Audit (Go)

> **Scope: any project built on `remarqable/blueprint-go`.** For a Flask project
> on `remarqable/blueprint-python`, use that blueprint's `/blueprint-audit`. The
> two have different configuration keys and different non-negotiables; neither
> review transfers.

Check changed code against the blueprint and report what violates it. The review
is run by a **separate agent** with an adversarial brief: its job is to find
violations, not to confirm the code is fine.

## This is a gate, not an option

The blueprint's agent instructions make this audit mandatory. Any agent that
generated or modified Go code under a blueprint project runs this skill before it
reports the work finished. A feature is not done when it compiles — it is done
when it compiles, passes, *and* conforms.

That last distinction carries more weight here than in most languages. `go build`
and `go vet` are genuinely good, which makes it tempting to treat a green build
as a passing review. But read the blueprint's own framing of its non-negotiables:
**each of them fails silently.** A handler that calls `db.Get()` instead of
`db.WithTenant` compiles, vets clean, passes its tests, and returns zero rows in
production under RLS — or every tenant's rows without it. The toolchain cannot
see any of that. This audit is what does.

So:

- **Do not skip it because the change is small.** The violations this catches —
  a handler on `db.Get()`, a policy without `WITH CHECK`, an unbuilt stylesheet —
  are overwhelmingly small changes. Size is not evidence of safety.
- **Do not skip it because the build is green.** Green is the precondition, not
  the result.
- **Do not skip it because you are confident.** Confidence is the symptom being
  controlled for. See below.
- **Do not report the audit's result as your own judgment.** Print what the
  reviewer returned.

## Why a separate agent

The agent that wrote the code knows why every shortcut was taken and will
rationalize each one. A reviewer with no memory of the implementation, and no
access to the goal or plan, cannot. That independence is the mechanism — do not
skip it, and do not do the review yourself in the implementing context.

## The scoped-reading rule

This blueprint is **not** a menu of alternatives to blend. It describes one
architecture with optional layers, and reading a layer that does not apply
produces code that does not compile — or worse, compiles and is wrong. The
blueprint states this as its first rule, and the reviewer must obey it too:

- `patterns/core/` applies to every project. Always in scope: `mvc`, `database`,
  `data-modeling`, `auth`, `security`, `testing`, `observability`, `i18n`,
  `deployment`, `audit`, `performance`.
- A layer doc in `patterns/` is in scope **only** if its condition holds:

  | Doc | Condition |
  |-----|-----------|
  | `tenancy.md` | `tenancy: shared` |
  | `jobs.md` | `jobs: true` |
  | `realtime.md` | `realtime: true` |
  | `ai.md` | `ai: true` (which requires `jobs: true`) |
  | `frontend.md`, `htmx.md`, `mobile-navigation.md` | `frontend: server` — all three are out of scope on `frontend: api` |
  | `embed.md` | `deploy: binary` |
  | `staging.md` | `staging: true` |
  | `admin-api.md` | `admin_api: true` |
  | `scale.md` | the dominant table is large, and the change touches its query path |

- Two decisions select a section *within* a doc rather than a layer doc:
  `deploy` picks one shape inside `patterns/core/deployment.md` (the systemd
  service on `binary`, the Docker build on `docker`), and `database` picks the
  PostgreSQL or SQLite branch wherever a doc carries both. Review against the
  chosen branch only.

A reviewer that reads a disabled layer will report violations for patterns the
project deliberately does not implement. That is worse than no review, because
the findings look authoritative.

## When to Use

- **After every implementation, before reporting the work complete** — mandatory
- Before committing any change to a blueprint project
- To review a contributor's pull request against the blueprint
- Any time you want a second opinion independent of the implementation context

## Workflow

### Step 1: Identify Changes

```bash
# Uncommitted changes (default)
git diff HEAD --name-only

# Nothing uncommitted? Review the last commit
git diff HEAD~1 --name-only

# Reviewing a PR branch
git diff main...HEAD --name-only
```

If nothing has changed, say so and stop.

When this skill runs as the post-implementation gate, the changed set is the work
you just did. Prefer the explicit file list you know you touched over a `git
diff` that may also sweep in unrelated working-tree edits.

| File pattern | Brings in |
|---|---|
| `internal/models/*.go` | MVC, database, data-modeling, record audit, performance, tenancy |
| `internal/controllers/*.go` | MVC, auth, security, performance |
| `internal/middleware/*.go` | Security, auth, observability |
| `internal/platform/db/**` | Database, tenancy |
| `internal/platform/obs/**` | Observability |
| `internal/jobs/**`, `cmd/worker/**` | Jobs layer, performance (jobs that loop over rows) |
| `internal/gateway/**`, `cmd/gateway/**` | Realtime layer |
| `internal/platform/ai/**`, `evals/**` | AI layer |
| `views/**/*.html` | Frontend, HTMX, mobile navigation, i18n, CSRF, performance (lookups in `range` loops, static URLs via `asset`) — and check `app.css` was rebuilt |
| `static/css/**` | Frontend — and check `app.css` was rebuilt |
| `internal/assets/**` | Embed layer, plus whatever the embedded views and styles bring in |
| `migrations/*.sql` | Database, record audit, tenancy |
| `cmd/*/main.go` | Deployment, observability |
| `*_test.go` | Testing |
| `lang/*.json` | i18n |

### Step 2: Resolve the Blueprint and Establish the Active Layers

The blueprint is a git submodule. Its path is per-project — find it rather than
assuming:

```bash
git config -f .gitmodules --get-regexp '\.path$'
```

It is `blueprint/` or `blueprint-go/` in a single-language project and
`blueprint/go/` where a repo carries more than one language blueprint.
Everything below is relative to that path:

- Master doc: `<blueprint>/claude.md` (lowercase)
- Always-applicable patterns: `<blueprint>/patterns/core/` — `audit`, `auth`,
  `data-modeling`, `database`, `deployment`, `i18n`, `mvc`, `observability`,
  `performance`, `security`, `testing`
- Layer docs: `<blueprint>/patterns/` — `ai.md`, `embed.md`, `frontend.md`,
  `admin-api.md`, `htmx.md`, `jobs.md`, `mobile-navigation.md`, `realtime.md`, `scale.md`,
  `staging.md`, `tenancy.md`
- Compiling reference: `<blueprint>/examples/` — the platform code the patterns
  are extracted from, with tests asserting what they claim

If the directory is empty, the submodule is not initialized. Stop and tell the
user to run `git submodule update --init --recursive` — do not review against
patterns you could not read.

**Determining which layers are active.** `<blueprint>/claude.md` carries a
Project Configuration block, but the blueprint is a shared submodule, so that
block holds **upstream defaults, not this project's answers**. Never take it at
face value. Establish the real configuration — `tenancy`, `database`,
`realtime`, `jobs`, `ai`, `frontend`, `deploy` — in this order:

1. If the project records its own answers (a root `claude.md` / `CLAUDE.md`,
   `AI.md`, an ADR under `docs/adr/`), that record wins.
2. Otherwise infer from evidence in the repo, and print what you inferred so the
   user can correct it before the review runs.

Evidence to look for:

| Setting | Evidence |
|---|---|
| `tenancy: shared` | A tenant model, `tenant_id` columns, RLS policies in migrations, a `WithTenant` helper in `platform/db` |
| `database` | The driver imported in `platform/db` (`gorm.io/driver/postgres` vs `sqlite`); note that `tenancy: shared` in production implies postgres |
| `realtime: true` | A `cmd/gateway` binary, a connections or channel package |
| `jobs: true` | A `cmd/worker` binary, a `job` table, an enqueue helper |
| `ai: true` | An `internal/platform/ai` package, an `ai_call` table, an `evals/` directory |
| `frontend` | `server` if there is a `views/` tree, `html/template` rendering and a Tailwind `static/css/input.css`; `api` if handlers only return JSON |
| `deploy` | `binary` if assets are embedded and there is a systemd unit; `docker` if there is a Dockerfile |

`scale.md` is a judgement, not a flag: it applies when the dominant table is
large. Say which way you judged it.

**Blueprint version.** Read `<blueprint>/VERSION` and the project's
`blueprint_version` (recorded with its configuration answers). Print both above
the report. If the project is behind, or records no version, read the files in
`<blueprint>/upgrades/` that are newer than the project's version and pass the
changes they name to the reviewer with this instruction: a finding that exists
only because of one of those changes, or because of a step listed under the
project's `blueprint_skipped`, is reported as **Low** and labelled
`pending upgrade <version>`. It does not block. Critical findings are never
downgraded this way: a security or tenant-isolation hole blocks whatever version
the project is on.

### Step 3: Run the Mechanical Checks First

Unlike the Python blueprint, this one ships real mechanical checks. Run them
before the reviewer so it starts from facts rather than re-deriving them:

```bash
gofmt -l .                      # must print nothing
go vet ./...
go build ./...
go test ./... -race -count=1
```

On `frontend: server`, if the change touched a template or the Tailwind input,
check that the committed stylesheet is current. An uncompiled class renders
unstyled in production and nowhere else, and no Go check can see it:

```bash
make css                                 # Tailwind standalone binary, rebuilds app.css
git status --porcelain -- '*app.css'     # must print nothing
```

If `app.css` changed, the committed stylesheet was stale. Report it as a
finding and leave the rebuilt file for the implementer to commit. If the project
has no `css` target, say so — the blueprint's Makefile defines one.

Run any grep checks the project or blueprint defines (for example, that
`db.Unscoped()` appears only under `platform/`), and, if the change touched the
blueprint submodule's own docs:

```bash
<blueprint>/scripts/check-links.py
```

Report any failure as a finding in its own right, then continue — a failing build
does not excuse skipping the conformance review, because the two catch disjoint
problems. Note in the report which checks you ran and what they returned.

### Step 4: Launch the Reviewer Agent

Use the Agent tool. Give it the changed files, the resolved blueprint path, the
mechanical check results, and — critically — the active layer list, so it knows
which docs it is forbidden to open.

```
You are a blueprint compliance reviewer for a Go service. Your role is
ADVERSARIAL — you are looking for violations, not confirming correctness.

You did not write this code. You did not plan this feature. You have no
investment in it passing review.

## Scope

Review ONLY these changed files: <changed file paths>
Blueprint: <resolved blueprint path> (master doc <blueprint>/claude.md)

Active project configuration:
<the resolved yaml block from Step 2: tenancy, database, realtime, jobs,
ai, frontend, deploy — plus whether scale.md applies>

Mechanical checks already run: <gofmt/vet/build/test/css/grep results
from Step 3>

## Reading rules — these are hard constraints

1. Read the docs in <blueprint>/patterns/core/ that the changed files pull
   in. Always in scope: mvc.md, database.md, data-modeling.md, auth.md,
   security.md, testing.md, observability.md, i18n.md, deployment.md,
   audit.md
2. Read a layer doc in <blueprint>/patterns/ ONLY if its condition holds:
     tenancy.md                    -> only if tenancy: shared
     jobs.md                       -> only if jobs: true
     realtime.md                   -> only if realtime: true
     ai.md                         -> only if ai: true
     frontend.md, htmx.md,
       mobile-navigation.md        -> only if frontend: server
     embed.md                      -> only if deploy: binary
     staging.md                    -> only if staging: true
     admin-api.md                  -> only if admin_api: true
     scale.md                      -> only if the dominant table is large
                                      and the change touches its query path
   If a layer is disabled, do not open its doc at all — not for reference,
   not for context. On frontend: api there is no view layer to review.
3. For deployment and the database engine, read only the branch the
   configuration selects: the systemd service (deploy: binary) or the
   Docker build (deploy: docker) inside core/deployment.md, and the
   postgres or sqlite branch wherever a doc carries both. Do not review
   against the branch this project did not choose.

Which docs the changes pull in:
   Models       -> core/mvc.md, core/database.md, core/data-modeling.md,
                   core/audit.md, core/performance.md
   Controllers  -> core/mvc.md, core/auth.md, core/security.md,
                   core/performance.md
   Middleware   -> core/security.md, core/auth.md, core/observability.md
   Migrations   -> core/database.md, core/audit.md
   main.go      -> core/deployment.md, core/observability.md
   Templates    -> frontend.md, htmx.md, core/i18n.md, core/performance.md;
                   layouts and navigation partials also mobile-navigation.md
   Tests        -> core/testing.md

## Rules

Do NOT:
- Assume the implementer "probably had a good reason"
- Treat a green build as evidence of conformance — every non-negotiable in
  this blueprint compiles and vets clean
- Skip a violation because it is minor
- Praise the code
- Suggest improvements the blueprint does not require
- Read GOAL.md, PLAN.md, or any spec — that context is what produces
  rationalization
- Audit files outside the changed list
- Open a disabled layer's doc

DO check every changed Go file for MVC violations: business logic in
controllers, the database handle touched from a controller, validation in
the wrong place. Controllers parse input, call models, render.

DO check the blueprint's non-negotiables explicitly. Each fails silently,
which is why they are the list:

Tenancy (only if tenancy: shared):
- Tenant-scoped queries go through db.WithTenant, never the shared db.Get()
  handle. db.Unscoped() is the owner connection and is platform-only.
- The application connects as a non-owner, NOBYPASSRLS role
- FORCE ROW LEVEL SECURITY, not just ENABLE
- Every policy has BOTH USING and WITH CHECK — USING alone permits
  cross-tenant INSERT
- Tenant set with set_config(..., true) INSIDE a transaction
- On database: sqlite, the predicate comes from GORM callbacks, which Raw
  and Exec bypass
- An unscoped query on a tenant-scoped table is Critical, not Medium

Database:
- No query per row (N+1) anywhere that lists rows: controllers, model
  functions, template loops, jobs. Batch lookups into a map, Joins/Preload per
  query, counts and groups in SQL, never Preload(clause.Associations) on a list
  (core/performance.md)
- SQL migrations in goose, never AutoMigrate; every schema change has one
- Migrations run at deploy as the owner, never inside application startup
- tenant_id leads every composite index on a tenant-scoped table
- Cursor pagination on the dominant table, not OFFSET

Record audit (core/audit.md):
- New business tables carry created_by / updated_by as core/audit.md
  specifies, set by the model layer from the authenticated user — never
  from request input

Jobs (only if jobs: true):
- A job is enqueued INSIDE the transaction that produced its cause
- Every handler is idempotent; delivery is at-least-once
- A handler takes its transaction from context and never opens its own —
  one that calls db.Get() has left the tenant scope

Realtime (only if realtime: true):
- Per-connection send buffers are bounded; a full buffer closes the
  connection
- Every event carries a server-assigned per-channel sequence

AI (only if ai: true):
- Inference runs in a job, never in a handler
- Output validated against a schema before storage; citations verified
- Nothing derived shown without provenance
- Every call written to ai_call with tokens and cost; budgets enforced

Frontend (only if frontend: server):
- All user-visible strings in i18n catalogs
- Every interactive component carries its own ARIA; Tailwind ships none
- Tailwind v4 utilities and the component layer in input.css, not invented
  one-off CSS; Alpine.js for client state; no npm, webpack or vite
- Logical properties (ms-/me-/ps-/pe-/text-start), never ml-/mr-/text-left
  — physical properties silently break RTL
- Any template change is reflected in a rebuilt, committed app.css
- CSRF token on every non-GET form; correct HTMX attributes
- html/template auto-escaping; no raw HTML unless trusted

Observability:
- One trace_id spans request -> job -> model call
- No tenant, user, or entity ID in a Prometheus label
- Never log credentials, user content, or model completions

Go conventions:
- Pointer receivers on models
- Context passed to every DB call, with a timeout
- No global singletons outside platform/db
- Files around 300 lines, split by concern

Also check for SQL injection, hardcoded secrets, and missing auth
middleware on protected routes.

## Report Format

Respond with ONLY this:

**Blueprint:** <path> @ <submodule short sha>
**Active layers:** <the layers you were permitted to read>
**Mechanical checks:** <gofmt/vet/build/test/css/grep results>
**Files reviewed:** <count>
**Patterns checked:** <list>
**Result:** FAIL | WARN | PASS

### Violations
| # | Severity | Pattern | File:Line | Description |
|---|----------|---------|-----------|-------------|

If none: "No violations found."

### Conformance
| Pattern | Score | Status | Notes |
|---------|-------|--------|-------|
(one row per category: MVC, Database, Data modeling, Record audit, Auth,
Security, Testing, Observability, i18n, Deployment, Go conventions, and
one per enabled layer — Tenancy, Jobs, Realtime, AI, Frontend, HTMX,
Mobile navigation, Embed, Scale. N/A for categories the changed files
never touched. Leave disabled layers out entirely.)

**Overall: X.X/10**

### Severity Guide
- Critical: exploitable security issue, cross-tenant data exposure, or
  fundamental architecture violation
- High: significant pattern violation
- Medium: best-practice violation
- Low: minor convention issue
```

### Step 5: Score

The reviewer fills the conformance table using this scale, per pattern category:

| Violations in that category | Score |
|---|---|
| none | 10/10 |
| 1 minor | 9/10 |
| 2-3 minor | 8/10 |
| 1 major, or 4+ minor | 7/10 |
| 2 major | 6/10 |
| 3+ major | 5/10 or lower |

**Major** — business logic in a controller, a tenant-scoped query off
`db.WithTenant`, a query per row on a list (N+1), a policy missing `WITH CHECK`,
`AutoMigrate` in place of a goose migration, a schema change with no migration,
a job enqueued outside its causing
transaction, inference in a handler, an unbounded send buffer, a missing auth
middleware on a protected route, a missing CSRF token, SQL injection, a stale
`app.css`, a high-cardinality Prometheus label, a violated non-negotiable.

**Minor** — a missing context timeout, an unwrapped user-facing string, a value
receiver where the blueprint asks for a pointer, a missing doc comment, naming
drift, an oversized file.

Score `N/A` for any category the changed files never touched. Do not average N/A
rows into the overall.

Strictness by category:

- **Strict** — MVC separation, tenancy, database, security (CSRF, injection,
  auth), jobs durability, AI provenance. These are correctness and safety, and
  every one of them fails silently.
- **Moderate** — observability, HTMX usage, record audit, Go conventions. Flag
  them; do not fail the review over one missing context timeout.
- **Lenient** — i18n on admin-only text, doc comments on small helpers, test
  coverage where critical paths are already covered.

### Step 6: Present Findings

1. Print the violations table and the conformance table in full.
2. State the result:
   - **FAIL** — "Critical or High violations. Fix before committing."
   - **WARN** — "Minor violations. Worth fixing, safe to proceed."
   - **PASS** — "No violations."
3. For each Critical and High violation, suggest the concrete fix with the
   pattern section it comes from. Where `examples/` contains the correct shape,
   point at it — it compiles and is tested, so it settles an argument that prose
   alone would not.
4. If the active layer set had to be inferred rather than read from a project
   record, say so, and suggest recording it — in a root `claude.md` or an ADR —
   so the next review does not have to guess.

### Step 7: Act on the Result

When this ran as the post-implementation gate:

- **FAIL** — fix the Critical and High violations, then re-run the audit on the
  corrected files. Do not report the work complete on a FAIL, and do not commit.
  Fixing in place and re-auditing is the expected loop, not an escalation.
- **WARN** — report the work complete with the violations table included, and say
  plainly which ones you left. Do not silently drop them.
- **PASS** — report the work complete and include the conformance table.

If a violation is one you intend not to fix, say so and give the reason. An
unfixed finding that the user can see is a decision; one you quietly discard is a
regression.

## Rules

- **Never put GOAL.md, PLAN.md, or the spec in the reviewer's context.** This is
  the single most important rule here.
- **Never let the reviewer read a disabled layer.** This is the second.
- This skill reviews and reports. It does not edit code. Fixes happen back in the
  implementing context, after the report. (`make css` in Step 3 regenerates a
  build artifact, not source; committing it is the implementer's fix.)
- Run it standalone, not nested inside another skill's workflow.
- Findings should be specific enough to act on without further explanation — the
  review often lands in a PR read by people outside the team.
- If a violation reveals a genuine gap in the blueprint rather than a mistake in
  the code, say so. The fix belongs in `remarqable/blueprint-go`, where every
  project picks it up — never edited in place inside the submodule checkout. If
  the gap is in a pattern that `examples/` covers, the fix belongs there too, so
  CI keeps it honest.
