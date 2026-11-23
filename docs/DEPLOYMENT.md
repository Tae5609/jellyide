# Deployment

How to run JellyIDE with Docker Compose, what every environment variable
does, where the data lives, and how to put the app behind a reverse proxy.

For how the authentication system works, see [AUTHENTICATION.md](./AUTHENTICATION.md).
For the API itself, see [API.md](./API.md). For how to develop on the code,
see [DEVELOPMENT.md](./DEVELOPMENT.md).

## Table of contents

- [Quick start](#quick-start)
  - [Development (build from source)](#development-build-from-source)
  - [Production (prebuilt image)](#production-prebuilt-image)
- [Environment variables](#environment-variables)
- [Volumes and data layout](#volumes-and-data-layout)
- [Reverse proxy](#reverse-proxy)
  - [Caddy](#caddy)
  - [nginx](#nginx)
- [Upgrading](#upgrading)
- [Backup and restore (SQLite)](#backup-and-restore-sqlite)
- [Troubleshooting](#troubleshooting)
- [Security notes](#security-notes)

## Quick start

The app is one container: a Go server that serves the REST API, the React
SPA, and the media files from the music directory. All state (SQLite
database, SAML key material, downloaded music) lives in the music
directory, so one volume is the whole deployment.

### Development (build from source)

`docker-compose.yml` builds the image from the `Dockerfile` and mounts
`/mnt/media/music` read/write.

```sh
# Optional — for unattended installs: pre-create the admin account on
# the first start (see docs/AUTHENTICATION.md, "Admin bootstrap").
# It is only used while no users exist and is never re-applied.
export ADMIN_PASSWORD='change-me'
docker compose up --build -d
```

Then open <http://localhost:8080> and log in as `admin`. If you left
`ADMIN_PASSWORD` unset, the server starts in first-run setup mode: open
<http://localhost:8080/setup> and complete the wizard with the one-time
setup token printed to the server logs.

### Production (prebuilt image)

`docker-compose.prod.yml` runs a prebuilt image (no build step) and reads
its settings from a `.env` file:

```sh
cp .env.example .env
# Edit .env: at minimum set PUBLIC_URL for anything beyond localhost.
docker compose -f docker-compose.prod.yml up -d
```

`ADMIN_PASSWORD` is optional. Set it to pre-create the admin account on
the very first start (empty database); afterwards the stored admin
account wins. If it is unset, the server starts in first-run setup mode
instead and serves the setup wizard (or the admin CLI bootstraps the
account) — see [AUTHENTICATION.md](./AUTHENTICATION.md).

Both compose files pass every auth/registration variable through as
`${VAR:-default}` — the defaults below are the ones the Go code falls back
to when a variable is empty.

Note: the compose files do not forward `TRUST_PROXY`,
`DOWNLOAD_MAX_RETRIES`, `YTDLP_MANAGED` or `SYNC_CONFLICT_POLICY`. To set
one of them, add a line to the service's `environment:` block (or set it
in the container environment).

## Environment variables

Every variable the server reads, with its default from the code
(`backend/internal/auth/config.go`, `backend/internal/auth/oidc.go`,
`backend/internal/auth/saml.go`, `backend/internal/download/downloader.go`,
`backend/internal/library/sync.go`, `backend/internal/ytdlp/manager.go`,
`backend/main.go`). Boolean variables
accept `1/true/yes/on` and `0/false/no/off` (`YTDLP_MANAGED` treats
anything that is not an explicit `0/false/no/off` as on).

| Variable | Default | Description |
|---|---|---|
| `DOWNLOAD_DIR` | `/mnt/media/music` | Container path of the music library. The SQLite database and the SAML cert/key live inside it. |
| `PORT` | `8080` | Port the server listens on inside the container. |
| `AUTH_ENABLED` | `true` | Master switch. `false` disables authentication entirely — every visitor is an anonymous superuser (see [disabling auth](./AUTHENTICATION.md#disabling-auth)). |
| `AUTH_LOCAL_ENABLED` | `true` | Allow username/password (local) login and self-registration. |
| `COOKIE_SECURE` | `false` | Set the `Secure` flag on the session and OIDC-state cookies. Use `true` when serving over HTTPS. |
| `TRUST_PROXY` | `false` | Let the login rate limiter read the client IP from the first `X-Forwarded-For` hop instead of the direct peer. Enable only behind a reverse proxy that overwrites the header, or clients can spoof the key. |
| `ADMIN_USERNAME` | `admin` | Username of the bootstrapped admin account (first start only). |
| `ADMIN_PASSWORD` | — (none) | Optional. Pre-creates the bootstrapped admin account on the first start with an empty database. If unset, first-run setup (the wizard or the admin CLI) creates the admin. |
| `PUBLIC_URL` | `http://localhost:8080` | Public base URL of this instance as the browser sees it. Builds the OIDC redirect URI and the SAML metadata/ACS URLs — it must be reachable from the browser and from the IdP. |
| `REGISTRATION_MODE` | `admin_only` | Initial registration mode: `admin_only`, `open` or `approval`. Seeded into the database once at first start; afterwards the admin UI wins over this variable. |
| `OIDC_ENABLED` | `false` | Enable the OIDC (SSO) login. Requires `OIDC_ISSUER`, `OIDC_CLIENT_ID` and `OIDC_CLIENT_SECRET`. |
| `OIDC_NAME` | `SSO` | Button name shown on the login page. |
| `OIDC_ISSUER` | — (none) | Provider discovery URL, e.g. `https://id.example.com/realms/jellyide`. |
| `OIDC_CLIENT_ID` | — (none) | OAuth2 client id registered at the provider. |
| `OIDC_CLIENT_SECRET` | — (none) | OAuth2 client secret. |
| `OIDC_SCOPES` | `openid,profile,email` | Comma-separated scopes requested from the provider. |
| `OIDC_ADMIN_GROUP` | — (none) | Users whose group list contains this group become admins. |
| `OIDC_GROUPS_CLAIM` | `groups` | Claim in the id_token that carries the group list. |
| `OIDC_AUTO_CREATE` | `true` | Create a local account on first login. `false` = only pre-provisioned users may log in. |
| `SAML_ENABLED` | `false` | Enable the SAML 2.0 service provider. Requires `SAML_IDP_METADATA_URL` or `SAML_IDP_METADATA_FILE`. |
| `SAML_NAME` | `SAML` | Button name shown on the login page. |
| `SAML_IDP_METADATA_URL` | — (none) | URL of the IdP metadata document (fetched at startup). |
| `SAML_IDP_METADATA_FILE` | — (none) | Path of a local IdP metadata file (mutually exclusive with the URL). |
| `SAML_SP_CERT` | `<DOWNLOAD_DIR>/saml/sp.crt` | SP certificate. A self-signed pair is generated here on first start when missing. |
| `SAML_SP_KEY` | `<DOWNLOAD_DIR>/saml/sp.key` | SP private key (`0600`). |
| `SAML_USERNAME_ATTR` | — (NameID fallback) | Assertion attribute used as the username (matched by Name or FriendlyName). |
| `SAML_EMAIL_ATTR` | `email` | Assertion attribute used as the email. |
| `SAML_NAME_ATTR` | `displayName` | Assertion attribute used as the display name. |
| `SAML_GROUPS_ATTR` | `groups` | Assertion attribute that carries the group list. |
| `SAML_ADMIN_GROUP` | — (none) | Users whose group list contains this group become admins. |
| `SAML_AUTO_CREATE` | `true` | Create a local account on first login. `false` = only pre-provisioned users may log in. |
| `CONCURRENT_DOWNLOAD` | `2` | Read at startup as the initial download concurrency limit: a missing, invalid or below-1 value falls back to the default of 2, and a value above 32 is clamped to 32. Can still be changed at runtime on the Settings page. |
| `DOWNLOAD_MAX_RETRIES` | `2` | How often a transient download failure is retried automatically, with a 30 s / 2 min / 10 min backoff (capped at 10 min). Permanent failures (HTTP 403/404, unavailable, private, members-only or copyrighted videos) are never retried; `0` disables the automatic retry. Empty or invalid values fall back to the default. |
| `YTDLP_MANAGED` | `true` | Use the JellyIDE-managed yt-dlp binary: it is installed on first start under `<DOWNLOAD_DIR>/.jellyide/bin` and auto-updated on an interval (Settings page). Anything that is not an explicit `0/false/no/off` keeps managed mode on; `YTDLP_MANAGED=false` makes downloads use the system `yt-dlp` on the `PATH` and leaves the auto-update loop off. |
| `SYNC_CONFLICT_POLICY` | `ask` | Initial policy for a scan that finds the file's tags differ from the database: `ask` (record a conflict and wait for a user decision), `file_wins` (the file overwrites the database) or `db_wins` (the file tags are rewritten from the database). Seeded into the database once at first start; afterwards the value set on the Settings page wins over this variable. |

The compose files also set `IMAGE` (prod compose) and the host-side
`PORT`/`MUSIC_DIR` mapping, which are compose-level, not server, variables.

## Volumes and data layout

Everything the server persists lives under `DOWNLOAD_DIR` (the mounted
music volume):

```
<DOWNLOAD_DIR>/                  container: /mnt/media/music
├── jellyide.db                  SQLite database (WAL mode)
│                                + jellyide.db-wal, jellyide.db-shm while running
├── saml/                        SAML SP key material (only when SAML_ENABLED=true)
│   ├── sp.crt                   self-signed cert (auto-generated, 10 years)
│   ├── sp.key                   RSA private key, mode 0600
├── .jellyide/
│   ├── bin/                     managed yt-dlp binary (YTDLP_MANAGED, the default)
│   │   ├── yt-dlp               checksum-verified release binary
│   │   └── yt-dlp.prev          previous binary (rollback candidate)
│   └── trash/                   files displaced by "replace" rename collisions
└── <Artist>/<Album>/            downloaded and scanned music
    ├── album artwork + tracks
```

- **Music.** The compose files mount the host music path read/write at
  `/mnt/media/music`. The server scans it, renames/moves files, and
  writes tag changes in place.
- **Database.** `jellyide.db` stores the library index, jobs, users,
  sessions and settings. Back it up as described in
  [Backup and restore](#backup-and-restore-sqlite).
- **SAML cert dir.** The auto-generated SP key lives in the music volume
  by default, so it persists across container recreates. If you point
  `SAML_SP_CERT`/`SAML_SP_KEY` at another location, mount **that** path as
  a persistent volume — a lost key breaks the IdP's trust of this SP and
  every SAML login until you re-register the certificate.
- **`.jellyide/`.** The managed yt-dlp binary and its rollback candidate
  live under `.jellyide/bin` and are re-downloaded from scratch if lost
  (first start reinstalls). Displaced files from "replace" rename
  collisions are kept under `.jellyide/trash` for 30 days, then purged at
  startup; restore them from the Conflicts page before that.

## Reverse proxy

For anything beyond a trusted LAN, terminate TLS in front of the app and
set:

- `PUBLIC_URL=https://music.example.com` (no trailing slash)
- `COOKIE_SECURE=true`

`PUBLIC_URL` matters for OIDC (the provider must redirect back to
`<PUBLIC_URL>/api/auth/oidc/callback`) and for SAML (the IdP needs the
`<PUBLIC_URL>/api/auth/saml/acs` endpoint; the SP metadata at
`<PUBLIC_URL>/api/auth/saml/metadata` contains both).

### Caddy

Caddy obtains and renews the TLS certificate automatically:

```caddyfile
music.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

### nginx

```nginx
server {
    listen 443 ssl;
    http2 on;
    server_name music.example.com;

    ssl_certificate     /etc/letsencrypt/live/music.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/music.example.com/privkey.pem;

    # Long-running media streams: do not buffer, keep timeouts generous.
    proxy_buffering off;
    proxy_read_timeout 3600s;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
    }
}

server {
    listen 80;
    server_name music.example.com;
    return 301 https://$host$request_uri;
}
```

## Upgrading

1. Get the new version:
   - dev: `git pull`, then `docker compose build` (or just `up --build`);
   - prod: `docker pull` the new image (or `docker build . -t <image>` from
     the checkout) and point `IMAGE=` at it.
2. Stop and recreate: `docker compose -f docker-compose.prod.yml up -d`
   (or `docker compose up -d` for the dev file). Compose only recreates the
   container when the image or configuration changed.
3. Schema migrations run automatically on startup — they are additive and
   guarded, and an existing database is never downgraded.
4. Verify: `curl -fsS https://music.example.com/api/info` and log in once.

Because all state is in the music volume, no data moves on upgrade.

## Backup and restore (SQLite)

The database is a single WAL-mode SQLite file plus its sidecars. The
library index can be rebuilt from the music files by a rescan, but users,
sessions and job history are only in the database — back up both.

**Backup** (while the server is stopped, or live using SQLite's online
backup API, which is consistent even with a running server):

```sh
# In the host music dir (stop the app first for a fully static copy):
sqlite3 jellyide.db ".backup backup/$(date +%F).db"
# Copy the music files as usual:
rsync -a /mnt/media/music/ backup/music/
```

`VACUUM INTO 'backup.db'` is an equivalent one-shot alternative. Do **not**
copy `jellyide.db` alone while the server runs — copy the `.db`,
`-wal` and `-shm` files together, or use `.backup`/`VACUUM INTO`.

**Restore:** stop the server, replace `jellyide.db` (and delete any
`-wal`/`-shm` sidecars), start the server.

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| API calls answer `503 {"error":"setup_required"}` and the app shows the setup page at `/setup` | First-run setup mode: the database has no users and `ADMIN_PASSWORD` was unset. Open `/setup` and complete the wizard with the one-time setup token printed to the server logs (`docker logs jelly-ide`), or restart once with `ADMIN_PASSWORD` set to pre-create the admin. |
| Login works in the browser but OIDC/SAML redirect fails or loops | `PUBLIC_URL` does not match what the browser sees (wrong scheme/host/port), so the callback URL or the `Secure` cookie flag is off. Set `PUBLIC_URL` and `COOKIE_SECURE=true` behind TLS. |
| OIDC login redirects back to `/login?error=invalid+sso+state` | The state cookie was dropped (10-minute TTL, or blocked by a cross-site cookie policy) or the callback came from another tab. Retry the login from the same browser session. |
| SAML fails with a metadata error at startup | The IdP metadata URL is unreachable or invalid at boot — the server treats a broken SAML config as fatal and does not start. Fix `SAML_IDP_METADATA_URL`/`SAML_IDP_METADATA_FILE` and restart. |
| `401 unauthorized` on API calls after days of inactivity | Sessions expire after 7 days (sliding). Log in again. |
| `403 {"error":"csrf"}` from a script/curl | State-changing requests (POST/PUT/PATCH/DELETE) to protected API paths require the `X-Requested-With: XMLHttpRequest` header; only the public auth paths are exempt — login, register, providers, the OIDC/SAML callback flows and `/api/setup/*`. `/api/auth/logout` and `/api/auth/password` are protected and do require it (see [AUTHENTICATION.md](./AUTHENTICATION.md#session-cookie-and-csrf-security-model)). |
| Login attempts return `429` | More than 10 failed logins from one IP within 15 minutes. Wait out the window. Behind a reverse proxy every client shares the proxy's IP unless you set `TRUST_PROXY=true` (the limiter then keys on the first `X-Forwarded-For` hop — only do this behind a proxy that overwrites the header). |
| Container exits with `bind: address already in use` | Another process holds the host port. Change `PORT` (prod compose) or stop the other process. |
| Media files not found in the UI | The host path mounted at `/mnt/media/music` is wrong or empty; run a scan from the Library page. |

## Security notes

- Keep the admin password long and unique; it is only used on first start
  but the account it creates persists.
- Set `COOKIE_SECURE=true` whenever the site is served over HTTPS.
- **Do not expose this instance to untrusted users.** `GET /api/info` and
  `POST /api/queue` pass user-supplied URLs to `yt-dlp`, which fetches
  them. There is deliberately no host allowlist (downloading arbitrary
  URLs is the product's core purpose) and every route requires a session —
  the app is a personal or team IDE, not a public service.
- `/media/` serves only known audio and image file types; dotfiles,
  `jellyide.db*` and the `saml/` directory answer `404`. The path checks
  are lexical, so a symlink *inside* the music dir that points outside it
  would be followed — keep the music dir operator-managed.
- API clients must complete the login-CSRF handshake (`GET /api/auth/csrf`
  first, then the token in the login/register body) — see
  [AUTHENTICATION.md](./AUTHENTICATION.md#session-cookie-and-csrf-security-model).
- JSON request bodies are capped at 1 MiB (`413` above that; cover uploads
  have their own 10 MB limit). The HTTP server runs with a 10 s
  read-header timeout, a 30 s read timeout, a 120 s idle timeout and no
  write timeout, so long media streams are not cut off.
