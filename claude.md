# claude.md - Go+Gin SaaS Blueprint (MVC Pattern)

> **Master guide for bootstrapping production-ready Go+Gin SaaS applications.**
> Single-file blueprint combining architecture, MVC patterns, database setup, i18n, security, and deployment.
> Optimized for **fast iteration**, **clean code**, and **AI agent execution**.

---

## 🚀 QUICK START (New Project Bootstrap)

**👉 AI Agents: Execute this section first (30 minutes to running app)**

### Prerequisites Checklist
- [ ] Go 1.21+
- [ ] PostgreSQL 15+
- [ ] Docker (optional, for local DB)
- [ ] Make

### Bootstrap Sequence (Follow in Order)

**Step 1: Initialize Project** (2 min)
```bash
go mod init github.com/yourorg/yourapp
mkdir -p cmd/yourapp
mkdir -p internal/{platform,models,controllers,middleware}
mkdir -p views/{layouts,partials,users,settings}
mkdir -p static/{css,js,img}
mkdir -p lang migrations config
```
✅ **Verify:** `ls` shows folder structure

**Step 2: Install Dependencies** (1 min)
```bash
go get github.com/gin-gonic/gin
go get github.com/jmoiron/sqlx
go get github.com/lib/pq
go get github.com/rs/zerolog
go install github.com/pressly/goose/v3/cmd/goose@latest
```
✅ **Verify:** `go mod tidy` succeeds

**Step 3: Database Setup** (5 min)
```bash
# Start local Postgres
docker run --name app-db \
  -e POSTGRES_USER=app \
  -e POSTGRES_PASSWORD=app \
  -e POSTGRES_DB=app \
  -p 5432:5432 -d postgres:15-alpine

# Set DATABASE_URL
export DATABASE_URL="postgres://app:app@localhost:5432/app?sslmode=disable"

# Create initial migration (see § Database Setup for schema)
# Apply migration
goose -dir migrations postgres "$DATABASE_URL" up
```
✅ **Verify:** `psql "$DATABASE_URL"` connects

**Step 4: Platform Layer** (10 min)

Create these files (see detailed sections below):
- `internal/platform/db/db.go` → § Database Setup
- `internal/platform/logger/logger.go` → § Logging
- `internal/platform/config/config.go` → § Configuration
- `internal/platform/i18n/i18n.go` → [reference/i18n.md](reference/i18n.md)
- `internal/platform/auth/session.go` → [reference/auth.md](reference/auth.md)

✅ **Verify:** Files exist, no import errors

**Step 5: First Model + Controller** (10 min)
- Create `internal/models/user.go` → § Models Pattern
- Create `internal/controllers/router.go` → § Controllers Pattern
- Create `views/layouts/base.html` → § Views Pattern

✅ **Verify:** `go build ./cmd/yourapp` succeeds

**Step 6: Environment & Run** (2 min)
```bash
# Create config/local.env
cat > config/local.env <<EOF
APP_ENV=dev
PORT=8000
DATABASE_URL=postgres://app:app@localhost:5432/app?sslmode=disable
EOF

# Create cmd/yourapp/main.go (see § Application Startup)
make run  # or: go run ./cmd/yourapp
```
✅ **Verify:** Server runs at http://localhost:8000

**Expected Time: 30 minutes to running app**

---

## 📋 TABLE OF CONTENTS

### Foundation (Read Once)
- [Philosophy](#philosophy) - Core principles
- [Folder Structure](#folder-structure) - MVC layout
- [Tech Stack](#tech-stack) - Dependencies & rationale
- [Conventions](#conventions) - Code style

### Platform Components (Implement in Order)
1. [Database Setup](#database-setup) - Connection, migrations
2. [Configuration](#configuration) - Environment variables
3. [Logging](#logging) - Structured logging
4. [Internationalization](#internationalization) → [reference/i18n.md](reference/i18n.md)
5. [Sessions & Auth](#sessions--auth) → [reference/auth.md](reference/auth.md)
6. [Error Handling](#error-handling) - Standardized errors

### MVC Pattern
- [Models](#models) - Fat models (business logic + DB)
- [Views](#views) - Templates with HTMX
- [Controllers](#controllers) - Thin HTTP handlers

### Features (Cross-referenced)
- [HTMX Patterns](#htmx-patterns) → [reference/htmx.md](reference/htmx.md)
- [Frontend Architecture](#frontend-architecture) → [reference/frontend.md](reference/frontend.md)
- [Multi-Tenancy](#multi-tenancy) → [reference/database.md](reference/database.md#multi-tenancy-with-row-level-security)
- [Security](#security) → [reference/security.md](reference/security.md)

### Operations
- [Testing](#testing) → [reference/testing.md](reference/testing.md)
- [Deployment](#deployment) → [reference/deployment.md](reference/deployment.md)

### Reference Documentation
- [MVC Pattern Guide](reference/mvc.md) - Models, Views, Controllers in detail
- [Database Patterns](reference/database.md) - JSONB, FTS, RLS, indexes
- [i18n Guide](reference/i18n.md) - Complete internationalization
- [Auth & Sessions](reference/auth.md) - Magic links, OAuth, JWT
- [HTMX Cookbook](reference/htmx.md) - Interactive patterns
- [Frontend Guide](reference/frontend.md) - Bootstrap + HTMX
- [Testing Guide](reference/testing.md) - Unit + integration tests
- [Security Guide](reference/security.md) - CSRF, rate limiting, security checklist
- [Deployment Guide](reference/deployment.md) - Production deployment

---

## Philosophy

### Core Principles

- **Keep it simple, explicit, and local.** No magic, no over-engineering.
- **MVC Pattern**: Models (fat), Views (templates), Controllers (thin).
- **Server-rendered HTML + HTMX**: No SPA complexity, progressive enhancement.
- **i18n from day 1**: Global-ready from the start.
- **Security and observability by default**: CSRF, rate limiting, logging, metrics.
- **Zero yak-shaving dev loop**: `make run` boots a working app.

### Fat Models, Thin Controllers

**Models** contain:
- Business logic (validation, calculations)
- Database access (CRUD, queries)
- Domain rules

**Controllers** contain:
- Parse input
- Call model methods
- Render view or return JSON

**Views** contain:
- HTML templates
- Minimal logic (loops, conditions)
- HTMX attributes for interactivity

---

## Folder Structure

```
yourapp/
├── cmd/
│   └── yourapp/
│       └── main.go              # Application entry point
├── internal/
│   ├── platform/                # Infrastructure layer
│   │   ├── db/                  # Database connection, transactions
│   │   ├── auth/                # Authentication (magic links, sessions)
│   │   ├── i18n/                # Internationalization
│   │   ├── logger/              # Structured logging (zerolog)
│   │   ├── config/              # Configuration management
│   │   └── errors/              # Error handling
│   ├── models/                  # Domain models (fat models)
│   │   ├── user.go              # User model (CRUD + business logic)
│   │   ├── setting.go           # Setting model (key-value config)
│   │   └── ...                  # Your domain models here
│   ├── controllers/             # HTTP request handlers (thin)
│   │   ├── users_controller.go  # Profile, user management
│   │   ├── settings_controller.go  # User settings
│   │   └── router.go            # Route setup
│   └── middleware/              # Custom middleware
│       ├── auth.go              # Session authentication
│       ├── csrf.go              # CSRF protection
│       └── ratelimit.go         # Rate limiting
├── views/                       # Templates (HTML)
│   ├── layouts/
│   │   ├── base.html            # Main layout (navbar, footer)
│   │   └── minimal.html         # Auth pages (no navbar)
│   ├── partials/
│   │   ├── _navbar.html         # Shared navbar
│   │   └── _toast.html          # Toast notifications
│   ├── users/
│   │   ├── profile.html         # User profile page
│   │   └── edit.html            # Edit profile form
│   └── settings/
│       ├── index.html           # Settings page
│       └── _setting_row.html    # HTMX partial (prefix: _)
├── static/                      # Static assets
│   ├── css/
│   │   ├── bootstrap.min.css    # Bootstrap 5
│   │   └── app.css              # Brand overrides (~50-100 lines)
│   ├── js/
│   │   ├── htmx.min.js          # HTMX library
│   │   └── app.js               # Minimal custom JS (if needed)
│   └── img/
│       └── logo.svg
├── lang/                        # i18n translation files
│   ├── en.json
│   └── es.json
├── migrations/                  # Database migrations (goose)
│   ├── 001_init_schema.sql
│   └── demo_data.sql            # Demo data for tests/dev
├── config/
│   └── local.env.example
├── reference/                   # Blueprint documentation (copy to new projects)
│   ├── reference/
│   │   ├── mvc.md               # Models, Views, Controllers guide
│   │   ├── database.md          # JSONB, FTS, RLS, indexes
│   │   ├── i18n.md              # Internationalization
│   │   ├── auth.md              # Magic links, OAuth, JWT
│   │   ├── htmx.md              # HTMX patterns
│   │   ├── frontend.md          # Bootstrap + HTMX
│   │   ├── testing.md           # Testing patterns
│   │   ├── security.md          # Security checklist
│   │   └── deployment.md        # Production deployment
│   └── examples/
│       └── (reference implementations)
├── Makefile
├── Dockerfile
├── README.md
└── claude.md                    # This file (master blueprint)
```

---

## Tech Stack

| Layer | Technology | Rationale |
|-------|-----------|-----------|
| **Backend** | Go 1.21+ | Performance, simplicity, compiled binary |
| **Web Framework** | Gin | Fast, minimal, great DX |
| **Database** | PostgreSQL 15+ | JSONB, full-text search, RLS |
| **SQL Library** | sqlx | Clear SQL, pragmatic ergonomics |
| **Templates** | html/template | Secure (auto-escape), fast, embedded |
| **Frontend** | Bootstrap 5 + HTMX | No build step, progressive enhancement |
| **i18n** | JSON catalogs | Simple, runtime-loaded |
| **Logging** | zerolog | Structured, fast |
| **Migrations** | goose | Simple, SQL-based |
| **Auth** | Magic links (dev) | Easy to start, extensible |

---

## Conventions

### Go Code
- **Pointers for models**: `func (u *User) ...`
- **Always pass context** to DB calls with 3s timeout (override per op if needed)
- **No global singletons** outside `platform/db`
- **Files ~300 lines**: Split by concern (`user.go`, `user_validation.go`)

### HTTP
- **Controllers**: Parse input, call models, render view/JSON
- **Use `gin.Recovery()`** and request logging
- **Add request ID middleware** for traceability

### Templates
- **html/template** with auto-escaping (no raw HTML unless trusted)
- **Partials prefix**: `_partial_name.html`
- **All user-visible strings in i18n catalogs** (lint for stray literals)

### Frontend
- **Bootstrap 5** for CSS (no Tailwind, no custom frameworks)
- **HTMX** for interactivity (no custom JavaScript)
- **One global CSS file** (`static/css/app.css`) for brand overrides only (~50-100 lines)
- **No build pipeline** (no npm, webpack, vite)

---

## Database Setup

**Minimal connection setup:**

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
```

**Conventions:**
- Tables: lowercase, singular (`user`, `setting`)
- Columns: `snake_case` (`created_at`, `user_id`)
- All tables have `created_at`, `updated_at`

→ **For complete guide** (migrations, transactions, JSONB, FTS, indexes, multi-tenancy):
See [reference/database.md](reference/database.md)

---

## Configuration

```go
// internal/platform/config/config.go
package config

import (
  "fmt"
  "os"
  "strconv"
)

type Config struct {
  AppEnv      string
  Port        string
  DatabaseURL string
  DevMagic    bool
}

func Load() (*Config, error) {
  cfg := &Config{
    AppEnv:      getEnv("APP_ENV", "dev"),
    Port:        getEnv("PORT", "8000"),
    DatabaseURL: os.Getenv("DATABASE_URL"),
    DevMagic:    getEnvBool("DEV_MAGIC", false),
  }

  if cfg.DatabaseURL == "" {
    return nil, fmt.Errorf("DATABASE_URL is required")
  }

  return cfg, nil
}

func getEnv(key, fallback string) string {
  if value := os.Getenv(key); value != "" {
    return value
  }
  return fallback
}

func getEnvBool(key string, fallback bool) bool {
  if value := os.Getenv(key); value != "" {
    b, err := strconv.ParseBool(value)
    if err == nil {
      return b
    }
  }
  return fallback
}
```

---

## Logging

```go
// internal/platform/logger/logger.go
package logger

import (
  "os"
  "time"

  "github.com/gin-gonic/gin"
  "github.com/rs/zerolog"
  "github.com/rs/zerolog/log"
)

var logger zerolog.Logger

func Init(env string) {
  zerolog.TimeFieldFormat = time.RFC3339

  if env == "dev" {
    logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}).
      With().
      Timestamp().
      Caller().
      Logger()
  } else {
    logger = zerolog.New(os.Stdout).
      With().
      Timestamp().
      Logger()
  }

  log.Logger = logger
}

func Get() *zerolog.Logger {
  return &logger
}

// FromContext extracts logger from gin context with request metadata
func FromContext(c *gin.Context) *zerolog.Logger {
  reqID := c.GetString("request_id")
  userID := c.GetInt64("user_id")

  l := logger.With().
    Str("req_id", reqID).
    Int64("user_id", userID).
    Logger()

  return &l
}
```

---

## Internationalization

→ **See complete guide:** [reference/i18n.md](reference/i18n.md)

---

## Sessions & Auth

→ **See complete guide:** [reference/auth.md](reference/auth.md)

---

## Error Handling

```go
// internal/platform/errors/errors.go
package errors

import "fmt"

const (
  CodeUnknown         = "E_UNKNOWN"
  CodeNotFound        = "E_NOT_FOUND"
  CodeInvalidInput    = "E_INVALID_INPUT"
  CodeUnauthorized    = "E_UNAUTHORIZED"
  CodeForbidden       = "E_FORBIDDEN"
  CodeConflict        = "E_CONFLICT"
)

type AppError struct {
  Code    string
  Message string
  Err     error
}

func (e *AppError) Error() string {
  if e.Err != nil {
    return fmt.Sprintf("%s: %s (%v)", e.Code, e.Message, e.Err)
  }
  return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func New(code, message string) *AppError {
  return &AppError{Code: code, Message: message}
}

func (e *AppError) HTTPStatus() int {
  switch e.Code {
  case CodeNotFound:
    return 404
  case CodeInvalidInput, CodeConflict:
    return 400
  case CodeUnauthorized:
    return 401
  case CodeForbidden:
    return 403
  default:
    return 500
  }
}
```

---

## Models

→ **See complete guide:** [reference/mvc.md](reference/mvc.md#models-fat-models)

---

## Controllers

→ **See complete guide:** [reference/mvc.md](reference/mvc.md#controllers-thin-controllers)

---

## Views

→ **See complete guide:** [reference/mvc.md](reference/mvc.md#views-templates)

---

## HTMX Patterns

→ **See complete guide:** [reference/htmx.md](reference/htmx.md)

---

## Frontend Architecture

**Bootstrap 5 + HTMX. No build pipeline.**

### Principles

- ✅ Reuse Bootstrap classes (never invent custom classes)
- ✅ One `app.css` for brand overrides (~50-100 lines max)
- ✅ No npm, webpack, or build tools
- ✅ HTMX for all interactivity
- ✅ Progressive enhancement (works without JS)

→ **For complete guide** (component library, accessibility, RTL):
See [reference/frontend.md](reference/frontend.md)

---

## Multi-Tenancy

### Default: User-Owned Data (B2C)

**Most SaaS apps** start here:
- Use `user_id` foreign keys
- Application-layer filtering: `WHERE user_id = ?`
- Simpler, faster to build

```sql
CREATE TABLE setting (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES "user"(id),
  key TEXT NOT NULL,
  value TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE(user_id, key)
);

CREATE INDEX idx_setting_user_id ON setting(user_id);
```

### Optional: Multi-Tenant (B2B)

**For team/organization apps**:
- Add `tenant_id` columns
- Database-level isolation via Row-Level Security (RLS)
- When: B2B SaaS, workspace-based apps

→ **For complete RLS implementation**:
See [reference/database.md#multi-tenancy-with-row-level-security](reference/database.md#multi-tenancy-with-row-level-security)

---

## Security

→ **For complete security implementation** (CSRF, rate limiting, input validation):
See [reference/security.md](reference/security.md)

---

## Testing

**Fast, isolated tests using transaction rollback.**

```go
func TestSetting_Set(t *testing.T) {
  db.SetupTestData(t) // Load demo data once

  db.TestTx(t, func(t *testing.T, tx *sqlx.Tx) {
    setting := Setting{
      UserID: 1,
      Key:    "theme",
      Value:  "dark",
    }

    err := setting.Set(context.Background())
    require.NoError(t, err)
    assert.NotZero(t, setting.ID)

    // Changes rolled back automatically
  })
}
```

→ **For complete testing guide** (HTTP handlers, integration tests, CI/CD):
See [reference/testing.md](reference/testing.md)

---

## Deployment

### Application Startup

```go
// cmd/yourapp/main.go
package main

import (
  "context"
  "fmt"
  "os"
  "time"

  "yourapp/internal/controllers"
  "yourapp/internal/platform/config"
  "yourapp/internal/platform/db"
  "yourapp/internal/platform/i18n"
  "yourapp/internal/platform/logger"

  "github.com/jmoiron/sqlx"
  _ "github.com/lib/pq"
)

func main() {
  logger.Init(os.Getenv("APP_ENV"))
  log := logger.Get()

  cfg, err := config.Load()
  if err != nil {
    log.Fatal().Err(err).Msg("failed to load config")
  }

  ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  defer cancel()

  database, err := sqlx.ConnectContext(ctx, "postgres", cfg.DatabaseURL)
  if err != nil {
    log.Fatal().Err(err).Msg("failed to connect to database")
  }
  defer database.Close()

  db.SetDB(database)
  database.SetMaxOpenConns(25)
  database.SetMaxIdleConns(5)

  i18n.Preload("en")
  router := controllers.SetupRouter()

  addr := fmt.Sprintf("0.0.0.0:%s", cfg.Port)
  log.Info().Str("addr", addr).Msg("server listening")

  if err := router.Run(addr); err != nil {
    log.Fatal().Err(err).Msg("server failed to start")
  }
}
```

→ **For complete deployment guide** (Docker, production checklist, graceful shutdown):
See [reference/deployment.md](reference/deployment.md)

---

## Makefile

```makefile
APP=yourapp

.PHONY: run build test fmt migrate

run:
	export $$(cat config/local.env | xargs) && go run ./cmd/${APP}

build:
	go build -o bin/${APP} ./cmd/${APP}

test:
	go test ./... -cover

fmt:
	go fmt ./...

migrate:
	goose -dir migrations postgres $${DATABASE_URL} up

migrate-status:
	goose -dir migrations postgres $${DATABASE_URL} status
```

---

## 🤖 AI Agent Instructions

### For Claude/AI Assistants

**When bootstrapping new project:**
1. Read § Quick Start (top of file)
2. Execute steps 1-6 in order
3. Reference detailed sections as needed
4. Verify: `make run` succeeds

**When adding features:**
1. Scan Table of Contents for relevant section
2. Jump to section via anchor link
3. If section says "See reference/X.md", read that file

**When troubleshooting:**
1. Check relevant platform component section
2. Review reference/ for edge cases

### Files to Read (in order)
1. `claude.md` - Main blueprint (this file)
2. `reference/*.md` - Only when referenced
3. `examples/` - Real-world implementations

**Never skip:**
- Philosophy (defines patterns)
- Quick Start (ensures nothing missed)
- MVC Pattern sections (core architecture)

---

## License

Copyright (c) Your Organization.
Author: Your Name

---

**That's it.** One file to guide any Go + Gin SaaS project with MVC, i18n, and production-ready patterns.
**Build fast, read easily, scale calmly.**
