# T1: Backend auth core (local username/password, sessions, middleware)

Repo: JellyIDE. Go backend in `backend/` (module name `yt-scraper`). Read `backend/main.go`, `backend/internal/api/router.go`, `backend/internal/db/db.go` first.

Follow `docs/plan/CONVENTIONS.md` (commit, PR and quality-gate rules).

## Do
1. Add deps: `cd backend && go get golang.org/x/crypto/bcrypt`.
2. In `backend/internal/db/db.go` `migrate()`, append these tables (keep the existing schema untouched):
```sql
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  username TEXT UNIQUE NOT NULL,
  password_hash TEXT,
  provider TEXT NOT NULL DEFAULT 'local',
  subject TEXT,
  email TEXT,
  display_name TEXT,
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
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
```
3. Create the package `backend/internal/auth/` with:
   - `config.go`: `type Config struct{ Enabled, LocalEnabled, CookieSecure bool; AdminUsername, AdminPassword, PublicURL string }` and `func LoadConfig() Config` reading env `AUTH_ENABLED` (default true), `AUTH_LOCAL_ENABLED` (default true), `COOKIE_SECURE` (default false), `ADMIN_USERNAME` (default "admin"), `ADMIN_PASSWORD`, `PUBLIC_URL` (default "http://localhost:8080").
   - `store.go`: `type User struct{ID, Username string; PasswordHash *string; Provider string; Subject, Email, DisplayName *string; IsAdmin bool; ...}` with db tags. Use `*db.DB` from `yt-scraper/internal/db`. Functions: `GetUserByUsername`, `GetUserByID`, `CreateUser`, `UpdatePassword`, `TouchLastLogin`, `CountUsers`, `FindOrCreateExternalUser(provider, subject, username, email, displayName string, isAdmin bool) (*User, error)` (T4/T5 use this; on username collision, append `-<provider>`).
   - `password.go`: `HashPassword(string) (string, error)` (bcrypt cost 12) and `CheckPassword(hash, pw string) bool`.
   - `session.go`: `CreateSession(w, userID)` generates 32 random bytes (crypto/rand), base64url encodes them, stores `sha256 hex` in sessions with a 7-day expiry, and sets cookie `jellyide_session` (`HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure=cfg.CookieSecure`, `MaxAge` 7d). `UserFromRequest(r) (*User, error)` looks up the hash, rejects expired sessions, and slides the expiry when less than 6 days remain. `DestroySession(w, r)` deletes the row and clears the cookie. Also add a goroutine that deletes expired sessions hourly.
   - `service.go`: `type Service struct{cfg Config; db *db.DB}` and `NewService(cfg, db) (*Service, error)`. If `cfg.Enabled` and `CountUsers()==0`: when `AdminPassword==""`, return an error with the message "no users exist: set ADMIN_PASSWORD to bootstrap the admin account". Otherwise create a local admin user.
   - `middleware.go`: `func (s *Service) Middleware(next http.Handler) http.Handler`. Pass through when `!cfg.Enabled`. Public (no auth) paths: anything not under `/api/` or `/media/`, and anything under `/api/auth/` except `/api/auth/me`, `/api/auth/logout`, `/api/auth/password`. Otherwise require a valid session, or respond `401 {"error":"unauthorized"}`. CSRF check for protected `POST/PUT/PATCH/DELETE`: require header `X-Requested-With: XMLHttpRequest`, or respond `403 {"error":"csrf"}`. Store the user in the request context. Expose `UserFromContext(ctx) *User`.
   - `handlers.go`: `func (s *Service) RegisterRoutes(r *mux.Router)` on the `/api` subrouter:
     - `GET  /auth/providers` returns `{"enabled":bool,"local":bool,"oidc":{"enabled":false,"name":""},"saml":{"enabled":false,"name":""}}`. Keep the oidc and saml fields; a `Providers` struct with setters lets T4/T5 fill them.
     - `POST /auth/login` takes body `{"username","password"}`. It returns 400 on bad JSON, 401 `{"error":"invalid credentials"}` on failure (use the same message for an unknown user, and still run bcrypt against a dummy hash to equalize timing), 403 when local is disabled, and 200 with the user JSON on success. Add simple in-memory rate limiting: 10 failures per IP per 15 minutes, then 429.
     - `POST /auth/logout` returns 204.
     - `GET  /auth/me` returns 200 with the user JSON `{"id","username","display_name","email","provider","is_admin"}`, or 401. When auth is disabled it returns `{"id":"anonymous","username":"anonymous","provider":"none","is_admin":true}`.
     - `POST /auth/password` takes body `{"current_password","new_password"}`. Local users only. New password must be at least 8 chars. Verify current. Returns 204.
     - Never serialize `password_hash`.
4. `backend/main.go`: load config, create the service (`log.Fatal` on error), call `authSvc.RegisterRoutes(apiSub)` before `api.RegisterRoutes`, and wrap the whole router: `handler := authSvc.Middleware(r)`. Remove the `rs/cors` wildcard. The SPA is same-origin, so delete the cors usage and run `go mod tidy`.
4b. Extension points for T4 (OIDC) and T5 (SAML), which add their own files in this package later. Export these methods on `*Service`: `Config() Config`, `DB() *db.DB`, `StartSession(w http.ResponseWriter, userID string) error`, `FindOrCreateExternalUser(...)`, `SetOIDCProvider(name string)`, `SetSAMLProvider(name string)` (the last two flip the fields returned by `/auth/providers`).
5. Add `backend/internal/auth/auth_test.go` with tests for: hash/check, login success/failure, middleware 401 on `/api/jobs` without a cookie, 200 with a cookie, CSRF 403 on POST without the header, and public access to `/api/auth/providers`. Use a temp-dir sqlite via `db.NewDB`.

## Do NOT
- Touch the frontend.
- Change existing API handlers' behaviour.
- Log passwords or tokens.

## Verify (all must pass before you stop)
```
cd backend && gofmt -l . && go vet ./... && go build ./... && go test ./...
```
`gofmt -l` must print nothing. Then `git add -A && git commit -m "feat(auth): add local login, sessions and auth middleware"`.
