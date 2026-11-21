package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"yt-scraper/internal/db"
	"yt-scraper/internal/download"
	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
	"yt-scraper/internal/metadata"
	"yt-scraper/internal/scanner"
)

// newTestRouter wires the API the same way main.go does, backed by a
// temp-dir SQLite database and an empty music dir. It returns the handler,
// the downloader, the library service, the job manager and the temp music
// dir for seeding and assertions.
func newTestRouter(t *testing.T) (http.Handler, *download.Downloader, *library.Service, *jobs.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.NewDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	libService := library.NewService(database, dir)
	jobManager := jobs.NewManager(database)
	musicScanner := scanner.NewScanner(libService, jobManager, dir)
	downloader := download.NewDownloader(jobManager, musicScanner, dir)
	metaService := metadata.NewService(database, libService)

	r := mux.NewRouter()
	apiSub := r.PathPrefix("/api").Subrouter()
	RegisterRoutes(apiSub, downloader, libService, metaService, jobManager, musicScanner)
	return r, downloader, libService, jobManager, dir
}

// seedTrack creates artist, album and one track row (plus the on-disk dir)
// and returns the album and track ids.
func seedTrack(t *testing.T, lib *library.Service, dir, artist, album, title string) (string, string) {
	t.Helper()
	artistID, err := lib.EnsureArtist(artist)
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumID, err := lib.EnsureAlbum(artistID, album)
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	path := filepath.Join(dir, artist, album, title+".flac")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir track dir: %v", err)
	}
	if err := lib.UpsertTrack(albumID, title, path, 120, 1000); err != nil {
		t.Fatalf("UpsertTrack: %v", err)
	}
	tracks, err := lib.GetTracksByAlbum(albumID)
	if err != nil {
		t.Fatalf("GetTracksByAlbum: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("tracks = %d, want 1", len(tracks))
	}
	return albumID, tracks[0].ID
}

// postRaw sends a POST with a raw body and returns the recorder.
func postRaw(t *testing.T, h http.Handler, path, raw string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(raw))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestGetInfoMissingURL(t *testing.T) {
	h, _, _, _, _ := newTestRouter(t)

	rec := get(t, h, "/api/info")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing url: got %d, want 400: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "missing url or id" {
		t.Fatalf("error = %q, want %q", body["error"], "missing url or id")
	}
}

func TestQueueDownloadsInvalidJSON(t *testing.T) {
	h, _, _, _, _ := newTestRouter(t)

	rec := postRaw(t, h, "/api/queue", "{not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: got %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestQueueDownloadsEmpty(t *testing.T) {
	h, _, _, _, _ := newTestRouter(t)

	rec := postRaw(t, h, "/api/queue", "[]")
	if rec.Code != http.StatusOK {
		t.Fatalf("empty queue: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["queued"] != float64(0) {
		t.Fatalf("queued = %v, want 0", body["queued"])
	}
}

func TestPreviewMetadataInvalidJSON(t *testing.T) {
	h, _, _, _, _ := newTestRouter(t)

	rec := postRaw(t, h, "/api/metadata/preview", "not json at all")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: got %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestRenameAlbumInvalidJSON(t *testing.T) {
	h, _, _, _, _ := newTestRouter(t)

	rec := postRaw(t, h, "/api/library/albums/some-id/rename", "nope")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: got %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestSetConfigInvalidJSON is a regression test: previously the decode error
// was ignored, so a bad body silently reset the limit to 0 and the proxy to
// "". Now it is rejected and the previous config is kept.
func TestSetConfigInvalidJSON(t *testing.T) {
	h, d, _, _, _ := newTestRouter(t)
	d.SetLimit(3)
	d.SetProxy("http://proxy.example:8080")

	rec := postRaw(t, h, "/api/config", "{broken")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json: got %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if got := d.GetLimit(); got != 3 {
		t.Fatalf("limit after bad body = %d, want 3 (must be untouched)", got)
	}
	if got := d.GetProxy(); got != "http://proxy.example:8080" {
		t.Fatalf("proxy after bad body = %q, want previous value", got)
	}
}

func TestSetConfigValid(t *testing.T) {
	h, d, _, _, _ := newTestRouter(t)

	rec := postRaw(t, h, "/api/config", `{"concurrent_limit": 5, "proxy": "http://p:1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set config: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := d.GetLimit(); got != 5 {
		t.Fatalf("limit = %d, want 5", got)
	}
	if got := d.GetProxy(); got != "http://p:1" {
		t.Fatalf("proxy = %q, want %q", got, "http://p:1")
	}
}

func TestGetConfig(t *testing.T) {
	h, d, _, _, _ := newTestRouter(t)
	d.SetLimit(7)

	rec := get(t, h, "/api/config")
	if rec.Code != http.StatusOK {
		t.Fatalf("get config: got %d, want 200", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["concurrent_limit"] != float64(7) {
		t.Fatalf("concurrent_limit = %v, want 7", body["concurrent_limit"])
	}
}

func TestGetJobsEmpty(t *testing.T) {
	h, _, _, _, _ := newTestRouter(t)

	rec := get(t, h, "/api/jobs")
	if rec.Code != http.StatusOK {
		t.Fatalf("get jobs: got %d, want 200", rec.Code)
	}
	var body []*jobs.Job
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body) != 0 {
		t.Fatalf("jobs = %d entries, want 0", len(body))
	}
}

func TestClearJobs(t *testing.T) {
	h, _, _, _, _ := newTestRouter(t)

	rec := postRaw(t, h, "/api/jobs/clear", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("clear jobs: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestLibraryReadEndpoints(t *testing.T) {
	h, _, lib, _, dir := newTestRouter(t)

	albumID, _ := seedTrack(t, lib, dir, "Artist One", "Album One", "Track 1")

	// Artists.
	rec := get(t, h, "/api/library/artists")
	if rec.Code != http.StatusOK {
		t.Fatalf("get artists: got %d, want 200", rec.Code)
	}
	var artists []library.Artist
	if err := json.Unmarshal(rec.Body.Bytes(), &artists); err != nil {
		t.Fatalf("decode artists: %v", err)
	}
	if len(artists) != 1 || artists[0].Name != "Artist One" {
		t.Fatalf("artists = %+v, want one 'Artist One'", artists)
	}

	// Albums of the artist (resolve the id from the track's album row).
	album, err := lib.GetAlbum(albumID)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	rec = get(t, h, "/api/library/artists/"+album.ArtistID+"/albums")
	if rec.Code != http.StatusOK {
		t.Fatalf("get albums: got %d, want 200", rec.Code)
	}
	var albums []library.Album
	if err := json.Unmarshal(rec.Body.Bytes(), &albums); err != nil {
		t.Fatalf("decode albums: %v", err)
	}
	if len(albums) != 1 || albums[0].Title != "Album One" {
		t.Fatalf("albums = %+v, want one 'Album One'", albums)
	}

	// Tracks of the album.
	rec = get(t, h, "/api/library/albums/"+albumID+"/tracks")
	if rec.Code != http.StatusOK {
		t.Fatalf("get tracks: got %d, want 200", rec.Code)
	}
	var tracks []library.Track
	if err := json.Unmarshal(rec.Body.Bytes(), &tracks); err != nil {
		t.Fatalf("decode tracks: %v", err)
	}
	if len(tracks) != 1 || tracks[0].Title != "Track 1" {
		t.Fatalf("tracks = %+v, want one 'Track 1'", tracks)
	}

	// Single album.
	rec = get(t, h, "/api/library/albums/"+albumID)
	if rec.Code != http.StatusOK {
		t.Fatalf("get album: got %d, want 200", rec.Code)
	}
	var one library.Album
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
		t.Fatalf("decode album: %v", err)
	}
	if one.Title != "Album One" || one.Artist != "Artist One" {
		t.Fatalf("album = %+v", one)
	}

	// Unknown album: 404.
	rec = get(t, h, "/api/library/albums/does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown album: got %d, want 404", rec.Code)
	}
}

func TestRenameAndDeleteAlbum(t *testing.T) {
	h, _, lib, _, dir := newTestRouter(t)
	albumID, _ := seedTrack(t, lib, dir, "Artist One", "Old Name", "Track 1")

	// Rename.
	rec := postRaw(t, h, "/api/library/albums/"+albumID+"/rename", `{"title": "New Name"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	album, err := lib.GetAlbum(albumID)
	if err != nil {
		t.Fatalf("GetAlbum after rename: %v", err)
	}
	if album.Title != "New Name" {
		t.Fatalf("album title = %q, want %q", album.Title, "New Name")
	}
	if _, err := os.Stat(filepath.Join(dir, "Artist One", "New Name")); err != nil {
		t.Fatalf("renamed dir missing: %v", err)
	}

	// Delete the track, then the album.
	tracks, err := lib.GetTracksByAlbum(albumID)
	if err != nil {
		t.Fatalf("GetTracksByAlbum: %v", err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/library/tracks/"+tracks[0].ID, nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("delete track: got %d, want 200", rec2.Code)
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/library/albums/"+albumID, nil)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req)
	if rec3.Code != http.StatusOK {
		t.Fatalf("delete album: got %d, want 200", rec3.Code)
	}
	if _, err := lib.GetAlbum(albumID); err == nil {
		t.Fatal("album still exists after delete")
	}
}

func TestTriggerScanEmptyDir(t *testing.T) {
	h, _, _, jm, _ := newTestRouter(t)

	rec := postRaw(t, h, "/api/library/scan", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("scan: got %d, want 202: %s", rec.Code, rec.Body.String())
	}
	// The scan job is persisted (created synchronously by the scanner).
	all, err := jm.GetAllJobs()
	if err != nil {
		t.Fatalf("GetAllJobs: %v", err)
	}
	found := false
	for _, j := range all {
		if j.Type == jobs.TypeScan {
			found = true
		}
	}
	if !found {
		t.Fatal("no scan job was created")
	}
}

func TestMetadataAnalyzeAndPreview(t *testing.T) {
	h, _, lib, _, dir := newTestRouter(t)
	albumID, trackID := seedTrack(t, lib, dir, "Artist One", "Album One", "Shared Prefix Song")

	// Analyze.
	rec := get(t, h, "/api/metadata/analyze/"+albumID)
	if rec.Code != http.StatusOK {
		t.Fatalf("analyze: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var analysis metadata.AnalysisResult
	if err := json.Unmarshal(rec.Body.Bytes(), &analysis); err != nil {
		t.Fatalf("decode analysis: %v", err)
	}

	// Preview a replace operation.
	body := `{"action":"replace","target":"Shared","value":"Common","track_ids":["` + trackID + `"]}`
	rec = postRaw(t, h, "/api/metadata/preview", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var previews []metadata.PreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &previews); err != nil {
		t.Fatalf("decode previews: %v", err)
	}
	if len(previews) != 1 || previews[0].NewTitle != "Common Prefix Song" {
		t.Fatalf("previews = %+v, want one renamed to 'Common Prefix Song'", previews)
	}

	// Apply an empty change list.
	rec = postRaw(t, h, "/api/metadata/apply", "[]")
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: got %d, want 200", rec.Code)
	}
}

// TestInfoArgsProtectsURL verifies the option-injection fix: the URL is
// always the last argument, preceded by the `--` end-of-options separator,
// no matter how the user input starts.
func TestInfoArgsProtectsURL(t *testing.T) {
	for _, url := range []string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://www.youtube.com/playlist?list=PL123",
		"-exec=touch /tmp/pwned",
		"--extractor-args=evil",
		"dQw4w9WgXcQ",
	} {
		args := infoArgs(url)
		if len(args) != 6 {
			t.Fatalf("infoArgs(%q) has %d args, want 6: %v", url, len(args), args)
		}
		if args[4] != "--" {
			t.Fatalf("infoArgs(%q)[4] = %q, want %q", url, args[4], "--")
		}
		if args[5] != url {
			t.Fatalf("infoArgs(%q)[5] = %q, want the url verbatim", url, args[5])
		}
	}
}
