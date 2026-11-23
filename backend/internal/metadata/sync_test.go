package metadata

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
)

// seedJobTracks seeds n tracks sharing one album and returns their ids
// and paths. The files are written so rename collision tests can use them.
func seedJobTracks(t *testing.T, lib *library.Service, dir string, n int) ([]string, []string) {
	t.Helper()
	artistID, err := lib.EnsureArtist("Artist")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumID, err := lib.EnsureAlbum(artistID, "Album")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	ids := make([]string, 0, n)
	paths := make([]string, 0, n)
	for i := 0; i < n; i++ {
		title := "Song " + string(rune('A'+i))
		path := filepath.Join(dir, "Artist", "Album", title+".flac")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if err := lib.UpsertTrack(albumID, title, path, 60, 900, library.TrackMeta{Comment: strPtr("c" + string(rune('0'+i)))}); err != nil {
			t.Fatalf("UpsertTrack: %v", err)
		}
		tracks, err := lib.GetTracksByAlbum(albumID)
		if err != nil {
			t.Fatalf("GetTracksByAlbum: %v", err)
		}
		for _, tr := range tracks {
			if tr.Path == path {
				ids = append(ids, tr.ID)
				paths = append(paths, path)
				break
			}
		}
	}
	return ids, paths
}

func strPtr(s string) *string { return &s }

// jobResults parses the finished job's details.results entries.
func jobResults(t *testing.T, jm *jobs.Manager, jobID string) []map[string]interface{} {
	t.Helper()
	job := waitForJob(t, jm, jobID, jobs.StatusCompleted)
	if job.Details == "" {
		return nil
	}
	var details struct {
		Results []map[string]interface{} `json:"results"`
	}
	if err := json.Unmarshal([]byte(job.Details), &details); err != nil {
		t.Fatalf("parse job details: %v", err)
	}
	return details.Results
}

func findResult(results []map[string]interface{}, trackID, reason string) map[string]interface{} {
	for _, r := range results {
		if r["track_id"] == trackID && r["reason"] == reason {
			return r
		}
	}
	return nil
}

func TestApplyTrackEditsVersionConflictAll(t *testing.T) {
	svc, lib, _, _, _ := newJobService(t)
	ids, _ := seedJobTracks(t, lib, t.TempDir(), 1)
	trackID := ids[0]
	track, err := lib.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}

	// Client A applies a comment edit (version 1 -> 2).
	jobID, _, err := svc.ApplyTrackEdits([]TrackEdit{
		{TrackID: trackID, Fields: map[string]interface{}{"comment": "A's edit"}, ExpectedVersion: &track.Version},
	})
	if err != nil {
		t.Fatalf("client A apply: %v", err)
	}
	waitForJob(t, svc.jm, jobID, jobs.StatusCompleted)
	track, _ = lib.GetTrack(trackID)
	if track.Version != 2 {
		t.Fatalf("version = %d, want 2 after client A", track.Version)
	}

	// Client B still holds version 1: its single-track request must fail
	// with a version conflict (the 409 case), not a job.
	_, _, err = svc.ApplyTrackEdits([]TrackEdit{
		{TrackID: trackID, Fields: map[string]interface{}{"genre": "B's genre"}, ExpectedVersion: intPtr(1)},
	})
	var vce *VersionConflictError
	if !errors.As(err, &vce) {
		t.Fatalf("err = %v, want VersionConflictError", err)
	}
	if len(vce.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(vce.Conflicts))
	}
	entry := vce.Conflicts[0]
	if entry["track_id"] != trackID || entry["reason"] != "version_conflict" {
		t.Fatalf("entry = %v", entry)
	}
	if entry["current_version"] != 2 {
		t.Fatalf("current_version = %v, want 2", entry["current_version"])
	}
	// The track was left untouched, and the rejected edit is preserved as
	// a concurrent conflict (nothing silently lost).
	track, _ = lib.GetTrack(trackID)
	if track.Genre != nil {
		t.Fatalf("genre = %v, want untouched (nil)", track.Genre)
	}
	groups, err := lib.ListConflicts(library.ConflictStatusOpen, "")
	if err != nil {
		t.Fatalf("ListConflicts: %v", err)
	}
	var found bool
	for _, g := range groups {
		if g.TrackID != trackID {
			continue
		}
		for _, f := range g.Fields {
			if f.Field == "genre" && f.Source == "concurrent" {
				if f.FileValue == nil || *f.FileValue != "B's genre" {
					t.Fatalf("file_value = %v, want B's genre", f.FileValue)
				}
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("groups = %+v, want an open concurrent genre conflict", groups)
	}
}

func TestApplyTrackEditsVersionConflictPartial(t *testing.T) {
	svc, lib, _, _, _ := newJobService(t)
	dir := t.TempDir()
	ids, _ := seedJobTracks(t, lib, dir, 2)
	staleID, freshID := ids[0], ids[1]

	// Stale out the first track by applying an edit to it.
	jobID, _, err := svc.ApplyTrackEdits([]TrackEdit{
		{TrackID: staleID, Fields: map[string]interface{}{"comment": "bump"}},
	})
	if err != nil {
		t.Fatalf("bump apply: %v", err)
	}
	waitForJob(t, svc.jm, jobID, jobs.StatusCompleted)

	// Second request: one track stale (expected 1), one current (no
	// expected_version). It must be accepted and the job skips only the
	// stale track.
	jobID2, _, err := svc.ApplyTrackEdits([]TrackEdit{
		{TrackID: staleID, Fields: map[string]interface{}{"genre": "late"}, ExpectedVersion: intPtr(1)},
		{TrackID: freshID, Fields: map[string]interface{}{"genre": "fine"}},
	})
	if err != nil {
		t.Fatalf("partial apply must be accepted: %v", err)
	}
	results := jobResults(t, svc.jm, jobID2)
	if e := findResult(results, staleID, "version_conflict"); e == nil {
		t.Fatalf("results = %v, want a version_conflict entry for %s", results, staleID)
	}
	if e := findResult(results, freshID, "version_conflict"); e != nil {
		t.Fatalf("fresh track must not conflict: %v", e)
	}

	stale, _ := lib.GetTrack(staleID)
	if stale.Genre != nil {
		t.Fatalf("stale track genre = %v, want untouched", stale.Genre)
	}
	fresh, _ := lib.GetTrack(freshID)
	if fresh.Genre == nil || *fresh.Genre != "fine" {
		t.Fatalf("fresh track genre = %v, want applied", fresh.Genre)
	}
}

func TestApplyExternalChangeDuringApply(t *testing.T) {
	svc, lib, _, stub, dir := newJobService(t)
	ids, paths := seedJobTracks(t, lib, dir, 1)
	trackID, path := ids[0], paths[0]
	track, _ := lib.GetTrack(trackID)

	// Someone else changed the genre on disk while the editor was working:
	// the re-read sees it in the field being edited.
	stub.setReadTags(path, map[string]interface{}{
		"title": track.Title, "album": "Album", "artist": "Artist",
		"genre": "External Genre",
	})

	jobID, _, err := svc.ApplyTrackEdits([]TrackEdit{
		{TrackID: trackID, Fields: map[string]interface{}{"genre": "My Genre"}},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	results := jobResults(t, svc.jm, jobID)
	entry := findResult(results, trackID, "external_change")
	if entry == nil {
		t.Fatalf("results = %v, want an external_change entry", results)
	}
	fields, _ := entry["fields"].([]interface{})
	if len(fields) != 1 || fields[0] != "genre" {
		t.Fatalf("external fields = %v, want [genre]", entry["fields"])
	}

	// The DB row is untouched and the divergence is recorded.
	after, _ := lib.GetTrack(trackID)
	if after.Genre != nil {
		t.Fatalf("genre = %v, want untouched", after.Genre)
	}
	if after.State != library.TrackStateConflict {
		t.Fatalf("state = %q, want conflict", after.State)
	}
	groups, err := lib.ListConflicts(library.ConflictStatusOpen, "")
	if err != nil {
		t.Fatalf("ListConflicts: %v", err)
	}
	var found bool
	for _, g := range groups {
		if g.TrackID != trackID {
			continue
		}
		for _, f := range g.Fields {
			if f.Field != "genre" {
				continue
			}
			if f.Source != "apply" || f.FileValue == nil || *f.FileValue != "External Genre" {
				t.Fatalf("row = %+v, want source=apply file_value=External Genre", f)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("groups = %+v, want an open apply genre conflict", groups)
	}
}

func TestApplyProceedsWhenReReadMatchesDB(t *testing.T) {
	svc, lib, _, stub, dir := newJobService(t)
	ids, paths := seedJobTracks(t, lib, dir, 1)
	trackID, path := ids[0], paths[0]
	track, _ := lib.GetTrack(trackID)

	// The re-read agrees with the DB: the edit goes through.
	stub.setReadTags(path, map[string]interface{}{
		"title": track.Title, "album": "Album", "artist": "Artist",
	})
	jobID, _, err := svc.ApplyTrackEdits([]TrackEdit{
		{TrackID: trackID, Fields: map[string]interface{}{"genre": "My Genre"}},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	jobResults(t, svc.jm, jobID)
	after, _ := lib.GetTrack(trackID)
	if after.Genre == nil || *after.Genre != "My Genre" {
		t.Fatalf("genre = %v, want applied", after.Genre)
	}
}

func TestApplyBumpsVersionAndRefreshesFingerprint(t *testing.T) {
	svc, lib, _, _, dir := newJobService(t)
	ids, paths := seedJobTracks(t, lib, dir, 1)
	trackID, path := ids[0], paths[0]

	jobID, _, err := svc.ApplyTrackEdits([]TrackEdit{
		{TrackID: trackID, Fields: map[string]interface{}{"comment": "new"}},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	jobResults(t, svc.jm, jobID)

	after, _ := lib.GetTrack(trackID)
	if after.Version != 2 {
		t.Fatalf("version = %d, want 2 (one data-changing write)", after.Version)
	}
	if after.TagsHash == nil || *after.TagsHash == "" {
		t.Fatal("tags_hash not refreshed")
	}
	if after.SyncedAt == nil {
		t.Fatal("synced_at not refreshed")
	}
	if after.State != "ok" {
		t.Fatalf("state = %q, want ok", after.State)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if after.FileSize == nil || *after.FileSize != st.Size() {
		t.Fatalf("file_size = %v, want %d", after.FileSize, st.Size())
	}
}

func TestRenameCollisionSkipMode(t *testing.T) {
	svc, lib, _, _, dir := newJobService(t)
	ids, paths := seedJobTracks(t, lib, dir, 1)
	trackID, path := ids[0], paths[0]

	// A file that will take the target name.
	taken := filepath.Join(filepath.Dir(path), "Taken.flac")
	if err := os.WriteFile(taken, []byte("other audio"), 0o600); err != nil {
		t.Fatalf("write taken: %v", err)
	}

	jobID, _, err := svc.ApplyTrackEdits([]TrackEdit{
		{TrackID: trackID, Fields: map[string]interface{}{"title": "Taken"}, OnCollision: "skip"},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	results := jobResults(t, svc.jm, jobID)
	if e := findResult(results, trackID, "rename_skipped"); e == nil {
		t.Fatalf("results = %v, want rename_skipped", results)
	}

	// The file keeps its name; the DB title changes.
	after, _ := lib.GetTrack(trackID)
	if after.Path != path {
		t.Fatalf("path = %q, want unchanged %q", after.Path, path)
	}
	if after.Title != "Taken" {
		t.Fatalf("title = %q, want Taken", after.Title)
	}
	if _, err := os.Stat(taken); err != nil {
		t.Fatalf("existing file must survive: %v", err)
	}
}

func TestRenameCollisionReplaceMode(t *testing.T) {
	svc, lib, _, _, dir := newJobService(t)
	ids, paths := seedJobTracks(t, lib, dir, 1)
	trackID, path := ids[0], paths[0]

	taken := filepath.Join(filepath.Dir(path), "Taken.flac")
	if err := os.WriteFile(taken, []byte("other audio"), 0o600); err != nil {
		t.Fatalf("write taken: %v", err)
	}

	jobID, _, err := svc.ApplyTrackEdits([]TrackEdit{
		{TrackID: trackID, Fields: map[string]interface{}{"title": "Taken"}, OnCollision: "replace"},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	jobResults(t, svc.jm, jobID)

	// The track file takes the name; the displaced file is in the trash
	// dir (never deleted) and recorded in sync_trash.
	after, _ := lib.GetTrack(trackID)
	if after.Path != taken {
		t.Fatalf("path = %q, want %q", after.Path, taken)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("old path must be gone (file moved), stat err = %v", err)
	}
	trashDir := lib.TrashDir()
	found := false
	err = filepath.Walk(trashDir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == "Taken.flac" {
			found = true
		}
		return nil
	})
	if err != nil || !found {
		t.Fatalf("displaced file not in trash (found=%v err=%v)", found, err)
	}
	entries, err := lib.ListTrash()
	if err != nil {
		t.Fatalf("ListTrash: %v", err)
	}
	n := 0
	for _, e := range entries {
		if e.OriginalPath == taken {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("trash rows = %d, want 1", n)
	}
}

func TestRenameCollisionSuffixModeDefault(t *testing.T) {
	svc, lib, _, _, dir := newJobService(t)
	ids, paths := seedJobTracks(t, lib, dir, 1)
	trackID, path := ids[0], paths[0]

	taken := filepath.Join(filepath.Dir(path), "Taken.flac")
	if err := os.WriteFile(taken, []byte("other audio"), 0o600); err != nil {
		t.Fatalf("write taken: %v", err)
	}

	// No on_collision: the T17 suffix behaviour is the default.
	jobID, _, err := svc.ApplyTrackEdits([]TrackEdit{
		{TrackID: trackID, Fields: map[string]interface{}{"title": "Taken"}},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	results := jobResults(t, svc.jm, jobID)
	var renameSeen bool
	for _, r := range results {
		if r["track_id"] == trackID && r["rename_collision"] == true {
			if r["final_filename"] != "Taken (2).flac" {
				t.Fatalf("final_filename = %v, want Taken (2).flac", r["final_filename"])
			}
			renameSeen = true
		}
	}
	if !renameSeen {
		t.Fatalf("results = %v, want a rename_collision entry", results)
	}
	after, _ := lib.GetTrack(trackID)
	if after.Path != filepath.Join(filepath.Dir(path), "Taken (2).flac") {
		t.Fatalf("path = %q, want the suffixed name", after.Path)
	}
	if _, err := os.Stat(taken); err != nil {
		t.Fatalf("existing file must survive: %v", err)
	}
}

func TestPreviewReportsTitleCollision(t *testing.T) {
	svc, lib, dir := newTestService(t)
	ids, _ := seedJobTracks(t, lib, dir, 2)
	trackA := ids[0]
	// Track B's file is "Song B.flac"; renaming A to "Song B" collides.
	res, err := svc.PreviewChanges(PreviewRequest{
		Action:   "replace",
		Target:   "A",
		Value:    "B",
		TrackIDs: []string{trackA},
		Field:    "title",
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("results = %d, want 1", len(res))
	}
	if !res[0].Collision {
		t.Fatal("collision = false, want true (target name taken)")
	}
	if filepath.Base(res[0].CollisionPath) != "Song B.flac" {
		t.Fatalf("collision path = %q, want Song B.flac", res[0].CollisionPath)
	}
}

func TestCheckRenameCollisions(t *testing.T) {
	svc, lib, dir := newTestService(t)
	ids, _ := seedJobTracks(t, lib, dir, 2)
	trackA := ids[0]
	reports, err := svc.CheckRenameCollisions([]TrackEdit{
		{TrackID: trackA, Fields: map[string]interface{}{"title": "Song B"}},
		{TrackID: trackA, Fields: map[string]interface{}{"genre": "X"}}, // not a title edit
		{TrackID: "unknown", Fields: map[string]interface{}{"title": "Song B"}},
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("reports = %+v, want 1 (title edits of known tracks only)", reports)
	}
	if !reports[0].Collides || reports[0].ExistingName != "Song B.flac" {
		t.Fatalf("report = %+v, want a collision on Song B.flac", reports[0])
	}
}

func TestApplyTrackEditsRejectsBadOnCollision(t *testing.T) {
	svc, lib, _, _, _ := newJobService(t)
	ids, _ := seedJobTracks(t, lib, t.TempDir(), 1)
	_, _, err := svc.ApplyTrackEdits([]TrackEdit{
		{TrackID: ids[0], Fields: map[string]interface{}{"title": "X"}, OnCollision: "overwrite"},
	})
	if err == nil {
		t.Fatal("bogus on_collision must be rejected")
	}
}

func intPtr(n int) *int { return &n }
