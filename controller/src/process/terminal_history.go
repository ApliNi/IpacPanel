package process

import "unicode/utf8"

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
	drop = safeTerminalHistoryStart(h.data, drop)
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
		drop := safeTerminalHistoryStart(data, len(data)-limit)
		h.baseSeq = h.endSeq - uint64(len(data)-drop)
		data = data[drop:]
	}
	h.data = append(h.data, data...)
}

func safeTerminalHistoryStart(data []byte, start int) int {
	if start <= 0 {
		return 0
	}
	if start >= len(data) {
		return len(data)
	}
	for start < len(data) && !utf8.RuneStart(data[start]) {
		start++
	}
	return skipPartialEscapeSequence(data, start)
}

func skipPartialEscapeSequence(data []byte, start int) int {
	const maxEscapeSequenceLen = 64
	esc := -1
	searchStart := start - maxEscapeSequenceLen
	if searchStart < 0 {
		searchStart = 0
	}
	for i := start - 1; i >= searchStart; i-- {
		if data[i] == 0x1b {
			esc = i
			break
		}
	}
	if esc < 0 || escapeSequenceEndsBefore(data, esc+1, start) {
		return start
	}
	end := escapeSequenceEnd(data, esc+1, maxEscapeSequenceLen)
	if end > start {
		return end
	}
	return start
}

func escapeSequenceEndsBefore(data []byte, seqStart int, before int) bool {
	end := escapeSequenceEnd(data, seqStart, before-seqStart)
	return end > seqStart && end <= before
}

func escapeSequenceEnd(data []byte, seqStart int, maxLen int) int {
	if seqStart >= len(data) || maxLen <= 0 {
		return seqStart
	}
	if data[seqStart] == '[' {
		for i := seqStart + 1; i < len(data) && i-seqStart <= maxLen; i++ {
			if data[i] >= 0x40 && data[i] <= 0x7e {
				return i + 1
			}
		}
		return seqStart
	}
	return seqStart + 1
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
