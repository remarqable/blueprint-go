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
- [ ] **Vendored front-end assets** (no CDN; see [frontend.md](../frontend.md#file-structure))
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

### One Token per Session

Each session gets **one** token, valid until the session ends, and every page
in it carries that token. Not a fresh token per form, and not one consumed on
first use. The frontend depends on this: the base layout puts the token in
`hx-headers` on `<body>` once per page
([frontend.md](../frontend.md#base-layout)), and every HTMX request from that
page sends the same value. A single-use token would let the first request
through and answer every later one with 403. It would also break a second tab
and the back button.

Single-use buys nothing against CSRF anyway. The attack works because the
browser attaches the victim's cookie to a forged request; it fails when the
request must also carry a value the attacker's page cannot read. The
same-origin policy already stops the attacker reading a per-session token, so
replacing it after each request adds no protection. What matters is that the
token is:

- **Bound to the session.** A token from the attacker's own session must not
  pass for the victim's. Derived as an HMAC of the session cookie, it cannot.
- **Unguessable and server-keyed.** 256 bits from HMAC-SHA256 with a server
  secret.
- **Compared in constant time**, so response timing does not leak it byte by
  byte.
- **Backed by an origin check.** A cross-site POST carries an `Origin` (or at
  least a `Referer`) naming another site; reject it before looking at the
  token.

The token changes when the session does. Regenerating the session at login
([Session Regeneration](#session-regeneration)) therefore also rotates the
token, so a token planted before login is worthless after it.

### Middleware Implementation

```go
// internal/middleware/csrf.go
package middleware

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
)

const (
	sessionCookie = "session_token" // set at login, auth.md
	anonCookie    = "__Host-csrf"   // binds the token before login
	csrfHeader    = "X-CSRF-Token"  // HTMX: hx-headers on <body>
	csrfField     = "csrf_token"    // plain forms: hidden input
)

// CSRF gives each session one token, valid for the session's lifetime, and
// checks it on every state-changing request together with the request's
// origin. secret is a server-side key of at least 32 random bytes; origin is
// the app's public origin, e.g. "https://app.example.com".
func CSRF(secret []byte, origin string) gin.HandlerFunc {
	if len(secret) < 32 {
		panic("middleware.CSRF: secret must be at least 32 bytes")
	}
	return func(c *gin.Context) {
		want := tokenFor(secret, binding(c))
		// The renderer reads the token from the writer (embed.md, Step 4).
		c.Writer = &csrfWriter{ResponseWriter: c.Writer, token: want}

		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}

		got := c.GetHeader(csrfHeader)
		if got == "" {
			got = c.PostForm(csrfField)
		}
		if !sameOrigin(c.Request, origin) ||
			subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}

// binding is the value the token is derived from: the session cookie once
// the user is signed in, otherwise a random cookie issued here.
func binding(c *gin.Context) string {
	if v, err := c.Cookie(sessionCookie); err == nil && v != "" {
		return v
	}
	if v, err := c.Cookie(anonCookie); err == nil && v != "" {
		return v
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	v := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(c.Writer, &http.Cookie{
		Name: anonCookie, Value: v, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	return v
}

// tokenFor derives the token from the binding. Nothing is stored: the same
// session always yields the same token, and a new session a new one.
func tokenFor(secret []byte, binding string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("csrf:" + binding))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// sameOrigin rejects a request whose Origin, or failing that Referer, names
// another site. A request carrying neither still has to present the token.
func sameOrigin(r *http.Request, origin string) bool {
	if o := r.Header.Get("Origin"); o != "" {
		return o == origin
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		u, err := url.Parse(ref)
		return err == nil && u.Scheme+"://"+u.Host == origin
	}
	return true
}

type csrfWriter struct {
	gin.ResponseWriter
	token string
}

func (w *csrfWriter) CSRFToken() string { return w.token }

// Unwrap exposes the writer underneath, so other wrappers (profiling,
// deployment.md) can still be found.
func (w *csrfWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
```

Nothing is stored server-side: the token is recomputed from the session cookie
on each request. Before login the binding is a random `__Host-csrf` cookie, so
the login form is protected too. The `__Host-` prefix makes the browser refuse
the cookie unless it is `Secure`, host-only and `Path=/`, which stops a sibling
subdomain from planting one.

Install it on the HTML router, after the static route and before every page
route ([embed.md](../embed.md#router-setup)). An API authenticated by an
`Authorization` header rather than a cookie is not exposed to CSRF and does not
need it. Set `CSRF_SECRET` to at least 32 random bytes and `APP_URL` to the
exact public origin, scheme included; the middleware panics at startup on a
short secret rather than running without protection.

### Template Helper

`{{ csrf }}` is registered in the template FuncMap
([embed.md](../embed.md#step-2-add-template-functions-optional)) and bound to
the request's token on every render. Gin hands the renderer the response
writer but not the `*gin.Context`, which is why the middleware attaches the
token to the writer. A template rendered on a route without the middleware
fails with an error rather than emitting an empty token.

### Template Usage

```html
<!-- Plain forms: a hidden field, so they work without JavaScript -->
<form method="POST" action="/settings">
  <input type="hidden" name="csrf_token" value="{{ csrf }}">
  <label for="title" class="label">{{ t .lang "tasks.title" }}</label>
  <input type="text" id="title" name="title" class="input mt-1">
  <button type="submit" class="btn-primary">{{ t .lang "tasks.create" }}</button>
</form>

<!-- HTMX: once, on <body>, in the layout. Every request inherits it. -->
<body hx-headers='{"X-CSRF-Token": "{{ csrf }}"}'>
```

Do not repeat `hx-headers` on individual elements; the one on `<body>` covers
them.

### Library Alternatives

- Go 1.25+ ships `http.CrossOriginProtection` in `net/http`, which rejects
  cross-origin state-changing requests using `Sec-Fetch-Site` and `Origin`.
  It can replace `sameOrigin` above; keep the token as well, for browsers that
  send neither header.
- [`github.com/gorilla/csrf`](https://github.com/gorilla/csrf) implements the
  same per-session pattern with masked tokens. It exposes the token through
  `csrf.Token(r)`, which needs the request, so wire it to `{{ csrf }}` the same
  way: through the response writer.

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

### Use Parameterized Queries

GORM's builder parameterises automatically. The risk is in the places that take
raw strings.

```go
// Safe -- builder, values are bound
db.Get().WithContext(ctx).
  Where("id = ? AND email = ?", userID, email).First(&user)

// Safe -- Raw/Exec with placeholders
db.Get().WithContext(ctx).
  Raw(`SELECT * FROM "user" WHERE email = ?`, email).Scan(&user)

// NEVER -- string interpolation into any of them
q := fmt.Sprintf(`SELECT * FROM "user" WHERE email = '%s'`, email)
db.Get().WithContext(ctx).Raw(q).Scan(&user)
```

### Identifiers cannot be parameterised

Placeholders bind *values*, never table names, column names, or sort direction.
A sort order taken from a query string and pasted into `Order()` is an injection
point, and it is the one that survives code review because it does not look like
SQL.

```go
// Vulnerable -- c.Query returns whatever the client sent
db.Get().Order(c.Query("sort")).Find(&users)

// Safe -- allowlist, never the raw input
var allowed = map[string]string{
  "newest": "created_at DESC",
  "oldest": "created_at ASC",
  "name":   "name ASC",
}
order, ok := allowed[c.Query("sort")]
if !ok {
  order = "created_at DESC"
}
db.Get().Order(order).Find(&users)
```

The same applies to `Table()`, `Select()` with computed column lists, and
`Group()`. If it is an identifier, it comes from an allowlist in your code.

### Dynamic Queries (Use Whitelists)

```go
// Safe (whitelist approach)
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
  err := db.Get().WithContext(ctx).Raw(query).Scan(&users).Error
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

See [Content Security Policy](#content-security-policy) below. With no inline
script allowed, an injection that slips past escaping still cannot run.

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

    // No X-XSS-Protection: modern browsers removed the filter it controlled,
    // and in old ones it could be abused to leak data. The CSP replaces it.

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

### The Policy

```
default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'
```

```go
// contentSecurityPolicy is sent on every response: router.Use(ContentSecurityPolicy()).
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; " +
  "img-src 'self' data:; font-src 'self'; connect-src 'self'; " +
  "object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

func ContentSecurityPolicy() gin.HandlerFunc {
  return func(c *gin.Context) {
    c.Header("Content-Security-Policy", contentSecurityPolicy)
    c.Next()
  }
}
```

Nothing is loosened: no `'unsafe-inline'`, no `'unsafe-eval'`, no CDN origins.
It holds because of how the frontend is built
([frontend.md](../frontend.md#alpinejs-and-the-csp)):

- **Everything is vendored** under `/static`, so `'self'` covers every script,
  stylesheet and font.
- **Alpine is the CSP build** (`@alpinejs/csp`), which never compiles strings
  into functions, so no `'unsafe-eval'`.
- **No inline code in templates**: no `<script>` blocks, no `<style>` blocks,
  no `style="…"`, no `onclick=` or other `on*` attributes. Page behaviour lives
  in `static/js/app.js`. A Go test enforces it.

### HTMX Under This Policy

HTMX does not need `'unsafe-inline'`. It runs under this policy when:

- **Its indicator styles are off.** By default HTMX injects a `<style>` element
  for `.htmx-indicator`, which `style-src 'self'` blocks. The layout's
  `htmx-config` meta sets `"includeIndicatorStyles": false`, and the indicator
  rules come from the compiled stylesheet instead.
- **Eval is off.** `"allowEval": false` in the same meta disables the features
  that evaluate strings (`hx-on`, `js:` values in `hx-vals`, event filters), so
  a stray one fails visibly rather than prompting a weaker policy.
- **No `hx-on` and no inline handlers.** Behaviour tied to an HTMX event lives
  in an Alpine component or in `app.js`, or is raised by the server with
  `HX-Trigger`.

The configuration and the reasons are in [htmx.md](../htmx.md#configuration).

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

### Do's

- **Use CSRF protection** on all state-changing requests
- **Rate limit** auth endpoints and public APIs
- **Validate all inputs** (length, format, type)
- **Use parameterized queries** (prevent SQL injection)
- **Auto-escape templates** (prevent XSS)
- **Secure cookies** (HttpOnly, Secure, SameSite)
- **Force HTTPS** in production
- **Add security headers** (X-Frame-Options, CSP, etc.)
- **Log security events** (failed logins, rate limits)
- **Never commit secrets** (use env vars or secrets manager)
- **Keep dependencies updated** (go mod, Dependabot)

### Don'ts

- **Don't trust user input** (always validate)
- **Don't use string concatenation** for SQL queries
- **Don't disable auto-escaping** in templates (unless trusted content)
- **Don't log sensitive data** (passwords, tokens, PII)
- **Don't expose stack traces** to users
- **Don't use weak session tokens** (use crypto/rand, not math/rand)
- **Don't store passwords in plaintext** (use bcrypt, argon2)
- **Don't ignore security advisories** (GitHub, Go security team)

---

## Additional Resources

- [OWASP Top 10](https://owasp.org/www-project-top-ten/)
- [OWASP Cheat Sheet Series](https://cheatsheetseries.owasp.org/)
- [Go Security Best Practices](https://github.com/OWASP/Go-SCP)
- [CWE Top 25](https://cwe.mitre.org/top25/)

---

**End of reference/ documentation**
