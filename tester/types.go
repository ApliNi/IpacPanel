package main

import (
	"os/exec"
	"time"
)

const (
	testerReportName  = "tester.md"
	testerWorkDirName = "tester-work"
	defaultTimeout    = 20 * time.Second
	terminalModePipe  = 2
)

var runStartedAt = time.Now()

type event struct {
	Time      time.Time         `json:"time"`
	ElapsedMS int64             `json:"elapsed_ms"`
	Source    string            `json:"source"`
	HelperID  string            `json:"helper_id,omitempty"`
	PID       int               `json:"pid,omitempty"`
	Event     string            `json:"event"`
	Data      map[string]string `json:"data,omitempty"`
}

type testResult struct {
	Suite    string
	Name     string
	Passed   bool
	Detail   string
	Duration time.Duration
	Evidence []string
}

type testEnv struct {
	RootDir         string
	DistributionDir string
	RunDir          string
	TesterExe       string
	Reporter        *reporter
}

type suiteContext struct {
	Env       *testEnv
	Name      string
	Dir       string
	AppDir    string
	DataDir   string
	EventDir  string
	HelperExe string
	Store     *eventStore
	Panel     *panelProcess
}

type panelProcess struct {
	cmd *exec.Cmd
}

type panelConfig struct {
	WebTitle                 string    `yaml:"web_title"`
	Debug                    bool      `yaml:"debug"`
	Listen                   string    `yaml:"listen"`
	HistorySize              int       `yaml:"history_size"`
	AutoStartInterval        int       `yaml:"auto_start_interval"`
	AutoRestartInterval      int       `yaml:"auto_restart_interval"`
	InstanceUpdateStagingDir string    `yaml:"instance_update_staging_dir"`
	TrustedProxyIPs          []string  `yaml:"trusted_proxy_ips"`
	Pow                      powConfig `yaml:"pow"`
}

type powConfig struct {
	Enabled          bool `yaml:"enabled"`
	TaskCount        int  `yaml:"task_count"`
	Difficulty       int  `yaml:"difficulty"`
	TimestampMaxSkew int  `yaml:"timestamp_max_skew"`
}

type instanceConfig struct {
	Name            string       `yaml:"name"`
	Group           string       `yaml:"group,omitempty"`
	Path            string       `yaml:"path"`
	Command         string       `yaml:"command"`
	Terminal        int          `yaml:"terminal,omitempty"`
	StopCommand     string       `yaml:"stop_command,omitempty"`
	AutoStart       bool         `yaml:"auto_start"`
	StartPriority   *int         `yaml:"start_priority,omitempty"`
	AutoRestart     bool         `yaml:"auto_restart"`
	RestartInterval *int         `yaml:"restart_interval,omitempty"`
	Tasks           []taskConfig `yaml:"tasks,omitempty"`
}

type taskConfig struct {
	Name          string `yaml:"name"`
	Enabled       bool   `yaml:"enabled"`
	Expr          string `yaml:"expr"`
	Action        string `yaml:"action"`
	Command       string `yaml:"command,omitempty"`
	StrictRestart bool   `yaml:"strict_restart,omitempty"`
}

type authUser struct {
	User           string   `yaml:"user"`
	Pass           string   `yaml:"pass"`
	Perm           int      `yaml:"perm"`
	AllowInstances []string `yaml:"allow_instances,omitempty"`
	AllowGroups    []string `yaml:"allow_groups,omitempty"`
}

type eventFilter func(event) bool
