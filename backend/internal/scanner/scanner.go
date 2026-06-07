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
				return nil
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

	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	
	rel, _ := filepath.Rel(s.basePath, path)
	parts := strings.Split(rel, string(os.PathSeparator))

	if artist == "" {
		if len(parts) >= 2 {
			artist = parts[0]
		} else {
			artist = "Unknown Artist"
		}
	}
	if album == "" {
		if len(parts) >= 3 {
			album = parts[1]
		} else if len(parts) == 2 {
			album = parts[0]
		} else {
			album = "Unknown Album"
		}
	}

	duration, _ := strconv.ParseFloat(data.Format.Duration, 64)
	bitrate, _ := strconv.Atoi(data.Format.BitRate)

	artistID, err := s.lib.EnsureArtist(artist)
	if err != nil {
		return
	}

	albumID, err := s.lib.EnsureAlbum(artistID, album)
	if err != nil {
		return
	}

	s.detectCover(albumID, filepath.Dir(path))

	err = s.lib.UpsertTrack(albumID, title, path, int(duration), bitrate)
	if err != nil {
		log.Printf("Error upserting track %s: %v", title, err)
	}
}

func (s *Scanner) detectCover(albumID string, dir string) {
	coverNames := []string{"cover.jpg", "cover.png", "folder.jpg", "front.jpg", "album.jpg"}
	
	for _, name := range coverNames {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			rel, _ := filepath.Rel(s.basePath, path)
			s.lib.SetAlbumCover(albumID, "/media/"+filepath.ToSlash(rel))
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
					s.lib.SetAlbumCover(albumID, "/media/"+filepath.ToSlash(rel))
					return
				}
			}
		}
	}
}
