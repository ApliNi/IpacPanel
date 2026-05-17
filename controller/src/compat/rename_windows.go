//go:build windows

package compat

import (
	"errors"
	"os"
)

// RenameNoReplace renames srcPath to dstPath without overwriting an existing
// destination.
func RenameNoReplace(srcPath string, dstPath string) error {
	// Windows os.Rename will not overwrite an existing destination.
	// Normalize errors to os.ErrExist when destination exists.
	if err := os.Rename(srcPath, dstPath); err == nil {
		return nil
	} else {
		if _, statErr := os.Stat(dstPath); statErr == nil {
			return os.ErrExist
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return err
		}
		return err
	}
}
