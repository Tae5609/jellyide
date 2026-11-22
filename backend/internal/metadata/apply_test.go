package metadata

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"yt-scraper/internal/jobs"
)

func TestValidateEditsValid(t *testing.T) {
	edits := []TrackEdit{
		{
			TrackID: "t1",
			Fields: map[string]interface{}{
				"genre":        "Rock",
				"year":         2021,
				"track_number": float64(3),
				"comment":      nil,
			},
		},
	}
	got, err := ValidateEdits(edits)
	if err != nil {
		t.Fatalf("ValidateEdits: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("edits = %d, want 1", len(got))
	}
	e := got[0]
	if e.TrackID != "t1" {
		t.Fatalf("track id = %q", e.TrackID)
	}
	if e.Set["genre"] != "Rock" {
		t.Fatalf("genre = %v", e.Set["genre"])
	}
	if e.Set["year"] != 2021 {
		t.Fatalf("year = %v", e.Set["year"])
	}
	if e.Set["track_number"] != 3 {
		t.Fatalf("track_number = %v", e.Set["track_number"])
	}
	// comment is nil -> cleared, not set.
	if _, ok := e.Set["comment"]; ok {
		t.Fatal("comment should not be in Set (it was null)")
	}
	if len(e.Cleared) != 1 || e.Cleared[0] != "comment" {
		t.Fatalf("cleared = %v, want [comment]", e.Cleared)
	}
}

func TestValidateEditsErrors(t *testing.T) {
	cases := []struct {
		name  string
		edits []TrackEdit
		want  string
	}{
		{
			name:  "empty edits",
			edits: []TrackEdit{},
			want:  "edits must not be empty",
		},
		{
			name:  "missing track id",
			edits: []TrackEdit{{Fields: map[string]interface{}{"genre": "X"}}},
			want:  "track_id is required",
		},
		{
			name:  "empty fields",
			edits: []TrackEdit{{TrackID: "t1", Fields: map[string]interface{}{}}},
			want:  "fields must not be empty",
		},
		{
			name:  "unknown field",
			edits: []TrackEdit{{TrackID: "t1", Fields: map[string]interface{}{"bogus": "X"}}},
			want:  "unknown field",
		},
		{
			name:  "unknown null field",
			edits: []TrackEdit{{TrackID: "t1", Fields: map[string]interface{}{"bogus": nil}}},
			want:  "unknown field",
		},
		{
			name:  "text field wrong type",
			edits: []TrackEdit{{TrackID: "t1", Fields: map[string]interface{}{"genre": 42}}},
			want:  "field must be a string or null",
		},
		{
			name:  "int field wrong type",
			edits: []TrackEdit{{TrackID: "t1", Fields: map[string]interface{}{"year": "2021"}}},
			want:  "field must be a non-negative integer or null",
		},
		{
			name:  "int field float",
			edits: []TrackEdit{{TrackID: "t1", Fields: map[string]interface{}{"year": 2021.5}}},
			want:  "field must be a non-negative integer or null",
		},
		{
			name:  "negative int",
			edits: []TrackEdit{{TrackID: "t1", Fields: map[string]interface{}{"year": -1}}},
			want:  "field must be a non-negative integer or null",
		},
		{
			name:  "year too large",
			edits: []TrackEdit{{TrackID: "t1", Fields: map[string]interface{}{"year": 10000}}},
			want:  "year must be between 0 and 9999, got 10000",
		},
		{
			name:  "track number too large",
			edits: []TrackEdit{{TrackID: "t1", Fields: map[string]interface{}{"track_number": 1000}}},
			want:  "track_number must be between 0 and 999, got 1000",
		},
		{
			name:  "disc number too large",
			edits: []TrackEdit{{TrackID: "t1", Fields: map[string]interface{}{"disc_number": 1000}}},
			want:  "disc_number must be between 0 and 999, got 1000",
		},
		{
			name:  "empty title",
			edits: []TrackEdit{{TrackID: "t1", Fields: map[string]interface{}{"title": "   "}}},
			want:  "title must not be empty",
		},
		{
			name:  "null album",
			edits: []TrackEdit{{TrackID: "t1", Fields: map[string]interface{}{"album": nil}}},
			want:  "album cannot be cleared; set a new album name instead",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateEdits(tt.edits)
			if err == nil {
				t.Fatal("want error, got nil")
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("error type = %T, want *ValidationError", err)
			}
			if ve.Message != tt.want {
				t.Fatalf("message = %q, want %q", ve.Message, tt.want)
			}
			// Validation errors carry the offending field and track id.
			if tt.name != "empty edits" && tt.name != "missing track id" && tt.name != "empty fields" && ve.Field == "" {
				t.Fatalf("field = %q, want the offending field", ve.Field)
			}
		})
	}
}

func TestApplyTrackEditsWritesTagsDBAndRenames(t *testing.T) {
	svc, lib, jm, stub, dir := newJobService(t)
	trackID, _ := seedTrack(t, lib, dir, "Old Title")
	// Create the on-disk file so the rename succeeds.
	track, _ := lib.GetTrack(trackID)
	if err := os.WriteFile(track.Path, []byte("audio"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	edits := []TrackEdit{{TrackID: trackID, Fields: map[string]interface{}{
		"title": "New Title",
		"genre": "Rock",
		"year":  2001,
	}}}
	jobID, changeSetID, err := svc.ApplyTrackEdits(edits)
	if err != nil {
		t.Fatalf("ApplyTrackEdits: %v", err)
	}
	waitForJob(t, jm, jobID, jobs.StatusCompleted)

	// DB updated.
	updated, err := lib.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	if updated.Title != "New Title" {
		t.Fatalf("title = %q, want New Title", updated.Title)
	}
	if updated.Genre == nil || *updated.Genre != "Rock" {
		t.Fatalf("genre = %v", updated.Genre)
	}
	if updated.Year == nil || *updated.Year != 2001 {
		t.Fatalf("year = %v", updated.Year)
	}
	// File renamed on disk.
	wantPath := filepath.Join(dir, "Artist", "Album", "New Title.flac")
	if updated.Path != wantPath {
		t.Fatalf("path = %q, want %q", updated.Path, wantPath)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}

	// Tags written through the stub exactly once.
	if len(stub.writeCalls) != 1 {
		t.Fatalf("writeCalls = %d, want 1", len(stub.writeCalls))
	}
	if stub.writeCalls[0].Fields["title"] != "New Title" {
		t.Fatalf("tag title = %v", stub.writeCalls[0].Fields["title"])
	}
	if stub.writeCalls[0].Fields["genre"] != "Rock" {
		t.Fatalf("tag genre = %v", stub.writeCalls[0].Fields["genre"])
	}

	// Change rows recorded under the change set.
	var rows []changeRow
	if err := svc.db.Select(&rows, "SELECT id, track_id, field, old_value, new_value, change_set_id FROM metadata_changes WHERE change_set_id = ?", changeSetID); err != nil {
		t.Fatalf("select changes: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("changes = %d, want 3 (title, genre, year)", len(rows))
	}
}

func TestApplyTrackEditsNullClears(t *testing.T) {
	svc, lib, jm, stub, dir := newJobService(t)
	trackID, _ := seedTrack(t, lib, dir, "Song")
	seedGenre := "Jazz"
	if _, err := svc.db.Exec("UPDATE tracks SET genre = ? WHERE id = ?", seedGenre, trackID); err != nil {
		t.Fatalf("seed genre: %v", err)
	}

	edits := []TrackEdit{{TrackID: trackID, Fields: map[string]interface{}{"genre": nil}}}
	jobID, changeSetID, err := svc.ApplyTrackEdits(edits)
	if err != nil {
		t.Fatalf("ApplyTrackEdits: %v", err)
	}
	waitForJob(t, jm, jobID, jobs.StatusCompleted)

	updated, _ := lib.GetTrack(trackID)
	if updated.Genre != nil {
		t.Fatalf("genre = %v, want NULL after clear", updated.Genre)
	}

	var rows []changeRow
	if err := svc.db.Select(&rows, "SELECT id, track_id, field, old_value, new_value, change_set_id FROM metadata_changes WHERE change_set_id = ?", changeSetID); err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].OldValue == nil || *rows[0].OldValue != "Jazz" {
		t.Fatalf("old = %v, want Jazz", rows[0].OldValue)
	}
	if rows[0].NewValue != nil {
		t.Fatalf("new = %v, want NULL", rows[0].NewValue)
	}
	// The stub saw a null field.
	if _, ok := stub.writeCalls[0].Fields["genre"]; !ok {
		t.Fatal("expected genre key in written fields (null)")
	}
	if v := stub.writeCalls[0].Fields["genre"]; v != nil {
		t.Fatalf("genre tag value = %v, want nil", v)
	}
}

func TestApplyTrackEditsTagFailureLeavesDBUntouched(t *testing.T) {
	svc, lib, jm, stub, dir := newJobService(t)
	trackID, _ := seedTrack(t, lib, dir, "Song")
	track, _ := lib.GetTrack(trackID)
	if err := os.WriteFile(track.Path, []byte("audio"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	stub.writeErr = errors.New("tag writer exploded")

	edits := []TrackEdit{{TrackID: trackID, Fields: map[string]interface{}{"title": "Changed"}}}
	jobID, changeSetID, err := svc.ApplyTrackEdits(edits)
	if err != nil {
		t.Fatalf("ApplyTrackEdits: %v", err)
	}
	waitForJob(t, jm, jobID, jobs.StatusCompleted)

	// DB must be untouched (title unchanged, no rename).
	updated, _ := lib.GetTrack(trackID)
	if updated.Title != "Song" {
		t.Fatalf("title = %q, want Song (unchanged after tag failure)", updated.Title)
	}
	if updated.Path != track.Path {
		t.Fatalf("path changed despite tag failure")
	}
	var rows []changeRow
	if err := svc.db.Select(&rows, "SELECT id FROM metadata_changes WHERE change_set_id = ?", changeSetID); err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("changes = %d, want 0 after tag failure", len(rows))
	}
}

func TestApplyAlbumEditRepointsTrack(t *testing.T) {
	svc, lib, jm, stub, dir := newJobService(t)
	trackID, _ := seedTrack(t, lib, dir, "Song")
	track, _ := lib.GetTrack(trackID)
	oldAlbumID := track.AlbumID
	if err := os.WriteFile(track.Path, []byte("audio"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	edits := []TrackEdit{{TrackID: trackID, Fields: map[string]interface{}{"album": "Greatest Hits"}}}
	jobID, changeSetID, err := svc.ApplyTrackEdits(edits)
	if err != nil {
		t.Fatalf("ApplyTrackEdits: %v", err)
	}
	waitForJob(t, jm, jobID, jobs.StatusCompleted)

	updated, err := lib.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	// The track is bound to the new album row...
	if updated.AlbumID == oldAlbumID {
		t.Fatal("track still bound to the old album")
	}
	target, err := lib.GetAlbum(updated.AlbumID)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if target.Title != "Greatest Hits" || target.Artist != "Artist" {
		t.Fatalf("target album = %q by %q", target.Title, target.Artist)
	}
	// ...created under the track's artist, the old album untouched.
	var oldTitle string
	if err := svc.db.Get(&oldTitle, "SELECT title FROM albums WHERE id = ?", oldAlbumID); err != nil {
		t.Fatalf("old album: %v", err)
	}
	if oldTitle != "Album" {
		t.Fatalf("old album title = %q, want Album", oldTitle)
	}

	// Album edits do not rename the file.
	if updated.Path != track.Path {
		t.Fatalf("path = %q, want %q (no rename for album edits)", updated.Path, track.Path)
	}

	// The file tag was written and the change row recorded old -> new.
	if len(stub.writeCalls) != 1 || stub.writeCalls[0].Fields["album"] != "Greatest Hits" {
		t.Fatalf("writeCalls = %+v", stub.writeCalls)
	}
	var rows []changeRow
	if err := svc.db.Select(&rows, "SELECT id, track_id, field, old_value, new_value, change_set_id FROM metadata_changes WHERE change_set_id = ?", changeSetID); err != nil {
		t.Fatalf("select: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("changes = %d, want 1", len(rows))
	}
	if rows[0].Field != "album" || rows[0].OldValue == nil || *rows[0].OldValue != "Album" || rows[0].NewValue == nil || *rows[0].NewValue != "Greatest Hits" {
		t.Fatalf("change row = %+v", rows[0])
	}
}

func TestApplyAlbumEditReusesExistingAlbum(t *testing.T) {
	svc, lib, jm, _, dir := newJobService(t)
	trackID, _ := seedTrack(t, lib, dir, "Song")
	artistID, _ := lib.EnsureArtist("Artist")
	existingID, err := lib.EnsureAlbum(artistID, "Known Album")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}

	jobID, _, err := svc.ApplyTrackEdits([]TrackEdit{{TrackID: trackID, Fields: map[string]interface{}{"album": "Known Album"}}})
	if err != nil {
		t.Fatalf("ApplyTrackEdits: %v", err)
	}
	waitForJob(t, jm, jobID, jobs.StatusCompleted)

	updated, _ := lib.GetTrack(trackID)
	if updated.AlbumID != existingID {
		t.Fatalf("album_id = %s, want the pre-existing album %s", updated.AlbumID, existingID)
	}
	var count int
	if err := svc.db.Get(&count, "SELECT COUNT(*) FROM albums WHERE title = 'Known Album'"); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("albums named 'Known Album' = %d, want 1", count)
	}
}

func TestApplyTrackEditsUnknownTrack(t *testing.T) {
	svc, _, _, _, _ := newJobService(t)
	edits := []TrackEdit{{TrackID: "does-not-exist", Fields: map[string]interface{}{"genre": "X"}}}
	_, _, err := svc.ApplyTrackEdits(edits)
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("error = %v, want *NotFoundError", err)
	}
}
