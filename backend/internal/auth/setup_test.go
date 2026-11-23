// Package auth — first-run setup wizard tests (T19).
package auth

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"yt-scraper/internal/db"

	"github.com/gorilla/mux"
)

// newSetupService builds a Service that starts in setup mode: auth
// enabled, no users, no ADMIN_PASSWORD.
func newSetupService(t *testing.T) *Service {
	t.Helper()
	withFastBcrypt(t)
	database, err := db.NewDB(filepath.Join(t.TempDir(), "setup.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	svc, err := NewService(Config{Enabled: true, LocalEnabled: true, AdminUsername: testAdmin}, database)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if !svc.SetupRequired() {
		t.Fatal("expected setup mode with no users and no ADMIN_PASSWORD")
	}
	return svc
}

// newSetupHandler wires the service the same way main.go does: the setup
// gate wraps the auth middleware, which wraps the router.
func newSetupHandler(s *Service) http.Handler {
	r := mux.NewRouter()
	apiSub := r.PathPrefix("/api").Subrouter()
	s.RegisterRoutes(apiSub)
	apiSub.HandleFunc("/jobs", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, []string{})
	})
	r.PathPrefix("/media/").HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "media")
	})
	r.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "spa")
	})
	return s.SetupGate(s.Middleware(r))
}

func setupToken(t *testing.T, s *Service) string {
	t.Helper()
	token := s.setupToken
	if token == "" {
		t.Fatal("setup token was not generated")
	}
	return token
}

func TestSetupModeStarts(t *testing.T) {
	svc := newSetupService(t)
	if n, err := svc.CountUsers(); err != nil || n != 0 {
		t.Fatalf("users = %d (err %v), want 0 in setup mode", n, err)
	}
	if svc.setupToken == "" {
		t.Fatal("setup token must be generated in memory")
	}
}

func TestSetupGateBlocksEverything(t *testing.T) {
	svc := newSetupService(t)
	h := newSetupHandler(svc)

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/jobs", http.StatusServiceUnavailable},
		{http.MethodGet, "/api/admin/users", http.StatusServiceUnavailable},
		{http.MethodPost, "/api/auth/login", http.StatusServiceUnavailable},
		{http.MethodGet, "/api/auth/me", http.StatusServiceUnavailable},
		{http.MethodGet, "/api/auth/providers", http.StatusOK},
		{http.MethodGet, "/api/setup/status", http.StatusOK},
		{http.MethodGet, "/media/song.flac", http.StatusServiceUnavailable},
		{http.MethodGet, "/", http.StatusOK}, // the SPA must load
		{http.MethodGet, "/setup", http.StatusOK},
	}
	for _, c := range cases {
		rec := doReq(t, h, c.method, c.path, nil, nil)
		if rec.Code != c.want {
			t.Fatalf("%s %s: got %d, want %d: %s", c.method, c.path, rec.Code, c.want, rec.Body.String())
		}
	}

	// The 503 body is the documented shape.
	rec := doReq(t, h, http.MethodGet, "/api/jobs", nil, nil)
	if msg := decodeBody(t, rec)["error"]; msg != "setup_required" {
		t.Fatalf("gate error = %v, want setup_required", msg)
	}

	// The providers response carries the flag the frontend redirects on.
	rec = doReq(t, h, http.MethodGet, "/api/auth/providers", nil, nil)
	body := decodeBody(t, rec)
	if body["setup_required"] != true {
		t.Fatalf("providers setup_required = %v, want true", body["setup_required"])
	}

	// The status endpoint reports the same.
	rec = doReq(t, h, http.MethodGet, "/api/setup/status", nil, nil)
	if body := decodeBody(t, rec); body["setup_required"] != true {
		t.Fatalf("status setup_required = %v, want true", body["setup_required"])
	}
}

func TestSetupCompleteFlow(t *testing.T) {
	svc := newSetupService(t)
	h := newSetupHandler(svc)
	token := setupToken(t, svc)

	complete := func(tok, username, password string) *httptest.ResponseRecorder {
		return doReq(t, h, http.MethodPost, "/api/setup/complete",
			map[string]string{"token": tok, "username": username, "password": password}, nil)
	}

	// Wrong token: 403.
	rec := complete("wrong-token-1", "firstadmin", "correct-horse-battery")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token: got %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeBody(t, rec)["error"]; msg != "invalid setup token" {
		t.Fatalf("wrong token error = %v", msg)
	}

	// No user may exist after a failed attempt.
	if n, _ := svc.CountUsers(); n != 0 {
		t.Fatalf("users after failed attempt = %d, want 0", n)
	}

	// Right token: 201, first admin created, session started, setup over.
	rec = complete(token, "firstadmin", "correct-horse-battery")
	if rec.Code != http.StatusCreated {
		t.Fatalf("complete: got %d, want 201: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["username"] != "firstadmin" || body["is_admin"] != true || body["status"] != "active" {
		t.Fatalf("complete body = %v", body)
	}
	if strings.Contains(rec.Body.String(), "password_hash") {
		t.Fatal("complete response leaked password_hash")
	}
	cookie := cookieValue(t, rec, SessionCookieName)

	if svc.SetupRequired() {
		t.Fatal("setup must be over after complete")
	}
	if u, err := svc.GetUserByUsername("firstadmin"); err != nil || !u.IsAdmin || u.Status != UserStatusActive {
		t.Fatalf("first admin = %+v (err %v)", u, err)
	}

	// No restart: the gate opens, the new session works, the admin can
	// also log in normally.
	rec = doReq(t, h, http.MethodGet, "/api/jobs", nil, sessionHeader(t, cookie, false))
	if rec.Code != http.StatusOK {
		t.Fatalf("jobs after complete: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/auth/me", nil, sessionHeader(t, cookie, false))
	if rec.Code != http.StatusOK {
		t.Fatalf("me after complete: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec := login(t, h, "firstadmin", "correct-horse-battery"); rec.Code != http.StatusOK {
		t.Fatalf("login after complete: got %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Status and providers now report setup as done.
	rec = doReq(t, h, http.MethodGet, "/api/setup/status", nil, nil)
	if body := decodeBody(t, rec); body["setup_required"] != false {
		t.Fatalf("status after complete = %v, want false", body["setup_required"])
	}
	rec = doReq(t, h, http.MethodGet, "/api/auth/providers", nil, nil)
	if body := decodeBody(t, rec); body["setup_required"] != false {
		t.Fatalf("providers after complete = %v, want false", body["setup_required"])
	}

	// A second call (even with the right token) is 409.
	rec = complete(token, "otheradmin", "another-password-1")
	if rec.Code != http.StatusConflict {
		t.Fatalf("second complete: got %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if n, _ := svc.CountUsers(); n != 1 {
		t.Fatalf("users after second complete = %d, want 1", n)
	}

	// The completion is audited.
	rows := auditRows(t, svc, AuditActionSetupComplete)
	if len(rows) != 1 || rows[0].Actor != "setup" || rows[0].Target != "firstadmin" {
		t.Fatalf("setup_complete audit rows = %+v", rows)
	}
}

func TestSetupCompleteValidation(t *testing.T) {
	svc := newSetupService(t)
	h := newSetupHandler(svc)
	token := setupToken(t, svc)

	complete := func(body map[string]string) *httptest.ResponseRecorder {
		return doReq(t, h, http.MethodPost, "/api/setup/complete", body, nil)
	}

	// Bad JSON.
	rec := doReq(t, h, http.MethodPost, "/api/setup/complete", "{not json", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: got %d, want 400", rec.Code)
	}

	// Invalid username (same rules as registration).
	for _, username := range []string{"ab", "has space", ""} {
		rec = complete(map[string]string{"token": token, "username": username, "password": "password-123"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("username %q: got %d, want 400: %s", username, rec.Code, rec.Body.String())
		}
	}

	// Short password.
	rec = complete(map[string]string{"token": token, "username": "firstadmin", "password": "short"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("short password: got %d, want 400: %s", rec.Code, rec.Body.String())
	}

	// Nothing was created by any of the failed attempts.
	if n, _ := svc.CountUsers(); n != 0 {
		t.Fatalf("users after validation failures = %d, want 0", n)
	}
	if !svc.SetupRequired() {
		t.Fatal("setup must still be required after validation failures")
	}
}

func TestSetupRateLimit(t *testing.T) {
	svc := newSetupService(t)
	h := newSetupHandler(svc)
	token := setupToken(t, svc)

	complete := func(tok string) *httptest.ResponseRecorder {
		return doReq(t, h, http.MethodPost, "/api/setup/complete",
			map[string]string{"token": tok, "username": "firstadmin", "password": "password-123"}, nil)
	}

	// setupFailureLimit wrong tokens are all 403...
	for i := 0; i < setupFailureLimit; i++ {
		rec := complete(fmt.Sprintf("wrong-token-%d", i))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("wrong token %d: got %d, want 403", i+1, rec.Code)
		}
	}
	// ...then the IP is blocked, even with the right token.
	rec := complete(token)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after %d failures: got %d, want 429", setupFailureLimit, rec.Code)
	}
}

// Concurrent completions: exactly one may win, the rest get 409.
func TestSetupConcurrentComplete(t *testing.T) {
	svc := newSetupService(t)
	h := newSetupHandler(svc)
	token := setupToken(t, svc)

	const workers = 8
	results := make(chan int, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- doReqStatus(t, h, http.MethodPost, "/api/setup/complete",
				map[string]string{"token": token, "username": "firstadmin", "password": "password-123"})
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var created, conflicted int
	for code := range results {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicted++
		default:
			t.Fatalf("unexpected status %d", code)
		}
	}
	if created != 1 {
		t.Fatalf("created = %d, want exactly 1 (conflicts = %d)", created, conflicted)
	}
	if conflicted != workers-1 {
		t.Fatalf("conflicts = %d, want %d", conflicted, workers-1)
	}
	if n, _ := svc.CountUsers(); n != 1 {
		t.Fatalf("users = %d, want 1", n)
	}
}

// doReqStatus is doReq without the t.Fatalf on oddities: it returns the
// status code so concurrent callers can be counted.
func doReqStatus(t *testing.T, h http.Handler, method, path string, body interface{}) int {
	t.Helper()
	rec := doReq(t, h, method, path, body, nil)
	return rec.Code
}
