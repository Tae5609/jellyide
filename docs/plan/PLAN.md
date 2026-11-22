# Roadmap: auth, users, flexible metadata, mobile, quality and docs

Scope, as set by the owner on 2026-11-21:
1. Authentication: local username/password, OIDC, SAML.
2. Registration modes: `admin_only` (default), `open`, `approval`. Admins manage users.
3. Mobile-friendly UI.
4. Flexible metadata editing: per-track tag fields, bulk edit, cover art, MusicBrainz lookup, history/undo, keyboard shortcuts.
5. Fix known bugs (`docs/plan/BUGS.md`) and harden security.
6. Comprehensive quality: unit tests, golangci-lint, ESLint, Prettier, ruff, CI gates.
7. Clear project documentation: architecture, deployment, auth, metadata guide, API, development.
8. Download reliability: retry failed downloads (manual button and automatic for transient errors), plus admin-managed yt-dlp updates (check, update, rollback, auto-update).
9. Sync safety: never overwrite files on rename (T17, urgent); detect DB/file/concurrent-user conflicts and let the user choose the resolution per field (T16, default policy `ask`).
10. Admin onboarding and recovery: first-run setup wizard (token printed to logs) when `ADMIN_PASSWORD` is unset, plus a `./server admin …` recovery CLI and an audit log (T19).
11. Conventional Commits and consistent PRs (`docs/plan/CONVENTIONS.md`, `.github/pull_request_template.md`).

## Current state (2026-11-21)
- Backend: Go 1.25, gorilla/mux, sqlx + modernc sqlite (`backend/internal/db/db.go`, split read/write pools, `migrate()` builds the schema).
- No auth at all. Every `/api/*` and `/media/*` route is open. CORS is `AllowedOrigins: *`.
- Frontend: CRA React 18, react-router v7, one hand-written `index.css`, raw `fetch()` calls in each page with `const API = ...`.
- Mobile: viewport meta exists, but there are zero `@media` rules. Layout is a fixed 260px sidebar plus `height: 100vh; overflow: hidden`. Pages use about 100 inline `style={{}}` blocks, which media queries cannot override.

## Target design

### Backend: `backend/internal/auth/`
| File | Purpose |
|---|---|
| `config.go` | Read env into `auth.Config` |
| `store.go` | Users and sessions tables, CRUD |
| `password.go` | bcrypt hash and verify (`golang.org/x/crypto/bcrypt`, cost 12) |
| `session.go` | Opaque random 32-byte token in an `HttpOnly; SameSite=Lax; Secure (when COOKIE_SECURE=true)` cookie named `jellyide_session`. Only the SHA-256 of the token goes in the DB. Expiry is 7 days, sliding. |
| `middleware.go` | `RequireAuth(next)`: on failure returns 401 JSON for `/api`, and 302 to `/login` is not used, because the SPA handles it |
| `local.go` | `POST /api/auth/login`, `POST /api/auth/logout`, `GET /api/auth/me`, `POST /api/auth/password` |
| `providers.go` | `GET /api/auth/providers` returns `{local:bool, oidc:{enabled,name}, saml:{enabled,name}}` |
| `oidc.go` | `github.com/coreos/go-oidc/v3` + `golang.org/x/oauth2`, using auth code + PKCE + state + nonce (stored in a short-lived signed cookie) |
| `saml.go` | `github.com/crewjam/saml` (`samlsp`): SP metadata, ACS, SP-initiated login |

DB schema (added to `migrate()`):
```sql
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  username TEXT UNIQUE NOT NULL,
  password_hash TEXT,              -- NULL for SSO-only users
  provider TEXT NOT NULL DEFAULT 'local',  -- local | oidc | saml
  subject TEXT,                    -- IdP subject / NameID
  email TEXT, display_name TEXT,
  is_admin INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  last_login DATETIME,
  UNIQUE(provider, subject)
);
CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at DATETIME NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

Env config:
```
AUTH_ENABLED=true                 # false = old open behaviour (dev only)
AUTH_LOCAL_ENABLED=true
ADMIN_USERNAME=admin              # bootstrap admin on first start if users table empty
ADMIN_PASSWORD=                   # required when bootstrap is needed; log a warning and refuse to start if empty
COOKIE_SECURE=false
PUBLIC_URL=http://localhost:8080  # used to build OIDC/SAML callback URLs

OIDC_ENABLED=false
OIDC_NAME=SSO
OIDC_ISSUER=  OIDC_CLIENT_ID=  OIDC_CLIENT_SECRET=
OIDC_SCOPES=openid,profile,email
OIDC_ADMIN_GROUP=                 # optional: groups claim value that grants admin
OIDC_AUTO_CREATE=true             # JIT provisioning

SAML_ENABLED=false
SAML_NAME=SAML
SAML_IDP_METADATA_URL=
SAML_SP_CERT=/data/saml/sp.crt  SAML_SP_KEY=/data/saml/sp.key
SAML_AUTO_CREATE=true
```

Routes:
- Public: `/api/auth/providers`, `/api/auth/login`, `/api/auth/oidc/login`, `/api/auth/oidc/callback`, `/api/auth/saml/metadata`, `/api/auth/saml/login`, `/api/auth/saml/acs`, plus the static SPA.
- Protected: every other `/api/*` and `/media/*`.
- CORS: drop `*`. The SPA is same-origin, so remove the cors middleware, or restrict it to `PUBLIC_URL`.
- CSRF: SameSite=Lax cookies, plus a check that state-changing `/api` requests carry `Content-Type: application/json` or the `X-Requested-With` header. The SAML ACS route is exempt.

### Frontend
- `src/api.js`: `apiFetch(path, opts)` wrapper (`credentials: 'same-origin'`, adds `X-Requested-With`). On a 401 it dispatches an `auth:logout` event. All pages switch to it.
- `src/auth/AuthContext.js`: loads `/api/auth/me` and exposes `{user, login, logout, providers}`.
- `src/pages/Login.js`: username and password form, plus SSO buttons that are full-page links to `/api/auth/oidc/login` and `/api/auth/saml/login`. It shows an `?error=` message.
- `RequireAuth` route wrapper in `App.js`. The sidebar footer shows the user and a Logout button.
- Settings gets a "Change password" card for local users.

### Mobile UI
- Breakpoints: `<=768px` (phone) and `769–1024px` (tablet).
- Phones get a top app bar with a hamburger that opens the sidebar as an off-canvas drawer with a backdrop. Tablets get a collapsed icon rail.
- Replace layout-relevant inline styles with CSS classes so media queries can apply. Cosmetic inline styles can stay.
- Tables (Jobs, Metadata track list, Downloads playlist) become stacked cards on phones or get `overflow-x:auto` wrappers.
- Metadata IDE: stack the side-by-side panels vertically. Bulk-op toolbar wraps.
- Modals: full-width on phones, scroll inside.
- Touch targets ≥ 44px, inputs `font-size:16px` on phones (stops iOS zoom), `100dvh` instead of `100vh`.

## Execution: self-orchestrating opencode agents on Orion (`orion/Qwen 3.8-27B`)

Agents are defined in `scripts/agents/opencode-agents/` and installed globally with `scripts/agents/install.sh`. They are global so that an old branch's files can never shadow them inside a worktree:
- `orchestrator` (primary, lane A): the product owner. It schedules tasks by dependency, dispatches background workers, and handles git and GitHub (push, PR, CI, squash-merge). It keeps `docs/plan/STATUS.md` current.
- Workers: `jelly-dev` / `jelly-reviewer` (lane A, Orion key A, 27B), `jelly-dev-b` / `jelly-reviewer-b` (lane B, Orion key B, 27B), and `jelly-fast-b` (lane B, Flash-Next, for docs and mechanical fixes).
- Dispatch is non-blocking through `scripts/agents/dispatch.sh`, `wait.sh` and `status.sh`. Each worker is its own `opencode run` process (logs in `docs/plan/logs/jobs/`).

Concurrency: 6 Orion requests in total. Lane A has the orchestrator plus 2 workers, and lane B has 3 workers.
Launch: `opencode run --pure --agent orchestrator --auto --dir /home/tae5609/jellyide "Resume the roadmap"`.

Waves (dependency order; each task has a brief in `docs/plan/tasks/`):
| Wave | Task | Branch | Depends on |
|---|---|---|---|
| 1 | T1 backend auth core | `feat/auth-core` | – |
| 1 | T2 mobile responsive UI | `feat/mobile-ui` | – |
| 1 | T7 backend quality tooling + unit tests | `test/backend-quality` | merges after T1 |
| 2 | T3 frontend auth | `feat/auth-frontend` | T1, T2 |
| 2 | T4 OIDC | `feat/auth-oidc` | T1 |
| 2 | T5 SAML | `feat/auth-saml` | T1 |
| 3 | T8 registration modes + admin user management | `feat/users-registration` | T3–T5 |
| 3 | T10 metadata backend (fields, cover, history, MusicBrainz) | `feat/metadata-fields` | T7 |
| 4 | T11 metadata editor UI + shortcuts | `feat/metadata-editor-ui` | T10 |
| 4 | T12 backend bug hunt + hardening | `fix/backend-hardening` | T10 |
| 4 | T14 download retry | `feat/download-retry` | T12 |
| 4 | T15 yt-dlp updater | `feat/ytdlp-updater` | T12, T13 |
| 4 | T17 no-overwrite rename (URGENT) | `fix/no-overwrite-rename` | T12 |
| 4 | T16 sync conflicts | `feat/sync-conflicts` | T11, T17 |
| 4 | T19 setup wizard + admin recovery CLI | `feat/admin-setup-recovery` | T12, T6b |
| 5 | T9 frontend lint/format/tests | `test/frontend-quality` | all frontend work (T11, T13, T14, T15, T16) |
| 5 | T6 project documentation + smoke test | `docs/project-docs` | everything |
| 6 | follow-ups the product owner creates from BUGS.md | `fix/*` | – |

## Risks
- The 27B model may struggle with crewjam/saml wiring. T5's brief pins the exact `samlsp` API pattern, and Claude takes over if two attempts fail.
- Existing deployments break once auth is on. Mitigation: bootstrap the admin from env and document it in the README. `AUTH_ENABLED=false` is the escape hatch.
- `/media/` is used by `<audio src>`. The cookie is sent same-origin, so this keeps working.
