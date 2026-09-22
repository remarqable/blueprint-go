---
name: blueprint-go-audit
description: Audit Go code against the Go + Gin SaaS blueprint. Launches an independent adversarial reviewer agent over the changed files, then reports violations and a scored conformance table across MVC, database, data modeling, security, testing, observability, i18n, deployment, and the enabled layers. Respects the blueprint's scoped-reading rule. This is the mandatory verification gate — run it after every implementation, before reporting the work complete.
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

- **Do not skip it because the change is small.** The violations this catches are
  overwhelmingly small changes.
- **Do not skip it because the build is green.** Green is the precondition, not
  the result.
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

- The core patterns apply to every project. Always in scope: `mvc`, `database`,
  `data-modeling`, `auth`, `security`, `testing`, `observability`, `i18n`,
  `deployment`.
- A layer doc is in scope **only** if its condition holds:

  | Doc | Condition |
  |-----|-----------|
  | `tenancy.md` | `tenancy: shared` |
  | `jobs.md` | `jobs: true` |
  | `realtime.md` | `realtime: true` |
  | `ai.md` | `ai: true` (which requires `jobs: true`) |
  | `frontend.md`, `htmx.md` | `frontend: server` — both are out of scope on `frontend: api` |
  | `embed.md` | `deploy: binary` |
  | `scale.md` | the dominant table is large, and the change touches its query path |

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
| `internal/models/*.go` | MVC, database, data-modeling, tenancy |
| `internal/controllers/*.go` | MVC, auth, security |
| `internal/middleware/*.go` | Security, auth, observability |
| `internal/platform/db/**` | Database, tenancy |
| `internal/platform/obs/**` | Observability |
| `internal/jobs/**`, `cmd/worker/**` | Jobs layer |
| `internal/gateway/**`, `cmd/gateway/**` | Realtime layer |
| `internal/platform/ai/**`, `evals/**` | AI layer |
| `views/**/*.html` | Frontend, HTMX, i18n |
| `migrations/*.sql` | Database, tenancy |
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
- Patterns: `<blueprint>/patterns/*.md` — flat, no `core/` subdirectory
- Compiling reference: `<blueprint>/examples/` — the platform code the patterns
  are extracted from, with tests asserting what they claim

If the directory is empty, the submodule is not initialized. Stop and tell the
user to run `git submodule update --init --recursive` — do not review against
patterns you could not read.

**Determining which layers are active.** `<blueprint>/claude.md` carries a
Project Configuration block, but the blueprint is a shared submodule, so that
block usually holds **upstream defaults, not this project's answers**. Never take
it at face value. Establish the real configuration in this order:

1. If the project records its own answers (a root `claude.md` / `CLAUDE.md`,
   `AI.md`, an ADR under `docs/adr/`), that record wins.
2. Otherwise infer from evidence in the repo, and print what you inferred so the
   user can correct it before the review runs.

Evidence to look for:

| Setting | Evidence |
|---|---|
| `tenancy: shared` | A tenant model, `tenant_id` columns, RLS policies in migrations, a `WithTenant` helper in `platform/db` |
| `database` | The driver imported in `platform/db` (`gorm.io/driver/postgres` vs `sqlite`); note that `tenancy: shared` in production implies postgres |
| `jobs: true` | A `cmd/worker` binary, a `job` table, an enqueue helper |
| `realtime: true` | A `cmd/gateway` binary, a connections or channel package |
| `ai: true` | An `internal/platform/ai` package, an `ai_call` table, an `evals/` directory |
| `frontend` | `server` if there is a `views/` tree and `html/template` rendering; `api` if handlers only return JSON |
| `deploy` | `binary` if assets are embedded and there is a systemd unit; `docker` if there is a Dockerfile |

### Step 3: Run the Mechanical Checks First

Unlike the Python blueprint, this one ships real mechanical checks. Run them
before the reviewer so it starts from facts rather than re-deriving them:

```bash
gofmt -l .                      # must print nothing
go vet ./...
go build ./...
go test ./... -race -count=1
```

And, if the change touched the blueprint submodule's own docs:

```bash
<blueprint>/scripts/check-links.py
```

Report any failure as a finding in its own right, then continue — a failing build
does not excuse skipping the conformance review, because the two catch disjoint
problems. Note in the report which checks you ran and what they returned.

### Step 4: Launch the Reviewer Agent

Use the Agent tool. Give it the changed files, the resolved blueprint path, and —
critically — the active layer list, so it knows which docs it is forbidden to
open.

```
You are a blueprint compliance reviewer for a Go service. Your role is
ADVERSARIAL — you are looking for violations, not confirming correctness.

You did not write this code. You did not plan this feature. You have no
investment in it passing review.

## Scope

Review ONLY these changed files: <changed file paths>
Blueprint: <resolved blueprint path> (master doc <blueprint>/claude.md)

Active project configuration:
<the resolved yaml block from Step 2>

Mechanical checks already run: <gofmt/vet/build/test results from Step 3>

## Reading rules — these are hard constraints

1. Read the core patterns the changed files pull in. Always in scope:
   mvc.md, database.md, data-modeling.md, auth.md, security.md,
   testing.md, observability.md, i18n.md, deployment.md
2. Read a layer doc ONLY if its condition holds:
     tenancy.md              -> only if tenancy: shared
     jobs.md                 -> only if jobs: true
     realtime.md             -> only if realtime: true
     ai.md                   -> only if ai: true
     frontend.md, htmx.md    -> only if frontend: server
     embed.md                -> only if deploy: binary
     scale.md                -> only if the change touches the dominant
                                table's query path
   If a layer is disabled, do not open its doc at all — not for reference,
   not for context. On frontend: api there is no view layer to review.
3. patterns/ is flat. There is no core/ subdirectory — do not invent one.

Which core docs the changes pull in:
   Models       -> mvc.md, database.md, data-modeling.md
   Controllers  -> mvc.md, auth.md, security.md
   Middleware   -> security.md, auth.md, observability.md
   Migrations   -> database.md
   main.go      -> deployment.md, observability.md
   Templates    -> frontend.md, htmx.md, i18n.md
   Tests        -> testing.md

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
- SQL migrations in goose, never AutoMigrate
- Migrations run at deploy as the owner, never inside application startup
- tenant_id leads every composite index on a tenant-scoped table
- Cursor pagination on the dominant table, not OFFSET

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
- Every interactive component carries its own ARIA
- Bootstrap 5 classes, not invented custom ones; no npm/webpack/vite
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
**Mechanical checks:** <gofmt/vet/build/test results>
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
(one row per pattern category you checked; N/A for categories the
changed files never touched)

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
`db.WithTenant`, a policy missing `WITH CHECK`, `AutoMigrate` in place of a goose
migration, a job enqueued outside its causing transaction, inference in a
handler, an unbounded send buffer, a missing auth middleware, SQL injection, a
schema change with no migration, a high-cardinality Prometheus label.

**Minor** — a missing context timeout, an unwrapped user-facing string, a value
receiver where the blueprint asks for a pointer, naming drift, an oversized file.

Score `N/A` for any category the changed files never touched. Do not average N/A
rows into the overall.

Strictness by category:

- **Strict** — tenancy, database, security, jobs durability, AI provenance.
  These are correctness and safety, and every one of them fails silently.
- **Moderate** — observability, HTMX usage, Go conventions. Flag them; do not
  fail the review over one missing context timeout.
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
  implementing context, after the report.
- Run it standalone, not nested inside another skill's workflow.
- If a violation reveals a genuine gap in the blueprint rather than a mistake in
  the code, say so. The fix belongs in `remarqable/blueprint-go`, where every
  project picks it up — never edited in place inside the submodule checkout. If
  the gap is in a pattern that `examples/` covers, the fix belongs there too, so
  CI keeps it honest.
