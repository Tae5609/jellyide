# JellyIDE architecture

One Go process serves the JSON API, the media files and the single-page frontend. This file
describes the components, the data model, the request lifecycle and the job system, taken
from the code in `backend/` and `frontend/src/`. The endpoint-by-endpoint contract lives in
[API.md](API.md); how to build and test is in [DEVELOPMENT.md](DEVELOPMENT.md).

## Table of contents

- [Overview and component diagram](#overview-and-component-diagram)
- [Request lifecycle](#request-lifecycle)
- [Packages](#packages)
- [Data model](#data-model)
- [Job system](#job-system)
- [External tools](#external-tools)
- [Key design decisions and trade-offs](#key-design-decisions-and-trade-offs)

## Overview and component diagram

The React SPA (built with react-scripts into `frontend/build`) talks to the Go server over
same-origin `fetch`. The server wraps everything in the auth middleware, then dispatches on a
`gorilla/mux` router: `/api/auth/*`, `/api/admin/*` and the route table in
`backend/internal/api/router.go` under the `/api` prefix, `/media/*` as a file server over the
music directory, and `/` as the SPA fallback handler.

```mermaid
graph TD
  SPA["React SPA frontend/src"]

  MW["auth middleware<br/>session + CSRF check"]
  AUTH["/api/auth/* handlers<br/>auth package"]
  ADMIN["/api/admin/* handlers<br/>RequireAdmin"]
  API["/api/* routes<br/>internal/api/router.go"]
  MED["/media/*<br/>http.FileServer DOWNLOAD_DIR"]
  SPAR["SPA handler /<br/>frontend/build + index.html"]

  SVC["services"]
  DL["download.Downloader"]
  SCAN["scanner.Scanner"]
  LIB["library.Service"]
  META["metadata.Service"]
  JOB["jobs.Manager"]
  MBC["musicbrainz.Client"]
  AS["auth.Service"]

  DB[("SQLite jellyide.db<br/>WAL, read + write pools")]
  FS[("music dir<br/>DOWNLOAD_DIR")]

  YTDLP["yt-dlp"]
  FFP["ffprobe (ffmpeg)"]
  PY["python3 update_metadata.py<br/>mutagen"]
  IDP["identity provider<br/>OIDC / SAML IdP"]
  MB["MusicBrainz web service"]

  SPA -->|fetch /api, cookie| MW
  SPA -->|media + static| MW
  MW --> AUTH
  MW --> ADMIN
  MW --> API
  MW --> MED
  MW --> SPAR
  AUTH --- AS
  AUTH <-.->|auth code + PKCE, SAML| IDP
  ADMIN --- AS
  API --> SVC
  SVC --> DL
  SVC --> SCAN
  SVC --> LIB
  SVC --> META
  SVC --> JOB
  META --> MBC
  DL --> YTDLP
  SCAN --> FFP
  META --> PY
  MBC --> MB
  LIB --> DB
  JOB --> DB
  AS --> DB
  DL --> FS
  SCAN --> FS
  META --> FS
  MED --> FS
```

The SPA is same-origin, so there is no CORS handling (the old `AllowedOrigins: *` middleware
was removed; see `docs/plan/BUGS.md`).

## Request lifecycle

`main.go` builds one `mux.Router`, mounts the subrouters and wraps the whole thing:

```go
handler := securityHeaders(authSvc.Middleware(r)) // backend/main.go
handler = authSvc.SetupGate(handler)              // 503s API + media while setup is pending
```

`securityHeaders` adds the browser hardening headers (CSP, `X-Content-Type-Options`,
`X-Frame-Options`, `Referrer-Policy`); the setup gate answers
`503 {"error":"setup_required"}` for every API/media route — except
`/api/auth/providers` and `/api/setup/*` — while first-run setup is pending, and opens
without a restart once the first user exists.

For every request, `Service.Middleware` (`backend/internal/auth/middleware.go`):

1. If `AUTH_ENABLED=false` (`auth.Config.Enabled`) → pass through; the anonymous superuser
   applies (see `/api/auth/me`).
2. Else classify the path with `isPublicPath`: everything outside `/api/` and `/media/`,
   `/api/auth/*` except `/api/auth/me`, `/api/auth/logout` and `/api/auth/password`, and
   `/api/setup/*`, is public → pass through.
3. Protected paths (`/api/*` and `/media/*`): resolve the `jellyide_session` cookie via
   `Service.UserFromRequest` (sha256 hash lookup, expiry + sliding renewal, active-status
   check). Failure → `401 {"error":"unauthorized"}`.
4. On `POST`/`PUT`/`PATCH`/`DELETE`, require the header `X-Requested-With: XMLHttpRequest`
   (CSRF guard) → missing or wrong → `403 {"error":"csrf"}`. The public login/register
   endpoints are guarded by the double-submit login-CSRF token instead
   (`GET /api/auth/csrf`, issued and verified in `handlers.go`).
5. Store the user in the request context (`userContextKey{}`), continue to the matched
   handler. Handlers read it back with `UserFromContext`.

The admin subrouter additionally runs `RequireAdmin` (`admin.go`): `is_admin` must be set,
else `403 {"error":"admin required"}`.

The router itself (`api.RegisterRoutes`) is a flat table — method + path pattern + handler
closure over one of the five injected services. Handlers decode JSON with
`json.NewDecoder(r.Body)`, answer through `writeJSON` / `sendJSONError`, and map the
metadata package's typed errors (`ValidationError`, `NotFoundError`, `ConflictError`) to
`400` / `404` / `409` in `writeMetadataError`.

Sessions: opaque 32-byte crypto/rand tokens, base64url in the `HttpOnly; SameSite=Lax`
cookie (`Secure` when `COOKIE_SECURE=true`), only the sha256 hex digest in the `sessions`
table, 7-day TTL, slid forward when under 6 days remain, purged hourly
(`startSessionCleanup`). Logout deletes the row and clears the cookie (`MaxAge=-1`).

## Packages

| Package | Responsibility (from the code) |
|---|---|
| `backend/main.go` | Entry point: loads env (`getEnv`), opens the DB, bootstraps auth (`auth.NewService` — creates the admin when `users` is empty and `ADMIN_PASSWORD` is set, otherwise arms the first-run setup mode), enables OIDC/SAML before route registration, wires services, mounts `/api`, `/media/` (extension- and traversal-filtered) and the SPA fallback, adds the security headers and the setup gate, serves `:PORT` (8080) with conservative timeouts (10 s read-header, 30 s read, 120 s idle, no write timeout for media streams); also the `server admin` recovery CLI subcommand |
| `internal/api` | Route table + handlers for download, jobs (incl. the retry endpoints), config, library, metadata, cover and lookup endpoints, the sync-conflict surface and the yt-dlp admin endpoints; JSON helpers (1 MiB body cap, `413` on overflow); typed-error → status mapping |
| `internal/auth` | `Config`/env loading, `Service`, local login + bcrypt (`password.go`), registration modes and users/sessions store (`store.go` — the last-active-admin guard runs inside the write transaction), login rate limiter, the double-submit login-CSRF and the auth handlers (`handlers.go`), first-run setup wizard + setup gate (`setup.go`), middleware + `RequireAdmin` (`middleware.go`), session cookie lifecycle (`session.go`), admin user management + settings (`admin.go`), audit log (`audit.go`), OIDC authorization-code flow with PKCE/state/nonce (`oidc.go`), SAML 2.0 service provider (`saml.go`), password hashing (`password.go`) |
| `internal/db` | SQLite open with split pools (`DB` embeds the read pool; `Exec`/`NamedExec`/`Beginx` route to a single write connection), per-connection pragmas, `migrate()` (create tables, guarded `ALTER TABLE` for `users.status`, the flexible track tag columns and `metadata_changes.change_set_id`) |
| `internal/download` | `Details` payload, yt-dlp command builder, dispatcher goroutine with a 5000-slot queue channel, concurrency limit (startup: `CONCURRENT_DOWNLOAD` env var, default 2, clamped 32) + proxy (in-memory, runtime), automatic retry of transient failures (`DOWNLOAD_MAX_RETRIES`, 30 s/2 min/10 min backoff), manual retry endpoints, per-job yt-dlp binary resolver (managed updater), crash recovery re-queues `queued`/`downloading` jobs at startup |
| `internal/scanner` | Walks `DOWNLOAD_DIR`, probes `.flac`/`.mp3`/`.m4a`/`.aac` with ffprobe, parses Vorbis/ID3 tag fields, `EnsureArtist`/`EnsureAlbum`/`UpsertTrack`, cover detection (`cover.jpg`, `folder.jpg`, …); sync-aware upsert (`syncTrack`) — fingerprint comparison, `file_wins`/`db_wins`/`ask` policy dispatch, conflict recording, ghost/missing detection |
| `internal/library` | Artist/album/track model (`Artist`, `Album`, `Track`), queries and mutations, `RenameAlbum` (directory rename + tag rewrite + DB sync), deletes, `SaveCoverArt`/`CoverFilePath` (media-URL ↔ music-dir path mapping with traversal checks); the sync bookkeeping — tag normalization + `tags_hash` (`tagsync.go`), sync state/conflict/trash stores and the `sync_conflict_policy` setting (`sync.go`) |
| `internal/syncconf` | The user-chosen resolution flows on top of the library stores: resolve (`db`/`file`/`custom`, through a metadata job so the outcome is revertible), dismiss, resolve-all, missing-track listing + relink/delete, trash listing/restore/purge (30-day retention), the policy admin accessors |
| `internal/ytdlp` | The managed yt-dlp binary: checksum-verified downloads from a fixed https-only GitHub release allowlist (stable/nightly), atomic swap with `yt-dlp.prev` rollback, the settings (auto-update on/off, 6–168 h interval, channel), the auto-update loop (first-start install + interval checks) and the admin API backing |
| `internal/fsutil` | Filesystem helpers shared by the rename/trash paths, notably `SafeRename` — a rename that refuses to clobber an existing target (the displaced file goes to the trash instead) |
| `internal/metadata` | Title-pattern analyzer (`findSignificantPatterns`), find/replace preview (`PreviewChanges`), the job-backed apply path (`ApplyTrackEdits`, `runApplyJob`, `applyEditsToTrack`), history + revert (`history.go`), cover policy (`cover.go`: validation, allowlist, embed/extract jobs), MusicBrainz lookup mapping (`lookup.go`), tag writer interface (`tags.go`, production: `pythonTagWriter`) |
| `internal/musicbrainz` | HTTP client for the MusicBrainz web service: shared token-bucket rate limiter (1 req/s), 10 s timeout, bounded 10-minute response cache (256 entries), release search + track mapping (exact number match, then Levenshtein title similarity) |
| `backend/update_metadata.py` | Tag writer/reader on the audio files (FLAC/MP3/Ogg/M4A via mutagen): `--title/--album`, `--fields-json`, `--cover`, `--extract-cover` (exit code 2 = no embedded cover); always called with `--` before the file path |
| `frontend/src` | React SPA: `api.js` (`apiFetch` — prefixes `/api`, sends the CSRF header + session cookie, `auth:unauthorized` event on `401`), pages (`Downloads`, `Library`, `Metadata`, `Jobs`, `Sync`/Conflicts, `Settings`, `Login`, `Register`, `Users`, `Setup`), `pages/metadata/` (grid, panels, history, lookup, version-conflict dialog, shortcut hooks), `auth/AuthContext.js`, `components/ModalContext.js` |

## Data model

The schema is what `migrate()` in `backend/internal/db/db.go` creates (plus the guarded
`ALTER TABLE` migrations for databases created before T8/T10). All ids are UUID strings.

```mermaid
erDiagram
  artists {
    TEXT id PK
    TEXT name UK
  }
  albums {
    TEXT id PK
    TEXT artist_id FK
    TEXT title
    TEXT cover_art "media URL"
  }
  tracks {
    TEXT id PK
    TEXT album_id FK
    TEXT title
    TEXT path UK "absolute, under DOWNLOAD_DIR"
    INTEGER duration
    INTEGER bitrate
    TEXT artist
    TEXT album_artist
    INTEGER track_number
    INTEGER disc_number
    INTEGER year
    TEXT genre
    TEXT composer
    TEXT comment
    INTEGER file_size "sync fingerprint"
    INTEGER file_mtime "sync fingerprint"
    TEXT tags_hash "sha256 of the normalized tags"
    INTEGER version "optimistic lock, starts at 1"
    DATETIME synced_at "last synchronization"
    TEXT state "ok missing conflict"
  }
  jobs {
    TEXT id PK
    TEXT type "download scan metadata"
    TEXT status
    TEXT details "JSON"
    TEXT progress
    TEXT error
    DATETIME created_at
    DATETIME updated_at
  }
  metadata_changes {
    TEXT id PK
    TEXT track_id FK
    TEXT field
    TEXT old_value
    TEXT new_value
    TEXT change_set_id "index"
    DATETIME timestamp
  }
  users {
    TEXT id PK
    TEXT username UK
    TEXT password_hash "NULL for SSO-only"
    TEXT provider "local oidc saml"
    TEXT subject
    TEXT email
    TEXT display_name
    INTEGER is_admin
    TEXT status "active pending disabled"
    DATETIME created_at
    DATETIME last_login
  }
  sessions {
    TEXT token_hash PK "sha256 of the cookie token"
    TEXT user_id FK "ON DELETE CASCADE"
    DATETIME expires_at
    DATETIME created_at
  }
  settings {
    TEXT key PK
    TEXT value
  }
  audit_log {
    INTEGER id PK
    DATETIME at
    TEXT actor
    TEXT action
    TEXT target
    TEXT detail
  }
  sync_conflicts {
    TEXT id PK
    TEXT track_id FK
    TEXT field
    TEXT db_value
    TEXT file_value
    TEXT source "scan apply concurrent"
    DATETIME detected_at
    TEXT status "open resolved dismissed"
    TEXT resolved_by
    DATETIME resolved_at
    TEXT resolution
  }
  sync_trash {
    TEXT id PK
    TEXT original_path
    TEXT trash_path
    TEXT track_id FK
    DATETIME created_at
  }

  artists      ||--o{ albums          : "artist_id"
  albums       ||--o{ tracks          : "album_id"
  tracks       ||--o{ metadata_changes : "track_id, cascade"
  tracks       ||--o{ sync_conflicts  : "track_id, cascade"
  users        ||--o{ sessions        : "user_id"
```

Notes from the migration code:

- Uniqueness: `artists.name`; `albums(artist_id, title)`; `tracks.path`; `users.username`;
  `users(provider, subject)`.
- Pragmas on every connection: `busy_timeout=5000`, `journal_mode=WAL`,
  `synchronous=NORMAL`, `foreign_keys=ON`, `wal_autocheckpoint=1000`.
- Indexes: `idx_sessions_user`, `idx_metadata_changes_change_set`,
  `idx_sync_conflicts_track`, `idx_sync_conflicts_status`, `idx_sync_trash_created`.
- `metadata_changes.track_id` and `sync_conflicts.track_id` cascade: deleting a track
  removes its change history and open conflicts with it.
- `tracks` carries the sync bookkeeping: the fingerprint columns (`file_size`,
  `file_mtime`, `tags_hash`), the optimistic-lock `version`, the last `synced_at` and the
  `state` (`ok` | `missing` | `conflict`).
- `settings` holds the runtime `registration_mode` (`admin_only` | `open` | `approval`), the
  `sync_conflict_policy` (`ask` | `file_wins` | `db_wins`) and the yt-dlp updater options
  (`ytdlp_auto_update`, `ytdlp_update_interval_hours`, `ytdlp_channel`, plus the
  `ytdlp_last_check`/`ytdlp_last_result`/`ytdlp_latest` bookkeeping); each environment
  variable seeds its row once, the database value wins afterwards
  (`seedRegistrationMode`, `SeedSyncPolicy`, `ytdlp.Manager.SetSettings`).
- `audit_log` records the admin user-management actions (create/patch/reset-password/
  delete) and the first-run setup completion.

## Job system

`jobs.Manager` (`backend/internal/jobs/manager.go`) is the queue and status store; every
long-running operation becomes a row in `jobs` and a goroutine.

| type | produced by | status flow | details |
|---|---|---|---|
| `download` | `POST /api/queue` | `queued` → `downloading` → `completed`/`failed`; transient failures go back to `queued` with `next_retry_at` armed (automatic retry) | the `download.Details` JSON; `attempts` / `next_retry_at` carry the retry bookkeeping |
| `scan` | `POST /api/library/scan` | `queued` → `running` → `completed`/`failed` | `{"path": …}`, progress `N tracks found` every 10 files |
| `metadata` | bulk edits, revert, cover upload/from-url, conflict resolution | `queued` → `running` → `completed`/`failed` | `action`: `apply_edits` (`change_set_id`, `edits`), `revert_change_set` (`reverts`), `embed_cover` (`album`, `cover`), `resolve_conflicts` (`change_set_id`, `resolutions`); progress `i/n` |
| `ytdlp_update` | `POST /api/admin/ytdlp/update` and the auto-update loop | `queued` → `running` → `completed`/`failed` | `channel`, `reason` (`manual` or `auto`); the progress text carries the outcome ("updated to …" / "up to date: …") |

Key properties from the code:

- The dispatcher is a single goroutine consuming a buffered channel (5000); it gates
  `activeDownloads` against the in-memory `concurrentLimit` (initial value from the
  `CONCURRENT_DOWNLOAD` env var at startup — default 2, clamped 32 — runtime-changeable
  via `POST /api/config`, admin only) with a `sync.Cond`.
- Automatic retry: a transient yt-dlp failure (network markers, HTTP 5xx/429) with retry
  budget left (`DOWNLOAD_MAX_RETRIES`, default 2, `0` disables) is re-queued with a
  backoff of 30 s / 2 min / 10 min (capped); permanent failures (HTTP 403/404,
  unavailable/private/members-only/copyright) are marked `failed` at once. The dispatcher
  defers a re-queued job whose `next_retry_at` is still in the future.
- Crash recovery: `NewDownloader` re-reads the table and re-queues every `download` job in
  `queued` or `downloading` state (`0%` progress) at startup.
- Status writes never abort the pipeline; a failed `UpdateJobStatus` is only logged.
- After a successful download the file is indexed immediately (`Scanner.IndexFile`).
- `POST /api/jobs/clear` deletes rows in `completed` or `failed` (`ClearCompleted`).
- Metadata jobs run detached: the endpoint answers `202` with the `job_id` and the shared
  `change_set_id`; the client polls `GET /api/jobs` and refreshes on `completed`.

## External tools

| Tool | Where | Command shape (from the code) |
|---|---|---|
| `yt-dlp` | info fetch | `--dump-single-json --flat-playlist --playlist-end 200 -- <url>` (`infoArgs`; the URL is always after `--`) |
| `yt-dlp` | download | fixed URL argument `https://youtube.com/watch?v=<id>` plus named options (`--extract-audio`, `--audio-format flac`, `--embed-thumbnail`, `--embed-metadata`, `--parse-metadata`, `--replace-in-metadata`, `--js-runtimes node`, `--output`, `--newline`, optional `--proxy`); no user value is ever an option |
| `ffprobe` | scan | `ffprobe -v quiet -print_format json -show_format <file>` |
| `python3 update_metadata.py` | tag writes, rename, cover embed/extract | `python3 update_metadata.py …args… -- <path>` (argument vector, never a shell; exit 2 = no embedded cover) |
| MusicBrainz web service | lookup | `GET <base>/release?...&fmt=json` with `User-Agent: JellyIDE/1.0 …` |

The production image installs `ffmpeg`, `python3`, `nodejs` and `pip3 install yt-dlp mutagen`
(see `Dockerfile`).

## Key design decisions and trade-offs

- **SQLite, split read/write pools.** One process, zero external database service. WAL +
  `busy_timeout=5000` + `synchronous=NORMAL` give concurrent readers (pool of 4) without
  blocking the single writer (pool of 1); `DB.Exec`/`Beginx` intercept and route every write
  to the write connection so call sites never have to care. Trade-off: one writer only —
  heavy scan+apply work serialises, which is exactly what SQLite prefers, but horizontal
  scaling would need a real database.
- **Sessions in the database, not JWTs.** Logout, "disable user" and "admin resets the
  password" must invalidate instantly; server-side rows (`sessions`, deleted on logout, on
  disable, on reset and on delete) give that. The cookie is `HttpOnly; SameSite=Lax`, only a
  sha256 hash is stored, and the state-changing CSRF check complements it. Trade-off: a
  database hit per protected request and no stateless scale-out — acceptable for a
  single-instance self-hosted tool.
- **Python helper for tag writing.** mutagen covers the FLAC/MP3/Ogg/M4A formats reliably;
  writing a full tag writer in Go would duplicate that. The subprocess is always an argument
  vector with `--` before the path (no shell, no option injection), and failures are per-file
  so one bad file does not abort a bulk apply. Trade-off: `python3` + `mutagen` are hard
  runtime dependencies and each write forks a process.
- **Server-rendered SPA fallback instead of a separate web server.** One process, one port,
  same-origin → no CORS and one cookie. The handler serves existing files as-is, serves
  `index.html` for unknown extensionless paths (client-side routes), answers `404` for
  missing assets with a file extension, and refuses `..` traversal and anything escaping
  the build dir.
- **Pending edits on the client, applied as one change set.** The metadata UI accumulates
  edits and reviews them before `PATCH /api/metadata/tracks`; the server validates the whole
  batch first (fail fast → `400` with `field`/`track_id`), then applies under one
  `change_set_id` so history and revert work per change set. Trade-off: an applied job that
  renames a file before its database update fails can leave the rename rolled forward
  (accepted parity gap, documented in `apply.go`; re-running or re-scanning repairs).
- **Last-admin check invariant.** The server refuses to demote, disable or delete the last
  active admin (checked before the self-delete rule), so the instance can never become
  unmanageable. The guard runs inside the write transaction
  (`checkLastActiveAdminTx`), so two concurrent demotions cannot both pass it.
- **Rate limiting where users (and servers) need it.** Per IP: 10 failed logins/registers per
  15 minutes → `429` (in-memory, keyed by `RemoteAddr`, or by the first `X-Forwarded-For`
  hop when `TRUST_PROXY=true` — only behind a proxy that overwrites the header); the
  first-run setup endpoint runs a stricter limiter (5 failures per IP per 15 minutes).
  Outbound to MusicBrainz: one shared token bucket (1 req/s) + 10-minute cache to respect
  the public service.
- **Managed yt-dlp binary with an allowlisted updater.** The downloader resolves the
  binary path per job through `ytdlp.Manager`; when `YTDLP_MANAGED` is on (the default) the
  server installs its own copy of the official zipapp on first start and auto-updates it
  on a configurable interval, from a fixed https-only GitHub release allowlist with
  checksum verification and an atomic swap that keeps the previous binary as rollback
  candidate. A swap pauses new downloads and waits for running ones (10-minute bound), and
  any failure leaves the previous binary in place — the updater can never break downloads.
- **Explicit sync conflicts instead of silent divergence.** A scan that finds the file
  differs from the database (and JellyIDE edited the row in the meantime) never guesses:
  with the default `ask` policy it records one `sync_conflicts` row per differing field
  and parks the track in `conflict` state until a user resolves, dismisses or lets the
  `file_wins`/`db_wins` policy settle it. The editor side is guarded by the per-row
  `version` optimistic lock, so concurrent editors produce recorded conflicts rather than
  lost edits. Trade-off: under `ask` a diverged track is left exactly as it is on both
  sides until someone resolves or dismisses the conflict (the grid marks it with a
  conflict chip that links to the Conflicts page); `file_wins`/`db_wins` trade that
  decision time for automatic settling.
- **File naming scheme.** Downloads and renames build file names from the sanitized
  title (`/ \ : * ? " < > |` → `-`) plus the original extension; albums map to
  `<DOWNLOAD_DIR>/<artist>/<album>/` with the same sanitizer.
- **Allowlist for outbound cover fetches.** Only `https` and `coverartarchive.org`,
  `archive.org`, `*.archive.org`, re-checked on every redirect (max 3), 15 s timeout,
  10 MiB size cap, `jpeg`/`png` sniffing, ≤4000 px — so a lookup can not turn the server
  into an arbitrary fetcher. The download URL allowlist for the MusicBrainz cover mirror is
  built into the lookup response (`https://coverartarchive.org/release/<mbid>/front`).
- **Timing-safe credential comparison.** Login runs a bcrypt comparison against a dummy hash
  for unknown users, so a wrong username and a wrong password answer in the same time and
  with the same `401 invalid credentials`; the account status (pending/disabled) is only
  revealed after a correct password.
