package download

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"yt-scraper/internal/jobs"
	"yt-scraper/internal/scanner"
)

type DownloadDetails struct {
	VideoID   string `json:"video_id"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Album     string `json:"album"`
	Thumbnail string `json:"thumbnail"`
}

type Downloader struct {
	jobManager      *jobs.Manager
	scanner         *scanner.Scanner
	downloadDir     string
	queue           chan string
	concurrentLimit int
	activeDownloads int
	proxy           string
	mu              sync.RWMutex
	cond            *sync.Cond
	ytDlpPath       string
}

func NewDownloader(jobManager *jobs.Manager, s *scanner.Scanner, downloadDir string) *Downloader {
	d := &Downloader{
		jobManager:      jobManager,
		scanner:         s,
		downloadDir:     downloadDir,
		queue:           make(chan string, 5000), // Increased buffer
		concurrentLimit: 2,
		ytDlpPath:       "yt-dlp",
	}
	d.cond = sync.NewCond(&d.mu)

	// Recover queued jobs from DB on startup
	allJobs, err := jobManager.GetAllJobs()
	if err == nil {
		for _, j := range allJobs {
			if j.Type == jobs.TypeDownload && (j.Status == jobs.StatusQueued || j.Status == jobs.StatusDownloading) {
				jobManager.UpdateJobStatus(j.ID, jobs.StatusQueued, "0%", "")
				d.queue <- j.ID
			}
		}
	}

	go d.dispatcher()
	return d
}

func (d *Downloader) QueueDownload(details DownloadDetails) (*jobs.Job, error) {
	created, err := d.BulkQueueDownloads([]DownloadDetails{details})
	if err != nil || len(created) == 0 {
		return nil, err
	}
	return created[0], nil
}

func (d *Downloader) BulkQueueDownloads(detailsList []DownloadDetails) ([]*jobs.Job, error) {
	// Convert details to interface list for BulkCreateJobs
	interfaces := make([]interface{}, len(detailsList))
	for i, d := range detailsList {
		interfaces[i] = d
	}

	created, err := d.jobManager.BulkCreateJobs(jobs.TypeDownload, interfaces)
	if err != nil {
		return nil, err
	}

	// Push to queue in background so we don't block the caller if channel is slow
	go func() {
		for _, j := range created {
			d.queue <- j.ID
		}
	}()

	return created, nil
}

func (d *Downloader) SetLimit(limit int) {
	d.mu.Lock()
	d.concurrentLimit = limit
	d.cond.Broadcast()
	d.mu.Unlock()
}

func (d *Downloader) GetLimit() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.concurrentLimit
}

func (d *Downloader) SetProxy(proxy string) {
	d.mu.Lock()
	d.proxy = proxy
	d.mu.Unlock()
}

func (d *Downloader) GetProxy() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.proxy
}

func (d *Downloader) dispatcher() {
	for jobID := range d.queue {
		d.mu.Lock()
		for d.activeDownloads >= d.concurrentLimit {
			d.cond.Wait()
		}
		d.activeDownloads++
		d.mu.Unlock()

		go func(id string) {
			defer func() {
				d.mu.Lock()
				d.activeDownloads--
				d.cond.Broadcast()
				d.mu.Unlock()
			}()
			d.processDownload(id)
		}(jobID)
	}
}

func (d *Downloader) processDownload(jobID string) {
	job, err := d.jobManager.GetJob(jobID)
	if err != nil || job == nil {
		return
	}

	// Double check status - might have been canceled or already finished
	if job.Status == jobs.StatusCompleted || job.Status == jobs.StatusFailed {
		return
	}

	d.jobManager.UpdateJobStatus(jobID, jobs.StatusDownloading, "0%", "")

	var details DownloadDetails
	err = d.parseDetails(job.Details, &details)
	if err != nil {
		d.jobManager.UpdateJobStatus(jobID, jobs.StatusFailed, "", "Invalid job details")
		return
	}

	artist := sanitize(details.Artist)
	album := sanitize(details.Album)
	title := sanitize(details.Title)

	outDir := filepath.Join(d.downloadDir, artist, album)
	os.MkdirAll(outDir, 0755)

	outTemplate := filepath.Join(outDir, title+".%(ext)s")

	args := []string{
		"https://youtube.com/watch?v=" + details.VideoID,
		"--extract-audio",
		"--audio-format", "flac",
		"--embed-thumbnail",
		"--embed-metadata",
		"--parse-metadata", "title:%(artist)s",
		"--replace-in-metadata", "artist", "^.*$", details.Artist,
		"--parse-metadata", "title:%(album)s",
		"--replace-in-metadata", "album", "^.*$", details.Album,
		"--js-runtimes", "node",
		"--output", outTemplate,
		"--newline",
	}

	proxy := d.GetProxy()
	if proxy != "" {
		args = append(args, "--proxy", proxy)
	}

	cmd := exec.Command(d.ytDlpPath, args...)

	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	if err != nil {
		errStr := outputStr
		if len(errStr) > 500 {
			errStr = errStr[:500]
		}
		d.jobManager.UpdateJobStatus(jobID, jobs.StatusFailed, "", errStr)
	} else {
		d.jobManager.UpdateJobStatus(jobID, jobs.StatusCompleted, "100%", "")

		// Auto-index
		downloadedFile := strings.Replace(outTemplate, "%(ext)s", "flac", 1)
		if d.scanner != nil {
			d.scanner.IndexFile(downloadedFile)
		}
	}
}

func sanitize(s string) string {
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-",
		"?", "-", "\"", "-", "<", "-", ">", "-", "|", "-",
	)
	return replacer.Replace(s)
}

func (d *Downloader) parseDetails(detailsStr string, v interface{}) error {
	return json.Unmarshal([]byte(detailsStr), v)
}
