//go:build !linux && !windows

package main

import (
	"os/exec"
	"syscall"
)

func prepareControllerCmd(cmd *exec.Cmd) {
}

func attachControllerLifecycle(proc *controllerProcess) error {
	return nil
}

func detachControllerLifecycle(proc *controllerProcess) {
}

func sendControllerTerminateSignal(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
}
