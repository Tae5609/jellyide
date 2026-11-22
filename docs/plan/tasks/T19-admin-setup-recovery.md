# T19: First-run setup wizard and admin recovery CLI

Branch `feat/admin-setup-recovery`. Read `docs/plan/CONVENTIONS.md`, `backend/main.go`, all of `backend/internal/auth/`, `Dockerfile`, the compose files, and the frontend `src/App.js`, `src/auth/`, `src/pages/Login.js`.

Follow `docs/plan/CONVENTIONS.md` (commit, PR and quality-gate rules).

Today the only way to get an admin is `ADMIN_PASSWORD` on the very first start, and the server refuses to start without it. There is no way to recover a forgotten admin password or a locked-out instance except editing SQLite by hand.

## 1. First-run setup wizard (when no users exist and `ADMIN_PASSWORD` is unset)
- Do **not** refuse to start. Start in **setup mode**:
  - Generate a one-time setup token (32 random bytes, base64url) and keep it in memory only.
  - Log it prominently to stdout, so `docker logs jelly-ide` shows it:
    ```
    ==== JellyIDE first-run setup ====
    Open <PUBLIC_URL>/setup and enter this setup token:
    <token>
    ==================================
    ```
  - Regenerate it on every start until setup completes.
- In setup mode:
  - Every `/api/*` route except `/api/setup/*` and `/api/auth/providers` returns 503 `{"error":"setup_required"}`.
  - `/api/auth/providers` gains `"setup_required": true`.
  - `/media` returns 503.
- API:

  | Method | Path | Body | Response |
  |---|---|---|---|
  | GET | `/api/setup/status` | – | `{"setup_required":bool}` |
  | POST | `/api/setup/complete` | `{"token","username","password","email"?}` | creates the first admin and logs them in. Then 201, and setup mode ends immediately with no restart. Wrong token: 403. |

  - Token compare: constant time.
  - Rate limit: 5 failures per IP per 15 min.
  - Username and password rules: the same as registration (T8).
  - Concurrency: complete exactly once, guarded inside a transaction (re-check `CountUsers()==0`). A second call returns 409.
- When `ADMIN_PASSWORD` is set, the existing env bootstrap behaviour stays. Document that the env password is applied **only** when no users exist, and is never re-applied or reset later.
- Frontend:
  - When `providers.setup_required`, every route redirects to `/setup`.
  - The `/setup` page uses the same card style as Login: setup token, username, password and confirm fields. It shows the hint "Find the setup token in the server logs: `docker logs jelly-ide`".
  - On success it navigates to `/downloads`.
  - Works at 360px. Add RTL tests.

## 2. Admin recovery CLI (same binary, subcommands)
`./server` with no args runs the server, as today. Subcommands operate directly on the DB, using `DOWNLOAD_DIR` / the DB path resolution from main, and work while the server is running (SQLite WAL with busy_timeout):

| Command | Effect |
|---|---|
| `./server admin list` | id, username, provider, is_admin, status, last_login |
| `./server admin reset-password <username>` | prompts twice with no echo (`golang.org/x/term`). `--password-stdin` reads one line for scripts. `--generate` creates a strong random password and prints it once. Local users only. Deletes the user's sessions. |
| `./server admin create <username> [--admin] [--password-stdin\|--generate]` | creates a local user |
| `./server admin promote <username>` / `demote <username>` | demote refuses the last active admin |
| `./server admin enable <username>` / `disable <username>` | enable also approves pending users. Disable refuses the last active admin and deletes sessions. |
| `./server admin unlock` | clears the in-memory login rate limiter. Document that a restart also clears it, because it is in-memory. |

- Use `flag.NewFlagSet` per subcommand (no new CLI framework), with `--help` text for each.
- Print clear errors and use non-zero exit codes.
- Never print password hashes. Only print a password when `--generate` creates one.
- Every action is logged to stdout and recorded in a new `audit_log` table (`id, at, actor, action, target, detail`). The actor is `cli` for these commands. Also record admin actions from the web admin API (T8 handlers) in it; that is a small addition in the same PR.
- Docker usage goes in the docs: `docker exec -it jelly-ide ./server admin reset-password admin`.

## 3. Docs
Update `docs/AUTHENTICATION.md` (or the README if T6b has not merged yet) with: first-run setup, env bootstrap, and recovery commands. Remove the "server refuses to start without ADMIN_PASSWORD" wording, and make `ADMIN_PASSWORD` optional in the compose files (prod: change `${ADMIN_PASSWORD:?…}` to `${ADMIN_PASSWORD:-}`, with a comment explaining the setup wizard).

## Tests
- Setup mode gating (503 everywhere else).
- Token: wrong is 403, right is 201, and a second call is 409.
- Concurrent complete: exactly one wins.
- The setup rate limit.
- The env bootstrap still works.
- Each CLI subcommand, through a `run(args, stdin, stdout) int` function tested against a temp DB: reset revokes sessions, last-admin guards, `--generate` output, `--password-stdin`.
- Audit rows written.
- Frontend: the redirect to `/setup`, and form validation.

## Verify
All gates from CONVENTIONS.md, plus `./scripts/smoke-auth.sh` if present (update it for optional `ADMIN_PASSWORD`). Commits, for example `feat(auth): add first-run setup wizard with log-printed token`, `feat(auth): add admin recovery cli`, `feat(auth): add audit log for admin actions`.
