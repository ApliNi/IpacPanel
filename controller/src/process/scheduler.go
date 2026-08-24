package process

import (
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/logbuf"
	"IpacPanel/controller/src/msg"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/go-co-op/gocron/v2"
	"github.com/gorilla/websocket"
)

type instanceTaskScheduler struct {
	Mu         sync.Mutex
	Cond       *sync.Cond
	Scheduler  gocron.Scheduler
	Jobs       map[string]gocron.Job
	Rebuilding map[string]bool
	Requested  map[string]uint64
	Completed  map[string]uint64
	Stopping   bool
	Ctx        context.Context
	Cancel     context.CancelFunc
}

var (
	taskSchedulerMu sync.RWMutex
	taskScheduler   *instanceTaskScheduler
)

func getTaskScheduler() *instanceTaskScheduler {
	taskSchedulerMu.RLock()
	ts := taskScheduler
	taskSchedulerMu.RUnlock()
	return ts
}

func initTaskScheduler() {
	taskSchedulerMu.Lock()
	defer taskSchedulerMu.Unlock()
	if taskScheduler != nil {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())

	s, err := gocron.NewScheduler()
	if err != nil {
		log.Fatalf("failed to create task scheduler: %v", err)
	}

	taskScheduler = &instanceTaskScheduler{
		Scheduler:  s,
		Jobs:       make(map[string]gocron.Job),
		Rebuilding: make(map[string]bool),
		Requested:  make(map[string]uint64),
		Completed:  make(map[string]uint64),
		Ctx:        ctx,
		Cancel:     cancel,
	}
	taskScheduler.Cond = sync.NewCond(&taskScheduler.Mu)

	s.Start()
}

func stopTaskScheduler() {
	taskSchedulerMu.Lock()
	ts := taskScheduler
	taskScheduler = nil
	taskSchedulerMu.Unlock()
	if ts == nil {
		return
	}

	ts.Mu.Lock()
	ts.Stopping = true
	for key, job := range ts.Jobs {
		ts.Scheduler.RemoveJob(job.ID())
		delete(ts.Jobs, key)
	}
	if ts.Cond != nil {
		ts.Cond.Broadcast()
	}
	ts.Mu.Unlock()

	// Shut down the scheduler and wait for running jobs to finish.
	_ = ts.Scheduler.Shutdown()

	if ts.Cancel != nil {
		ts.Cancel()
	}
}

func disconnectAllInstanceClients() {
	cfg.ManagerMu.RLock()
	processes := make([]*InstanceProcess, 0, len(InstanceProcesses))
	for _, sp := range InstanceProcesses {
		processes = append(processes, sp)
	}
	cfg.ManagerMu.RUnlock()

	for _, sp := range processes {
		if sp == nil {
			continue
		}
		sp.DetachAndCloseAllClients()
	}
}

func beginInstanceTaskRebuild(ts *instanceTaskScheduler, instanceName string) (uint64, bool, error) {
	ts.Mu.Lock()
	defer ts.Mu.Unlock()
	if ts.Stopping {
		return 0, false, fmt.Errorf(msg.SchedulerStopping)
	}
	ts.Requested[instanceName]++
	target := ts.Requested[instanceName]
	if ts.Rebuilding[instanceName] {
		for ts.Completed[instanceName] < target && !ts.Stopping {
			ts.Cond.Wait()
		}
		if ts.Completed[instanceName] >= target {
			return target, false, nil
		}
		return target, false, fmt.Errorf(msg.SchedulerStopping)
	}
	ts.Rebuilding[instanceName] = true
	return target, true, nil
}

func finishInstanceTaskRebuild(ts *instanceTaskScheduler, instanceName string, target uint64) (uint64, bool, error) {
	ts.Mu.Lock()
	defer ts.Mu.Unlock()
	if ts.Stopping {
		delete(ts.Rebuilding, instanceName)
		ts.Cond.Broadcast()
		return 0, false, fmt.Errorf(msg.SchedulerStopping)
	}
	if ts.Requested[instanceName] > target {
		return ts.Requested[instanceName], true, nil
	}
	ts.Completed[instanceName] = target
	delete(ts.Rebuilding, instanceName)
	ts.Cond.Broadcast()
	return target, false, nil
}

func deleteInstanceTaskJobsLocked(ts *instanceTaskScheduler, instanceName string) []string {
	var errs []string
	for key, job := range ts.Jobs {
		if strings.HasPrefix(key, instanceName+"::") {
			ts.Scheduler.RemoveJob(job.ID())
			delete(ts.Jobs, key)
		}
	}
	delete(ts.Requested, instanceName)
	delete(ts.Completed, instanceName)
	delete(ts.Rebuilding, instanceName)
	return errs
}

func taskJobKey(instanceName string, taskName string) string {
	return instanceName + "::" + taskName
}

func rebuildAllInstanceTasksLocked() {
	if err := rebuildAllInstanceTasks(); err != nil {
		log.Printf(msg.RebuildInstanceScheduledTasksFailedLogFmt, msg.RebuildAllInstanceTasks, err)
	}
}

func rebuildAllInstanceTasks() error {
	if getTaskScheduler() == nil {
		return fmt.Errorf(msg.SchedulerNotInitialized)
	}

	errs := make([]error, 0)
	for _, sp := range List() {
		if sp == nil {
			continue
		}
		instanceName := sp.InstanceSnapshot().Name
		if err := rebuildInstanceTasks(instanceName); err != nil {
			log.Printf(msg.RebuildInstanceScheduledTasksFailedLogFmt, instanceName, err)
			errs = append(errs, fmt.Errorf("%s: %w", instanceName, err))
		}
	}
	return errors.Join(errs...)
}

func rebuildInstanceTasks(instanceName string) error {
	ts := getTaskScheduler()
	if ts == nil {
		return fmt.Errorf(msg.SchedulerNotInitialized)
	}
	instanceName = strings.TrimSpace(instanceName)
	if instanceName == "" {
		return fmt.Errorf(msg.InstanceNameRequired)
	}
	target, shouldRun, err := beginInstanceTaskRebuild(ts, instanceName)
	if err != nil {
		return err
	}
	if !shouldRun {
		return nil
	}

	for {
		shouldStop := false
		errs := make([]string, 0)
		ts.Mu.Lock()
		if ts.Stopping {
			shouldStop = true
		} else {
			errs = append(errs, deleteInstanceTaskJobsLocked(ts, instanceName)...)
		}
		ts.Mu.Unlock()
		if shouldStop {
			_, _, finishErr := finishInstanceTaskRebuild(ts, instanceName, target)
			if finishErr != nil {
				return finishErr
			}
			return nil
		}

		sp, ok := Get(instanceName)
		if ok {
			ins := sp.InstanceSnapshot()
			tasks := append([]cfg.Task(nil), ins.Tasks...)

			for i := range tasks {
				t := tasks[i]
				name := strings.TrimSpace(t.Name)
				if name == "" {
					continue
				}
				if !t.Enabled {
					continue
				}

				spec, _, err := cfg.BuildTaskSchedule(t.Expr)
				if err != nil {
					log.Printf(msg.ParseScheduledTaskFailedLogFmt, instanceName, name, err)
					errs = append(errs, fmt.Sprintf(msg.ParseScheduledTaskFailedFmt, name, err))
					_ = logbuf.EmitInstance(logbuf.LevelWarn, instanceName, fmt.Sprintf(msg.ParseScheduledTaskFailedFmt, name, err))
					limit := cfg.GetHistoryLimit() * 1024
					sp.Mu.Lock()
					terminalMsg := BuildWarningTerminalSystemMessage(fmt.Sprintf(msg.ParseScheduledTaskFailedFmt, name, err))
					sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
					sp.Mu.Unlock()
					continue
				}

				action := strings.TrimSpace(t.Action)
				cmd := strings.TrimSpace(t.Command)
				useKillStop := t.UseKillStop
				strictRestart := t.StrictRestart
				key := taskJobKey(instanceName, name)

				job, err := ts.Scheduler.NewJob(
					gocron.CronJob(spec, false),
					gocron.NewTask(func() {
						sp, ok := Get(instanceName)
						if !ok {
							return
						}
						executeTask(sp, name, action, cmd, useKillStop, strictRestart)
					}),
				)
				if err != nil {
					log.Printf(msg.RegisterScheduledTaskFailedLogFmt, instanceName, name, err)
					errs = append(errs, fmt.Sprintf(msg.RegisterScheduledTaskFailedFmt, name, err))
					_ = logbuf.EmitInstance(logbuf.LevelWarn, instanceName, fmt.Sprintf(msg.RegisterScheduledTaskFailedFmt, name, err))
					limit := cfg.GetHistoryLimit() * 1024
					sp.Mu.Lock()
					terminalMsg := BuildWarningTerminalSystemMessage(fmt.Sprintf(msg.RegisterScheduledTaskFailedFmt, name, err))
					sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
					sp.Mu.Unlock()
					continue
				}

				ts.Mu.Lock()
				if ts.Stopping {
					ts.Scheduler.RemoveJob(job.ID())
					ts.Mu.Unlock()
					shouldStop = true
					break
				}
				ts.Jobs[key] = job
				ts.Mu.Unlock()
			}
		}
		if shouldStop {
			_, _, finishErr := finishInstanceTaskRebuild(ts, instanceName, target)
			if finishErr != nil {
				return finishErr
			}
			return nil
		}

		nextTarget, again, finishErr := finishInstanceTaskRebuild(ts, instanceName, target)
		if finishErr != nil {
			return finishErr
		}
		if again {
			target = nextTarget
			continue
		}
		if len(errs) > 0 {
			return fmt.Errorf("%s", strings.Join(errs, "; "))
		}
		return nil
	}
}

func executeTask(sp *InstanceProcess, taskName string, action string, command string, useKillStop bool, strictRestart bool) {
	if sp == nil {
		return
	}
	instanceName := sp.InstanceSnapshot().Name

	limit := cfg.GetHistoryLimit() * 1024
	sp.Mu.Lock()
	if sp.Updating {
		terminalMsg := BuildWarningTerminalSystemMessage(fmt.Sprintf(msg.ScheduledTaskSkippedUpdatingFmt, taskName))
		sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
		sp.Mu.Unlock()
		return
	}
	terminalMsg := BuildNormalTerminalSystemMessage(fmt.Sprintf(msg.ScheduledTaskTriggeredFmt, taskName, action))
	sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
	sp.Mu.Unlock()

	switch action {
	case "start":
		if err := sp.Start(); err != nil {
			sp.Mu.Lock()
			terminalMsg := BuildWarningTerminalSystemMessage(fmt.Sprintf(msg.ScheduledTaskStartFailedFmt, taskName, err))
			sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
			sp.Mu.Unlock()
		}
	case "stop":
		sp.Stop(useKillStop)
	case "restart":
		if strictRestart {
			writeStrictRestartTaskResult(sp, taskName, sp.RequestStrictRestartWithKillStop(useKillStop), limit)
			return
		}
		writeRestartTaskResult(sp, taskName, sp.RequestRestartWithKillStopResult(useKillStop), limit)
	case "command":
		if err := sp.SendCommand(command); err != nil {
			sp.Mu.Lock()
			terminalMsg := BuildWarningTerminalSystemMessage(fmt.Sprintf(msg.ScheduledTaskSendCommandFailedFmt, taskName, err))
			sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
			sp.Mu.Unlock()
		}
	default:
		log.Printf(msg.UnknownScheduledTaskActionLogFmt, instanceName, taskName, action)
		sp.Mu.Lock()
		terminalMsg := BuildWarningTerminalSystemMessage(fmt.Sprintf(msg.ScheduledTaskActionInvalidFmt, taskName, action))
		sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
		sp.Mu.Unlock()
	}
}

func writeStrictRestartTaskResult(sp *InstanceProcess, taskName string, result RestartRequestResult, limit int) {
	if result.IsAccepted() {
		return
	}
	text := ""
	switch result {
	case RestartRequestNoopStarting:
		text = fmt.Sprintf(msg.ScheduledTaskStrictRestartSkippedStartingFmt, taskName)
	case RestartRequestNoopAlreadyRestarting:
		text = fmt.Sprintf(msg.ScheduledTaskStrictRestartSkippedRestartingFmt, taskName)
	case RestartRequestSkippedStopped:
		text = fmt.Sprintf(msg.ScheduledTaskStrictRestartSkippedStoppedFmt, taskName)
	case RestartRequestRejectedDeleting:
		text = fmt.Sprintf(msg.ScheduledTaskStrictRestartRejectedDeletingFmt, taskName)
	default:
		text = fmt.Sprintf(msg.ScheduledTaskActionInvalidFmt, taskName, "restart")
	}
	sp.Mu.Lock()
	terminalMsg := BuildWarningTerminalSystemMessage(text)
	sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
	sp.Mu.Unlock()
}

func writeRestartTaskResult(sp *InstanceProcess, taskName string, result RestartRequestResult, limit int) {
	if result.IsAllowed() {
		return
	}
	text := fmt.Sprintf(msg.ScheduledTaskRestartRejectedFmt, taskName)
	if result == RestartRequestRejectedDeleting {
		text = fmt.Sprintf(msg.ScheduledTaskRestartRejectedDeletingFmt, taskName)
	}
	sp.Mu.Lock()
	terminalMsg := BuildWarningTerminalSystemMessage(text)
	sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
	sp.Mu.Unlock()
}

func InitTaskScheduler()                             { initTaskScheduler() }
func StopTaskScheduler()                             { stopTaskScheduler() }
func DisconnectAllInstanceClients()                  { disconnectAllInstanceClients() }
func RebuildAllInstanceTasksLocked()                 { rebuildAllInstanceTasksLocked() }
func RebuildAllInstanceTasks() error                 { return rebuildAllInstanceTasks() }
func RebuildInstanceTasks(instanceName string) error { return rebuildInstanceTasks(instanceName) }