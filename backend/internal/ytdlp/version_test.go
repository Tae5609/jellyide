package ytdlp

import "testing"

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"", "2025.1.15", true},           // no binary in use: any release is newer
		{"2025.1.15", "2025.1.15", false}, // equal is not newer
		{"2025.1.15", "2025.1.16", true},
		{"2025.1.15", "2024.12.31", false}, // older release: no downgrade
		{"2025.1.15", "2025.2.1", true},
		{"2025.2.1", "2025.1.15", false},
		{"2025.1.15.532", "2025.1.15", false}, // nightly build vs same stable base
		{"2025.1.15", "2025.1.15.532", true},  // nightly build is newer
		{"2025.1.15.532", "2025.1.15.9", false},
		{"2025.1.15.9", "2025.1.15.532", true},
		{"2025.1.15", "", false}, // nothing to compare against
		{"", "", false},
		// Unparseable versions: a garbage current never stalls an update,
		// an unverifiable latest never triggers one.
		{"not-a-version", "2025.1.15", true},
		{"2025.1.15", "not-a-release", false},
		{"2025.01.15", "2025.1.15", false}, // same number, different padding
	}
	for _, tt := range tests {
		if got := IsNewer(tt.current, tt.latest); got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}
