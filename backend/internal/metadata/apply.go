package metadata

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"yt-scraper/internal/fsutil"
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
	// ExpectedVersion is the optimistic lock (T16): the track version the
	// editor saw when it loaded the track. Optional — absent means the
	// caller does not want locking (back-compat). Present and mismatched,
	// the track is skipped and reported in the job result; if every track
	// mismatches the request itself fails with 409.
	ExpectedVersion *int `json:"expected_version"`
	// OnCollision selects the rename-collision mode (T16) for a title
	// change whose target name is taken: "suffix" (default, T17
	// auto-numbering), "skip" (keep the file name, update the title only)
	// or "replace" (move the existing file to the trash first).
	OnCollision string `json:"on_collision"`
}

// NormalizedEdit is a validated edit: set fields (typed values) and fields
// to clear.
type NormalizedEdit struct {
	TrackID         string
	Set             map[string]interface{}
	Cleared         []string
	ExpectedVersion *int
	OnCollision     string // normalized: "" becomes "suffix"
}

// Rename collision modes accepted in the PATCH body.
const (
	OnCollisionSuffix  = "suffix"
	OnCollisionSkip    = "skip"
	OnCollisionReplace = "replace"
)

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

		if e.ExpectedVersion != nil && *e.ExpectedVersion < 0 {
			return nil, &ValidationError{Message: "expected_version must be a non-negative integer", Field: "expected_version", TrackID: e.TrackID}
		}
		switch e.OnCollision {
		case "", OnCollisionSuffix, OnCollisionSkip, OnCollisionReplace:
		default:
			return nil, &ValidationError{Message: "on_collision must be suffix, skip or replace", Field: "on_collision", TrackID: e.TrackID}
		}

		normalized := NormalizedEdit{TrackID: e.TrackID, Set: map[string]interface{}{}}
		normalized.ExpectedVersion = e.ExpectedVersion
		if e.OnCollision == "" {
			normalized.OnCollision = OnCollisionSuffix
		} else {
			normalized.OnCollision = e.OnCollision
		}
		for name, raw := range e.Fields {
			switch {
			case raw == nil:
				if !isKnownField(name) {
					return nil, &ValidationError{Message: "unknown field", Field: name, TrackID: e.TrackID}
				}
				if name == "album" {
					// A track must always belong to an album row, so the
					// field can be moved but not cleared.
					return nil, &ValidationError{
						Message: "album cannot be cleared; set a new album name instead",
						Field:   name,
						TrackID: e.TrackID,
					}
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
//
// Optimistic locking (T16): every edit carries an optional
// expected_version, the version the editor loaded. The check runs here,
// synchronously, so a request in which every track already lost the lock
// fails with a VersionConflictError (HTTP 409) instead of a 202 and a
// job that skips everything. Partial conflicts are accepted (202): the
// job skips the stale tracks and reports them in its results.
func (s *Service) ApplyTrackEdits(edits []TrackEdit) (string, string, error) {
	normalized, err := ValidateEdits(edits)
	if err != nil {
		return "", "", err
	}
	// Fail fast on unknown tracks (the job re-checks when it runs) and
	// run the optimistic-lock pre-check.
	var conflicts []map[string]interface{}
	for _, e := range normalized {
		track, err := s.lib.GetTrack(e.TrackID)
		if err != nil {
			return "", "", &NotFoundError{Message: "track not found", TrackID: e.TrackID}
		}
		if e.ExpectedVersion != nil && track.Version != *e.ExpectedVersion {
			conflicts = append(conflicts, s.versionConflictEntry(track))
			// Record the rejected edit as a concurrent conflict, so the
			// lost change shows up on the Conflicts page instead of
			// vanishing into a 409 body.
			s.recordConcurrentConflicts(track, e)
		}
	}
	if len(conflicts) > 0 && len(conflicts) == len(normalized) {
		return "", "", &VersionConflictError{Conflicts: conflicts}
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
// Renames that had to be auto-numbered leave a note in the job's progress
// text and a per-track result (rename_collision/final_filename) in its
// details, for the UI to display. Skipped tracks (version conflict,
// external change detected by the apply-time re-read) carry their own
// per-track result entry (T16).
func (s *Service) runApplyJob(jobID, changeSetID string, edits []NormalizedEdit) {
	total := strconv.Itoa(len(edits))
	if err := s.jm.UpdateJobStatus(jobID, jobs.StatusRunning, "0/"+total, ""); err != nil {
		log.Printf("metadata: update job %s: %v", jobID, err)
	}

	notes := []string{}
	results := []interface{}{}
	for i, e := range edits {
		track, err := s.lib.GetTrack(e.TrackID)
		if err != nil {
			log.Printf("metadata: track %s no longer exists, skipping its edits", e.TrackID)
			continue
		}
		outcome := s.applyEditsToTrack(track, e, changeSetID)
		if outcome.note != "" {
			notes = append(notes, outcome.note)
		}
		if outcome.rename != nil {
			results = append(results, *outcome.rename)
		}
		if outcome.result != nil {
			results = append(results, outcome.result)
		}
		if err := s.jm.UpdateJobStatus(jobID, jobs.StatusRunning, strconv.Itoa(i+1)+"/"+total, ""); err != nil {
			log.Printf("metadata: update job %s: %v", jobID, err)
		}
	}

	// Merge the results before flipping the status, so a job that reads
	// "completed" already carries them.
	if len(results) > 0 {
		s.appendJobResults(jobID, results)
	}
	progress := "100%"
	if len(notes) > 0 {
		progress += ": " + strings.Join(notes, "; ")
	}
	if err := s.jm.UpdateJobStatus(jobID, jobs.StatusCompleted, progress, ""); err != nil {
		log.Printf("metadata: update job %s: %v", jobID, err)
	}
}

// applyEditsToTrack applies one track's edits. If the tag write or the
// file rename fails, the track is left completely untouched (no DB update,
// no change records) so the database never diverges from the file. The
// returned outcome carries the rename note and per-track results: an
// auto-numbered rename (renameCollision), a skipped rename, or a skipped
// track (version conflict / external change, T16).
func (s *Service) applyEditsToTrack(track *library.Track, edit NormalizedEdit, changeSetID string) applyOutcome {
	var outcome applyOutcome

	// 0. Optimistic lock, re-checked at run time (the synchronous
	// pre-check in ApplyTrackEdits already answered 409 when every track
	// was stale; this catches the window between that check and now).
	if edit.ExpectedVersion != nil && track.Version != *edit.ExpectedVersion {
		s.recordConcurrentConflicts(track, edit)
		outcome.result = s.versionConflictEntry(track)
		return outcome
	}

	// 0b. Apply-time re-read (T16): re-read the file and compare it with
	// the DB snapshot in every field being edited. An external change
	// there means the user's edit would silently clobber it: the track is
	// skipped, the divergence is recorded as a source='apply' conflict and
	// reported in the job result. A read failure is logged and the apply
	// proceeds — the tag write itself still fails on an unreadable file.
	if fileFields, err := s.tags.ReadFields(track.Path); err != nil {
		log.Printf("metadata: re-read tags for %s before apply: %v", track.Path, err)
	} else {
		dbValues := s.lib.RowTagValues(track)
		if external := externalChangeFields(dbValues, fileFields, edit); len(external) > 0 {
			fileNorm := library.NormalizeTagValues(fileFields)
			for _, field := range external {
				if _, err := s.lib.InsertConflict(track.ID, field,
					library.TagValueText(field, dbValues[field]),
					library.TagValueText(field, fileNorm[field]),
					library.ConflictSourceApply); err != nil {
					log.Printf("metadata: record apply conflict %s/%s: %v", track.ID, field, err)
				}
			}
			if err := s.lib.SetTrackState(track.ID, library.TrackStateConflict); err != nil {
				log.Printf("metadata: set state for %s: %v", track.ID, err)
			}
			outcome.result = map[string]interface{}{
				"track_id": track.ID,
				"reason":   "external_change",
				"fields":   external,
			}
			return outcome
		}
	}

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
			return outcome
		}
	}

	// 2. Rename on disk when the title changed (existing naming scheme:
	// sanitized title plus the original extension). The collision mode is
	// per-track (T16): suffix (default) auto-numbers " (2)", " (3)", ...;
	// skip keeps the file name and updates the title only; replace moves
	// the existing file to the trash first (never deletes it).
	newTitle := track.Title
	if value, ok := edit.Set["title"]; ok {
		newTitle = value.(string)
	}
	newPath := track.Path
	if newTitle != track.Title {
		base := sanitizeFilename(newTitle)
		ext := filepath.Ext(track.Path)
		candidate := filepath.Join(filepath.Dir(track.Path), base+ext)
		if candidate != track.Path {
			switch edit.OnCollision {
			case OnCollisionSkip:
				// Keep the file name; the title (and tags) still change.
				if _, err := os.Stat(candidate); err == nil {
					outcome.result = map[string]interface{}{
						"track_id": track.ID,
						"reason":   "rename_skipped",
						"existing": filepath.Base(candidate),
					}
					outcome.note = fmt.Sprintf("rename skipped for %q (name taken)", newTitle)
				}
			case OnCollisionReplace:
				taken := false
				if _, err := os.Stat(candidate); err == nil {
					taken = true
					trashID, terr := s.lib.TrashFile(candidate, track.ID)
					if terr != nil {
						log.Printf("metadata: move %s to trash: %v", candidate, terr)
						return outcome
					}
					outcome.note = fmt.Sprintf("moved %q to the trash (id %s)", filepath.Base(candidate), trashID)
				}
				if err := fsutil.SafeRename(track.Path, candidate); err != nil {
					log.Printf("metadata: rename %s to %s: %v", track.Path, candidate, err)
					return outcome
				}
				newPath = candidate
				if taken {
					outcome.rename = &renameResult{
						TrackID:         track.ID,
						RenameCollision: true,
						FinalFilename:   filepath.Base(candidate),
					}
				}
			default: // OnCollisionSuffix (T17 behaviour)
				finalPath, suffixed, err := renameFileNoOverwrite(track.Path, filepath.Dir(track.Path), base, ext)
				if err != nil {
					log.Printf("metadata: rename %s to %s: %v", track.Path, candidate, err)
					return outcome
				}
				newPath = finalPath
				if suffixed {
					outcome.note = renameNote(filepath.Base(finalPath))
					outcome.rename = &renameResult{
						TrackID:         track.ID,
						RenameCollision: true,
						FinalFilename:   filepath.Base(finalPath),
					}
				}
			}
		}
	}

	// 3. Album edits move the track to the target album row: the album
	// lives in the albums table (keyed by artist + title), so the track is
	// repointed rather than having a column updated. The file is not
	// renamed for an album edit (the naming scheme is title-based).
	if name, ok := edit.Set["album"]; ok {
		albumID, err := s.ensureTargetAlbum(track, name.(string))
		if err != nil {
			log.Printf("metadata: resolve album %q for track %s: %v", name, track.ID, err)
			return outcome
		}
		if _, err := s.db.Exec("UPDATE tracks SET album_id = ?, version = version + 1 WHERE id = ?", albumID, track.ID); err != nil {
			log.Printf("metadata: repoint track %s: %v", track.ID, err)
			return outcome
		}
	}

	// 4. Update the database row. Every edited field except "album"
	// (handled above) maps to a track column.
	//
	// Accepted parity gap (same as the pre-existing ApplyChanges): the
	// rename on disk has already happened, so a failure here — or of the
	// process — can leave the renamed file ahead of the database row, and
	// the rename is not rolled back. An edit without a rename can simply
	// be re-run; after a title rename, deleting the stale row and
	// rescanning restores the library. The change row is only recorded
	// after this DB update succeeds, so a failed edit never leaves a
	// phantom history entry.
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
		// Every write that reaches this point changes track data, so the
		// optimistic lock advances with it (T16).
		args = append(args, track.ID)
		if _, err := s.db.Exec("UPDATE tracks SET "+strings.Join(sets, ", ")+", version = version + 1 WHERE id = ?", args...); err != nil {
			log.Printf("metadata: update track %s: %v", track.ID, err)
			return outcome
		}
	}

	// 5. Record one row per changed field under the change set.
	for name, value := range edit.Set {
		s.recordChange(track, name, value, changeSetID)
	}
	for _, name := range edit.Cleared {
		s.recordChange(track, name, nil, changeSetID)
	}

	// 6. Refresh the sync fingerprint: the file was just written with the
	// new values and the DB row matches it, so the two are known-equal
	// again. A mid-conflict track keeps its state (the open conflicts
	// still describe the divergence) but gets a fresh fingerprint so the
	// next scan compares against the new file.
	s.refreshSyncFingerprint(track.ID, newPath)
	return outcome
}

// ensureTargetAlbum finds the album with the given title under the
// track's artist, creating it with the standard artist/album scheme when
// it does not exist yet, and returns its id. A track without an artist of
// its own keeps the artist of the album it currently belongs to.
func (s *Service) ensureTargetAlbum(track *library.Track, albumName string) (string, error) {
	artistName := ""
	if track.Artist != nil {
		artistName = *track.Artist
	}
	if strings.TrimSpace(artistName) == "" {
		album, err := s.lib.GetAlbum(track.AlbumID)
		if err != nil {
			return "", err
		}
		artistName = album.Artist
	}
	artistID, err := s.lib.EnsureArtist(artistName)
	if err != nil {
		return "", err
	}
	return s.lib.EnsureAlbum(artistID, albumName)
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

// versionConflictEntry is the per-track job result / 409 body for a track
// that lost the optimistic lock: the current field values (the "theirs"
// side of the diff) and the version to re-apply against.
func (s *Service) versionConflictEntry(track *library.Track) map[string]interface{} {
	values := s.lib.RowTagValues(track)
	current := make(map[string]interface{}, len(values))
	for field, value := range values {
		current[field] = value
	}
	return map[string]interface{}{
		"track_id":        track.ID,
		"reason":          "version_conflict",
		"current":         current,
		"current_version": track.Version,
	}
}

// recordConcurrentConflicts records one sync_conflicts row (source
// 'concurrent') per edited field when the version lock rejects the edit.
// The DB keeps its value and the file is untouched; the row preserves the
// rejected edit so it can be re-applied or inspected on the Conflicts page
// (nothing is silently lost).
func (s *Service) recordConcurrentConflicts(track *library.Track, edit NormalizedEdit) {
	dbValues := s.lib.RowTagValues(track)
	for name, value := range edit.Set {
		if _, err := s.lib.InsertConflict(track.ID, name,
			library.TagValueText(name, dbValues[name]),
			library.TagValueText(name, value),
			library.ConflictSourceConcurrent); err != nil {
			log.Printf("metadata: record concurrent conflict %s/%s: %v", track.ID, name, err)
		}
	}
	for _, name := range edit.Cleared {
		if _, err := s.lib.InsertConflict(track.ID, name,
			library.TagValueText(name, dbValues[name]),
			"",
			library.ConflictSourceConcurrent); err != nil {
			log.Printf("metadata: record concurrent conflict %s/%s: %v", track.ID, name, err)
		}
	}
}

// externalChangeFields returns the fields being edited whose on-disk value
// differs from the DB snapshot (normalized comparison: int fields parse
// "3/12" -> 3, missing values compare equal to 0/"").
func externalChangeFields(dbValues library.TagValues, fileFields map[string]interface{}, edit NormalizedEdit) []string {
	fileNorm := library.NormalizeTagValues(fileFields)
	seen := make(map[string]bool, len(edit.Set)+len(edit.Cleared))
	for name := range edit.Set {
		seen[name] = true
	}
	for _, name := range edit.Cleared {
		seen[name] = true
	}
	fields := []string{}
	for name := range seen {
		if !library.IsCanonicalTagField(name) {
			continue
		}
		if !library.TagValueEqual(name, fileNorm[name], dbValues[name]) {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}

// refreshSyncFingerprint re-stats the (possibly renamed) file and stores
// the fresh fingerprint plus synced_at after a successful apply. A track
// in state=conflict keeps its state and its old synced_at (the open
// conflicts still describe the divergence) but gets the new fingerprint,
// so the next scan compares against the file that now exists.
func (s *Service) refreshSyncFingerprint(trackID, path string) {
	st, err := os.Stat(path)
	if err != nil {
		log.Printf("metadata: stat %s after apply: %v", path, err)
		return
	}
	track, err := s.lib.GetTrack(trackID)
	if err != nil {
		log.Printf("metadata: re-read track %s after apply: %v", trackID, err)
		return
	}
	hash := library.TagsHash(s.lib.RowTagValues(track))
	state := track.State
	syncedAt := ""
	if state == library.TrackStateMissing {
		state = library.TrackStateOK
	}
	if state != library.TrackStateConflict {
		syncedAt = library.NowSQL()
	} else if track.SyncedAt != nil {
		syncedAt = *track.SyncedAt
	}
	if err := s.lib.SetSyncState(trackID, st.Size(), st.ModTime().Unix(), hash, syncedAt, state, false); err != nil {
		log.Printf("metadata: set sync state for %s: %v", trackID, err)
	}
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
