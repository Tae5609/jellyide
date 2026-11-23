package syncconf

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"yt-scraper/internal/db"
	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
)

// fakeTagWriter records WriteFields calls per path and can fail on
// demand.
type fakeTagWriter struct {
	written  map[string]map[string]interface{}
	writeErr error
}

func (f *fakeTagWriter) WriteFields(path string, fields map[string]interface{}) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	if f.written == nil {
		f.written = map[string]map[string]interface{}{}
	}
	stored := map[string]interface{}{}
	for k, v := range fields {
		stored[k] = v
	}
	f.written[path] = stored
	return nil
}

// newTestService wires a syncconf service on a temp-dir database. The
// raw handle is returned for test-only SQL (e.g. backdating trash rows).
func newTestService(t *testing.T) (*Service, *library.Service, *jobs.Manager, *fakeTagWriter, string, *db.DB) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.NewDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	lib := library.NewService(database, dir)
	jm := jobs.NewManager(database)
	tw := &fakeTagWriter{}
	return NewService(lib, jm, tw), lib, jm, tw, dir, database
}

// seedTrack creates artist, album, a track row and the on-disk file, and
// returns the track id and the file path.
func seedTrack(t *testing.T, lib *library.Service, dir, artist, album, title string, trackNumber int) (string, string) {
	t.Helper()
	artistID, err := lib.EnsureArtist(artist)
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumID, err := lib.EnsureAlbum(artistID, album)
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	path := filepath.Join(dir, artist, album, title+".mp3")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("fake audio"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	artistCopy := artist
	meta := library.TrackMeta{Artist: &artistCopy}
	if trackNumber > 0 {
		n := trackNumber
		meta.TrackNumber = &n
	}
	if err := lib.CreateTrack(albumID, title, path, 200, 320, meta, st.Size(), st.ModTime().Unix(), "hash-"+title); err != nil {
		t.Fatalf("CreateTrack: %v", err)
	}
	track, err := lib.GetTrackByPath(path)
	if err != nil {
		t.Fatalf("GetTrackByPath: %v", err)
	}
	return track.ID, path
}

// waitForJob polls the job until it leaves the queue (10 s deadline).
func waitForJob(t *testing.T, jm *jobs.Manager, jobID string) *jobs.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, err := jm.GetJob(jobID)
		if err == nil && (job.Status == jobs.StatusCompleted || job.Status == jobs.StatusFailed) {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish in time", jobID)
	return nil
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// seedConflict creates one open conflict row for the track.
func seedConflict(t *testing.T, lib *library.Service, trackID, field, dbValue, fileValue string) string {
	t.Helper()
	id, err := lib.InsertConflict(trackID, field, dbValue, fileValue, library.ConflictSourceScan)
	if err != nil {
		t.Fatalf("InsertConflict: %v", err)
	}
	return id
}

func TestResolveFileChoiceUpdatesDB(t *testing.T) {
	svc, lib, jm, tw, dir, database := newTestService(t)
	trackID, path := seedTrack(t, lib, dir, "Artist", "Album", "Old Title", 1)
	cid := seedConflict(t, lib, trackID, "title", "Old Title", "New Title")

	jobID, err := svc.Resolve([]Resolution{{ConflictID: cid, Choice: ChoiceFile}}, "tester")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	job := waitForJob(t, jm, jobID)
	if job.Status != jobs.StatusCompleted {
		t.Fatalf("job status = %s, error = %s", job.Status, job.Error)
	}

	track, err := lib.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	if track.Title != "New Title" {
		t.Errorf("title = %q, want New Title", track.Title)
	}
	if track.Version != 2 {
		t.Errorf("version = %d, want 2 (bumped once)", track.Version)
	}
	if track.State != library.TrackStateOK {
		t.Errorf("state = %s, want ok", track.State)
	}
	// The file already carried the value: no tag write for 'file'.
	if _, ok := tw.written[path]; ok || len(tw.written) != 0 {
		t.Errorf("unexpected tag writes: %v", tw.written)
	}

	c, err := lib.GetConflict(cid)
	if err != nil {
		t.Fatalf("GetConflict: %v", err)
	}
	if c.Status != library.ConflictStatusResolved || c.Resolution == nil || *c.Resolution != ChoiceFile {
		t.Errorf("conflict = %s / %v, want resolved / file", c.Status, c.Resolution)
	}
	if c.ResolvedBy == nil || *c.ResolvedBy != "tester" {
		t.Errorf("resolved_by = %v, want tester", c.ResolvedBy)
	}

	// The resolution is recorded in the history (revertible).
	var hist []struct {
		Field    string  `db:"field"`
		OldValue *string `db:"old_value"`
		NewValue *string `db:"new_value"`
	}
	if err := database.Select(&hist, "SELECT field, old_value, new_value FROM metadata_changes WHERE track_id = ?", trackID); err != nil {
		t.Fatalf("history query: %v", err)
	}
	if len(hist) != 1 || hist[0].Field != "title" || derefStr(hist[0].OldValue) != "Old Title" || derefStr(hist[0].NewValue) != "New Title" {
		t.Errorf("history = %+v, want one title row Old Title -> New Title", hist)
	}

	// The per-track result landed in the job details.
	var details struct {
		Results []map[string]interface{} `json:"results"`
	}
	if err := json.Unmarshal([]byte(job.Details), &details); err != nil {
		t.Fatalf("job details: %v", err)
	}
	if len(details.Results) != 1 || details.Results[0]["track_id"] != trackID {
		t.Errorf("results = %v, want one entry for %s", details.Results, trackID)
	}
	if open, _ := lib.CountOpenConflicts(""); open != 0 {
		t.Errorf("open conflicts = %d, want 0", open)
	}
}

func TestResolveDBChoiceRewritesFileTags(t *testing.T) {
	svc, lib, jm, tw, dir, _ := newTestService(t)
	trackID, path := seedTrack(t, lib, dir, "Artist", "Album", "Title", 3)
	track, err := lib.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	if err := lib.ApplyFieldText(track, "year", "2001"); err != nil {
		t.Fatalf("ApplyFieldText year: %v", err)
	}
	// The file now carries a different year than the DB.
	cid := seedConflict(t, lib, trackID, "year", "2001", "2003")

	jobID, err := svc.Resolve([]Resolution{{ConflictID: cid, Choice: ChoiceDB}}, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	waitForJob(t, jm, jobID)

	want, ok := tw.written[path]
	if !ok {
		t.Fatalf("no tag write for %s", path)
	}
	if v, ok := want["year"].(int); !ok || v != 2001 {
		t.Errorf("tag write year = %v, want 2001 (the DB value)", want["year"])
	}
	track, err = lib.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	if track.Year == nil || *track.Year != 2001 {
		t.Errorf("track year = %v, unchanged DB value wanted", track.Year)
	}
	if track.State != library.TrackStateOK {
		t.Errorf("state = %s, want ok", track.State)
	}
}

func TestResolveCustomChoiceWritesBoth(t *testing.T) {
	svc, lib, jm, tw, dir, _ := newTestService(t)
	trackID, path := seedTrack(t, lib, dir, "Artist", "Album", "Title", 1)
	cid := seedConflict(t, lib, trackID, "title", "Old Title", "File Title")

	jobID, err := svc.Resolve([]Resolution{{ConflictID: cid, Choice: ChoiceCustom, Value: "Chosen Title"}}, "tester")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	waitForJob(t, jm, jobID)

	if v, ok := tw.written[path]["title"]; !ok || v != "Chosen Title" {
		t.Errorf("tag write title = %v, want Chosen Title", tw.written[path]["title"])
	}
	track, err := lib.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	if track.Title != "Chosen Title" {
		t.Errorf("title = %q, want Chosen Title", track.Title)
	}
}

func TestResolveAlbumChoiceRepointsAlbum(t *testing.T) {
	svc, lib, jm, _, dir, _ := newTestService(t)
	trackID, _ := seedTrack(t, lib, dir, "Artist", "Album One", "Title", 1)
	cid := seedConflict(t, lib, trackID, "album", "Album One", "Album Two")

	jobID, err := svc.Resolve([]Resolution{{ConflictID: cid, Choice: ChoiceFile}}, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	waitForJob(t, jm, jobID)

	track, err := lib.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	album, err := lib.GetAlbum(track.AlbumID)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if album.Title != "Album Two" {
		t.Errorf("album = %q, want Album Two", album.Title)
	}
}

func TestResolveValidation(t *testing.T) {
	svc, lib, _, _, dir, _ := newTestService(t)
	trackID, _ := seedTrack(t, lib, dir, "Artist", "Album", "Title", 1)
	cid := seedConflict(t, lib, trackID, "title", "a", "b")

	if _, err := svc.Resolve(nil, ""); !errors.Is(err, ErrInvalidResolution) {
		t.Errorf("empty resolutions: err = %v, want ErrInvalidResolution", err)
	}
	if _, err := svc.Resolve([]Resolution{{ConflictID: cid, Choice: "banana"}}, ""); !errors.Is(err, ErrInvalidResolution) {
		t.Errorf("bad choice: err = %v, want ErrInvalidResolution", err)
	}
	if _, err := svc.Resolve([]Resolution{{ConflictID: cid, Choice: ChoiceCustom}}, ""); !errors.Is(err, ErrInvalidResolution) {
		t.Errorf("custom without value: err = %v, want ErrInvalidResolution", err)
	}
	if _, err := svc.Resolve([]Resolution{{ConflictID: "nope", Choice: ChoiceFile}}, ""); err == nil || !errors.Is(err, sql.ErrNoRows) {
		// GetConflict returns sql.ErrNoRows; it must survive the wrap.
		t.Errorf("unknown conflict: err = %v, want sql.ErrNoRows", err)
	}
}

func TestResolveClosedConflict(t *testing.T) {
	svc, lib, _, _, dir, _ := newTestService(t)
	trackID, _ := seedTrack(t, lib, dir, "Artist", "Album", "Title", 1)
	cid := seedConflict(t, lib, trackID, "title", "a", "b")
	if err := lib.MarkConflictStatus(cid, library.ConflictStatusDismissed, "", "x"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if _, err := svc.Resolve([]Resolution{{ConflictID: cid, Choice: ChoiceFile}}, ""); !errors.Is(err, ErrConflictClosed) {
		t.Errorf("resolve closed: err = %v, want ErrConflictClosed", err)
	}
}

func TestResolveFailureKeepsConflictOpen(t *testing.T) {
	svc, lib, jm, tw, dir, _ := newTestService(t)
	trackID, _ := seedTrack(t, lib, dir, "Artist", "Album", "Title", 1)
	cid := seedConflict(t, lib, trackID, "year", "2001", "2003")
	tw.writeErr = errors.New("disk full")

	jobID, err := svc.Resolve([]Resolution{{ConflictID: cid, Choice: ChoiceDB}}, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	job := waitForJob(t, jm, jobID)
	var details struct {
		Results []map[string]interface{} `json:"results"`
	}
	if err := json.Unmarshal([]byte(job.Details), &details); err != nil {
		t.Fatalf("job details: %v", err)
	}
	if len(details.Results) != 1 {
		t.Fatalf("results = %v", details.Results)
	}
	if res, ok := details.Results[0]["errors"].([]interface{}); !ok || len(res) == 0 {
		t.Errorf("results[0].errors = %v, want a failure entry", details.Results[0])
	}
	c, err := lib.GetConflict(cid)
	if err != nil {
		t.Fatalf("GetConflict: %v", err)
	}
	if c.Status != library.ConflictStatusOpen {
		t.Errorf("conflict status = %s, want open after a failed write", c.Status)
	}
}

func TestResolveAllAndDismiss(t *testing.T) {
	svc, lib, jm, _, dir, _ := newTestService(t)
	trackID, _ := seedTrack(t, lib, dir, "Artist", "Album", "Title", 1)
	c1 := seedConflict(t, lib, trackID, "title", "a", "b")
	c2 := seedConflict(t, lib, trackID, "year", "1", "2")

	if _, err := svc.ResolveAll("", "custom", ""); err == nil {
		t.Error("ResolveAll(custom) should fail (no global value)")
	}
	jobID, err := svc.ResolveAll("", ChoiceFile, "tester")
	if err != nil {
		t.Fatalf("ResolveAll: %v", err)
	}
	waitForJob(t, jm, jobID)
	if open, _ := lib.CountOpenConflicts(""); open != 0 {
		t.Errorf("open conflicts = %d, want 0", open)
	}

	c, err := svc.Dismiss(c1, "tester")
	if err == nil {
		t.Errorf("Dismiss(resolved) = %+v, want ErrConflictClosed", c)
	}
	_ = c2
}

func TestDismiss(t *testing.T) {
	svc, lib, _, _, dir, _ := newTestService(t)
	trackID, _ := seedTrack(t, lib, dir, "Artist", "Album", "Title", 1)
	cid := seedConflict(t, lib, trackID, "title", "a", "b")
	c, err := svc.Dismiss(cid, "tester")
	if err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	if c.Status != library.ConflictStatusDismissed || c.ResolvedBy == nil || *c.ResolvedBy != "tester" {
		t.Errorf("conflict = %+v, want dismissed by tester", c)
	}
}

// TestRelinkMissingKeepsHistory re-points a missing track at the
// duplicate row a later scan created for the same file: the id and the
// history rows stay with the missing row, the duplicate is removed.
func TestRelinkMissingKeepsHistory(t *testing.T) {
	svc, lib, _, _, dir, database := newTestService(t)
	missingID, originalPath := seedTrack(t, lib, dir, "Artist", "Album", "Title", 1)
	candID, candPath := seedTrack(t, lib, dir, "Artist", "Album", "Title Copy", 1)

	// The original file is gone; the copy was scanned as a new track.
	if err := os.Remove(originalPath); err != nil {
		t.Fatalf("remove original: %v", err)
	}
	if err := lib.MarkMissing(missingID); err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}
	// Same fingerprint on both rows (identical file content).
	if err := lib.SetSyncState(candID, 10, 0, "hash-same", library.NowSQL(), library.TrackStateOK, false); err != nil {
		t.Fatalf("SetSyncState cand: %v", err)
	}
	if err := lib.SetSyncState(missingID, 10, 0, "hash-same", library.NowSQL(), library.TrackStateMissing, false); err != nil {
		t.Fatalf("SetSyncState missing: %v", err)
	}
	lib.RecordChange(missingID, "title", "Old", "Title", "cs-1")

	updated, err := svc.Relink(missingID, candPath)
	if err != nil {
		t.Fatalf("Relink: %v", err)
	}
	if updated.ID != missingID {
		t.Errorf("relinked id = %s, want %s (the id must be kept)", updated.ID, missingID)
	}
	if updated.Path != candPath {
		t.Errorf("path = %s, want %s", updated.Path, candPath)
	}
	if updated.State != library.TrackStateOK {
		t.Errorf("state = %s, want ok", updated.State)
	}
	if _, err := lib.GetTrack(candID); err == nil {
		t.Error("candidate row still exists, want removed")
	}
	var hist []struct {
		Field    string  `db:"field"`
		OldValue *string `db:"old_value"`
	}
	if err := database.Select(&hist, "SELECT field, old_value FROM metadata_changes WHERE track_id = ?", missingID); err != nil {
		t.Fatalf("history query: %v", err)
	}
	if len(hist) != 1 || hist[0].Field != "title" || derefStr(hist[0].OldValue) != "Old" {
		t.Errorf("history = %+v, want the kept title row", hist)
	}
}

func TestRelinkRejections(t *testing.T) {
	svc, lib, _, _, dir, _ := newTestService(t)
	missingID, originalPath := seedTrack(t, lib, dir, "Artist", "Album", "Title", 1)
	candID, candPath := seedTrack(t, lib, dir, "Artist", "Album", "Other", 2)

	// Not missing yet.
	if _, err := svc.Relink(missingID, candPath); !errors.Is(err, ErrNotMissing) {
		t.Errorf("Relink(ok track) = %v, want ErrNotMissing", err)
	}
	if err := os.Remove(originalPath); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := lib.MarkMissing(missingID); err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}
	// No row at the path.
	if _, err := svc.Relink(missingID, filepath.Join(dir, "nowhere.mp3")); !errors.Is(err, ErrNoCandidate) {
		t.Errorf("Relink(no row) = %v, want ErrNoCandidate", err)
	}
	// Fingerprint mismatch (different size).
	if err := lib.SetSyncState(candID, 999, 0, "hash-x", library.NowSQL(), library.TrackStateOK, false); err != nil {
		t.Fatalf("SetSyncState: %v", err)
	}
	if _, err := svc.Relink(missingID, candPath); !errors.Is(err, ErrNoCandidate) {
		t.Errorf("Relink(mismatch) = %v, want ErrNoCandidate", err)
	}
	_ = candID
}

func TestDeleteMissing(t *testing.T) {
	svc, lib, _, _, dir, _ := newTestService(t)
	trackID, path := seedTrack(t, lib, dir, "Artist", "Album", "Title", 1)
	lib.RecordChange(trackID, "title", "Old", "Title", "cs-1")

	// Not missing yet.
	if err := svc.DeleteMissing(trackID); !errors.Is(err, ErrNotMissing) {
		t.Errorf("DeleteMissing(ok) = %v, want ErrNotMissing", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := lib.MarkMissing(trackID); err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}
	if err := svc.DeleteMissing(trackID); err != nil {
		t.Fatalf("DeleteMissing: %v", err)
	}
	if _, err := lib.GetTrack(trackID); err == nil {
		t.Error("track row still exists, want deleted")
	}
}

func TestMissingCandidates(t *testing.T) {
	svc, lib, _, _, dir, _ := newTestService(t)
	missingID, originalPath := seedTrack(t, lib, dir, "Artist", "Album", "Title", 1)
	candID, _ := seedTrack(t, lib, dir, "Artist", "Album", "Title Copy", 1)
	otherID, _ := seedTrack(t, lib, dir, "Artist", "Album", "Different", 5)

	if err := os.Remove(originalPath); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := lib.MarkMissing(missingID); err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}
	// The copy matches the fingerprint; "Different" does not (duration).
	if err := lib.SetSyncState(candID, 10, 0, "hash-same", library.NowSQL(), library.TrackStateOK, false); err != nil {
		t.Fatalf("SetSyncState cand: %v", err)
	}
	if err := lib.SetSyncState(missingID, 10, 0, "hash-same", library.NowSQL(), library.TrackStateMissing, false); err != nil {
		t.Fatalf("SetSyncState missing: %v", err)
	}
	if err := lib.SetSyncState(otherID, 10, 0, "hash-other", library.NowSQL(), library.TrackStateOK, false); err != nil {
		t.Fatalf("SetSyncState other: %v", err)
	}

	missing, err := svc.Missing()
	if err != nil {
		t.Fatalf("Missing: %v", err)
	}
	if len(missing) != 1 {
		t.Fatalf("missing = %d entries, want 1", len(missing))
	}
	if len(missing[0].Candidates) != 1 || missing[0].Candidates[0].TrackID != candID {
		t.Errorf("candidates = %+v, want only %s", missing[0].Candidates, candID)
	}
}

func TestTrashRestoreAndPurge(t *testing.T) {
	svc, lib, _, _, dir, database := newTestService(t)
	src := filepath.Join(dir, "displaced.mp3")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	id, err := lib.TrashFile(src, "")
	if err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	entries, err := svc.Trash()
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != id {
		t.Fatalf("trash = %+v, want one entry %s", entries, id)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("original path still exists after trashing")
	}
	if err := svc.Restore(id); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("restored file missing: %v", err)
	}
	if entries, _ := svc.Trash(); len(entries) != 0 {
		t.Errorf("trash = %d entries after restore, want 0", len(entries))
	}

	// Restoring while the original path is taken again is a conflict.
	other := filepath.Join(dir, "other.mp3")
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	id2, err := lib.TrashFile(other, "")
	if err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if err := os.WriteFile(other, []byte("taken"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := svc.Restore(id2); !errors.Is(err, library.ErrTrashRestoreConflict) {
		t.Errorf("Restore(taken path) = %v, want ErrTrashRestoreConflict", err)
	}

	// Purge drops entries past the retention (backdate one row, keep a
	// fresh one).
	if err := os.WriteFile(filepath.Join(dir, "old.mp3"), []byte("y"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	id3, err := lib.TrashFile(filepath.Join(dir, "old.mp3"), "")
	if err != nil {
		t.Fatalf("TrashFile old: %v", err)
	}
	if _, err := database.Exec("UPDATE sync_trash SET created_at = ? WHERE id = ?", "2000-01-02 03:04:05", id3); err != nil {
		t.Fatalf("backdate trash row: %v", err)
	}
	if err := svc.Purge(); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	entries, err = svc.Trash()
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != id2 {
		t.Errorf("trash after purge = %+v, want only the fresh entry %s", entries, id2)
	}
}

func TestPolicyEndpoints(t *testing.T) {
	t.Setenv("SYNC_CONFLICT_POLICY", "")
	svc, _, _, _, _, _ := newTestService(t)
	if got := svc.Policy(); got != library.SyncPolicyAsk {
		t.Errorf("default policy = %s, want ask", got)
	}
	if err := svc.SetPolicy("banana"); err == nil {
		t.Error("SetPolicy(banana) should fail")
	}
	if err := svc.SetPolicy(library.SyncPolicyFileWins); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	if got := svc.Policy(); got != library.SyncPolicyFileWins {
		t.Errorf("policy = %s, want file_wins", got)
	}
}

// TestSummaryCounts checks the badge payload.
func TestSummaryCounts(t *testing.T) {
	svc, lib, _, _, dir, _ := newTestService(t)
	trackID, path := seedTrack(t, lib, dir, "Artist", "Album", "Title", 1)
	seedConflict(t, lib, trackID, "title", "a", "b")
	seedConflict(t, lib, trackID, "year", "1", "2")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := lib.MarkMissing(trackID); err != nil {
		t.Fatalf("MarkMissing: %v", err)
	}

	sum, err := svc.Summary()
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if sum.OpenConflicts != 2 || sum.MissingTracks != 1 {
		t.Errorf("summary = %+v, want {2 1}", sum)
	}
}

func TestConflictsListFiltering(t *testing.T) {
	svc, lib, _, _, dir, _ := newTestService(t)
	trackID, _ := seedTrack(t, lib, dir, "Artist", "Album", "Title", 1)
	otherID, _ := seedTrack(t, lib, dir, "Artist", "Other Album", "Other", 1)
	seedConflict(t, lib, trackID, "title", "a", "b")
	seedConflict(t, lib, otherID, "title", "c", "d")

	groups, err := svc.Conflicts("", "")
	if err != nil {
		t.Fatalf("Conflicts: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	albumID := groups[0].AlbumID
	groups, err = svc.Conflicts("", albumID)
	if err != nil {
		t.Fatalf("Conflicts(album): %v", err)
	}
	if len(groups) != 1 {
		t.Errorf("album filter returned %d groups, want 1", len(groups))
	}
	groups, err = svc.Conflicts(library.ConflictStatusDismissed, "")
	if err != nil {
		t.Fatalf("Conflicts(dismissed): %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("dismissed = %d groups, want 0", len(groups))
	}
}
