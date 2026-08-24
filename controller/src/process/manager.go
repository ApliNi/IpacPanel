package process

import (
	cfg "IpacPanel/controller/src/config"
	"sort"
)

var (
	InstanceProcesses         = make(map[string]*InstanceProcess)
	InstanceProcessAliases    = make(map[string]*InstanceProcess)
	resetUploadSessionsHook   func()
	instanceStatusChangedHook func(string)
)

func NewInstanceProcess(ins *cfg.Instance) *InstanceProcess {
	instance := cfg.Instance{}
	if ins != nil {
		instance = cfg.CloneInstances([]cfg.Instance{*ins})[0]
	}
	return &InstanceProcess{
		Ins:          instance,
		Cols:         cfg.DefaultTerminalCols,
		Rows:         cfg.DefaultTerminalRows,
		RestartCount: -1,
		Clients:      make(map[*WSClient]bool),
	}
}

func SetResetUploadSessionsHook(fn func()) {
	resetUploadSessionsHook = fn
}

func SetInstanceStatusChangedHook(fn func(string)) {
	instanceStatusChangedHook = fn
}

func NotifyInstanceStatusChanged(instanceName string) {
	if instanceStatusChangedHook != nil {
		instanceStatusChangedHook(instanceName)
	}
}

// InitializeInstanceRegistry rebuilds the in-memory process registry during cold startup only.
// It resets derived runtime registries and must not be used as a runtime hot-reload mechanism.
func InitializeInstanceRegistry(instances []cfg.Instance) {
	cfg.ManagerMu.Lock()

	InstanceProcesses = make(map[string]*InstanceProcess)
	InstanceProcessAliases = make(map[string]*InstanceProcess)
	if resetUploadSessionsHook != nil {
		resetUploadSessionsHook()
	}

	for i := range instances {
		ins := &instances[i]
		if err := cfg.ValidateInstanceName(ins.Name); err != nil {
			continue
		}
		if err := cfg.ValidateGroupName(ins.Group); err != nil {
			continue
		}
		if _, ok := InstanceProcesses[ins.Name]; ok {
			continue
		}
		sp := NewInstanceProcess(ins)
		InstanceProcesses[ins.Name] = sp
		InstanceProcessAliases[ins.Name] = sp
	}
	cfg.ManagerMu.Unlock()
	NotifyInstanceStatusChanged("")
}

func RestoreDaemonRuntimeStates(states []DaemonRuntimeState) {
	runtimeByName := make(map[string]DaemonRuntimeState, len(states))
	for _, state := range states {
		if state.InstanceName == "" {
			continue
		}
		runtimeByName[state.InstanceName] = state
	}

	cfg.ManagerMu.RLock()
	processes := make(map[string]*InstanceProcess, len(InstanceProcesses))
	for name, sp := range InstanceProcesses {
		processes[name] = sp
	}
	cfg.ManagerMu.RUnlock()

	for name, sp := range processes {
		if sp == nil {
			continue
		}
		state, ok := runtimeByName[name]
		if !ok {
			continue
		}
		sp.Mu.Lock()
		sp.cancelStartLocked()
		sp.cancelStopLocked()
		sp.cancelRestartLocked()
		sp.StartTime = state.StartTime
		sp.RestartCount = state.RestartCount
		sp.ActiveTerminalMode = cfg.NormalizeTerminalMode(state.Terminal)
		sp.resetPTYAlternateScreenStateLocked()
		runtimeAlias := state.RuntimeAlias
		if runtimeAlias == "" {
			runtimeAlias = state.InstanceName
		}
		RegisterInstanceProcessAliasLocked(runtimeAlias, sp)
		switch state.Lifecycle {
		case DaemonLifecycleRunning:
			sp.enterRunningStateLocked()
		case DaemonLifecycleStopping, DaemonLifecycleCleaning:
			sp.setStateLocked(processStateStopping)
		default:
			sp.enterStoppedStateLocked()
		}
		sp.Mu.Unlock()
		NotifyInstanceStatusChanged(name)
	}
}

func RestoreDaemonAutoRestarts(states []DaemonRuntimeState) {
	for _, state := range states {
		if state.RuntimeCode != RuntimeCodeUnexpectedExit || state.InstanceName == "" {
			continue
		}
		sp, ok := Get(state.InstanceName)
		if !ok || sp == nil {
			continue
		}
		ins := sp.InstanceSnapshot()
		if !ins.AutoRestart {
			continue
		}
		sp.ScheduleRecoveredAutoRestart(state)
	}
}

func Get(name string) (*InstanceProcess, bool) {
	cfg.ManagerMu.RLock()
	defer cfg.ManagerMu.RUnlock()
	ip, ok := InstanceProcesses[name]
	return ip, ok
}

func GetByRuntimeAlias(name string) (*InstanceProcess, bool) {
	cfg.ManagerMu.RLock()
	defer cfg.ManagerMu.RUnlock()
	ip, ok := InstanceProcessAliases[name]
	return ip, ok
}

func RegisterInstanceProcessAliasLocked(name string, sp *InstanceProcess) {
	if name == "" || sp == nil {
		return
	}
	InstanceProcessAliases[name] = sp
}

func UnregisterInstanceProcessAliasesLocked(sp *InstanceProcess) {
	if sp == nil {
		return
	}
	for name, candidate := range InstanceProcessAliases {
		if candidate == sp {
			delete(InstanceProcessAliases, name)
		}
	}
}

func RenameInstanceProcessAndKeepRuntimeAliasLocked(oldName string, newName string, sp *InstanceProcess) {
	if oldName != "" {
		delete(InstanceProcesses, oldName)
	}
	if newName != "" && sp != nil {
		InstanceProcesses[newName] = sp
		InstanceProcessAliases[newName] = sp
	}
	if oldName != "" && sp != nil {
		InstanceProcessAliases[oldName] = sp
	}
}

func SyncInstancePointers() {
	cfg.ManagerMu.RLock()
	instances := cfg.CloneInstances(cfg.CurrentConfig.Instances)
	processes := make(map[string]*InstanceProcess, len(InstanceProcesses))
	for name, sp := range InstanceProcesses {
		processes[name] = sp
	}
	cfg.ManagerMu.RUnlock()
	for i := range instances {
		sp, ok := processes[instances[i].Name]
		if !ok || sp == nil {
			continue
		}
		sp.Mu.Lock()
		previousTerminal := cfg.NormalizeTerminalMode(sp.Ins.Terminal)
		sp.Ins = instances[i]
		if previousTerminal != cfg.NormalizeTerminalMode(sp.Ins.Terminal) {
			sp.resetPTYAlternateScreenStateLocked()
		}
		sp.Mu.Unlock()
	}
}

func GetAutoStartProcesses() []*InstanceProcess {
	processes := List()
	type autoStartProcessSortItem struct {
		process  *InstanceProcess
		name     string
		priority int
	}
	items := make([]autoStartProcessSortItem, 0, len(processes))
	for _, sp := range processes {
		if sp == nil {
			continue
		}
		ins := sp.InstanceSnapshot()
		if !ins.AutoStart {
			continue
		}
		items = append(items, autoStartProcessSortItem{
			process:  sp,
			name:     ins.Name,
			priority: autoStartPriorityValue(ins.StartPriority),
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].priority != items[j].priority {
			return items[i].priority > items[j].priority
		}
		return items[i].name < items[j].name
	})
	autoStartProcesses := make([]*InstanceProcess, 0, len(items))
	for _, item := range items {
		autoStartProcesses = append(autoStartProcesses, item.process)
	}
	return autoStartProcesses
}

func autoStartPriorityValue(priority *int) int {
	if priority == nil {
		return 0
	}
	return *priority
}

func List() []*InstanceProcess {
	cfg.ManagerMu.RLock()
	processes := make([]*InstanceProcess, 0, len(InstanceProcesses))
	for _, sp := range InstanceProcesses {
		if sp == nil {
			continue
		}
		processes = append(processes, sp)
	}
	cfg.ManagerMu.RUnlock()
	return processes
}

func shutdownAllInstances() {
	for _, sp := range List() {
		if sp == nil {
			continue
		}
		sp.forceShutdown()
	}
}
