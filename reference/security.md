# Security Reference Guide

> Comprehensive security best practices for Go+Gin SaaS applications

---

## Table of Contents

- [Security Checklist](#security-checklist)
- [CSRF Protection](#csrf-protection)
- [Rate Limiting](#rate-limiting)
- [Input Validation](#input-validation)
- [SQL Injection Prevention](#sql-injection-prevention)
- [XSS Prevention](#xss-prevention)
- [Session Security](#session-security)
- [HTTPS and Headers](#https-and-headers)
- [Content Security Policy](#content-security-policy)
- [Secrets Management](#secrets-management)
- [Logging and Monitoring](#logging-and-monitoring)

---

## Security Checklist

### Essential (Must-Have)

- [ ] **CSRF protection** on all state-changing requests (POST, PUT, DELETE)
- [ ] **Rate limiting** on auth endpoints and API routes
- [ ] **Input validation** on all user inputs (length, format, type)
- [ ] **Parameterized queries** (prevent SQL injection)
- [ ] **Auto-escaping templates** (prevent XSS)
- [ ] **Secure sessions** (HttpOnly, Secure, SameSite cookies)
- [ ] **HTTPS only** in production (enforce with middleware)
- [ ] **Security headers** (X-Frame-Options, X-Content-Type-Options, etc.)
- [ ] **Password hashing** (bcrypt, argon2) if using passwords
- [ ] **Secrets management** (never commit secrets, use env vars)

### Recommended

- [ ] **Content Security Policy** (CSP) header
- [ ] **Subresource Integrity** (SRI) for CDN assets
- [ ] **CORS** configuration (restrict origins)
- [ ] **Request logging** (with request ID, user ID)
- [ ] **Error handling** (don't leak internals to users)
- [ ] **Database RLS** (Row-Level Security for multi-tenancy)
- [ ] **API versioning** (prevent breaking changes)
- [ ] **Dependency scanning** (go mod, Dependabot)

### Advanced

- [ ] **2FA/MFA** (two-factor authentication)
- [ ] **OAuth/OIDC** (third-party login)
- [ ] **Audit logging** (track sensitive actions)
- [ ] **Penetration testing** (annual or before major releases)
- [ ] **Bug bounty program** (for mature products)

---

## CSRF Protection

### What is CSRF?

Cross-Site Request Forgery: Attacker tricks user into making unwanted requests on a site where they're authenticated.

**Example attack:**
```html
<!-- Attacker's site -->
<img src="https://yourapp.com/settings/delete/theme">
<!-- If user is logged in, this deletes their setting -->
```

### Middleware Implementation

```go
// internal/middleware/csrf.go
package middleware

import (
  "crypto/rand"
  "encoding/base64"
  "sync"

  "github.com/gin-gonic/gin"
  "yourapp/internal/platform/errors"
)

var (
  csrfTokens = make(map[string]bool)
  csrfMu     sync.RWMutex
)

// CSRFMiddleware validates CSRF tokens on state-changing requests
func CSRFMiddleware() gin.HandlerFunc {
  return func(c *gin.Context) {
    method := c.Request.Method

    // Only check on state-changing methods
    if method == "POST" || method == "PUT" || method == "PATCH" || method == "DELETE" {
      token := c.GetHeader("X-CSRF-Token")
      if token == "" {
        token = c.PostForm("csrf_token")
      }

      if !validateCSRFToken(token) {
        c.AbortWithStatusJSON(403, gin.H{
          "error": errors.CodeForbidden,
          "message": "Invalid CSRF token",
        })
        return
      }

      // Consume the token (single use)
      consumeCSRFToken(token)
    }

    c.Next()
  }
}

// GenerateCSRFToken creates a new CSRF token
func GenerateCSRFToken() string {
  b := make([]byte, 32)
  rand.Read(b)
  token := base64.RawURLEncoding.EncodeToString(b)

  csrfMu.Lock()
  csrfTokens[token] = true
  csrfMu.Unlock()

  return token
}

func validateCSRFToken(token string) bool {
  if token == "" {
    return false
  }

  csrfMu.RLock()
  valid := csrfTokens[token]
  csrfMu.RUnlock()

  return valid
}

func consumeCSRFToken(token string) {
  csrfMu.Lock()
  delete(csrfTokens, token)
  csrfMu.Unlock()
}
```

### Template Helper

```go
// In router setup
router.SetFuncMap(template.FuncMap{
  "csrf": func() string {
    return middleware.GenerateCSRFToken()
  },
})
```

### Template Usage

```html
<!-- In forms -->
<form method="POST" action="/tasks/create">
  <input type="hidden" name="csrf_token" value="{{ csrf }}">
  <input type="text" name="title">
  <button type="submit">Create</button>
</form>

<!-- With HTMX -->
<button
  hx-post="/tasks/create"
  hx-headers='{"X-CSRF-Token": "{{ csrf }}"}'
  hx-vals='{"title": "My Task"}'>
  Create Task
</button>
```

### Production Note

For production, use a battle-tested library:
- [`github.com/gorilla/csrf`](https://github.com/gorilla/csrf)

---

## Rate Limiting

### In-Memory Rate Limiter

```go
// internal/middleware/ratelimit.go
package middleware

import (
  "sync"
  "time"

  "github.com/gin-gonic/gin"
  "yourapp/internal/platform/errors"
)

type visitor struct {
  lastSeen time.Time
  count    int
}

var (
  visitors = make(map[string]*visitor)
  mu       sync.RWMutex
)

// RateLimitMiddleware limits requests per IP
func RateLimitMiddleware(requestsPerMinute int) gin.HandlerFunc {
  // Cleanup old visitors every 5 minutes
  go cleanupVisitors()

  return func(c *gin.Context) {
    ip := c.ClientIP()

    mu.Lock()
    v, exists := visitors[ip]
    if !exists {
      visitors[ip] = &visitor{lastSeen: time.Now(), count: 1}
      mu.Unlock()
      c.Next()
      return
    }

    // Reset count if more than 1 minute has passed
    if time.Since(v.lastSeen) > time.Minute {
      v.count = 1
      v.lastSeen = time.Now()
      mu.Unlock()
      c.Next()
      return
    }

    v.count++
    v.lastSeen = time.Now()
    count := v.count
    mu.Unlock()

    if count > requestsPerMinute {
      c.AbortWithStatusJSON(429, gin.H{
        "error": errors.CodeRateLimited,
        "message": "Too many requests. Please slow down.",
      })
      return
    }

    c.Next()
  }
}

func cleanupVisitors() {
  for {
    time.Sleep(5 * time.Minute)
    mu.Lock()
    for ip, v := range visitors {
      if time.Since(v.lastSeen) > 5*time.Minute {
        delete(visitors, ip)
      }
    }
    mu.Unlock()
  }
}
```

### Usage

```go
// Global rate limit
router.Use(middleware.RateLimitMiddleware(100)) // 100 req/min

// Stricter for auth endpoints
authRoutes := router.Group("/auth")
authRoutes.Use(middleware.RateLimitMiddleware(5)) // 5 req/min
{
  authRoutes.POST("/magic-link", controllers.SendMagicLink)
  authRoutes.POST("/verify", controllers.VerifyMagicLink)
}
```

### Production: Redis-Based Rate Limiting

For multi-instance deployments:

```go
import "github.com/ulule/limiter/v3"
import "github.com/ulule/limiter/v3/drivers/store/redis"

// Setup Redis store
store, _ := redis.NewStore(redisClient)
rate := limiter.Rate{
  Period: 1 * time.Minute,
  Limit:  100,
}
middleware := limiter.NewMiddleware(limiter.New(store, rate))
```

---

## Input Validation

### Model Validation

```go
// internal/models/user.go
func (u *User) Validate() error {
  // Required fields
  if u.Email == "" {
    return errors.New(errors.CodeInvalidInput, "email is required")
  }

  if u.Name == "" {
    return errors.New(errors.CodeInvalidInput, "name is required")
  }

  // Length validation
  if len(u.Name) > 100 {
    return errors.New(errors.CodeInvalidInput, "name too long (max 100 characters)")
  }

  // Format validation (email, URL, etc.)
  if !isValidEmail(u.Email) {
    return errors.New(errors.CodeInvalidInput, "invalid email format")
  }

  if u.AvatarURL != "" && !isValidURL(u.AvatarURL) {
    return errors.New(errors.CodeInvalidInput, "invalid avatar URL")
  }

  return nil
}

func isValidEmail(email string) bool {
  // Simple regex (production: use a library)
  re := regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
  return re.MatchString(email)
}
```

### Controller Validation

```go
func UpdateProfile(c *gin.Context) {
  var user models.User

  // Bind and validate input
  if err := c.ShouldBind(&user); err != nil {
    c.JSON(400, gin.H{"error": "Invalid input"})
    return
  }

  // Business logic validation
  if err := user.Validate(); err != nil {
    handleError(c, err)
    return
  }

  // Proceed with update
  // ...
}
```

### Sanitization

```go
import "html"

// Sanitize user input before storing
user.Name = html.EscapeString(strings.TrimSpace(user.Name))
user.Email = strings.TrimSpace(strings.ToLower(user.Email))
```

---

## SQL Injection Prevention

### Use Parameterized Queries (sqlx)

```go
// ✅ Safe (parameterized)
db.Get().QueryRowContext(ctx,
  `SELECT * FROM "user" WHERE id = $1 AND email = $2`,
  userID, email)

// ❌ NEVER do this (vulnerable to SQL injection)
query := fmt.Sprintf("SELECT * FROM \"user\" WHERE email = '%s'", email)
db.Get().QueryRowContext(ctx, query)
```

### Named Parameters

```go
// ✅ Safe (named parameters with sqlx)
query := `SELECT * FROM setting WHERE user_id = :user_id AND key = :key`
rows, err := db.Get().NamedQueryContext(ctx, query, map[string]interface{}{
  "user_id": userID,
  "key":     key,
})
```

### Dynamic Queries (Use Whitelists)

```go
// ✅ Safe (whitelist approach)
func ListUsers(ctx context.Context, sortBy string) ([]User, error) {
  // Whitelist allowed sort fields
  allowedSorts := map[string]bool{
    "created_at": true,
    "name":       true,
    "email":      true,
  }

  if !allowedSorts[sortBy] {
    sortBy = "created_at" // Default
  }

  query := fmt.Sprintf(`SELECT * FROM "user" ORDER BY %s DESC`, sortBy)
  // Safe because sortBy is whitelisted

  var users []User
  err := db.Get().SelectContext(ctx, &users, query)
  return users, err
}
```

---

## XSS Prevention

### Auto-Escaping Templates

Go's `html/template` **auto-escapes by default**:

```html
<!-- Automatically escaped (safe) -->
<h1>{{ .title }}</h1>

<!-- If .title = "<script>alert('XSS')</script>" -->
<!-- Rendered as: <h1>&lt;script&gt;alert('XSS')&lt;/script&gt;</h1> -->
```

### Trusted HTML (Use Sparingly)

```go
import "html/template"

// Mark as safe (only for trusted content!)
safeHTML := template.HTML("<strong>Bold text</strong>")

c.HTML(200, "page.html", gin.H{
  "content": safeHTML,
})
```

```html
<!-- Renders as HTML (not escaped) -->
<div>{{ .content }}</div>
```

**⚠️ Only use for content you control (not user input)!**

### Content Security Policy

See [Content Security Policy](#content-security-policy) section below.

---

## Session Security

### Secure Cookies

```go
// Set session cookie securely
http.SetCookie(c.Writer, &http.Cookie{
  Name:     "session_token",
  Value:    token,
  Path:     "/",
  MaxAge:   86400,           // 24 hours
  HttpOnly: true,            // Prevents JavaScript access
  Secure:   true,            // HTTPS only (production)
  SameSite: http.SameSiteLaxMode, // CSRF protection
})
```

### Session Expiration

```go
type Session struct {
  Email     string
  UserID    int64
  ExpiresAt time.Time
}

func GetSession(token string) (Session, error) {
  session, exists := sessions[token]
  if !exists {
    return Session{}, errors.New("invalid session")
  }

  // Check expiration
  if time.Now().After(session.ExpiresAt) {
    delete(sessions, token)
    return Session{}, errors.New("session expired")
  }

  return session, nil
}
```

### Session Regeneration

```go
// After login, regenerate session ID
func Login(c *gin.Context) {
  // ... authenticate user ...

  // Delete old session
  oldToken, _ := c.Cookie("session_token")
  delete(sessions, oldToken)

  // Create new session
  newToken := generateToken()
  sessions[newToken] = Session{
    UserID:    user.ID,
    ExpiresAt: time.Now().Add(24 * time.Hour),
  }

  http.SetCookie(c.Writer, &http.Cookie{
    Name:     "session_token",
    Value:    newToken,
    HttpOnly: true,
    Secure:   true,
    SameSite: http.SameSiteLaxMode,
  })
}
```

---

## HTTPS and Headers

### Force HTTPS

```go
// Middleware to enforce HTTPS
func ForceHTTPS() gin.HandlerFunc {
  return func(c *gin.Context) {
    if c.Request.Header.Get("X-Forwarded-Proto") != "https" {
      httpsURL := "https://" + c.Request.Host + c.Request.RequestURI
      c.Redirect(301, httpsURL)
      c.Abort()
      return
    }
    c.Next()
  }
}

// Apply in production only
if os.Getenv("APP_ENV") == "production" {
  router.Use(ForceHTTPS())
}
```

### Security Headers

```go
// Middleware to add security headers
func SecurityHeaders() gin.HandlerFunc {
  return func(c *gin.Context) {
    // Prevent clickjacking
    c.Header("X-Frame-Options", "DENY")

    // Prevent MIME sniffing
    c.Header("X-Content-Type-Options", "nosniff")

    // Enable XSS filter (legacy browsers)
    c.Header("X-XSS-Protection", "1; mode=block")

    // Force HTTPS
    c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")

    // Referrer policy
    c.Header("Referrer-Policy", "strict-origin-when-cross-origin")

    // Permissions policy
    c.Header("Permissions-Policy", "geolocation=(), microphone=(), camera=()")

    c.Next()
  }
}

router.Use(SecurityHeaders())
```

---

## Content Security Policy

### Basic CSP

```go
func SecurityHeaders() gin.HandlerFunc {
  return func(c *gin.Context) {
    // Content Security Policy
    csp := "default-src 'self'; " +
      "script-src 'self' https://cdn.jsdelivr.net https://unpkg.com; " +
      "style-src 'self' https://cdn.jsdelivr.net; " +
      "img-src 'self' data: https:; " +
      "font-src 'self' data:; " +
      "connect-src 'self'; " +
      "frame-ancestors 'none'; " +
      "base-uri 'self'; " +
      "form-action 'self'"

    c.Header("Content-Security-Policy", csp)
    c.Next()
  }
}
```

### CSP for HTMX

```go
// Allow inline event handlers (HTMX uses them)
csp := "default-src 'self'; " +
  "script-src 'self' 'unsafe-inline' https://unpkg.com; " +
  "style-src 'self' https://cdn.jsdelivr.net; " +
  "img-src 'self' data: https:; " +
  "connect-src 'self'"
```

**Note:** `'unsafe-inline'` is required for HTMX. Use nonces for better security in production.

---

## Secrets Management

### Never Commit Secrets

```bash
# .gitignore
config/local.env
.env
*.pem
*.key
credentials.json
```

### Use Environment Variables

```go
// internal/platform/config/config.go
func Load() (*Config, error) {
  cfg := &Config{
    DatabaseURL: os.Getenv("DATABASE_URL"),
    JWTSecret:   os.Getenv("JWT_SECRET"),
    APIKey:      os.Getenv("API_KEY"),
  }

  // Validate required secrets
  if cfg.DatabaseURL == "" {
    return nil, errors.New("DATABASE_URL is required")
  }

  if cfg.JWTSecret == "" {
    return nil, errors.New("JWT_SECRET is required")
  }

  return cfg, nil
}
```

### Production: Secrets Manager

```go
// Use AWS Secrets Manager, Google Secret Manager, HashiCorp Vault, etc.
import "github.com/aws/aws-sdk-go/service/secretsmanager"

func getSecret(secretName string) (string, error) {
  svc := secretsmanager.New(session.New())
  input := &secretsmanager.GetSecretValueInput{
    SecretId: aws.String(secretName),
  }

  result, err := svc.GetSecretValue(input)
  if err != nil {
    return "", err
  }

  return *result.SecretString, nil
}
```

---

## Logging and Monitoring

### Request Logging

```go
// internal/middleware/logging.go
func RequestLogger() gin.HandlerFunc {
  return func(c *gin.Context) {
    start := time.Now()
    requestID := generateRequestID()
    c.Set("request_id", requestID)

    // Process request
    c.Next()

    // Log after request
    latency := time.Since(start)
    logger.Get().Info().
      Str("req_id", requestID).
      Str("method", c.Request.Method).
      Str("path", c.Request.URL.Path).
      Int("status", c.Writer.Status()).
      Dur("latency", latency).
      Str("ip", c.ClientIP()).
      Msg("request")
  }
}
```

### Error Logging (Don't Leak Internals)

```go
func handleError(c *gin.Context, err error) {
  log := logger.FromContext(c)

  var appErr *errors.AppError
  if !errors.As(err, &appErr) {
    appErr = errors.Wrap(err, errors.CodeUnknown, "unexpected error")
  }

  // Log with full context (internal only)
  log.Error().
    Str("code", appErr.Code).
    Err(appErr.Err).
    Str("user_id", c.GetString("user_id")).
    Str("path", c.Request.URL.Path).
    Msg(appErr.Message)

  // Return user-friendly message (no internals)
  lang := i18n.GetLang(c)
  message := i18n.T(lang, "errors."+appErr.Code)

  c.JSON(appErr.HTTPStatus(), gin.H{
    "error": appErr.Code,
    "message": message,
  })
}
```

### Security Event Logging

```go
// Log security events
func logSecurityEvent(event, userID, ip, details string) {
  logger.Get().Warn().
    Str("event", event).
    Str("user_id", userID).
    Str("ip", ip).
    Str("details", details).
    Msg("security_event")
}

// Usage
logSecurityEvent("failed_login", email, ip, "invalid credentials")
logSecurityEvent("rate_limit_exceeded", userID, ip, "100 requests in 1 minute")
logSecurityEvent("csrf_token_invalid", userID, ip, "POST /tasks/create")
```

---

## Best Practices Summary

### Do's ✅

- ✅ **Use CSRF protection** on all state-changing requests
- ✅ **Rate limit** auth endpoints and public APIs
- ✅ **Validate all inputs** (length, format, type)
- ✅ **Use parameterized queries** (prevent SQL injection)
- ✅ **Auto-escape templates** (prevent XSS)
- ✅ **Secure cookies** (HttpOnly, Secure, SameSite)
- ✅ **Force HTTPS** in production
- ✅ **Add security headers** (X-Frame-Options, CSP, etc.)
- ✅ **Log security events** (failed logins, rate limits)
- ✅ **Never commit secrets** (use env vars or secrets manager)
- ✅ **Keep dependencies updated** (go mod, Dependabot)

### Don'ts ❌

- ❌ **Don't trust user input** (always validate)
- ❌ **Don't use string concatenation** for SQL queries
- ❌ **Don't disable auto-escaping** in templates (unless trusted content)
- ❌ **Don't log sensitive data** (passwords, tokens, PII)
- ❌ **Don't expose stack traces** to users
- ❌ **Don't use weak session tokens** (use crypto/rand, not math/rand)
- ❌ **Don't store passwords in plaintext** (use bcrypt, argon2)
- ❌ **Don't ignore security advisories** (GitHub, Go security team)

---

## Additional Resources

- [OWASP Top 10](https://owasp.org/www-project-top-ten/)
- [OWASP Cheat Sheet Series](https://cheatsheetseries.owasp.org/)
- [Go Security Best Practices](https://github.com/OWASP/Go-SCP)
- [CWE Top 25](https://cwe.mitre.org/top25/)

---

**End of reference/ documentation**
