//go:build !windows

package terminal

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
)

// ProcessTree tracks a Unix process group for a managed instance process.
// It is a best-effort containment boundary: descendants that create a new
// session or move to another process group may escape this process group.
// Residual cleanup after the main process exits is also best-effort because
// Unix process group IDs can be reused after the original group disappears.
type ProcessTree struct {
	mu     sync.Mutex
	pgid   int
	closed bool
}

func NewProcessTree() (*ProcessTree, error) {
	return &ProcessTree{}, nil
}

func IsProcessTreeRequired() bool {
	return true
}

func (t *ProcessTree) PrepareCommand(cmd *exec.Cmd, usePTY bool) {
	if t == nil || cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	if usePTY {
		cmd.SysProcAttr.Setsid = true
		cmd.SysProcAttr.Setctty = true
		return
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
}

func (t *ProcessTree) UsesSoftSignal() bool {
	return true
}

func (t *ProcessTree) AttachCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("process is not started")
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return ErrProcessAlreadyExited
		}
		return fmt.Errorf("get process group: %w", err)
	}
	if err := validateManagedProcessGroup(pgid); err != nil {
		return err
	}
	t.mu.Lock()
	t.pgid = pgid
	t.mu.Unlock()
	return nil
}

func (t *ProcessTree) AttachPID(pid int) error {
	if pid <= 0 {
		return errors.New("process is not started")
	}
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return ErrProcessAlreadyExited
		}
		return fmt.Errorf("get process group: %w", err)
	}
	if err := validateManagedProcessGroup(pgid); err != nil {
		return err
	}
	t.mu.Lock()
	t.pgid = pgid
	t.mu.Unlock()
	return nil
}

func (t *ProcessTree) Interrupt() error {
	return t.signal(syscall.SIGINT)
}

func (t *ProcessTree) Terminate() error {
	return t.signal(syscall.SIGTERM)
}

func (t *ProcessTree) Kill() error {
	return t.signal(syscall.SIGKILL)
}

// CloseAndKillResidual performs best-effort residual process cleanup, then
// closes this process tree so later cleanup paths do not signal the same PGID
// again. Callers must not treat this as a strict guarantee: escaped descendants
// and PGID reuse remain possible on Unix.
func (t *ProcessTree) CloseAndKillResidual() error {
	if t == nil {
		return nil
	}
	if err := t.Kill(); err != nil {
		closeErr := t.Close()
		return errors.Join(err, closeErr)
	}
	return t.Close()
}

func (t *ProcessTree) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	return nil
}

func (t *ProcessTree) signal(sig syscall.Signal) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	pgid := t.pgid
	closed := t.closed
	t.mu.Unlock()
	if closed || pgid <= 0 {
		return nil
	}
	if err := validateManagedProcessGroup(pgid); err != nil {
		return err
	}
	if err := syscall.Kill(-pgid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("send signal %s to process group %d: %w", sig, pgid, err)
	}
	return nil
}

func validateManagedProcessGroup(pgid int) error {
	if pgid <= 0 {
		return fmt.Errorf("invalid process group %d", pgid)
	}
	daemonPGID := syscall.Getpgrp()
	if daemonPGID <= 0 {
		return fmt.Errorf("invalid daemon process group %d", daemonPGID)
	}
	if pgid == daemonPGID {
		return fmt.Errorf("refuse to manage daemon process group %d", pgid)
	}
	return nil
}
