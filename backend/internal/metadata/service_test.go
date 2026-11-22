package metadata

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"yt-scraper/internal/db"
	"yt-scraper/internal/library"
)

func TestIsJunk(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"   ", true},
		{"1", true}, // len < 3 and no letters
		{"ab", false},
		{"12", true},
		{"1a", false},
		{"123", false}, // len >= 3: never junk
		{"中", false},   // 3 bytes, so len >= 3
	}
	for _, tt := range tests {
		if got := isJunk(tt.in); got != tt.want {
			t.Fatalf("isJunk(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestFindSignificantPatternsCommonPrefix(t *testing.T) {
	titles := []string{"(Live) Song A", "(Live) Song B", "(Live) Song C"}
	got := findSignificantPatterns(titles)
	if len(got) == 0 {
		t.Fatal("no patterns found")
	}
	// The longest span shared by all three titles is maximal.
	if got[0].Text != "(Live) Song " {
		t.Fatalf("top pattern = %q, want %q", got[0].Text, "(Live) Song ")
	}
	if got[0].Count != 3 {
		t.Fatalf("count = %d, want 3", got[0].Count)
	}
	if got[0].Score != float64(len("(Live) Song ")*3) {
		t.Fatalf("score = %v, want %d", got[0].Score, len("(Live) Song ")*3)
	}
}

func TestFindSignificantPatternsJunkExcluded(t *testing.T) {
	// Both titles share only "  --  " (and its parts): it trims to "--"
	// (short, no letters) and must be treated as junk, leaving no pattern.
	// Without the junk filter, "  --  " would show up as a candidate.
	titles := []string{"A  --  B", "C  --  D"}
	if got := findSignificantPatterns(titles); len(got) != 0 {
		t.Fatalf("patterns = %+v, want none (all shared substrings are junk)", got)
	}

	// A real pattern next to a junk separator: the maximal shared span is
	// "Live - ", and no shorter junk fragment may appear.
	titles = []string{"Live - A", "Live - B"}
	got := findSignificantPatterns(titles)
	if len(got) != 1 {
		t.Fatalf("patterns = %+v, want exactly one", got)
	}
	if got[0].Text != "Live - " {
		t.Fatalf("top pattern = %q, want %q", got[0].Text, "Live - ")
	}
}

func TestFindSignificantPatternsThreshold(t *testing.T) {
	// 3 of 4 titles share "Same " -> 0.75 < 0.8 -> dropped.
	titles := []string{"Same X1", "Same X2", "Same X3", "Totally different title"}
	got := findSignificantPatterns(titles)
	if len(got) != 0 {
		t.Fatalf("patterns = %+v, want none below the 0.8 threshold", got)
	}
}

func TestFindSignificantPatternsCapAndOrder(t *testing.T) {
	// 15 disjoint shared words -> capped at 10 candidates.
	var seg1, seg2 []string
	for i := 1; i <= 15; i++ {
		w := "w" + string(rune('a'+i-1)) + "x"
		seg1 = append(seg1, w)
		seg2 = append(seg2, w)
	}
	t1 := strings.Join(seg1, "##")
	t2 := strings.Join(seg2, "@@")
	got := findSignificantPatterns([]string{t1, t2})
	if len(got) != 10 {
		t.Fatalf("got %d candidates, want the cap of 10", len(got))
	}

	// Scores are sorted descending.
	titles := []string{"P qqqq R sss T", "X qqqq Y sss Z"}
	got = findSignificantPatterns(titles)
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2: %+v", len(got), got)
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].Score > got[j].Score }) {
		t.Fatalf("candidates not sorted by score: %+v", got)
	}
	if got[0].Text != " qqqq " {
		t.Fatalf("top candidate = %q, want %q", got[0].Text, " qqqq ")
	}
}

func TestFindSignificantPatternsEmpty(t *testing.T) {
	if got := findSignificantPatterns(nil); len(got) != 0 {
		t.Fatalf("patterns = %+v, want none", got)
	}
	if got := findSignificantPatterns([]string{""}); len(got) != 0 {
		t.Fatalf("patterns = %+v, want none for an empty title", got)
	}
}

// newTestService builds a metadata Service over a temp-dir database and
// music dir, with one seeded track per call.
func newTestService(t *testing.T) (*Service, *library.Service, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.NewDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	lib := library.NewService(database, dir)
	return NewService(database, lib), lib, dir
}

// seedTrack inserts one track and returns its id and on-disk path.
func seedTrack(t *testing.T, lib *library.Service, dir, title string) (string, string) {
	t.Helper()
	artistID, err := lib.EnsureArtist("Artist")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumID, err := lib.EnsureAlbum(artistID, "Album")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	path := filepath.Join(dir, "Artist", "Album", title+".flac")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := lib.UpsertTrack(albumID, title, path, 60, 900, library.TrackMeta{}); err != nil {
		t.Fatalf("UpsertTrack: %v", err)
	}
	tracks, err := lib.GetTracksByAlbum(albumID)
	if err != nil {
		t.Fatalf("GetTracksByAlbum: %v", err)
	}
	for _, tr := range tracks {
		if tr.Path == path {
			return tr.ID, path
		}
	}
	t.Fatalf("seeded track %q not found", path)
	return "", ""
}

func TestPreviewChanges(t *testing.T) {
	s, lib, dir := newTestService(t)

	cases := []struct {
		name  string
		title string
		req   PreviewRequest
		want  string
	}{
		{
			name:  "replace",
			title: "Live at the Club",
			req:   PreviewRequest{Action: "replace", Target: "Live", Value: "Studio"},
			want:  "Studio at the Club",
		},
		{
			name:  "prefix",
			title: "Song",
			req:   PreviewRequest{Action: "prefix", Value: "[Remix] "},
			want:  "[Remix] Song",
		},
		{
			name:  "suffix",
			title: "Song",
			req:   PreviewRequest{Action: "suffix", Value: " (Live)"},
			want:  "Song (Live)",
		},
		{
			name:  "regex",
			title: "Foo (Remix) Bar",
			req:   PreviewRequest{Action: "regex", Target: "Foo", Value: "Baz"},
			want:  "Baz (Remix) Bar",
		},
		{
			name:  "regex repeated",
			title: "foo ooo",
			req:   PreviewRequest{Action: "regex", Target: "o+", Value: "0"},
			want:  "f0 0",
		},
		{
			// An unmatchable pattern leaves the title untouched.
			name:  "regex no match",
			title: "Plain Title",
			req:   PreviewRequest{Action: "regex", Target: "zzz", Value: "x"},
			want:  "Plain Title",
		},
		{
			// An invalid regex must not fail the preview; the title stays.
			name:  "invalid regex",
			title: "Keep Me",
			req:   PreviewRequest{Action: "regex", Target: "[", Value: "x"},
			want:  "Keep Me",
		},
		{
			// Unknown actions leave the title untouched.
			name:  "unknown action",
			title: "Keep Me",
			req:   PreviewRequest{Action: "bogus", Target: "x", Value: "y"},
			want:  "Keep Me",
		},
		{
			// Results are trimmed.
			name:  "trimmed result",
			title: "Song",
			req:   PreviewRequest{Action: "prefix", Value: "  pad  "},
			want:  "pad  Song",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			trackID, _ := seedTrack(t, lib, dir, tt.title)
			got, err := s.PreviewChanges(PreviewRequest{
				Action:   tt.req.Action,
				Target:   tt.req.Target,
				Value:    tt.req.Value,
				TrackIDs: []string{trackID, "missing-track-id"},
			})
			if err != nil {
				t.Fatalf("PreviewChanges: %v", err)
			}
			// The unknown track id is skipped, so exactly one result.
			if len(got) != 1 {
				t.Fatalf("previews = %d, want 1 (unknown id skipped): %+v", len(got), got)
			}
			if got[0].OldTitle != tt.title || got[0].NewTitle != tt.want {
				t.Fatalf("preview = %+v, want %q -> %q", got[0], tt.title, tt.want)
			}
		})
	}
}

func TestAnalyzeAlbum(t *testing.T) {
	s, lib, dir := newTestService(t)

	_, _ = seedTrack(t, lib, dir, "Shared Prefix One")
	_, _ = seedTrack(t, lib, dir, "Shared Prefix Two")

	// Find the album id through the library service.
	artists, err := lib.GetArtists()
	if err != nil {
		t.Fatalf("GetArtists: %v", err)
	}
	albums, err := lib.GetAlbumsByArtist(artists[0].ID)
	if err != nil {
		t.Fatalf("GetAlbumsByArtist: %v", err)
	}

	res, err := s.AnalyzeAlbum(albums[0].ID)
	if err != nil {
		t.Fatalf("AnalyzeAlbum: %v", err)
	}
	if len(res.Suggestions) == 0 {
		t.Fatal("no suggestions for a shared prefix")
	}
	if res.Suggestions[0].Text != "Shared Prefix " {
		t.Fatalf("top suggestion = %q, want %q", res.Suggestions[0].Text, "Shared Prefix ")
	}
}

func TestAnalyzeAlbumUnknownAlbum(t *testing.T) {
	s, _, _ := newTestService(t)

	res, err := s.AnalyzeAlbum("missing-album")
	if err != nil {
		t.Fatalf("AnalyzeAlbum: %v", err)
	}
	if len(res.Suggestions) != 0 {
		t.Fatalf("suggestions = %+v, want none", res.Suggestions)
	}
}

// chdir switches the test process cwd (tests in this package run
// sequentially) so the Python helper call can be intercepted with a fake
// update_metadata.py.
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Fatalf("chdir back: %v", err)
		}
	})
}

func TestApplyChangesRenamesAndUpdates(t *testing.T) {
	s, lib, dir := newTestService(t)
	trackID, oldPath := seedTrack(t, lib, dir, "Old Title")
	if err := os.WriteFile(oldPath, []byte("flac data"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// The fake helper just succeeds, like the real one does.
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "update_metadata.py"), []byte("import sys\nsys.exit(0)\n"), 0o600); err != nil {
		t.Fatalf("write fake helper: %v", err)
	}
	chdir(t, work)

	newTitle := "New Title"
	if err := s.ApplyChanges([]PreviewResponse{
		{TrackID: trackID, OldTitle: "Old Title", NewTitle: newTitle},
	}); err != nil {
		t.Fatalf("ApplyChanges: %v", err)
	}

	// The file was renamed on disk...
	wantPath := filepath.Join(dir, "Artist", "Album", "New Title.flac")
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	// ...and the DB row follows it.
	track, err := lib.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	if track.Title != newTitle || track.Path != wantPath {
		t.Fatalf("track = %+v, want title %q path %q", track, newTitle, wantPath)
	}
}

func TestApplyChangesHelperFailureKeepsState(t *testing.T) {
	s, lib, dir := newTestService(t)
	trackID, oldPath := seedTrack(t, lib, dir, "Old Title")
	if err := os.WriteFile(oldPath, []byte("flac data"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// The fake helper fails, so nothing may be renamed or rewritten.
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "update_metadata.py"), []byte("import sys\nprint('nope', file=sys.stderr)\nsys.exit(1)\n"), 0o600); err != nil {
		t.Fatalf("write fake helper: %v", err)
	}
	chdir(t, work)

	newTitle := "New Title"
	if err := s.ApplyChanges([]PreviewResponse{
		{TrackID: trackID, OldTitle: "Old Title", NewTitle: newTitle},
	}); err != nil {
		t.Fatalf("ApplyChanges: %v", err)
	}

	track, err := lib.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	if track.Title != "Old Title" || track.Path != oldPath {
		t.Fatalf("track changed despite helper failure: %+v", track)
	}
}
