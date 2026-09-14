package db

import (
	"testing"

	"gorm.io/gorm"
)

// SetupTest gives each test a fresh in-memory database with the tenant
// callbacks installed, wired through the same handles the application uses.
func SetupTest(t *testing.T, migrate func(*gorm.DB) error) *gorm.DB {
	t.Helper()

	g, err := Connect("sqlite", "file::memory:?cache=shared&_foreign_keys=on")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := RegisterTenantCallbacks(g); err != nil {
		t.Fatalf("callbacks: %v", err)
	}
	if err := migrate(g); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	SetDriver("sqlite")
	SetDB(g)
	SetOwnerDB(g) // one role on SQLite; on PostgreSQL these differ

	t.Cleanup(func() {
		if sqlDB, err := g.DB(); err == nil {
			_ = sqlDB.Close()
		}
		gdb, gdbOwner = nil, nil
	})
	return g
}
