# JellyIDE

*"Download, clean, organize and manage your music library."*

JellyIDE is a self-hosted web app for building and keeping a pristine, locally hosted music
library — the one tool from download to perfectly tagged files. What started as a "YouTube
scraper for Jellyfin" grew into a full IDE for your collection: downloads, a SQLite-backed
library browser, a spreadsheet-style **metadata editor**, and a background job dashboard —
behind one Go server and a React SPA, in a single Docker container.

It complements media servers like Jellyfin, Navidrome and Plex: point them at the same
music directory JellyIDE manages.

## Table of contents

- [Features](#features)
- [Screenshots](#screenshots)
- [Quick start (Docker Compose)](#quick-start-docker-compose)
- [Documentation](#documentation)
- [Tech stack](#tech-stack)
- [License](#license)

## Features

**Downloads**

- Queue music from YouTube URLs or whole playlists (`/api/info` + `/api/queue`).
- FLAC extraction with embedded thumbnail and metadata via `yt-dlp` (URLs are always passed
  after a `--` separator — no option injection).
- Concurrent downloads with a runtime-adjustable limit, optional proxy for region-locked
  content; unfinished jobs are re-queued after a crash.
- Finished files land in `<DOWNLOAD_DIR>/<artist>/<album>/<title>.<ext>` and are indexed
  automatically.

**Library**

- Persistent SQLite index (WAL mode, split read/write pools) of artists, albums and tracks
  with duration, bitrate and full tag fields.
- Background scanner walks the music dir and probes every `.flac` / `.mp3` / `.m4a` /
  `.aac` file with `ffprobe`; cover art (`cover.jpg`, `folder.jpg`, …) is picked up.
- Hierarchical browse (artist → album → tracks), album and track deletion, album rename
  that renames the directory and rewrites the tags, audio streaming through `/media/…`.

**Metadata IDE** — the flagship, see [docs/METADATA.md](docs/METADATA.md)

- Editable track grid with dirty-cell highlight, pending edits, undo/redo and a
  review-&-apply diff; bulk edit, fill-down, auto-numbering, column toggles.
- Tag fields per track: title, album, artist, album artist, track/disc number, year,
  genre, composer, comment — validated (`year` 0…9999, numbers 0…999), written into the
  real files via a Python helper using mutagen (FLAC, MP3/ID3, Ogg, M4A), with file renames
  kept in sync.
- Smart analyzer: finds noisy shared patterns (`[Official Audio]` …) across an album for
  one-click cleanup; find/replace with literal, prefix, suffix and regular-expression
  operations and a dry-run preview.
- Cover art: upload (jpeg/png, ≤10 MiB, ≤4000 px), extract from file, or fetch from an
  allowlisted URL; an embed job writes the art into every track of the album.
- **MusicBrainz lookup**: search a release, review a track-mapping table with confidence
  scores, apply the proposals as pending edits.
- Full **history**: every applied change set is recorded and can be **reverted** (a revert
  is itself revertible — even file renames).
- Keyboard shortcuts for everything (`Ctrl+S`, `Ctrl+Z`, `/`, `L`, `H`, `?`, `g d`, …).

**Authentication & users**

- Local username/password (bcrypt, timing-safe), OIDC (authorization code flow with PKCE,
  state and nonce) and SAML 2.0 service provider — per-instance switcheable.
- Registration modes: `admin_only` (default), `open`, `approval` with pending accounts;
  admin user management (create, patch, disable, delete, password reset) with a
  last-active-admin invariant.
- `HttpOnly; SameSite=Lax` session cookies (sha256-hashed tokens, 7-day sliding expiry),
  CSRF header check, login rate limiting, and an admin bootstrap via `ADMIN_PASSWORD`.

**Jobs** — one dashboard for downloads, scans and metadata operations, with per-job type,
status, progress and error.

**Mobile-friendly UI** — responsive down to 360 px: card list instead of the grid,
bottom-sheet editors, sticky action bar.

## Screenshots

> _Placeholders — capture the four pages and save the images under `docs/images/`
> (`downloads.png`, `library.png`, `metadata.png`, `jobs.png`), then link them here._

## Quick start (Docker Compose)

Requirements: Docker and Docker Compose v2 (`docker compose`, plugin form). The image
bundles `ffmpeg`, `python3`+`mutagen` and `yt-dlp`.

```bash
git clone https://github.com/Tae5609/jellyide && cd jellyide

# Point the music mount at your library
export MUSIC_DIR=/mnt/media/music

docker compose up -d --build
docker logs jelly-ide   # shows the one-time setup token
```

Open `http://localhost:8080/setup`, paste the setup token from the logs, and create your
admin account. Then start the scan from the Library page. (For unattended installs, set
`ADMIN_PASSWORD` (and optionally `ADMIN_USERNAME`) before the first start instead; it is
only used while no users exist and is never re-applied.) The database lives in the music dir as `jellyide.db`, so your collection
survives a rebuilt container.

The Compose files forward `MUSIC_DIR` and the optional `ADMIN_PASSWORD` (plus the rest of the auth
variables) to the container — the complete, authoritative env table, the production stack
and the reverse-proxy recipes are in [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md).

For the prebuilt-image production stack (`docker-compose.prod.yml`, `.env`, healthcheck,
HTTPS/reverse-proxy, backups, the full env-var table and the OIDC/SAML provider setup) see
[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) and
[docs/AUTHENTICATION.md](docs/AUTHENTICATION.md).

### Admin recovery

Forgot the admin password or locked every admin out? Use the CLI built into the server
binary. It works while the server is running:

```bash
docker exec -it jelly-ide ./server admin reset-password admin             # prompts, no echo
docker exec -it jelly-ide ./server admin reset-password admin --generate  # prints a random password once
docker exec -it jelly-ide ./server admin list
docker exec -it jelly-ide ./server admin create alice --admin
docker exec -it jelly-ide ./server admin promote|demote|enable|disable <user>
docker exec -it jelly-ide ./server admin unlock                           # clear login rate limit
```

A password reset signs that user out everywhere. The last active admin cannot be demoted
or disabled, and every action is written to the `audit_log` table.

## Documentation

| Document | About |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | components, request lifecycle, data model (ERD), job system, design decisions |
| [docs/API.md](docs/API.md) | every HTTP endpoint: method, path, auth, request, response, error codes |
| [docs/METADATA.md](docs/METADATA.md) | user guide for the metadata IDE incl. the keyboard shortcut table |
| [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) | prerequisites, local run, Makefile targets, quality gates, tests, layout, agent workflow |
| [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) | compose files, env-var table, volumes, reverse proxy, upgrades, backup, troubleshooting |
| [docs/AUTHENTICATION.md](docs/AUTHENTICATION.md) | admin bootstrap, local accounts, registration modes, OIDC + SAML setup, security model |
| [docs/dev/keycloak/README.md](docs/dev/keycloak/README.md) | step-by-step Keycloak dev realm for OIDC and SAML testing |
| [CONTRIBUTING.md](CONTRIBUTING.md) | commit and PR conventions |
| [docs/plan/CONVENTIONS.md](docs/plan/CONVENTIONS.md) | the binding engineering conventions |

## Tech stack

| Layer | Technology |
|---|---|
| Backend | Go 1.25, `gorilla/mux`, `sqlx` + pure-Go SQLite (`modernc.org/sqlite`), `crypto/bcrypt`, `coreos/go-oidc`, `crewjam/saml` |
| Frontend | React 18 + react-router 7 (create-react-app / react-scripts), hand-rolled dark CSS, plain `fetch` |
| Media tooling | `yt-dlp` (downloads/info), `ffmpeg/ffprobe` (probing), Python 3 + `mutagen` (tag writing) |
| Storage | single SQLite file (WAL) + the filesystem music tree |
| Auth | local bcrypt, OIDC (code+PKCE+state+nonce), SAML 2.0 SP, opaque hashed session cookies |
| Delivery | one Docker image (alpine), Docker Compose dev + prod files, GitHub Actions CI |

## License

This project has no LICENSE file yet; until one is added, all rights are reserved.
