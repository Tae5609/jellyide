# T12: Backend bug hunt and security hardening

Branch `fix/backend-hardening`. Read `docs/plan/CONVENTIONS.md`, `docs/plan/BUGS.md`, and all of `backend/`.

## Do
1. Fix **every unchecked backend item in `docs/plan/BUGS.md`**. For each fix: add a regression test that fails before the fix, then tick the item (`- [x] … — fixed in <commit>`).
2. Then audit the whole backend and fix what you find:
   - Path traversal anywhere a DB value or request value becomes a filesystem path (rename, delete, cover, scanner, `/media`). Resolve with `filepath.Clean` + `filepath.Rel` against the music dir, and reject anything that escapes it.
   - Command injection: every `exec.Command` call. Use `--` separators and no shell.
   - `/media/` must not serve `jellyide.db*`, dotfiles, or the `saml/` key dir. Serve only audio and image extensions.
   - Goroutine leaks and races (`go test -race`), job manager concurrency, download cancellation.
   - HTTP server hardening: replace `http.ListenAndServe` with an `http.Server` that sets `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout` (the media streaming route needs none or a long one) and `IdleTimeout`, plus graceful shutdown on SIGTERM.
   - Request body limits (`http.MaxBytesReader`) on JSON endpoints: 1 MB, and the cover upload limit from T10.
   - Security headers middleware: `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, `X-Frame-Options: DENY`, and a basic CSP compatible with the CRA build (`default-src 'self'; img-src 'self' data: https://coverartarchive.org https://*.archive.org; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src https://fonts.gstatic.com`). Test the app still loads.
   - Consistent JSON error responses and correct status codes.
3. Log any frontend bugs you notice in BUGS.md. Do not fix them here.

## Verify
All gates from CONVENTIONS.md, plus `./scripts/smoke-auth.sh` if it exists. Use one `fix(<scope>): …` commit per bug, so the PR body lists each bug, its impact, and its test.
