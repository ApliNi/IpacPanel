//go:build windows

package main

import (
	"IpacPanel/daemon/terminal"
	"fmt"
	"os/exec"
)

func prepareControllerCmd(cmd *exec.Cmd) {
}

func attachControllerLifecycle(proc *controllerProcess) error {
	if proc == nil || proc.cmd == nil || proc.cmd.Process == nil {
		return nil
	}
	pt, err := terminal.NewProcessTree()
	if err != nil {
		return fmt.Errorf("create process tree for controller: %w", err)
	}
	if err := pt.AttachCommand(proc.cmd); err != nil {
		_ = pt.Close()
		return fmt.Errorf("attach controller to job object: %w", err)
	}
	proc.processTree = pt
	return nil
}

func detachControllerLifecycle(proc *controllerProcess) {
	if proc != nil && proc.processTree != nil {
		_ = proc.processTree.Close()
		proc.processTree = nil
	}
}

func sendControllerTerminateSignal(cmd *exec.Cmd) {
	// Windows doesn't support SIGTERM; closing stdio pipe signals controller exit.
}
