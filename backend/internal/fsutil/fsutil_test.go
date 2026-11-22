package fsutil

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304 - path is built from t.TempDir()
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be gone, stat err = %v", path, err)
	}
}

func TestSafeRenameFileToFreeTarget(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.flac")
	dst := filepath.Join(dir, "dst.flac")
	writeFile(t, src, "audio one")

	if err := SafeRename(src, dst); err != nil {
		t.Fatalf("SafeRename: %v", err)
	}
	if got := readFile(t, dst); !bytes.Equal(got, []byte("audio one")) {
		t.Fatalf("dst content = %q, want the source bytes", got)
	}
	mustNotExist(t, src)
}

func TestSafeRenameFileTargetExistsKeepsBothFiles(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.flac")
	dst := filepath.Join(dir, "dst.flac")
	writeFile(t, src, "audio one")
	writeFile(t, dst, "audio two")

	err := SafeRename(src, dst)
	var exists *ExistsError
	if !errors.As(err, &exists) {
		t.Fatalf("error = %v, want *ExistsError", err)
	}
	if exists.Path != dst {
		t.Fatalf("ExistsError.Path = %q, want %q", exists.Path, dst)
	}
	// Neither file was touched: byte-identical contents, both present.
	if got := readFile(t, src); !bytes.Equal(got, []byte("audio one")) {
		t.Fatalf("src content changed: %q", got)
	}
	if got := readFile(t, dst); !bytes.Equal(got, []byte("audio two")) {
		t.Fatalf("dst content changed: %q", got)
	}
}

func TestSafeRenameSamePathIsNoOp(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "same.flac")
	writeFile(t, src, "audio")

	// The same path twice (and, on a case-insensitive filesystem, a
	// case-only rename) must be a no-op, not a collision.
	if err := SafeRename(src, src); err != nil {
		t.Fatalf("SafeRename(same, same): %v", err)
	}
	if got := readFile(t, src); !bytes.Equal(got, []byte("audio")) {
		t.Fatalf("content changed: %q", got)
	}
}

func TestSafeRenameFileOntoExistingDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.flac")
	writeFile(t, src, "audio")
	dst := filepath.Join(dir, "adir")
	if err := os.Mkdir(dst, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	err := SafeRename(src, dst)
	var exists *ExistsError
	if !errors.As(err, &exists) {
		t.Fatalf("error = %v, want *ExistsError", err)
	}
	mustExist(t, src)
	mustExist(t, dst)
}

func TestSafeRenameDirToFreeTarget(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Old Album")
	writeFile(t, filepath.Join(src, "One.flac"), "one")
	writeFile(t, filepath.Join(src, "Two.flac"), "two")
	dst := filepath.Join(dir, "New Album")

	if err := SafeRename(src, dst); err != nil {
		t.Fatalf("SafeRename: %v", err)
	}
	mustNotExist(t, src)
	if got := readFile(t, filepath.Join(dst, "One.flac")); !bytes.Equal(got, []byte("one")) {
		t.Fatalf("One.flac content = %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "Two.flac")); !bytes.Equal(got, []byte("two")) {
		t.Fatalf("Two.flac content = %q", got)
	}
}

func TestSafeRenameDirToExistingEmptyDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Old")
	writeFile(t, filepath.Join(src, "One.flac"), "one")
	dst := filepath.Join(dir, "Taken")
	if err := os.Mkdir(dst, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	err := SafeRename(src, dst)
	var exists *ExistsError
	if !errors.As(err, &exists) {
		t.Fatalf("error = %v, want *ExistsError", err)
	}
	// The source directory is fully intact.
	if got := readFile(t, filepath.Join(src, "One.flac")); !bytes.Equal(got, []byte("one")) {
		t.Fatalf("One.flac content = %q", got)
	}
	mustExist(t, dst)
}

func TestSafeRenameDirToExistingNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Old")
	writeFile(t, filepath.Join(src, "One.flac"), "one")
	dst := filepath.Join(dir, "Occupied")
	writeFile(t, filepath.Join(dst, "Other.flac"), "other")

	err := SafeRename(src, dst)
	var exists *ExistsError
	if !errors.As(err, &exists) {
		t.Fatalf("error = %v, want *ExistsError", err)
	}
	if got := readFile(t, filepath.Join(src, "One.flac")); !bytes.Equal(got, []byte("one")) {
		t.Fatalf("One.flac content = %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "Other.flac")); !bytes.Equal(got, []byte("other")) {
		t.Fatalf("Other.flac content = %q", got)
	}
}

// TestSafeRenameConcurrentSameTarget races two renames onto the same
// target: exactly one may win, and no file may be lost.
func TestSafeRenameConcurrentSameTarget(t *testing.T) {
	for round := 0; round < 25; round++ {
		t.Run(fmt.Sprintf("round%02d", round), func(t *testing.T) {
			dir := t.TempDir()
			srcA := filepath.Join(dir, "A.flac")
			srcB := filepath.Join(dir, "B.flac")
			dst := filepath.Join(dir, "T.flac")
			writeFile(t, srcA, "content A")
			writeFile(t, srcB, "content B")

			start := make(chan struct{})
			errs := make([]error, 2)
			var wg sync.WaitGroup
			for i, src := range []string{srcA, srcB} {
				wg.Add(1)
				go func(i int, src string) {
					defer wg.Done()
					<-start
					errs[i] = SafeRename(src, dst)
				}(i, src)
			}
			close(start)
			wg.Wait()

			wins := 0
			for _, err := range errs {
				switch err {
				case nil:
					wins++
				default:
					var exists *ExistsError
					if !errors.As(err, &exists) {
						t.Fatalf("loser error = %v, want *ExistsError", err)
					}
				}
			}
			if wins != 1 {
				t.Fatalf("winners = %d, want exactly 1 (errs = %v)", wins, errs)
			}

			// The winner's content is at the target, byte-identical...
			winner, wantContent := srcA, "content A"
			if errs[0] != nil {
				winner, wantContent = srcB, "content B"
			}
			if got := readFile(t, dst); !bytes.Equal(got, []byte(wantContent)) {
				t.Fatalf("target content = %q, want %q", got, wantContent)
			}
			// ...the loser's file is untouched, the winner's old name gone.
			loser := srcB
			if errs[1] == nil {
				loser = srcA
			}
			if got := readFile(t, loser); len(got) == 0 {
				t.Fatal("loser's file lost its content")
			}
			mustNotExist(t, winner)
		})
	}
}
