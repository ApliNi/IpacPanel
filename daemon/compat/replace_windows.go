//go:build windows

package compat

import (
	"errors"

	"golang.org/x/sys/windows"
)

func ReplaceFileAtomic(srcPath string, dstPath string) error {
	if srcPath == "" || dstPath == "" {
		return errors.New("path is empty")
	}
	src, err := windows.UTF16PtrFromString(srcPath)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(dstPath)
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH | windows.MOVEFILE_REPLACE_EXISTING)
	return windows.MoveFileEx(src, dst, flags)
}

func SyncDirIfPossible(path string) error {
	return nil
}
