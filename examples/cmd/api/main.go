// Command api serves HTTP. One of up to three binaries sharing internal/ and
// one database: see claude.md § Process Shape.
package main

import (
	"log"
	"os"

	"blueprintexample/internal/models"
	"blueprintexample/internal/platform/db"
	"blueprintexample/internal/platform/jobs"
)

func main() {
	driver := envOr("DB_DRIVER", "sqlite")
	dsn := envOr("DATABASE_URL", "file:data/app.db?_foreign_keys=on&_journal_mode=WAL")

	db.SetDriver(driver)

	g, err := db.Connect(driver, dsn)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	if driver == "sqlite" {
		if err := db.RegisterTenantCallbacks(g); err != nil {
			log.Fatalf("tenant callbacks: %v", err)
		}
	}
	db.SetDB(g)

	// The owner handle bypasses tenant isolation. Migrations, workers and
	// platform tooling only. Absent means single-role (sqlite dev).
	if ownerURL := os.Getenv("DATABASE_OWNER_URL"); ownerURL != "" {
		owner, err := db.Connect(driver, ownerURL)
		if err != nil {
			log.Fatalf("owner database: %v", err)
		}
		db.SetOwnerDB(owner)
	}

	// Budget connections across ALL processes, not per process.
	if sqlDB, err := g.DB(); err == nil {
		sqlDB.SetMaxOpenConns(20)
		sqlDB.SetMaxIdleConns(5)
		defer sqlDB.Close()
	}

	// Migrations do NOT run here: N instances would race the same upgrade on
	// boot. This example has no migration tool, so it syncs the schema once.
	if err := g.AutoMigrate(&models.Tenant{}, &models.User{}, &models.Post{}, &jobs.Job{}); err != nil {
		log.Fatalf("schema: %v", err)
	}

	log.Println("api ready (routing omitted: see patterns/mvc.md)")
}

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
