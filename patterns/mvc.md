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
  ctx, cancel := db.WithTimeout(ctx, 0)
  defer cancel()

  var user User
  err := db.Get().GetContext(ctx, &user,
    `SELECT * FROM "user" WHERE id = $1`,
    userID)

  if err == sql.ErrNoRows {
    return nil, errors.New(errors.CodeNotFound, "user not found")
  }

  return &user, err
}

// GetUserByEmail retrieves a user by email
func GetUserByEmail(ctx context.Context, email string) (*User, error) {
  ctx, cancel := db.WithTimeout(ctx, 0)
  defer cancel()

  email = strings.TrimSpace(strings.ToLower(email))

  var user User
  err := db.Get().GetContext(ctx, &user,
    `SELECT * FROM "user" WHERE email = $1`,
    email)

  if err == sql.ErrNoRows {
    return nil, errors.New(errors.CodeNotFound, "user not found")
  }

  return &user, err
}

// ListUsers retrieves all users (with optional pagination)
func ListUsers(ctx context.Context, limit, offset int) ([]User, error) {
  ctx, cancel := db.WithTimeout(ctx, 0)
  defer cancel()

  if limit == 0 {
    limit = 50 // default
  }

  var users []User
  err := db.Get().SelectContext(ctx, &users,
    `SELECT * FROM "user" ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
    limit, offset)

  return users, err
}
```

### Setting Model (Secondary Example - Key-Value Pattern)

```go
// internal/models/setting.go
package models

import (
  "context"
  "database/sql"
  "strings"
  "time"

  "yourapp/internal/platform/db"
  "yourapp/internal/platform/errors"
)

type Setting struct {
  ID        int64     `db:"id" json:"id"`
  UserID    int64     `db:"user_id" json:"user_id"`
  Key       string    `db:"key" json:"key"`
  Value     string    `db:"value" json:"value"`
  CreatedAt time.Time `db:"created_at" json:"created_at"`
  UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
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

  ctx, cancel := db.WithTimeout(ctx, 0)
  defer cancel()

  // PostgreSQL upsert
  return db.Get().QueryRowContext(ctx,
    `INSERT INTO setting (user_id, key, value)
     VALUES ($1, $2, $3)
     ON CONFLICT (user_id, key)
     DO UPDATE SET value = EXCLUDED.value, updated_at = CURRENT_TIMESTAMP
     RETURNING id, created_at, updated_at`,
    s.UserID, s.Key, s.Value,
  ).Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)
}

// Delete removes a setting
func (s *Setting) Delete(ctx context.Context) error {
  ctx, cancel := db.WithTimeout(ctx, 0)
  defer cancel()

  result, err := db.Get().ExecContext(ctx,
    `DELETE FROM setting WHERE user_id = $1 AND key = $2`,
    s.UserID, s.Key)

  if err != nil {
    return err
  }

  rows, _ := result.RowsAffected()
  if rows == 0 {
    return errors.New(errors.CodeNotFound, "setting not found")
  }

  return nil
}

// GetUserSetting retrieves a specific setting for a user
func GetUserSetting(ctx context.Context, userID int64, key string) (*Setting, error) {
  ctx, cancel := db.WithTimeout(ctx, 0)
  defer cancel()

  var setting Setting
  err := db.Get().GetContext(ctx, &setting,
    `SELECT * FROM setting WHERE user_id = $1 AND key = $2`,
    userID, key)

  if err == sql.ErrNoRows {
    return nil, errors.New(errors.CodeNotFound, "setting not found")
  }

  return &setting, err
}

// GetUserSettings retrieves all settings for a user
func GetUserSettings(ctx context.Context, userID int64) ([]Setting, error) {
  ctx, cancel := db.WithTimeout(ctx, 0)
  defer cancel()

  var settings []Setting
  err := db.Get().SelectContext(ctx, &settings,
    `SELECT * FROM setting WHERE user_id = $1 ORDER BY key`,
    userID)

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

### Base Layout

```html
<!-- views/layouts/base.html -->
<!DOCTYPE html>
<html lang="{{ .lang }}" dir="{{ if .isRTL }}rtl{{ else }}ltr{{ end }}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{ block "title" . }}Your App{{ end }}</title>

  <!-- Bootstrap CSS -->
  <link href="/static/css/bootstrap.min.css" rel="stylesheet">

  <!-- Custom CSS (brand overrides only) -->
  <link href="/static/css/app.css" rel="stylesheet">

  <!-- HTMX -->
  <script src="/static/js/htmx.min.js"></script>

  {{ block "head" . }}{{ end }}
</head>
<body class="bg-light">
  <!-- Navbar -->
  {{ template "partials/_navbar.html" . }}

  <!-- master Content -->
  <main class="container py-4">
    <!-- Flash messages -->
    {{ if .flash }}
    <div class="alert alert-{{ .flash.type }} alert-dismissible fade show" role="alert">
      {{ .flash.message }}
      <button type="button" class="btn-close" data-bs-dismiss="alert"></button>
    </div>
    {{ end }}

    <!-- Page content -->
    {{ block "content" . }}{{ end }}
  </main>

  <!-- Footer -->
  <footer class="mt-auto py-3 bg-white border-top">
    <div class="container text-center text-muted">
      <small>&copy; 2024 Your App</small>
    </div>
  </footer>

  <!-- Bootstrap JS -->
  <script src="/static/js/bootstrap.bundle.min.js"></script>

  {{ block "scripts" . }}{{ end }}
</body>
</html>
```

### User Profile Page

```html
<!-- views/users/profile.html -->
{{ define "title" }}{{ .user.Name }} - Profile{{ end }}

{{ define "content" }}
<div class="row justify-content-center">
  <div class="col-md-8">
    <div class="card">
      <div class="card-body">
        <div class="d-flex align-items-center mb-4">
          {{ if .user.AvatarURL }}
          <img src="{{ .user.AvatarURL }}" class="rounded-circle me-3" width="80" height="80" alt="Avatar">
          {{ else }}
          <div class="rounded-circle bg-primary text-white d-flex align-items-center justify-content-center me-3"
               style="width: 80px; height: 80px; font-size: 2rem;">
            {{ substr .user.Name 0 1 }}
          </div>
          {{ end }}

          <div class="flex-grow-1">
            <h2 class="mb-0">{{ .user.Name }}</h2>
            <p class="text-muted mb-0">{{ .user.Email }}</p>
          </div>

          <a href="/profile/edit" class="btn btn-outline-primary">
            Edit Profile
          </a>
        </div>

        <hr>

        <dl class="row mb-0">
          <dt class="col-sm-3">Member Since</dt>
          <dd class="col-sm-9">{{ .user.CreatedAt.Format "January 2, 2006" }}</dd>

          <dt class="col-sm-3">Last Updated</dt>
          <dd class="col-sm-9">{{ .user.UpdatedAt.Format "January 2, 2006" }}</dd>
        </dl>
      </div>
    </div>
  </div>
</div>
{{ end }}
```

### Settings Form

```html
<!-- views/settings/form.html -->
{{ define "title" }}Settings{{ end }}

{{ define "content" }}
<div class="row justify-content-center">
  <div class="col-md-8">
    <h2 class="mb-4">Settings</h2>

    <div class="card mb-3">
      <div class="card-header">
        <h5 class="mb-0">Preferences</h5>
      </div>
      <div class="card-body">
        <!-- Theme Setting -->
        <div class="mb-3">
          <label for="theme" class="form-label">Theme</label>
          <select id="theme" name="theme" class="form-select"
                  hx-post="/settings"
                  hx-trigger="change"
                  hx-vals='{"key": "theme"}'
                  hx-target="#theme-status"
                  hx-swap="innerHTML">
            <option value="light" {{ if eq .settings.theme "light" }}selected{{ end }}>Light</option>
            <option value="dark" {{ if eq .settings.theme "dark" }}selected{{ end }}>Dark</option>
            <option value="auto" {{ if eq .settings.theme "auto" }}selected{{ end }}>Auto</option>
          </select>
          <div id="theme-status" class="form-text"></div>
        </div>

        <!-- Language Setting -->
        <div class="mb-3">
          <label for="language" class="form-label">Language</label>
          <select id="language" name="language" class="form-select"
                  hx-post="/settings"
                  hx-trigger="change"
                  hx-vals='{"key": "language"}'
                  hx-target="#language-status"
                  hx-swap="innerHTML">
            <option value="en" {{ if eq .settings.language "en" }}selected{{ end }}>English</option>
            <option value="es" {{ if eq .settings.language "es" }}selected{{ end }}>Español</option>
            <option value="fr" {{ if eq .settings.language "fr" }}selected{{ end }}>Français</option>
          </select>
          <div id="language-status" class="form-text"></div>
        </div>

        <!-- Timezone Setting -->
        <div class="mb-3">
          <label for="timezone" class="form-label">Timezone</label>
          <select id="timezone" name="timezone" class="form-select"
                  hx-post="/settings"
                  hx-trigger="change"
                  hx-vals='{"key": "timezone"}'
                  hx-target="#timezone-status"
                  hx-swap="innerHTML">
            <option value="UTC" {{ if eq .settings.timezone "UTC" }}selected{{ end }}>UTC</option>
            <option value="America/New_York" {{ if eq .settings.timezone "America/New_York" }}selected{{ end }}>Eastern Time</option>
            <option value="America/Los_Angeles" {{ if eq .settings.timezone "America/Los_Angeles" }}selected{{ end }}>Pacific Time</option>
          </select>
          <div id="timezone-status" class="form-text"></div>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-header">
        <h5 class="mb-0">Notifications</h5>
      </div>
      <div class="card-body">
        <!-- Email Notifications -->
        <div class="form-check form-switch mb-2">
          <input class="form-check-input" type="checkbox" id="email-notifications"
                 {{ if eq .settings.email_notifications "true" }}checked{{ end }}
                 hx-post="/settings"
                 hx-trigger="change"
                 hx-vals='{"key": "email_notifications"}'
                 hx-include="[name='email-notifications']"
                 hx-target="#email-notifications-status">
          <label class="form-check-label" for="email-notifications">
            Email Notifications
          </label>
          <div id="email-notifications-status" class="form-text"></div>
        </div>
      </div>
    </div>
  </div>
</div>
{{ end }}
```

### User Edit Form

```html
<!-- views/users/edit.html -->
{{ define "title" }}Edit Profile{{ end }}

{{ define "content" }}
<div class="row justify-content-center">
  <div class="col-md-8">
    <div class="card">
      <div class="card-body">
        <h2 class="card-title mb-4">Edit Profile</h2>

        <form method="POST" action="/profile">
          <!-- CSRF token -->
          <input type="hidden" name="csrf_token" value="{{ .csrf_token }}">

          <!-- Name -->
          <div class="mb-3">
            <label for="name" class="form-label">Name</label>
            <input
              type="text"
              class="form-control {{ if .errors.name }}is-invalid{{ end }}"
              id="name"
              name="name"
              value="{{ .user.Name }}"
              maxlength="100"
              required>
            {{ if .errors.name }}
            <div class="invalid-feedback">{{ .errors.name }}</div>
            {{ end }}
          </div>

          <!-- Email (read-only) -->
          <div class="mb-3">
            <label for="email" class="form-label">Email</label>
            <input
              type="email"
              class="form-control"
              id="email"
              value="{{ .user.Email }}"
              disabled>
            <div class="form-text">Email cannot be changed</div>
          </div>

          <!-- Avatar URL -->
          <div class="mb-3">
            <label for="avatar_url" class="form-label">Avatar URL</label>
            <input
              type="url"
              class="form-control {{ if .errors.avatar_url }}is-invalid{{ end }}"
              id="avatar_url"
              name="avatar_url"
              value="{{ .user.AvatarURL }}"
              placeholder="https://example.com/avatar.jpg">
            {{ if .errors.avatar_url }}
            <div class="invalid-feedback">{{ .errors.avatar_url }}</div>
            {{ end }}
          </div>

          <!-- Actions -->
          <div class="d-flex gap-2">
            <button type="submit" class="btn btn-primary">
              Save Changes
            </button>
            <a href="/profile" class="btn btn-secondary">
              Cancel
            </a>
          </div>
        </form>
      </div>
    </div>
  </div>
</div>
{{ end }}
```

---

## Controllers (Thin Controllers)

### Users Controller

```go
// internal/controllers/users_controller.go
package controllers

import (
  "net/http"
  "strconv"

  "github.com/gin-gonic/gin"
  "yourapp/internal/models"
  "yourapp/internal/platform/i18n"
  "yourapp/internal/platform/logger"
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
    c.HTML(http.StatusBadRequest, "users/edit.html", gin.H{
      "lang":   lang,
      "user":   user,
      "errors": map[string]string{"form": "Invalid input"},
    })
    return
  }

  // Update in database
  if err := user.Update(c.Request.Context()); err != nil {
    // Handle validation errors
    if appErr, ok := err.(*errors.AppError); ok && appErr.Code == errors.CodeInvalidInput {
      c.HTML(http.StatusBadRequest, "users/edit.html", gin.H{
        "lang":   lang,
        "user":   user,
        "errors": map[string]string{"form": appErr.Message},
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
  "net/http"

  "github.com/gin-gonic/gin"
  "yourapp/internal/models"
  "yourapp/internal/platform/i18n"
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
    c.String(http.StatusBadRequest, "Setting key is required")
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

  // HTMX response: return success message
  if c.GetHeader("HX-Request") == "true" {
    c.String(http.StatusOK, "Saved")
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

  if appErr, ok := err.(*errors.AppError); ok {
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

### Template Loading (Nested Directories)

**Problem:** Gin's `LoadHTMLGlob("views/**/*")` doesn't preserve directory paths in template names. Templates in `views/layouts/base.html` get registered as just `base.html`, so `c.HTML(200, "layouts/base.html", data)` fails.

**Solution:** Use manual loading to preserve directory structure:

```go
// internal/controllers/router.go
import (
    "html/template"
    "os"
    "path/filepath"
    "strings"
)

func loadTemplates() *template.Template {
    tmpl := template.New("")

    patterns := []string{
        "views/layouts/*.html",
        "views/partials/*.html",
        "views/users/*.html",
        "views/settings/*.html",
        // Add new view directories here
    }

    for _, pattern := range patterns {
        files, _ := filepath.Glob(pattern)
        for _, file := range files {
            name := strings.TrimPrefix(file, "views/")
            content, _ := os.ReadFile(file)
            tmpl = template.Must(tmpl.New(name).Parse(string(content)))
        }
    }
    return tmpl
}
```

Templates are then referenced as `"layouts/base.html"`, `"partials/_toast.html"`, etc.

> **For single-binary deployment** with embedded templates and static files, see [embed.md](embed.md)

### Full Router Example

```go
// internal/controllers/router.go
package controllers

import (
  "github.com/gin-gonic/gin"
  "yourapp/internal/middleware"
  "yourapp/internal/platform/logger"
)

func SetupRouter() *gin.Engine {
  router := gin.New()

  // Global middleware
  router.Use(gin.Recovery())
  router.Use(logger.Middleware())
  router.Use(middleware.RequestID())

  // Static files
  router.Static("/static", "./static")

  // Load templates (with directory structure preserved)
  router.SetHTMLTemplate(loadTemplates())

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
  protected.Use(middleware.CSRF())
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
2. **Global middleware** runs (logger, recovery, request ID)
3. **Route matched** → Controller function called
4. **Route middleware** runs (auth, CSRF)
5. **Controller**:
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
  ↓
[Route Middleware]
  - Auth: Verify user session
  - CSRF: Validate CSRF token
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

✅ **Do:**
- Keep all business logic in models
- Use pointer receivers for methods
- Return specific errors (use custom error types)
- Always pass context to DB calls
- Validate data before database operations
- Use transactions for multi-step operations

❌ **Don't:**
- Don't put HTTP logic in models (no `gin.Context`)
- Don't access global state (except `db.Get()`)
- Don't log in models (return errors instead)
- Don't hardcode timeouts (use `db.WithTimeout`)

### Controllers

✅ **Do:**
- Keep controllers thin (just HTTP orchestration)
- Extract user ID, language from context
- Use HTTP status code constants (`http.StatusOK`)
- Handle HTMX requests separately
- Return consistent error responses
- Log errors before returning to user

❌ **Don't:**
- Don't put business logic in controllers
- Don't write raw SQL in controllers
- Don't return internal error details to users
- Don't forget to validate CSRF tokens on state-changing requests

### Views

✅ **Do:**
- Use partials for reusable components (prefix with `_`)
- Use HTMX attributes for interactivity
- Use Bootstrap classes (no custom CSS)
- Use i18n for all user-visible text
- Escape user input (html/template does this automatically)

❌ **Don't:**
- Don't put complex logic in templates
- Don't use inline styles
- Don't hardcode text (use i18n keys)
- Don't trust user input (always escape)

---

**Next:** [HTMX Patterns](htmx.md) | [Frontend Guide](frontend.md) | [Testing](testing.md)
