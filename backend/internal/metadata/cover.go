package metadata

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"yt-scraper/internal/jobs"
)

const (
	// MaxCoverBytes is the maximum accepted cover size (10 MB).
	MaxCoverBytes = 10 << 20
	// MaxCoverDim is the maximum accepted image dimension.
	MaxCoverDim = 4000
)

// allowedCoverHosts are the hosts cover/from-url may fetch from.
// archive.org subdomains (*.archive.org) are additionally allowed.
var allowedCoverHosts = map[string]bool{
	"coverartarchive.org": true,
	"archive.org":         true,
}

var errCoverTooLarge = errors.New("cover exceeds the 10 MB limit")

// validateCoverURL applies the cover download policy to one URL: https
// only, allowlisted hosts.
func validateCoverURL(u *url.URL) error {
	if u.Scheme != "https" {
		return errors.New("only https URLs are allowed for cover art")
	}
	host := strings.ToLower(u.Hostname())
	if !allowedCoverHosts[host] && !strings.HasSuffix(host, ".archive.org") {
		return fmt.Errorf("host %q is not in the cover art allowlist", host)
	}
	return nil
}

// coverRedirectPolicy re-checks the allowlist on every redirect hop and
// caps the redirect chain at three hops.
func coverRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= 4 {
		return errors.New("too many redirects (3 max)")
	}
	return validateCoverURL(req.URL)
}

// newCoverClient builds the production HTTP client for cover downloads.
func newCoverClient() *http.Client {
	return &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: coverRedirectPolicy,
	}
}

// validateCoverData checks the sniffed content type and the decoded
// dimensions. It returns the content type on success.
func validateCoverData(data []byte) (string, error) {
	mime := http.DetectContentType(data)
	if mime != "image/jpeg" && mime != "image/png" {
		return "", fmt.Errorf("unsupported content type %s (jpeg and png only)", mime)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("invalid image: %w", err)
	}
	if cfg.Width > MaxCoverDim || cfg.Height > MaxCoverDim {
		return "", fmt.Errorf("image is %dx%d, maximum is %dx%d", cfg.Width, cfg.Height, MaxCoverDim, MaxCoverDim)
	}
	return mime, nil
}

func readCoverBody(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxCoverBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxCoverBytes {
		return nil, errCoverTooLarge
	}
	return data, nil
}

// UploadCover validates and stores an uploaded cover for the album, then
// queues a job that embeds the image into every track of the album.
func (s *Service) UploadCover(albumID string, body io.Reader) (string, string, error) {
	album, err := s.lib.GetAlbum(albumID)
	if err != nil {
		return "", "", &NotFoundError{Message: "album not found"}
	}

	data, err := readCoverBody(body)
	if err != nil {
		if errors.Is(err, errCoverTooLarge) {
			return "", "", &ValidationError{Message: errCoverTooLarge.Error()}
		}
		return "", "", err
	}
	mime, err := validateCoverData(data)
	if err != nil {
		return "", "", &ValidationError{Message: err.Error()}
	}

	ext := "jpg"
	if mime == "image/png" {
		ext = "png"
	}
	coverURL, err := s.lib.SaveCoverArt(album, data, ext)
	if err != nil {
		return "", "", err
	}

	return s.queueCoverEmbedJob(albumID, coverURL)
}

// ExtractCoverArt saves the cover embedded in the first track that has
// one as the album cover.
func (s *Service) ExtractCoverArt(albumID string) (string, error) {
	album, err := s.lib.GetAlbum(albumID)
	if err != nil {
		return "", &NotFoundError{Message: "album not found"}
	}
	tracks, err := s.lib.GetTracksByAlbum(albumID)
	if err != nil {
		return "", err
	}

	for _, track := range tracks {
		tmp, err := os.CreateTemp("", "cover-extract-*")
		if err != nil {
			return "", err
		}
		tmpName := tmp.Name()
		if cerr := tmp.Close(); cerr != nil {
			return "", cerr
		}
		defer func() {
			if rerr := os.Remove(tmpName); rerr != nil {
				log.Printf("metadata: remove temp cover %s: %v", tmpName, rerr)
			}
		}()

		ok, err := s.tags.ExtractCover(track.Path, tmpName)
		if err != nil || !ok {
			continue
		}
		//nolint:gosec // G304 - tmpName is a fresh os.CreateTemp path
		data, err := os.ReadFile(tmpName)
		if err != nil {
			return "", err
		}
		mime, err := validateCoverData(data)
		if err != nil {
			// Not a storable image type: try the next track.
			log.Printf("metadata: embedded cover of %s is %s, skipping", track.Path, http.DetectContentType(data))
			continue
		}
		ext := "jpg"
		if mime == "image/png" {
			ext = "png"
		}
		return s.lib.SaveCoverArt(album, data, ext)
	}
	return "", &NotFoundError{Message: "no track has embedded cover art"}
}

// CoverFromURL downloads a cover from an allowlisted URL and stores it for
// the album, then embeds it into every track.
func (s *Service) CoverFromURL(albumID, rawURL string) (string, string, error) {
	album, err := s.lib.GetAlbum(albumID)
	if err != nil {
		return "", "", &NotFoundError{Message: "album not found"}
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", &ValidationError{Message: "invalid url"}
	}
	if err := validateCoverURL(u); err != nil {
		return "", "", &ValidationError{Message: err.Error()}
	}

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return "", "", &ValidationError{Message: "invalid url"}
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", "", &ValidationError{Message: "cover download failed: " + err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", "", &ValidationError{Message: fmt.Sprintf("cover download failed: status %d", resp.StatusCode)}
	}

	data, err := readCoverBody(resp.Body)
	if err != nil {
		if errors.Is(err, errCoverTooLarge) {
			return "", "", &ValidationError{Message: errCoverTooLarge.Error()}
		}
		return "", "", err
	}
	mime, err := validateCoverData(data)
	if err != nil {
		return "", "", &ValidationError{Message: err.Error()}
	}

	ext := "jpg"
	if mime == "image/png" {
		ext = "png"
	}
	coverURL, err := s.lib.SaveCoverArt(album, data, ext)
	if err != nil {
		return "", "", err
	}

	return s.queueCoverEmbedJob(albumID, coverURL)
}

// queueCoverEmbedJob creates and starts the job that embeds the album
// cover into every track.
func (s *Service) queueCoverEmbedJob(albumID, coverURL string) (string, string, error) {
	job, err := s.jm.CreateJob(jobs.TypeMetadata, map[string]string{
		"action": "embed_cover",
		"album":  albumID,
		"cover":  coverURL,
	})
	if err != nil {
		return "", "", err
	}

	go s.runEmbedCoverJob(job.ID, albumID)
	return job.ID, coverURL, nil
}

// runEmbedCoverJob embeds the album cover into every track. The cover file
// is re-resolved from the album row because the job may run long after the
// upload response was written.
func (s *Service) runEmbedCoverJob(jobID, albumID string) {
	coverPath, ok, err := s.lib.CoverFilePath(albumID)
	if err != nil || !ok {
		log.Printf("metadata: resolve cover for %s: %v", albumID, err)
		markJobFailed(s, jobID, "cannot resolve album cover")
		return
	}

	tracks, err := s.lib.GetTracksByAlbum(albumID)
	if err != nil {
		markJobFailed(s, jobID, err.Error())
		return
	}
	total := strconv.Itoa(len(tracks))
	if err := s.jm.UpdateJobStatus(jobID, jobs.StatusRunning, "0/"+total, ""); err != nil {
		log.Printf("metadata: update job %s: %v", jobID, err)
	}

	failures := 0
	for i, track := range tracks {
		if err := s.tags.EmbedCover(track.Path, coverPath); err != nil {
			log.Printf("metadata: embed cover into %s: %v", track.Path, err)
			failures++
			continue
		}
		if err := s.jm.UpdateJobStatus(jobID, jobs.StatusRunning, strconv.Itoa(i+1)+"/"+total, ""); err != nil {
			log.Printf("metadata: update job %s: %v", jobID, err)
		}
	}

	if failures > 0 {
		markJobFailed(s, jobID, strconv.Itoa(failures)+" tracks failed")
		return
	}
	if err := s.jm.UpdateJobStatus(jobID, jobs.StatusCompleted, "100%", ""); err != nil {
		log.Printf("metadata: update job %s: %v", jobID, err)
	}
}

func markJobFailed(s *Service, jobID, message string) {
	if err := s.jm.UpdateJobStatus(jobID, jobs.StatusFailed, "", message); err != nil {
		log.Printf("metadata: update job %s: %v", jobID, err)
	}
}
