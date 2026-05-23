package config

import (
	"IpacPanel/controller/src/msg"
	"fmt"

	"gopkg.in/yaml.v3"
)

const (
	NoTerminal  = 1
	Terminal    = 2
	PTYTerminal = 3
)

func NormalizeTerminalMode(mode int) int {
	switch mode {
	case NoTerminal, Terminal, PTYTerminal:
		return mode
	default:
		return Terminal
	}
}

func ValidateTerminalMode(mode int) error {
	switch mode {
	case NoTerminal, Terminal, PTYTerminal:
		return nil
	default:
		return fmt.Errorf(msg.InvalidTerminalModeFmt, mode)
	}
}

func IsNoTerminal(mode int) bool {
	return NormalizeTerminalMode(mode) == NoTerminal
}

func IsTerminal(mode int) bool {
	return NormalizeTerminalMode(mode) == Terminal
}

func IsPTYTerminal(mode int) bool {
	return NormalizeTerminalMode(mode) == PTYTerminal
}

type Instance struct {
	Name            string `yaml:"name" json:"name"`
	Group           string `yaml:"group,omitempty" json:"group,omitempty"`
	Path            string `yaml:"path" json:"path"`
	Command         string `yaml:"command" json:"command"`
	AccessLinks     string `yaml:"access_links,omitempty" json:"access_links,omitempty"`
	Terminal        int    `yaml:"terminal,omitempty" json:"terminal,omitempty"`
	InputEncoding   string `yaml:"input_encoding,omitempty" json:"input_encoding,omitempty"`
	OutputEncoding  string `yaml:"output_encoding,omitempty" json:"output_encoding,omitempty"`
	StopCommand     string `yaml:"stop_command,omitempty" json:"stop_command,omitempty"`
	CleanupCommand  string `yaml:"cleanup_command,omitempty" json:"cleanup_command,omitempty"`
	AutoStart       bool   `yaml:"auto_start" json:"auto_start"`
	StartPriority   *int   `yaml:"start_priority,omitempty" json:"start_priority,omitempty"`
	AutoRestart     bool   `yaml:"auto_restart" json:"auto_restart"`
	RestartInterval *int   `yaml:"restart_interval,omitempty" json:"restart_interval,omitempty"`
	Tasks           []Task `yaml:"tasks,omitempty" json:"tasks,omitempty"`
}

type Task struct {
	Name          string `yaml:"name" json:"name"`
	Enabled       bool   `yaml:"enabled" json:"enabled"`
	Expr          string `yaml:"expr" json:"expr"`
	Action        string `yaml:"action" json:"action"`
	Command       string `yaml:"command,omitempty" json:"command,omitempty"`
	UseKillStop   bool   `yaml:"use_kill_stop,omitempty" json:"use_kill_stop,omitempty"`
	StrictRestart bool   `yaml:"strict_restart,omitempty" json:"strict_restart,omitempty"`
}

type Config struct {
	DataVersion              int    `yaml:"data_version" json:"data_version"`
	WebTitle                 string `yaml:"web_title" json:"web_title"`
	Listen                   string `yaml:"listen" json:"listen"`
	HistorySize              int    `yaml:"history_size"`
	AutoStartInterval        int    `yaml:"auto_start_interval"`
	AutoRestartInterval      int    `yaml:"auto_restart_interval"`
	InstanceUpdateStagingDir string `yaml:"instance_update_staging_dir"`
	// TrustedProxyIPs must contain the direct proxy IPs that overwrite Forwarded/X-Forwarded-* headers.
	// Keep this empty unless the reverse proxy is under your control and rewrites those headers.
	TrustedProxyIPs []string      `yaml:"trusted_proxy_ips"`
	Metrics         MetricsConfig `yaml:"metrics" json:"metrics"`
	Web             WebConfig     `yaml:"web" json:"web"`
	Pow             PowConfig     `yaml:"pow"`
	Debug           bool          `yaml:"debug" json:"debug"`
	Auth            []AuthUser    `yaml:"-"`
	Instances       []Instance    `yaml:"-"`
}

func (c *Config) UnmarshalYAML(value *yaml.Node) error {
	type rawConfig struct {
		DataVersion              *int           `yaml:"data_version"`
		WebTitle                 *string        `yaml:"web_title"`
		Listen                   *string        `yaml:"listen"`
		HistorySize              *int           `yaml:"history_size"`
		AutoStartInterval        *int           `yaml:"auto_start_interval"`
		AutoRestartInterval      *int           `yaml:"auto_restart_interval"`
		InstanceUpdateStagingDir *string        `yaml:"instance_update_staging_dir"`
		TrustedProxyIPs          *[]string      `yaml:"trusted_proxy_ips"`
		Metrics                  *MetricsConfig `yaml:"metrics"`
		Web                      *WebConfig     `yaml:"web"`
		Pow                      *PowConfig     `yaml:"pow"`
		Debug                    *bool          `yaml:"debug"`
		Auth                     *[]AuthUser    `yaml:"auth"`
		Instances                *[]Instance    `yaml:"instances"`
	}
	var decoded rawConfig
	if err := value.Decode(&decoded); err != nil {
		return err
	}

	result := NewDefaultConfig()
	if decoded.DataVersion != nil {
		result.DataVersion = *decoded.DataVersion
	}
	if decoded.WebTitle != nil {
		result.WebTitle = NormalizeWebTitle(*decoded.WebTitle)
	}
	if decoded.Listen != nil {
		result.Listen = NormalizeListenAddress(*decoded.Listen)
	}
	if decoded.HistorySize != nil {
		result.HistorySize = *decoded.HistorySize
	}
	if decoded.AutoStartInterval != nil {
		result.AutoStartInterval = *decoded.AutoStartInterval
	}
	if decoded.AutoRestartInterval != nil {
		result.AutoRestartInterval = *decoded.AutoRestartInterval
	}
	if decoded.InstanceUpdateStagingDir != nil {
		result.InstanceUpdateStagingDir = *decoded.InstanceUpdateStagingDir
	}
	if decoded.TrustedProxyIPs != nil {
		result.TrustedProxyIPs = append([]string{}, (*decoded.TrustedProxyIPs)...)
	}
	if decoded.Metrics != nil {
		result.Metrics = *decoded.Metrics
	}
	if decoded.Web != nil {
		result.Web = *decoded.Web
	}
	if decoded.Pow != nil {
		result.Pow = *decoded.Pow
	}
	if decoded.Debug != nil {
		result.Debug = *decoded.Debug
	}
	*c = result
	return nil
}

type WebConfig struct {
	EnableHTTPS    bool   `yaml:"enable_https" json:"enable_https"`
	ForceHTTPS     bool   `yaml:"force_https" json:"force_https"`
	PrivateKeyPath string `yaml:"private_key_path" json:"private_key_path"`
	PublicKeyPath  string `yaml:"public_key_path" json:"public_key_path"`
}

func (c *WebConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawWebConfig struct {
		EnableHTTPS    *bool   `yaml:"enable_https"`
		ForceHTTPS     *bool   `yaml:"force_https"`
		PrivateKeyPath *string `yaml:"private_key_path"`
		PublicKeyPath  *string `yaml:"public_key_path"`
	}
	var decoded rawWebConfig
	if err := value.Decode(&decoded); err != nil {
		return err
	}

	result := defaultConfig.Web
	if decoded.EnableHTTPS != nil {
		result.EnableHTTPS = *decoded.EnableHTTPS
	}
	if decoded.ForceHTTPS != nil {
		result.ForceHTTPS = *decoded.ForceHTTPS
	}
	if decoded.PrivateKeyPath != nil {
		result.PrivateKeyPath = *decoded.PrivateKeyPath
	}
	if decoded.PublicKeyPath != nil {
		result.PublicKeyPath = *decoded.PublicKeyPath
	}
	*c = result
	return nil
}

type MetricsConfig struct {
	Enabled               bool   `yaml:"enabled" json:"enabled"`
	PublicDashboard       bool   `yaml:"public_dashboard" json:"public_dashboard"`
	StorageMode           string `yaml:"storage_mode" json:"storage_mode"`
	MemoryMaxMin          int    `yaml:"memory_max_min" json:"memory_max_min"`
	SQLiteMaxDay          int    `yaml:"sqlite_max_day" json:"sqlite_max_day"`
	SQLiteCompactAfterDay int    `yaml:"sqlite_compact_after_day" json:"sqlite_compact_after_day"`
}

func (c *MetricsConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawMetricsConfig struct {
		Enabled               *bool   `yaml:"enabled"`
		PublicDashboard       *bool   `yaml:"public_dashboard"`
		StorageMode           *string `yaml:"storage_mode"`
		MemoryMaxMin          *int    `yaml:"memory_max_min"`
		SQLiteMaxDay          *int    `yaml:"sqlite_max_day"`
		SQLiteCompactAfterDay *int    `yaml:"sqlite_compact_after_day"`
	}
	var decoded rawMetricsConfig
	if err := value.Decode(&decoded); err != nil {
		return err
	}

	result := defaultConfig.Metrics
	if decoded.Enabled != nil {
		result.Enabled = *decoded.Enabled
	}
	if decoded.PublicDashboard != nil {
		result.PublicDashboard = *decoded.PublicDashboard
	}
	if decoded.StorageMode != nil {
		result.StorageMode = *decoded.StorageMode
	}
	if decoded.MemoryMaxMin != nil {
		result.MemoryMaxMin = *decoded.MemoryMaxMin
	}
	if decoded.SQLiteMaxDay != nil {
		result.SQLiteMaxDay = *decoded.SQLiteMaxDay
	}
	if decoded.SQLiteCompactAfterDay != nil {
		result.SQLiteCompactAfterDay = *decoded.SQLiteCompactAfterDay
	}
	*c = result
	return nil
}

type PowConfig struct {
	Enabled          bool `yaml:"enabled"`
	TaskCount        int  `yaml:"task_count"`
	Difficulty       int  `yaml:"difficulty"`
	TimestampMaxSkew int  `yaml:"timestamp_max_skew"`
}

func (c *PowConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawPowConfig struct {
		Enabled          *bool `yaml:"enabled"`
		TaskCount        *int  `yaml:"task_count"`
		Difficulty       *int  `yaml:"difficulty"`
		TimestampMaxSkew *int  `yaml:"timestamp_max_skew"`
	}
	var decoded rawPowConfig
	if err := value.Decode(&decoded); err != nil {
		return err
	}

	result := PowConfig{
		Enabled:          defaultConfig.Pow.Enabled,
		TaskCount:        defaultConfig.Pow.TaskCount,
		Difficulty:       defaultConfig.Pow.Difficulty,
		TimestampMaxSkew: defaultConfig.Pow.TimestampMaxSkew,
	}
	if decoded.Enabled != nil {
		result.Enabled = *decoded.Enabled
	}
	if decoded.TaskCount != nil {
		result.TaskCount = *decoded.TaskCount
	}
	if decoded.Difficulty != nil {
		result.Difficulty = *decoded.Difficulty
	}
	if decoded.TimestampMaxSkew != nil {
		result.TimestampMaxSkew = *decoded.TimestampMaxSkew
	}
	*c = result
	return nil
}

type AuthUser struct {
	User           string   `yaml:"user"`
	Pass           string   `yaml:"pass"`
	Perm           int      `yaml:"perm"`
	AllowInstances []string `yaml:"allow_instances,omitempty"`
	AllowGroups    []string `yaml:"allow_groups,omitempty"`
}
