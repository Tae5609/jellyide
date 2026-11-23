package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"yt-scraper/internal/auth"
	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
)

// waitForJobID polls the job until it leaves the queue (10 s deadline).
func waitForJobID(t *testing.T, jm *jobs.Manager, jobID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, err := jm.GetJob(jobID)
		if err == nil && (job.Status == jobs.StatusCompleted || job.Status == jobs.StatusFailed) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish in time", jobID)
}

// seedConflictForAPI creates an open conflict row for the track.
func seedConflictForAPI(t *testing.T, lib *library.Service, trackID, field, dbValue, fileValue string) string {
	t.Helper()
	id, err := lib.InsertConflict(trackID, field, dbValue, fileValue, library.ConflictSourceScan)
	if err != nil {
		t.Fatalf("InsertConflict: %v", err)
	}
	return id
}

func doMethod(t *testing.T, h http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func deleteRaw(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	return doMethod(t, h, http.MethodDelete, path, nil)
}

func putRaw(t *testing.T, h http.Handler, path string, body []byte) *httptest.ResponseRecorder {
	return doMethod(t, h, http.MethodPut, path, body)
}

func TestSyncSummaryEndpoint(t *testing.T) {
	h, _, lib, _, dir := newTestRouter(t)
	_, trackID := seedTrack(t, lib, dir, "Artist", "Gone Album", "Gone")
	_, tid := seedTrack(t, lib, dir, "Artist", "Album", "Title")
	seedConflictForAPI(t, lib, tid, "title", "a", "b")
	if err := lib.MarkMissing(trackID); err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}

	rec := get(t, h, "/api/sync/summary")
	if rec.Code != http.StatusOK {
		t.Fatalf("summary: got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		OpenConflicts int `json:"open_conflicts"`
		MissingTracks int `json:"missing_tracks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.OpenConflicts != 1 || body.MissingTracks != 1 {
		t.Errorf("summary = %+v, want {1 1}", body)
	}
}

func TestSyncConflictsEndpoint(t *testing.T) {
	h, _, lib, _, dir := newTestRouter(t)
	albumID, trackID := seedTrack(t, lib, dir, "Artist", "Album", "Title")
	seedConflictForAPI(t, lib, trackID, "title", "a", "b")
	seedConflictForAPI(t, lib, trackID, "year", "1", "2")

	rec := get(t, h, "/api/sync/conflicts")
	if rec.Code != http.StatusOK {
		t.Fatalf("conflicts: got %d: %s", rec.Code, rec.Body.String())
	}
	var groups []struct {
		TrackID string `json:"track_id"`
		AlbumID string `json:"album_id"`
		Fields  []struct {
			ID         string `json:"id"`
			ConflictID string `json:"conflict_id"`
			Field      string `json:"field"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &groups); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(groups) != 1 || groups[0].TrackID != trackID || len(groups[0].Fields) != 2 {
		t.Fatalf("groups = %+v, want one group with two fields", groups)
	}
	for _, f := range groups[0].Fields {
		if f.ConflictID == "" || f.ConflictID != f.ID {
			t.Errorf("field %s: conflict_id %q, want %q", f.Field, f.ConflictID, f.ID)
		}
	}

	// Album filter.
	rec = get(t, h, "/api/sync/conflicts?album_id="+albumID)
	if rec.Code != http.StatusOK {
		t.Fatalf("conflicts(album): got %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &groups); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(groups) != 1 {
		t.Errorf("album filter = %d groups, want 1", len(groups))
	}

	// Invalid status.
	rec = get(t, h, "/api/sync/conflicts?status=banana")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad status: got %d, want 400", rec.Code)
	}

	// Dismissed filter is empty.
	rec = get(t, h, "/api/sync/conflicts?status=dismissed")
	if rec.Code != http.StatusOK {
		t.Fatalf("conflicts(dismissed): got %d", rec.Code)
	}
	var dismissed []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &dismissed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(dismissed) != 0 {
		t.Errorf("dismissed = %d groups, want 0", len(dismissed))
	}
}

func TestSyncResolveEndpoint(t *testing.T) {
	h, _, lib, jm, dir := newTestRouter(t)
	_, trackID := seedTrack(t, lib, dir, "Artist", "Album", "Old Title")
	cid := seedConflictForAPI(t, lib, trackID, "title", "Old Title", "New Title")

	// Bad payloads.
	rec := postRaw(t, h, "/api/sync/conflicts/resolve", `{"resolutions":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty resolutions: got %d, want 400", rec.Code)
	}
	rec = postRaw(t, h, "/api/sync/conflicts/resolve", `{"resolutions":[{"conflict_id":"`+cid+`","choice":"banana"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad choice: got %d, want 400", rec.Code)
	}
	rec = postRaw(t, h, "/api/sync/conflicts/resolve", `{"resolutions":[{"conflict_id":"nope","choice":"file"}]}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown conflict: got %d, want 404", rec.Code)
	}

	// Good payload: the file choice updates the DB row.
	rec = postRaw(t, h, "/api/sync/conflicts/resolve", `{"resolutions":[{"conflict_id":"`+cid+`","choice":"file"}]}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("resolve: got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["job_id"] == "" {
		t.Fatal("resolve: no job_id")
	}
	// Wait for the job (the resolution runs asynchronously).
	waitForJobID(t, jm, body["job_id"])

	track, err := lib.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	if track.Title != "New Title" {
		t.Errorf("title = %q, want New Title", track.Title)
	}
	c, err := lib.GetConflict(cid)
	if err != nil {
		t.Fatalf("GetConflict: %v", err)
	}
	if c.Status != library.ConflictStatusResolved {
		t.Errorf("conflict status = %s, want resolved", c.Status)
	}

	// Resolving a closed conflict is a conflict.
	rec = postRaw(t, h, "/api/sync/conflicts/resolve", `{"resolutions":[{"conflict_id":"`+cid+`","choice":"file"}]}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("resolve closed: got %d, want 409", rec.Code)
	}
}

func TestSyncResolveAllEndpoint(t *testing.T) {
	h, _, lib, jm, dir := newTestRouter(t)
	_, trackID := seedTrack(t, lib, dir, "Artist", "Album", "Title")
	seedConflictForAPI(t, lib, trackID, "title", "a", "b")

	rec := postRaw(t, h, "/api/sync/conflicts/resolve-all", `{"choice":"banana"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad choice: got %d, want 400", rec.Code)
	}
	rec = postRaw(t, h, "/api/sync/conflicts/resolve-all", `{"choice":"file"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("resolve-all: got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	waitForJobID(t, jm, body["job_id"])
	// Nothing left to resolve.
	rec = postRaw(t, h, "/api/sync/conflicts/resolve-all", `{"choice":"file"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("resolve-all empty: got %d, want 400", rec.Code)
	}
}

func TestSyncDismissEndpoint(t *testing.T) {
	h, _, lib, _, dir := newTestRouter(t)
	_, trackID := seedTrack(t, lib, dir, "Artist", "Album", "Title")
	cid := seedConflictForAPI(t, lib, trackID, "title", "a", "b")

	rec := postRaw(t, h, "/api/sync/conflicts/"+cid+"/dismiss", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("dismiss: got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != library.ConflictStatusDismissed {
		t.Errorf("status = %q, want dismissed", body["status"])
	}

	// Dismissing again is a conflict; dismissing unknown is a 404.
	rec = postRaw(t, h, "/api/sync/conflicts/"+cid+"/dismiss", "")
	if rec.Code != http.StatusConflict {
		t.Errorf("dismiss closed: got %d, want 409", rec.Code)
	}
	rec = postRaw(t, h, "/api/sync/conflicts/nope/dismiss", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("dismiss unknown: got %d, want 404", rec.Code)
	}
}

// seedMissingPair creates a missing track (no file on disk) and a matching
// candidate row with the same stored fingerprint and a live file, and
// returns both ids and the candidate path.
func seedMissingPair(t *testing.T, lib *library.Service, dir, suffix string) (string, string, string) {
	t.Helper()
	_, missingID := seedTrack(t, lib, dir, "Artist", "Missing Album"+suffix, "Title")
	_, candID := seedTrack(t, lib, dir, "Artist", "Copy Album"+suffix, "Title Copy")

	// seedTrack creates rows only: the missing track's file never exists,
	// the candidate's must (the candidate filter stats it).
	cand, err := lib.GetTrack(candID)
	if err != nil {
		t.Fatalf("GetTrack cand: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cand.Path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cand.Path, []byte("audio"), 0o600); err != nil {
		t.Fatalf("write cand file: %v", err)
	}
	if err := lib.MarkMissing(missingID); err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}
	// Same fingerprint on both rows (identical file content).
	if err := lib.SetSyncState(candID, 10, 0, "hash-same", library.NowSQL(), library.TrackStateOK, false); err != nil {
		t.Fatalf("SetSyncState: %v", err)
	}
	if err := lib.SetSyncState(missingID, 10, 0, "hash-same", library.NowSQL(), library.TrackStateMissing, false); err != nil {
		t.Fatalf("SetSyncState: %v", err)
	}
	return missingID, candID, cand.Path
}

func TestSyncMissingEndpoints(t *testing.T) {
	h, _, lib, _, dir := newTestRouter(t)
	missingID, candID, candPath := seedMissingPair(t, lib, dir, "")
	_ = candID

	rec := get(t, h, "/api/sync/missing")
	if rec.Code != http.StatusOK {
		t.Fatalf("missing: got %d: %s", rec.Code, rec.Body.String())
	}
	var missing []struct {
		TrackID    string `json:"track_id"`
		Candidates []struct {
			TrackID string `json:"track_id"`
			Path    string `json:"path"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &missing); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(missing) != 1 || len(missing[0].Candidates) != 1 {
		t.Fatalf("missing = %+v, want one entry with one candidate", missing)
	}

	// Relink re-points the missing row at the candidate file.
	rec = postRaw(t, h, "/api/sync/missing/"+missingID+"/relink", `{"path":"`+candPath+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("relink: got %d: %s", rec.Code, rec.Body.String())
	}
	var track map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &track); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if track["id"] != missingID {
		t.Errorf("relinked id = %v, want %s (kept)", track["id"], missingID)
	}
	if track["state"] != library.TrackStateOK {
		t.Errorf("state = %v, want ok", track["state"])
	}

	// Relinking a non-missing track is a conflict; unknown is a 404.
	_, otherID := seedTrack(t, lib, dir, "Artist", "Album", "Another")
	rec = postRaw(t, h, "/api/sync/missing/"+otherID+"/relink", `{"path":"`+candPath+`"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("relink non-missing: got %d, want 409", rec.Code)
	}
	rec = postRaw(t, h, "/api/sync/missing/nope/relink", `{"path":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("relink unknown: got %d, want 404", rec.Code)
	}

	// Deleting a non-missing row is a conflict; a missing row goes away.
	rec = deleteRaw(t, h, "/api/sync/missing/"+otherID)
	if rec.Code != http.StatusConflict {
		t.Errorf("delete non-missing: got %d, want 409", rec.Code)
	}
	missingID2, _, _ := seedMissingPair(t, lib, dir, "2")
	rec = deleteRaw(t, h, "/api/sync/missing/"+missingID2)
	if rec.Code != http.StatusOK {
		t.Errorf("delete missing: got %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := lib.GetTrack(missingID2); err == nil {
		t.Error("track row still exists after delete")
	}
}

func TestSyncTrashEndpoints(t *testing.T) {
	h, _, lib, _, dir := newTestRouter(t)
	src := filepath.Join(dir, "displaced.mp3")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	id, err := lib.TrashFile(src, "")
	if err != nil {
		t.Fatalf("TrashFile: %v", err)
	}

	rec := get(t, h, "/api/sync/trash")
	if rec.Code != http.StatusOK {
		t.Fatalf("trash: got %d: %s", rec.Code, rec.Body.String())
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("trash = %d entries, want 1", len(entries))
	}

	rec = postRaw(t, h, "/api/sync/trash/"+id+"/restore", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: got %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("restored file missing: %v", err)
	}

	// Restore of an unknown entry is a 404.
	rec = postRaw(t, h, "/api/sync/trash/nope/restore", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("restore unknown: got %d, want 404", rec.Code)
	}
}

func TestSyncPolicyEndpoints(t *testing.T) {
	t.Setenv("SYNC_CONFLICT_POLICY", "")
	h, _, _, _, _ := newTestRouter(t)

	rec := get(t, h, "/api/admin/settings/sync")
	if rec.Code != http.StatusOK {
		t.Fatalf("get policy: got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["policy"] != library.SyncPolicyAsk {
		t.Errorf("policy = %q, want ask", body["policy"])
	}

	rec = putRaw(t, h, "/api/admin/settings/sync", []byte(`{"policy":"db_wins"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("put policy: got %d: %s", rec.Code, rec.Body.String())
	}
	rec = get(t, h, "/api/admin/settings/sync")
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["policy"] != library.SyncPolicyDBWins {
		t.Errorf("policy = %q, want db_wins", body["policy"])
	}

	rec = putRaw(t, h, "/api/admin/settings/sync", []byte(`{"policy":"banana"}`))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("put bad policy: got %d, want 400", rec.Code)
	}
}

func TestSyncPolicyAdminGate(t *testing.T) {
	if !syncPolicyAdminAllowed(nil) {
		t.Error("auth disabled (no user) must be allowed")
	}
	if syncPolicyAdminAllowed(&auth.User{Username: "dev", IsAdmin: false}) {
		t.Error("non-admin must be forbidden")
	}
	if !syncPolicyAdminAllowed(&auth.User{Username: "root", IsAdmin: true}) {
		t.Error("admin must be allowed")
	}
}

func TestRenameCollisionsEndpoint(t *testing.T) {
	h, _, lib, _, dir := newTestRouter(t)
	_, trackID := seedTrack(t, lib, dir, "Artist", "Album", "Title")
	track, err := lib.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	// A file on disk already holds the target name in the same folder.
	if err := os.WriteFile(filepath.Join(filepath.Dir(track.Path), "Taken.flac"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	edits := `{"edits":[{"track_id":"` + trackID + `","fields":{"title":"Taken"}}]}`
	rec := postRaw(t, h, "/api/metadata/rename-collisions", edits)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename-collisions: got %d: %s", rec.Code, rec.Body.String())
	}
	var reports []struct {
		TrackID      string `json:"track_id"`
		Collides     bool   `json:"collides"`
		ExistingName string `json:"existing_name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reports); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(reports) != 1 || reports[0].TrackID != trackID || !reports[0].Collides {
		t.Errorf("reports = %+v, want one colliding report for %s", reports, trackID)
	}
}
