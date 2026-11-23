package scanner

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"yt-scraper/internal/db"
	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
)

// fakeTags records WriteFields calls (the db_wins / resolve path).
type fakeTags struct {
	mu    sync.Mutex
	calls []fakeWriteCall
	err   error
}

type fakeWriteCall struct {
	Path   string
	Fields map[string]interface{}
}

func (f *fakeTags) WriteFields(path string, fields map[string]interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		copied[k] = v
	}
	f.calls = append(f.calls, fakeWriteCall{Path: path, Fields: copied})
	return f.err
}

// newSyncScanner builds a scanner with an injectable probe and no real
// ffprobe dependency.
func newSyncScanner(t *testing.T) (*Scanner, *library.Service, *db.DB, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.NewDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	lib := library.NewService(database, dir)
	jm := jobs.NewManager(database)
	s := NewScanner(lib, jm, dir)
	return s, lib, database, dir
}

// probeWith builds a fake probe returning the given values.
func probeWith(info probeInfo) func(string) (probeInfo, error) {
	return func(string) (probeInfo, error) { return info, nil }
}

// writeAudio writes a (fake) audio file and returns its path.
func writeAudio(t *testing.T, dir, rel string, size int) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// touch bumps the mtime of path so the fingerprint changes even when the
// size stays the same.
func touch(t *testing.T, path string) {
	t.Helper()
	when := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

// recordJellyIDEEdit inserts a metadata_changes row stamped after the
// track's synced_at, simulating an edit through the normal edit path.
func recordJellyIDEEdit(t *testing.T, database *db.DB, trackID, field, newValue string) {
	t.Helper()
	future := time.Now().Add(5 * time.Second).UTC().Format("2006-01-02 15:04:05")
	if _, err := database.Exec(
		"INSERT INTO metadata_changes (id, track_id, field, old_value, new_value, timestamp) VALUES (?, ?, ?, ?, ?, ?)",
		"test-change-1", trackID, field, "old", newValue, future); err != nil {
		t.Fatalf("insert change: %v", err)
	}
}

func countConflicts(t *testing.T, database *db.DB) int {
	t.Helper()
	var n int
	if err := database.Get(&n, "SELECT COUNT(*) FROM sync_conflicts"); err != nil {
		t.Fatalf("count conflicts: %v", err)
	}
	return n
}

func TestSyncNewFileInsertsWithFingerprint(t *testing.T) {
	s, lib, _, dir := newSyncScanner(t)
	s.probeFile = probeWith(probeInfo{Title: "T1", Artist: "A", Album: "B", Duration: 100, Bitrate: 1000, TrackNumber: 1, Genre: "Rock"})

	path := writeAudio(t, dir, filepath.Join("A", "B", "01 T1.flac"), 512)
	s.IndexFile(path)

	track, err := lib.GetTrackByPath(path)
	if err != nil {
		t.Fatalf("GetTrackByPath: %v", err)
	}
	if track.Version != 1 {
		t.Fatalf("version = %d, want 1", track.Version)
	}
	if track.State != "ok" {
		t.Fatalf("state = %q, want ok", track.State)
	}
	if track.TagsHash == nil || *track.TagsHash == "" {
		t.Fatal("tags_hash not set")
	}
	if track.FileSize == nil || track.FileMtime == nil {
		t.Fatal("fingerprint not set")
	}
	if track.SyncedAt == nil {
		t.Fatal("synced_at not set")
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if *track.FileSize != st.Size() {
		t.Fatalf("file_size = %d, want %d", *track.FileSize, st.Size())
	}
}

func TestSyncFileChangedNoEditsFileWins(t *testing.T) {
	s, lib, database, dir := newSyncScanner(t)
	s.probeFile = probeWith(probeInfo{Title: "Old Title", Artist: "A", Album: "B", Duration: 100, Bitrate: 1000, Genre: "Rock"})

	path := writeAudio(t, dir, filepath.Join("A", "B", "01 Song.flac"), 512)
	s.IndexFile(path)

	// An external program rewrites the file: new size, new mtime, new tags.
	if err := os.WriteFile(path, append([]byte("x"), make([]byte, 1000)...), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	touch(t, path)
	s.probeFile = probeWith(probeInfo{Title: "New Title", Artist: "A", Album: "B", Duration: 101, Bitrate: 1000, Genre: "Jazz"})
	s.IndexFile(path)

	track, err := lib.GetTrackByPath(path)
	if err != nil {
		t.Fatalf("GetTrackByPath: %v", err)
	}
	if track.Title != "New Title" {
		t.Fatalf("title = %q, want New Title", track.Title)
	}
	if track.Genre == nil || *track.Genre != "Jazz" {
		t.Fatalf("genre = %v, want Jazz", track.Genre)
	}
	if track.Version != 2 {
		t.Fatalf("version = %d, want 2 (file wins bumps it)", track.Version)
	}
	if track.State != "ok" {
		t.Fatalf("state = %q, want ok", track.State)
	}
	// The overwrite is recorded in the history.
	var n int
	if err := database.Get(&n, "SELECT COUNT(*) FROM metadata_changes WHERE track_id = ? AND field = 'title'", track.ID); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if n != 1 {
		t.Fatalf("title history rows = %d, want 1", n)
	}
	if countConflicts(t, database) != 0 {
		t.Fatalf("conflicts = %d, want 0 (not a conflict)", countConflicts(t, database))
	}
}

func TestSyncFileChangedWithEditsAskRecordsConflicts(t *testing.T) {
	s, lib, database, dir := newSyncScanner(t)
	if err := lib.SetSyncPolicy(library.SyncPolicyAsk); err != nil {
		t.Fatalf("SetSyncPolicy: %v", err)
	}
	s.probeFile = probeWith(probeInfo{Title: "Song", Artist: "A", Album: "B", Duration: 100, Bitrate: 1000, Genre: "Rock"})

	path := writeAudio(t, dir, filepath.Join("A", "B", "01 Song.flac"), 512)
	s.IndexFile(path)
	track, _ := lib.GetTrackByPath(path)

	// A JellyIDE edit lands in the DB after the last sync: genre -> Metal.
	if _, err := database.Exec("UPDATE tracks SET genre = ?, version = version + 1 WHERE id = ?", "Metal", track.ID); err != nil {
		t.Fatalf("edit: %v", err)
	}
	recordJellyIDEEdit(t, database, track.ID, "genre", "Metal")

	// The file was changed out of band in the same field (and duration).
	touch(t, path)
	s.probeFile = probeWith(probeInfo{Title: "Song", Artist: "A", Album: "B", Duration: 99, Bitrate: 1000, Genre: "Blues"})
	s.IndexFile(path)

	// Both DB and file are untouched; the conflict is recorded.
	track, _ = lib.GetTrackByPath(path)
	if track.Genre == nil || *track.Genre != "Metal" {
		t.Fatalf("genre = %v, want Metal (DB untouched)", track.Genre)
	}
	if track.State != "conflict" {
		t.Fatalf("state = %q, want conflict", track.State)
	}
	var row struct {
		Field     string `db:"field"`
		DBValue   string `db:"db_value"`
		FileValue string `db:"file_value"`
		Source    string `db:"source"`
		Status    string `db:"status"`
	}
	if err := database.Get(&row, "SELECT field, db_value, file_value, source, status FROM sync_conflicts WHERE track_id = ? AND field = 'genre'", track.ID); err != nil {
		t.Fatalf("genre conflict row: %v", err)
	}
	if row.DBValue != "Metal" || row.FileValue != "Blues" {
		t.Fatalf("conflict values = %q/%q, want Metal/Blues", row.DBValue, row.FileValue)
	}
	if row.Source != "scan" || row.Status != "open" {
		t.Fatalf("source/status = %q/%q, want scan/open", row.Source, row.Status)
	}
	// Only the tracked tag field (genre) differs; duration is a container
	// property, not a tag, and does not produce a conflict row.
	if n := countConflicts(t, database); n != 1 {
		t.Fatalf("conflicts = %d, want 1 (genre)", n)
	}

	// A second scan must not duplicate the conflicts.
	s.IndexFile(path)
	if n := countConflicts(t, database); n != 1 {
		t.Fatalf("conflicts after rescan = %d, want 1 (no duplicates)", n)
	}
}

func TestSyncFileChangedWithEditsFileWinsPolicy(t *testing.T) {
	s, lib, database, dir := newSyncScanner(t)
	if err := lib.SetSyncPolicy(library.SyncPolicyFileWins); err != nil {
		t.Fatalf("SetSyncPolicy: %v", err)
	}
	s.probeFile = probeWith(probeInfo{Title: "Song", Artist: "A", Album: "B", Duration: 100, Bitrate: 1000, Genre: "Rock"})

	path := writeAudio(t, dir, filepath.Join("A", "B", "01 Song.flac"), 512)
	s.IndexFile(path)
	track, _ := lib.GetTrackByPath(path)

	if _, err := database.Exec("UPDATE tracks SET genre = ?, version = version + 1 WHERE id = ?", "Metal", track.ID); err != nil {
		t.Fatalf("edit: %v", err)
	}
	recordJellyIDEEdit(t, database, track.ID, "genre", "Metal")

	touch(t, path)
	s.probeFile = probeWith(probeInfo{Title: "Song", Artist: "A", Album: "B", Duration: 100, Bitrate: 1000, Genre: "Blues"})
	s.IndexFile(path)

	track, _ = lib.GetTrackByPath(path)
	if track.Genre == nil || *track.Genre != "Blues" {
		t.Fatalf("genre = %v, want Blues (file_wins)", track.Genre)
	}
	if track.State != "ok" {
		t.Fatalf("state = %q, want ok", track.State)
	}
	if countConflicts(t, database) != 0 {
		t.Fatalf("conflicts = %d, want 0", countConflicts(t, database))
	}
	// The overwrite is still recorded in the history.
	var n int
	if err := database.Get(&n, "SELECT COUNT(*) FROM metadata_changes WHERE track_id = ? AND field = 'genre' AND new_value = 'Blues'", track.ID); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if n != 1 {
		t.Fatalf("genre history rows = %d, want 1", n)
	}
}

func TestSyncFileChangedWithEditsDBWinsPolicy(t *testing.T) {
	s, lib, database, dir := newSyncScanner(t)
	if err := lib.SetSyncPolicy(library.SyncPolicyDBWins); err != nil {
		t.Fatalf("SetSyncPolicy: %v", err)
	}
	tags := &fakeTags{}
	s.Tags = tags
	s.probeFile = probeWith(probeInfo{Title: "Song", Artist: "A", Album: "B", Duration: 100, Bitrate: 1000, Genre: "Rock"})

	path := writeAudio(t, dir, filepath.Join("A", "B", "01 Song.flac"), 512)
	s.IndexFile(path)
	track, _ := lib.GetTrackByPath(path)

	if _, err := database.Exec("UPDATE tracks SET genre = ?, version = version + 1 WHERE id = ?", "Metal", track.ID); err != nil {
		t.Fatalf("edit: %v", err)
	}
	recordJellyIDEEdit(t, database, track.ID, "genre", "Metal")

	touch(t, path)
	s.probeFile = probeWith(probeInfo{Title: "Song", Artist: "A", Album: "B", Duration: 100, Bitrate: 1000, Genre: "Blues"})
	s.IndexFile(path)

	// The file was rewritten from the DB.
	tags.mu.Lock()
	defer tags.mu.Unlock()
	if len(tags.calls) != 1 {
		t.Fatalf("tag writes = %d, want 1", len(tags.calls))
	}
	if tags.calls[0].Path != path {
		t.Fatalf("tag write path = %q, want %q", tags.calls[0].Path, path)
	}
	if tags.calls[0].Fields["genre"] != "Metal" {
		t.Fatalf("rewritten genre = %v, want Metal (from DB)", tags.calls[0].Fields["genre"])
	}

	// The DB row keeps its value; the fingerprint now matches the DB map.
	track, _ = lib.GetTrackByPath(path)
	if track.Genre == nil || *track.Genre != "Metal" {
		t.Fatalf("genre = %v, want Metal", track.Genre)
	}
	if track.State != "ok" {
		t.Fatalf("state = %q, want ok", track.State)
	}
	if countConflicts(t, database) != 0 {
		t.Fatalf("conflicts = %d, want 0", countConflicts(t, database))
	}
}

func TestSyncUnchangedFileKeepsInSync(t *testing.T) {
	s, lib, database, dir := newSyncScanner(t)
	s.probeFile = probeWith(probeInfo{Title: "Song", Artist: "A", Album: "B", Duration: 100, Bitrate: 1000})

	path := writeAudio(t, dir, filepath.Join("A", "B", "01 Song.flac"), 512)
	s.IndexFile(path)
	first, _ := lib.GetTrackByPath(path)

	// Rescan the same file: no version bump, no conflicts, no history.
	s.IndexFile(path)
	second, _ := lib.GetTrackByPath(path)
	if second.Version != first.Version {
		t.Fatalf("version = %d, want %d (unchanged file must not bump)", second.Version, first.Version)
	}
	if n := countConflicts(t, database); n != 0 {
		t.Fatalf("conflicts = %d, want 0", n)
	}
	var changes int
	if err := database.Get(&changes, "SELECT COUNT(*) FROM metadata_changes"); err != nil {
		t.Fatalf("count: %v", err)
	}
	if changes != 0 {
		t.Fatalf("history rows = %d, want 0", changes)
	}
}

func TestSyncConflictClearedWhenFileMatchesDB(t *testing.T) {
	s, lib, database, dir := newSyncScanner(t)
	if err := lib.SetSyncPolicy(library.SyncPolicyAsk); err != nil {
		t.Fatalf("SetSyncPolicy: %v", err)
	}
	s.probeFile = probeWith(probeInfo{Title: "Song", Artist: "A", Album: "B", Duration: 100, Bitrate: 1000, Genre: "Rock"})

	path := writeAudio(t, dir, filepath.Join("A", "B", "01 Song.flac"), 512)
	s.IndexFile(path)
	track, _ := lib.GetTrackByPath(path)

	if _, err := database.Exec("UPDATE tracks SET genre = ?, version = version + 1 WHERE id = ?", "Metal", track.ID); err != nil {
		t.Fatalf("edit: %v", err)
	}
	recordJellyIDEEdit(t, database, track.ID, "genre", "Metal")
	touch(t, path)
	s.probeFile = probeWith(probeInfo{Title: "Song", Artist: "A", Album: "B", Duration: 100, Bitrate: 1000, Genre: "Blues"})
	s.IndexFile(path)
	if n := countConflicts(t, database); n != 1 {
		t.Fatalf("conflicts = %d, want 1", n)
	}

	// The file is fixed out of band to match the DB (same tags).
	touch(t, path)
	s.probeFile = probeWith(probeInfo{Title: "Song", Artist: "A", Album: "B", Duration: 100, Bitrate: 1000, Genre: "Metal"})
	s.IndexFile(path)

	track, _ = lib.GetTrackByPath(path)
	if track.State != "ok" {
		t.Fatalf("state = %q, want ok after file matches DB", track.State)
	}
	var status, resolution string
	if err := database.Get(&status, "SELECT status FROM sync_conflicts WHERE track_id = ?", track.ID); err != nil {
		t.Fatalf("status: %v", err)
	}
	if err := database.Get(&resolution, "SELECT resolution FROM sync_conflicts WHERE track_id = ?", track.ID); err != nil {
		t.Fatalf("resolution: %v", err)
	}
	if status != "resolved" || resolution != "file_matches_db" {
		t.Fatalf("status/resolution = %q/%q, want resolved/file_matches_db", status, resolution)
	}
}

func TestSyncGhostRowsMarkedMissing(t *testing.T) {
	s, lib, database, dir := newSyncScanner(t)
	s.probeFile = probeWith(probeInfo{Title: "Song", Artist: "A", Album: "B", Duration: 100, Bitrate: 1000})

	path := writeAudio(t, dir, filepath.Join("A", "B", "01 Song.flac"), 512)
	s.IndexFile(path)
	track, _ := lib.GetTrackByPath(path)

	// A JellyIDE edit happens, then the file disappears out of band.
	recordJellyIDEEdit(t, database, track.ID, "comment", "a note")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	s.markGhosts()

	// The row (and its history) is kept, only marked missing.
	kept, err := lib.GetTrackByPath(path)
	if err != nil {
		t.Fatalf("row must be kept: %v", err)
	}
	if kept.State != "missing" {
		t.Fatalf("state = %q, want missing", kept.State)
	}
	var changes int
	if err := database.Get(&changes, "SELECT COUNT(*) FROM metadata_changes WHERE track_id = ?", track.ID); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if changes != 1 {
		t.Fatalf("history rows = %d, want 1 (history survives)", changes)
	}
}

func TestIndexFileProbeFailureMarksMissing(t *testing.T) {
	s, lib, _, dir := newSyncScanner(t)
	s.probeFile = probeWith(probeInfo{Title: "Song", Artist: "A", Album: "B", Duration: 100, Bitrate: 1000})

	path := writeAudio(t, dir, filepath.Join("A", "B", "01 Song.flac"), 512)
	s.IndexFile(path)

	// The file is deleted, then the probe fails (e.g. corrupt file): the
	// row must be marked missing, not dropped.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	s.probeFile = func(string) (probeInfo, error) { return probeInfo{}, os.ErrNotExist }
	s.IndexFile(path)

	track, err := lib.GetTrackByPath(path)
	if err != nil {
		t.Fatalf("row must be kept: %v", err)
	}
	if track.State != "missing" {
		t.Fatalf("state = %q, want missing", track.State)
	}
}

func TestSyncMissingFileReappears(t *testing.T) {
	s, lib, _, dir := newSyncScanner(t)
	s.probeFile = probeWith(probeInfo{Title: "Song", Artist: "A", Album: "B", Duration: 100, Bitrate: 1000})

	path := writeAudio(t, dir, filepath.Join("A", "B", "01 Song.flac"), 512)
	s.IndexFile(path)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	s.markGhosts()

	// The file reappears with the same content: state returns to ok.
	writeAudio(t, dir, filepath.Join("A", "B", "01 Song.flac"), 512)
	s.IndexFile(path)

	track, err := lib.GetTrackByPath(path)
	if err != nil {
		t.Fatalf("GetTrackByPath: %v", err)
	}
	if track.State != "ok" {
		t.Fatalf("state = %q, want ok after file reappears", track.State)
	}
}

func TestSyncPolicyFallback(t *testing.T) {
	_, lib, _, _ := newSyncScanner(t)
	// No settings row, no env: the default is ask.
	t.Setenv("SYNC_CONFLICT_POLICY", "")
	if got := lib.SyncPolicy(); got != library.SyncPolicyAsk {
		t.Fatalf("policy = %q, want ask", got)
	}
	t.Setenv("SYNC_CONFLICT_POLICY", "db_wins")
	if got := lib.SyncPolicy(); got != library.SyncPolicyDBWins {
		t.Fatalf("policy = %q, want db_wins (env)", got)
	}
	if err := lib.SetSyncPolicy(library.SyncPolicyFileWins); err != nil {
		t.Fatalf("SetSyncPolicy: %v", err)
	}
	if got := lib.SyncPolicy(); got != library.SyncPolicyFileWins {
		t.Fatalf("policy = %q, want file_wins (settings beat env)", got)
	}
	if err := lib.SetSyncPolicy("bogus"); err == nil {
		t.Fatal("SetSyncPolicy(bogus) must fail")
	}
	if err := lib.SeedSyncPolicy(); err != nil {
		t.Fatalf("SeedSyncPolicy: %v", err)
	}
	// Seeding must not overwrite an existing row.
	if got := lib.SyncPolicy(); got != library.SyncPolicyFileWins {
		t.Fatalf("policy after seed = %q, want file_wins", got)
	}
}
