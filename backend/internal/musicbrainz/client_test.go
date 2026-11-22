package musicbrainz

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

// searchFixture is a minimal MusicBrainz release-search response.
const searchFixture = `{
  "count": 2,
  "offset": 0,
  "releases": [
    {
      "id": "mbid-1",
      "score": 97,
      "title": "The Dark Side of the Moon",
      "date": "1973-03-01",
      "country": "GB",
      "track-count": 9,
      "artist-credit": [{"name": "Pink Floyd"}]
    },
    {
      "id": "mbid-2",
      "score": 61,
      "title": "Another Album",
      "date": "",
      "artist-credit": [{"name": "A"}, {"name": "B"}]
    }
  ]
}`

func TestSearchReleases(t *testing.T) {
	var sawUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUserAgent = r.Header.Get("User-Agent")
		if r.URL.Path != "/ws/2/release" || r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("fmt") != "json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(searchFixture))
	}))
	defer server.Close()

	client := newClient(server.URL+"/ws/2", 1000, time.Minute)
	results, err := client.SearchReleases(context.Background(), "pink floyd dark side")
	if err != nil {
		t.Fatalf("SearchReleases: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	first := results[0]
	if first.MBID != "mbid-1" || first.Title != "The Dark Side of the Moon" || first.Artist != "Pink Floyd" {
		t.Fatalf("first = %+v", first)
	}
	if first.Date != "1973-03-01" || first.Country != "GB" || first.TrackCount != 9 || first.Score != 97 {
		t.Fatalf("first = %+v", first)
	}
	if results[1].Artist != "A, B" {
		t.Fatalf("second artist = %q, want 'A, B'", results[1].Artist)
	}
	if sawUserAgent != UserAgent {
		t.Fatalf("user agent = %q, want %q", sawUserAgent, UserAgent)
	}
}

func TestSearchReleasesErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := newClient(server.URL+"/ws/2", 1000, time.Minute)
	if _, err := client.SearchReleases(context.Background(), "x"); err == nil {
		t.Fatal("want error for a 503 response")
	}
}

const releaseFixture = `{
  "id": "mbid-1",
  "title": "The Dark Side of the Moon",
  "date": "1973-03-01",
  "artist-credit": [{"name": "Pink Floyd"}],
  "tags": [{"name": "rock", "count": 120}, {"name": "progressive rock", "count": 90}],
  "media": [
    {"position": 1, "tracks": [
      {"number": "1", "recording": {"title": "Speak to Me"}},
      {"number": "2", "title": "Breathe"},
      {"number": "3/2", "title": "On the Run"}
    ]},
    {"position": 2, "tracks": [
      {"number": "1", "title": "Time"}
    ]}
  ]
}`

func TestGetRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws/2/release/mbid-1" || r.URL.Query().Get("inc") != "recordings+tags" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(releaseFixture))
	}))
	defer server.Close()

	client := newClient(server.URL+"/ws/2", 1000, time.Minute)
	release, err := client.GetRelease(context.Background(), "mbid-1")
	if err != nil {
		t.Fatalf("GetRelease: %v", err)
	}
	if release.MBID != "mbid-1" || release.Title != "The Dark Side of the Moon" || release.Artist != "Pink Floyd" {
		t.Fatalf("release = %+v", release)
	}
	if release.TopTag != "rock" {
		t.Fatalf("top tag = %q, want rock", release.TopTag)
	}
	if len(release.Tracks) != 4 {
		t.Fatalf("tracks = %d, want 4", len(release.Tracks))
	}
	if release.Tracks[0].Title != "Speak to Me" {
		t.Fatalf("track 1 title = %q (recording title fallback)", release.Tracks[0].Title)
	}
	if release.Tracks[2].Number != 3 {
		t.Fatalf("track 3 number = %d, want 3 (parsed from 3/2)", release.Tracks[2].Number)
	}
	if release.Tracks[3].Disc != 2 || release.Tracks[3].Number != 1 {
		t.Fatalf("track 4 = %+v, want disc 2 number 1", release.Tracks[3])
	}
	if release.Raw["title"] != "The Dark Side of the Moon" {
		t.Fatal("raw release document missing")
	}
}

func TestCacheServesRepeatedRequests(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(searchFixture))
	}))
	defer server.Close()

	client := newClient(server.URL+"/ws/2", 1000, time.Minute)
	for i := 0; i < 3; i++ {
		if _, err := client.SearchReleases(context.Background(), "same query"); err != nil {
			t.Fatalf("SearchReleases: %v", err)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("server hits = %d, want 1 (cached)", got)
	}
	// A different query is a different cache key.
	if _, err := client.SearchReleases(context.Background(), "different query"); err != nil {
		t.Fatalf("SearchReleases: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("server hits = %d, want 2", got)
	}
}

func TestCacheExpires(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(searchFixture))
	}))
	defer server.Close()

	client := newClient(server.URL+"/ws/2", 1000, 30*time.Millisecond)
	if _, err := client.SearchReleases(context.Background(), "q"); err != nil {
		t.Fatalf("SearchReleases: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := client.SearchReleases(context.Background(), "q"); err != nil {
		t.Fatalf("SearchReleases: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("server hits = %d, want 2 (cache expired)", got)
	}
}

func TestRateLimiterPacesRequests(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"releases": []}`))
	}))
	defer server.Close()

	// 10 tokens/second -> ~100ms between the first and fourth request.
	client := newClient(server.URL, 10, time.Minute)
	start := time.Now()
	for i := 0; i < 4; i++ {
		if _, err := client.SearchReleases(context.Background(), "query "+string(rune('a'+i))); err != nil {
			t.Fatalf("SearchReleases: %v", err)
		}
	}
	elapsed := time.Since(start)
	if elapsed < 250*time.Millisecond {
		t.Fatalf("4 requests at 10/s took %v, want at least ~250ms (rate limited)", elapsed)
	}
}

func TestMapTracks(t *testing.T) {
	release := &Release{
		Artist: "Pink Floyd",
		Date:   "1973-03-01",
		TopTag: "rock",
		Tracks: []ReleaseTrack{
			{Disc: 1, Number: 1, Title: "Speak to Me"},
			{Disc: 1, Number: 2, Title: "Breathe"},
			{Disc: 2, Number: 1, Title: "Time"},
		},
	}
	local := []LocalTrack{
		{TrackID: "t1", Title: "Whatever", Number: 2, Disc: 1, HasNumber: true},
		{TrackID: "t2", Title: "Time", Number: 1, Disc: 2, HasNumber: true},
		{TrackID: "t3", Title: "Speak to Me (Live)"},
		{TrackID: "t4", Title: "Completely Different"},
	}
	entries := MapTracks(release, local)

	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3 (t4 has no candidate left): %+v", len(entries), entries)
	}
	// Local order is preserved.
	for i, want := range []string{"t1", "t2", "t3"} {
		if entries[i].TrackID != want {
			t.Fatalf("entry %d = %s, want %s", i, entries[i].TrackID, want)
		}
	}

	// Number matches: confidence 1, proposed title from the release.
	if entries[0].Confidence != 1 {
		t.Fatalf("t1 confidence = %v, want 1", entries[0].Confidence)
	}
	if entries[0].Proposed["title"] != "Breathe" {
		t.Fatalf("t1 proposed title = %v", entries[0].Proposed["title"])
	}
	if entries[1].Confidence != 1 || entries[1].Proposed["title"] != "Time" {
		t.Fatalf("t2 = %+v", entries[1])
	}

	// t3 has no number: best remaining title match is "Speak to Me".
	if entries[2].Confidence <= 0 || entries[2].Confidence >= 1 {
		t.Fatalf("t3 confidence = %v, want a ratio between 0 and 1", entries[2].Confidence)
	}
	if entries[2].Proposed["title"] != "Speak to Me" {
		t.Fatalf("t3 proposed = %v", entries[2].Proposed)
	}
	// t4 has no number and no release track left to match: it is dropped.
}

func TestMapTracksDropsUnmatchable(t *testing.T) {
	release := &Release{
		Artist: "A",
		Tracks: []ReleaseTrack{{Disc: 1, Number: 1, Title: "Only"}},
	}
	local := []LocalTrack{
		{TrackID: "t1", Title: "Only", Number: 1, Disc: 1, HasNumber: true},
		{TrackID: "t2", Title: "No Match Left"},
	}
	entries := MapTracks(release, local)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1: %+v", len(entries), entries)
	}
	if entries[0].TrackID != "t1" {
		t.Fatalf("entry = %s, want t1", entries[0].TrackID)
	}
}

func TestMapTracksProposedFields(t *testing.T) {
	release := &Release{
		Artist: "The Band",
		Date:   "1999-12-31",
		TopTag: "pop",
		Tracks: []ReleaseTrack{{Disc: 1, Number: 1, Title: "One"}},
	}
	local := []LocalTrack{{TrackID: "t1", Title: "One", Number: 1, Disc: 1, HasNumber: true}}
	entries := MapTracks(release, local)
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	p := entries[0].Proposed
	if p["title"] != "One" || p["artist"] != "The Band" || p["album_artist"] != "The Band" {
		t.Fatalf("proposed = %v", p)
	}
	if p["year"] != 1999 {
		t.Fatalf("proposed year = %v, want 1999", p["year"])
	}
	if p["genre"] != "pop" {
		t.Fatalf("proposed genre = %v, want pop", p["genre"])
	}
}

func TestTitleSimilarity(t *testing.T) {
	cases := []struct {
		a, b string
		want float64
	}{
		{"Song One", "song one", 1},
		{"", "", 0},
		{"", "x", 0.0},
		{"abc", "abd", 2.0 / 3.0},
		{"kitten", "sitting", 4.0 / 7.0}, // distance 3 over 7 runes
	}
	for _, tt := range cases {
		got := TitleSimilarity(tt.a, tt.b)
		if mathAbsDiff(got, tt.want) > 1e-9 {
			t.Fatalf("TitleSimilarity(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func mathAbsDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

func TestParseNumberAndYear(t *testing.T) {
	if parseNumber("3/12") != 3 {
		t.Fatal("parseNumber(3/12)")
	}
	if parseNumber("2") != 2 {
		t.Fatal("parseNumber(2)")
	}
	if parseNumber("x") != 0 {
		t.Fatal("parseNumber(x)")
	}
	if ParseYear("1973-05-01") != 1973 {
		t.Fatal("ParseYear date")
	}
	if ParseYear("1973") != 1973 {
		t.Fatal("ParseYear bare")
	}
	if ParseYear("unknown") != 0 {
		t.Fatal("ParseYear none")
	}
}

func TestNormalizeTitle(t *testing.T) {
	if got := NormalizeTitle("  The  GREAT song! "); got != "thegreatsong" {
		t.Fatalf("NormalizeTitle = %q", got)
	}
}

func TestCacheEvictsExpiredOnRead(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The cached re-request never reaches the server, so the
		// refetch after the TTL is the second hit.
		if hits.Add(1) == 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"releases": []}`))
	}))
	defer server.Close()

	client := newClient(server.URL+"/ws/2", 1000, 20*time.Millisecond)
	if _, err := client.SearchReleases(context.Background(), "q"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := client.SearchReleases(context.Background(), "q"); err != nil {
		t.Fatalf("cached: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("hits = %d, want 1 (cache hit)", got)
	}

	// After the TTL the stale entry must be evicted on the next read.
	time.Sleep(40 * time.Millisecond)
	if _, err := client.SearchReleases(context.Background(), "q"); err == nil {
		t.Fatal("want the 500 from the refetch")
	}
	client.mu.Lock()
	cached := len(client.cache)
	client.mu.Unlock()
	if cached != 0 {
		t.Fatalf("cache = %d entries, want 0 (expired entry evicted on read)", cached)
	}
}

func TestCacheBoundedToMaxEntries(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"releases": []}`))
	}))
	defer server.Close()

	client := newClient(server.URL+"/ws/2", 1000, time.Minute)
	last := maxCacheEntries + 9
	for i := 0; i <= last; i++ {
		if _, err := client.SearchReleases(context.Background(), fmt.Sprintf("q%d", i)); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}

	client.mu.Lock()
	cached := len(client.cache)
	_, oldestCached := client.cache[client.base+"/release?"+url.Values{"fmt": []string{"json"}, "limit": []string{"10"}, "query": []string{"q0"}}.Encode()]
	client.mu.Unlock()
	if cached != maxCacheEntries {
		t.Fatalf("cache = %d entries, want the cap %d", cached, maxCacheEntries)
	}
	if oldestCached {
		t.Fatal("oldest entry should have been evicted")
	}

	// The evicted entry is refetched; the newest one is still served
	// from the cache.
	if _, err := client.SearchReleases(context.Background(), "q0"); err != nil {
		t.Fatalf("refetch: %v", err)
	}
	before := hits.Load()
	if _, err := client.SearchReleases(context.Background(), fmt.Sprintf("q%d", last)); err != nil {
		t.Fatalf("newest: %v", err)
	}
	if got := hits.Load(); got != before {
		t.Fatalf("hits went %d -> %d, newest entry should be cached", before, got)
	}
}
