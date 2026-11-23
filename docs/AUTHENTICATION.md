# Authentication

How JellyIDE authenticates users: the admin bootstrap and first-run
setup, local accounts, self-registration modes, user administration, and
single sign-on via OIDC or SAML 2.0 — plus the session, cookie and CSRF
security model.

Environment variables are documented in
[DEPLOYMENT.md](./DEPLOYMENT.md#environment-variables). A local Keycloak
instance for trying out both SSO protocols is in
[docs/dev/keycloak](./dev/keycloak/README.md). For the raw endpoints, see
[API.md](./API.md).

## Table of contents

- [Admin bootstrap and first-run setup](#admin-bootstrap-and-first-run-setup)
  - [Unattended pre-creation via the admin password](#unattended-pre-creation-via-the-admin-password)
  - [First-run setup wizard](#first-run-setup-wizard)
  - [Admin CLI bootstrap and recovery](#admin-cli-bootstrap-and-recovery)
  - [Later starts](#later-starts)
- [Local accounts](#local-accounts)
- [Registration](#registration)
  - [Modes](#modes)
  - [Approval flow](#approval-flow)
- [User management (admin)](#user-management-admin)
- [Single sign-on: OIDC](#single-sign-on-oidc)
  - [How the flow works](#how-the-flow-works)
  - [Generic setup](#generic-setup)
  - [Example: Keycloak](#example-keycloak)
  - [Example: Authentik](#example-authentik)
  - [Example: Google](#example-google)
- [Single sign-on: SAML](#single-sign-on-saml)
  - [Endpoints](#endpoints)
  - [Certificate handling](#certificate-handling)
  - [IdP metadata](#idp-metadata)
  - [Attribute mapping](#attribute-mapping)
  - [Admin group](#admin-group)
- [Session, cookie and CSRF security model](#session-cookie-and-csrf-security-model)
- [Disabling auth](#disabling-auth)

## Admin bootstrap and first-run setup

When auth is enabled (`AUTH_ENABLED` is on, the default) and the
database still has **zero users**, exactly one of two paths creates the
first admin account. Afterwards the stored users are the source of
truth and neither path ever runs again.

### Unattended pre-creation via the admin password

`ADMIN_PASSWORD` set before the very first start (empty database): the
server creates the admin account right away from

- `ADMIN_USERNAME` (default `admin`)
- `ADMIN_PASSWORD` (no default)

This is the unattended path. The variables are only consulted while no
users exist — they are never re-applied and never reset an existing
password, so restarts and upgrades are safe. Omitting `ADMIN_PASSWORD`
instead selects the setup wizard below; it does **not** refuse the start.

### First-run setup wizard

`ADMIN_PASSWORD` unset (or empty) on a database with zero users: the
server starts in **first-run setup mode** and prints a one-time setup
token to the log banner with the URL to open (`docker logs jelly-ide`
shows it):

```text
==== JellyIDE first-run setup ====
Open http://localhost:8080/setup and enter this setup token:
...
==================================
```

- Open **`<PUBLIC_URL>/setup`** in a browser and complete the wizard:
  enter the token, pick the admin username and password. Username and
  password follow the same rules as registration (see
  [Local accounts](#local-accounts)), and the new account is created
  active with admin rights.
- While setup is pending, every API route answers
  `503 {"error":"setup_required"}` (same for `/media/*`) — except
  `GET /api/auth/providers` and the `GET /api/setup/status` /
  `POST /api/setup/complete` pair the wizard itself uses. The SPA still
  loads, which is how the `/setup` page is served.
- The token is kept in memory only, is regenerated on every start until
  setup completes, and is never persisted or logged anywhere else.
  Failed attempts are rate limited per IP (`429`), a wrong token gets
  `403`.
- Completing the wizard creates the admin and starts a session for it
  **immediately — no restart needed**. A second (or concurrent)
  completion attempt gets `409 setup already complete`.

### Admin CLI bootstrap and recovery

The `admin` subcommand built into the server binary operates directly on
the database (`DOWNLOAD_DIR/jellyide.db`) and works while the server is
running. Besides recovery it can also do the bootstrap without the web
wizard:

```sh
# bootstrap the first admin instead of using /setup
./server admin create <username> --admin

# recovery: locked out of the admin account
./server admin reset-password admin            # prompts twice, no echo
./server admin reset-password admin --generate # prints a strong password once
./server admin list                            # id, provider, admin flag, status
./server admin promote <user> | demote <user> | enable <user> | disable <user>
./server admin unlock                          # clear the login rate limiter
```

In Docker, run them inside the container, e.g.
`docker exec -it jelly-ide ./server admin list`. Creating the first
user through the CLI ends setup mode just like the wizard does — no
restart. Demote/disable refuse to touch the last active admin, the same
guard the admin API applies. Note: `unlock` clears the in-memory login
rate limiter of the process it runs in; to clear a running server's
limiter, restart the server.

### Later starts

Once at least one user exists, both `ADMIN_USERNAME`/`ADMIN_PASSWORD`
and setup mode are ignored — the database is the source of truth. Change
the first admin's password immediately (Users page, or
`POST /api/auth/password`).

The admin is a normal local account: it can log in with username and
password like any other user, and it can be demoted or deleted like any
other user — with the guard that the last active admin can never be
demoted, disabled or deleted.

## Local accounts

- **Username:** 3–32 characters of `[a-zA-Z0-9._-]`, unique.
- **Password:** minimum 8 characters, stored as a bcrypt hash. Unknown
  usernames are checked against a dummy hash so response timing does not
  reveal which usernames exist.
- **Login rate limiting:** more than 10 failed logins from one IP within
  15 minutes get `429` until the window passes.
- **Password change:** `POST /api/auth/password` with the current and new
  password (local users only; minimum 8 characters).

## Registration

Self sign-up is `POST /api/auth/register` (public) and is gated by the
registration mode. Like login, the body must carry the
[login-CSRF token](#session-cookie-and-csrf-security-model)
(`GET /api/auth/csrf` first); in `admin_only` mode the
`403 registration is disabled` answer comes before the token is checked.

### Modes

| Mode | Self sign-up | New account state |
|---|---|---|
| `admin_only` (default) | rejected with `403` | — |
| `open` | allowed | `active` — the response starts a session immediately |
| `approval` | allowed | `pending` — no session until an admin approves |

The mode is seeded from `REGISTRATION_MODE` into the settings table once
at first start. Afterwards it is changed in the admin UI (Users page,
"Registration" setting) or `PUT /api/admin/settings` with
`{"registration_mode": "open"}` — the stored value wins over the
environment variable. `GET /api/auth/providers` reports the current mode,
which is how the login page decides whether to show a "Create account"
link.

### Approval flow

1. In `approval` mode a visitor registers: `POST /api/auth/register`
   returns `201 {"status":"pending"}` and **no** session cookie.
2. The user tries to log in. With a *wrong* password the response is the
   same as for any user (the account status is only revealed after a
   correct password check, so it cannot be probed). With the *correct*
   password the login returns `403 account pending approval`.
3. An admin approves the account (Users page, or
   `PATCH /api/admin/users/{id}` with `{"status":"active"}`).
4. The user can now log in normally.

The same state machine applies to SSO-provisioned users: in `approval`
mode a first OIDC/SAML login provisions a `pending` account and is
refused until approved.

## User management (admin)

The Users page (admin-only) and the `/api/admin/*` endpoints:

| Operation | Endpoint |
|---|---|
| List users | `GET /api/admin/users` |
| Create user (active, local; optional `is_admin`) | `POST /api/admin/users` |
| Change `is_admin`, `status` (`active`/`pending`/`disabled`), `email`, `display_name` | `PATCH /api/admin/users/{id}` |
| Reset a local user's password (ends all their sessions) | `POST /api/admin/users/{id}/password` |
| Delete a user (ends all their sessions) | `DELETE /api/admin/users/{id}` |
| Get/set registration mode | `GET` / `PUT /api/admin/settings` |

Guards:

- The **last active admin** cannot be demoted, disabled or deleted
  (`409`), so the instance always has at least one admin who can log in.
- Nobody can delete their own account (`400`).
- Password reset and any move out of `active` delete all of the user's
  sessions immediately.
- Password reset is only available for local users; SSO users inherit
  credentials from the provider (their `email`, `display_name` and admin
  flag are re-synced from the provider on every login).

## Single sign-on: OIDC

JellyIDE is an OIDC client using the authorization-code flow with PKCE
and nonce verification. Enable it with the `OIDC_*` variables (see
[DEPLOYMENT.md](./DEPLOYMENT.md#environment-variables)); the server
discovers the provider at startup, so a broken `OIDC_ISSUER` fails the
boot.

### How the flow works

1. `GET /api/auth/oidc/login` stores a random `state` (bound to the
   browser in a short-lived HttpOnly cookie) plus a nonce and PKCE
   verifier (10-minute TTL) and redirects to the provider.
2. The provider redirects to
   **`<PUBLIC_URL>/api/auth/oidc/callback`** with a code.
3. The server exchanges the code (PKCE), verifies the id_token
   (signature, audience, nonce) and reads the claims:
   - username: `preferred_username`, else the local part of `email`,
     else `sub`
   - email: `email`, display name: `name`
   - groups: the list claim named by `OIDC_GROUPS_CLAIM` (default
     `groups`); a user in `OIDC_ADMIN_GROUP` becomes an admin
4. The local account is resolved or auto-created (`OIDC_AUTO_CREATE`,
   default on) and re-synced on **every** login, then a regular
   JellyIDE session starts.

### Generic setup

1. Register an OIDC/OAuth2 client at the provider with redirect URI
   **`<PUBLIC_URL>/api/auth/oidc/callback`** (e.g.
   `https://music.example.com/api/auth/oidc/callback`).
2. Use a **confidential** client (client secret) — the code requires
   `OIDC_CLIENT_SECRET`.
3. Make sure the id_token carries at least `openid`, plus `profile` and
   `email` for sensible usernames (that is the default
   `OIDC_SCOPES`).
4. Set in `.env`:

   ```sh
   OIDC_ENABLED=true
   OIDC_NAME="My SSO"
   OIDC_ISSUER=https://id.example.com/realms/jellyide
   OIDC_CLIENT_ID=jellyide
   OIDC_CLIENT_SECRET=<secret>
   # optional:
   OIDC_ADMIN_GROUP=admin
   ```

5. Restart the container. The login page now shows the SSO button; the
   name comes from `OIDC_NAME`.

A ready-made Keycloak with both an OIDC and a SAML client configured is
in [docs/dev/keycloak](./dev/keycloak/README.md).

### Example: Keycloak

1. Create a realm `jellyide`.
2. Create a client: client id `jellyide`, type **OpenID Connect**,
   access type **confidential**.
3. In *Valid redirect URIs* add exactly
   `http://<host>:<port>/api/auth/oidc/callback` (e.g.
   `http://localhost:8080/api/auth/oidc/callback` for the local dev
   stack). *Standard flow* is enabled by default.
4. Copy the client secret from the *Credentials* tab.
5. For admin-group mapping, Keycloak does not put groups in the token by
   default: create a group (e.g. `admin`), then add a **Group mapper**
   (Token Claim Name `groups`, Claim Type Group, Multivalued on) to the
   client, and set `OIDC_ADMIN_GROUP=admin`.
6. `.env`:

   ```sh
   OIDC_ENABLED=true
   OIDC_NAME=Keycloak
   OIDC_ISSUER=http://localhost:8081/realms/jellyide
   OIDC_CLIENT_ID=jellyide
   OIDC_CLIENT_SECRET=<from credentials tab>
   OIDC_ADMIN_GROUP=admin
   ```

### Example: Authentik

1. In the authentik admin, create an **Application** of type *OIDC* named
   `jellyide` and an associated *Provider*.
2. In the provider's *Client settings*: grant type *Authorization Code*,
   and add the redirect URI
   `https://music.example.com/api/auth/oidc/callback`.
3. The issuer is `https://<authentik-host>/application/o/jellyide/`; the
   client id and secret are in the provider's *Client credentials*.
4. `.env`:

   ```sh
   OIDC_ENABLED=true
   OIDC_NAME=Authentik
   OIDC_ISSUER=https://authentik.example.com/application/o/jellyide/
   OIDC_CLIENT_ID=<client id>
   OIDC_CLIENT_SECRET=<client secret>
   # authentik exposes groups under the "groups" claim by default:
   OIDC_ADMIN_GROUP=jellyide-admins
   ```

### Example: Google

1. Google Cloud Console → *APIs & Services* → *OAuth consent screen*:
   create an **External** project if needed.
2. *Credentials* → *Create OAuth client ID* → **Web application**.
3. Under *Authorized redirect URIs* add
   `https://music.example.com/api/auth/oidc/callback`.
4. The issuer for Google is `https://accounts.google.com`; the client id
   and secret are the OAuth client's credentials.
5. `.env`:

   ```sh
   OIDC_ENABLED=true
   OIDC_NAME=Google
   OIDC_ISSUER=https://accounts.google.com
   OIDC_CLIENT_ID=<client id>.apps.googleusercontent.com
   OIDC_CLIENT_SECRET=<client secret>
   ```

Note: Google id_tokens carry no group claim, so `OIDC_ADMIN_GROUP` does
not work with Google; promote admins afterwards in the Users page.

## Single sign-on: SAML

JellyIDE is a SAML 2.0 **service provider** (crewjam/saml) rooted at
`<PUBLIC_URL>/api/auth`. IdP-initiated (unsolicited) logins are refused;
only the SP-initiated flow works.

### Endpoints

| Purpose | URL |
|---|---|
| SP metadata (give this to the IdP) | `GET <PUBLIC_URL>/api/auth/saml/metadata` |
| Assertion consumer service (IdP POSTs here) | `POST <PUBLIC_URL>/api/auth/saml/acs` |
| SP-initiated login (start the flow) | `GET <PUBLIC_URL>/api/auth/saml/login` |

Example with `PUBLIC_URL=https://music.example.com`:

- metadata: `https://music.example.com/api/auth/saml/metadata`
- ACS: `https://music.example.com/api/auth/saml/acs`

### Certificate handling

- When `SAML_ENABLED=true` and the certificate/key are missing, the
  server generates a self-signed pair on startup: RSA 2048, CN
  `JellyIDE SP`, valid 10 years, written to `SAML_SP_CERT` (mode 0644)
  and `SAML_SP_KEY` (mode 0600), parent dir mode 0700.
- Default location is `<DOWNLOAD_DIR>/saml/` — inside the music volume,
  so it persists across upgrades. See the persistent-volume note in
  [DEPLOYMENT.md](./DEPLOYMENT.md#volumes-and-data-layout).
- **Keep the key.** The IdP stores the SP certificate to validate
  responses; if the key is lost or regenerated, re-export the SP
  metadata and update the IdP's SP trust.
- You may also bring your own CA-signed pair by pointing
  `SAML_SP_CERT`/`SAML_SP_KEY` at your files.

### IdP metadata

Set exactly one of:

- `SAML_IDP_METADATA_URL` — fetched over HTTP(S) at startup; the IdP
  must be reachable or the server does not start.
- `SAML_IDP_METADATA_FILE` — path inside the container to a downloaded
  metadata XML (mount it as a volume).

Most IdPs (Keycloak, Authentik, ADFS, Okta) publish a metadata document
you can point at directly or download and mount.

### Attribute mapping

The assertion's attributes are matched by **Name or FriendlyName**:

| Variable | Default | Used for |
|---|---|---|
| `SAML_USERNAME_ATTR` | *(empty → NameID)* | username |
| `SAML_EMAIL_ATTR` | `email` | email |
| `SAML_NAME_ATTR` | `displayName` | display name |
| `SAML_GROUPS_ATTR` | `groups` | group list |

If the username attribute is empty or missing, the assertion's `NameID`
is used. Users are auto-created on first login unless
`SAML_AUTO_CREATE=false`; profile fields (email, display name, admin
flag) are re-synced on every login.

### Admin group

Set `SAML_ADMIN_GROUP` to the group value (from `SAML_GROUPS_ATTR`) that
grants admin rights, e.g. `SAML_ADMIN_GROUP=jellyide-admins`.

## Session, cookie and CSRF security model

- **Sessions** are server-side rows in the `sessions` table. The cookie
  holds 32 random bytes (base64url); only its SHA-256 hash is stored.
  Lifetime is 7 days from (re)creation, with sliding expiry: when less
  than 6 days remain, the next authenticated request extends it back to 7.
  Expired sessions are deleted on use and purged hourly.
- **Session cookie** `jellyide_session`: `HttpOnly`, `SameSite=Lax`,
  `Path=/`, `Secure` when `COOKIE_SECURE=true`.
- **Authentication check:** every request to `/api/*` (except the public
  paths: `/api/auth/*` — login, register, providers, the CSRF token
  endpoint and the whole OIDC and SAML flows — and the `/api/setup/*`
  wizard endpoints) and `/media/*` needs a valid session, else `401`.
  While first-run setup is pending, the setup gate answers
  `503 {"error":"setup_required"}` for all of them instead (except
  `/api/auth/providers` and `/api/setup/*`).
- **CSRF protection:** the SPA sends `X-Requested-With: XMLHttpRequest`
  on every request; the middleware requires that header on all
  state-changing requests (POST/PUT/PATCH/DELETE) to protected paths and
  answers `403 {"error":"csrf"}` without it. Public auth endpoints are
  exempt because they need to work without a session — but login and
  register are protected by the double-submit token below.
- **Login CSRF (double submit):** `GET /api/auth/csrf` returns a random
  token in the body **and** in the `jellyide_login_csrf` cookie
  (`HttpOnly`, `SameSite=Lax`, 1-hour expiry, `Secure` when
  `COOKIE_SECURE=true`). `POST /api/auth/login` and
  `POST /api/auth/register` must carry the token in the body
  (`csrf_token`); a missing or mismatching token answers
  `403 {"error":"login csrf mismatch"}`. A cross-site page can read
  neither half of the pair, so a forged login POST — which would pin the
  victim's browser onto the attacker's account, or in `open` mode create
  an attacker-controlled account for it — gets refused.
- **Status enforcement:** only `active` users hold sessions. A user who
  becomes `pending` or `disabled` loses all sessions immediately and
  cannot log in again until re-activated.
- **Rate limiting:** 10 failed logins/registrations per IP per 15
  minutes, then `429`; validation rejections (bad username/password
  shape) feed the limiter too. The key is the direct peer address, or
  the first `X-Forwarded-For` hop when `TRUST_PROXY=true` — only set
  that behind a proxy that overwrites the header. The first-run setup
  endpoint runs a stricter limiter (5 failures per IP per 15 minutes).
- OIDC additionally uses `state` (bound to the browser via a 10-minute
  HttpOnly cookie), PKCE and a verified nonce; SAML relies on the
  library's signature validation and refuses unsolicited responses.

## Disabling auth

`AUTH_ENABLED=false` turns off the whole security boundary:

- the middleware passes every request through, no login is possible;
- `GET /api/auth/me` reports an `anonymous` superuser with
  `is_admin: true`, and the admin endpoints accept every caller.

Use this only for a single-user instance on a trusted network. All other
variables are ignored while auth is disabled.
