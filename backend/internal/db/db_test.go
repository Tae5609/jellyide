// Package db — migration tests.
package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
)

func TestMigrateAddsStatusColumnAndSettingsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// Build the legacy schema by hand, as the old migration used to.
	legacy, err := sqlx.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := legacy.Exec(`CREATE TABLE users (
		id            TEXT PRIMARY KEY,
		username      TEXT UNIQUE NOT NULL,
		password_hash TEXT,
		provider      TEXT NOT NULL DEFAULT 'local',
		subject       TEXT,
		email         TEXT,
		display_name  TEXT,
		is_admin      INTEGER NOT NULL DEFAULT 0,
		created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_login    DATETIME,
		UNIQUE(provider, subject)
	)`); err != nil {
		t.Fatalf("create legacy users table: %v", err)
	}
	if _, err := legacy.Exec(`INSERT INTO users (id, username, provider) VALUES ('u1', 'old-admin', 'local')`); err != nil {
		t.Fatalf("insert legacy user: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	database, err := NewDB(path)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}

	// The status column exists and the pre-existing row was defaulted.
	var n int
	if err := database.Get(&n, `SELECT COUNT(*) FROM pragma_table_info('users') WHERE name = 'status'`); err != nil {
		t.Fatalf("pragma table_info: %v", err)
	}
	if n != 1 {
		t.Fatalf("status column count = %d, want 1", n)
	}
	var status string
	if err := database.Get(&status, `SELECT status FROM users WHERE id = 'u1'`); err != nil {
		t.Fatalf("select status: %v", err)
	}
	if status != "active" {
		t.Fatalf("legacy row status = %q, want %q", status, "active")
	}

	// The settings table exists.
	var tables int
	if err := database.Get(&tables, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'settings'`); err != nil {
		t.Fatalf("count settings table: %v", err)
	}
	if tables != 1 {
		t.Fatalf("settings table count = %d, want 1", tables)
	}

	// Reopening the migrated database must not fail (idempotent migration).
	if err := database.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	again, err := NewDB(path)
	if err != nil {
		t.Fatalf("second NewDB: %v", err)
	}
	defer func() { _ = again.Close() }()
}

func tableColumns(t *testing.T, file, table string) map[string]bool {
	t.Helper()
	raw, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer func() { _ = raw.Close() }()

	rows, err := raw.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("pragma table_info: %v", err)
	}
	defer func() { _ = rows.Close() }()

	cols := map[string]bool{}
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, _type      string
			dflt             any
		)
		if err := rows.Scan(&cid, &name, &_type, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return cols
}

// TestMigrateFreshDB verifies a brand-new database gets the full schema,
// including the flexible metadata columns and the change-set index.
func TestMigrateFreshDB(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.db")

	database, err := NewDB(file)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer func() { _ = database.Close() }()

	wantTrack := []string{
		"id", "album_id", "title", "path", "duration", "bitrate",
		"artist", "album_artist", "track_number", "disc_number",
		"year", "genre", "composer", "comment",
	}
	cols := tableColumns(t, file, "tracks")
	for _, c := range wantTrack {
		if !cols[c] {
			t.Fatalf("tracks table missing column %q (have %v)", c, cols)
		}
	}

	changeCols := tableColumns(t, file, "metadata_changes")
	if !changeCols["change_set_id"] {
		t.Fatalf("metadata_changes missing change_set_id (have %v)", changeCols)
	}

	var indexCount int
	if err := database.Get(&indexCount,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_metadata_changes_change_set'"); err != nil {
		t.Fatalf("query index: %v", err)
	}
	if indexCount != 1 {
		t.Fatalf("change-set index count = %d, want 1", indexCount)
	}
}

// createOldSchema writes a database with the pre-T10 schema (tracks without
// the tag columns, metadata_changes without change_set_id).
func createOldSchema(t *testing.T, file string) {
	t.Helper()
	raw, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer func() { _ = raw.Close() }()

	old := []string{
		`CREATE TABLE artists (id TEXT PRIMARY KEY, name TEXT UNIQUE NOT NULL)`,
		`CREATE TABLE albums (
			id TEXT PRIMARY KEY, artist_id TEXT NOT NULL, title TEXT NOT NULL,
			cover_art TEXT, UNIQUE(artist_id, title),
			FOREIGN KEY(artist_id) REFERENCES artists(id)
		)`,
		`CREATE TABLE tracks (
			id TEXT PRIMARY KEY, album_id TEXT NOT NULL, title TEXT NOT NULL,
			path TEXT UNIQUE NOT NULL, duration INTEGER DEFAULT 0, bitrate INTEGER DEFAULT 0,
			FOREIGN KEY(album_id) REFERENCES albums(id)
		)`,
		`CREATE TABLE metadata_changes (
			id TEXT PRIMARY KEY, track_id TEXT NOT NULL, field TEXT NOT NULL,
			old_value TEXT, new_value TEXT, timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(track_id) REFERENCES tracks(id)
		)`,
	}
	for _, stmt := range old {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
}

// TestMigrateUpgradesOldSchema verifies a pre-T10 database gains the new
// columns without losing data or failing on the second run.
func TestMigrateUpgradesOldSchema(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "test.db")
	createOldSchema(t, file)

	// Seed a row so the upgrade cannot silently rebuild the table.
	raw, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.Exec("INSERT INTO artists (id, name) VALUES ('a1', 'Artist')"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := raw.Exec("INSERT INTO albums (id, artist_id, title) VALUES ('al1', 'a1', 'Album')"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := raw.Exec("INSERT INTO tracks (id, album_id, title, path) VALUES ('t1', 'al1', 'Song', '/x/Song.flac')"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = raw.Close()

	database, err := NewDB(file)
	if err != nil {
		t.Fatalf("NewDB over old schema: %v", err)
	}
	defer func() { _ = database.Close() }()

	cols := tableColumns(t, file, "tracks")
	for _, c := range []string{"artist", "album_artist", "track_number", "disc_number", "year", "genre", "composer", "comment"} {
		if !cols[c] {
			t.Fatalf("tracks missing %q after migration (have %v)", c, cols)
		}
	}
	if !tableColumns(t, file, "metadata_changes")["change_set_id"] {
		t.Fatal("metadata_changes missing change_set_id after migration")
	}

	// Data survived.
	var artistName string
	if err := database.Get(&artistName, "SELECT name FROM artists WHERE id = ?", "a1"); err != nil || artistName != "Artist" {
		t.Fatalf("seeded artist lost (name=%q err=%v)", artistName, err)
	}

	// Migration is idempotent: a second open must not fail.
	database2, err := NewDB(file)
	if err != nil {
		t.Fatalf("second NewDB: %v", err)
	}
	_ = database2.Close()

	// The old database file is still readable with the new columns NULL.
	var artist sql.NullString
	if err := database.Get(&artist, "SELECT artist FROM tracks WHERE id = ?", "t1"); err != nil {
		t.Fatalf("read new column: %v", err)
	}
	if artist.Valid {
		t.Fatalf("artist for old row = %q, want NULL", artist.String)
	}
}

// TestMigrateAddsJobsRetryColumns verifies a database whose jobs table
// predates the retry columns (T14) gains them via the guarded ALTER, with
// the legacy row defaulted, and that the migration stays idempotent.
func TestMigrateAddsJobsRetryColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	legacy, err := sqlx.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := legacy.Exec(`CREATE TABLE jobs (
		id         TEXT     PRIMARY KEY,
		type       TEXT     NOT NULL,
		status     TEXT     NOT NULL,
		details    TEXT,
		progress   TEXT,
		error      TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create legacy jobs table: %v", err)
	}
	if _, err := legacy.Exec(`INSERT INTO jobs (id, type, status) VALUES ('j1', 'download', 'failed')`); err != nil {
		t.Fatalf("insert legacy job: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	database, err := NewDB(path)
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer func() { _ = database.Close() }()

	cols := tableColumns(t, path, "jobs")
	if !cols["attempts"] || !cols["next_retry_at"] {
		t.Fatalf("jobs missing retry columns after migration (have %v)", cols)
	}

	// The legacy row was defaulted.
	var (
		attempts  int
		nextRetry sql.NullTime
	)
	if err := database.Get(&attempts, `SELECT attempts FROM jobs WHERE id = 'j1'`); err != nil {
		t.Fatalf("select attempts: %v", err)
	}
	if attempts != 0 {
		t.Fatalf("legacy row attempts = %d, want 0", attempts)
	}
	if err := database.Get(&nextRetry, `SELECT next_retry_at FROM jobs WHERE id = 'j1'`); err != nil {
		t.Fatalf("select next_retry_at: %v", err)
	}
	if nextRetry.Valid {
		t.Fatalf("legacy row next_retry_at = %v, want NULL", nextRetry.Time)
	}

	// A fresh database gets the columns as well.
	freshPath := filepath.Join(t.TempDir(), "fresh.db")
	fresh, err := NewDB(freshPath)
	if err != nil {
		t.Fatalf("fresh NewDB: %v", err)
	}
	_ = fresh.Close()
	freshCols := tableColumns(t, freshPath, "jobs")
	if !freshCols["attempts"] || !freshCols["next_retry_at"] {
		t.Fatalf("fresh jobs table missing retry columns (have %v)", freshCols)
	}

	// Migration is idempotent: a second open must not fail.
	if err := database.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	again, err := NewDB(path)
	if err != nil {
		t.Fatalf("second NewDB: %v", err)
	}
	_ = again.Close()
}

// TestAddColumnIfMissingDirect drives the helper on a throwaway table.
func TestAddColumnIfMissingDirect(t *testing.T) {
	dir := t.TempDir()
	database, err := NewDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer func() { _ = database.Close() }()

	if _, err := database.Exec("CREATE TABLE probe (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatalf("create probe: %v", err)
	}
	if err := addColumnIfMissing(database.DB, "probe", "extra", "INTEGER"); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := addColumnIfMissing(database.DB, "probe", "extra", "INTEGER"); err != nil {
		t.Fatalf("second add (must be a no-op): %v", err)
	}
}
