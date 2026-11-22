package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"yt-scraper/internal/db"
	"yt-scraper/internal/download"
	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
	"yt-scraper/internal/metadata"
	"yt-scraper/internal/musicbrainz"
	"yt-scraper/internal/scanner"
)

// noOpTagWriter implements metadata.TagWriter without touching Python or
// real audio files: every call succeeds.
type noOpTagWriter struct{}

func (noOpTagWriter) WriteFields(string, map[string]interface{}) error { return nil }
func (noOpTagWriter) EmbedCover(string, string) error                  { return nil }
func (noOpTagWriter) ExtractCover(string, string) (bool, error)        { return false, nil }

// newTestRouter wires the API the same way main.go does, backed by a
// temp-dir SQLite database and an empty music dir. It returns the handler,
// the downloader, the library service, the job manager and the temp music
// dir for seeding and assertions.
func newTestRouter(t *testing.T) (http.Handler, *download.Downloader, *library.Service, *jobs.Manager, string) {
	return newTestRouterWithMB(t, nil)
}

// newTestRouterWithMB is newTestRouter with an optional MusicBrainz
// client override (pointing at an httptest server in tests).
func newTestRouterWithMB(t *testing.T, mb *musicbrainz.Client) (http.Handler, *download.Downloader, *library.Service, *jobs.Manager, string) {
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
	deps := metadata.Deps{
		DB:      database,
		Library: libService,
		Jobs:    jobManager,
		Tags:    noOpTagWriter{},
	}
	if mb != nil {
		deps.MusicBrainz = mb
	}
	metaService := metadata.NewServiceWith(deps)

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
	if err := lib.UpsertTrack(albumID, title, path, 120, 1000, library.TrackMeta{}); err != nil {
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

// --- T10: flexible metadata editing endpoints ---

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func multipartFile(t *testing.T, fieldName, filename string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	part, err := mw.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}
	return body, mw.FormDataContentType()
}

// setTrackMeta re-upserts a track by its stored path with new tag meta
// (the upsert updates the row in place).
func setTrackMeta(t *testing.T, lib *library.Service, albumID, trackID string, meta library.TrackMeta) error {
	t.Helper()
	track, err := lib.GetTrack(trackID)
	if err != nil {
		return err
	}
	return lib.UpsertTrack(albumID, track.Title, track.Path, track.Duration, track.Bitrate, meta)
}

func TestGetTrackEndpoint(t *testing.T) {
	h, _, lib, _, dir := newTestRouter(t)
	albumID, trackID := seedTrack(t, lib, dir, "Artist One", "Album One", "Track 1")
	_ = albumID

	rec := get(t, h, "/api/library/tracks/"+trackID)
	if rec.Code != http.StatusOK {
		t.Fatalf("get track: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var track library.Track
	if err := json.Unmarshal(rec.Body.Bytes(), &track); err != nil {
		t.Fatalf("decode track: %v", err)
	}
	if track.ID != trackID || track.Title != "Track 1" {
		t.Fatalf("track = %+v", track)
	}
	// New fields are present (null when unset).
	var raw map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	for _, key := range []string{"artist", "album_artist", "track_number", "disc_number", "year", "genre", "composer", "comment"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("track JSON missing field %q: %v", key, raw)
		}
	}

	// Unknown track: 404.
	rec = get(t, h, "/api/library/tracks/does-not-exist")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown track: got %d, want 404", rec.Code)
	}
}

func TestPatchTracksAPI(t *testing.T) {
	h, _, lib, jm, dir := newTestRouter(t)
	_, trackID := seedTrack(t, lib, dir, "Artist One", "Album One", "Track 1")
	_, trackID2 := seedTrack(t, lib, dir, "Artist Two", "Album Two", "Track 2")

	// Valid bulk edit: 202 with job_id and change_set_id.
	body := fmtPatch(t, []map[string]interface{}{
		{"track_id": trackID, "fields": map[string]interface{}{"genre": "Rock", "year": float64(2021)}},
		{"track_id": trackID2, "fields": map[string]interface{}{"comment": nil}},
	})
	rec := patch(t, h, "/api/metadata/tracks", body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("patch: got %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var accepted struct {
		JobID       string `json:"job_id"`
		ChangeSetID string `json:"change_set_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode accepted: %v", err)
	}
	if accepted.JobID == "" || accepted.ChangeSetID == "" {
		t.Fatalf("accepted = %+v, want job_id and change_set_id", accepted)
	}

	// The job runs and lands in the DB.
	waitForAPIJob(t, jm, accepted.JobID)
	updated, err := lib.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	if updated.Genre == nil || *updated.Genre != "Rock" {
		t.Fatalf("genre = %v, want Rock", updated.Genre)
	}
	if updated.Year == nil || *updated.Year != 2021 {
		t.Fatalf("year = %v, want 2021", updated.Year)
	}

	// Wrong type for an int field: 400 with the field error shape.
	rec = patch(t, h, "/api/metadata/tracks", fmtPatch(t, []map[string]interface{}{
		{"track_id": trackID, "fields": map[string]interface{}{"year": "2021"}},
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad year: got %d, want 400: %s", rec.Code, rec.Body.String())
	}
	var fieldErr map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &fieldErr); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if fieldErr["field"] != "year" || fieldErr["track_id"] != trackID || fieldErr["error"] == "" {
		t.Fatalf("error body = %v, want error/field/track_id", fieldErr)
	}

	// Unknown field: 400.
	rec = patch(t, h, "/api/metadata/tracks", fmtPatch(t, []map[string]interface{}{
		{"track_id": trackID, "fields": map[string]interface{}{"bogus": "x"}},
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: got %d, want 400", rec.Code)
	}

	// Unknown track: 404.
	rec = patch(t, h, "/api/metadata/tracks", fmtPatch(t, []map[string]interface{}{
		{"track_id": "missing", "fields": map[string]interface{}{"genre": "x"}},
	}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown track: got %d, want 404: %s", rec.Code, rec.Body.String())
	}

	// Empty edits: 400.
	rec = patch(t, h, "/api/metadata/tracks", `{"edits": []}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty edits: got %d, want 400", rec.Code)
	}

	// Year out of range: 400.
	rec = patch(t, h, "/api/metadata/tracks", fmtPatch(t, []map[string]interface{}{
		{"track_id": trackID, "fields": map[string]interface{}{"year": float64(10000)}},
	}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("year range: got %d, want 400", rec.Code)
	}
}

func fmtPatch(t *testing.T, edits []map[string]interface{}) string {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{"edits": edits})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(body)
}

func patch(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// waitForAPIJob polls a job until it is completed or failed.
func waitForAPIJob(t *testing.T, jm *jobs.Manager, jobID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, err := jm.GetJob(jobID)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if job.Status == jobs.StatusCompleted || job.Status == jobs.StatusFailed {
			if job.Status == jobs.StatusFailed {
				t.Fatalf("job %s failed: %s", jobID, job.Error)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish in time", jobID)
}

func TestPreviewFieldAPI(t *testing.T) {
	h, _, lib, _, dir := newTestRouter(t)
	albumID, trackID := seedTrack(t, lib, dir, "Artist One", "Album One", "Song X")
	_ = albumID
	rock := "Rock"
	if err := setTrackMeta(t, lib, albumID, trackID, library.TrackMeta{Genre: &rock}); err != nil {
		t.Fatalf("seed genre: %v", err)
	}

	// Ops apply to the selected field.
	rec := postRaw(t, h, "/api/metadata/preview",
		fmt.Sprintf(`{"action":"replace","target":"Rock","value":"Jazz","track_ids":["%s"],"field":"genre"}`, trackID))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var previews []metadata.PreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &previews); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(previews) != 1 || previews[0].OldTitle != "Rock" || previews[0].NewTitle != "Jazz" {
		t.Fatalf("previews = %+v", previews)
	}

	// Default field is still title.
	rec = postRaw(t, h, "/api/metadata/preview",
		fmt.Sprintf(`{"action":"replace","target":"X","value":"Y","track_ids":["%s"]}`, trackID))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview default: got %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &previews); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(previews) != 1 || previews[0].OldTitle != "Song X" || previews[0].NewTitle != "Song Y" {
		t.Fatalf("previews = %+v, want title preview", previews)
	}

	// Invalid field: 400.
	rec = postRaw(t, h, "/api/metadata/preview",
		fmt.Sprintf(`{"action":"replace","target":"a","value":"b","track_ids":["%s"],"field":"track_number"}`, trackID))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid field: got %d, want 400", rec.Code)
	}
}

func TestCoverUploadAndGetAPI(t *testing.T) {
	h, _, lib, _, dir := newTestRouter(t)
	albumID, _ := seedTrack(t, lib, dir, "Artist One", "Album One", "Track 1")

	// No cover yet: 404.
	rec := get(t, h, "/api/library/albums/"+albumID+"/cover")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get cover (none): got %d, want 404", rec.Code)
	}

	// Upload a PNG.
	pngData := tinyPNG(t)
	body, contentType := multipartFile(t, "file", "cover.png", pngData)
	req := httptest.NewRequest(http.MethodPost, "/api/library/albums/"+albumID+"/cover", body)
	req.Header.Set("Content-Type", contentType)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("upload cover: got %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var uploaded struct {
		JobID string `json:"job_id"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	if uploaded.Path != "/media/Artist One/Album One/cover.png" {
		t.Fatalf("path = %q", uploaded.Path)
	}

	// The file is stored and served.
	if _, err := os.Stat(filepath.Join(dir, "Artist One", "Album One", "cover.png")); err != nil {
		t.Fatalf("stored cover missing: %v", err)
	}
	rec = get(t, h, "/api/library/albums/"+albumID+"/cover")
	if rec.Code != http.StatusOK {
		t.Fatalf("get cover: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content type = %q, want image/png", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), pngData) {
		t.Fatal("served cover differs from the upload")
	}
}

func TestCoverUploadValidationAPI(t *testing.T) {
	h, _, lib, _, dir := newTestRouter(t)
	albumID, _ := seedTrack(t, lib, dir, "Artist One", "Album One", "Track 1")

	gif := append([]byte("GIF89a"), bytes.Repeat([]byte{0}, 64)...)
	body, contentType := multipartFile(t, "file", "cover.gif", gif)
	req := httptest.NewRequest(http.MethodPost, "/api/library/albums/"+albumID+"/cover", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("gif upload: got %d, want 400: %s", rec.Code, rec.Body.String())
	}

	// Missing file field: 400.
	body, contentType = multipartFile(t, "wrong", "x.png", tinyPNG(t))
	req = httptest.NewRequest(http.MethodPost, "/api/library/albums/"+albumID+"/cover", body)
	req.Header.Set("Content-Type", contentType)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing field: got %d, want 400", rec.Code)
	}
}

func TestHistoryAndRevertAPI(t *testing.T) {
	h, _, lib, jm, dir := newTestRouter(t)
	albumID, trackID := seedTrack(t, lib, dir, "Artist One", "Album One", "Track 1")

	rec := patch(t, h, "/api/metadata/tracks", fmtPatch(t, []map[string]interface{}{
		{"track_id": trackID, "fields": map[string]interface{}{"genre": "Rock"}},
	}))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("patch: got %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var accepted struct {
		JobID       string `json:"job_id"`
		ChangeSetID string `json:"change_set_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode: %v", err)
	}
	waitForAPIJob(t, jm, accepted.JobID)

	// History: newest first, with the documented shape.
	rec = get(t, h, "/api/metadata/history?album_id="+albumID)
	if rec.Code != http.StatusOK {
		t.Fatalf("history: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var history []metadata.HistoryEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history = %d entries, want 1", len(history))
	}
	entry := history[0]
	if entry.ChangeSetID != accepted.ChangeSetID || entry.TrackCount != 1 {
		t.Fatalf("entry = %+v", entry)
	}
	if len(entry.Changes) != 1 || entry.Changes[0].Field != "genre" || entry.Changes[0].TrackTitle != "Track 1" {
		t.Fatalf("changes = %+v", entry.Changes)
	}
	if entry.Changes[0].OldValue != nil || entry.Changes[0].NewValue == nil || *entry.Changes[0].NewValue != "Rock" {
		t.Fatalf("change values = %+v", entry.Changes[0])
	}

	// Revert: 202 with a new change set.
	req := httptest.NewRequest(http.MethodPost, "/api/metadata/history/"+accepted.ChangeSetID+"/revert", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("revert: got %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil {
		t.Fatalf("decode revert: %v", err)
	}
	if accepted.ChangeSetID == "" {
		t.Fatal("revert returned no change_set_id")
	}
	waitForAPIJob(t, jm, accepted.JobID)
	updated, _ := lib.GetTrack(trackID)
	if updated.Genre != nil {
		t.Fatalf("genre = %v, want nil after revert", updated.Genre)
	}

	// Reverting an unknown set: 404.
	req = httptest.NewRequest(http.MethodPost, "/api/metadata/history/nope/revert", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("revert unknown: got %d, want 404", rec.Code)
	}
}

func TestLookupAPI(t *testing.T) {
	const mbID = "release-mbid"
	mbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws/2/release":
			_, _ = w.Write([]byte(`{"releases": [
				{"id": "` + mbID + `", "score": 100, "title": "Album One", "date": "2001-02-03",
				 "track-count": 2, "artist-credit": [{"name": "Artist One"}]}]}`))
		case "/ws/2/release/" + mbID:
			_, _ = w.Write([]byte(`{
				"id": "` + mbID + `", "title": "Album One", "date": "2001-02-03",
				"artist-credit": [{"name": "Artist One"}],
				"tags": [{"name": "rock", "count": 5}],
				"media": [{"position": 1, "tracks": [
					{"number": "1", "title": "Track 1"},
					{"number": "2", "title": "Track 2"}
				]}]
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mbServer.Close()

	h, _, lib, _, dir := newTestRouterWithMB(t, musicbrainz.NewClient(mbServer.URL+"/ws/2"))
	albumID, trackID := seedTrack(t, lib, dir, "Artist One", "Album One", "Track 1")
	number := 1
	if err := setTrackMeta(t, lib, albumID, trackID, library.TrackMeta{TrackNumber: &number}); err != nil {
		t.Fatalf("seed number: %v", err)
	}

	// Search with the default query (album artist + title).
	rec := get(t, h, "/api/metadata/lookup/search?album_id="+albumID)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var results []musicbrainz.ReleaseSearchResult
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	if len(results) != 1 || results[0].MBID != mbID || results[0].Artist != "Artist One" {
		t.Fatalf("results = %+v", results)
	}

	// Release lookup: release doc + cover url + number-matched mapping.
	rec = get(t, h, "/api/metadata/lookup/release/"+mbID+"?album_id="+albumID)
	if rec.Code != http.StatusOK {
		t.Fatalf("release: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Release  map[string]interface{}     `json:"release"`
		CoverURL string                     `json:"cover_url"`
		Mapping  []musicbrainz.MappingEntry `json:"mapping"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode release: %v", err)
	}
	if body.Release["title"] != "Album One" {
		t.Fatalf("release = %v", body.Release)
	}
	if body.CoverURL != "https://coverartarchive.org/release/"+mbID+"/front" {
		t.Fatalf("cover url = %q", body.CoverURL)
	}
	if len(body.Mapping) != 1 {
		t.Fatalf("mapping = %+v, want one entry", body.Mapping)
	}
	entry := body.Mapping[0]
	if entry.TrackID != trackID || entry.Confidence != 1 {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.Proposed["title"] != "Track 1" || entry.Proposed["year"] != float64(2001) {
		t.Fatalf("proposed = %v", entry.Proposed)
	}
	if entry.Current["track_number"] != float64(1) {
		t.Fatalf("current = %v", entry.Current)
	}

	// Missing album_id: 400.
	rec = get(t, h, "/api/metadata/lookup/release/"+mbID)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("release without album_id: got %d, want 400", rec.Code)
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
