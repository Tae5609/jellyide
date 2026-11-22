# T14: Retry failed downloads (manual and automatic)

Branch `feat/download-retry`. Read `docs/plan/CONVENTIONS.md`, `backend/internal/{download,jobs,api}`, `backend/internal/db/db.go`, and `frontend/src/pages/{Jobs,Downloads}.js`.

Follow `docs/plan/CONVENTIONS.md` (commit, PR and quality-gate rules).

Today a failed download stays failed. The user has to find the video and queue it again by hand.

## Do
### Backend
1. Migration: add guarded `ALTER TABLE jobs ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0` and `ADD COLUMN next_retry_at DATETIME`. Expose `attempts` in the job JSON.
2. `jobs.Manager.RetryJob(id string) (*Job, error)`. Only jobs with `status == failed` and a retryable type (`download`) qualify. It resets status to `queued`, clears progress and error, and keeps `details`. Errors: `ErrJobNotFound` and `ErrNotRetryable`.
3. Downloader: `Retry(id)` calls `RetryJob`, then wakes the dispatcher so the job runs without waiting for a poll tick.
4. API (authenticated users, the same permission as queueing):

   | Method | Path | Response |
   |---|---|---|
   | POST | `/api/jobs/{id}/retry` | 200 with the job · 404 unknown · 409 `{"error":"job is not a failed download"}` |
   | POST | `/api/jobs/retry-failed` | 200 `{"retried": n}` (retries every failed download job) |
5. **Automatic retry** for transient failures:
   - Env `DOWNLOAD_MAX_RETRIES`, default 2 (0 disables).
   - Classify yt-dlp stderr in a pure function `isTransient(stderr string) bool`. Transient: `HTTP Error 5\d\d`, `HTTP Error 429`, `timed out`, `Connection reset`, `Temporary failure in name resolution`, `Unable to download webpage`, `IncompleteRead`. Permanent: `Video unavailable`, `Private video`, `members-only`, `copyright`, `HTTP Error 404`/`403`, and anything else.
   - On a transient failure with `attempts < max`: increment attempts, set `next_retry_at = now + backoff` (30s, 2m, 10m, capped), status `queued`, error "retrying (n/max): <reason>". The dispatcher skips queued jobs whose `next_retry_at` is in the future.
   - Manual retry resets `next_retry_at`, but still increments `attempts`.
6. Tests: `isTransient` table test, RetryJob state rules, the API status codes (404/409/200), retry-failed count, backoff scheduling (inject a clock), and that the dispatcher skips future `next_retry_at`.

### Frontend
1. Jobs page:
   - Failed download jobs show the error message (truncated, with a "details" expander) and a **Retry** button (spinner while the request runs, then refresh).
   - The header gets **Retry all failed (n)** when n > 0.
   - Show "attempt n" when attempts > 0, and a "retrying in …" badge for queued jobs whose `next_retry_at` is in the future.
2. Downloads page: if it lists recent job results, failed items get the same Retry button.
3. Use `apiFetch` and `ModalContext` for error messages, never `alert()`. Works at 360px, and buttons are at least 44px tall on mobile.
4. Tests (RTL): Retry button calls the endpoint and refreshes, retry-all is hidden when there are no failures, and the error expander works.

## Do NOT
- Retry non-download job types.
- Change the download argument building (`buildArgs`).

## Verify
All gates from CONVENTIONS.md. Commits, for example: `feat(download): add manual and automatic retry for failed downloads`, `feat(ui): add retry buttons to jobs page`.
