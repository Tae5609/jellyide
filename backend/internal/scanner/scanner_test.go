package scanner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"yt-scraper/internal/db"
	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
)

// TestMainFFProbeParses runs parseFFProbe against the fixture files.
func TestParseFFProbe(t *testing.T) {
	base := "/music"

	tests := []struct {
		name    string
		fixture string
		path    string
		want    probeInfo
		wantErr bool
	}{
		{
			name:    "tagged",
			fixture: "testdata/tagged.json",
			path:    filepath.Join(base, "Artist", "Album", "01 Song.flac"),
			want: probeInfo{
				Title:    "Track One",
				Artist:   "Tagged Artist",
				Album:    "Tagged Album",
				Duration: 213,
				Bitrate:  1007354,
			},
		},
		{
			name:    "untagged falls back to file name and layout",
			fixture: "testdata/untagged.json",
			path:    filepath.Join(base, "Artist", "Album", "02 Untagged.flac"),
			want: probeInfo{
				Title:    "02 Untagged",
				Artist:   "Artist",
				Album:    "Album",
				Duration: 120,
				Bitrate:  1411000,
			},
		},
		{
			name:    "root level file with partial tags",
			fixture: "testdata/root.json",
			path:    filepath.Join(base, "root Song.flac"),
			want: probeInfo{
				Title:    "root Song",
				Artist:   "Only Artist",
				Album:    "Unknown Album",
				Duration: 0, // "not-a-number" does not parse
				Bitrate:  0, // empty string does not parse
			},
		},
		{
			name:    "flat layout uses the artist dir as album",
			fixture: "testdata/flat.json",
			path:    filepath.Join(base, "Artist", "03 Flat.flac"),
			want: probeInfo{
				Title:    "03 Flat",
				Artist:   "Artist",
				Album:    "Artist",
				Duration: 60,
				Bitrate:  700000,
			},
		},
		{
			name:    "invalid json",
			fixture: "testdata/invalid.json",
			path:    filepath.Join(base, "Artist", "Album", "x.flac"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(tt.fixture)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			got, err := parseFFProbe(data, tt.path, base)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseFFProbe = %+v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFFProbe: %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseFFProbe = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// newTestScanner builds a Scanner over a temp-dir database and music dir.
func newTestScanner(t *testing.T) (*Scanner, *jobs.Manager, *library.Service, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.NewDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	lib := library.NewService(database, dir)
	jm := jobs.NewManager(database)
	return NewScanner(lib, jm, dir), jm, lib, dir
}

// waitForJobStatus polls the job until it has the wanted status or the
// deadline passes.
func waitForJobStatus(t *testing.T, jm *jobs.Manager, jobID string, want jobs.JobStatus) *jobs.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, err := jm.GetJob(jobID)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if job.Status == want {
			return job
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job did not reach %s in time", want)
	return nil
}

func TestScanEmptyDir(t *testing.T) {
	s, jm, _, _ := newTestScanner(t)
	if err := s.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	all, err := jm.GetAllJobs()
	if err != nil {
		t.Fatalf("GetAllJobs: %v", err)
	}
	var jobID string
	for _, j := range all {
		if j.Type == jobs.TypeScan {
			jobID = j.ID
		}
	}
	if jobID == "" {
		t.Fatal("no scan job created")
	}
	job := waitForJobStatus(t, jm, jobID, jobs.StatusCompleted)
	if job.Error != "" {
		t.Fatalf("scan job error = %q, want empty", job.Error)
	}
}

func TestIsSupportedAudioExt(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"a.flac", true},
		{"a.mp3", true},
		{"a.m4a", true},
		{"a.aac", true},
		{"a.FLAC", true}, // case-insensitive
		{"a.mp3x", false},
		{"a.txt", false},
		{"a.wav", false},
		{"a", false},
		{"/dir/with/ext.MP3", true},
	}
	for _, tt := range tests {
		if got := isSupportedAudioExt(tt.path); got != tt.want {
			t.Fatalf("isSupportedAudioExt(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// TestScanWalksAndCompletes drives a full scan over a real temp tree and
// only asserts the job lifecycle, because which files get indexed depends
// on the ffprobe binary available on the machine.
func TestScanWalksAndCompletes(t *testing.T) {
	s, jm, _, dir := newTestScanner(t)

	for _, name := range []string{"a.flac", "b.mp3", "notes.txt", "c.wav"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if err := s.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	all, err := jm.GetAllJobs()
	if err != nil {
		t.Fatalf("GetAllJobs: %v", err)
	}
	var jobID string
	for _, j := range all {
		if j.Type == jobs.TypeScan {
			jobID = j.ID
		}
	}
	if jobID == "" {
		t.Fatal("no scan job created")
	}
	waitForJobStatus(t, jm, jobID, jobs.StatusCompleted)
}

func TestIndexFileMissingFileIsNoOp(t *testing.T) {
	s, _, lib, dir := newTestScanner(t)

	// The file does not exist, so ffprobe (if present) must fail and
	// nothing may be indexed.
	s.IndexFile(filepath.Join(dir, "missing.flac"))

	artists, err := lib.GetArtists()
	if err != nil {
		t.Fatalf("GetArtists: %v", err)
	}
	if len(artists) != 0 {
		t.Fatalf("artists = %+v, want none", artists)
	}
}

func TestDetectCover(t *testing.T) {
	s, _, lib, dir := newTestScanner(t)

	artistID, err := lib.EnsureArtist("A")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumID, err := lib.EnsureAlbum(artistID, "B")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}

	albumDir := filepath.Join(dir, "A", "B")
	if err := os.MkdirAll(albumDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// No image at all: the cover stays empty.
	s.detectCover(albumID, albumDir)
	album, err := lib.GetAlbum(albumID)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if album.CoverArt != "" {
		t.Fatalf("cover = %q, want empty", album.CoverArt)
	}

	// A well-known cover name wins.
	if err := os.WriteFile(filepath.Join(albumDir, "cover.jpg"), []byte("img"), 0o600); err != nil {
		t.Fatalf("write cover: %v", err)
	}
	s.detectCover(albumID, albumDir)
	album, err = lib.GetAlbum(albumID)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if album.CoverArt != "/media/A/B/cover.jpg" {
		t.Fatalf("cover = %q, want /media/A/B/cover.jpg", album.CoverArt)
	}

	// An arbitrary image is picked up when no well-known name exists.
	albumID2, err := lib.EnsureAlbum(artistID, "C")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	dirC := filepath.Join(dir, "A", "C")
	if err := os.MkdirAll(dirC, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirC, "photo.PNG"), []byte("img"), 0o600); err != nil {
		t.Fatalf("write photo: %v", err)
	}
	s.detectCover(albumID2, dirC)
	album, err = lib.GetAlbum(albumID2)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if album.CoverArt != "/media/A/C/photo.PNG" {
		t.Fatalf("cover = %q, want /media/A/C/photo.PNG", album.CoverArt)
	}
}
