# Admin API

> **Layer doc.** Read this only when `admin_api: true` in the Project
> Configuration.

A read-only REST API that gives an AI agent (or any script) the app's numbers and
people: signups, sign-ins, active people, activation, retention, invitations,
revenue, product usage. It is authenticated by API keys that platform admins make
in the admin panel, and answers under `/admin/api/v1`.

The contract (URLs, response shapes, the metric catalogue, the privacy rules) is
the remarQable spec, `remarqable/docs/specs/admin-api.md`. Every app that turns
this layer on, in Go or Python, answers the same URLs the same way, so one agent
can watch them all. This doc is how to build it in Go; where the two disagree,
the spec wins.

---

## Table of Contents

- [When You Need This](#when-you-need-this)
- [What It Adds](#what-it-adds)
- [Platform Admins and Real People](#platform-admins-and-real-people)
- [Schema](#schema)
- [The ApiKey Model](#the-apikey-model)
- [Recording What Happens](#recording-what-happens)
- [The Metric Registry](#the-metric-registry)
- [Series in SQL](#series-in-sql)
- [The Core Catalogue](#the-core-catalogue)
- [The API Handlers](#the-api-handlers)
- [The Keys Page](#the-keys-page)
- [OpenAPI](#openapi)
- [Tenancy](#tenancy)
- [Testing](#testing)

---

## When You Need This

When something other than a person in the admin panel needs the app's numbers: an
agent that reports on growth and joins the app's figures with web analytics,
Search Console and GitHub (which it reads with their own APIs; the app never
relays them). If the only reader is a person, the admin dashboard is enough;
build its numbers on [the registry](#the-metric-registry) anyway, so turning this
on later is wiring, not rewriting.

## What It Adds

```
internal/
├── models/
│   ├── api_key.go           # ApiKey: hashed keys, scopes, expiry, revocation
│   └── activity.go          # ActivityDay, AuthEvent: what the metrics count
├── platform/metrics/
│   ├── registry.go          # Metric, Funnel, Register(), Get(), All()
│   ├── sql.go               # bucketing per engine, CountBy, DistinctBy, Starts
│   └── core.go              # the core catalogue (signups, sign-ins, DAU/WAU/MAU…)
├── controllers/adminapi/
│   ├── middleware.go        # bearer key, rate limit, scope check
│   ├── handlers.go          # /admin/api/v1/…
│   └── problem.go           # RFC 9457 problem details, the envelope
├── controllers/admin/keys.go   # /admin/api-keys: make, list, revoke
└── assets/views/admin/api_keys.html
migrations/00NN_admin_api.sql
```

Config (environment):

```go
type Config struct {
  // ...
  PlatformAdminEmails []string // PLATFORM_ADMIN_EMAILS, comma-separated, lowercased
  APIKeyPrefix        string   // API_KEY_PREFIX, e.g. "jd" -> jd_live_…
  APIRateLimit        int      // API_RATE_LIMIT, per key per minute, default 120
  AppRepositoryURL    string   // APP_REPOSITORY_URL, for /status commit links
  GitSHA              string   // set with -ldflags "-X main.gitSHA=…" at build
}
```

---

## Platform Admins and Real People

Every number depends on two definitions. Write them once:

```go
// internal/models/user.go
type User struct {
  // ...
  IsSynthetic bool `gorm:"not null;default:false"` // demo, test or stand-in accounts
}

// IsPlatformAdmin: runs the installation (not a tenant's admin role).
func (u *User) IsPlatformAdmin(cfg *config.Config) bool {
  return slices.Contains(cfg.PlatformAdminEmails, strings.ToLower(u.Email))
}

// RealPeople is every account that is a person: not synthetic, not platform
// staff. Every number the API gives counts only these.
func RealPeople(cfg *config.Config) *gorm.DB {
  q := db.Unscoped().Model(&User{}).Where("is_synthetic = ?", false)
  if len(cfg.PlatformAdminEmails) > 0 {
    q = q.Where("lower(email) NOT IN ?", cfg.PlatformAdminEmails)
  }
  return q
}
```

An app that marks stand-ins another way puts that rule in `RealPeople` instead;
nothing else should know it.

## Schema

```sql
-- migrations/00NN_admin_api.sql
-- +goose Up
CREATE TABLE api_key (
  id            BIGSERIAL PRIMARY KEY,
  name          TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  prefix        TEXT NOT NULL,
  key_hash      TEXT NOT NULL UNIQUE,
  scopes        TEXT[] NOT NULL,
  created_by_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at    TIMESTAMPTZ,
  revoked_at    TIMESTAMPTZ,
  last_used_at  TIMESTAMPTZ,
  last_used_ip  TEXT
);

CREATE TABLE activity_day (
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  day     DATE NOT NULL,
  area    TEXT NOT NULL DEFAULT 'app',     -- a product key, or 'app'
  PRIMARY KEY (user_id, day, area)
);
CREATE INDEX activity_day_day ON activity_day (day);

CREATE TABLE auth_event (
  id         BIGSERIAL PRIMARY KEY,
  kind       TEXT NOT NULL,                -- 'signin' | 'signin_failed'
  user_id    BIGINT REFERENCES users(id) ON DELETE SET NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX auth_event_kind_created ON auth_event (kind, created_at);
CREATE INDEX users_created_at ON users (created_at);

-- +goose Down
DROP TABLE auth_event; DROP TABLE activity_day; DROP TABLE api_key;
DROP INDEX users_created_at;
```

None of these is tenant-scoped (no `tenant_id`, no RLS policy): they belong to
the installation, and only the owner handle reads them. On SQLite, `scopes` is a
JSON text column and `BIGSERIAL` is `INTEGER PRIMARY KEY`
([database.md](core/database.md)).

## The ApiKey Model

The key itself is never stored: only its HMAC with the app's secret, the same way
magic-link tokens are ([auth.md](core/auth.md)). Lookup is an equality match on
the hash, so its timing says nothing about how close a guess was.

```go
// internal/models/api_key.go
package models

import (
  "context"
  "crypto/hmac"
  "crypto/rand"
  "crypto/sha256"
  "encoding/hex"
  "math/big"
  "slices"
  "strings"
  "time"

  "github.com/lib/pq"

  "yourapp/internal/config"
  "yourapp/internal/platform/db"
  "yourapp/internal/platform/errors"
)

var Scopes = []string{"metrics:read", "people:read"}

const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

type ApiKey struct {
  ID          int64          `gorm:"primaryKey"`
  Name        string         `gorm:"not null"`
  Prefix      string         `gorm:"not null"`
  KeyHash     string         `gorm:"not null;uniqueIndex"`
  Scopes      pq.StringArray `gorm:"type:text[];not null"`
  CreatedByID *int64
  CreatedBy   *User
  CreatedAt   time.Time      `gorm:"autoCreateTime"`
  ExpiresAt   *time.Time
  RevokedAt   *time.Time
  LastUsedAt  *time.Time
  LastUsedIP  string
}

func (ApiKey) TableName() string { return "api_key" }

func digest(cfg *config.Config, raw string) string {
  m := hmac.New(sha256.New, []byte(cfg.SecretKey))
  m.Write([]byte(raw))
  return hex.EncodeToString(m.Sum(nil))
}

// IssueApiKey makes a key. It returns the row and the key itself, shown once.
func IssueApiKey(ctx context.Context, cfg *config.Config, admin *User, name string,
  scopes []string, days int) (*ApiKey, string, error) {
  name = strings.TrimSpace(name)
  if name == "" || len(name) > 80 {
    return nil, "", errors.New(errors.CodeInvalidInput, "api_keys.err_name")
  }
  if len(scopes) == 0 {
    return nil, "", errors.New(errors.CodeInvalidInput, "api_keys.err_scopes")
  }
  for _, s := range scopes {
    if !slices.Contains(Scopes, s) {
      return nil, "", errors.New(errors.CodeInvalidInput, "api_keys.err_scopes")
    }
  }
  env := "test"
  if cfg.Env == "production" {
    env = "live"
  }
  var b strings.Builder
  for range 32 {
    n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
    if err != nil {
      return nil, "", err
    }
    b.WriteByte(alphabet[n.Int64()])
  }
  raw := cfg.APIKeyPrefix + "_" + env + "_" + b.String()
  k := &ApiKey{Name: name, Prefix: raw[:12], KeyHash: digest(cfg, raw),
    Scopes: pq.StringArray(scopes), CreatedByID: &admin.ID}
  if days > 0 {
    exp := time.Now().UTC().AddDate(0, 0, days)
    k.ExpiresAt = &exp
  }
  if err := db.Unscoped().WithContext(ctx).Create(k).Error; err != nil {
    return nil, "", err
  }
  return k, raw, nil
}

// AuthenticateApiKey returns the live key, or nil: unknown, revoked, expired, or
// its maker is no longer a platform admin. Callers answer every nil alike.
func AuthenticateApiKey(ctx context.Context, cfg *config.Config, raw string) *ApiKey {
  if raw == "" || len(raw) > 100 {
    return nil
  }
  var k ApiKey
  err := db.Unscoped().WithContext(ctx).Preload("CreatedBy").
    Where("key_hash = ?", digest(cfg, raw)).First(&k).Error
  switch {
  case err != nil, k.RevokedAt != nil:
    return nil
  case k.ExpiresAt != nil && !k.ExpiresAt.After(time.Now()):
    return nil
  case k.CreatedBy == nil || !k.CreatedBy.IsPlatformAdmin(cfg):
    return nil
  }
  return &k
}

func (k *ApiKey) Allows(scope string) bool { return slices.Contains(k.Scopes, scope) }

// Touch notes the use, at most once a minute: a busy agent mustn't write per request.
func (k *ApiKey) Touch(ctx context.Context, ip string) {
  now := time.Now().UTC()
  if k.LastUsedAt != nil && now.Sub(*k.LastUsedAt) < time.Minute {
    return
  }
  db.Unscoped().WithContext(ctx).Model(k).
    Updates(map[string]any{"last_used_at": now, "last_used_ip": ip})
}

func (k *ApiKey) Revoke(ctx context.Context) error {
  return db.Unscoped().WithContext(ctx).Model(k).
    Where("revoked_at IS NULL").Update("revoked_at", time.Now().UTC()).Error
}
```

Revoked keys are kept, never deleted: the admin log names them.

## Recording What Happens

Metrics come from timestamps the app already has (`users.created_at`, each
product's `created_at`) plus the two tables above.

```go
// internal/models/activity.go

// NoteActivity records that a person was active today in an area. Call it from
// the middleware that already notes "last seen"; the session's note of today
// keeps it to one insert per person per day per area.
func NoteActivity(ctx context.Context, userID int64, area string) error {
  return db.Unscoped().WithContext(ctx).Exec(
    `INSERT INTO activity_day (user_id, day, area) VALUES (?, CURRENT_DATE, ?)
     ON CONFLICT DO NOTHING`, userID, area).Error
}

// RecordAuthEvent: 'signin' after a successful sign-in, 'signin_failed' on a
// wrong code or password. userID is nil when nobody was matched.
func RecordAuthEvent(ctx context.Context, kind string, userID *int64) error {
  return db.Unscoped().WithContext(ctx).Exec(
    `INSERT INTO auth_event (kind, user_id) VALUES (?, ?)`, kind, userID).Error
}
```

`CURRENT_DATE` is the database's date: run the database in UTC
([database.md](core/database.md)).

## The Metric Registry

The API, the admin dashboard and anything later read numbers from here. Each
package registers its metrics in `init()` or at startup; the registry answers.

```go
// internal/platform/metrics/registry.go
package metrics

import (
  "context"
  "fmt"
  "sort"
  "time"
)

type Unit string
type Kind string

const (
  Count   Unit = "count"
  Percent Unit = "percent"
  Ratio   Unit = "ratio"
  Cents   Unit = "cents"
  Seconds Unit = "seconds"

  Counter Kind = "counter" // events per bucket; summable
  Gauge   Kind = "gauge"   // a state measured at each bucket
  Rate    Kind = "rate"    // a ratio of two counters; not summable
)

// Series returns values by bucket start ("2026-11-09"); missing buckets are filled
// by the handler. With a segment it returns map[segment]map[bucket]value.
type SeriesFunc func(ctx context.Context, from, to time.Time, interval, segment string) (map[string]float64, error)

type Metric struct {
  Key            string // "core.signups", "billing.mrr"
  Title          string
  Description    string // the exact definition: an agent reads it
  Unit           Unit
  Kind           Kind
  Series         SeriesFunc
  HigherIsBetter bool
  Intervals      []string // default day, week, month
  Segments       []string
  Headline       bool
  Product        string // "core" by default
}

type FunnelStep struct {
  Key, Title string
  People     func(ctx context.Context, from, to time.Time) (map[int64]struct{}, error)
}

type Funnel struct {
  Key, Title string
  Steps      []FunnelStep
}

var (
  metrics = map[string]Metric{}
  funnels = map[string]Funnel{}
)

// Register panics on a repeated key: an agent must never get two meanings for one name.
func Register(m Metric) {
  if _, dup := metrics[m.Key]; dup {
    panic(fmt.Sprintf("metrics: %s registered twice", m.Key))
  }
  if len(m.Intervals) == 0 {
    m.Intervals = []string{"day", "week", "month"}
  }
  if m.Product == "" {
    m.Product = "core"
  }
  metrics[m.Key] = m
}

func RegisterFunnel(f Funnel) { funnels[f.Key] = f }

func Get(key string) (Metric, bool) { m, ok := metrics[key]; return m, ok }

func All() []Metric {
  out := make([]Metric, 0, len(metrics))
  for _, m := range metrics {
    out = append(out, m)
  }
  sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
  return out
}
```

Registration happens before the server starts; the maps are read-only after, so
handlers read them without locks.

## Series in SQL

A series is one grouped query, never rows loaded into Go. Bucketing differs by
engine, so it lives in one place:

```go
// internal/platform/metrics/sql.go

// Bucket is the SQL for the start of col's day, ISO week (Monday) or month, as
// 'YYYY-MM-DD'. Only ever called with a column name from code, never input.
func Bucket(driver, col, interval string) string {
  if driver == "postgres" {
    return fmt.Sprintf("to_char(date_trunc('%s', %s AT TIME ZONE 'UTC'), 'YYYY-MM-DD')", interval, col)
  }
  switch interval {
  case "week":
    return fmt.Sprintf("date(%s, '-6 days', 'weekday 1')", col)
  case "month":
    return fmt.Sprintf("strftime('%%Y-%%m-01', %s)", col)
  }
  return fmt.Sprintf("date(%s)", col)
}

// CountBy: a counter, how many rows of q fall in each bucket of col.
func CountBy(ctx context.Context, q *gorm.DB, col, interval string, from, to time.Time) (map[string]float64, error) {
  b := Bucket(db.Driver(), col, interval)
  var rows []struct {
    Bucket string
    N      float64
  }
  err := q.WithContext(ctx).Where(col+" >= ? AND "+col+" < ?", from, to).
    Select(b + " AS bucket, count(*) AS n").Group("bucket").Scan(&rows).Error
  out := make(map[string]float64, len(rows))
  for _, r := range rows {
    out[r.Bucket] = r.N
  }
  return out, err
}

// Starts lists every bucket start from the one holding from up to to (exclusive).
func Starts(from, to time.Time, interval string) []time.Time {
  cur := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
  switch interval {
  case "week":
    cur = cur.AddDate(0, 0, -((int(cur.Weekday()) + 6) % 7))
  case "month":
    cur = time.Date(cur.Year(), cur.Month(), 1, 0, 0, 0, 0, time.UTC)
  }
  var out []time.Time
  for cur.Before(to) {
    out = append(out, cur)
    switch interval {
    case "day":
      cur = cur.AddDate(0, 0, 1)
    case "week":
      cur = cur.AddDate(0, 0, 7)
    default:
      cur = cur.AddDate(0, 1, 0)
    }
  }
  return out
}
```

`DistinctBy` is the same with `count(DISTINCT user_id)`. Sliding-window gauges
(WAU, MAU) are one indexed range count on `activity_day` per bucket end: at most
104 for two years of weeks. Allow `day` for them only over ranges of 90 days or
less.

## The Core Catalogue

```go
// internal/platform/metrics/core.go

func RegisterCore(cfg *config.Config) {
  Register(Metric{Key: "core.signups", Title: "Signups", Unit: Count, Kind: Counter,
    Description: "Accounts created, not counting platform admins or synthetic accounts.",
    HigherIsBetter: true, Headline: true,
    Series: func(ctx context.Context, from, to time.Time, interval, _ string) (map[string]float64, error) {
      return CountBy(ctx, models.RealPeople(cfg), "created_at", interval, from, to)
    }})
  Register(Metric{Key: "core.signins", Title: "Sign-ins", Unit: Count, Kind: Counter,
    Description: "Successful sign-ins by real people.", HigherIsBetter: true, Headline: true,
    Series: func(ctx context.Context, from, to time.Time, interval, _ string) (map[string]float64, error) {
      q := db.Unscoped().Table("auth_event").Where("kind = 'signin'").
        Where("user_id IN (?)", models.RealPeople(cfg).Select("id"))
      return CountBy(ctx, q, "auth_event.created_at", interval, from, to)
    }})
  Register(Metric{Key: "core.mau", Title: "Monthly active people", Unit: Count, Kind: Gauge,
    Description: "Distinct real people active in the 30 days ending each bucket.",
    HigherIsBetter: true, Headline: true, Intervals: []string{"week", "month"},
    Series: activeIn(cfg, 30)})
  // core.verified_signups, core.signin_failures, core.dau, core.wau, core.stickiness,
  // core.activation_rate, core.time_to_activate, core.churned, core.resurrected,
  // core.people: as defined in the spec.
}
```

Each product package registers its own (`<product>.<name>`), so the core never
imports a product.

## The API Handlers

Its own middleware chain: no session, no cookie, no CSRF. A browser session, even
an admin's, never authenticates it, and a key opens nothing outside the group.

```go
// internal/controllers/adminapi/middleware.go

func RequireKey(cfg *config.Config) gin.HandlerFunc {
  limiter := newLimiter(cfg.APIRateLimit) // per-process; see security.md for shared limits
  return func(c *gin.Context) {
    c.Header("Cache-Control", "no-store")
    raw, _ := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
    key := models.AuthenticateApiKey(c.Request.Context(), cfg, strings.TrimSpace(raw))
    if key == nil { // one answer for every reason
      c.Header("WWW-Authenticate", "Bearer")
      Problem(c, 401, "unauthorized", "Missing or invalid API key", "")
      return
    }
    if wait, ok := limiter.Allow(key.ID); !ok {
      c.Header("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
      Problem(c, 429, "rate-limited", "Too many requests", "")
      return
    }
    key.Touch(c.Request.Context(), c.ClientIP())
    c.Set("api_key", key)
    c.Next()
    obs.From(c.Request.Context()).Info().Str("event", "admin_api.request").
      Int64("key_id", key.ID).Str("path", c.FullPath()).Int("status", c.Writer.Status()).Msg("")
  }
}

func Need(scope string) gin.HandlerFunc {
  return func(c *gin.Context) {
    if !c.MustGet("api_key").(*models.ApiKey).Allows(scope) {
      Problem(c, 403, "forbidden", "Not allowed", "This key needs the "+scope+" scope.")
    }
  }
}

// internal/controllers/adminapi/problem.go

func Problem(c *gin.Context, status int, kind, title, detail string, extra ...gin.H) {
  body := gin.H{"type": "https://remarqable.io/problems/" + kind, "title": title, "status": status}
  if detail != "" {
    body["detail"] = detail
  }
  for _, e := range extra {
    maps.Copy(body, e)
  }
  c.Header("Content-Type", "application/problem+json")
  c.AbortWithStatusJSON(status, body)
}

func OK(c *gin.Context, cfg *config.Config, data any) {
  c.JSON(200, gin.H{"data": data, "meta": gin.H{
    "app": cfg.AppName, "generated_at": time.Now().UTC().Format(time.RFC3339),
    "version": cfg.Version, "commit": cfg.GitSHA}})
}
```

```go
// internal/controllers/router.go
api := r.Group("/admin/api/v1", adminapi.RequireKey(cfg))
{
  m := api.Group("", adminapi.Need("metrics:read"))
  m.GET("/status", h.Status)
  m.GET("/summary", h.Summary)
  m.GET("/metrics", h.Catalogue)
  m.GET("/metrics/:key", h.Series)
  m.GET("/funnels", h.Funnels)
  m.GET("/funnels/:key", h.Funnel)
  m.GET("/cohorts/retention", h.Retention)
  m.GET("/events", h.Events)         // identities only with people:read
  m.GET("/openapi.json", h.OpenAPI)
  p := api.Group("", adminapi.Need("people:read"))
  p.GET("/people", h.People)
  p.GET("/people/:id", h.Person)
}
```

Register this group **before** the session and CSRF middleware are attached to
the rest of `/admin`, or outside that group: the API must never pass through
cookie auth.

`h.Series` parses `from`/`to` (default the last 30 days, at most two years: else
422 with `param`), checks `interval` against the metric's, calls `Series`, then
fills every bucket from `Starts()` (`0` for counters, the last value for gauges),
marks the current bucket `partial`, and adds `total` for counters. Handlers stay
thin: what a person "is" for `/people` is a model function products extend.

## The Keys Page

`/admin/api-keys`, platform admins only, in the admin panel's layout and behind
its session, CSRF and `IsPlatformAdmin` checks (404 otherwise).

- A table: name, prefix (`jd_live_4f9K…`), scopes, created (by whom, when),
  expires, last used, and Revoke.
- **New key**: a name, the scopes (`metrics:read` ticked; `people:read` unticked,
  with "Shares people's emails, names and activity"), and an expiry (30, 90, 365
  days, or never; 90 selected).
- After making one, the page shows the whole key **once**, with Copy and "Copy it
  now: it won't be shown again." Render it in that response only; never put it
  in the session or a flash.
- Revoke asks first (a confirm step, not a JavaScript `confirm()`).
- Making and revoking each write an admin-log entry naming the key's name and
  prefix, never the key.

## OpenAPI

`GET /admin/api/v1/openapi.json` is built at startup from the registry and the
route table, so it can't drift: each path, its parameters, the envelope, the
problem schema, and `components.schemas.MetricKey` as an enum of every registered
key.

## Tenancy

With `tenancy: shared`, metrics count across every tenant, so every query in this
layer uses `db.Unscoped()` (the owner handle,
[tenancy.md](tenancy.md)). That is deliberate and confined to
`internal/platform/metrics` and `internal/controllers/adminapi`: add both to the
list of places CI allows `Unscoped()`. The B2B catalogue (`b2b.orgs`,
`b2b.active_orgs`, `b2b.seats`) counts tenants and memberships; give
`activity_day` a nullable `tenant_id` so active tenants come from the same table.

## Testing

```go
func TestNoValidKeyIsOne401(t *testing.T) {
  srv := testServer(t)
  for _, h := range []string{"", "Bearer ", "Bearer nonsense", "Basic abc"} {
    resp := srv.Get(t, "/admin/api/v1/summary", h)
    require.Equal(t, 401, resp.Code, h)
    require.Equal(t, "application/problem+json", resp.Header().Get("Content-Type"))
  }
}

func TestASessionNeverAuthenticatesTheAPI(t *testing.T) {
  srv := testServer(t)
  srv.SignIn(t, srv.Admin)
  require.Equal(t, 401, srv.Get(t, "/admin/api/v1/summary", "").Code)
}

func TestMetricsReadCannotReadPeople(t *testing.T) {
  srv := testServer(t)
  _, raw := srv.IssueKey(t, "metrics:read")
  require.Equal(t, 403, srv.Get(t, "/admin/api/v1/people", "Bearer "+raw).Code)
}

func TestSignupsFillEveryBucketAndLeaveOutStaff(t *testing.T) {
  srv := testServer(t)
  srv.MakeUser(t, "a@example.com")
  srv.MakeUser(t, "b@example.com")
  _, raw := srv.IssueKey(t, "metrics:read")
  var body struct{ Data struct {
    Points []struct{ Value float64 }
    Total  float64
  } }
  srv.GetJSON(t, "/admin/api/v1/metrics/core.signups?interval=day", "Bearer "+raw, &body)
  require.Len(t, body.Data.Points, 30)
  require.Equal(t, 2.0, body.Data.Total) // the admin isn't counted
}
```

Plus, from the spec's list: revoked, expired, and orphaned keys (maker no longer
a platform admin) get the same 401; the key is stored only as its hash; the 429
carries `Retry-After`; `/openapi.json` names every registered metric; making and
revoking appear in the admin log; each product's metrics match a hand count on a
seeded dataset.
