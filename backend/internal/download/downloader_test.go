package download

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"yt-scraper/internal/db"
	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
	"yt-scraper/internal/scanner"
)

// newTestDownloader builds a Downloader (no dispatcher goroutine) backed by
// a temp-dir SQLite database and music dir.
func newTestDownloader(t *testing.T) (*Downloader, *jobs.Manager, *db.DB, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.NewDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	libService := library.NewService(database, dir)
	jm := jobs.NewManager(database)
	sc := scanner.NewScanner(libService, jm, dir)
	d := &Downloader{
		jobManager:      jm,
		scanner:         sc,
		downloadDir:     dir,
		queue:           make(chan string, 5000),
		concurrentLimit: 2,
		ytDlpPath:       "yt-dlp-fake",
		// maxRetries stays 0 (automatic retry disabled) unless a test
		// opts in; now is the real clock unless a test injects one.
		now: time.Now,
	}
	d.cond = sync.NewCond(&d.mu)
	return d, jm, database, dir
}

// writeFakeYtDlp writes a stand-in yt-dlp script into dir. In success mode
// it honours --output (replacing the %(ext)s token) and creates the file;
// in failure mode it prints an error and exits 1.
func writeFakeYtDlp(t *testing.T, dir, name, mode string) string {
	t.Helper()
	var body string
	switch mode {
	case "success":
		body = `#!/bin/sh
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    --output) out="$2"; shift 2;;
    *) shift;;
  esac
done
tok='%(ext)s'
out="${out%"$tok"*}flac${out#*"$tok"}"
mkdir -p "$(dirname "$out")"
: > "$out"
`
	case "failure":
		body = "#!/bin/sh\necho 'boom: download exploded' >&2\nexit 1\n"
	default:
		t.Fatalf("unknown fake mode %q", mode)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil { //nolint:gosec // G306 - the fake yt-dlp must be executable
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	return path
}

func TestBuildArgs(t *testing.T) {
	tests := []struct {
		name        string
		details     Details
		outTemplate string
		proxy       string
		want        []string
	}{
		{
			name: "no proxy",
			details: Details{
				VideoID: "dQw4w9WgXcQ",
				Artist:  "Artist",
				Album:   "Album",
			},
			outTemplate: "/music/Artist/Album/Title.flac",
			want: []string{
				"https://youtube.com/watch?v=dQw4w9WgXcQ",
				"--extract-audio",
				"--audio-format", "flac",
				"--embed-thumbnail",
				"--embed-metadata",
				"--parse-metadata", "title:%(artist)s",
				"--replace-in-metadata", "artist", "^.*$", "Artist",
				"--parse-metadata", "title:%(album)s",
				"--replace-in-metadata", "album", "^.*$", "Album",
				"--js-runtimes", "node",
				"--output", "/music/Artist/Album/Title.flac",
				"--newline",
			},
		},
		{
			name: "with proxy",
			details: Details{
				VideoID: "abc123",
				Artist:  "A",
				Album:   "B",
			},
			outTemplate: "/music/A/B/T.flac",
			proxy:       "http://proxy:8080",
			want: []string{
				"https://youtube.com/watch?v=abc123",
				"--extract-audio",
				"--audio-format", "flac",
				"--embed-thumbnail",
				"--embed-metadata",
				"--parse-metadata", "title:%(artist)s",
				"--replace-in-metadata", "artist", "^.*$", "A",
				"--parse-metadata", "title:%(album)s",
				"--replace-in-metadata", "album", "^.*$", "B",
				"--js-runtimes", "node",
				"--output", "/music/A/B/T.flac",
				"--newline",
				"--proxy", "http://proxy:8080",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildArgs(tt.details, tt.outTemplate, tt.proxy)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("buildArgs() =\n%v\nwant:\n%v", got, tt.want)
			}
		})
	}
}

func TestBuildArgsUserValuesStayOptionValues(t *testing.T) {
	// Values that look like options must only ever appear as the value of
	// a named option (the argument after the option's field/regex pair),
	// never as a bare option.
	details := Details{
		VideoID: "dQw4w9WgXcQ",
		Artist:  "-fake-option",
		Album:   "--evil",
	}
	got := buildArgs(details, "/music/x.flac", "")
	for i, arg := range got {
		if arg == "--evil" || arg == "-fake-option" {
			// Both user values sit after a 3-argument option:
			// --replace-in-metadata <field> <regex> <value>
			if i < 3 || got[i-3] != "--replace-in-metadata" {
				t.Fatalf("user value %q at arg %d not in a named-option slot: %v", arg, i, got)
			}
		}
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"plain title", "plain title"},
		{"a/b", "a-b"},
		{`a\b`, "a-b"},
		{"a:b", "a-b"},
		{`a*b?c"d`, "a-b-c-d"},
		{`a<b>c|d`, "a-b-c-d"},
		{"", ""},
		{"Ünïcödé 文件", "Ünïcödé 文件"},
	}
	for _, tt := range tests {
		if got := sanitize(tt.in); got != tt.want {
			t.Fatalf("sanitize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestProcessDownloadSuccess(t *testing.T) {
	d, jm, _, dir := newTestDownloader(t)
	d.ytDlpPath = writeFakeYtDlp(t, dir, "yt-dlp-fake", "success")

	job, err := jm.CreateJob(jobs.TypeDownload, Details{
		VideoID: "dQw4w9WgXcQ",
		Title:   "Cool Song",
		Artist:  "Artist",
		Album:   "Album",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	d.processDownload(job.ID)

	got, err := jm.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != jobs.StatusCompleted {
		t.Fatalf("status = %s, want completed (error: %s)", got.Status, got.Error)
	}
	if got.Progress != "100%" {
		t.Fatalf("progress = %q, want 100%%", got.Progress)
	}
	wantFile := filepath.Join(dir, "Artist", "Album", "Cool Song.flac")
	if _, err := os.Stat(wantFile); err != nil {
		t.Fatalf("downloaded file missing: %v", err)
	}
}

func TestProcessDownloadFailure(t *testing.T) {
	d, jm, _, dir := newTestDownloader(t)
	d.ytDlpPath = writeFakeYtDlp(t, dir, "yt-dlp-fake", "failure")

	job, err := jm.CreateJob(jobs.TypeDownload, Details{
		VideoID: "dQw4w9WgXcQ",
		Title:   "Cool Song",
		Artist:  "Artist",
		Album:   "Album",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	d.processDownload(job.ID)

	got, err := jm.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != jobs.StatusFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "boom") {
		t.Fatalf("error = %q, want it to contain the yt-dlp output", got.Error)
	}
}

// TestProcessDownloadMkdirFailure is a regression test for the MkdirAll
// error handling: when the output directory cannot be created (here because
// the artist path is a regular file), the job must fail immediately with a
// clear message instead of dying inside yt-dlp.
func TestProcessDownloadMkdirFailure(t *testing.T) {
	d, jm, _, dir := newTestDownloader(t)

	// Block the artist directory with a regular file.
	if err := os.WriteFile(filepath.Join(dir, "Artist"), []byte("blocker"), 0o600); err != nil {
		t.Fatalf("block artist path: %v", err)
	}

	job, err := jm.CreateJob(jobs.TypeDownload, Details{
		VideoID: "dQw4w9WgXcQ",
		Title:   "Cool Song",
		Artist:  "Artist",
		Album:   "Album",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	d.processDownload(job.ID)

	got, err := jm.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != jobs.StatusFailed {
		t.Fatalf("status = %s, want failed (error: %s)", got.Status, got.Error)
	}
	const wantPrefix = "failed to create output directory: "
	if !strings.HasPrefix(got.Error, wantPrefix) || got.Error == wantPrefix {
		t.Fatalf("error = %q, want prefix %q with the underlying cause", got.Error, wantPrefix)
	}
}

func TestProcessDownloadInvalidDetails(t *testing.T) {
	d, jm, database, _ := newTestDownloader(t)

	job, err := jm.CreateJob(jobs.TypeDownload, Details{VideoID: "x"})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	// Corrupt the stored details so parseDetails fails.
	if _, err := database.Exec("UPDATE jobs SET details = 'not json' WHERE id = ?", job.ID); err != nil {
		t.Fatalf("corrupt details: %v", err)
	}

	d.processDownload(job.ID)

	got, err := jm.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != jobs.StatusFailed {
		t.Fatalf("status = %s, want failed", got.Status)
	}
	if got.Error != "Invalid job details" {
		t.Fatalf("error = %q, want %q", got.Error, "Invalid job details")
	}
}

// writeFailingYtDlp writes a stand-in yt-dlp that prints the given stderr
// and exits 1.
func writeFailingYtDlp(t *testing.T, dir, name, stderr string) string {
	t.Helper()
	sq := strings.ReplaceAll(stderr, "'", `'\''`)
	body := "#!/bin/sh\nprintf '%s\\n' '" + sq + "' >&2\nexit 1\n"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil { //nolint:gosec // G306 - the fake yt-dlp must be executable
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	return path
}

// writeMarkerYtDlp writes a successful stand-in yt-dlp that also touches
// marker, so tests can detect whether it was executed.
func writeMarkerYtDlp(t *testing.T, dir, name, marker string) string {
	t.Helper()
	body := "#!/bin/sh\n: > " + marker + "\n" + `
out=""
while [ $# -gt 0 ]; do
  case "$1" in
    --output) out="$2"; shift 2;;
    *) shift;;
  esac
done
tok='%(ext)s'
out="${out%"$tok"*}flac${out#*"$tok"}"
mkdir -p "$(dirname "$out")"
: > "$out"
`
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil { //nolint:gosec // G306 - the fake yt-dlp must be executable
		t.Fatalf("write fake yt-dlp: %v", err)
	}
	return path
}

func TestIsTransient(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   bool
	}{
		{"empty", "", false},
		{"unrecognised", "boom: download exploded", false},
		{"http 500", "ERROR: [youtube] x: HTTP Error 500: Internal Server Error", true},
		{"http 503", "HTTP Error 503: Service Unavailable", true},
		{"http 599", "ERROR: HTTP Error 599", true},
		{"http 429", "ERROR: HTTP Error 429: Too Many Requests", true},
		{"timed out", "ERROR: [youtube] x: Read timed out.", true},
		{"connection reset", "ERROR: Connection reset by peer", true},
		{"dns failure", "Temporary failure in name resolution", true},
		{"unable to download webpage 5xx", "ERROR: Unable to download webpage: HTTP Error 503", true},
		{"incomplete read", "urllib3.exceptions.IncompleteRead: IncompleteRead(0 bytes read)", true},
		{"video unavailable", "ERROR: [youtube] x: Video unavailable", false},
		{"private video", "ERROR: [youtube] x: Private video", false},
		{"members only", "ERROR: [youtube] x: This is a (members-only) video", false},
		{"copyright", "ERROR: [youtube] x: copyright strike", false},
		{"http 404", "ERROR: HTTP Error 404: Not Found", false},
		{"http 403", "ERROR: unable to download video data: HTTP Error 403", false},
		{"webpage 404 is permanent", "ERROR: Unable to download webpage: HTTP Error 404: Not Found", false},
		{"429 plus permanent marker", "ERROR: Private video; timed out", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransient(tt.stderr); got != tt.want {
				t.Fatalf("isTransient(%q) = %v, want %v", tt.stderr, got, tt.want)
			}
		})
	}
}

func TestRetryBackoffSchedule(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 30 * time.Second}, // clamped to 1
		{1, 30 * time.Second},
		{2, 2 * time.Minute},
		{3, 10 * time.Minute},
		{4, 10 * time.Minute}, // capped
		{100, 10 * time.Minute},
	}
	for _, tt := range tests {
		if got := retryBackoff(tt.attempt); got != tt.want {
			t.Fatalf("retryBackoff(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestMaxRetriesFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  string
		set  bool
		want int
	}{
		{"unset defaults to 2", "", false, 2},
		{"explicit zero disables", "0", true, 0},
		{"explicit five", "5", true, 5},
		{"invalid falls back", "abc", true, DefaultMaxRetries},
		{"negative falls back", "-3", true, DefaultMaxRetries},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("DOWNLOAD_MAX_RETRIES", tt.env)
			} else {
				t.Setenv("DOWNLOAD_MAX_RETRIES", "")
			}
			if got := MaxRetriesFromEnv(); got != tt.want {
				t.Fatalf("MaxRetriesFromEnv() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestAutoRetryBackoffSchedule drives a transient yt-dlp failure through
// the whole automatic-retry loop with an injected clock: each failure
// bumps attempts, arms the next backoff step and re-queues; once the
// budget is spent the job fails for good.
func TestAutoRetryBackoffSchedule(t *testing.T) {
	d, jm, _, dir := newTestDownloader(t)
	d.maxRetries = 2
	base := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return base }
	d.ytDlpPath = writeFailingYtDlp(t, dir, "yt-dlp-fake", "HTTP Error 503: Service Unavailable")

	job, err := jm.CreateJob(jobs.TypeDownload, Details{
		VideoID: "dQw4w9WgXcQ", Title: "Cool Song", Artist: "Artist", Album: "Album",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	const stderr = "HTTP Error 503: Service Unavailable"
	// The fake yt-dlp terminates its output with a newline, which the
	// captured stderr keeps.
	const stderrStored = stderr + "\n"

	// First failure: attempt 1 armed, 30s backoff.
	d.processDownload(job.ID)
	got, err := jm.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != jobs.StatusQueued {
		t.Fatalf("after 1st failure: status = %s, want queued", got.Status)
	}
	if got.Attempts != 1 {
		t.Fatalf("after 1st failure: attempts = %d, want 1", got.Attempts)
	}
	if !got.NextRetryAt.Valid || !within(got.NextRetryAt.Time, base.Add(30*time.Second), time.Second) {
		t.Fatalf("after 1st failure: next_retry_at = %v, want ≈ %v", got.NextRetryAt, base.Add(30*time.Second))
	}
	if want := "retrying (1/2): " + stderrStored; got.Error != want {
		t.Fatalf("after 1st failure: error = %q, want %q", got.Error, want)
	}
	// The job must be pushed back onto the queue for the dispatcher.
	assertQueuedID(t, d, job.ID)

	// Second failure: attempt 2 armed, 2m backoff.
	d.processDownload(job.ID)
	got, err = jm.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != jobs.StatusQueued {
		t.Fatalf("after 2nd failure: status = %s, want queued", got.Status)
	}
	if got.Attempts != 2 {
		t.Fatalf("after 2nd failure: attempts = %d, want 2", got.Attempts)
	}
	if !got.NextRetryAt.Valid || !within(got.NextRetryAt.Time, base.Add(2*time.Minute), time.Second) {
		t.Fatalf("after 2nd failure: next_retry_at = %v, want ≈ %v", got.NextRetryAt, base.Add(2*time.Minute))
	}
	if want := "retrying (2/2): " + stderrStored; got.Error != want {
		t.Fatalf("after 2nd failure: error = %q, want %q", got.Error, want)
	}
	assertQueuedID(t, d, job.ID)

	// Third failure: budget exhausted, the job fails for good.
	d.processDownload(job.ID)
	got, err = jm.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != jobs.StatusFailed {
		t.Fatalf("after 3rd failure: status = %s, want failed", got.Status)
	}
	if got.Error != stderrStored {
		t.Fatalf("after 3rd failure: error = %q, want the raw stderr", got.Error)
	}
}

func TestAutoRetryDisabledWhenMaxIsZero(t *testing.T) {
	d, jm, _, dir := newTestDownloader(t)
	d.maxRetries = 0 // 0 disables automatic retry
	d.ytDlpPath = writeFailingYtDlp(t, dir, "yt-dlp-fake", "timed out")

	job, err := jm.CreateJob(jobs.TypeDownload, Details{VideoID: "x", Title: "T", Artist: "A", Album: "B"})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	d.processDownload(job.ID)

	got, err := jm.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != jobs.StatusFailed {
		t.Fatalf("status = %s, want failed (retry disabled)", got.Status)
	}
	if got.Attempts != 0 {
		t.Fatalf("attempts = %d, want 0", got.Attempts)
	}
	if got.NextRetryAt.Valid {
		t.Fatalf("next_retry_at = %v, want unset", got.NextRetryAt.Time)
	}
}

func TestAutoRetryPermanentFailureFailsImmediately(t *testing.T) {
	d, jm, _, dir := newTestDownloader(t)
	d.maxRetries = 2
	d.ytDlpPath = writeFailingYtDlp(t, dir, "yt-dlp-fake", "ERROR: [youtube] x: Video unavailable")

	job, err := jm.CreateJob(jobs.TypeDownload, Details{VideoID: "x", Title: "T", Artist: "A", Album: "B"})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	d.processDownload(job.ID)

	got, err := jm.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != jobs.StatusFailed {
		t.Fatalf("status = %s, want failed (permanent error)", got.Status)
	}
	if got.Attempts != 0 {
		t.Fatalf("attempts = %d, want 0", got.Attempts)
	}
}

func TestManualRetryWakesDispatcher(t *testing.T) {
	d, jm, _, _ := newTestDownloader(t)

	job := makeFailedDownload(t, jm, "HTTP Error 500")

	got, err := d.Retry(job.ID)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if got.Status != jobs.StatusQueued {
		t.Fatalf("status = %s, want queued", got.Status)
	}
	if got.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", got.Attempts)
	}
	if got.Error != "" || got.Progress != "" {
		t.Fatalf("error/progress = %q/%q, want cleared", got.Error, got.Progress)
	}
	assertQueuedID(t, d, job.ID)

	// Retrying the now-queued job is not allowed.
	if _, err := d.Retry(job.ID); !errors.Is(err, jobs.ErrNotRetryable) {
		t.Fatalf("second Retry err = %v, want ErrNotRetryable", err)
	}
}

// TestConcurrentRetriesEnqueueOnce proves two concurrent manual retries of
// the same failed job enqueue it exactly once: the loser of the atomic
// failed→queued transition errors out before it can push to the queue, so
// the dispatcher cannot spawn two yt-dlp processes on the same output
// template.
func TestConcurrentRetriesEnqueueOnce(t *testing.T) {
	d, jm, _, _ := newTestDownloader(t)
	job := makeFailedDownload(t, jm, "HTTP Error 500")

	type outcome struct {
		job *jobs.Job
		err error
	}
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, err := d.Retry(job.ID)
			outcomes <- outcome{job: j, err: err}
		}()
	}
	wg.Wait()
	close(outcomes)

	var winners, losers int
	for o := range outcomes {
		switch {
		case o.err == nil:
			winners++
		case errors.Is(o.err, jobs.ErrNotRetryable) || errors.Is(o.err, jobs.ErrJobNotFound):
			losers++
		default:
			t.Fatalf("unexpected Retry error: %v", o.err)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("winners/losers = %d/%d, want 1/1", winners, losers)
	}
	assertQueuedID(t, d, job.ID)

	// The loser must not have enqueued a second copy.
	select {
	case extra := <-d.queue:
		t.Fatalf("queue holds a second copy of job %s", extra)
	case <-time.After(200 * time.Millisecond):
	}

	got, err := jm.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (incremented exactly once)", got.Attempts)
	}
}

// TestDispatcherSkipsFutureNextRetryAt runs the real dispatcher: a queued
// job whose next_retry_at is in the future must not be processed before
// the deadline passes.
func TestDispatcherSkipsFutureNextRetryAt(t *testing.T) {
	d, jm, _, dir := newTestDownloader(t)
	marker := filepath.Join(dir, "yt-dlp-ran")
	d.ytDlpPath = writeMarkerYtDlp(t, dir, "yt-dlp-marker", marker)

	job, err := jm.CreateJob(jobs.TypeDownload, Details{VideoID: "x", Title: "T", Artist: "A", Album: "B"})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := jm.ScheduleRetry(job.ID, 1, time.Now().Add(time.Hour), "retrying (1/2): net err"); err != nil {
		t.Fatalf("ScheduleRetry: %v", err)
	}

	go d.dispatcher()
	d.queue <- job.ID
	time.Sleep(200 * time.Millisecond)

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("yt-dlp ran before next_retry_at passed")
	}
	got, err := jm.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != jobs.StatusQueued {
		t.Fatalf("status = %s, want queued (deferred past the deadline)", got.Status)
	}
}

func TestDeferredRetryDecision(t *testing.T) {
	d, jm, _, _ := newTestDownloader(t)

	job, err := jm.CreateJob(jobs.TypeDownload, Details{VideoID: "x"})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// No next_retry_at: run now.
	if d.deferredRetry(job.ID) {
		t.Fatal("queued job without next_retry_at must not be deferred")
	}

	// Future deadline: defer.
	if err := jm.ScheduleRetry(job.ID, 1, time.Now().Add(time.Hour), "x"); err != nil {
		t.Fatalf("ScheduleRetry: %v", err)
	}
	if !d.deferredRetry(job.ID) {
		t.Fatal("queued job with future next_retry_at must be deferred")
	}

	// Past deadline: run now.
	if err := jm.ScheduleRetry(job.ID, 1, time.Now().Add(-time.Hour), "x"); err != nil {
		t.Fatalf("ScheduleRetry: %v", err)
	}
	if d.deferredRetry(job.ID) {
		t.Fatal("queued job with past next_retry_at must not be deferred")
	}

	// Non-queued status: the dispatcher's own double-check governs.
	if err := jm.UpdateJobStatus(job.ID, jobs.StatusCompleted, "100%", ""); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}
	if d.deferredRetry(job.ID) {
		t.Fatal("completed job must not be deferred")
	}

	// Unknown id: not a deferral.
	if d.deferredRetry("no-such-job") {
		t.Fatal("unknown job must not be deferred")
	}
}

// helpers

func within(got, want time.Time, tol time.Duration) bool {
	d := got.Sub(want)
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func assertQueuedID(t *testing.T, d *Downloader, id string) {
	t.Helper()
	select {
	case got := <-d.queue:
		if got != id {
			t.Fatalf("queued id = %s, want %s", got, id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("job id was not pushed back onto the queue")
	}
}

func makeFailedDownload(t *testing.T, jm *jobs.Manager, errMsg string) *jobs.Job {
	t.Helper()
	job, err := jm.CreateJob(jobs.TypeDownload, Details{VideoID: "x", Title: "T", Artist: "A", Album: "B"})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := jm.UpdateJobStatus(job.ID, jobs.StatusFailed, "", errMsg); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}
	return job
}

func TestQueueDownloadAndLimits(t *testing.T) {
	d, _, _, _ := newTestDownloader(t)

	job, err := d.QueueDownload(Details{VideoID: "abc", Title: "T", Artist: "A", Album: "B"})
	if err != nil {
		t.Fatalf("QueueDownload: %v", err)
	}
	if job.Status != jobs.StatusQueued {
		t.Fatalf("status = %s, want queued", job.Status)
	}
	if !strings.Contains(job.Details, "abc") {
		t.Fatalf("details = %q, want the video id", job.Details)
	}

	d.SetLimit(4)
	if got := d.GetLimit(); got != 4 {
		t.Fatalf("limit = %d, want 4", got)
	}
	d.SetProxy("http://p:1")
	if got := d.GetProxy(); got != "http://p:1" {
		t.Fatalf("proxy = %q, want http://p:1", got)
	}

	// The queued id must land in the channel (BulkQueueDownloads pushes in
	// a goroutine; the buffer absorbs it).
	select {
	case id := <-d.queue:
		if id != job.ID {
			t.Fatalf("queued id = %s, want %s", id, job.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued id was not delivered")
	}
}

// newEnvTestDownloader builds a Downloader through the real NewDownloader
// (dispatcher included) backed by a temp-dir SQLite database and music dir,
// so CONCURRENT_DOWNLOAD is resolved exactly like in production.
func newEnvTestDownloader(t *testing.T) *Downloader {
	t.Helper()
	dir := t.TempDir()
	database, err := db.NewDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	libService := library.NewService(database, dir)
	jm := jobs.NewManager(database)
	sc := scanner.NewScanner(libService, jm, dir)
	return NewDownloader(jm, sc, dir)
}

// TestNewDownloaderConcurrentDownloadEnv is the regression test: before the
// fix NewDownloader hard-coded concurrentLimit: 2 and never read the
// CONCURRENT_DOWNLOAD env var, so this case failed.
func TestNewDownloaderConcurrentDownloadEnv(t *testing.T) {
	t.Setenv("CONCURRENT_DOWNLOAD", "5")

	d := newEnvTestDownloader(t)
	if got := d.GetLimit(); got != 5 {
		t.Fatalf("limit = %d, want 5 (CONCURRENT_DOWNLOAD=5)", got)
	}
}

func TestNewDownloaderConcurrentDownloadEnvInvalid(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{"empty", ""},
		{"not a number", "abc"},
		{"zero", "0"},
		{"negative", "-3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CONCURRENT_DOWNLOAD", tt.raw)

			d := newEnvTestDownloader(t)
			if got := d.GetLimit(); got != 2 {
				t.Fatalf("limit = %d, want default 2 for CONCURRENT_DOWNLOAD=%q", got, tt.raw)
			}
		})
	}
}

func TestNewDownloaderConcurrentDownloadEnvClamped(t *testing.T) {
	t.Setenv("CONCURRENT_DOWNLOAD", "999")

	d := newEnvTestDownloader(t)
	if got := d.GetLimit(); got != 32 {
		t.Fatalf("limit = %d, want clamped to 32", got)
	}
}
