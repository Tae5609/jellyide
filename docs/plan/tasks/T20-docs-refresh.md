# Docs refresh — final pass after all roadmap tasks merge

Runs LAST (after T6a, T6b, T9, T11, T12, T12b, T14, T15, T16, T17, T19 are all merged). One branch, one pass: verify the docs describe the final code, fix drift in place.

## Scope
Re-read each doc against the merged code and fix every mismatch. Known surfaces to check (not exhaustive — follow the code):

0. **KNOWN STALE SPOTS (found by T6b-fix-r1, 2026-11-23):** `docs/API.md:731-734` ("values are not persisted… hard-coded in download.NewDownloader… CONCURRENT_DOWNLOAD is never read by the server code") and `docs/DEVELOPMENT.md:204-206` ("the server code never reads it") are both wrong post-T18 (#31: env read in NewDownloader, default 2, clamp 32, runtime SetLimit). Fix both; the corrected .env.example/DEPLOYMENT.md wording from T6b is the reference.

1. **Env vars** (docs/DEPLOYMENT.md env table + .env.example + README quick start): every var the code reads, with final defaults and semantics:
   - `DOWNLOAD_MAX_RETRIES` (T14: default 2, 0 disables auto-retry).
   - `CONCURRENT_DOWNLOAD` (T18: read at startup, fallback 2, clamp 32).
   - `YTDLP_MANAGED` + the updater env/behaviour (T15: managed binary, auto-update, YTDLP channel/source vars as the code has them).
   - `SYNC_CONFLICT_POLICY` (T16: default `ask`, values).
   - `ADMIN_PASSWORD` optional semantics (T19 — already aligned by T6b, re-verify).
   - `TRUST_PROXY` (T12: XFF first-hop limiter keying), `AUTH_*` vars, `PUBLIC_URL`, `DOWNLOAD_DIR`, music dir.
   - Anything else in `grep -rn 'os.Getenv\|LookupEnv' backend/` not in the table.
2. **Endpoints** (docs/API.md or wherever endpoints live): the retry endpoints (`POST /api/jobs/{id}/retry`, `POST /api/jobs/retry-failed`), the yt-dlp admin endpoints (`/api/admin/ytdlp*` incl. update/status/rollback + 202/409 contract), the sync endpoints (T16: the 9+ routes), the setup endpoints (T19), the CSRF token endpoint (`GET /api/auth/csrf` — login now requires it), register behaviour in open vs admin-only modes post-T12b.
3. **Keyboard shortcuts / metadata editor** (whichever doc covers the editor, likely docs/METADATA.md or UI section of a core doc): T11's final shortcut set (arrows, Enter, Ctrl+D fill-down, Tab behaviour, select-all) and T16's additions (expected_version conflict dialog, Conflicts page, collision select suffix/skip/replace).
4. **Sync conflict model** (docs/METADATA.md — the queue mandates it): fingerprint columns (tags_hash normalization, version bump, synced_at), the 3 policies + default, `sync_conflicts` states (ok/missing/conflict), trash + 30-day purge, optimistic locking semantics. Write it from the merged code, not from the brief.
5. **Security/ops notes**: the SSRF product decision ("do not expose to untrusted users" — the deployment docs must carry this note per the T12 decision), the accepted /media symlink + SPA-fallthrough + ReadTimeout stances, the 1 MiB JSON body cap / 413, the new server timeouts, the login-CSRF requirement for API clients.
6. **Links**: every relative link in docs/ resolves (AUTHENTICATION.md → API.md only resolves once T6a/T6b are both on main — verify at this point), README quick start matches the final first-run flow.
7. **docs/plan/** is OUT OF SCOPE (orchestrator-owned; never edit it here).

## Rules
- Fix in place; no rewrites of correct sections. Match existing doc voice.
- Every factual claim in a changed section must be verifiable in the merged code (cite file:line in your report, not in the docs).
- Gates: ruff (if any .py doc tooling touched — none expected), `CI=true npm run build` if any frontend-adjacent doc includes live code samples that are test-gated (none expected); the main gate is the link check + a `git diff` you can defend line by line.
- Commits: split by doc (`docs(deploy): ...`, `docs(api): ...`, `docs(metadata): ...`), Conventional ≤72, no push.

## Report format
```
VERDICT: DONE
DRIFT FIXED: <per doc: what was wrong, what the code says, file:line evidence>
LINKS: <all-resolve yes/no + list>
COMMIT HYGIENE: pass/fail + notes
NOTES: <anything still inaccurate that needs a product decision>
```
