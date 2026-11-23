package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestMediaHandlerAllowsAudioAndImages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Artist", "Album", "song.flac"), "audio")
	writeFile(t, filepath.Join(dir, "Artist", "Album", "cover.JPG"), "image")

	handler := newMediaHandler(dir)
	for _, path := range []string{"/Artist/Album/song.flac", "/Artist/Album/cover.JPG"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", path, rec.Code)
		}
	}
}

func TestMediaHandlerBlocksSensitiveAndUnknownFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"jellyide.db",
		"jellyide.db-wal",
		"jellyide.db-shm",
		"saml/metadata.xml",
		".env",
		".git/config",
		"notes.txt",
		"Artist/Album/song.exe",
	} {
		writeFile(t, filepath.Join(dir, name), "secret")
	}

	handler := newMediaHandler(dir)
	for _, path := range []string{
		"/jellyide.db",
		"/jellyide.db-wal",
		"/jellyide.db-shm",
		"/saml/metadata.xml",
		"/.env",
		"/.git/config",
		"/notes.txt",
		"/Artist/Album/song.exe",
		"/../escape.flac",
		"/Artist/../../escape.flac",
		"/missing.flac",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", path, rec.Code)
		}
	}
}

// TestMediaHandlerBlocksJellyideDir is the held T15 item: the managed
// yt-dlp binary lives in <musicDir>/.jellyide, and T12's dotfile rule must
// keep /media from serving it. The rule hides every path containing a
// dotfile segment behind a 404.
func TestMediaHandlerBlocksJellyideDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".jellyide", "bin", "yt-dlp"), "bin")
	writeFile(t, filepath.Join(dir, ".jellyide", "settings.json"), "{}")
	writeFile(t, filepath.Join(dir, "Artist", ".hidden", "cover.png"), "image")

	handler := newMediaHandler(dir)
	for _, path := range []string{
		"/.jellyide/bin/yt-dlp",
		"/.jellyide/settings.json",
		"/Artist/.hidden/cover.png",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", path, rec.Code)
		}
	}
}

func TestSPAHandler(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "<html>index</html>")
	writeFile(t, filepath.Join(dir, "static", "app.js"), "console.log(1)")

	handler := newSPAHandler(dir)

	tests := []struct {
		path string
		code int
		body string
	}{
		{"/", http.StatusOK, "<html>index</html>"},
		{"/library", http.StatusOK, "<html>index</html>"},
		{"/static/app.js", http.StatusOK, "console.log(1)"},
		{"/static/missing.js", http.StatusNotFound, ""},
		{"/missing.css", http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
		if rec.Code != tt.code {
			t.Fatalf("%s: status = %d, want %d", tt.path, rec.Code, tt.code)
		}
		if tt.body != "" && rec.Body.String() != tt.body {
			t.Fatalf("%s: body = %q, want %q", tt.path, rec.Body.String(), tt.body)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "same-origin",
		"Content-Security-Policy": contentSecurityPolicy,
	}
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Fatalf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestNewHTTPServerTimeouts(t *testing.T) {
	srv := newHTTPServer(":0", http.NewServeMux())
	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Fatalf("ReadHeaderTimeout = %v", srv.ReadHeaderTimeout)
	}
	if srv.ReadTimeout != 30*time.Second {
		t.Fatalf("ReadTimeout = %v", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %v, want 0 for media streaming", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 120*time.Second {
		t.Fatalf("IdleTimeout = %v", srv.IdleTimeout)
	}
}
