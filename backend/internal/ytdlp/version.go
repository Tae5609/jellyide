package ytdlp

import (
	"strconv"
	"strings"
)

// parseVersion splits a yt-dlp version like "2025.01.15" or the nightly
// "2025.01.15.532" into its numeric fields. It returns nil when a field is
// not a plain integer.
func parseVersion(v string) []int {
	fields := strings.Split(strings.TrimSpace(v), ".")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

// IsNewer reports whether latest is a strictly newer yt-dlp version than
// current. An empty current means no binary in use (any release is
// newer); an empty latest means nothing to compare against. An
// unparseable current (garbage --version output) is treated as older than
// anything, so an update is still allowed; an unparseable latest is
// refused, since it cannot be verified as a real release.
func IsNewer(current, latest string) bool {
	if latest == "" {
		return false
	}
	if current == "" {
		return true
	}
	cur := parseVersion(current)
	lat := parseVersion(latest)
	if lat == nil {
		return false
	}
	if cur == nil {
		return true
	}
	for i := 0; i < len(lat); i++ {
		c := 0
		if i < len(cur) {
			c = cur[i]
		}
		if lat[i] != c {
			return lat[i] > c
		}
	}
	// All shared fields are equal: the longer version carries the extra
	// (nightly build) field and is therefore newer.
	return len(lat) > len(cur)
}
