package db

import (
	"log"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

type DB struct {
	*sqlx.DB
}

func NewDB(dbPath string) (*DB, error) {
	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// modernc.org/sqlite requires explicit pragmas
	_, err = db.Exec("PRAGMA busy_timeout = 5000;")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec("PRAGMA journal_mode = WAL;")
	if err != nil {
		return nil, err
	}

	// Increase connection pool, WAL mode and busy_timeout will handle the rest
	db.SetMaxOpenConns(10)

	if err := migrate(db); err != nil {
		return nil, err
	}

	return &DB{db}, nil
}

func migrate(db *sqlx.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS artists (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL
	);

	CREATE TABLE IF NOT EXISTS albums (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		artist_id INTEGER NOT NULL,
		title TEXT NOT NULL,
		cover_art TEXT,
		UNIQUE(artist_id, title),
		FOREIGN KEY(artist_id) REFERENCES artists(id)
	);

	CREATE TABLE IF NOT EXISTS tracks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		album_id INTEGER NOT NULL,
		title TEXT NOT NULL,
		path TEXT UNIQUE NOT NULL,
		duration INTEGER DEFAULT 0,
		bitrate INTEGER DEFAULT 0,
		FOREIGN KEY(album_id) REFERENCES albums(id)
	);

	CREATE TABLE IF NOT EXISTS jobs (
		id TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		status TEXT NOT NULL,
		details TEXT,
		progress TEXT,
		error TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS metadata_changes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		track_id INTEGER NOT NULL,
		field TEXT NOT NULL,
		old_value TEXT,
		new_value TEXT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(track_id) REFERENCES tracks(id)
	);
	`
	_, err := db.Exec(schema)
	if err != nil {
		log.Printf("Migration error: %v", err)
	}
	return err
}
