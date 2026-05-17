package main

import (
	"time"
)

func runStopCmdSuite(env *testEnv) error {
	ctx, err := newSuiteContext(env, "停止命令")
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
		{Name: "sc-ctrl", Path: "./instances/", Command: helperCommand(ctx, "sc-ctrl", "echo-stdin"), Terminal: terminalModePipe, AutoStart: true, AutoRestart: false, StopCommand: "^C"},
		{Name: "sc-text", Path: "./instances/", Command: helperCommand(ctx, "sc-text", "echo-stdin", "--expect-command", "stop"), Terminal: terminalModePipe, AutoStart: true, AutoRestart: false, StopCommand: "stop"},
		{Name: "sc-empty", Path: "./instances/", Command: helperCommand(ctx, "sc-empty", "long-running"), Terminal: terminalModePipe, AutoStart: true, AutoRestart: false, StopCommand: ""},
		{Name: "sc-noterm", Path: "./instances/", Command: helperCommand(ctx, "sc-noterm", "long-running"), Terminal: terminalModeNoTerminal, AutoStart: true, AutoRestart: false, StopCommand: "^C"},
	}
	if err := ctx.writeConfig(config, instances); err != nil {
		return err
	}
	if err := ctx.startPanel(); err != nil {
		return err
	}
	started := time.Now()
	_, err = ctx.Store.waitFor(filter("sc-ctrl", "ctrl_c_received"), 8*time.Second)
	ctx.record("默认 ^C 中断: 配置 ^C 时发送 0x03 字节, 辅助程序收到 ctrl_c 事件", started, err)

	started = time.Now()
	_, err = ctx.Store.waitFor(filter("sc-text", "expected_command"), 8*time.Second)
	ctx.record("自定义停止命令: 配置自定义命令文本时发送命令并追加回车, 辅助程序收到对应 stdin 数据", started, err)

	started = time.Now()
	_, err = ctx.Store.waitFor(filter("sc-noterm", "started"), 6*time.Second)
	if err == nil {
		err = ctx.Store.noEvent(func(ev event) bool {
			return ev.HelperID == "sc-noterm" && (ev.Event == "stdin" || ev.Event == "stdin_ctrl")
		}, 4*time.Second)
	}
	ctx.record("无终端模式下停止命令无效: 配置停止命令但无终端实例不接收 stdin, 面板直接结束进程", started, err)

	started = time.Now()
	_, err = ctx.Store.waitFor(filter("sc-empty", "started"), 6*time.Second)
	if err == nil {
		err = ctx.Store.noEvent(func(ev event) bool {
			return ev.HelperID == "sc-empty" && (ev.Event == "stdin" || ev.Event == "stdin_ctrl")
		}, 4*time.Second)
	}
	ctx.record("空停止命令直接结束进程: 配置留空时面板直接 kill 进程, 不发送 stdin", started, err)
	return nil
}
