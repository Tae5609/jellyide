# T6: Auth docs, deployment config and end-to-end smoke test

Repo: JellyIDE. Auth (local, OIDC, SAML) and the mobile UI are merged. Read `README.md`, `docker-compose.yml`, `docker-compose.prod.yml`, `.env.example`, `Dockerfile`, `backend/internal/auth/*.go` (for the exact env var names and defaults) and `.github/workflows/ci.yml`.

## Do
1. `.env.example` and both compose files: add every auth env var with comments: `AUTH_ENABLED`, `AUTH_LOCAL_ENABLED`, `ADMIN_USERNAME`, `ADMIN_PASSWORD`, `COOKIE_SECURE`, `PUBLIC_URL`, `OIDC_*` and `SAML_*`, with exact names taken from the code. In compose, pass them through as `${VAR:-default}`. `ADMIN_PASSWORD` must have no default; use `${ADMIN_PASSWORD:?set ADMIN_PASSWORD}` only in prod compose. In dev compose, set `AUTH_ENABLED` and a dev password with a comment.
2. `README.md`: add an "Authentication" section covering:
   - first-run admin bootstrap
   - local users and changing passwords
   - OIDC setup, with a generic example and the redirect URI `${PUBLIC_URL}/api/auth/oidc/callback`
   - SAML setup (SP metadata URL `${PUBLIC_URL}/api/auth/saml/metadata`, ACS URL, cert location and auto-generation)
   - reverse proxy / HTTPS notes (`COOKIE_SECURE=true`, `PUBLIC_URL`)
   - disabling auth (`AUTH_ENABLED=false`, not for internet-facing deployments)

   Also mention mobile support in the features list.
3. `docs/dev/keycloak/docker-compose.yml`: Keycloak (`quay.io/keycloak/keycloak:26.0`, `start-dev`, port 8081) for local testing, plus `docs/dev/keycloak/README.md` with step-by-step instructions: create realm `jellyide`, an OIDC client `jellyide` (confidential, redirect `http://localhost:8080/api/auth/oidc/callback`), a SAML client (entity ID = SP metadata URL, ACS URL), and a test user. Include the matching `.env` lines.
4. `scripts/smoke-auth.sh` (bash, `set -euo pipefail`, executable). It builds the backend (`cd backend && go build -o /tmp/jellyide-smoke .`), runs it with `DOWNLOAD_DIR=$(mktemp -d) ADMIN_PASSWORD=smoke-pass-123 PORT=18080` in the background (trap-kill on exit), waits for the port, then asserts with curl:
   - `/api/jobs` without a cookie returns 401
   - `/api/auth/providers` returns 200
   - a bad login returns 401
   - a good login returns 200 and saves a cookie jar
   - `/api/auth/me` with the jar returns 200 with the username admin
   - POST `/api/jobs/clear` with the jar but without `X-Requested-With` returns 403, and with it returns 200
   - logout returns 204, then `/api/auth/me` returns 401

   It prints PASS/FAIL per check and exits non-zero on any failure. Note: the backend serves `./frontend/build` relative to the cwd. That is fine; API checks do not need it.
5. `.github/workflows/ci.yml`: add a step running `scripts/smoke-auth.sh` after the Go build step.

## Do NOT
- Change Go or React source code. If the smoke test reveals a bug, report it in your final reply instead of fixing it.

## Verify
```
bash -n scripts/smoke-auth.sh && ./scripts/smoke-auth.sh
docker compose -f docker-compose.yml config >/dev/null
ADMIN_PASSWORD=x docker compose -f docker-compose.prod.yml config >/dev/null
```
Commit: `document auth setup, add keycloak dev env and auth smoke test`.
