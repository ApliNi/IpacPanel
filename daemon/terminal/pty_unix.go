//go:build !windows

package terminal

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
	"github.com/kballard/go-shellquote"
)

type Proxy struct {
	readCloser   io.ReadCloser
	writeCloser  io.WriteCloser
	cmd          *exec.Cmd
	ptyFile      *os.File
	inputWriter  *encodingAwareWriter
	outputReader *encodingAwareReader
	closeMu      sync.Mutex
	closeCalled  bool
	killMu       sync.Mutex
	killCalled   bool
}

func Start(path string, command string, usePTY bool, inputEncoding string, outputEncoding string) (*Proxy, error) {
	cmd, err := buildUnixCommand(path, command)
	if err != nil {
		return nil, err
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
		proxy, err = startUnixPTY(cmd)
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

func buildUnixCommand(path string, command string) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	if command == "" {
		cmd = exec.Command("sh")
	} else {
		args, err := shellquote.Split(command)
		if err != nil {
			return nil, err
		}
		if len(args) == 0 {
			return nil, errors.New("command is empty")
		}
		cmd = exec.Command(args[0], args[1:]...)
	}
	if path != "" {
		cmd.Dir = path
	}
	return cmd, nil
}

func BuildCommand(path string, command string) (*exec.Cmd, error) {
	return buildUnixCommand(path, command)
}

func startUnixPTY(cmd *exec.Cmd) (*Proxy, error) {
	f, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return &Proxy{
		readCloser:  f,
		writeCloser: f,
		cmd:         cmd,
		ptyFile:     f,
	}, nil
}

func startUnixPipe(cmd *exec.Cmd) (*Proxy, error) {
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
	return &Proxy{
		readCloser:  reader,
		writeCloser: stdin,
		cmd:         cmd,
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

func (p *Proxy) Resize(cols, rows uint16) {
	if p.ptyFile == nil {
		return
	}
	_ = pty.Setsize(p.ptyFile, &pty.Winsize{Cols: cols, Rows: rows})
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
	if p.cmd != nil && p.cmd.Process != nil {
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

	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return p.Close()
}

func (p *Proxy) Wait() error {
	if p.cmd == nil {
		return nil
	}
	return p.cmd.Wait()
}

func (p *Proxy) NotifyReadClosed() {}
