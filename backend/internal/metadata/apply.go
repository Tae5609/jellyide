package metadata

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
)

// intFromValue converts a JSON-decoded (or Go-native) number to a
// non-negative int; the second return value reports success.
func intFromValue(raw interface{}) (int, bool) {
	switch v := raw.(type) {
	case float64:
		if v != float64(int64(v)) || v < 0 {
			return 0, false
		}
		return int(v), true
	case int:
		if v < 0 {
			return 0, false
		}
		return v, true
	case int64:
		if v < 0 {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}

// TrackEdit is one entry of PATCH /metadata/tracks.
type TrackEdit struct {
	TrackID string                 `json:"track_id"`
	Fields  map[string]interface{} `json:"fields"`
}

// NormalizedEdit is a validated edit: set fields (typed values) and fields
// to clear.
type NormalizedEdit struct {
	TrackID string
	Set     map[string]interface{}
	Cleared []string
}

// ValidateEdits checks a bulk edit request and normalizes the values.
// Only keys present are touched; a null value clears the field.
func ValidateEdits(edits []TrackEdit) ([]NormalizedEdit, error) {
	if len(edits) == 0 {
		return nil, &ValidationError{Message: "edits must not be empty"}
	}

	seen := make(map[string]map[string]bool, len(edits))
	out := make([]NormalizedEdit, 0, len(edits))
	for _, e := range edits {
		if e.TrackID == "" {
			return nil, &ValidationError{Message: "track_id is required"}
		}
		if len(e.Fields) == 0 {
			return nil, &ValidationError{Message: "fields must not be empty", TrackID: e.TrackID}
		}

		normalized := NormalizedEdit{TrackID: e.TrackID, Set: map[string]interface{}{}}
		for name, raw := range e.Fields {
			switch {
			case raw == nil:
				if !isKnownField(name) {
					return nil, &ValidationError{Message: "unknown field", Field: name, TrackID: e.TrackID}
				}
				normalized.Cleared = append(normalized.Cleared, name)
			case textFields[name]:
				text, ok := raw.(string)
				if !ok {
					return nil, &ValidationError{Message: "field must be a string or null", Field: name, TrackID: e.TrackID}
				}
				if name == "title" && strings.TrimSpace(text) == "" {
					return nil, &ValidationError{Message: "title must not be empty", Field: name, TrackID: e.TrackID}
				}
				normalized.Set[name] = text
			case intFields[name]:
				number, ok := intFromValue(raw)
				if !ok {
					return nil, &ValidationError{Message: "field must be a non-negative integer or null", Field: name, TrackID: e.TrackID}
				}
				if msg := fieldRangeError(name, number); msg != "" {
					return nil, &ValidationError{Message: msg, Field: name, TrackID: e.TrackID}
				}
				normalized.Set[name] = number
			default:
				return nil, &ValidationError{Message: "unknown field", Field: name, TrackID: e.TrackID}
			}

			if seen[e.TrackID] == nil {
				seen[e.TrackID] = map[string]bool{}
			}
			if seen[e.TrackID][name] {
				return nil, &ValidationError{Message: "duplicate field in one request", Field: name, TrackID: e.TrackID}
			}
			seen[e.TrackID][name] = true
		}
		out = append(out, normalized)
	}
	return out, nil
}

// ApplyTrackEdits validates a bulk edit request and queues it as a
// metadata_apply job. It returns the job id and the change-set id the
// resulting metadata_changes rows will share.
func (s *Service) ApplyTrackEdits(edits []TrackEdit) (string, string, error) {
	normalized, err := ValidateEdits(edits)
	if err != nil {
		return "", "", err
	}
	// Fail fast on unknown tracks; the job re-checks when it runs.
	for _, e := range normalized {
		if _, err := s.lib.GetTrack(e.TrackID); err != nil {
			return "", "", &NotFoundError{Message: "track not found", TrackID: e.TrackID}
		}
	}

	changeSetID := uuid.New().String()
	job, err := s.jm.CreateJob(jobs.TypeMetadata, map[string]string{
		"action":        "apply_edits",
		"change_set_id": changeSetID,
		"edits":         strconv.Itoa(len(normalized)),
	})
	if err != nil {
		return "", "", err
	}

	go s.runApplyJob(job.ID, changeSetID, normalized)
	return job.ID, changeSetID, nil
}

// runApplyJob is the metadata_apply job body: for every edit it writes the
// tags, renames the file when the title changed, updates the database and
// records one metadata_changes row per field, all under the change-set id.
func (s *Service) runApplyJob(jobID, changeSetID string, edits []NormalizedEdit) {
	total := strconv.Itoa(len(edits))
	if err := s.jm.UpdateJobStatus(jobID, jobs.StatusRunning, "0/"+total, ""); err != nil {
		log.Printf("metadata: update job %s: %v", jobID, err)
	}

	for i, e := range edits {
		track, err := s.lib.GetTrack(e.TrackID)
		if err != nil {
			log.Printf("metadata: track %s no longer exists, skipping its edits", e.TrackID)
			continue
		}
		s.applyEditsToTrack(track, e, changeSetID)
		if err := s.jm.UpdateJobStatus(jobID, jobs.StatusRunning, strconv.Itoa(i+1)+"/"+total, ""); err != nil {
			log.Printf("metadata: update job %s: %v", jobID, err)
		}
	}

	if err := s.jm.UpdateJobStatus(jobID, jobs.StatusCompleted, "100%", ""); err != nil {
		log.Printf("metadata: update job %s: %v", jobID, err)
	}
}

// applyEditsToTrack applies one track's edits. If the tag write or the
// file rename fails, the track is left completely untouched (no DB update,
// no change records) so the database never diverges from the file.
func (s *Service) applyEditsToTrack(track *library.Track, edit NormalizedEdit, changeSetID string) {
	// 1. Write the tags on disk.
	fields := make(map[string]interface{}, len(edit.Set)+len(edit.Cleared))
	for name, value := range edit.Set {
		fields[name] = value
	}
	for _, name := range edit.Cleared {
		fields[name] = nil
	}
	if len(fields) > 0 {
		if err := s.tags.WriteFields(track.Path, fields); err != nil {
			log.Printf("metadata: write tags for %s: %v", track.Path, err)
			return
		}
	}

	// 2. Rename on disk when the title changed (existing naming scheme:
	// sanitized title plus the original extension).
	newTitle := track.Title
	if value, ok := edit.Set["title"]; ok {
		newTitle = value.(string)
	}
	newPath := track.Path
	if newTitle != track.Title {
		newPath = filepath.Join(filepath.Dir(track.Path), sanitizeFilename(newTitle)+filepath.Ext(track.Path))
		if newPath != track.Path {
			if err := os.Rename(track.Path, newPath); err != nil {
				log.Printf("metadata: rename %s to %s: %v", track.Path, newPath, err)
				return
			}
		}
	}

	// 3. Update the database row. Every edited field except "album" (which
	// lives on the album row, not the track row) maps to a track column.
	sets := []string{}
	args := []interface{}{}
	for name, value := range edit.Set {
		if name == "album" {
			continue
		}
		sets = append(sets, name+" = ?")
		args = append(args, value)
	}
	for _, name := range edit.Cleared {
		if name == "album" {
			continue
		}
		sets = append(sets, name+" = ?")
		args = append(args, nil)
	}
	if newPath != track.Path {
		sets = append(sets, "path = ?")
		args = append(args, newPath)
	}
	if len(sets) > 0 {
		args = append(args, track.ID)
		if _, err := s.db.Exec("UPDATE tracks SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...); err != nil {
			log.Printf("metadata: update track %s: %v", track.ID, err)
			return
		}
	}

	// 4. Record one row per changed field under the change set.
	for name, value := range edit.Set {
		s.recordChange(track, name, value, changeSetID)
	}
	for _, name := range edit.Cleared {
		s.recordChange(track, name, nil, changeSetID)
	}
}

// recordChange inserts one metadata_changes row. old and new are the typed
// values (nil = NULL).
func (s *Service) recordChange(track *library.Track, field string, newValue interface{}, changeSetID string) {
	oldValue := s.oldValueOf(track, field)
	row := &changeRow{
		ID:          uuid.New().String(),
		TrackID:     track.ID,
		Field:       field,
		OldValue:    toText(oldValue),
		NewValue:    toText(newValue),
		ChangeSetID: changeSetID,
	}
	query := `INSERT INTO metadata_changes (id, track_id, field, old_value, new_value, change_set_id) VALUES (?, ?, ?, ?, ?, ?)`
	if _, err := s.db.Exec(query, row.ID, row.TrackID, row.Field, row.OldValue, row.NewValue, row.ChangeSetID); err != nil {
		log.Printf("metadata: record change for track %s: %v", track.ID, err)
	}
}

// changeRow is one metadata_changes row.
type changeRow struct {
	ID          string  `db:"id"`
	TrackID     string  `db:"track_id"`
	Field       string  `db:"field"`
	OldValue    *string `db:"old_value"`
	NewValue    *string `db:"new_value"`
	ChangeSetID string  `db:"change_set_id"`
}

// oldValueOf returns the current database value of a track field as an
// interface{} (nil = NULL).
func (s *Service) oldValueOf(track *library.Track, field string) interface{} {
	switch field {
	case "title":
		return track.Title
	case "album":
		album, err := s.albumTitle(track.AlbumID)
		if err != nil {
			return nil
		}
		return album
	case "artist":
		return track.Artist
	case "album_artist":
		return track.AlbumArtist
	case "track_number":
		return track.TrackNumber
	case "disc_number":
		return track.DiscNumber
	case "year":
		return track.Year
	case "genre":
		return track.Genre
	case "composer":
		return track.Composer
	case "comment":
		return track.Comment
	default:
		return nil
	}
}

func (s *Service) albumTitle(albumID string) (string, error) {
	album, err := s.lib.GetAlbum(albumID)
	if err != nil {
		return "", err
	}
	return album.Title, nil
}

// toText converts a typed field value (string, int, *string, *int or nil)
// to its stored text form (nil = NULL).
func toText(value interface{}) *string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return &v
	case int:
		text := strconv.Itoa(v)
		return &text
	case *string:
		return v
	case *int:
		if v == nil {
			return nil
		}
		text := strconv.Itoa(*v)
		return &text
	default:
		text := fmt.Sprint(v)
		return &text
	}
}
