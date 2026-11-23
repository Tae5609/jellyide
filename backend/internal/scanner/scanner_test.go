package scanner

import (
	"net/http"
	"os"
	"os/exec"
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
			// New tag columns: matched case-insensitively, "3/12" track
			// number, four-digit year from a full date.
			name:    "full tags with mixed case",
			fixture: "testdata/fulltags.json",
			path:    filepath.Join(base, "Artist", "Album", "03 Full.flac"),
			want: probeInfo{
				Title:       "Full Tagged",
				Artist:      "Case Artist",
				Album:       "Case Album",
				AlbumArtist: "Case Album Artist",
				TrackNumber: 3,
				DiscNumber:  2,
				Year:        2001,
				Genre:       "Rock",
				Composer:    "Case Composer",
				Comment:     "Case Comment",
				Duration:    180,
				Bitrate:     1000000,
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

func TestParseTrackNumber(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"3", 3},
		{"3/12", 3},
		{" 3 / 12 ", 3},
		{"0", 0},
		{"", 0},
		{"12/", 12},
		{"/12", 0},
		{"abc", 0},
		{"-2", 0},
	}
	for _, tt := range tests {
		if got := parseTrackNumber(tt.in); got != tt.want {
			t.Fatalf("parseTrackNumber(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestParseYear(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"2001", 2001},
		{"2001-05-15", 2001},
		{"1973", 1973},
		{"Spring 1973 release", 1973},
		{"20011231", 2001}, // first four digits of a long run
		{"999", 0},
		{"", 0},
		{"no digits", 0},
	}
	for _, tt := range tests {
		if got := parseYear(tt.in); got != tt.want {
			t.Fatalf("parseYear(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestTagGetCaseInsensitive(t *testing.T) {
	tags := map[string]string{"TrAcK": "7/9", "ALBUMARTIST": "Someone"}
	if got := tagGet(tags, "track", "tracknumber"); got != "7/9" {
		t.Fatalf("tagGet track = %q, want 7/9", got)
	}
	if got := tagGet(tags, "album_artist", "albumartist"); got != "Someone" {
		t.Fatalf("tagGet albumartist = %q, want Someone", got)
	}
	if got := tagGet(tags, "missing"); got != "" {
		t.Fatalf("tagGet missing = %q, want empty", got)
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
	s.detectCover(albumID, albumDir, filepath.Join(albumDir, "missing.flac"))
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
	s.detectCover(albumID, albumDir, filepath.Join(albumDir, "missing.flac"))
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
	s.detectCover(albumID2, dirC, filepath.Join(dirC, "missing.flac"))
	album, err = lib.GetAlbum(albumID2)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if album.CoverArt != "/media/A/C/photo.PNG" {
		t.Fatalf("cover = %q, want /media/A/C/photo.PNG", album.CoverArt)
	}
}

// TestScanSkipsJellyideDir verifies that files inside the .jellyide
// directory (such as the managed yt-dlp binary) are skipped during
// scanning. A fake ffprobe on PATH makes the indexing deterministic:
// without the skip, the audio file inside .jellyide would be indexed.
func TestScanSkipsJellyideDir(t *testing.T) {
	s, jm, lib, dir := newTestScanner(t)

	jellyDir := filepath.Join(dir, ".jellyide", "bin")
	if err := os.MkdirAll(jellyDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jellyDir, "yt-dlp"), []byte("bin"), 0o600); err != nil {
		t.Fatalf("write yt-dlp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jellyDir, "cache.flac"), []byte("fake audio"), 0o600); err != nil {
		t.Fatalf("write cache.flac: %v", err)
	}

	// A fake ffprobe that always succeeds, so processFile indexes every
	// supported file the walk reaches (no machine ffprobe dependency).
	fakeDir := t.TempDir()
	fake := "#!/bin/sh\necho '{\"format\":{\"duration\":\"1\",\"bit_rate\":\"128000\"}}'\n"
	if err := os.WriteFile(filepath.Join(fakeDir, "ffprobe"), []byte(fake), 0o700); err != nil { //nolint:gosec // G306 - the fake ffprobe must be executable
		t.Fatalf("write fake ffprobe: %v", err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := s.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	all, err := jm.GetAllJobs()
	if err != nil {
		t.Fatalf("GetAllJobs: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("jobs = %d, want 1", len(all))
	}
	waitForJobStatus(t, jm, all[0].ID, jobs.StatusCompleted)

	artists, err := lib.GetArtists()
	if err != nil {
		t.Fatalf("GetArtists: %v", err)
	}
	if len(artists) != 0 {
		t.Fatalf("artists = %+v, want none (.jellyide must be skipped)", artists)
	}
}

// makeTrackWithArt builds a 1s MP3 with an embedded picture using the
// real ffmpeg binary, or skips the test when ffmpeg is not installed.
func makeTrackWithArt(t *testing.T, path string, withArt bool) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono", "-t", "1"}
	if withArt {
		// A PNG cover (not JPEG) proves the extractor re-encodes to JPEG.
		args = append(args, "-f", "lavfi", "-i", "color=c=red:s=64x64:d=1",
			"-map", "0:a", "-map", "1:v", "-c:v", "png", "-frames:v", "1",
			"-disposition:v", "attached_pic", "-id3v2_version", "3")
	}
	args = append(args, path)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil { //nolint:gosec // G204 - test fixture generation with fixed args
		t.Fatalf("ffmpeg fixture: %v: %s", err, out)
	}
}

// TestDetectCoverFromEmbeddedArt is the regression test for downloads
// showing "No cover yet": yt-dlp embeds the thumbnail in the audio file and
// writes no image next to it, so the cover must come from the track.
func TestDetectCoverFromEmbeddedArt(t *testing.T) {
	s, _, lib, dir := newTestScanner(t)
	artistID, err := lib.EnsureArtist("Artist")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumID, err := lib.EnsureAlbum(artistID, "Album")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	albumDir := filepath.Join(dir, "Artist", "Album")
	if err := os.MkdirAll(albumDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	track := filepath.Join(albumDir, "song.mp3")
	makeTrackWithArt(t, track, true)

	s.detectCover(albumID, albumDir, track)

	album, err := lib.GetAlbum(albumID)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if album.CoverArt != "/media/Artist/Album/cover.jpg" {
		t.Fatalf("cover = %q, want /media/Artist/Album/cover.jpg", album.CoverArt)
	}
	data, err := os.ReadFile(filepath.Join(albumDir, "cover.jpg")) //nolint:gosec // G304 - test temp dir
	if err != nil {
		t.Fatalf("cover.jpg not written: %v", err)
	}
	if ct := http.DetectContentType(data); ct != "image/jpeg" {
		t.Fatalf("cover content type = %s, want image/jpeg", ct)
	}
}

// TestDetectCoverNoEmbeddedArtTriedOnce checks that a track without art
// leaves the cover empty and that ffmpeg is only attempted once per album.
func TestDetectCoverNoEmbeddedArtTriedOnce(t *testing.T) {
	s, _, lib, dir := newTestScanner(t)
	artistID, err := lib.EnsureArtist("Artist")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumID, err := lib.EnsureAlbum(artistID, "Plain")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	albumDir := filepath.Join(dir, "Artist", "Plain")
	if err := os.MkdirAll(albumDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	track := filepath.Join(albumDir, "song.mp3")
	makeTrackWithArt(t, track, false)

	s.detectCover(albumID, albumDir, track)
	album, err := lib.GetAlbum(albumID)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if album.CoverArt != "" {
		t.Fatalf("cover = %q, want empty", album.CoverArt)
	}
	if !s.coverTried[albumID] {
		t.Fatalf("album not recorded as tried")
	}

	// Once tried, a track that now has art is not re-extracted in the
	// same scanner lifetime (bounded ffmpeg cost per scan).
	makeTrackWithArt(t, track, true)
	s.detectCover(albumID, albumDir, track)
	album, err = lib.GetAlbum(albumID)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if album.CoverArt != "" {
		t.Fatalf("cover = %q, want empty (already tried)", album.CoverArt)
	}
}
