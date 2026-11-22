// Package db — migration tests.
package db

import (
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
)

// TestMigrateAddsStatusColumnAndSettingsTable simulates a pre-T8 database
// (users table without the status column, no settings table) and checks that
// NewDB upgrades it in place: the column is added, existing rows default to
// 'active', and a second open is a no-op.
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
