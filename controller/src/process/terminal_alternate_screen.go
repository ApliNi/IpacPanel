package process

const ptyAlternateScreenMaxPending = 32

func (sp *InstanceProcess) trackPTYAlternateScreenLocked(data []byte) {
	if sp == nil {
		return
	}
	_, active, pending := filterPTYAlternateScreenHistory(sp.PTYAlternateScreenActive, sp.PTYAlternateScreenPending, data)
	sp.PTYAlternateScreenActive = active
	sp.PTYAlternateScreenPending = pending
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
	sp.PTYAlternateScreenPending = pending
	return out
}

func filterPTYAlternateScreenHistory(active bool, pending []byte, data []byte) ([]byte, bool, []byte) {
	if len(pending) > 0 {
		data = append(append([]byte(nil), pending...), data...)
	}
	if len(data) == 0 {
		return nil, active, nil
	}

	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if data[i] != escapeByte {
			if !active {
				out = append(out, data[i])
			}
			i++
			continue
		}
		if i+1 >= len(data) {
			return out, active, append([]byte(nil), data[i:]...)
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
			pending = append([]byte(nil), data[i:]...)
			if len(pending) > ptyAlternateScreenMaxPending {
				pending = nil
			}
			return out, active, pending
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

func ptyAlternateScreenStateFromCSI(seq []byte) (bool, bool) {
	if len(seq) < 5 || seq[0] != escapeByte || seq[1] != '[' {
		return false, false
	}
	final := seq[len(seq)-1]
	if final != 'h' && final != 'l' {
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
	switch string(param) {
	case "?1049", "?1047", "?1048", "?47":
		return true
	case "1049", "1047", "1048", "47":
		return privateMode
	default:
		return false
	}
}
