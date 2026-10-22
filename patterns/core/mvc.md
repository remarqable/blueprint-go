# MVC Pattern Reference

> Complete guide to Models, Views, and Controllers in Go+Gin applications

---

## Table of Contents

- [Philosophy](#philosophy)
- [Models (Fat Models)](#models-fat-models)
- [Views (Templates)](#views-templates)
- [Controllers (Thin Controllers)](#controllers-thin-controllers)
- [Router Setup](#router-setup)
- [Request Flow](#request-flow)
- [Best Practices](#best-practices)

---

## Philosophy

### Fat Models, Thin Controllers

**Models** contain:
- Business logic (validation, calculations)
- Database access (CRUD, queries)
- Domain rules

**Controllers** contain:
- Parse input (query params, form data, JSON)
- Call model methods
- Render view or return JSON
- Handle HTTP-specific concerns (status codes, headers)

**Views** contain:
- HTML templates
- Minimal logic (loops, conditions)
- HTMX attributes for interactivity

---

## Models (Fat Models)

> **Universal Examples**: This guide uses **User** and **Setting** models - objects that every SaaS application needs. These patterns apply to any domain model (Product, Article, Order, etc.).

### User Model (Primary Example)

```go
// internal/models/user.go
package models

import (
  "context"
  "database/sql"
  "strings"
  "time"

  "yourapp/internal/platform/db"
  "yourapp/internal/platform/errors"
)

type User struct {
  ID        int64     `gorm:"primaryKey" json:"id"`
  Email     string    `gorm:"uniqueIndex" json:"email"`
  Name      string    `json:"name"`
  AvatarURL string    `json:"avatar_url"`
  CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
  UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// Validate performs business logic validation
func (u *User) Validate() error {
  u.Email = strings.TrimSpace(strings.ToLower(u.Email))
  u.Name = strings.TrimSpace(u.Name)

  if u.Email == "" {
    return errors.New(errors.CodeInvalidInput, "email is required")
  }
  if !isValidEmail(u.Email) {
    return errors.New(errors.CodeInvalidInput, "invalid email format")
  }
  if u.Name == "" {
    return errors.New(errors.CodeInvalidInput, "name is required")
  }
  if len(u.Name) > 100 {
    return errors.New(errors.CodeInvalidInput, "name too long (max 100 chars)")
  }
  return nil
}

// Create inserts a new user
func (u *User) Create(ctx context.Context) error {
  if err := u.Validate(); err != nil {
    return err
  }
  return db.Get().WithContext(ctx).Create(u).Error
}

// Update updates an existing user
func (u *User) Update(ctx context.Context) error {
  if err := u.Validate(); err != nil {
    return err
  }

  result := db.Get().WithContext(ctx).Save(u)
  if result.Error != nil {
    return result.Error
  }
  if result.RowsAffected == 0 {
    return errors.New(errors.CodeNotFound, "user not found")
  }
  return nil
}

// Delete deletes a user
func (u *User) Delete(ctx context.Context) error {
  result := db.Get().WithContext(ctx).Delete(u)
  if result.Error != nil {
    return result.Error
  }
  if result.RowsAffected == 0 {
    return errors.New(errors.CodeNotFound, "user not found")
  }
  return nil
}

func isValidEmail(email string) bool {
  // Simple email validation (use a proper library in production)
  return strings.Contains(email, "@") && strings.Contains(email, ".")
}
```

### Query Functions (Package-level)

```go
// GetUserByID retrieves a user by ID
func GetUserByID(ctx context.Context, userID int64) (*User, error) {
  var user User
  err := db.Get().WithContext(ctx).First(&user, userID).Error

  if goerrors.Is(err, gorm.ErrRecordNotFound) {
    return nil, errors.New(errors.CodeNotFound, "user not found")
  }
  return &user, err
}

// GetUserByEmail retrieves a user by email
func GetUserByEmail(ctx context.Context, email string) (*User, error) {
  email = strings.TrimSpace(strings.ToLower(email))

  var user User
  err := db.Get().WithContext(ctx).Where("email = ?", email).First(&user).Error

  if goerrors.Is(err, gorm.ErrRecordNotFound) {
    return nil, errors.New(errors.CodeNotFound, "user not found")
  }
  return &user, err
}

// ListUsers retrieves a page of users.
// Offset pagination is fine on a small table. On the dominant table use a
// cursor instead -- see patterns/scale.md#cursor-pagination.
func ListUsers(ctx context.Context, limit, offset int) ([]User, error) {
  if limit == 0 {
    limit = 50
  }

  var users []User
  err := db.Get().WithContext(ctx).
    Order("created_at DESC").
    Limit(limit).Offset(offset).
    Find(&users).Error

  return users, err
}
```

### Setting Model (Secondary Example - Key-Value Pattern)

```go
// internal/models/setting.go
package models

import (
  "context"
  goerrors "errors"
  "strings"
  "time"

  "gorm.io/gorm"
  "gorm.io/gorm/clause"

  "yourapp/internal/platform/db"
  "yourapp/internal/platform/errors"
)

type Setting struct {
  ID        int64     `gorm:"primaryKey" json:"id"`
  UserID    int64     `gorm:"not null;uniqueIndex:uq_setting_user_key,priority:1" json:"user_id"`
  Key       string    `gorm:"not null;uniqueIndex:uq_setting_user_key,priority:2" json:"key"`
  Value     string    `gorm:"not null" json:"value"`
  CreatedAt time.Time `json:"created_at"`
  UpdatedAt time.Time `json:"updated_at"`
}

// Validate performs validation
func (s *Setting) Validate() error {
  s.Key = strings.TrimSpace(s.Key)

  if s.Key == "" {
    return errors.New(errors.CodeInvalidInput, "setting key is required")
  }
  if s.UserID == 0 {
    return errors.New(errors.CodeInvalidInput, "user_id is required")
  }
  if len(s.Value) > 10000 {
    return errors.New(errors.CodeInvalidInput, "value too long (max 10000 chars)")
  }
  return nil
}

// Set creates or updates a setting (upsert pattern)
func (s *Setting) Set(ctx context.Context) error {
  if err := s.Validate(); err != nil {
    return err
  }

  // Upsert. Works on both PostgreSQL and SQLite.
  return db.Get().WithContext(ctx).Clauses(clause.OnConflict{
    Columns:   []clause.Column{{Name: "user_id"}, {Name: "key"}},
    DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
  }).Create(s).Error
}

// Delete removes a setting
func (s *Setting) Delete(ctx context.Context) error {
  result := db.Get().WithContext(ctx).
    Where("user_id = ? AND key = ?", s.UserID, s.Key).
    Delete(&Setting{})

  if result.Error != nil {
    return result.Error
  }
  if result.RowsAffected == 0 {
    return errors.New(errors.CodeNotFound, "setting not found")
  }
  return nil
}

// GetUserSetting retrieves a specific setting for a user
func GetUserSetting(ctx context.Context, userID int64, key string) (*Setting, error) {
  var setting Setting
  err := db.Get().WithContext(ctx).
    Where("user_id = ? AND key = ?", userID, key).
    First(&setting).Error

  if goerrors.Is(err, gorm.ErrRecordNotFound) {
    return nil, errors.New(errors.CodeNotFound, "setting not found")
  }
  return &setting, err
}

// GetUserSettings retrieves all settings for a user
func GetUserSettings(ctx context.Context, userID int64) ([]Setting, error) {
  var settings []Setting
  err := db.Get().WithContext(ctx).
    Where("user_id = ?", userID).
    Order("key").
    Find(&settings).Error

  return settings, err
}

// GetUserSettingsMap retrieves settings as a map for easy access
func GetUserSettingsMap(ctx context.Context, userID int64) (map[string]string, error) {
  settings, err := GetUserSettings(ctx, userID)
  if err != nil {
    return nil, err
  }

  result := make(map[string]string)
  for _, s := range settings {
    result[s.Key] = s.Value
  }

  return result, nil
}
```

### Model Organization

**Single file for simple models:**
```
internal/models/
  user.go          # User struct + all methods
  setting.go       # Setting struct + all methods
  notification.go  # Notification struct + all methods
```

**Multiple files for complex models:**
```
internal/models/
  user.go          # User struct + basic CRUD
  user_auth.go     # Authentication-related methods
  user_profile.go  # Profile management methods
  user_queries.go  # Complex query functions
```

---

## Views (Templates)

Templates live in `internal/assets/views/`, are embedded in the binary
([embed.md](../embed.md)) and are named by their path under `views/`:
`users/profile.html`. Styling is Tailwind utilities plus the small component
layer (`btn-primary`, `btn-secondary`, `label`, `input`, `select`,
`form-error`, `card`) defined in [frontend.md](../frontend.md#the-component-layer).

### Base Layout

The layout is `layouts/base.html`, shown in full in
[frontend.md](../frontend.md#base-layout): `lang` and `dir` from the request,
the compiled `app.css`, vendored HTMX and Alpine (CSP build), the CSRF token in
`hx-headers` on `<body>`, and `title` and `content` blocks for pages to fill.
A page is a file of `{{ define }}` blocks; the renderer wraps it in that layout
([embed.md](../embed.md#step-3-one-template-set-per-page)).

### User Profile Page

```html
<!-- internal/assets/views/users/profile.html -->
{{ define "title" }}{{ .user.Name }}{{ end }}

{{ define "content" }}
<div class="card mx-auto max-w-2xl">
  <div class="mb-6 flex items-center gap-4">
    {{ if .user.AvatarURL }}
    <img src="{{ .user.AvatarURL }}" alt="" class="size-20 rounded-full object-cover">
    {{ else }}
    <div aria-hidden="true"
         class="flex size-20 items-center justify-center rounded-full bg-brand-600 text-2xl font-semibold text-white">
      {{ substr .user.Name 0 1 }}
    </div>
    {{ end }}

    <div class="min-w-0 flex-1">
      <h1 class="text-2xl font-semibold">{{ .user.Name }}</h1>
      <p class="text-sm text-slate-600 dark:text-slate-300">{{ .user.Email }}</p>
    </div>

    <a href="/profile/edit" class="btn-secondary">{{ t .lang "common.edit" }}</a>
  </div>

  <dl class="grid gap-2 border-t border-slate-200 pt-6 text-sm sm:grid-cols-3 dark:border-slate-700">
    <dt class="text-slate-600 dark:text-slate-300">{{ t .lang "user.member_since" }}</dt>
    <dd class="sm:col-span-2">{{ .user.CreatedAt.Format "2006-01-02" }}</dd>

    <dt class="text-slate-600 dark:text-slate-300">{{ t .lang "user.last_updated" }}</dt>
    <dd class="sm:col-span-2">{{ .user.UpdatedAt.Format "2006-01-02" }}</dd>
  </dl>
</div>
{{ end }}
```

The avatar's `alt` is empty because the name beside it already says whose it
is; the initial is `aria-hidden` for the same reason.

### Settings Form

Each setting is its own small form, posted by HTMX on `change`, the shape
[htmx.md](../htmx.md#template-structure) uses. The hidden `key` says which
setting; the control is always named `value`, which is what `UpdateSetting`
reads.

```html
<!-- internal/assets/views/settings/form.html -->
{{ define "title" }}{{ t .lang "settings.title" }}{{ end }}

{{ define "content" }}
<h1 class="mb-8 text-2xl font-semibold">{{ t .lang "settings.title" }}</h1>

<section aria-labelledby="preferences-title" class="card mb-6 space-y-6">
  <h2 id="preferences-title" class="font-semibold">{{ t .lang "settings.preferences" }}</h2>

  <form method="POST" action="/settings" hx-post="/settings" hx-trigger="change"
        hx-target="#theme-status">
    <input type="hidden" name="csrf_token" value="{{ csrf }}">
    <input type="hidden" name="key" value="theme">
    <label for="theme" class="label">{{ t .lang "settings.theme.label" }}</label>
    <select id="theme" name="value" class="select mt-1">
      <option value="light" {{ if eq .settings.theme "light" }}selected{{ end }}>{{ t .lang "settings.theme.light" }}</option>
      <option value="dark" {{ if eq .settings.theme "dark" }}selected{{ end }}>{{ t .lang "settings.theme.dark" }}</option>
      <option value="auto" {{ if eq .settings.theme "auto" }}selected{{ end }}>{{ t .lang "settings.theme.auto" }}</option>
    </select>
    <p id="theme-status" role="status" class="mt-1 text-sm text-slate-600 dark:text-slate-300"></p>
  </form>

  <form method="POST" action="/settings" hx-post="/settings" hx-trigger="change"
        hx-target="#language-status">
    <input type="hidden" name="csrf_token" value="{{ csrf }}">
    <input type="hidden" name="key" value="language">
    <label for="language" class="label">{{ t .lang "settings.language.label" }}</label>
    <!-- Language names are endonyms: each in its own language, never translated. -->
    <select id="language" name="value" class="select mt-1">
      <option value="en" lang="en" {{ if eq .settings.language "en" }}selected{{ end }}>English</option>
      <option value="es" lang="es" {{ if eq .settings.language "es" }}selected{{ end }}>Español</option>
      <option value="fr" lang="fr" {{ if eq .settings.language "fr" }}selected{{ end }}>Français</option>
    </select>
    <p id="language-status" role="status" class="mt-1 text-sm text-slate-600 dark:text-slate-300"></p>
  </form>

  <form method="POST" action="/settings" hx-post="/settings" hx-trigger="change"
        hx-target="#timezone-status">
    <input type="hidden" name="csrf_token" value="{{ csrf }}">
    <input type="hidden" name="key" value="timezone">
    <label for="timezone" class="label">{{ t .lang "settings.timezone.label" }}</label>
    <select id="timezone" name="value" class="select mt-1">
      <option value="UTC" {{ if eq .settings.timezone "UTC" }}selected{{ end }}>UTC</option>
      <option value="America/New_York" {{ if eq .settings.timezone "America/New_York" }}selected{{ end }}>America/New_York</option>
      <option value="America/Los_Angeles" {{ if eq .settings.timezone "America/Los_Angeles" }}selected{{ end }}>America/Los_Angeles</option>
    </select>
    <p id="timezone-status" role="status" class="mt-1 text-sm text-slate-600 dark:text-slate-300"></p>
  </form>
</section>

<section aria-labelledby="notifications-title" class="card">
  <h2 id="notifications-title" class="mb-4 font-semibold">{{ t .lang "settings.notifications" }}</h2>

  <form method="POST" action="/settings" hx-post="/settings" hx-trigger="change"
        hx-target="#email-notifications-status">
    <input type="hidden" name="csrf_token" value="{{ csrf }}">
    <input type="hidden" name="key" value="email_notifications">
    <div class="flex items-center gap-2">
      <!-- Checked sends "true" first; c.PostForm returns the first value, so
           the hidden "false" after it only counts when the box is unchecked. -->
      <input type="checkbox" id="email-notifications" name="value" value="true"
             class="size-4 accent-brand-600"
             {{ if eq .settings.email_notifications "true" }}checked{{ end }}>
      <input type="hidden" name="value" value="false">
      <label for="email-notifications" class="text-sm">{{ t .lang "settings.email_notifications" }}</label>
    </div>
    <p id="email-notifications-status" role="status" class="mt-1 text-sm text-slate-600 dark:text-slate-300"></p>
  </form>
</section>
{{ end }}
```

`role="status"` makes the "Saved" text `UpdateSetting` swaps in reach a screen
reader; without it the confirmation is purely visual.

### User Edit Form

```html
<!-- internal/assets/views/users/edit.html -->
{{ define "title" }}{{ t .lang "user.edit_profile" }}{{ end }}

{{ define "content" }}
<div class="card mx-auto max-w-2xl">
  <h1 class="mb-6 text-2xl font-semibold">{{ t .lang "user.edit_profile" }}</h1>

  {{ with .errors.form }}
  <p role="alert" class="form-error mb-4">{{ . }}</p>
  {{ end }}

  <form method="POST" action="/profile" class="space-y-4">
    <input type="hidden" name="csrf_token" value="{{ csrf }}">

    <div>
      <label for="name" class="label">{{ t .lang "user.name" }}</label>
      <input type="text" id="name" name="name" class="input mt-1"
             value="{{ .user.Name }}" maxlength="100" required
             {{ with .errors.name }}aria-invalid="true" aria-describedby="name-error"{{ end }}>
      {{ with .errors.name }}
      <p id="name-error" class="form-error">{{ . }}</p>
      {{ end }}
    </div>

    <div>
      <label for="email" class="label">{{ t .lang "user.email" }}</label>
      <input type="email" id="email" class="input mt-1" value="{{ .user.Email }}"
             disabled aria-describedby="email-help">
      <p id="email-help" class="mt-1 text-sm text-slate-600 dark:text-slate-300">
        {{ t .lang "user.email_locked" }}
      </p>
    </div>

    <div>
      <label for="avatar_url" class="label">{{ t .lang "user.avatar_url" }}</label>
      <input type="url" id="avatar_url" name="avatar_url" class="input mt-1"
             value="{{ .user.AvatarURL }}" placeholder="https://example.com/avatar.jpg"
             {{ with .errors.avatar_url }}aria-invalid="true" aria-describedby="avatar-url-error"{{ end }}>
      {{ with .errors.avatar_url }}
      <p id="avatar-url-error" class="form-error">{{ . }}</p>
      {{ end }}
    </div>

    <div class="flex gap-2">
      <button type="submit" class="btn-primary">{{ t .lang "common.save_changes" }}</button>
      <a href="/profile" class="btn-secondary">{{ t .lang "common.cancel" }}</a>
    </div>
  </form>
</div>
{{ end }}
```

`aria-invalid` and `aria-describedby` tie each error to its field
([frontend.md](../frontend.md#forms)). The messages in `.errors` are already
translated by the handler.

---

## Controllers (Thin Controllers)

### Users Controller

```go
// internal/controllers/users_controller.go
package controllers

import (
  stderrors "errors"
  "net/http"

  "github.com/gin-gonic/gin"
  "yourapp/internal/models"
  apperrors "yourapp/internal/platform/errors"
  "yourapp/internal/platform/i18n"
)

// ShowProfile shows the user's profile
func ShowProfile(c *gin.Context) {
  userID := c.GetInt64("user_id")
  lang := i18n.GetLang(c)

  user, err := models.GetUserByID(c.Request.Context(), userID)
  if err != nil {
    handleError(c, err)
    return
  }

  c.HTML(http.StatusOK, "users/profile.html", gin.H{
    "lang": lang,
    "user": user,
  })
}

// EditProfile shows the edit profile form
func EditProfile(c *gin.Context) {
  userID := c.GetInt64("user_id")
  lang := i18n.GetLang(c)

  user, err := models.GetUserByID(c.Request.Context(), userID)
  if err != nil {
    handleError(c, err)
    return
  }

  c.HTML(http.StatusOK, "users/edit.html", gin.H{
    "lang": lang,
    "user": user,
  })
}

// UpdateProfile handles profile updates
func UpdateProfile(c *gin.Context) {
  userID := c.GetInt64("user_id")
  lang := i18n.GetLang(c)

  user, err := models.GetUserByID(c.Request.Context(), userID)
  if err != nil {
    handleError(c, err)
    return
  }

  // Bind form data
  if err := c.ShouldBind(user); err != nil {
    c.HTML(http.StatusUnprocessableEntity, "users/edit.html", gin.H{
      "lang":   lang,
      "user":   user,
      "errors": map[string]string{"form": i18n.T(lang, "errors.invalid_input")},
    })
    return
  }

  // Update in database
  if err := user.Update(c.Request.Context()); err != nil {
    // Handle validation errors. appErr.Message is for the log; the reader
    // gets the catalog's text for the code.
    var appErr *apperrors.AppError
    if stderrors.As(err, &appErr) && appErr.Code == apperrors.CodeInvalidInput {
      c.HTML(http.StatusUnprocessableEntity, "users/edit.html", gin.H{
        "lang":   lang,
        "user":   user,
        "errors": map[string]string{"form": i18n.T(lang, "errors."+appErr.Code)},
      })
      return
    }
    handleError(c, err)
    return
  }

  // Success - redirect to profile
  c.Redirect(http.StatusSeeOther, "/profile")
}
```

### Settings Controller

```go
// internal/controllers/settings_controller.go
package controllers

import (
  stderrors "errors"
  "net/http"

  "github.com/gin-gonic/gin"
  "yourapp/internal/models"
  apperrors "yourapp/internal/platform/errors"
  "yourapp/internal/platform/i18n"
  "yourapp/internal/platform/logger"
)

// ShowSettings shows the settings page
func ShowSettings(c *gin.Context) {
  userID := c.GetInt64("user_id")
  lang := i18n.GetLang(c)

  // Get all user settings as a map
  settings, err := models.GetUserSettingsMap(c.Request.Context(), userID)
  if err != nil {
    handleError(c, err)
    return
  }

  c.HTML(http.StatusOK, "settings/form.html", gin.H{
    "lang":     lang,
    "settings": settings,
  })
}

// UpdateSetting handles individual setting updates (for HTMX)
func UpdateSetting(c *gin.Context) {
  userID := c.GetInt64("user_id")

  key := c.PostForm("key")
  value := c.PostForm("value")

  if key == "" {
    c.String(http.StatusBadRequest, i18n.T(i18n.GetLang(c), "errors."+apperrors.CodeInvalidInput))
    return
  }

  // Create/update setting
  setting := &models.Setting{
    UserID: userID,
    Key:    key,
    Value:  value,
  }

  if err := setting.Set(c.Request.Context()); err != nil {
    handleError(c, err)
    return
  }

  // HTMX response: the confirmation for the setting's role="status" element
  if c.GetHeader("HX-Request") == "true" {
    c.String(http.StatusOK, i18n.T(i18n.GetLang(c), "common.saved"))
    return
  }

  // Regular response: redirect
  c.Redirect(http.StatusSeeOther, "/settings")
}

// DeleteSetting handles setting deletion
func DeleteSetting(c *gin.Context) {
  userID := c.GetInt64("user_id")
  key := c.Param("key")

  setting := &models.Setting{
    UserID: userID,
    Key:    key,
  }

  if err := setting.Delete(c.Request.Context()); err != nil {
    handleError(c, err)
    return
  }

  c.Status(http.StatusOK)
}

// Helper function to handle errors consistently
func handleError(c *gin.Context, err error) {
  log := logger.FromContext(c)

  var appErr *apperrors.AppError
  if stderrors.As(err, &appErr) {
    log.Error().Err(err).Str("code", appErr.Code).Msg("application error")

    // HTMX request: return error message
    if c.GetHeader("HX-Request") == "true" {
      c.String(appErr.HTTPStatus(), i18n.T(i18n.GetLang(c), "errors."+appErr.Code))
      return
    }

    // Regular request: render error page
    c.HTML(appErr.HTTPStatus(), "errors/error.html", gin.H{
      "lang":    i18n.GetLang(c),
      "status":  appErr.HTTPStatus(),
      "message": i18n.T(i18n.GetLang(c), "errors."+appErr.Code),
    })
    return
  }

  // Unknown error
  log.Error().Err(err).Msg("internal server error")
  c.HTML(http.StatusInternalServerError, "errors/500.html", gin.H{
    "lang": i18n.GetLang(c),
  })
}
```

---

## Router Setup

### Template Loading

Templates are loaded by `assets.NewRenderer()`
([embed.md](../embed.md#implementation)), not by `LoadHTMLGlob`. Two reasons:

- **Names keep their directories.** Gin's `LoadHTMLGlob` names templates by
  file name, so `layouts/base.html` becomes `base.html`, and its glob does not
  reach `profiles/mobile/profile.html`. The renderer walks `views/` at any
  depth and names each template by its path under `views/`.
- **One template set per page.** Every page defines `title` and `content`. In
  one shared set each definition replaces the previous, and every route renders
  the page parsed last. The renderer parses layouts and partials once and gives
  each page its own clone of them
  ([embed.md](../embed.md#step-3-one-template-set-per-page)).

Handlers just name the page: `c.HTML(http.StatusOK, "users/profile.html", data)`.
With `APP_ENV=dev` the renderer re-reads `views/` on every render, so a
template edit shows on refresh.

### Full Router Example

```go
// internal/controllers/router.go
package controllers

import (
  "net/http"
  "os"

  "github.com/gin-gonic/gin"
  "yourapp/internal/assets"
  "yourapp/internal/middleware"
  "yourapp/internal/platform/logger"
)

func SetupRouter() *gin.Engine {
  router := gin.New()

  // Global middleware
  router.Use(gin.Recovery())
  router.Use(logger.Middleware())
  router.Use(middleware.RequestID())

  // Templates: one set per page, embedded (embed.md)
  renderer, err := assets.NewRenderer()
  if err != nil {
    panic(err) // a template that does not parse is a build error
  }
  router.HTMLRender = renderer

  // Static files, registered before CSRF so they never get a token cookie
  router.StaticFS("/static", http.FS(assets.StaticFS()))

  // CSRF on every HTML route: every page renders {{ csrf }}, the login form
  // included (security.md#csrf-protection)
  router.Use(middleware.CSRF([]byte(os.Getenv("CSRF_SECRET")), os.Getenv("APP_URL")))

  // Public routes
  public := router.Group("/")
  {
    public.GET("/", HomeIndex)
    public.GET("/about", HomeAbout)

    // Auth routes
    public.GET("/login", AuthShowLogin)
    public.POST("/login", AuthLogin)
    public.POST("/logout", AuthLogout)
  }

  // Protected routes (require authentication)
  protected := router.Group("/")
  protected.Use(middleware.Auth())
  {
    // Profile
    protected.GET("/profile", ShowProfile)
    protected.GET("/profile/edit", EditProfile)
    protected.POST("/profile", UpdateProfile)

    // Settings
    protected.GET("/settings", ShowSettings)
    protected.POST("/settings", UpdateSetting)
    protected.DELETE("/settings/:key", DeleteSetting)
  }

  return router
}
```

---

## Request Flow

### Typical Request Lifecycle

1. **Request arrives** → Gin router
2. **Global middleware** runs (logger, recovery, request ID, CSRF)
3. **Route matched**
4. **Route middleware** runs (auth)
5. **Controller** function called
   - Extract user ID from context
   - Parse input (query params, form data, JSON)
   - Call model method
   - Handle errors
   - Render view or return JSON
6. **Response sent** to client

### Example Flow (Update Profile)

```
POST /profile
  ↓
[Global Middleware]
  - Logger: Log incoming request
  - Recovery: Catch panics
  - RequestID: Add request ID to context
  - CSRF: Check origin and session token
  ↓
[Route Middleware]
  - Auth: Verify user session
  ↓
[Controller: UpdateProfile]
  - Get user ID from context
  - Get user from database
  - Bind form data to User struct
  - Call user.Update(ctx)
  ↓
[Model: User.Update]
  - Validate user data
  - Update in database
  - Return error or success
  ↓
[Controller: Render Response]
  - If error: Re-render form with errors
  - Else: Redirect to /profile
  ↓
[Response sent to client]
```

---

## Best Practices

### Models

**Do:**
- Keep all business logic in models
- Use pointer receivers for methods
- Return specific errors (use custom error types)
- Always pass context to DB calls
- Validate data before database operations
- Use transactions for multi-step operations

**Don't:**
- Don't put HTTP logic in models (no `gin.Context`)
- Don't access global state (except `db.Get()`, and never `db.Unscoped()`)
- Don't log in models (return errors instead)
- Don't hardcode timeouts (pass a context with one: `tx.WithContext(ctx)`)

### Controllers

**Do:**
- Keep controllers thin (just HTTP orchestration)
- Extract user ID, language from context
- Use HTTP status code constants (`http.StatusOK`)
- Handle HTMX requests separately
- Return consistent error responses
- Log errors before returning to user

**Don't:**
- Don't put business logic in controllers
- Don't write raw SQL in controllers
- Don't return internal error details to users
- Don't forget to validate CSRF tokens on state-changing requests

### Views

**Do:**
- Use partials for reusable components (prefix with `_`)
- Use HTMX attributes for interactivity
- Use Tailwind utilities and the component layer from [frontend.md](../frontend.md#the-component-layer) (`btn-primary`, `input`, `card`); no hand-written CSS
- Use logical properties (`ms-`, `pe-`, `text-start`) so RTL needs no override
- Use i18n for all user-visible text
- Escape user input (html/template does this automatically)

**Don't:**
- Don't put complex logic in templates
- Don't use inline `style`, `<script>`, `on*=` or `hx-on`: the CSP blocks them ([security.md](security.md#content-security-policy))
- Don't hardcode text (use i18n keys)
- Don't trust user input (always escape)

---

**Next:** [HTMX Patterns](../htmx.md) | [Frontend Guide](../frontend.md) | [Testing](testing.md)
