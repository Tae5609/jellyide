package ytdlp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"yt-scraper/internal/db"
	"yt-scraper/internal/jobs"
)

// releaseState is what the fake release server serves; all fields may be
// swapped at runtime by the tests (under mu, which the handler holds too).
type releaseState struct {
	mu      sync.Mutex
	tag     string // reported by the releases/latest endpoint
	script  string // bytes served as the "binary"
	sums    string // served as SHA2-256SUMS
	binHits int64  // binary downloads served
	status  int    // status code for the binary download (200 by default)
}

// set swaps the served release atomically.
func (st *releaseState) set(tag, script, sums string) {
	st.mu.Lock()
	st.tag, st.script, st.sums = tag, script, sums
	st.mu.Unlock()
}

// binDownloadHits reports how many binary downloads were served.
func (st *releaseState) binDownloadHits() int64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.binHits
}

// newFakeReleaseServer serves a mutable fake of the GitHub release
// endpoints: releases/latest, the binary download and SHA2-256SUMS.
func newFakeReleaseServer(t *testing.T, st *releaseState) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		switch r.URL.Path {
		case "/api/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tag_name":"` + st.tag + `"}`))
		case "/download/yt-dlp":
			st.binHits++
			if st.status != 0 && st.status != http.StatusOK {
				http.Error(w, "nope", st.status)
				return
			}
			_, _ = w.Write([]byte(st.script))
		case "/download/SHA2-256SUMS":
			_, _ = w.Write([]byte(st.sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeBinary is an executable stand-in for the yt-dlp zipapp that prints
// the given version for any invocation (in particular --version).
func fakeBinary(version string) string {
	return "#!/bin/sh\necho \"" + version + "\"\n"
}

// newTestManager builds a managed Manager over a temp-dir database with its
// stable channel pointed at the fake release server serving st.
func newTestManager(t *testing.T, srv *httptest.Server) *Manager {
	t.Helper()
	dir := t.TempDir()
	database, err := db.NewDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	m := NewManager(database, jobs.NewManager(database), filepath.Join(dir, ".jellyide", "bin"), true)
	m.SetTestURLs(ChannelStable, ChannelURLs{
		Binary: srv.URL + "/download/yt-dlp",
		Sums:   srv.URL + "/download/SHA2-256SUMS",
		API:    srv.URL + "/api/releases/latest",
	})
	// The initial requests go to the fixed test URLs; no redirects are
	// exercised here, so the plain client (no CheckRedirect) is safe.
	m.client = &http.Client{}
	return m
}

// seedBinary writes a fake managed binary with the given version in place.
func seedBinary(t *testing.T, m *Manager, version string) {
	t.Helper()
	if err := os.MkdirAll(m.binDir, 0o700); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	if err := os.WriteFile(m.BinPath(), []byte(fakeBinary(version)), 0o755); err != nil {
		t.Fatalf("write seed %v", err)
	}
}

func TestParseManagedEnv(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"true", true},
		{"1", true},
		{"FALSE", false},
		{"0", false},
		{"no", false},
		{"off", false},
		{"banana", true},
	}
	for _, tt := range cases {
		if got := ParseManagedEnv(tt.in); got != tt.want {
			t.Errorf("ParseManagedEnv(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestPathManagedAndFallback(t *testing.T) {
	st := &releaseState{}
	m := newTestManager(t, newFakeReleaseServer(t, st))

	// No binary yet: fall back to yt-dlp on PATH.
	if got := m.Path(); got != "yt-dlp" {
		t.Fatalf("path without binary = %q, want yt-dlp", got)
	}
	seedBinary(t, m, "2025.1.15")
	if got := m.Path(); got != m.BinPath() {
		t.Fatalf("path with binary = %q, want the managed path", got)
	}
	if m.Version() != "2025.1.15" {
		t.Fatalf("Version() = %q, want 2025.1.15", m.Version())
	}

	// Unmanaged managers always report the system binary.
	unmanaged := NewManager(m.db, m.jm, m.binDir, false)
	if got := unmanaged.Path(); got != "yt-dlp" {
		t.Fatalf("unmanaged path = %q, want yt-dlp", got)
	}
	if got := unmanaged.BinPath(); got != m.BinPath() {
		t.Fatalf("unmanaged BinPath = %q, want the same location", got)
	}
}

func TestSettingsDefaultsAndRoundTrip(t *testing.T) {
	st := &releaseState{}
	m := newTestManager(t, newFakeReleaseServer(t, st))

	// Defaults on a fresh database.
	s, err := m.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	want := Settings{AutoUpdate: true, IntervalHours: 24, Channel: ChannelStable}
	if s != want {
		t.Fatalf("default settings = %+v, want %+v", s, want)
	}

	// Round trip.
	updated := Settings{AutoUpdate: false, IntervalHours: 72, Channel: ChannelNightly}
	if err := m.SetSettings(updated); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	got, err := m.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings after set: %v", err)
	}
	if got != updated {
		t.Fatalf("settings = %+v, want %+v", got, updated)
	}
}

func TestSettingsValidation(t *testing.T) {
	st := &releaseState{}
	m := newTestManager(t, newFakeReleaseServer(t, st))

	bad := []Settings{
		{AutoUpdate: true, IntervalHours: 5, Channel: ChannelStable},   // below 6
		{AutoUpdate: true, IntervalHours: 169, Channel: ChannelStable}, // above 168
		{AutoUpdate: true, IntervalHours: 24, Channel: "banana"},       // unknown channel
		{AutoUpdate: true, IntervalHours: 0, Channel: ChannelNightly},  // below 6
	}
	for _, s := range bad {
		if err := m.SetSettings(s); err == nil {
			t.Errorf("SetSettings(%+v) = nil, want a validation error", s)
		}
	}
	for _, s := range []Settings{
		{AutoUpdate: true, IntervalHours: 6, Channel: ChannelStable},
		{AutoUpdate: false, IntervalHours: 168, Channel: ChannelNightly},
	} {
		if err := m.SetSettings(s); err != nil {
			t.Errorf("SetSettings(%+v) = %v, want nil", s, err)
		}
	}
}

func TestStatusShape(t *testing.T) {
	st := &releaseState{}
	m := newTestManager(t, newFakeReleaseServer(t, st))
	seedBinary(t, m, "2025.1.15")

	// No check yet: latest is null and no update is advertised.
	s, err := m.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if s.Version != "2025.1.15" {
		t.Fatalf("version = %q, want 2025.1.15", s.Version)
	}
	if !s.Managed {
		t.Fatal("managed = false, want true")
	}
	if s.LatestVersion != nil {
		t.Fatalf("latest = %v, want null before any check", *s.LatestVersion)
	}
	if s.UpdateAvailable {
		t.Fatal("update_available = true without a known latest version")
	}
	if s.HasRollback {
		t.Fatal("has_rollback = true with no previous binary")
	}
	if s.Path != m.BinPath() {
		t.Fatalf("path = %q, want the managed path", s.Path)
	}

	// A stored newer release flags an available update.
	_ = m.setSetting(keyLatestVersion, "2025.2.2")
	_ = m.setSetting(keyLastCheck, "2025-02-02T00:00:00Z")
	_ = m.setSetting(keyLastResult, "ok")
	s, err = m.Status()
	if err != nil {
		t.Fatalf("Status after check: %v", err)
	}
	if s.LatestVersion == nil || *s.LatestVersion != "2025.2.2" {
		t.Fatalf("latest = %v, want 2025.2.2", s.LatestVersion)
	}
	if !s.UpdateAvailable {
		t.Fatal("update_available = false, want true (2025.1.15 < 2025.2.2)")
	}
	if s.LastCheck != "2025-02-02T00:00:00Z" || s.LastResult != "ok" {
		t.Fatalf("last check/result = %q/%q", s.LastCheck, s.LastResult)
	}

	// A rollback candidate flips has_rollback.
	if err := os.WriteFile(m.prevPath(), []byte(fakeBinary("2025.1.1")), 0o755); err != nil {
		t.Fatalf("write prev: %v", err)
	}
	s, _ = m.Status()
	if !s.HasRollback {
		t.Fatal("has_rollback = false with a parked previous binary")
	}
}
