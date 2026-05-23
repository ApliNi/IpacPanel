package web

import (
	"IpacPanel/controller/src/msg"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
)

const (
	contentEncodingZstd = "zstd"
	contentEncodingGzip = "gzip"
)

type compressionResponseWriter struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
	compressor  io.WriteCloser
	request     *http.Request
}

func WithResponseCompression(next http.Handler) http.Handler {
	if next == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cw := &compressionResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
			request:        r,
		}
		defer cw.Close()
		next.ServeHTTP(cw, r)
	})
}

func (w *compressionResponseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *compressionResponseWriter) WriteHeader(statusCode int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = statusCode
	if w.shouldVaryAcceptEncoding() {
		addVaryHeader(w.Header(), "Accept-Encoding")
	}
	if w.shouldCompressResponse() {
		if err := w.startCompression(); err != nil {
			w.Header().Del("Content-Encoding")
			w.Header().Del("Content-Length")
			w.ResponseWriter.WriteHeader(statusCode)
			return
		}
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *compressionResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(data))
		}
		w.WriteHeader(http.StatusOK)
	}
	if w.compressor != nil {
		_, err := w.compressor.Write(data)
		if err != nil {
			return 0, err
		}
		return len(data), nil
	}
	return w.ResponseWriter.Write(data)
}

func (w *compressionResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	if r == nil {
		return 0, nil
	}
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.compressor != nil {
		return io.Copy(w.compressor, r)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(r)
	}
	return copyWithoutReaderFrom(w.ResponseWriter, r)
}

func (w *compressionResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.compressor != nil {
		if flusher, ok := w.compressor.(interface{ Flush() error }); ok {
			_ = flusher.Flush()
		}
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *compressionResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w.compressor != nil {
		return nil, nil, errors.New(msg.CompressedResponseHijackUnsupported)
	}
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf(msg.ResponseWriterHijackUnsupported)
	}
	return hijacker.Hijack()
}

func (w *compressionResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *compressionResponseWriter) Close() {
	if w.compressor == nil {
		return
	}
	_ = w.compressor.Close()
	w.compressor = nil
}

func (w *compressionResponseWriter) shouldCompressResponse() bool {
	if !w.shouldVaryAcceptEncoding() {
		return false
	}
	return selectResponseCompression(w.request.Header.Get("Accept-Encoding")) != ""
}

func (w *compressionResponseWriter) shouldVaryAcceptEncoding() bool {
	if w.request == nil {
		return false
	}
	if !isCompressibleStatus(w.statusCode) {
		return false
	}
	if w.Header().Get("Content-Encoding") != "" {
		return false
	}
	if strings.Contains(strings.ToLower(w.Header().Get("Cache-Control")), "no-transform") {
		return false
	}
	if w.request.Header.Get("Range") != "" {
		return false
	}
	if strings.Contains(strings.ToLower(w.request.Header.Get("Connection")), "upgrade") {
		return false
	}
	if strings.EqualFold(w.request.Header.Get("Upgrade"), "websocket") {
		return false
	}
	contentType := strings.ToLower(strings.TrimSpace(w.Header().Get("Content-Type")))
	if strings.HasPrefix(contentType, "text/event-stream") {
		return false
	}
	return true
}

func (w *compressionResponseWriter) startCompression() error {
	encoding := selectResponseCompression(w.request.Header.Get("Accept-Encoding"))
	if encoding == "" {
		return nil
	}
	w.Header().Set("Content-Encoding", encoding)
	w.Header().Del("Content-Length")

	switch encoding {
	case contentEncodingZstd:
		encoder, err := zstd.NewWriter(w.ResponseWriter)
		if err != nil {
			return err
		}
		w.compressor = encoder
	case contentEncodingGzip:
		w.compressor = gzip.NewWriter(w.ResponseWriter)
	default:
		return nil
	}
	return nil
}

func isCompressibleStatus(statusCode int) bool {
	if statusCode < http.StatusOK || statusCode == http.StatusNoContent || statusCode == http.StatusPartialContent || statusCode == http.StatusNotModified {
		return false
	}
	return true
}

func selectResponseCompression(acceptEncoding string) string {
	encodings := parseAcceptEncoding(acceptEncoding)
	zstdQ := encodings[contentEncodingZstd]
	gzipQ := encodings[contentEncodingGzip]
	if zstdQ > gzipQ && zstdQ > 0 {
		return contentEncodingZstd
	}
	if gzipQ > zstdQ && gzipQ > 0 {
		return contentEncodingGzip
	}
	if zstdQ > 0 {
		return contentEncodingZstd
	}
	return ""
}

func copyWithoutReaderFrom(dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[:nr])
			if nw < 0 || nw > nr {
				return written, errInvalidWrite
			}
			written += int64(nw)
			if ew != nil {
				return written, ew
			}
			if nw != nr {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if errors.Is(er, io.EOF) {
				er = nil
			}
			return written, er
		}
	}
}

var errInvalidWrite = errors.New(msg.WriterInvalidWriteLength)

func addVaryHeader(header http.Header, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	for _, existing := range header.Values("Vary") {
		for _, part := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(part), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

func parseAcceptEncoding(header string) map[string]float64 {
	encodings := make(map[string]float64)
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, q := parseAcceptEncodingPart(part)
		if name == "" {
			continue
		}
		if current, exists := encodings[name]; !exists || q > current {
			encodings[name] = q
		}
	}
	return encodings
}

func parseAcceptEncodingPart(part string) (string, float64) {
	segments := strings.Split(part, ";")
	name := strings.ToLower(strings.TrimSpace(segments[0]))
	q := 1.0
	for _, segment := range segments[1:] {
		key, value, ok := strings.Cut(strings.TrimSpace(segment), "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") {
			continue
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			q = 0
			continue
		}
		if parsed < 0 {
			q = 0
			continue
		}
		if parsed > 1 {
			q = 1
			continue
		}
		q = parsed
	}
	return name, q
}
