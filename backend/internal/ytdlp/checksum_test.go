package ytdlp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Digests for the fixture sums file (64 hex chars each).
const (
	fixtureA = "aa00000000000000000000000000000000000000000000000000000000000000"
	fixtureB = "bb00000000000000000000000000000000000000000000000000000000000000"
	fixtureC = "cc00000000000000000000000000000000000000000000000000000000000000"
)

func TestParseSHA256Sums(t *testing.T) {
	valid := fixtureA + "  yt-dlp\n"
	valid += fixtureB + "  yt-dlp.exe\n"
	valid += "\n# a comment line\n"
	valid += fixtureC + "  *yt-dlp.bat\n"

	sums, err := parseSHA256Sums([]byte(valid))
	if err != nil {
		t.Fatalf("parseSHA256Sums: %v", err)
	}
	if len(sums) != 3 {
		t.Fatalf("entries = %d, want 3: %v", len(sums), sums)
	}
	if sums["yt-dlp"] != fixtureA {
		t.Fatalf("yt-dlp digest = %q, want %q", sums["yt-dlp"], fixtureA)
	}
	if _, ok := sums["*yt-dlp.bat"]; ok {
		t.Fatal("the binary-mode prefix must be stripped from the name")
	}
	if sums["yt-dlp.bat"] != fixtureC {
		t.Fatalf("yt-dlp.bat digest = %q, want %q", sums["yt-dlp.bat"], fixtureC)
	}
}

func TestParseSHA256SumsRejectsMalformed(t *testing.T) {
	for name, in := range map[string]string{
		"empty":        "",
		"comments":     "# nothing\n",
		"one field":    fixtureA + "\n",
		"three fields": fixtureA + " yt-dlp extra\n",
		"short digest": "beef  yt-dlp\n",
	} {
		if _, err := parseSHA256Sums([]byte(in)); err == nil {
			t.Errorf("%s: wanted an error", name)
		}
	}
	// Uppercase digests are accepted and normalised to lower case.
	sums, err := parseSHA256Sums([]byte("AA" + "00000000000000000000000000000000000000000000000000000000000000  yt-dlp\n"))
	if err != nil {
		t.Fatalf("uppercase digest: %v", err)
	}
	if sums["yt-dlp"] != fixtureA {
		t.Fatalf("digest = %q, want the lower-cased %q", sums["yt-dlp"], fixtureA)
	}
}

func TestVerifySHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "yt-dlp")
	content := []byte("fake zipapp content")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	sum := sha256.Sum256(content)
	good := hex.EncodeToString(sum[:])

	if err := verifySHA256(path, good); err != nil {
		t.Fatalf("verifySHA256 with matching digest = %v, want nil", err)
	}
	// Comparison must be case-insensitive on the expected digest.
	if err := verifySHA256(path, strings.ToUpper(good)); err != nil {
		t.Fatalf("verifySHA256 with upper-case digest = %v, want nil", err)
	}
	bad := "0000000000000000000000000000000000000000000000000000000000000000"
	if err := verifySHA256(path, bad); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("verifySHA256 with wrong digest = %v, want ErrChecksumMismatch", err)
	}
	if err := verifySHA256(filepath.Join(dir, "missing"), good); err == nil {
		t.Fatal("verifySHA256 on a missing file = nil, want an error")
	}
}
