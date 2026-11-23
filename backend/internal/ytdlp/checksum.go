package ytdlp

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrChecksumMismatch is returned when the downloaded binary does not hash
// to the digest published in SHA2-256SUMS.
var ErrChecksumMismatch = errors.New("sha256 checksum mismatch")

// ErrChecksumMissing is returned when SHA2-256SUMS has no entry for the
// downloaded file name.
var ErrChecksumMissing = errors.New("no checksum entry for the downloaded file")

// parseSHA256Sums parses a SHA2-256SUMS file into filename → lower-case
// hex digest. Comment and blank lines are skipped; the GNU coreutils
// "binary/text mode" prefix on the name is stripped.
func parseSHA256Sums(data []byte) (map[string]string, error) {
	sums := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("malformed sums line: %q", line)
		}
		digest := strings.ToLower(fields[0])
		if len(digest) != sha256.Size*2 {
			return nil, fmt.Errorf("malformed digest in sums line: %q", line)
		}
		name := strings.TrimPrefix(fields[1], "*")
		sums[name] = digest
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read sums: %w", err)
	}
	if len(sums) == 0 {
		return nil, errors.New("empty SHA2-256SUMS file")
	}
	return sums, nil
}

// verifySHA256 streams path through sha256 and compares the digest to
// want in constant time.
func verifySHA256(path, want string) error {
	f, err := os.Open(path) //nolint:gosec // G304 - path is the temp file created inside the managed bin dir
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash %s: %w", filepath.Base(path), err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(got), []byte(strings.ToLower(want))) != 1 {
		return ErrChecksumMismatch
	}
	return nil
}
