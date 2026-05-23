package api

import (
	web "IpacPanel/controller/src/web"
	"IpacPanel/controller/src/web/authz"

	cfg "IpacPanel/controller/src/config"

	"net/http"
)

type instanceGetRequest struct {
	Instance string `json:"instance"`
}

func HandleApiInstanceGet(w http.ResponseWriter, r *http.Request) {
	var req instanceGetRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	authedUser, ok := authz.DefaultRuntime.CurrentAuthUser(r)
	if !ok {
		web.WriteUnauthorized(w)
		return
	}
	sp, ok := web.RequireInstanceProcessByName(w, authedUser, req.Instance)
	if !ok {
		return
	}

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
