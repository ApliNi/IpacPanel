package process

import (
	cfg "IpacPanel/controller/src/config"
	"IpacPanel/controller/src/msg"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/reugn/go-quartz/quartz"
)

type instanceTaskScheduler struct {
	Mu         sync.Mutex
	Cond       *sync.Cond
	Scheduler  quartz.Scheduler
	Jobs       map[string]*quartz.JobKey
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
	scheduler, err := quartz.NewStdScheduler()
	if err != nil {
		cancel()
		log.Printf(msg.InitSchedulerFailedLogFmt, err)
		return
	}
	scheduler.Start(ctx)

	taskScheduler = &instanceTaskScheduler{
		Scheduler:  scheduler,
		Jobs:       make(map[string]*quartz.JobKey),
		Rebuilding: make(map[string]bool),
		Requested:  make(map[string]uint64),
		Completed:  make(map[string]uint64),
		Ctx:        ctx,
		Cancel:     cancel,
	}
	taskScheduler.Cond = sync.NewCond(&taskScheduler.Mu)
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
	for key, jobKey := range ts.Jobs {
		if err := ts.Scheduler.DeleteJob(jobKey); err != nil {
			log.Printf(msg.DeleteScheduledTaskFailedLogFmt, key, err)
		}
		delete(ts.Jobs, key)
	}
	if ts.Cond != nil {
		ts.Cond.Broadcast()
	}
	ts.Mu.Unlock()

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
	for key, jobKey := range ts.Jobs {
		if strings.HasPrefix(key, instanceName+"::") {
			if err := ts.Scheduler.DeleteJob(jobKey); err != nil {
				log.Printf(msg.DeleteScheduledTaskFailedLogFmt, key, err)
				errs = append(errs, fmt.Sprintf(msg.DeleteScheduledTaskFailedFmt, key, err))
			}
			delete(ts.Jobs, key)
		}
	}
	return errs
}

func taskJobKey(instanceName string, taskName string) string {
	return instanceName + "::" + taskName
}

type instanceTaskJob struct {
	InstanceName  string
	TaskName      string
	Action        string
	Command       string
	UseKillStop   bool
	StrictRestart bool
}

func (j *instanceTaskJob) Execute(_ context.Context) error {
	sp, ok := Get(j.InstanceName)
	if !ok {
		return nil
	}
	executeTask(sp, j.TaskName, j.Action, j.Command, j.UseKillStop, j.StrictRestart)
	return nil
}

func (j *instanceTaskJob) Description() string {
	return fmt.Sprintf("task %s/%s", j.InstanceName, j.TaskName)
}

func rebuildAllInstanceTasksLocked() {
	if getTaskScheduler() == nil {
		return
	}

	for _, sp := range List() {
		if sp == nil {
			continue
		}
		instanceName := sp.InstanceSnapshot().Name
		if err := rebuildInstanceTasks(instanceName); err != nil {
			log.Printf(msg.RebuildInstanceScheduledTasksFailedLogFmt, instanceName, err)
		}
	}
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
				trigger, normalizedExpr, err := cfg.NewTaskTrigger(t.Expr)
				if err != nil {
					log.Printf(msg.ParseScheduledTaskFailedLogFmt, instanceName, name, err)
					errs = append(errs, fmt.Sprintf(msg.ParseScheduledTaskFailedFmt, name, err))
					limit := cfg.GetHistoryLimit() * 1024
					sp.Mu.Lock()
					msg := buildTerminalMessage("\x1b[31m", fmt.Sprintf(msg.ParseScheduledTaskFailedFmt, name, err))
					sp.appendAndBroadcastLocked(websocket.BinaryMessage, msg, limit)
					sp.Mu.Unlock()
					continue
				}
				expr := normalizedExpr
				if expr == "" {
					continue
				}
				action := strings.TrimSpace(t.Action)
				cmd := strings.TrimSpace(t.Command)

				key := taskJobKey(instanceName, name)
				jobKey := quartz.NewJobKeyWithGroup(name, instanceName)
				job := &instanceTaskJob{InstanceName: instanceName, TaskName: name, Action: action, Command: cmd, UseKillStop: t.UseKillStop, StrictRestart: t.StrictRestart}
				jobDetail := quartz.NewJobDetail(job, jobKey)

				ts.Mu.Lock()
				if ts.Stopping {
					ts.Mu.Unlock()
					shouldStop = true
					break
				}
				err = ts.Scheduler.ScheduleJob(jobDetail, trigger)
				if err != nil {
					ts.Mu.Unlock()
					log.Printf(msg.RegisterScheduledTaskFailedLogFmt, instanceName, name, err)
					errs = append(errs, fmt.Sprintf(msg.RegisterScheduledTaskFailedFmt, name, err))
					limit := cfg.GetHistoryLimit() * 1024
					sp.Mu.Lock()
					msg := buildTerminalMessage("\x1b[31m", fmt.Sprintf(msg.RegisterScheduledTaskFailedFmt, name, err))
					sp.appendAndBroadcastLocked(websocket.BinaryMessage, msg, limit)
					sp.Mu.Unlock()
					continue
				}
				ts.Jobs[key] = jobKey
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
		terminalMsg := buildTerminalMessage("\x1b[33m", fmt.Sprintf(msg.ScheduledTaskSkippedUpdatingFmt, taskName))
		sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
		sp.Mu.Unlock()
		return
	}
	terminalMsg := buildTerminalMessage("\x1b[34m", fmt.Sprintf(msg.ScheduledTaskTriggeredFmt, taskName, action))
	sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
	sp.Mu.Unlock()

	switch action {
	case "start":
		if err := sp.Start(); err != nil {
			sp.Mu.Lock()
			terminalMsg := buildTerminalMessage("\x1b[31m", fmt.Sprintf(msg.ScheduledTaskStartFailedFmt, taskName, err))
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
			terminalMsg := buildTerminalMessage("\x1b[31m", fmt.Sprintf(msg.ScheduledTaskSendCommandFailedFmt, taskName, err))
			sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
			sp.Mu.Unlock()
		}
	default:
		log.Printf(msg.UnknownScheduledTaskActionLogFmt, instanceName, taskName, action)
		sp.Mu.Lock()
		terminalMsg := buildTerminalMessage("\x1b[31m", fmt.Sprintf(msg.ScheduledTaskActionInvalidFmt, taskName, action))
		sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
		sp.Mu.Unlock()
	}
}

func writeStrictRestartTaskResult(sp *InstanceProcess, taskName string, result RestartRequestResult, limit int) {
	if result.IsAccepted() {
		return
	}
	colorCode := "\x1b[33m"
	text := ""
	switch result {
	case RestartRequestNoopStarting:
		text = fmt.Sprintf(msg.ScheduledTaskStrictRestartSkippedStartingFmt, taskName)
	case RestartRequestNoopAlreadyRestarting:
		text = fmt.Sprintf(msg.ScheduledTaskStrictRestartSkippedRestartingFmt, taskName)
	case RestartRequestSkippedStopped:
		text = fmt.Sprintf(msg.ScheduledTaskStrictRestartSkippedStoppedFmt, taskName)
	case RestartRequestRejectedDeleting:
		colorCode = "\x1b[31m"
		text = fmt.Sprintf(msg.ScheduledTaskStrictRestartRejectedDeletingFmt, taskName)
	default:
		colorCode = "\x1b[31m"
		text = fmt.Sprintf(msg.ScheduledTaskActionInvalidFmt, taskName, "restart")
	}
	sp.Mu.Lock()
	terminalMsg := buildTerminalMessage(colorCode, text)
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
	terminalMsg := buildTerminalMessage("\x1b[31m", text)
	sp.appendAndBroadcastLocked(websocket.BinaryMessage, terminalMsg, limit)
	sp.Mu.Unlock()
}

func InitTaskScheduler()                             { initTaskScheduler() }
func StopTaskScheduler()                             { stopTaskScheduler() }
func DisconnectAllInstanceClients()                  { disconnectAllInstanceClients() }
func RebuildAllInstanceTasksLocked()                 { rebuildAllInstanceTasksLocked() }
func RebuildInstanceTasks(instanceName string) error { return rebuildInstanceTasks(instanceName) }
