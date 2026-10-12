//go:build windows

package sysnet

import (
	"encoding/binary"
	"errors"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	tcpTableOwnerPidAll = 5
	udpTableOwnerPid    = 1
	afInet              = 2
	afInet6             = 23
)

var (
	modiphlpapi             = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable = modiphlpapi.NewProc("GetExtendedTcpTable")
	procGetExtendedUdpTable = modiphlpapi.NewProc("GetExtendedUdpTable")

	bufMu sync.Mutex
	buf   []byte
)

func queryTableCountLocked(proc *windows.LazyProc, family uint32, tableClass uint32) (uint64, error) {
	for {
		size := uint32(len(buf))
		var p uintptr
		if size > 0 {
			p = uintptr(unsafe.Pointer(&buf[0]))
		}

		r1, _, _ := syscall.Syscall6(
			proc.Addr(),
			6,
			p,
			uintptr(unsafe.Pointer(&size)),
			0,
			uintptr(family),
			uintptr(tableClass),
			0,
		)

		if r1 == 0 {
			if size < 4 {
				return 0, nil
			}
			return uint64(binary.LittleEndian.Uint32(buf[:4])), nil
		}

		if errors.Is(windows.Errno(r1), windows.ERROR_INSUFFICIENT_BUFFER) {
			if int(size) > cap(buf) {
				buf = make([]byte, size)
			} else {
				buf = buf[:cap(buf)]
			}
			continue
		}

		return 0, windows.Errno(r1)
	}
}

func readSocketCounts() (Counts, error) {
	bufMu.Lock()
	defer bufMu.Unlock()

	tcp4, err := queryTableCountLocked(procGetExtendedTcpTable, afInet, tcpTableOwnerPidAll)
	if err != nil {
		return Counts{}, err
	}
	tcp6, err := queryTableCountLocked(procGetExtendedTcpTable, afInet6, tcpTableOwnerPidAll)
	if err != nil && !errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
		return Counts{}, err
	}

	udp4, err := queryTableCountLocked(procGetExtendedUdpTable, afInet, udpTableOwnerPid)
	if err != nil {
		return Counts{}, err
	}
	udp6, err := queryTableCountLocked(procGetExtendedUdpTable, afInet6, udpTableOwnerPid)
	if err != nil && !errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
		return Counts{}, err
	}

	return Counts{
		TCP: tcp4 + tcp6,
		UDP: udp4 + udp6,
	}, nil
}
