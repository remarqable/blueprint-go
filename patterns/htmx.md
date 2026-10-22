# HTMX Patterns

> Server-rendered interactivity: Gin handlers return HTML fragments, HTMX swaps
> them in, Alpine handles what never needs the server.

Read [frontend.md](frontend.md) first: the layout, component classes, Alpine
components and CSP referred to here are defined there. Examples use a
**Setting** and a **Notification** model; the patterns apply to any model.

---

## Table of Contents

- [Philosophy](#philosophy)
- [Configuration](#configuration)
- [Core Concepts](#core-concepts)
- [Template Structure](#template-structure)
- [Controller Patterns](#controller-patterns)
- [Response Headers](#response-headers)
- [Toast Notifications](#toast-notifications)
- [Common UI Patterns](#common-ui-patterns)
- [Loading States](#loading-states)
- [Error Handling](#error-handling)
- [Best Practices](#best-practices)

---

## Philosophy

- The server returns HTML, not JSON. Links and forms drive state.
- Progressive enhancement: every form has a real `method` and `action`, so it
  still works when JavaScript does not load.
- No client-side routing and no client-side copy of server state.
- **HTMX talks to the server; Alpine handles the rest.** A dropdown opening is
  Alpine. Saving the setting chosen in it is HTMX. Neither reaches into the
  other's job.

**Good for:** forms, inline editing, search and filter, pagination and
infinite scroll, settings, notifications.

**Not for:** real-time collaboration ([realtime.md](realtime.md) when
`realtime: true`), complex drag-and-drop, offline-first apps.

---

## Configuration

The base layout configures HTMX with one meta tag
([frontend.md](frontend.md#base-layout)):

```html
<meta name="htmx-config"
      content='{"includeIndicatorStyles":false,"allowEval":false,"responseHandling":[{"code":"204","swap":false},{"code":"[23]..","swap":true},{"code":"422","swap":true},{"code":"[45]..","swap":false,"error":true}]}'>
```

| Setting | Why |
|---------|-----|
| `includeIndicatorStyles: false` | HTMX otherwise injects a `<style>` block, which `style-src 'self'` blocks. The indicator rules live in `input.css` instead ([Loading States](#loading-states)). |
| `allowEval: false` | The CSP has no `'unsafe-eval'`, so anything HTMX would evaluate fails anyway. Turning it off makes that explicit. |
| `responseHandling` | Adds `422` as a swapping status for validation errors. See [Error Handling](#error-handling). |

HTMX merges this object shallowly, so `responseHandling` replaces the default
list entirely; all four entries are needed. Entries match in order.

`allowEval: false` rules out, in any template:

- `hx-on:*` attributes — use an Alpine component or an `HX-Trigger` event.
- `js:` or `javascript:` values in `hx-vals`, and `hx-vars`.
- Trigger filters such as `hx-trigger="keyup[key=='Enter']"`. Use a modifier
  (`keyup changed delay:300ms`) or a form submit instead.

The test in [frontend.md](frontend.md#testing-the-rules) rejects `hx-on`.

---

## Core Concepts

| Attribute | Purpose | Example |
|-----------|---------|---------|
| `hx-get` / `hx-post` | Request | `hx-post="/settings"` |
| `hx-put` / `hx-patch` / `hx-delete` | Request | `hx-delete="/settings/theme"` |
| `hx-trigger` | Event that fires it | `hx-trigger="change"` |
| `hx-target` | Where the response goes | `hx-target="#result"` |
| `hx-swap` | How it goes in | `hx-swap="outerHTML"` |
| `hx-include` | Extra inputs to send | `hx-include="#filters"` |
| `hx-confirm` | Native confirm first | `hx-confirm="{{ t .lang "settings.delete_confirm" }}"` |
| `hx-indicator` | Element shown while in flight | `hx-indicator="#spinner"` |
| `hx-disabled-elt` | Element disabled while in flight | `hx-disabled-elt="find button"` |

| Swap | Effect |
|------|--------|
| `innerHTML` | Replace the target's children (default) |
| `outerHTML` | Replace the target itself |
| `beforeend` / `afterbegin` | Append / prepend inside the target |
| `delete` | Remove the target |
| `none` | Swap nothing; headers still apply |

**Prefer inputs over `hx-vals` for values from Go.** `hx-vals='{"key": "{{ .Key }}"}'`
puts a template value inside a JSON string inside an HTML attribute;
`html/template` escapes it for HTML, not for JSON, so a quote in the value
breaks the JSON. A hidden `<input>` inside the form, or `hx-include`, has no
such seam.

---

## Template Structure

### File organization

```
internal/assets/views/
├── layouts/
│   └── base.html                 # frontend.md
├── partials/
│   ├── _navbar.html
│   ├── _flash.html
│   ├── _toasts.html
│   └── _modal.html
├── settings/
│   ├── index.html                # full page
│   └── _setting_row.html         # HTMX partial
└── notifications/
    ├── list.html                 # full page
    ├── _list.html                # HTMX partial: the list alone
    ├── _row.html                 # HTMX partial: one row
    └── _form.html                # HTMX partial: modal body
```

- **Full pages** extend the layout.
- **Partials** are prefixed `_` and render alone — they are what HTMX swaps.
- A full page includes its partials, so the first render and every later swap
  produce the same markup from the same file.

### Partials that need page data

`{{ template }}` passes one value. A row partial needs both its row and `.lang`,
so register a `dict` helper beside `t` in the FuncMap
([embed.md](embed.md#step-2-add-template-functions-optional)):

```go
// internal/assets/funcs.go
package assets

import "fmt"

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
```

### Full page

```html
<!-- internal/assets/views/settings/index.html -->
{{ define "title" }}{{ t .lang "settings.title" }}{{ end }}

{{ define "content" }}
<h1 class="mb-8 text-2xl font-semibold">{{ t .lang "settings.title" }}</h1>

<div id="settings-list" class="card divide-y divide-slate-200 dark:divide-slate-700">
  {{ range .settings }}
    {{ template "settings/_setting_row.html" (dict "lang" $.lang "setting" .) }}
  {{ else }}
    <p class="text-sm text-slate-600">{{ t .lang "settings.empty" }}</p>
  {{ end }}
</div>
{{ end }}
```

### Partial (the HTMX target)

```html
<!-- internal/assets/views/settings/_setting_row.html -->
<form id="setting-{{ .setting.Key }}" method="POST" action="/settings"
      hx-post="/settings" hx-trigger="change" hx-target="this" hx-swap="outerHTML"
      class="grid gap-2 py-4 md:grid-cols-3 md:items-center">
  <input type="hidden" name="csrf_token" value="{{ csrf }}">
  <input type="hidden" name="key" value="{{ .setting.Key }}">

  <label for="setting-{{ .setting.Key }}-value" class="label">
    {{ t .lang (printf "settings.%s.label" .setting.Key) }}
  </label>

  <div class="md:col-span-2">
    {{ if eq .setting.Key "theme" }}
    <select id="setting-{{ .setting.Key }}-value" name="value" class="select">
      <option value="light" {{ if eq .setting.Value "light" }}selected{{ end }}>{{ t .lang "settings.theme.light" }}</option>
      <option value="dark" {{ if eq .setting.Value "dark" }}selected{{ end }}>{{ t .lang "settings.theme.dark" }}</option>
    </select>
    {{ else }}
    <input id="setting-{{ .setting.Key }}-value" type="text" name="value"
           value="{{ .setting.Value }}" class="input"
           {{ with .errors.value }}aria-invalid="true" aria-describedby="setting-{{ $.setting.Key }}-error"{{ end }}>
    {{ end }}
    {{ with .errors.value }}
    <p id="setting-{{ $.setting.Key }}-error" class="form-error">{{ t $.lang . }}</p>
    {{ end }}
  </div>
</form>
```

`change` bubbles from the input to the form, so the form is the trigger and its
own target. The hidden `csrf_token` keeps the plain POST working without
JavaScript; HTMX requests also carry the header set on `<body>`.

---

## Controller Patterns

### Helpers

```go
// internal/controllers/htmx.go
package controllers

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

func isHTMX(c *gin.Context) bool {
	return c.GetHeader("HX-Request") == "true"
}

// render serves the partial to HTMX and the full page to everyone else.
// Vary keeps a cache from handing one to the other.
func render(c *gin.Context, status int, page, partial string, data gin.H) {
	c.Writer.Header().Add("Vary", "HX-Request")
	if isHTMX(c) {
		c.HTML(status, partial, data)
		return
	}
	c.HTML(status, page, data)
}

// trigger sets HX-Trigger to the given events. Call it once per response:
// a second call replaces the first.
func trigger(c *gin.Context, events map[string]any) {
	b, err := json.Marshal(events)
	if err != nil {
		return // events are built here from strings and maps; this cannot fail
	}
	c.Header("HX-Trigger", asciiJSON(b))
}

// toast raises the client-side toast. message is already translated.
func toast(c *gin.Context, level, message string) {
	trigger(c, map[string]any{"toast": map[string]string{"level": level, "message": message}})
}

// asciiJSON escapes every non-ASCII rune as \uXXXX. Browsers decode response
// headers as Latin-1, so a translated "Paramètres enregistrés" sent as raw
// UTF-8 arrives as "ParamÃ¨tres enregistrÃ©s". Non-ASCII only occurs inside
// JSON strings, so escaping it is always valid JSON.
func asciiJSON(b []byte) string {
	var sb strings.Builder
	for _, r := range string(b) {
		if r < utf8.RuneSelf {
			sb.WriteRune(r)
			continue
		}
		for _, u := range utf16.Encode([]rune{r}) {
			fmt.Fprintf(&sb, `\u%04x`, u)
		}
	}
	return sb.String()
}
```

### Full page or partial

```go
func ListNotifications(c *gin.Context) {
	lang := i18n.GetLang(c)
	userID := c.GetInt64("user_id")

	notifications, err := models.GetNotificationsByUser(c.Request.Context(), userID)
	if err != nil {
		renderError(c, err)
		return
	}

	render(c, http.StatusOK, "notifications/list.html", "notifications/_list.html", gin.H{
		"lang":          lang,
		"notifications": notifications,
	})
}
```

### Update: re-render the row, or redirect

```go
func UpdateSetting(c *gin.Context) {
	lang := i18n.GetLang(c)
	setting := models.Setting{
		UserID: c.GetInt64("user_id"),
		Key:    c.PostForm("key"),
		Value:  c.PostForm("value"),
	}

	// Validate returns field -> catalog key, e.g. {"value": "errors.setting.too_long"}.
	if errs := setting.Validate(); len(errs) > 0 {
		data := gin.H{"lang": lang, "setting": &setting, "errors": errs}
		if isHTMX(c) {
			c.HTML(http.StatusUnprocessableEntity, "settings/_setting_row.html", data)
			return
		}
		c.HTML(http.StatusUnprocessableEntity, "settings/index.html", data)
		return
	}

	if err := setting.Set(c.Request.Context()); err != nil {
		renderError(c, err)
		return
	}

	if isHTMX(c) {
		toast(c, "success", i18n.T(lang, "settings.saved"))
		c.HTML(http.StatusOK, "settings/_setting_row.html", gin.H{"lang": lang, "setting": &setting})
		return
	}
	c.Redirect(http.StatusSeeOther, "/settings")
}
```

Validation failure is **422**, and the configuration above swaps 422, so the
form comes back with its errors in place. Any other 4xx is not swapped —
see [Error Handling](#error-handling).

The non-HTMX branch redirects with 303 so a browser refresh does not resubmit.

### Delete: empty body, element removed

```go
func DeleteSetting(c *gin.Context) {
	setting := models.Setting{UserID: c.GetInt64("user_id"), Key: c.Param("key")}
	if err := setting.Delete(c.Request.Context()); err != nil {
		renderError(c, err)
		return
	}
	toast(c, "success", i18n.T(i18n.GetLang(c), "settings.deleted"))
	c.Status(http.StatusOK) // empty body + hx-swap="outerHTML" removes the target
}
```

Return 200, not 204: the configuration does not swap 204, so the row would stay.

---

## Response Headers

| Header | Effect | Example |
|--------|--------|---------|
| `HX-Trigger` | Dispatch client events when the response arrives | `{"toast": {…}}` |
| `HX-Trigger-After-Swap` | Dispatch them after the swap | `open-modal` |
| `HX-Redirect` | Full-page navigation | `/settings` |
| `HX-Reswap` | Override `hx-swap` | `beforeend` |
| `HX-Retarget` | Override `hx-target` | `#settings-list` |
| `HX-Push-Url` | Update the address bar | `/notifications?filter=unread` |

```go
c.Header("HX-Redirect", "/settings")
c.Status(http.StatusOK)
```

`HX-Trigger` is processed for every status, before HTMX decides whether to
swap. That is what lets an unswapped error response still raise a toast.

**One `HX-Trigger` per response.** `c.Header` replaces, so a second call drops
the first event. Send several events as one JSON object through `trigger`
below.

---

## Toast Notifications

The `toasts` Alpine component and its partial live in
[frontend.md](frontend.md#toasts-from-the-server). It listens for a `toast`
window event; the server raises one with `HX-Trigger`.

`toast` and `trigger` are in [the helpers](#helpers). The non-ASCII escaping
is not optional once the catalog has a second language: browsers decode
response headers as Latin-1, so an unescaped translated message arrives
garbled.

```go
toast(c, "success", i18n.T(lang, "settings.saved"))
// HX-Trigger: {"toast":{"level":"success","message":"Param\u00e8tres enregistr\u00e9s"}}
```

Every toast message comes from the catalog, and follows the copy rules in
[frontend.md](frontend.md#copy): the **Save changes** button produces a
**Changes saved** toast.

---

## Common UI Patterns

### Inline edit

```html
<!-- internal/assets/views/profile/_name_display.html -->
<div id="name" class="flex items-center gap-4">
  <span>{{ .user.Name }}</span>
  <button type="button" hx-get="/profile/name/edit" hx-target="#name" hx-swap="outerHTML"
          class="text-sm text-brand-600 hover:underline">{{ t .lang "common.edit" }}</button>
</div>
```

```html
<!-- internal/assets/views/profile/_name_form.html -->
<form id="name" method="POST" action="/profile/name"
      hx-post="/profile/name" hx-target="#name" hx-swap="outerHTML"
      class="flex items-center gap-2">
  <input type="hidden" name="csrf_token" value="{{ csrf }}">
  <label for="name-input" class="sr-only">{{ t .lang "user.name" }}</label>
  <input id="name-input" type="text" name="name" value="{{ .user.Name }}" class="input" autofocus>
  <button type="submit" class="btn-primary">{{ t .lang "common.save" }}</button>
  <button type="button" hx-get="/profile/name" hx-target="#name" hx-swap="outerHTML"
          class="btn-secondary">{{ t .lang "common.cancel" }}</button>
</form>
```

### Search with debounce

```html
<label for="q" class="sr-only">{{ t .lang "notifications.search" }}</label>
<input id="q" type="search" name="q" class="input"
       placeholder="{{ t .lang "notifications.search_placeholder" }}"
       hx-get="/notifications/search" hx-trigger="input changed delay:300ms, search"
       hx-target="#search-results">

<div id="search-results" aria-live="polite"></div>
```

```go
func SearchNotifications(c *gin.Context) {
	lang := i18n.GetLang(c)
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.HTML(http.StatusOK, "notifications/_empty.html", gin.H{"lang": lang})
		return
	}
	results, err := models.SearchNotifications(c.Request.Context(), c.GetInt64("user_id"), q)
	if err != nil {
		renderError(c, err)
		return
	}
	c.HTML(http.StatusOK, "notifications/_list.html", gin.H{"lang": lang, "notifications": results})
}
```

### Infinite scroll

```html
<!-- internal/assets/views/notifications/_list.html -->
{{ range .notifications }}
  {{ template "notifications/_row.html" (dict "lang" $.lang "notification" .) }}
{{ end }}
{{ if .nextCursor }}
<div hx-get="/notifications?after={{ .nextCursor }}" hx-trigger="revealed"
     hx-swap="outerHTML" class="flex justify-center py-4">
  <span class="inline-block size-4 animate-spin rounded-full border-2 border-current border-t-transparent"
        aria-hidden="true"></span>
  <span class="sr-only">{{ t .lang "common.loading" }}</span>
</div>
{{ end }}
```

Cursor, not offset — see [scale.md](scale.md).

### Delete with confirmation

```html
<!-- internal/assets/views/notifications/_row.html -->
<div id="notification-{{ .notification.ID }}"
     class="flex items-center gap-4 py-4 transition-opacity duration-200 [&.htmx-swapping]:opacity-0">
  <p class="flex-1 text-sm">{{ .notification.Body }}</p>
  <button type="button" hx-delete="/notifications/{{ .notification.ID }}"
          hx-confirm="{{ t .lang "notifications.delete_confirm" }}"
          hx-target="#notification-{{ .notification.ID }}" hx-swap="outerHTML swap:200ms"
          class="btn-danger">{{ t .lang "common.delete" }}</button>
</div>
```

`swap:200ms` holds the `htmx-swapping` class for the length of the fade, which
the arbitrary variant turns into `opacity-0`.

### Dependent selects

```html
<label for="country" class="label">{{ t .lang "address.country" }}</label>
<select id="country" name="country" class="select"
        hx-get="/regions" hx-trigger="change" hx-target="#region">
  <option value="">{{ t .lang "address.choose_country" }}</option>
  {{ range .countries }}<option value="{{ .Code }}">{{ .Name }}</option>{{ end }}
</select>

<label for="region" class="label mt-4">{{ t .lang "address.region" }}</label>
<select id="region" name="region" class="select">
  <option value="">{{ t .lang "address.choose_region" }}</option>
</select>
```

`/regions` returns only `<option>` elements; the default `innerHTML` swap
replaces the list.

### Modal loaded by HTMX

HTMX fetches the content; Alpine owns open and closed. The `modal` component
and shell are in [frontend.md](frontend.md#modal).

```html
<button type="button" hx-get="/notifications/new" hx-target="#modal-body" class="btn-primary">
  {{ t .lang "notifications.new" }}
</button>
```

```go
func NewNotificationForm(c *gin.Context) {
	c.Header("HX-Trigger-After-Swap", "open-modal") // after: the focus trap needs the form in place
	c.HTML(http.StatusOK, "notifications/_form.html", gin.H{"lang": i18n.GetLang(c)})
}
```

The swapped-in form starts with `<h2 id="modal-title">`, which the dialog's
`aria-labelledby` points at. When it is submitted successfully, respond with
`trigger(c, map[string]any{"close-modal": map[string]any{}, "toast": …})`,
which the `modal` component hears as `@close-modal.window`.

---

## Loading States

The indicator rules HTMX would have injected live in `input.css`:

```css
/* internal/assets/input.css (continued) */
@layer components {
  .htmx-indicator { opacity: 0; transition: opacity 200ms ease-in; }
  .htmx-request .htmx-indicator,
  .htmx-request.htmx-indicator { opacity: 1; }
}
```

Opacity rather than `display: none`, so a spinner reserves its space and the
button does not change width mid-request.

### Spinner and disable during the request

```html
<button type="submit" class="btn-primary" hx-post="/settings" hx-disabled-elt="this">
  <span class="htmx-indicator inline-block size-4 animate-spin rounded-full border-2
               border-current border-t-transparent" aria-hidden="true"></span>
  {{ t .lang "common.save_changes" }}
</button>
```

`hx-disabled-elt` prevents the double submit; the spinner tells the reader why
the button stopped responding.

### Skeleton

```html
<div hx-get="/dashboard/stats" hx-trigger="load" aria-busy="true">
  <div class="h-4 w-full animate-pulse rounded bg-slate-200 dark:bg-slate-700"></div>
  <div class="mt-2 h-4 w-2/3 animate-pulse rounded bg-slate-200 dark:bg-slate-700"></div>
</div>
```

---

## Error Handling

**HTMX 2 does not swap 4xx or 5xx responses by default.** A handler that
answers a failed validation with `400` and an error partial renders nothing:
the partial is discarded and the reader sees no change. The configuration
above settles the policy in one place:

| Status | Swapped | Meaning |
|--------|---------|---------|
| 2xx, 3xx | yes | Success |
| 204 | no | Success, nothing to show |
| **422** | **yes** | Validation failed; the response is the form with its errors |
| other 4xx, 5xx | no | Failure; the reader is told by a toast |

So: **validation errors are 422 and re-render the form; every other failure
sets a toast and is not swapped.** Never return 400 with a partial you expect
to see.

### The error helper

```go
// internal/controllers/errors.go
package controllers

import (
	stderrors "errors"
	"net/http"

	"github.com/gin-gonic/gin"

	apperrors "yourapp/internal/platform/errors"
	"yourapp/internal/platform/i18n"
	"yourapp/internal/platform/logger"
)

func renderError(c *gin.Context, err error) {
	lang := i18n.GetLang(c)

	var appErr *apperrors.AppError
	if !stderrors.As(err, &appErr) {
		appErr = &apperrors.AppError{Code: apperrors.CodeUnknown, Message: "unexpected error", Err: err}
	}
	status := appErr.HTTPStatus()

	log := logger.Get()
	if status >= http.StatusInternalServerError {
		log.Error().Str("code", appErr.Code).Err(err).Msg(appErr.Message)
	} else {
		log.Warn().Str("code", appErr.Code).Err(err).Msg(appErr.Message)
	}

	message := i18n.T(lang, "errors."+appErr.Code) // never the internal message
	if isHTMX(c) {
		toast(c, "error", message)
		c.Status(status) // not swapped: the toast is the whole response
		return
	}
	c.HTML(status, "errors/error.html", gin.H{"lang": lang, "message": message})
}
```

### Failures the server did not explain

A panic recovered by `gin.Recovery()`, a proxy's 502, or a dropped connection
arrives with no `HX-Trigger`. `app.js` catches those and shows the generic
message the layout put on `<body>`, so a failed request is never silent:

```js
// internal/assets/static/js/app.js (continued)
function requestFailed(event) {
  const xhr = event.detail.xhr;
  if (xhr && xhr.getResponseHeader('HX-Trigger')) return; // the server sent its own toast
  window.dispatchEvent(new CustomEvent('toast', {
    detail: { level: 'error', message: document.body.dataset.errorMessage },
  }));
}
document.addEventListener('htmx:responseError', requestFailed); // unswapped 4xx/5xx
document.addEventListener('htmx:sendError', requestFailed);     // network failure
```

The message comes from the catalog through `data-error-message`, so even this
fallback is translated.

### Field-level validation

```html
<label for="email" class="label">{{ t .lang "user.email" }}</label>
<input id="email" type="email" name="email" class="input mt-1"
       aria-describedby="email-error"
       hx-post="/validate/email" hx-trigger="blur changed" hx-target="#email-error">
<p id="email-error" class="form-error" aria-live="polite"></p>
```

```go
func ValidateEmail(c *gin.Context) {
	lang := i18n.GetLang(c)
	key := models.ValidateEmail(c.PostForm("email")) // "" or a catalog key
	if key == "" {
		c.String(http.StatusOK, "")
		return
	}
	c.String(http.StatusOK, i18n.T(lang, key)) // plain text into a <p>: nothing to escape into
}
```

The check returns **200**: a field that fails validation is an answer, not an
error, and the message has to be swapped in to be seen. `c.String` writes
`text/plain`; HTMX inserts it as markup, so the catalog text must not contain
HTML — keep catalog entries as plain text.

---

## Best Practices

**Do**

- Return a partial to HTMX and a full page otherwise, from the same templates.
- Give every form a real `method` and `action`.
- Use 422 for validation errors and re-render the form.
- Raise toasts with `HX-Trigger`, built by `trigger`, with catalog text.
- Debounce search (`delay:300ms`) and confirm destructive actions
  (`hx-confirm`, translated).
- Disable the submitting control (`hx-disabled-elt`) and show an indicator.
- Use `hx-push-url` where a swap changes what the page is.

**Do not**

- Return JSON from an HTMX endpoint.
- Return 400 with a partial you expect to be swapped.
- Use `hx-on`, `js:` values or trigger filters: the CSP blocks them.
- Interpolate Go values into `hx-vals` JSON; use inputs.
- Put state that must survive a swap inside the swapped element
  ([frontend.md](frontend.md#alpine--htmx)).
- Leave a live region off content that changes without navigation.

---

**Next:** [Frontend](frontend.md) | [Mobile Navigation](mobile-navigation.md) | [Security](core/security.md)
