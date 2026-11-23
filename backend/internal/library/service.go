// Package library tracks the artist/album/track hierarchy of the music
// collection in SQLite and keeps the on-disk folder layout in sync.
package library

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"

	"yt-scraper/internal/db"
	"yt-scraper/internal/fsutil"
)

// ErrAlbumExists is returned by RenameAlbum when the target folder
// already exists. Nothing is moved or updated; the API maps it to a 409.
var ErrAlbumExists = errors.New("an album folder with that name already exists")

// Artist is one row in the artists table.
type Artist struct {
	ID   string `json:"id" db:"id"`
	Name string `json:"name" db:"name"`
}

// Album is one row in the albums table, joined with its artist name.
type Album struct {
	ID       string `json:"id" db:"id"`
	ArtistID string `json:"artist_id" db:"artist_id"`
	Title    string `json:"title" db:"title"`
	CoverArt string `json:"cover_art" db:"cover_art"`
	Artist   string `json:"artist" db:"artist_name"`
}

// Track is one row in the tracks table. The tag fields are pointers so a
// missing (NULL) value is distinguishable from an empty one. The sync
// columns (T16) track the file fingerprint at the last synchronization:
//
//   - FileSize/FileMtime/TagsHash: the file as it was when the row was
//     last synchronized (NULL before the first sync).
//   - Version: bumped on every DB write that changes a track field. The
//     editor sends it back as expected_version for optimistic locking.
//     Pure fingerprint bookkeeping (state, synced_at, hash) does not bump
//     it, so a rescan that only refreshes the fingerprint does not
//     invalidate in-flight edits.
//   - SyncedAt: when DB and file were last known-equal (or last forced
//     equal by a policy).
//   - State: "ok", "missing" (file gone, row kept) or "conflict" (open
//     sync_conflicts rows; DB and file untouched until resolved).
type Track struct {
	ID          string  `json:"id" db:"id"`
	AlbumID     string  `json:"album_id" db:"album_id"`
	Title       string  `json:"title" db:"title"`
	Path        string  `json:"path" db:"path"`
	Duration    int     `json:"duration" db:"duration"`
	Bitrate     int     `json:"bitrate" db:"bitrate"`
	Artist      *string `json:"artist" db:"artist"`
	AlbumArtist *string `json:"album_artist" db:"album_artist"`
	TrackNumber *int    `json:"track_number" db:"track_number"`
	DiscNumber  *int    `json:"disc_number" db:"disc_number"`
	Year        *int    `json:"year" db:"year"`
	Genre       *string `json:"genre" db:"genre"`
	Composer    *string `json:"composer" db:"composer"`
	Comment     *string `json:"comment" db:"comment"`
	FileSize    *int64  `json:"file_size" db:"file_size"`
	FileMtime   *int64  `json:"file_mtime" db:"file_mtime"`
	TagsHash    *string `json:"tags_hash" db:"tags_hash"`
	Version     int     `json:"version" db:"version"`
	SyncedAt    *string `json:"synced_at" db:"synced_at"`
	State       string  `json:"state" db:"state"`
}

// TrackMeta holds the optional tag columns of a track. A nil field means
// "not set" (stored as NULL).
type TrackMeta struct {
	Artist      *string
	AlbumArtist *string
	TrackNumber *int
	DiscNumber  *int
	Year        *int
	Genre       *string
	Composer    *string
	Comment     *string
}

// Service provides the library queries and mutations.
type Service struct {
	db          *db.DB
	downloadDir string
	mu          sync.Mutex
}

// NewService builds a library Service.
func NewService(db *db.DB, downloadDir string) *Service {
	return &Service{db: db, downloadDir: downloadDir}
}

// GetArtists returns all artists ordered by name.
func (s *Service) GetArtists() ([]Artist, error) {
	artists := []Artist{}
	err := s.db.Select(&artists, "SELECT * FROM artists ORDER BY name ASC")
	if err != nil {
		log.Printf("Library: GetArtists error: %v", err)
	}
	return artists, err
}

// GetAlbumsByArtist returns the albums of one artist ordered by title.
func (s *Service) GetAlbumsByArtist(artistID string) ([]Album, error) {
	artistID = strings.TrimSpace(artistID)
	albums := []Album{}
	query := `
		SELECT a.id, a.artist_id, a.title, COALESCE(a.cover_art, '') as cover_art, ar.name as artist_name
		FROM albums a
		JOIN artists ar ON a.artist_id = ar.id
		WHERE a.artist_id = ?
		ORDER BY a.title ASC
	`
	err := s.db.Select(&albums, query, artistID)
	if err != nil {
		log.Printf("Library: GetAlbumsByArtist error: %v", err)
		return albums, err
	}
	return albums, nil
}

// GetTracksByAlbum returns the tracks of one album ordered by title.
func (s *Service) GetTracksByAlbum(albumID string) ([]Track, error) {
	tracks := []Track{}
	err := s.db.Select(&tracks, "SELECT * FROM tracks WHERE album_id = ? ORDER BY title ASC", albumID)
	if err != nil {
		log.Printf("Library: GetTracksByAlbum error: %v", err)
	}
	return tracks, err
}

// EnsureArtist returns the id of the named artist, creating it if missing.
func (s *Service) EnsureArtist(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name = strings.TrimSpace(name)
	var id string
	err := s.db.Get(&id, "SELECT id FROM artists WHERE name = ?", name)
	if err == nil {
		return id, nil
	}

	id = uuid.New().String()
	_, err = s.db.Exec("INSERT INTO artists (id, name) VALUES (?, ?)", id, name)
	if err != nil {
		err2 := s.db.Get(&id, "SELECT id FROM artists WHERE name = ?", name)
		if err2 == nil {
			return id, nil
		}
		return "", err
	}
	return id, nil
}

// EnsureAlbum returns the id of (artist, title), creating it if missing.
func (s *Service) EnsureAlbum(artistID string, title string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	title = strings.TrimSpace(title)
	var id string
	err := s.db.Get(&id, "SELECT id FROM albums WHERE artist_id = ? AND title = ?", artistID, title)
	if err == nil {
		return id, nil
	}

	id = uuid.New().String()
	_, err = s.db.Exec("INSERT INTO albums (id, artist_id, title) VALUES (?, ?, ?)", id, artistID, title)
	if err != nil {
		err2 := s.db.Get(&id, "SELECT id FROM albums WHERE artist_id = ? AND title = ?", artistID, title)
		if err2 == nil {
			return id, nil
		}
		return "", err
	}
	return id, nil
}

// RenameAlbum renames the album in the database, renames the on-disk
// directory (updating the stored track paths and cover URL) and rewrites
// the album tag on every track file with the Python helper. It returns
// ErrAlbumExists when the target folder already exists; then nothing is
// moved and no database row changes.
func (s *Service) RenameAlbum(albumID string, newTitle string) error {
	album, err := s.GetAlbum(albumID)
	if err != nil {
		return err
	}

	tracks, err := s.GetTracksByAlbum(albumID)
	if err != nil {
		return err
	}

	if len(tracks) > 0 {
		oldPath := filepath.Dir(tracks[0].Path)
		if !withinDir(s.downloadDir, oldPath) {
			return fmt.Errorf("library: album directory %q is outside the music dir", oldPath)
		}
		folder := sanitizeFilename(newTitle)
		if folder == "" || folder == "." || folder == ".." {
			return fmt.Errorf("library: invalid album title %q", newTitle)
		}
		newPath := filepath.Join(filepath.Dir(oldPath), folder)
		if !withinDir(s.downloadDir, newPath) {
			return fmt.Errorf("library: album title %q escapes the music dir", newTitle)
		}

		if oldPath != newPath {
			if err := fsutil.SafeRename(oldPath, newPath); err != nil {
				var exists *fsutil.ExistsError
				if errors.As(err, &exists) {
					return ErrAlbumExists
				}
				log.Printf("Library: Failed to rename directory from %s to %s: %v", oldPath, newPath, err)
				return err
			}

			for i := range tracks {
				rel, _ := filepath.Rel(oldPath, tracks[i].Path)
				tracks[i].Path = filepath.Join(newPath, rel)
				if _, err := s.db.Exec("UPDATE tracks SET path = ? WHERE id = ?", tracks[i].Path, tracks[i].ID); err != nil {
					log.Printf("Library: Failed to update path for track %s: %v", tracks[i].ID, err)
				}
			}

			if album.CoverArt != "" && strings.HasPrefix(album.CoverArt, "/media/") {
				oldFolder := filepath.Base(oldPath)
				newFolder := filepath.Base(newPath)
				newCover := strings.Replace(album.CoverArt, "/"+oldFolder+"/", "/"+newFolder+"/", 1)
				if _, err := s.db.Exec("UPDATE albums SET cover_art = ? WHERE id = ?", newCover, albumID); err != nil {
					log.Printf("Library: Failed to update cover for album %s: %v", albumID, err)
				}
			}
		}

		// Use the more robust Python script with mutagen. The `--` stops
		// option parsing so a file path starting with '-' cannot be read as
		// an option.
		for _, t := range tracks {
			cmd := exec.Command("python3", "update_metadata.py", "--album", newTitle, "--", t.Path) //nolint:gosec // G204 - python3 and the script name are fixed; t.Path is a file path rooted in the music dir, after `--`
			if out, err := cmd.CombinedOutput(); err != nil {
				log.Printf("Library: Failed to update album tag for %s: %v. Output: %s", t.Path, err, string(out))
			}
		}
	}

	_, err = s.db.Exec("UPDATE albums SET title = ? WHERE id = ?", newTitle, albumID)
	return err
}

// DeleteAlbum removes the album and its tracks from the database (files on
// disk are left in place). It returns sql.ErrNoRows when the album does not
// exist, so the API can answer 404 like the read endpoints do.
func (s *Service) DeleteAlbum(albumID string) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM tracks WHERE album_id = ?", albumID); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			log.Printf("Library: rollback delete album: %v", rbErr)
		}
		return err
	}
	res, err := tx.Exec("DELETE FROM albums WHERE id = ?", albumID)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			log.Printf("Library: rollback delete album: %v", rbErr)
		}
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// No tracks can reference a missing album (FK), so a zero-row album
	// delete means the album was absent from the start.
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteTrack removes one track from the database (the file stays on disk).
// It returns sql.ErrNoRows when the track does not exist.
func (s *Service) DeleteTrack(trackID string) error {
	res, err := s.db.Exec("DELETE FROM tracks WHERE id = ?", trackID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetAlbumCover stores the cover art URL for an album.
func (s *Service) SetAlbumCover(albumID string, coverPath string) error {
	_, err := s.db.Exec("UPDATE albums SET cover_art = ? WHERE id = ?", coverPath, albumID)
	return err
}

// AlbumDir returns the on-disk directory of an album: the directory of its
// first track, or <music dir>/<artist>/<album> when it has no tracks.
func (s *Service) AlbumDir(album *Album) (string, error) {
	tracks, err := s.GetTracksByAlbum(album.ID)
	if err != nil {
		return "", err
	}
	if len(tracks) > 0 {
		dir := filepath.Dir(tracks[0].Path)
		if !withinDir(s.downloadDir, dir) {
			return "", fmt.Errorf("library: album directory %q is outside the music dir", dir)
		}
		return dir, nil
	}
	dir := filepath.Join(s.downloadDir, sanitizeFilename(album.Artist), sanitizeFilename(album.Title))
	if !withinDir(s.downloadDir, dir) {
		return "", fmt.Errorf("library: album path %q escapes the music dir", dir)
	}
	return dir, nil
}

// SaveCoverArt writes cover bytes into the album directory as
// cover.<ext> and stores the /media/ URL on the album row.
func (s *Service) SaveCoverArt(album *Album, data []byte, ext string) (string, error) {
	dir, err := s.AlbumDir(album)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "cover."+ext)
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // G306 - cover art is served to authenticated users
		return "", err
	}
	rel, err := filepath.Rel(s.downloadDir, path)
	if err != nil {
		return "", err
	}
	mediaURL := "/media/" + filepath.ToSlash(rel)
	if err := s.SetAlbumCover(album.ID, mediaURL); err != nil {
		return "", err
	}
	return mediaURL, nil
}

// CoverFilePath resolves the album's stored cover URL to a path inside the
// music dir. It reports false when the album has no cover, and refuses
// URLs that would escape the music dir.
func (s *Service) CoverFilePath(albumID string) (string, bool, error) {
	album, err := s.GetAlbum(albumID)
	if err != nil {
		return "", false, err
	}
	if album.CoverArt == "" {
		return "", false, nil
	}
	rel := strings.TrimPrefix(album.CoverArt, "/media/")
	if rel == album.CoverArt {
		return "", false, fmt.Errorf("cover %q is not a media URL", album.CoverArt)
	}
	rel = filepath.Clean(rel)
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false, fmt.Errorf("invalid cover path %q", album.CoverArt)
	}
	full := filepath.Join(s.downloadDir, rel)
	if !withinDir(s.downloadDir, full) {
		return "", false, fmt.Errorf("cover path escapes the music dir")
	}
	return full, true, nil
}

// UpsertTrack inserts or updates (by file path) the track of an album,
// including its optional tag columns.
func (s *Service) UpsertTrack(albumID string, title, path string, duration, bitrate int, meta TrackMeta) error {
	var id string
	err := s.db.Get(&id, "SELECT id FROM tracks WHERE path = ?", path)
	if err == nil {
		_, err = s.db.Exec(`
			UPDATE tracks SET title = ?, album_id = ?, duration = ?, bitrate = ?,
				artist = ?, album_artist = ?, track_number = ?, disc_number = ?,
				year = ?, genre = ?, composer = ?, comment = ?
			WHERE id = ?
		`, title, albumID, duration, bitrate,
			meta.Artist, meta.AlbumArtist, meta.TrackNumber, meta.DiscNumber,
			meta.Year, meta.Genre, meta.Composer, meta.Comment, id)
		return err
	}

	id = uuid.New().String()
	_, err = s.db.Exec(`
		INSERT INTO tracks (id, album_id, title, path, duration, bitrate,
			artist, album_artist, track_number, disc_number, year, genre, composer, comment)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, albumID, title, path, duration, bitrate,
		meta.Artist, meta.AlbumArtist, meta.TrackNumber, meta.DiscNumber,
		meta.Year, meta.Genre, meta.Composer, meta.Comment)
	return err
}

// GetAlbum returns the album with the given id (sql.ErrNoRows when missing).
func (s *Service) GetAlbum(albumID string) (*Album, error) {
	var a Album
	query := `
		SELECT a.id, a.artist_id, a.title, COALESCE(a.cover_art, '') as cover_art, ar.name as artist_name
		FROM albums a
		JOIN artists ar ON a.artist_id = ar.id
		WHERE a.id = ?
	`
	err := s.db.Get(&a, query, albumID)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// GetTrack returns the track with the given id (sql.ErrNoRows when missing).
func (s *Service) GetTrack(trackID string) (*Track, error) {
	var track Track
	err := s.db.Get(&track, "SELECT * FROM tracks WHERE id = ?", trackID)
	return &track, err
}

// withinDir reports whether path is base itself or lies inside base.
func withinDir(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

// sanitizeFilename replaces characters that are illegal in file names.
func sanitizeFilename(s string) string {
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-",
		"?", "-", "\"", "-", "<", "-", ">", "-", "|", "-",
	)
	return replacer.Replace(s)
}
