// Package download implements the yt-dlp backed download queue: it tracks
// download jobs, limits concurrency and hands finished files to the scanner.
package download

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"yt-scraper/internal/jobs"
	"yt-scraper/internal/scanner"
)

// Details describes one video to download, as sent by the frontend.
type Details struct {
	VideoID   string `json:"video_id"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Album     string `json:"album"`
	Thumbnail string `json:"thumbnail"`
}

// Downloader manages the download queue and the concurrency limit.
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

// NewDownloader builds a Downloader, re-queues unfinished download jobs
// found in the database and starts the dispatcher.
//
// The initial concurrency limit is resolved with the precedence
// CONCURRENT_DOWNLOAD env var -> default 2 -> runtime SetLimit: the env var
// wins at startup; a missing, unparseable or below-1 value falls back to
// the default of 2 with a warning, and a value above 32 is clamped to 32
// with a warning. SetLimit (e.g. via POST /api/config) still overrides the
// limit afterwards.
func NewDownloader(jobManager *jobs.Manager, s *scanner.Scanner, downloadDir string) *Downloader {
	limit := 2
	raw := os.Getenv("CONCURRENT_DOWNLOAD")
	n, err := strconv.Atoi(raw)
	switch {
	case raw == "":
		log.Printf("download: CONCURRENT_DOWNLOAD is not set, using default %d", limit)
	case err != nil:
		// The raw value is not logged: it is env input (gosec G706).
		log.Printf("download: CONCURRENT_DOWNLOAD is not a valid integer, using default %d", limit)
	case n < 1:
		log.Printf("download: CONCURRENT_DOWNLOAD=%d is below 1, using default %d", n, limit)
	case n > 32:
		log.Printf("download: CONCURRENT_DOWNLOAD=%d exceeds the maximum of 32, clamping", n)
		limit = 32
	default:
		limit = n
	}

	d := &Downloader{
		jobManager:      jobManager,
		scanner:         s,
		downloadDir:     downloadDir,
		queue:           make(chan string, 5000), // Increased buffer
		concurrentLimit: limit,
		ytDlpPath:       "yt-dlp",
	}
	d.cond = sync.NewCond(&d.mu)

	// Recover queued jobs from DB on startup
	allJobs, err := jobManager.GetAllJobs()
	if err == nil {
		for _, j := range allJobs {
			if j.Type == jobs.TypeDownload && (j.Status == jobs.StatusQueued || j.Status == jobs.StatusDownloading) {
				if uerr := jobManager.UpdateJobStatus(j.ID, jobs.StatusQueued, "0%", ""); uerr != nil {
					log.Printf("download: requeue job %s: %v", j.ID, uerr)
				}
				d.queue <- j.ID
			}
		}
	}

	go d.dispatcher()
	return d
}

// QueueDownload queues a single download and returns its job.
func (d *Downloader) QueueDownload(details Details) (*jobs.Job, error) {
	created, err := d.BulkQueueDownloads([]Details{details})
	if err != nil || len(created) == 0 {
		return nil, err
	}
	return created[0], nil
}

// BulkQueueDownloads creates one queued job per details entry and pushes
// them onto the download queue.
func (d *Downloader) BulkQueueDownloads(detailsList []Details) ([]*jobs.Job, error) {
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

// SetLimit changes the number of concurrent downloads; waiting workers are
// woken up.
func (d *Downloader) SetLimit(limit int) {
	d.mu.Lock()
	d.concurrentLimit = limit
	d.cond.Broadcast()
	d.mu.Unlock()
}

// GetLimit returns the current concurrency limit.
func (d *Downloader) GetLimit() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.concurrentLimit
}

// SetProxy sets the proxy passed to yt-dlp as --proxy.
func (d *Downloader) SetProxy(proxy string) {
	d.mu.Lock()
	d.proxy = proxy
	d.mu.Unlock()
}

// GetProxy returns the currently configured proxy (may be empty).
func (d *Downloader) GetProxy() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.proxy
}

// setStatus updates the job status and logs failures; a status write must
// never fail the download pipeline itself.
func (d *Downloader) setStatus(id string, status jobs.JobStatus, progress, errMsg string) {
	if err := d.jobManager.UpdateJobStatus(id, status, progress, errMsg); err != nil {
		log.Printf("download: update job %s to %s: %v", id, status, err)
	}
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

	d.setStatus(jobID, jobs.StatusDownloading, "0%", "")

	var details Details
	err = d.parseDetails(job.Details, &details)
	if err != nil {
		d.setStatus(jobID, jobs.StatusFailed, "", "Invalid job details")
		return
	}

	artist := sanitize(details.Artist)
	album := sanitize(details.Album)
	title := sanitize(details.Title)

	outDir := filepath.Join(d.downloadDir, artist, album)
	if err := os.MkdirAll(outDir, 0o755); err != nil { //nolint:gosec // G301 - the music dir is a shared media library that other services (e.g. a music player) must read; not a private dir
		d.setStatus(jobID, jobs.StatusFailed, "", "failed to create output directory: "+err.Error())
		return
	}

	outTemplate := filepath.Join(outDir, title+".%(ext)s")
	args := buildArgs(details, outTemplate, d.GetProxy())

	cmd := exec.Command(d.ytDlpPath, args...) //nolint:gosec // G204 - ytDlpPath is the fixed "yt-dlp" binary; user values only ever fill named option values (see buildArgs)

	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	if err != nil {
		errStr := outputStr
		if len(errStr) > 500 {
			errStr = errStr[:500]
		}
		d.setStatus(jobID, jobs.StatusFailed, "", errStr)
	} else {
		d.setStatus(jobID, jobs.StatusCompleted, "100%", "")

		// Auto-index
		downloadedFile := strings.Replace(outTemplate, "%(ext)s", "flac", 1)
		if d.scanner != nil {
			d.scanner.IndexFile(downloadedFile)
		}
	}
}

// buildArgs assembles the yt-dlp command line for downloading one video as
// FLAC. The constructed URL is always prefixed with a literal scheme, and
// every user-derived value (artist, album, output template) is passed as
// the value of a named option, so none of them can be parsed as an option.
func buildArgs(details Details, outTemplate, proxy string) []string {
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
	if proxy != "" {
		args = append(args, "--proxy", proxy)
	}
	return args
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
