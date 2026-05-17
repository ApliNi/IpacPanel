package process

type TerminalHistory struct {
	data    []byte
	baseSeq uint64
	endSeq  uint64
}

func (h *TerminalHistory) Append(data []byte, limit int) {
	originalLen := len(data)
	if originalLen == 0 {
		return
	}
	h.endSeq += uint64(originalLen)
	h.data = append(h.data, data...)
	if len(h.data) <= limit {
		return
	}
	drop := len(h.data) - limit
	h.data = h.data[drop:]
	h.baseSeq += uint64(drop)
}

func (h *TerminalHistory) Reset(data []byte, limit int) {
	originalLen := len(data)
	h.data = nil
	h.baseSeq = h.endSeq
	h.endSeq += uint64(originalLen)
	if originalLen == 0 {
		return
	}
	if originalLen > limit {
		h.baseSeq = h.endSeq - uint64(limit)
		data = data[len(data)-limit:]
	}
	h.data = append(h.data, data...)
}

func (h *TerminalHistory) Snapshot() ([]byte, uint64) {
	return append([]byte(nil), h.data...), h.endSeq
}

func (h *TerminalHistory) EndSeq() uint64 {
	return h.endSeq
}

func (h *TerminalHistory) ReadFrom(seq uint64, maxBytes int) ([]byte, uint64, bool) {
	if maxBytes <= 0 || seq >= h.endSeq {
		return nil, seq, false
	}
	dropped := false
	if seq < h.baseSeq {
		seq = h.baseSeq
		dropped = true
	}
	offset := int(seq - h.baseSeq)
	if offset < 0 || offset >= len(h.data) {
		return nil, seq, dropped
	}
	available := len(h.data) - offset
	if available > maxBytes {
		available = maxBytes
	}
	payload := append([]byte(nil), h.data[offset:offset+available]...)
	return payload, seq + uint64(available), dropped
}
