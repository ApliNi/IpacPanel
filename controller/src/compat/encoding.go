package compat

import (
	"io"
	"strings"
	"sync"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

const DefaultTerminalEncoding = "utf-8"

func NormalizeTerminalEncoding(name string) (string, bool) {
	key := strings.TrimSpace(strings.ToLower(name))
	key = strings.ReplaceAll(key, "_", "-")
	key = strings.ReplaceAll(key, " ", "")
	switch key {
	case "", "utf8", "utf-8", "cp65001", "65001":
		return DefaultTerminalEncoding, true
	case "gbk", "cp936", "936", "windows-936":
		return "gbk", true
	case "gb18030":
		return "gb18030", true
	case "big5", "big-5", "cp950", "950", "windows-950":
		return "big5", true
	case "shift-jis", "shiftjis", "sjis", "cp932", "932", "windows-31j", "ms932":
		return "shift_jis", true
	case "euc-jp", "eucjp":
		return "euc-jp", true
	case "iso-2022-jp", "iso2022jp":
		return "iso-2022-jp", true
	case "euc-kr", "euckr", "cp949", "949", "windows-949":
		return "euc-kr", true
	case "windows-1252", "cp1252", "1252":
		return "windows-1252", true
	case "windows-1251", "cp1251", "1251":
		return "windows-1251", true
	case "windows-1250", "cp1250", "1250":
		return "windows-1250", true
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1":
		return "iso-8859-1", true
	case "utf-16le", "utf16le", "utf-16":
		return "utf-16le", true
	case "utf-16be", "utf16be":
		return "utf-16be", true
	default:
		return "", false
	}
}

func newTerminalEncoding(name string) encoding.Encoding {
	normalized, ok := NormalizeTerminalEncoding(name)
	if !ok || normalized == DefaultTerminalEncoding {
		return nil
	}
	switch normalized {
	case "gbk":
		return simplifiedchinese.GBK
	case "gb18030":
		return simplifiedchinese.GB18030
	case "big5":
		return traditionalchinese.Big5
	case "shift_jis":
		return japanese.ShiftJIS
	case "euc-jp":
		return japanese.EUCJP
	case "iso-2022-jp":
		return japanese.ISO2022JP
	case "euc-kr":
		return korean.EUCKR
	case "windows-1252":
		return charmap.Windows1252
	case "windows-1251":
		return charmap.Windows1251
	case "windows-1250":
		return charmap.Windows1250
	case "iso-8859-1":
		return charmap.ISO8859_1
	case "utf-16le":
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)
	case "utf-16be":
		return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM)
	default:
		return nil
	}
}

func wrapTerminalOutputReader(r io.Reader, outputEncoding string) io.Reader {
	enc := newTerminalEncoding(outputEncoding)
	if enc == nil {
		return r
	}
	return transform.NewReader(r, enc.NewDecoder())
}

func wrapTerminalInputWriter(w io.Writer, inputEncoding string) io.Writer {
	enc := newTerminalEncoding(inputEncoding)
	if enc == nil {
		return w
	}
	return transform.NewWriter(w, enc.NewEncoder())
}

type readerFunc func([]byte) (int, error)

func (fn readerFunc) Read(p []byte) (int, error) {
	return fn(p)
}

type writerFunc func([]byte) (int, error)

func (fn writerFunc) Write(p []byte) (int, error) {
	return fn(p)
}

type encodingAwareReader struct {
	mu       sync.RWMutex
	base     io.Reader
	current  io.Reader
	encoding string
}

func newEncodingAwareReader(base io.Reader, encoding string) *encodingAwareReader {
	r := &encodingAwareReader{base: base}
	r.SetEncoding(encoding)
	return r
}

func (r *encodingAwareReader) Read(p []byte) (int, error) {
	r.mu.RLock()
	reader := r.current
	r.mu.RUnlock()
	if reader == nil {
		return 0, io.EOF
	}
	return reader.Read(p)
}

func (r *encodingAwareReader) SetEncoding(name string) {
	normalized, ok := NormalizeTerminalEncoding(name)
	if !ok {
		normalized = DefaultTerminalEncoding
	}
	r.mu.Lock()
	if r.encoding == normalized && r.current != nil {
		r.mu.Unlock()
		return
	}
	r.encoding = normalized
	r.current = wrapTerminalOutputReader(r.base, normalized)
	r.mu.Unlock()
}

type encodingAwareWriter struct {
	mu       sync.RWMutex
	base     io.Writer
	current  io.Writer
	encoding string
}

func newEncodingAwareWriter(base io.Writer, encoding string) *encodingAwareWriter {
	w := &encodingAwareWriter{base: base}
	w.SetEncoding(encoding)
	return w
}

func (w *encodingAwareWriter) Write(p []byte) (int, error) {
	w.mu.RLock()
	writer := w.current
	w.mu.RUnlock()
	if writer == nil {
		return 0, io.ErrClosedPipe
	}
	return writer.Write(p)
}

func (w *encodingAwareWriter) SetEncoding(name string) {
	normalized, ok := NormalizeTerminalEncoding(name)
	if !ok {
		normalized = DefaultTerminalEncoding
	}
	w.mu.Lock()
	if w.encoding == normalized && w.current != nil {
		w.mu.Unlock()
		return
	}
	w.encoding = normalized
	w.current = wrapTerminalInputWriter(w.base, normalized)
	w.mu.Unlock()
}
