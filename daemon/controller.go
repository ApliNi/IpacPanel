package main

import (
	"IpacPanel/daemon/compat"
	"IpacPanel/daemon/version"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type controllerProcess struct {
	cmd   *exec.Cmd
	ipc   *IPCConn
	ready chan struct{}
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

type controllerUpdateResult struct {
	ControllerPath string
	BackupPath     string
	Updated        bool
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

func getUpdateDir(baseDir string) string {
	return filepath.Join(baseDir, "data", "update")
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

func controllerExecutableMode(info os.FileInfo) os.FileMode {
	mode := info.Mode().Perm()
	if runtime.GOOS != "windows" && mode&0111 == 0 {
		mode |= 0755
	}
	return mode
}

func trySetExecutableMode(path string, mode os.FileMode) {
	if runtime.GOOS == "windows" {
		return
	}
	_ = os.Chmod(path, mode)
}

func copyFileAtomic(srcPath string, dstPath string, mode os.FileMode) error {
	tmpPath := fmt.Sprintf("%s.tmp.%d", dstPath, time.Now().UnixNano())
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create temp backup: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("copy backup data: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := dst.Chmod(mode); err != nil {
			_ = dst.Close()
			return fmt.Errorf("chmod temp backup: %w", err)
		}
	}
	if err := dst.Sync(); err != nil {
		_ = dst.Close()
		return fmt.Errorf("sync temp backup: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close temp backup: %w", err)
	}

	if err := compat.ReplaceFileAtomic(tmpPath, dstPath); err != nil {
		return fmt.Errorf("replace backup: %w", err)
	}
	cleanupTmp = false
	if err := compat.SyncDirIfPossible(filepath.Dir(dstPath)); err != nil {
		return fmt.Errorf("sync backup directory: %w", err)
	}
	return nil
}

func rollbackControllerUpdate(update controllerUpdateResult) error {
	if !update.Updated || update.BackupPath == "" {
		return fmt.Errorf("no controller backup available")
	}
	if _, err := os.Stat(update.BackupPath); err != nil {
		return fmt.Errorf("stat controller backup: %w", err)
	}
	if err := compat.ReplaceFileAtomic(update.BackupPath, update.ControllerPath); err != nil {
		return fmt.Errorf("restore controller backup: %w", err)
	}
	if err := compat.SyncDirIfPossible(filepath.Dir(update.ControllerPath)); err != nil {
		return fmt.Errorf("sync controller directory after rollback: %w", err)
	}
	return nil
}

func cleanupControllerBackup(update controllerUpdateResult) {
	if !update.Updated || update.BackupPath == "" {
		return
	}
	if err := os.Remove(update.BackupPath); err != nil && !os.IsNotExist(err) {
		log.Printf("remove controller backup failed: %v", err)
	}
}

func tryUpdateController(controllerPath string) (controllerUpdateResult, error) {
	result := controllerUpdateResult{ControllerPath: controllerPath}
	updateDir := getUpdateDir(appBaseDir)
	updateBinary := filepath.Join(updateDir, controllerBinaryName())

	controllerInfo, err := os.Stat(controllerPath)
	if err != nil {
		return result, fmt.Errorf("stat controller binary: %w", err)
	}
	controllerMode := controllerExecutableMode(controllerInfo)

	info, err := os.Stat(updateBinary)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, fmt.Errorf("stat update binary: %w", err)
	}
	if info.IsDir() {
		log.Printf("update path is a directory, skipping")
		return result, nil
	}

	updateMode := controllerExecutableMode(info)
	trySetExecutableMode(updateBinary, updateMode)

	log.Printf("found controller update, validating version...")
	if err := validateControllerProtocol(updateBinary); err != nil {
		log.Printf("update validation failed: %v, discarding update", err)
		os.Remove(updateBinary)
		return result, nil
	}

	backupPath := controllerPath + ".backup"
	log.Printf("copying current controller backup to %s", backupPath)
	if err := copyFileAtomic(controllerPath, backupPath, controllerMode); err != nil {
		return result, fmt.Errorf("backup controller: %w", err)
	}

	log.Printf("installing update from %s", updateBinary)
	if err := compat.ReplaceFileAtomic(updateBinary, controllerPath); err != nil {
		if rollbackErr := compat.ReplaceFileAtomic(backupPath, controllerPath); rollbackErr != nil {
			return result, fmt.Errorf("install update: %w; rollback failed: %v", err, rollbackErr)
		}
		return result, fmt.Errorf("install update: %w", err)
	}
	result.BackupPath = backupPath
	result.Updated = true
	trySetExecutableMode(controllerPath, controllerMode)
	if err := compat.SyncDirIfPossible(filepath.Dir(controllerPath)); err != nil {
		if rollbackErr := rollbackControllerUpdate(result); rollbackErr != nil {
			return result, fmt.Errorf("sync controller directory: %w; rollback failed: %v", err, rollbackErr)
		}
		result.Updated = false
		result.BackupPath = ""
		return result, fmt.Errorf("sync controller directory: %w", err)
	}

	log.Printf("controller updated successfully")
	return result, nil
}

func startController(binaryPath string, firstStart bool) (*controllerProcess, error) {
	cmd := exec.Command(binaryPath,
		"--daemon-stdio",
		fmt.Sprintf("--daemon-first-start=%t", firstStart),
	)
	cmd.Stderr = os.Stderr
	cmd.Dir = appBaseDir
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
	return &controllerProcess{
		cmd:   cmd,
		ipc:   ipcConn,
		ready: make(chan struct{}),
	}, nil
}

func monitorController(proc *controllerProcess) error {
	return proc.cmd.Wait()
}

func controllerLoop(controllerPath string, server *IPCServer) error {
	firstControllerStart := true
	for {
		update, err := tryUpdateController(controllerPath)
		if err != nil {
			log.Printf("controller update error: %v", err)
		}
		controllerPath = update.ControllerPath
		if firstControllerStart {
			log.Printf("validating controller protocol...")
			if err := validateControllerProtocol(controllerPath); err != nil {
				if update.Updated {
					log.Printf("updated controller validation failed, rolling back: %v", err)
					if rollbackErr := rollbackControllerUpdate(update); rollbackErr != nil {
						return fmt.Errorf("validate updated controller: %w; rollback failed: %v", err, rollbackErr)
					}
					update = controllerUpdateResult{}
					if err := validateControllerProtocol(controllerPath); err != nil {
						return fmt.Errorf("validate rolled back controller before first start: %w", err)
					}
				} else {
					return fmt.Errorf("validate controller before first start: %w", err)
				}
			}
		}

		proc, err := startController(controllerPath, firstControllerStart)
		if err != nil {
			if update.Updated {
				log.Printf("updated controller failed to start, rolling back: %v", err)
				if rollbackErr := rollbackControllerUpdate(update); rollbackErr != nil {
					return fmt.Errorf("start updated controller: %w; rollback failed: %v", err, rollbackErr)
				}
				update = controllerUpdateResult{}
				proc, err = startController(controllerPath, firstControllerStart)
				if err != nil {
					return fmt.Errorf("start rolled back controller: %w", err)
				}
			} else {
				if firstControllerStart {
					return err
				}
				log.Printf("start controller failed: %v, retrying in 3 seconds", err)
				time.Sleep(3 * time.Second)
				continue
			}
		}
		server.Bind(proc.ipc)
		go server.Serve(proc.ready)
		if firstControllerStart {
			firstControllerStart = false
		}

		monitorDone := make(chan error, 1)
		go func() {
			monitorDone <- monitorController(proc)
		}()
		if update.Updated {
			select {
			case err = <-monitorDone:
				log.Printf("updated controller exited before ready, rolling back")
				if rollbackErr := rollbackControllerUpdate(update); rollbackErr != nil {
					if err != nil {
						return fmt.Errorf("updated controller exited before ready: %w; rollback failed: %v", err, rollbackErr)
					}
					return fmt.Errorf("updated controller exited before ready; rollback failed: %v", rollbackErr)
				}
			case <-proc.ready:
				cleanupControllerBackup(update)
				err = <-monitorDone
			}
			if err != nil {
				server.WaitServeDone(2 * time.Second)
			}
		} else {
			err = <-monitorDone
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

		log.Printf("waiting 2 seconds before checking for updates...")
		time.Sleep(2 * time.Second)
		server.WaitServeDone(2 * time.Second)

		// Do not stop daemon-held instances when the controller exits. Controller
		// restarts are part of the update path, and instance lifetime belongs to the daemon.
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
