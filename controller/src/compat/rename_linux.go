//go:build linux

package compat

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// RenameNoReplace renames srcPath to dstPath atomically without overwriting an
// existing destination.
func RenameNoReplace(srcPath string, dstPath string) error {
	if err := unix.Renameat2(unix.AT_FDCWD, srcPath, unix.AT_FDCWD, dstPath, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return os.ErrExist
		}
		return err
	}
	return nil
}
