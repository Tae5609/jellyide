package download

import (
	"encoding/json"
	"log"
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

func NewDownloader(jobManager *jobs.Manager, downloadDir string, scanner *scanner.Scanner) *Downloader {
	d := &Downloader{
		jobManager:      jobManager,
		scanner:         scanner,
		downloadDir:     downloadDir,
		queue:           make(chan string, 1000),
		concurrentLimit: 2,
		ytDlpPath:       "yt-dlp",
	}
	d.cond = sync.NewCond(&d.mu)

	// Recover queued jobs from DB on startup
	allJobs, err := jobManager.GetAllJobs()
	if err != nil {
		log.Printf("Failed to recover jobs: %v", err)
	} else {
		count := 0
		for _, j := range allJobs {
			if j.Type == jobs.TypeDownload && (j.Status == jobs.StatusQueued || j.Status == jobs.StatusDownloading) {
				// Re-queue
				jobManager.UpdateJobStatus(j.ID, jobs.StatusQueued, "0%", "")
				d.queue <- j.ID
				count++
			}
		}
		if count > 0 {
			log.Printf("Recovered %d pending download jobs from DB", count)
		}
	}

	go d.dispatcher()
	return d
}

func (d *Downloader) QueueDownload(details DownloadDetails) (*jobs.Job, error) {
	job, err := d.jobManager.CreateJob(jobs.TypeDownload, details)
	if err != nil {
		return nil, err
	}
	d.queue <- job.ID
	return job, nil
}

func (d *Downloader) BulkQueueDownloads(detailsList []DownloadDetails) ([]string, error) {
	var interfaces []interface{}
	for _, d := range detailsList {
		interfaces = append(interfaces, d)
	}

	createdJobs, err := d.jobManager.BulkCreateJobs(jobs.TypeDownload, interfaces)
	if err != nil {
		return nil, err
	}

	var jobIDs []string
	for _, job := range createdJobs {
		jobIDs = append(jobIDs, job.ID)
		d.queue <- job.ID
	}
	return jobIDs, nil
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
	log.Printf("Downloader dispatcher started")
	for jobID := range d.queue {
		log.Printf("Dispatcher picked up job: %s", jobID)
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
				log.Printf("Dispatcher released slot for job: %s", id)
			}()
			d.processDownload(id)
		}(jobID)
	}
}

func (d *Downloader) processDownload(jobID string) {
	log.Printf("Processing job: %s", jobID)
	job, err := d.jobManager.GetJob(jobID)
	if err != nil || job == nil {
		log.Printf("Job not found: %s", jobID)
		return
	}

	d.jobManager.UpdateJobStatus(jobID, jobs.StatusDownloading, "0%", "")

	var details DownloadDetails
	// We need to parse details from job.Details JSON
	// For simplicity, we just use a helper function to decode
	// Assuming details is JSON string

	// We will unmarshal properly, since we marshal in jobs package
	// details field is a JSON string

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
		
		// Automatically index the newly downloaded file
		downloadedFile := strings.Replace(outTemplate, "%(ext)s", "flac", 1)
		log.Printf("Downloader: Auto-indexing %s", downloadedFile)
		d.scanner.IndexFile(downloadedFile)
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
