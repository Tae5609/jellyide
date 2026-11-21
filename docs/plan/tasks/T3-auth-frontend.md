# T3: Frontend authentication (login page, auth context, guards)

Repo: JellyIDE. Frontend in `frontend/` (CRA, React 18, react-router v7, plain CSS `frontend/src/index.css`; the responsive layout from T2 is already merged). Backend auth API (already merged, see `backend/internal/auth/handlers.go`):

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/api/auth/providers` | – | `{"enabled":bool,"local":bool,"oidc":{"enabled":bool,"name":str},"saml":{"enabled":bool,"name":str}}` |
| POST | `/api/auth/login` | `{"username","password"}` | 200 user · 401 `{"error"}` · 429 too many attempts |
| POST | `/api/auth/logout` | – | 204 |
| GET | `/api/auth/me` | – | 200 `{"id","username","display_name","email","provider","is_admin"}` or 401 |
| POST | `/api/auth/password` | `{"current_password","new_password"}` | 204 · 400/401 `{"error"}` |
| GET (full-page nav) | `/api/auth/oidc/login`, `/api/auth/saml/login` | – | redirects to IdP, comes back to `/` or `/login?error=<msg>` |

Every protected `POST/PUT/PATCH/DELETE` needs header `X-Requested-With: XMLHttpRequest`, or the server returns 403. Any protected request without a session returns 401.

Follow `docs/plan/CONVENTIONS.md` (commit, PR and quality-gate rules).

## Do
1. `frontend/src/api.js`: `export async function apiFetch(path, opts={})`. It prefixes `/api` when the path starts with `/` and is not already `/api`. It sets `credentials:'same-origin'` and `X-Requested-With: XMLHttpRequest`, and sets `Content-Type: application/json` when the body is a string. On a 401 for any path except `/api/auth/me` and `/api/auth/login`, it does `window.dispatchEvent(new Event('auth:unauthorized'))`. It returns the Response (callers keep their `.json()` handling).
2. Replace **every** `fetch(\`${API}...\`)` in `frontend/src/pages/*.js` with `apiFetch('...')`, and remove the now-unused `API` constants. Keep behaviour identical.
3. `frontend/src/auth/AuthContext.js`: `AuthProvider` loads `/auth/providers` and `/auth/me` on mount and holds state `{user, providers, loading}`. It exposes `login(username,password)` (throws Error(message) on failure), `logout()` and `refresh()`. It listens for `auth:unauthorized` and sets user=null. Export `useAuth()`.
4. `frontend/src/pages/Login.js`: a centered card with the JellyIDE logo.
   - When `providers.local`, show a username + password form with a submit button that shows a spinner/disabled state.
   - Show "Continue with {oidc.name}" and "Continue with {saml.name}" buttons as plain `<a href>` to the login URLs when enabled, with a divider "or" between the form and the SSO buttons.
   - Show an error banner from a failed login or from the `?error=` query param.
   - Must look good at 360px wide: full-width card with a 16px gutter and inputs at font-size 16px. Add CSS classes in `index.css` (`.login-page`, `.login-card`, ...), reusing existing tokens/vars.
   - After login, navigate to `location.state?.from` or `/downloads`. Only accept internal paths that start with `/` and not `//`.
5. `App.js`: wrap in `AuthProvider`. Add route `/login` (public; redirect to `/downloads` if already logged in). A `RequireAuth` wrapper shows a centered loader while loading, and `<Navigate to="/login" state={{from: location.pathname}} replace/>` when no user. When `providers.enabled === false`, skip guards entirely. The sidebar footer (above Settings) shows the user display name/username and a Logout button. The mobile drawer must show them too.
6. `Settings.js`: add a "Change password" card (current, new, confirm; client-side check that new is at least 8 chars and matches confirm). Only show it when `user.provider === 'local'`. Show success/error messages.

## Do NOT
- Add dependencies.
- Store tokens in localStorage (the cookie is HttpOnly and handled by the browser).
- Change backend code.

## Verify
```
cd frontend && npm ci && CI=true npm run build
grep -rn 'fetch(`${API}' src/ && exit 1 || true
```
Build passes with zero warnings. Commit: `feat(auth): add login page, auth context and route guards`.
