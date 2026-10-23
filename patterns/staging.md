# Staging

> A second, complete copy of the service on the same VPS, at
> `staging.<domain>`: its own releases, process, port, database and settings.
> Caddy routes by subdomain. Every change runs there first; production gets
> only what already worked on staging.

Read this layer only when `staging: true`. It requires `deploy: binary`: a
distributed Docker product has no server of yours to stage on.

---

## Table of Contents

- [Why a second copy](#why-a-second-copy)
- [The shape](#the-shape)
- [Server layout](#server-layout)
- [Settings](#settings)
- [What the service does differently](#what-the-service-does-differently)
- [systemd](#systemd)
- [Caddy and DNS](#caddy-and-dns)
- [The database](#the-database)
- [Refreshing staging from production](#refreshing-staging-from-production)
- [Deploying](#deploying)
- [Migrations](#migrations)
- [Limits of one server](#limits-of-one-server)
- [Checklist](#checklist)

---

## Why a second copy

`go test ./...` proves the code is correct against its tests. It does not prove
the binary runs on the server, against the server's Postgres, its env, its
Caddy, or real data at real scale. Staging does, without touching production:

- **A branch can be tried in the real environment** before it is merged.
- **Risky work** (a migration, a new provider, a change to a job) runs there
  first, against a copy of production's data.
- **People who aren't engineers can try it** at a real URL, signed in as
  themselves, before it ships.

One VPS is enough. A second Go process and a second database cost little; a
second server costs money and drifts from the first. Move staging to its own
server only when it competes with production (see
[Limits of one server](#limits-of-one-server)).

---

## The shape

Everything is doubled except the server, Postgres itself and Caddy.

| | Production | Staging |
|---|---|---|
| URL | `yourapp.com` | `staging.yourapp.com` |
| Releases | `/opt/yourapp/releases/<sha>` (always `main`) | `/opt/yourapp-staging/releases/<sha>` (any branch) |
| Service | `yourapp` → `127.0.0.1:8000` | `yourapp-staging` → `127.0.0.1:8001` |
| Worker (`jobs: true`) | `yourapp-worker` | `yourapp-staging-worker` |
| Database | `yourapp` | `yourapp_staging` (same Postgres server) |
| Settings | `/opt/yourapp/.env` | `/opt/yourapp-staging/.env` (`STAGING=1`) |
| Who can open it | everyone | platform admins only |

**Same repository, same build.** Staging runs a binary built from a commit of
the same repo by the same deploy script. "It worked on staging" means "this
commit works", and the binary production gets is built the same way from the
same commit.

**Nothing is shared at runtime.** The two processes never read the other's
database, files, sessions or job queue. Staging holds no credential that can
write to production.

---

## Server layout

```
/opt/yourapp/                 # production
├── .env
├── current -> releases/a1b2c3d
└── releases/
/opt/yourapp-staging/         # staging
├── .env
├── current -> releases/e4f5a6b
└── releases/
```

The layout is production's ([deployment.md](core/deployment.md#layout)) under
a second directory, with its own `current` symlink, so a staging rollback is a
symlink flip just like production's. Both directories are owned by the
service user (`yourapp`).

---

## Settings

Staging's `.env` starts as a copy of production's and changes these lines.
Most incidents with staging come from one left as production's value.

```bash
APP_ENV=staging
STAGING=1
PORT=8001
BASE_URL=https://staging.yourapp.com
SESSION_SECRET=<a new one: openssl rand -hex 32>     # never production's
DATABASE_URL=postgres://yourapp_staging_app:<pw>@localhost/yourapp_staging
DATABASE_OWNER_URL=postgres://yourapp_staging:<pw>@localhost/yourapp_staging

# Outbound side effects: off, or pointed somewhere safe
MAIL_REDIRECT_TO=team@yourapp.com
SMS_ENABLED=false
WEBHOOKS_ENABLED=false
ANALYTICS_ENABLED=false

# Paid providers: separate keys with their own low limits
ANTHROPIC_API_KEY=<staging key>
```

- **A different session secret.** Sharing it would let a staging cookie sign
  in to production.
- **Its own database roles** (owner and app, as production has), with rights
  on `yourapp_staging` only. Staging must be *unable* to reach the production
  database, not merely configured not to.
- **Outbound side effects off.** Once staging holds a copy of production data,
  it holds real customers' addresses. Redirect all mail to the team and switch
  off SMS, webhooks and payment capture.
- **Paid providers on their own keys** and spending limits.

```go
// internal/platform/config/config.go
type Config struct {
  // ...
  Staging        bool   `env:"STAGING"`
  MailRedirectTo string `env:"MAIL_REDIRECT_TO"`
}
```

```go
// internal/platform/mail/mail.go -- the one place mail is sent
func (m *Mailer) Send(ctx context.Context, msg Message) error {
  if m.cfg.MailRedirectTo != "" {
    msg.Subject = fmt.Sprintf("[staging → %s] %s", msg.To, msg.Subject)
    msg.To = m.cfg.MailRedirectTo
  }
  return m.transport.Send(ctx, msg)
}
```

---

## What the service does differently

One flag, `cfg.Staging`, read in one place each. Never a host-name check
scattered through handlers.

**Admins only.** Staging may hold production's data; the public must not reach
it and search engines must not index it.

```go
// internal/middleware/staging.go -- installed in the router when cfg.Staging
var stagingOpen = map[string]bool{
  "/login": true, "/auth/verify": true, "/logout": true,
  "/healthz": true, "/readyz": true,
}

func StagingGate() gin.HandlerFunc {
  return func(c *gin.Context) {
    c.Header("X-Robots-Tag", "noindex, nofollow")
    if stagingOpen[c.FullPath()] || strings.HasPrefix(c.Request.URL.Path, "/static/") {
      c.Next()
      return
    }
    u := auth.CurrentUser(c)
    if u == nil {
      c.Redirect(http.StatusSeeOther, "/login?next="+url.QueryEscape(c.Request.URL.RequestURI()))
      c.Abort()
      return
    }
    if !u.IsPlatformAdmin {
      c.AbortWithStatus(http.StatusForbidden)
      return
    }
    c.Next()
  }
}
```

```go
// cmd/api/main.go
if cfg.Staging {
  r.Use(middleware.StagingGate())
}
```

- **`robots.txt` disallows everything**, and the sitemap is not served.
- **A bar on every page** (`frontend: server`): "Staging: changes here don't
  affect the live site". The layout shows it when `Staging` is in the view
  data.
- **Host-only cookies, with their own names.** Never set the cookie `Domain`
  to `.yourapp.com`: a parent-domain cookie is sent to both sites. Name
  staging's session cookie `yourapp_staging`.
- **Absolute URLs come from `BASE_URL`** (emails, canonical links, OAuth
  callbacks), so a link generated on staging points at staging.
- **OAuth providers** need staging's callback registered as a second redirect
  URI, or a separate staging client.

---

## systemd

Production's unit with the paths, port and name changed.

```ini
# /etc/systemd/system/yourapp-staging.service
[Unit]
Description=Your App (staging)
After=network.target postgresql.service

[Service]
Type=simple
User=yourapp
Group=yourapp
WorkingDirectory=/opt/yourapp-staging
EnvironmentFile=/opt/yourapp-staging/.env
ExecStart=/opt/yourapp-staging/current/yourapp
Restart=on-failure
RestartSec=5s
TimeoutStopSec=40s
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/opt/yourapp-staging

[Install]
WantedBy=multi-user.target
```

`ReadWritePaths` names the staging directory only: the staging process cannot
write into production's even by mistake. With `jobs: true`, a
`yourapp-staging-worker.service` runs `current/yourapp-worker` the same way,
with lower concurrency than production's.


**Restart workers by their unit files, not a glob.** `systemctl restart
'yourapp-staging-worker-*'` matches only units systemd has loaded, so a worker
that has never run (just added, or just renamed) is silently skipped. List the
enabled ones instead:

```bash
units=$(systemctl list-unit-files --no-legend 'yourapp-staging-worker-*' | awk '$2 == "enabled" {print $1}')
systemctl restart $units
```

---

## Caddy and DNS

**DNS:** `staging.yourapp.com` points at the same server, as an `A` record to
its IP or a `CNAME` to `yourapp.com`. Caddy gets the certificate on the first
request once the name resolves.

```
# /etc/caddy/Caddyfile
yourapp.com {
    reverse_proxy localhost:8000 {
        lb_try_duration 5s
        lb_try_interval 250ms
    }
    import be_right_back
}

staging.yourapp.com {
    encode gzip
    header X-Robots-Tag "noindex, nofollow"
    reverse_proxy localhost:8001 {
        lb_try_duration 5s
        lb_try_interval 250ms
    }
    import be_right_back
}
```

Routing is by host name alone. Both sites share the `be_right_back` snippet
from [deployment.md](core/deployment.md#be-right-back-page).

```bash
caddy validate --config /etc/caddy/Caddyfile && sudo systemctl reload caddy
```

---

## The database

A separate database on the same Postgres server, with the same two roles
production has ([database.md](core/database.md)):

```sql
CREATE ROLE yourapp_staging LOGIN PASSWORD '...';        -- owner: migrations
CREATE ROLE yourapp_staging_app LOGIN PASSWORD '...';    -- the service
CREATE DATABASE yourapp_staging OWNER yourapp_staging;
REVOKE CONNECT ON DATABASE yourapp FROM yourapp_staging, yourapp_staging_app;
```

Grant `yourapp_staging_app` the same rights production's app role has, on the
staging database. With `database: sqlite`, the file lives under
`/opt/yourapp-staging/`, never a path into production's directory.

---

## Refreshing staging from production

Staging is most useful with production's data. A refresh replaces the staging
database with a copy of production's, then makes the copy safe to run.

```bash
# scripts/refresh-staging.sh -- run on the server, as root
set -euo pipefail
STAMP=$(date +%Y%m%d-%H%M)

systemctl stop yourapp-staging yourapp-staging-worker 2>/dev/null || true

# Keep the old staging database: a refresh must be undoable.
sudo -u postgres psql -c "ALTER DATABASE yourapp_staging RENAME TO yourapp_staging_$STAMP"
sudo -u postgres createdb -O yourapp_staging yourapp_staging
sudo -u postgres pg_dump -Fc --no-owner --no-acl yourapp \
  | sudo -u postgres pg_restore --no-owner --role=yourapp_staging -d yourapp_staging
sudo -u postgres psql -d yourapp_staging -f /opt/yourapp-staging/current/deploy/staging-grants.sql

# Make it safe: nothing queued in production runs again here.
sudo -u postgres psql -d yourapp_staging <<'SQL'
DELETE FROM jobs WHERE state IN ('queued', 'running');
DELETE FROM sessions;
SQL

systemctl start yourapp-staging yourapp-staging-worker 2>/dev/null || true
echo "Staging refreshed. Previous copy: yourapp_staging_$STAMP"
```

- **Rename, don't drop.** Staging may hold work of its own. The renamed copy
  goes back with two `ALTER DATABASE ... RENAME` statements, and is dropped
  once nobody needs it.
- **Re-grant the app role** (`deploy/staging-grants.sql`): a restore with
  `--no-acl` leaves the service role with no rights.
- **Clear the job queue.** A copied queued job would run again from staging:
  a second email, a second charge, a second model call.
- **Scrub what staging must not use**: payment tokens, stored third-party
  keys, anything that would let staging act on a customer's behalf. Keep the
  list next to the script.
- **Run it on purpose**, not on a timer: a refresh discards whatever was being
  tried on staging.

---

## Deploying

One script, two targets. The steps are the ones in
[deployment.md](core/deployment.md#scriptsdeploysh); only the directory, the
service name, the URL, and which branches are allowed change.

```bash
scripts/deploy.sh staging            # main, to staging
scripts/deploy.sh staging my-branch  # a branch being tried
scripts/deploy.sh                    # production: main only
```

```bash
# scripts/deploy.sh (head)
TARGET="${1:-production}"
BRANCH="${2:-main}"
case "$TARGET" in
  production)
    [ "$BRANCH" = main ] || { echo "Production deploys main only."; exit 1; }
    BASE=/opt/yourapp;         APP_UNIT=yourapp;         READY_URL=https://yourapp.com/readyz ;;
  staging)
    BASE=/opt/yourapp-staging; APP_UNIT=yourapp-staging; READY_URL=https://staging.yourapp.com/readyz ;;
  *) echo "usage: deploy.sh [production|staging [branch]]"; exit 1 ;;
esac

git fetch -q origin
git checkout -q "$BRANCH"
git reset -q --hard "origin/$BRANCH"
# ... then build, ship to $BASE/releases/$SHA, migrate with the target's
# DATABASE_OWNER_URL, flip $BASE/current, restart $APP_UNIT, and gate on
# $READY_URL reporting $SHA -- exactly as for production.
```

A branch deploy builds from a clean checkout of `origin/<branch>`, never from
uncommitted work, so what ran on staging can be named by its commit.

**The flow:**

1. Work on a branch.
2. `deploy.sh staging my-branch`, then try it at `staging.yourapp.com`, on
   real data if the change depends on it.
3. Merge to `main`, deploy `main` to staging once more, then `deploy.sh` to
   production.
4. Leave staging on `main` when nothing is being tried.

**An optional gate:** the production deploy can refuse a commit staging hasn't
run. Staging's `/readyz` reports its version:

```bash
STAGED=$(curl -fsS https://staging.yourapp.com/readyz | jq -r .version)
if [ "$TARGET" = production ] && [ "$STAGED" != "$SHA" ] && [ "${FORCE:-}" != 1 ]; then
  echo "Staging runs $STAGED, not $SHA. Deploy it there first (or FORCE=1)."
  exit 1
fi
```

---

## Migrations

The deploy script runs `goose up` against the target's database before the
restart, so a branch's migration is applied to staging when the branch is
deployed there.

- **Switching staging back does not undo it.** Before deploying another
  branch, run `goose down` to the version that branch expects, or
  [refresh from production](#refreshing-staging-from-production).
- **Number migrations by timestamp**, not sequence, so two branches don't both
  add `00042_...`. Goose applies out-of-order migrations only with
  `-allow-missing`; prefer rebasing a branch's migration past `main`'s.
- **Staging catches migrations that fail on real data** (a `NOT NULL` column on
  a full table, a unique index over duplicates) before production does.
  Refresh staging, deploy the branch, and watch the migration run against
  production's data first. Backward compatibility with the previous release
  (deployment.md) is tested the same way: roll staging's `current` back one
  release and check it still serves.

---

## Limits of one server

- **CPU and memory are shared.** A heavy job on staging slows production.
  Keep staging's worker concurrency low; load-test elsewhere.
- **One Postgres.** A restore or a long query on staging competes for
  production's I/O. Refresh in quiet hours.
- **Not a backup.** Staging is not backed up; production's backups are
  unchanged.
- **Graduate** to a separate server when staging must match production's size
  to be a fair test, or its load becomes noticeable. The pattern is the same
  with a different `REMOTE`; the refresh dumps over the network.

---

## Checklist

- [ ] `staging.yourapp.com` resolves to the server; Caddy serves it with
      `X-Robots-Tag: noindex`
- [ ] `/opt/yourapp-staging/{releases,current}`; `yourapp-staging` listens on
      `127.0.0.1:8001`, `ReadWritePaths` limited to its own directory
- [ ] Its own database and roles, unable to connect to production's
- [ ] Its own session secret and cookie name; cookies host-only
- [ ] `STAGING=1`: admins only, a staging bar, robots.txt disallows all
- [ ] Mail redirected, SMS/webhooks/payments off, paid providers on separate
      keys
- [ ] `deploy.sh staging [branch]` works; production deploys `main` only
- [ ] `refresh-staging.sh` keeps the old database, re-grants, clears the queue
