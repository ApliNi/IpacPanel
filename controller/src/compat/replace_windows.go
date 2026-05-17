//go:build windows

package compat

import daemoncompat "IpacPanel/daemon/compat"

// ReplaceFileAtomic replaces dstPath with srcPath atomically when possible.
//
// On Windows it uses MoveFileExW with REPLACE_EXISTING + WRITE_THROUGH.
//
// srcPath must be on the same volume as dstPath for atomic behavior.
func ReplaceFileAtomic(srcPath string, dstPath string) error {
	return daemoncompat.ReplaceFileAtomic(srcPath, dstPath)
}
