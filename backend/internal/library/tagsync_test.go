package library

import (
	"testing"
)

func TestNormalizeTagValues(t *testing.T) {
	raw := map[string]interface{}{
		"title":        "Song",
		"album":        nil, // missing -> ""
		"artist":       "A",
		"track_number": "3/12",     // numeric text -> 3
		"disc_number":  float64(2), // JSON number -> 2
		"year":         2001,
		"genre":        "Rock",
		"unknown":      "dropped",
	}
	got := NormalizeTagValues(raw)
	if len(got) != len(CanonicalTagFields) {
		t.Fatalf("len = %d, want %d canonical fields", len(got), len(CanonicalTagFields))
	}
	want := map[string]interface{}{
		"title": "Song", "album": "", "artist": "A", "album_artist": "",
		"track_number": 3, "disc_number": 2, "year": 2001,
		"genre": "Rock", "composer": "", "comment": "",
	}
	for field, w := range want {
		if got[field] != w {
			t.Fatalf("%s = %v (%T), want %v", field, got[field], got[field], w)
		}
	}
}

func TestTagsHashStable(t *testing.T) {
	a := NormalizeTagValues(map[string]interface{}{"title": "S", "artist": "A", "track_number": "1/10"})
	b := NormalizeTagValues(map[string]interface{}{"title": "S", "artist": "A", "track_number": 1})
	if TagsHash(a) != TagsHash(b) {
		t.Fatal("same normalized values must hash equal")
	}
	c := NormalizeTagValues(map[string]interface{}{"title": "S", "artist": "A", "track_number": 2})
	if TagsHash(a) == TagsHash(c) {
		t.Fatal("different values must hash differently")
	}
}

func TestTrackTagValues(t *testing.T) {
	str := func(s string) *string { return &s }
	track := &Track{Title: "T", AlbumID: "alb", Genre: str("Rock")}
	values := TrackTagValues(track, "Album Name")
	if values["title"] != "T" || values["album"] != "Album Name" {
		t.Fatalf("title/album = %v/%v", values["title"], values["album"])
	}
	if values["genre"] != "Rock" {
		t.Fatalf("genre = %v", values["genre"])
	}
	if values["artist"] != "" || values["track_number"] != 0 {
		t.Fatalf("unset fields must normalize to zero values: %v", values)
	}
}

func TestTagValueEqual(t *testing.T) {
	cases := []struct {
		field string
		a, b  interface{}
		want  bool
	}{
		{"title", "S", "S", true},
		{"title", "S", "Other", false},
		{"title", nil, "", true},
		{"track_number", "3/12", 3, true},
		{"track_number", float64(3), "3", true},
		{"track_number", nil, 0, true},
		{"track_number", 1, 2, false},
		{"year", "2001-05-15", 2001, true},
		{"genre", nil, "Rock", false},
	}
	for _, c := range cases {
		if got := TagValueEqual(c.field, c.a, c.b); got != c.want {
			t.Fatalf("TagValueEqual(%s, %v, %v) = %v, want %v", c.field, c.a, c.b, got, c.want)
		}
	}
}

func TestDifferingFields(t *testing.T) {
	a := NormalizeTagValues(map[string]interface{}{"title": "S", "artist": "A"})
	b := NormalizeTagValues(map[string]interface{}{"title": "S2", "artist": "A", "genre": "Rock"})
	diff := DifferingFields(a, b)
	want := map[string]bool{"title": true, "genre": true}
	if len(diff) != len(want) {
		t.Fatalf("diff = %v, want %v", diff, want)
	}
	for _, f := range diff {
		if !want[f] {
			t.Fatalf("unexpected diff field %q", f)
		}
	}
}

func TestTagValueText(t *testing.T) {
	if got := TagValueText("title", "S"); got != "S" {
		t.Fatalf("title = %q", got)
	}
	if got := TagValueText("track_number", 3); got != "3" {
		t.Fatalf("track_number = %q, want 3", got)
	}
	if got := TagValueText("track_number", 0); got != "" {
		t.Fatalf("track_number 0 = %q, want empty", got)
	}
	if got := TagValueText("title", ""); got != "" {
		t.Fatalf("empty = %q", got)
	}
}
