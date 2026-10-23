# Deployment Reference

> Deploying Go+Gin applications to production: one binary under systemd behind
> Caddy, or a Docker image when other people run it

---

## Table of Contents

- [Overview](#overview)
- [Application Startup](#application-startup)
- [Environment Configuration](#environment-configuration)
- [Server Provisioning](#server-provisioning)
- [Production Setup (binary)](#production-setup-binary)
- [Deploy Script](#deploy-script)
- [Docker Deployment](#docker-deployment)
- [Production Checklist](#production-checklist)
- [Monitoring & Observability](#monitoring--observability)
- [Request Profiling (System Info)](#request-profiling-system-info)
- [Graceful Shutdown](#graceful-shutdown)
- [Common Deployment Patterns](#common-deployment-patterns)
- [Continuous Deployment](#continuous-deployment)
- [Troubleshooting](#troubleshooting)

---

## Overview

Two deployment shapes, recorded as `deploy:` in the project configuration:

- **`deploy: binary`** (default). You operate the server. One statically linked
  binary with templates and static files embedded ([embed.md](../embed.md)),
  run by systemd, behind Caddy. A deploy copies a new binary, runs migrations,
  restarts the service, and waits for the new build to answer.
- **`deploy: docker`**. *Other people* run your application (a self-hostable
  product, an on-premise install). You cannot assume their OS or init system; the
  image is the distribution artifact. See [Docker Deployment](#docker-deployment).

With `staging: true`, a second copy of the service runs on the same server at
`staging.<domain>`: see [staging.md](../staging.md).

**Binary stack:**
- **Server**: any VPS (one $12/month droplet goes a long way)
- **Process manager**: systemd (restarts, logs, boot)
- **Reverse proxy**: Caddy (automatic HTTPS, HTTP/2, the "be right back" page)
- **Database**: PostgreSQL (or SQLite for single-tenant installs)
- **Migrations**: goose, run once per deploy as the owner role

No container runtime, no orchestrator, nothing to keep in sync but one file.

---

## Application Startup

### Entry Point

```go
// cmd/api/main.go
package main

import (
  "context"
  "errors"
  "net"
  "net/http"
  "os"
  "os/signal"
  "syscall"
  "time"

  "yourapp/internal/controllers"
  "yourapp/internal/platform/buildinfo"
  "yourapp/internal/platform/config"
  "yourapp/internal/platform/db"
  "yourapp/internal/platform/i18n"
  "yourapp/internal/platform/logger"
  "yourapp/internal/platform/profile"
)

func main() {
  // 1. Initialize logger
  logger.Init(os.Getenv("APP_ENV"))
  log := logger.Get()

  log.Info().Str("version", buildinfo.Commit).Msg("starting application")

  // 2. Load configuration
  cfg, err := config.Load()
  if err != nil {
    log.Fatal().Err(err).Msg("failed to load configuration")
  }

  // 3. Connect to database and set the global handle
  db.SetDriver(cfg.Driver)
  database, err := db.Connect(cfg.Driver, cfg.DatabaseURL)
  if err != nil {
    log.Fatal().Err(err).Msg("failed to connect to database")
  }
  db.SetDB(database)

  // 4. Request profiling: counts queries and DB time per request.
  // See § Request Profiling.
  if err := profile.RegisterGORM(database); err != nil {
    log.Fatal().Err(err).Msg("failed to register profiling callbacks")
  }

  // 5. Configure connection pool.
  // Budget across ALL processes, not per process -- see patterns/scale.md.
  sqlDB, err := database.DB()
  if err != nil {
    log.Fatal().Err(err).Msg("failed to reach underlying pool")
  }
  defer sqlDB.Close()
  sqlDB.SetMaxOpenConns(20)
  sqlDB.SetMaxIdleConns(5)
  sqlDB.SetConnMaxLifetime(30 * time.Minute)

  // 6. Verify database connectivity
  pingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  defer cancel()
  if err := sqlDB.PingContext(pingCtx); err != nil {
    log.Fatal().Err(err).Msg("database ping failed")
  }
  log.Info().Msg("database connection established")

  // Migrations are NOT run here: they run once per deploy, as app_owner.
  // See § Deploy Script.

  // 7. Preload translations
  supportedLanguages := []string{"en", "es"}
  for _, lang := range supportedLanguages {
    if err := i18n.Preload(lang); err != nil {
      log.Fatal().Err(err).Str("lang", lang).Msg("failed to load translations")
    }
  }
  log.Info().Strs("languages", supportedLanguages).Msg("translations loaded")

  // 8. Setup router
  router := controllers.SetupRouter()

  // 9. Display network addresses (dev only)
  if cfg.AppEnv == "dev" {
    displayNetworkAddresses(cfg.Port)
  }

  // 10. Start server
  srv := &http.Server{
    Addr:              "0.0.0.0:" + cfg.Port,
    Handler:           router,
    ReadHeaderTimeout: 10 * time.Second,
  }
  go func() {
    if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
      log.Fatal().Err(err).Msg("server failed")
    }
  }()
  log.Info().Str("addr", srv.Addr).Msg("server listening")

  // 11. Wait for SIGINT/SIGTERM, then drain in-flight requests.
  stop := make(chan os.Signal, 1)
  signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
  <-stop

  log.Info().Msg("shutdown signal received")
  shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
  defer shutdownCancel()
  if err := srv.Shutdown(shutdownCtx); err != nil {
    log.Error().Err(err).Msg("forced shutdown")
  }
  log.Info().Msg("shutdown complete")
  // 12. The deferred sqlDB.Close() closes the pool.
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
3. **Connect to database** and set the global handle
4. **Register profiling callbacks** on GORM
5. **Configure connection pool** (20 max open, 5 idle, 30 min lifetime)
6. **Verify connectivity** (ping with a 10s timeout)
7. **Preload translations** for supported languages
8. **Initialize router** with middleware (recovery, request ID, profiling, logging, i18n)
9. **Display network addresses** (dev only, for mobile testing)
10. **Start HTTP server**
11. **Listen for shutdown signals** (SIGINT, SIGTERM) and drain
12. **Close the pool** and exit

Migrations are not in this list on purpose. N instances starting together would
race the same upgrade; they run once, at deploy.

---

## Environment Configuration

### Environment Variables

```bash
# Required
DATABASE_URL=postgres://app_user:pass@host:port/db?sslmode=disable
DATABASE_OWNER_URL=postgres://app_owner:pass@host:port/db?sslmode=disable  # migrations only

# Optional (with defaults)
APP_ENV=dev          # dev, staging, production
PORT=8000
DEV_MAGIC=1          # Enable dev magic links

# Production
APP_ENV=production
PORT=8000
DATABASE_URL=postgres://app_user:pass@host:port/db?sslmode=require

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

See [Configuration](../../claude.md#configuration) in master blueprint.

---

## Server Provisioning

A fresh Debian/Ubuntu VPS needs very little: no Go toolchain (the binary is
built on your machine or in CI), just Caddy, a firewall, and PostgreSQL if it
runs on the same box.

```bash
# Update system
apt update && apt upgrade -y

# Caddy
apt install -y debian-keyring debian-archive-keyring apt-transport-https curl
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | tee /etc/apt/sources.list.d/caddy-stable.list
apt update && apt install -y caddy

# goose, for migrations at deploy (or ship it next to the binary)
# Download the release binary for your arch from github.com/pressly/goose/releases

# Firewall: SSH, HTTP, HTTPS only. The app's port is never public.
ufw default deny incoming
ufw default allow outgoing
ufw allow ssh
ufw allow http
ufw allow https
ufw enable

# Brute-force protection for SSH, and the usual tools
apt install -y fail2ban htop ncdu jq

# A user to own and run the app -- never root
useradd --system --home /opt/yourapp --shell /usr/sbin/nologin yourapp
mkdir -p /opt/yourapp/releases && chown -R yourapp:yourapp /opt/yourapp
```

---

## Production Setup (binary)

### Layout

```
/opt/yourapp/
├── .env                      # secrets, mode 600, owned by yourapp
├── current -> releases/a1b2c3d
└── releases/
    ├── 9f8e7d6/              # previous: rollback is a symlink flip
    │   ├── yourapp
    │   ├── migrations/
    │   └── maintenance.html
    └── a1b2c3d/
        ├── yourapp
        ├── migrations/
        └── maintenance.html
```

One directory per release, named by commit. The service runs `current/yourapp`;
a deploy repoints `current`, and so does a rollback. Prune all but the last few
releases from the deploy script or a cron job.

### Systemd Service

```ini
# /etc/systemd/system/yourapp.service
[Unit]
Description=Your App
After=network.target postgresql.service

[Service]
Type=simple
User=yourapp
Group=yourapp
WorkingDirectory=/opt/yourapp
EnvironmentFile=/opt/yourapp/.env
ExecStart=/opt/yourapp/current/yourapp
Restart=on-failure
RestartSec=5s
# Longer than the in-process shutdown timeout (30s), so systemd never
# SIGKILLs a server that is still draining requests.
TimeoutStopSec=40s
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/opt/yourapp

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now yourapp
sudo systemctl status yourapp
sudo journalctl -u yourapp -f
```

Migrations are deliberately not an `ExecStartPre`: they need the owner DSN,
which the running service should not hold. The deploy script runs them.

### Caddy Reverse Proxy

```
# /etc/caddy/Caddyfile
(be_right_back) {
    handle_errors 502 503 504 {
        header Cache-Control "no-store"
        root * /etc/caddy
        rewrite * /maintenance.html
        file_server
    }
}

yourapp.com {
    encode zstd gzip
    reverse_proxy localhost:8000 {
        # While the service restarts, hold the request and retry the
        # connection instead of failing at once. Caddy retries only when it
        # could not connect, so the request never reached the app and a
        # retried POST is not a double submit.
        lb_try_duration 5s
        lb_try_interval 250ms
    }
    import be_right_back
}
```

```bash
caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

Caddy obtains and renews certificates, redirects HTTP to HTTPS, and serves
HTTP/2.

#### "Be right back" page

Every deploy restarts the service. Between the old process closing its listener
and the new one opening it, Caddy has nowhere to send requests, and without help
it answers `502 Bad Gateway`. Two layers cover that window:

1. **`lb_try_duration`** absorbs a normal restart. A Go binary binds its port in
   well under a second, so most users see a short pause, not an error.
2. **`handle_errors`** covers anything longer: a slow drain, a failed start, a
   rollback, the service stopped on purpose. Caddy catches the 502/503/504 it
   generated itself and returns a static page that does not need the app.

Errors the app returns pass through untouched: `/readyz` answering 503 still
reaches whoever asked, and the app's own error pages stay the app's.

`deploy/maintenance.html` lives in the repo, so it is reviewed and versioned
like everything else. It is **self-contained**: inline CSS, no `/static/`
files, no app routes, because none of those answer while the app is down. It
says what is happening ("We'll be right back") in the product's colours,
carries `<meta name="robots" content="noindex">`, and **comes back by itself**
by polling readiness:

```html
<script>
  (function check() {
    fetch('/readyz', { cache: 'no-store' }).then(function (r) {
      if (r.ok) location.reload(); else setTimeout(check, 3000);
    }).catch(function () { setTimeout(check, 3000); });
  })();
</script>
```

It polls `/readyz`, not `/healthz`: the page should reload when the app can
serve the page, not merely when the process is alive.

The inline script is fine here and nowhere else: Caddy serves this file, so the
app's Content-Security-Policy does not apply to it. App templates load scripts
from `/static/` only (see [Security](security.md#content-security-policy)).

Why a file on disk and not embedded like every other asset: the page exists for
the moments the binary is not running, so it cannot be served by the binary. The
deploy script copies it out of the release (`install -m 644
$REL/maintenance.html /etc/caddy/maintenance.html`) so a change ships with the
next deploy. The Caddyfile itself changes rarely.

API clients receive the same status codes (with an HTML body) and should treat
502, 503 and 504 as retryable. HTMX does not swap 5xx responses by default, so
an in-page request during a restart fails quietly rather than replacing a
fragment with the maintenance page.

**Test it once** by stopping the service: any page should show the maintenance
page with status 502, and reload itself within seconds of
`systemctl start yourapp`.

If even the short pause is unacceptable, the next step is systemd socket
activation, which keeps the listening socket open across restarts so
connections wait in the kernel backlog instead of being refused. Most products
never need it.

### File Permissions

```bash
sudo chown -R yourapp:yourapp /opt/yourapp
sudo chmod 600 /opt/yourapp/.env
```

The service user can read its release and write only where `ReadWritePaths`
allows (a SQLite file or an uploads directory, if any).

---

## Deploy Script

One command: build, ship, migrate, switch, restart, and refuse to call it done
until the new commit answers `/readyz`.

### Version from the build

The deploy gate and the admin Info box both need to know which commit is
running. Stamp it into the binary at build time:

```go
// internal/platform/buildinfo/buildinfo.go
package buildinfo

import (
  "os"
  "time"
)

// Commit is set at build time:
//   go build -ldflags "-X yourapp/internal/platform/buildinfo.Commit=$(git rev-parse --short HEAD)"
var Commit = "dev"

// Deployed is when this binary was installed: the executable's mtime.
func Deployed() time.Time {
  exe, err := os.Executable()
  if err != nil {
    return time.Time{}
  }
  fi, err := os.Stat(exe)
  if err != nil {
    return time.Time{}
  }
  return fi.ModTime()
}
```

`-ldflags` rather than `runtime/debug.ReadBuildInfo`: the VCS stamp needs the
`.git` directory at build time, which Docker builds usually lack, and the
deploy script already knows the commit.

### scripts/deploy.sh

```bash
#!/bin/bash
# scripts/deploy.sh -- build, ship, migrate, restart, verify; roll back on failure
set -euo pipefail

REMOTE="deploy@yourapp.com"
APP="yourapp"
BASE="/opt/yourapp"
READY_URL="https://yourapp.com/readyz"

cd "$(git rev-parse --show-toplevel)"
if [ -n "$(git status --porcelain)" ]; then
  echo "Uncommitted changes. Commit first."
  git status --short
  exit 1
fi

SHA=$(git rev-parse --short HEAD)
REL="$BASE/releases/$SHA"

if ssh "$REMOTE" "[ \"\$(readlink $BASE/current)\" = $REL ]"; then
  echo "Already deployed ($SHA)"
  exit 0
fi

go test ./...

# Static binary for the server. With database: sqlite, use a pure-Go driver
# or build with CGO for the target instead.
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X $APP/internal/platform/buildinfo.Commit=$SHA" \
  -o "bin/$APP-linux" ./cmd/api

ssh "$REMOTE" "mkdir -p $REL"
scp -q "bin/$APP-linux" "$REMOTE:$REL/$APP"
rsync -a --delete migrations/ "$REMOTE:$REL/migrations/"
scp -q deploy/maintenance.html "$REMOTE:$REL/maintenance.html"
echo "Shipped $SHA"

# Migrations: once, as the owner, BEFORE the restart. The old binary keeps
# serving until then, so every migration must be backward compatible with it
# (add columns and tables first; drop them a release later).
ssh "$REMOTE" "set -a; . $BASE/.env; set +a; \
  goose -dir $REL/migrations postgres \"\$DATABASE_OWNER_URL\" up"
echo "Migrated"

PREV=$(ssh "$REMOTE" "readlink $BASE/current || true")

# The page Caddy shows if the restart takes longer than lb_try_duration.
ssh "$REMOTE" "sudo install -m 644 $REL/maintenance.html /etc/caddy/maintenance.html"

ssh "$REMOTE" "ln -sfn $REL $BASE/current && sudo systemctl restart $APP"
echo "Restarted"

# Health gate: not just "something answers", but "the new commit answers".
for _ in $(seq 1 30); do
  BODY=$(curl -fsS --max-time 2 "$READY_URL" 2>/dev/null || true)
  if grep -q "\"version\":\"$SHA\"" <<<"$BODY"; then
    echo "Deployed $SHA"
    exit 0
  fi
  sleep 1
done

echo "Health check failed for $SHA"
ssh "$REMOTE" "journalctl -u $APP -n 30 --no-pager"
if [ -n "$PREV" ]; then
  echo "Rolling back to $PREV"
  ssh "$REMOTE" "ln -sfn $PREV $BASE/current && sudo systemctl restart $APP"
fi
exit 1
```

What the gate relies on:

- `/readyz` returns `"version"` (see [Health Check Endpoint](#health-check-endpoint)).
  Checking the version, not just the status, catches the case where the restart
  silently failed and the **old** process is still answering.
- During the restart Caddy answers 502 with the maintenance page; `curl -f`
  treats that as a failure and the loop keeps waiting.
- A rollback restores the binary, **not the schema**. That is the second reason
  migrations must be backward compatible: the previous release has to run
  against the new schema.

The deploy user needs passwordless sudo for exactly two commands
(`systemctl restart yourapp` and `install ... /etc/caddy/maintenance.html`),
via a file in `/etc/sudoers.d/`.

```bash
chmod +x scripts/deploy.sh
make deploy   # or ./scripts/deploy.sh
```

---

## Docker Deployment

Use when `deploy: docker`. The goal is that a stranger runs
`docker compose up` and gets a working installation, with no Go toolchain,
goose, or systemd knowledge required.

### Principles

- **One image, one volume.** Everything mutable the app writes (uploads,
  wizard-written configuration, a SQLite file if `database: sqlite`) lives
  under one mounted volume (`/data`). Upgrading is
  `docker compose pull && docker compose up -d`; the volume carries the
  installation across image versions.
- **Migrations run once, before the app starts**, as a one-shot service the
  app depends on. Not inside application startup: the moment someone scales to
  two replicas, they race.
- **PostgreSQL is a service in the compose file**, or an external
  `DATABASE_URL`; either way the app container never owns the schema.
- **TLS is out of scope for the container.** Self-hosters put their own reverse
  proxy (Caddy, nginx, Traefik) in front, and the "be right back" page is
  theirs to configure. Document that; don't bundle it.

### Dockerfile (Multi-stage Build)

```dockerfile
# Build stage
FROM golang:1.23 AS build
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG COMMIT=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
      -ldflags "-s -w -X yourapp/internal/platform/buildinfo.Commit=${COMMIT}" \
      -o /out/app ./cmd/api
# goose for the migrate service; pin the version in your project
RUN CGO_ENABLED=0 go install github.com/pressly/goose/v3/cmd/goose@latest

# Runtime stage: templates, static files and translations are embedded
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/app /app
COPY --from=build /go/bin/goose /goose
COPY --from=build /app/migrations /migrations

EXPOSE 8000
USER nonroot:nonroot
CMD ["/app"]
```

### docker-compose.yml

```yaml
services:
  db:
    image: postgres:16
    environment:
      POSTGRES_USER: app_owner
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env}
      POSTGRES_DB: app
    volumes:
      - pg_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U app_owner -d app"]
      interval: 10s
      timeout: 5s
      retries: 5

  # One-shot: applies migrations as the owner, then exits.
  migrate:
    image: yourorg/yourapp:latest
    command: ["/goose", "-dir", "/migrations", "up"]
    environment:
      GOOSE_DRIVER: postgres
      GOOSE_DBSTRING: ${DATABASE_OWNER_URL:?set DATABASE_OWNER_URL in .env}
    depends_on:
      db:
        condition: service_healthy

  app:
    image: yourorg/yourapp:latest
    build: .
    ports:
      - "8000:8000"
    volumes:
      - app_data:/data
    environment:
      APP_ENV: production
      PORT: 8000
      DATABASE_URL: ${DATABASE_URL:?set DATABASE_URL in .env}
    depends_on:
      migrate:
        condition: service_completed_successfully

volumes:
  pg_data:
  app_data:
```

Do not mount `migrations/` into `/docker-entrypoint-initdb.d`. Postgres would
run each goose file as plain SQL, `-- +goose Down` section included, and goose
would have no record of having applied it.

With `jobs: true` the worker is another service on the same image with a
different command (`/app worker` or a second binary), so there is never a
second artifact to build or version. See [jobs.md](../jobs.md).

### Runtime-written configuration

A self-hosted product with a first-run web wizard cannot get all its config
from the environment, because the wizard runs *after* boot. The pattern:

1. The image boots with safe defaults (no email, sign-up closed).
2. The wizard writes its answers to a file on the volume (`/data/config.env`)
   and to settings rows in the database.
3. `config.Load` layers: defaults, then `/data/config.env`, then real
   environment variables. The environment always wins, so operators can
   override anything.
4. Secrets the wizard generates (a session key the operator did not set) are
   written to the volume once and reused. Never regenerate on restart, or every
   upgrade logs out every user.

### Build and Run

```bash
docker build --build-arg COMMIT=$(git rev-parse --short HEAD) -t yourorg/yourapp:latest .
docker compose up -d
```

---

## Production Checklist

### Pre-Deployment

- [ ] **Environment variables** set correctly
- [ ] **DATABASE_URL** uses SSL (`sslmode=require`) unless on localhost
- [ ] **Secrets** stored securely (not in code), `.env` mode 600
- [ ] **Migrations** tested on copy of production data, and backward compatible
      with the release still running
- [ ] **Dependencies** up to date (`go mod tidy`)
- [ ] **Tests** passing (`go test ./...`)
- [ ] **Linter** passing (`staticcheck ./...`)
- [ ] **Build** succeeds with the commit stamped in (`buildinfo.Commit`)

### Security

- [ ] **HTTPS enforced** (Caddy)
- [ ] **Security headers** enabled (X-Frame-Options, CSP, etc.)
- [ ] **Rate limiting** on auth and API endpoints
- [ ] **CSRF protection** enabled
- [ ] **Input validation** on all endpoints
- [ ] **SQL injection** prevented (parameterized queries)
- [ ] **XSS prevention** (auto-escaping templates)
- [ ] **Secrets rotation** (database passwords, API keys)
- [ ] **Diagnostics for admins only**: `Server-Timing` and the Info box never
      reach other users

### Database

- [ ] **Migrations** run by the deploy, as the owner
- [ ] **Connection pooling** configured
- [ ] **Indexes** created for common queries
- [ ] **Backups** automated (daily minimum) and a restore tested
- [ ] **Point-in-time recovery** enabled
- [ ] **Monitoring** set up (slow queries, connections)

### Application

- [ ] **Logging** to stdout (JSON format)
- [ ] **Health checks** working: `/healthz` (no dependencies) and `/readyz`
      (with `version`)
- [ ] **Metrics** endpoint (optional: `/metrics`)
- [ ] **Graceful shutdown** on SIGTERM, shorter than `TimeoutStopSec`
- [ ] **Request ID** tracking enabled
- [ ] **Error handling** doesn't leak internals
- [ ] **"Be right back" page** installed (Caddy `handle_errors`), tested by
      stopping the service
- [ ] **Request profiling** on: admin Info link, `Server-Timing` for admins only
- [ ] **Static URLs** written with `asset` and cached a year
      ([performance.md](performance.md#static-files))

### Infrastructure

- [ ] **Reverse proxy** configured (Caddy, Nginx, or cloud LB), compressing
      responses (Caddy: `encode zstd gzip`)
- [ ] **SSL/TLS** certificates installed
- [ ] **Firewall**: only 22, 80, 443 open
- [ ] **Service user** is not root
- [ ] **Auto-scaling** rules (if needed)
- [ ] **CDN** for static assets (optional)
- [ ] **Log aggregation** (CloudWatch, Datadog, etc.)
- [ ] **Alerting** configured (errors, downtime)

### Backups

```bash
#!/bin/bash
# /etc/cron.daily/backup-yourapp
set -euo pipefail
STAMP=$(date +%Y%m%d)

# PostgreSQL: a consistent dump of a live database
pg_dump --format=custom "$DATABASE_OWNER_URL" > /backups/yourapp/app-$STAMP.dump

# SQLite: never cp a live file (WAL pages may not be in it yet)
# sqlite3 /opt/yourapp/data/app.db ".backup /backups/yourapp/app-$STAMP.db"

find /backups/yourapp -mtime +7 -delete   # keep 7 days locally; ship copies off the box
```

A backup nobody has restored is a hope, not a backup. Restore one into a
scratch database once, and again after major schema changes.

---

## Monitoring & Observability

> This section covers the deployment-side wiring only. For correlation IDs,
> logging conventions, metric selection, cardinality limits, and what is worth
> alerting on, see [observability.md](observability.md).


### Health Check Endpoint

Two endpoints, different jobs. **Liveness must not touch the database** — if it
does, one brief database blip fails liveness on every instance at once, the
orchestrator restarts them all, and a recoverable incident becomes a full
outage with a cold cache.

```go
// internal/controllers/health.go

// Liveness: is this process wedged? No dependencies, ever.
func Healthz(c *gin.Context) {
  c.JSON(200, gin.H{"status": "ok", "version": buildinfo.Commit})
}

// Readiness: can it serve requests? Checks dependencies.
func Readyz(c *gin.Context) {
  ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
  defer cancel()

  sqlDB, err := db.Get().DB()
  if err == nil {
    err = sqlDB.PingContext(ctx)
  }
  if err != nil {
    c.JSON(503, gin.H{"status": "degraded", "database": err.Error()})
    return
  }
  c.JSON(200, gin.H{
    "status":  "ok",
    "version": buildinfo.Commit, // the deploy script's gate checks this
    "uptime":  time.Since(startTime).String(),
  })
}
```

Point the orchestrator's liveness probe at `/healthz` and its readiness probe
at `/readyz`. Load balancers, the deploy script, and the maintenance page use
`/readyz`.

→ Full rationale, plus the equivalent for workers:
[observability.md](observability.md#health-checks)


### Metrics Endpoint (Optional)

```go
// Using prometheus
import "github.com/prometheus/client_golang/prometheus/promhttp"

router.GET("/metrics", gin.WrapH(promhttp.Handler()))
```

Bind `/metrics` to an internal listener or block it in Caddy
(`respond /metrics 404`); it is not for the public.

### Logging Best Practices

```go
// Structured logging with context
log := obs.From(c.Request.Context())

log.Info().
  Str("event", "setting.updated").
  Int64("user_id", userID).
  Str("key", setting.Key).
  Msg("")

// Error logging (don't expose to user)
log.Error().
  Err(err).
  Str("event", "settings.load_failed").
  Int64("user_id", userID).
  Msg("")
```

---

## Request Profiling (System Info)

"The admin is slow" is a guess until a page says how slow and why. Measure
every request, show platform admins the numbers in a small **Info** box, and
turn the link red when a page is slow, so a slow page is caught while it is
happening rather than reconstructed from a complaint.

Three parts: a per-request `Stats` in the request context, a GORM callback that
adds each statement's time to it, and a wrapper around the HTML renderer that
times template execution and hands the numbers to the template, for admins
only.

### What is measured

```go
// internal/platform/profile/profile.go
package profile

import (
  "context"
  "net/http"
  "sync/atomic"
  "time"

  "github.com/gin-gonic/gin"

  "yourapp/internal/platform/obs"
)

// Stats is one request's timing. The counters are atomic because a handler
// may query from several goroutines (errgroup) under the same context.
type Stats struct {
  Start     time.Time
  RequestID string
  User      string
  Reveal    bool // set by Reveal(); nothing is shown or sent unless true

  queries     atomic.Int64
  dbNanos     atomic.Int64
  renderNanos atomic.Int64
}

type ctxKey struct{}

// From returns the request's Stats, or nil outside a profiled request (jobs,
// CLI, or profiling switched off) unless WithStats started one.
func From(ctx context.Context) *Stats {
  s, _ := ctx.Value(ctxKey{}).(*Stats)
  return s
}

func (s *Stats) Queries() int64        { return s.queries.Load() }
func (s *Stats) DB() time.Duration     { return time.Duration(s.dbNanos.Load()) }
func (s *Stats) Render() time.Duration { return time.Duration(s.renderNanos.Load()) }

// Middleware attaches Stats to the request. Install it right after RequestID.
func Middleware() gin.HandlerFunc {
  return func(c *gin.Context) {
    s := &Stats{Start: time.Now()}
    if t, ok := c.Get("trace"); ok {
      s.RequestID = t.(obs.Trace).RequestID
    }
    c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxKey{}, s))
    c.Writer = &writer{ResponseWriter: c.Writer, stats: s}
    c.Next()
  }
}

// WithStats counts queries outside a request: a test measuring a model
// function, or a job measuring itself (performance.md).
func WithStats(ctx context.Context) (context.Context, *Stats) {
  s := &Stats{Start: time.Now()}
  return context.WithValue(ctx, ctxKey{}, s), s
}

// Reveal marks the request as one whose numbers may be shown. Mount it ONLY
// after the platform-admin check.
func Reveal() gin.HandlerFunc {
  return func(c *gin.Context) {
    if s := From(c.Request.Context()); s != nil {
      s.Reveal = true
      s.User = c.GetString("user_email")
    }
    c.Next()
  }
}

// writer carries the Stats to the renderer, which only sees the ResponseWriter.
type writer struct {
  gin.ResponseWriter
  stats *Stats
}

// Unwrap lets code looking for another wrapper (the CSRF token's, security.md)
// see past this one, and lets statsFrom find this one under later wrappers.
func (w *writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// statsFrom finds the profiling writer however many wrappers sit above it.
func statsFrom(w http.ResponseWriter) *Stats {
  for {
    switch v := w.(type) {
    case *writer:
      return v.stats
    case interface{ Unwrap() http.ResponseWriter }:
      w = v.Unwrap()
    default:
      return nil
    }
  }
}
```

```go
// internal/platform/profile/gorm.go
package profile

import (
  "errors"
  "time"

  "gorm.io/gorm"
)

const startKey = "profile:start"

// RegisterGORM times every statement GORM issues under a profiled context.
// Outside one, each callback is a single context lookup.
func RegisterGORM(g *gorm.DB) error {
  start := func(tx *gorm.DB) {
    if From(tx.Statement.Context) != nil {
      tx.InstanceSet(startKey, time.Now())
    }
  }
  done := func(tx *gorm.DB) {
    s := From(tx.Statement.Context)
    if s == nil || tx.DryRun {         // a subquery is built by a dry run: not a query
      return
    }
    if t, ok := tx.InstanceGet(startKey); ok {
      s.queries.Add(1)
      s.dbNanos.Add(int64(time.Since(t.(time.Time))))
    }
  }

  cb := g.Callback()
  return errors.Join(
    cb.Create().Before("gorm:create").Register("profile:create_start", start),
    cb.Create().After("gorm:create").Register("profile:create_done", done),
    cb.Query().Before("gorm:query").Register("profile:query_start", start),
    cb.Query().After("gorm:query").Register("profile:query_done", done),
    cb.Update().Before("gorm:update").Register("profile:update_start", start),
    cb.Update().After("gorm:update").Register("profile:update_done", done),
    cb.Delete().Before("gorm:delete").Register("profile:delete_start", start),
    cb.Delete().After("gorm:delete").Register("profile:delete_done", done),
    cb.Row().Before("gorm:row").Register("profile:row_start", start),
    cb.Row().After("gorm:row").Register("profile:row_done", done),
    cb.Raw().Before("gorm:raw").Register("profile:raw_start", start),
    cb.Raw().After("gorm:raw").Register("profile:raw_done", done),
  )
}
```

Two consequences worth knowing:

- **Only queries that carry the request context are counted.** A query issued
  without `WithContext(ctx)` is invisible here, which is one more reason the
  blueprint requires passing context to every DB call. `db.WithTenant` passes
  it, so tenant-scoped work is covered, including its `set_config` statement.
- **It counts statements, not round trips.** `BEGIN` and `COMMIT` do not go
  through GORM callbacks. That is enough to find an N+1; if you ever need every
  round trip, pgx's `QueryTracer` sits below GORM.

```go
// internal/platform/profile/render.go
package profile

import (
  "fmt"
  "net/http"
  "time"

  "github.com/gin-gonic/gin"
  "github.com/gin-gonic/gin/render"
)

// HTML wraps the engine's HTML renderer: it times template execution and, for
// revealed requests, sets Server-Timing and gives the template a SystemInfo.
type HTML struct{ render.HTMLRender }

func (h HTML) Instance(name string, data any) render.Render {
  return timed{inner: h.HTMLRender.Instance(name, data), data: data}
}

type timed struct {
  inner render.Render
  data  any
}

func (t timed) WriteContentType(w http.ResponseWriter) { t.inner.WriteContentType(w) }

func (t timed) Render(w http.ResponseWriter) error {
  s := statsFrom(w)
  if s == nil {
    return t.inner.Render(w)
  }
  if s.Reveal {
    // Headers are not sent until the template writes, so this still lands.
    w.Header().Set("Server-Timing", fmt.Sprintf(`app;dur=%.1f, db;dur=%.1f;desc="%d queries"`,
      ms(time.Since(s.Start)), ms(s.DB()), s.Queries()))
    if h, ok := t.data.(gin.H); ok {
      h["SystemInfo"] = s.Info()
    }
  }
  start := time.Now()
  err := t.inner.Render(w)
  s.renderNanos.Add(int64(time.Since(start)))
  return err
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
```

```go
// internal/platform/profile/info.go
package profile

import (
  "os"
  "runtime"
  "time"

  "yourapp/internal/platform/buildinfo"
  "yourapp/internal/platform/db"
)

// Info is what the admin Info box shows.
type Info struct {
  RequestID  string
  User       string
  Elapsed    time.Duration // server time before rendering began
  Queries    int64
  DB         time.Duration
  PoolInUse  int
  PoolWaits  int64 // cumulative waits for a free connection
  Host       string
  PID        int
  GoVersion  string
  Goroutines int
  Commit     string
  Deployed   time.Time
}

func (s *Stats) Info() Info {
  host, _ := os.Hostname()
  i := Info{
    RequestID: s.RequestID, User: s.User,
    Elapsed: time.Since(s.Start), Queries: s.Queries(), DB: s.DB(),
    Host: host, PID: os.Getpid(),
    GoVersion: runtime.Version(), Goroutines: runtime.NumGoroutine(),
    Commit: buildinfo.Commit, Deployed: buildinfo.Deployed(),
  }
  if sqlDB, err := db.Get().DB(); err == nil {
    st := sqlDB.Stats()
    i.PoolInUse, i.PoolWaits = st.InUse, st.WaitCount
  }
  return i
}
```

Wiring:

```go
// internal/controllers/router.go
router := gin.New()
router.Use(gin.Recovery())
router.Use(middleware.RequestID())     // first: everything after can use the ID
router.Use(profile.Middleware())
router.Use(logger.Middleware())        // logs the request with the profile fields

renderer, err := assets.NewRenderer()   // one template set per page (embed.md)
if err != nil {
  panic(err) // a template that does not parse is a build error
}
router.HTMLRender = profile.HTML{HTMLRender: renderer} // times every render

admin := router.Group("/admin")
admin.Use(middleware.Auth(), middleware.RequirePlatformAdmin(), profile.Reveal())
```

- **Request ID** on every response (`X-Request-ID`, from the
  [correlation middleware](observability.md#correlation)) and in the box, so a
  slow page can be found in the logs. It is harmless, so everyone gets it.
- **Server-Timing** (`app;dur=…, db;dur=…;desc="N queries"`) shows in the
  browser's network tab. **Admins only**: query counts, timings, host names and
  pool state tell an outsider how the app is built and where it is slow.
- **`SystemInfo`** is added to the template data **only when `Reveal` is set**.
  For everyone else the key does not exist and the footer block renders
  nothing.
- **Platform admins, not tenant admins.** `RequirePlatformAdmin` checks the
  operator flag on the user (`is_admin`), not a role inside a tenant. A tenant
  admin seeing host names and pool state of shared infrastructure is a leak.

Handlers build a fresh `gin.H` per request already; the renderer adds a key to
it, so never pass a package-level map to `c.HTML`.

**Cost when nobody is looking:** one small allocation and a `context.WithValue`
per request, and two `time.Now` calls plus two atomic adds per statement, all
dwarfed by the network round trip to the database. To switch it off entirely,
do not install `profile.Middleware()` (for example behind a `PROFILE=off`
setting); the GORM callbacks then cost one context lookup per statement.

### Logs and metrics

The request log line already exists ("one log line per unit of work"); profile
data goes on it, so every request, admin or not, can be looked up afterwards by
its request ID:

```go
// internal/platform/logger/middleware.go
func Middleware() gin.HandlerFunc {
  return func(c *gin.Context) {
    start := time.Now()
    c.Next()

    took := time.Since(start)
    log := obs.From(c.Request.Context()) // carries trace_id = the request ID
    ev := log.Info()
    if took > time.Second {
      ev = log.Warn() // the same threshold that turns the Info link red
    }
    ev = ev.Str("event", "http.request").
      Str("route", c.FullPath()).
      Int("status", c.Writer.Status()).
      Dur("took", took)
    if s := profile.From(c.Request.Context()); s != nil {
      ev = ev.Int64("db_queries", s.Queries()).Dur("db", s.DB()).Dur("render", s.Render())
    }
    ev.Msg("")
  }
}
```

The middleware reads `c.Request` after `c.Next()`, so it sees the context the
profiling middleware installed further down the chain.

If you want queries-per-request in Prometheus, use a histogram labelled by
**route template** (`c.FullPath()`) only. Never the request ID, user, or tenant;
see [Cardinality](observability.md#cardinality). The request ID belongs in logs.

### The Info link

In the admin layout's footer: `yourapp <commit> (<deployed>) · Info · 0.31s`.
**Info** opens a native `<dialog>` with the table above plus the **page load**
measured in the browser (Navigation Timing: `loadEventEnd - startTime`). The
link **turns red when the page took over a second**, in the browser or on the
server. A `<dialog>` opened with `showModal()` traps focus, closes on Escape and
restores focus on close by itself, so this admin-only box needs no Alpine
component and no framework markup.

```html
{{/* internal/assets/views/layouts/admin.html, at the end of <body> */}}
{{ with .SystemInfo }}
<footer class="py-3 text-sm text-gray-500">
  yourapp {{ .Commit }} ({{ .Deployed.Format "2006-01-02 15:04" }}) ·
  <button type="button" id="system-info-link" aria-haspopup="dialog" aria-controls="system-info"
          data-render-ms="{{ .Elapsed.Milliseconds }}"
          class="underline data-slow:text-red-600">{{ t $.lang "sysinfo.info" }}</button> ·
  <span data-page-load></span>
</footer>
<dialog id="system-info" aria-labelledby="system-info-title"
        class="card w-full max-w-md backdrop:bg-black/50">
  <h2 id="system-info-title" class="text-base font-semibold">{{ t $.lang "sysinfo.title" }}</h2>
  <table class="mt-3 w-full text-sm">
    <tr><th class="pe-4 text-start">{{ t $.lang "sysinfo.user" }}</th><td>{{ .User }}</td></tr>
    <tr><th class="pe-4 text-start">{{ t $.lang "sysinfo.request_id" }}</th><td><code>{{ .RequestID }}</code></td></tr>
    <tr><th class="pe-4 text-start">{{ t $.lang "sysinfo.server" }}</th><td>{{ .Elapsed }}</td></tr>
    <tr><th class="pe-4 text-start">{{ t $.lang "sysinfo.database" }}</th><td>{{ t $.lang "sysinfo.queries" "n" (print .Queries) }}, {{ .DB }}</td></tr>
    <tr><th class="pe-4 text-start">{{ t $.lang "sysinfo.pool" }}</th><td>{{ t $.lang "sysinfo.pool_value" "in_use" (print .PoolInUse) "waits" (print .PoolWaits) }}</td></tr>
    <tr><th class="pe-4 text-start">{{ t $.lang "sysinfo.page_load" }}</th><td data-page-load></td></tr>
    <tr><th class="pe-4 text-start">{{ t $.lang "sysinfo.host" }}</th><td>{{ .Host }} / {{ .PID }}</td></tr>
    <tr><th class="pe-4 text-start">{{ t $.lang "sysinfo.runtime" }}</th><td>{{ .GoVersion }} / {{ .Goroutines }}</td></tr>
  </table>
  <form method="dialog" class="mt-4 flex justify-end">
    <button class="btn-secondary">{{ t $.lang "common.close" }}</button>
  </form>
</dialog>
<script src="{{ asset "js/sysinfo.js" }}" defer></script>
{{ end }}
```

The labels go through the catalog like every other string
([i18n.md](i18n.md)); `$.lang` reaches the page's language from inside `with`,
and `print` turns the counts into the strings `t` substitutes.
The timer is a static file, not an inline script: the CSP allows scripts from
`'self'` only (see [Security](security.md#content-security-policy)). It marks a
slow page with a `data-slow` attribute rather than adding a class, so the red is
the `data-slow:text-red-600` variant already in the markup, where Tailwind's
scan finds it.

```javascript
// internal/assets/static/js/sysinfo.js
(function () {
  var link = document.getElementById('system-info-link');
  var box = document.getElementById('system-info');
  if (!link || !box) return;
  link.addEventListener('click', function () { box.showModal(); });

  window.addEventListener('load', function () {
    setTimeout(function () {   // loadEventEnd is set after load handlers return
      var nav = performance.getEntriesByType('navigation')[0];
      if (!nav) return;
      var ms = Math.round(nav.loadEventEnd - nav.startTime);
      document.querySelectorAll('[data-page-load]').forEach(function (el) {
        el.textContent = (ms / 1000).toFixed(2) + 's';
      });
      var server = parseInt(link.dataset.renderMs || '0', 10);
      if (ms > 1000 || server > 1000) link.dataset.slow = '';
    }, 0);
  });
})();
```

The box shows server time **before** rendering began, because the template
cannot report on its own execution. Render time is on the request's log line.

### Test that it does not leak

```go
func TestServerTimingIsAdminOnly(t *testing.T) {
  r := setupTestRouter(t)

  for _, tc := range []struct {
    name   string
    cookie string
    want   bool
  }{
    {"anonymous", "", false},
    {"user", userSession(t), false},
    {"tenant admin", tenantAdminSession(t), false},
    {"platform admin", platformAdminSession(t), true},
  } {
    t.Run(tc.name, func(t *testing.T) {
      w := httptest.NewRecorder()
      req := httptest.NewRequest("GET", "/admin", nil)
      if tc.cookie != "" {
        req.AddCookie(&http.Cookie{Name: "session_token", Value: tc.cookie})
      }
      r.ServeHTTP(w, req)

      assert.Equal(t, tc.want, w.Header().Get("Server-Timing") != "")
      assert.Equal(t, tc.want, strings.Contains(w.Body.String(), `id="system-info"`))
      assert.NotEmpty(t, w.Header().Get("X-Request-ID"), "everyone gets a request ID")
    })
  }
}
```

### Using it

Time each admin page twice in one process (the first run pays cold caches and
connection setup) and look at queries and DB time against total time. The usual
culprits:

- **One query per row** (N+1): a list fetching each row's children one by one.
  Fetch them for the whole page in one `WHERE id IN ?` query. See
  [performance.md](performance.md#fixing-it).
- **Loading rows to count them**: `Find(&rows)` then `len(rows)`. Use
  `Count(&n)`.
- **Work for something folded away**: a collapsed section computed on every
  visit. Load it when it is opened (`hx-trigger="toggle once"`).
- **A lookup repeated per row** (scanning a slice for each row). Build the map
  once per request, or once per process (`sync.OnceValue`) for data that does
  not change.
- **Lists without paging**: page in the query (`Limit`/`Offset`, or a cursor on
  big tables; see [scale.md](../scale.md#cursor-pagination)), never after.
- **Pool waits climbing**: requests are queuing for a connection, not for the
  database. See [scale.md § Connection Pooling](../scale.md#connection-pooling).
- **DB time small, queries few, page still slow**: the time is in Go (a slow
  template function, an outbound HTTP call in the handler). Outbound calls belong
  in a job ([jobs.md](../jobs.md)).

---

## Graceful Shutdown

### Implementation

The entry point above already does this; the essentials:

```go
srv := &http.Server{Addr: ":" + cfg.Port, Handler: router}

go func() {
  if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
    log.Fatal().Err(err).Msg("server failed")
  }
}()

quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
<-quit

ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
if err := srv.Shutdown(ctx); err != nil { // stops accepting, drains in-flight
  log.Error().Err(err).Msg("forced shutdown")
}

// *gorm.DB has no Close(); the pool underneath does.
if sqlDB, err := database.DB(); err == nil {
  _ = sqlDB.Close()
}
```

### Why Graceful Shutdown?

- Finish in-flight requests (don't drop connections)
- Close database connections cleanly
- Flush logs and metrics
- Signal to systemd or the orchestrator that shutdown is complete

### How it plays with the restart

`srv.Shutdown` closes the listener first, then waits for in-flight requests.
From that moment new connections are refused, Caddy's `lb_try_duration` holds
them, and the new process takes them once it binds. The gap a user can see is
roughly *drain time + start time*:

- A drain shorter than `lb_try_duration` (5s) is invisible: a pause.
- A long drain (a slow report, an upload) pushes past it, and those users see
  the "be right back" page, which reloads itself.
- `TimeoutStopSec` in the unit must exceed the shutdown timeout, or systemd
  kills the process mid-drain and the requests it was finishing are lost.

Long-running work belongs in jobs, which is also what keeps restarts short.

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

Managed platforms do rolling restarts and health checks themselves; point them
at `/readyz`, and run migrations as a release or pre-deploy step with the owner
DSN, never in the app's startup.

### Reverse Proxy (Caddy)

See [Caddy Reverse Proxy](#caddy-reverse-proxy) for the full configuration,
including the retry window and the "be right back" page.

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

  # "Be right back": the same idea as Caddy's handle_errors
  error_page 502 503 504 /maintenance.html;
  location = /maintenance.html {
    root /etc/nginx/maintenance;
    add_header Cache-Control "no-store" always;
    internal;
  }
}
```

### Systemd Service

See [Systemd Service](#systemd-service).

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
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'

      - name: Deploy
        env:
          SSH_KEY: ${{ secrets.DEPLOY_SSH_KEY }}
        run: |
          mkdir -p ~/.ssh && echo "$SSH_KEY" > ~/.ssh/id_ed25519 && chmod 600 ~/.ssh/id_ed25519
          ssh-keyscan yourapp.com >> ~/.ssh/known_hosts
          ./scripts/deploy.sh   # tests, build, migrate, restart, health gate
```

CI runs the same script a human does. A deploy path that only CI knows is one
nobody can run when CI is down.

---

## Troubleshooting

### Service won't start

```bash
sudo journalctl -u yourapp -n 50 --no-pager
sudo systemctl status yourapp

# Run it by hand, as the service user, with the service's environment
sudo -u yourapp bash -c 'set -a; . /opt/yourapp/.env; /opt/yourapp/current/yourapp'
```

### Every page shows "be right back"

The app is down or not listening where Caddy expects. Check the service, then
that the port in `.env` matches the `reverse_proxy` line, then
`curl -i localhost:8000/readyz` on the server.

### Deploy fails the health gate

The script prints the last 30 journal lines and rolls back the binary. If
`/readyz` answers but with the old version, the restart did not take; if it
answers 503, the database is unreachable with the runtime DSN.

### Caddy not serving

```bash
sudo systemctl status caddy
sudo journalctl -u caddy -n 50 --no-pager
caddy validate --config /etc/caddy/Caddyfile
```

---

**Next:** [Security](security.md) | [Observability](observability.md) | back to [claude.md](../../claude.md)
