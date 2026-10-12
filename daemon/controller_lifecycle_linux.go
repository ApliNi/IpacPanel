//go:build linux

package main

import (
	"os/exec"
	"syscall"
)

func prepareControllerCmd(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM
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
