# Frontend Architecture Reference

> Bootstrap 5 + HTMX for building responsive, interactive web applications without a build pipeline

---

## Table of Contents

- [Philosophy](#philosophy)
- [Folder Structure](#folder-structure)
- [Bootstrap Foundations](#bootstrap-foundations)
- [Component Library](#component-library)
- [Responsive Design](#responsive-design)
- [Accessibility](#accessibility)
- [Performance](#performance)
- [Best Practices](#best-practices)

---

## Philosophy

**Bootstrap + HTMX. No custom JS. Minimal custom CSS.**

### Core Principles

1. **Zero frontend build pipeline** (no npm, webpack, vite)
2. **95% of UI = server-rendered HTML + Bootstrap utilities**
3. **HTMX for interactivity** without JavaScript
4. **One `app.css`** for brand overrides only (~50-100 lines)
5. **10-year maintainability** with zero dependencies

### Why This Stack

- ✅ No JavaScript framework complexity
- ✅ No build tools to maintain
- ✅ Works with JavaScript disabled
- ✅ Fast development (copy Bootstrap classes)
- ✅ Future-proof (HTML + CSS don't deprecate)

---

## Folder Structure

```
views/
  layouts/
    base.html              # Main layout (<head>, navbar, footer)
    minimal.html           # Auth pages (no navbar)
  partials/
    _navbar.html           # Shared navigation
    _toast.html            # Toast notifications
    _modal.html            # Modal shell
    _form_errors.html      # Error display
  users/
    profile.html           # Full page views
    edit.html
    _user_row.html         # HTMX partials (prefix: _)
  settings/
    index.html
    _setting_row.html

static/
  css/
    bootstrap.min.css      # v5.3+ (vendored, version-locked)
    app.css                # Brand overrides (~50-100 lines max)
  js/
    htmx.min.js            # Vendored
    app.js                 # Optional, rare (~20 lines max)
  img/
    logo.svg
    favicon.ico

lang/
  en.json                  # Shared UI strings
  es.json
```

---

## Bootstrap Foundations

### CSS Only (No Custom Classes)

**Rule:** Reuse existing Bootstrap classes. Never invent custom classes when Bootstrap utilities exist.

```html
<!-- Good: Use Bootstrap utilities -->
<div class="d-flex justify-content-between align-items-center mb-3">
  <h1 class="mb-0">Settings</h1>
  <button class="btn btn-primary">Add Setting</button>
</div>

<!-- Bad: Custom classes -->
<div class="settings-header">
  <h1 class="page-title">Settings</h1>
  <button class="create-btn">Add Setting</button>
</div>
```

### One Global CSS File

**app.css should only contain:**
- Brand colors (CSS variables)
- Font family override
- Minor component tweaks (<5 lines each)
- HTMX loading indicators

```css
/* static/css/app.css */

/* Brand colors (override Bootstrap variables) */
:root {
  --bs-primary: #0a3d91;
  --bs-primary-rgb: 10, 61, 145;
  --bs-secondary: #6c757d;
  --bs-success: #28a745;
  --bs-danger: #dc3545;
}

/* Typography */
body {
  font-family: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
  -webkit-font-smoothing: antialiased;
}

/* Utility: Text truncate (2 lines) - Only if not in Bootstrap */
.text-truncate-2 {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

/* Component: Card hover effect */
.card-hover:hover {
  box-shadow: 0 .5rem 1rem rgba(0, 0, 0, .15);
  transition: box-shadow .15s ease-in-out;
}

/* HTMX loading indicators */
.htmx-indicator {
  display: none;
}
.htmx-request .htmx-indicator {
  display: inline-block;
}
.htmx-request.htmx-indicator {
  display: inline-block;
}
```

**Total lines: ~40-50. Never exceed 100.**

---

## Component Library

### Layout

**Base Layout:**
```html
<!-- views/layouts/base.html -->
<!DOCTYPE html>
<html lang="{{ .lang }}" dir="{{ if .isRTL }}rtl{{ else }}ltr{{ end }}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{ block "title" . }}{{ .appName }}{{ end }}</title>

  <link href="/static/css/bootstrap.min.css" rel="stylesheet">
  <link href="/static/css/app.css" rel="stylesheet">
  <script src="/static/js/htmx.min.js"></script>

  {{ block "head" . }}{{ end }}
</head>
<body class="bg-light">
  {{ template "_navbar.html" . }}

  <div id="toast-container" class="position-fixed top-0 end-0 p-3" style="z-index: 11"></div>

  <main class="container py-4">
    {{ block "content" . }}{{ end }}
  </main>

  <footer class="mt-auto py-3 bg-white border-top">
    <div class="container text-center text-muted">
      <small>&copy; 2024 {{ .appName }}</small>
    </div>
  </footer>

  {{ block "scripts" . }}{{ end }}
</body>
</html>
```

### Navbar

```html
<!-- views/partials/_navbar.html -->
<nav class="navbar navbar-expand-lg navbar-dark bg-primary">
  <div class="container">
    <a class="navbar-brand" href="/">
      <img src="/static/img/logo.svg" alt="Logo" height="30" class="d-inline-block align-text-top">
      {{ .appName }}
    </a>

    <button class="navbar-toggler" type="button" data-bs-toggle="collapse" data-bs-target="#navbarNav">
      <span class="navbar-toggler-icon"></span>
    </button>

    <div class="collapse navbar-collapse" id="navbarNav">
      <ul class="navbar-nav ms-auto">
        <li class="nav-item">
          <a class="nav-link" href="/dashboard">{{ t .lang "nav.dashboard" }}</a>
        </li>

        {{ if .user }}
        <li class="nav-item dropdown">
          <a class="nav-link dropdown-toggle" href="#" data-bs-toggle="dropdown">
            {{ .user.Name }}
          </a>
          <ul class="dropdown-menu dropdown-menu-end">
            <li><a class="dropdown-item" href="/settings">{{ t .lang "nav.settings" }}</a></li>
            <li><hr class="dropdown-divider"></li>
            <li><a class="dropdown-item" href="/logout">{{ t .lang "nav.logout" }}</a></li>
          </ul>
        </li>
        {{ else }}
        <li class="nav-item">
          <a class="nav-link" href="/login">{{ t .lang "nav.login" }}</a>
        </li>
        {{ end }}
      </ul>
    </div>
  </div>
</nav>
```

### Buttons

```html
<!-- Primary action -->
<button class="btn btn-primary">Create</button>

<!-- Secondary -->
<button class="btn btn-outline-secondary">Cancel</button>

<!-- Danger -->
<button class="btn btn-danger btn-sm">Delete</button>

<!-- Success -->
<button class="btn btn-success">Save</button>

<!-- Loading state -->
<button class="btn btn-primary" disabled>
  <span class="spinner-border spinner-border-sm me-2"></span>
  Loading...
</button>

<!-- Button group -->
<div class="btn-group" role="group">
  <button class="btn btn-outline-primary">View</button>
  <button class="btn btn-outline-primary">Edit</button>
  <button class="btn btn-outline-danger">Delete</button>
</div>
```

### Cards

```html
<!-- Basic card -->
<div class="card">
  <div class="card-body">
    <h5 class="card-title">{{ .title }}</h5>
    <p class="card-text">{{ .content }}</p>
  </div>
</div>

<!-- Card with header and footer -->
<div class="card">
  <div class="card-header">
    <h5 class="mb-0">{{ .title }}</h5>
  </div>
  <div class="card-body">
    <p class="card-text">{{ .content }}</p>
  </div>
  <div class="card-footer text-muted">
    {{ .createdAt | formatDate }}
  </div>
</div>

<!-- Hoverable card (add .card-hover class) -->
<div class="card card-hover mb-3">
  <div class="card-body">
    <h5 class="card-title">{{ .title }}</h5>
  </div>
</div>
```

### Forms

```html
<!-- Basic form -->
<form method="POST" action="/profile">
  <input type="hidden" name="csrf_token" value="{{ csrf }}">

  <div class="mb-3">
    <label for="name" class="form-label">{{ t .lang "profile.form.name" }}</label>
    <input type="text" class="form-control" id="name" name="name" required>
    <div class="form-text">{{ t .lang "profile.form.name_help" }}</div>
  </div>

  <div class="mb-3">
    <label for="email" class="form-label">{{ t .lang "profile.form.email" }}</label>
    <input type="email" class="form-control" id="email" name="email" required disabled>
    <div class="form-text">{{ t .lang "profile.form.email_readonly" }}</div>
  </div>

  <div class="mb-3">
    <label for="avatar_url" class="form-label">{{ t .lang "profile.form.avatar" }}</label>
    <input type="url" class="form-control" id="avatar_url" name="avatar_url">
  </div>

  <button type="submit" class="btn btn-primary">
    {{ t .lang "common.save" }}
  </button>
  <a href="/profile" class="btn btn-secondary">{{ t .lang "cancel" }}</a>
</form>

<!-- Form with validation errors -->
<div class="mb-3">
  <label for="email" class="form-label">Email</label>
  <input
    type="email"
    class="form-control {{ if .errors.email }}is-invalid{{ end }}"
    id="email"
    name="email"
    value="{{ .email }}">
  {{ if .errors.email }}
    <div class="invalid-feedback">{{ .errors.email }}</div>
  {{ end }}
</div>
```

### Alerts

```html
<!-- Success -->
<div class="alert alert-success alert-dismissible fade show" role="alert">
  {{ t .lang "profile.updated" }}
  <button type="button" class="btn-close" data-bs-dismiss="alert"></button>
</div>

<!-- Error -->
<div class="alert alert-danger" role="alert">
  <strong>Error:</strong> {{ .error }}
</div>

<!-- Info -->
<div class="alert alert-info" role="alert">
  {{ .message }}
</div>

<!-- Warning -->
<div class="alert alert-warning" role="alert">
  {{ .warning }}
</div>
```

### Tables

```html
<!-- Basic table -->
<table class="table">
  <thead>
    <tr>
      <th>Name</th>
      <th>Email</th>
      <th>Status</th>
      <th>Actions</th>
    </tr>
  </thead>
  <tbody>
    {{ range .users }}
    <tr>
      <td>{{ .Name }}</td>
      <td>{{ .Email }}</td>
      <td>
        {{ if .Active }}
          <span class="badge bg-success">Active</span>
        {{ else }}
          <span class="badge bg-secondary">Inactive</span>
        {{ end }}
      </td>
      <td>
        <a href="/users/{{ .ID }}" class="btn btn-sm btn-outline-primary">View</a>
      </td>
    </tr>
    {{ end }}
  </tbody>
</table>

<!-- Striped, hover, bordered -->
<table class="table table-striped table-hover table-bordered">
  <!-- ... -->
</table>

<!-- Small table -->
<table class="table table-sm">
  <!-- ... -->
</table>
```

### Modals

```html
<!-- Modal trigger -->
<button type="button" class="btn btn-primary" data-bs-toggle="modal" data-bs-target="#exampleModal">
  Open Modal
</button>

<!-- Modal -->
<div class="modal fade" id="exampleModal" tabindex="-1">
  <div class="modal-dialog">
    <div class="modal-content">
      <div class="modal-header">
        <h5 class="modal-title">{{ .title }}</h5>
        <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
      </div>
      <div class="modal-body">
        {{ .content }}
      </div>
      <div class="modal-footer">
        <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Close</button>
        <button type="button" class="btn btn-primary">Save changes</button>
      </div>
    </div>
  </div>
</div>
```

### Badges

```html
<span class="badge bg-primary">Primary</span>
<span class="badge bg-secondary">Secondary</span>
<span class="badge bg-success">Success</span>
<span class="badge bg-danger">Danger</span>
<span class="badge bg-warning text-dark">Warning</span>
<span class="badge bg-info text-dark">Info</span>

<!-- Pill badges -->
<span class="badge rounded-pill bg-primary">14</span>
```

### Spinners (Loading)

```html
<!-- Spinner (border) -->
<div class="spinner-border" role="status">
  <span class="visually-hidden">Loading...</span>
</div>

<!-- Spinner (grow) -->
<div class="spinner-grow" role="status">
  <span class="visually-hidden">Loading...</span>
</div>

<!-- Small spinner (for buttons) -->
<button class="btn btn-primary" type="button" disabled>
  <span class="spinner-border spinner-border-sm me-2"></span>
  Loading...
</button>
```

---

## Responsive Design

### Mobile-First Breakpoints

| Breakpoint | Size | Device |
|------------|------|--------|
| `xs` | <576px | Mobile (default) |
| `sm` | ≥576px | Phone landscape |
| `md` | ≥768px | Tablet |
| `lg` | ≥992px | Desktop |
| `xl` | ≥1200px | Large desktop |
| `xxl` | ≥1400px | Extra large |

### Responsive Utilities

```html
<!-- Hide on mobile, show on desktop -->
<div class="d-none d-md-block">Desktop only</div>

<!-- Show on mobile, hide on desktop -->
<div class="d-block d-md-none">Mobile only</div>

<!-- Stack on mobile, row on desktop -->
<div class="d-flex flex-column flex-md-row">
  <div>Left</div>
  <div>Right</div>
</div>

<!-- Full width on mobile, half on tablet+ -->
<div class="row">
  <div class="col-12 col-md-6">Column 1</div>
  <div class="col-12 col-md-6">Column 2</div>
</div>

<!-- Grid with 1/2/3 columns -->
<div class="row g-3">
  <div class="col-12 col-md-6 col-lg-4">Card 1</div>
  <div class="col-12 col-md-6 col-lg-4">Card 2</div>
  <div class="col-12 col-md-6 col-lg-4">Card 3</div>
</div>
```

### Responsive Typography

```html
<!-- Responsive headings -->
<h1 class="display-1">Display 1</h1>  <!-- Large -->
<h1 class="display-6">Display 6</h1>  <!-- Small -->

<!-- Responsive text sizing (fs = font-size) -->
<p class="fs-1">Font size 1 (largest)</p>
<p class="fs-6">Font size 6 (smallest)</p>
```

---

## Accessibility

### Checklist

- ✅ Use semantic HTML (`<button>`, `<nav>`, `<main>`, `<article>`)
- ✅ Label all form inputs with `<label for="id">`
- ✅ Add `alt` text to images
- ✅ Use ARIA labels where needed (`aria-label`, `aria-describedby`)
- ✅ Ensure keyboard navigation (Tab, Enter, Escape)
- ✅ Maintain color contrast (WCAG AA minimum: 4.5:1)
- ✅ Test with screen readers (VoiceOver, NVDA)
- ✅ Use `role` attributes for dynamic content

### Examples

```html
<!-- Skip to main content link -->
<a href="#main-content" class="visually-hidden-focusable">Skip to main content</a>

<main id="main-content">
  <!-- Content -->
</main>

<!-- Form with proper labels -->
<label for="email">Email address</label>
<input type="email" id="email" name="email" required>

<!-- Button with ARIA label -->
<button type="button" aria-label="Close" class="btn-close"></button>

<!-- ARIA live region (for dynamic content) -->
<div id="notifications" role="status" aria-live="polite" aria-atomic="true">
  <!-- Dynamic notifications appear here -->
</div>

<!-- Visually hidden (accessible to screen readers) -->
<span class="visually-hidden">This text is only for screen readers</span>
```

---

## Performance

### Targets

- 🎯 **TTI (Time to Interactive)** < 1s on broadband
- 📦 **Payload**: Bootstrap (~25 KB) + HTMX (~14 KB) + App CSS (~2 KB) = ~41 KB gzipped
- 🚀 **First Contentful Paint** < 500ms

### Optimization

**1. Use CDN in production:**
```html
<!-- Production: CDN with version pinning -->
<link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.2/dist/css/bootstrap.min.css" rel="stylesheet">
<script src="https://unpkg.com/htmx.org@1.9.10"></script>
```

**2. Vendor files locally (alternative):**
```
static/
  css/
    bootstrap.min.css  (committed to git, version-locked)
  js/
    htmx.min.js        (committed to git, version-locked)
```

**3. Enable compression (Nginx/Caddy):**
```nginx
# Nginx
gzip on;
gzip_types text/css application/javascript application/json;

# Caddy (automatic)
encode gzip
```

**4. Cache static assets:**
```nginx
# Cache for 1 year
location /static/ {
  expires 1y;
  add_header Cache-Control "public, immutable";
}
```

**5. Use preload for critical assets:**
```html
<link rel="preload" href="/static/css/bootstrap.min.css" as="style">
<link rel="preload" href="/static/js/htmx.min.js" as="script">
```

### Lighthouse Audit

Run regularly:
```bash
lighthouse https://yourapp.com --view
```

Target scores:
- **Performance**: 90+
- **Accessibility**: 95+
- **Best Practices**: 90+
- **SEO**: 90+

---

## Best Practices

### Do's ✅

- ✅ **Reuse Bootstrap classes** (never invent custom classes)
- ✅ **Keep app.css under 100 lines** (brand overrides only)
- ✅ **No build pipeline** (commit CSS/JS directly)
- ✅ **Progressive enhancement** (works without JS)
- ✅ **Mobile-first** (design for small screens, scale up)
- ✅ **Semantic HTML** (use correct elements)
- ✅ **Test accessibility** (keyboard nav, screen readers)
- ✅ **Version-lock dependencies** (vendor files or CDN with version)

### Don'ts ❌

- ❌ **Don't use Tailwind** (Bootstrap is enough)
- ❌ **Don't create custom frameworks** (Bootstrap utilities cover 95%)
- ❌ **Don't add npm** (no build tools)
- ❌ **Don't write custom JavaScript** (HTMX handles interactivity)
- ❌ **Don't override Bootstrap deeply** (brand colors only)
- ❌ **Don't support old browsers** (evergreen browsers only: Chrome, Firefox, Safari, Edge)

### When to Break the Rules (Rarely)

| Need | Solution | Justification |
|------|----------|---------------|
| Real-time updates | HTMX SSE extension or Alpine.js (<15 KB) | WebSocket alternative |
| Complex client state | Alpine.js (small components) | Still no build step |
| Data visualization | Chart.js via CDN | Charts need JS |
| Rich text editing | Trix or TinyMCE via CDN | WYSIWYG requires JS |
| File uploads | Dropzone.js via CDN | Better UX than native input |

**Rule of thumb:** Total custom JS should stay under 20 KB uncompressed.

---

## RTL Support

### Automatic with Bootstrap 5.3+

```html
<html dir="rtl" lang="ar">
  <!-- Bootstrap automatically flips layout -->
</html>
```

### In Middleware

```go
func I18nMiddleware() gin.HandlerFunc {
  return func(c *gin.Context) {
    lang := c.Query("lang")
    if lang == "" { lang = "en" }

    // Set text direction
    dir := "ltr"
    isRTL := false
    if contains([]string{"ar", "fa", "he", "ur"}, lang) {
      dir = "rtl"
      isRTL = true
    }

    c.Set("lang", lang)
    c.Set("dir", dir)
    c.Set("isRTL", isRTL)
    c.Next()
  }
}
```

### In Base Layout

```html
<html lang="{{ .lang }}" dir="{{ .dir }}">
  <!-- Rest of layout -->
</html>
```

---

**Next:** See [testing.md](testing.md) for testing patterns
