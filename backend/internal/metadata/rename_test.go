package metadata

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yt-scraper/internal/jobs"
)

// decodeJobResults parses the per-track rename results merged into the
// finished job's details JSON (empty when no suffix was applied).
func decodeJobResults(t *testing.T, job *jobs.Job) []renameResult {
	t.Helper()
	if !strings.Contains(job.Details, `"results"`) {
		return nil
	}
	var details struct {
		Results []renameResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(job.Details), &details); err != nil {
		t.Fatalf("parse job details %q: %v", job.Details, err)
	}
	return details.Results
}

// TestApplyDuplicateTitleKeepsBothFiles is the core T17 regression: a
// duplicate title in the same album must keep both audio files,
// byte-identical, instead of deleting the existing one.
func TestApplyDuplicateTitleKeepsBothFiles(t *testing.T) {
	svc, lib, jm, _, dir := newJobService(t)
	firstID, _ := seedTrack(t, lib, dir, "First")
	secondID, _ := seedTrack(t, lib, dir, "Second")
	first, _ := lib.GetTrack(firstID)
	second, _ := lib.GetTrack(secondID)
	writeAudio(t, first.Path, "content one")
	writeAudio(t, second.Path, "content two")

	jobID, _, err := svc.ApplyTrackEdits([]TrackEdit{{TrackID: secondID, Fields: map[string]interface{}{
		"title": "First",
	}}})
	if err != nil {
		t.Fatalf("ApplyTrackEdits: %v", err)
	}
	job := waitForJob(t, jm, jobID, jobs.StatusCompleted)

	// Both files exist with byte-identical contents.
	firstFile := filepath.Join(dir, "Artist", "Album", "First.flac")
	suffixedFile := filepath.Join(dir, "Artist", "Album", "First (2).flac")
	if got, err := os.ReadFile(firstFile); err != nil || !bytes.Equal(got, []byte("content one")) { //nolint:gosec // G304 - path is built from t.TempDir()
		t.Fatalf("first file = %q, err = %v, want the original bytes", got, err)
	}
	if got, err := os.ReadFile(suffixedFile); err != nil || !bytes.Equal(got, []byte("content two")) { //nolint:gosec // G304 - path is built from t.TempDir()
		t.Fatalf("suffixed file = %q, err = %v, want the original bytes", got, err)
	}

	// The database records the actual final path, nothing else moved.
	updated, err := lib.GetTrack(secondID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	if updated.Title != "First" || updated.Path != suffixedFile {
		t.Fatalf("track = %q @ %q, want First @ %q", updated.Title, updated.Path, suffixedFile)
	}
	got, err := lib.GetTrack(firstID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	if got.Path != firstFile {
		t.Fatalf("other track moved to %q, want %q", got.Path, firstFile)
	}

	// Job progress carries the note; the details carry the structured
	// result the UI reads (rename_collision/final_filename).
	wantNote := `renamed to "First (2).flac" (name taken)`
	if !strings.Contains(job.Progress, wantNote) {
		t.Fatalf("job progress = %q, want a note containing %q", job.Progress, wantNote)
	}
	if job.Error != "" {
		t.Fatalf("job error = %q, want empty (a completed job)", job.Error)
	}
	results := decodeJobResults(t, job)
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one entry", results)
	}
	if results[0].TrackID != secondID || !results[0].RenameCollision || results[0].FinalFilename != "First (2).flac" {
		t.Fatalf("result = %+v", results[0])
	}
}

// TestApplySanitizeCollisionKeepsBothFiles covers two different titles
// that sanitize to the same filename ("A/B" and "A-B" both become "A-B").
func TestApplySanitizeCollisionKeepsBothFiles(t *testing.T) {
	svc, lib, jm, _, dir := newJobService(t)
	dashID, _ := seedTrack(t, lib, dir, "A-B")
	otherID, _ := seedTrack(t, lib, dir, "Other")
	dashTrack, _ := lib.GetTrack(dashID)
	otherTrack, _ := lib.GetTrack(otherID)
	writeAudio(t, dashTrack.Path, "dash bytes")
	writeAudio(t, otherTrack.Path, "slash bytes")

	jobID, _, err := svc.ApplyTrackEdits([]TrackEdit{{TrackID: otherID, Fields: map[string]interface{}{
		"title": "A/B",
	}}})
	if err != nil {
		t.Fatalf("ApplyTrackEdits: %v", err)
	}
	job := waitForJob(t, jm, jobID, jobs.StatusCompleted)

	base := filepath.Join(dir, "Artist", "Album")
	if got, err := os.ReadFile(filepath.Join(base, "A-B.flac")); err != nil || !bytes.Equal(got, []byte("dash bytes")) { //nolint:gosec // G304 - path is built from t.TempDir()
		t.Fatalf("A-B.flac = %q, err = %v, want the original bytes", got, err)
	}
	suffixed := filepath.Join(base, "A-B (2).flac")
	if got, err := os.ReadFile(suffixed); err != nil || !bytes.Equal(got, []byte("slash bytes")) { //nolint:gosec // G304 - path is built from t.TempDir()
		t.Fatalf("A-B (2).flac = %q, err = %v, want the original bytes", got, err)
	}

	updated, _ := lib.GetTrack(otherID)
	if updated.Title != "A/B" || updated.Path != suffixed {
		t.Fatalf("track = %q @ %q, want A/B @ %q", updated.Title, updated.Path, suffixed)
	}
	if results := decodeJobResults(t, job); len(results) != 1 || results[0].FinalFilename != "A-B (2).flac" {
		t.Fatalf("results = %+v, want one suffixed to A-B (2).flac", results)
	}
}

// TestApplyCaseOnlyRename covers a rename that differs only in case: the
// file must end up at the new name (or stay, on a case-insensitive
// filesystem) with its content intact, and no collision is reported.
func TestApplyCaseOnlyRename(t *testing.T) {
	svc, lib, jm, _, dir := newJobService(t)
	trackID, _ := seedTrack(t, lib, dir, "Intro")
	track, _ := lib.GetTrack(trackID)
	writeAudio(t, track.Path, "casey")

	jobID, _, err := svc.ApplyTrackEdits([]TrackEdit{{TrackID: trackID, Fields: map[string]interface{}{
		"title": "intro",
	}}})
	if err != nil {
		t.Fatalf("ApplyTrackEdits: %v", err)
	}
	waitForJob(t, jm, jobID, jobs.StatusCompleted)

	updated, err := lib.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	if updated.Title != "intro" {
		t.Fatalf("title = %q, want intro", updated.Title)
	}
	wantPath := filepath.Join(dir, "Artist", "Album", "intro.flac")
	if updated.Path != wantPath {
		t.Fatalf("path = %q, want %q", updated.Path, wantPath)
	}
	if got, err := os.ReadFile(updated.Path); err != nil || !bytes.Equal(got, []byte("casey")) { //nolint:gosec // G304 - path is built from t.TempDir()
		t.Fatalf("file at new path = %q, err = %v, want the original bytes", got, err)
	}
	// No collision happened: no results in the job details.
	job, _ := jm.GetJob(jobID)
	if results := decodeJobResults(t, job); results != nil {
		t.Fatalf("results = %+v, want none for a collision-free rename", results)
	}
}

// TestRevertChangeSetCollisionSuffixed covers a revert whose old name is
// already taken: the revert auto-numbers instead of deleting the file
// that now holds the name.
func TestRevertChangeSetCollisionSuffixed(t *testing.T) {
	svc, lib, jm, _, dir := newJobService(t)
	firstID, _ := seedTrack(t, lib, dir, "First")
	secondID, _ := seedTrack(t, lib, dir, "Second")
	thirdID, _ := seedTrack(t, lib, dir, "Third")
	first, _ := lib.GetTrack(firstID)
	second, _ := lib.GetTrack(secondID)
	third, _ := lib.GetTrack(thirdID)
	writeAudio(t, first.Path, "one")
	writeAudio(t, second.Path, "two")
	writeAudio(t, third.Path, "three")

	// Change set one: rename First onto Second's file (auto-numbered).
	jobID1, set1, err := svc.ApplyTrackEdits([]TrackEdit{{TrackID: firstID, Fields: map[string]interface{}{
		"title": "Second",
	}}})
	if err != nil {
		t.Fatalf("ApplyTrackEdits: %v", err)
	}
	waitForJob(t, jm, jobID1, jobs.StatusCompleted)
	base := filepath.Join(dir, "Artist", "Album")
	if _, err := os.Stat(filepath.Join(base, "Second (2).flac")); err != nil {
		t.Fatalf("expected Second (2).flac after first rename: %v", err)
	}

	// A third track moves onto the name the revert wants to reclaim.
	jobID, _, err := svc.ApplyTrackEdits([]TrackEdit{{TrackID: thirdID, Fields: map[string]interface{}{
		"title": "First",
	}}})
	if err != nil {
		t.Fatalf("ApplyTrackEdits: %v", err)
	}
	waitForJob(t, jm, jobID, jobs.StatusCompleted)

	// Revert change set one: First's old name is now taken, so the
	// revert must land on "First (2)" and keep both files.
	jobID, set2, err := svc.RevertChangeSet(set1)
	if err != nil {
		t.Fatalf("RevertChangeSet: %v", err)
	}
	job := waitForJob(t, jm, jobID, jobs.StatusCompleted)
	if set2 == set1 {
		t.Fatal("revert must use a new change set id")
	}

	reverted, _ := lib.GetTrack(firstID)
	if reverted.Title != "First" || reverted.Path != filepath.Join(base, "First (2).flac") {
		t.Fatalf("reverted track = %q @ %q, want First @ First (2).flac", reverted.Title, reverted.Path)
	}
	if got, err := os.ReadFile(filepath.Join(base, "First (2).flac")); err != nil || !bytes.Equal(got, []byte("one")) { //nolint:gosec // G304 - path is built from t.TempDir()
		t.Fatalf("First (2).flac = %q, err = %v, want the original bytes", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(base, "First.flac")); err != nil || !bytes.Equal(got, []byte("three")) { //nolint:gosec // G304 - path is built from t.TempDir()
		t.Fatalf("First.flac = %q, err = %v, want the third track's bytes", got, err)
	}

	wantNote := `renamed to "First (2).flac" (name taken)`
	if !strings.Contains(job.Progress, wantNote) {
		t.Fatalf("revert job progress = %q, want a note containing %q", job.Progress, wantNote)
	}
	results := decodeJobResults(t, job)
	if len(results) != 1 || results[0].TrackID != firstID || results[0].FinalFilename != "First (2).flac" {
		t.Fatalf("results = %+v, want First (2).flac for %s", results, firstID)
	}
}

func writeAudio(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
