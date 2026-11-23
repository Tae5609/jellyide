package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"

	"yt-scraper/internal/fsutil"
)

// Sync states of a track row.
const (
	TrackStateOK       = "ok"
	TrackStateMissing  = "missing" // file gone from disk; the row is kept
	TrackStateConflict = "conflict"
)

// Conflict sources and statuses.
const (
	ConflictSourceScan       = "scan"       // changed on disk by another program
	ConflictSourceApply      = "apply"      // changed on disk during an apply
	ConflictSourceConcurrent = "concurrent" // changed by another user (version lock)

	ConflictStatusOpen      = "open"
	ConflictStatusResolved  = "resolved"
	ConflictStatusDismissed = "dismissed"
)

// Sync conflict policies (settings table, env SYNC_CONFLICT_POLICY).
const (
	SyncPolicyAsk      = "ask"
	SyncPolicyFileWins = "file_wins"
	SyncPolicyDBWins   = "db_wins"

	// SettingKeySyncConflictPolicy is the settings-table key for the
	// policy. The environment only seeds the initial value; once the row
	// exists, the database value wins.
	SettingKeySyncConflictPolicy = "sync_conflict_policy"
)

// IsValidSyncPolicy reports whether policy is one of the three policies.
func IsValidSyncPolicy(policy string) bool {
	switch policy {
	case SyncPolicyAsk, SyncPolicyFileWins, SyncPolicyDBWins:
		return true
	}
	return false
}

// Conflict is one row in the sync_conflicts table.
type Conflict struct {
	ID         string  `json:"id" db:"id"`
	ConflictID string  `json:"conflict_id" db:"-"` // mirrors ID in the grouped API shape
	TrackID    string  `json:"track_id" db:"track_id"`
	Field      string  `json:"field" db:"field"`
	DBValue    *string `json:"db_value" db:"db_value"`
	FileValue  *string `json:"file_value" db:"file_value"`
	Source     string  `json:"source" db:"source"`
	DetectedAt string  `json:"detected_at" db:"detected_at"`
	Status     string  `json:"status" db:"status"`
	ResolvedBy *string `json:"resolved_by" db:"resolved_by"`
	ResolvedAt *string `json:"resolved_at" db:"resolved_at"`
	Resolution *string `json:"resolution" db:"resolution"`
}

// TrashEntry is one row in the sync_trash table (a file displaced by a
// "replace" rename collision).
type TrashEntry struct {
	ID           string  `json:"id" db:"id"`
	OriginalPath string  `json:"original_path" db:"original_path"`
	TrashPath    string  `json:"trash_path" db:"trash_path"`
	TrackID      *string `json:"track_id" db:"track_id"`
	CreatedAt    string  `json:"created_at" db:"created_at"`
}

// NowSQL renders the current UTC time in the layout SQLite's
// CURRENT_TIMESTAMP uses, so stored values compare correctly.
func NowSQL() string {
	return time.Now().UTC().Format("2006-01-02 15:04:05")
}

// nowSQL is the internal alias used throughout this file.
func nowSQL() string { return NowSQL() }

// GetTrackByPath looks up a track by its file path (sql.ErrNoRows).
func (s *Service) GetTrackByPath(path string) (*Track, error) {
	var track Track
	err := s.db.Get(&track, "SELECT * FROM tracks WHERE path = ?", path)
	if err != nil {
		return nil, err
	}
	return &track, nil
}

// CreateTrack inserts a new track row with its sync fingerprint already
// set (a freshly indexed file is by definition in sync with itself).
func (s *Service) CreateTrack(albumID, title, path string, duration, bitrate int, meta TrackMeta, size, mtime int64, tagsHash string) error {
	id := uuid.New().String()
	_, err := s.db.Exec(`
		INSERT INTO tracks (id, album_id, title, path, duration, bitrate,
			artist, album_artist, track_number, disc_number, year, genre, composer, comment,
			file_size, file_mtime, tags_hash, version, synced_at, state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`, id, albumID, title, path, duration, bitrate,
		meta.Artist, meta.AlbumArtist, meta.TrackNumber, meta.DiscNumber,
		meta.Year, meta.Genre, meta.Composer, meta.Comment,
		size, mtime, tagsHash, nowSQL(), TrackStateOK)
	return err
}

// EditedSince reports whether any metadata_changes row exists for the
// track with timestamp after since. An empty since means "since the
// beginning" (the track has never been synchronized).
func (s *Service) EditedSince(trackID, since string) (bool, error) {
	query := "SELECT COUNT(*) FROM metadata_changes WHERE track_id = ?"
	args := []interface{}{trackID}
	if since != "" {
		// Every writer stores "2006-01-02 15:04:05" (UTC), but a value
		// read back through the driver comes in RFC3339 form; normalize
		// so the string comparison in SQL is apples to apples.
		if t, err := parseSyncTime(since); err == nil {
			since = t.Format("2006-01-02 15:04:05")
		}
		query += " AND timestamp > ?"
		args = append(args, since)
	}
	var n int
	if err := s.db.Get(&n, query, args...); err != nil {
		return false, err
	}
	return n > 0, nil
}

// parseSyncTime parses the datetime layouts used by this codebase: the
// canonical "2006-01-02 15:04:05" (UTC) and the RFC3339 form the SQLite
// driver returns when a DATETIME column is scanned into a string.
func parseSyncTime(v string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, v, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("unrecognized time format: " + v)
}

// SetSyncState updates the sync bookkeeping columns of a track row.
// bumpVersion is true only when a track field value changed (the scanner's
// file-wins path, apply, resolve): it is what invalidates editors'
// expected_version. Fingerprint-only updates (state, synced_at, hash,
// size, mtime) pass false.
func (s *Service) SetSyncState(trackID string, size, mtime int64, tagsHash, syncedAt, state string, bumpVersion bool) error {
	versionSQL := ""
	if bumpVersion {
		versionSQL = ", version = version + 1"
	}
	_, err := s.db.Exec(
		"UPDATE tracks SET file_size = ?, file_mtime = ?, tags_hash = ?"+versionSQL+
			", synced_at = ?, state = ? WHERE id = ?",
		size, mtime, tagsHash, syncedAt, state, trackID,
	)
	return err
}

// SetTrackState changes only the state column (no fingerprint change).
func (s *Service) SetTrackState(trackID, state string) error {
	_, err := s.db.Exec("UPDATE tracks SET state = ? WHERE id = ?", state, trackID)
	return err
}

// MarkMissing sets state='missing' for a track whose file no longer
// exists. The row (and its history) is kept, never deleted.
func (s *Service) MarkMissing(trackID string) error {
	_, err := s.db.Exec("UPDATE tracks SET state = ? WHERE id = ? AND state <> ?",
		TrackStateMissing, trackID, TrackStateMissing)
	return err
}

// ListMissing returns all tracks whose file is gone.
func (s *Service) ListMissing() ([]Track, error) {
	tracks := []Track{}
	err := s.db.Select(&tracks, "SELECT * FROM tracks WHERE state = ? ORDER BY title ASC", TrackStateMissing)
	return tracks, err
}

// ListTracks returns every track (for the scanner's ghost pass).
func (s *Service) ListTracks() ([]Track, error) {
	tracks := []Track{}
	err := s.db.Select(&tracks, "SELECT * FROM tracks")
	return tracks, err
}

// SyncFileToDB applies the file's tag values to the track row (the
// file-wins path): the named columns, the album repoint and the duration
// and bitrate are updated, the version is bumped, and one metadata_changes
// row is recorded per changed field under changeSetID. It returns the
// names of the changed fields.
func (s *Service) SyncFileToDB(track *Track, values TagValues, duration, bitrate int, albumID, changeSetID string) ([]string, error) {
	dbValues := s.RowTagValues(track)
	changed := DifferingFields(dbValues, values)

	sets := []string{}
	args := []interface{}{}
	add := func(column string, value interface{}) {
		sets = append(sets, column+" = ?")
		args = append(args, value)
	}
	if v, ok := values["title"].(string); ok {
		add("title", v)
	}
	add("duration", duration)
	add("bitrate", bitrate)
	add("album_id", albumID)
	for _, field := range []string{"artist", "album_artist", "genre", "composer", "comment"} {
		if v, ok := values[field].(string); ok {
			add(field, orNil(v))
		}
	}
	for _, field := range []string{"track_number", "disc_number", "year"} {
		if n, ok := values[field].(int); ok {
			add(field, orNilInt(n))
		}
	}
	// Bump the version only when the row data actually changes: the
	// editor's expected_version must invalidate when the data moves, but
	// an idempotent write must not.
	bump := len(changed) > 0 || track.Duration != duration || track.Bitrate != bitrate || track.AlbumID != albumID
	versionSQL := ""
	if bump {
		versionSQL = ", version = version + 1"
	}
	args = append(args, track.ID)
	if _, err := s.db.Exec("UPDATE tracks SET "+joinSets(sets)+versionSQL+" WHERE id = ?", args...); err != nil {
		return nil, err
	}

	for _, field := range changed {
		s.insertChange(track.ID, field, TagValueText(field, dbValues[field]), TagValueText(field, values[field]), changeSetID)
	}
	return changed, nil
}

// RowTagValues is TrackTagValues but fetches the album title itself.
func (s *Service) RowTagValues(track *Track) TagValues {
	albumTitle := ""
	if track.AlbumID != "" {
		albumTitle, _ = s.albumTitle(track.AlbumID)
	}
	return TrackTagValues(track, albumTitle)
}

// albumTitle returns the album row's title ("" when it does not exist).
func (s *Service) albumTitle(albumID string) (string, error) {
	var title string
	err := s.db.Get(&title, "SELECT title FROM albums WHERE id = ?", albumID)
	return title, err
}

// insertChange records one metadata_changes row (the text form the
// history and revert flows expect; "" is stored as NULL).
func (s *Service) insertChange(trackID, field, oldValue, newValue, changeSetID string) {
	query := `INSERT INTO metadata_changes (id, track_id, field, old_value, new_value, change_set_id) VALUES (?, ?, ?, ?, ?, ?)`
	if _, err := s.db.Exec(query, uuid.New().String(), trackID, field,
		sqlNull(oldValue), sqlNull(newValue), changeSetID); err != nil {
		// Logging a change must never break the operation itself.
		fmt.Fprintf(os.Stderr, "library: record change for track %s: %v\n", trackID, err)
	}
}

func sqlNull(text string) interface{} {
	if text == "" {
		return nil
	}
	return text
}

func orNil(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func orNilInt(n int) interface{} {
	if n == 0 {
		return nil
	}
	return n
}

func joinSets(sets []string) string {
	out := ""
	for i, s := range sets {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// ---------------------------------------------------------------------------
// sync_conflicts table
// ---------------------------------------------------------------------------

// InsertConflict records one open conflict between the DB value and the
// file value of one field.
func (s *Service) InsertConflict(trackID, field string, dbValue, fileValue, source string) (string, error) {
	id := uuid.New().String()
	_, err := s.db.Exec(`
		INSERT INTO sync_conflicts (id, track_id, field, db_value, file_value, source)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, trackID, field, sqlNull(dbValue), sqlNull(fileValue), source)
	if err != nil {
		return "", err
	}
	return id, nil
}

// ConflictTrack is one track with its conflict rows (the grouped response
// of GET /api/sync/conflicts).
type ConflictTrack struct {
	TrackID    string     `json:"track_id"`
	TrackTitle string     `json:"track_title"`
	AlbumID    string     `json:"album_id"`
	Fields     []Conflict `json:"fields"`
}

// ListConflicts returns the conflict rows with the given status ("" for
// all), optionally restricted to one album, grouped by track.
func (s *Service) ListConflicts(status, albumID string) ([]ConflictTrack, error) {
	query := `
		SELECT c.id, c.track_id, c.field, c.db_value, c.file_value, c.source,
			COALESCE(c.detected_at, '') AS detected_at, c.status,
			c.resolved_by, c.resolved_at, c.resolution,
			t.title AS track_title, t.album_id
		FROM sync_conflicts c
		JOIN tracks t ON t.id = c.track_id`
	args := []interface{}{}
	if status != "" {
		query += " WHERE c.status = ?"
		args = append(args, status)
	}
	if albumID != "" {
		if len(args) > 0 {
			query += " AND"
		} else {
			query += " WHERE"
		}
		query += " t.album_id = ?"
		args = append(args, albumID)
	}
	query += " ORDER BY t.title ASC, c.field ASC, c.detected_at ASC"

	var rows []struct {
		Conflict
		TrackTitle string `db:"track_title"`
		AlbumID    string `db:"album_id"`
	}
	if err := s.db.Select(&rows, query, args...); err != nil {
		return nil, err
	}

	grouped := map[string]*ConflictTrack{}
	order := []string{}
	for _, r := range rows {
		g, ok := grouped[r.TrackID]
		if !ok {
			g = &ConflictTrack{TrackID: r.TrackID, TrackTitle: r.TrackTitle, AlbumID: r.AlbumID, Fields: []Conflict{}}
			grouped[r.TrackID] = g
			order = append(order, r.TrackID)
		}
		r.ConflictID = r.ID
		g.Fields = append(g.Fields, r.Conflict)
	}
	out := make([]ConflictTrack, 0, len(order))
	for _, id := range order {
		out = append(out, *grouped[id])
	}
	return out, nil
}

// GetConflict returns one conflict row (sql.ErrNoRows when missing).
func (s *Service) GetConflict(id string) (*Conflict, error) {
	var c Conflict
	err := s.db.Get(&c, "SELECT * FROM sync_conflicts WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// MarkConflictStatus moves a conflict to resolved or dismissed.
func (s *Service) MarkConflictStatus(id, status, resolution, resolvedBy string) error {
	_, err := s.db.Exec(
		"UPDATE sync_conflicts SET status = ?, resolution = ?, resolved_by = ?, resolved_at = ? WHERE id = ? AND status = ?",
		status, orNil(resolution), orNil(resolvedBy), nowSQL(), id, ConflictStatusOpen)
	return err
}

// AutoResolveConflicts marks every open conflict of a track resolved
// (used when the file is found to match the DB again).
func (s *Service) AutoResolveConflicts(trackID, resolution string) error {
	_, err := s.db.Exec(
		"UPDATE sync_conflicts SET status = ?, resolution = ?, resolved_at = ? WHERE track_id = ? AND status = ?",
		ConflictStatusResolved, resolution, nowSQL(), trackID, ConflictStatusOpen)
	return err
}

// CountOpenConflicts counts the open conflict rows (optionally per album).
func (s *Service) CountOpenConflicts(albumID string) (int, error) {
	query := `SELECT COUNT(*) FROM sync_conflicts c JOIN tracks t ON t.id = c.track_id WHERE c.status = ?`
	args := []interface{}{ConflictStatusOpen}
	if albumID != "" {
		query += " AND t.album_id = ?"
		args = append(args, albumID)
	}
	var n int
	err := s.db.Get(&n, query, args...)
	return n, err
}

// DeleteTrackHistory removes the metadata_changes rows of a track. Needed
// on this base because metadata_changes.track_id has no ON DELETE CASCADE
// yet (T12 adds it); after T12 the extra delete is harmless.
func (s *Service) DeleteTrackHistory(trackID string) error {
	_, err := s.db.Exec("DELETE FROM metadata_changes WHERE track_id = ?", trackID)
	return err
}

// RecordChange records one metadata_changes row in the text form the
// history and revert flows expect (the same store path the edit path
// uses, so resolutions are revertible).
func (s *Service) RecordChange(trackID, field, oldValue, newValue, changeSetID string) {
	s.insertChange(trackID, field, oldValue, newValue, changeSetID)
}

// RepointTrack moves a track row to a new file path, adopts the stored
// fingerprint of the file it is pointed at and marks it in sync. The id
// and the history rows are preserved (the T16 relink). No version bump:
// the field values are unchanged, the row now refers to a file with the
// same content.
func (s *Service) RepointTrack(trackID, newPath string, size, mtime int64, tagsHash string) error {
	_, err := s.db.Exec(
		"UPDATE tracks SET path = ?, file_size = ?, file_mtime = ?, tags_hash = ?, state = ?, synced_at = ? WHERE id = ?",
		newPath, size, mtime, tagsHash, TrackStateOK, nowSQL(), trackID)
	return err
}

// ApplyFieldText sets one track field from its stored text form (the way
// conflict rows carry values) and bumps the version: "" clears text
// fields and int fields to NULL, "album" repoints the track to the target
// album row (the albums table is keyed by artist + title), and int fields
// are parsed. Field names come from the fixed canonical set.
func (s *Service) ApplyFieldText(track *Track, field, value string) error {
	textSQL := func(v string) interface{} {
		if v == "" {
			return nil
		}
		return v
	}
	intSQL := func(v string) (interface{}, error) {
		if v == "" {
			return nil, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("value %q is not a valid integer for %s", v, field)
		}
		return n, nil
	}
	switch field {
	case "title":
		if value == "" {
			return errors.New("title must not be empty")
		}
		_, err := s.db.Exec("UPDATE tracks SET title = ?, version = version + 1 WHERE id = ?", value, track.ID)
		return err
	case "artist", "album_artist", "genre", "composer", "comment":
		_, err := s.db.Exec("UPDATE tracks SET "+field+" = ?, version = version + 1 WHERE id = ?", textSQL(value), track.ID)
		return err
	case "track_number", "disc_number", "year":
		v, err := intSQL(value)
		if err != nil {
			return err
		}
		_, err = s.db.Exec("UPDATE tracks SET "+field+" = ?, version = version + 1 WHERE id = ?", v, track.ID)
		return err
	case "album":
		artistName := ""
		if track.Artist != nil {
			artistName = *track.Artist
		}
		if artistName == "" {
			album, err := s.GetAlbum(track.AlbumID)
			if err != nil {
				return err
			}
			artistName = album.Artist
		}
		artistID, err := s.EnsureArtist(artistName)
		if err != nil {
			return err
		}
		albumID, err := s.EnsureAlbum(artistID, value)
		if err != nil {
			return err
		}
		_, err = s.db.Exec("UPDATE tracks SET album_id = ?, version = version + 1 WHERE id = ?", albumID, track.ID)
		return err
	default:
		return fmt.Errorf("unknown field %q", field)
	}
}

// UpdateJobDetails replaces a job's details JSON (the T16/T17 pattern for
// merging per-track results without restructuring the jobs package).
func (s *Service) UpdateJobDetails(jobID, details string) error {
	_, err := s.db.Exec("UPDATE jobs SET details = ? WHERE id = ?", details, jobID)
	return err
}

// ---------------------------------------------------------------------------
// sync_trash table
// ---------------------------------------------------------------------------

// TrashDir is where "replace" collisions move displaced files:
// <download dir>/.jellyide/trash.
func (s *Service) TrashDir() string {
	return filepath.Join(s.downloadDir, ".jellyide", "trash")
}

// ErrTrashRestoreConflict is returned when the original path is taken
// again, so a trash entry cannot be restored.
var ErrTrashRestoreConflict = errors.New("the original file path is taken again")

// TrashFile moves path into the trash dir (<trash dir>/<timestamp>/name)
// and records it in sync_trash. It never deletes the file.
func (s *Service) TrashFile(path, trackID string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("trash %s: %w", path, err)
	}
	if st.IsDir() {
		return "", fmt.Errorf("refusing to trash a directory: %s", path)
	}
	stamp := time.Now().UTC().Format("20060102T150405") + "-" + uuid.New().String()[:8]
	// Keep the entry inside the music dir: the trash root is
	// <download dir>/.jellyide/trash and the file name is the basename.
	base := filepath.Base(path)
	trashSub := filepath.Join(s.TrashDir(), stamp)
	if err := os.MkdirAll(trashSub, 0o750); err != nil {
		return "", fmt.Errorf("create trash dir: %w", err)
	}
	trashPath := filepath.Join(trashSub, base)
	if err := fsutil.SafeRename(path, trashPath); err != nil {
		return "", fmt.Errorf("move to trash: %w", err)
	}
	id := uuid.New().String()
	_, err = s.db.Exec(`INSERT INTO sync_trash (id, original_path, trash_path, track_id) VALUES (?, ?, ?, ?)`,
		id, path, trashPath, orNil(trackID))
	if err != nil {
		return "", err
	}
	return id, nil
}

// GetTrashEntry returns one trash row (sql.ErrNoRows when missing).
func (s *Service) GetTrashEntry(id string) (*TrashEntry, error) {
	var e TrashEntry
	err := s.db.Get(&e, "SELECT * FROM sync_trash WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// ListTrash returns all trash entries, oldest first.
func (s *Service) ListTrash() ([]TrashEntry, error) {
	entries := []TrashEntry{}
	err := s.db.Select(&entries, "SELECT * FROM sync_trash ORDER BY created_at ASC, id ASC")
	return entries, err
}

// RestoreTrash moves a trashed file back to its original path (refusing
// when the path is taken again) and drops the entry.
func (s *Service) RestoreTrash(id string) error {
	entry, err := s.GetTrashEntry(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(entry.OriginalPath); err == nil {
		return ErrTrashRestoreConflict
	}
	if err := os.MkdirAll(filepath.Dir(entry.OriginalPath), 0o750); err != nil {
		return fmt.Errorf("restore dir: %w", err)
	}
	if err := fsutil.SafeRename(entry.TrashPath, entry.OriginalPath); err != nil {
		return fmt.Errorf("restore file: %w", err)
	}
	if _, err := s.db.Exec("DELETE FROM sync_trash WHERE id = ?", id); err != nil {
		return err
	}
	return nil
}

// PurgeTrash removes trash entries older than cutoff (file and row).
// Called once at startup with a 30 day cutoff.
func (s *Service) PurgeTrash(cutoff time.Duration) error {
	entries, err := s.ListTrash()
	if err != nil {
		return err
	}
	cutoffTime := time.Now().Add(-cutoff)
	for _, e := range entries {
		created, err := parseSyncTime(e.CreatedAt)
		if err != nil || created.After(cutoffTime) {
			continue
		}
		// Remove the displaced file (already in the trash dir), then the row.
		_ = os.RemoveAll(filepath.Dir(e.TrashPath))
		if _, err := s.db.Exec("DELETE FROM sync_trash WHERE id = ?", e.ID); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// sync conflict policy (settings table)
// ---------------------------------------------------------------------------

// SyncPolicy returns the active sync conflict policy: the settings row
// wins, then the SYNC_CONFLICT_POLICY environment variable, then "ask".
func (s *Service) SyncPolicy() string {
	var v string
	if err := s.db.Get(&v, "SELECT value FROM settings WHERE key = ?", SettingKeySyncConflictPolicy); err == nil && IsValidSyncPolicy(v) {
		return v
	}
	if env := os.Getenv("SYNC_CONFLICT_POLICY"); IsValidSyncPolicy(env) {
		return env
	}
	return SyncPolicyAsk
}

// SeedSyncPolicy writes the env-provided initial policy without ever
// overwriting an existing row, so a later admin change in the UI keeps
// winning over the environment.
func (s *Service) SeedSyncPolicy() error {
	mode := os.Getenv("SYNC_CONFLICT_POLICY")
	if !IsValidSyncPolicy(mode) {
		mode = SyncPolicyAsk
	}
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`,
		SettingKeySyncConflictPolicy, mode)
	return err
}

// SetSyncPolicy validates and stores the policy.
func (s *Service) SetSyncPolicy(policy string) error {
	if !IsValidSyncPolicy(policy) {
		return fmt.Errorf("invalid policy %q (want ask, file_wins or db_wins)", policy)
	}
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		SettingKeySyncConflictPolicy, policy)
	return err
}
