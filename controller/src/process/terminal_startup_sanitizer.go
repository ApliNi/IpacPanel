package process

import "strings"

const (
	escapeByte                = byte(0x1b)
	terminalStartupMaxPending = 256
)

func (sp *InstanceProcess) sanitizeTerminalStartupOutputLocked(data []byte) []byte {
	if sp == nil || len(data) == 0 {
		return nil
	}
	input := append(append([]byte(nil), sp.TerminalStartupPendingEscape...), data...)
	out, pending, _, completed := sanitizeTerminalStartupOutput(input)
	if len(pending) > terminalStartupMaxPending {
		pending = nil
	}
	sp.TerminalStartupPendingEscape = pending
	if completed {
		sp.TerminalStartupProtecting = false
		sp.TerminalStartupPendingEscape = nil
	}
	return out
}

func sanitizeTerminalStartupOutput(data []byte) ([]byte, []byte, bool, bool) {
	var out []byte
	changed := false
	for i := 0; i < len(data); {
		if data[i] != escapeByte {
			if isOrdinaryTerminalByte(data[i]) {
				out = append(out, data[i:]...)
				return out, nil, changed, true
			}
			out = append(out, data[i])
			i++
			continue
		}

		if i+1 >= len(data) {
			return out, append([]byte(nil), data[i:]...), changed, false
		}

		next := data[i+1]
		if next == 'c' {
			changed = true
			i += 2
			continue
		}
		if next == '[' {
			end, complete := findCSIEnd(data, i+2)
			if !complete {
				return out, append([]byte(nil), data[i:]...), changed, false
			}
			seq := data[i : end+1]
			if isDestructiveStartupCSI(seq) {
				changed = true
				i = end + 1
				continue
			}
			out = append(out, seq...)
			i = end + 1
			continue
		}

		out = append(out, data[i], next)
		i += 2
	}
	return out, nil, changed, false
}

func isOrdinaryTerminalByte(b byte) bool {
	return b >= 0x20 && b != 0x7f
}

func findCSIEnd(data []byte, start int) (int, bool) {
	for i := start; i < len(data); i++ {
		if data[i] >= 0x40 && data[i] <= 0x7e {
			return i, true
		}
	}
	return 0, false
}

func isDestructiveStartupCSI(seq []byte) bool {
	if len(seq) < 3 || seq[0] != escapeByte || seq[1] != '[' {
		return false
	}
	final := seq[len(seq)-1]
	params := string(seq[2 : len(seq)-1])
	switch final {
	case 'J':
		return strings.Contains(params, "2") || strings.Contains(params, "3")
	case 'H', 'f':
		return true
	case 'h', 'l':
		return strings.Contains(params, "?1049") || strings.Contains(params, "?1047") || strings.Contains(params, "?1048")
	case 'p':
		return strings.Contains(params, "!")
	default:
		return false
	}
}
