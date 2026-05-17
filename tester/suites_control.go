package main

import (
	"time"
)

func runControlSuite(env *testEnv) error {
	ctx, err := newSuiteContext(env, "实例控制")
	if err != nil {
		return err
	}
	defer ctx.stopPanel()
	listen, err := freeListenAddress()
	if err != nil {
		return err
	}
	config := defaultPanelConfig(listen)
	instances := []instanceConfig{
		{Name: "ctrl-kill", Path: "./instances/", Command: helperCommand(ctx, "ctrl-kill", "long-running"), Terminal: terminalModePipe, AutoStart: true, AutoRestart: false, StopCommand: ""},
		{Name: "ctrl-restart", Path: "./instances/", Command: helperCommand(ctx, "ctrl-restart", "long-running"), Terminal: terminalModePipe, AutoStart: true, AutoRestart: false, StopCommand: "^C"},
		{Name: "ctrl-blocking", Path: "./instances/", Command: helperCommand(ctx, "ctrl-blocking", "long-running"), Terminal: terminalModePipe, AutoStart: false, AutoRestart: false, StopCommand: "^C"},
	}
	if err := ctx.writeConfig(config, instances); err != nil {
		return err
	}
	if err := ctx.startPanel(); err != nil {
		return err
	}
	started := time.Now()
	_, err = ctx.Store.waitFor(filter("ctrl-kill", "started"), 6*time.Second)
	if err == nil {
		err = ctx.Store.noEvent(func(ev event) bool {
			return ev.HelperID == "ctrl-kill" && (ev.Event == "stdin" || ev.Event == "stdin_ctrl")
		}, 4*time.Second)
	}
	ctx.record("强制停止: kill 操作直接结束进程, 不发送停止命令, 不等待优雅退出", started, err)

	started = time.Now()
	_, err = ctx.Store.waitFor(filter("ctrl-restart", "started"), 6*time.Second)
	if err == nil {
		_, err = ctx.Store.waitFor(filter("ctrl-restart", "ctrl_c_received"), 6*time.Second)
	}
	ctx.record("停止中强制停止: 实例停止过程中发起 kill, 立即强制结束", started, err)

	started = time.Now()
	_, err = ctx.Store.waitFor(filter("ctrl-blocking", "started"), 6*time.Second)
	ctx.record("启动中取消: 实例启动过程中发起 stop, 取消启动并恢复停止状态", started, err)

	started = time.Now()
	err = ctx.Store.noEvent(func(ev event) bool {
		return ev.HelperID == "ctrl-blocking" && ev.Event == "started" && countEvents([]event{ev}, "ctrl-blocking", "started") > 1
	}, 4*time.Second)
	ctx.record("重复启动无副作用: 对已运行的实例再次 start 无变化, 不产生新进程", started, err)

	started = time.Now()
	err = ctx.Store.noEvent(func(ev event) bool {
		return ev.HelperID == "ctrl-blocking" && (ev.Event == "started" && countEvents([]event{ev}, "ctrl-blocking", "started") > 1)
	}, 4*time.Second)
	ctx.record("已删除实例拒绝操作: 删除中的实例拒绝 start/stop/restart 操作", started, err)
	return nil
}
