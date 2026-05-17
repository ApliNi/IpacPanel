package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func runInitSuite(env *testEnv) error {
	ctx, err := newSuiteContext(env, "初始化")
	if err != nil {
		return err
	}
	defer ctx.stopPanel()
	started := time.Now()
	err = ctx.startPanel()
	if err == nil {
		err = ctx.waitPanelReady(defaultTimeout)
	}
	ctx.record("首次启动正常: 启动后存在守护进程和管理进程", started, err)
	started = time.Now()
	err = assertYAMLFiles(ctx.DataDir, "config.yml", "auth.yml", "instances.yml")
	ctx.record("生成默认配置和初始账号: 在正确位置生成对应 yml 文件, 内容不为空且可被 yml 解析", started, err)
	started = time.Now()
	err = assertSecondPanelExits(ctx)
	ctx.record("相同路径下不可重复启动守护进程: 重复启动的守护进程自动退出", started, err)
	return nil
}

func assertYAMLFiles(dir string, names ...string) error {
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			return fmt.Errorf("%s 内容为空", name)
		}
		var parsed any
		if err := yaml.Unmarshal(data, &parsed); err != nil {
			return fmt.Errorf("%s 解析失败: %w", name, err)
		}
	}
	return nil
}

func assertSecondPanelExits(ctx *suiteContext) error {
	cmd := exec.Command(filepath.Join(ctx.AppDir, panelBinaryName()))
	cmd.Dir = ctx.AppDir
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		return errors.New("重复启动的守护进程未在 5s 内退出")
	}
}
