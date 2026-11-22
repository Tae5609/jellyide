//go:build !linux

package fsutil

// safeRenameDir is the portable directory move (see safeRenameDirPortable).
func safeRenameDir(oldPath, newPath string) error {
	return safeRenameDirPortable(oldPath, newPath)
}
