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
    err = db.Get().WithContext(ctx).Exec(
      "SET LOCAL app.tenant_id = ?",
      session.TenantID).Error

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

### Usage in Application Code (GORM)

**Controllers:**
```go
func ListPosts(c *gin.Context) {
  ctx := c.Request.Context()

  var posts []models.Post

  // RLS automatically filters by tenant_id in database
  // No need for WHERE tenant_id = ? in query!
  err := db.Get().WithContext(ctx).
    Order("created_at DESC").
    Find(&posts).Error

  if err != nil {
    handleError(c, err)
    return
  }

  c.HTML(200, "posts/list.html", gin.H{"posts": posts})
}
```

**Models:**
```go
// Post model with tenant_id
type Post struct {
  ID        int64     `gorm:"primaryKey" json:"id"`
  TenantID  int64     `gorm:"index" json:"tenant_id"`
  UserID    int64     `gorm:"index" json:"user_id"`
  Title     string    `json:"title"`
  Content   string    `json:"content"`
  Published bool      `gorm:"default:false" json:"published"`
  CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
  UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// Create: explicitly pass tenant_id
func (p *Post) Create(ctx context.Context, tenantID, userID int64) error {
  p.TenantID = tenantID
  p.UserID = userID
  return db.Get().WithContext(ctx).Create(p).Error
}

// List: RLS filters automatically (PostgreSQL) or use scope (SQLite)
func GetPostsByUser(ctx context.Context, userID int64) ([]Post, error) {
  var posts []Post
  err := db.Get().WithContext(ctx).
    Where("user_id = ?", userID).
    Order("created_at DESC").
    Find(&posts).Error
  return posts, err
}
```

> **Note:** RLS is PostgreSQL-only. For SQLite, use GORM scopes to filter by tenant_id.

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

### Testing Multi-Tenancy (GORM)

```go
func TestPost_Create_MultiTenant(t *testing.T) {
  db.SetupTestData(t)

  db.TestTx(t, func(t *testing.T, tx *gorm.DB) {
    // Set tenant context (PostgreSQL RLS)
    tx.Exec("SET LOCAL app.tenant_id = 1")

    post := Post{Title: "Test post", Content: "Hello world"}
    err := post.Create(context.Background(), 1, 1)
    require.NoError(t, err)

    // Verify isolation: different tenant can't see post
    tx.Exec("SET LOCAL app.tenant_id = 2")

    var count int64
    tx.Model(&Post{}).Count(&count)
    assert.Equal(t, int64(0), count)  // Tenant 2 sees no posts
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
