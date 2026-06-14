//go:build windows

package compat

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// RenameNoReplace renames srcPath to dstPath without overwriting an existing
// destination.
func RenameNoReplace(srcPath string, dstPath string) error {
	src, err := windows.UTF16PtrFromString(srcPath)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(dstPath)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(src, dst, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		if errors.Is(err, os.ErrExist) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) || errors.Is(err, windows.ERROR_FILE_EXISTS) {
			return os.ErrExist
		}
		if _, statErr := os.Lstat(dstPath); statErr == nil {
			return os.ErrExist
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return err
		}
		return err
	}
	return nil
}
