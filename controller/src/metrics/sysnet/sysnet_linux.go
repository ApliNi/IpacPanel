//go:build linux

package sysnet

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
)

func parseSockstatData(data []byte, counts *Counts) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var isTCP, isUDP bool
		if strings.HasPrefix(line, "TCP:") || strings.HasPrefix(line, "TCP6:") {
			isTCP = true
		} else if strings.HasPrefix(line, "UDP:") || strings.HasPrefix(line, "UDP6:") {
			isUDP = true
		} else {
			continue
		}

		fields := strings.Fields(line)
		for i, field := range fields {
			if field == "inuse" && i+1 < len(fields) {
				val, err := strconv.ParseUint(fields[i+1], 10, 64)
				if err != nil {
					return err
				}
				if isTCP {
					counts.TCP += val
				} else if isUDP {
					counts.UDP += val
				}
				break
			}
		}
	}
	return scanner.Err()
}

func readSocketCounts() (Counts, error) {
	var counts Counts

	data4, err := os.ReadFile("/proc/net/sockstat")
	if err != nil {
		return counts, err
	}
	if err := parseSockstatData(data4, &counts); err != nil {
		return counts, err
	}

	data6, err := os.ReadFile("/proc/net/sockstat6")
	if err == nil {
		if err := parseSockstatData(data6, &counts); err != nil {
			return counts, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return counts, err
	}

	return counts, nil
}
