# Database Reference Guide

> Comprehensive database patterns for Go+Gin SaaS applications using GORM with SQLite or PostgreSQL

---

## Database Selection

Choose your database based on your needs:

| Feature | SQLite | PostgreSQL |
|---------|--------|------------|
| Setup complexity | None (file-based) | Docker or install |
| Concurrency | Limited (file locks) | Excellent |
| JSON support | TEXT field | Native JSONB |
| Full-text search | Basic FTS5 | Advanced tsvector |
| Row-Level Security | No | Yes |
| Migrations (goose) | Yes | Yes |
| GORM support | Full | Full |
| Best for | Dev, single-server, simple apps | Production, multi-server, advanced features |

---

## Table of Contents

- [Database Selection](#database-selection)
- [Database Conventions](#database-conventions)
- [Connection Management](#connection-management)
- [Migrations](#migrations)
- [JSONB Usage Patterns](#jsonb-usage-patterns)
- [Full-text Search](#full-text-search)
- [Indexes and Performance](#indexes-and-performance)
- [Multi-Tenancy](#multi-tenancy)
- [Database Synchronization](#database-synchronization)
- [Troubleshooting](#troubleshooting)

---

## Database Conventions

### Naming Style

**Tables**: lowercase, **singular**
```sql
-- Good
CREATE TABLE "user" (...);
CREATE TABLE product (...);
CREATE TABLE order_item (...);

-- Bad
CREATE TABLE Users (...);
CREATE TABLE PRODUCTS (...);
CREATE TABLE OrderItems (...);
```

**Columns**: `snake_case`
```sql
created_at, is_admin, created_by_user_id, first_name
```

**Primary Keys**: Choose one pattern and stick to it
```sql
-- Option 1: BIGSERIAL (auto-incrementing integers)
id BIGSERIAL PRIMARY KEY

-- Option 2: UUID (distributed systems, no sequence)
id UUID DEFAULT gen_random_uuid() PRIMARY KEY
```

**Foreign Keys**: `<entity>_id`
```sql
user_id, product_id, order_id, tenant_id
```

**Join Tables**: `<left>_<right>`
```sql
product_tag, order_item, team_member
```

### Standard Columns

**Timestamps** (add to all tables):
```sql
created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
```

**Auto-update trigger for `updated_at`**:
```sql
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = CURRENT_TIMESTAMP;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Apply to each table
CREATE TRIGGER set_user_updated_at BEFORE UPDATE ON "user"
FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

**Booleans and Enums**:
```sql
-- Flags
is_admin BOOLEAN DEFAULT false,
archived BOOLEAN DEFAULT false

-- Small enums: TEXT + CHECK
status TEXT CHECK (status IN ('pending', 'active', 'completed'))

-- Stable enums: Postgres ENUM
CREATE TYPE order_status AS ENUM ('pending', 'paid', 'shipped', 'delivered');
status order_status DEFAULT 'pending'
```

---

## Connection Management

### Database Handle Pattern (GORM)

```go
// internal/platform/db/db.go
package db

import (
  "gorm.io/gorm"
  "gorm.io/driver/sqlite"
  "gorm.io/driver/postgres"
)

var gdb *gorm.DB

func SetDB(database *gorm.DB) { gdb = database }
func Get() *gorm.DB { return gdb }

// ConnectSQLite connects to SQLite database
func ConnectSQLite(path string) (*gorm.DB, error) {
  return gorm.Open(sqlite.Open(path), &gorm.Config{})
}

// ConnectPostgres connects to PostgreSQL database
func ConnectPostgres(dsn string) (*gorm.DB, error) {
  return gorm.Open(postgres.Open(dsn), &gorm.Config{})
}

// WithTx wraps operations in a transaction
func WithTx(fn func(tx *gorm.DB) error) error {
  return gdb.Transaction(fn)
}

// WithContext returns DB with context for timeouts
func WithContext(ctx context.Context) *gorm.DB {
  return gdb.WithContext(ctx)
}
```

### Connection Pooling (PostgreSQL only)

```go
// cmd/app/main.go
database, err := db.ConnectPostgres(dsn)
if err != nil {
  log.Fatal().Err(err).Msg("failed to connect to database")
}

// Get underlying sql.DB for pool settings
sqlDB, _ := database.DB()

// Production settings
sqlDB.SetMaxOpenConns(25)                    // Max open connections
sqlDB.SetMaxIdleConns(5)                     // Max idle connections
sqlDB.SetConnMaxLifetime(5 * time.Minute)    // Connection lifetime
```

> **Note:** SQLite doesn't need connection pooling - it uses file-level locking.

### Model Pattern (GORM)

```go
// internal/models/user.go
package models

import (
  "context"
  "time"

  "gorm.io/gorm"
  "yourapp/internal/platform/db"
  "yourapp/internal/platform/errors"
)

type User struct {
  ID        int64     `gorm:"primaryKey" json:"id"`
  Email     string    `gorm:"uniqueIndex" json:"email"`
  Name      string    `json:"name"`
  CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
  UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// GetByEmail finds user by email
func (u *User) GetByEmail(email string) error {
  result := db.Get().Where("email = ?", email).First(u)
  if result.Error != nil {
    if result.Error == gorm.ErrRecordNotFound {
      return errors.New(errors.CodeNotFound, "user not found")
    }
    return errors.Wrap(result.Error, errors.CodeDatabaseQuery, "failed to query user")
  }
  return nil
}

// Create inserts a new user
func (u *User) Create(ctx context.Context) error {
  return db.Get().WithContext(ctx).Create(u).Error
}
```

---

## Migrations

### Tool: goose

```bash
# Install
go install github.com/pressly/goose/v3/cmd/goose@latest

# Create migration
goose -dir migrations create add_tasks sql

# Apply migrations
export DATABASE_URL="postgres://user:pass@localhost:5432/db?sslmode=disable"
goose -dir migrations postgres "$DATABASE_URL" up

# Rollback
goose -dir migrations postgres "$DATABASE_URL" down

# Status
goose -dir migrations postgres "$DATABASE_URL" status
```

### Migration Template

```sql
-- migrations/001_init_schema.sql
-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE "user" (
  id BIGSERIAL PRIMARY KEY,
  email TEXT NOT NULL,
  name TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX idx_user_email_lower ON "user"(lower(email));

CREATE TRIGGER set_user_updated_at BEFORE UPDATE ON "user"
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS "user" CASCADE;
```

### Best Practices

- ✅ Never edit a migration after it's applied in shared environments
- ✅ Use `-- +goose Up` and `-- +goose Down` for reversibility
- ✅ Test migrations on a copy of production data
- ✅ Keep migrations small and focused
- ✅ Run migrations during deployment (CI/CD)

---

## JSONB Usage Patterns

### When to Use JSONB

- ✅ User preferences/settings (flexible schema)
- ✅ Event metadata (varying structures)
- ✅ API response caching
- ✅ Feature flags per entity
- ✅ Audit logs with arbitrary context

### Schema Example

```sql
CREATE TABLE product (
  id BIGSERIAL PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT,
  metadata JSONB,  -- flexible data
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Index for containment queries (@>)
CREATE INDEX idx_product_metadata_gin ON product USING GIN (metadata);

-- Index specific keys
CREATE INDEX idx_product_metadata_category ON product USING GIN ((metadata->'category'));
```

### Go Integration (GORM)

```go
import (
  "gorm.io/datatypes"
)

// Model with JSON using GORM datatypes
type Product struct {
  ID          int64          `gorm:"primaryKey" json:"id"`
  Name        string         `json:"name"`
  Description string         `json:"description"`
  Metadata    datatypes.JSON `gorm:"type:jsonb" json:"metadata"`  // jsonb for PostgreSQL, text for SQLite
  CreatedAt   time.Time      `gorm:"autoCreateTime" json:"created_at"`
}

// For typed JSON (recommended)
type ProductMetadata struct {
  Category string            `json:"category"`
  Specs    map[string]string `json:"specs"`
  InStock  bool              `json:"in_stock"`
}

type ProductTyped struct {
  ID          int64                                `gorm:"primaryKey"`
  Name        string
  Metadata    datatypes.JSONType[ProductMetadata] `gorm:"type:jsonb"`
  CreatedAt   time.Time                           `gorm:"autoCreateTime"`
}
```

### Common Queries (GORM)

```go
// Insert with JSON
product := Product{
  Name:        "Laptop",
  Description: "Gaming laptop...",
  Metadata:    datatypes.JSON([]byte(`{"category":"electronics","in_stock":true}`)),
}
db.Get().Create(&product)

// Query by JSON field (PostgreSQL - uses jsonb operators)
var products []Product
db.Get().Where("metadata->>'in_stock' = ?", "true").Find(&products)

// Containment query (PostgreSQL)
db.Get().Where("metadata @> ?", `{"category": "electronics"}`).Find(&products)

// Using datatypes helper (works with both SQLite and PostgreSQL)
db.Get().Where(datatypes.JSONQuery("metadata").HasKey("category")).Find(&products)

// Update JSON field
db.Get().Model(&product).Update("metadata", datatypes.JSON([]byte(`{"in_stock":false}`)))
```

> **Note:** For SQLite, JSON is stored as TEXT. Use `datatypes.JSONQuery` for cross-database compatibility.

### Best Practices

- ❌ Don't overuse: prefer typed columns for known fields
- ✅ Index frequently queried keys
- ✅ Use `@>` (contains) for fast lookups
- ✅ Use `||` (concatenate) for updates
- ✅ Validate JSONB structure in application layer
- ✅ Consider `CHECK` constraints for required keys:
  ```sql
  ALTER TABLE product ADD CONSTRAINT check_metadata_has_category
    CHECK (metadata ? 'category');
  ```

---

## Full-text Search

### Preferred Approach: Generated tsvector Column

```sql
-- Add tsvector column (auto-generated from text fields)
ALTER TABLE product ADD COLUMN tsv tsvector
  GENERATED ALWAYS AS (
    to_tsvector('simple', coalesce(name,'') || ' ' || coalesce(description,''))
  ) STORED;

-- GIN index for fast search
CREATE INDEX idx_product_tsv ON product USING GIN (tsv);

-- Query
SELECT * FROM product
WHERE tsv @@ to_tsquery('simple', 'laptop')
ORDER BY ts_rank(tsv, to_tsquery('simple', 'laptop')) DESC;
```

### Alternative: Inline to_tsvector

```sql
-- No additional column, query-time indexing
SELECT * FROM product
WHERE to_tsvector('simple', name || ' ' || description) @@ to_tsquery('simple', 'laptop');

-- Still need GIN index
CREATE INDEX idx_product_search ON product USING GIN (to_tsvector('simple', name || ' ' || description));
```

### Partial/Fuzzy Search with pg_trgm

```sql
-- Enable extension
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Trigram index
CREATE INDEX idx_product_name_trgm ON product USING GIN (name gin_trgm_ops);

-- Query (prefix, substring, fuzzy)
SELECT * FROM product WHERE name ILIKE '%lap%';  -- Substring
SELECT * FROM product WHERE name % 'labtop';     -- Fuzzy (typo tolerance)
```

### Go Model Example (GORM)

```go
func SearchProducts(ctx context.Context, query string) ([]Product, error) {
  var products []Product
  err := db.Get().WithContext(ctx).
    Where("tsv @@ to_tsquery('simple', ?)", query).
    Order("ts_rank(tsv, to_tsquery('simple', ?)) DESC", query).
    Limit(50).
    Find(&products).Error
  return products, err
}

// Alternative using Raw for complex queries
func SearchProductsRaw(ctx context.Context, query string) ([]Product, error) {
  var products []Product
  err := db.Get().WithContext(ctx).Raw(`
    SELECT * FROM product
    WHERE tsv @@ to_tsquery('simple', ?)
    ORDER BY ts_rank(tsv, to_tsquery('simple', ?)) DESC
    LIMIT 50`, query, query).Scan(&products).Error
  return products, err
}
```

> **Note:** Full-text search is PostgreSQL-only. For SQLite, use LIKE queries or FTS5 extension.

---

## Indexes and Performance

### When to Add Indexes

**Always index:**
- ✅ Primary keys (automatic)
- ✅ Foreign keys (manual)
- ✅ Unique constraints (email, username, etc.)
- ✅ Columns used in WHERE, ORDER BY, JOIN

**Examples:**
```sql
-- Foreign keys
CREATE INDEX idx_order_user_id ON "order" (user_id);
CREATE INDEX idx_order_item_order_id ON order_item (order_id);
CREATE INDEX idx_order_item_product_id ON order_item (product_id);

-- Unique email (case-insensitive)
CREATE UNIQUE INDEX idx_user_email_lower ON "user"(lower(email));

-- Composite indexes (multi-column WHERE)
CREATE INDEX idx_order_user_status ON "order" (user_id, status);

-- Partial indexes (filtered)
CREATE INDEX idx_order_pending ON "order" (user_id) WHERE status = 'pending';

-- JSONB
CREATE INDEX idx_product_metadata_gin ON product USING GIN (metadata);

-- Full-text search
CREATE INDEX idx_product_tsv ON product USING GIN (tsv);

-- Trigram (fuzzy search)
CREATE INDEX idx_product_name_trgm ON product USING GIN (name gin_trgm_ops);
```

### Index Types

| Type | Use Case | Example |
|------|----------|---------|
| B-tree | Default, most queries | `CREATE INDEX idx_user_email ON "user"(email)` |
| GIN | JSONB, arrays, full-text | `CREATE INDEX idx_product_metadata ON product USING GIN (metadata)` |
| GIST | Geometric, full-text | Less common in SaaS apps |
| Hash | Equality only (rare) | `CREATE INDEX idx_user_id ON "user" USING HASH (id)` |

### Performance Tips

- ✅ Use `EXPLAIN ANALYZE` to test queries
- ✅ Monitor slow queries (`pg_stat_statements`)
- ✅ Keep indexes small (fewer columns)
- ✅ Drop unused indexes (they slow down writes)
- ✅ Consider partial indexes for large tables with filters

---

## Multi-Tenancy

Two mechanisms, chosen by database, behind one API.

**PostgreSQL: Row-Level Security.** A policy is merged into the `WHERE` clause
of every query the planner builds, so isolation holds even when application code
forgets it. This is the real thing and the reason to run PostgreSQL in
production.

**SQLite: application-layer scoping.** SQLite has no RLS. A GORM callback adds
the tenant predicate to every query. It works, and it is strictly weaker —
raw SQL bypasses it, so it is a development and single-tenant-deployment
convenience, not a security boundary.

Both are entered through the same `WithTenant` helper, so application code is
identical on either database. **Anything holding real multi-tenant data runs on
PostgreSQL.**

### The four silent failures (PostgreSQL)

It is easy to build something that looks like RLS and enforces nothing. Four
defaults work against you, and all four fail **silently** — no error, no
warning, just every tenant seeing every row.

**1. Table owners bypass RLS.** This is the big one. `ENABLE ROW LEVEL SECURITY`
does not apply to the role that owns the table, and in almost every project the
application connects as the role that ran the migrations — which owns the
tables. Your policies are live, `pg_policies` lists them, and they are skipped
on every query.

```sql
ALTER TABLE post ENABLE ROW LEVEL SECURITY;
ALTER TABLE post FORCE  ROW LEVEL SECURITY;   -- applies to the owner too
```

Better: do not connect as the owner at all. See [Roles](#roles).

**2. `USING` does not govern writes.** A `USING` clause filters rows a query can
*see* — SELECT, UPDATE, DELETE. It says nothing about rows you may *write*. With
a `USING`-only policy, any tenant can `INSERT` a row carrying another tenant's
`tenant_id`, and will then be unable to see the row it just created. You need
`WITH CHECK` for the write direction.

**3. `SET LOCAL` outside a transaction does nothing.** PostgreSQL accepts it,
emits `WARNING: SET LOCAL can only be used in transaction blocks`, and discards
it. Most drivers do not surface that warning.

**4. A shared handle is not a connection.** `SET` and `SET LOCAL` both apply to
one backend session. Issuing either against the global `*gorm.DB` from `db.Get()`
sets it on whichever pooled connection answered, and the next query will likely
use a different one. Add pgbouncer in transaction mode and even session-scoped
settings stop surviving between statements.

Failures 3 and 4 compound: the standard mistake is to set the tenant in HTTP
middleware against the shared handle, which is both outside a transaction and on
an arbitrary connection. It is a no-op twice over.

GORM makes failure 4 easier to hit than a raw driver would, because `db.Get()`
returns a usable handle from anywhere. Treat any tenant-scoped query issued
outside `WithTenant` as a bug — see [Enforcing the boundary](#enforcing-the-boundary).

### Roles

Do not let the application connect as the table owner.

```sql
-- migrations run as the owner
CREATE ROLE app_owner LOGIN PASSWORD '...';

-- the application connects as this one
CREATE ROLE app_user LOGIN PASSWORD '...' NOBYPASSRLS;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO app_user;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO app_user;
ALTER DEFAULT PRIVILEGES FOR ROLE app_owner IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO app_user;
```

Two DSNs: `DATABASE_OWNER_URL` for migrations and workers, `DATABASE_URL` for
the application. `NOBYPASSRLS` means that even if `app_user` is later granted
ownership by accident, policies still apply. Superusers always bypass RLS; never
run the application as one.

### Schema

```go
// internal/models/post.go
type Post struct {
  ID        int64     `gorm:"primaryKey"`
  TenantID  int64     `gorm:"not null;index:idx_post_tenant_created,priority:1"`
  UserID    int64     `gorm:"not null"`
  Title     string    `gorm:"not null"`
  Content   string
  CreatedAt time.Time `gorm:"index:idx_post_tenant_created,priority:2,sort:desc"`
  UpdatedAt time.Time
}

func (Post) TableName() string { return "post" }
```

```sql
-- migrations/00X_post.sql
CREATE TABLE post (
  id         BIGSERIAL PRIMARY KEY,
  tenant_id  BIGINT NOT NULL REFERENCES tenant(id),
  user_id    BIGINT NOT NULL REFERENCES "user"(id),
  title      TEXT NOT NULL,
  content    TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- tenant_id leads every composite index: it is in the predicate of every query
CREATE INDEX idx_post_tenant_created ON post (tenant_id, created_at DESC);
CREATE INDEX idx_post_tenant_user    ON post (tenant_id, user_id);
```

Migrations stay in goose, as SQL. GORM's `AutoMigrate` is not used here — it
cannot express policies, partial indexes, or `FORCE ROW LEVEL SECURITY`, and it
does not version anything.

### Policy

One policy, both directions, on every tenant-scoped table:

```sql
ALTER TABLE post ENABLE ROW LEVEL SECURITY;
ALTER TABLE post FORCE  ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON post
  USING      (tenant_id = current_setting('app.tenant_id', true)::BIGINT)
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::BIGINT);
```

The second argument to `current_setting` is `missing_ok`. Without it, an unset
variable raises `unrecognized configuration parameter` and the query errors.
With it, an unset variable returns NULL, `tenant_id = NULL` is NULL, and the
policy matches nothing.

**Unset means zero rows, never all rows.** That is the behaviour you want: a
code path that forgets to set the tenant returns empty results rather than
leaking. Write a test that asserts it.

### Setting the tenant

The tenant must be set on the same connection, inside the same transaction, as
the queries it governs. Every tenant-scoped request therefore runs in a
transaction, and the tenant is set as its first statement.

```go
// internal/platform/db/tenant.go
package db

import (
  "context"
  "errors"

  "gorm.io/gorm"
)

var ErrNoTenant = errors.New("no tenant in context")

// WithTenant runs fn inside a transaction scoped to tenantID. It is the ONLY
// way tenant-scoped queries reach the database. Using db.Get() directly
// defeats isolation entirely -- see § The four silent failures.
func WithTenant(ctx context.Context, tenantID int64, fn func(tx *gorm.DB) error) error {
  if tenantID == 0 {
    return ErrNoTenant
  }

  return gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
    if isPostgres {
      // set_config(name, value, is_local=true) == SET LOCAL, but accepts a
      // bind parameter. SET LOCAL does not, and interpolating invites
      // injection.
      if err := tx.Exec(
        `SELECT set_config('app.tenant_id', ?::text, true)`, tenantID,
      ).Error; err != nil {
        return err
      }
    } else {
      // SQLite: no RLS. The callbacks below read this and add the predicate.
      tx = tx.Set("app:tenant_id", tenantID)
    }
    return fn(tx)
  })
}
```

Middleware resolves the tenant into the request context and **does not touch the
database**:

```go
// internal/middleware/tenant.go
func TenantMiddleware() gin.HandlerFunc {
  return func(c *gin.Context) {
    session, err := auth.GetSession(getToken(c))
    if err != nil {
      c.AbortWithStatusJSON(401, gin.H{"error": errors.CodeInvalidSession})
      return
    }
    c.Set("tenant_id", session.TenantID)
    c.Set("user_id", session.UserID)
    c.Next()
  }
}
```

Controllers open the scope:

```go
func ListPosts(c *gin.Context) {
  var posts []models.Post

  err := db.WithTenant(c.Request.Context(), c.GetInt64("tenant_id"),
    func(tx *gorm.DB) error {
      // No Where("tenant_id = ?") -- the policy supplies it.
      return tx.Order("created_at DESC").Limit(50).Find(&posts).Error
    })
  if err != nil {
    handleError(c, err)
    return
  }
  c.HTML(200, "posts/list.html", gin.H{"posts": posts})
}
```

### SQLite scoping

On SQLite the predicate comes from GORM callbacks rather than the database.
Register once at startup:

```go
// internal/platform/db/sqlite_tenant.go
func registerTenantCallbacks(g *gorm.DB) error {
  scope := func(tx *gorm.DB) {
    if tx.Statement.Table == "" || !isTenantScoped(tx.Statement.Table) {
      return
    }
    v, ok := tx.Get("app:tenant_id")
    if !ok {
      // Fail closed, exactly like an unset RLS variable.
      tx.AddError(ErrNoTenant)
      return
    }
    tx.Statement.AddClause(clause.Where{Exprs: []clause.Expression{
      clause.Eq{Column: clause.Column{Table: tx.Statement.Table, Name: "tenant_id"},
                Value: v},
    }})
  }

  if err := g.Callback().Query().Before("gorm:query").
    Register("tenant:query", scope); err != nil {
    return err
  }
  if err := g.Callback().Update().Before("gorm:update").
    Register("tenant:update", scope); err != nil {
    return err
  }
  if err := g.Callback().Delete().Before("gorm:delete").
    Register("tenant:delete", scope); err != nil {
    return err
  }
  // Create sets rather than filters.
  return g.Callback().Create().Before("gorm:create").
    Register("tenant:create", setTenantOnCreate)
}
```

Its limits, stated plainly:

- **`Raw` and `Exec` bypass it.** Callbacks run on GORM's query builder, not on
  SQL you wrote yourself. Every raw statement against a tenant-scoped table must
  carry its own predicate.
- **It is in-process.** A bug, a migration script, or anything reaching the file
  directly sees everything.
- **`isTenantScoped` is a list you maintain.** A new table that nobody adds to it
  is unscoped, silently. Generate the list from the models that embed a tenant
  field rather than hand-maintaining it.

This is why the recommendation is unambiguous: SQLite for development and
single-tenant installs, PostgreSQL wherever more than one tenant's data shares a
file.

### Enforcing the boundary

`db.Get()` returning a usable global handle is convenient and is the main way
isolation gets lost. Two mechanical guards:

```go
// Make the unscoped handle explicit and greppable.
func Get() *gorm.DB {
  panic("db.Get() is not tenant-scoped; use db.WithTenant or db.Unscoped()")
}

// Unscoped is for migrations, workers claiming jobs, and platform admin.
// Every call site is a deliberate decision.
func Unscoped() *gorm.DB { return gdb }
```

Then a CI check that `db.Unscoped()` appears only where it should:

```bash
! grep -rn "db.Unscoped()" internal/controllers internal/models \
  || { echo "tenant scope escaped in controllers/models"; exit 1; }
```

### Outside the request cycle

Workers, cron jobs, migrations, and admin tooling have no session to read a
tenant from. Two rules:

- Work that belongs to a tenant carries `tenant_id` on the job row and calls
  `WithTenant` with it. A job is scoped exactly like a request.
- Work that legitimately spans tenants (billing reconciliation, platform admin,
  schema migration) connects as `app_owner`, which bypasses RLS by design. Keep
  those paths few, name them explicitly, and never reach for that connection
  from request-handling code.

See [jobs.md](jobs.md#tenant-scoping-in-workers).

### Testing it

The obvious test passes on a broken configuration. If the test sets the tenant
inside a transaction but production sets it on the shared handle, the test
proves nothing about production. **Test through the same helper the application
uses.**

```go
func TestTenantIsolation(t *testing.T) {
  ctx := context.Background()

  // Tenant 1 writes.
  require.NoError(t, db.WithTenant(ctx, 1, func(tx *gorm.DB) error {
    return tx.Create(&models.Post{TenantID: 1, UserID: 1, Title: "tenant one"}).Error
  }))

  // Tenant 2 cannot see it.
  var count int64
  require.NoError(t, db.WithTenant(ctx, 2, func(tx *gorm.DB) error {
    return tx.Model(&models.Post{}).Count(&count).Error
  }))
  assert.Zero(t, count, "tenant 2 must not see tenant 1 rows")

  // Tenant 2 cannot write into tenant 1 either -- this is the WITH CHECK half,
  // and it fails on a USING-only policy.
  err := db.WithTenant(ctx, 2, func(tx *gorm.DB) error {
    return tx.Create(&models.Post{TenantID: 1, UserID: 1, Title: "forged"}).Error
  })
  assert.Error(t, err, "cross-tenant INSERT must be rejected by WITH CHECK")

  // No tenant set means no rows, not all rows.
  assert.ErrorIs(t, db.WithTenant(ctx, 0, func(tx *gorm.DB) error { return nil }),
    db.ErrNoTenant)
}
```

Run this suite as `app_user`, not as the owner. **A CI job that connects as the
owner will pass every isolation test on a completely unprotected database.**

Run it on PostgreSQL even if development uses SQLite. The two mechanisms fail
differently, and only one of them is the one you ship.

### Performance

RLS predicates are planned as ordinary quals, so they are as fast as the index
behind them — and as slow as its absence.

- `tenant_id` is the **leading column** of every composite index on a
  tenant-scoped table. A policy on `tenant_id` plus an index on `(created_at)`
  gives you a filter after the scan, not a seek.
- The policy expression is evaluated per row unless PostgreSQL can prove it
  constant. `current_setting(...)::BIGINT` is `STABLE`, so it is evaluated once
  per statement. Do not wrap it in a `VOLATILE` function, and do not put a
  subquery in a policy — `tenant_id IN (SELECT ...)` turns every query into a
  join.

### Choosing an isolation strategy

RLS is the right default on PostgreSQL. The alternatives are worth knowing so
you can rule them out deliberately.

| Strategy | Isolation | Cross-tenant queries | Migration cost | Practical ceiling |
|---|---|---|---|---|
| `tenant_id` + app filtering | Weakest — one forgotten predicate leaks | Trivial | One migration | Any |
| `tenant_id` + RLS | Strong — enforced in the planner | Needs an owner connection | One migration | Any |
| Schema per tenant | Strong | Painful (`UNION` across schemas) | N migrations per release | Low hundreds |
| Database per tenant | Strongest | Effectively impossible | N migrations, N connections | Dozens |

Schema-per-tenant is the one people reach for and regret. Every release runs
migrations N times, the catalog grows until planning slows down, connection
pooling degrades because `search_path` is session state, and any product
question that spans tenants becomes a batch job. Choose it only when a contract
or regulator demands physical separation, and then consider database-per-tenant
instead — same cost, better isolation.

### Checklist

Per table:
- [ ] `tenant_id BIGINT NOT NULL REFERENCES tenant(id)`
- [ ] `tenant_id` leads every composite index
- [ ] `ENABLE ROW LEVEL SECURITY`
- [ ] `FORCE ROW LEVEL SECURITY`
- [ ] Policy has both `USING` and `WITH CHECK`
- [ ] `current_setting('app.tenant_id', true)` — with `missing_ok`
- [ ] Listed in `isTenantScoped` if SQLite is supported

Per application:
- [ ] Runtime connects as a `NOBYPASSRLS` non-owner role
- [ ] Migrations connect as the owner, on a separate DSN
- [ ] Every tenant-scoped query goes through `WithTenant`
- [ ] `db.Unscoped()` appears only in platform code, checked in CI
- [ ] Jobs carry `tenant_id` and re-enter `WithTenant`
- [ ] Raw `Exec`/`Raw` against tenant tables carry their own predicate
- [ ] Isolation tests run on PostgreSQL, as the runtime role
- [ ] A test asserts unset tenant yields zero rows
- [ ] A test asserts cross-tenant `INSERT` is rejected

## Database Synchronization

### Production → Local Sync

**Use Cases:**
- Fresh production data for testing
- Debugging production issues locally
- Demo data for presentations

**Safety First:**
- ✅ Always backup local DB first
- ✅ Never sync local → production (one-way only)
- ✅ Anonymize PII and sensitive data
- ✅ Validate data integrity after sync

### Full Production Sync Script

```bash
#!/bin/bash
# migrations/sync-prod-full.sh
set -e

LOCAL_DB="yourapp"
PROD_HOST="your-prod-db-host.com"
PROD_PORT="5432"
PROD_USER="yourapp"
BACKUP_FILE="local_backup_$(date +%Y%m%d_%H%M%S).sql"

echo "📦 Starting production sync..."

# 1. Backup local database
echo "💾 Backing up local database..."
PGPASSWORD=yourapp pg_dump -h localhost -U yourapp -d $LOCAL_DB > $BACKUP_FILE
echo "✅ Local backup saved: $BACKUP_FILE"

# 2. Drop and recreate local database
echo "🗑️  Dropping local database..."
PGPASSWORD=yourapp psql -h localhost -U yourapp -d postgres -c "DROP DATABASE IF EXISTS $LOCAL_DB;"
PGPASSWORD=yourapp psql -h localhost -U yourapp -d postgres -c "CREATE DATABASE $LOCAL_DB;"

# 3. Create production dump
echo "📥 Creating production dump..."
PGPASSWORD=$PROD_PASS pg_dump \
  -h $PROD_HOST -p $PROD_PORT -U $PROD_USER -d $LOCAL_DB \
  --no-owner --no-privileges \
  --exclude-table-data=auth_method \
  --exclude-table-data=magic_link \
  > prod_dump.sql

# 4. Load into local database
echo "📤 Loading production data..."
PGPASSWORD=yourapp psql -h localhost -U yourapp -d $LOCAL_DB < prod_dump.sql

# 5. Anonymize sensitive data
echo "🔒 Anonymizing sensitive data..."
PGPASSWORD=yourapp psql -h localhost -U yourapp -d $LOCAL_DB << EOF
-- Anonymize user emails
UPDATE "user" SET email = 'user' || id || '@example.com' WHERE email != 'alice@example.com';

-- Clear sensitive fields
UPDATE "user" SET avatar_url = NULL WHERE avatar_url IS NOT NULL;

-- Add dev magic link
INSERT INTO magic_link (token, email, expires_at)
VALUES ('alice-permanent-token', 'alice@example.com', NOW() + INTERVAL '1 year')
ON CONFLICT (token) DO NOTHING;
EOF

# 6. Cleanup
rm prod_dump.sql

echo "✅ Production sync completed!"
echo "📁 Local backup available: $BACKUP_FILE"
echo "🔐 Dev login: alice@example.com (magic link: alice-permanent-token)"
```

### Makefile Integration

```makefile
sync-prod-full:
	chmod +x migrations/sync-prod-full.sh
	./migrations/sync-prod-full.sh

backup-local:
	PGPASSWORD=yourapp pg_dump -h localhost -U yourapp -d yourapp > "backup_$(shell date +%Y%m%d_%H%M%S).sql"
```

---

## Troubleshooting

### Common Issues

**Cannot connect to database:**
```bash
# Check if Postgres is running
docker ps  # If using Docker
pg_isready -h localhost -p 5432

# Verify DATABASE_URL
echo $DATABASE_URL

# Test connection
psql "$DATABASE_URL"
```

**Migration errors:**
```bash
# Verbose output
goose -v -dir migrations postgres "$DATABASE_URL" up

# Check migration status
goose -dir migrations postgres "$DATABASE_URL" status

# Force to specific version
goose -dir migrations postgres "$DATABASE_URL" up-to 3
```

**Slow queries:**
```sql
-- Enable query logging (PostgreSQL config)
log_statement = 'all'
log_min_duration_statement = 1000  -- Log queries > 1s

-- Find slow queries
SELECT query, calls, total_time, mean_time
FROM pg_stat_statements
ORDER BY mean_time DESC
LIMIT 10;

-- Analyze query
EXPLAIN ANALYZE SELECT * FROM post WHERE user_id = 123;
```

**Index not being used:**
```sql
-- Check if index exists
\d post  -- Shows table structure and indexes

-- Analyze query plan
EXPLAIN SELECT * FROM post WHERE user_id = 123;

-- Update statistics
ANALYZE post;

-- Rebuild index if needed
REINDEX INDEX idx_post_user_id;
```

**RLS not working (every tenant sees every row):**

Work down this list. The first two causes account for nearly all of it.

```sql
-- 1. Are you the table owner? Owners bypass RLS unless FORCE is set.
SELECT tableowner FROM pg_tables WHERE tablename = 'post';
SELECT current_user, rolsuper, rolbypassrls
  FROM pg_roles WHERE rolname = current_user;
-- If current_user owns the table, or rolbypassrls/rolsuper is true,
-- policies are skipped. Fix: ALTER TABLE post FORCE ROW LEVEL SECURITY,
-- and connect as a NOBYPASSRLS non-owner role.

-- 2. Is the tenant actually set on THIS connection, in THIS transaction?
SELECT current_setting('app.tenant_id', true);
-- NULL means it was never set, was set on a different pooled connection,
-- or was issued as SET LOCAL outside a transaction (a silent no-op).

-- 3. Is RLS enabled and forced?
SELECT relrowsecurity, relforcerowsecurity
  FROM pg_class WHERE relname = 'post';   -- want t, t

-- 4. Does the policy cover writes as well as reads?
SELECT polname, polcmd, pg_get_expr(polqual, polrelid)      AS using_expr,
                        pg_get_expr(polwithcheck, polrelid) AS check_expr
  FROM pg_policy WHERE polrelid = 'post'::regclass;
-- check_expr NULL means cross-tenant INSERT is permitted.

-- 5. See what the planner actually applied.
EXPLAIN (ANALYZE, VERBOSE) SELECT * FROM post;
-- The policy should appear as a Filter. If it does not, RLS is being bypassed.
```

**Isolation tests pass but production leaks.** The test sets the tenant inside a
transaction; production sets it on the shared `db.Get()` handle. Both look like
they set the tenant. Only one of them does. Test through the same `WithTenant`
helper the application uses, and run CI as the runtime role — not as the owner.

**Running on SQLite.** There is no RLS. Scoping comes from GORM callbacks, which
`Raw` and `Exec` bypass entirely. Verify isolation on PostgreSQL.

---

## Best Practices Summary

✅ **Always use transactions** for multi-step operations (`db.Transaction()`)
✅ **Always pass context** to DB calls (`db.WithContext(ctx)`)
✅ **Index all foreign keys** for JOIN performance
✅ **Use GORM's query builder** for type-safe queries
✅ **Validate input** before DB calls (in models)
✅ **Log DB errors** with context (request ID, user ID)
✅ **Monitor slow queries** in production
✅ **Test migrations** on copy of production data
✅ **Backup before sync** (production → local)
✅ **Use RLS** for multi-tenancy (PostgreSQL only, defense in depth)
✅ **Use GORM scopes** for reusable query patterns

---

**Next:** See [htmx.md](htmx.md) for frontend interactivity patterns
