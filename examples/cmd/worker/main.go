// Command worker drains the job queue. It connects as the owner role so it can
// see every tenant's jobs, and re-enters the tenant scope for each one.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"blueprintexample/internal/platform/db"
	"blueprintexample/internal/platform/jobs"
)

func main() {
	driver := envOr("DB_DRIVER", "sqlite")
	dsn := envOr("DATABASE_OWNER_URL", envOr("DATABASE_URL", "file:data/app.db?_foreign_keys=on"))

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
	db.SetOwnerDB(g)

	w := jobs.NewWorker(db.Unscoped(), db.WithTenant, []string{"default"}, 5*time.Minute, hostname())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	log.Println("worker started")
	for {
		select {
		case <-ctx.Done():
			log.Println("worker draining")
			return
		case <-ticker.C:
			if _, err := w.Reap(ctx); err != nil {
				log.Printf("reap: %v", err)
			}
			if err := w.WorkOff(ctx); err != nil {
				log.Printf("work: %v", err)
			}
		}
	}
}

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "worker"
	}
	return h
}
