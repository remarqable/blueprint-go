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
   git commit -m "changes"  # ❌ This modifies the blueprint
   ```

2. **The submodule is tracked as a reference only:**
   - Git tracks which commit hash your project references
   - Changes inside `blueprint/` are ignored by your main project
   - Run `git status` from your project root (not inside `blueprint/`)

3. **To update the blueprint to a newer version:**
   ```bash
   cd blueprint
   git fetch
   git checkout main  # or a specific version tag like v1.0.0
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

- **[mvc.md](patterns/mvc.md)** - Models (fat), Views (templates), Controllers (thin)
- **[database.md](patterns/database.md)** - Migrations, JSONB, full-text search, RLS, indexes
- **[htmx.md](patterns/htmx.md)** - Interactive patterns (inline edit, delete, modals)
- **[frontend.md](patterns/frontend.md)** - Bootstrap 5 UI, responsive design
- **[i18n.md](patterns/i18n.md)** - Multi-language support with JSON catalogs
- **[auth.md](patterns/auth.md)** - Magic links, sessions, OAuth
- **[security.md](patterns/security.md)** - CSRF, rate limiting, input validation
- **[testing.md](patterns/testing.md)** - Unit, integration, HTTP tests
- **[deployment.md](patterns/deployment.md)** - Docker, production checklist

---

## 🏗️ Architecture

**Classic MVC Pattern:**

- **Models** (`internal/models/`) - Fat models with business logic + database access
- **Views** (`views/`) - Server-rendered HTML templates with HTMX for interactivity
- **Controllers** (`internal/controllers/`) - Thin HTTP handlers that route requests

**Key Principles:**

- ✅ Fat models, thin controllers
- ✅ Server-rendered HTML (no SPA complexity)
- ✅ HTMX for rich interactivity without JavaScript
- ✅ Bootstrap for professional UI
- ✅ Progressive enhancement (works without JavaScript)

---

## 🎯 Features

### Core Stack

- **Go 1.21+** with Gin web framework
- **PostgreSQL 15+** with goose migrations
- **HTMX 1.9+** for rich interactivity
- **Bootstrap 5.3+** for responsive UI
- **sqlx** for clean SQL queries (no ORM)
- **html/template** for secure server-rendered views
- **zerolog** for structured logging

### Built-in Patterns

- ✅ **Internationalization (i18n)** - Multi-language from day one
- ✅ **Magic link authentication** - Passwordless, secure
- ✅ **CSRF protection** - All state-changing requests protected
- ✅ **Rate limiting** - Prevent abuse
- ✅ **Input validation** - Server-side validation in models
- ✅ **Transaction rollback testing** - Fast, isolated tests
- ✅ **Error handling** - Structured errors with i18n
- ✅ **Request logging** - Structured logs with request context

### Data Patterns

- **B2C (default)**: User-owned data with `user_id` foreign keys
- **B2B (optional)**: Multi-tenant with PostgreSQL Row-Level Security (RLS)

See [patterns/database.md#multi-tenancy](patterns/database.md#multi-tenancy-with-row-level-security) for migration guide.

---

## 🎓 Example Applications

Reference implementations demonstrating these patterns are maintained in **separate repositories**:

### Coming Soon

- **[blueprint-taskmanager](https://github.com/remarqable/blueprint-taskmanager)** - Task management app
  - Complete CRUD with HTMX
  - Inline editing, animations
  - Full test coverage

### Future Examples

- **blueprint-multitenant** - B2B SaaS with Row-Level Security
- **blueprint-ecommerce** - Product catalog, cart, checkout
- **blueprint-api** - API-first architecture

**Why separate repos?** Each example is a complete application that can evolve independently. They use this blueprint as a git submodule for reference.

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
- **No ORM** - Clear SQL queries, predictable performance
- **No build step** - Bootstrap + HTMX work out of the box
- **No custom CSS** - Bootstrap utilities cover 95% of use cases
- **Progressive enhancement** - Works without JavaScript

---

## 🚢 Updating Blueprint Version

When using as a git submodule, pin to specific versions:

```bash
# In your project with blueprint submodule
cd blueprint
git fetch
git checkout v1.0.0  # Pin to specific version
cd ..
git add blueprint
git commit -m "Update blueprint to v1.0.0"
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

MIT License - Use this as a starting point for your projects.

---

**Build fast. Read easily. Scale calmly.**
