# Go+Gin SaaS Blueprint

> **Production-ready patterns for building Go+Gin SaaS applications with MVC, HTMX, and PostgreSQL**

---

## 🚀 Quick Start

### Using This Blueprint as a Git Submodule

Add this blueprint to your project as a **read-only reference**:

```bash
# In your new project
mkdir myapp && cd myapp
git init
git submodule add https://github.com/remarqable/blueprint-go.git blueprint
cat blueprint/claude.md  # Follow the 30-minute bootstrap
```

### Keeping the Submodule Read-Only

**Important:** The blueprint submodule should remain unchanged in your project. To prevent accidental modifications:

1. **Never commit changes from inside the submodule:**
   ```bash
   # DON'T do this:
   cd blueprint
   git add .
   git commit -m "changes"  # This modifies the blueprint
   ```

2. **The submodule is tracked as a reference only:**
   - Git tracks which commit hash your project references
   - Changes inside `blueprint/` are ignored by your master project
   - Run `git status` from your project root (not inside `blueprint/`)

3. **To update the blueprint to a newer version:**
   ```bash
   cd blueprint
   git fetch
   git checkout origin/main  # or a specific commit
   cd ..
   git add blueprint
   git commit -m "Update blueprint to latest version"
   ```

4. **If you accidentally modify files in the submodule:**
   ```bash
   cd blueprint
   git checkout .  # Discard all changes
   git clean -fd   # Remove untracked files
   cd ..
   ```

**Best Practice:** Treat the `blueprint/` directory as read-only documentation. Copy patterns and code to your own project directories instead of editing the blueprint directly.

### Versioning & Updates

A submodule always pins an exact commit, which is the guarantee you want: the
blueprint your project was built against cannot change under you.

```bash
# Pin to the current commit during setup (this is what `git submodule add` does)
cd .. && git add blueprint && git commit -m "Pin the blueprint"

# Move to a newer one deliberately, and read what changed first
cd blueprint && git fetch && git log --oneline HEAD..origin/main
cd blueprint && git checkout <commit>
cd .. && git add blueprint && git commit -m "Update the blueprint to <commit>"
```

No version tags are published yet, so pin by commit and move deliberately when
you update.

---

## 📚 Documentation

### Master Blueprint

**[claude.md](claude.md)** - Single-file master guide covering:
- 30-minute bootstrap sequence
- MVC architecture patterns
- Database setup and migrations
- HTMX interactivity patterns
- i18n from day one
- Security best practices
- Testing with transaction rollback
- Production deployment

### Pattern Guides

Detailed architecture and coding standards in `patterns/`:

**Core — applies to every project:**

- **[mvc.md](patterns/mvc.md)** - Models (fat), Views (templates), Controllers (thin)
- **[database.md](patterns/database.md)** - Migrations, JSONB, full-text search, indexes
- **[data-modeling.md](patterns/data-modeling.md)** - Evidence vs derivation, temporal data, graph shapes
- **[auth.md](patterns/auth.md)** - Magic links, sessions, OAuth
- **[security.md](patterns/security.md)** - CSRF, rate limiting, input validation
- **[testing.md](patterns/testing.md)** - Unit, integration, HTTP tests
- **[observability.md](patterns/observability.md)** - Correlation IDs, structured logs, metrics, alerting
- **[i18n.md](patterns/i18n.md)** - Multi-language support with JSON catalogs
- **[deployment.md](patterns/deployment.md)** - Docker, production checklist

**Layers — read only when the project needs them:**

- **[tenancy.md](patterns/tenancy.md)** - Row-Level Security, roles, request/worker scoping, isolation tests
- **[jobs.md](patterns/jobs.md)** - Durable Postgres queue, retries, idempotency, workers
- **[realtime.md](patterns/realtime.md)** - WebSocket/SSE, fanout, ordering, reconnect-and-resume
- **[ai.md](patterns/ai.md)** - LLM providers, structured output, provenance, cost control, evals
- **[frontend.md](patterns/frontend.md)** - Bootstrap 5 UI, responsive design
- **[htmx.md](patterns/htmx.md)** - Interactive patterns (inline edit, delete, modals)
- **[embed.md](patterns/embed.md)** - Single-binary asset embedding
- **[scale.md](patterns/scale.md)** - Cursor pagination, partitioning, pooling, caching

### Skills

Procedures to run, as distinct from patterns to read, in `skills/`:

- **[blueprint-go-audit](skills/blueprint-go-audit/SKILL.md)** - the mandatory
  conformance gate. See [skills/README.md](skills/README.md).

The blueprint is one architecture with optional layers, not a menu. Establish
the project configuration in [claude.md](claude.md#project-configuration) first,
then read only the layers whose condition holds.

### Verifying it

Two different things get verified, and neither substitutes for the other.

**That the blueprint is right.** `examples/` is a compiling skeleton of the
platform code these documents describe — handles, tenant scoping, the job queue,
the model provider — with tests asserting the properties the docs claim. CI
builds and tests it, so a pattern that stops compiling fails the build rather
than reaching someone's project.

```bash
cd examples && go test ./... -race
```

**That code built from it conforms.** Every implementation against this blueprint
ends with the **blueprint audit** — a separate reviewer agent, over the changed
files, that never sees the goal or the plan, because the agent that wrote the
code can justify every shortcut it took. It reports violations and a scored
conformance table, and Critical or High findings block the commit.

This matters more in Go than it looks. `gofmt`, `go vet` and `go test -race` are
good enough that a green build feels like a passing review, but every
non-negotiable in [claude.md](claude.md#non-negotiables) fails *silently* — a
handler on `db.Get()` rather than `db.WithTenant` compiles, vets clean, passes,
and leaks in production.

The agent runs it by reading `blueprint/skills/blueprint-go-audit/SKILL.md`, so
the gate needs nothing installed and holds in a fresh clone. To invoke the same
audit yourself as a slash command:

```bash
make skills        # symlink skills/* into .claude/skills/
# then: /blueprint-go-audit
```

### Checking the docs

Cross-references between pattern docs are load-bearing: an agent that follows a
dead link invents its own answer. `scripts/check-links.py` verifies every
internal link and heading anchor, and runs in CI.

```bash
./scripts/check-links.py          # verify
./scripts/check-links.py --list   # print every anchor
```

---

## 🏗️ Architecture

**Classic MVC Pattern:**

- **Models** (`internal/models/`) - Fat models with business logic + database access
- **Views** (`views/`) - Server-rendered HTML templates with HTMX for interactivity
- **Controllers** (`internal/controllers/`) - Thin HTTP handlers that route requests

**Key Principles:**

- Fat models, thin controllers
- Server-rendered HTML (no SPA complexity)
- HTMX for rich interactivity without JavaScript
- Bootstrap for professional UI
- Progressive enhancement (works without JavaScript)

---

## 🎯 Features

### Core Stack

- **Go 1.23+** with Gin web framework
- **PostgreSQL 15+** with goose migrations
- **HTMX 1.9+** for rich interactivity
- **Bootstrap 5.3+** for responsive UI
- **GORM** over PostgreSQL or SQLite, with SQL migrations in goose
- **html/template** for secure server-rendered views
- **zerolog** for structured logging

### Built-in Patterns

- **Internationalization (i18n)** - Multi-language from day one
- **Magic link authentication** - Passwordless, secure
- **CSRF protection** - All state-changing requests protected
- **Rate limiting** - Prevent abuse
- **Input validation** - Server-side validation in models
- **Transaction rollback testing** - Fast, isolated tests
- **Error handling** - Structured errors with i18n
- **Request logging** - Structured logs with request context

### Data Patterns

- **B2C (default)**: User-owned data with `user_id` foreign keys
- **B2B (optional)**: Multi-tenant with PostgreSQL Row-Level Security (RLS)

See [patterns/tenancy.md](patterns/tenancy.md) for the full guide.


---

## 📖 Philosophy

### Core Values

1. **Simple over clever** - No magic, explicit code
2. **Fast to iterate** - `make run` boots a working app
3. **Production-ready** - Security, logging, error handling from day one
4. **Global from start** - i18n baked in, not bolted on
5. **Server-rendered** - HTMX for interactivity, no SPA complexity

### Design Decisions

- **Fat models, thin controllers** - Business logic lives with data
- **GORM for reads and writes, SQL for schema** - migrations are goose SQL you can read, so the schema is never inferred from struct tags
- **No build step** - Bootstrap + HTMX work out of the box
- **No custom CSS** - Bootstrap utilities cover 95% of use cases
- **Progressive enhancement** - Works without JavaScript

---

## 🚢 Updating Blueprint Version

When using as a git submodule, pin to specific versions:

```bash
# In your project with the blueprint submodule
cd blueprint
git fetch
git checkout <commit>
cd ..
git add blueprint
git commit -m "Update the blueprint to <commit>"
```

---

## 🤝 Contributing

Improvements should come from **real-world usage**:

1. Use this blueprint in your project
2. Discover a better pattern through production experience
3. Open an issue with evidence
4. Community discusses and validates
5. Blueprint updated if broadly valuable

**Philosophy:** Patterns proven in production > theoretical improvements

---

## 📜 License

[MIT](LICENSE). Use it, copy it into your own project, change it. Attribution
is appreciated but the licence only asks you to keep the notice.

---

**Build fast. Read easily. Scale calmly.**
