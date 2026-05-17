//go:build windows

package compat

import "golang.org/x/sys/windows"

func GetFreeDiskBytes(path string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}

	var freeBytesAvailable uint64
	if err := windows.GetDiskFreeSpaceEx(p, &freeBytesAvailable, nil, nil); err != nil {
		return 0, err
	}
	return freeBytesAvailable, nil
}
