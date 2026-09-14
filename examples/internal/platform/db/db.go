// Package db owns every database handle. Nothing else opens a connection.
package db

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	gdb        *gorm.DB // runtime: non-owner role, RLS applies
	gdbOwner   *gorm.DB // owner: bypasses RLS, sees every tenant
	isPostgres bool
)

// Connect opens a handle for the configured driver.
func Connect(driver, dsn string) (*gorm.DB, error) {
	cfg := &gorm.Config{
		// Callers pass context and open transactions explicitly. GORM's
		// implicit per-statement transactions would not carry a tenant scope.
		SkipDefaultTransaction: true,
		Logger:                 logger.Default.LogMode(logger.Silent),
	}

	switch driver {
	case "sqlite":
		// SQLite enforces no foreign keys by default: without this every
		// ON DELETE CASCADE in the schema silently does nothing.
		if !strings.Contains(dsn, "_foreign_keys") && !strings.Contains(dsn, ":memory:") {
			return nil, fmt.Errorf("sqlite dsn must set _foreign_keys=on")
		}
		return gorm.Open(sqlite.Open(dsn), cfg)
	case "postgres":
		return nil, fmt.Errorf("postgres driver not wired in this example; see patterns/database.md")
	default:
		return nil, fmt.Errorf("unknown driver %q", driver)
	}
}

func SetDB(g *gorm.DB)      { gdb = g }
func SetOwnerDB(g *gorm.DB) { gdbOwner = g }
func SetDriver(d string)    { isPostgres = d == "postgres" }

// Get returns the runtime handle. Correct for tables with no tenant_id.
// On a tenant-scoped table it yields zero rows, because no tenant is set.
func Get() *gorm.DB { return gdb }

// Unscoped returns the owner handle: it bypasses tenant isolation.
// Migrations, workers claiming jobs, platform admin. CI checks where it appears.
func Unscoped() *gorm.DB {
	if gdbOwner == nil {
		return gdb
	}
	return gdbOwner
}

// WithTx wraps non-tenant work in a transaction.
func WithTx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return gdb.WithContext(ctx).Transaction(fn)
}
