// Package db opens the SQLite database with a read pool plus a dedicated
// single-connection write pool and runs the schema migration.
package db

import (
	"database/sql"
	"fmt"
	"log"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite" // register the pure-Go SQLite driver
)

// DB embeds the read pool as *sqlx.DB so all existing call sites
// (s.db.Get, s.db.Select, s.db.QueryRowx, etc.) keep working unchanged.
//
// Writes and transactions are intercepted and routed to a dedicated
// single-connection write pool, preventing SQLITE_BUSY under concurrent
// load without requiring any changes to callers.
type DB struct {
	*sqlx.DB          // read pool — embedded for full backward compatibility
	write    *sqlx.DB // single write connection, serializes all mutations
}

// Beginx overrides sqlx.DB.Beginx to always start transactions on the
// write connection. Callers do s.db.Beginx() unchanged.
func (d *DB) Beginx() (*sqlx.Tx, error) {
	return d.write.Beginx()
}

// Exec overrides sqlx.DB.Exec to route all writes through the write connection.
func (d *DB) Exec(query string, args ...interface{}) (sql.Result, error) {
	return d.write.Exec(query, args...)
}

// NamedExec overrides sqlx.DB.NamedExec for named-param writes.
func (d *DB) NamedExec(query string, arg interface{}) (sql.Result, error) {
	return d.write.NamedExec(query, arg)
}

// Close closes both pools cleanly.
func (d *DB) Close() error {
	rerr := d.DB.Close()
	werr := d.write.Close()
	if rerr != nil {
		return rerr
	}
	return werr
}

// NewDB opens (creating if needed) the SQLite database at dbPath and runs
// the migration on the write connection.
func NewDB(dbPath string) (*DB, error) {
	write, err := openConn(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open write conn: %w", err)
	}
	// Single write connection — SQLite only allows one writer at a time anyway.
	// Extra write connections just queue up burning busy_timeout.
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)

	read, err := openConn(dbPath)
	if err != nil {
		if cerr := write.Close(); cerr != nil {
			log.Printf("db: closing write pool after open error: %v", cerr)
		}
		return nil, fmt.Errorf("open read conn: %w", err)
	}
	// WAL mode allows many concurrent readers, each with their own snapshot.
	read.SetMaxOpenConns(4)
	read.SetMaxIdleConns(4)

	database := &DB{DB: read, write: write}

	if err := migrate(write); err != nil {
		if cerr := database.Close(); cerr != nil {
			log.Printf("db: closing pools after migration error: %v", cerr)
		}
		return nil, err
	}

	return database, nil
}

// openConn opens a SQLite connection and applies all required pragmas.
// Pragmas are per-connection in SQLite, so every connection needs this.
func openConn(dbPath string) (*sqlx.DB, error) {
	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	pragmas := []string{
		// Wait up to 5s instead of immediately returning SQLITE_BUSY.
		"PRAGMA busy_timeout = 5000;",
		// WAL: readers don't block writers, writers don't block readers.
		"PRAGMA journal_mode = WAL;",
		// NORMAL is safe with WAL (no data loss on crash) and much faster than FULL.
		"PRAGMA synchronous = NORMAL;",
		// Enforce FK constraints — SQLite ignores them by default.
		"PRAGMA foreign_keys = ON;",
		// Prevent WAL file growing unboundedly under write load.
		"PRAGMA wal_autocheckpoint = 1000;",
	}

	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			if cerr := db.Close(); cerr != nil {
				log.Printf("db: closing conn after pragma error: %v", cerr)
			}
			return nil, fmt.Errorf("pragma %q: %w", p, err)
		}
	}

	return db, nil
}

func migrate(db *sqlx.DB) error {
	// Run each statement individually — multi-statement Exec is not guaranteed
	// behavior across SQLite driver versions.
	statements := []string{
		`CREATE TABLE IF NOT EXISTS artists (
			id   TEXT PRIMARY KEY,
			name TEXT UNIQUE NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS albums (
			id        TEXT PRIMARY KEY,
			artist_id TEXT NOT NULL,
			title     TEXT NOT NULL,
			cover_art TEXT,
			UNIQUE(artist_id, title),
			FOREIGN KEY(artist_id) REFERENCES artists(id)
		)`,
		`CREATE TABLE IF NOT EXISTS tracks (
			id           TEXT PRIMARY KEY,
			album_id     TEXT NOT NULL,
			title        TEXT NOT NULL,
			path         TEXT UNIQUE NOT NULL,
			duration     INTEGER DEFAULT 0,
			bitrate      INTEGER DEFAULT 0,
			artist       TEXT,
			album_artist TEXT,
			track_number INTEGER,
			disc_number  INTEGER,
			year         INTEGER,
			genre        TEXT,
			composer     TEXT,
			comment      TEXT,
			FOREIGN KEY(album_id) REFERENCES albums(id)
		)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id         TEXT     PRIMARY KEY,
			type       TEXT     NOT NULL,
			status     TEXT     NOT NULL,
			details    TEXT,
			progress   TEXT,
			error      TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS metadata_changes (
			id            TEXT     PRIMARY KEY,
			track_id      TEXT     NOT NULL,
			field         TEXT     NOT NULL,
			old_value     TEXT,
			new_value     TEXT,
			change_set_id TEXT,
			timestamp     DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY(track_id) REFERENCES tracks(id)
		)`,
		`CREATE TABLE IF NOT EXISTS users (
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
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			expires_at DATETIME NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS audit_log (
			id     INTEGER PRIMARY KEY AUTOINCREMENT,
			at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			actor  TEXT NOT NULL,
			action TEXT NOT NULL,
			target TEXT,
			detail TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id)`,
	}

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			log.Printf("Migration error: %v", err)
			return err
		}
	}

	// The users table predates the status column (T8): databases created
	// before the column existed need the ALTER, fresh ones already have it
	// via the CREATE TABLE. Guard on PRAGMA table_info so the ALTER runs
	// at most once.
	var hasStatus int
	if err := db.Get(&hasStatus, `SELECT COUNT(*) FROM pragma_table_info('users') WHERE name = 'status'`); err != nil {
		log.Printf("Migration error: %v", err)
		return err
	}
	if hasStatus == 0 {
		if _, err := db.Exec(`ALTER TABLE users ADD COLUMN status TEXT NOT NULL DEFAULT 'active'`); err != nil {
			log.Printf("Migration error: %v", err)
			return err
		}
	}

	// The CREATE TABLE IF NOT EXISTS statements above do not add columns to a
	// table that already exists, so pre-existing databases get the flexible
	// metadata columns via guarded ALTER TABLE calls.
	trackColumns := []struct {
		name string
		def  string
	}{
		{"artist", "TEXT"},
		{"album_artist", "TEXT"},
		{"track_number", "INTEGER"},
		{"disc_number", "INTEGER"},
		{"year", "INTEGER"},
		{"genre", "TEXT"},
		{"composer", "TEXT"},
		{"comment", "TEXT"},
	}
	for _, col := range trackColumns {
		if err := addColumnIfMissing(db, "tracks", col.name, col.def); err != nil {
			log.Printf("Migration error: %v", err)
			return err
		}
	}
	if err := addColumnIfMissing(db, "metadata_changes", "change_set_id", "TEXT"); err != nil {
		log.Printf("Migration error: %v", err)
		return err
	}
	// The index needs the column to exist first, so it is created after the
	// guarded ALTER above (a fresh database already has both).
	if _, err := db.Exec("CREATE INDEX IF NOT EXISTS idx_metadata_changes_change_set ON metadata_changes(change_set_id)"); err != nil {
		log.Printf("Migration error: %v", err)
		return err
	}

	// The jobs table predates the automatic-retry columns (T14); like the
	// users.status column above, pre-existing databases get them via a
	// guarded ALTER (a fresh database gets them the same way, the CREATE
	// TABLE intentionally does not carry them).
	if err := addColumnIfMissing(db, "jobs", "attempts", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		log.Printf("Migration error: %v", err)
		return err
	}
	if err := addColumnIfMissing(db, "jobs", "next_retry_at", "DATETIME"); err != nil {
		log.Printf("Migration error: %v", err)
		return err
	}

	return nil
}

// addColumnIfMissing runs ALTER TABLE table ADD COLUMN column def only when
// the column is not already present (the table names are internal constants,
// never user input).
func addColumnIfMissing(db *sqlx.DB, table, column, def string) error {
	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, column); err != nil {
		return fmt.Errorf("check column %s.%s: %w", table, column, err)
	}
	if count > 0 {
		return nil
	}
	stmt := "ALTER TABLE " + table + " ADD COLUMN " + column + " " + def
	if _, err := db.Exec(stmt); err != nil {
		return fmt.Errorf("add column %s.%s: %w", table, column, err)
	}
	return nil
}
