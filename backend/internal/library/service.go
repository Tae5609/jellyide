package library

import (
	"github.com/google/uuid"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"yt-scraper/internal/db"
)

type Artist struct {
	ID   string `json:"id" db:"id"`
	Name string `json:"name" db:"name"`
}

type Album struct {
	ID       string `json:"id" db:"id"`
	ArtistID string `json:"artist_id" db:"artist_id"`
	Title    string `json:"title" db:"title"`
	CoverArt string `json:"cover_art" db:"cover_art"`
	Artist   string `json:"artist" db:"artist_name"`
}

type Track struct {
	ID       string `json:"id" db:"id"`
	AlbumID  string `json:"album_id" db:"album_id"`
	Title    string `json:"title" db:"title"`
	Path     string `json:"path" db:"path"`
	Duration int    `json:"duration" db:"duration"`
	Bitrate  int    `json:"bitrate" db:"bitrate"`
}

type Service struct {
	db          *db.DB
	downloadDir string
	mu          sync.Mutex
}

func NewService(db *db.DB, downloadDir string) *Service {
	return &Service{db: db, downloadDir: downloadDir}
}

func (s *Service) GetArtists() ([]Artist, error) {
	artists := []Artist{}
	err := s.db.Select(&artists, "SELECT * FROM artists ORDER BY name ASC")
	if err != nil {
		log.Printf("Library: GetArtists error: %v", err)
	}
	return artists, err
}

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

func (s *Service) GetTracksByAlbum(albumID string) ([]Track, error) {
	tracks := []Track{}
	err := s.db.Select(&tracks, "SELECT * FROM tracks WHERE album_id = ? ORDER BY title ASC", albumID)
	if err != nil {
		log.Printf("Library: GetTracksByAlbum error: %v", err)
	}
	return tracks, err
}

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
				s.db.Exec("UPDATE tracks SET path = ? WHERE id = ?", tracks[i].Path, tracks[i].ID)
			}

			if album.CoverArt != "" && strings.HasPrefix(album.CoverArt, "/media/") {
				oldFolder := filepath.Base(oldPath)
				newFolder := filepath.Base(newPath)
				newCover := strings.Replace(album.CoverArt, "/"+oldFolder+"/", "/"+newFolder+"/", 1)
				s.db.Exec("UPDATE albums SET cover_art = ? WHERE id = ?", newCover, albumID)
			}
		}

		// Use the more robust Python script with mutagen
		for _, t := range tracks {
			cmd := exec.Command("python3", "update_metadata.py", t.Path, "--album", newTitle)
			if out, err := cmd.CombinedOutput(); err != nil {
				log.Printf("Library: Failed to update album tag for %s: %v. Output: %s", t.Path, err, string(out))
			}
		}
	}

	_, err = s.db.Exec("UPDATE albums SET title = ? WHERE id = ?", newTitle, albumID)
	return err
}

func (s *Service) DeleteAlbum(albumID string) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM tracks WHERE album_id = ?", albumID)
	if err != nil {
		tx.Rollback()
		return err
	}
	_, err = tx.Exec("DELETE FROM albums WHERE id = ?", albumID)
	if err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Service) DeleteTrack(trackID string) error {
	_, err := s.db.Exec("DELETE FROM tracks WHERE id = ?", trackID)
	return err
}

func (s *Service) SetAlbumCover(albumID string, coverPath string) error {
	_, err := s.db.Exec("UPDATE albums SET cover_art = ? WHERE id = ?", coverPath, albumID)
	return err
}

func (s *Service) UpsertTrack(albumID string, title, path string, duration, bitrate int) error {
	var id string
	err := s.db.Get(&id, "SELECT id FROM tracks WHERE path = ?", path)
	if err == nil {
		_, err = s.db.Exec(`
			UPDATE tracks SET title = ?, album_id = ?, duration = ?, bitrate = ?
			WHERE id = ?
		`, title, albumID, duration, bitrate, id)
		return err
	}

	id = uuid.New().String()
	_, err = s.db.Exec(`
		INSERT INTO tracks (id, album_id, title, path, duration, bitrate)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, albumID, title, path, duration, bitrate)
	return err
}

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

func (s *Service) GetTrack(trackID string) (*Track, error) {
	var track Track
	err := s.db.Get(&track, "SELECT * FROM tracks WHERE id = ?", trackID)
	return &track, err
}

func sanitizeFilename(s string) string {
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-",
		"?", "-", "\"", "-", "<", "-", ">", "-", "|", "-",
	)
	return replacer.Replace(s)
}
