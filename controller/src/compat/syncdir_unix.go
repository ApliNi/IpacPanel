//go:build !windows

package compat

import daemoncompat "IpacPanel/daemon/compat"

func SyncDirIfPossible(path string) error {
	return daemoncompat.SyncDirIfPossible(path)
}
