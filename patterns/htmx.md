# HTMX Patterns Reference

> Complete guide to using HTMX for interactive server-rendered web applications

> **Universal Examples**: This guide uses **Setting** and **Notification** models to demonstrate HTMX patterns. These examples show common SaaS interaction patterns and apply to any domain model.

---

## Table of Contents

- [Philosophy](#philosophy)
- [Template Structure](#template-structure)
- [Controller Patterns](#controller-patterns)
- [Response Headers](#response-headers)
- [Common UI Patterns](#common-ui-patterns)
- [Error Handling](#error-handling)
- [Real-World Examples](#real-world-examples)
- [Best Practices](#best-practices)

---

## Philosophy

**HTMX enables rich interactivity without writing JavaScript** by extending HTML with attributes.

### Core Principles

1. **Hypermedia as the engine of application state** (HATEOAS)
2. **Server renders HTML**, not JSON
3. **Progressive enhancement** (works without JS)
4. **Partial page updates** (no full page reloads)
5. **Simple, declarative** (attributes, not code)

### Why HTMX

- ✅ No build step (just include htmx.min.js)
- ✅ No JavaScript framework complexity
- ✅ Server-rendered templates (security, SEO)
- ✅ Works with any backend (Go, Python, Ruby, etc.)
- ✅ Small payload (~14 KB gzipped)

---

## Template Structure

### File Organization

```
views/
├── layouts/
│   ├── base.html          # master layout with <head>, navbar, footer
│   └── minimal.html       # Auth pages, no navbar
├── partials/
│   ├── _navbar.html       # Shared navbar
│   ├── _toast.html        # Toast notifications
│   └── _form_errors.html  # Form validation errors
├── settings/
│   ├── index.html         # Full page view
│   ├── form.html          # Settings form
│   └── _setting_row.html  # HTMX partial (prefixed with _)
├── notifications/
│   ├── list.html          # Full page view
│   └── _notification_row.html  # HTMX partial
└── users/
    └── profile.html
```

### Naming Convention

- **Full pages**: `index.html`, `form.html`, `list.html`
- **Partials**: `_partial_name.html` (underscore prefix)
- **Layouts**: `base.html`, `minimal.html`

### Base Layout

```html
<!-- views/layouts/base.html -->
<!DOCTYPE html>
<html lang="{{ .lang }}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{ block "title" . }}{{ .appName }}{{ end }}</title>

  <!-- Bootstrap CSS -->
  <link href="/static/css/bootstrap.min.css" rel="stylesheet">
  <link href="/static/css/app.css" rel="stylesheet">

  <!-- HTMX -->
  <script src="/static/js/htmx.min.js"></script>

  {{ block "head" . }}{{ end }}
</head>
<body>
  {{ template "_navbar.html" . }}

  <!-- Toast container for notifications -->
  <div id="toast-container" class="position-fixed top-0 end-0 p-3" style="z-index: 11"></div>

  <!-- master content -->
  <main class="container py-4">
    {{ block "content" . }}{{ end }}
  </main>

  <!-- Footer -->
  <footer class="mt-auto py-3 bg-white border-top">
    <div class="container text-center text-muted">
      <small>&copy; 2024 {{ .appName }}</small>
    </div>
  </footer>

  {{ block "scripts" . }}{{ end }}
</body>
</html>
```

### Full Page View

```html
<!-- views/settings/index.html -->
{{ define "title" }}Settings{{ end }}

{{ define "content" }}
<div class="d-flex justify-content-between align-items-center mb-4">
  <h1>{{ t .lang "settings.title" }}</h1>
</div>

<!-- Settings form (HTMX will update individual rows) -->
<div id="settings-list" class="card">
  <div class="card-body">
    {{ range .settings }}
      {{ template "settings/_setting_row.html" . }}
    {{ else }}
      <p class="text-muted">{{ t .lang "settings.empty" }}</p>
    {{ end }}
  </div>
</div>
{{ end }}
```

### Partial (HTMX target)

```html
<!-- views/settings/_setting_row.html -->
<div class="row mb-3 align-items-center" id="setting-{{ .Key }}">
  <div class="col-md-4">
    <label class="form-label fw-semibold">{{ .Key | title }}</label>
    <small class="d-block text-muted">{{ t .lang (printf "settings.%s.description" .Key) }}</small>
  </div>
  <div class="col-md-6">
    {{ if eq .Key "theme" }}
      <!-- Theme dropdown -->
      <select
        name="value"
        class="form-select"
        hx-post="/settings"
        hx-vals='{"key": "theme"}'
        hx-target="#setting-theme"
        hx-swap="outerHTML">
        <option value="light" {{ if eq .Value "light" }}selected{{ end }}>Light</option>
        <option value="dark" {{ if eq .Value "dark" }}selected{{ end }}>Dark</option>
        <option value="auto" {{ if eq .Value "auto" }}selected{{ end }}>Auto</option>
      </select>
    {{ else if eq .Key "notifications" }}
      <!-- Boolean toggle -->
      <div class="form-check form-switch">
        <input
          type="checkbox"
          class="form-check-input"
          {{ if eq .Value "true" }}checked{{ end }}
          hx-post="/settings"
          hx-vals='{"key": "notifications", "value": "{{ if eq .Value "true" }}false{{ else }}true{{ end }}"}'
          hx-target="#setting-notifications"
          hx-swap="outerHTML">
      </div>
    {{ else }}
      <!-- Text input with inline edit -->
      <input
        type="text"
        name="value"
        value="{{ .Value }}"
        class="form-control"
        hx-post="/settings"
        hx-vals='{"key": "{{ .Key }}"}'
        hx-trigger="blur changed"
        hx-target="#setting-{{ .Key }}"
        hx-swap="outerHTML">
    {{ end }}
  </div>
  <div class="col-md-2">
    <!-- Delete setting -->
    <button
      hx-delete="/settings/{{ .Key }}"
      hx-confirm="{{ t .lang "settings.delete.confirm" }}"
      hx-target="#setting-{{ .Key }}"
      hx-swap="outerHTML swap:1s"
      class="btn btn-sm btn-outline-danger">
      Delete
    </button>
  </div>
</div>
```

---

## Controller Patterns

### Detect HTMX Requests

```go
func isHTMX(c *gin.Context) bool {
  return c.GetHeader("HX-Request") == "true"
}
```

### Pattern 1: Full Page or Partial

```go
func ListNotifications(c *gin.Context) {
  userID := c.GetInt64("user_id")

  notifications, err := models.GetNotificationsByUser(c.Request.Context(), userID)
  if err != nil {
    handleError(c, err)
    return
  }

  // HTMX request: return partial
  if isHTMX(c) {
    c.HTML(200, "notifications/_notification_list.html", gin.H{
      "notifications": notifications,
    })
    return
  }

  // Regular request: return full page
  c.HTML(200, "notifications/list.html", gin.H{
    "notifications": notifications,
  })
}
```

### Pattern 2: Create with Redirect or Partial

```go
func UpdateSetting(c *gin.Context) {
  userID := c.GetInt64("user_id")

  var setting models.Setting
  setting.UserID = userID
  setting.Key = c.PostForm("key")
  setting.Value = c.PostForm("value")

  if err := setting.Validate(); err != nil {
    // Return error partial (HTMX or full page)
    if isHTMX(c) {
      c.HTML(400, "partials/_form_errors.html", gin.H{
        "error": "Invalid input",
      })
      return
    }
    c.HTML(400, "settings/index.html", gin.H{
      "error": "Invalid input",
      "setting": setting,
    })
    return
  }

  // Upsert setting (ON CONFLICT UPDATE)
  if err := setting.Set(c.Request.Context()); err != nil {
    handleError(c, err)
    return
  }

  // HTMX request: return updated setting row
  if isHTMX(c) {
    c.HTML(200, "settings/_setting_row.html", gin.H{"Setting": &setting})
    return
  }

  // Regular request: redirect to settings
  c.Redirect(302, "/settings")
}
```

### Pattern 3: Update with Swap

```go
func MarkNotificationRead(c *gin.Context) {
  notificationID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
  userID := c.GetInt64("user_id")

  var notification models.Notification
  if err := notification.GetByID(c.Request.Context(), notificationID, userID); err != nil {
    handleError(c, err)
    return
  }

  // Mark as read
  notification.ReadAt = sql.NullTime{Time: time.Now(), Valid: true}
  if err := notification.Update(c.Request.Context()); err != nil {
    handleError(c, err)
    return
  }

  // Return updated notification row (HTMX will swap it)
  c.HTML(200, "notifications/_notification_row.html", gin.H{
    "notification": notification,
  })
}
```

### Pattern 4: Delete with Empty Response

```go
func DeleteSetting(c *gin.Context) {
  key := c.Param("key")
  userID := c.GetInt64("user_id")

  setting := &models.Setting{
    UserID: userID,
    Key:    key,
  }

  if err := setting.Delete(c.Request.Context()); err != nil {
    handleError(c, err)
    return
  }

  // HTMX will remove the element (hx-swap="outerHTML")
  c.Status(200)
}
```

### Helper Function

```go
func renderHTMX(c *gin.Context, status int, fullPage, partial string, data gin.H) {
  if isHTMX(c) {
    c.HTML(status, partial, data)
  } else {
    c.HTML(status, fullPage, data)
  }
}

// Usage
renderHTMX(c, 200, "notifications/list.html", "notifications/_notification_list.html", gin.H{
  "notifications": notifications,
})
```

---

## Response Headers

### HX-Trigger (Client-side Events)

Trigger JavaScript events on the client:

```go
// Trigger custom event
c.Header("HX-Trigger", "settingUpdated")

// Trigger with data (JSON)
c.Header("HX-Trigger", `{"showToast": {"type": "success", "message": "Setting saved!"}}`)

// Multiple events
c.Header("HX-Trigger", `{"settingUpdated": {}, "refreshStats": {}}`)
```

JavaScript listener:

```javascript
document.body.addEventListener('showToast', (e) => {
  const { type, message } = e.detail;
  // Show toast notification
  showToast(type, message);
});
```

### HX-Redirect (Full Page Redirect)

```go
c.Header("HX-Redirect", "/settings")
c.Status(200)
```

### HX-Reswap (Override Swap Strategy)

```go
c.Header("HX-Reswap", "innerHTML")  // Instead of outerHTML
c.Header("HX-Reswap", "beforeend")  // Append to end
c.Header("HX-Reswap", "afterbegin") // Prepend to beginning
```

### HX-Retarget (Override Target Element)

```go
c.Header("HX-Retarget", "#settings-list")
c.Header("HX-Retarget", "body")
```

### HX-Push-Url (Update Browser URL)

```go
c.Header("HX-Push-Url", "/profile/settings")  // Update URL without reload
c.Header("HX-Push-Url", "false")              // Don't update URL
```

---

## Common UI Patterns

### 1. Inline Edit

```html
<!-- View mode -->
<div id="setting-timezone-{{ .ID }}" class="d-flex">
  <span>{{ .Value }}</span>
  <button
    hx-get="/settings/timezone/edit"
    hx-target="#setting-timezone-{{ .ID }}"
    hx-swap="outerHTML"
    class="btn btn-sm btn-link">
    Edit
  </button>
</div>
```

```html
<!-- Edit mode (returned by GET /settings/timezone/edit) -->
<form
  id="setting-timezone-{{ .ID }}"
  hx-post="/settings"
  hx-vals='{"key": "timezone"}'
  hx-target="#setting-timezone-{{ .ID }}"
  hx-swap="outerHTML">
  <select name="value" class="form-select" autofocus>
    <option value="UTC" {{ if eq .Value "UTC" }}selected{{ end }}>UTC</option>
    <option value="America/New_York" {{ if eq .Value "America/New_York" }}selected{{ end }}>Eastern</option>
    <option value="America/Los_Angeles" {{ if eq .Value "America/Los_Angeles" }}selected{{ end }}>Pacific</option>
  </select>
  <button type="submit" class="btn btn-sm btn-primary">Save</button>
  <button
    type="button"
    hx-get="/settings/timezone/view"
    hx-target="#setting-timezone-{{ .ID }}"
    hx-swap="outerHTML"
    class="btn btn-sm btn-secondary">
    Cancel
  </button>
</form>
```

### 2. Infinite Scroll

```html
<div id="notification-list">
  {{ range .notifications }}
    {{ template "notifications/_notification_row.html" . }}
  {{ end }}

  <!-- Load more trigger -->
  {{ if .hasMore }}
    <div
      hx-get="/notifications?offset={{ .nextOffset }}"
      hx-trigger="revealed"
      hx-swap="outerHTML">
      <p class="text-center">Loading more...</p>
    </div>
  {{ end }}
</div>
```

### 3. Search with Debounce

```html
<input
  type="search"
  name="q"
  placeholder="Search notifications..."
  hx-get="/notifications/search"
  hx-trigger="keyup changed delay:300ms"
  hx-target="#search-results"
  class="form-control">

<div id="search-results"></div>
```

### 4. Form with Live Validation

```html
<form hx-post="/profile" hx-target="#profile-section">
  <div class="mb-3">
    <label for="email" class="form-label">Email</label>
    <input
      type="email"
      id="email"
      name="email"
      class="form-control"
      hx-post="/profile/validate-email"
      hx-trigger="blur"
      hx-target="#email-error"
      required>
    <div id="email-error" class="text-danger"></div>
  </div>

  <button type="submit" class="btn btn-primary">Update Profile</button>
</form>
```

### 5. Modal with HTMX

```html
<!-- Button to open modal -->
<button
  hx-get="/notifications/new"
  hx-target="#modal-content"
  data-bs-toggle="modal"
  data-bs-target="#notificationModal"
  class="btn btn-primary">
  Create Notification
</button>

<!-- Modal (Bootstrap) -->
<div class="modal fade" id="notificationModal" tabindex="-1">
  <div class="modal-dialog">
    <div class="modal-content" id="modal-content">
      <!-- HTMX loads form here -->
    </div>
  </div>
</div>
```

### 6. Dependent Dropdowns

```html
<!-- Country selector -->
<select
  name="country"
  hx-get="/api/states"
  hx-trigger="change"
  hx-target="#state-select"
  class="form-select">
  <option value="">Select country</option>
  <option value="US">United States</option>
  <option value="CA">Canada</option>
</select>

<!-- State selector (populated by HTMX) -->
<select id="state-select" name="state" class="form-select">
  <option value="">Select state</option>
</select>
```

### 7. Toast Notifications

```html
<!-- Toast partial (views/partials/_toast.html) -->
<div class="toast align-items-center text-bg-{{ .type }} border-0" role="alert">
  <div class="d-flex">
    <div class="toast-body">{{ .message }}</div>
    <button type="button" class="btn-close btn-close-white me-2 m-auto" data-bs-dismiss="toast"></button>
  </div>
</div>
```

Controller:

```go
func UpdateSetting(c *gin.Context) {
  // ... update setting ...

  // Trigger toast via HX-Trigger
  c.Header("HX-Trigger", `{"showToast": {"type": "success", "message": "Setting saved!"}}`)
  c.HTML(200, "settings/_setting_row.html", gin.H{"Setting": &setting})
}
```

JavaScript listener:

```javascript
document.body.addEventListener('showToast', (e) => {
  const { type, message } = e.detail;
  const container = document.getElementById('toast-container');

  const toast = document.createElement('div');
  toast.className = `toast align-items-center text-bg-${type} border-0`;
  toast.setAttribute('role', 'alert');
  toast.innerHTML = `
    <div class="d-flex">
      <div class="toast-body">${message}</div>
      <button type="button" class="btn-close btn-close-white me-2 m-auto" data-bs-dismiss="toast"></button>
    </div>
  `;

  container.appendChild(toast);
  const bsToast = new bootstrap.Toast(toast);
  bsToast.show();
});
```

### 8. Loading Indicators

```html
<!-- Button with spinner -->
<button
  hx-post="/settings"
  hx-vals='{"key": "theme", "value": "dark"}'
  hx-target="#settings-list"
  class="btn btn-primary">
  Save Changes
  <span class="spinner-border spinner-border-sm htmx-indicator" role="status"></span>
</button>

<!-- CSS (auto-shows during HTMX request) -->
<style>
.htmx-indicator {
  display: none;
}
.htmx-request .htmx-indicator {
  display: inline-block;
}
</style>
```

---

## Error Handling

### Pattern 1: Error Partial

```go
func handleHTMXError(c *gin.Context, err error) {
  log := logger.FromContext(c)
  lang := i18n.GetLang(c)

  var appErr *errors.AppError
  if !stderrors.As(err, &appErr) {
    appErr = errors.Wrap(err, errors.CodeUnknown, "unexpected error")
  }

  log.Error().Str("code", appErr.Code).Err(appErr.Err).Msg(appErr.Message)

  message := i18n.T(lang, "errors."+appErr.Code)

  // HTMX request: return error partial
  if isHTMX(c) {
    c.HTML(appErr.HTTPStatus(), "partials/_error.html", gin.H{
      "code": appErr.Code,
      "message": message,
    })
    return
  }

  // Regular request: full error page
  c.HTML(appErr.HTTPStatus(), "error.html", gin.H{
    "code": appErr.Code,
    "message": message,
  })
}
```

Error partial:

```html
<!-- views/partials/_error.html -->
<div class="alert alert-danger alert-dismissible fade show" role="alert">
  <strong>Error:</strong> {{ .message }}
  <button type="button" class="btn-close" data-bs-dismiss="alert"></button>
</div>
```

### Pattern 2: Toast on Error

```go
func UpdateSetting(c *gin.Context) {
  // ... validation fails ...

  if isHTMX(c) {
    c.Header("HX-Trigger", `{"showToast": {"type": "danger", "message": "Invalid input"}}`)
    c.HTML(400, "partials/_form_errors.html", gin.H{"errors": errors})
    return
  }

  // ...
}
```

### Pattern 3: Form Validation Errors

```html
<!-- Form with inline errors -->
<form hx-post="/profile">
  <div class="mb-3">
    <label for="name" class="form-label">Name</label>
    <input
      type="text"
      id="name"
      name="name"
      class="form-control {{ if .errors.name }}is-invalid{{ end }}"
      value="{{ .user.Name }}">
    {{ if .errors.name }}
      <div class="invalid-feedback">{{ .errors.name }}</div>
    {{ end }}
  </div>

  <button type="submit" class="btn btn-primary">Save</button>
</form>
```

---

## Real-World Examples

### Example 1: Settings Management

**Features:**
- Display all user settings (theme, language, timezone, notifications)
- Inline editing with instant updates
- Different input types (select, toggle, text)
- Delete settings with confirmation
- Toast notifications on save/delete

**Key Routes:**
```go
protected.GET("/settings", ShowSettings)
protected.POST("/settings", UpdateSetting)       // Upsert
protected.DELETE("/settings/:key", DeleteSetting)
```

**HTMX Highlights:**
- `hx-post` on blur for text inputs (auto-save)
- `hx-post` on change for selects/toggles (instant update)
- `hx-target` and `hx-swap` for seamless row replacement
- `HX-Trigger` header for toast notifications

### Example 2: Notification Center

**Features:**
- List notifications with infinite scroll
- Mark as read (instant visual update)
- Delete notification (fade out animation)
- Filter by unread/all
- Real-time search

**Key Routes:**
```go
protected.GET("/notifications", ListNotifications)
protected.PATCH("/notifications/:id/read", MarkNotificationRead)
protected.DELETE("/notifications/:id", DeleteNotification)
protected.GET("/notifications/search", SearchNotifications)
```

**HTMX Highlights:**
- `hx-trigger="revealed"` for infinite scroll
- `hx-trigger="keyup changed delay:300ms"` for search
- `hx-swap="outerHTML swap:1s"` for delete animation
- Conditional rendering (read vs unread styles)

### Example 3: Live Search

```html
<!-- Search input -->
<input
  type="search"
  name="q"
  placeholder="Search..."
  hx-get="/search"
  hx-trigger="keyup changed delay:300ms"
  hx-target="#results"
  class="form-control">

<!-- Results container -->
<div id="results">
  <!-- HTMX populates this -->
</div>
```

Controller:

```go
func Search(c *gin.Context) {
  query := c.Query("q")

  if query == "" {
    c.HTML(200, "search/_empty.html", nil)
    return
  }

  results, err := models.Search(c.Request.Context(), query)
  if err != nil {
    handleError(c, err)
    return
  }

  c.HTML(200, "search/_results.html", gin.H{"results": results})
}
```

---

## Best Practices

### Do's ✅

- ✅ **Use partials** for reusable components (`_setting_row.html`)
- ✅ **Detect HTMX requests** and respond with partials
- ✅ **Return full pages** for non-HTMX clients (progressive enhancement)
- ✅ **Use semantic HTML** (buttons, forms, anchors)
- ✅ **Add loading indicators** (`htmx-indicator` class)
- ✅ **Debounce search** (`delay:300ms`)
- ✅ **Confirm destructive actions** (`hx-confirm`)
- ✅ **Use HX-Trigger** for client-side events (toasts, etc.)
- ✅ **Test without JavaScript** (forms should still work)

### Don'ts ❌

- ❌ **Don't return JSON** from HTMX endpoints (return HTML)
- ❌ **Don't write custom JavaScript** unless absolutely necessary
- ❌ **Don't nest HTMX attributes** deeply (keep it simple)
- ❌ **Don't over-optimize** (HTMX is fast, full page reloads are fine for some actions)
- ❌ **Don't ignore errors** (always handle and show user feedback)
- ❌ **Don't break browser history** (use `hx-push-url` when appropriate)

### Performance Tips

- ✅ Use `hx-swap="outerHTML swap:1s"` for smooth transitions
- ✅ Debounce search and autocomplete (`delay:300ms`)
- ✅ Use `hx-trigger="revealed"` for infinite scroll (load on scroll)
- ✅ Return minimal HTML (partials, not full pages)
- ✅ Cache static assets (htmx.min.js)

### Accessibility

- ✅ Use semantic HTML (`<button>`, `<form>`, `<a>`)
- ✅ Add ARIA labels where needed
- ✅ Ensure keyboard navigation works
- ✅ Announce dynamic content changes (ARIA live regions)
- ✅ Test with screen readers

---

**Next:** See [frontend.md](frontend.md) for Bootstrap UI patterns
