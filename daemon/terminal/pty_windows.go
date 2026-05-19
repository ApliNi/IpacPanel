//go:build windows

package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/UserExistsError/conpty"
	"github.com/kballard/go-shellquote"
)

type windowsCommand struct {
	args        []string
	commandLine string
}

type Proxy struct {
	pty          *conpty.ConPty
	readCloser   io.ReadCloser
	writeCloser  io.WriteCloser
	stdout       io.ReadCloser
	stderr       io.ReadCloser
	pipeWriter   *io.PipeWriter
	cmd          *exec.Cmd
	inputWriter  *encodingAwareWriter
	outputReader *encodingAwareReader
	ptyMu        sync.Mutex
	closeMu      sync.Mutex
	closeCalled  bool
	killMu       sync.Mutex
	killCalled   bool
	waitMu       sync.Mutex
	waitCancel   context.CancelFunc
	waitWake     chan struct{}
	wakeOnce     sync.Once
	readClosed   chan struct{}
	readOnce     sync.Once
}

const windowsPTYWaitFallbackTimeout = 5 * time.Second
const windowsPTYReadClosedWaitTimeout = 2 * time.Second

const (
	defaultPTYCols uint16 = 80
	defaultPTYRows uint16 = 24
)

type windowsPTYWaitResult struct {
	err error
}

func normalizeWindowsPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func buildWindowsCommand(path string, command string) (*windowsCommand, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		args := []string{"cmd.exe"}
		return &windowsCommand{args: args, commandLine: windowsCommandLine(args)}, nil
	}

	args, err := shellquote.Split(command)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, errors.New("command is empty")
	}

	resolved, err := resolveWindowsExecutable(path, args[0])
	if err != nil {
		return nil, err
	}
	args[0] = resolved

	switch strings.ToLower(filepath.Ext(resolved)) {
	case ".bat", ".cmd":
		cmdArgs := []string{"cmd.exe", "/d", "/s", "/c", windowsCommandLine(args)}
		return &windowsCommand{args: cmdArgs, commandLine: windowsCommandLine(cmdArgs)}, nil
	}

	return &windowsCommand{args: args, commandLine: windowsCommandLine(args)}, nil
}

func BuildCommand(path string, command string) (*exec.Cmd, error) {
	resolved, err := buildWindowsCommand(path, command)
	if err != nil {
		return nil, err
	}
	if len(resolved.args) == 0 {
		return nil, errors.New("command is empty")
	}
	cmd := exec.Command(resolved.args[0], resolved.args[1:]...)
	if path = normalizeWindowsPath(path); path != "" {
		cmd.Dir = path
	}
	return cmd, nil
}

func resolveWindowsExecutable(path string, entry string) (string, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return "", errors.New("command is empty")
	}

	if filepath.Base(entry) != entry {
		return resolveWindowsPathEntry(entry)
	}

	resolvedPath := normalizeWindowsPath(path)
	if resolvedPath != "" {
		if resolved, ok := findWindowsExecutableInDir(resolvedPath, entry); ok {
			return resolved, nil
		}
	}

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		dir = normalizeWindowsPath(dir)
		if dir == "" {
			continue
		}
		if resolved, ok := findWindowsExecutableInDir(dir, entry); ok {
			return resolved, nil
		}
	}

	return exec.LookPath(entry)
}

func resolveWindowsPathEntry(entry string) (string, error) {
	if filepath.Ext(entry) != "" {
		return entry, nil
	}
	dir := filepath.Dir(entry)
	base := filepath.Base(entry)
	if resolved, ok := findWindowsExecutableInDir(dir, base); ok {
		return resolved, nil
	}
	return entry, nil
}

func findWindowsExecutableInDir(dir string, entry string) (string, bool) {
	for _, candidate := range windowsExecutableCandidates(entry) {
		fullPath := filepath.Join(dir, candidate)
		if info, statErr := os.Stat(fullPath); statErr == nil && !info.IsDir() {
			return fullPath, true
		}
	}
	return "", false
}

func windowsExecutableCandidates(entry string) []string {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return nil
	}
	if filepath.Ext(entry) != "" {
		return []string{entry}
	}

	pathExt := strings.TrimSpace(os.Getenv("PATHEXT"))
	if pathExt == "" {
		pathExt = ".COM;.EXE;.BAT;.CMD"
	}

	parts := strings.Split(pathExt, ";")
	candidates := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		ext := strings.TrimSpace(part)
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		candidate := entry + ext
		key := strings.ToLower(candidate)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func windowsCommandLine(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, syscall.EscapeArg(arg))
	}
	return strings.Join(parts, " ")
}

func Start(path string, command string, usePTY bool, inputEncoding string, outputEncoding string, cols uint16, rows uint16) (*Proxy, error) {
	if _, ok := NormalizeTerminalEncoding(inputEncoding); !ok {
		return nil, errors.New("terminal encoding is invalid")
	}
	if _, ok := NormalizeTerminalEncoding(outputEncoding); !ok {
		return nil, errors.New("terminal encoding is invalid")
	}
	inputEncoding, _ = NormalizeTerminalEncoding(inputEncoding)
	outputEncoding, _ = NormalizeTerminalEncoding(outputEncoding)
	var proxy *Proxy
	var err error
	if usePTY {
		proxy, err = startWindowsPTY(path, command, cols, rows)
	} else {
		proxy, err = startWindowsPipe(path, command)
	}
	if err != nil {
		return nil, err
	}
	if proxy.pty != nil {
		proxy.outputReader = newEncodingAwareReader(readerFunc(func(b []byte) (int, error) {
			return proxy.pty.Read(b)
		}), outputEncoding)
		proxy.inputWriter = newEncodingAwareWriter(writerFunc(func(b []byte) (int, error) {
			return proxy.pty.Write(b)
		}), inputEncoding)
	} else {
		proxy.outputReader = newEncodingAwareReader(proxy.readCloser, outputEncoding)
		proxy.inputWriter = newEncodingAwareWriter(proxy.writeCloser, inputEncoding)
	}
	return proxy, nil
}

func startWindowsPTY(path string, command string, cols uint16, rows uint16) (*Proxy, error) {
	cmd, err := buildWindowsCommand(path, command)
	if err != nil {
		return nil, err
	}

	cols, rows = normalizePTYSize(cols, rows)
	options := make([]conpty.ConPtyOption, 0, 3)
	options = append(options, conpty.ConPtyDimensions(int(cols), int(rows)))
	options = append(options, conpty.ConPtyEnv(defaultTerminalEnv(os.Environ(), true)))
	if path = normalizeWindowsPath(path); path != "" {
		options = append(options, conpty.ConPtyWorkDir(path))
	}

	cpty, err := conpty.Start(cmd.commandLine, options...)
	if err != nil {
		return nil, err
	}

	return newWindowsProxy(&Proxy{pty: cpty}), nil
}

func defaultTerminalEnv(env []string, caseInsensitive bool) []string {
	values := append([]string(nil), env...)
	values = appendEnvIfMissing(values, "TERM", "xterm-256color", caseInsensitive)
	values = appendEnvIfMissing(values, "LANG", "C.UTF-8", caseInsensitive)
	values = appendEnvIfMissing(values, "LC_CTYPE", "C.UTF-8", caseInsensitive)
	return values
}

func appendEnvIfMissing(env []string, key string, value string, caseInsensitive bool) []string {
	for _, item := range env {
		name, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		if name == key || (caseInsensitive && strings.EqualFold(name, key)) {
			return env
		}
	}
	return append(env, key+"="+value)
}

func normalizePTYSize(cols uint16, rows uint16) (uint16, uint16) {
	if cols == 0 {
		cols = defaultPTYCols
	}
	if rows == 0 {
		rows = defaultPTYRows
	}
	return cols, rows
}

func startWindowsPipe(path string, command string) (*Proxy, error) {
	cmd, err := BuildCommand(path, command)
	if err != nil {
		return nil, err
	}
	PreventConsoleInheritance(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, err
	}
	reader, writer := io.Pipe()
	var wg sync.WaitGroup
	wg.Add(2)
	go copyPipeOutput(&wg, writer, stdout)
	go copyPipeOutput(&wg, writer, stderr)
	go func() {
		wg.Wait()
		_ = writer.Close()
	}()
	return newWindowsProxy(&Proxy{
		readCloser:  reader,
		writeCloser: stdin,
		stdout:      stdout,
		stderr:      stderr,
		pipeWriter:  writer,
		cmd:         cmd,
	}), nil
}

func newWindowsProxy(p *Proxy) *Proxy {
	p.waitWake = make(chan struct{})
	p.readClosed = make(chan struct{})
	return p
}

func copyPipeOutput(wg *sync.WaitGroup, dst *io.PipeWriter, src io.ReadCloser) {
	defer wg.Done()
	_, _ = io.Copy(dst, src)
	_ = src.Close()
}

func (p *Proxy) UpdateEncoding(inputEncoding string, outputEncoding string) error {
	if _, ok := NormalizeTerminalEncoding(inputEncoding); !ok {
		return errors.New("terminal encoding is invalid")
	}
	if _, ok := NormalizeTerminalEncoding(outputEncoding); !ok {
		return errors.New("terminal encoding is invalid")
	}
	if p.inputWriter != nil {
		p.inputWriter.SetEncoding(inputEncoding)
	}
	if p.outputReader != nil {
		p.outputReader.SetEncoding(outputEncoding)
	}
	return nil
}

func (p *Proxy) Read(b []byte) (int, error) {
	if p.outputReader != nil {
		return p.outputReader.Read(b)
	}
	if p.pty != nil {
		return p.pty.Read(b)
	}
	return p.readCloser.Read(b)
}

func (p *Proxy) Write(b []byte) (int, error) {
	if p.inputWriter != nil {
		return p.inputWriter.Write(b)
	}
	if p.pty != nil {
		return p.pty.Write(b)
	}
	return p.writeCloser.Write(b)
}

func (p *Proxy) Resize(cols, rows uint16) error {
	if p.pty != nil {
		cols, rows = normalizePTYSize(cols, rows)
		return p.pty.Resize(int(cols), int(rows))
	}
	return nil
}

func (p *Proxy) PID() int {
	if p == nil {
		return 0
	}
	if p.pty != nil {
		return p.pty.Pid()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

func (p *Proxy) Close() error {
	if p == nil {
		return nil
	}
	p.closeMu.Lock()
	if p.closeCalled {
		p.closeMu.Unlock()
		return nil
	}
	p.closeCalled = true
	p.closeMu.Unlock()

	defer p.notifyWait()
	if p.pty != nil {
		p.ptyMu.Lock()
		defer p.ptyMu.Unlock()
		return p.pty.Close()
	}
	return p.closePipe(true)
}

func (p *Proxy) Kill() error {
	if p == nil {
		return nil
	}
	p.killMu.Lock()
	if p.killCalled {
		p.killMu.Unlock()
		return nil
	}
	p.killCalled = true
	p.killMu.Unlock()

	defer p.notifyWait()
	if p.pty != nil {
		return p.killPTY()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		killErr := p.cmd.Process.Kill()
		if isWindowsProcessAlreadyDoneError(killErr) {
			killErr = nil
		}
		return errors.Join(killErr, p.closePipe(false))
	}
	return p.Close()
}

func (p *Proxy) closePipe(killProcess bool) error {
	var errs []error
	if p.writeCloser != nil {
		errs = appendWindowsCloseError(errs, p.writeCloser.Close())
	}
	if p.stdout != nil {
		errs = appendWindowsCloseError(errs, p.stdout.Close())
	}
	if p.stderr != nil {
		errs = appendWindowsCloseError(errs, p.stderr.Close())
	}
	if p.pipeWriter != nil {
		errs = appendWindowsCloseError(errs, p.pipeWriter.Close())
	}
	if p.readCloser != nil {
		errs = appendWindowsCloseError(errs, p.readCloser.Close())
	}
	if killProcess && p.cmd != nil && p.cmd.Process != nil {
		err := p.cmd.Process.Kill()
		if err != nil && !isWindowsProcessAlreadyDoneError(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *Proxy) killPTY() error {
	p.ptyMu.Lock()
	defer p.ptyMu.Unlock()

	var errs []error
	pid := p.pty.Pid()
	if pid > 0 {
		proc, err := os.FindProcess(pid)
		if err != nil {
			errs = append(errs, err)
		} else if err := proc.Kill(); err != nil && !isWindowsProcessAlreadyDoneError(err) {
			errs = append(errs, err)
		}
	}
	if err := p.pty.Close(); err != nil && !isWindowsCloseAlreadyDoneError(err) {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func appendWindowsCloseError(errs []error, err error) []error {
	if err == nil || isWindowsCloseAlreadyDoneError(err) {
		return errs
	}
	return append(errs, err)
}

func isWindowsCloseAlreadyDoneError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "file already closed") ||
		strings.Contains(msg, "handle is invalid") ||
		strings.Contains(msg, "use of closed") ||
		strings.Contains(msg, "pipe has been ended") ||
		strings.Contains(msg, "read/write on closed pipe")
}

func isWindowsProcessAlreadyDoneError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "process already finished") ||
		strings.Contains(msg, "process has already exited") ||
		strings.Contains(msg, "invalid argument")
}

func (p *Proxy) notifyWait() {
	if p.waitWake != nil {
		p.wakeOnce.Do(func() { close(p.waitWake) })
	}
	p.waitMu.Lock()
	cancel := p.waitCancel
	p.waitMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (p *Proxy) NotifyReadClosed() {
	if p == nil || p.readClosed == nil {
		return
	}
	p.readOnce.Do(func() { close(p.readClosed) })
}

func (p *Proxy) Wait() error {
	if p.pty != nil {
		ctx, cancel := context.WithCancel(context.Background())
		p.waitMu.Lock()
		p.waitCancel = cancel
		p.waitMu.Unlock()
		defer func() {
			cancel()
			p.waitMu.Lock()
			p.waitCancel = nil
			p.waitMu.Unlock()
		}()

		done := make(chan windowsPTYWaitResult, 1)
		go func() {
			_, err := p.pty.Wait(ctx)
			done <- windowsPTYWaitResult{err: err}
		}()

		return p.waitWindowsPTYResult(ctx, cancel, done)
	}
	if p.cmd != nil {
		return p.cmd.Wait()
	}
	return nil
}

func (p *Proxy) waitWindowsPTYResult(ctx context.Context, cancel context.CancelFunc, done <-chan windowsPTYWaitResult) error {
	select {
	case result := <-done:
		return result.err
	case <-p.waitWake:
		cancel()
		return waitWindowsPTYDone(done, windowsPTYWaitFallbackTimeout, "windows pty wait timeout after lifecycle signal")
	case <-p.readClosed:
		return waitWindowsPTYDone(done, windowsPTYReadClosedWaitTimeout, "windows pty wait timeout after output closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func waitWindowsPTYDone(done <-chan windowsPTYWaitResult, timeout time.Duration, message string) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-done:
		return result.err
	case <-timer.C:
		return fmt.Errorf("%s after %s: %w", message, timeout, context.DeadlineExceeded)
	}
}
