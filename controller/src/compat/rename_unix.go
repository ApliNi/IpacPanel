//go:build unix && !linux && !darwin

package compat

import (
	"errors"
	"fmt"
	"os"
)

// RenameNoReplace renames srcPath to dstPath without overwriting an existing
// destination.
//
// Non-Linux Unix platforms outside Darwin do not provide a portable atomic
// no-replace rename for every file type. For regular files, use link+unlink:
// link creates the destination atomically and fails with EEXIST if it already
// exists, so it does not introduce an overwrite race. Directories and other
// special files are refused explicitly because they cannot be handled safely by
// this fallback.
func RenameNoReplace(srcPath string, dstPath string) error {
	info, err := os.Lstat(srcPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("rename no-replace from %q to %q is only supported for regular files on this platform: %w", srcPath, dstPath, errors.ErrUnsupported)
	}
	if err := os.Link(srcPath, dstPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return os.ErrExist
		}
		return err
	}
	dstInfo, err := os.Lstat(dstPath)
	if err != nil {
		_ = os.Remove(dstPath)
		return err
	}
	if !os.SameFile(info, dstInfo) {
		_ = os.Remove(dstPath)
		return fmt.Errorf("rename no-replace from %q to %q linked unexpected destination: %w", srcPath, dstPath, errors.ErrUnsupported)
	}
	if err := os.Remove(srcPath); err != nil {
		return fmt.Errorf("rename no-replace from %q to %q linked destination but failed to remove source: %w", srcPath, dstPath, err)
	}
	return nil
}
