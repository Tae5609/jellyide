# T18: Read the CONCURRENT_DOWNLOAD env var (follow-up)

Branch `fix/concurrent-download-env`. Read `docs/plan/CONVENTIONS.md`, the BUGS.md entry (line ~33, "CONCURRENT_DOWNLOAD … never read"), `backend/internal/download/downloader.go`, `backend/main.go`, `Dockerfile`, both compose files, `.env.example`.

Follow `docs/plan/CONVENTIONS.md` (commit, PR and quality-gate rules).

## Bug
`Dockerfile:24`, both compose files and `.env.example` export `CONCURRENT_DOWNLOAD=2`, but no Go code reads it. `NewDownloader` (`downloader.go:49`) hard-codes `concurrentLimit: 2`, so the setting is silently ignored (operators think they changed the concurrency; only `POST /api/config` works at runtime).

## Do
1. In `NewDownloader`, read the `CONCURRENT_DOWNLOAD` env var as the **initial** limit (runtime `POST /api/config` still overrides afterwards, unchanged):
   - parse as base-10 integer; a value that is missing, unparseable, or `< 1` falls back to the existing default `2` and logs a warning (use the package's existing logging style; no new logger).
   - cap at a sane maximum of `32` (log a warning if clamped) to keep a typo like `2000` from spawning a worker storm.
   - document the effective precedence in the function comment: env var → default 2 → runtime `SetLimit`.
2. Do NOT change `SetLimit`, the dispatcher loop, or any API. Do NOT touch the compose files or `.env.example` (they are already correct — the bug is the missing reader).
3. Tests (in `downloader_test.go` or a new file):
   - `CONCURRENT_DOWNLOAD=5` → `GetLimit()` returns 5 after `NewDownloader` (t.Setenv).
   - empty / `abc` / `0` / `-3` → limit 2 (default) and no panic.
   - `CONCURRENT_DOWNLOAD=999` → clamped to 32.
   - a regression test that fails before the fix (the `=5` case fails on the old code).
4. Tick the BUGS.md entry (`- [x] … — fixed in T18`).

## Verify
All gates from CONVENTIONS.md (Go: gofmt, go vet, golangci-lint, `go test -race ./...`; nothing else is touched).

Commits, for example: `fix(download): read CONCURRENT_DOWNLOAD env var as initial limit`.
