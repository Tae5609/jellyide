# T12b: backend bug sweep (leftovers from T12 scope split)

Source: T12-review-r2 (APPROVE) NOTES — per inbox P1, T12 ships with its brief items done; these two leftovers move here. Branch from main **after T12 (PR #34) merges** — both items build on T12's code.

## Items

### 1. Cap the first-run setup body (security, same class T12 closed)
`backend/internal/auth/setup.go:61` — `handleSetupComplete` (public, unauthenticated, only reachable in first-run mode) decodes its JSON body with plain `json.NewDecoder` — no 1 MiB `MaxBytesReader` cap. T12 put every other JSON endpoint in the auth+api packages behind its `decodeJSONBody` helper (1 MiB, 413 on overflow). Fix: route `handleSetupComplete` (and any other T19 setup/admin_cli decode site in that file) through the same helper. Tests: oversized setup body → 413 and no admin created; valid body still works.

### 2. Login-CSRF double-submit for register in open-registration mode (MINOR, harden)
`/api/auth/register` is public and, in open-registration mode (`AUTH_LOCAL_ENABLED` + non-admin_only), creates an account AND starts a session immediately with no double-submit check — a cross-site form POST can create an account and pin the victim's browser. T12's login path has the `GET /api/auth/csrf` + `verifyLoginCSRF` machinery; extend it to `handleRegister` (same 403 contract: token verified before any account work). In admin-only mode the 403 answer already comes before any state change — keep that precedence. Frontend: `Register` page must fetch the CSRF token like `AuthContext` does for login (check the house pattern). Tests: cross-site register POST without token → 403 + zero users created; with token pair → 201/200 as before; admin-only mode unchanged.

## Scope rules
- ONLY the two items above (+ their tests + BUGS.md entries). No other refactors.
- If either item turns out to be already handled on main (T12 or a later merge), report it and tick/annotate the BUGS.md line instead of coding.
- BUGS.md: add the two entries (or tick the existing ones if present — check first; they may exist as reviewer notes) in their fix commits.

## Gates
All from CONVENTIONS.md: gofmt, go vet, golangci-lint (0 issues, must appear in report), `go test -race -count=1 ./...`, CI=true npm test, CI=true npm run build. If `frontend/package.json`/lock is touched: verify with `npx -y npm@10 ci --dry-run` (CI runs node 20 / npm 10).

## Commits
Split per item (`fix(auth): ...`), Conventional ≤72 chars. No push.

## Report format
```
VERDICT: DONE
GATES: <each + result, incl. golangci-lint>
ITEM 1: <what changed + test names>
ITEM 2: <what changed + test names>
COMMIT HYGIENE: pass/fail + notes
NOTES: <anything the reviewer/rebase must know>
```
