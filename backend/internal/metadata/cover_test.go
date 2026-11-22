package metadata

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
)

func makePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 30, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func makeJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// oversizedPNGHeader builds a PNG whose IHDR claims width x height; only
// the header is present, which is all image.DecodeConfig needs.
func oversizedPNGHeader(t *testing.T, width, height uint32) []byte {
	t.Helper()
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8] = 8  // bit depth
	ihdr[9] = 2  // color type: truecolor
	ihdr[10] = 0 // compression
	ihdr[11] = 0 // filter
	ihdr[12] = 0 // interlace

	var out []byte
	out = append(out, 0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], 13)
	out = append(out, lenBuf[:]...)
	out = append(out, 'I', 'H', 'D', 'R')
	out = append(out, ihdr[:]...)
	crc := crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr[:]...))
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], crc)
	out = append(out, crcBuf[:]...)
	// Pad so the body is large enough to be a plausible (truncated) file.
	return append(out, bytes.Repeat([]byte{0x00}, 64)...)
}

func TestValidateCoverData(t *testing.T) {
	cases := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{"valid png", makePNG(t, 16, 16), ""},
		{"valid jpeg", makeJPEG(t), ""},
		{"wrong type gif", append([]byte("GIF89a"), bytes.Repeat([]byte{0}, 64)...), "unsupported content type"},
		{"not an image", bytes.Repeat([]byte{'x'}, 32), "unsupported content type"},
		{"oversized dimensions", oversizedPNGHeader(t, 5000, 5000), "maximum is"},
		{"tall oversized dimensions", oversizedPNGHeader(t, 10, 4001), "maximum is"},
		// Signature plus a truncated IHDR: sniffs as PNG but does not decode.
		{"broken png", append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte("junk")...), "invalid image"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			mime, err := validateCoverData(tt.data)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateCoverData: %v", err)
				}
				if mime == "" {
					t.Fatal("empty content type")
				}
				return
			}
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestReadCoverBodyLimit(t *testing.T) {
	small := bytes.Repeat([]byte{'a'}, 100)
	data, err := readCoverBody(bytes.NewReader(small))
	if err != nil || len(data) != 100 {
		t.Fatalf("readCoverBody small: %v len=%d", err, len(data))
	}

	big := bytes.Repeat([]byte{'a'}, MaxCoverBytes+1)
	if _, err := readCoverBody(bytes.NewReader(big)); err == nil {
		t.Fatal("want error for an oversized body")
	}
}

func seedAlbumWithTracks(t *testing.T, lib *library.Service, dir string, n int) (string, []string, string) {
	t.Helper()
	artistID, err := lib.EnsureArtist("Artist")
	if err != nil {
		t.Fatalf("EnsureArtist: %v", err)
	}
	albumID, err := lib.EnsureAlbum(artistID, "Album")
	if err != nil {
		t.Fatalf("EnsureAlbum: %v", err)
	}
	var paths []string
	for i := 0; i < n; i++ {
		title := "Song " + string(rune('A'+i))
		path := filepath.Join(dir, "Artist", "Album", title+".flac")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := lib.UpsertTrack(albumID, title, path, 60, 900, library.TrackMeta{}); err != nil {
			t.Fatalf("UpsertTrack: %v", err)
		}
		paths = append(paths, path)
	}
	return albumID, paths, filepath.Join(dir, "Artist", "Album")
}

func TestUploadCoverStoresAndEmbeds(t *testing.T) {
	svc, lib, jm, stub, dir := newJobService(t)
	albumID, trackPaths, albumDir := seedAlbumWithTracks(t, lib, dir, 2)

	pngData := makePNG(t, 16, 16)
	jobID, coverURL, err := svc.UploadCover(albumID, bytes.NewReader(pngData))
	if err != nil {
		t.Fatalf("UploadCover: %v", err)
	}
	if coverURL != "/media/Artist/Album/cover.png" {
		t.Fatalf("cover url = %q", coverURL)
	}
	waitForJob(t, jm, jobID, jobs.StatusCompleted)

	// File stored in the album dir...
	coverPath := filepath.Join(albumDir, "cover.png")
	//nolint:gosec // G304 - test path under t.TempDir()
	stored, err := os.ReadFile(coverPath)
	if err != nil {
		t.Fatalf("read cover: %v", err)
	}
	if !bytes.Equal(stored, pngData) {
		t.Fatal("stored cover differs from upload")
	}
	// ...and the album row points at it.
	album, _ := lib.GetAlbum(albumID)
	if album.CoverArt != coverURL {
		t.Fatalf("cover_art = %q, want %q", album.CoverArt, coverURL)
	}
	// Embedded into every track.
	if len(stub.embedCalls) != 2 {
		t.Fatalf("embedCalls = %v, want one per track", stub.embedCalls)
	}
	for _, p := range trackPaths {
		found := false
		for _, c := range stub.embedCalls {
			if c == p {
				found = true
			}
		}
		if !found {
			t.Fatalf("embedCalls missing track %s", p)
		}
	}
}

func TestUploadCoverValidationErrors(t *testing.T) {
	svc, lib, _, _, dir := newJobService(t)
	albumID, _, _ := seedAlbumWithTracks(t, lib, dir, 1)

	cases := []struct {
		name string
		data []byte
	}{
		{"wrong type", append([]byte("GIF89a"), bytes.Repeat([]byte{0}, 64)...)},
		{"oversized", bytes.Repeat([]byte{'a'}, MaxCoverBytes+1)},
		{"oversized dimensions", oversizedPNGHeader(t, 4001, 10)},
		{"invalid image", append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, []byte("junk")...)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := svc.UploadCover(albumID, bytes.NewReader(tt.data))
			var ve *ValidationError
			if !asValidationError(err, &ve) {
				t.Fatalf("error = %v, want *ValidationError", err)
			}
		})
	}
}

func TestUploadCoverUnknownAlbum(t *testing.T) {
	svc, _, _, _, _ := newJobService(t)
	_, _, err := svc.UploadCover("no-album", bytes.NewReader(makePNG(t, 4, 4)))
	if !asNotFoundError(err) {
		t.Fatalf("error = %v, want *NotFoundError", err)
	}
}

func TestExtractCoverArt(t *testing.T) {
	svc, lib, _, stub, dir := newJobService(t)
	albumID, trackPaths, albumDir := seedAlbumWithTracks(t, lib, dir, 2)

	pngData := makePNG(t, 8, 8)
	// Only the second track carries embedded art.
	stub.extractByPath[trackPaths[1]] = pngData

	coverURL, err := svc.ExtractCoverArt(albumID)
	if err != nil {
		t.Fatalf("ExtractCoverArt: %v", err)
	}
	if coverURL != "/media/Artist/Album/cover.png" {
		t.Fatalf("cover url = %q", coverURL)
	}
	//nolint:gosec // G304 - test path under t.TempDir()
	stored, err := os.ReadFile(filepath.Join(albumDir, "cover.png"))
	if err != nil {
		t.Fatalf("read cover: %v", err)
	}
	if !bytes.Equal(stored, pngData) {
		t.Fatal("extracted cover differs")
	}
	album, _ := lib.GetAlbum(albumID)
	if album.CoverArt != coverURL {
		t.Fatalf("cover_art = %q", album.CoverArt)
	}
}

func TestExtractCoverArtNoneAvailable(t *testing.T) {
	svc, lib, _, _, dir := newJobService(t)
	albumID, _, _ := seedAlbumWithTracks(t, lib, dir, 2)
	if _, err := svc.ExtractCoverArt(albumID); !asNotFoundError(err) {
		t.Fatalf("error = %v, want *NotFoundError", err)
	}
}

// stubRoundTripper scripts HTTP responses for the cover client.
type stubRoundTripper struct {
	fn   func(*http.Request) (*http.Response, error)
	seen []*http.Request
}

func (s *stubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	s.seen = append(s.seen, req)
	if s.fn == nil {
		return nil, io.EOF
	}
	return s.fn(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Header:        make(http.Header),
	}
}

func jpegResponse(data []byte) *http.Response {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Status:        "200 OK",
		Body:          io.NopCloser(bytes.NewReader(data)),
		ContentLength: int64(len(data)),
		Header:        make(http.Header),
	}
}

func redirectResponse(location string) *http.Response {
	resp := jsonResponse(http.StatusFound, "")
	resp.Header.Set("Location", location)
	return resp
}

func newCoverTestService(t *testing.T, rt *stubRoundTripper) (*Service, *library.Service, *jobs.Manager, *stubTagWriter, string) {
	t.Helper()
	svc, lib, jm, stub, dir := newJobService(t)
	svc.httpClient = &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: coverRedirectPolicy,
		Transport:     rt,
	}
	return svc, lib, jm, stub, dir
}

func TestCoverFromURLAllowlist(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{"https allowed", "https://coverartarchive.org/release/1/front", ""},
		{"https archive.org", "https://archive.org/download/x/cover.jpg", ""},
		{"https subdomain", "https://ia801409.us.archive.org/cover.jpg", ""},
		{"http rejected", "http://coverartarchive.org/release/1/front", "https"},
		{"other host", "https://evil.example/cover.jpg", "allowlist"},
		{"suffix lookalike", "https://notarchive.org/cover.jpg", "allowlist"},
		{"missing scheme", "coverartarchive.org/x", "https"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rt := &stubRoundTripper{fn: func(*http.Request) (*http.Response, error) {
				return jpegResponse(makeJPEG(t)), nil
			}}
			svc, lib, jm, _, dir := newCoverTestService(t, rt)
			albumID, _, _ := seedAlbumWithTracks(t, lib, dir, 1)

			jobID, _, err := svc.CoverFromURL(albumID, tt.raw)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("CoverFromURL: %v", err)
				}
				if len(rt.seen) != 1 {
					t.Fatalf("requests = %d, want 1", len(rt.seen))
				}
				waitForJob(t, jm, jobID, jobs.StatusCompleted)
				return
			}
			if err == nil {
				t.Fatal("want error, got nil")
			}
			var ve *ValidationError
			if !asValidationError(err, &ve) {
				t.Fatalf("error = %v (%T), want *ValidationError", err, err)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCoverFromURLRedirects(t *testing.T) {
	jpegData := makeJPEG(t)

	t.Run("redirect to disallowed host rejected", func(t *testing.T) {
		rt := &stubRoundTripper{fn: func(_ *http.Request) (*http.Response, error) {
			return redirectResponse("https://evil.example/stolen.jpg"), nil
		}}
		svc, lib, _, _, dir := newCoverTestService(t, rt)
		albumID, _, _ := seedAlbumWithTracks(t, lib, dir, 1)
		_, _, err := svc.CoverFromURL(albumID, "https://coverartarchive.org/release/1/front")
		if err == nil {
			t.Fatal("want error for redirect to a disallowed host")
		}
		if len(rt.seen) != 1 {
			t.Fatalf("requests = %d, want 1 (redirect not followed)", len(rt.seen))
		}
	})

	t.Run("redirect within allowlist followed", func(t *testing.T) {
		rt := &stubRoundTripper{fn: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.String(), "hop1") {
				return redirectResponse("https://coverartarchive.org/final.jpg"), nil
			}
			return jpegResponse(jpegData), nil
		}}
		svc, lib, jm, _, dir := newCoverTestService(t, rt)
		albumID, _, _ := seedAlbumWithTracks(t, lib, dir, 1)
		jobID, _, err := svc.CoverFromURL(albumID, "https://coverartarchive.org/hop1")
		if err != nil {
			t.Fatalf("CoverFromURL: %v", err)
		}
		if len(rt.seen) != 2 {
			t.Fatalf("requests = %d, want 2", len(rt.seen))
		}
		waitForJob(t, jm, jobID, jobs.StatusCompleted)
	})

	t.Run("too many redirects", func(t *testing.T) {
		rt := &stubRoundTripper{fn: func(_ *http.Request) (*http.Response, error) {
			return redirectResponse("https://coverartarchive.org/next"), nil
		}}
		svc, lib, _, _, dir := newCoverTestService(t, rt)
		albumID, _, _ := seedAlbumWithTracks(t, lib, dir, 1)
		_, _, err := svc.CoverFromURL(albumID, "https://coverartarchive.org/start")
		if err == nil || !strings.Contains(err.Error(), "too many redirects") {
			t.Fatalf("error = %v, want too many redirects", err)
		}
		if len(rt.seen) != 4 {
			t.Fatalf("requests = %d, want 4 (initial + 3 allowed hops)", len(rt.seen))
		}
	})

	t.Run("non-200 status", func(t *testing.T) {
		rt := &stubRoundTripper{fn: func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusNotFound, "nope"), nil
		}}
		svc, lib, _, _, dir := newCoverTestService(t, rt)
		albumID, _, _ := seedAlbumWithTracks(t, lib, dir, 1)
		_, _, err := svc.CoverFromURL(albumID, "https://coverartarchive.org/missing.jpg")
		var ve *ValidationError
		if !asValidationError(err, &ve) || !strings.Contains(err.Error(), "status 404") {
			t.Fatalf("error = %v, want ValidationError status 404", err)
		}
	})

	t.Run("downloaded cover too large", func(t *testing.T) {
		big := bytes.Repeat([]byte{'a'}, MaxCoverBytes+1)
		rt := &stubRoundTripper{fn: func(*http.Request) (*http.Response, error) {
			resp := &http.Response{
				StatusCode:    http.StatusOK,
				Status:        "200 OK",
				Body:          io.NopCloser(bytes.NewReader(big)),
				ContentLength: int64(len(big)),
				Header:        make(http.Header),
			}
			return resp, nil
		}}
		svc, lib, _, _, dir := newCoverTestService(t, rt)
		albumID, _, _ := seedAlbumWithTracks(t, lib, dir, 1)
		_, _, err := svc.CoverFromURL(albumID, "https://coverartarchive.org/big.jpg")
		var ve *ValidationError
		if !asValidationError(err, &ve) || !strings.Contains(err.Error(), "10 MB") {
			t.Fatalf("error = %v, want ValidationError 10 MB", err)
		}
	})

	t.Run("downloaded cover wrong type", func(t *testing.T) {
		gif := append([]byte("GIF89a"), bytes.Repeat([]byte{0}, 64)...)
		rt := &stubRoundTripper{fn: func(*http.Request) (*http.Response, error) {
			resp := &http.Response{
				StatusCode:    http.StatusOK,
				Status:        "200 OK",
				Body:          io.NopCloser(bytes.NewReader(gif)),
				ContentLength: int64(len(gif)),
				Header:        make(http.Header),
			}
			return resp, nil
		}}
		svc, lib, _, _, dir := newCoverTestService(t, rt)
		albumID, _, _ := seedAlbumWithTracks(t, lib, dir, 1)
		_, _, err := svc.CoverFromURL(albumID, "https://coverartarchive.org/x.gif")
		var ve *ValidationError
		if !asValidationError(err, &ve) || !strings.Contains(err.Error(), "unsupported content type") {
			t.Fatalf("error = %v, want unsupported content type", err)
		}
	})
}

func TestValidateCoverURLDirect(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr bool
	}{
		{"https://coverartarchive.org/x", false},
		{"https://archive.org/x", false},
		{"https://sub.archive.org/x", false},
		{"http://coverartarchive.org/x", true},
		{"https://example.org/x", true},
		{"https://notarchive.org/x", true},
		{"ftp://coverartarchive.org/x", true},
	}
	for _, tt := range cases {
		u, err := url.Parse(tt.raw)
		if err != nil {
			t.Fatalf("parse %q: %v", tt.raw, err)
		}
		if got := validateCoverURL(u); (got != nil) != tt.wantErr {
			t.Fatalf("validateCoverURL(%q) error = %v, wantErr %v", tt.raw, got, tt.wantErr)
		}
	}
}

// asValidationError reports whether err is a *ValidationError and fills ve.
func asValidationError(err error, ve **ValidationError) bool {
	return errors.As(err, ve)
}

func asNotFoundError(err error) bool {
	var e *NotFoundError
	return errors.As(err, &e)
}
