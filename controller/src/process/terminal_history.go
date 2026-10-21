package process

import "unicode/utf8"

type TerminalHistory struct {
	buf     []byte
	start   int
	baseSeq uint64
	endSeq  uint64
}

// ShrinkToFit 压缩缓冲区, 释放未使用的多余容量并消除已废弃的前缀数据.
func (h *TerminalHistory) ShrinkToFit() {
	activeLen := len(h.buf) - h.start
	if activeLen <= 0 {
		h.buf = nil
		h.start = 0
		return
	}
	if h.start == 0 && cap(h.buf) == activeLen {
		return
	}
	newBuf := make([]byte, activeLen)
	copy(newBuf, h.buf[h.start:])
	h.buf = newBuf
	h.start = 0
}

func (h *TerminalHistory) Append(data []byte, limit int) {
	originalLen := len(data)
	if originalLen == 0 || limit <= 0 {
		return
	}

	capLimit := 2 * limit
	if originalLen >= limit {
		drop := safeTerminalHistoryStart(data, originalLen-limit)
		h.baseSeq = h.endSeq + uint64(drop)
		h.endSeq += uint64(originalLen)
		h.start = 0
		data = data[drop:]
		if cap(h.buf) != capLimit {
			h.buf = make([]byte, 0, capLimit)
		} else {
			h.buf = h.buf[:0]
		}
		h.buf = append(h.buf, data...)
		return
	}

	h.endSeq += uint64(originalLen)
	if cap(h.buf) != capLimit {
		activeLen := len(h.buf) - h.start
		newBuf := make([]byte, activeLen, capLimit)
		copy(newBuf, h.buf[h.start:])
		h.buf = newBuf
		h.start = 0
	} else if len(h.buf)+originalLen > cap(h.buf) {
		activeLen := len(h.buf) - h.start
		copy(h.buf[:activeLen], h.buf[h.start:])
		h.buf = h.buf[:activeLen]
		h.start = 0
	}

	h.buf = append(h.buf, data...)
	activeLen := len(h.buf) - h.start
	if activeLen > limit {
		excess := activeLen - limit
		newStart := safeTerminalHistoryStart(h.buf, h.start+excess)
		h.baseSeq += uint64(newStart - h.start)
		h.start = newStart
	}
}

func (h *TerminalHistory) Reset(data []byte, limit int) {
	originalLen := len(data)
	h.start = 0
	h.baseSeq = h.endSeq
	h.endSeq += uint64(originalLen)
	if originalLen == 0 || limit <= 0 {
		h.buf = nil
		return
	}

	if originalLen > limit {
		drop := safeTerminalHistoryStart(data, originalLen-limit)
		h.baseSeq = h.endSeq - uint64(originalLen-drop)
		data = data[drop:]
	}

	capLimit := 2 * limit
	if cap(h.buf) != capLimit {
		h.buf = make([]byte, 0, capLimit)
	} else {
		h.buf = h.buf[:0]
	}
	h.buf = append(h.buf, data...)
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
	activeLen := len(h.buf) - h.start
	if activeLen <= 0 {
		return nil, h.endSeq
	}
	cp := make([]byte, activeLen)
	copy(cp, h.buf[h.start:])
	return cp, h.endSeq
}

func (h *TerminalHistory) EndSeq() uint64 {
	return h.endSeq
}

func (h *TerminalHistory) ReadInto(dst []byte, seq uint64) (int, uint64, bool) {
	if len(dst) == 0 || seq >= h.endSeq {
		return 0, seq, false
	}
	dropped := false
	if seq < h.baseSeq {
		seq = h.baseSeq
		dropped = true
	}
	offset := int(seq - h.baseSeq)
	activeLen := len(h.buf) - h.start
	if offset < 0 || offset >= activeLen {
		return 0, seq, dropped
	}
	available := activeLen - offset
	if available > len(dst) {
		available = len(dst)
	}
	copy(dst[:available], h.buf[h.start+offset:h.start+offset+available])
	return available, seq + uint64(available), dropped
}

func (h *TerminalHistory) ReadFrom(seq uint64, maxBytes int) ([]byte, uint64, bool) {
	if maxBytes <= 0 || seq >= h.endSeq {
		return nil, seq, false
	}
	effectiveSeq := seq
	if effectiveSeq < h.baseSeq {
		effectiveSeq = h.baseSeq
	}
	offset := int(effectiveSeq - h.baseSeq)
	activeLen := len(h.buf) - h.start
	if offset < 0 || offset >= activeLen {
		return nil, effectiveSeq, seq < h.baseSeq
	}
	available := activeLen - offset
	if available > maxBytes {
		available = maxBytes
	}
	payload := make([]byte, available)
	n, nextSeq, dropped := h.ReadInto(payload, seq)
	if n == 0 {
		return nil, nextSeq, dropped
	}
	return payload[:n], nextSeq, dropped
}
