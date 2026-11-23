package ytdlp

import (
	"net/http"
	"net/url"
	"testing"
)

func TestIsAllowedHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"github.com", true},
		{"api.github.com", true}, // subdomain of the release API
		{"codeload.github.com", false},
		{"objects.githubusercontent.com", true},
		{"release-assets.githubusercontent.com", true},
		{"GITHUB.COM", true}, // case-insensitive
		{"evil.com", false},
		{"github.com.evil.com", false}, // suffix attack
		{"evilgithub.com", false},
		{"raw.githubusercontent.com", false}, // not in the fixed list
		{"", false},
	}
	for _, tt := range tests {
		if got := isAllowedHost(tt.host); got != tt.want {
			t.Errorf("isAllowedHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestCheckRedirect(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		hops    int
		wantErr bool
	}{
		{
			name:   "first hop to the asset CDN is allowed",
			target: "https://objects.githubusercontent.com/yt-dlp/yt-dlp/xyz/yt-dlp",
		},
		{
			name:   "second asset host is allowed",
			target: "https://release-assets.githubusercontent.com/yt-dlp/yt-dlp/xyz/yt-dlp",
		},
		{
			name:   "subdomain of github.com is allowed",
			target: "https://api.github.com/repos/yt-dlp/yt-dlp/releases/latest",
		},
		{
			name:    "http redirect is rejected",
			target:  "http://objects.githubusercontent.com/yt-dlp",
			wantErr: true,
		},
		{
			name:    "unknown host is rejected",
			target:  "https://evil.com/yt-dlp",
			wantErr: true,
		},
		{
			name:    "suffix-lookalike host is rejected",
			target:  "https://github.com.evil.com/yt-dlp",
			wantErr: true,
		},
		{
			name:    "five hops exhaust the redirect budget",
			target:  "https://github.com/yt-dlp",
			hops:    5,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{URL: mustURL(t, tt.target)}
			via := make([]*http.Request, tt.hops)
			for i := range via {
				via[i] = &http.Request{}
			}
			err := checkRedirect(req, via)
			if tt.wantErr && err == nil {
				t.Fatalf("checkRedirect(%s) = nil, want an error", tt.target)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("checkRedirect(%s) = %v, want nil", tt.target, err)
			}
		})
	}
}
