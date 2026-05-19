//go:build !windows

package compat

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func ReplaceFileAtomic(srcPath string, dstPath string) error {
	if srcPath == "" || dstPath == "" {
		return errors.New("path is empty")
	}
	return os.Rename(srcPath, dstPath)
}

func SyncDirIfPossible(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "."
	}
	dir, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
