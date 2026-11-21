package library

import (
	"os"
	"path/filepath"
	"testing"

	"yt-scraper/internal/db"
)

// newTestService builds a Service on a temp-dir SQLite database and music dir.
func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.NewDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return NewService(database, dir), dir
}

func TestSanitizeFilename(t *testing.T) {
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
		{`/root/abs/look`, "-root-abs-look"},
		{"..", ".."}, // NOTE: dots are not sanitized today (see docs/plan/BUGS.md); pin current behaviour
		{"../escape", "..-escape"},
		{"Ünïcödé 文件", "Ünïcödé 文件"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := sanitizeFilename(tt.in); got != tt.want {
			t.Fatalf("sanitizeFilename(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEnsureArtist(t *testing.T) {
	s, _ := newTestService(t)

	id1, err := s.EnsureArtist("Some Artist")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	if id1 == "" {
		t.Fatal("empty artist id")
	}

	// Same name again (with stray whitespace): same id, no duplicate row.
	id2, err := s.EnsureArtist("  Some Artist  ")
	if err != nil {
		t.Fatalf("EnsureArtist again: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("EnsureArtist returned %s, want the existing %s", id2, id1)
	}

	artists, err := s.GetArtists()
	if err != nil {
		t.Fatalf("GetArtists: %v", err)
	}
	if len(artists) != 1 || artists[0].Name != "Some Artist" {
		t.Fatalf("artists = %+v, want one trimmed 'Some Artist'", artists)
	}
}

func TestEnsureAlbum(t *testing.T) {
	s, _ := newTestService(t)

	artistID, err := s.EnsureArtist("A")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}

	id1, err := s.EnsureAlbum(artistID, "Album One")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	id2, err := s.EnsureAlbum(artistID, "Album One")
	if err != nil {
		t.Fatalf("EnsureAlbum again: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("EnsureAlbum returned %s, want the existing %s", id2, id1)
	}

	albums, err := s.GetAlbumsByArtist(artistID)
	if err != nil {
		t.Fatalf("GetAlbumsByArtist: %v", err)
	}
	if len(albums) != 1 || albums[0].Title != "Album One" {
		t.Fatalf("albums = %+v", albums)
	}

	// Unknown artist yields an empty list, not an error.
	none, err := s.GetAlbumsByArtist("missing-artist")
	if err != nil {
		t.Fatalf("GetAlbumsByArtist unknown: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("albums for unknown artist = %+v, want none", none)
	}
}

func TestUpsertAndGetTrack(t *testing.T) {
	s, _ := newTestService(t)

	artistID, err := s.EnsureArtist("A")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumID, err := s.EnsureAlbum(artistID, "B")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	path := filepath.Join("A", "B", "Song One.flac")

	if err := s.UpsertTrack(albumID, "Song One", path, 60, 900); err != nil {
		t.Fatalf("UpsertTrack: %v", err)
	}
	tracks, err := s.GetTracksByAlbum(albumID)
	if err != nil {
		t.Fatalf("GetTracksByAlbum: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("tracks = %d, want 1", len(tracks))
	}
	trackID := tracks[0].ID

	// Upsert on the same path updates in place instead of inserting.
	if err := s.UpsertTrack(albumID, "Song One (remix)", path, 61, 901); err != nil {
		t.Fatalf("UpsertTrack again: %v", err)
	}
	tracks, err = s.GetTracksByAlbum(albumID)
	if err != nil {
		t.Fatalf("GetTracksByAlbum: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("tracks after upsert = %d, want 1 (no duplicate row)", len(tracks))
	}
	if tracks[0].Title != "Song One (remix)" || tracks[0].Duration != 61 || tracks[0].Bitrate != 901 {
		t.Fatalf("track = %+v, want updated values", tracks[0])
	}

	got, err := s.GetTrack(trackID)
	if err != nil {
		t.Fatalf("GetTrack: %v", err)
	}
	if got.ID != trackID || got.Title != "Song One (remix)" {
		t.Fatalf("GetTrack = %+v", got)
	}
	if _, err := s.GetTrack("missing"); err == nil {
		t.Fatal("GetTrack for a missing id should error")
	}
}

func TestGetAlbum(t *testing.T) {
	s, _ := newTestService(t)

	artistID, err := s.EnsureArtist("Artist One")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumID, err := s.EnsureAlbum(artistID, "Album One")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}

	album, err := s.GetAlbum(albumID)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if album.Title != "Album One" || album.Artist != "Artist One" {
		t.Fatalf("album = %+v", album)
	}
	if _, err := s.GetAlbum("missing"); err == nil {
		t.Fatal("GetAlbum for a missing id should error")
	}
}

func TestRenameAlbum(t *testing.T) {
	s, dir := newTestService(t)

	artistID, err := s.EnsureArtist("Artist One")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumID, err := s.EnsureAlbum(artistID, "Old Name")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	oldDir := filepath.Join(dir, "Artist One", "Old Name")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(oldDir, "Song One.flac")
	if err := s.UpsertTrack(albumID, "Song One", path, 60, 900); err != nil {
		t.Fatalf("UpsertTrack: %v", err)
	}

	if err := s.RenameAlbum(albumID, "New Name"); err != nil {
		t.Fatalf("RenameAlbum: %v", err)
	}

	// The on-disk directory moved...
	if _, err := os.Stat(filepath.Join(dir, "Artist One", "New Name")); err != nil {
		t.Fatalf("renamed dir missing: %v", err)
	}
	// ...the track path followed it...
	tracks, err := s.GetTracksByAlbum(albumID)
	if err != nil {
		t.Fatalf("GetTracksByAlbum: %v", err)
	}
	if want := filepath.Join(dir, "Artist One", "New Name", "Song One.flac"); tracks[0].Path != want {
		t.Fatalf("track path = %q, want %q", tracks[0].Path, want)
	}
	// ...and the title changed.
	album, err := s.GetAlbum(albumID)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if album.Title != "New Name" {
		t.Fatalf("album title = %q, want %q", album.Title, "New Name")
	}
}

func TestRenameAlbumSanitizesTitle(t *testing.T) {
	s, dir := newTestService(t)

	artistID, err := s.EnsureArtist("Artist One")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumID, err := s.EnsureAlbum(artistID, "Old")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	oldDir := filepath.Join(dir, "Artist One", "Old")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(oldDir, "Song.flac")
	if err := s.UpsertTrack(albumID, "Song", path, 60, 900); err != nil {
		t.Fatalf("UpsertTrack: %v", err)
	}

	// Illegal characters in the new title become dashes on disk.
	if err := s.RenameAlbum(albumID, "A/B: C"); err != nil {
		t.Fatalf("RenameAlbum: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Artist One", "A-B- C")); err != nil {
		t.Fatalf("sanitized dir missing: %v", err)
	}
}

func TestDeleteAlbum(t *testing.T) {
	s, dir := newTestService(t)

	artistID, err := s.EnsureArtist("A")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumID, err := s.EnsureAlbum(artistID, "B")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	if err := s.UpsertTrack(albumID, "Song", filepath.Join(dir, "A", "B", "Song.flac"), 60, 900); err != nil {
		t.Fatalf("UpsertTrack: %v", err)
	}

	if err := s.DeleteAlbum(albumID); err != nil {
		t.Fatalf("DeleteAlbum: %v", err)
	}
	if _, err := s.GetAlbum(albumID); err == nil {
		t.Fatal("album still present after delete")
	}
	tracks, err := s.GetTracksByAlbum(albumID)
	if err != nil {
		t.Fatalf("GetTracksByAlbum: %v", err)
	}
	if len(tracks) != 0 {
		t.Fatalf("tracks = %d after album delete, want 0", len(tracks))
	}
}

func TestDeleteTrack(t *testing.T) {
	s, dir := newTestService(t)

	artistID, err := s.EnsureArtist("A")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumID, err := s.EnsureAlbum(artistID, "B")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	if err := s.UpsertTrack(albumID, "Song", filepath.Join(dir, "A", "B", "Song.flac"), 60, 900); err != nil {
		t.Fatalf("UpsertTrack: %v", err)
	}
	tracks, err := s.GetTracksByAlbum(albumID)
	if err != nil {
		t.Fatalf("GetTracksByAlbum: %v", err)
	}

	if err := s.DeleteTrack(tracks[0].ID); err != nil {
		t.Fatalf("DeleteTrack: %v", err)
	}
	remaining, err := s.GetTracksByAlbum(albumID)
	if err != nil {
		t.Fatalf("GetTracksByAlbum: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("tracks = %d after delete, want 0", len(remaining))
	}

	// Deleting a missing track is a no-op without error.
	if err := s.DeleteTrack("missing"); err != nil {
		t.Fatalf("DeleteTrack missing: %v", err)
	}
}

func TestSetAlbumCover(t *testing.T) {
	s, _ := newTestService(t)

	artistID, err := s.EnsureArtist("A")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumID, err := s.EnsureAlbum(artistID, "B")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	if err := s.SetAlbumCover(albumID, "/media/A/B/cover.jpg"); err != nil {
		t.Fatalf("SetAlbumCover: %v", err)
	}
	album, err := s.GetAlbum(albumID)
	if err != nil {
		t.Fatalf("GetAlbum: %v", err)
	}
	if album.CoverArt != "/media/A/B/cover.jpg" {
		t.Fatalf("cover = %q", album.CoverArt)
	}
}
