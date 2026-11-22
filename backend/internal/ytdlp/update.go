package ytdlp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"yt-scraper/internal/jobs"
)

// Errors surfaced by the update pipeline.
var (
	// ErrUpdateRunning is returned when an update is already in flight;
	// Rollback returns it too, because it shares the same update slot.
	ErrUpdateRunning = errors.New("an update is already running")
	// ErrUpToDate is returned when no newer release is known.
	ErrUpToDate = errors.New("already up to date")
	// ErrNoRollback is returned when there is no yt-dlp.prev to restore.
	ErrNoRollback = errors.New("no previous binary to roll back to")
)

// UpdateOnce performs one full check-and-install cycle:
//
//  1. query the channel's releases/latest endpoint for the tag,
//  2. download the binary and SHA2-256SUMS to a temp file in the binary
//     dir and verify the SHA-256 (aborting on mismatch or missing entry),
//  3. smoke-test the temp binary with --version (10s timeout),
//  4. pause the dispatcher and wait up to idleTimeout for running
//     downloads, then resume,
//  5. swap in atomically, parking the previous binary as yt-dlp.prev.
//
// Every failure leaves the previous binary in place. It returns a short
// human-readable result for logging and the last_result setting.
func (m *Manager) UpdateOnce(ctx context.Context, force bool) (string, error) {
	st, err := m.GetSettings()
	if err != nil {
		return "", err
	}
	latest, err := m.fetchLatestTag(ctx, st.Channel)
	if err != nil {
		m.storeResult("", "error: "+err.Error())
		return "", fmt.Errorf("check latest version: %w", err)
	}
	m.storeResult(latest, "checked "+latest)

	current := m.Version()
	if !force && !IsNewer(current, latest) {
		m.storeResult(latest, "up to date ("+latest+")")
		return "up to date: " + latest, ErrUpToDate
	}

	if err := m.install(ctx, st.Channel); err != nil {
		m.storeResult(latest, "error: "+err.Error())
		return "", err
	}
	m.storeResult(latest, "updated to "+latest)
	return "updated to " + latest, nil
}

// StartManualUpdate creates a ytdlp_update job and runs the update in the
// background. It always enqueues the job (the API answers 202) and only
// returns ErrUpdateRunning when an update is already in flight; when the
// binary in use is already the known latest release and force is false,
// the job simply completes with an "up to date" result instead of
// downloading.
func (m *Manager) StartManualUpdate(ctx context.Context, force bool) (*jobs.Job, error) {
	if !m.beginUpdate() {
		return nil, ErrUpdateRunning
	}
	st, err := m.GetSettings()
	if err != nil {
		m.endUpdate()
		return nil, err
	}
	job, err := m.jm.CreateJob(jobs.TypeYTDLPUpdate, map[string]string{
		"channel": st.Channel,
		"reason":  "manual",
	})
	if err != nil {
		m.endUpdate()
		return nil, err
	}
	// The work outlives the HTTP request, so detach from its context.
	bg := context.WithoutCancel(ctx)
	go func() {
		defer m.endUpdate()
		m.setJob(job.ID, jobs.StatusRunning, "checking for updates", "")
		res, err := m.UpdateOnce(bg, force)
		switch {
		case err == nil || errors.Is(err, ErrUpToDate):
			// The job's progress carries the human-readable result
			// ("updated to X" or "up to date: X") on the Jobs page.
			m.setJob(job.ID, jobs.StatusCompleted, res, "")
			log.Printf("[ytdlp] manual update: %s", res)
		default:
			m.setJob(job.ID, jobs.StatusFailed, "", err.Error())
			log.Printf("[ytdlp] manual update failed: %v", err)
		}
	}()
	return job, nil
}

func (m *Manager) setJob(id string, status jobs.JobStatus, progress, errMsg string) {
	if m.jm == nil || id == "" {
		return
	}
	if err := m.jm.UpdateJobStatus(id, status, progress, errMsg); err != nil {
		log.Printf("[ytdlp] update job %s to %s: %v", id, status, err)
	}
}

// install downloads, verifies and swaps in the binary for the channel.
func (m *Manager) install(ctx context.Context, channel string) error {
	urls, ok := m.channels[channel]
	if !ok {
		return fmt.Errorf("unknown channel %q", channel)
	}

	if err := os.MkdirAll(m.binDir, 0o700); err != nil {
		return fmt.Errorf("create binary dir: %w", err)
	}
	tmp, err := os.CreateTemp(m.binDir, binName+".*.new")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	if err := tmp.Close(); err != nil {
		return err
	}
	// no-op after a successful swap (the file was renamed away)
	defer func() { _ = os.Remove(tmpName) }()

	if err := m.downloadFile(ctx, urls.Binary, tmpName); err != nil {
		return fmt.Errorf("download %w", err)
	}
	sumsData, err := m.downloadData(ctx, urls.Sums)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	sums, err := parseSHA256Sums(sumsData)
	if err != nil {
		return err
	}
	want, ok := sums[binName]
	if !ok {
		return ErrChecksumMissing
	}
	if err := verifySHA256(tmpName, want); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil { //nolint:gosec // G302 - the yt-dlp binary must be executable
		return fmt.Errorf("chmod new %w", err)
	}
	version, err := m.smokeTest(tmpName)
	if err != nil {
		return err
	}

	if err := m.quiesce(); err != nil {
		return err
	}
	if err := m.swap(tmpName); err != nil {
		return err
	}
	log.Printf("[ytdlp] installed %s from the %s channel", version, channel)
	return nil
}

// smokeTest runs <path> --version with a 10s timeout and requires a
// non-empty version string.
func (m *Manager) smokeTest(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), smokeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output() //nolint:gosec // G204 - path is the just-downloaded temp file inside the managed bin dir
	if err != nil {
		return "", fmt.Errorf("smoke test of new %w", err)
	}
	version := strings.TrimSpace(string(out))
	if version == "" {
		return "", errors.New("smoke test: the new binary printed no version")
	}
	return version, nil
}

// quiesce pauses the dispatcher, waits up to idleTimeout for running
// downloads and resumes, so the swap never happens mid-download.
func (m *Manager) quiesce() error {
	if m.gate == nil {
		return nil
	}
	m.gate.Pause()
	defer m.gate.Resume()
	if err := m.gate.WaitForIdle(m.idleTimeout); err != nil {
		return fmt.Errorf("wait for running downloads: %w", err)
	}
	return nil
}

// swap moves tmp into place atomically, parking the current binary as
// yt-dlp.prev for rollback. A failed rename restores the previous binary.
func (m *Manager) swap(tmp string) error {
	bin := m.BinPath()
	prev := m.prevPath()

	if m.HasBinary() {
		if err := os.Rename(bin, prev); err != nil {
			return fmt.Errorf("park previous %w", err)
		}
	}
	if err := os.Rename(tmp, bin); err != nil {
		if _, statErr := os.Stat(prev); statErr == nil {
			if rbErr := os.Rename(prev, bin); rbErr != nil {
				log.Printf("[ytdlp] restore previous binary after failed swap: %v", rbErr)
			}
		}
		return fmt.Errorf("swap in new %w", err)
	}
	return nil
}

// Rollback swaps the parked yt-dlp.prev back into place, discarding the
// current binary. It returns ErrNoRollback when no previous binary exists
// and ErrUpdateRunning when an update (or another rollback) is in flight.
//
// Rollback shares the single update slot with updates, so the two are
// mutually exclusive: a rollback racing an in-flight update could hit the
// swap window or be clobbered by it, and the brief's guarantee is that a
// failure never breaks downloads because the old binary stays. It is
// refused rather than blocked because an update can hold the slot for up
// to the idle timeout (10 minutes) waiting for running downloads; the
// API answers 409 and the admin can retry once the update is done.
//
// The remove/rename window below is quiesced exactly like an update, so
// no download starts or runs while no binary exists at all.
func (m *Manager) Rollback() error {
	if !m.beginUpdate() {
		return ErrUpdateRunning
	}
	defer m.endUpdate()

	bin := m.BinPath()
	prev := m.prevPath()
	if _, err := os.Stat(prev); err != nil {
		return ErrNoRollback
	}
	if err := m.quiesce(); err != nil {
		return err
	}
	if m.HasBinary() {
		if err := os.Remove(bin); err != nil {
			return fmt.Errorf("remove current %w", err)
		}
	}
	if err := os.Rename(prev, bin); err != nil {
		return fmt.Errorf("restore previous %w", err)
	}
	log.Printf("[ytdlp] rolled back to the previous binary")
	return nil
}

// ---------------------------------------------------------------------
// Downloading (fixed allowlist URLs only)
// ---------------------------------------------------------------------

// fetchLatestTag queries the channel's GitHub releases/latest endpoint and
// returns its tag_name.
func (m *Manager) fetchLatestTag(ctx context.Context, channel string) (string, error) {
	urls, ok := m.channels[channel]
	if !ok {
		return "", fmt.Errorf("unknown channel %q", channel)
	}
	data, err := m.downloadData(ctx, urls.API)
	if err != nil {
		return "", err
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(data, &rel); err != nil {
		return "", fmt.Errorf("parse release: %w", err)
	}
	if rel.TagName == "" {
		return "", errors.New("release has no tag_name")
	}
	return rel.TagName, nil
}

// downloadFile streams url to path (created fresh), rejecting anything
// over maxDownloadBytes.
func (m *Manager) downloadFile(ctx context.Context, url, path string) error {
	res, err := m.get(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	f, err := os.Create(path) //nolint:gosec // G304 - path is the temp file created in the managed bin dir
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	n, err := io.Copy(f, io.LimitReader(res.Body, maxDownloadBytes+1))
	if err != nil {
		return err
	}
	if n > maxDownloadBytes {
		return fmt.Errorf("download exceeds %d bytes", maxDownloadBytes)
	}
	return nil
}

// downloadData fetches url into memory (bounded by maxDownloadBytes).
func (m *Manager) downloadData(ctx context.Context, url string) ([]byte, error) {
	res, err := m.get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	return io.ReadAll(io.LimitReader(res.Body, maxDownloadBytes))
}

// get performs the GET and checks for a 200 status.
func (m *Manager) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return nil, fmt.Errorf("unexpected status %d for %s", res.StatusCode, url)
	}
	return res, nil
}
