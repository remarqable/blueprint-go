package db

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrNoTenant is returned when tenant-scoped work is attempted with no tenant.
var ErrNoTenant = errors.New("no tenant in context")

const tenantKey = "app:tenant_id"

// tenantScoped lists the tables the SQLite callbacks must filter. On PostgreSQL
// the policies do this and the list is unused. Generate it from the models that
// carry a TenantID rather than maintaining it by hand in a real project.
var tenantScoped = map[string]bool{
	"post": true,
}

// WithTenant runs fn inside a transaction scoped to tenantID. It is the ONLY
// way tenant-scoped queries reach the database.
func WithTenant(ctx context.Context, tenantID int64, fn func(tx *gorm.DB) error) error {
	if tenantID == 0 {
		return ErrNoTenant
	}

	return gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if isPostgres {
			// set_config(name, value, is_local=true) is SET LOCAL, but accepts
			// a bind parameter. SET LOCAL does not, and interpolating the value
			// into the statement invites injection.
			if err := tx.Exec(
				`SELECT set_config('app.tenant_id', ?::text, true)`, tenantID,
			).Error; err != nil {
				return err
			}
			return fn(tx)
		}
		// SQLite has no row-level security. The callbacks below read this.
		return fn(tx.Set(tenantKey, tenantID))
	})
}

// RegisterTenantCallbacks installs application-layer scoping for SQLite.
// Strictly weaker than RLS: Raw and Exec bypass it entirely.
func RegisterTenantCallbacks(g *gorm.DB) error {
	if err := g.Callback().Query().Before("gorm:query").
		Register("tenant:query", scopeRead); err != nil {
		return err
	}
	if err := g.Callback().Update().Before("gorm:update").
		Register("tenant:update", scopeRead); err != nil {
		return err
	}
	if err := g.Callback().Delete().Before("gorm:delete").
		Register("tenant:delete", scopeRead); err != nil {
		return err
	}
	return g.Callback().Create().Before("gorm:create").
		Register("tenant:create", scopeWrite)
}

func currentTenant(tx *gorm.DB) (int64, bool) {
	v, ok := tx.Get(tenantKey)
	if !ok {
		return 0, false
	}
	id, ok := v.(int64)
	return id, ok
}

func scopeRead(tx *gorm.DB) {
	if tx.Statement.Table == "" || !tenantScoped[tx.Statement.Table] {
		return
	}
	id, ok := currentTenant(tx)
	if !ok {
		// Fail closed, exactly like an unset RLS variable: no rows, not all rows.
		_ = tx.AddError(ErrNoTenant)
		return
	}
	tx.Statement.AddClause(clause.Where{Exprs: []clause.Expression{
		clause.Eq{
			Column: clause.Column{Table: tx.Statement.Table, Name: "tenant_id"},
			Value:  id,
		},
	}})
}

func scopeWrite(tx *gorm.DB) {
	if tx.Statement.Table == "" || !tenantScoped[tx.Statement.Table] {
		return
	}
	id, ok := currentTenant(tx)
	if !ok {
		_ = tx.AddError(ErrNoTenant)
		return
	}
	// The WITH CHECK half: a row may not be written into another tenant.
	if f := tx.Statement.Schema.LookUpField("TenantID"); f != nil {
		if v, zero := f.ValueOf(tx.Statement.Context, tx.Statement.ReflectValue); !zero {
			if got, ok := v.(int64); ok && got != id {
				_ = tx.AddError(ErrNoTenant)
			}
		}
	}
}
