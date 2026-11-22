// Package library tracks the artist/album/track hierarchy of the music
// collection in SQLite and keeps the on-disk folder layout in sync.
package library

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"

	"yt-scraper/internal/db"
)

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
// missing (NULL) value is distinguishable from an empty one.
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
// the album tag on every track file with the Python helper.
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
		newPath := filepath.Join(filepath.Dir(oldPath), sanitizeFilename(newTitle))

		if oldPath != newPath {
			if err := os.Rename(oldPath, newPath); err != nil {
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
// disk are left in place).
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
	if _, err = tx.Exec("DELETE FROM albums WHERE id = ?", albumID); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			log.Printf("Library: rollback delete album: %v", rbErr)
		}
		return err
	}
	return tx.Commit()
}

// DeleteTrack removes one track from the database (the file stays on disk).
func (s *Service) DeleteTrack(trackID string) error {
	_, err := s.db.Exec("DELETE FROM tracks WHERE id = ?", trackID)
	return err
}

// SetAlbumCover stores the cover art URL for an album.
func (s *Service) SetAlbumCover(albumID string, coverPath string) error {
	_, err := s.db.Exec("UPDATE albums SET cover_art = ? WHERE id = ?", coverPath, albumID)
	return err
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

// sanitizeFilename replaces characters that are illegal in file names.
func sanitizeFilename(s string) string {
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-",
		"?", "-", "\"", "-", "<", "-", ">", "-", "|", "-",
	)
	return replacer.Replace(s)
}
