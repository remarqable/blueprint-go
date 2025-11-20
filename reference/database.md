# Database Reference Guide

> Comprehensive database patterns for Go+Gin SaaS applications using PostgreSQL

---

## Table of Contents

- [Database Conventions](#database-conventions)
- [Connection Management](#connection-management)
- [Migrations](#migrations)
- [JSONB Usage Patterns](#jsonb-usage-patterns)
- [Full-text Search](#full-text-search)
- [Indexes and Performance](#indexes-and-performance)
- [Multi-Tenancy with Row-Level Security](#multi-tenancy-with-row-level-security)
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

### Database Handle Pattern

```go
// internal/platform/db/db.go
package db

import (
  "context"
  "time"
  "github.com/jmoiron/sqlx"
)

var gdb *sqlx.DB
const DefaultTimeout = 3 * time.Second

func SetDB(database *sqlx.DB) { gdb = database }
func Get() *sqlx.DB { return gdb }

func WithTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
  if ctx == nil { ctx = context.Background() }
  if d == 0 { d = DefaultTimeout }
  return context.WithTimeout(ctx, d)
}

func WithTx(ctx context.Context, fn func(ctx context.Context, tx *sqlx.Tx) error) error {
  tx, err := gdb.BeginTxx(ctx, nil)
  if err != nil { return err }
  if err := fn(ctx, tx); err != nil {
    _ = tx.Rollback()
    return err
  }
  return tx.Commit()
}
```

### Connection Pooling

```go
// cmd/app/main.go
database, err := sqlx.ConnectContext(ctx, "postgres", dsn)
if err != nil {
  log.Fatal().Err(err).Msg("failed to connect to database")
}

// Production settings
database.SetMaxOpenConns(25)           // Max open connections
database.SetMaxIdleConns(5)            // Max idle connections
database.SetConnMaxLifetime(5 * time.Minute)  // Connection lifetime
```

### Model Pattern

```go
// internal/models/user.go
package models

import (
  "context"
  "database/sql"
  "time"

  "yourapp/internal/platform/db"
  "yourapp/internal/platform/errors"
)

type User struct {
  ID        int64     `db:"id"`
  Email     string    `db:"email"`
  Name      string    `db:"name"`
  CreatedAt time.Time `db:"created_at"`
  UpdatedAt time.Time `db:"updated_at"`
}

func (u *User) GetByEmail(email string) error {
  ctx, cancel := db.WithTimeout(context.Background(), 0)
  defer cancel()

  err := db.Get().GetContext(ctx, u, `SELECT * FROM "user" WHERE email=$1`, email)
  if err != nil {
    if err == sql.ErrNoRows {
      return errors.New(errors.CodeNotFound, "user not found")
    }
    return errors.Wrap(err, errors.CodeDatabaseQuery, "failed to query user")
  }
  return nil
}

func (u *User) Create(ctx context.Context) error {
  ctx, cancel := db.WithTimeout(ctx, 0)
  defer cancel()

  return db.Get().QueryRowContext(ctx,
    `INSERT INTO "user" (email, name) VALUES ($1, $2)
     RETURNING id, created_at, updated_at`,
    u.Email, u.Name).Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt)
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

### Go Integration

```go
import (
  "database/sql/driver"
  "encoding/json"
)

// Custom JSONB type for sqlx
type JSONB map[string]interface{}

func (j JSONB) Value() (driver.Value, error) {
  return json.Marshal(j)
}

func (j *JSONB) Scan(value interface{}) error {
  b, ok := value.([]byte)
  if !ok {
    return errors.New("type assertion to []byte failed")
  }
  return json.Unmarshal(b, j)
}

// Model with JSONB
type Product struct {
  ID          int64     `db:"id"`
  Name        string    `db:"name"`
  Description string    `db:"description"`
  Metadata    JSONB     `db:"metadata"`
  CreatedAt   time.Time `db:"created_at"`
}
```

### Common Queries

```go
// Insert with JSONB
metadata := JSONB{
  "category": "electronics",
  "specs": map[string]interface{}{
    "color": "black",
    "weight": "1.5kg",
  },
  "in_stock": true,
}

db.Get().ExecContext(ctx,
  `INSERT INTO product (name, description, metadata) VALUES ($1, $2, $3)`,
  "Laptop", "Gaming laptop...", metadata)

// Query by JSONB field
db.Get().SelectContext(ctx, &products,
  `SELECT * FROM product WHERE metadata->>'in_stock' = 'true'`)

// Containment query (@>)
db.Get().SelectContext(ctx, &products,
  `SELECT * FROM product WHERE metadata @> '{"category": "electronics"}'`)

// Nested field
db.Get().SelectContext(ctx, &products,
  `SELECT * FROM product WHERE metadata->'specs'->>'color' = 'black'`)

// Update JSONB (merge)
db.Get().ExecContext(ctx,
  `UPDATE product SET metadata = metadata || '{"in_stock": false}' WHERE id = $1`,
  productID)
```

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

### Go Model Example

```go
func SearchProducts(ctx context.Context, query string) ([]Product, error) {
  ctx, cancel := db.WithTimeout(ctx, 0)
  defer cancel()

  var products []Product
  err := db.Get().SelectContext(ctx, &products,
    `SELECT * FROM product
     WHERE tsv @@ to_tsquery('simple', $1)
     ORDER BY ts_rank(tsv, to_tsquery('simple', $1)) DESC
     LIMIT 50`,
    query)
  return products, err
}
```

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

## Multi-Tenancy with Row-Level Security

### When to Use Multi-Tenancy

**Use for B2B SaaS:**
- Team collaboration tools
- Workspace-based applications
- Organization/company isolation required
- Shared infrastructure, isolated data

**Don't use for B2C:**
- User-owned data is sufficient (`user_id` foreign keys)
- Simpler, faster to build
- Most consumer apps don't need tenant isolation

### Architecture Overview

PostgreSQL Row-Level Security (RLS) enforces data isolation **at the database level**, ensuring tenants can only access their own data—even if your application code has bugs.

**4 Steps:**
1. Add `tenant_id` to tables
2. Enable RLS on tables
3. Create RLS policy
4. Set tenant in session (via middleware)

### Step 1: Add tenant_id to Schema

```sql
CREATE TABLE post (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL,  -- ← Multi-tenancy column
  user_id BIGINT NOT NULL REFERENCES "user"(id),
  title TEXT NOT NULL,
  content TEXT,
  published BOOLEAN DEFAULT false,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Index for tenant filtering
CREATE INDEX idx_post_tenant ON post (tenant_id);

-- Composite index for user + tenant queries
CREATE INDEX idx_post_tenant_user ON post (tenant_id, user_id);
```

### Step 2: Enable RLS

```sql
ALTER TABLE post ENABLE ROW LEVEL SECURITY;
```

### Step 3: Create RLS Policy

```sql
CREATE POLICY tenant_isolation_policy ON post
  USING (tenant_id = current_setting('app.tenant_id')::BIGINT);
```

**What this does:**
- Automatically filters ALL queries by `tenant_id = current_setting('app.tenant_id')`
- Applies to SELECT, UPDATE, DELETE (unless you create separate policies)
- No need to add `WHERE tenant_id = ?` to every query

### Step 4: Set Tenant in Middleware

```go
// internal/middleware/tenant.go
package middleware

import (
  "github.com/gin-gonic/gin"
  "yourapp/internal/platform/auth"
  "yourapp/internal/platform/db"
  "yourapp/internal/platform/errors"
)

// TenantMiddleware sets tenant_id in Gin context and database session
// IMPORTANT: Call AFTER authentication middleware
func TenantMiddleware() gin.HandlerFunc {
  return func(c *gin.Context) {
    // Get session (includes tenant_id)
    session, err := auth.GetSession(getToken(c))
    if err != nil {
      c.AbortWithStatusJSON(401, gin.H{"error": errors.CodeInvalidSession})
      return
    }

    // Set in Gin context (for application logic)
    c.Set("tenant_id", session.TenantID)
    c.Set("user_id", session.UserID)
    c.Set("user_email", session.Email)

    // Set in database session (for RLS enforcement)
    // IMPORTANT: Use SET LOCAL (not SET SESSION) so it resets after request
    ctx := c.Request.Context()
    _, err = db.Get().ExecContext(ctx,
      "SET LOCAL app.tenant_id = $1",
      session.TenantID)

    if err != nil {
      c.AbortWithStatusJSON(500, gin.H{"error": errors.CodeUnknown})
      return
    }

    c.Next()
  }
}

func getToken(c *gin.Context) string {
  token := c.GetHeader("Authorization")
  if token == "" {
    token, _ = c.Cookie("session_token")
  }
  return token
}
```

### Usage in Application Code

**Controllers:**
```go
func ListPosts(c *gin.Context) {
  tenantID := c.GetInt64("tenant_id")  // From Gin context
  ctx := c.Request.Context()

  var posts []models.Post

  // RLS automatically filters by tenant_id in database
  // No need for WHERE tenant_id = ? in query!
  err := db.Get().SelectContext(ctx, &posts,
    `SELECT * FROM post ORDER BY created_at DESC`)

  if err != nil {
    handleError(c, err)
    return
  }

  c.HTML(200, "posts/list.html", gin.H{"posts": posts})
}
```

**Models:**
```go
// Create: explicitly pass tenant_id
func (p *Post) Create(ctx context.Context, tenantID, userID int64) error {
  ctx, cancel := db.WithTimeout(ctx, 0)
  defer cancel()

  return db.Get().QueryRowContext(ctx,
    `INSERT INTO post (tenant_id, user_id, title, content, published)
     VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at, updated_at`,
    tenantID, userID, p.Title, p.Content, p.Published,
  ).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

// List: RLS filters automatically
func GetPostsByUser(ctx context.Context, userID int64) ([]Post, error) {
  ctx, cancel := db.WithTimeout(ctx, 0)
  defer cancel()

  var posts []Post
  err := db.Get().SelectContext(ctx, &posts,
    `SELECT * FROM post WHERE user_id = $1 ORDER BY created_at DESC`,
    userID)
  return posts, err
}
```

### Benefits of RLS

**Enhanced Security:**
- ✅ Data isolation enforced at database level
- ✅ Even if app code has bugs, tenants can't see each other's data
- ✅ SQL injection can't bypass tenant boundaries
- ✅ Defense in depth: app layer + database layer

**Simplified Code:**
- ✅ No need to add `WHERE tenant_id = ?` to every query
- ✅ Cleaner, more maintainable code
- ✅ Less chance of forgetting tenant filter

**Centralized Control:**
- ✅ Policies defined once in database
- ✅ Applied consistently across all queries
- ✅ Easy to audit and update

### Migration from B2C to B2B

If you start with user-owned data (B2C) and need to add multi-tenancy later:

```sql
-- 1. Add tenant_id column
ALTER TABLE post ADD COLUMN tenant_id BIGINT NOT NULL DEFAULT 1;

-- 2. Create index
CREATE INDEX idx_post_tenant ON post (tenant_id);

-- 3. Enable RLS
ALTER TABLE post ENABLE ROW LEVEL SECURITY;

-- 4. Create policy
CREATE POLICY tenant_isolation_policy ON post
  USING (tenant_id = current_setting('app.tenant_id')::BIGINT);

-- 5. Update application code to set tenant_id in session
```

### Testing Multi-Tenancy

```go
func TestPost_Create_MultiTenant(t *testing.T) {
  db.SetupTestData(t)

  db.TestTx(t, func(t *testing.T, tx *sqlx.Tx) {
    // Set tenant context
    _, err := tx.Exec("SET LOCAL app.tenant_id = 1")
    require.NoError(t, err)

    post := Post{Title: "Test post", Content: "Hello world"}
    err = post.Create(context.Background(), 1, 1)
    require.NoError(t, err)

    // Verify isolation: different tenant can't see post
    _, err = tx.Exec("SET LOCAL app.tenant_id = 2")
    require.NoError(t, err)

    var count int
    err = tx.Get(&count, `SELECT COUNT(*) FROM post`)
    require.NoError(t, err)
    assert.Equal(t, 0, count)  // Tenant 2 sees no posts
  })
}
```

### Implementation Checklist

**For each table:**
- [ ] Add `tenant_id BIGINT NOT NULL` column
- [ ] Add index: `CREATE INDEX idx_<table>_tenant ON <table> (tenant_id)`
- [ ] Enable RLS: `ALTER TABLE <table> ENABLE ROW LEVEL SECURITY`
- [ ] Create policy: `CREATE POLICY tenant_isolation_policy ON <table> USING (...)`

**For application:**
- [ ] Implement `TenantMiddleware()` (see above)
- [ ] Apply middleware AFTER authentication
- [ ] Use `SET LOCAL` (not `SET SESSION`)
- [ ] Extract `tenant_id` from Gin context in controllers
- [ ] Pass `tenant_id` to model Create/Update methods

---

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

**RLS not working:**
```bash
# Check if RLS is enabled
\d+ post  # Should show "Policies" section

# Verify policy
SELECT * FROM pg_policies WHERE tablename = 'post';

# Check session variable
SHOW app.tenant_id;  -- Should return current tenant

# Test manually
SET LOCAL app.tenant_id = 1;
SELECT * FROM post;  -- Should only show tenant 1 data
```

---

## Best Practices Summary

✅ **Always use transactions** for multi-step operations
✅ **Always pass context** to DB calls with timeouts
✅ **Index all foreign keys** for JOIN performance
✅ **Use prepared statements** (sqlx does this automatically)
✅ **Validate input** before DB calls (in models)
✅ **Log DB errors** with context (request ID, user ID)
✅ **Monitor slow queries** in production
✅ **Test migrations** on copy of production data
✅ **Backup before sync** (production → local)
✅ **Use RLS** for multi-tenancy (defense in depth)

---

**Next:** See [htmx.md](htmx.md) for frontend interactivity patterns
