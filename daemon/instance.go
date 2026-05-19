package main

import (
	"IpacPanel/daemon/terminal"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	daemonInstanceStopping      = "Instance is stopping"
	daemonInstanceNotRunning    = "Instance is not running"
	daemonInputEncodingInvalid  = "Invalid input encoding"
	daemonOutputEncodingInvalid = "Invalid output encoding"
)

const daemonOutputReadBufferSize = 64 * 1024

const daemonRuntimeEventSendTimeout = 5 * time.Second

var daemonOutputBufferPool = sync.Pool{
	New: func() any {
		buf := make([]byte, daemonOutputReadBufferSize)
		return &buf
	},
}

func getDaemonOutputBuffer() []byte {
	buf := *(daemonOutputBufferPool.Get().(*[]byte))
	return buf[:cap(buf)]
}

func putDaemonOutputBuffer(buf []byte) {
	if cap(buf) != daemonOutputReadBufferSize {
		return
	}
	buf = buf[:cap(buf)]
	daemonOutputBufferPool.Put(&buf)
}

type instanceState uint8

const (
	instanceStopped instanceState = iota
	instanceRunning
	instanceStopping
	instanceCleaning
)

type DaemonInstance struct {
	Name           string
	Command        string
	CleanupCommand string
	Path           string
	Terminal       int
	InputEnc       string
	OutputEnc      string
	State          instanceState
	Runtime        InstanceRuntimeState
	Proxy          *terminal.Proxy
	Cmd            *exec.Cmd
	CleanupCmd     *exec.Cmd
	DevNull        *os.File
	OutputCh       chan<- IPCResponse
	runtimeID      string
	proxySeq       uint64
	outputDone     chan struct{}
	skipCleanup    bool
	Mu             sync.Mutex
}

func instanceLifecycle(state instanceState) string {
	switch state {
	case instanceRunning:
		return InstanceLifecycleRunning
	case instanceStopping:
		return InstanceLifecycleStopping
	case instanceCleaning:
		return InstanceLifecycleCleaning
	default:
		return InstanceLifecycleStopped
	}
}

type InstanceManager struct {
	instances map[string]*DaemonInstance
	Mu        sync.RWMutex
}

func NewInstanceManager() *InstanceManager {
	return &InstanceManager{
		instances: make(map[string]*DaemonInstance),
	}
}

func (m *InstanceManager) Get(name string) (*DaemonInstance, bool) {
	m.Mu.RLock()
	defer m.Mu.RUnlock()
	ins, ok := m.instances[name]
	return ins, ok
}

func (m *InstanceManager) Set(name string, ins *DaemonInstance) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	m.instances[name] = ins
}

func (m *InstanceManager) Delete(name string) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	delete(m.instances, name)
}

func (m *InstanceManager) List() []*DaemonInstance {
	m.Mu.RLock()
	defer m.Mu.RUnlock()
	list := make([]*DaemonInstance, 0, len(m.instances))
	for _, ins := range m.instances {
		list = append(list, ins)
	}
	return list
}

func (m *InstanceManager) ListRuntime() []InstanceRuntimeState {
	m.Mu.RLock()
	defer m.Mu.RUnlock()
	runtime := make([]InstanceRuntimeState, 0, len(m.instances))
	for _, ins := range m.instances {
		if ins == nil {
			continue
		}
		runtime = append(runtime, ins.RuntimeSnapshot())
	}
	return runtime
}

func (m *InstanceManager) RenameInstance(oldName string, newName string) error {
	if strings.TrimSpace(oldName) == "" || strings.TrimSpace(newName) == "" {
		return errors.New("old_name and new_name required")
	}
	if oldName == newName {
		return nil
	}
	m.Mu.Lock()
	defer m.Mu.Unlock()
	ins, ok := m.instances[oldName]
	if !ok {
		return nil
	}
	if _, exists := m.instances[newName]; exists {
		return fmt.Errorf("instance %s already exists", newName)
	}
	delete(m.instances, oldName)
	ins.Mu.Lock()
	ins.Name = newName
	ins.Runtime.Name = newName
	ins.Mu.Unlock()
	m.instances[newName] = ins
	return nil
}

func (m *InstanceManager) prepareInstanceForStartLocked(req *IPCRequest) *DaemonInstance {
	ins, ok := m.instances[req.Instance]
	if !ok || ins == nil {
		ins = &DaemonInstance{Name: req.Instance}
	}
	ins.Mu.Lock()
	ins.Name = req.Instance
	ins.Command = req.Command
	ins.CleanupCommand = strings.TrimSpace(req.CleanupCommand)
	ins.Path = req.Path
	ins.Terminal = NormalizeTerminalMode(req.Terminal)
	ins.InputEnc = req.InputEnc
	ins.OutputEnc = req.OutputEnc
	if ins.Runtime.Name == "" {
		ins.Runtime.Name = req.Instance
	}
	ins.Mu.Unlock()
	return ins
}

func (ins *DaemonInstance) UpdateRuntimeConfig(cleanupCommand string) {
	ins.Mu.Lock()
	defer ins.Mu.Unlock()
	ins.CleanupCommand = strings.TrimSpace(cleanupCommand)
}

func (ins *DaemonInstance) RuntimeSnapshot() InstanceRuntimeState {
	ins.Mu.Lock()
	defer ins.Mu.Unlock()
	rt := ins.Runtime
	if rt.Name == "" {
		rt.Name = ins.Name
	}
	if rt.RuntimeName == "" {
		rt.RuntimeName = ins.runtimeID
	}
	rt.Lifecycle = instanceLifecycle(ins.State)
	return rt
}

func (ins *DaemonInstance) Start(outputCh chan<- IPCResponse) error {
	ins.Mu.Lock()
	defer ins.Mu.Unlock()

	if ins.State == instanceRunning {
		return nil
	}
	if ins.State == instanceStopping {
		return errors.New(daemonInstanceStopping)
	}

	if ins.OutputEnc == "" {
		ins.OutputEnc = terminal.DefaultTerminalEncoding
	}
	if ins.InputEnc == "" {
		ins.InputEnc = terminal.DefaultTerminalEncoding
	}
	if _, ok := terminal.NormalizeTerminalEncoding(ins.InputEnc); !ok {
		return errors.New(daemonInputEncodingInvalid)
	}
	if _, ok := terminal.NormalizeTerminalEncoding(ins.OutputEnc); !ok {
		return errors.New(daemonOutputEncodingInvalid)
	}

	resolvedPath, err := resolveInstancePath(ins.Path)
	if err != nil {
		return fmt.Errorf("resolve instance path: %w", err)
	}
	resolvedPath = filepath.Clean(resolvedPath)

	var proxy *terminal.Proxy
	var cmd *exec.Cmd
	var devNull *os.File
	var pid int
	if IsNoTerminal(ins.Terminal) {
		cmd, devNull, err = startNoTerminalProcess(resolvedPath, ins.Command)
		if err != nil {
			return fmt.Errorf("start instance: %w", err)
		}
		if cmd.Process != nil {
			pid = cmd.Process.Pid
		}
	} else {
		proxy, err = terminal.Start(resolvedPath, ins.Command, IsPTYTerminal(ins.Terminal), ins.InputEnc, ins.OutputEnc)
		if err != nil {
			return fmt.Errorf("start instance: %w", err)
		}
		pid = proxy.PID()
	}

	runtimeID := ins.Name
	ins.Proxy = proxy
	ins.Cmd = cmd
	ins.DevNull = devNull
	ins.OutputCh = outputCh
	if proxy != nil {
		ins.outputDone = make(chan struct{})
	} else {
		ins.outputDone = nil
	}
	ins.State = instanceRunning
	ins.runtimeID = runtimeID
	ins.skipCleanup = false
	restartCount := ins.Runtime.RestartCount
	if !ins.Runtime.StartTime.IsZero() {
		restartCount++
	}
	ins.Runtime = InstanceRuntimeState{
		Name:         ins.Name,
		RuntimeName:  runtimeID,
		Lifecycle:    InstanceLifecycleRunning,
		RuntimeCode:  RuntimeCodeRunning,
		PID:          pid,
		StartTime:    time.Now(),
		RestartCount: restartCount,
		Terminal:     ins.Terminal,
	}
	ins.proxySeq++
	proxySeq := ins.proxySeq

	if proxy != nil {
		go ins.readOutput(proxy, proxySeq, runtimeID, ins.outputDone)
		go ins.waitProxyExit(proxy, proxySeq)
	} else if cmd != nil {
		go ins.waitCommandExit(cmd, proxySeq)
	}

	return nil
}

func startNoTerminalProcess(path string, command string) (*exec.Cmd, *os.File, error) {
	cmd, err := terminal.BuildCommand(path, command)
	if err != nil {
		return nil, nil, err
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open dev null: %w", err)
	}
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	if err := cmd.Start(); err != nil {
		_ = devNull.Close()
		return nil, nil, err
	}
	return cmd, devNull, nil
}

func (ins *DaemonInstance) Stop(force bool, runtimeCode int) {
	ins.Mu.Lock()
	if ins.State == instanceCleaning {
		if runtimeCode != RuntimeCodeUnknown {
			ins.markRuntimeCodeLocked(runtimeCode)
		}
		cleanupCmd := ins.CleanupCmd
		name := ins.Name
		ins.Mu.Unlock()
		if force && cleanupCmd != nil && cleanupCmd.Process != nil {
			if err := cleanupCmd.Process.Kill(); err != nil {
				log.Printf("instance %s cleanup kill error: %v", name, err)
			}
		}
		return
	}
	if runtimeCode != RuntimeCodeUnknown {
		ins.markRuntimeCodeLocked(runtimeCode)
	}
	if (ins.State != instanceRunning && !(force && ins.State == instanceStopping)) || (ins.Proxy == nil && ins.Cmd == nil) {
		ins.Mu.Unlock()
		return
	}
	ins.State = instanceStopping
	ins.skipCleanup = force
	ins.Runtime.Lifecycle = InstanceLifecycleStopping
	if runtimeCode == RuntimeCodeUnknown && force {
		ins.Runtime.RuntimeCode = RuntimeCodeManualKill
	} else if runtimeCode == RuntimeCodeUnknown {
		ins.Runtime.RuntimeCode = RuntimeCodeManualStop
	}
	proxy := ins.Proxy
	cmd := ins.Cmd
	name := ins.Name
	ins.Mu.Unlock()

	if proxy == nil && cmd != nil {
		if cmd.Process != nil {
			if force {
				if err := cmd.Process.Kill(); err != nil {
					log.Printf("instance %s kill error: %v", name, err)
				}
			} else {
				if err := cmd.Process.Signal(os.Interrupt); err != nil {
					log.Printf("instance %s interrupt error: %v", name, err)
					if killErr := cmd.Process.Kill(); killErr != nil {
						log.Printf("instance %s fallback kill error: %v", name, killErr)
					}
				}
			}
		}
		return
	}
	if force {
		proxy.Kill()
		return
	}
	proxy.Close()
}

func (ins *DaemonInstance) MarkStopping(runtimeCode int) {
	ins.Mu.Lock()
	defer ins.Mu.Unlock()
	if runtimeCode != RuntimeCodeUnknown {
		ins.markRuntimeCodeLocked(runtimeCode)
	}
	if ins.State == instanceRunning {
		ins.State = instanceStopping
		ins.Runtime.Lifecycle = InstanceLifecycleStopping
	}
}

func (ins *DaemonInstance) MarkRuntimeCode(runtimeCode int) {
	ins.Mu.Lock()
	defer ins.Mu.Unlock()
	ins.markRuntimeCodeLocked(runtimeCode)
}

func (ins *DaemonInstance) markRuntimeCodeLocked(runtimeCode int) {
	if runtimeCode == RuntimeCodeUnknown {
		return
	}
	if ins.Runtime.Name == "" {
		ins.Runtime.Name = ins.Name
	}
	ins.Runtime.RuntimeCode = runtimeCode
}

func (ins *DaemonInstance) Kill() {
	ins.Stop(true, RuntimeCodeManualKill)
}

func (ins *DaemonInstance) WriteStdin(data []byte) error {
	ins.Mu.Lock()
	defer ins.Mu.Unlock()
	if IsNoTerminal(ins.Terminal) {
		return errors.New("instance has no active terminal")
	}
	if (ins.State != instanceRunning && ins.State != instanceStopping) || ins.Proxy == nil {
		return errors.New(daemonInstanceNotRunning)
	}
	_, err := ins.Proxy.Write(data)
	return err
}

func (ins *DaemonInstance) ResizeTerminal(cols, rows uint16) {
	ins.Mu.Lock()
	defer ins.Mu.Unlock()
	if ins.Proxy != nil && !IsNoTerminal(ins.Terminal) {
		ins.Proxy.Resize(cols, rows)
	}
}

func (ins *DaemonInstance) readOutput(proxy *terminal.Proxy, proxySeq uint64, runtimeID string, done chan struct{}) {
	if done != nil {
		defer close(done)
	}
	for {
		buf := getDaemonOutputBuffer()
		n, err := proxy.Read(buf)
		if n > 0 {
			data := buf[:n]

			ins.Mu.Lock()
			currentProxy := ins.Proxy
			currentSeq := ins.proxySeq
			outputCh := ins.OutputCh
			ins.Mu.Unlock()

			if currentProxy != nil && currentProxy != proxy {
				putDaemonOutputBuffer(data)
				return
			}
			if currentSeq != proxySeq {
				putDaemonOutputBuffer(data)
				return
			}
			if outputCh != nil {
				releaseData := data
				select {
				case outputCh <- IPCResponse{Type: ipcFrameTypeInstanceOutput, Instance: runtimeID, Body: data, release: func() { putDaemonOutputBuffer(releaseData) }}:
				default:
					putDaemonOutputBuffer(data)
				}
			} else {
				putDaemonOutputBuffer(data)
			}
		} else {
			putDaemonOutputBuffer(buf)
		}
		if err != nil {
			if !isBenignReadError(err) {
				log.Printf("instance %s read error: %v", runtimeID, err)
			}
			return
		}
	}
}

func (ins *DaemonInstance) waitProxyExit(proxy *terminal.Proxy, proxySeq uint64) {
	_ = proxy.Wait()
	ins.finishProcessExit(proxy, nil, proxySeq)
}

func (ins *DaemonInstance) waitCommandExit(cmd *exec.Cmd, proxySeq uint64) {
	_ = cmd.Wait()
	ins.finishProcessExit(nil, cmd, proxySeq)
}

func (ins *DaemonInstance) finishProcessExit(proxy *terminal.Proxy, cmd *exec.Cmd, proxySeq uint64) {
	ins.Mu.Lock()
	if ins.Proxy != proxy || ins.Cmd != cmd || ins.proxySeq != proxySeq {
		ins.Mu.Unlock()
		return
	}
	ins.Proxy = nil
	ins.Cmd = nil
	devNull := ins.DevNull
	ins.DevNull = nil
	outputDone := ins.outputDone
	ins.outputDone = nil
	if ins.Runtime.RuntimeCode == RuntimeCodeRunning {
		ins.Runtime.RuntimeCode = RuntimeCodeUnexpectedExit
	}
	ins.Runtime.PID = 0
	ins.Runtime.ExitTime = time.Now()
	outputCh := ins.OutputCh
	runtimeID := ins.runtimeID
	if runtimeID == "" {
		runtimeID = ins.Runtime.RuntimeName
	}
	if runtimeID == "" {
		runtimeID = ins.Runtime.Name
	}
	cleanupCommand := strings.TrimSpace(ins.CleanupCommand)
	cleanupPath := ins.Path
	shouldCleanup := cleanupCommand != "" && !ins.skipCleanup && ins.Runtime.RuntimeCode != RuntimeCodeManualKill
	if shouldCleanup {
		ins.State = instanceCleaning
		ins.Runtime.Lifecycle = InstanceLifecycleCleaning
		runtime := ins.Runtime
		ins.Mu.Unlock()
		waitDaemonOutputDone(outputDone)
		if devNull != nil {
			_ = devNull.Close()
		}
		ins.runCleanupCommand(cleanupPath, cleanupCommand, outputCh, runtimeID)
		ins.finishCleanupExit(proxySeq, runtime)
		return
	}
	ins.State = instanceStopped
	ins.Runtime.Lifecycle = InstanceLifecycleStopped
	runtime := ins.Runtime
	ins.Mu.Unlock()
	waitDaemonOutputDone(outputDone)
	if devNull != nil {
		_ = devNull.Close()
	}

	ins.sendRuntimeEvent(outputCh, IPCResponse{Type: "instance_exited", Instance: runtimeID, State: &runtime})
}

func waitDaemonOutputDone(done <-chan struct{}) {
	if done == nil {
		return
	}
	<-done
}

func (ins *DaemonInstance) runCleanupCommand(path string, command string, outputCh chan<- IPCResponse, runtimeID string) {
	cmd, err := terminal.BuildCommand(path, command)
	if err != nil {
		ins.sendCleanupMessage(outputCh, runtimeID, cleanupMessageBuildFailed, err.Error())
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		ins.sendCleanupMessage(outputCh, runtimeID, cleanupMessageStdoutFailed, err.Error())
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		ins.sendCleanupMessage(outputCh, runtimeID, cleanupMessageStderrFailed, err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		ins.sendCleanupMessage(outputCh, runtimeID, cleanupMessageStartFailed, err.Error())
		return
	}

	ins.Mu.Lock()
	if ins.State == instanceCleaning {
		ins.CleanupCmd = cmd
	}
	ins.Mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(2)
	go ins.copyCleanupOutput(&wg, outputCh, runtimeID, stdout)
	go ins.copyCleanupOutput(&wg, outputCh, runtimeID, stderr)
	ins.sendCleanupMessage(outputCh, runtimeID, cleanupMessageStarted)
	waitErr := cmd.Wait()
	wg.Wait()
	if waitErr != nil {
		ins.sendCleanupMessage(outputCh, runtimeID, cleanupMessageExited, waitErr.Error())
	} else {
		ins.sendCleanupMessage(outputCh, runtimeID, cleanupMessageCompleted)
	}
}

func (ins *DaemonInstance) copyCleanupOutput(wg *sync.WaitGroup, outputCh chan<- IPCResponse, runtimeID string, src io.ReadCloser) {
	defer wg.Done()
	defer src.Close()
	buf := make([]byte, daemonOutputReadBufferSize)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			ins.sendOutput(outputCh, runtimeID, data)
		}
		if err != nil {
			return
		}
	}
}

const (
	cleanupMessageBuildFailed  = "cleanup_command.build_failed"
	cleanupMessageStdoutFailed = "cleanup_command.stdout_failed"
	cleanupMessageStderrFailed = "cleanup_command.stderr_failed"
	cleanupMessageStartFailed  = "cleanup_command.start_failed"
	cleanupMessageStarted      = "cleanup_command.started"
	cleanupMessageExited       = "cleanup_command.exited"
	cleanupMessageCompleted    = "cleanup_command.completed"
)

func (ins *DaemonInstance) sendCleanupMessage(outputCh chan<- IPCResponse, runtimeID string, placeholder string, args ...string) {
	if outputCh == nil || runtimeID == "" || strings.TrimSpace(placeholder) == "" {
		return
	}
	select {
	case outputCh <- IPCResponse{Type: ipcFrameTypeCleanupMessage, Instance: runtimeID, Placeholder: placeholder, Args: args}:
	default:
		log.Printf("instance %s cleanup message %s dropped", runtimeID, placeholder)
	}
}

func (ins *DaemonInstance) sendOutput(outputCh chan<- IPCResponse, runtimeID string, data []byte) {
	if outputCh == nil || runtimeID == "" || len(data) == 0 {
		return
	}
	select {
	case outputCh <- IPCResponse{Type: ipcFrameTypeInstanceOutput, Instance: runtimeID, Body: data}:
	default:
	}
}

func (ins *DaemonInstance) finishCleanupExit(proxySeq uint64, runtime InstanceRuntimeState) {
	ins.Mu.Lock()
	if ins.proxySeq != proxySeq || ins.State != instanceCleaning {
		ins.Mu.Unlock()
		return
	}
	ins.CleanupCmd = nil
	ins.State = instanceStopped
	ins.Runtime.Lifecycle = InstanceLifecycleStopped
	ins.Runtime.PID = 0
	ins.Runtime.ExitTime = time.Now()
	runtime = ins.Runtime
	outputCh := ins.OutputCh
	runtimeID := ins.runtimeID
	if runtimeID == "" {
		runtimeID = runtime.RuntimeName
	}
	if runtimeID == "" {
		runtimeID = runtime.Name
	}
	ins.Mu.Unlock()

	ins.sendRuntimeEvent(outputCh, IPCResponse{Type: "instance_exited", Instance: runtimeID, State: &runtime})
}

func (ins *DaemonInstance) sendRuntimeEvent(outputCh chan<- IPCResponse, resp IPCResponse) {
	if outputCh == nil {
		resp.Release()
		return
	}
	timer := time.NewTimer(daemonRuntimeEventSendTimeout)
	defer timer.Stop()
	select {
	case outputCh <- resp:
	case <-timer.C:
		resp.Release()
		log.Printf("instance %s runtime event %s dropped after waiting %s", resp.Instance, resp.Type, daemonRuntimeEventSendTimeout)
	}
}

func (ins *DaemonInstance) Shutdown() {
	ins.Mu.Lock()
	proxy := ins.Proxy
	cmd := ins.Cmd
	devNull := ins.DevNull
	cleanupCmd := ins.CleanupCmd
	ins.Proxy = nil
	ins.Cmd = nil
	ins.CleanupCmd = nil
	ins.DevNull = nil
	ins.outputDone = nil
	ins.State = instanceStopped
	ins.Runtime.Lifecycle = InstanceLifecycleStopped
	ins.OutputCh = nil
	ins.Mu.Unlock()
	if proxy != nil {
		proxy.Kill()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if cleanupCmd != nil && cleanupCmd.Process != nil {
		_ = cleanupCmd.Process.Kill()
	}
	if devNull != nil {
		_ = devNull.Close()
	}
}

func isBenignReadError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "handle is invalid") ||
		strings.Contains(msg, "pipe has been ended") ||
		strings.Contains(msg, "read/write on closed pipe")
}
