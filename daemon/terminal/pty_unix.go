//go:build !windows

package terminal

import (
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

const (
	defaultPTYCols uint16 = 80
	defaultPTYRows uint16 = 24
)

type Proxy struct {
	readCloser   io.ReadCloser
	writeCloser  io.WriteCloser
	cmd          *exec.Cmd
	processTree  *ProcessTree
	ptyFile      *os.File
	inputWriter  *encodingAwareWriter
	outputReader *encodingAwareReader
	closeMu      sync.Mutex
	closeCalled  bool
	killMu       sync.Mutex
	killCalled   bool
}

func Start(path string, argv []string, usePTY bool, inputEncoding string, outputEncoding string, cols uint16, rows uint16) (*Proxy, error) {
	cmd, err := buildUnixCommand(path, argv)
	if err != nil {
		return nil, err
	}
	if usePTY {
		ensureDefaultTerminalEnv(&cmd.Env)
	}
	if _, ok := NormalizeTerminalEncoding(inputEncoding); !ok {
		return nil, errors.New("terminal encoding is invalid")
	}
	if _, ok := NormalizeTerminalEncoding(outputEncoding); !ok {
		return nil, errors.New("terminal encoding is invalid")
	}
	inputEncoding, _ = NormalizeTerminalEncoding(inputEncoding)
	outputEncoding, _ = NormalizeTerminalEncoding(outputEncoding)
	var proxy *Proxy
	if usePTY {
		cols, rows = normalizePTYSize(cols, rows)
		proxy, err = startUnixPTY(cmd, cols, rows)
	} else {
		proxy, err = startUnixPipe(cmd)
	}
	if err != nil {
		return nil, err
	}
	proxy.outputReader = newEncodingAwareReader(proxy.readCloser, outputEncoding)
	proxy.inputWriter = newEncodingAwareWriter(proxy.writeCloser, inputEncoding)
	return proxy, nil
}

func ensureDefaultTerminalEnv(env *[]string) {
	values := *env
	if values == nil {
		values = os.Environ()
	}
	values = appendEnvIfMissing(values, "TERM", "xterm-256color", false)
	values = appendEnvIfMissing(values, "LANG", "C.UTF-8", false)
	values = appendEnvIfMissing(values, "LC_CTYPE", "C.UTF-8", false)
	*env = values
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

func buildUnixCommand(path string, argv []string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	if len(argv) == 0 {
		return nil, errors.New("command is empty")
	}
	if argv[0] == "" {
		return nil, errors.New("command is empty")
	}
	cmd = exec.Command(argv[0], argv[1:]...)
	if path != "" {
		cmd.Dir = path
	}
	return cmd, nil
}

func BuildCommand(path string, argv []string) (*exec.Cmd, error) {
	return buildUnixCommand(path, argv)
}

func startUnixPTY(cmd *exec.Cmd, cols uint16, rows uint16) (*Proxy, error) {
	processTree, err := NewProcessTree()
	if err != nil {
		return nil, err
	}
	processTree.PrepareCommand(cmd, true)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		_ = processTree.Close()
		return nil, err
	}
	if err := processTree.AttachCommand(cmd); err != nil {
		if errors.Is(err, ErrProcessAlreadyExited) {
			_ = processTree.Close()
			return &Proxy{
				readCloser:  f,
				writeCloser: f,
				cmd:         cmd,
				processTree: nil,
				ptyFile:     f,
			}, nil
		}
		_ = f.Close()
		if killErr := cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) && !errors.Is(killErr, syscall.ESRCH) {
			_ = processTree.Close()
			return nil, errors.Join(err, killErr)
		}
		_ = cmd.Wait()
		_ = processTree.Close()
		return nil, err
	}
	return &Proxy{
		readCloser:  f,
		writeCloser: f,
		cmd:         cmd,
		processTree: processTree,
		ptyFile:     f,
	}, nil
}

func startUnixPipe(cmd *exec.Cmd) (*Proxy, error) {
	processTree, err := NewProcessTree()
	if err != nil {
		return nil, err
	}
	processTree.PrepareCommand(cmd, false)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = processTree.Close()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		_ = processTree.Close()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = processTree.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		_ = processTree.Close()
		return nil, err
	}
	if err := processTree.AttachCommand(cmd); err != nil {
		if errors.Is(err, ErrProcessAlreadyExited) {
			_ = processTree.Close()
			reader, writer := io.Pipe()
			var wg sync.WaitGroup
			wg.Add(2)
			go copyPipeOutput(&wg, writer, stdout)
			go copyPipeOutput(&wg, writer, stderr)
			go func() {
				wg.Wait()
				_ = writer.Close()
			}()
			return &Proxy{
				readCloser:  reader,
				writeCloser: stdin,
				cmd:         cmd,
				processTree: nil,
			}, nil
		}
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		if killErr := cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) && !errors.Is(killErr, syscall.ESRCH) {
			_ = processTree.Close()
			return nil, errors.Join(err, killErr)
		}
		_ = cmd.Wait()
		_ = processTree.Close()
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
	return &Proxy{
		readCloser:  reader,
		writeCloser: stdin,
		cmd:         cmd,
		processTree: processTree,
	}, nil
}

func copyPipeOutput(wg *sync.WaitGroup, dst *io.PipeWriter, src io.ReadCloser) {
	defer wg.Done()
	_, _ = io.Copy(dst, src)
	_ = src.Close()
}

func (p *Proxy) Read(b []byte) (int, error) {
	if p.outputReader != nil {
		return p.outputReader.Read(b)
	}
	return p.readCloser.Read(b)
}

func (p *Proxy) Write(b []byte) (int, error) {
	if p.inputWriter != nil {
		return p.inputWriter.Write(b)
	}
	return p.writeCloser.Write(b)
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

func (p *Proxy) Resize(cols, rows uint16) error {
	if p.ptyFile == nil {
		return nil
	}
	p.closeMu.Lock()
	closed := p.closeCalled
	p.closeMu.Unlock()
	if closed {
		return nil
	}
	cols, rows = normalizePTYSize(cols, rows)
	return pty.Setsize(p.ptyFile, &pty.Winsize{Cols: cols, Rows: rows})
}

func (p *Proxy) PID() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
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

	var errs []error
	if p.processTree != nil {
		errs = append(errs, p.processTree.Interrupt())
	} else if p.cmd != nil && p.cmd.Process != nil {
		errs = append(errs, p.cmd.Process.Signal(os.Interrupt))
	}
	if p.ptyFile != nil {
		errs = append(errs, p.ptyFile.Close())
		return errors.Join(errs...)
	}
	if p.writeCloser != nil {
		errs = append(errs, p.writeCloser.Close())
	}
	if p.readCloser != nil {
		errs = append(errs, p.readCloser.Close())
	}
	return errors.Join(errs...)
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

	if p.processTree != nil {
		return p.processTree.Kill()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return p.Close()
}

func (p *Proxy) Wait() error {
	if p.cmd == nil {
		return nil
	}
	err := p.cmd.Wait()
	p.CleanupProcessTree()
	return err
}

// CleanupProcessTree performs best-effort residual cleanup after the main wait
// result has been collected. Cleanup errors are intentionally not mixed into the
// wait result so callers can distinguish process exit from residual cleanup.
func (p *Proxy) CleanupProcessTree() {
	if p == nil || p.processTree == nil {
		return
	}
	if err := p.processTree.CloseAndKillResidual(); err != nil {
		log.Printf("proxy best-effort residual process tree cleanup error: %v", err)
	}
}

func (p *Proxy) NotifyReadClosed() {}
