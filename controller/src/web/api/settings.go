package api

import (
	"IpacPanel/controller/src/metrics"
	"IpacPanel/controller/src/msg"
	"IpacPanel/controller/src/process"
	web "IpacPanel/controller/src/web"

	cfg "IpacPanel/controller/src/config"

	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
)

type settingsResponse struct {
	WebTitle                 string                  `json:"web_title"`
	Debug                    bool                    `json:"debug"`
	Listen                   string                  `json:"listen"`
	Web                      settingsWebResponse     `json:"web"`
	HistorySize              int                     `json:"history_size"`
	AutoStartInterval        int                     `json:"auto_start_interval"`
	AutoRestartInterval      int                     `json:"auto_restart_interval"`
	InstanceUpdateStagingDir string                  `json:"instance_update_staging_dir"`
	TrustedProxyIPs          []string                `json:"trusted_proxy_ips"`
	Metrics                  settingsMetricsResponse `json:"metrics"`
	Pow                      settingsPowResponse     `json:"pow"`
}

type settingsPublicResponse struct {
	WebTitle string                        `json:"web_title"`
	Metrics  settingsPublicMetricsResponse `json:"metrics"`
}

type settingsPublicMetricsResponse struct {
	Enabled         bool `json:"enabled"`
	PublicDashboard bool `json:"public_dashboard"`
}

type settingsUpdateRequest struct {
	WebTitle                 *string                       `json:"web_title"`
	Debug                    *bool                         `json:"debug"`
	Listen                   *string                       `json:"listen"`
	Web                      *settingsWebUpdateRequest     `json:"web"`
	HistorySize              *int                          `json:"history_size"`
	AutoStartInterval        *int                          `json:"auto_start_interval"`
	AutoRestartInterval      *int                          `json:"auto_restart_interval"`
	InstanceUpdateStagingDir *string                       `json:"instance_update_staging_dir"`
	TrustedProxyIPs          *[]string                     `json:"trusted_proxy_ips"`
	Metrics                  *settingsMetricsUpdateRequest `json:"metrics"`
	Pow                      *settingsPowUpdateRequest     `json:"pow"`
}

type settingsWebResponse struct {
	EnableHTTPS    bool   `json:"enable_https"`
	ForceHTTPS     bool   `json:"force_https"`
	PrivateKeyPath string `json:"private_key_path"`
	PublicKeyPath  string `json:"public_key_path"`
}

type settingsWebUpdateRequest struct {
	EnableHTTPS    *bool   `json:"enable_https"`
	ForceHTTPS     *bool   `json:"force_https"`
	PrivateKeyPath *string `json:"private_key_path"`
	PublicKeyPath  *string `json:"public_key_path"`
}

type settingsMetricsResponse struct {
	StorageMode           string `json:"storage_mode"`
	MemoryMaxMin          int    `json:"memory_max_min"`
	SQLiteMaxDay          int    `json:"sqlite_max_day"`
	SQLiteCompactAfterDay int    `json:"sqlite_compact_after_day"`
	Enabled               bool   `json:"enabled"`
	PublicDashboard       bool   `json:"public_dashboard"`
}

type settingsMetricsUpdateRequest struct {
	StorageMode           *string `json:"storage_mode"`
	MemoryMaxMin          *int    `json:"memory_max_min"`
	SQLiteMaxDay          *int    `json:"sqlite_max_day"`
	SQLiteCompactAfterDay *int    `json:"sqlite_compact_after_day"`
	Enabled               *bool   `json:"enabled"`
	PublicDashboard       *bool   `json:"public_dashboard"`
}

type settingsPowResponse struct {
	Enabled          bool `json:"enabled"`
	TaskCount        int  `json:"task_count"`
	Difficulty       int  `json:"difficulty"`
	TimestampMaxSkew int  `json:"timestamp_max_skew"`
}

type settingsPowUpdateRequest struct {
	Enabled          *bool `json:"enabled"`
	TaskCount        *int  `json:"task_count"`
	Difficulty       *int  `json:"difficulty"`
	TimestampMaxSkew *int  `json:"timestamp_max_skew"`
}

type settingsRestartControllerResponse struct {
	Restarting bool `json:"restarting"`
}

func HandleApiSettingsPublic(w http.ResponseWriter, r *http.Request) {
	_, ok := web.GuardRequest(w, r, web.GuardOptions{
		Methods: []string{http.MethodGet},
	})
	if !ok {
		return
	}
	web.WriteOK(w, buildSettingsPublicResponse())
}

func HandleApiSettingsGet(w http.ResponseWriter, r *http.Request) {
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth: true,
		Methods:     []string{http.MethodGet},
	})
	if !ok {
		return
	}
	if guard.User.Perm != 7 {
		web.WriteAPIError(w, http.StatusForbidden, msg.Forbidden, nil)
		return
	}

	web.WriteOK(w, buildSettingsResponse())
}

func HandleApiSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth:     true,
		Methods:         []string{http.MethodPost},
		CSRFFromRequest: true,
	})
	if !ok {
		return
	}
	if guard.User.Perm != 7 {
		web.WriteAPIError(w, http.StatusForbidden, msg.Forbidden, nil)
		return
	}

	var req settingsUpdateRequest
	if !web.DecodeJSONBody(w, r, &req) {
		return
	}
	cfg.ConfigTxnMu.Lock()
	defer cfg.ConfigTxnMu.Unlock()

	cfg.ManagerMu.Lock()
	savedCfg := cfg.CloneConfigLocked()
	cfg.ManagerMu.Unlock()

	if req.WebTitle != nil {
		savedCfg.WebTitle = cfg.NormalizeWebTitle(*req.WebTitle)
	}
	if req.Debug != nil {
		savedCfg.Debug = *req.Debug
	}
	if req.Listen != nil {
		savedCfg.Listen = cfg.NormalizeListenAddress(*req.Listen)
	}
	if req.Web != nil {
		if req.Web.EnableHTTPS != nil {
			savedCfg.Web.EnableHTTPS = *req.Web.EnableHTTPS
		}
		if req.Web.ForceHTTPS != nil {
			savedCfg.Web.ForceHTTPS = *req.Web.ForceHTTPS
		}
		if req.Web.PrivateKeyPath != nil {
			savedCfg.Web.PrivateKeyPath = *req.Web.PrivateKeyPath
		}
		if req.Web.PublicKeyPath != nil {
			savedCfg.Web.PublicKeyPath = *req.Web.PublicKeyPath
		}
		savedCfg.Web = cfg.NormalizeWebConfig(savedCfg.Web)
	}
	if req.HistorySize != nil {
		savedCfg.HistorySize = cfg.NormalizeHistorySize(*req.HistorySize)
	}
	if req.AutoStartInterval != nil {
		savedCfg.AutoStartInterval = cfg.NormalizeAutoStartInterval(*req.AutoStartInterval)
	}
	if req.AutoRestartInterval != nil {
		savedCfg.AutoRestartInterval = cfg.NormalizeAutoRestartInterval(*req.AutoRestartInterval)
	}
	if req.InstanceUpdateStagingDir != nil {
		savedCfg.InstanceUpdateStagingDir = cfg.NormalizeSettingsInstanceUpdateStagingDir(*req.InstanceUpdateStagingDir)
	}
	if req.TrustedProxyIPs != nil {
		savedCfg.TrustedProxyIPs = cfg.NormalizeTrustedProxyIPs(*req.TrustedProxyIPs)
	}
	if req.Metrics != nil {
		if req.Metrics.Enabled != nil {
			savedCfg.Metrics.Enabled = *req.Metrics.Enabled
		}
		if req.Metrics.PublicDashboard != nil {
			savedCfg.Metrics.PublicDashboard = *req.Metrics.PublicDashboard
		}
		if req.Metrics.MemoryMaxMin != nil {
			savedCfg.Metrics.MemoryMaxMin = *req.Metrics.MemoryMaxMin
		}
		if req.Metrics.StorageMode != nil {
			savedCfg.Metrics.StorageMode = strings.ToLower(strings.TrimSpace(*req.Metrics.StorageMode))
		}
		if req.Metrics.SQLiteMaxDay != nil {
			savedCfg.Metrics.SQLiteMaxDay = *req.Metrics.SQLiteMaxDay
		}
		if req.Metrics.SQLiteCompactAfterDay != nil {
			savedCfg.Metrics.SQLiteCompactAfterDay = *req.Metrics.SQLiteCompactAfterDay
		}
	}
	savedCfg.Metrics = cfg.NormalizeMetricsConfig(savedCfg.Metrics)
	if err := cfg.ValidateMetricsConfig(savedCfg.Metrics); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, err.Error(), err)
		return
	}
	if err := cfg.ValidateSettingsTextFields(savedCfg.WebTitle, savedCfg.Listen, savedCfg.Web, savedCfg.InstanceUpdateStagingDir, savedCfg.TrustedProxyIPs); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if req.Pow != nil {
		if req.Pow.Enabled != nil {
			savedCfg.Pow.Enabled = *req.Pow.Enabled
		}
		if req.Pow.TaskCount != nil {
			savedCfg.Pow.TaskCount = *req.Pow.TaskCount
		}
		if req.Pow.Difficulty != nil {
			savedCfg.Pow.Difficulty = *req.Pow.Difficulty
		}
		if req.Pow.TimestampMaxSkew != nil {
			savedCfg.Pow.TimestampMaxSkew = *req.Pow.TimestampMaxSkew
		}
	}
	savedCfg.Pow = cfg.NormalizePowConfig(savedCfg.Pow)
	if err := cfg.ValidatePowConfig(savedCfg.Pow); err != nil {
		web.WriteAPIError(w, http.StatusBadRequest, err.Error(), err)
		return
	}
	debug := savedCfg.Debug
	metricsConfig := savedCfg.Metrics

	plan := cfg.MutationPlan{NextCfg: savedCfg}
	plan.AddRequiredPostCommit("sync debug mode", func() error {
		return process.SetDaemonDebug(debug)
	})
	plan.AddRequiredPostCommit("sync dashboard metrics", func() error {
		if dashboardCollector == nil {
			return nil
		}
		dashboardCollector.ApplyConfig(newDashboardMetricsConfig(metricsConfig))
		return nil
	})
	plan.Publish = func() {
		cfg.ManagerMu.Lock()
		cfg.CurrentConfig = savedCfg
		cfg.ManagerMu.Unlock()
	}
	if err := cfg.CommitMutationPlan(plan); err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, msg.SaveConfigFailed, err)
		return
	}
	result := cfg.RunMutationPostCommit(plan)
	if err := result.Error(); err != nil {
		writeMutationRuntimeSyncError(w, http.StatusInternalServerError, "settings saved, but runtime sync failed", result)
		return
	}

	web.WriteOK(w, buildSettingsResponseFromConfig(savedCfg))
}

func HandleApiSettingsRestartController(w http.ResponseWriter, r *http.Request) {
	guard, ok := web.GuardRequest(w, r, web.GuardOptions{
		RequireAuth:     true,
		Methods:         []string{http.MethodPost},
		CSRFFromRequest: true,
	})
	if !ok {
		return
	}
	if guard.User.Perm != 7 {
		web.WriteAPIError(w, http.StatusForbidden, msg.Forbidden, nil)
		return
	}
	if r.Body != nil {
		defer r.Body.Close()
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			web.WriteAPIError(w, http.StatusBadRequest, msg.InvalidRequestBody, err)
			return
		}
	}
	if err := process.RestartController(); err != nil {
		web.WriteAPIError(w, http.StatusInternalServerError, "restart controller failed", err)
		return
	}
	web.WriteOK(w, settingsRestartControllerResponse{Restarting: true})
	requestControllerShutdown()
}

func buildSettingsResponse() settingsResponse {
	cfg.ManagerMu.RLock()
	savedCfg := cfg.CloneConfigLocked()
	cfg.ManagerMu.RUnlock()
	return buildSettingsResponseFromConfig(savedCfg)
}

func buildSettingsPublicResponse() settingsPublicResponse {
	cfg.ManagerMu.RLock()
	savedCfg := cfg.CloneConfigLocked()
	cfg.ManagerMu.RUnlock()
	metricsConfig := savedCfg.Metrics
	return settingsPublicResponse{
		WebTitle: cfg.NormalizeWebTitle(savedCfg.WebTitle),
		Metrics: settingsPublicMetricsResponse{
			Enabled:         metricsConfig.Enabled,
			PublicDashboard: metricsConfig.PublicDashboard,
		},
	}
}

func buildSettingsResponseFromConfig(savedCfg cfg.Config) settingsResponse {
	pow := cfg.NormalizePowConfig(savedCfg.Pow)
	metricsConfig := cfg.NormalizeMetricsConfig(savedCfg.Metrics)
	return settingsResponse{
		WebTitle:                 cfg.NormalizeWebTitle(savedCfg.WebTitle),
		Debug:                    savedCfg.Debug,
		Listen:                   cfg.NormalizeListenAddress(savedCfg.Listen),
		Web:                      newSettingsWebResponse(savedCfg.Web),
		HistorySize:              cfg.NormalizeHistorySize(savedCfg.HistorySize),
		AutoStartInterval:        cfg.NormalizeAutoStartInterval(savedCfg.AutoStartInterval),
		AutoRestartInterval:      cfg.NormalizeAutoRestartInterval(savedCfg.AutoRestartInterval),
		InstanceUpdateStagingDir: cfg.NormalizeSettingsInstanceUpdateStagingDir(savedCfg.InstanceUpdateStagingDir),
		TrustedProxyIPs:          cfg.NormalizeTrustedProxyIPs(savedCfg.TrustedProxyIPs),
		Metrics:                  newSettingsMetricsResponse(metricsConfig),
		Pow: settingsPowResponse{
			Enabled:          pow.Enabled,
			TaskCount:        pow.TaskCount,
			Difficulty:       pow.Difficulty,
			TimestampMaxSkew: pow.TimestampMaxSkew,
		},
	}
}

func newSettingsWebResponse(webConfig cfg.WebConfig) settingsWebResponse {
	webConfig = cfg.NormalizeWebConfig(webConfig)
	return settingsWebResponse{
		EnableHTTPS:    webConfig.EnableHTTPS,
		ForceHTTPS:     webConfig.ForceHTTPS,
		PrivateKeyPath: webConfig.PrivateKeyPath,
		PublicKeyPath:  webConfig.PublicKeyPath,
	}
}

func newDashboardMetricsConfig(metricsConfig cfg.MetricsConfig) metrics.Config {
	return metrics.Config{
		Enabled:               metricsConfig.Enabled,
		MemoryMaxMin:          metricsConfig.MemoryMaxMin,
		StorageMode:           metricsConfig.StorageMode,
		SQLiteMaxDay:          metricsConfig.SQLiteMaxDay,
		SQLiteCompactAfterDay: metricsConfig.SQLiteCompactAfterDay,
		SQLitePath:            cfg.ResolveDataPath("dashboard") + string(os.PathSeparator),
	}
}

func newSettingsMetricsResponse(metricsConfig cfg.MetricsConfig) settingsMetricsResponse {
	return settingsMetricsResponse{
		StorageMode:           metricsConfig.StorageMode,
		MemoryMaxMin:          metricsConfig.MemoryMaxMin,
		SQLiteMaxDay:          metricsConfig.SQLiteMaxDay,
		SQLiteCompactAfterDay: metricsConfig.SQLiteCompactAfterDay,
		Enabled:               metricsConfig.Enabled,
		PublicDashboard:       metricsConfig.PublicDashboard,
	}
}
