# T8: Registration modes and admin user management

Branch `feat/users-registration`. Read `docs/plan/CONVENTIONS.md`, all of `backend/internal/auth/`, and the frontend `src/auth/`, `src/pages/Login.js`, `src/pages/Settings.js`, `src/App.js`, and `src/api.js`.

## Product rules
- Registration mode is one of `admin_only` (**default**), `open`, or `approval`.
  - `admin_only`: no self sign-up. Only admins create accounts.
  - `open`: anyone can register and is active immediately.
  - `approval`: anyone can register, but the account is `pending` until an admin approves it.
- The initial mode comes from env `REGISTRATION_MODE` (default `admin_only`). Once an admin changes it in the UI, the DB value wins. Store it in a `settings(key TEXT PRIMARY KEY, value TEXT NOT NULL)` table.
- Users have a `status`: `active | pending | disabled`. Only `active` users can log in or hold sessions. Changing a user to `disabled` deletes their sessions.
- SSO (OIDC/SAML) auto-created users follow the mode: in `approval` mode they are created `pending`. In `admin_only` mode they are still created only if the provider's `*_AUTO_CREATE=true` (the existing env behaviour). Document this in a code comment.
- There must always be at least one active admin. Block demoting, disabling or deleting the last one with 409.

## Backend
1. Migration: run `ALTER TABLE users ADD COLUMN status TEXT NOT NULL DEFAULT 'active'` guarded (check `PRAGMA table_info` first), and create the `settings` table.
2. Login failures for non-active users: pending returns 403 `{"error":"account pending approval"}`, disabled returns 403 `{"error":"account disabled"}`. Only return these after a correct password, so account status is not leaked.
3. `GET /api/auth/providers` gains `"registration": "admin_only|open|approval"`.
4. `POST /api/auth/register` (public). Body `{"username","password","email"?}`.
   - 403 in admin_only.
   - Username matches `^[a-zA-Z0-9._-]{3,32}$`. Password is at least 8 characters.
   - 409 if the username is taken.
   - Rate-limit with the same limiter as login.
   - Returns 201 `{"status":"active"|"pending"}`. In `open` mode it also starts a session.
5. Admin API, behind a new `RequireAdmin` middleware (403 if not admin), at `/api/admin/*`:

   | Method | Path | Notes |
   |---|---|---|
   | GET | `/admin/users` | list: id, username, email, display_name, provider, is_admin, status, created_at, last_login |
   | POST | `/admin/users` | `{"username","password","email"?,"is_admin"?}` creates an active local user |
   | PATCH | `/admin/users/{id}` | any of `{"is_admin","status","email","display_name"}` |
   | POST | `/admin/users/{id}/password` | `{"new_password"}` admin reset (local users only); deletes their sessions |
   | DELETE | `/admin/users/{id}` | cannot delete self; 204 |
   | GET | `/admin/settings` | `{"registration_mode"}` |
   | PUT | `/admin/settings` | `{"registration_mode"}` validated |
6. Tests cover every mode, every status, the last-admin protections, non-admin getting 403, and that the register validation rejects bad input.

## Frontend
1. Login page: show a "Create account" link when `registration !== 'admin_only'`. Add a `/register` page with the same styling as login. It shows "Your account is awaiting admin approval" in approval mode, and logs in and redirects in open mode.
2. A new admin-only `Users` page (`/admin/users`), with a sidebar link visible only to admins:
   - a table on desktop and cards on ≤768px
   - a create-user dialog
   - row actions: approve (pending), enable/disable, make/remove admin, reset password, delete (with confirm modal via `ModalContext`)
   - a badge with the count of pending users in the sidebar
3. Settings: an admin-only "Registration" card with a radio for the 3 modes and a description of each.
4. All new UI must work at 360px width.

## Verify
Run all gates from CONVENTIONS.md that exist at this point. Commits, for example: `feat(users): add registration modes and admin user api`, `feat(ui): add users admin page and register page`.
