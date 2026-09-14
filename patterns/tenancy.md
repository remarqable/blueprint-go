# Multi-Tenancy

> Read only when `tenancy: shared`. With `tenancy: personal`, data belongs to
> one user: use a `user_id` foreign key and skip this document entirely.

Isolating one tenant's data from another's, and the several ways that quietly
fails to happen.

## Table of Contents

- [The four silent failures (PostgreSQL)](#the-four-silent-failures-postgresql)
- [Roles](#roles)
- [Schema](#schema)
- [Policy](#policy)
- [Setting the tenant](#setting-the-tenant)
- [SQLite scoping](#sqlite-scoping)
- [Enforcing the boundary](#enforcing-the-boundary)
- [Outside the request cycle](#outside-the-request-cycle)
- [Testing it](#testing-it)
- [Performance](#performance)
- [Choosing an isolation strategy](#choosing-an-isolation-strategy)
- [Checklist](#checklist)

---


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

## The four silent failures (PostgreSQL)

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
returns a usable handle from anywhere. With RLS configured correctly this fails
closed — the query returns zero rows rather than another tenant's — but it is
still a bug. See [Enforcing the boundary](#enforcing-the-boundary).

## Roles

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

## Schema

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

## Policy

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

## Setting the tenant

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

## SQLite scoping

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

## Enforcing the boundary

`db.Get()` returns a usable handle from anywhere, and reaching for it is how
tenant scope gets lost. The good news is that a correct RLS setup **fails
closed**: a query issued through `db.Get()` as `app_user`, outside a transaction
where `set_config` ran, matches no policy and returns **zero rows**. That is a
loud, obvious bug in development rather than a silent cross-tenant leak.

So the rule is enforced by convention plus CI, not by crippling the handle:

```go
// Get returns the shared handle. Correct for tables with no tenant_id
// (user, tenant, session) and for platform code.
//
// On a tenant-scoped table it returns zero rows, because no tenant is set.
// If a list is mysteriously empty, this is why -- use WithTenant.
func Get() *gorm.DB { return gdb }

// Unscoped is the owner connection: it bypasses RLS and sees every tenant.
// Migrations, workers claiming jobs, platform admin, billing reconciliation.
// Every call site is a deliberate decision.
func Unscoped() *gorm.DB { return gdbOwner }
```

Two checks worth having in CI:

```bash
# The owner connection must never appear in request-handling code.
! grep -rn "db.Unscoped()" internal/controllers internal/models   || { echo "owner connection used in controllers/models"; exit 1; }

# Tenant-scoped models should not query through the shared handle.
! grep -rn "db.Get()" internal/models/tenant_scoped   || { echo "tenant-scoped model bypassing WithTenant"; exit 1; }
```

**On SQLite there is no fail-closed backstop from the database**, so the
callbacks supply one: a query against a tenant-scoped table with no tenant set
returns `ErrNoTenant` rather than every row. Same symptom, raised in-process.

## Outside the request cycle

Workers, cron jobs, migrations, and admin tooling have no session to read a
tenant from. Two rules:

- Work that belongs to a tenant carries `tenant_id` on the job row and calls
  `WithTenant` with it. A job is scoped exactly like a request.
- Work that legitimately spans tenants (billing reconciliation, platform admin,
  schema migration) connects as `app_owner`, which bypasses RLS by design. Keep
  those paths few, name them explicitly, and never reach for that connection
  from request-handling code.

See [jobs.md](jobs.md#tenant-scoping-in-workers).

## Testing it

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

## Performance

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

## Choosing an isolation strategy

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

## Checklist

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
- [ ] An empty result from a tenant-scoped table is understood as a missing
      scope, not an empty table
- [ ] Jobs carry `tenant_id` and re-enter `WithTenant`
- [ ] Raw `Exec`/`Raw` against tenant tables carry their own predicate
- [ ] Isolation tests run on PostgreSQL, as the runtime role
- [ ] A test asserts unset tenant yields zero rows
- [ ] A test asserts cross-tenant `INSERT` is rejected

