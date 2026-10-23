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
│   │   ├── templates.go      # One template set per page
│   │   ├── funcs.go          # t, dict, csrf, substr
│   │   ├── render.go         # Gin's HTMLRender
│   │   ├── input.css         # Tailwind source: outside static/, never embedded
│   │   ├── views/            # Templates, any depth
│   │   │   ├── layouts/
│   │   │   │   ├── base.html
│   │   │   │   ├── mobile_base.html
│   │   │   │   └── minimal.html
│   │   │   ├── partials/
│   │   │   │   ├── _navbar.html
│   │   │   │   ├── _flash.html
│   │   │   │   ├── _toasts.html
│   │   │   │   ├── _modal.html
│   │   │   │   └── _icon.html
│   │   │   ├── settings/
│   │   │   │   ├── index.html
│   │   │   │   └── _setting_row.html
│   │   │   └── profiles/
│   │   │       ├── desktop/profile.html
│   │   │       └── mobile/profile.html
│   │   └── static/
│   │       ├── css/
│   │       │   └── app.css   # built by `make css`, committed
│   │       ├── js/
│   │       │   ├── htmx.min.js
│   │       │   ├── app.js
│   │       │   ├── alpine-focus.min.js
│   │       │   └── alpine.min.js   # @alpinejs/csp
│   │       └── img/
│   │           └── logo.svg
│   ├── controllers/
│   ├── models/
│   └── platform/
├── lang/                     # Keep at root (or embed separately)
├── migrations/
└── config/
```

The stylesheet and scripts are described in [frontend.md](frontend.md#file-structure);
`make css` and `make assets` produce them.

---

## Implementation

Four small files. Templates are named by their path under `views/`
(`settings/index.html`, `profiles/mobile/profile.html`), at any depth.

### Step 1: Create Embed Declarations

```go
// internal/assets/assets.go
package assets

import (
	"embed"
	"io/fs"
	"os"
)

//go:embed all:views
var viewsFS embed.FS

//go:embed all:static
var staticFS embed.FS

// devDir is the asset tree's path from the repository root, where
// `make run` starts the binary.
const devDir = "internal/assets"

var useEmbed = true

// SetDev reads templates and static files from disk instead of the binary.
// Call it before NewRenderer.
func SetDev(dev bool) { useEmbed = !dev }

// views returns the template tree, with paths relative to views/.
func views() fs.FS {
	if !useEmbed {
		return os.DirFS(devDir + "/views")
	}
	sub, err := fs.Sub(viewsFS, "views")
	if err != nil {
		panic(err) // only an invalid literal path fails here
	}
	return sub
}

// StaticFS returns the static files, with paths relative to static/.
func StaticFS() fs.FS {
	if !useEmbed {
		return os.DirFS(devDir + "/static")
	}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
```

### Step 2: Add Template Functions (Optional)

`t`, `dict` and `csrf` are not optional: every template in
[frontend.md](frontend.md) and [htmx.md](htmx.md) calls them. Add your own
helpers beside them.

```go
// internal/assets/funcs.go
package assets

import (
	"errors"
	"fmt"
	"html/template"

	"yourapp/internal/platform/buildinfo"
	"yourapp/internal/platform/i18n"
)

func funcs() template.FuncMap {
	return template.FuncMap{
		"t":      i18n.T,        // {{ t .lang "key" }}, core/i18n.md
		"dict":   dict,          // several values into a partial, htmx.md
		"csrf":   boundCSRF(""), // replaced per render by Templates.Execute
		"asset":  asset,         // {{ asset "css/app.css" }}, core/performance.md
		"substr": substr,
	}
}

var errNoCSRF = errors.New("csrf: no token for this request; is middleware.CSRF installed?")

// boundCSRF returns the csrf function for one render. A page rendered
// outside the CSRF middleware fails loudly instead of shipping forms that
// can never be submitted.
func boundCSRF(token string) func() (string, error) {
	return func() (string, error) {
		if token == "" {
			return "", errNoCSRF
		}
		return token, nil
	}
}

// dict builds a map from key/value pairs so a partial can receive more than
// one value: {{ template "x.html" (dict "lang" $.lang "setting" .) }}.
func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of arguments")
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k, ok := kv[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %v is not a string", kv[i])
		}
		m[k] = kv[i+1]
	}
	return m, nil
}

// asset is a static file's URL, stamped with the build so browsers can keep it
// for a year (core/performance.md#static-files).
func asset(name string) string {
	return "/static/" + name + "?v=" + buildinfo.Commit
}

// substr counts runes, not bytes, so "Élise" gives "É" rather than half of it.
func substr(s string, start, length int) string {
	r := []rune(s)
	if start < 0 || start >= len(r) || length <= 0 {
		return ""
	}
	end := min(start+length, len(r))
	return string(r[start:end])
}
```

There is deliberately no `safe` helper. One that turns any string into
`template.HTML` lets a template mark user input as trusted; build trusted HTML
in Go instead ([security.md](core/security.md#xss-prevention)).

### Step 3: One Template Set per Page

Every page defines blocks with the same names: `title`, `content`. Parse all
pages into one `*template.Template` and each page's `{{ define "content" }}`
replaces the previous one, so every URL renders whichever page was parsed
last. The standard Go fix is one set per page: parse the layouts and partials
once, then `Clone` that base for each page and add only that page to the clone.

```go
// internal/assets/templates.go
package assets

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path"
	"strings"
	"text/template/parse"
)

// The layout a page gets when it does not call one itself.
const (
	desktopLayout = "layouts/base.html"
	mobileLayout  = "layouts/mobile_base.html" // mobile-navigation.md
)

// entry is one renderable name: the set it lives in and the template to
// execute there. set is never executed itself, only cloned, because
// html/template refuses to Clone a set that has run.
type entry struct {
	set  *template.Template
	exec string
}

// Templates holds every renderable template, keyed by its path under views/.
type Templates struct {
	entries map[string]entry
}

// shared reports whether a file belongs in every page's set: layouts, and
// partials (a "_" prefix) wherever they live. Shared files must not define
// blocks of their own, or they collide with the pages that do.
func shared(name string) bool {
	return strings.HasPrefix(name, "layouts/") || strings.HasPrefix(path.Base(name), "_")
}

// Load walks the whole views tree, at any depth, and builds one template set
// per page: the shared layouts and partials plus that page alone.
func Load(fsys fs.FS) (*Templates, error) {
	var common, pages []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".html" {
			return err
		}
		if shared(p) {
			common = append(common, p)
		} else {
			pages = append(pages, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk views: %w", err)
	}

	base := template.New("").Funcs(funcs())
	for _, name := range common {
		if err := parseFile(base, fsys, name); err != nil {
			return nil, err
		}
	}

	t := &Templates{entries: make(map[string]entry, len(common)+len(pages))}
	for _, name := range common {
		t.entries[name] = entry{set: base, exec: name}
	}
	for _, name := range pages {
		set, err := base.Clone()
		if err != nil {
			return nil, err
		}
		if err := parseFile(set, fsys, name); err != nil {
			return nil, err
		}
		// A page made only of {{ define }} blocks is wrapped in the default
		// layout for its path. A page that calls a layout itself
		// ({{ template "layouts/minimal.html" . }}) is executed as written.
		exec := name
		if page := set.Lookup(name); page.Tree == nil || parse.IsEmptyTree(page.Tree.Root) {
			exec = layoutFor(name)
			if set.Lookup(exec) == nil {
				return nil, fmt.Errorf("%s: layout %s not found", name, exec)
			}
		}
		t.entries[name] = entry{set: set, exec: exec}
	}
	return t, nil
}

func parseFile(set *template.Template, fsys fs.FS, name string) error {
	src, err := fs.ReadFile(fsys, name)
	if err != nil {
		return err
	}
	if _, err := set.New(name).Parse(string(src)); err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}
	return nil
}

// layoutFor picks the mobile layout for anything under a mobile/ directory.
func layoutFor(name string) string {
	if strings.Contains("/"+name, "/mobile/") {
		return mobileLayout
	}
	return desktopLayout
}

// Has reports whether name can be rendered.
func (t *Templates) Has(name string) bool {
	_, ok := t.entries[name]
	return ok
}

// Execute renders name with data. It clones the page's set so that csrf can
// return this request's token (security.md#csrf-protection); the clone costs
// one escaping pass per render.
func (t *Templates) Execute(w io.Writer, name string, data any, csrfToken string) error {
	e, ok := t.entries[name]
	if !ok {
		return fmt.Errorf("template %q not found", name)
	}
	set, err := e.set.Clone()
	if err != nil {
		return err
	}
	set.Funcs(template.FuncMap{"csrf": boundCSRF(csrfToken)})
	return set.ExecuteTemplate(w, e.exec, data)
}
```

What decides where a file goes:

| File | Set | Executed as |
|------|-----|-------------|
| `layouts/*.html` | shared base | (called by pages) |
| `**/_*.html` (partials, any directory) | shared base | itself, for HTMX swaps |
| a page made only of `{{ define }}` blocks | its own clone | `layouts/base.html`, or `layouts/mobile_base.html` under a `mobile/` directory |
| a page that calls a layout itself | its own clone | itself |

So `settings/index.html` from [htmx.md](htmx.md#full-page) needs no layout
line, and an error page can opt into another layout:

```html
<!-- internal/assets/views/errors/error.html -->
{{ template "layouts/minimal.html" . }}
{{ define "content" }}<p>{{ .message }}</p>{{ end }}
```

### Step 4: The Renderer

Gin calls `HTMLRender.Instance` for every `c.HTML`. This one also hands each
render the request's CSRF token: Gin passes an `HTMLRender` the response
writer but never the `*gin.Context`, so `middleware.CSRF` attaches the token to
the writer ([security.md](core/security.md#csrf-protection)).

```go
// internal/assets/render.go
package assets

import (
	"bytes"
	"net/http"

	"github.com/gin-gonic/gin/render"
)

// Renderer is Gin's HTMLRender: c.HTML(status, "settings/index.html", data)
// arrives here.
type Renderer struct {
	tmpl *Templates // nil in dev: the tree is re-read on every render
}

// NewRenderer parses every template once, so a broken template fails at
// startup in every environment, dev included.
func NewRenderer() (*Renderer, error) {
	tmpl, err := Load(views())
	if err != nil {
		return nil, err
	}
	if !useEmbed {
		return &Renderer{}, nil
	}
	return &Renderer{tmpl: tmpl}, nil
}

func (r *Renderer) templates() (*Templates, error) {
	if r.tmpl != nil {
		return r.tmpl, nil
	}
	return Load(views())
}

// Has reports whether name exists, for callers choosing between variants.
func (r *Renderer) Has(name string) bool {
	t, err := r.templates()
	return err == nil && t.Has(name)
}

// Instance implements render.HTMLRender.
func (r *Renderer) Instance(name string, data any) render.Render {
	return page{r: r, name: name, data: data}
}

type page struct {
	r    *Renderer
	name string
	data any
}

func (p page) WriteContentType(w http.ResponseWriter) {
	if h := w.Header(); h.Get("Content-Type") == "" {
		h.Set("Content-Type", "text/html; charset=utf-8")
	}
}

// Render executes into a buffer, so a template error writes nothing and
// error-handling middleware can still answer 500 instead of half a page.
func (p page) Render(w http.ResponseWriter) error {
	p.WriteContentType(w)
	t, err := p.r.templates()
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, p.name, p.data, csrfToken(w)); err != nil {
		return err
	}
	_, err = buf.WriteTo(w)
	return err
}

// csrfToken reads the token middleware.CSRF attached to the response
// writer. Gin hands an HTMLRender the writer but never the *gin.Context.
// Other middleware may wrap the writer again (profiling, deployment.md), so
// it follows Unwrap down the chain.
func csrfToken(w http.ResponseWriter) string {
	for {
		switch v := w.(type) {
		case interface{ CSRFToken() string }:
			return v.CSRFToken()
		case interface{ Unwrap() http.ResponseWriter }:
			w = v.Unwrap()
		default:
			return ""
		}
	}
}
```

Wrapping the renderer is fine: the request profiler in
[deployment.md](core/deployment.md) sets `router.HTMLRender` to
`profile.HTML{HTMLRender: renderer}`. A middleware that wraps the response
writer should give its wrapper an `Unwrap() http.ResponseWriter` method, the
`net/http` convention, so the token is still found beneath it.

Cloning per render costs one escaping pass, tens of microseconds for a typical
page. That is the price of a `{{ csrf }}` bound to the session; it is far
below a database query.

---

## Router Setup

```go
// internal/controllers/router.go
package controllers

import (
	"net/http"
	"os"

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

	renderer, err := assets.NewRenderer()
	if err != nil {
		panic(err) // a template that does not parse is a build error
	}
	router.HTMLRender = renderer // not SetHTMLTemplate/LoadHTMLGlob: they replace it

	// Registered before the CSRF middleware, so static files never get it.
	// CacheStatic lets browsers keep versioned URLs a year (core/performance.md).
	router.Group("/static", assets.CacheStatic()).StaticFS("/", http.FS(assets.StaticFS()))

	// Every page renders {{ csrf }}, so every HTML route needs a token.
	router.Use(middleware.CSRF([]byte(os.Getenv("CSRF_SECRET")), os.Getenv("APP_URL")))

	// ... routes
	return router
}
```

Gin applies `router.Use` only to routes registered after it, which is why the
static route comes first.

`Renderer.Has` answers "does this template exist?" for code choosing between
variants, such as a mobile page with a desktop fallback
([mobile-navigation.md](mobile-navigation.md#template-structure)).

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

    // Use filesystem in dev for hot-reload. Before SetupRouter, which
    // builds the renderer.
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

**Development:** Edit a template, refresh the browser: the renderer re-reads
`views/` on every render. Run `make css-watch` beside `make run` and a
stylesheet change shows the same way ([frontend.md](frontend.md#the-build-step)).
Go code changes still need a restart. Startup parses every template in dev too,
so a syntax error fails immediately rather than on the first request.

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
// Correct: assets.go is parent of views/
// internal/assets/assets.go
//go:embed all:views
var viewsFS embed.FS

// Wrong: can't embed files outside current directory tree
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

### 4. Shared Block Names

Never parse every page into one `*template.Template`. Each page's
`{{ define "content" }}` overwrites the last, and every route renders the page
parsed last. [Step 3](#step-3-one-template-set-per-page) keeps one set per
page. Two consequences:

- **Partials must not `{{ define }}` blocks.** They are in every page's set,
  so a block they define collides like a page's would.
- **Layouts share block defaults.** `base.html` and `mobile_base.html` both
  declare `{{ block "title" . }}`; the shared set keeps one default, so keep
  the defaults identical.

### 5. Directory Depth

Load with `fs.WalkDir`, not `fs.Glob("views/*/*.html")`. A glob stops at one
level, so `profiles/mobile/profile.html` is never parsed and nothing reports
it: the mobile page is silently never chosen.

### 6. Clone Before Execute

`html/template` refuses to `Clone` a set that has been executed. The base and
per-page sets are only ever cloned, never executed; each render executes its
own clone.

---

## Migration from Filesystem

If you have an existing project with `views/` and `static/` at the root:

1. Create `internal/assets/` directory
2. Move `views/` to `internal/assets/views/`
3. Move `static/` to `internal/assets/static/`
4. Create `internal/assets/assets.go` (code above)
5. Update `internal/controllers/router.go` to set `router.HTMLRender` from `assets.NewRenderer()` and serve `assets.StaticFS()` behind `assets.CacheStatic()`
6. Write static URLs in templates with `asset` (`/static/css/app.css` becomes `{{ asset "css/app.css" }}`; see [core/performance.md](core/performance.md#static-files))

---

**Next:** [MVC Pattern](core/mvc.md) | [Deployment Guide](core/deployment.md)
