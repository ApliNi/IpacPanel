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
	"slices"
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

const daemonOutputDoneWaitTimeout = 2 * time.Second

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
	Name               string
	CommandArgv        []string
	CleanupCommandArgv []string
	Path               string
	Terminal           int
	InputEnc           string
	OutputEnc          string
	Cols               uint16
	Rows               uint16
	State              instanceState
	Runtime            InstanceRuntimeState
	Proxy              *terminal.Proxy
	Cmd                *exec.Cmd
	ProcessTree        *terminal.ProcessTree
	CleanupCmd         *exec.Cmd
	CleanupTree        *terminal.ProcessTree
	DevNull            *os.File
	OutputCh           chan<- IPCResponse
	EventCh            chan<- IPCResponse
	runtimeID          string
	proxySeq           uint64
	outputDone         chan struct{}
	skipCleanup        bool
	Mu                 sync.Mutex
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
	if ins.State != instanceStopped && ins.Runtime.RuntimeAlias == "" && ins.Runtime.InstanceName == oldName {
		ins.Runtime.RuntimeAlias = oldName
	}
	ins.Name = newName
	ins.Runtime.InstanceName = newName
	ins.Mu.Unlock()
	m.instances[newName] = ins
	return nil
}

func (m *InstanceManager) prepareInstanceForStartLocked(req *IPCRequest) *DaemonInstance {
	ins, ok := m.instances[req.Instance]
	if !ok || ins == nil {
		// StartCount 语义: 未曾启动为 -1, 每次成功启动 +1.
		ins = &DaemonInstance{Name: req.Instance, Runtime: InstanceRuntimeState{RestartCount: -1}}
	}
	ins.Mu.Lock()
	ins.Name = req.Instance
	ins.CommandArgv = slices.Clone(req.CommandArgv)
	ins.CleanupCommandArgv = slices.Clone(req.CleanupCommandArgv)
	ins.Path = req.Path
	ins.Terminal = NormalizeTerminalMode(req.Terminal)
	ins.InputEnc = req.InputEnc
	ins.OutputEnc = req.OutputEnc
	ins.Cols = req.Cols
	ins.Rows = req.Rows
	if ins.Runtime.InstanceName == "" {
		ins.Runtime.InstanceName = req.Instance
	}
	ins.Mu.Unlock()
	return ins
}

func (ins *DaemonInstance) UpdateRuntimeConfig(cleanupCommandArgv []string) {
	ins.Mu.Lock()
	defer ins.Mu.Unlock()
	ins.CleanupCommandArgv = slices.Clone(cleanupCommandArgv)
}

func (ins *DaemonInstance) RuntimeSnapshot() InstanceRuntimeState {
	ins.Mu.Lock()
	defer ins.Mu.Unlock()
	rt := ins.Runtime
	if rt.InstanceName == "" {
		rt.InstanceName = ins.Name
	}
	if rt.RuntimeAlias == "" && ins.runtimeID != "" && ins.runtimeID != rt.InstanceName {
		rt.RuntimeAlias = ins.runtimeID
	}
	rt.Lifecycle = instanceLifecycle(ins.State)
	return rt
}

func (ins *DaemonInstance) Start(outputCh chan<- IPCResponse, eventCh chan<- IPCResponse) error {
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
	var processTree *terminal.ProcessTree
	var devNull *os.File
	var pid int
	if IsNoTerminal(ins.Terminal) {
		cmd, processTree, devNull, err = startNoTerminalProcess(resolvedPath, ins.CommandArgv)
		if err != nil {
			return fmt.Errorf("start instance: %w", err)
		}
		if cmd.Process != nil {
			pid = cmd.Process.Pid
		}
	} else {
		proxy, err = terminal.Start(resolvedPath, ins.CommandArgv, IsPTYTerminal(ins.Terminal), ins.InputEnc, ins.OutputEnc, ins.Cols, ins.Rows)
		if err != nil {
			return fmt.Errorf("start instance: %w", err)
		}
		pid = proxy.PID()
	}

	runtimeID := ins.Name
	ins.Proxy = proxy
	ins.Cmd = cmd
	ins.ProcessTree = processTree
	ins.DevNull = devNull
	ins.OutputCh = outputCh
	ins.EventCh = eventCh
	if proxy != nil {
		ins.outputDone = make(chan struct{})
	} else {
		ins.outputDone = nil
	}
	ins.State = instanceRunning
	ins.runtimeID = runtimeID
	ins.skipCleanup = false
	// StartCount 语义: 未曾启动为 -1, 每次成功启动 +1.
	startCount := ins.Runtime.RestartCount + 1
	ins.Runtime = InstanceRuntimeState{
		InstanceName: ins.Name,
		RuntimeAlias: "",
		Lifecycle:    InstanceLifecycleRunning,
		RuntimeCode:  RuntimeCodeRunning,
		PID:          pid,
		StartTime:    time.Now(),
		RestartCount: startCount,
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

func startNoTerminalProcess(path string, argv []string) (*exec.Cmd, *terminal.ProcessTree, *os.File, error) {
	cmd, err := terminal.BuildCommand(path, argv)
	if err != nil {
		return nil, nil, nil, err
	}
	processTree, err := terminal.NewProcessTree()
	if err != nil {
		if terminal.IsProcessTreeRequired() {
			return nil, nil, nil, err
		}
		log.Printf("no-terminal process tree unavailable: %v", err)
	}
	if processTree != nil {
		processTree.PrepareCommand(cmd, false)
	}
	terminal.PreventConsoleInheritance(cmd)
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		if processTree != nil {
			_ = processTree.Close()
		}
		return nil, nil, nil, fmt.Errorf("open dev null: %w", err)
	}
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	if err := cmd.Start(); err != nil {
		_ = devNull.Close()
		if processTree != nil {
			_ = processTree.Close()
		}
		return nil, nil, nil, err
	}
	if processTree != nil {
		if err := processTree.AttachCommand(cmd); err != nil {
			if errors.Is(err, terminal.ErrProcessAlreadyExited) {
				if closeErr := processTree.Close(); closeErr != nil {
					log.Printf("no-terminal process tree close error: %v", closeErr)
				}
				return cmd, nil, devNull, nil
			}
			_ = processTree.Close()
			if terminal.IsProcessTreeRequired() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				_ = devNull.Close()
				return nil, nil, nil, err
			}
			log.Printf("no-terminal process tree unavailable: %v", err)
			processTree = nil
		}
	}
	return cmd, processTree, devNull, nil
}

func (ins *DaemonInstance) Stop(force bool, runtimeCode int) {
	ins.Mu.Lock()
	if ins.State == instanceCleaning {
		if runtimeCode != RuntimeCodeUnknown {
			ins.markRuntimeCodeLocked(runtimeCode)
		}
		cleanupCmd := ins.CleanupCmd
		cleanupTree := ins.CleanupTree
		name := ins.Name
		ins.Mu.Unlock()
		if force && cleanupCmd != nil && cleanupCmd.Process != nil {
			if cleanupTree != nil {
				if err := cleanupTree.Kill(); err != nil {
					log.Printf("instance %s cleanup tree kill error: %v", name, err)
				}
			} else if err := cleanupCmd.Process.Kill(); err != nil {
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
	processTree := ins.ProcessTree
	name := ins.Name
	ins.Mu.Unlock()

	if proxy == nil && cmd != nil {
		if cmd.Process != nil {
			if force {
				killNoTerminalProcess(name, cmd, processTree)
			} else {
				stopNoTerminalProcess(name, cmd, processTree)
			}
		}
		return
	}
	if force {
		if err := proxy.Kill(); err != nil {
			log.Printf("instance %s proxy kill error: %v", name, err)
		}
		return
	}
	if err := proxy.Close(); err != nil {
		log.Printf("instance %s proxy close error: %v", name, err)
	}
}

func stopNoTerminalProcess(name string, cmd *exec.Cmd, processTree *terminal.ProcessTree) {
	if processTree != nil {
		if !processTree.UsesSoftSignal() {
			log.Printf("instance %s process tree does not support soft stop; using hard kill fallback", name)
			killNoTerminalProcess(name, cmd, processTree)
			return
		}
		if err := processTree.Interrupt(); err != nil {
			log.Printf("instance %s process tree interrupt error: %v", name, err)
			killNoTerminalProcess(name, cmd, processTree)
		}
		return
	}
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		log.Printf("instance %s interrupt error: %v", name, err)
		killNoTerminalProcess(name, cmd, nil)
	}
}

func killNoTerminalProcess(name string, cmd *exec.Cmd, processTree *terminal.ProcessTree) {
	if processTree != nil {
		if err := processTree.Kill(); err != nil {
			log.Printf("instance %s process tree kill error: %v", name, err)
		}
		return
	}
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := cmd.Process.Kill(); err != nil {
		log.Printf("instance %s kill error: %v", name, err)
	}
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
	if ins.Runtime.InstanceName == "" {
		ins.Runtime.InstanceName = ins.Name
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

func (ins *DaemonInstance) ResizeTerminal(cols, rows uint16) error {
	ins.Mu.Lock()
	defer ins.Mu.Unlock()
	if ins.Proxy == nil || IsNoTerminal(ins.Terminal) {
		return nil
	}
	if err := ins.Proxy.Resize(cols, rows); err != nil {
		return fmt.Errorf("resize terminal: %w", err)
	}
	return nil
}

func (ins *DaemonInstance) readOutput(proxy *terminal.Proxy, proxySeq uint64, runtimeID string, done chan struct{}) {
	defer proxy.NotifyReadClosed()
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
				case outputCh <- IPCResponse{Type: "o", Instance: runtimeID, Body: data, ReleaseFunc: func() { putDaemonOutputBuffer(releaseData) }}:
				default:
					putDaemonOutputBuffer(data)
					log.Printf("instance %s output dropped (channel full, %d bytes)", runtimeID, len(data))
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
	if err := proxy.Wait(); err != nil {
		log.Printf("instance proxy wait error: %v", err)
	}
	ins.finishProcessExit(proxy, nil, proxySeq)
}

func (ins *DaemonInstance) waitCommandExit(cmd *exec.Cmd, proxySeq uint64) {
	if err := cmd.Wait(); err != nil {
		log.Printf("instance command wait error: %v", err)
	}
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
	processTree := ins.ProcessTree
	ins.ProcessTree = nil
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
	eventCh := ins.EventCh
	runtimeID := ins.runtimeID
	if runtimeID == "" {
		runtimeID = ins.Runtime.RuntimeAlias
	}
	if runtimeID == "" {
		runtimeID = ins.Runtime.InstanceName
	}
	cleanupArgv := slices.Clone(ins.CleanupCommandArgv)
	cleanupPath := ins.Path
	shouldCleanup := len(cleanupArgv) > 0 && !ins.skipCleanup && ins.Runtime.RuntimeCode != RuntimeCodeManualKill
	if shouldCleanup {
		ins.State = instanceCleaning
		ins.Runtime.Lifecycle = InstanceLifecycleCleaning
		runtime := ins.Runtime
		ins.Mu.Unlock()
		waitDaemonOutputDone(runtimeID, outputDone)
		if devNull != nil {
			_ = devNull.Close()
		}
		cleanupProcessTree(runtimeID, processTree, "instance exit before cleanup command")
		ins.runCleanupCommand(cleanupPath, cleanupArgv, outputCh, eventCh, runtimeID)
		ins.finishCleanupExit(proxySeq, runtime)
		return
	}
	ins.State = instanceStopped
	ins.Runtime.Lifecycle = InstanceLifecycleStopped
	runtime := ins.Runtime
	ins.Mu.Unlock()
	waitDaemonOutputDone(runtimeID, outputDone)
	if devNull != nil {
		_ = devNull.Close()
	}
	cleanupProcessTree(runtimeID, processTree, "instance exit")

	ins.sendRuntimeEvent(eventCh, IPCResponse{Type: "instance_exited", State: &runtime})
}

func cleanupProcessTree(runtimeID string, processTree *terminal.ProcessTree, context string) {
	if processTree == nil {
		return
	}
	if err := processTree.CloseAndKillResidual(); err != nil {
		log.Printf("instance %s best-effort residual process tree cleanup error during %s: %v", runtimeID, context, err)
	}
}

func waitDaemonOutputDone(runtimeID string, done <-chan struct{}) {
	if done == nil {
		return
	}
	timer := time.NewTimer(daemonOutputDoneWaitTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		log.Printf("instance %s output reader did not finish after %s; continuing exit event", runtimeID, daemonOutputDoneWaitTimeout)
	}
}

func (ins *DaemonInstance) runCleanupCommand(path string, argv []string, outputCh chan<- IPCResponse, eventCh chan<- IPCResponse, runtimeID string) {
	if len(argv) == 0 {
		return
	}
	cmd, err := terminal.BuildCommand(path, argv)
	if err != nil {
		ins.sendCleanupMessage(eventCh, runtimeID, cleanupMessageBuildFailed, err.Error())
		return
	}
	terminal.PreventConsoleInheritance(cmd)
	processTree, err := terminal.NewProcessTree()
	if err != nil {
		if terminal.IsProcessTreeRequired() {
			ins.sendCleanupMessage(eventCh, runtimeID, cleanupMessageStartFailed, err.Error())
			return
		}
		log.Printf("instance %s cleanup process tree unavailable: %v", runtimeID, err)
	}
	if processTree != nil {
		processTree.PrepareCommand(cmd, false)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		if processTree != nil {
			_ = processTree.Close()
		}
		ins.sendCleanupMessage(eventCh, runtimeID, cleanupMessageStdoutFailed, err.Error())
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		if processTree != nil {
			_ = processTree.Close()
		}
		ins.sendCleanupMessage(eventCh, runtimeID, cleanupMessageStderrFailed, err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		if processTree != nil {
			_ = processTree.Close()
		}
		ins.sendCleanupMessage(eventCh, runtimeID, cleanupMessageStartFailed, err.Error())
		return
	}
	if processTree != nil {
		if err := processTree.AttachCommand(cmd); err != nil {
			if errors.Is(err, terminal.ErrProcessAlreadyExited) {
				if closeErr := processTree.Close(); closeErr != nil {
					log.Printf("instance %s cleanup process tree close error: %v", runtimeID, closeErr)
				}
				processTree = nil
			} else {
				_ = processTree.Close()
				if terminal.IsProcessTreeRequired() {
					_ = stdout.Close()
					_ = stderr.Close()
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					ins.sendCleanupMessage(eventCh, runtimeID, cleanupMessageStartFailed, err.Error())
					return
				}
				log.Printf("instance %s cleanup process tree unavailable: %v", runtimeID, err)
				processTree = nil
			}
		}
	}

	ins.Mu.Lock()
	if ins.State == instanceCleaning {
		ins.CleanupCmd = cmd
		ins.CleanupTree = processTree
	}
	ins.Mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(2)
	go ins.copyCleanupOutput(&wg, outputCh, runtimeID, stdout)
	go ins.copyCleanupOutput(&wg, outputCh, runtimeID, stderr)
	ins.sendCleanupMessage(eventCh, runtimeID, cleanupMessageStarted)
	waitErr := cmd.Wait()
	cleanupProcessTree(runtimeID, processTree, "cleanup command exit")
	wg.Wait()
	if waitErr != nil {
		ins.sendCleanupMessage(eventCh, runtimeID, cleanupMessageExited, waitErr.Error())
	} else {
		ins.sendCleanupMessage(eventCh, runtimeID, cleanupMessageCompleted)
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

func (ins *DaemonInstance) sendCleanupMessage(eventCh chan<- IPCResponse, runtimeID string, placeholder string, args ...string) {
	if eventCh == nil || runtimeID == "" || strings.TrimSpace(placeholder) == "" {
		return
	}
	select {
	case eventCh <- IPCResponse{Type: "cleanup_message", Instance: runtimeID, Placeholder: placeholder, Args: args}:
	default:
		log.Printf("instance %s cleanup message %s dropped", runtimeID, placeholder)
	}
}

func (ins *DaemonInstance) sendOutput(outputCh chan<- IPCResponse, runtimeID string, data []byte) {
	if outputCh == nil || runtimeID == "" || len(data) == 0 {
		return
	}
	select {
	case outputCh <- IPCResponse{Type: "o", Instance: runtimeID, Body: data}:
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
	cleanupTree := ins.CleanupTree
	ins.CleanupTree = nil
	ins.State = instanceStopped
	ins.Runtime.Lifecycle = InstanceLifecycleStopped
	ins.Runtime.PID = 0
	ins.Runtime.ExitTime = time.Now()
	runtime = ins.Runtime
	eventCh := ins.EventCh
	runtimeID := ins.runtimeID
	if runtimeID == "" {
		runtimeID = runtime.RuntimeAlias
	}
	if runtimeID == "" {
		runtimeID = runtime.InstanceName
	}
	ins.Mu.Unlock()
	if cleanupTree != nil {
		cleanupProcessTree(runtimeID, cleanupTree, "cleanup finalization")
	}

	ins.sendRuntimeEvent(eventCh, IPCResponse{Type: "instance_exited", State: &runtime})
}

func (ins *DaemonInstance) sendRuntimeEvent(eventCh chan<- IPCResponse, resp IPCResponse) {
	if eventCh == nil {
		resp.Release()
		return
	}
	select {
	case eventCh <- resp:
	default:
		instanceName := runtimeEventInstanceName(resp)
		eventType := resp.Type
		resp.Release()
		log.Printf("instance %s runtime event %s dropped (event channel full)", instanceName, eventType)
	}
}

func runtimeEventInstanceName(resp IPCResponse) string {
	if resp.Instance != "" {
		return resp.Instance
	}
	if resp.State == nil {
		return ""
	}
	if resp.State.RuntimeAlias != "" {
		return resp.State.RuntimeAlias
	}
	return resp.State.InstanceName
}

func (ins *DaemonInstance) Shutdown() {
	ins.Mu.Lock()
	proxy := ins.Proxy
	cmd := ins.Cmd
	processTree := ins.ProcessTree
	devNull := ins.DevNull
	cleanupCmd := ins.CleanupCmd
	cleanupTree := ins.CleanupTree
	ins.Proxy = nil
	ins.Cmd = nil
	ins.ProcessTree = nil
	ins.CleanupCmd = nil
	ins.CleanupTree = nil
	ins.DevNull = nil
	ins.outputDone = nil
	ins.State = instanceStopped
	ins.Runtime.Lifecycle = InstanceLifecycleStopped
	ins.OutputCh = nil
	ins.EventCh = nil
	ins.Mu.Unlock()
	if proxy != nil {
		if err := proxy.Kill(); err != nil {
			log.Printf("instance shutdown proxy kill error: %v", err)
		}
	}
	if cmd != nil && cmd.Process != nil {
		if processTree != nil {
			_ = processTree.Kill()
			_ = processTree.Close()
		} else {
			_ = cmd.Process.Kill()
		}
	}
	if cleanupCmd != nil && cleanupCmd.Process != nil {
		if cleanupTree != nil {
			_ = cleanupTree.Kill()
			_ = cleanupTree.Close()
		} else {
			_ = cleanupCmd.Process.Kill()
		}
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
