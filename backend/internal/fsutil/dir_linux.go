//go:build linux

package fsutil

import (
	"errors"

	"golang.org/x/sys/unix"
)

// safeRenameDir moves a directory without replacing an existing target.
// On Linux renameat2 with RENAME_NOREPLACE fails atomically with EEXIST;
// the portable path covers the failures it does not (e.g. EXDEV).
func safeRenameDir(oldPath, newPath string) error {
	err := unix.Renameat2(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_NOREPLACE)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.EEXIST) {
		return &ExistsError{Path: newPath}
	}
	return safeRenameDirPortable(oldPath, newPath)
}
