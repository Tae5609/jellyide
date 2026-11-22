package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"yt-scraper/internal/auth"
	"yt-scraper/internal/db"
	"yt-scraper/internal/download"
	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
	"yt-scraper/internal/metadata"
	"yt-scraper/internal/scanner"
	"yt-scraper/internal/ytdlp"
)

// newAuthedRouter wires the full API behind a real auth service (login
// middleware + RequireAdmin) and returns the handler plus the handles the
// tests need. The ytdlp manager is pointed at a fake release server.
func newAuthedRouter(t *testing.T, st *fakeRelease) (http.Handler, *auth.Service, *ytdlp.Manager, *jobs.Manager) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.NewDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	svc, err := auth.NewService(auth.Config{
		Enabled:          true,
		LocalEnabled:     true,
		AdminUsername:    "admin",
		AdminPassword:    "admin-password-1",
		RegistrationMode: auth.RegistrationModeAdminOnly,
	}, database)
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	if _, err := svc.CreateLocalUser("joe", "joe-password-1", "", false); err != nil {
		t.Fatalf("create non-admin user: %v", err)
	}

	libService := library.NewService(database, dir)
	jobManager := jobs.NewManager(database)
	musicScanner := scanner.NewScanner(libService, jobManager, dir)
	downloader := download.NewDownloader(jobManager, musicScanner, dir)
	yt := ytdlp.NewManager(database, jobManager, filepath.Join(dir, ".jellyide", "bin"), true)
	downloader.SetYtDlpResolver(yt)
	yt.SetGate(downloader)
	if st != nil {
		srv := newFakeReleaseServer(st)
		yt.SetTestURLs(ytdlp.ChannelStable, ytdlp.ChannelURLs{
			Binary: srv.URL + "/download/yt-dlp",
			Sums:   srv.URL + "/download/SHA2-256SUMS",
			API:    srv.URL + "/api/releases/latest",
		})
	}
	metaService := metadata.NewServiceWith(metadata.Deps{
		DB:      database,
		Library: libService,
		Jobs:    jobManager,
		Tags:    noOpTagWriter{},
	})

	r := mux.NewRouter()
	apiSub := r.PathPrefix("/api").Subrouter()
	svc.RegisterRoutes(apiSub)
	RegisterRoutes(apiSub, downloader, libService, metaService, jobManager, musicScanner, yt, svc.RequireAdmin)
	return svc.Middleware(r), svc, yt, jobManager
}

// fakeRelease is a mutable fake of the GitHub release endpoints.
type fakeRelease struct {
	mu     sync.Mutex
	tag    string
	script string
	sums   string
}

func (fr *fakeRelease) set(tag, script, sums string) {
	fr.mu.Lock()
	fr.tag, fr.script, fr.sums = tag, script, sums
	fr.mu.Unlock()
}

func sumsForScript(script string) string {
	h := sha256.Sum256([]byte(script))
	return hex.EncodeToString(h[:]) + "  yt-dlp\n"
}

func newFakeReleaseServer(fr *fakeRelease) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fr.mu.Lock()
		defer fr.mu.Unlock()
		switch r.URL.Path {
		case "/api/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tag_name":"` + fr.tag + `"}`))
		case "/download/yt-dlp":
			_, _ = w.Write([]byte(fr.script))
		case "/download/SHA2-256SUMS":
			_, _ = w.Write([]byte(fr.sums))
		default:
			http.NotFound(w, r)
		}
	}))
	return srv
}

func fakeScript(version string) string {
	return "#!/bin/sh\necho \"" + version + "\"\n"
}

// loginToken logs in with username/password and returns the session cookie.
func loginToken(t *testing.T, h http.Handler, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: got %d: %s", username, rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c.Value
		}
	}
	t.Fatal("login did not set the session cookie")
	return ""
}

// apiReq performs one request with the given session cookie ("" for none).
func apiReq(t *testing.T, h http.Handler, method, path, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Cookie", auth.SessionCookieName+"="+token)
	}
	if method != http.MethodGet {
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestYtDlpAdminEndpointsRejectNonAdmin is the admin-only enforcement
// test: every yt-dlp admin endpoint and the config POST must return 403
// for a logged-in non-admin and 401 with no session at all.
func TestYtDlpAdminEndpointsRejectNonAdmin(t *testing.T) {
	h, _, _, _ := newAuthedRouter(t, nil)
	token := loginToken(t, h, "joe", "joe-password-1")

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/admin/ytdlp"},
		{http.MethodPost, "/api/admin/ytdlp/check"},
		{http.MethodPost, "/api/admin/ytdlp/update"},
		{http.MethodPost, "/api/admin/ytdlp/rollback"},
		{http.MethodPut, "/api/admin/ytdlp/settings"},
		{http.MethodPost, "/api/config"},
	}
	for _, c := range cases {
		rec := apiReq(t, h, c.method, c.path, token, nil)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s (non-admin): got %d, want 403: %s", c.method, c.path, rec.Code, rec.Body.String())
		}
		// Without a session the middleware rejects first.
		rec = apiReq(t, h, c.method, c.path, "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s (anonymous): got %d, want 401: %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

// TestPostConfigAdminOnly: only admins may change the concurrency limit
// and proxy; reading stays open to everyone.
func TestPostConfigAdminOnly(t *testing.T) {
	h, _, _, _ := newAuthedRouter(t, nil)
	admin := loginToken(t, h, "admin", "admin-password-1")
	joe := loginToken(t, h, "joe", "joe-password-1")

	body := []byte(`{"concurrent_limit": 4, "proxy": "http://p:1"}`)
	rec := apiReq(t, h, http.MethodPost, "/api/config", joe, body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /config (non-admin): got %d, want 403: %s", rec.Code, rec.Body.String())
	}
	rec = apiReq(t, h, http.MethodGet, "/api/config", joe, nil)
	var cfg map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("GET /config: %v", err)
	}
	if cfg["concurrent_limit"] != float64(2) {
		t.Fatalf("concurrency after a refused change = %v, want the untouched 2", cfg["concurrent_limit"])
	}

	// The admin succeeds, and a non-admin read sees the updated config.
	rec = apiReq(t, h, http.MethodPost, "/api/config", admin, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /config (admin): got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	rec = apiReq(t, h, http.MethodGet, "/api/config", joe, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("GET /config (non-admin): %v", err)
	}
	if cfg["concurrent_limit"] != float64(4) || cfg["proxy"] != "http://p:1" {
		t.Fatalf("config after admin change = %v, want limit 4 and the proxy", cfg)
	}
}

// waitForJobStatus polls a job until terminal or timeout.
func waitForJobStatus(t *testing.T, jm *jobs.Manager, id string) *jobs.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, err := jm.GetJob(id)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if j.Status == jobs.StatusCompleted || j.Status == jobs.StatusFailed {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish in time", id)
	return nil
}

// TestYtDlpAdminEndpoints covers the happy paths as an admin: the status
// shape, the check, the update job flow (202, 409 up to date, force),
// the rollback and the settings validation.
func TestYtDlpAdminEndpoints(t *testing.T) {
	fr := &fakeRelease{tag: "2025.1.15", script: fakeScript("2025.1.15"), sums: sumsForScript(fakeScript("2025.1.15"))}
	h, _, yt, jm := newAuthedRouter(t, fr)
	admin := loginToken(t, h, "admin", "admin-password-1")

	// GET returns the full status shape.
	rec := apiReq(t, h, http.MethodGet, "/api/admin/ytdlp", admin, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/admin/ytdlp: got %d: %s", rec.Code, rec.Body.String())
	}
	var st map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	for _, key := range []string{
		"version", "path", "managed", "channel", "latest_version",
		"update_available", "last_check", "last_result", "auto_update",
		"interval_hours", "updating", "has_rollback",
	} {
		if _, ok := st[key]; !ok {
			t.Fatalf("status shape missing %q: %v", key, st)
		}
	}
	if st["managed"] != true || st["channel"] != "stable" {
		t.Fatalf("managed/channel = %v/%v, want true/stable", st["managed"], st["channel"])
	}

	// Check refreshes the latest version.
	rec = apiReq(t, h, http.MethodPost, "/api/admin/ytdlp/check", admin, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("check: got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode check: %v", err)
	}
	if st["latest_version"] != "2025.1.15" {
		t.Fatalf("latest_version = %v, want 2025.1.15", st["latest_version"])
	}
	if st["last_result"] != "checked 2025.1.15" {
		t.Fatalf("last_result = %v", st["last_result"])
	}

	// Update: 202 with a job id, then the job completes and the binary is
	// in place.
	rec = apiReq(t, h, http.MethodPost, "/api/admin/ytdlp/update", admin, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("update: got %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var upd map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &upd); err != nil || upd["job_id"] == "" {
		t.Fatalf("update body = %q, want a job_id", rec.Body.String())
	}
	job := waitForJobStatus(t, jm, upd["job_id"])
	if job.Status != jobs.StatusCompleted {
		t.Fatalf("update job = %s (error: %s), want completed", job.Status, job.Error)
	}
	if !yt.HasBinary() || yt.Version() != "2025.1.15" {
		t.Fatalf("binary after first update = %q, want 2025.1.15", yt.Version())
	}
	// A first install leaves no rollback candidate.
	rec = apiReq(t, h, http.MethodPost, "/api/admin/ytdlp/rollback", admin, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("rollback without a previous binary: got %d, want 409: %s", rec.Code, rec.Body.String())
	}

	// A second update to a newer release leaves a rollback candidate.
	fr.set("2025.2.2", fakeScript("2025.2.2"), sumsForScript(fakeScript("2025.2.2")))
	rec = apiReq(t, h, http.MethodPost, "/api/admin/ytdlp/check", admin, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("check before second update: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	rec = apiReq(t, h, http.MethodPost, "/api/admin/ytdlp/update", admin, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("second update: got %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &upd); err != nil {
		t.Fatalf("decode second update: %v", err)
	}
	job = waitForJobStatus(t, jm, upd["job_id"])
	if job.Status != jobs.StatusCompleted {
		t.Fatalf("second update job = %s (error: %s), want completed", job.Status, job.Error)
	}
	if yt.Version() != "2025.2.2" {
		t.Fatalf("version after second update = %q, want 2025.2.2", yt.Version())
	}

	// A third non-forced update is refused: already up to date.
	rec = apiReq(t, h, http.MethodPost, "/api/admin/ytdlp/update", admin, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("update when up to date: got %d, want 409: %s", rec.Code, rec.Body.String())
	}

	// Rollback restores the previous binary, then 409s.
	rec = apiReq(t, h, http.MethodPost, "/api/admin/ytdlp/rollback", admin, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback: got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode rollback: %v", err)
	}
	if st["has_rollback"] != false {
		t.Fatalf("has_rollback = %v after a rollback, want false", st["has_rollback"])
	}
	if yt.Version() != "2025.1.15" {
		t.Fatalf("version after rollback = %q, want 2025.1.15", yt.Version())
	}
	rec = apiReq(t, h, http.MethodPost, "/api/admin/ytdlp/rollback", admin, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second rollback: got %d, want 409: %s", rec.Code, rec.Body.String())
	}

	// Settings: valid values are persisted, invalid ones are 400.
	rec = apiReq(t, h, http.MethodPut, "/api/admin/ytdlp/settings", admin,
		[]byte(`{"auto_update": false, "interval_hours": 72, "channel": "nightly"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("settings: got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if st["auto_update"] != false || st["interval_hours"] != float64(72) || st["channel"] != "nightly" {
		t.Fatalf("settings echo = %v/%v/%v, want false/72/nightly", st["auto_update"], st["interval_hours"], st["channel"])
	}
	for _, body := range []string{
		`{"auto_update": true, "interval_hours": 5, "channel": "stable"}`,
		`{"auto_update": true, "interval_hours": 169, "channel": "stable"}`,
		`{"auto_update": true, "interval_hours": 24, "channel": "banana"}`,
		`not json`,
	} {
		rec = apiReq(t, h, http.MethodPut, "/api/admin/ytdlp/settings", admin, []byte(body))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("settings %s: got %d, want 400: %s", body, rec.Code, rec.Body.String())
		}
	}
}
