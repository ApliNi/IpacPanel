//go:build windows

package terminal

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ProcessTree tracks a Windows Job Object for a managed instance process.
// Attaching a process after it has started is best-effort: without creating the
// process suspended, the process may exit or spawn children before assignment.
// The job is configured with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE by design, so
// Close/CloseAndKillResidual release the handle and may terminate residual
// assigned processes when the last job handle is closed.
type ProcessTree struct {
	mu      sync.Mutex
	job     windows.Handle
	managed bool
	closed  bool
}

func NewProcessTree() (*ProcessTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		closeErr := windows.CloseHandle(job)
		if closeErr != nil {
			return nil, fmt.Errorf("set job object information: %w; close job object: %v", err, closeErr)
		}
		return nil, fmt.Errorf("set job object information: %w", err)
	}
	return &ProcessTree{job: job}, nil
}

func IsProcessTreeRequired() bool {
	return false
}

// PrepareCommand is intentionally a no-op on Windows. ProcessTree assignment is
// currently performed after process start, so job containment is best-effort and
// does not provide a race-free start guarantee.
func (t *ProcessTree) PrepareCommand(cmd *exec.Cmd, usePTY bool) {}

func (t *ProcessTree) UsesSoftSignal() bool {
	return false
}

func (t *ProcessTree) AttachCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("process is not started")
	}
	return t.AttachPID(cmd.Process.Pid)
}

func (t *ProcessTree) AttachPID(pid int) error {
	if t == nil {
		return nil
	}
	if pid <= 0 {
		return errors.New("process is not started")
	}
	t.mu.Lock()
	job := t.job
	closed := t.closed
	t.mu.Unlock()
	if closed || job == 0 {
		return nil
	}
	processHandle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open process for job assignment: %w", err)
	}
	defer windows.CloseHandle(processHandle)
	if err := windows.AssignProcessToJobObject(job, processHandle); err != nil {
		return fmt.Errorf("assign process to job object: %w", err)
	}
	t.mu.Lock()
	t.managed = true
	t.mu.Unlock()
	return nil
}

func (t *ProcessTree) Interrupt() error {
	return nil
}

func (t *ProcessTree) Terminate() error {
	return t.Kill()
}

func (t *ProcessTree) Kill() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	job := t.job
	managed := t.managed
	closed := t.closed
	t.mu.Unlock()
	if closed || job == 0 || !managed {
		return nil
	}
	if err := windows.TerminateJobObject(job, 1); err != nil && !isWindowsJobAlreadyDoneError(err) {
		return fmt.Errorf("terminate job object: %w", err)
	}
	return nil
}

func (t *ProcessTree) Close() error {
	return t.CloseAndKillResidual()
}

// CloseAndKillResidual releases the Job Object handle. Because the job has
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, this is the intended residual cleanup path
// for any still-assigned processes, but assignment itself remains best-effort.
func (t *ProcessTree) CloseAndKillResidual() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	job := t.job
	t.job = 0
	t.closed = true
	t.mu.Unlock()
	if job == 0 {
		return nil
	}
	if err := windows.CloseHandle(job); err != nil && !isWindowsJobAlreadyDoneError(err) {
		return fmt.Errorf("close job object: %w", err)
	}
	return nil
}

func isWindowsJobAlreadyDoneError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "invalid handle") ||
		strings.Contains(msg, "handle is invalid")
}
