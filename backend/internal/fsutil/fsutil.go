// Package fsutil provides filesystem helpers that never destroy existing
// data. SafeRename moves files and directories without ever replacing an
// existing target.
package fsutil

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ExistsError is returned by SafeRename when the target path already
// exists. Neither path was touched.
type ExistsError struct {
	Path string
}

func (e *ExistsError) Error() string {
	return fmt.Sprintf("target already exists: %s", e.Path)
}

// SafeRename renames oldPath to newPath without ever overwriting an
// existing file or directory at newPath.
//
//   - When newPath is the same file or directory as oldPath (including
//     case-only renames on case-insensitive filesystems, detected with
//     os.SameFile), it is a no-op and returns nil.
//   - When newPath already exists, it returns *ExistsError and touches
//     neither path.
//
// Files are moved by hard-linking the old name to the new one (the link
// fails atomically with EEXIST when the target appears in the meantime)
// and then removing the old name; until that final remove the data is
// referenced by both names, so even a crash loses no bytes. When hard
// links are unsupported (EXDEV, EPERM) it falls back to creating an
// O_CREATE|O_EXCL placeholder at newPath and renaming onto it. The
// placeholder is an empty file, so nothing pre-existing is replaced;
// between the placeholder creation and the rename there is a small window
// in which newPath briefly holds an empty file.
//
// Directories use renameat2(RENAME_NOREPLACE) on Linux, which fails
// atomically with EEXIST; on other platforms (or when renameat2 is not
// available) it reserves the target with an exclusive os.Mkdir and
// renames onto the empty placeholder directory (same small window, and
// the placeholder is removed when the rename fails).
func SafeRename(oldPath, newPath string) error {
	old, err := os.Lstat(oldPath)
	if err != nil {
		return err
	}
	newInfo, err := os.Lstat(newPath)
	if err == nil {
		if os.SameFile(old, newInfo) {
			return nil // same entry, e.g. a case-only rename
		}
		return &ExistsError{Path: newPath}
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if old.IsDir() {
		return safeRenameDir(oldPath, newPath)
	}
	return safeRenameFile(oldPath, newPath)
}

// safeRenameFile moves one file (see SafeRename for the guarantees).
func safeRenameFile(oldPath, newPath string) error {
	err := os.Link(oldPath, newPath)
	if err == nil {
		// The data now has two names; drop the old one. A failure here
		// leaves both names in place, so no data is lost.
		return os.Remove(oldPath)
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return err
	}
	switch errno {
	case syscall.EEXIST:
		return &ExistsError{Path: newPath}
	case syscall.EXDEV, syscall.EPERM, syscall.ENOTSUP:
		return safeRenameFileFallback(oldPath, newPath)
	default:
		return err
	}
}

// safeRenameFileFallback is the no-hard-links path of safeRenameFile:
// reserve the target with O_CREATE|O_EXCL, then rename onto the empty
// placeholder (see SafeRename for the documented small window).
func safeRenameFileFallback(oldPath, newPath string) error {
	ph, err := os.OpenFile(newPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // G304 - newPath is built from the music dir plus a sanitized name
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return &ExistsError{Path: newPath}
		}
		return err
	}
	if cerr := ph.Close(); cerr != nil {
		_ = os.Remove(newPath)
		return cerr
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		// Only the just-created empty placeholder can be at newPath
		// here; clean it up.
		_ = os.Remove(newPath)
		return err
	}
	return nil
}

// safeRenameDirPortable is the non-renameat2 path of safeRenameDir:
// reserve the target with an exclusive Mkdir, then rename onto the empty
// placeholder directory (cleaned up when the rename fails).
func safeRenameDirPortable(oldPath, newPath string) error {
	if err := os.Mkdir(newPath, 0o750); err != nil { //nolint:gosec // G304 - newPath is built from the music dir plus a sanitized name
		if errors.Is(err, os.ErrExist) {
			return &ExistsError{Path: newPath}
		}
		return err
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		// The rename only succeeds against an empty directory, so the
		// placeholder is still empty and can be removed.
		_ = os.Remove(newPath)
		return err
	}
	return nil
}
