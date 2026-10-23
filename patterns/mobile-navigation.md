# Mobile Navigation

> Device-aware rendering: one handler, a desktop template and an optional
> mobile one, styled by the same Tailwind build.

Read [frontend.md](frontend.md) first. The Alpine components, icon partial,
CSP rules and accessibility table used here are defined there.

---

## Table of Contents

- [Philosophy](#philosophy)
- [Device Detection](#device-detection)
- [Template Structure](#template-structure)
- [Mobile Base Layout](#mobile-base-layout)
- [Navigation Components](#navigation-components)
- [Safe Areas](#safe-areas)
- [Controller Integration](#controller-integration)
- [Testing](#testing)
- [Checklist for New Mobile Templates](#checklist-for-new-mobile-templates)

---

## Philosophy

1. **Detect the device** from the User-Agent, with a `?device=` override for
   testing.
2. **Render the matching template** with `devices.HTML`, which falls back to
   the desktop template when there is no mobile one.
3. **Same handler, same data, different presentation.**
4. **HTMX and Alpine work identically on both.**

Rules:

- A mobile template is optional. Most pages need only responsive prefixes
  (`md:`, `lg:`) on the desktop template.
- Write a separate mobile template only when the **structure** differs — a
  bottom tab bar instead of a top navbar, a sheet instead of a sidebar.
- One stylesheet for both trees. There is no `mobile.css`.

---

## Device Detection

```
internal/platform/devices/
├── devices.go         # Detect, Middleware, HTML
└── devices_test.go
```

Priority, highest first:

1. **Query parameter** — `?device=mobile` or `?device=desktop`, remembered in a
   cookie so the next click keeps it. `?device=auto` clears it.
2. **Cookie** — a previous override.
3. **User-Agent** — automatic.

```go
// internal/platform/devices/devices.go
package devices

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type Type string

const (
	Mobile  Type = "mobile"
	Desktop Type = "desktop"
)

const (
	ctxKey     = "devices.state"
	cookieName = "device"
	cookieAge  = 30 * 24 * 60 * 60
)

// Templates reports whether a template exists. *assets.Renderer satisfies it
// (embed.md); tests pass a stub.
type Templates interface {
	Has(name string) bool
}

type state struct {
	device Type
	tmpl   Templates
}

// Detect classifies a User-Agent. It follows MDN's advice — look for "Mobi" —
// which matches phones and not tablets. Tablets get the desktop layout,
// which suits their width.
func Detect(userAgent string) Type {
	if strings.Contains(userAgent, "Mobi") || strings.Contains(userAgent, "Opera Mini") {
		return Mobile
	}
	return Desktop
}

func parse(s string) (Type, bool) {
	switch t := Type(s); t {
	case Mobile, Desktop:
		return t, true
	}
	return "", false
}

// Middleware decides the device once per request. tmpl is the renderer the
// engine uses, so HTML can check a mobile template exists.
func Middleware(tmpl Templates) gin.HandlerFunc {
	return func(c *gin.Context) {
		device := Detect(c.GetHeader("User-Agent"))

		if q := c.Query("device"); q != "" {
			c.SetSameSite(http.SameSiteLaxMode)
			if t, ok := parse(q); ok {
				device = t
				c.SetCookie(cookieName, string(t), cookieAge, "/", "", true, true)
			} else if q == "auto" {
				c.SetCookie(cookieName, "", -1, "/", "", true, true)
			}
		} else if v, err := c.Cookie(cookieName); err == nil {
			if t, ok := parse(v); ok {
				device = t
			}
		}

		c.Set(ctxKey, &state{device: device, tmpl: tmpl})
		c.Next()
	}
}

func get(c *gin.Context) *state {
	if v, ok := c.Get(ctxKey); ok {
		if s, ok := v.(*state); ok {
			return s
		}
	}
	return &state{device: Desktop}
}

// IsMobile reports whether this request gets mobile templates.
func IsMobile(c *gin.Context) bool { return get(c).device == Mobile }

// HTML renders name, swapping its "/desktop/" segment for "/mobile/" on a
// mobile device when that template exists. It adds device and isMobile to data.
func HTML(c *gin.Context, status int, name string, data gin.H) {
	s := get(c)
	if s.device == Mobile && s.tmpl != nil {
		if alt := strings.Replace(name, "/desktop/", "/mobile/", 1); alt != name && s.tmpl.Has(alt) {
			name = alt
		}
	}
	if data == nil {
		data = gin.H{}
	}
	data["device"] = string(s.device)
	data["isMobile"] = s.device == Mobile
	c.Writer.Header().Add("Vary", "User-Agent")
	c.HTML(status, name, data)
}
```

Wire it with the renderer the engine uses:

```go
// internal/controllers/router.go
renderer, err := assets.NewRenderer()
if err != nil {
	panic(err) // a template that does not parse is a build error
}
router.HTMLRender = renderer
router.Use(devices.Middleware(renderer))
```

The device lives in the Gin context, not in a package variable, and
`devices.HTML` reads it from there; there is no global state to set up in
tests.

---

## Template Structure

```
internal/assets/views/
├── layouts/
│   ├── base.html                 # desktop layout (frontend.md)
│   └── mobile_base.html          # mobile layout
├── partials/
│   ├── _navbar.html              # desktop navbar
│   ├── _mobile_header.html       # mobile top bar
│   ├── _mobile_nav.html          # mobile bottom tabs + "More" sheet
│   └── _icon.html                # inline SVG icons, shared
├── profiles/
│   ├── desktop/profile.html
│   └── mobile/profile.html
└── dashboard/
    ├── desktop/index.html
    └── mobile/index.html
```

`devices.HTML(c, 200, "profiles/desktop/profile.html", data)` on a phone
renders `profiles/mobile/profile.html` if it exists, otherwise the desktop
file.

**The template loader must walk `views/` recursively.** `desktop/` and
`mobile/` are a level deeper than a loader that globs `views/<dir>/*.html`
sees, and a mobile template that was never parsed is silently never chosen —
`Has` returns false and every phone gets the desktop page. The loader in
[embed.md](embed.md#step-3-one-template-set-per-page) walks `views/` with
`fs.WalkDir`, names each template by its path under `views/`, and wraps a
page under `mobile/` in `layouts/mobile_base.html` unless it calls a layout
itself.

---

## Mobile Base Layout

```html
<!-- internal/assets/views/layouts/mobile_base.html -->
<!DOCTYPE html>
<html lang="{{ .lang }}" dir="{{ if .isRTL }}rtl{{ else }}ltr{{ end }}"
      {{ if .dark }}class="dark"{{ end }}>
<head>
  <meta charset="utf-8">
  <!-- viewport-fit=cover is required for env(safe-area-inset-*) to report
       real values on notched iPhones. Without it, every inset is 0. -->
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
  <meta name="htmx-config"
        content='{"includeIndicatorStyles":false,"allowEval":false,"responseHandling":[{"code":"204","swap":false},{"code":"[23]..","swap":true},{"code":"422","swap":true},{"code":"[45]..","swap":false,"error":true}]}'>
  <title>{{ block "title" . }}{{ t .lang "app.name" }}{{ end }}</title>

  <link href="{{ asset "css/app.css" }}" rel="stylesheet">
  <script defer src="{{ asset "js/htmx.min.js" }}"></script>
  <script defer src="{{ asset "js/app.js" }}"></script>
  <script defer src="{{ asset "js/alpine-focus.min.js" }}"></script>
  <script defer src="{{ asset "js/alpine.min.js" }}"></script>
</head>
<body class="bg-slate-50 text-slate-900 dark:bg-slate-900 dark:text-slate-100"
      hx-headers='{"X-CSRF-Token": "{{ csrf }}"}'
      data-error-message="{{ t .lang "errors.request_failed" }}">

  {{ template "partials/_mobile_header.html" . }}

  <!-- Padding clears the fixed header and tab bar, plus the iOS insets. -->
  <main id="main" class="px-4 pt-[calc(3.5rem_+_var(--inset-top))]
                         pb-[calc(3.5rem_+_var(--inset-bottom))]">
    {{ template "partials/_flash.html" . }}
    {{ block "content" . }}{{ end }}
  </main>

  {{ template "partials/_mobile_nav.html" . }}
  {{ template "partials/_toasts.html" . }}
</body>
</html>
```

The `<head>` matches the desktop layout line for line — same stylesheet, same
scripts, same HTMX configuration. If they drift, HTMX error handling or the
CSP behaves differently on phones. The underscores in the arbitrary `calc()`
values are Tailwind's spaces; CSS requires spaces around `+`.

---

## Navigation Components

### Icons

Inline SVG in one partial, chosen by name. An icon webfont costs a
render-blocking request and ships hundreds of glyphs to deliver five.
`html/template` cannot choose a template by a variable name, so the partial
branches on its argument instead:

```html
<!-- internal/assets/views/partials/_icon.html -->
{{ if eq . "home" }}
<svg class="size-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
  <path stroke-linecap="round" stroke-linejoin="round" d="m2.25 12 8.954-8.955c.44-.439 1.152-.439 1.591 0L21.75 12M4.5 9.75v10.125c0 .621.504 1.125 1.125 1.125H9.75v-4.875c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125.504 1.125 1.125V21h4.125c.621 0 1.125-.504 1.125-1.125V9.75M8.25 21h8.25"/>
</svg>
{{ else if eq . "search" }}
<svg class="size-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
  <path stroke-linecap="round" stroke-linejoin="round" d="m21 21-5.197-5.197m0 0A7.5 7.5 0 1 0 5.196 5.196a7.5 7.5 0 0 0 10.607 10.607Z"/>
</svg>
{{ else if eq . "menu" }}
<svg class="size-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
  <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5"/>
</svg>
{{ else if eq . "close" }}
<svg class="size-6" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18 18 6M6 6l12 12"/>
</svg>
{{ else if eq . "chevron-start" }}
<svg class="size-6 rtl:rotate-180" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
  <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 19.5 8.25 12l7.5-7.5"/>
</svg>
{{ end }}
```

Add one branch per icon (`user`, `login`, `help`, `logout`, …). Every icon is
`aria-hidden="true"`; the accessible name is on the surrounding link or button.
`chevron-start` mirrors in RTL with `rtl:rotate-180` because "back" points the
other way there.

### Mobile header

```html
<!-- internal/assets/views/partials/_mobile_header.html -->
<header class="fixed inset-x-0 top-0 z-40 border-b border-slate-200 bg-white
               pt-(--inset-top) dark:border-slate-700 dark:bg-slate-800">
  <div class="flex h-14 items-center gap-2 px-2">
    {{ if .backHref }}
    <a href="{{ .backHref }}"
       class="grid size-12 place-items-center rounded-full hover:bg-slate-100 dark:hover:bg-slate-700">
      <span class="sr-only">{{ t .lang "nav.back" }}</span>
      {{ template "partials/_icon.html" "chevron-start" }}
    </a>
    {{ else }}
    <div class="size-12"></div>
    {{ end }}

    <h1 class="flex-1 truncate text-center text-base font-semibold">
      {{ if .pageTitle }}{{ .pageTitle }}{{ else }}{{ t .lang "app.name" }}{{ end }}
    </h1>

    <div class="size-12"></div>
  </div>
</header>
```

Back is a link to a real URL the handler supplies in `backHref`, not
`history.back()`: an inline `onclick` is blocked by `script-src 'self'`, and a
link works without JavaScript and when the page was opened directly. The empty
`size-12` spacers keep the title optically centred. 48px is above the 44px
minimum touch target and on the 8px grid. `pageTitle` is already translated by
the handler.

### Bottom navigation

The tabs come from Go, so active state is decided once, server-side:

```go
// internal/controllers/nav.go
package controllers

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// NavItem is one bottom tab. Label is a catalog key, translated in the template.
type NavItem struct {
	Href, Icon, Label string
	Active            bool
}

func mobileNav(c *gin.Context) []NavItem {
	path := c.Request.URL.Path
	items := []NavItem{
		{Href: "/", Icon: "home", Label: "nav.home", Active: path == "/"},
		{Href: "/search", Icon: "search", Label: "nav.find", Active: strings.HasPrefix(path, "/search")},
	}
	if _, signedIn := c.Get("user_id"); signedIn {
		items = append(items, NavItem{Href: "/dashboard", Icon: "user", Label: "nav.profile",
			Active: strings.HasPrefix(path, "/dashboard")})
	} else {
		items = append(items, NavItem{Href: "/login", Icon: "login", Label: "nav.login",
			Active: path == "/login"})
	}
	return items
}
```

```html
<!-- internal/assets/views/partials/_mobile_nav.html -->
<nav x-data="disclosure" aria-label="{{ t .lang "nav.primary" }}"
     class="fixed inset-x-0 bottom-0 z-40 border-t border-slate-200 bg-white
            pb-(--inset-bottom) dark:border-slate-700 dark:bg-slate-800">
  <div class="grid grid-cols-4">
    {{ range .nav }}
    <a href="{{ .Href }}" {{ if .Active }}aria-current="page"{{ end }}
       class="flex min-h-14 flex-col items-center justify-center gap-1 text-xs
              {{ if .Active }}text-brand-600{{ else }}text-slate-600 dark:text-slate-300{{ end }}">
      {{ template "partials/_icon.html" .Icon }}
      <span>{{ t $.lang .Label }}</span>
    </a>
    {{ end }}

    <button type="button" @click="toggle" :aria-expanded="open" aria-controls="more-menu"
            class="flex min-h-14 flex-col items-center justify-center gap-1 text-xs
                   text-slate-600 dark:text-slate-300">
      {{ template "partials/_icon.html" "menu" }}
      <span>{{ t .lang "nav.more" }}</span>
    </button>
  </div>

  <!-- Bottom sheet -->
  <div x-show="open" x-cloak class="fixed inset-0 z-50">
    <div class="absolute inset-0 bg-black/50" @click="close" aria-hidden="true"
         x-transition.opacity></div>

    <div id="more-menu" role="dialog" aria-modal="true" aria-label="{{ t .lang "nav.more" }}"
         @keydown.escape.window="close" x-trap.noscroll="open"
         x-transition:enter-start="translate-y-full"
         x-transition:leave-end="translate-y-full"
         class="absolute inset-x-0 bottom-0 max-h-[50vh] overflow-y-auto rounded-t-2xl
                bg-white pb-(--inset-bottom) transition dark:bg-slate-800">
      <div class="mx-auto my-4 h-1 w-12 rounded-full bg-slate-300" aria-hidden="true"></div>

      <a href="/help" class="flex min-h-14 items-center gap-4 px-6 hover:bg-slate-50
                             dark:hover:bg-slate-700">
        {{ template "partials/_icon.html" "help" }} {{ t .lang "nav.help" }}
      </a>
      {{ if .user }}
      <form action="/logout" method="POST">
        <input type="hidden" name="csrf_token" value="{{ csrf }}">
        <button type="submit"
                class="flex min-h-14 w-full items-center gap-4 px-6 text-start text-red-600
                       hover:bg-slate-50 dark:hover:bg-slate-700">
          {{ template "partials/_icon.html" "logout" }} {{ t .lang "nav.logout" }}
        </button>
      </form>
      {{ end }}
    </div>
  </div>
</nav>
```

Inside `range`, `.` is the `NavItem`; the page's language is `$.lang`.

`x-trap.noscroll` needs the Alpine Focus plugin, loaded before Alpine core
([frontend.md](frontend.md#accessibility)). Without it, keyboard focus escapes
behind the open sheet and the page scrolls under it. Every Alpine value here is
a name (`toggle`, `open`, `close`), as the CSP build requires
([frontend.md](frontend.md#alpinejs-and-the-csp)).

Logout is a POST form, not a link: a GET logout is triggerable by any image tag
on any page.

### Navigation rules

| Rule | Guideline |
|------|-----------|
| Max tabs | 4–5 including More |
| Icons | Inline SVG, `aria-hidden="true"` |
| Labels | One word, from the catalog |
| Touch target | `min-h-14` (56px); never below 44px |
| Active state | `text-brand-600` **and** `aria-current="page"` |
| Inactive state | `text-slate-600` or darker; `slate-500` fails contrast at `text-xs` |

Colour alone cannot signal the active tab; `aria-current` is what a screen
reader announces.

---

## Safe Areas

The only mobile styling Tailwind utilities do not cover is the iOS safe area.
Define the insets once as custom properties:

```css
/* internal/assets/input.css (continued) */
@layer base {
  :root {
    --inset-top: env(safe-area-inset-top, 0px);
    --inset-bottom: env(safe-area-inset-bottom, 0px);
  }
}
```

and use them through Tailwind's variable shorthand:

```html
<nav class="pb-(--inset-bottom)">…</nav>
```

Two rules:

1. `viewport-fit=cover` in the viewport meta, or every inset reports `0px`.
2. Pad **fixed** elements (header, tab bar, bottom sheet) with the inset, and
   pad `<main>` by the element height *plus* the inset. Skip this and the home
   indicator sits on top of the tab bar.

Everything else — headers, cards, empty states, list rows — is utilities on
the element. Do not reintroduce a mobile stylesheet; a second source of truth
for layout is how the mobile and desktop trees drift apart.

---

## Controller Integration

Every page needs the same layout data, so handlers render through one helper:

```go
// internal/controllers/view.go
package controllers

import (
	"github.com/gin-gonic/gin"

	"yourapp/internal/platform/devices"
	"yourapp/internal/platform/i18n"
)

// view renders a device-aware page with the data every layout reads.
func view(c *gin.Context, status int, name string, data gin.H) {
	if data == nil {
		data = gin.H{}
	}
	lang := i18n.GetLang(c)
	data["lang"] = lang
	data["isRTL"] = i18n.IsRTL(lang)
	data["nav"] = mobileNav(c)
	devices.HTML(c, status, name, data)
}
```

```go
// internal/controllers/profiles.go
func ShowProfile(c *gin.Context) {
	profile, err := models.FindProfileBySlug(c.Request.Context(), c.Param("slug"))
	if err != nil {
		renderError(c, err)
		return
	}
	view(c, http.StatusOK, "profiles/desktop/profile.html", gin.H{
		"profile":   profile,
		"pageTitle": profile.Name,
		"backHref":  "/search",
	})
}
```

### Device-specific data, when needed

```go
func Dashboard(c *gin.Context) {
	limit := 20
	if devices.IsMobile(c) {
		limit = 6 // fewer images on a phone: less to download, less to scroll
	}
	images, err := models.RecentImages(c.Request.Context(), c.GetInt64("user_id"), limit)
	if err != nil {
		renderError(c, err)
		return
	}
	view(c, http.StatusOK, "dashboard/desktop/index.html", gin.H{"images": images})
}
```

HTMX partials (`_row.html` and friends) are normally shared by both trees and
rendered with `c.HTML` as in [htmx.md](htmx.md). A partial that genuinely
differs on mobile follows the same `desktop/`/`mobile/` convention and is
rendered with `devices.HTML`.

### Viewing the other layout

```
http://localhost:8000/profiles/jamie?device=mobile    # mobile on a desktop browser
http://localhost:8000/profiles/jamie?device=desktop   # desktop on a phone
http://localhost:8000/profiles/jamie?device=auto      # back to detection
```

---

## Testing

### Unit tests

```go
// internal/platform/devices/devices_test.go
package devices

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
)

func TestDetect(t *testing.T) {
	cases := []struct {
		name, ua string
		want     Type
	}{
		{"iPhone", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1", Mobile},
		{"Android phone", "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Mobile Safari/537.36", Mobile},
		{"Android tablet", "Mozilla/5.0 (Linux; Android 14; SM-X710) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36", Desktop},
		{"desktop Chrome", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36", Desktop},
		{"empty", "", Desktop},
	}
	for _, tc := range cases {
		if got := Detect(tc.ua); got != tc.want {
			t.Errorf("%s: Detect = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// stub stands in for *assets.Renderer: a plain template set that can say
// which names exist, so the test needs no views tree.
type stub struct{ *template.Template }

func (s stub) Has(name string) bool { return s.Lookup(name) != nil }

func newRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	tmpl := template.Must(template.New("").Parse(
		`{{ define "p/desktop/x.html" }}desktop{{ end }}` +
			`{{ define "p/mobile/x.html" }}mobile{{ end }}` +
			`{{ define "p/desktop/only.html" }}desktop{{ end }}`))
	r := gin.New()
	r.HTMLRender = render.HTMLProduction{Template: tmpl}
	r.Use(Middleware(stub{tmpl}))
	r.GET("/x", func(c *gin.Context) { HTML(c, http.StatusOK, "p/desktop/x.html", nil) })
	r.GET("/only", func(c *gin.Context) { HTML(c, http.StatusOK, "p/desktop/only.html", nil) })
	return r
}

const desktopUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/120.0 Safari/537.36"

func TestQueryOverrideIsRemembered(t *testing.T) {
	r := newRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x?device=mobile", nil)
	req.Header.Set("User-Agent", desktopUA)
	r.ServeHTTP(w, req)
	if w.Body.String() != "mobile" {
		t.Fatalf("?device=mobile rendered %q", w.Body.String())
	}

	// The next request carries the cookie and no query parameter.
	next := httptest.NewRequest(http.MethodGet, "/x", nil)
	next.Header.Set("User-Agent", desktopUA)
	for _, ck := range w.Result().Cookies() {
		next.AddCookie(ck)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, next)
	if w.Body.String() != "mobile" {
		t.Fatalf("override not remembered: rendered %q", w.Body.String())
	}
}

func TestMissingMobileTemplateFallsBackToDesktop(t *testing.T) {
	w := httptest.NewRecorder()
	newRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/only?device=mobile", nil))
	if w.Code != http.StatusOK || w.Body.String() != "desktop" {
		t.Fatalf("got %d %q, want 200 desktop", w.Code, w.Body.String())
	}
}
```

The visual-rule tests in [frontend.md](frontend.md#testing-the-rules) walk
every template, so the mobile tree is held to the same type budget, logical
properties and CSP rules without a test of its own. Add the mobile pages to
the rendered-page budgets by requesting them with `?device=mobile`.

### Manual checklist

1. `?device=mobile` on a desktop browser.
2. A real iPhone and a real Android phone.
3. Safe-area padding on a notched phone, portrait and landscape.
4. The tab bar does not cover the last item on a long page.
5. Touch targets 44px or more.
6. HTMX requests, toasts and the More sheet work.
7. An RTL language: tab order, back chevron direction, sheet text alignment.

In Chrome DevTools, toggle the device toolbar (Ctrl+Shift+M / Cmd+Shift+M),
pick a device and reload — the User-Agent changes only on reload.

---

## Checklist for New Mobile Templates

1. Create the `mobile/` directory beside the feature's `desktop/`.
2. Use `layouts/mobile_base.html` as the layout.
3. Pass `pageTitle` (translated) and, where there is a parent page, `backHref`.
4. Style with Tailwind utilities; add nothing to a mobile stylesheet.
5. Logical properties only (`ms-`/`me-`/`ps-`/`pe-`/`text-start`).
6. Every Alpine value a name, every string through `t`.
7. Touch targets 44px minimum (`size-12`, `min-h-14`).
8. `viewport-fit=cover` set, and safe-area padding on every fixed element.
9. Render through `view` / `devices.HTML` with the **desktop** template name.
10. Run `make css`, commit the rebuilt `app.css`, and run `go test ./...`.
11. Check with `?device=mobile`.

---

**Next:** [Frontend](frontend.md) | [HTMX Patterns](htmx.md)
