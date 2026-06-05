package scanner

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
)

type Scanner struct {
	lib        *library.Service
	jobManager *jobs.Manager
	basePath   string
}

func NewScanner(lib *library.Service, jobManager *jobs.Manager, basePath string) *Scanner {
	return &Scanner{
		lib:        lib,
		jobManager: jobManager,
		basePath:   basePath,
	}
}

type FFProbeOutput struct {
	Format struct {
		Duration string            `json:"duration"`
		BitRate  string            `json:"bit_rate"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

func (s *Scanner) Scan() error {
	job, err := s.jobManager.CreateJob(jobs.TypeScan, map[string]string{"path": s.basePath})
	if err != nil {
		return err
	}

	go func() {
		s.jobManager.UpdateJobStatus(job.ID, jobs.StatusRunning, "Scanning...", "")
		
		count := 0
		err := filepath.Walk(s.basePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors
			}
			if info.IsDir() {
				return nil
			}

			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".flac" && ext != ".mp3" && ext != ".m4a" && ext != ".aac" {
				return nil
			}

			s.processFile(path)
			count++
			if count%10 == 0 {
				s.jobManager.UpdateJobStatus(job.ID, jobs.StatusRunning, strconv.Itoa(count)+" tracks found", "")
			}
			return nil
		})

		if err != nil {
			s.jobManager.UpdateJobStatus(job.ID, jobs.StatusFailed, "", err.Error())
		} else {
			s.jobManager.UpdateJobStatus(job.ID, jobs.StatusCompleted, "100%", "")
		}
	}()

	return nil
}

func (s *Scanner) IndexFile(path string) {
	s.processFile(path)
}

func (s *Scanner) processFile(path string) {
	cmd := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", path)
	out, err := cmd.Output()
	if err != nil {
		return
	}

	var data FFProbeOutput
	if err := json.Unmarshal(out, &data); err != nil {
		return
	}

	tags := data.Format.Tags
	title := tags["title"]
	artist := tags["artist"]
	album := tags["album"]

	// Fallback to filename if tags are missing
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if artist == "" {
		// Try to get artist from directory structure: basePath/Artist/Album/Track
		rel, err := filepath.Rel(s.basePath, path)
		if err == nil {
			parts := strings.Split(rel, string(os.PathSeparator))
			if len(parts) >= 2 {
				artist = parts[0]
			}
		}
		if artist == "" {
			artist = "Unknown Artist"
		}
	}
	if album == "" {
		rel, err := filepath.Rel(s.basePath, path)
		if err == nil {
			parts := strings.Split(rel, string(os.PathSeparator))
			if len(parts) >= 2 {
				album = parts[1]
			}
		}
		if album == "" {
			album = "Unknown Album"
		}
	}

	duration, _ := strconv.ParseFloat(data.Format.Duration, 64)
	bitrate, _ := strconv.Atoi(data.Format.BitRate)

	artistID, err := s.lib.EnsureArtist(artist)
	if err != nil {
		log.Printf("Error ensuring artist %s: %v", artist, err)
		return
	}

	albumID, err := s.lib.EnsureAlbum(artistID, album)
	if err != nil {
		log.Printf("Error ensuring album %s: %v", album, err)
		return
	}

	err = s.lib.UpsertTrack(albumID, title, path, int(duration), bitrate)
	if err != nil {
		log.Printf("Error upserting track %s: %v", title, err)
	}
}
