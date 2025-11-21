# Authentication & Sessions Reference

> Complete guide to implementing authentication and session management

---

## Table of Contents

- [Philosophy](#philosophy)
- [Magic Links (Passwordless)](#magic-links-passwordless)
- [Session Management](#session-management)
- [Middleware](#middleware)
- [Production Considerations](#production-considerations)
- [Alternative Auth Methods](#alternative-auth-methods)
- [Best Practices](#best-practices)

---

## Philosophy

**Start simple, scale later.**

### Core Principles

1. **Magic links for MVP** (no passwords, easy UX)
2. **In-memory sessions for dev** (swap to Redis for production)
3. **Secure by default** (HttpOnly cookies, SameSite, HTTPS)
4. **Extensible** (add OAuth, JWT, 2FA later)

---

## Magic Links (Passwordless)

### Why Magic Links?

- ✅ No password management (no bcrypt, no resets)
- ✅ Better UX (one-click login)
- ✅ More secure (no weak passwords)
- ✅ Fast to implement (perfect for MVP)

### Database Schema

```sql
-- migrations/001_init_schema.sql
CREATE TABLE magic_link (
  token TEXT PRIMARY KEY,
  email TEXT NOT NULL,
  expires_at TIMESTAMP NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_magic_link_email ON magic_link(email);
CREATE INDEX idx_magic_link_expires ON magic_link(expires_at);

-- Optional: Add used_at column to prevent token reuse
ALTER TABLE magic_link ADD COLUMN used_at TIMESTAMP;
```

### Generate Magic Link

```go
// internal/platform/auth/magic_link.go
package auth

import (
  "context"
  "crypto/rand"
  "encoding/base64"
  "fmt"
  "time"

  "yourapp/internal/platform/db"
)

const (
  MagicLinkExpiry = 15 * time.Minute
  TokenLength     = 48 // 384 bits
)

// GenerateMagicLink creates a magic link token for an email
func GenerateMagicLink(ctx context.Context, email string) (string, error) {
  // Generate cryptographically secure token
  b := make([]byte, TokenLength)
  if _, err := rand.Read(b); err != nil {
    return "", err
  }
  token := base64.RawURLEncoding.EncodeToString(b)

  // Store in database
  ctx, cancel := db.WithTimeout(ctx, 0)
  defer cancel()

  _, err := db.Get().ExecContext(ctx,
    `INSERT INTO magic_link (token, email, expires_at)
     VALUES ($1, $2, $3)`,
    token, email, time.Now().Add(MagicLinkExpiry))

  if err != nil {
    return "", err
  }

  return token, nil
}

// VerifyMagicLink verifies a token and returns the associated email
func VerifyMagicLink(ctx context.Context, token string) (string, error) {
  ctx, cancel := db.WithTimeout(ctx, 0)
  defer cancel()

  var email string
  var expiresAt time.Time
  var usedAt *time.Time

  err := db.Get().QueryRowContext(ctx,
    `SELECT email, expires_at, used_at
     FROM magic_link
     WHERE token = $1`,
    token).Scan(&email, &expiresAt, &usedAt)

  if err != nil {
    if err == sql.ErrNoRows {
      return "", errors.New("invalid or expired magic link")
    }
    return "", err
  }

  // Check if already used
  if usedAt != nil {
    return "", errors.New("magic link already used")
  }

  // Check if expired
  if time.Now().After(expiresAt) {
    return "", errors.New("magic link expired")
  }

  // Mark as used
  _, err = db.Get().ExecContext(ctx,
    `UPDATE magic_link SET used_at = NOW() WHERE token = $1`,
    token)

  if err != nil {
    return "", err
  }

  return email, nil
}

// CleanupExpiredLinks removes old magic links (run periodically)
func CleanupExpiredLinks(ctx context.Context) error {
  ctx, cancel := db.WithTimeout(ctx, 0)
  defer cancel()

  _, err := db.Get().ExecContext(ctx,
    `DELETE FROM magic_link
     WHERE expires_at < NOW() - INTERVAL '1 day'`)

  return err
}
```

### Send Magic Link Email

```go
// internal/platform/auth/email.go
package auth

import (
  "fmt"
  "net/smtp"
  "os"
)

// SendMagicLinkEmail sends magic link to user's email
func SendMagicLinkEmail(email, token string) error {
  // Build magic link URL
  baseURL := os.Getenv("APP_URL") // e.g., https://yourapp.com
  magicURL := fmt.Sprintf("%s/auth/verify?token=%s", baseURL, token)

  // Email content
  subject := "Your login link"
  body := fmt.Sprintf(`
Click the link below to sign in:

%s

This link will expire in 15 minutes.

If you didn't request this, you can safely ignore this email.
`, magicURL)

  // Send email (example with SMTP)
  return sendEmail(email, subject, body)
}

func sendEmail(to, subject, body string) error {
  from := os.Getenv("SMTP_FROM")
  password := os.Getenv("SMTP_PASSWORD")
  smtpHost := os.Getenv("SMTP_HOST")
  smtpPort := os.Getenv("SMTP_PORT")

  message := []byte(fmt.Sprintf("Subject: %s\r\n\r\n%s", subject, body))

  auth := smtp.PlainAuth("", from, password, smtpHost)
  addr := fmt.Sprintf("%s:%s", smtpHost, smtpPort)

  return smtp.SendMail(addr, auth, from, []string{to}, message)
}
```

### Controller: Request Magic Link

```go
// internal/controllers/auth_controller.go
func RequestMagicLink(c *gin.Context) {
  var req struct {
    Email string `json:"email" binding:"required,email"`
  }

  if err := c.ShouldBindJSON(&req); err != nil {
    c.JSON(400, gin.H{"error": "invalid email"})
    return
  }

  // Generate magic link
  token, err := auth.GenerateMagicLink(c.Request.Context(), req.Email)
  if err != nil {
    c.JSON(500, gin.H{"error": "failed to generate link"})
    return
  }

  // Send email
  if err := auth.SendMagicLinkEmail(req.Email, token); err != nil {
    c.JSON(500, gin.H{"error": "failed to send email"})
    return
  }

  c.JSON(200, gin.H{
    "message": "Magic link sent! Check your email.",
  })
}
```

### Controller: Verify Magic Link

```go
func VerifyMagicLink(c *gin.Context) {
  token := c.Query("token")
  if token == "" {
    c.JSON(400, gin.H{"error": "missing token"})
    return
  }

  // Verify token
  email, err := auth.VerifyMagicLink(c.Request.Context(), token)
  if err != nil {
    c.JSON(401, gin.H{"error": "invalid or expired link"})
    return
  }

  // Get or create user
  var user models.User
  if err := user.GetByEmail(email); err != nil {
    // Create new user
    user.Email = email
    user.Name = email // Default name
    if err := user.Create(c.Request.Context()); err != nil {
      c.JSON(500, gin.H{"error": "failed to create user"})
      return
    }
  }

  // Create session
  sessionToken, err := auth.CreateSession(user.Email, user.ID)
  if err != nil {
    c.JSON(500, gin.H{"error": "failed to create session"})
    return
  }

  // Set session cookie
  http.SetCookie(c.Writer, &http.Cookie{
    Name:     "session_token",
    Value:    sessionToken,
    Path:     "/",
    MaxAge:   86400, // 24 hours
    HttpOnly: true,
    Secure:   true, // HTTPS only in production
    SameSite: http.SameSiteLaxMode,
  })

  // Redirect to dashboard
  c.Redirect(302, "/dashboard")
}
```

---

## Session Management

### In-Memory Sessions (Development)

```go
// internal/platform/auth/session.go
package auth

import (
  "crypto/rand"
  "encoding/base64"
  "errors"
  "sync"
  "time"
)

type Session struct {
  Email     string
  UserID    int64
  TenantID  int64 // Optional: for multi-tenancy
  ExpiresAt time.Time
}

var Sessions = struct {
  mu sync.RWMutex
  m  map[string]Session
}{m: make(map[string]Session)}

// CreateSession creates a new session
func CreateSession(email string, userID int64) (string, error) {
  // Generate secure token
  b := make([]byte, 48)
  if _, err := rand.Read(b); err != nil {
    return "", err
  }
  token := base64.RawURLEncoding.EncodeToString(b)

  // Store session
  Sessions.mu.Lock()
  Sessions.m[token] = Session{
    Email:     email,
    UserID:    userID,
    ExpiresAt: time.Now().Add(24 * time.Hour),
  }
  Sessions.mu.Unlock()

  return token, nil
}

// GetSession retrieves a session by token
func GetSession(token string) (Session, error) {
  Sessions.mu.RLock()
  session, exists := Sessions.m[token]
  Sessions.mu.RUnlock()

  if !exists {
    return Session{}, errors.New("E_INVALID_SESSION")
  }

  // Check expiration
  if time.Now().After(session.ExpiresAt) {
    Sessions.mu.Lock()
    delete(Sessions.m, token)
    Sessions.mu.Unlock()
    return Session{}, errors.New("E_SESSION_EXPIRED")
  }

  return session, nil
}

// DeleteSession removes a session (logout)
func DeleteSession(token string) {
  Sessions.mu.Lock()
  delete(Sessions.m, token)
  Sessions.mu.Unlock()
}

// CleanupExpiredSessions removes expired sessions (run periodically)
func CleanupExpiredSessions() {
  Sessions.mu.Lock()
  defer Sessions.mu.Unlock()

  now := time.Now()
  for token, session := range Sessions.m {
    if now.After(session.ExpiresAt) {
      delete(Sessions.m, token)
    }
  }
}

// Start cleanup goroutine
func init() {
  go func() {
    ticker := time.NewTicker(1 * time.Hour)
    defer ticker.Stop()

    for range ticker.C {
      CleanupExpiredSessions()
    }
  }()
}
```

---

## Middleware

### Authentication Middleware

```go
// internal/middleware/auth.go
package middleware

import (
  "github.com/gin-gonic/gin"
  "yourapp/internal/platform/auth"
  "yourapp/internal/platform/errors"
)

// RequireAuth ensures user is authenticated
func RequireAuth() gin.HandlerFunc {
  return func(c *gin.Context) {
    // Get session token from cookie
    token, err := c.Cookie("session_token")
    if err != nil {
      c.AbortWithStatusJSON(401, gin.H{
        "error": errors.CodeUnauthorized,
        "message": "Please sign in to continue",
      })
      return
    }

    // Verify session
    session, err := auth.GetSession(token)
    if err != nil {
      c.AbortWithStatusJSON(401, gin.H{
        "error": errors.CodeInvalidSession,
        "message": "Invalid or expired session",
      })
      return
    }

    // Set user info in context
    c.Set("user_id", session.UserID)
    c.Set("user_email", session.Email)

    c.Next()
  }
}

// OptionalAuth sets user info if authenticated, but doesn't require it
func OptionalAuth() gin.HandlerFunc {
  return func(c *gin.Context) {
    token, err := c.Cookie("session_token")
    if err != nil {
      c.Next()
      return
    }

    session, err := auth.GetSession(token)
    if err != nil {
      c.Next()
      return
    }

    c.Set("user_id", session.UserID)
    c.Set("user_email", session.Email)

    c.Next()
  }
}
```

### Usage in Routes

```go
// internal/controllers/router.go
func SetupRouter() *gin.Engine {
  router := gin.Default()

  // Public routes
  router.GET("/", controllers.Home)
  router.POST("/auth/magic-link", controllers.RequestMagicLink)
  router.GET("/auth/verify", controllers.VerifyMagicLink)

  // Protected routes
  protected := router.Group("/")
  protected.Use(middleware.RequireAuth())
  {
    protected.GET("/dashboard", controllers.Dashboard)
    protected.GET("/tasks", controllers.ListTasks)
    protected.POST("/tasks", controllers.CreateTask)
    protected.GET("/logout", controllers.Logout)
  }

  return router
}
```

---

## Production Considerations

### Redis-Based Sessions

```go
// internal/platform/auth/redis_session.go
package auth

import (
  "context"
  "encoding/json"
  "time"

  "github.com/redis/go-redis/v9"
)

var redisClient *redis.Client

func InitRedis(addr string) {
  redisClient = redis.NewClient(&redis.Options{
    Addr: addr,
  })
}

func CreateSessionRedis(email string, userID int64) (string, error) {
  // Generate token
  b := make([]byte, 48)
  rand.Read(b)
  token := base64.RawURLEncoding.EncodeToString(b)

  // Store in Redis with TTL
  session := Session{
    Email:     email,
    UserID:    userID,
    ExpiresAt: time.Now().Add(24 * time.Hour),
  }

  data, _ := json.Marshal(session)
  ctx := context.Background()

  err := redisClient.Set(ctx, "session:"+token, data, 24*time.Hour).Err()
  if err != nil {
    return "", err
  }

  return token, nil
}

func GetSessionRedis(token string) (Session, error) {
  ctx := context.Background()

  data, err := redisClient.Get(ctx, "session:"+token).Bytes()
  if err == redis.Nil {
    return Session{}, errors.New("E_INVALID_SESSION")
  }
  if err != nil {
    return Session{}, err
  }

  var session Session
  if err := json.Unmarshal(data, &session); err != nil {
    return Session{}, err
  }

  return session, nil
}
```

### Session Security

**Cookie Settings:**
```go
http.SetCookie(c.Writer, &http.Cookie{
  Name:     "session_token",
  Value:    token,
  Path:     "/",
  MaxAge:   86400,           // 24 hours
  HttpOnly: true,            // Prevents JavaScript access (XSS protection)
  Secure:   true,            // HTTPS only (production)
  SameSite: http.SameSiteLaxMode, // CSRF protection
})
```

**Session Regeneration:**
```go
// After successful login, regenerate session ID
func RegenerateSession(oldToken string) (string, error) {
  // Get old session
  session, err := GetSession(oldToken)
  if err != nil {
    return "", err
  }

  // Delete old session
  DeleteSession(oldToken)

  // Create new session with same data
  return CreateSession(session.Email, session.UserID)
}
```

---

## Alternative Auth Methods

### JWT Tokens (Stateless)

```go
import "github.com/golang-jwt/jwt/v5"

type Claims struct {
  UserID int64 `json:"user_id"`
  Email  string `json:"email"`
  jwt.RegisteredClaims
}

func GenerateJWT(userID int64, email string) (string, error) {
  claims := Claims{
    UserID: userID,
    Email:  email,
    RegisteredClaims: jwt.RegisteredClaims{
      ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
      IssuedAt:  jwt.NewNumericDate(time.Now()),
    },
  }

  token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
  return token.SignedString([]byte(os.Getenv("JWT_SECRET")))
}

func VerifyJWT(tokenString string) (*Claims, error) {
  token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
    return []byte(os.Getenv("JWT_SECRET")), nil
  })

  if err != nil {
    return nil, err
  }

  if claims, ok := token.Claims.(*Claims); ok && token.Valid {
    return claims, nil
  }

  return nil, errors.New("invalid token")
}
```

### OAuth (Google, GitHub, etc.)

Use a library like `golang.org/x/oauth2`:

```go
import (
  "golang.org/x/oauth2"
  "golang.org/x/oauth2/google"
)

var googleOAuthConfig = &oauth2.Config{
  ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
  ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
  RedirectURL:  "http://localhost:8000/auth/google/callback",
  Scopes: []string{
    "https://www.googleapis.com/auth/userinfo.email",
    "https://www.googleapis.com/auth/userinfo.profile",
  },
  Endpoint: google.Endpoint,
}

func GoogleLogin(c *gin.Context) {
  url := googleOAuthConfig.AuthCodeURL("state", oauth2.AccessTypeOffline)
  c.Redirect(302, url)
}

func GoogleCallback(c *gin.Context) {
  code := c.Query("code")
  token, err := googleOAuthConfig.Exchange(c, code)
  // ... get user info, create session ...
}
```

---

## Best Practices

### Do's ✅

- ✅ **Use magic links for MVP** (simplest, most secure)
- ✅ **HttpOnly cookies** (prevent XSS)
- ✅ **SameSite=Lax** (CSRF protection)
- ✅ **Secure flag in production** (HTTPS only)
- ✅ **Regenerate session after login** (prevent fixation)
- ✅ **Cleanup expired sessions** (prevent memory leaks)
- ✅ **Rate limit auth endpoints** (prevent brute force)
- ✅ **Log security events** (failed logins, etc.)

### Don'ts ❌

- ❌ **Don't store passwords in plain text** (if using passwords, use bcrypt)
- ❌ **Don't use math/rand for tokens** (use crypto/rand)
- ❌ **Don't set session_token in localStorage** (use HttpOnly cookies)
- ❌ **Don't skip HTTPS in production** (cookies must be Secure)
- ❌ **Don't use long-lived sessions** (24 hours max, refresh if needed)
- ❌ **Don't forget to cleanup** (expired magic links, sessions)

---

**Next:** Back to [claude.md](../../claude.md) for master blueprint
