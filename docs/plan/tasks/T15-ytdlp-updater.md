# T15: yt-dlp version management (admin UI and auto-update)

Branch `feat/ytdlp-updater`. Read `docs/plan/CONVENTIONS.md`, `backend/internal/download/`, `backend/internal/auth/admin.go` (`RequireAdmin`, `settings` table helpers), `backend/main.go`, `Dockerfile`, and `frontend/src/pages/Settings.js`.

Follow `docs/plan/CONVENTIONS.md` (commit, PR and quality-gate rules).

YouTube breaks old yt-dlp versions often. Today yt-dlp is `pip install`ed into the image at build time, so it can only be updated by rebuilding the image.

## Design
- **Managed binary.** JellyIDE keeps its own copy of the official platform-independent `yt-dlp` zipapp (runs with `python3`).
  - Location: `<DOWNLOAD_DIR>/.jellyide/bin/yt-dlp`. This is on the mounted volume, so updates survive container restarts.
  - Env `YTDLP_MANAGED` (default `true`). When false, or before the first install succeeds, fall back to `yt-dlp` on PATH.
  - The downloader resolves the binary path through a `Resolver` on every job, so an update takes effect without a restart.
- **Download source** (fixed allowlist, https only):
  - stable: `https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp` with `SHA2-256SUMS` from the same release.
  - nightly: `https://github.com/yt-dlp/yt-dlp-nightly-builds/releases/latest/download/…`.
  - Version check: the GitHub API `https://api.github.com/repos/yt-dlp/<repo>/releases/latest` (`tag_name`).
  - Follow redirects only to `github.com`, `objects.githubusercontent.com` and `release-assets.githubusercontent.com`.
- **Safe install:**
  1. Download to a temp file in the same dir and verify the SHA-256 against `SHA2-256SUMS`, aborting on mismatch.
  2. `chmod 0755`, then run `<temp> --version` (10s timeout) and check that it prints a version.
  3. Wait until no download is running: pause the dispatcher from starting new jobs, wait at most 10 minutes for running ones, then resume.
  4. Swap atomically with `os.Rename`. Keep the previous binary as `yt-dlp.prev` for rollback.
- **Settings** are stored in the `settings` table:
  - `ytdlp_auto_update` (bool, default `true`)
  - `ytdlp_update_interval_hours` (6–168, default 24)
  - `ytdlp_channel` (`stable` or `nightly`, default `stable`)
  - `ytdlp_last_check`, `ytdlp_last_result`
- **Auto-update loop:** at startup, if managed and no managed binary exists, install it. Then every interval, if auto-update is on and a newer version exists, update. Log the result and store `last_result`. A failure never breaks downloads, because the old binary stays.

## API (all `RequireAdmin`)
| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/admin/ytdlp` | – | `{"version","path","managed":bool,"channel","latest_version"\|null,"update_available":bool,"last_check","last_result","auto_update":bool,"interval_hours","updating":bool,"has_rollback":bool}` |
| POST | `/api/admin/ytdlp/check` | – | refreshes the latest version and returns the same shape |
| POST | `/api/admin/ytdlp/update` | – | 202 `{"job_id"}`. Runs as job type `ytdlp_update` (visible on the Jobs page). 409 if an update is already running |
| POST | `/api/admin/ytdlp/rollback` | – | swaps `yt-dlp.prev` back. 409 if none |
| PUT | `/api/admin/ytdlp/settings` | `{"auto_update","interval_hours","channel"}` | validated, 400 on bad values. Returns the GET shape |

Also make the existing `POST /api/config` (concurrency, proxy) **admin-only**, because any user can currently change the proxy for everyone. `GET /api/config` stays available to all users. Add this to the PR's Risks section as a behaviour change.

## Frontend
- In Settings, an admin-only **yt-dlp** card showing:
  - the current version, channel and binary path (managed / system)
  - the latest version, with an "Update available" badge
  - the last check time and last result
  - buttons: **Check for updates**, **Update now** (disabled when up to date unless "force"; shows the job progress until done), and **Roll back** (with confirm modal, only when `has_rollback`)
  - an auto-update toggle, interval select (6h / 12h / 24h / 3d / 7d) and channel select (stable / nightly, with a warning text for nightly)
- Non-admins do not see the card or the concurrency/proxy save controls (show read-only values).
- Works at 360px. Use `ModalContext`, never `alert()`.

## Other changes
- **Dockerfile:** keep the pip yt-dlp as the fallback, and make sure `python3` is available for the zipapp (it already is).
- **Scanner:** skip the `.jellyide` directory.
- **`/media`:** must not serve `.jellyide/`. Verify that T12's dotfile rule covers it, and add a test.

## Tests
- Checksum verification: pass, mismatch, and missing entry.
- Redirect allowlist.
- Version parsing.
- Atomic swap and rollback with temp dirs.
- The "wait for running downloads" gate, with fake downloader state.
- Settings validation.
- Admin-only enforcement: 403 for non-admins, including `POST /api/config`.
- The auto-update loop with an injected clock and a fake release server (`httptest`).
- No real network in tests.
- Frontend RTL: the card renders for admins only, the update flow polls the job, and the rollback confirm works.

## Verify
All gates from CONVENTIONS.md. Commits, for example: `feat(download): add managed yt-dlp binary with checksum-verified updates`, `feat(download): add yt-dlp auto-update loop`, `feat(ui): add yt-dlp admin card`, `fix(api): restrict config changes to admins`.
