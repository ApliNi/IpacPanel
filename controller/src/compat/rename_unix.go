//go:build !windows

package compat

import (
	"errors"
	"os"
)

func fallbackRenameNoReplace(srcPath string, dstPath string) error {
	// Best-effort fallback: avoid overwriting by checking existence.
	// Not atomic on Unix, but keeps behavior consistent on unsupported systems.
	if _, err := os.Stat(dstPath); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(srcPath, dstPath)
}

// RenameNoReplace renames srcPath to dstPath without overwriting an existing
// destination.
//
// On platforms where we don't have an atomic no-replace primitive, we fall back
// to a best-effort implementation.
func RenameNoReplace(srcPath string, dstPath string) error {
	return fallbackRenameNoReplace(srcPath, dstPath)
}
