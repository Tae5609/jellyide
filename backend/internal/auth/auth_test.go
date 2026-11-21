// Package auth — handlers, middleware and session tests.
package auth

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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

func login(t *testing.T, h http.Handler, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	return doReq(t, h, http.MethodPost, "/api/auth/login",
		map[string]string{"username": username, "password": password}, nil)
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
	if hash == "secret-123" || !strings.HasPrefix(hash, "$2") {
		t.Fatalf("expected bcrypt hash, got %q", hash)
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

func TestBootstrapRequiresAdminPassword(t *testing.T) {
	_, err := newTestServiceErr(t, Config{Enabled: true, AdminUsername: testAdmin, AdminPassword: ""})
	if err == nil {
		t.Fatal("expected error when ADMIN_PASSWORD is empty and no users exist")
	}
	if err.Error() != "no users exist: set ADMIN_PASSWORD to bootstrap the admin account" {
		t.Fatalf("unexpected bootstrap error: %v", err)
	}
}

// newTestServiceErr is like newTestService but returns the error instead of
// failing the test.
func newTestServiceErr(t *testing.T, cfg Config) (*Service, error) {
	t.Helper()
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
