//go:build darwin

package compat

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// RenameNoReplace renames srcPath to dstPath atomically without overwriting an
// existing destination.
func RenameNoReplace(srcPath string, dstPath string) error {
	if err := unix.RenameatxNp(unix.AT_FDCWD, srcPath, unix.AT_FDCWD, dstPath, unix.RENAME_EXCL); err != nil {
		if errors.Is(err, os.ErrExist) {
			return os.ErrExist
		}
		return err
	}
	return nil
}
