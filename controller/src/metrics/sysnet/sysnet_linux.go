//go:build linux

package sysnet

import (
	"errors"
	"os"
)

func readSockstatFile(path string, counts *Counts) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return parseSockstatStream(f, counts)
}

func readSocketCounts() (Counts, error) {
	var counts Counts

	if err := readSockstatFile("/proc/net/sockstat", &counts); err != nil {
		return counts, err
	}

	if err := readSockstatFile("/proc/net/sockstat6", &counts); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return counts, err
		}
	}

	return counts, nil
}
