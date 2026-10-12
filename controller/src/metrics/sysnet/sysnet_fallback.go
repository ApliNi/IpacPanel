//go:build !windows && !linux

package sysnet

import (
	"github.com/shirou/gopsutil/v4/net"
)

func readSocketCounts() (Counts, error) {
	tcpConns, err := net.Connections("tcp")
	if err != nil {
		return Counts{}, err
	}
	udpConns, err := net.Connections("udp")
	if err != nil {
		return Counts{}, err
	}

	return Counts{
		TCP: uint64(len(tcpConns)),
		UDP: uint64(len(udpConns)),
	}, nil
}
