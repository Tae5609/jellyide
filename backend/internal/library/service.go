package library

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"yt-scraper/internal/db"
)

type Artist struct {
	ID         int    `json:"id" db:"id"`
	Name       string `json:"name" db:"name"`
	AlbumCount int    `json:"album_count" db:"album_count"`
}

type Album struct {
	ID         int    `json:"id" db:"id"`
	ArtistID   int    `json:"artist_id" db:"artist_id"`
	Title      string `json:"title" db:"title"`
	CoverArt   string `json:"cover_art" db:"cover_art"`
	Artist     string `json:"artist" db:"artist_name"` // For convenience
	TrackCount int    `json:"track_count" db:"track_count"`
}

type Track struct {
	ID       int    `json:"id" db:"id"`
	AlbumID  int    `json:"album_id" db:"album_id"`
	Title    string `json:"title" db:"title"`
	Path     string `json:"path" db:"path"`
	Duration int    `json:"duration" db:"duration"`
	Bitrate  int    `json:"bitrate" db:"bitrate"`
}

type Service struct {
	db          *db.DB
	downloadDir string
}

func NewService(db *db.DB, downloadDir string) *Service {
	return &Service{db: db, downloadDir: downloadDir}
}

func (s *Service) GetArtists() ([]Artist, error) {
	var artists []Artist
	query := `
		SELECT a.id, a.name, COUNT(al.id) as album_count 
		FROM artists a 
		LEFT JOIN albums al ON a.id = al.artist_id 
		GROUP BY a.id 
		ORDER BY a.name ASC
	`
	err := s.db.Select(&artists, query)
	return artists, err
}

func (s *Service) GetAlbumsByArtist(artistID int) ([]Album, error) {
	var albums []Album
	query := `
		SELECT a.id, a.artist_id, a.title, COALESCE(a.cover_art, '') as cover_art, COALESCE(ar.name, '') as artist_name, COUNT(t.id) as track_count
		FROM albums a
		JOIN artists ar ON a.artist_id = ar.id
		LEFT JOIN tracks t ON a.id = t.album_id
		WHERE a.artist_id = ?
		GROUP BY a.id
		ORDER BY a.title ASC
	`
	err := s.db.Select(&albums, query, artistID)
	return albums, err
}

func (s *Service) GetFirstTrackPath(albumID int) (string, error) {
	var path string
	err := s.db.Get(&path, "SELECT path FROM tracks WHERE album_id = ? LIMIT 1", albumID)
	return path, err
}

func (s *Service) GetTracksByAlbum(albumID int) ([]Track, error) {
	var tracks []Track
	err := s.db.Select(&tracks, "SELECT * FROM tracks WHERE album_id = ? ORDER BY title ASC", albumID)
	return tracks, err
}

// EnsureArtist creates or gets an artist
func (s *Service) EnsureArtist(name string) (int, error) {
	var id int
	err := s.db.Get(&id, "SELECT id FROM artists WHERE name = ?", name)
	if err == nil {
		return id, nil
	}
	res, err := s.db.Exec("INSERT INTO artists (name) VALUES (?)", name)
	if err != nil {
		return 0, err
	}
	insertId, _ := res.LastInsertId()
	return int(insertId), nil
}

// EnsureAlbum creates or gets an album
func (s *Service) EnsureAlbum(artistID int, title string) (int, error) {
	var id int
	err := s.db.Get(&id, "SELECT id FROM albums WHERE artist_id = ? AND title = ?", artistID, title)
	if err == nil {
		return id, nil
	}
	res, err := s.db.Exec("INSERT INTO albums (artist_id, title) VALUES (?, ?)", artistID, title)
	if err != nil {
		return 0, err
	}
	insertId, _ := res.LastInsertId()
	return int(insertId), nil
}

// UpsertTrack inserts or updates a track
func (s *Service) UpsertTrack(albumID int, title, path string, duration, bitrate int) error {
	_, err := s.db.Exec(`
		INSERT INTO tracks (album_id, title, path, duration, bitrate)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			title=excluded.title,
			duration=excluded.duration,
			bitrate=excluded.bitrate
	`, albumID, title, path, duration, bitrate)
	return err
}

func (s *Service) GetAlbum(albumID int) (*Album, error) {
	var album Album
	query := `
		SELECT a.id, a.artist_id, a.title, COALESCE(a.cover_art, '') as cover_art, COALESCE(ar.name, '') as artist_name, COUNT(t.id) as track_count
		FROM albums a
		JOIN artists ar ON a.artist_id = ar.id
		LEFT JOIN tracks t ON a.id = t.album_id
		WHERE a.id = ?
		GROUP BY a.id
	`
	err := s.db.Get(&album, query, albumID)
	return &album, err
}

func (s *Service) DeleteTrack(trackID int) error {
	// Get path first to delete file
	var path string
	err := s.db.Get(&path, "SELECT path FROM tracks WHERE id = ?", trackID)
	if err != nil {
		return err
	}

	// Delete from DB
	_, err = s.db.Exec("DELETE FROM tracks WHERE id = ?", trackID)
	if err != nil {
		return err
	}

	// Try to delete physical file
	_ = os.Remove(path)
	return nil
}

func (s *Service) DeleteAlbum(albumID int) error {
	// 1. Get album details to find path
	album, err := s.GetAlbum(albumID)
	if err != nil {
		return err
	}

	albumDir := filepath.Join(s.downloadDir, Sanitize(album.Artist), Sanitize(album.Title))

	// 2. Delete all tracks for this album from DB
	_, err = s.db.Exec("DELETE FROM tracks WHERE album_id = ?", albumID)
	if err != nil {
		return err
	}

	// 3. Delete album from DB
	_, err = s.db.Exec("DELETE FROM albums WHERE id = ?", albumID)
	if err != nil {
		return err
	}

	// 4. Delete physical directory
	_ = os.RemoveAll(albumDir)

	return nil
}

func (s *Service) UpdateAlbumTitle(albumID int, newTitle string) error {
	// 1. Get current details
	album, err := s.GetAlbum(albumID)
	if err != nil {
		return err
	}

	if album.Title == newTitle {
		return nil
	}

	var tracks []Track
	err = s.db.Select(&tracks, "SELECT * FROM tracks WHERE album_id = ?", albumID)
	if err != nil || len(tracks) == 0 {
		// Just update title in DB if no tracks
		_, err = s.db.Exec("UPDATE albums SET title = ? WHERE id = ?", newTitle, albumID)
		return err
	}

	// Use the directory of the first track as the definitive oldPath
	oldPath := filepath.Dir(tracks[0].Path)
	newAlbumDir := Sanitize(newTitle)
	newPath := filepath.Join(filepath.Dir(oldPath), newAlbumDir)

	// 2. Rename directory on disk
	if _, err := os.Stat(oldPath); err == nil && oldPath != newPath {
		err = os.Rename(oldPath, newPath)
		if err != nil {
			log.Printf("Failed to rename directory from %s to %s: %v", oldPath, newPath, err)
			return err
		}
	}

	// 3. Update database
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}

	// Update album title
	_, err = tx.Exec("UPDATE albums SET title = ? WHERE id = ?", newTitle, albumID)
	if err != nil {
		tx.Rollback()
		return err
	}

	for _, track := range tracks {
		// New path = replace old folder part with new folder part
		rel, err := filepath.Rel(oldPath, track.Path)
		if err != nil {
			log.Printf("Rel path error: %v (oldPath: %s, trackPath: %s)", err, oldPath, track.Path)
			continue
		}
		newTrackPath := filepath.Join(newPath, rel)

		_, err = tx.Exec("UPDATE tracks SET path = ? WHERE id = ?", newTrackPath, track.ID)
		if err != nil {
			tx.Rollback()
			return err
		}

		// 4. Update physical metadata tags
		cmd := exec.Command("python3", "update_metadata.py", newTrackPath, "--album", newTitle)
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Printf("Failed to update album tag for %s: %v, output: %s", newTrackPath, err, string(out))
		}
	}

	return tx.Commit()
}

func Sanitize(s string) string {
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-",
		"?", "-", "\"", "-", "<", "-", ">", "-", "|", "-",
	)
	return replacer.Replace(s)
}
