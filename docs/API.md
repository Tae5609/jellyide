# JellyIDE HTTP API reference

Every endpoint the Go server exposes, grouped by area. The reference is taken from the
code: routes from `backend/internal/api/router.go` (`sync.go`, `ytdlp.go`) and
`backend/internal/auth/` (`handlers.go`, `setup.go`, `admin.go`, `oidc.go`, `saml.go`),
request and response shapes from the handler bodies.

## Table of contents

- [General conventions](#general-conventions)
  - [Base URL and formats](#base-url-and-formats)
  - [Authentication and the CSRF header](#authentication-and-the-csrf-header)
  - [Error shape and status codes](#error-shape-and-status-codes)
- [1. Auth](#1-auth)
- [2. Single sign-on flows](#2-single-sign-on-flows)
- [3. Users and admin](#3-users-and-admin)
- [4. Library](#4-library)
- [5. Metadata](#5-metadata)
- [6. Download and jobs](#6-download-and-jobs)
- [7. Config](#7-config)
- [8. Media](#8-media)
- [9. Sync conflicts](#9-sync-conflicts)

## General conventions

### Base URL and formats

- Every route below is listed relative to the API base URL **`/api`** — with the one
  exception of the media area, which lives at the root: **`/media/...`**.
  Example: `POST /api/auth/login`, `GET /media/Artist/Album/track.flac`.
- Request and response bodies are JSON (`Content-Type: application/json`) unless stated
  otherwise. Two endpoints differ: cover upload takes `multipart/form-data`, and
  `GET /api/library/albums/{id}/cover` and the media area return raw file bytes.
- The CSRF header is spelled **`X-Requested-With`** everywhere: the middleware checks that
  name, the bundled frontend sends it and the tests assert it
  (`backend/internal/auth/middleware.go`, `frontend/src/api.js`). See
  [the CSRF header](#the-csrf-header).

### Authentication and the CSRF header

The auth middleware (`backend/internal/auth/middleware.go`) wraps the whole router. When
`AUTH_ENABLED=false` it passes every request through, and every endpoint below is open
(`/api/auth/me` then reports the anonymous superuser). With auth enabled:

| Path class | Session required? | CSRF header required? |
|---|---|---|
| everything outside `/api/` and `/media/` (the SPA) | no | no |
| `/api/auth/*`, except `/api/auth/me`, `/api/auth/logout`, `/api/auth/password` | no | no (login and register instead require the [login-CSRF token](#the-login-csrf-token) in the body) |
| `/api/setup/*` (first-run wizard) | no | no |
| all other `/api/*` and all `/media/*` | **yes** | only on `POST`, `PUT`, `PATCH`, `DELETE` |
| `/api/admin/*` | yes | yes (state changes) — plus the caller must be an admin, else `403 {"error":"admin required"}` |

A missing or invalid session answers `401 {"error":"unauthorized"}`. A state-changing
request without the CSRF header answers `403 {"error":"csrf"}`.

While first-run setup is pending (no users exist yet), every `/api/*` route except
`/api/auth/providers` and the `/api/setup/*` pair, plus all of `/media/*`, answers
`503 {"error":"setup_required"}` instead; the gate opens without a restart as soon as the
first user exists.

#### The CSRF header

State-changing requests (`POST`, `PUT`, `PATCH`, `DELETE`) on protected paths must carry the
standard header with the exact value `XMLHttpRequest`:

```http
X-Requested-With: XMLHttpRequest
```

`middleware.go` compares `r.Header.Get("X-Requested-With")` against that value; the bundled
SPA adds the header to every call through `apiFetch` (`frontend/src/api.js`), so the browser
flow works end to end. The check is a same-site CSRF guard for cookie-authenticated requests:
a cross-origin form post cannot set custom headers. Third-party clients must send it on every
state-changing call, else they receive `403 {"error":"csrf"}`.

#### The login-CSRF token

The public login and register endpoints do not need a session, so the header check above
does not apply to them. They are protected by a double-submit token instead:

1. `GET /api/auth/csrf` (public) returns a random token in the body **and** mirrors it in
   the `jellyide_login_csrf` cookie (`HttpOnly`, `SameSite=Lax`, `Secure` when
   `COOKIE_SECURE=true`, 1-hour expiry).
2. `POST /api/auth/login` and `POST /api/auth/register` must echo the token in the body
   (`csrf_token`); the server compares it with the cookie. A cross-site page can read
   neither the body (no CORS) nor a fresh cookie (Lax), so a forged login or registration
   gets `403 {"error":"login csrf mismatch"}`.

The bundled SPA fetches the token automatically before submitting
(`frontend/src/auth/AuthContext.js` for login, `frontend/src/pages/Register.js` for
registration).

### Error shape and status codes

Errors are JSON. Most errors carry one member:

```json
{ "error": "invalid credentials" }
```

Metadata validation errors may carry two additional members naming the offending field and
track:

```json
{ "error": "year must be between 0 and 9999, got 99999", "field": "year", "track_id": "0f66…" }
```

| Code | Meaning in this API |
|---|---|
| `200 OK` | done; body holds the result (sometimes empty) |
| `201 Created` | user created (`/api/auth/register`, `POST /api/admin/users`) |
| `202 Accepted` | accepted for processing; a background job was queued — poll `/api/jobs` |
| `204 No Content` | done; no body (logouts, deletes, admin writes) |
| `302 Found` | SSO redirects only |
| `400 Bad Request` | malformed JSON, missing or invalid parameters, validation failures |
| `401 Unauthorized` | no valid session; wrong credentials |
| `403 Forbidden` | CSRF header missing; not an admin; forbidden action (registration off, own-account rules) |
| `404 Not Found` | album, track, cover, change set or job does not exist |
| `409 Conflict` | username taken; metadata target exists; affected track no longer exists; version-lock conflict |
| `413 Request Entity Too Large` | JSON body above the 1 MiB cap (`request body too large`; cover uploads have their own 10 MB limit) |
| `429 Too Many Requests` | login/register failure limit reached (10 failures per IP per 15 minutes); setup-token limit reached (5 per IP per 15 minutes) |
| `500 Internal Server Error` | backend failure; the message is the internal error text |
| `503 Service Unavailable` | OIDC endpoints called while OIDC is disabled; any API/media route while first-run setup is pending (see [first-run setup](#first-run-setup)) |

---

## 1. Auth

Local accounts and sessions. With `AUTH_LOCAL_ENABLED=false` the login and registration
endpoints answer `403 {"error":"local login disabled"}`.

### GET /api/auth/providers

Public. Describes what the login page may offer.

- Request: none.
- Response `200`:

```json
{
  "enabled": true,
  "local": true,
  "oidc": { "enabled": false, "name": "" },
  "saml": { "enabled": false, "name": "" },
  "registration": "admin_only",
  "setup_required": false
}
```

`registration` is the current mode: `admin_only` | `open` | `approval`.
`setup_required` is `true` while the first-run setup wizard is pending
(the SPA then redirects every route to `/setup`).

### POST /api/auth/login

Public (no session, no CSRF header needed), but the body must carry the
[login-CSRF token](#the-login-csrf-token) from `GET /api/auth/csrf`. Verifies the bcrypt
hash; an unknown username still runs a bcrypt comparison against a dummy hash so timing
does not leak existence.

- Request:

```json
{ "username": "admin", "password": "…", "csrf_token": "…" }
```

- Response `200` — the authenticated [user object](#user-object); the server also sets the
  `HttpOnly` session cookie `jellyide_session` (`SameSite=Lax`; `Secure` when
  `COOKIE_SECURE=true`; 7-day sliding expiry).
- Errors: `400` invalid json · `401 {"error":"invalid credentials"}` ·
  `403` `login csrf mismatch` | `account pending approval` | `account disabled` ·
  `403 {"error":"local login disabled"}` · `413` body too large ·
  `429` too many failed attempts.

The account status is checked only after a correct password, so a wrong password never
reveals whether the account is pending or disabled.

### POST /api/auth/logout

Session + CSRF header required.

- Request: none.
- Response `204`. The session row matching the cookie is deleted (if any) and the cookie is
  cleared (`MaxAge=-1`). An authenticated caller always gets `204`, even when the cookie
  matches no session row.

### GET /api/auth/me

Session required (not public, but no CSRF header — `GET`).

- Response `200` — the [user object](#user-object). With `AUTH_ENABLED=false` it is the
  anonymous superuser: `{ "id": "anonymous", "username": "anonymous", "provider": "none",
  "is_admin": true }`.
- Errors: `401 unauthorized`.

### POST /api/auth/password

Session + CSRF header required. Local users only.

- Request:

```json
{ "current_password": "…", "new_password": "at least 8 chars" }
```

- Response `204`. Other sessions of this user keep running; re-login is not forced.
- Errors: `400` invalid json / new password too short · `401` unauthorized (no session) or
  `invalid credentials` (wrong current password) · `403` not a local user.

### POST /api/auth/register

Public. Honours the registration mode from the `settings` table (seeded once from
`REGISTRATION_MODE`; the database value wins afterwards). Like login, the body must carry
the [login-CSRF token](#the-login-csrf-token); in mode `admin_only` the
`403 registration is disabled` answer is given before the token is checked.

- Request:

```json
{ "username": "peter", "password": "at least 8 chars", "email": "optional@example.org",
  "csrf_token": "…" }
```

Usernames must match `^[a-zA-Z0-9._-]{3,32}$`.

- Response `201` — and a session cookie in mode `open` only:

```json
{ "status": "active" }
```

  In mode `approval` the status is `pending` and no session is issued.
- Errors: `400` invalid username / password too short · `403 registration is disabled`
  (mode `admin_only`) or `local login disabled` · `403 {"error":"login csrf mismatch"}` ·
  `409 username already taken` · `413` body too large ·
  `429` too many failed attempts (shared limiter with login — validation rejections feed
  it too).

### First-run setup

The first-run wizard (`backend/internal/auth/setup.go`). While it is pending, the setup
gate answers `503 {"error":"setup_required"}` for every other API route and for
`/media/*` (see [Authentication and the CSRF header](#authentication-and-the-csrf-header)).

#### GET /api/setup/status

Public. - Response `200`: `{ "setup_required": true }`

#### POST /api/setup/complete

Public. Creates the first admin (active, `is_admin`), starts a session for it and ends
setup mode immediately — no restart. The whole completion runs in one transaction, so it
completes exactly once.

- Request:

```json
{ "token": "…", "username": "admin", "password": "at least 8 chars", "email": "…" }
```

  The `token` is the one-time setup token printed to the server log; the username and
  password follow the same rules as registration.
- Response `201` — the new [user object](#user-object) and a session cookie.
- Errors: `400` invalid username / password too short · `403 {"error":"invalid setup
  token"}` · `409 {"error":"setup already complete"}` · `413` body too large ·
  `429` too many failed attempts (stricter limit than login: 5 per IP per 15 minutes).

### User object

Returned by login, `/api/auth/me` and the admin endpoints. The members `password_hash` and
`subject` are never serialized.

```json
{
  "id": "9f8b…",
  "username": "admin",
  "provider": "local",
  "email": null,
  "display_name": null,
  "is_admin": true,
  "status": "active",
  "created_at": "2026-11-22 09:00:00",
  "last_login": "2026-11-22 09:10:00"
}
```

`provider`: `local` | `oidc` | `saml`. `status`: `active` | `pending` | `disabled`.

---

## 2. Single sign-on flows

These routes exist only when the provider is enabled (`OIDC_ENABLED` / `SAML_ENABLED`).
All are public; none needs the CSRF header. They are browser redirects, not a JSON API.

### OIDC (authorization code flow with PKCE, state and nonce)

#### GET /api/auth/oidc/login

Starts the flow. `302` to the provider's authorization endpoint; sets the binding cookie
`jellyide_oidc_state` (`HttpOnly`, `Path=/api/auth/oidc`, max age 600 s).

#### GET /api/auth/oidc/callback

Query parameters from the provider: `code`, `state`, optional `error`.

- Success: verifies state (cookie + pending entry, single use), exchanges the code with the
  PKCE verifier, verifies the `id_token` signature and nonce, provisions or updates the user
  (`OIDC_AUTO_CREATE`, admin group from `OIDC_ADMIN_GROUP` in the `OIDC_GROUPS_CLAIM`
  claim), starts a session and `302`s to `/`.
- Failure: `302` to `/login?error=<message>` (generic text; tokens and codes are never
  logged nor leaked). Pending or disabled accounts are refused here.
- `503 sso disabled` if the provider went down after startup.

The redirect URI registered at the provider must be
`<PUBLIC_URL>/api/auth/oidc/callback` (default `http://localhost:8080`).

### SAML 2.0 service provider

Endpoints, built on `github.com/crewjam/saml/samlsp`, rooted at `<PUBLIC_URL>/api/auth`:

| Route | Method | Purpose |
|---|---|---|
| `/api/auth/saml/login` | `GET` | SP-initiated SSO; `302` to the IdP |
| `/api/auth/saml/acs` | `POST` | Assertion Consumer Service; the IdP posts the response here (cross-site, so no session and no CSRF header are required). Signature and conditions validated by the library; unsolicited (IdP-initiated) responses are refused |
| `/api/auth/saml/metadata` | `GET` | SAML metadata of this service provider (XML) |

On success the user is provisioned (`SAML_AUTO_CREATE`, admin group from
`SAML_GROUPS_ATTR` equals `SAML_ADMIN_GROUP`), a JellyIDE session is started and the
browser is `302`ed to `/`. On any failure the browser goes to
`/login?error=SAML+login+failed`.

---

## 3. Users and admin

All routes below require a session of an **admin** user (`is_admin`), the CSRF header on
state changes, and answer `403 {"error":"admin required"}` otherwise. With
`AUTH_ENABLED=false` everyone passes (anonymous superuser).

### GET /api/admin/users

Lists all users, oldest first. Response `200`: array of [user object](#user-object).

### POST /api/admin/users

Creates an active local user (bypasses the registration mode).

- Request:

```json
{ "username": "peter", "password": "at least 8 chars", "email": "…", "is_admin": false }
```

- Response `201` — [user object](#user-object).
- Errors: `400` invalid username / password too short · `409 username already taken`.

### PATCH /api/admin/users/{id}

Partial update. Any of the four members may be sent; absent members stay untouched. A
non-null `email`/`display_name` of `""` clears the field to `NULL`.

- Request:

```json
{ "is_admin": true, "status": "disabled", "email": "…", "display_name": "…" }
```

- Response `200` — the updated [user object](#user-object). Setting a status other than
  `active` deletes every session of that user.
- Errors: `400 invalid status` · `404 user not found` ·
  `409 cannot demote the last active admin` | `cannot disable the last active admin`.

### DELETE /api/admin/users/{id}

Deletes the user and all of their sessions.

- Response `204`.
- Errors: `400 cannot delete your own account` · `404 user not found` ·
  `409 cannot delete the last active admin` (checked before the self-check).

### POST /api/admin/users/{id}/password

Admin password reset, local users only; it then deletes every session of the user.

- Request: `{ "new_password": "at least 8 chars" }`
- Response `204`.
- Errors: `400` too short / invalid json · `403` not a local user · `404 user not found`.

### GET /api/admin/settings

Response `200`: `{ "registration_mode": "admin_only" }` (`admin_only` | `open` |
`approval`).

### PUT /api/admin/settings

- Request: `{ "registration_mode": "open" }`
- Response `204`.
- Errors: `400 invalid registration mode`.

### yt-dlp updater

The managed yt-dlp binary (`backend/internal/ytdlp/`). All five routes require an admin
session. They share one status shape (returned by `GET`, `POST /check`, `POST /rollback`
and `PUT /settings`):

```json
{
  "version": "2025.11.21",
  "path": "/mnt/media/music/.jellyide/bin/yt-dlp",
  "managed": true,
  "channel": "stable",
  "latest_version": "2025.12.01",
  "update_available": true,
  "last_check": "2026-11-22T09:00:00Z",
  "last_result": "checked 2025.12.01",
  "auto_update": true,
  "interval_hours": 24,
  "updating": false,
  "has_rollback": true
}
```

`latest_version` is `null` until the first check; `managed` is `false` when
`YTDLP_MANAGED` is set to an explicit negative (the system `yt-dlp` on the `PATH` is used
and no update is ever installed).

#### GET /api/admin/ytdlp

- Response `200` — the status. Errors: `500`.

#### POST /api/admin/ytdlp/check

Refreshes the latest known release for the configured channel (2-minute bound) and
records it; it never installs anything. Even a failed check returns the status shape,
with the failure in `last_result`.

- Response `200` — the status.

#### POST /api/admin/ytdlp/update

Always enqueues a `ytdlp_update` job (poll `/api/jobs` for the outcome — "updated to
…" or "up to date: …" in its progress) and answers immediately. An update swap pauses
new downloads, waits for running ones (10-minute bound) and restores the previous
binary on any failure; the updater can never break downloads.

- Request (optional body): `{ "force": true }` — reinstall even when the binary in use
  already matches the known latest release.
- Response `202`: `{ "job_id": "…" }`
- Errors: `409 {"error":"an update is already running"}` · `500`.

#### POST /api/admin/ytdlp/rollback

Restores the previous binary (`yt-dlp.prev`); it shares the single update slot, so it is
refused (not blocked) while an update is in flight.

- Response `200` — the status.
- Errors: `409` `no previous binary to roll back to` | `an update is already running` ·
  `500`.

#### PUT /api/admin/ytdlp/settings

Partial update of the updater options; absent members keep their value.

- Request: any of `{ "auto_update": false, "interval_hours": 48, "channel": "stable" }`
  — `interval_hours` 6…168 (default 24), `channel` `stable` | `nightly` (default
  `stable`), `auto_update` default `true` (a disabled auto-update still honours the
  forced first-start install and manual updates).
- Response `200` — the status.
- Errors: `400` invalid json / value out of range · `500`.

---

## 4. Library

Read and maintenance of the artist → album → track index. All routes need a session; the
state-changing ones also the CSRF header.

### GET /api/library/artists

Response `200`:

```json
[ { "id": "08f1…", "name": "Kraftwerk" } ]
```

### GET /api/library/artists/{id}/albums

Response `200`:

```json
[ { "id": "1a2b…", "artist_id": "08f1…", "title": "Autobahn", "cover_art": "/media/Kraftwerk/Autobahn/cover.jpg", "artist": "Kraftwerk" } ]
```

`cover_art` is `""` when the album has no cover.

### GET /api/library/albums/{id}/tracks

Response `200`: array of track objects. Missing tags are `null` (stored as `NULL`):

```json
[
  {
    "id": "4c9d…", "album_id": "1a2b…", "title": "Autobahn",
    "path": "/mnt/media/music/Kraftwerk/Autobahn/Autobahn.flac",
    "duration": 372, "bitrate": 903,
    "artist": "Kraftwerk", "album_artist": "Kraftwerk",
    "track_number": 1, "disc_number": 1, "year": 1974,
    "genre": "Electronic", "composer": null, "comment": null
  }
]
```

### GET /api/library/albums/{id}

Response `200` — album object (see above). Errors: `404` (album not found).

### POST /api/library/albums/{id}/rename

Renames the album in the database, the directory on disk and the `album` tag of every
track file (via the Python tag writer).

- Request: `{ "title": "Autobahn (Deluxe)" }`
- Response `200` (empty body). Errors: `400 invalid json` · `500`.

### DELETE /api/library/albums/{id}

Removes the album and its tracks from the database. Files on disk are left in place.

- Response `200` (empty body). Errors: `500`.

### GET /api/library/albums/{id}/cover

Serves the cover image file (bytes, `Content-Type` sniffed by the server).

- Errors: `404 {"error":"album has no cover"}` · `500`.

### POST /api/library/albums/{id}/cover

Uploads a cover. `multipart/form-data` with the file in the form field **`file`**;
in-memory form limit 10 MB plus 512 KiB, larger payloads spill to disk.

- Response `202`:

```json
{ "job_id": "c0ffee…", "path": "/media/Kraftwerk/Autobahn/cover.jpg" }
```

  The response is `202` because a job is queued: `type: "metadata"` with
  `details.action: "embed_cover"` (see [jobs](#6-download-and-jobs)). The job embeds the
  image into every track of the album; poll `/api/jobs` until it is `completed`.
- Errors: `400 invalid multipart form or file too large` · `400 missing file field` ·
  `400` cover validation (`cover exceeds the 10 MB limit`, dimension or sniff failures —
  allowed media types are checked, maximum dimension 4000 px) · `404` album not found.

### POST /api/library/albums/{id}/cover/extract

Extracts the embedded cover art of the first track that carries one and stores it as the
album cover. No job, synchronous.

- Response `200`: `{ "path": "/media/Kraftwerk/Autobahn/cover.jpg" }`
- Errors: `400` validation · `404` album not found or no embedded cover anywhere · `500`.

### POST /api/library/albums/{id}/cover/from-url

Downloads a cover from an allowlisted URL, stores it and queues the embed job.

- Request: `{ "url": "https://coverartarchive.org/…" }`
- Response `202`: `{ "job_id": "…", "path": "/media/…/cover.jpg" }`
- Allowlist (`backend/internal/metadata/cover.go`): `https` only; hosts
  `coverartarchive.org`, `archive.org` and `*.archive.org`; redirects are re-checked and
  capped at 3 hops; the client timeout is 15 s.
- Errors: `400 only https URLs are allowed for cover art` | `host … is not in the cover
  art allowlist` | `invalid url` | `too many redirects (3 max)` | `cover download failed:
  …` | size/dimension violations (`cover exceeds the 10 MB limit`, `image is WxH, maximum
  is 4000x4000`, `unsupported content type … (jpeg and png only)`) · `404` album not found.

### GET /api/library/tracks/{id}

Response `200` — track object (see above). Errors: `404 {"error":"track not found"}`.

### DELETE /api/library/tracks/{id}

Removes one track from the database; the file on disk stays. The track's
`metadata_changes` rows are removed with it (`ON DELETE CASCADE`), so a track with
recorded edits can be deleted.

- Response `200` (empty body). Errors: `404 {"error":"track not found"}` · `500`.

### POST /api/library/scan

Starts a background scan (`202 Accepted`): walks `DOWNLOAD_DIR`, probes every `.flac`,
`.mp3`, `.m4a`, `.aac` file with `ffprobe` and indexes it. Progress appears in the job
list as `N tracks found`.

- Response `202` (empty body). Errors: `500`.

---

## 5. Metadata

The metadata IDE's engine. Bulk edits are queued as jobs; every applied edit is recorded
in `metadata_changes` under a shared `change_set_id`, which powers the history and the
revert.

### GET /api/metadata/analyze/{album_id}

Finds significant shared substrings among the album's track titles (the noisy-suffix
suggestions, e.g. `[Official Audio]`).

- Response `200`:

```json
{
  "suggestions": [ { "text": "[Official Audio]", "count": 9, "score": 0.9 } ],
  "common_substrings": [ { "text": " – Topic", "count": 4, "score": 0.4 } ]
}
```

- Errors: `500`.

### POST /api/metadata/preview

Dry-run of a find-and-replace operation over one field of many tracks. Nothing is written.

- Request:

```json
{
  "action": "regex",
  "target": "\\s*\\[(Official.*)\\]$",
  "value": "",
  "track_ids": ["4c9d…", "5dae…"],
  "field": "title"
}
```

  - `action`: `replace` (literal `strings.ReplaceAll`) | `prefix` | `suffix` | `regex`
    (an invalid regular expression leaves the value unchanged).
  - `field`: `title` (default) | `artist` | `album_artist` | `genre` | `composer` |
    `comment`. Unknown tracks are skipped silently.
- Response `200` (the keys `old_title`/`new_title` are kept for compatibility with earlier
  versions even for non-title fields):

```json
[ { "track_id": "4c9d…", "old_title": "Autobahn [Official Audio]", "new_title": "Autobahn" } ]
```

- Errors: `400` invalid json · `400 {"error":"invalid field …","field":"…"}`.

### POST /api/metadata/apply

Writes a previously previewed change set to disk and database: tags via the Python helper,
file renamed when the title changed, database row updated, change recorded.

- Request: the array of preview rows, e.g. `[ { "track_id": "…", "old_title": "…",
  "new_title": "…" } ]`
- Response `200` (empty body). Errors: `400 invalid json`.
- Per-track failures (tag write, rename, database) are logged server-side and the track is
  skipped; the handler answers `200` for any well-formed JSON body. Prefer
  `PATCH /api/metadata/tracks`, which reports validation problems as `400`.

### PATCH /api/metadata/tracks

The bulk-edit endpoint. Validates every entry, then queues a `metadata` job with
`details.action: "apply_edits"` (see [jobs](#6-download-and-jobs)).

- Request — `fields` is a sparse map; a member set to `null` clears the column
  (`NULL`):

```json
{
  "edits": [
    { "track_id": "4c9d…",
      "fields": { "title": "Autobahn", "track_number": 1, "year": 1974, "comment": null },
      "expected_version": 3 }
  ]
}
```

  - Text fields: `title`, `album`, `artist`, `album_artist`, `genre`, `composer`,
    `comment`. Integer fields: `track_number`, `disc_number`, `year` (0–9999).
  - `title` must not be empty; integers must be non-null non-negative (use `null` to
    clear). An `album` edit repoints the track to the target album row (created if
    needed); the file on disk is not renamed for album edits.
  - `expected_version` (optional) is the optimistic lock: the `version` the editor
    loaded (the editor sends the version of every row in the grid). The check runs
    synchronously **and** is re-checked when the job runs; a track that moved in the
    meantime is skipped and recorded as a `concurrent` sync conflict. While the apply
    runs, every edited field is re-read from the file first — an external change there
    skips the track, records an `apply` conflict and leaves both sides untouched.
- Response `202`:

```json
{ "job_id": "c0ffee…", "change_set_id": "beef…" }
```

  Poll `GET /api/jobs` until the job is `completed`, then refresh.
- Errors (validation errors carry `field` and `track_id` where applicable):
  `400` — `edits must not be empty`, `track_id is required`, `fields must not be empty`,
  `unknown field`, `field must be a string or null`, `title must not be empty`,
  `field must be a non-negative integer or null`, `album cannot be cleared; set a new album
  name instead`, `duplicate field in one request`, range messages (`year must be between 0
  and 9999, got N`, `track_number must be between 0 and 999, got N`) ·
  `404 {"error":"track not found","track_id":"…"}` (checked before the job is queued) ·
  `409` when **every** track already lost the version lock —
  `{ "error": "N track(s) were changed by another user; re-apply with the new version",
  "conflicts": [ { "track_id": "…", "reason": "version_conflict", "current": { …current
  field values… }, "current_version": 4 } ] }` (a **partial** conflict answers `202`; the
  job skips the stale tracks and reports them in its results) ·
  `500`.

### GET /api/metadata/history

Lists change sets, newest first, with their recorded field edits.

- Query: `album_id` (optional — omit for the whole history), `limit` (default 50, values
  above 200 are clamped to 200).
- Response `200`:

```json
[
  {
    "change_set_id": "beef…",
    "created_at": "2026-11-22T10:00:00Z",
    "track_count": 2,
    "changes": [
      { "track_id": "4c9d…", "track_title": "Autobahn", "field": "title",
        "old_value": "Autobahn [Official]", "new_value": "Autobahn" }
    ]
  }
]
```

- Errors: `400 {"error":"limit must be a positive integer"}` · `500`.

### POST /api/metadata/history/{change_set_id}/revert

Applies the `old_value`s of a change set as a **new** change set (a revert is itself
revertible, including file renames).

- Response `202`: `{ "job_id": "…", "change_set_id": "new one…" }` — the queued job has
  `type: "metadata"` and `details.action: "revert_change_set"`; its `details.reverts` names
  the change set being undone.
- Errors: `404 {"error":"change set not found"}` · `404 {"error":"change set has no
  reversible fields"}` · `409 {"error":"track no longer exists","track_id":"…"}` ·
  `400 {"error":"stored value for field is not an integer","field":"…","track_id":"…"}` ·
  `500`.

### GET /api/metadata/lookup/search

Proxies the MusicBrainz release search (max 10 results, 1 request/second shared rate
limit, 10-minute response cache).

- Query: `album_id` (optional) and `q`. When `q` is empty the query defaults to the
  album's artist and title.
- Response `200`:

```json
[
  { "mbid": "8f65…", "title": "Autobahn", "artist": "Kraftwerk", "date": "1974-11",
    "country": "XE", "track_count": 9, "score": 100 }
]
```

- Errors: `400 {"error":"q or album_id is required"}` (both parameters missing) ·
  `400 {"error":"album has no name to search for"}` · `404 {"error":"album not found"}` ·
  `500` (upstream MusicBrainz failure).

### GET /api/metadata/lookup/release/{mbid}

Fetches one release and maps it onto the album's local tracks (exact disc/track number
first — confidence 1; otherwise best normalized-title similarity, Levenshtein ratio).

- Query: `album_id` (required — a missing value answers `400 {"error":"album_id is required"}`).
- Response `200`:

```json
{
  "release": { "mbid": "8f66…", "title": "Autobahn", "artist": "Kraftwerk", "…": "…" },
  "cover_url": "https://coverartarchive.org/…",
  "mapping": [
    { "track_id": "4c9d…",
      "current":  { "title": "Autobahn", "track_number": 1, "…": "…" },
      "proposed": { "title": "Autobahn", "track_number": 1, "year": 1974, "…": "…" },
      "confidence": 1.0 }
  ]
}
```

   Entries with `confidence` below 0.6 are rendered as low-confidence by the UI. The UI
   applies the selection through `PATCH /api/metadata/tracks` and
   `POST /api/library/albums/{id}/cover/from-url` — there is no separate apply endpoint.
- Errors: `404 {"error":"album not found"}` · `500` (upstream MusicBrainz failure — the
  release fetch error is reported in the server log only).

### POST /api/metadata/rename-collisions

Dry run: for the pending edits, which title changes would rename onto a file name that is
already taken in the album. The review dialog uses it to offer **add a number** (suffix) /
**skip the rename** / **replace** (the existing file is moved to the trash) per colliding
track. Unknown tracks and no-op title changes are skipped; the reports come back in
request order.

- Request: `{ "edits": [ { "track_id": "…", "fields": { "title": "…" } } ] }`
- Response `200`:

```json
[
  { "track_id": "4c9d…", "new_title": "Autobahn", "collides": true,
    "target_path": "/mnt/media/music/Kraftwerk/Autobahn/Autobahn.flac",
    "existing_name": "Autobahn.flac" }
]
```

- Errors: `400 invalid json` · `500`.

---

## 6. Download and jobs

### GET /api/info

Fetches title/playlist information with `yt-dlp --dump-single-json --flat-playlist
--playlist-end 200 -- <url>` (the URL is passed after the `--` separator, so it can never
be parsed as an option) and re-encodes the JSON. The prod compose healthcheck probes this
route with `wget --spider`; with auth enabled the probe receives the `401` JSON answer,
which `--spider` treats as reachable.

- Query: `url` or `id` (either one; an 11-character `id` becomes a video URL, otherwise a
  playlist URL; values without `youtube.com`/`youtu.be` are treated as IDs).
- Response `200` — the yt-dlp info document (re-encoded).
- Errors: `400 {"error":"missing url or id"}` · `500 failed to fetch info` |
  `failed to parse yt-dlp output`.

### POST /api/queue

Queues one download per element (FLAC extraction, embedded thumbnail and metadata,
`<DOWNLOAD_DIR>/<artist>/<album>/<title>.<ext>`).

- Request — an array:

```json
[
  { "video_id": "dQw4…", "title": "Autobahn", "artist": "Kraftwerk",
    "album": "Autobahn", "thumbnail": "https://…" }
]
```

- Response `200`:

```json
{ "queued": 1, "job_ids": ["c0ffee…"] }
```

- Errors: `400 invalid json` · `500`.

### GET /api/jobs

Lists every job, newest first (download, scan, metadata and yt-dlp-update jobs share one
table).

- Response `200`:

```json
[
  { "id": "c0ffee…", "type": "download", "status": "completed",
    "details": "{\"video_id\":\"dQw4…\",…}", "progress": "100%", "error": "",
    "attempts": 1, "next_retry_at": null,
    "created_at": "2026-11-22T09:00:00Z", "updated_at": "2026-11-22T09:03:11Z" }
]
```

  `type`: `download` | `scan` | `metadata` | `ytdlp_update`. `status`: `queued` |
  `running` | `downloading` | `completed` | `failed`. `details` is the JSON-encoded job
  details — for `download` the queued `Details` (`video_id`, `title`, `artist`, `album`,
  `thumbnail`), for `scan` `{ "path": … }`, for `metadata` an `action` marker:
  `apply_edits`, `revert_change_set`, `embed_cover` or `resolve_conflicts` (plus
  `change_set_id` where applicable), and for `ytdlp_update` the `channel` and `reason`
  (`manual` or `auto`). `attempts` and `next_retry_at` are the automatic-retry
  bookkeeping of download jobs: a transient failure (network errors, HTTP 5xx/429) with
  retry budget left is set back to `queued` with `next_retry_at` armed
  (backoff 30 s / 2 min / 10 min, capped at 10 min; the budget is
  `DOWNLOAD_MAX_RETRIES`, default 2, `0` disables it) — permanent failures (HTTP 403/404,
  unavailable or private videos) are never retried.

### POST /api/jobs/clear

Deletes all jobs in state `completed` or `failed`.

- Response `200` (empty body). Errors: `500`.

### POST /api/jobs/{id}/retry

Manually re-queues one failed **download** job; the dispatcher runs it immediately. The
job's `attempts` counter is advanced, so a manual retry counts against the
automatic-retry budget (`DOWNLOAD_MAX_RETRIES`).

- Response `200` — the [job](#get-api-jobs) object.
- Errors: `404 {"error":"job not found"}` · `409 {"error":"job is not a failed download"}` ·
  `500`.

### POST /api/jobs/retry-failed

Re-queues every failed download job.

- Response `200`: `{ "retried": 3 }`
- Errors: `500`.

---

## 7. Config

Runtime configuration of the downloader. (The deployment configuration — environment
variables — is documented in [Deployment](DEPLOYMENT.md).)

### GET /api/config

- Response `200`: `{ "concurrent_limit": 2, "proxy": "" }`

### POST /api/config

Admin only (`403 {"error":"admin required"}` otherwise).

- Request: `{ "concurrent_limit": 4, "proxy": "http://proxy:3128" }`
- Response `200` (empty body). Errors: `400 invalid json` — previous values are kept.
- Note: values are not persisted; a restart restores the limit the
  `CONCURRENT_DOWNLOAD` environment variable resolved to at startup (the variable is read
  in `download.NewDownloader`: missing, invalid or below-1 values fall back to the
  default of 2, values above 32 are clamped to 32) and no proxy.

---

## 8. Media

### GET /media/{path…}

Serves files from `DOWNLOAD_DIR` (the music library) under the same-origin root, e.g.
`GET /media/Kraftwerk/Autobahn/cover.jpg`. Audio playback in the browser and the album
covers use this area.

- Session required (`/media/` is outside the public path class); `GET` needs no CSRF
  header. `401` without a session.
- Path names are cleaned (`filepath.Clean`) and must stay inside `DOWNLOAD_DIR`;
  `library.CoverFilePath` refuses URLs that would escape the music dir.
- Only known media files are served: the audio extensions `flac`, `mp3`, `m4a`, `aac`,
  `ogg`, `opus`, `wav` and the image extensions `jpg`, `jpeg`, `png`, `gif`, `webp`.
  Anything else — dotfiles at any level, `jellyide.db*`, the `saml/` directory, unknown
  extensions — answers `404` as if it did not exist.

---

## 9. Sync conflicts

Detection and user-chosen resolution of DB-vs-file divergence. All routes need a
session; state-changing ones also the CSRF header. The model — fingerprints, policies,
optimistic locking — is documented in [the metadata guide](METADATA.md#sync-conflicts-and-the-conflicts-page).

### GET /api/sync/conflicts

Lists the conflict rows, grouped by track.

- Query: `status` — `open` (default) | `resolved` | `dismissed`; `album_id` (optional).
- Response `200`:

```json
[
  { "track_id": "4c9d…", "track_title": "Autobahn", "album_id": "1a2b…",
    "fields": [
      { "id": "…", "conflict_id": "…", "track_id": "4c9d…", "field": "title",
        "db_value": "Autobahn [Official Audio]", "file_value": "Autobahn",
        "source": "scan", "detected_at": "2026-11-22 09:00:00", "status": "open",
        "resolved_by": null, "resolved_at": null, "resolution": null } ] }
]
```

  `source`: `scan` (changed on disk by another program), `apply` (external change found
  while an apply was running) or `concurrent` (another editor won the version lock).
- Errors: `400 {"error":"status must be open, resolved or dismissed"}` · `500`.

### GET /api/sync/summary

The sidebar badge payload.

- Response `200`: `{ "open_conflicts": 2, "missing_tracks": 1 }`

### POST /api/sync/conflicts/resolve

Applies the requested per-field resolutions through a metadata job
(`details.action: "resolve_conflicts"`), so the outcome is recorded in the change history
and revertible like any other edit. A failed field leaves its conflict open for another
attempt.

- Request:

```json
{ "resolutions": [
    { "conflict_id": "…", "choice": "db" },
    { "conflict_id": "…", "choice": "custom", "value": "Autobahn (Remaster)" } ] }
```

  - `choice`: `db` (rewrite the file tags from the DB) · `file` (update the DB from the
    file) · `custom` (write the supplied `value` to both — `value` is required).
- Response `202`: `{ "job_id": "…" }`
- Errors: `400` — `invalid resolution: resolutions must not be empty`,
  `invalid resolution: conflict_id is required`,
  `invalid resolution: invalid choice "…" (want db, file or custom)`,
  `invalid resolution: custom choice for conflict … needs a value` ·
  `404 {"error":"not found"}` · `409 {"error":"conflict is no longer open"}` · `500`.

### POST /api/sync/conflicts/resolve-all

Resolves every open conflict (optionally one album only) with one choice.

- Request: `{ "album_id": "…", "choice": "db" }` — `choice` is `db` or `file` (custom has
  no global value).
- Response `202`: `{ "job_id": "…" }`
- Errors: `400` — `invalid resolution: resolve-all choice must be db or file, got "…"` |
  `invalid resolution: no open conflicts to resolve` · `409` · `500`.

### POST /api/sync/conflicts/{id}/dismiss

Marks one conflict `dismissed` — the divergence is acknowledged and left in place.

- Response `200` — the conflict row.
- Errors: `404 {"error":"not found"}` · `409 {"error":"conflict is no longer open"}` · `500`.

### GET /api/sync/missing

Lists the tracks whose file is gone from disk (state `missing`; the row and its history
are kept). Each entry carries its relink candidates: existing files with the same size,
a duration within 1 second and the same tags hash.

- Response `200`:

```json
[
  { "track_id": "4c9d…", "album_id": "1a2b…", "title": "Autobahn",
    "artist": "Kraftwerk", "album": "Autobahn", "path": "/mnt/media/music/…",
    "duration": 372, "size": 31000000,
    "candidates": [ { "track_id": "…", "path": "…", "title": "Autobahn", "duration": 372 } ] }
]
```

### POST /api/sync/missing/{track_id}/relink

Re-points a missing track at one of its candidate files: the track id and the history
rows are kept, the duplicate row a scan created for the same file is removed, and the
candidate's fingerprint is adopted.

- Request: `{ "path": "/mnt/media/music/…/Autobahn.flac" }`
- Response `200` — the updated track object.
- Errors: `404 {"error":"not found"}` · `409` `track is not missing` |
  `path is not a relink candidate for this track` · `500`.

### DELETE /api/sync/missing/{track_id}

Removes a missing row (the file is gone; its history rows go with it).

- Response `200` (empty body).
- Errors: `404 {"error":"not found"}` · `409 {"error":"track is not missing"}` · `500`.

### GET /api/sync/trash

Lists the files displaced by "replace" rename collisions
(`<DOWNLOAD_DIR>/.jellyide/trash`), oldest first.

- Response `200`: `[ { "id": "…", "original_path": "…", "trash_path": "…",
  "track_id": "…", "created_at": "…" } ]`

### POST /api/sync/trash/{id}/restore

Moves a trashed file back to its original path and drops the entry.

- Response `200` (empty body).
- Errors: `404 {"error":"trash entry not found"}` ·
  `409 {"error":"the original file path is taken again"}` · `500`.

### GET /api/admin/settings/sync

Admin. Returns the active sync conflict policy.

- Response `200`: `{ "policy": "ask" }`

### PUT /api/admin/settings/sync

Admin. Changes the policy (the settings-table value wins over the
`SYNC_CONFLICT_POLICY` environment variable from this point on).

- Request: `{ "policy": "file_wins" }` — `ask` (default) | `file_wins` | `db_wins`.
- Response `200`: `{ "policy": "file_wins" }`
- Errors: `400` `invalid policy "…" (want ask, file_wins or db_wins)` ·
  `403 {"error":"admin access required"}`.

---

*See also:* [Architecture](ARCHITECTURE.md) · [Development](DEVELOPMENT.md) ·
[Metadata guide](METADATA.md) · [Deployment](DEPLOYMENT.md) ·
[Authentication](AUTHENTICATION.md).
