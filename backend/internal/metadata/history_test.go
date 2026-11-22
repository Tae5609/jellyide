package metadata

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
)

// applyAndWait is a test helper: apply one track edit and wait for the job.
func applyAndWait(t *testing.T, svc *Service, jm *jobs.Manager, trackID string, fields map[string]interface{}) (string, string) {
	t.Helper()
	jobID, changeSetID, err := svc.ApplyTrackEdits([]TrackEdit{{TrackID: trackID, Fields: fields}})
	if err != nil {
		t.Fatalf("ApplyTrackEdits: %v", err)
	}
	waitForJob(t, jm, jobID, jobs.StatusCompleted)
	return jobID, changeSetID
}

func TestHistoryNewestFirst(t *testing.T) {
	svc, lib, jm, _, dir := newJobService(t)

	trackA, _ := seedTrack(t, lib, dir, "Alpha Song")
	trackB, _ := seedTrack(t, lib, dir, "Beta Song")

	_, set1 := applyAndWait(t, svc, jm, trackA, map[string]interface{}{"genre": "Rock"})
	// The timestamp column has second resolution; wait past it so the
	// second change set is strictly newer.
	time.Sleep(1100 * time.Millisecond)
	_, set2 := applyAndWait(t, svc, jm, trackB, map[string]interface{}{"genre": "Jazz"})

	history, err := svc.History("", 0)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history = %d entries, want 2", len(history))
	}
	if history[0].ChangeSetID != set2 {
		t.Fatalf("newest = %s, want %s", history[0].ChangeSetID, set2)
	}
	if history[1].ChangeSetID != set1 {
		t.Fatalf("second = %s, want %s", history[1].ChangeSetID, set1)
	}
	entry := history[1]
	if entry.TrackCount != 1 {
		t.Fatalf("track_count = %d, want 1", entry.TrackCount)
	}
	if len(entry.Changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(entry.Changes))
	}
	change := entry.Changes[0]
	if change.TrackID != trackA || change.Field != "genre" {
		t.Fatalf("change = %+v", change)
	}
	if change.OldValue != nil {
		t.Fatalf("old_value = %v, want nil", change.OldValue)
	}
	if change.NewValue == nil || *change.NewValue != "Rock" {
		t.Fatalf("new_value = %v, want Rock", change.NewValue)
	}
	if change.TrackTitle != "Alpha Song" {
		t.Fatalf("track_title = %q, want Alpha Song", change.TrackTitle)
	}
}

func TestHistoryAlbumFilterAndLimit(t *testing.T) {
	svc, lib, jm, _, dir := newJobService(t)

	artistID, err := lib.EnsureArtist("Other")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumOtherID, err := lib.EnsureAlbum(artistID, "Other Album")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	trackOther, _ := seedTrackInAlbum(t, lib, dir, albumOtherID, "Other Song")

	trackMain, _ := seedTrack(t, lib, dir, "Main Song")

	// Both albums live in the same music dir but different albums.
	_, setMain := applyAndWait(t, svc, jm, trackMain, map[string]interface{}{"genre": "Rock"})
	_, setOther := applyAndWait(t, svc, jm, trackOther, map[string]interface{}{"genre": "Folk"})

	// Find the main track's album id.
	mainTrack, _ := lib.GetTrack(trackMain)
	history, err := svc.History(mainTrack.AlbumID, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 || history[0].ChangeSetID != setMain {
		t.Fatalf("history = %+v, want only the main album's set", history)
	}
	_ = setOther

	// Limit 1 over all sets.
	all, err := svc.History("", 1)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("history with limit 1 = %d entries, want 1", len(all))
	}
}

func TestRevertChangeSetRestoresOldValues(t *testing.T) {
	svc, lib, jm, _, dir := newJobService(t)
	trackID, oldPath := seedTrack(t, lib, dir, "Song One")
	if err := os.WriteFile(oldPath, []byte("audio"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Change set 1: rename + genre.
	_, set1 := applyAndWait(t, svc, jm, trackID, map[string]interface{}{
		"title": "Song Two",
		"genre": "Rock",
	})

	// Revert: a new change set applying the old values.
	jobID, set2, err := svc.RevertChangeSet(set1)
	if err != nil {
		t.Fatalf("RevertChangeSet: %v", err)
	}
	if set2 == set1 {
		t.Fatal("revert must use a new change set id")
	}
	waitForJob(t, jm, jobID, jobs.StatusCompleted)

	track, err := lib.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	if track.Title != "Song One" {
		t.Fatalf("title = %q, want Song One", track.Title)
	}
	if track.Genre != nil {
		t.Fatalf("genre = %v, want nil", track.Genre)
	}
	// The file was renamed back.
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("original file missing after revert: %v", err)
	}

	// The revert is itself revertible.
	jobID, set3, err := svc.RevertChangeSet(set2)
	if err != nil {
		t.Fatalf("RevertChangeSet again: %v", err)
	}
	waitForJob(t, jm, jobID, jobs.StatusCompleted)
	track, _ = lib.GetTrack(trackID)
	if track.Title != "Song Two" {
		t.Fatalf("title = %q, want Song Two after revert-of-revert", track.Title)
	}
	if track.Genre == nil || *track.Genre != "Rock" {
		t.Fatalf("genre = %v, want Rock", track.Genre)
	}
	_ = set3
}

func TestRevertChangeSetMissingTrackConflicts(t *testing.T) {
	svc, lib, jm, _, dir := newJobService(t)
	trackID, _ := seedTrack(t, lib, dir, "Song")
	_, set1 := applyAndWait(t, svc, jm, trackID, map[string]interface{}{"genre": "Rock"})

	// The metadata_changes FK (no ON DELETE clause) blocks the track
	// delete while its change rows exist; bypass it to simulate a track
	// that was removed after the change set was applied.
	if _, err := svc.db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("pragma off: %v", err)
	}
	if err := lib.DeleteTrack(trackID); err != nil {
		t.Fatalf("DeleteTrack: %v", err)
	}
	if _, err := svc.db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("pragma on: %v", err)
	}
	_, _, err := svc.RevertChangeSet(set1)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want *ConflictError", err)
	}
	if conflict.TrackID != trackID {
		t.Fatalf("track id = %q, want %q", conflict.TrackID, trackID)
	}
}

func TestRevertUnknownChangeSet(t *testing.T) {
	svc, _, _, _, _ := newJobService(t)
	_, _, err := svc.RevertChangeSet("no-such-set")
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("error = %v, want *NotFoundError", err)
	}
}

// seedTrackInAlbum seeds a track into a specific album (instead of the
// default single-artist/album layout seedTrack uses).
func seedTrackInAlbum(t *testing.T, lib *library.Service, dir, albumID, title string) (string, string) {
	t.Helper()
	path := filepath.Join(dir, "Other", "Other Album", title+".flac")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := lib.UpsertTrack(albumID, title, path, 60, 900, library.TrackMeta{}); err != nil {
		t.Fatalf("UpsertTrack: %v", err)
	}
	tracks, err := lib.GetTracksByAlbum(albumID)
	if err != nil {
		t.Fatalf("GetTracksByAlbum: %v", err)
	}
	for _, tr := range tracks {
		if tr.Title == title {
			return tr.ID, tr.Path
		}
	}
	t.Fatalf("seeded track %q not found", title)
	return "", ""
}
