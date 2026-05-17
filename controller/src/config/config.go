package config

import (
	"IpacPanel/controller/src/atomic/file"
	compat "IpacPanel/controller/src/compat"
	"IpacPanel/controller/src/msg"
	"IpacPanel/daemon/version"
	"bytes"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	defaultTerminalCols uint16 = 184
	defaultTerminalRows uint16 = 39
)

const (
	DefaultTerminalCols = defaultTerminalCols
	DefaultTerminalRows = defaultTerminalRows
	DisplayTimeLayout   = "2006/01/02 15:04:05"
	FileTimeLayout      = "2006-01-02 15:04:05"
	MinHistorySizeKB    = 2
)

var (
	nameRegex     = regexp.MustCompile(`[\\/:*?"<>|]`)
	nameSuffixRe  = regexp.MustCompile(`^(.*?)(?:-(\d+))?$`)
	CurrentConfig Config
	ManagerMu     sync.RWMutex
	ConfigTxnMu   sync.Mutex
	appBaseDir    string
)

// InitializeInstanceRegistryHook is invoked after config load to build the startup-time runtime registry.
// It is a cold-start initialization hook, not a runtime reload hook.
var InitializeInstanceRegistryHook func([]Instance)

const (
	maxGroupNameLen       = 32
	maxInstanceNameLen    = 32
	maxUserNameLen        = 32
	MaxUserPasswordLen    = 4096
	MaxUserScopeItems     = 4096
	maxPathLen            = 4096
	maxCommandLen         = 4096
	maxStopCommandLen     = 4096
	maxCleanupCommandLen  = 4096
	maxAccessLinksTextLen = 2048
	maxTrustedProxyIPs    = 512
	maxTrustedProxyIPLen  = 128
	maxTasksPerInstance   = 512
	minAutoIntervalMS     = 0
	maxAutoIntervalMS     = 86400000
	minHistorySizeKB      = MinHistorySizeKB
	maxHistorySizeKB      = 65536
	minMetricsMemoryMin   = 1
	maxMetricsMemoryMin   = 10080
	minMetricsSQLiteDay   = 0
	maxMetricsSQLiteDay   = 36500
	minPowTaskCount       = 1
	maxPowTaskCount       = 128
	minPowDifficulty      = 1
	maxPowDifficulty      = 10
	minPowTimestampSkew   = 1
	maxPowTimestampSkew   = 3600
	minStartPriority      = -99999999
	maxStartPriority      = 99999999
	minRestartIntervalMS  = 0
	maxRestartIntervalMS  = 86400000
	maxTaskExprLen        = 128
	maxTaskCommandLen     = 4096
	maxWebTitleLen        = maxInstanceNameLen
	defaultWebTitle       = "IpacPanel"
	defaultListenAddress  = "127.0.0.1:25555"
	defaultPrivateKeyPath = "./data/cert/key"
	defaultPublicKeyPath  = "./data/cert/pem"
)

var defaultConfig = Config{
	DataVersion:              version.ControllerDataVersion,
	WebTitle:                 defaultWebTitle,
	Listen:                   defaultListenAddress,
	HistorySize:              27,
	AutoStartInterval:        200,
	AutoRestartInterval:      1000,
	InstanceUpdateStagingDir: "./!InstanceUpdate/",
	TrustedProxyIPs:          []string{"127.0.0.1"},
	Metrics: MetricsConfig{
		Enabled:               true,
		PublicDashboard:       false,
		StorageMode:           "memory",
		MemoryMaxMin:          30,
		SQLiteMaxDay:          7,
		SQLiteCompactAfterDay: 2,
	},
	Web: WebConfig{
		EnableHTTPS:    false,
		ForceHTTPS:     false,
		PrivateKeyPath: defaultPrivateKeyPath,
		PublicKeyPath:  defaultPublicKeyPath,
	},
	Pow: PowConfig{
		Enabled:          true,
		TaskCount:        24,
		Difficulty:       3,
		TimestampMaxSkew: 90,
	},
	Debug: false,
}

var configWriteMu sync.Mutex

var defaultInstanceConfig = Instance{
	Path:           "./instances/",
	Command:        "",
	Terminal:       Terminal,
	InputEncoding:  compat.DefaultTerminalEncoding,
	OutputEncoding: compat.DefaultTerminalEncoding,
	StopCommand:    "^C",
	AutoStart:      false,
	AutoRestart:    false,
}

func ValidateGroupName(group string) error {
	g := strings.TrimSpace(group)
	if g == "" {
		return nil
	}
	if nameRegex.MatchString(g) {
		return errors.New(msg.GroupNameInvalidChars)
	}
	if utf8.RuneCountInString(g) > maxGroupNameLen {
		return errors.New(msg.GroupNameTooLong)
	}
	return nil
}

func ValidateInstanceName(name string) error {
	n := strings.TrimSpace(name)
	if n == "" {
		return errors.New(msg.InstanceNameRequired)
	}
	if nameRegex.MatchString(n) {
		return errors.New(msg.InstanceNameInvalidChars)
	}
	if utf8.RuneCountInString(n) > maxInstanceNameLen {
		return errors.New(msg.InstanceNameTooLong)
	}
	return nil
}

func ValidateUserName(name string) error {
	n := strings.TrimSpace(name)
	if n == "" {
		return nil
	}
	if utf8.RuneCountInString(n) > maxUserNameLen {
		return errors.New(msg.UserNameTooLong)
	}
	return nil
}

func ValidateUserPassword(password string) error {
	if utf8.RuneCountInString(strings.TrimSpace(password)) > MaxUserPasswordLen {
		return errors.New(msg.InvalidPasswordLength)
	}
	return nil
}

func NormalizeUserScopeInstances(values []string) []string {
	return normalizeLimitedStringList(values, maxInstanceNameLen, MaxUserScopeItems)
}

func NormalizeUserScopeGroups(values []string) []string {
	return normalizeLimitedStringList(values, maxGroupNameLen, MaxUserScopeItems)
}

func NormalizeWebTitle(title string) string {
	t := strings.TrimSpace(title)
	if t == "" {
		return defaultWebTitle
	}
	return t
}

func containsInvalidSettingsControlChar(value string, allowNewline bool) bool {
	for _, r := range value {
		if allowNewline && (r == '\r' || r == '\n') {
			continue
		}
		if r == '\r' || r == '\n' || r == '\t' || r < 0x20 {
			return true
		}
	}
	return false
}

func ValidateSettingsWebTitle(title string) error {
	t := NormalizeWebTitle(title)
	if utf8.RuneCountInString(t) > maxWebTitleLen {
		return errors.New("WEB TITLE 最多包含 32 个字符")
	}
	if nameRegex.MatchString(t) {
		return errors.New("WEB TITLE 包含非法字符")
	}
	if containsInvalidSettingsControlChar(t, false) {
		return errors.New("WEB TITLE 包含非法控制字符")
	}
	return nil
}

func ValidateSettingsListenAddress(listen string) error {
	value := NormalizeListenAddress(listen)
	if containsInvalidSettingsControlChar(value, false) {
		return errors.New("LISTEN 包含非法控制字符")
	}
	if utf8.RuneCountInString(value) > maxPathLen {
		return errors.New("LISTEN 最多包含 4096 个字符")
	}
	if err := validateListenHostPort(value); err != nil {
		return err
	}
	return nil
}

func validateListenHostPort(listen string) error {
	host, portText, err := net.SplitHostPort(listen)
	if err != nil {
		return errors.New("LISTEN 必须是 host:port 或 :port")
	}
	if strings.TrimSpace(host) != host || strings.TrimSpace(portText) != portText || portText == "" {
		return errors.New("LISTEN 必须是 host:port 或 :port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("LISTEN 端口必须在 1-65535 范围内")
	}
	return nil
}

func ValidateSettingsInstanceUpdateStagingDir(path string) error {
	if utf8.RuneCountInString(strings.TrimSpace(path)) > maxPathLen {
		return errors.New("INSTANCE UPDATE DIR 最多包含 4096 个字符")
	}
	return nil
}

func ValidateSettingsWebCertificatePath(label string, path string) error {
	if containsInvalidSettingsControlChar(path, false) {
		return errors.New(label + " 包含非法控制字符")
	}
	if utf8.RuneCountInString(strings.TrimSpace(path)) > maxPathLen {
		return errors.New(label + " 最多包含 4096 个字符")
	}
	return nil
}

func ValidateSettingsTrustedProxyIPs(values []string) error {
	text := strings.Join(values, "\n")
	if containsInvalidSettingsControlChar(text, true) {
		return errors.New("TRUSTED PROXY IPS 包含非法控制字符")
	}
	for _, value := range values {
		item := strings.TrimSpace(value)
		if item == "" {
			continue
		}
		if utf8.RuneCountInString(item) > maxTrustedProxyIPLen {
			return fmt.Errorf("TRUSTED PROXY IPS 每项最多包含 %d 个字符", maxTrustedProxyIPLen)
		}
	}
	return nil
}

func ValidateSettingsTextFields(webTitle string, listen string, webConfig WebConfig, instanceUpdateStagingDir string, trustedProxyIPs []string) error {
	if err := ValidateSettingsWebTitle(webTitle); err != nil {
		return err
	}
	if err := ValidateSettingsListenAddress(listen); err != nil {
		return err
	}
	if err := ValidateSettingsWebCertificatePath("HTTPS PRIVATE KEY PATH", webConfig.PrivateKeyPath); err != nil {
		return err
	}
	if err := ValidateSettingsWebCertificatePath("HTTPS PUBLIC KEY PATH", webConfig.PublicKeyPath); err != nil {
		return err
	}
	if err := ValidateSettingsInstanceUpdateStagingDir(instanceUpdateStagingDir); err != nil {
		return err
	}
	return ValidateSettingsTrustedProxyIPs(trustedProxyIPs)
}

func NormalizeListenAddress(listen string) string {
	value := strings.TrimSpace(listen)
	if value == "" {
		return defaultListenAddress
	}
	return value
}

func NormalizeSettingsInstanceUpdateStagingDir(path string) string {
	value := strings.TrimSpace(path)
	if value == "" {
		return defaultConfig.InstanceUpdateStagingDir
	}
	return truncateRunes(value, maxPathLen)
}

func NormalizeWebConfig(webConfig WebConfig) WebConfig {
	webConfig.PrivateKeyPath = strings.TrimSpace(webConfig.PrivateKeyPath)
	if webConfig.PrivateKeyPath == "" {
		webConfig.PrivateKeyPath = defaultPrivateKeyPath
	}
	webConfig.PrivateKeyPath = truncateRunes(webConfig.PrivateKeyPath, maxPathLen)
	webConfig.PublicKeyPath = strings.TrimSpace(webConfig.PublicKeyPath)
	if webConfig.PublicKeyPath == "" {
		webConfig.PublicKeyPath = defaultPublicKeyPath
	}
	webConfig.PublicKeyPath = truncateRunes(webConfig.PublicKeyPath, maxPathLen)
	return webConfig
}

func NormalizeTrustedProxyIPs(values []string) []string {
	items := normalizePathList(values)
	if len(items) > maxTrustedProxyIPs {
		items = items[:maxTrustedProxyIPs]
	}
	return items
}

func ValidateTaskName(name string) error {
	n := strings.TrimSpace(name)
	if n == "" {
		return errors.New(msg.TaskNameRequired)
	}
	if utf8.RuneCountInString(n) > maxInstanceNameLen {
		return errors.New(msg.TaskNameTooLong)
	}
	return nil
}

func ValidateTaskExpr(expr string) error {
	e := strings.TrimSpace(expr)
	if e == "" {
		return errors.New(msg.TaskExprRequired)
	}
	if utf8.RuneCountInString(e) > maxTaskExprLen {
		return errors.New(msg.TaskExprTooLong)
	}
	if !IsValidTaskExpr(e) {
		return errors.New(msg.TaskExprInvalid)
	}
	return nil
}

func ValidateStringLength(value string, maxLen int, tooLongErr error) error {
	if maxLen <= 0 || tooLongErr == nil {
		return nil
	}
	if utf8.RuneCountInString(strings.TrimSpace(value)) > maxLen {
		return tooLongErr
	}
	return nil
}

func clampInt(value int, minValue int, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func truncateRunes(value string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxLen {
		return value
	}
	return string(runes[:maxLen])
}

func NormalizeInstanceNameForStorage(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return strings.TrimSpace(nameRegex.ReplaceAllString(name, "_"))
}

func splitNumericSuffix(name string) (string, int, bool) {
	name = strings.TrimSpace(name)
	matches := nameSuffixRe.FindStringSubmatch(name)
	if len(matches) != 3 {
		return name, 0, false
	}
	base := matches[1]
	if matches[2] == "" {
		return name, 0, false
	}
	index, err := strconv.Atoi(matches[2])
	if err != nil {
		return name, 0, false
	}
	return base, index, true
}

func uniqueName(name string, exists func(string) bool) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return name
	}
	if exists == nil || !exists(name) {
		return name
	}
	base, currentIndex, hasSuffix := splitNumericSuffix(name)
	if !hasSuffix {
		base = name
		currentIndex = 0
	}
	for next := currentIndex + 1; ; next++ {
		suffix := "-" + strconv.Itoa(next)
		candidate := base + suffix
		if !exists(candidate) {
			return candidate
		}
	}
}

func UniqueInstanceName(name string, exists func(string) bool) string {
	return uniqueName(name, exists)
}

func UniqueUserName(name string, exists func(string) bool) string {
	return uniqueName(name, exists)
}

func EnsureUniqueInstanceNames(instances []Instance) {
	seen := make(map[string]struct{}, len(instances))
	for i := range instances {
		instances[i].Name = UniqueInstanceName(instances[i].Name, func(candidate string) bool {
			_, ok := seen[candidate]
			return ok
		})
		if instances[i].Name != "" {
			seen[instances[i].Name] = struct{}{}
		}
	}
}

func EnsureUniqueUserNames(users []AuthUser) {
	seen := make(map[string]struct{}, len(users))
	for i := range users {
		users[i].User = UniqueUserName(users[i].User, func(candidate string) bool {
			_, ok := seen[candidate]
			return ok
		})
		if users[i].User != "" {
			seen[users[i].User] = struct{}{}
		}
	}
}

func NewDefaultConfig() Config {
	return defaultConfig
}

func init() {
	appBaseDir = detectAppBaseDir()
}

func pathExists(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func looksLikeAppBaseDir(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	dir = filepath.Clean(dir)
	if pathExists(filepath.Join(dir, "data")) {
		return true
	}
	if pathExists(filepath.Join(dir, "go.mod")) && pathExists(filepath.Join(dir, "src")) {
		return true
	}
	return false
}

func detectAppBaseDir() string {
	wd, err := os.Getwd()
	if err == nil {
		wd = strings.TrimSpace(wd)
		if wd != "" {
			wd = filepath.Clean(wd)
			if looksLikeAppBaseDir(wd) {
				return wd
			}
		}
	}

	exePath, err := os.Executable()
	if err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(exePath); resolveErr == nil {
			exePath = resolved
		}
		dir := strings.TrimSpace(filepath.Dir(exePath))
		if dir != "" {
			dir = filepath.Clean(dir)
			if looksLikeAppBaseDir(dir) {
				return dir
			}
			return dir
		}
	}
	if wd != "" {
		return filepath.Clean(wd)
	}
	return "."
}

func GetAppBaseDir() string {
	return appBaseDir
}

func ResolveAppPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return filepath.Clean(appBaseDir)
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(appBaseDir, path))
}

func ResolveDataPath(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ResolveAppPath("data")
	}
	return ResolveAppPath(filepath.Join("data", name))
}

func GetPublicDir() string {
	return ResolveAppPath("public")
}

func PublicDirExists() bool {
	publicDir := GetPublicDir()
	info, err := os.Stat(publicDir)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func ApplyInstanceDefaults(ins *Instance) {
	if ins == nil {
		return
	}

	ins.Name = strings.TrimSpace(ins.Name)
	ins.Group = NormalizeGroupNameForStorage(ins.Group)
	ins.Path = truncateRunes(strings.TrimSpace(ins.Path), maxPathLen)
	ins.Command = strings.TrimSpace(ins.Command)
	ins.Terminal = NormalizeTerminalMode(ins.Terminal)
	ins.InputEncoding = strings.TrimSpace(ins.InputEncoding)
	ins.OutputEncoding = strings.TrimSpace(ins.OutputEncoding)
	ins.StopCommand = strings.TrimSpace(ins.StopCommand)
	ins.CleanupCommand = strings.TrimSpace(ins.CleanupCommand)
	if ins.Path == "" {
		ins.Path = defaultInstanceConfig.Path
	}
	if ins.InputEncoding == "" {
		ins.InputEncoding = defaultInstanceConfig.InputEncoding
	} else if normalized, ok := compat.NormalizeTerminalEncoding(ins.InputEncoding); ok {
		ins.InputEncoding = normalized
	}
	if ins.OutputEncoding == "" {
		ins.OutputEncoding = defaultInstanceConfig.OutputEncoding
	} else if normalized, ok := compat.NormalizeTerminalEncoding(ins.OutputEncoding); ok {
		ins.OutputEncoding = normalized
	}
	ins.Tasks = NormalizeInstanceTasks(ins.Tasks)
}

func ResolveInstancePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultInstanceConfig.Path
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return ResolveAppPath(path), nil
}

func NormalizeGroupNameForStorage(group string) string {
	g := strings.TrimSpace(group)
	if strings.EqualFold(g, "UNGROUPED") {
		return ""
	}
	return g
}

func marshalConfig(cfg Config) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type persistedConfig struct {
	DataVersion              int            `yaml:"data_version"`
	WebTitle                 string         `yaml:"web_title"`
	Listen                   string         `yaml:"listen"`
	HistorySize              int            `yaml:"history_size"`
	AutoStartInterval        int            `yaml:"auto_start_interval"`
	AutoRestartInterval      *int           `yaml:"auto_restart_interval,omitempty"`
	InstanceUpdateStagingDir string         `yaml:"instance_update_staging_dir"`
	TrustedProxyIPs          *[]string      `yaml:"trusted_proxy_ips"`
	Metrics                  *MetricsConfig `yaml:"metrics"`
	Web                      WebConfig      `yaml:"web"`
	Pow                      *PowConfig     `yaml:"pow"`
	Debug                    bool           `yaml:"debug"`
}

func makePersistedConfig(cfg Config) persistedConfig {
	autoRestartInterval := NormalizeAutoRestartInterval(cfg.AutoRestartInterval)
	metrics := cfg.Metrics
	normalizeMetricsConfig(&metrics)
	pow := cfg.Pow
	normalizePowConfig(&pow)
	trustedProxyIPs := NormalizeTrustedProxyIPs(cfg.TrustedProxyIPs)
	return persistedConfig{
		DataVersion:              version.ControllerDataVersion,
		WebTitle:                 NormalizeWebTitle(cfg.WebTitle),
		Listen:                   NormalizeListenAddress(cfg.Listen),
		HistorySize:              NormalizeHistorySize(cfg.HistorySize),
		AutoStartInterval:        NormalizeAutoStartInterval(cfg.AutoStartInterval),
		AutoRestartInterval:      &autoRestartInterval,
		InstanceUpdateStagingDir: NormalizeSettingsInstanceUpdateStagingDir(cfg.InstanceUpdateStagingDir),
		TrustedProxyIPs:          &trustedProxyIPs,
		Metrics:                  &metrics,
		Web:                      NormalizeWebConfig(cfg.Web),
		Pow:                      &pow,
		Debug:                    cfg.Debug,
	}
}

func marshalPersistedConfig(cfg Config) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(makePersistedConfig(cfg)); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func unmarshalPersistedConfig(data []byte) (Config, error) {
	var persisted persistedConfig
	if err := yaml.Unmarshal(data, &persisted); err != nil {
		return Config{}, err
	}
	cfg := NewDefaultConfig()

	var decoded struct {
		DataVersion              *int       `yaml:"data_version"`
		WebTitle                 *string    `yaml:"web_title"`
		Listen                   *string    `yaml:"listen"`
		HistorySize              *int       `yaml:"history_size"`
		AutoStartInterval        *int       `yaml:"auto_start_interval"`
		InstanceUpdateStagingDir *string    `yaml:"instance_update_staging_dir"`
		Web                      *WebConfig `yaml:"web"`
		Debug                    *bool      `yaml:"debug"`
	}
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		return Config{}, err
	}
	if decoded.DataVersion != nil {
		cfg.DataVersion = *decoded.DataVersion
	}
	if decoded.WebTitle != nil {
		cfg.WebTitle = NormalizeWebTitle(*decoded.WebTitle)
	}
	if decoded.Listen != nil {
		cfg.Listen = NormalizeListenAddress(*decoded.Listen)
	}
	if decoded.HistorySize != nil {
		cfg.HistorySize = *decoded.HistorySize
	}
	if decoded.AutoStartInterval != nil {
		cfg.AutoStartInterval = *decoded.AutoStartInterval
	}
	if persisted.AutoRestartInterval != nil {
		cfg.AutoRestartInterval = *persisted.AutoRestartInterval
	}
	if decoded.InstanceUpdateStagingDir != nil {
		cfg.InstanceUpdateStagingDir = NormalizeSettingsInstanceUpdateStagingDir(*decoded.InstanceUpdateStagingDir)
	}
	if persisted.TrustedProxyIPs != nil {
		cfg.TrustedProxyIPs = append([]string(nil), (*persisted.TrustedProxyIPs)...)
	}
	if persisted.Metrics != nil {
		cfg.Metrics = *persisted.Metrics
	}
	if decoded.Web != nil {
		cfg.Web = *decoded.Web
	}
	if persisted.Pow != nil {
		cfg.Pow = *persisted.Pow
	}
	if decoded.Debug != nil {
		cfg.Debug = *decoded.Debug
	}
	return cfg, nil
}

func writeConfigAtomic(path string, data []byte) error {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "config.yml"
	}
	return file.WriteFile(path, data, file.Options{Overwrite: true, Mode: 0644, SyncDir: true})
}

func loadAuth() ([]AuthUser, error) {
	data, err := os.ReadFile(ResolveDataPath("auth.yml"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var users []AuthUser
	if err := yaml.Unmarshal(data, &users); err != nil {
		return nil, err
	}
	return users, nil
}

func loadNormalizedAuth() ([]AuthUser, error) {
	users, err := loadAuth()
	if err != nil {
		return nil, err
	}
	changed, err := NormalizeAuthUserPasswords(users)
	if err != nil {
		return nil, err
	}
	if changed {
		if err := saveAuth(users); err != nil {
			return nil, err
		}
	}
	return users, nil
}

func saveAuth(users []AuthUser) error {
	data, err := yaml.Marshal(users)
	if err != nil {
		return err
	}
	return writeConfigAtomic(ResolveDataPath("auth.yml"), data)
}

func loadInstances() ([]Instance, error) {
	data, err := os.ReadFile(ResolveDataPath("instances.yml"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var instances []Instance
	if err := yaml.Unmarshal(data, &instances); err != nil {
		return nil, err
	}
	return instances, nil
}

func saveInstances(instances []Instance) error {
	data, err := yaml.Marshal(instances)
	if err != nil {
		return err
	}
	return writeConfigAtomic(ResolveDataPath("instances.yml"), data)
}

func LoadConfig() error {
	data, err := os.ReadFile(ResolveDataPath("config.yml"))
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("配置文件不存在，使用默认配置")
		} else {
			log.Printf("读取 config.yml 失败，使用默认配置：%v", err)
		}
		CurrentConfig = NewDefaultConfig()
		authUsers, authErr := loadNormalizedAuth()
		if authErr != nil {
			return fmt.Errorf("读取 auth.yml 失败: %w", authErr)
		}
		CurrentConfig.Auth = authUsers
		instances, instancesErr := loadInstances()
		if instancesErr != nil {
			log.Printf("读取 instances.yml 失败：%v", instancesErr)
		} else {
			CurrentConfig.Instances = instances
		}
		NormalizeConfig()
		if InitializeInstanceRegistryHook != nil {
			InitializeInstanceRegistryHook(CurrentConfig.Instances)
		}
		if err := EnsureAdminUser(); err != nil {
			return err
		}
		return nil
	}

	loadedCfg, err := unmarshalPersistedConfig(data)
	if err != nil {
		return err
	}
	CurrentConfig = loadedCfg

	authUsers, err := loadNormalizedAuth()
	if err != nil {
		return fmt.Errorf("读取 auth.yml 失败: %w", err)
	}
	CurrentConfig.Auth = authUsers

	instances, err := loadInstances()
	if err != nil {
		log.Printf("读取 instances.yml 失败：%v", err)
	} else {
		CurrentConfig.Instances = instances
	}

	NormalizeConfig()
	if InitializeInstanceRegistryHook != nil {
		InitializeInstanceRegistryHook(CurrentConfig.Instances)
	}
	if err := EnsureAdminUser(); err != nil {
		return err
	}
	return nil
}

func SaveConfigSnapshot(cfg Config) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	data, err := marshalPersistedConfig(cfg)
	if err != nil {
		return err
	}
	if err := saveAuth(cfg.Auth); err != nil {
		return err
	}
	if err := saveInstances(cfg.Instances); err != nil {
		return err
	}
	if err := writeConfigAtomic(ResolveDataPath("config.yml"), data); err != nil {
		return err
	}
	return nil
}

func CloneIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func CloneAuthUsers(users []AuthUser) []AuthUser {
	if len(users) == 0 {
		return nil
	}
	out := make([]AuthUser, len(users))
	for i := range users {
		u := users[i]
		u.AllowInstances = NormalizeUserScopeInstances(u.AllowInstances)
		u.AllowGroups = NormalizeUserScopeGroups(u.AllowGroups)
		u.AllowInstances = append([]string(nil), u.AllowInstances...)
		u.AllowGroups = append([]string(nil), u.AllowGroups...)
		out[i] = u
	}
	return out
}

func CloneInstances(instances []Instance) []Instance {
	if len(instances) == 0 {
		return nil
	}
	out := make([]Instance, len(instances))
	for i := range instances {
		ins := instances[i]
		ins.StartPriority = CloneIntPtr(ins.StartPriority)
		ins.RestartInterval = CloneIntPtr(ins.RestartInterval)
		ins.AccessLinks = normalizeInstanceAccessLinksText(ins.AccessLinks)
		ins.Tasks = append([]Task(nil), ins.Tasks...)
		out[i] = ins
	}
	return out
}

func CloneConfigLocked() Config {
	cfg := CurrentConfig
	cfg.TrustedProxyIPs = append([]string{}, CurrentConfig.TrustedProxyIPs...)
	cfg.Auth = CloneAuthUsers(CurrentConfig.Auth)
	cfg.Instances = CloneInstances(CurrentConfig.Instances)
	return cfg
}

func NormalizeInstanceRequest(req *Instance) {
	ApplyInstanceDefaults(req)
	req.AccessLinks = normalizeInstanceAccessLinksText(req.AccessLinks)
	req.Tasks = NormalizeInstanceTasks(req.Tasks)
	req.StartPriority = NormalizeStartPriorityPtr(req.StartPriority)
	req.RestartInterval = NormalizeRestartIntervalPtr(req.RestartInterval)
}

func NormalizeConfig() {
	CurrentConfig.DataVersion = version.ControllerDataVersion
	CurrentConfig.WebTitle = NormalizeWebTitle(CurrentConfig.WebTitle)
	CurrentConfig.HistorySize = NormalizeHistorySize(CurrentConfig.HistorySize)
	CurrentConfig.AutoStartInterval = NormalizeAutoStartInterval(CurrentConfig.AutoStartInterval)
	CurrentConfig.AutoRestartInterval = NormalizeAutoRestartInterval(CurrentConfig.AutoRestartInterval)
	CurrentConfig.Listen = NormalizeListenAddress(CurrentConfig.Listen)
	CurrentConfig.Web = NormalizeWebConfig(CurrentConfig.Web)
	normalizeMetricsConfig(&CurrentConfig.Metrics)
	normalizePowConfig(&CurrentConfig.Pow)
	CurrentConfig.InstanceUpdateStagingDir = NormalizeSettingsInstanceUpdateStagingDir(CurrentConfig.InstanceUpdateStagingDir)
	CurrentConfig.TrustedProxyIPs = NormalizeTrustedProxyIPs(CurrentConfig.TrustedProxyIPs)
	for i := range CurrentConfig.Auth {
		CurrentConfig.Auth[i].AllowInstances = NormalizeUserScopeInstances(CurrentConfig.Auth[i].AllowInstances)
		CurrentConfig.Auth[i].AllowGroups = NormalizeUserScopeGroups(CurrentConfig.Auth[i].AllowGroups)
	}
	EnsureUniqueUserNames(CurrentConfig.Auth)
	for i := range CurrentConfig.Instances {
		ApplyInstanceDefaults(&CurrentConfig.Instances[i])
		CurrentConfig.Instances[i].AccessLinks = normalizeInstanceAccessLinksText(CurrentConfig.Instances[i].AccessLinks)
		CurrentConfig.Instances[i].Name = NormalizeInstanceNameForStorage(CurrentConfig.Instances[i].Name)
		CurrentConfig.Instances[i].StartPriority = NormalizeStartPriorityPtr(CurrentConfig.Instances[i].StartPriority)
		CurrentConfig.Instances[i].RestartInterval = NormalizeRestartIntervalPtr(CurrentConfig.Instances[i].RestartInterval)
	}
	EnsureUniqueInstanceNames(CurrentConfig.Instances)
}

func NormalizeHistorySize(size int) int {
	return clampInt(size, minHistorySizeKB, maxHistorySizeKB)
}

func ValidateHistorySize(size int) error {
	if size < minHistorySizeKB || size > maxHistorySizeKB {
		return fmt.Errorf("HISTORY SIZE 必须在 %d-%d 范围内", minHistorySizeKB, maxHistorySizeKB)
	}
	return nil
}

func NormalizeAutoStartInterval(value int) int {
	return clampInt(value, minAutoIntervalMS, maxAutoIntervalMS)
}

func NormalizeAutoRestartInterval(value int) int {
	return clampInt(value, minAutoIntervalMS, maxAutoIntervalMS)
}

func GetWebTitle() string {
	ManagerMu.RLock()
	defer ManagerMu.RUnlock()
	return NormalizeWebTitle(CurrentConfig.WebTitle)
}

func GetDebug() bool {
	ManagerMu.RLock()
	defer ManagerMu.RUnlock()
	return CurrentConfig.Debug
}

func normalizePowValue(value int, fallback int) int {
	if value < 1 {
		return fallback
	}
	return value
}

func normalizePowConfig(pow *PowConfig) {
	if pow == nil {
		return
	}
	pow.TaskCount = clampInt(normalizePowValue(pow.TaskCount, minPowTaskCount), minPowTaskCount, maxPowTaskCount)
	pow.Difficulty = clampInt(normalizePowValue(pow.Difficulty, minPowDifficulty), minPowDifficulty, maxPowDifficulty)
	pow.TimestampMaxSkew = clampInt(normalizePowValue(pow.TimestampMaxSkew, minPowTimestampSkew), minPowTimestampSkew, maxPowTimestampSkew)
}

func NormalizePowConfig(pow PowConfig) PowConfig {
	normalizePowConfig(&pow)
	return pow
}

func ValidatePowConfig(pow PowConfig) error {
	if pow.TaskCount < minPowTaskCount || pow.TaskCount > maxPowTaskCount {
		return fmt.Errorf("TASK COUNT 必须在 %d-%d 范围内", minPowTaskCount, maxPowTaskCount)
	}
	if pow.Difficulty < minPowDifficulty || pow.Difficulty > maxPowDifficulty {
		return fmt.Errorf("DIFFICULTY 必须在 %d-%d 范围内", minPowDifficulty, maxPowDifficulty)
	}
	if pow.TimestampMaxSkew < minPowTimestampSkew || pow.TimestampMaxSkew > maxPowTimestampSkew {
		return fmt.Errorf("TIMESTAMP MAX SKEW 必须在 %d-%d 范围内", minPowTimestampSkew, maxPowTimestampSkew)
	}
	return nil
}

func normalizePathList(paths []string) []string {
	if paths == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for i := range paths {
		path := strings.TrimSpace(paths[i])
		if path == "" {
			continue
		}
		if utf8.RuneCountInString(path) > maxTrustedProxyIPLen {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func normalizeLimitedStringList(values []string, maxLen int, maxItems int) []string {
	if len(values) == 0 || maxLen <= 0 || maxItems <= 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for i := range values {
		value := truncateRunes(strings.TrimSpace(values[i]), maxLen)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

func ValidateInstanceConfig(ins *Instance) error {
	if ins == nil {
		return errors.New(msg.InstanceConfigRequired)
	}
	if err := ValidateInstanceName(ins.Name); err != nil {
		return err
	}
	if ins.StartPriority != nil && (*ins.StartPriority < minStartPriority || *ins.StartPriority > maxStartPriority) {
		return errors.New(msg.StartPriorityInvalid)
	}
	if err := ValidateTerminalMode(ins.Terminal); err != nil {
		return err
	}
	if IsNoTerminal(ins.Terminal) && strings.TrimSpace(ins.Command) == "" {
		return errors.New(msg.NoTerminalCommandRequired)
	}
	if ins.RestartInterval != nil && (*ins.RestartInterval < minRestartIntervalMS || *ins.RestartInterval > maxRestartIntervalMS) {
		return errors.New(msg.RestartIntervalInvalid)
	}
	if _, ok := compat.NormalizeTerminalEncoding(ins.InputEncoding); !ok {
		return errors.New(msg.InputEncodingInvalid)
	}
	if _, ok := compat.NormalizeTerminalEncoding(ins.OutputEncoding); !ok {
		return errors.New(msg.OutputEncodingInvalid)
	}
	if err := ValidateGroupName(ins.Group); err != nil {
		return err
	}
	if err := ValidateStringLength(ins.Path, maxPathLen, errors.New(msg.PathTooLong)); err != nil {
		return err
	}
	if err := ValidateStringLength(ins.Command, maxCommandLen, errors.New(msg.CommandTooLong)); err != nil {
		return err
	}
	if err := ValidateStringLength(ins.StopCommand, maxStopCommandLen, errors.New(msg.StopCommandTooLong)); err != nil {
		return err
	}
	if err := ValidateStringLength(ins.CleanupCommand, maxCleanupCommandLen, errors.New(msg.CleanupCommandTooLong)); err != nil {
		return err
	}
	if err := validateInstanceAccessLinksText(ins.AccessLinks); err != nil {
		return err
	}
	if err := validateInstanceTasks(ins.Tasks); err != nil {
		return err
	}
	return nil
}

func normalizeInstanceAccessLinksText(text string) string {
	return truncateRunes(strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n")), maxAccessLinksTextLen)
}

func NormalizeStartPriorityPtr(value *int) *int {
	if value == nil {
		return nil
	}
	normalized := clampInt(*value, minStartPriority, maxStartPriority)
	return &normalized
}

func NormalizeRestartIntervalPtr(value *int) *int {
	if value == nil {
		return nil
	}
	normalized := clampInt(*value, minRestartIntervalMS, maxRestartIntervalMS)
	return &normalized
}

func validateInstanceAccessLinksText(text string) error {
	trimmed := normalizeInstanceAccessLinksText(text)
	if trimmed == "" {
		return nil
	}
	if utf8.RuneCountInString(trimmed) > maxAccessLinksTextLen {
		return errors.New(msg.AccessLinksTooLong)
	}
	return nil
}

func normalizeTaskName(name string) string {
	name = strings.TrimSpace(nameRegex.ReplaceAllString(strings.TrimSpace(name), "_"))
	return truncateRunes(name, maxInstanceNameLen)
}

func NormalizeInstanceTasks(tasks []Task) []Task {
	if len(tasks) == 0 {
		return tasks
	}
	limit := len(tasks)
	if limit > maxTasksPerInstance {
		limit = maxTasksPerInstance
	}
	out := make([]Task, 0, limit)
	seen := make(map[string]struct{}, limit)
	for i := 0; i < limit; i++ {
		t := tasks[i]
		t.Name = normalizeTaskName(t.Name)
		t.Expr = strings.TrimSpace(t.Expr)
		t.Action = strings.TrimSpace(t.Action)
		if t.Action == "strict_restart" {
			t.Action = "restart"
			t.StrictRestart = true
		}
		if t.Action != "stop" && t.Action != "restart" {
			t.UseKillStop = false
		}
		if t.Action != "restart" {
			t.StrictRestart = false
		}
		t.Command = strings.TrimSpace(t.Command)
		if t.Name == "" {
			continue
		}
		if _, ok := seen[t.Name]; ok {
			continue
		}
		if utf8.RuneCountInString(t.Expr) < 1 || utf8.RuneCountInString(t.Expr) > maxTaskExprLen {
			continue
		}
		if utf8.RuneCountInString(t.Command) > maxTaskCommandLen {
			continue
		}
		if t.Action == "command" && t.Command == "" {
			continue
		}
		seen[t.Name] = struct{}{}
		out = append(out, t)
	}
	return out
}

func validateInstanceTasks(tasks []Task) error {
	if len(tasks) == 0 {
		return nil
	}

	tasks = NormalizeInstanceTasks(tasks)
	seen := make(map[string]struct{}, len(tasks))
	for i := range tasks {
		t := &tasks[i]
		if err := ValidateTaskName(t.Name); err != nil {
			return err
		}
		if _, ok := seen[t.Name]; ok {
			return errors.New(msg.TaskNameNotUnique)
		}
		seen[t.Name] = struct{}{}
		if err := ValidateTaskExpr(t.Expr); err != nil {
			return err
		}
		switch t.Action {
		case "start", "stop", "restart", "command":
		default:
			return errors.New(msg.TaskActionInvalid)
		}
		if err := ValidateStringLength(t.Command, maxTaskCommandLen, errors.New(msg.TaskCommandTooLong)); err != nil {
			return err
		}
		if t.Action == "command" && t.Command == "" {
			return errors.New(msg.TaskCommandRequired)
		}
	}

	return nil
}

func GetHistoryLimit() int {
	ManagerMu.RLock()
	defer ManagerMu.RUnlock()
	return CurrentConfig.HistorySize
}

func normalizeMetricsConfig(metrics *MetricsConfig) {
	if metrics == nil {
		return
	}
	metrics.StorageMode = strings.ToLower(strings.TrimSpace(metrics.StorageMode))
	if metrics.StorageMode != "sqlite" {
		metrics.StorageMode = "memory"
	}
	metrics.MemoryMaxMin = clampInt(metrics.MemoryMaxMin, minMetricsMemoryMin, maxMetricsMemoryMin)
	metrics.SQLiteMaxDay = clampInt(metrics.SQLiteMaxDay, minMetricsSQLiteDay, maxMetricsSQLiteDay)
	metrics.SQLiteCompactAfterDay = clampInt(metrics.SQLiteCompactAfterDay, minMetricsSQLiteDay, maxMetricsSQLiteDay)
}

func NormalizeMetricsConfig(metrics MetricsConfig) MetricsConfig {
	normalizeMetricsConfig(&metrics)
	return metrics
}

func ValidateMetricsConfig(metrics MetricsConfig) error {
	storageMode := strings.ToLower(strings.TrimSpace(metrics.StorageMode))
	if storageMode != "memory" && storageMode != "sqlite" {
		return errors.New("METRICS STORAGE MODE 无效")
	}
	if metrics.MemoryMaxMin < minMetricsMemoryMin || metrics.MemoryMaxMin > maxMetricsMemoryMin {
		return fmt.Errorf("MEMORY MAX MIN 必须在 %d-%d 范围内", minMetricsMemoryMin, maxMetricsMemoryMin)
	}
	if metrics.SQLiteMaxDay < minMetricsSQLiteDay || metrics.SQLiteMaxDay > maxMetricsSQLiteDay {
		return fmt.Errorf("SQLITE MAX DAY 必须在 %d-%d 范围内", minMetricsSQLiteDay, maxMetricsSQLiteDay)
	}
	if metrics.SQLiteCompactAfterDay < minMetricsSQLiteDay || metrics.SQLiteCompactAfterDay > maxMetricsSQLiteDay {
		return fmt.Errorf("SQLITE COMPACT AFTER DAY 必须在 %d-%d 范围内", minMetricsSQLiteDay, maxMetricsSQLiteDay)
	}
	return nil
}

func GetMetricsConfig() MetricsConfig {
	ManagerMu.RLock()
	defer ManagerMu.RUnlock()
	metrics := CurrentConfig.Metrics
	normalizeMetricsConfig(&metrics)
	return metrics
}

func IsMetricsEnabled() bool {
	metrics := GetMetricsConfig()
	return metrics.Enabled
}

func GetAutoStartInterval() int {
	ManagerMu.RLock()
	defer ManagerMu.RUnlock()
	return CurrentConfig.AutoStartInterval
}

func GetAutoRestartInterval() int {
	ManagerMu.RLock()
	defer ManagerMu.RUnlock()
	return CurrentConfig.AutoRestartInterval
}

func GetListenAddress() string {
	ManagerMu.RLock()
	defer ManagerMu.RUnlock()
	return NormalizeListenAddress(CurrentConfig.Listen)
}

func GetWebConfig() WebConfig {
	ManagerMu.RLock()
	defer ManagerMu.RUnlock()
	return NormalizeWebConfig(CurrentConfig.Web)
}

func ResolveWebCertificatePath(path string) string {
	return ResolveAppPath(path)
}

func GetInstanceUpdateStagingDir() string {
	ManagerMu.RLock()
	defer ManagerMu.RUnlock()
	return NormalizeSettingsInstanceUpdateStagingDir(CurrentConfig.InstanceUpdateStagingDir)
}

func IsTrustedProxyIP(ip string) bool {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return false
	}
	ManagerMu.RLock()
	defer ManagerMu.RUnlock()
	for i := range CurrentConfig.TrustedProxyIPs {
		if CurrentConfig.TrustedProxyIPs[i] == ip {
			return true
		}
	}
	return false
}

func GetPowConfig() PowConfig {
	ManagerMu.RLock()
	defer ManagerMu.RUnlock()
	pow := CurrentConfig.Pow
	normalizePowConfig(&pow)
	return pow
}

func IsPowEnabled() bool {
	pow := GetPowConfig()
	return pow.Enabled && pow.TaskCount > 0 && pow.Difficulty > 0 && pow.TimestampMaxSkew > 0
}
