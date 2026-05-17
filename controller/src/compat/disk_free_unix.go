//go:build !windows

package compat

import "syscall"

func GetFreeDiskBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	// Bavail is free blocks available to unprivileged user.
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
