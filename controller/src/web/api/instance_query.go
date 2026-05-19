package api

import (
	web "IpacPanel/controller/src/web"

	cfg "IpacPanel/controller/src/config"

	"net/http"
)

func HandleApiInstanceGet(w http.ResponseWriter, r *http.Request) {
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth:       true,
		Methods:           []string{http.MethodGet},
		InstanceFromQuery: true,
	})
	if !ok {
		return
	}
	sp := guard.Instance

	status := sp.StatusSnapshot()
	ins := sp.InstanceSnapshot()
	web.WriteOK(w, instanceDetailResponse{
		instanceListItem: instanceListItem{
			Name:           status.Name,
			Group:          status.Group,
			Running:        status.Running,
			Updating:       status.Updating,
			Restarting:     status.Restarting,
			StartTime:      status.StartTime,
			RestartCount:   status.RestartCount,
			Terminal:       cfg.NormalizeTerminalMode(ins.Terminal),
			ActiveTerminal: status.ActiveTerminal,
		},
		Path:                     ins.Path,
		Command:                  ins.Command,
		AccessLinks:              ins.AccessLinks,
		Terminal:                 cfg.NormalizeTerminalMode(ins.Terminal),
		ActiveTerminal:           status.ActiveTerminal,
		InputEncoding:            ins.InputEncoding,
		OutputEncoding:           ins.OutputEncoding,
		StopCommand:              ins.StopCommand,
		CleanupCommand:           ins.CleanupCommand,
		AutoStart:                ins.AutoStart,
		StartPriority:            ins.StartPriority,
		AutoRestart:              ins.AutoRestart,
		RestartInterval:          ins.RestartInterval,
		Tasks:                    append([]cfg.Task(nil), ins.Tasks...),
		HistorySize:              cfg.GetHistoryLimit(),
		InstanceUpdateStagingDir: cfg.GetInstanceUpdateStagingDir(),
	})
}
