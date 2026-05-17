//go:build windows

package terminal

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

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
	once         sync.Once
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
		return nil, errEmptyCommand
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
		return nil, errEmptyCommand
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
		return "", errEmptyCommand
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

func Start(path string, command string, usePTY bool, inputEncoding string, outputEncoding string) (*Proxy, error) {
	if _, ok := NormalizeTerminalEncoding(inputEncoding); !ok {
		return nil, errInvalidEncoding
	}
	if _, ok := NormalizeTerminalEncoding(outputEncoding); !ok {
		return nil, errInvalidEncoding
	}
	inputEncoding, _ = NormalizeTerminalEncoding(inputEncoding)
	outputEncoding, _ = NormalizeTerminalEncoding(outputEncoding)
	var proxy *Proxy
	var err error
	if usePTY {
		proxy, err = startWindowsPTY(path, command)
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

func startWindowsPTY(path string, command string) (*Proxy, error) {
	cmd, err := buildWindowsCommand(path, command)
	if err != nil {
		return nil, err
	}

	options := make([]conpty.ConPtyOption, 0, 1)
	if path = normalizeWindowsPath(path); path != "" {
		options = append(options, conpty.ConPtyWorkDir(path))
	}

	cpty, err := conpty.Start(cmd.commandLine, options...)
	if err != nil {
		return nil, err
	}

	return &Proxy{pty: cpty}, nil
}

func startWindowsPipe(path string, command string) (*Proxy, error) {
	cmd, err := BuildCommand(path, command)
	if err != nil {
		return nil, err
	}
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
		stdout:      stdout,
		stderr:      stderr,
		pipeWriter:  writer,
		cmd:         cmd,
	}, nil
}

func copyPipeOutput(wg *sync.WaitGroup, dst *io.PipeWriter, src io.ReadCloser) {
	defer wg.Done()
	_, _ = io.Copy(dst, src)
	_ = src.Close()
}

func (p *Proxy) UpdateEncoding(inputEncoding string, outputEncoding string) error {
	if _, ok := NormalizeTerminalEncoding(inputEncoding); !ok {
		return errInvalidEncoding
	}
	if _, ok := NormalizeTerminalEncoding(outputEncoding); !ok {
		return errInvalidEncoding
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

func (p *Proxy) Resize(cols, rows uint16) {
	if p.pty != nil {
		p.pty.Resize(int(cols), int(rows))
	}
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
	var err error
	p.once.Do(func() {
		if p.pty != nil {
			err = p.pty.Close()
			return
		}
		if p.writeCloser != nil {
			_ = p.writeCloser.Close()
		}
		if p.stdout != nil {
			_ = p.stdout.Close()
		}
		if p.stderr != nil {
			_ = p.stderr.Close()
		}
		if p.pipeWriter != nil {
			_ = p.pipeWriter.Close()
		}
		if p.readCloser != nil {
			err = p.readCloser.Close()
		}
		if p.cmd != nil && p.cmd.Process != nil {
			killErr := p.cmd.Process.Kill()
			if err == nil && killErr != nil && !strings.Contains(killErr.Error(), "process already finished") {
				err = killErr
			}
		}
	})
	return err
}

func (p *Proxy) Kill() error {
	if p.pty != nil {
		var err error
		p.once.Do(func() {
			pid := p.pty.Pid()
			if pid > 0 {
				var proc *os.Process
				proc, err = os.FindProcess(pid)
				if err == nil {
					err = proc.Kill()
				}
			}
			if closeErr := p.pty.Close(); err == nil {
				err = closeErr
			}
		})
		return err
	}
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return p.Close()
}

func (p *Proxy) Wait() error {
	if p.pty != nil {
		_, err := p.pty.Wait(context.Background())
		return err
	}
	if p.cmd != nil {
		return p.cmd.Wait()
	}
	return nil
}
