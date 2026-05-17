package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func locateDistributionDir(candidates ...string) (string, error) {
	for _, dir := range candidates {
		if hasPanelBinaries(dir) {
			return dir, nil
		}
	}
	return "", fmt.Errorf("未找到面板软件, 请将测试器和 %s / %s 放置在同一目录", panelBinaryName(), controllerBinaryName())
}

func hasPanelBinaries(dir string) bool {
	if dir == "" {
		return false
	}
	_, panelErr := os.Stat(filepath.Join(dir, panelBinaryName()))
	_, controllerErr := os.Stat(filepath.Join(dir, controllerBinaryName()))
	return panelErr == nil && controllerErr == nil
}

func newSuiteContext(env *testEnv, name string) (*suiteContext, error) {
	dir := filepath.Join(env.RunDir, safeFileName(name))
	ctx := &suiteContext{Env: env, Name: name, Dir: dir, AppDir: filepath.Join(dir, "app"), DataDir: filepath.Join(dir, "app", "data"), EventDir: filepath.Join(dir, "events")}
	ctx.Store = &eventStore{dir: ctx.EventDir}
	for _, path := range []string{ctx.AppDir, ctx.DataDir, ctx.EventDir, filepath.Join(ctx.AppDir, "instances")} {
		if err := os.MkdirAll(path, 0755); err != nil {
			return nil, err
		}
	}
	if err := copyFile(filepath.Join(env.DistributionDir, panelBinaryName()), filepath.Join(ctx.AppDir, panelBinaryName()), 0755); err != nil {
		return nil, err
	}
	if err := copyFile(filepath.Join(env.DistributionDir, controllerBinaryName()), filepath.Join(ctx.AppDir, controllerBinaryName()), 0755); err != nil {
		return nil, err
	}
	ctx.HelperExe = filepath.Join(ctx.AppDir, "instances", filepath.Base(env.TesterExe))
	if err := copyFile(env.TesterExe, ctx.HelperExe, 0755); err != nil {
		return nil, err
	}
	return ctx, nil
}

func panelBinaryName() string {
	if runtime.GOOS == "windows" {
		return "IpacPanel.exe"
	}
	return "IpacPanel"
}

func controllerBinaryName() string {
	if runtime.GOOS == "windows" {
		return "IpacPanel_Controller.exe"
	}
	return "IpacPanel_Controller"
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("打开源文件 %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("创建目标文件 %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dst, mode)
	}
	return nil
}

func (ctx *suiteContext) startPanel() error {
	cmd := exec.Command(filepath.Join(ctx.AppDir, panelBinaryName()))
	cmd.Dir = ctx.AppDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动面板: %w", err)
	}
	ctx.Panel = &panelProcess{cmd: cmd}
	go drainLog(filepath.Join(ctx.Dir, "panel.stdout.log"), stdout)
	go drainLog(filepath.Join(ctx.Dir, "panel.stderr.log"), stderr)
	return nil
}

func drainLog(path string, r io.Reader) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		_, _ = io.Copy(io.Discard, r)
		return
	}
	defer f.Close()
	_, _ = io.Copy(f, r)
}

func (ctx *suiteContext) stopPanel() {
	if ctx.Panel == nil || ctx.Panel.cmd == nil || ctx.Panel.cmd.Process == nil {
		return
	}
	_ = ctx.Panel.cmd.Process.Kill()
	done := make(chan struct{})
	go func() { _ = ctx.Panel.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}

func (ctx *suiteContext) waitPanelReady(timeout time.Duration) error {
	return waitUntil(timeout, func() error {
		if ctx.Panel == nil || ctx.Panel.cmd == nil || ctx.Panel.cmd.Process == nil {
			return fmt.Errorf("面板进程未启动")
		}
		if _, err := os.Stat(filepath.Join(ctx.DataDir, "config.yml")); err != nil {
			return fmt.Errorf("等待 config.yml")
		}
		return nil
	})
}

func defaultPanelConfig(listen string) panelConfig {
	return panelConfig{WebTitle: "IpacPanel Tester", Listen: listen, HistorySize: 27, AutoStartInterval: 200, AutoRestartInterval: 1000, InstanceUpdateStagingDir: "./!InstanceUpdate/", TrustedProxyIPs: []string{"127.0.0.1"}, Pow: powConfig{Enabled: false, TaskCount: 1, Difficulty: 1, TimestampMaxSkew: 90000}}
}

func (ctx *suiteContext) writeConfig(config panelConfig, instances []instanceConfig) error {
	if err := writeYAML(filepath.Join(ctx.DataDir, "config.yml"), config); err != nil {
		return err
	}
	if err := writeYAML(filepath.Join(ctx.DataDir, "instances.yml"), instances); err != nil {
		return err
	}
	return writeYAML(filepath.Join(ctx.DataDir, "auth.yml"), []authUser{{User: "admin", Pass: "HASH/$2a$10$Q.8xQwZ8gN3jG6lJ3P6vCe9eI6f9uQ5TArl66S5G8XkK8yG1LaR7m", Perm: 7}})
}

func writeYAML(path string, value any) error {
	data, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func freeListenAddress() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	return ln.Addr().String(), nil
}

func helperCommand(ctx *suiteContext, helperID, helperCase string, args ...string) string {
	parts := []string{quoteArg(ctx.HelperExe), "--helper", "--helper-id", quoteArg(helperID), "--case", quoteArg(helperCase), "--event-dir", quoteArg(ctx.EventDir)}
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}

func quoteArg(value string) string {
	if runtime.GOOS == "windows" {
		return strconv.Quote(value)
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
func intPtr(v int) *int { return &v }
func safeFileName(name string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_").Replace(name)
}
