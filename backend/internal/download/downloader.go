// Package download implements the yt-dlp backed download queue: it tracks
// download jobs, limits concurrency and hands finished files to the scanner.
package download

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"yt-scraper/internal/jobs"
	"yt-scraper/internal/scanner"
)

// DefaultMaxRetries is the automatic-retry count when DOWNLOAD_MAX_RETRIES
// is unset.
const DefaultMaxRetries = 2

// maxRetriesBackoff is the automatic-retry backoff schedule, indexed by the
// 1-based attempt number: 30s, 2m, 10m, capped at 10m.
var maxRetriesBackoff = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}

// retryBackoff returns the delay before automatic retry number n (1-based),
// capped at the last entry of the schedule.
func retryBackoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	if n-1 >= len(maxRetriesBackoff) {
		return maxRetriesBackoff[len(maxRetriesBackoff)-1]
	}
	return maxRetriesBackoff[n-1]
}

// MaxRetriesFromEnv reads DOWNLOAD_MAX_RETRIES. The default is 2; 0
// disables automatic retry. Empty or invalid values fall back to the
// default (a negative value is invalid too).
func MaxRetriesFromEnv() int {
	v := os.Getenv("DOWNLOAD_MAX_RETRIES")
	if v == "" {
		return DefaultMaxRetries
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		//nolint:gosec // G706 - v is the operator's own env value, not remote input; %q keeps it one token
		log.Printf("download: invalid DOWNLOAD_MAX_RETRIES %q, using default %d", v, DefaultMaxRetries)
		return DefaultMaxRetries
	}
	return n
}

// Details describes one video to download, as sent by the frontend.
type Details struct {
	VideoID   string `json:"video_id"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Album     string `json:"album"`
	Thumbnail string `json:"thumbnail"`
}

// Downloader manages the download queue, the concurrency limit and the
// automatic retry of transiently failed downloads.
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
	maxRetries      int
	now             func() time.Time
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
//
// The automatic-retry count comes from DOWNLOAD_MAX_RETRIES (default 2,
// 0 disables).
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
		maxRetries:      MaxRetriesFromEnv(),
		now:             time.Now,
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

// Retry manually re-queues one failed download job (via jobs.Manager.
// RetryJob) and wakes the dispatcher so it runs without waiting for the
// next poll tick.
func (d *Downloader) Retry(id string) (*jobs.Job, error) {
	job, err := d.jobManager.RetryJob(id)
	if err != nil {
		return nil, err
	}
	d.queue <- id
	return job, nil
}

// RetryFailedDownloads re-queues every failed download job and wakes the
// dispatcher for each of them. It returns the number of retried jobs.
func (d *Downloader) RetryFailedDownloads() (int, error) {
	retried, err := d.jobManager.RetryFailedDownloads()
	if err != nil {
		return 0, err
	}
	for _, j := range retried {
		d.queue <- j.ID
	}
	return len(retried), nil
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
		if d.deferredRetry(jobID) {
			continue
		}
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

// deferredRetry reports whether the queued job is an automatic retry whose
// next_retry_at is still in the future. When it is, the job is not run
// now: it is re-queued with time.AfterFunc once the deadline passes, and
// the dispatcher moves on.
func (d *Downloader) deferredRetry(jobID string) bool {
	job, err := d.jobManager.GetJob(jobID)
	if err != nil || job == nil || job.Status != jobs.StatusQueued || !job.NextRetryAt.Valid {
		return false
	}
	if !job.NextRetryAt.Time.After(d.now()) {
		return false
	}
	d.scheduleRequeue(jobID, time.Until(job.NextRetryAt.Time))
	return true
}

// scheduleRequeue pushes jobID back onto the queue after delay. The queue
// buffer absorbs the send; a stuck send only happens if the buffer is full
// of 5000 pending ids.
func (d *Downloader) scheduleRequeue(jobID string, delay time.Duration) {
	if delay < 0 {
		delay = 0
	}
	time.AfterFunc(delay, func() {
		d.queue <- jobID
	})
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
		d.failOrRetry(job, errStr)
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

// failOrRetry handles a yt-dlp failure: a transient failure with retry
// budget left is scheduled for an automatic retry (status queued,
// next_retry_at armed, attempts incremented); everything else is marked
// failed with the truncated stderr.
func (d *Downloader) failOrRetry(job *jobs.Job, stderr string) {
	if d.maxRetries > 0 && job.Attempts < d.maxRetries && isTransient(stderr) {
		next := job.Attempts + 1
		nextAt := d.now().Add(retryBackoff(next))
		errMsg := fmt.Sprintf("retrying (%d/%d): %s", next, d.maxRetries, stderr)
		if err := d.jobManager.ScheduleRetry(job.ID, next, nextAt, errMsg); err != nil {
			log.Printf("download: schedule retry for job %s: %v", job.ID, err)
			d.setStatus(job.ID, jobs.StatusFailed, "", stderr)
			return
		}
		d.queue <- job.ID
		return
	}
	d.setStatus(job.ID, jobs.StatusFailed, "", stderr)
}

// transientTextMarkers, permanentTextMarkers and the two HTTP-code
// regular expressions are the classifier rules for yt-dlp stderr.
var (
	transientTextMarkers = []string{
		"timed out",
		"Connection reset",
		"Temporary failure in name resolution",
		"Unable to download webpage",
		"IncompleteRead",
	}
	permanentTextMarkers = []string{
		"Video unavailable",
		"Private video",
		"members-only",
		"copyright",
	}
	transientHTTP = regexp.MustCompile(`HTTP Error (5\d{2}|429)`)
	permanentHTTP = regexp.MustCompile(`HTTP Error (403|404)`)
)

// isTransient classifies yt-dlp stderr: true when the failure looks
// temporary (network errors or HTTP 5xx/429) and a retry may succeed.
// Permanent failures (unavailable, private, members-only or copyrighted
// videos, HTTP 403/404) and anything unrecognised are permanent. A
// permanent marker overrides a transient one in the same message (e.g.
// "Unable to download webpage: HTTP Error 404" is permanent).
func isTransient(stderr string) bool {
	for _, m := range permanentTextMarkers {
		if strings.Contains(stderr, m) {
			return false
		}
	}
	if permanentHTTP.MatchString(stderr) {
		return false
	}
	if transientHTTP.MatchString(stderr) {
		return true
	}
	for _, m := range transientTextMarkers {
		if strings.Contains(stderr, m) {
			return true
		}
	}
	return false
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
