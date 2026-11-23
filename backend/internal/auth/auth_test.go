// Package auth — handlers, middleware and session tests.
package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"yt-scraper/internal/db"

	"github.com/gorilla/mux"
)

const (
	testAdmin    = "admin"
	testPassword = "test-password-123"
)

// newTestService builds a Service backed by a temp-dir sqlite database.
func newTestService(t *testing.T, cfg Config) *Service {
	t.Helper()
	withFastBcrypt(t)
	if cfg.AdminPassword == "" {
		cfg.AdminPassword = testPassword
	}
	if cfg.AdminUsername == "" {
		cfg.AdminUsername = testAdmin
	}
	database, err := db.NewDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	svc, err := NewService(cfg, database)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// newTestHandler wires the service the same way main.go does: auth routes
// plus a stub /api/jobs, wrapped in the auth middleware.
func newTestHandler(s *Service) http.Handler {
	r := mux.NewRouter()
	apiSub := r.PathPrefix("/api").Subrouter()
	s.RegisterRoutes(apiSub)
	apiSub.HandleFunc("/jobs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, []string{})
	})
	return s.Middleware(r)
}

// doReq runs a request against the handler and returns the recorder.
// body may be nil, a string (raw) or any JSON-encodable value.
func doReq(t *testing.T, h http.Handler, method, path string, body interface{}, hdr http.Header) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	switch v := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	for k, vals := range hdr {
		for _, val := range vals {
			req.Header.Add(k, val)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func cookieValue(t *testing.T, rec *httptest.ResponseRecorder, name string) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	t.Fatalf("cookie %s not set in response", name)
	return ""
}

// fetchLoginCSRF grabs a login-CSRF token and its double-submit cookie
// from GET /api/auth/csrf.
func fetchLoginCSRF(t *testing.T, h http.Handler) (token, cookie string) {
	t.Helper()
	rec := doReq(t, h, http.MethodGet, "/api/auth/csrf", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("csrf: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	token, _ = body["token"].(string)
	if token == "" {
		t.Fatal("csrf response has no token")
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "jellyide_login_csrf" {
			cookie = c.Name + "=" + c.Value
		}
	}
	if cookie != "jellyide_login_csrf="+token {
		t.Fatalf("csrf cookie %q does not match the body token (double-submit premise)", cookie)
	}
	return token, cookie
}

// login fetches a login-CSRF token and performs a local login with it.
func login(t *testing.T, h http.Handler, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	token, cookie := fetchLoginCSRF(t, h)
	hdr := http.Header{}
	hdr.Set("Cookie", cookie)
	return doReq(t, h, http.MethodPost, "/api/auth/login",
		map[string]string{"username": username, "password": password, "csrf_token": token}, hdr)
}

func sessionHeader(t *testing.T, token string, xrw bool) http.Header {
	t.Helper()
	hdr := http.Header{}
	hdr.Set("Cookie", SessionCookieName+"="+token)
	if xrw {
		hdr.Set("X-Requested-With", "XMLHttpRequest")
	}
	return hdr
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("secret-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	// This test deliberately does not install the fast-bcrypt seam: it
	// pins the production default, which must stay a cost-12 hash.
	if hash == "secret-123" || !strings.HasPrefix(hash, "$2a$12$") {
		t.Fatalf("expected bcrypt cost-%d hash, got %q", bcryptCost, hash)
	}
	if !CheckPassword(hash, "secret-123") {
		t.Fatal("CheckPassword should accept the right password")
	}
	if CheckPassword(hash, "wrong-password") {
		t.Fatal("CheckPassword should reject a wrong password")
	}
	if CheckPassword("not-a-hash", "secret-123") {
		t.Fatal("CheckPassword should reject an invalid hash")
	}
}

func TestLoginSuccess(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)

	rec := login(t, h, testAdmin, testPassword)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["username"] != testAdmin {
		t.Fatalf("login body username = %v, want %q", body["username"], testAdmin)
	}
	if body["is_admin"] != true {
		t.Fatalf("login body is_admin = %v, want true", body["is_admin"])
	}
	if strings.Contains(rec.Body.String(), "password_hash") {
		t.Fatal("login response leaked password_hash")
	}
	if _, ok := body["password_hash"]; ok {
		t.Fatal("login response contains password_hash key")
	}
	token := cookieValue(t, rec, SessionCookieName)
	if token == "" {
		t.Fatal("login did not set a session cookie")
	}
}

func TestLoginFailure(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)

	// Wrong password.
	rec := login(t, h, testAdmin, "wrong-password-1")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: got %d, want 401", rec.Code)
	}
	if msg := decodeBody(t, rec)["error"]; msg != "invalid credentials" {
		t.Fatalf("wrong password error = %v", msg)
	}

	// Unknown user: same status and message as wrong password.
	rec = login(t, h, "ghost-user", "whatever-123")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user: got %d, want 401", rec.Code)
	}
	if msg := decodeBody(t, rec)["error"]; msg != "invalid credentials" {
		t.Fatalf("unknown user error = %v", msg)
	}

	// Bad JSON.
	rec = doReq(t, h, http.MethodPost, "/api/auth/login", "{not json", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: got %d, want 400", rec.Code)
	}
}

func TestLoginLocalDisabled(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: false})
	h := newTestHandler(svc)

	rec := login(t, h, testAdmin, testPassword)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("local disabled: got %d, want 403", rec.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)

	for i := 0; i < loginFailureLimit; i++ {
		rec := login(t, h, testAdmin, "wrong-password-1")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d, want 401", i+1, rec.Code)
		}
	}
	// 11th attempt from the same IP is rejected before any bcrypt work.
	rec := login(t, h, testAdmin, testPassword)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after %d failures: got %d, want 429", loginFailureLimit, rec.Code)
	}
}

// The admin CLI's `unlock` command clears the login rate limiter; the
// limiter is in memory per process, so a restart clears it as well.
func TestClearLoginRateLimit(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})

	for i := 0; i < loginFailureLimit; i++ {
		svc.limiter.record("192.0.2.10")
	}
	if !svc.limiter.blocked("192.0.2.10") {
		t.Fatal("limiter should be blocked after the failure limit")
	}
	svc.ClearLoginRateLimit()
	if svc.limiter.blocked("192.0.2.10") {
		t.Fatal("limiter should be unblocked after ClearLoginRateLimit")
	}
	// A fresh history can reach the limit again.
	for i := 0; i < loginFailureLimit; i++ {
		svc.limiter.record("192.0.2.11")
	}
	if !svc.limiter.blocked("192.0.2.11") {
		t.Fatal("limiter should block again after a fresh failure history")
	}
}

func TestMiddlewareRequiresSession(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)

	// No cookie: 401.
	rec := doReq(t, h, http.MethodGet, "/api/jobs", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: got %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeBody(t, rec)["error"]; msg != "unauthorized" {
		t.Fatalf("no cookie error = %v", msg)
	}

	// Garbage cookie: 401.
	rec = doReq(t, h, http.MethodGet, "/api/jobs", nil,
		http.Header{"Cookie": {SessionCookieName + "=garbage"}})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("garbage cookie: got %d, want 401", rec.Code)
	}

	// Valid session: 200.
	token := cookieValue(t, login(t, h, testAdmin, testPassword), SessionCookieName)
	rec = doReq(t, h, http.MethodGet, "/api/jobs", nil, sessionHeader(t, token, false))
	if rec.Code != http.StatusOK {
		t.Fatalf("valid session: got %d, want 200", rec.Code)
	}
}

func TestMiddlewareCSRF(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	token := cookieValue(t, login(t, h, testAdmin, testPassword), SessionCookieName)

	// POST with a session but without the XHR header: 403 csrf.
	rec := doReq(t, h, http.MethodPost, "/api/jobs", nil, sessionHeader(t, token, false))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST without XHR header: got %d, want 403", rec.Code)
	}
	if msg := decodeBody(t, rec)["error"]; msg != "csrf" {
		t.Fatalf("csrf error = %v", msg)
	}

	// With the header: allowed through to the handler.
	rec = doReq(t, h, http.MethodPost, "/api/jobs", nil, sessionHeader(t, token, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST with XHR header: got %d, want 200", rec.Code)
	}
}

func TestProvidersPublicAndSetters(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)

	// No session cookie needed.
	rec := doReq(t, h, http.MethodGet, "/api/auth/providers", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("providers: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["enabled"] != true || body["local"] != true {
		t.Fatalf("providers enabled/local = %v/%v, want true/true", body["enabled"], body["local"])
	}
	oidc, _ := body["oidc"].(map[string]interface{})
	saml, _ := body["saml"].(map[string]interface{})
	if oidc == nil || saml == nil {
		t.Fatalf("providers missing oidc/saml fields: %s", rec.Body.String())
	}
	if oidc["enabled"] != false || saml["enabled"] != false {
		t.Fatalf("oidc/saml should start disabled: %v / %v", oidc, saml)
	}

	// T4/T5 extension points flip the response.
	svc.SetOIDCProvider("github")
	svc.SetSAMLProvider("keycloak")
	rec = doReq(t, h, http.MethodGet, "/api/auth/providers", nil, nil)
	body = decodeBody(t, rec)
	oidc, _ = body["oidc"].(map[string]interface{})
	saml, _ = body["saml"].(map[string]interface{})
	if oidc["enabled"] != true || oidc["name"] != "github" {
		t.Fatalf("oidc after SetOIDCProvider = %v", oidc)
	}
	if saml["enabled"] != true || saml["name"] != "keycloak" {
		t.Fatalf("saml after SetSAMLProvider = %v", saml)
	}
}

func TestMe(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)

	// Without a session the middleware already rejects.
	rec := doReq(t, h, http.MethodGet, "/api/auth/me", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("me without session: got %d, want 401", rec.Code)
	}

	token := cookieValue(t, login(t, h, testAdmin, testPassword), SessionCookieName)
	rec = doReq(t, h, http.MethodGet, "/api/auth/me", nil, sessionHeader(t, token, false))
	if rec.Code != http.StatusOK {
		t.Fatalf("me with session: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["username"] != testAdmin || body["is_admin"] != true || body["provider"] != "local" {
		t.Fatalf("me body = %v", body)
	}
	if _, ok := body["password_hash"]; ok {
		t.Fatal("me response contains password_hash")
	}
}

func TestAuthDisabled(t *testing.T) {
	svc := newTestService(t, Config{Enabled: false})
	h := newTestHandler(svc)

	// Everything passes through; /api/jobs needs no session.
	rec := doReq(t, h, http.MethodGet, "/api/jobs", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("auth disabled /api/jobs: got %d, want 200", rec.Code)
	}

	// /api/auth/me reports the anonymous user.
	rec = doReq(t, h, http.MethodGet, "/api/auth/me", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("auth disabled /api/auth/me: got %d, want 200", rec.Code)
	}
	body := decodeBody(t, rec)
	if body["id"] != "anonymous" || body["username"] != "anonymous" ||
		body["provider"] != "none" || body["is_admin"] != true {
		t.Fatalf("anonymous me body = %v", body)
	}

	// /api/auth/providers reports disabled.
	rec = doReq(t, h, http.MethodGet, "/api/auth/providers", nil, nil)
	body = decodeBody(t, rec)
	if body["enabled"] != false {
		t.Fatalf("providers enabled = %v, want false", body["enabled"])
	}
}

func TestChangePassword(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	token := cookieValue(t, login(t, h, testAdmin, testPassword), SessionCookieName)

	// Too short.
	rec := doReq(t, h, http.MethodPost, "/api/auth/password",
		map[string]string{"current_password": testPassword, "new_password": "short"},
		sessionHeader(t, token, true))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("short new password: got %d, want 400", rec.Code)
	}

	// Wrong current password.
	rec = doReq(t, h, http.MethodPost, "/api/auth/password",
		map[string]string{"current_password": "nope-nope-nope", "new_password": "new-password-456"},
		sessionHeader(t, token, true))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong current password: got %d, want 401", rec.Code)
	}

	// Correct change.
	rec = doReq(t, h, http.MethodPost, "/api/auth/password",
		map[string]string{"current_password": testPassword, "new_password": "new-password-456"},
		sessionHeader(t, token, true))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("change password: got %d, want 204: %s", rec.Code, rec.Body.String())
	}

	// Old password no longer works, new one does.
	rec = login(t, h, testAdmin, testPassword)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("old password after change: got %d, want 401", rec.Code)
	}
	rec = login(t, h, testAdmin, "new-password-456")
	if rec.Code != http.StatusOK {
		t.Fatalf("new password after change: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestLogout(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	token := cookieValue(t, login(t, h, testAdmin, testPassword), SessionCookieName)

	rec := doReq(t, h, http.MethodPost, "/api/auth/logout", nil, sessionHeader(t, token, true))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: got %d, want 204", rec.Code)
	}
	// The cookie is cleared (MaxAge negative) in the response.
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName {
			found = true
			if c.MaxAge >= 0 {
				t.Fatalf("logout cookie MaxAge = %d, want negative", c.MaxAge)
			}
		}
	}
	if !found {
		t.Fatal("logout did not clear the session cookie")
	}
	// The old token no longer authenticates.
	rec = doReq(t, h, http.MethodGet, "/api/jobs", nil, sessionHeader(t, token, false))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("old token after logout: got %d, want 401", rec.Code)
	}
}

// With no users and no ADMIN_PASSWORD the service no longer refuses to
// start: it enters setup mode instead (no admin row is created).
func TestSetupModeWhenNoUsersAndNoPassword(t *testing.T) {
	svc, err := newTestServiceErr(t, Config{Enabled: true, AdminUsername: testAdmin, AdminPassword: ""})
	if err != nil {
		t.Fatalf("NewService in setup mode: %v", err)
	}
	if !svc.SetupRequired() {
		t.Fatal("SetupRequired should be true with no users and no ADMIN_PASSWORD")
	}
	if n, err := svc.CountUsers(); err != nil || n != 0 {
		t.Fatalf("users in setup mode = %d (err %v), want 0", n, err)
	}
}

// newTestServiceErr is like newTestService but returns the error instead of
// failing the test.
func newTestServiceErr(t *testing.T, cfg Config) (*Service, error) {
	t.Helper()
	withFastBcrypt(t)
	if cfg.AdminUsername == "" {
		cfg.AdminUsername = testAdmin
	}
	database, err := db.NewDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		return nil, err
	}
	return NewService(cfg, database)
}

// Regression: the collision suffix must apply to the *resolved* name (the
// username param, falling back to the subject), and repeated collisions must
// keep producing valid unique names instead of "-<provider>" or a UNIQUE
// violation.
func TestFindOrCreateExternalUserNameCollision(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})

	// Occupy the base name and the first collision suffix with local users.
	for _, name := range []string{"alice", "alice-oidc"} {
		if err := svc.CreateUser(&User{ID: newID(), Username: name, Provider: "local"}); err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
	}

	// alice is taken and alice-oidc is taken too: expect alice-oidc-2.
	u, err := svc.FindOrCreateExternalUser("oidc", "sub-alice-1", "alice", "alice@example.com", "Alice", false)
	if err != nil {
		t.Fatalf("FindOrCreateExternalUser: %v", err)
	}
	if u.Username != "alice-oidc-2" {
		t.Fatalf("username = %q, want %q", u.Username, "alice-oidc-2")
	}

	// Empty username falls back to the subject; the subject-derived name is
	// taken, so the suffix must apply to the subject, not to "".
	if err := svc.CreateUser(&User{ID: newID(), Username: "taken-subject", Provider: "local"}); err != nil {
		t.Fatalf("create taken-subject: %v", err)
	}
	u2, err := svc.FindOrCreateExternalUser("oidc", "taken-subject", "", "", "", false)
	if err != nil {
		t.Fatalf("FindOrCreateExternalUser (empty username): %v", err)
	}
	if u2.Username != "taken-subject-oidc" {
		t.Fatalf("username = %q, want %q", u2.Username, "taken-subject-oidc")
	}

	// Re-login for the same (provider, subject) returns the same user.
	again, err := svc.FindOrCreateExternalUser("oidc", "sub-alice-1", "alice", "", "", false)
	if err != nil {
		t.Fatalf("re-login: %v", err)
	}
	if again.ID != u.ID || again.Username != u.Username {
		t.Fatalf("re-login returned a different user: id=%q username=%q, want id=%q username=%q",
			again.ID, again.Username, u.ID, u.Username)
	}

	// No user may have an empty or a duplicated username.
	var rows []struct {
		Username string `db:"username"`
	}
	if err := svc.db.Select(&rows, `SELECT username FROM users`); err != nil {
		t.Fatalf("select users: %v", err)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if row.Username == "" {
			t.Fatal("found a user with an empty username")
		}
		if seen[row.Username] {
			t.Fatalf("duplicate username %q", row.Username)
		}
		seen[row.Username] = true
	}
}

// Regression: concurrent first logins that all resolve to the same base
// username must all succeed with distinct names instead of one hitting the
// UNIQUE constraint and surfacing a 500.
func TestFindOrCreateExternalUserConcurrent(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})

	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	users := make(chan *User, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u, err := svc.FindOrCreateExternalUser("oidc", fmt.Sprintf("sub-%d", i), "same", "", "", false)
			if err != nil {
				errs <- err
				return
			}
			users <- u
		}(i)
	}
	wg.Wait()
	close(errs)
	close(users)

	for err := range errs {
		t.Fatalf("FindOrCreateExternalUser: %v", err)
	}
	seen := map[string]bool{}
	for u := range users {
		if seen[u.Username] {
			t.Fatalf("duplicate username %q", u.Username)
		}
		seen[u.Username] = true
	}
	if len(seen) != n {
		t.Fatalf("created %d users, want %d", len(seen), n)
	}
}

// Regression: concurrent first logins for the same (provider, subject) must
// all return the same user, not a UNIQUE violation.
func TestFindOrCreateExternalUserConcurrentSameSubject(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})

	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	users := make(chan *User, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, err := svc.FindOrCreateExternalUser("oidc", "same-subject", "same", "", "", false)
			if err != nil {
				errs <- err
				return
			}
			users <- u
		}()
	}
	wg.Wait()
	close(errs)
	close(users)

	for err := range errs {
		t.Fatalf("FindOrCreateExternalUser: %v", err)
	}
	var id string
	for u := range users {
		if id == "" {
			id = u.ID
		} else if u.ID != id {
			t.Fatalf("same subject returned different users: %q and %q", id, u.ID)
		}
	}
}

// TestClientIP covers the rate-limiter key selection. With TrustProxy on
// the first X-Forwarded-For hop wins; an unparseable or missing header
// falls back to the peer. With TrustProxy off (the default) the header is
// ignored so a client cannot spoof it.
func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		trustProxy bool
		remoteAddr string
		xff        string
		want       string
	}{
		{"trust proxy: first hop wins", true, "10.0.0.1:40000", "203.0.113.7, 10.0.0.1", "203.0.113.7"},
		{"trust proxy: single hop", true, "10.0.0.1:40000", "203.0.113.7", "203.0.113.7"},
		{"trust proxy: padded hop", true, "10.0.0.1:40000", "  203.0.113.9 , 10.0.0.1", "203.0.113.9"},
		{"trust proxy: ipv6 hop", true, "10.0.0.1:40000", "2001:db8::7, 10.0.0.1", "2001:db8::7"},
		{"trust proxy: unparseable hop falls back to peer", true, "10.0.0.1:40000", "not-an-ip, 10.0.0.1", "10.0.0.1"},
		{"trust proxy: missing header falls back to peer", true, "10.0.0.1:40000", "", "10.0.0.1"},
		{"trust proxy off: header ignored", false, "10.0.0.1:40000", "203.0.113.7", "10.0.0.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t, Config{Enabled: true, LocalEnabled: true, TrustProxy: tt.trustProxy})
			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				req.Header.Set("X-Forwarded-For", tt.xff)
			}
			if got := svc.clientIP(req); got != tt.want {
				t.Fatalf("clientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

// Regression for the shared-bucket lockout: behind a reverse proxy every
// request shares the proxy's RemoteAddr, so the limiter must key on the
// first X-Forwarded-For hop when TrustProxy is on. One locked-out client
// must not lock out the other clients, and with TrustProxy off a client
// cannot pin another client's bucket by spoofing the header.
func TestLoginRateLimitTrustProxy(t *testing.T) {
	const (
		attackerPeer = "10.0.0.1:40000"
		otherPeer    = "10.0.0.2:40000"
		victimPeer   = "198.51.100.9:55555"
	)
	// A valid double-submit pair (matching cookie and body token) so the
	// attempts reach the credential check and feed the limiter.
	badLogin := `{"username":"nobody","password":"wrong-password-1","csrf_token":"trust-proxy-test-token"}`

	post := func(t *testing.T, h http.Handler, peer, xff string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(badLogin))
		req.RemoteAddr = peer
		req.Header.Set("Cookie", "jellyide_login_csrf=trust-proxy-test-token")
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	t.Run("trust proxy keys on first hop", func(t *testing.T) {
		svc := newTestService(t, Config{Enabled: true, LocalEnabled: true, TrustProxy: true})
		h := newTestHandler(svc)

		for i := 0; i < loginFailureLimit; i++ {
			rec := post(t, h, attackerPeer, "203.0.113.7")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("attempt %d: got %d, want 401", i+1, rec.Code)
			}
		}
		// Same XFF hop from a different peer is the same client: blocked.
		rec := post(t, h, otherPeer, "203.0.113.7")
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("same XFF hop: got %d, want 429", rec.Code)
		}
		// A different XFF hop is a different client: not blocked.
		rec = post(t, h, attackerPeer, "203.0.113.8")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("different XFF hop: got %d, want 401", rec.Code)
		}
	})

	t.Run("trust proxy off ignores spoofed header", func(t *testing.T) {
		svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
		h := newTestHandler(svc)

		// The attacker floods the login with the victim's address in the
		// header; the limiter must only see the attacker's peer.
		for i := 0; i < loginFailureLimit; i++ {
			rec := post(t, h, attackerPeer, "198.51.100.9")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("attempt %d: got %d, want 401", i+1, rec.Code)
			}
		}
		// The attacker's peer is now locked out.
		rec := post(t, h, attackerPeer, "198.51.100.9")
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("attacker: got %d, want 429", rec.Code)
		}
		// The victim's own connection is untouched by the spoofed header.
		rec = post(t, h, victimPeer, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("victim: got %d, want 401", rec.Code)
		}
	})
}

// TestLoginCSRFTokenFlow: the double-submit flow works end to end —
// GET /auth/csrf issues the token (body + SameSite=Lax cookie), and the
// login POST carrying the matching pair succeeds.
func TestLoginCSRFTokenFlow(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)

	token, cookie := fetchLoginCSRF(t, h)
	hdr := http.Header{}
	hdr.Set("Cookie", cookie)
	rec := doReq(t, h, http.MethodPost, "/api/auth/login",
		map[string]string{"username": testAdmin, "password": testPassword, "csrf_token": token}, hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("login with valid token pair: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if cookieValue(t, rec, SessionCookieName) == "" {
		t.Fatal("valid login did not set a session cookie")
	}
}

// TestLoginCSRFRejection: a cross-site request cannot carry the token
// pair. Missing token, missing cookie and a mismatched pair are all
// rejected with 403 before any credential work, and no session is created.
func TestLoginCSRFRejection(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)

	// Cross-site form POST: no cookie, no token.
	rec := doReq(t, h, http.MethodPost, "/api/auth/login",
		map[string]string{"username": testAdmin, "password": testPassword}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no token: got %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeBody(t, rec)["error"]; msg != "login csrf mismatch" {
		t.Fatalf("no token error = %v", msg)
	}

	// Token in the body but the cookie is absent (the attacker cannot
	// read the victim's cookie).
	rec = doReq(t, h, http.MethodPost, "/api/auth/login",
		map[string]string{"username": testAdmin, "password": testPassword, "csrf_token": "forged-token"}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forged token, no cookie: got %d, want 403", rec.Code)
	}

	// Cookie present, body token mismatched.
	_, cookie := fetchLoginCSRF(t, h)
	hdr := http.Header{}
	hdr.Set("Cookie", cookie)
	rec = doReq(t, h, http.MethodPost, "/api/auth/login",
		map[string]string{"username": testAdmin, "password": testPassword, "csrf_token": "wrong-token"}, hdr)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("mismatched pair: got %d, want 403", rec.Code)
	}

	// None of the rejected attempts pinned a session onto the victim.
	var n int
	if err := svc.db.Get(&n, `SELECT COUNT(*) FROM sessions`); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if n != 0 {
		t.Fatalf("sessions created by rejected logins = %d, want 0", n)
	}
}

// Regression: a session row pointing at a deleted user (orphaned session)
// must be dropped and reported as "no valid session". GetUserByID maps
// sql.ErrNoRows to ErrNoUser, so the check must compare against ErrNoUser;
// comparing against sql.ErrNoRows was dead code and the error leaked
// instead of the session being cleaned up.
func TestUserFromRequestDropsOrphanedSession(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})

	const token = "orphan-session-token"
	sum := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(sum[:])
	// The sessions FK would refuse the row, so switch it off on the write
	// connection just for the insert and back on before continuing.
	if _, err := svc.DB().Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("pragma off: %v", err)
	}
	if _, err := svc.DB().Exec(
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		tokenHash, "missing-user-id", formatTime(time.Now().Add(time.Hour))); err != nil {
		t.Fatalf("insert orphaned session: %v", err)
	}
	if _, err := svc.DB().Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("pragma on: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	// The request cookie mirrors the real session cookie flags (Secure is
	// unconditionally true here; it is config-driven only on the way out).
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	_, err := svc.UserFromRequest(req)
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("UserFromRequest err = %v, want ErrNoSession", err)
	}
	var n int
	if err := svc.db.Get(&n, `SELECT COUNT(*) FROM sessions WHERE token_hash = ?`, tokenHash); err != nil {
		t.Fatalf("count session: %v", err)
	}
	if n != 0 {
		t.Fatalf("orphaned session row still present: %d, want 0", n)
	}
}

// Regression: oversized JSON bodies are rejected with 413 before any of
// the body is processed.
func TestLoginOversizedBody(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)

	body := `{"username":"` + testAdmin + `","password":"` + strings.Repeat("a", maxAuthBody+1024) + `"}`
	rec := doReq(t, h, http.MethodPost, "/api/auth/login", body, nil)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: got %d, want 413", rec.Code)
	}
}
