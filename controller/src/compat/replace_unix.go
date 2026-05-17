//go:build !windows

package compat

import daemoncompat "IpacPanel/daemon/compat"

// ReplaceFileAtomic replaces dstPath with srcPath atomically.
//
// On Unix, os.Rename is an atomic replacement when src/dst are on the same
// filesystem.
func ReplaceFileAtomic(srcPath string, dstPath string) error {
	return daemoncompat.ReplaceFileAtomic(srcPath, dstPath)
}
