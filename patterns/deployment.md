# Deployment Reference

> Complete guide to deploying Go+Gin applications to production

---

## Table of Contents

- [Application Startup](#application-startup)
- [Environment Configuration](#environment-configuration)
- [Docker Deployment](#docker-deployment)
- [Production Checklist](#production-checklist)
- [Monitoring & Observability](#monitoring--observability)
- [Graceful Shutdown](#graceful-shutdown)
- [Common Deployment Patterns](#common-deployment-patterns)

---

## Application Startup

### master Entry Point

```go
// cmd/yourapp/main.go
package main

import (
  "context"
  "fmt"
  "os"
  "os/signal"
  "syscall"
  "time"

  "yourapp/internal/controllers"
  "yourapp/internal/platform/config"
  "yourapp/internal/platform/db"
  "yourapp/internal/platform/i18n"
  "yourapp/internal/platform/logger"

  "gorm.io/gorm"
)

const appVersion = "0.1.0"

func main() {
  // 1. Initialize logger
  logger.Init(os.Getenv("APP_ENV"))
  log := logger.Get()

  log.Info().Str("version", appVersion).Msg("starting application")

  // 2. Load configuration
  cfg, err := config.Load()
  if err != nil {
    log.Fatal().Err(err).Msg("failed to load configuration")
  }

  // 3. Connect to database
  database, err := db.Connect(cfg.Driver, cfg.DatabaseURL)
  if err != nil {
    log.Fatal().Err(err).Msg("failed to connect to database")
  }

  // Set global DB handle
  db.SetDB(database)

  // 4. Configure connection pool.
  // Budget across ALL processes, not per process -- see patterns/scale.md.
  sqlDB, err := database.DB()
  if err != nil {
    log.Fatal().Err(err).Msg("failed to reach underlying pool")
  }
  defer sqlDB.Close()
  sqlDB.SetMaxOpenConns(25)
  sqlDB.SetMaxIdleConns(5)
  sqlDB.SetConnMaxLifetime(30 * time.Minute)

  // 5. Verify database connectivity
  if err := sqlDB.PingContext(ctx); err != nil {
    log.Fatal().Err(err).Msg("database ping failed")
  }
  log.Info().Msg("database connection established")

  // 6. Preload translations
  supportedLanguages := []string{"en", "es"}
  for _, lang := range supportedLanguages {
    if err := i18n.Preload(lang); err != nil {
      log.Fatal().Err(err).Str("lang", lang).Msg("failed to load translations")
    }
  }
  log.Info().Strs("languages", supportedLanguages).Msg("translations loaded")

  // 7. Setup router
  router := controllers.SetupRouter()

  // 8. Display network addresses (dev only)
  if cfg.AppEnv == "dev" {
    displayNetworkAddresses(cfg.Port)
  }

  // 9. Start server
  addr := fmt.Sprintf("0.0.0.0:%s", cfg.Port)
  log.Info().Str("addr", addr).Msg("server listening")

  // 10. Graceful shutdown handler
  go func() {
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
    <-sigChan

    log.Info().Msg("shutdown signal received")
    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer shutdownCancel()

    if err := database.Close(); err != nil {
      log.Error().Err(err).Msg("error closing database")
    }

    log.Info().Msg("shutdown complete")
    os.Exit(0)
  }()

  // 11. Run server
  if err := router.Run(addr); err != nil {
    log.Fatal().Err(err).Msg("server failed to start")
  }
}

func displayNetworkAddresses(port string) {
  log := logger.Get()
  log.Info().Msg("server accessible at:")
  log.Info().Msgf("  - Local:   http://localhost:%s", port)

  // Show all network interfaces for mobile testing
  addrs, err := net.InterfaceAddrs()
  if err == nil {
    for _, addr := range addrs {
      if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
        if ipnet.IP.To4() != nil {
          log.Info().Msgf("  - Network: http://%s:%s", ipnet.IP.String(), port)
        }
      }
    }
  }
}
```

### Startup Sequence

1. **Initialize logger** (console for dev, JSON for prod)
2. **Load configuration** from environment
3. **Connect to database** with timeout (10s)
4. **Configure connection pool** (25 max open, 5 idle, 5min lifetime)
5. **Verify connectivity** (ping database)
6. **Set global DB handle**
7. **Preload translations** for supported languages
8. **Initialize router** with middleware (logger, recovery, i18n, CORS)
9. **Display network addresses** (dev only, for mobile testing)
10. **Start HTTP server** on 0.0.0.0
11. **Listen for shutdown signals** (SIGINT, SIGTERM)
12. **Gracefully close DB** and exit

---

## Environment Configuration

### Environment Variables

```bash
# Required
DATABASE_URL=postgres://user:pass@host:port/db?sslmode=disable

# Optional (with defaults)
APP_ENV=dev          # dev, staging, production
PORT=8000
DEV_MAGIC=1          # Enable dev magic links

# Production
APP_ENV=production
PORT=8080
DATABASE_URL=postgres://user:pass@host:port/db?sslmode=require

# Email (for magic links)
SMTP_HOST=smtp.gmail.com
SMTP_PORT=587
SMTP_FROM=noreply@yourapp.com
SMTP_PASSWORD=your-app-password

# Optional
APP_URL=https://yourapp.com
JWT_SECRET=your-secret-key-here
```

### Config File Example

```bash
# config/local.env
APP_ENV=dev
PORT=8000
DATABASE_URL=postgres://app:app@localhost:5432/app?sslmode=disable
DEV_MAGIC=1
APP_URL=http://localhost:8000
```

### Loading Config

See [Configuration](../claude.md#configuration) in master blueprint.

---

## Docker Deployment

### Dockerfile (Multi-stage Build)

```dockerfile
# Build stage
FROM golang:1.23 AS build
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -o app ./cmd/yourapp

# Runtime stage
FROM gcr.io/distroless/base-debian12
WORKDIR /

# Copy binary and assets
COPY --from=build /app/app /app
COPY --from=build /app/views /views
COPY --from=build /app/static /static
COPY --from=build /app/lang /lang

EXPOSE 8080

USER nonroot:nonroot

CMD ["/app"]
```

### docker-compose.yml (Local Development)

```yaml
version: '3.8'

services:
  db:
    image: postgres:15-alpine
    environment:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: app
      POSTGRES_DB: app
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data
      - ./migrations:/docker-entrypoint-initdb.d
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U app"]
      interval: 10s
      timeout: 5s
      retries: 5

  app:
    build: .
    ports:
      - "8000:8000"
    environment:
      APP_ENV: dev
      PORT: 8000
      DATABASE_URL: postgres://app:app@db:5432/app?sslmode=disable
    depends_on:
      db:
        condition: service_healthy
    volumes:
      - ./views:/views
      - ./static:/static
      - ./lang:/lang

volumes:
  postgres_data:
```

### Build and Run

```bash
# Development
docker-compose up

# Production build
docker build -t yourapp:latest .
docker run -p 8080:8080 \
  -e DATABASE_URL="postgres://..." \
  -e APP_ENV=production \
  yourapp:latest
```

---

## Production Checklist

### Pre-Deployment

- [ ] **Environment variables** set correctly
- [ ] **DATABASE_URL** uses SSL (`sslmode=require`)
- [ ] **Secrets** stored securely (not in code)
- [ ] **Migrations** tested on copy of production data
- [ ] **Dependencies** up to date (`go mod tidy`)
- [ ] **Tests** passing (`go test ./...`)
- [ ] **Linter** passing (`staticcheck ./...`)
- [ ] **Build** succeeds (`go build ./cmd/yourapp`)

### Security

- [ ] **HTTPS enforced** (middleware or reverse proxy)
- [ ] **Security headers** enabled (X-Frame-Options, CSP, etc.)
- [ ] **Rate limiting** on auth and API endpoints
- [ ] **CSRF protection** enabled
- [ ] **Input validation** on all endpoints
- [ ] **SQL injection** prevented (parameterized queries)
- [ ] **XSS prevention** (auto-escaping templates)
- [ ] **Secrets rotation** (database passwords, API keys)

### Database

- [ ] **Migrations** applied successfully
- [ ] **Connection pooling** configured
- [ ] **Indexes** created for common queries
- [ ] **Backups** automated (daily minimum)
- [ ] **Point-in-time recovery** enabled
- [ ] **Monitoring** set up (slow queries, connections)

### Application

- [ ] **Logging** to stdout (JSON format)
- [ ] **Health check** endpoint working (`/healthz`)
- [ ] **Metrics** endpoint (optional: `/metrics`)
- [ ] **Graceful shutdown** on SIGTERM
- [ ] **Request ID** tracking enabled
- [ ] **Error handling** doesn't leak internals

### Infrastructure

- [ ] **Reverse proxy** configured (Caddy, Nginx, or cloud LB)
- [ ] **SSL/TLS** certificates installed
- [ ] **Auto-scaling** rules (if needed)
- [ ] **CDN** for static assets (optional)
- [ ] **Log aggregation** (CloudWatch, Datadog, etc.)
- [ ] **Alerting** configured (errors, downtime)

---

## Monitoring & Observability

> This section covers the deployment-side wiring only. For correlation IDs,
> logging conventions, metric selection, cardinality limits, and what is worth
> alerting on, see [observability.md](observability.md).


### Health Check Endpoint

```go
// internal/controllers/health.go
func HealthCheck(c *gin.Context) {
  // Ping database
  ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
  defer cancel()

  dbStatus := "ok"
  sqlDB, err := db.Unscoped().DB()
  if err == nil {
    err = sqlDB.PingContext(ctx)
  }
  if err != nil {
    dbStatus = "error"
    c.JSON(503, gin.H{
      "status": "unhealthy",
      "database": dbStatus,
      "error": err.Error(),
    })
    return
  }

  c.JSON(200, gin.H{
    "status": "healthy",
    "version": os.Getenv("APP_VERSION"),
    "database": dbStatus,
    "uptime": time.Since(startTime).String(),
  })
}
```

### Metrics Endpoint (Optional)

```go
// Using prometheus
import "github.com/prometheus/client_golang/prometheus/promhttp"

router.GET("/metrics", gin.WrapH(promhttp.Handler()))
```

### Logging Best Practices

```go
// Structured logging with context
log := logger.FromContext(c)

log.Info().
  Str("user_id", userID).
  Str("action", "setting_updated").
  Str("key", setting.Key).
  Msg("setting updated successfully")

// Error logging (don't expose to user)
log.Error().
  Err(err).
  Str("user_id", userID).
  Str("query", query).
  Msg("database query failed")
```

---

## Graceful Shutdown

### Implementation

```go
func main() {
  // ... setup ...

  // Create server
  srv := &http.Server{
    Addr:    fmt.Sprintf(":%s", cfg.Port),
    Handler: router,
  }

  // Start server in goroutine
  go func() {
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
      log.Fatal().Err(err).Msg("server failed")
    }
  }()

  // Wait for interrupt signal
  quit := make(chan os.Signal, 1)
  signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
  <-quit

  log.Info().Msg("shutting down server...")

  // Graceful shutdown with timeout
  ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  defer cancel()

  // Shutdown server
  if err := srv.Shutdown(ctx); err != nil {
    log.Fatal().Err(err).Msg("server forced to shutdown")
  }

  // Close database connections
  if err := database.Close(); err != nil {
    log.Error().Err(err).Msg("error closing database")
  }

  log.Info().Msg("server exited")
}
```

### Why Graceful Shutdown?

- ✅ Finish in-flight requests (don't drop connections)
- ✅ Close database connections cleanly
- ✅ Flush logs and metrics
- ✅ Clean up resources (files, connections, etc.)
- ✅ Signal to orchestrator that shutdown is complete

---

## Common Deployment Patterns

### Cloud Platforms

**Heroku:**
```bash
# Procfile
web: ./app

# heroku.yml
build:
  docker:
    web: Dockerfile
```

**Google Cloud Run:**
```bash
gcloud run deploy yourapp \
  --image gcr.io/project-id/yourapp \
  --platform managed \
  --region us-central1 \
  --allow-unauthenticated
```

**AWS Elastic Beanstalk:**
```yaml
# .ebextensions/01_app.config
option_settings:
  aws:elasticbeanstalk:application:environment:
    PORT: 8080
    APP_ENV: production
```

**DigitalOcean App Platform:**
```yaml
# .do/app.yaml
name: yourapp
services:
  - name: web
    github:
      repo: yourorg/yourapp
      branch: main
    envs:
      - key: DATABASE_URL
        scope: RUN_TIME
        type: SECRET
```

### Reverse Proxy (Caddy)

```
# Caddyfile
yourapp.com {
  reverse_proxy localhost:8000

  # Automatic HTTPS
  # Automatic HTTP/2
  # Automatic compression
}
```

### Reverse Proxy (Nginx)

```nginx
server {
  listen 80;
  server_name yourapp.com;

  location / {
    proxy_pass http://localhost:8000;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
  }
}
```

### Systemd Service

```ini
# /etc/systemd/system/yourapp.service
[Unit]
Description=Your App
After=network.target postgresql.service

[Service]
Type=simple
User=yourapp
WorkingDirectory=/opt/yourapp
EnvironmentFile=/opt/yourapp/.env
ExecStart=/opt/yourapp/app
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

```bash
# Enable and start
sudo systemctl enable yourapp
sudo systemctl start yourapp

# View logs
sudo journalctl -u yourapp -f
```

---

## Continuous Deployment

### GitHub Actions

```yaml
# .github/workflows/deploy.yml
name: Deploy

on:
  push:
    branches: [main]

jobs:
  deploy:
    runs-on: ubuntu-latest

    steps:
      - uses: actions/checkout@v3

      - uses: actions/setup-go@v4
        with:
          go-version: '1.23'

      - name: Run tests
        run: go test ./...

      - name: Build
        run: go build -o app ./cmd/yourapp

      - name: Deploy to production
        run: |
          # Your deployment script here
          scp app user@server:/opt/yourapp/
          ssh user@server 'sudo systemctl restart yourapp'
```

---

**Next:** Back to [claude.md](../claude.md) for master blueprint
