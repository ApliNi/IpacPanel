package process

import "bytes"

const (
	escapeByte                   = byte(0x1b)
	ptyAlternateScreenMaxPending = 32
)

func (sp *InstanceProcess) trackPTYAlternateScreenLocked(data []byte) {
	if sp == nil {
		return
	}
	_, active, pending := filterPTYAlternateScreenHistory(sp.PTYAlternateScreenActive, sp.PTYAlternateScreenPending, data)
	sp.PTYAlternateScreenActive = active
	if len(pending) == 0 {
		sp.PTYAlternateScreenPending = nil
	} else {
		sp.PTYAlternateScreenPending = append(sp.PTYAlternateScreenPending[:0], pending...)
	}
}

func trackPTYAlternateScreenState(active bool, pending []byte, data []byte) (bool, []byte) {
	_, active, pending = filterPTYAlternateScreenHistory(active, pending, data)
	return active, pending
}

func (sp *InstanceProcess) filterPTYHistoryOutputLocked(data []byte) []byte {
	if sp == nil || len(data) == 0 {
		return nil
	}
	out, active, pending := filterPTYAlternateScreenHistory(sp.PTYAlternateScreenActive, sp.PTYAlternateScreenPending, data)
	sp.PTYAlternateScreenActive = active
	if len(pending) == 0 {
		sp.PTYAlternateScreenPending = nil
	} else {
		sp.PTYAlternateScreenPending = append(sp.PTYAlternateScreenPending[:0], pending...)
	}
	return out
}

func filterPTYAlternateScreenHistory(active bool, pending []byte, data []byte) ([]byte, bool, []byte) {
	if len(pending) > 0 {
		combined := make([]byte, len(pending)+len(data))
		copy(combined, pending)
		copy(combined[len(pending):], data)
		data = combined
		pending = nil
	}
	if len(data) == 0 {
		return nil, active, nil
	}

	// Fast path: 无转义字符时直接返回, 避免任何内存分配.
	if bytes.IndexByte(data, escapeByte) < 0 {
		if !active {
			return data, false, nil
		}
		return nil, true, nil
	}

	// 快速扫描: 若不含备用屏幕切换序列且末尾无不完整序列, 零分配返回切片.
	if !active {
		for i := 0; i < len(data); {
			idx := bytes.IndexByte(data[i:], escapeByte)
			if idx < 0 {
				return data, false, nil
			}
			i += idx
			if i+1 >= len(data) {
				return data[:i], false, copyPending(data[i:])
			}
			if data[i+1] != '[' {
				i += 2
				continue
			}
			end, complete := findCSIEnd(data, i+2)
			if !complete {
				return data[:i], false, copyPending(data[i:])
			}
			seq := data[i : end+1]
			if nextActive, ok := ptyAlternateScreenStateFromCSI(seq); ok {
				out := make([]byte, 0, len(data))
				out = append(out, data[:i]...)
				active = nextActive
				i = end + 1
				return finishFilterPTY(out, active, data, i)
			}
			i = end + 1
		}
		return data, false, nil
	}

	// 备用屏幕激活状态: 快速扫描, 若无退出序列则零分配返回 nil.
	for i := 0; i < len(data); {
		idx := bytes.IndexByte(data[i:], escapeByte)
		if idx < 0 {
			return nil, true, nil
		}
		i += idx
		if i+1 >= len(data) {
			return nil, true, copyPending(data[i:])
		}
		if data[i+1] != '[' {
			i += 2
			continue
		}
		end, complete := findCSIEnd(data, i+2)
		if !complete {
			return nil, true, copyPending(data[i:])
		}
		seq := data[i : end+1]
		if nextActive, ok := ptyAlternateScreenStateFromCSI(seq); ok {
			if !nextActive {
				out := make([]byte, 0, len(data)-end-1)
				active = false
				i = end + 1
				return finishFilterPTY(out, active, data, i)
			}
		}
		i = end + 1
	}
	return nil, true, nil
}

func finishFilterPTY(out []byte, active bool, data []byte, start int) ([]byte, bool, []byte) {
	for i := start; i < len(data); {
		idx := bytes.IndexByte(data[i:], escapeByte)
		if idx < 0 {
			if !active {
				out = append(out, data[i:]...)
			}
			break
		}
		if idx > 0 {
			if !active {
				out = append(out, data[i:i+idx]...)
			}
			i += idx
		}
		if i+1 >= len(data) {
			return out, active, copyPending(data[i:])
		}
		if data[i+1] != '[' {
			if !active {
				out = append(out, data[i], data[i+1])
			}
			i += 2
			continue
		}
		end, complete := findCSIEnd(data, i+2)
		if !complete {
			return out, active, copyPending(data[i:])
		}
		seq := data[i : end+1]
		if nextActive, ok := ptyAlternateScreenStateFromCSI(seq); ok {
			active = nextActive
		} else if !active {
			out = append(out, seq...)
		}
		i = end + 1
	}
	return out, active, nil
}

func copyPending(data []byte) []byte {
	if len(data) == 0 || len(data) > ptyAlternateScreenMaxPending {
		return nil
	}
	return append([]byte(nil), data...)
}

func ptyAlternateScreenStateFromCSI(seq []byte) (bool, bool) {
	if len(seq) < 5 || seq[0] != escapeByte || seq[1] != '[' {
		return false, false
	}
	final := seq[len(seq)-1]
	if final != 'h' && final != 'l' {
		return false, false
	}
	if bytes.IndexByte(seq, '4') < 0 {
		return false, false
	}
	params := seq[2 : len(seq)-1]
	if !containsPTYAlternateScreenParam(params) {
		return false, false
	}
	return final == 'h', true
}

func containsPTYAlternateScreenParam(params []byte) bool {
	privateMode := false
	for len(params) > 0 {
		partEnd := 0
		for partEnd < len(params) && params[partEnd] != ';' {
			partEnd++
		}
		part := params[:partEnd]
		if len(part) > 0 && part[0] == '?' {
			privateMode = true
		}
		if isPTYAlternateScreenParam(part, privateMode) {
			return true
		}
		if partEnd == len(params) {
			break
		}
		params = params[partEnd+1:]
	}
	return false
}

func isPTYAlternateScreenParam(param []byte, privateMode bool) bool {
	if len(param) == 0 {
		return false
	}
	if param[0] == '?' {
		param = param[1:]
	} else if !privateMode {
		return false
	}
	switch len(param) {
	case 2:
		return param[0] == '4' && param[1] == '7'
	case 4:
		return param[0] == '1' && param[1] == '0' && param[2] == '4' && (param[3] == '9' || param[3] == '7' || param[3] == '8')
	default:
		return false
	}
}

func findCSIEnd(data []byte, start int) (int, bool) {
	for i := start; i < len(data); i++ {
		if data[i] >= 0x40 && data[i] <= 0x7e {
			return i, true
		}
	}
	return 0, false
}
