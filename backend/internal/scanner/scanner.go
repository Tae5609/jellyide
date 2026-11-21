// Package scanner walks the music directory, probes media files with
// ffprobe and indexes them into the library database.
package scanner

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
)

// Scanner indexes media files found under a base directory.
type Scanner struct {
	lib        *library.Service
	jobManager *jobs.Manager
	basePath   string
}

// NewScanner builds a Scanner for the given library, job manager and base
// directory.
func NewScanner(lib *library.Service, jobManager *jobs.Manager, basePath string) *Scanner {
	return &Scanner{
		lib:        lib,
		jobManager: jobManager,
		basePath:   basePath,
	}
}

// FFProbeOutput is the JSON shape of
// `ffprobe -print_format json -show_format` for one file.
type FFProbeOutput struct {
	Format struct {
		Duration string            `json:"duration"`
		BitRate  string            `json:"bit_rate"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

// probeInfo is the per-file metadata derived by parseFFProbe.
type probeInfo struct {
	Title    string
	Artist   string
	Album    string
	Duration int
	Bitrate  int
}

// parseFFProbe parses ffprobe JSON output and derives title, artist,
// album, duration and bitrate for the file at path. Fallbacks: the title
// comes from the file name, artist and album from the directory layout
// relative to basePath.
func parseFFProbe(data []byte, path, basePath string) (probeInfo, error) {
	var out FFProbeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return probeInfo{}, fmt.Errorf("parse ffprobe output: %w", err)
	}

	tags := out.Format.Tags
	info := probeInfo{
		Title:  tags["title"],
		Artist: tags["artist"],
		Album:  tags["album"],
	}
	if info.Title == "" {
		info.Title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	rel, _ := filepath.Rel(basePath, path)
	parts := strings.Split(rel, string(os.PathSeparator))

	if info.Artist == "" {
		if len(parts) >= 2 {
			info.Artist = parts[0]
		} else {
			info.Artist = "Unknown Artist"
		}
	}
	if info.Album == "" {
		switch {
		case len(parts) >= 3:
			info.Album = parts[1]
		case len(parts) == 2:
			info.Album = parts[0]
		default:
			info.Album = "Unknown Album"
		}
	}

	duration, _ := strconv.ParseFloat(out.Format.Duration, 64)
	bitrate, _ := strconv.Atoi(out.Format.BitRate)
	info.Duration = int(duration)
	info.Bitrate = bitrate

	return info, nil
}

// Scan starts a scan job that walks the base directory in the background
// and indexes every supported media file it finds.
func (s *Scanner) Scan() error {
	job, err := s.jobManager.CreateJob(jobs.TypeScan, map[string]string{"path": s.basePath})
	if err != nil {
		return err
	}

	go func() {
		if err := s.jobManager.UpdateJobStatus(job.ID, jobs.StatusRunning, "Scanning...", ""); err != nil {
			log.Printf("scanner: update scan job %s: %v", job.ID, err)
		}

		count := 0
		walkErr := filepath.Walk(s.basePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				// Skip unreadable entries and keep scanning the rest.
				return nil //nolint:nilerr // walk param error is intentionally swallowed
			}
			if info.IsDir() {
				return nil
			}

			if !isSupportedAudioExt(path) {
				return nil
			}

			s.processFile(path)
			count++
			if count%10 == 0 {
				if uerr := s.jobManager.UpdateJobStatus(job.ID, jobs.StatusRunning, strconv.Itoa(count)+" tracks found", ""); uerr != nil {
					log.Printf("scanner: update scan job %s: %v", job.ID, uerr)
				}
			}
			return nil
		})

		if walkErr != nil {
			if err := s.jobManager.UpdateJobStatus(job.ID, jobs.StatusFailed, "", walkErr.Error()); err != nil {
				log.Printf("scanner: update scan job %s: %v", job.ID, err)
			}
			return
		}
		if err := s.jobManager.UpdateJobStatus(job.ID, jobs.StatusCompleted, "100%", ""); err != nil {
			log.Printf("scanner: update scan job %s: %v", job.ID, err)
		}
	}()

	return nil
}

// IndexFile indexes a single file (used after a download finishes).
func (s *Scanner) IndexFile(path string) {
	s.processFile(path)
}

// isSupportedAudioExt reports whether path has a media extension the
// scanner indexes.
func isSupportedAudioExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".flac", ".mp3", ".m4a", ".aac":
		return true
	default:
		return false
	}
}

func (s *Scanner) processFile(path string) {
	cmd := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", path) //nolint:gosec // G204 - ffprobe is a fixed binary; path comes from the walk under basePath or from a finished download
	out, err := cmd.Output()
	if err != nil {
		return
	}

	info, err := parseFFProbe(out, path, s.basePath)
	if err != nil {
		log.Printf("scanner: %v", err)
		return
	}

	artistID, err := s.lib.EnsureArtist(info.Artist)
	if err != nil {
		return
	}

	albumID, err := s.lib.EnsureAlbum(artistID, info.Album)
	if err != nil {
		return
	}

	s.detectCover(albumID, filepath.Dir(path))

	err = s.lib.UpsertTrack(albumID, info.Title, path, info.Duration, info.Bitrate)
	if err != nil {
		log.Printf("Error upserting track %s: %v", info.Title, err)
	}
}

func (s *Scanner) detectCover(albumID string, dir string) {
	coverNames := []string{"cover.jpg", "cover.png", "folder.jpg", "front.jpg", "album.jpg"}

	for _, name := range coverNames {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			rel, _ := filepath.Rel(s.basePath, path)
			if err := s.lib.SetAlbumCover(albumID, "/media/"+filepath.ToSlash(rel)); err != nil {
				log.Printf("scanner: set cover for album %s: %v", albumID, err)
			}
			return
		}
	}

	files, err := os.ReadDir(dir)
	if err == nil {
		for _, f := range files {
			if !f.IsDir() {
				ext := strings.ToLower(filepath.Ext(f.Name()))
				if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
					path := filepath.Join(dir, f.Name())
					rel, _ := filepath.Rel(s.basePath, path)
					if err := s.lib.SetAlbumCover(albumID, "/media/"+filepath.ToSlash(rel)); err != nil {
						log.Printf("scanner: set cover for album %s: %v", albumID, err)
					}
					return
				}
			}
		}
	}
}
