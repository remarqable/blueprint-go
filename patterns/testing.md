# Testing Reference Guide

> Comprehensive testing patterns for Go+Gin applications with transaction rollback and CI/CD

> **Universal Examples**: This guide uses **User** and **Setting** models for test examples. These patterns apply to testing any domain model in your application.

---

## Table of Contents

- [Philosophy](#philosophy)
- [Test Structure](#test-structure)
- [Transaction Rollback Pattern](#transaction-rollback-pattern)
- [Demo Data Management](#demo-data-management)
- [Unit Tests](#unit-tests)
- [Integration Tests](#integration-tests)
- [HTTP Handler Tests](#http-handler-tests)
- [Pre-commit Hooks](#pre-commit-hooks)
- [CI/CD Integration](#cicd-integration)
- [Best Practices](#best-practices)

---

## Philosophy

**Fast, isolated, reliable tests using transaction rollback.**

### Core Principles

1. **Use existing local DB** (no separate test database)
2. **Transaction rollback** for isolation (fast, clean)
3. **Demo data** in sync with schema
4. **Table-driven tests** for comprehensive coverage
5. **Test close to production** (same DB, real queries)

### Test Types

| Type | Scope | Speed | Use Case |
|------|-------|-------|----------|
| **Unit** | Pure logic | Very fast | Validation, calculations |
| **Integration** | Model + DB | Fast (rollback) | CRUD operations, queries |
| **HTTP** | Controller + DB | Fast (rollback) | Request/response, routing |
| **E2E** | Full stack | Slow | Critical user flows |

---

## Test Structure

### File Organization

```
internal/
  models/
    user.go
    user_test.go                # Unit tests (pure logic)
    user_integration_test.go    # Integration tests (DB)
    setting.go
    setting_test.go
    setting_integration_test.go
  controllers/
    users_controller.go
    users_controller_test.go    # HTTP handler tests
    settings_controller.go
    settings_controller_test.go
  platform/
    db/
      testdb.go                 # Test helpers (TestTx, SetupTestData)
      testdb_test.go

migrations/
  001_init_schema.sql
  002_add_settings.sql
  demo_data.sql                 # Demo data for tests (CRITICAL: keep in sync)
```

### Naming Conventions

- `*_test.go` - Unit tests (no external dependencies)
- `*_integration_test.go` - Integration tests (database, external services)
- `testdb.go` - Test helpers (not `*_test.go` so it's importable)

---

## Transaction Rollback Pattern

### Why Transaction Rollback?

**Traditional approach (slow):**
1. Spin up test database
2. Apply migrations
3. Load fixtures
4. Run test
5. Teardown database

**Time:** 5-10 seconds per test

**Transaction rollback (fast):**
1. Use existing local DB
2. Start transaction
3. Run test
4. Rollback (automatic)

**Time:** 50-100ms per test

### Test Database Helper

```go
// internal/platform/db/testdb.go
package db

import (
  "context"
  "testing"
  "github.com/jmoiron/sqlx"
)

// TestTx wraps a test in a transaction that auto-rolls back
// Ensures test isolation without DB spin-up overhead
func TestTx(t *testing.T, fn func(t *testing.T, tx *sqlx.Tx)) {
  t.Helper()

  ctx := context.Background()
  tx, err := Get().BeginTxx(ctx, nil)
  if err != nil {
    t.Fatalf("failed to begin tx: %v", err)
  }

  // Always rollback (even if test passes)
  defer tx.Rollback()

  fn(t, tx)
}

// SetupTestData loads demo data from migrations/demo_data.sql
// Call once before running tests (e.g., in TestMain)
// IMPORTANT: Keep in sync with schema and demo_data.sql
func SetupTestData(t *testing.T) {
  t.Helper()

  // Truncate all tables for clean slate
  Get().MustExec(`
    TRUNCATE TABLE "user", setting CASCADE;
  `)

  // Load demo data (keep in sync with migrations/demo_data.sql)
  Get().MustExec(`
    INSERT INTO "user" (id, email, name, avatar_url, created_at, updated_at) VALUES
      (1, 'alice@example.com', 'Alice', 'https://i.pravatar.cc/150?u=alice', NOW(), NOW()),
      (2, 'bob@example.com', 'Bob', 'https://i.pravatar.cc/150?u=bob', NOW(), NOW()),
      (3, 'charlie@example.com', 'Charlie', 'https://i.pravatar.cc/150?u=charlie', NOW(), NOW());

    INSERT INTO setting (id, user_id, key, value, created_at, updated_at) VALUES
      (1, 1, 'theme', 'dark', NOW(), NOW()),
      (2, 1, 'language', 'en', NOW(), NOW()),
      (3, 1, 'timezone', 'America/New_York', NOW(), NOW()),
      (4, 2, 'theme', 'light', NOW(), NOW()),
      (5, 2, 'notifications', 'true', NOW(), NOW());
  `)

  // Reset sequences
  Get().MustExec(`
    SELECT setval(pg_get_serial_sequence('"user"', 'id'), (SELECT COALESCE(MAX(id), 0) FROM "user"));
    SELECT setval(pg_get_serial_sequence('setting', 'id'), (SELECT COALESCE(MAX(id), 0) FROM setting));
  `)
}
```

### Usage in Tests

```go
func TestSetting_Set(t *testing.T) {
  db.SetupTestData(t) // Load demo data once

  db.TestTx(t, func(t *testing.T, tx *sqlx.Tx) {
    setting := Setting{
      UserID: 1,
      Key:    "email_notifications",
      Value:  "true",
    }

    err := setting.Set(context.Background())
    require.NoError(t, err)
    assert.NotZero(t, setting.ID)
    assert.NotZero(t, setting.CreatedAt)

    // Changes rolled back automatically
  })
}
```

---

## Demo Data Management

### Demo Data File

**CRITICAL:** Keep `migrations/demo_data.sql` in sync with schema changes.

```sql
-- migrations/demo_data.sql
-- Demo data for local development and testing
-- IMPORTANT: Keep in sync with schema changes

-- Clean slate
TRUNCATE TABLE "user", setting CASCADE;

-- Test users (standard fixtures)
INSERT INTO "user" (id, email, name, avatar_url, created_at, updated_at) VALUES
  (1, 'alice@example.com', 'Alice', 'https://i.pravatar.cc/150?u=alice', NOW(), NOW()),
  (2, 'bob@example.com', 'Bob', 'https://i.pravatar.cc/150?u=bob', NOW(), NOW()),
  (3, 'charlie@example.com', 'Charlie', 'https://i.pravatar.cc/150?u=charlie', NOW(), NOW());

-- Test settings (common configurations)
INSERT INTO setting (id, user_id, key, value, created_at, updated_at) VALUES
  (1, 1, 'theme', 'dark', NOW(), NOW()),
  (2, 1, 'language', 'en', NOW(), NOW()),
  (3, 1, 'timezone', 'America/New_York', NOW(), NOW()),
  (4, 2, 'theme', 'light', NOW(), NOW()),
  (5, 2, 'notifications', 'true', NOW(), NOW());

-- Reset sequences (CRITICAL: matches table structure)
SELECT setval(pg_get_serial_sequence('"user"', 'id'), (SELECT COALESCE(MAX(id), 0) FROM "user"));
SELECT setval(pg_get_serial_sequence('setting', 'id'), (SELECT COALESCE(MAX(id), 0) FROM setting));
```

### Load Demo Data (Makefile)

```makefile
# Load demo data for development/testing
load-demo-data:
	psql $${DATABASE_URL} -f migrations/demo_data.sql
```

---

## Unit Tests

### Testing Pure Logic (No DB)

```go
// internal/models/user_test.go
package models

import (
  "testing"
  "strings"
  "github.com/stretchr/testify/assert"
  "github.com/stretchr/testify/require"
  "yourapp/internal/platform/errors"
)

func TestUser_Validate(t *testing.T) {
  tests := []struct {
    name    string
    user    User
    wantErr bool
    errCode string
  }{
    {
      name:    "valid user",
      user:    User{Email: "test@example.com", Name: "Test User"},
      wantErr: false,
    },
    {
      name:    "missing email",
      user:    User{Name: "Test User"},
      wantErr: true,
      errCode: errors.CodeInvalidInput,
    },
    {
      name:    "invalid email format",
      user:    User{Email: "not-an-email", Name: "Test"},
      wantErr: true,
      errCode: errors.CodeInvalidInput,
    },
    {
      name:    "missing name",
      user:    User{Email: "test@example.com"},
      wantErr: true,
      errCode: errors.CodeInvalidInput,
    },
    {
      name:    "name too long",
      user:    User{Email: "test@example.com", Name: strings.Repeat("a", 256)},
      wantErr: true,
      errCode: errors.CodeInvalidInput,
    },
  }

  for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
      err := tt.user.Validate()

      if tt.wantErr {
        require.Error(t, err)
        var appErr *errors.AppError
        require.True(t, errors.As(err, &appErr))
        assert.Equal(t, tt.errCode, appErr.Code)
      } else {
        require.NoError(t, err)
      }
    })
  }
}

func TestSetting_Validate(t *testing.T) {
  tests := []struct {
    name    string
    setting Setting
    wantErr bool
    errCode string
  }{
    {
      name:    "valid setting",
      setting: Setting{UserID: 1, Key: "theme", Value: "dark"},
      wantErr: false,
    },
    {
      name:    "missing key",
      setting: Setting{UserID: 1, Value: "dark"},
      wantErr: true,
      errCode: errors.CodeInvalidInput,
    },
    {
      name:    "missing value",
      setting: Setting{UserID: 1, Key: "theme"},
      wantErr: true,
      errCode: errors.CodeInvalidInput,
    },
    {
      name:    "invalid user_id",
      setting: Setting{UserID: 0, Key: "theme", Value: "dark"},
      wantErr: true,
      errCode: errors.CodeInvalidInput,
    },
  }

  for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
      err := tt.setting.Validate()

      if tt.wantErr {
        require.Error(t, err)
        var appErr *errors.AppError
        require.True(t, errors.As(err, &appErr))
        assert.Equal(t, tt.errCode, appErr.Code)
      } else {
        require.NoError(t, err)
      }
    })
  }
}
```

---

## Integration Tests

### Testing Models with Database

```go
// internal/models/user_integration_test.go
package models

import (
  "context"
  "testing"
  "github.com/stretchr/testify/assert"
  "github.com/stretchr/testify/require"
  "yourapp/internal/platform/db"
)

func TestUser_Create(t *testing.T) {
  db.SetupTestData(t) // Load demo data once

  db.TestTx(t, func(t *testing.T, tx *sqlx.Tx) {
    user := User{
      Email: "newuser@example.com",
      Name:  "New User",
    }

    err := user.Create(context.Background())
    require.NoError(t, err)
    assert.NotZero(t, user.ID)
    assert.NotZero(t, user.CreatedAt)
    assert.NotZero(t, user.UpdatedAt)

    // Verify in DB (within transaction)
    var count int
    err = tx.Get(&count, `SELECT COUNT(*) FROM "user" WHERE email=$1`, "newuser@example.com")
    require.NoError(t, err)
    assert.Equal(t, 1, count)

    // Changes rolled back automatically
  })
}

func TestUser_GetByEmail(t *testing.T) {
  db.SetupTestData(t) // Demo data includes alice@example.com

  db.TestTx(t, func(t *testing.T, tx *sqlx.Tx) {
    user, err := GetUserByEmail(context.Background(), "alice@example.com")

    require.NoError(t, err)
    assert.Equal(t, "Alice", user.Name)
    assert.Equal(t, int64(1), user.ID)
  })
}

func TestUser_Update(t *testing.T) {
  db.SetupTestData(t)

  db.TestTx(t, func(t *testing.T, tx *sqlx.Tx) {
    // Get existing user
    user, err := GetUserByEmail(context.Background(), "alice@example.com")
    require.NoError(t, err)

    // Update name
    user.Name = "Alice Updated"
    err = user.Update(context.Background())
    require.NoError(t, err)

    // Verify update
    updated, err := GetUserByID(context.Background(), user.ID)
    require.NoError(t, err)
    assert.Equal(t, "Alice Updated", updated.Name)
  })
}

func TestUser_Delete(t *testing.T) {
  db.SetupTestData(t)

  db.TestTx(t, func(t *testing.T, tx *sqlx.Tx) {
    user, err := GetUserByEmail(context.Background(), "charlie@example.com")
    require.NoError(t, err)

    err = user.Delete(context.Background())
    require.NoError(t, err)

    // Verify deletion
    var count int
    err = tx.Get(&count, `SELECT COUNT(*) FROM "user" WHERE id=$1`, user.ID)
    require.NoError(t, err)
    assert.Equal(t, 0, count)
  })
}
```

```go
// internal/models/setting_integration_test.go
package models

import (
  "context"
  "testing"
  "github.com/stretchr/testify/assert"
  "github.com/stretchr/testify/require"
  "yourapp/internal/platform/db"
)

func TestSetting_Set(t *testing.T) {
  db.SetupTestData(t)

  tests := []struct {
    name     string
    userID   int64
    key      string
    value    string
    isUpdate bool // true if setting already exists
  }{
    {
      name:     "create new setting",
      userID:   1,
      key:      "email_notifications",
      value:    "true",
      isUpdate: false,
    },
    {
      name:     "update existing setting",
      userID:   1,
      key:      "theme",
      value:    "light", // Alice has dark theme in demo data
      isUpdate: true,
    },
  }

  for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
      db.TestTx(t, func(t *testing.T, tx *sqlx.Tx) {
        setting := Setting{
          UserID: tt.userID,
          Key:    tt.key,
          Value:  tt.value,
        }

        err := setting.Set(context.Background())
        require.NoError(t, err)
        assert.NotZero(t, setting.ID)

        // Verify in DB
        retrieved, err := GetUserSetting(context.Background(), tt.userID, tt.key)
        require.NoError(t, err)
        assert.Equal(t, tt.value, retrieved.Value)
      })
    })
  }
}

func TestSetting_GetUserSettings(t *testing.T) {
  db.SetupTestData(t)

  db.TestTx(t, func(t *testing.T, tx *sqlx.Tx) {
    // Alice has 3 settings in demo data: theme, language, timezone
    settings, err := GetUserSettings(context.Background(), 1)
    require.NoError(t, err)
    assert.Len(t, settings, 3)

    // Verify as map
    settingsMap, err := GetUserSettingsMap(context.Background(), 1)
    require.NoError(t, err)
    assert.Equal(t, "dark", settingsMap["theme"])
    assert.Equal(t, "en", settingsMap["language"])
    assert.Equal(t, "America/New_York", settingsMap["timezone"])
  })
}

func TestSetting_Delete(t *testing.T) {
  db.SetupTestData(t)

  db.TestTx(t, func(t *testing.T, tx *sqlx.Tx) {
    setting := &Setting{
      UserID: 1,
      Key:    "theme",
    }

    err := setting.Delete(context.Background())
    require.NoError(t, err)

    // Verify deletion
    _, err = GetUserSetting(context.Background(), 1, "theme")
    require.Error(t, err) // Should return "not found" error
  })
}
```

---

## HTTP Handler Tests

### Testing Controllers

```go
// internal/controllers/users_controller_test.go
package controllers

import (
  "net/http"
  "net/http/httptest"
  "strings"
  "testing"
  "github.com/gin-gonic/gin"
  "github.com/stretchr/testify/assert"
  "yourapp/internal/platform/db"
)

func setupRouter() *gin.Engine {
  gin.SetMode(gin.TestMode)
  router := gin.New()

  // User routes
  router.GET("/profile", ShowProfile)
  router.POST("/profile", UpdateProfile)

  // Settings routes
  router.GET("/settings", ShowSettings)
  router.POST("/settings", UpdateSetting)
  router.DELETE("/settings/:key", DeleteSetting)

  return router
}

func TestShowProfile(t *testing.T) {
  db.SetupTestData(t)

  db.TestTx(t, func(t *testing.T, tx *sqlx.Tx) {
    router := setupRouter()

    w := httptest.NewRecorder()
    req, _ := http.NewRequest("GET", "/profile", nil)

    // Simulate authenticated user (Alice)
    req.Header.Set("X-User-ID", "1")

    router.ServeHTTP(w, req)

    assert.Equal(t, 200, w.Code)
    assert.Contains(t, w.Body.String(), "Alice")
    assert.Contains(t, w.Body.String(), "alice@example.com")
  })
}

func TestUpdateProfile(t *testing.T) {
  db.SetupTestData(t)

  db.TestTx(t, func(t *testing.T, tx *sqlx.Tx) {
    router := setupRouter()

    w := httptest.NewRecorder()
    body := strings.NewReader(`name=Alice+Updated&email=alice@example.com`)
    req, _ := http.NewRequest("POST", "/profile", body)
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    req.Header.Set("X-User-ID", "1")

    router.ServeHTTP(w, req)

    assert.Equal(t, 302, w.Code) // Redirect after update

    // Verify user was updated (within transaction)
    var name string
    err := tx.Get(&name, `SELECT name FROM "user" WHERE id=1`)
    assert.NoError(t, err)
    assert.Equal(t, "Alice Updated", name)
  })
}
```

```go
// internal/controllers/settings_controller_test.go
package controllers

import (
  "net/http"
  "net/http/httptest"
  "strings"
  "testing"
  "github.com/gin-gonic/gin"
  "github.com/stretchr/testify/assert"
  "yourapp/internal/platform/db"
)

func TestShowSettings(t *testing.T) {
  db.SetupTestData(t)

  db.TestTx(t, func(t *testing.T, tx *sqlx.Tx) {
    router := setupRouter()

    w := httptest.NewRecorder()
    req, _ := http.NewRequest("GET", "/settings", nil)
    req.Header.Set("X-User-ID", "1") // Alice

    router.ServeHTTP(w, req)

    assert.Equal(t, 200, w.Code)
    // Alice has theme, language, timezone settings
    assert.Contains(t, w.Body.String(), "theme")
    assert.Contains(t, w.Body.String(), "dark")
    assert.Contains(t, w.Body.String(), "language")
  })
}

func TestUpdateSetting(t *testing.T) {
  db.SetupTestData(t)

  db.TestTx(t, func(t *testing.T, tx *sqlx.Tx) {
    router := setupRouter()

    w := httptest.NewRecorder()
    body := strings.NewReader(`key=theme&value=light`)
    req, _ := http.NewRequest("POST", "/settings", body)
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    req.Header.Set("X-User-ID", "1")

    router.ServeHTTP(w, req)

    assert.Equal(t, 200, w.Code)

    // Verify setting was updated (within transaction)
    var value string
    err := tx.Get(&value, `SELECT value FROM setting WHERE user_id=1 AND key='theme'`)
    assert.NoError(t, err)
    assert.Equal(t, "light", value)
  })
}

func TestDeleteSetting(t *testing.T) {
  db.SetupTestData(t)

  db.TestTx(t, func(t *testing.T, tx *sqlx.Tx) {
    router := setupRouter()

    w := httptest.NewRecorder()
    req, _ := http.NewRequest("DELETE", "/settings/theme", nil)
    req.Header.Set("X-User-ID", "1")

    router.ServeHTTP(w, req)

    assert.Equal(t, 200, w.Code)

    // Verify deletion
    var count int
    err := tx.Get(&count, `SELECT COUNT(*) FROM setting WHERE user_id=1 AND key='theme'`)
    assert.NoError(t, err)
    assert.Equal(t, 0, count)
  })
}
```

---

## Pre-commit Hooks

### Install Hook

```makefile
# Install git pre-commit hook for running tests
setup-hooks:
	@echo '#!/bin/bash\nset -e\necho "🧪 Running tests..."\nexport DATABASE_URL="postgres://app:app@localhost:5432/app?sslmode=disable"\ngo test ./... -timeout 30s\necho "✅ Tests passed"' > .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "✅ Pre-commit hook installed"
```

### Pre-commit Script

```bash
#!/bin/bash
# .git/hooks/pre-commit
set -e

echo "🧪 Running tests before commit..."

# Use existing local DB (transaction rollback, no spin-up needed)
export DATABASE_URL="postgres://app:app@localhost:5432/app?sslmode=disable"

# Run tests (fast because transaction rollback)
go test ./... -timeout 30s

# Run linter (optional)
# staticcheck ./...

echo "✅ Tests passed - committing changes"
```

---

## CI/CD Integration

### GitHub Actions

```yaml
# .github/workflows/test.yml
name: Test
on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest

    services:
      postgres:
        image: postgres:15
        env:
          POSTGRES_USER: app
          POSTGRES_PASSWORD: app
          POSTGRES_DB: app
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
        ports:
          - 5432:5432

    steps:
      - uses: actions/checkout@v3

      - uses: actions/setup-go@v4
        with:
          go-version: '1.23'

      - name: Install dependencies
        run: go mod download

      - name: Install goose
        run: go install github.com/pressly/goose/v3/cmd/goose@latest

      - name: Run migrations
        env:
          DATABASE_URL: postgres://app:app@localhost/app?sslmode=disable
        run: goose -dir migrations postgres "$DATABASE_URL" up

      - name: Load demo data
        env:
          DATABASE_URL: postgres://app:app@localhost/app?sslmode=disable
        run: psql "$DATABASE_URL" -f migrations/demo_data.sql

      - name: Run tests
        env:
          DATABASE_URL: postgres://app:app@localhost/app?sslmode=disable
        run: go test ./... -v -cover -race

      - name: Run linter
        run: |
          go install honnef.co/go/tools/cmd/staticcheck@latest
          staticcheck ./...
```

---

## Best Practices

### Do's ✅

- ✅ **Use transaction rollback** for fast, isolated tests
- ✅ **Keep demo_data.sql in sync** with schema
- ✅ **Load demo data once** (before test suite, not per test)
- ✅ **Test business logic** (validation, calculations)
- ✅ **Test database operations** (CRUD, queries)
- ✅ **Test HTTP handlers** (request/response, routing)
- ✅ **Use table-driven tests** for comprehensive coverage
- ✅ **Run tests on commit** (pre-commit hook)
- ✅ **Run tests in CI** (GitHub Actions, etc.)

### Don'ts ❌

- ❌ **Don't create separate test database** (use transaction rollback)
- ❌ **Don't use mocks for database** (test real queries)
- ❌ **Don't skip tests** (run all tests on every commit)
- ❌ **Don't ignore flaky tests** (fix or remove them)
- ❌ **Don't test framework code** (test your code, not Gin/sqlx)

### Coverage Targets

- **Unit tests**: 80%+ coverage
- **Integration tests**: Cover all CRUD operations
- **HTTP tests**: Cover all endpoints (happy path + errors)
- **Total coverage**: 70%+ (focus on critical paths)

---

## Troubleshooting

### Tests Fail: "database not found"

```bash
# Ensure local database exists
createdb app

# Ensure DATABASE_URL is set
export DATABASE_URL="postgres://app:app@localhost:5432/app?sslmode=disable"

# Run migrations
goose -dir migrations postgres "$DATABASE_URL" up
```

### Tests Fail: "relation does not exist"

```bash
# Reload demo data (schema might have changed)
make load-demo-data

# Or manually
psql "$DATABASE_URL" -f migrations/demo_data.sql
```

### Tests Are Slow

- ✅ Use transaction rollback (not separate DB)
- ✅ Load demo data once (TestMain, not per test)
- ✅ Run tests in parallel (`t.Parallel()` for independent tests)
- ✅ Use shorter timeouts (`-timeout 30s`)

### Flaky Tests

- Check for race conditions (`go test -race`)
- Ensure test isolation (transaction rollback)
- Avoid time-based assertions (use mocked time)
- Check for leftover goroutines

---

**Next:** See [security.md](security.md) for security best practices
