package download

import (
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
