# Performance

> A page's query count is a constant. It does not grow with the rows the page
> shows.

Most slow pages are not slow databases. They are fast queries issued many times:
one per row, one per card, one per group in a list of fifty. Each is a round
trip, and the total grows with the data, so a page that took 20 ms against
twelve seeded rows takes seconds in production. This doc is the rule, how to
catch a break of it, the fixes, and the two cheap wins outside the database
(static file caching and compression).

Measuring is already built: the admin **Info** box and the `Server-Timing`
header ([deployment.md](deployment.md#request-profiling-system-info)). The
large-table decisions (cursor pagination, partitioning, counters) are in
[scale.md](../scale.md); this doc applies to every project.

## Table of Contents

- [The Rule](#the-rule)
- [Catching It](#catching-it)
- [Fixing It](#fixing-it)
- [Association Loading](#association-loading)
- [Caching Data](#caching-data)
- [Static Files](#static-files)
- [Compression](#compression)
- [Checklist](#checklist)

---

## The Rule

**An N+1 is a bug, not a tuning opportunity.** Fix it before the page ships,
the same as a missing CSRF token. It applies to everything that lists rows:
pages, admin tables, API endpoints, sitemaps, and jobs that loop over rows.

It rarely looks like a query in a visible loop. It hides in:

- a model function called per row (`models.GetUserByID` for each post's author)
- a template that calls a method doing a lookup, inside `range`
- a helper that "just looks one thing up", called once per card
- loading rows to count or group them in Go
- a scope or helper that preloads every association, used by a list that shows
  none of them

---

## Catching It

**In the browser:** open the page as a platform admin and read the Info box. A
list whose query count is near its row count has an N+1. DB time small against
total time means the cost is in Go instead
([deployment.md § Using it](deployment.md#using-it)).

**In a test**, mechanically, so it cannot come back. The profiling Stats count
every statement that carries their context, inside a request or not:
`profile.WithStats(ctx)` starts one outside a request
([deployment.md](deployment.md#what-is-measured)). Count once, grow the data,
count again. The two numbers must match:

```go
// internal/models/post_test.go
func TestListPostsQueriesDoNotGrow(t *testing.T) {
  g := db.SetupTest(t, migrate) // examples/internal/platform/db/testdb.go
  require.NoError(t, profile.RegisterGORM(g))

  count := func() int64 {
    ctx, s := profile.WithStats(context.Background())
    _, err := models.ListPosts(ctx, g, 50)
    require.NoError(t, err)
    return s.Queries()
  }

  seedPosts(t, g, 3)
  few := count()
  seedPosts(t, g, 30)
  assert.Equal(t, few, count(), "a query per post")
}
```

Comparing two sizes beats a fixed ceiling: it fails on the first query per row
and never needs retuning when the page gains a legitimate query. A test with
three rows and a ceiling of ten passes whether or not the bug exists.

Only queries issued with the context are counted (`WithContext(ctx)`), which the
blueprint requires of every DB call anyway; a query without it is invisible to
the Info box too.

---

## Fixing It

In order of preference. All of these live in the model
([mvc.md](mvc.md#models-fat-models)); the controller still makes one call.

### Fetch for the whole page, then look up in a map

The general fix. Collect the IDs on the page, fetch once, return a map the
caller indexes. [audit.md § Showing Names](audit.md#showing-names) is the
worked example: `UserNamesByIDs` fetches two columns for every user on the page
in one `WHERE id IN ?`.

```go
// internal/models/post.go, inside the function that serves the list

// One query, then one per row.
for i := range posts {
  posts[i].Author, _ = GetUserByID(ctx, posts[i].UserID)
}

// One query, then one more.
names, err := UserNamesByIDs(ctx, tx, authorIDs(posts))
```

Keep the single-row function for single-row pages and add the batch one beside
it; don't make every caller build a slice of one.

When a row needs "the first of several" (a cover photo, a primary address),
fetch them all for the page in the order the single-row function uses, and keep
the first per key. If the single-row function has no `ORDER BY`, the batch one
cannot promise the same row: give both the same order before switching.

### Load the association, per query

When the page shows an association, load it with the list:

```go
var posts []Post
err := tx.WithContext(ctx).
  Joins("Author").            // belongs-to: one JOIN
  Preload("Comments").        // has-many: one extra IN query
  Order("post.created_at DESC").Limit(50).
  Find(&posts).Error
```

`Joins` for a belongs-to or has-one, `Preload` for a has-many. Two queries
however long the list. With `Joins`, qualify the columns in `Where` and `Order`
(`post.created_at`): both tables have `created_at`, and an unqualified one is
ambiguous.

### Count and group in SQL

Loading rows to count them moves the table into Go. Let the database aggregate:

```go
// internal/models/comment.go
func CommentCountsByPost(ctx context.Context, tx *gorm.DB, postIDs []int64) (map[int64]int64, error) {
  counts := make(map[int64]int64, len(postIDs))
  if len(postIDs) == 0 {
    return counts, nil
  }
  var rows []struct {
    PostID int64
    N      int64
  }
  if err := tx.WithContext(ctx).Model(&Comment{}).
    Select("post_id, count(*) AS n").Where("post_id IN ?", postIDs).
    Group("post_id").Scan(&rows).Error; err != nil {
    return nil, err
  }
  for _, r := range rows {
    counts[r.PostID] = r.N
  }
  return counts, nil
}
```

The same goes for "one query per group": fifty categories each counting their
posts is one query grouped by category, split in Go.

### Select columns, not rows, for lists

A list that shows three fields does not need the model. `Select` the columns
into a small struct, as `UserNamesByIDs` does. It moves less data and never
drags a sensitive column (an email, a token) into a value that may later be
serialized.

### Insert in one statement

Writing a row per result (impressions, audit entries, imported lines) is an
N+1 on the write side. `Create` with a slice is one `INSERT`; use
`CreateInBatches(rows, 500)` when there can be thousands, to stay under the
driver's parameter limit. With `SkipDefaultTransaction` (database.md) each batch
commits on its own, so run it inside `db.WithTx` when it must be all or
nothing.

### Page in the query

`Limit`/`Offset` in SQL (a cursor on a large table,
[scale.md](../scale.md#cursor-pagination)), never slicing a full result in Go.

---

## Association Loading

GORM loads no association unless the query asks, which is the right default:
nothing is loaded that a page does not use. The trap is the opposite of an
N+1:

- **Never `Preload(clause.Associations)` on a list.** It loads every
  association for every row, including ones the page never shows, and new
  associations added to the model later are loaded by every list for free.
- **No shared scope that preloads.** A `WithEverything` scope reused by the
  list and the detail page makes the list pay for the detail page.
- Preload in the function that serves the page, for what that page shows.

---

## Caching Data

In order of value:

1. **Do less work.** A removed N+1 beats any cache, and it is never stale.
2. **Before caching, add the right index**
   ([database.md](database.md#indexes-and-performance)).
3. **A process-local cache** for small, hot, slow-changing data.
4. **A shared cache** (Redis) only for state several processes must agree on.

[scale.md § Caching](../scale.md#caching) has the rules for any cache (tenant in
the key, correct when empty, expiry over invalidation). For most pages the
first step is the whole answer.

For a lookup several parts of one page repeat, build the map once in the model
function that serves the page and pass it down; there is no request-scoped
store to reach for, and a map in hand is easier to test than one hidden in the
context. For data that does not change while the process runs (a catalog
file), `sync.OnceValue`. For data that changes rarely, a value with a TTL;
every process holds its own copy, so the TTL bounds how long they disagree.

**Don't cache HTML pages in a shared cache.** A page carries the session cookie,
a CSRF token and, for admins, the Info box. Fix the queries instead.

---

## Static Files

With `frontend: server`. Files embedded with `//go:embed` have no modification time, so
`router.StaticFS` sends neither `Last-Modified` nor `ETag`. Nothing tells the
browser it already has `app.css`: it downloads the stylesheet, htmx and Alpine
again on every page. Put the build's commit in the URL instead, and a URL's
content never changes, so the browser can keep it for a year.

A template function writes the URL:

```go
// internal/assets/funcs.go, in funcs()
"asset": asset,   // {{ asset "css/app.css" }}

// asset is a static file's URL, stamped with the build so it can be cached.
func asset(name string) string {
  return "/static/" + name + "?v=" + buildinfo.Commit
}
```

```html
<link href="{{ asset "css/app.css" }}" rel="stylesheet">
<script defer src="{{ asset "js/htmx.min.js" }}"></script>
```

A middleware on the static route sets the caching:

```go
// internal/assets/cache.go
package assets

import (
  "io/fs"
  "strings"

  "github.com/gin-gonic/gin"

  "yourapp/internal/platform/buildinfo"
)

// CacheStatic sets how long the browser keeps a static file: a year when the
// URL carries this build's commit, an hour when it carries none (a font a
// stylesheet names with url()), and never for an old commit, dev, or a 404.
func CacheStatic() gin.HandlerFunc {
  return func(c *gin.Context) {
    if _, err := fs.Stat(StaticFS(), strings.TrimPrefix(c.Param("filepath"), "/")); err != nil {
      c.Next() // a 404 is never cached
      return
    }
    switch v := c.Query("v"); {
    case buildinfo.Commit == "dev":
      c.Header("Cache-Control", "no-cache")
    case v == buildinfo.Commit:
      c.Header("Cache-Control", "public, max-age=31536000, immutable")
    case v == "":
      c.Header("Cache-Control", "public, max-age=3600")
    default:
      c.Header("Cache-Control", "no-cache")
    }
    c.Next()
  }
}
```

```go
// internal/controllers/router.go -- replaces router.StaticFS("/static", ...)
router.Group("/static", assets.CacheStatic()).StaticFS("/", http.FS(assets.StaticFS()))
```

- **A deploy busts it.** The new binary has a new `buildinfo.Commit`
  ([deployment.md](deployment.md#version-from-the-build)), so every URL changes.
- **Only this build's URL is cached long.** A page from before the deploy asking
  for `?v=<old>` gets `no-cache`, so it can never pin old content.
- **Dev is unaffected.** `Commit` is `dev` locally: every file is revalidated,
  and `SetDev` reads it from disk, so an edit shows on refresh.
- **Write every static URL with `asset`.** A hard-coded `/static/...` path still
  works, but is kept only an hour. Files a stylesheet or script names by path
  (`url()` fonts and images) can't use `asset`; the hour is for them.
- **Every deploy re-downloads every file**, htmx and Alpine included, because
  the version is the commit, not each file's content. That costs one download
  per deploy and saves one per page.

---

## Compression

HTML, CSS and JavaScript compress to a fraction of their size. Caddy does it
with one line in the site block, `encode zstd gzip`
([deployment.md](deployment.md#caddy-reverse-proxy)), and skips images and
other formats that are already compressed.

---

## Checklist

Before a page that lists rows is done:

- [ ] Its query count is the same with 3 rows and 30 (a `WithStats` test)
- [ ] No model function is called per row in the controller or the template
- [ ] Counts and groups are computed in SQL
- [ ] Associations are loaded per query, for what the page shows; no
      `clause.Associations` on a list
- [ ] Lists page in SQL
- [ ] The admin Info box shows it under a second in production

Once per project:

- [ ] `asset` in the template functions, `CacheStatic` on `/static`, every
      static URL written with `asset`
- [ ] `encode zstd gzip` in the Caddyfile
