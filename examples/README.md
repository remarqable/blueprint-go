# examples

A compiling skeleton of the blueprint's patterns. It exists so the
documentation cannot drift into being wrong without CI noticing.

This is **not** a starter app — there are no routes, no templates, no auth. It
is the load-bearing platform code from `patterns/`, built and tested:

| Package | Pattern doc |
|---|---|
| `internal/platform/db` | [database.md](../patterns/database.md), [tenancy.md](../patterns/tenancy.md) |
| `internal/platform/jobs` | [jobs.md](../patterns/jobs.md) |
| `internal/platform/ai` | [ai.md](../patterns/ai.md) |
| `internal/models` | [mvc.md](../patterns/mvc.md), [data-modeling.md](../patterns/data-modeling.md) |

```bash
cd examples
go test ./... -race
```

The tests are the interesting part. They assert the properties the docs claim:

- A tenant cannot read, write, update or delete across the boundary.
- An unset tenant yields an error, never every row.
- The shared handle fails closed rather than leaking.
- A rolled-back transaction leaves no job behind.
- A retryable failure requeues with backoff; a terminal one dies on attempt 1.
- An expired lease is reclaimed.
- A tenant job re-enters its scope.
- A fabricated evidence citation is rejected.

It runs on SQLite so the suite needs no services. **SQLite has no row-level
security**, so isolation here is enforced by the GORM callbacks in
`db/tenant.go`. That is the weaker of the two mechanisms; the PostgreSQL path
is the one that ships, and it is the one to verify before launch. See
[tenancy.md](../patterns/tenancy.md).
