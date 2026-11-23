#!/usr/bin/env bash
# Auth smoke test (T6b).
#
# Builds the backend, runs it against a temp DOWNLOAD_DIR on a scratch
# port, and asserts the core auth behaviour end to end:
#
#   - unauthenticated GET /api/jobs                     -> 401
#   - GET  /api/auth/providers                          -> 200
#   - POST /api/auth/login (wrong password)             -> 401
#   - POST /api/auth/login (correct password)           -> 200 + session cookie
#   - GET  /api/auth/me                                 -> 200, username "admin"
#   - POST /api/jobs/clear without X-Requested-With     -> 403 (CSRF)
#   - POST /api/jobs/clear with X-Requested-With        -> 200
#   - GET  /media/jellyide.db (authenticated)           -> not served (404/403)
#   - POST /api/auth/register (default admin_only mode) -> 403
#   - POST /api/auth/logout                             -> 204, then /api/auth/me -> 401
#
# Prints PASS/FAIL per check and exits non-zero on any failure.

set -euo pipefail

PORT=18080
BASE="http://127.0.0.1:${PORT}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

WORK_DIR="$(mktemp -d)"
DATA_DIR="$(mktemp -d)"
COOKIE_JAR="$WORK_DIR/cookies.txt"
SERVER_BIN="$WORK_DIR/server"
SERVER_LOG="$WORK_DIR/server.log"
BODY_FILE="$WORK_DIR/body"
SERVER_PID=""

PASS=0
FAIL=0

cleanup() {
  if [ -n "$SERVER_PID" ]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$WORK_DIR" "$DATA_DIR"
}
trap cleanup EXIT

pass() { PASS=$((PASS + 1)); printf 'PASS: %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf 'FAIL: %s\n' "$1"; }

# check_eq NAME EXPECTED ACTUAL
check_eq() {
  local name="$1" expected="$2" actual="$3"
  if [ "$actual" = "$expected" ]; then
    pass "$name (got ${actual})"
  else
    fail "$name (expected ${expected}, got ${actual})"
  fi
}

# check_any NAME ACTUAL EXPECTED...
check_any() {
  local name="$1" actual="$2"
  shift 2
  for expected in "$@"; do
    if [ "$actual" = "$expected" ]; then
      pass "$name (got ${actual})"
      return 0
    fi
  done
  fail "$name (expected one of: $*, got ${actual})"
}

# check_in NAME PATTERN HAYSTACK
check_in() {
  local name="$1" pattern="$2" haystack="$3"
  if printf '%s' "$haystack" | grep -q -- "$pattern"; then
    pass "$name"
  else
    fail "$name (pattern ${pattern} not found in response)"
  fi
}

# http_code METHOD PATH [curl extra args...]
http_code() {
  local method="$1" path="$2"
  shift 2
  curl -s -o /dev/null -w '%{http_code}' -X "$method" --max-time 10 "$@" "${BASE}${path}"
}

command -v go >/dev/null 2>&1 || { echo "FAIL: go toolchain not found"; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "FAIL: curl not found"; exit 1; }

echo "==> building backend"
(cd "$REPO_ROOT/backend" && go build -o "$SERVER_BIN" .)

echo "==> starting server (port ${PORT}, data dir: ${DATA_DIR})"
# env -i keeps the test hermetic: the surrounding environment must not be
# able to switch on OIDC/SAML or change the registration mode.
env -i \
  PATH="$PATH" \
  DOWNLOAD_DIR="$DATA_DIR" \
  ADMIN_PASSWORD=smoke-pass-123 \
  PORT="$PORT" \
  AUTH_ENABLED=true \
  AUTH_LOCAL_ENABLED=true \
  COOKIE_SECURE=false \
  REGISTRATION_MODE=admin_only \
  OIDC_ENABLED=false \
  SAML_ENABLED=false \
  "$SERVER_BIN" >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!

# Wait for the port (up to 30s).
for _ in $(seq 1 60); do
  if curl -s -o /dev/null --max-time 2 "${BASE}/api/auth/providers"; then
    break
  fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "FAIL: server exited during startup; log follows"
    cat "$SERVER_LOG"
    exit 1
  fi
  sleep 0.5
done
if ! curl -s -o /dev/null --max-time 2 "${BASE}/api/auth/providers"; then
  echo "FAIL: server did not come up on port ${PORT}; log follows"
  cat "$SERVER_LOG"
  exit 1
fi

echo "==> running checks"

# 1. Unauthenticated API access is rejected.
code=$(http_code GET /api/jobs)
check_eq "unauthenticated GET /api/jobs is 401" 401 "$code"

# 2. Provider discovery is public.
code=$(http_code GET /api/auth/providers)
check_eq "GET /api/auth/providers is 200" 200 "$code"

# Login-CSRF handshake, mirroring frontend/src/auth/AuthContext.js login().
# POST /api/auth/login is double-submit protected (handlers.go
# handleLoginCSRF + verifyLoginCSRF): the public GET below answers
# {"token":"..."} and mirrors the same value into the jellyide_login_csrf
# cookie, and the login POST only passes when its csrf_token body field
# equals that cookie. The token is random per request but the check is a
# stateless cookie-vs-body comparison that stays valid until
# loginCSRFMaxAge, so one fetch serves both login attempts below as long as
# the cookie jar it fills is replayed with each POST.
CSRF_TOKEN="$(curl -s --max-time 10 -c "$COOKIE_JAR" "${BASE}/api/auth/csrf" \
  | sed -n 's/.*"token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' || true)"
if [ -z "$CSRF_TOKEN" ]; then
  echo "FAIL: login-CSRF handshake: GET /api/auth/csrf returned no token; log follows"
  cat "$SERVER_LOG"
  exit 1
fi

# 3. A bad login is rejected.
code=$(http_code POST /api/auth/login \
  -H 'Content-Type: application/json' \
  -b "$COOKIE_JAR" \
  -d "{\"username\":\"admin\",\"password\":\"wrong-password\",\"csrf_token\":\"$CSRF_TOKEN\"}")
check_eq "login with a wrong password is 401" 401 "$code"

# 4. A good login succeeds and fills the cookie jar.
code=$(curl -s -b "$COOKIE_JAR" -c "$COOKIE_JAR" -o "$BODY_FILE" -w '%{http_code}' \
  -X POST -H 'Content-Type: application/json' \
  -H 'X-Requested-With: XMLHttpRequest' --max-time 10 \
  -d "{\"username\":\"admin\",\"password\":\"smoke-pass-123\",\"csrf_token\":\"$CSRF_TOKEN\"}" \
  "${BASE}/api/auth/login")
check_eq "login with the correct password is 200" 200 "$code"
if [ -f "$COOKIE_JAR" ] && grep -q 'jellyide_session' "$COOKIE_JAR"; then
  pass "session cookie stored in the cookie jar"
else
  fail "session cookie missing from the cookie jar"
fi

# 5. The session identifies the bootstrapped admin.
code=$(curl -s -b "$COOKIE_JAR" -o "$BODY_FILE" -w '%{http_code}' \
  --max-time 10 "${BASE}/api/auth/me")
check_eq "GET /api/auth/me is 200" 200 "$code"
check_in "GET /api/auth/me reports username admin" '"username":"admin"' "$(cat "$BODY_FILE")"

# 6. CSRF: state-changing requests need the X-Requested-With header.
code=$(http_code POST /api/jobs/clear -b "$COOKIE_JAR")
check_eq "POST /api/jobs/clear without X-Requested-With is 403" 403 "$code"
code=$(http_code POST /api/jobs/clear -b "$COOKIE_JAR" \
  -H 'X-Requested-With: XMLHttpRequest')
check_eq "POST /api/jobs/clear with X-Requested-With is 200" 200 "$code"

# 7. The database must not be reachable through /media/. (Requested with a
# session: unauthenticated /media/* already 401s in the middleware, which
# would make the assertion vacuous.)
code=$(http_code GET /media/jellyide.db -b "$COOKIE_JAR")
check_any "GET /media/jellyide.db is not served" "$code" 404 403

# 8. Self-registration is disabled in the default admin_only mode.
code=$(http_code POST /api/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"username":"smokeuser","password":"smoke-pass-123"}')
check_eq "register in default admin_only mode is 403" 403 "$code"

# 9. Logout ends the session.
code=$(http_code POST /api/auth/logout -b "$COOKIE_JAR" -c "$COOKIE_JAR" \
  -H 'X-Requested-With: XMLHttpRequest')
check_eq "POST /api/auth/logout is 204" 204 "$code"
code=$(http_code GET /api/auth/me -b "$COOKIE_JAR")
check_eq "GET /api/auth/me after logout is 401" 401 "$code"

echo "==> smoke-auth: ${PASS} passed, ${FAIL} failed"
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
