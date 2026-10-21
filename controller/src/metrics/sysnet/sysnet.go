package sysnet

import (
	"bytes"
	"errors"
	"io"
	"sync"
)

// Counts represents active TCP and UDP socket counts.
type Counts struct {
	TCP uint64
	UDP uint64
}

// ReadSocketCounts returns current TCP and UDP socket counts.
func ReadSocketCounts() (Counts, error) {
	return readSocketCounts()
}

var sockstatBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 1024)
		return &b
	},
}

func parseUintDigits(b []byte) (uint64, error) {
	if len(b) == 0 {
		return 0, errors.New("empty number")
	}
	var n uint64
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid digit")
		}
		n = n*10 + uint64(c-'0')
	}
	return n, nil
}

func parseSockstatLine(line []byte, counts *Counts) error {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil
	}

	var isTCP, isUDP bool
	if bytes.HasPrefix(line, []byte("TCP:")) || bytes.HasPrefix(line, []byte("TCP6:")) {
		isTCP = true
	} else if bytes.HasPrefix(line, []byte("UDP:")) || bytes.HasPrefix(line, []byte("UDP6:")) {
		isUDP = true
	} else {
		return nil
	}

	for len(line) > 0 {
		for len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			line = line[1:]
		}
		if len(line) == 0 {
			break
		}
		end := 0
		for end < len(line) && line[end] != ' ' && line[end] != '\t' {
			end++
		}
		token := line[:end]
		line = line[end:]

		if bytes.Equal(token, []byte("inuse")) {
			for len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
				line = line[1:]
			}
			numEnd := 0
			for numEnd < len(line) && line[numEnd] != ' ' && line[numEnd] != '\t' {
				numEnd++
			}
			if numEnd == 0 {
				return nil
			}
			val, err := parseUintDigits(line[:numEnd])
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
	return nil
}

func parseSockstatStream(r io.Reader, counts *Counts) error {
	bufPtr := sockstatBufPool.Get().(*[]byte)
	buf := *bufPtr
	defer sockstatBufPool.Put(bufPtr)

	var pending int
	for {
		n, err := r.Read(buf[pending:])
		if n > 0 {
			total := pending + n
			data := buf[:total]
			processed := 0
			for {
				lineEnd := bytes.IndexByte(data[processed:], '\n')
				if lineEnd < 0 {
					break
				}
				line := data[processed : processed+lineEnd]
				processed += lineEnd + 1
				if err := parseSockstatLine(line, counts); err != nil {
					return err
				}
			}
			copy(buf, data[processed:])
			pending = total - processed
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if pending > 0 {
					if err := parseSockstatLine(buf[:pending], counts); err != nil {
						return err
					}
				}
				return nil
			}
			return err
		}
	}
}

func parseSockstatData(data []byte, counts *Counts) error {
	for len(data) > 0 {
		lineEnd := bytes.IndexByte(data, '\n')
		var line []byte
		if lineEnd >= 0 {
			line = data[:lineEnd]
			data = data[lineEnd+1:]
		} else {
			line = data
			data = nil
		}
		if err := parseSockstatLine(line, counts); err != nil {
			return err
		}
	}
	return nil
}
