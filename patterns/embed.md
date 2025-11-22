# Embedding Assets (Single Binary Deployment)

> Use Go's `embed` package to bundle templates and static files into the binary. No external files needed at runtime.

---

## Table of Contents

- [Overview](#overview)
- [Directory Structure](#directory-structure)
- [Implementation](#implementation)
- [Router Setup](#router-setup)
- [Development vs Production](#development-vs-production)
- [Makefile Updates](#makefile-updates)

---

## Overview

By default, the blueprint loads templates and static files from the filesystem. For production deployment, you often want a **single binary** that contains everything.

**Benefits:**
- Single file to deploy (no `views/` or `static/` directories)
- Immutable assets (can't be modified on server)
- Simpler Docker images
- Faster startup (no filesystem reads)

---

## Directory Structure

For embed to work, `assets.go` must be in a parent directory of the files being embedded.

### Recommended Structure (for embed)

```
yourapp/
├── cmd/yourapp/main.go
├── internal/
│   ├── assets/
│   │   ├── assets.go         # Embed declarations
│   │   ├── views/            # Templates (moved here)
│   │   │   ├── layouts/
│   │   │   │   ├── base.html
│   │   │   │   └── minimal.html
│   │   │   ├── partials/
│   │   │   │   ├── _navbar.html
│   │   │   │   └── _toast.html
│   │   │   └── users/
│   │   │       ├── profile.html
│   │   │       └── edit.html
│   │   └── static/           # Static files (moved here)
│   │       ├── css/
│   │       │   ├── bootstrap.min.css
│   │       │   └── app.css
│   │       ├── js/
│   │       │   ├── htmx.min.js
│   │       │   └── bootstrap.bundle.min.js
│   │       └── img/
│   │           └── logo.svg
│   ├── controllers/
│   ├── models/
│   └── platform/
├── lang/                     # Keep at root (or embed separately)
├── migrations/
└── config/
```

---

## Implementation

### Step 1: Create Embed Declarations

```go
// internal/assets/assets.go
package assets

import (
    "embed"
    "html/template"
    "io/fs"
    "os"
    "path/filepath"
    "strings"
)

//go:embed all:views
var viewsFS embed.FS

//go:embed all:static
var staticFS embed.FS

var useEmbed = true

// SetDev enables filesystem loading for development hot-reload
func SetDev(dev bool) {
    useEmbed = !dev
}

// Templates returns parsed templates with directory-prefixed names
func Templates() *template.Template {
    tmpl := template.New("")

    dirs := []string{"layouts", "partials", "users", "settings", "errors"}

    if useEmbed {
        for _, dir := range dirs {
            pattern := "views/" + dir + "/*.html"
            files, _ := fs.Glob(viewsFS, pattern)

            for _, file := range files {
                name := strings.TrimPrefix(file, "views/")
                content, _ := fs.ReadFile(viewsFS, file)
                tmpl = template.Must(tmpl.New(name).Parse(string(content)))
            }
        }
    } else {
        // Filesystem loading for development
        for _, dir := range dirs {
            files, _ := filepath.Glob("internal/assets/views/" + dir + "/*.html")

            for _, file := range files {
                name := strings.TrimPrefix(file, "internal/assets/views/")
                content, _ := os.ReadFile(file)
                tmpl = template.Must(tmpl.New(name).Parse(string(content)))
            }
        }
    }

    return tmpl
}

// StaticFS returns the static files filesystem
func StaticFS() fs.FS {
    if useEmbed {
        sub, _ := fs.Sub(staticFS, "static")
        return sub
    }
    return os.DirFS("internal/assets/static")
}
```

### Step 2: Add Template Functions (Optional)

If you need custom template functions (for i18n, formatting, etc.):

```go
// internal/assets/assets.go

import (
    // ... existing imports
    "yourapp/internal/platform/i18n"
)

// Templates returns parsed templates with custom functions
func Templates() *template.Template {
    funcs := template.FuncMap{
        "t":      i18n.T,           // Translation function
        "substr": Substr,           // String substring
        "safe":   Safe,             // Mark HTML as safe
    }

    tmpl := template.New("").Funcs(funcs)

    // ... rest of template loading
}

func Substr(s string, start, length int) string {
    if start >= len(s) {
        return ""
    }
    end := start + length
    if end > len(s) {
        end = len(s)
    }
    return s[start:end]
}

func Safe(s string) template.HTML {
    return template.HTML(s)
}
```

---

## Router Setup

```go
// internal/controllers/router.go
package controllers

import (
    "net/http"

    "yourapp/internal/assets"
    "yourapp/internal/middleware"
    "yourapp/internal/platform/logger"

    "github.com/gin-gonic/gin"
)

func SetupRouter() *gin.Engine {
    router := gin.New()

    // Global middleware
    router.Use(gin.Recovery())
    router.Use(logger.Middleware())
    router.Use(middleware.RequestID())

    // Load embedded templates
    router.SetHTMLTemplate(assets.Templates())

    // Serve embedded static files
    staticFS := http.FS(assets.StaticFS())
    router.StaticFS("/static", staticFS)

    // ... routes
    return router
}
```

---

## Development vs Production

### main.go Setup

```go
// cmd/yourapp/main.go
package main

import (
    "yourapp/internal/assets"
    "yourapp/internal/controllers"
    "yourapp/internal/platform/config"
    "yourapp/internal/platform/logger"
    // ... other imports
)

func main() {
    cfg, err := config.Load()
    if err != nil {
        log.Fatal().Err(err).Msg("failed to load config")
    }

    logger.Init(cfg.AppEnv)

    // Use filesystem in dev for hot-reload
    if cfg.AppEnv == "dev" {
        assets.SetDev(true)
    }

    // ... database setup

    router := controllers.SetupRouter()
    router.Run("0.0.0.0:" + cfg.Port)
}
```

### Behavior

| Environment | `APP_ENV` | Template Source | Static Source |
|-------------|-----------|-----------------|---------------|
| Development | `dev` | Filesystem | Filesystem |
| Production | `prod` | Embedded | Embedded |

**Development:** Edit templates, refresh browser to see changes (no restart needed for templates, but Go code changes need restart).

**Production:** Everything baked into binary.

---

## Makefile Updates

```makefile
APP=yourapp

.PHONY: run build test

# Development: use filesystem for hot-reload
run:
	APP_ENV=dev go run ./cmd/${APP}

# Production: single binary with embedded assets
build:
	go build -o bin/${APP} ./cmd/${APP}
	# Binary is self-contained - no need to copy views/ or static/

# Verify embed works
build-check:
	go build -o /tmp/${APP} ./cmd/${APP}
	ls -lh /tmp/${APP}
	@echo "Binary contains all templates and static files"
```

---

## Gotchas

### 1. Embed Directive Location

The `//go:embed` directive must be in a file that's in a parent directory of the embedded files:

```go
// ✅ Correct: assets.go is parent of views/
// internal/assets/assets.go
//go:embed all:views
var viewsFS embed.FS

// ❌ Wrong: can't embed files outside current directory tree
// internal/controllers/router.go
//go:embed ../../../views  // This won't work!
```

### 2. Empty Directories

Go's embed ignores empty directories. If you have placeholder directories, add a `.gitkeep` file.

### 3. Hidden Files

By default, `embed` ignores files starting with `.` or `_`. Use `all:` prefix to include them:

```go
//go:embed all:views  // Includes _partial.html files
```

### 4. Template Parsing Order

Templates that reference other templates (via `{{ template "partials/_navbar.html" . }}`) must have the referenced template already parsed. The loading order in `Templates()` handles this by loading all templates into the same `*template.Template`.

---

## Migration from Filesystem

If you have an existing project with `views/` and `static/` at the root:

1. Create `internal/assets/` directory
2. Move `views/` to `internal/assets/views/`
3. Move `static/` to `internal/assets/static/`
4. Create `internal/assets/assets.go` (code above)
5. Update `internal/controllers/router.go` to use `assets.Templates()` and `assets.StaticFS()`
6. Update any hardcoded paths in templates (e.g., `/static/css/app.css` stays the same)

---

**Next:** [MVC Pattern](mvc.md) | [Deployment Guide](deployment.md)
