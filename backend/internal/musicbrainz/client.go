// Package musicbrainz is a small client for the MusicBrainz web service.
// All requests share one rate limiter (1 request/second by default), use a
// 10 second timeout, and responses are cached in memory for 10 minutes.
package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBaseURL is the public MusicBrainz web service.
	DefaultBaseURL = "https://musicbrainz.org/ws/2"
	// UserAgent is the identifier MusicBrainz asks clients to send.
	UserAgent = "JellyIDE/1.0 ( https://github.com/Tae5609/jellyide )"

	defaultRatePerSecond = 1
	defaultCacheTTL      = 10 * time.Minute
	defaultTimeout       = 10 * time.Second
	maxResponseBytes     = 10 << 20
)

// Client talks to the MusicBrainz web service.
type Client struct {
	base     string
	http     *http.Client
	limiter  *tokenBucket
	cacheTTL time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	data      []byte
	expiresAt time.Time
}

// NewClient builds a client for the given base URL with the production
// limits: 1 request/second, 10s timeout, 10-minute response cache.
func NewClient(baseURL string) *Client {
	return newClient(baseURL, defaultRatePerSecond, defaultCacheTTL)
}

func newClient(baseURL string, ratePerSecond float64, cacheTTL time.Duration) *Client {
	return &Client{
		base:     strings.TrimRight(baseURL, "/"),
		http:     &http.Client{Timeout: defaultTimeout},
		limiter:  newTokenBucket(ratePerSecond),
		cacheTTL: cacheTTL,
		cache:    map[string]cacheEntry{},
	}
}

// tokenBucket is a capacity-1 refill bucket shared by all requests of the
// client.
type tokenBucket struct {
	mu     sync.Mutex
	tokens float64
	rate   float64 // tokens per second
	last   time.Time
}

func newTokenBucket(ratePerSecond float64) *tokenBucket {
	return &tokenBucket{tokens: 1, rate: ratePerSecond, last: time.Now()}
}

// Wait blocks until one token is available.
func (b *tokenBucket) Wait() {
	for {
		b.mu.Lock()
		now := time.Now()
		b.tokens = math.Min(1, b.tokens+now.Sub(b.last).Seconds()*b.rate)
		b.last = now
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return
		}
		wait := time.Duration(float64(time.Second) * (1 - b.tokens) / b.rate)
		b.mu.Unlock()
		time.Sleep(wait)
	}
}

// do fetches one web service path, serving it from the cache when fresh.
func (c *Client) do(ctx context.Context, pathAndQuery string) ([]byte, error) {
	key := c.base + pathAndQuery

	c.mu.Lock()
	if entry, ok := c.cache[key]; ok && time.Now().Before(entry.expiresAt) {
		c.mu.Unlock()
		return entry.data, nil
	}
	c.mu.Unlock()

	c.limiter.Wait()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, key, nil)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("musicbrainz: unexpected status %d", resp.StatusCode)
	}

	c.mu.Lock()
	c.cache[key] = cacheEntry{data: body, expiresAt: time.Now().Add(c.cacheTTL)}
	c.mu.Unlock()
	return body, nil
}

// ReleaseSearchResult is one row of the /metadata/lookup/search response.
type ReleaseSearchResult struct {
	MBID       string  `json:"mbid"`
	Title      string  `json:"title"`
	Artist     string  `json:"artist"`
	Date       string  `json:"date"`
	Country    string  `json:"country"`
	TrackCount int     `json:"track_count"`
	Score      float64 `json:"score"`
}

// SearchReleases searches the release index, returning at most 10 results.
func (c *Client) SearchReleases(ctx context.Context, query string) ([]ReleaseSearchResult, error) {
	vals := url.Values{}
	vals.Set("query", query)
	vals.Set("fmt", "json")
	vals.Set("limit", "10")
	data, err := c.do(ctx, "/release?"+vals.Encode())
	if err != nil {
		return nil, err
	}

	var payload struct {
		Releases []struct {
			ID           string `json:"id"`
			Title        string `json:"title"`
			ArtistCredit []struct {
				Name string `json:"name"`
			} `json:"artist-credit"`
			Date       string `json:"date"`
			Country    string `json:"country"`
			TrackCount int    `json:"track-count"`
			Score      int    `json:"score"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("musicbrainz: parse search: %w", err)
	}

	results := make([]ReleaseSearchResult, 0, len(payload.Releases))
	for _, rel := range payload.Releases {
		names := make([]string, 0, len(rel.ArtistCredit))
		for _, credit := range rel.ArtistCredit {
			if credit.Name != "" {
				names = append(names, credit.Name)
			}
		}
		results = append(results, ReleaseSearchResult{
			MBID:       rel.ID,
			Title:      rel.Title,
			Artist:     strings.Join(names, ", "),
			Date:       rel.Date,
			Country:    rel.Country,
			TrackCount: rel.TrackCount,
			Score:      float64(rel.Score),
		})
	}
	return results, nil
}

// ReleaseTrack is one track of a release, flattened from its media.
type ReleaseTrack struct {
	Disc   int
	Number int
	Title  string
}

// Release is the subset of the MusicBrainz release document used for the
// lookup response and the track mapping.
type Release struct {
	MBID   string
	Title  string
	Artist string
	Date   string
	TopTag string
	Tracks []ReleaseTrack
	Raw    map[string]interface{}
}

// GetRelease fetches a release with its recordings and tags.
func (c *Client) GetRelease(ctx context.Context, mbid string) (*Release, error) {
	vals := url.Values{}
	vals.Set("inc", "recordings+tags")
	vals.Set("fmt", "json")
	data, err := c.do(ctx, "/release/"+url.PathEscape(mbid)+"?"+vals.Encode())
	if err != nil {
		return nil, err
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("musicbrainz: parse release: %w", err)
	}
	var doc struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		Date         string `json:"date"`
		ArtistCredit []struct {
			Name string `json:"name"`
		} `json:"artist-credit"`
		Tags []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"tags"`
		Media []struct {
			Position int `json:"position"`
			Tracks   []struct {
				Number    string `json:"number"`
				Title     string `json:"title"`
				Recording *struct {
					Title string `json:"title"`
				} `json:"recording"`
			} `json:"tracks"`
		} `json:"media"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("musicbrainz: parse release: %w", err)
	}

	names := make([]string, 0, len(doc.ArtistCredit))
	for _, credit := range doc.ArtistCredit {
		if credit.Name != "" {
			names = append(names, credit.Name)
		}
	}
	sort.SliceStable(doc.Tags, func(i, j int) bool { return doc.Tags[i].Count > doc.Tags[j].Count })

	release := &Release{
		MBID:   doc.ID,
		Title:  doc.Title,
		Artist: strings.Join(names, ", "),
		Date:   doc.Date,
		Raw:    raw,
	}
	if len(doc.Tags) > 0 {
		release.TopTag = doc.Tags[0].Name
	}
	for _, medium := range doc.Media {
		for _, track := range medium.Tracks {
			title := track.Title
			if title == "" && track.Recording != nil {
				title = track.Recording.Title
			}
			release.Tracks = append(release.Tracks, ReleaseTrack{
				Disc:   medium.Position,
				Number: parseNumber(track.Number),
				Title:  title,
			})
		}
	}
	return release, nil
}

// parseNumber extracts the leading integer of a MusicBrainz track number
// ("3", "3/12"); it returns 0 when there is none.
func parseNumber(value string) int {
	value = strings.TrimSpace(value)
	if i := strings.IndexByte(value, '/'); i >= 0 {
		value = value[:i]
	}
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// ParseYear takes the first four-digit year of a MusicBrainz date
// ("" when none).
func ParseYear(value string) int {
	for i := 0; i+4 <= len(value); i++ {
		if isDigit(value[i]) && isDigit(value[i+1]) && isDigit(value[i+2]) && isDigit(value[i+3]) {
			n, err := strconv.Atoi(value[i : i+4])
			if err != nil {
				return 0
			}
			return n
		}
	}
	return 0
}

func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// LocalTrack is one local album track for the mapping.
type LocalTrack struct {
	TrackID   string
	Title     string
	Disc      int
	Number    int
	HasNumber bool
	Current   map[string]interface{}
}

// MappingEntry pairs one local track with its proposed values.
type MappingEntry struct {
	TrackID    string                 `json:"track_id"`
	Current    map[string]interface{} `json:"current"`
	Proposed   map[string]interface{} `json:"proposed"`
	Confidence float64                `json:"confidence"`
}

// MapTracks matches local tracks to release tracks: exact disc/track
// number first (confidence 1), then best normalized-title similarity
// (Levenshtein ratio). Entries keep the local track order.
func MapTracks(release *Release, local []LocalTrack) []MappingEntry {
	used := make([]bool, len(release.Tracks))
	results := make([]MappingEntry, len(local))
	have := make([]bool, len(local))

	// Pass 1: disc/track number match.
	for i, lt := range local {
		if !lt.HasNumber {
			continue
		}
		for j, rt := range release.Tracks {
			if used[j] {
				continue
			}
			if rt.Number != lt.Number {
				continue
			}
			// A known local disc must match; an unknown one matches any.
			if lt.Disc != 0 && rt.Disc != lt.Disc {
				continue
			}
			used[j] = true
			results[i] = buildEntry(lt, rt, release, 1)
			have[i] = true
			break
		}
	}

	// Pass 2: best title similarity among the remaining release tracks.
	for i, lt := range local {
		if have[i] {
			continue
		}
		best, bestScore := -1, -1.0
		for j, rt := range release.Tracks {
			if used[j] {
				continue
			}
			score := TitleSimilarity(lt.Title, rt.Title)
			if score > bestScore {
				best, bestScore = j, score
			}
		}
		if best < 0 {
			continue
		}
		used[best] = true
		results[i] = buildEntry(lt, release.Tracks[best], release, bestScore)
		have[i] = true
	}

	out := make([]MappingEntry, 0, len(local))
	for i, entry := range results {
		if have[i] {
			out = append(out, entry)
		}
	}
	return out
}

func buildEntry(lt LocalTrack, rt ReleaseTrack, release *Release, confidence float64) MappingEntry {
	proposed := map[string]interface{}{}
	if rt.Title != "" {
		proposed["title"] = rt.Title
	}
	if release.Artist != "" {
		proposed["artist"] = release.Artist
		proposed["album_artist"] = release.Artist
	}
	if year := ParseYear(release.Date); year > 0 {
		proposed["year"] = year
	}
	if release.TopTag != "" {
		proposed["genre"] = release.TopTag
	}
	current := lt.Current
	if current == nil {
		current = map[string]interface{}{}
	}
	return MappingEntry{
		TrackID:    lt.TrackID,
		Current:    current,
		Proposed:   proposed,
		Confidence: roundTo(confidence, 4),
	}
}

func roundTo(v float64, digits int) float64 {
	switch digits {
	case 4:
		return math.Round(v*10000) / 10000
	default:
		return v
	}
}

// NormalizeTitle lower-cases a title and strips everything that is not a
// letter or digit.
func NormalizeTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TitleSimilarity is the Levenshtein ratio of the two normalized titles,
// in [0,1] (1 means identical).
func TitleSimilarity(a, b string) float64 {
	na, nb := NormalizeTitle(a), NormalizeTitle(b)
	if na == nb {
		if na == "" {
			return 0
		}
		return 1
	}
	dist := levenshtein([]rune(na), []rune(nb))
	maxLen := len(na)
	if len(nb) > maxLen {
		maxLen = len(nb)
	}
	if maxLen == 0 {
		return 0
	}
	return 1 - float64(dist)/float64(maxLen)
}

func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
