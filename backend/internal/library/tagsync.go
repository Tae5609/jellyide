package library

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// CanonicalTagFields are the ten tag fields JellyIDE tracks for sync
// purposes, in canonical (sorted) order. They match ALL_FIELDS in
// update_metadata.py and the ffprobe keys the scanner parses.
var CanonicalTagFields = []string{
	"album_artist",
	"album",
	"artist",
	"comment",
	"composer",
	"disc_number",
	"genre",
	"title",
	"track_number",
	"year",
}

// tagIntFields are the canonical fields whose normalized form is an int.
var tagIntFields = map[string]bool{
	"disc_number":  true,
	"track_number": true,
	"year":         true,
}

// TagValues is the normalized tag map: exactly one entry per canonical
// field. Text fields are "" when unset, int fields are 0 when unset, so a
// value read from the file, from a probe or from a DB row is comparable
// regardless of its origin (NULL, "", 0 and "0" all normalize together).
type TagValues map[string]interface{}

// IsCanonicalTagField reports whether field is one of the ten canonical
// tag fields (the editable fields and the synced ones are the same set).
func IsCanonicalTagField(field string) bool {
	for _, f := range CanonicalTagFields {
		if f == field {
			return true
		}
	}
	return false
}

// NormalizeTagValues reduces raw tag values to the canonical shape:
// unknown fields are dropped, missing text becomes "", missing ints
// become 0, numeric text ("3", "3/12") is parsed to int, empty text and
// whitespace-only strings stay as read (trimming is the caller's job).
// This is the normalization the scanner, the Python --read helper and the
// DB-row side all share, so TagsHash is comparable across all three.
func NormalizeTagValues(raw map[string]interface{}) TagValues {
	out := make(TagValues, len(CanonicalTagFields))
	for _, field := range CanonicalTagFields {
		value, ok := raw[field]
		if !ok || value == nil {
			if tagIntFields[field] {
				out[field] = 0
			} else {
				out[field] = ""
			}
			continue
		}
		if tagIntFields[field] {
			if n, ok := toIntValue(value); ok {
				out[field] = n
			} else {
				out[field] = 0
			}
			continue
		}
		if s, ok := value.(string); ok {
			out[field] = s
		} else {
			out[field] = fmt.Sprint(value)
		}
	}
	return out
}

// toIntValue parses a JSON-decoded (or Go-native) number or numeric text
// to a non-negative int. "3/12" style values take the first part.
func toIntValue(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, v >= 0
	case int64:
		return int(v), v >= 0
	case float64:
		if v != float64(int64(v)) || v < 0 {
			return 0, false
		}
		return int(v), true
	case string:
		s := v
		if i := indexOfSlash(s); i >= 0 {
			s = s[:i]
		}
		n := 0
		found := false
		for _, r := range trimSpace(s) {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
			found = true
		}
		if !found {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func indexOfSlash(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

// TagsHash is the sha256 hex digest of the normalized values as JSON.
// encoding/json sorts map keys, so the digest is stable for one value set.
func TagsHash(values TagValues) string {
	raw, err := json.Marshal(values)
	if err != nil {
		// A TagValues map only holds strings and ints; marshalling cannot
		// fail. Treat it as "unknown" rather than crashing the scan.
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// TrackTagValues builds the normalized tag map of a DB row. albumTitle is
// the title of the album the row belongs to (the file's "album" tag).
func TrackTagValues(t *Track, albumTitle string) TagValues {
	text := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	intf := func(p *int) int {
		if p == nil {
			return 0
		}
		return *p
	}
	return TagValues{
		"title":        t.Title,
		"album":        albumTitle,
		"artist":       text(t.Artist),
		"album_artist": text(t.AlbumArtist),
		"track_number": intf(t.TrackNumber),
		"disc_number":  intf(t.DiscNumber),
		"year":         intf(t.Year),
		"genre":        text(t.Genre),
		"composer":     text(t.Composer),
		"comment":      text(t.Comment),
	}
}

// TagValueEqual compares two raw (pre-normalization) values of one field.
// For int fields "3", "3/12", 3 and 3.0 all compare equal, and missing
// (nil) equals 0 / "".
func TagValueEqual(field string, a, b interface{}) bool {
	na, nb := NormalizeTagValues(map[string]interface{}{field: a}),
		NormalizeTagValues(map[string]interface{}{field: b})
	return na[field] == nb[field]
}

// DifferingFields returns the canonical fields (sorted) whose values
// differ between two normalized tag maps.
func DifferingFields(a, b TagValues) []string {
	diff := []string{}
	for _, field := range CanonicalTagFields {
		if a[field] != b[field] {
			diff = append(diff, field)
		}
	}
	sort.Strings(diff)
	return diff
}

// TagValueText renders one normalized field value the way conflict rows
// and history rows store it (ints as decimal text, "" stays "").
func TagValueText(field string, value interface{}) string {
	if tagIntFields[field] {
		if n, ok := value.(int); ok {
			if n == 0 {
				return ""
			}
			return fmt.Sprint(n)
		}
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
