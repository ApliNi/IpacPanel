package main

import (
	"IpacPanel/daemon/terminal"
	"IpacPanel/daemon/version"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type controllerProcess struct {
	cmd         *exec.Cmd
	ipc         *IPCConn
	ready       chan struct{}
	exited      chan struct{}
	processTree *terminal.ProcessTree
}

type controllerPipeCloser struct {
	stdin  io.Closer
	stdout io.Closer
}

func (c controllerPipeCloser) Close() error {
	var closeErr error
	if c.stdin != nil {
		closeErr = c.stdin.Close()
	}
	if c.stdout != nil {
		if err := c.stdout.Close(); closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

type controllerVersionInfo struct {
	Role           string `yaml:"role"`
	Version        string `yaml:"version"`
	DaemonProtocol int    `yaml:"daemon_protocol"`
}

func controllerBinaryName() string {
	name := "IpacPanel_Controller"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func findControllerBinary() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get executable path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		resolved = exePath
	}
	dir := filepath.Dir(resolved)
	candidate := filepath.Join(dir, controllerBinaryName())
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("controller binary not found at %s: %w", candidate, err)
	}
	return candidate, nil
}

func checkControllerVersion(binaryPath string) (*controllerVersionInfo, error) {
	cmd := exec.Command(binaryPath, "--version")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("run --version: %w", err)
	}
	var wrapper struct {
		Version controllerVersionInfo `yaml:"version"`
	}
	if err := yaml.Unmarshal(output, &wrapper); err != nil {
		return nil, fmt.Errorf("parse --version output: %w", err)
	}
	if wrapper.Version.Role != "controller" {
		return nil, fmt.Errorf("expected role=controller, got %s", wrapper.Version.Role)
	}
	return &wrapper.Version, nil
}

func validateControllerProtocol(binaryPath string) error {
	versionInfo, err := checkControllerVersion(binaryPath)
	if err != nil {
		return err
	}
	if versionInfo.DaemonProtocol != version.DaemonProtocol {
		return fmt.Errorf("daemon_protocol mismatch: controller=%d daemon=%d", versionInfo.DaemonProtocol, version.DaemonProtocol)
	}
	return nil
}

var (
	activeControllerMu   sync.Mutex
	activeControllerProc *controllerProcess
)

func setActiveController(proc *controllerProcess) {
	activeControllerMu.Lock()
	activeControllerProc = proc
	activeControllerMu.Unlock()
}

func getActiveController() *controllerProcess {
	activeControllerMu.Lock()
	defer activeControllerMu.Unlock()
	return activeControllerProc
}

func startController(binaryPath string, firstStart bool) (*controllerProcess, error) {
	cmd := exec.Command(binaryPath,
		"--daemon-stdio",
		fmt.Sprintf("--daemon-first-start=%t", firstStart),
	)
	cmd.Stderr = os.Stderr
	cmd.Dir = appBaseDir
	prepareControllerCmd(cmd)
	controllerStdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open controller stdin pipe: %w", err)
	}
	controllerStdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = controllerStdin.Close()
		return nil, fmt.Errorf("open controller stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = controllerStdin.Close()
		_ = controllerStdout.Close()
		return nil, fmt.Errorf("start controller: %w", err)
	}
	log.Printf("controller started [pid=%d, ipc=stdio]", cmd.Process.Pid)
	ipcConn := NewIPCConn(controllerStdout, controllerStdin, controllerPipeCloser{
		stdin:  controllerStdin,
		stdout: controllerStdout,
	})
	ipcConn.SetDebug(ipcDebug.Load())
	proc := &controllerProcess{
		cmd:    cmd,
		ipc:    ipcConn,
		ready:  make(chan struct{}),
		exited: make(chan struct{}),
	}
	if err := attachControllerLifecycle(proc); err != nil {
		log.Printf("attach controller lifecycle warning: %v", err)
	}
	return proc, nil
}

func monitorController(proc *controllerProcess) error {
	return proc.cmd.Wait()
}

func controllerLoop(controllerPath string, server *IPCServer) error {
	firstControllerStart := true
	for {
		if isDaemonStopping() {
			log.Printf("daemon is stopping, controller loop exiting")
			return nil
		}

		if firstControllerStart {
			log.Printf("validating controller protocol...")
			if err := validateControllerProtocol(controllerPath); err != nil {
				return fmt.Errorf("validate controller before first start: %w", err)
			}
		}

		proc, err := startController(controllerPath, firstControllerStart)
		if err != nil {
			if firstControllerStart {
				return err
			}
			log.Printf("start controller failed: %v, retrying in 3 seconds", err)
			time.Sleep(3 * time.Second)
			continue
		}
		setActiveController(proc)
		server.Bind(proc.ipc)
		go server.Serve(proc.ready)
		firstControllerStart = false

		monitorDone := make(chan error, 1)
		go func() {
			err := monitorController(proc)
			close(proc.exited)
			monitorDone <- err
		}()
		err = <-monitorDone
		setActiveController(nil)
		detachControllerLifecycle(proc)

		if isDaemonStopping() {
			log.Printf("daemon is stopping, controller will not be restarted")
			return nil
		}

		exitCode := -1
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
			log.Printf("controller exited with error (code=%d): %v", exitCode, err)
		} else {
			log.Printf("controller exited normally")
		}

		server.WaitServeDone(2 * time.Second)

		if server.ConsumeControllerUpdateRestartPending() {
			log.Printf("controller exited for update, restarting immediately...")
		} else {
			log.Printf("waiting 2 seconds before restarting controller...")
			time.Sleep(2 * time.Second)
		}

		// Do not stop daemon-held instances when the controller exits. Controller
		// restarts are part of the update path, and instance lifetime belongs to the daemon.
	}
}

func shutdownController() {
	proc := getActiveController()
	if proc == nil || proc.cmd == nil || proc.cmd.Process == nil {
		return
	}

	select {
	case <-proc.exited:
		return
	default:
	}

	log.Printf("stopping controller [pid=%d]...", proc.cmd.Process.Pid)
	if proc.ipc != nil {
		_ = proc.ipc.Close()
	}
	sendControllerTerminateSignal(proc.cmd)

	select {
	case <-proc.exited:
		log.Printf("controller stopped cleanly")
	case <-time.After(3 * time.Second):
		log.Printf("controller did not exit in 3 seconds, killing [pid=%d]...", proc.cmd.Process.Pid)
		_ = proc.cmd.Process.Kill()
		if proc.processTree != nil {
			_ = proc.processTree.Kill()
			_ = proc.processTree.Close()
		}
	}
}

func shutdownAllInstances() {
	instances := instanceMgr.List()
	for _, ins := range instances {
		ins.Shutdown()
	}
}

func resolveInstancePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "./instances/"
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Clean(filepath.Join(appBaseDir, path)), nil
}
