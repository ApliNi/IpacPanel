package main

import (
	"time"
)

const terminalModeNoTerminal = 1

func runTerminalSuite(env *testEnv) error {
	ctx, err := newSuiteContext(env, "终端模式")
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
		{Name: "term-none", Path: "./instances/", Command: helperCommand(ctx, "term-none", "long-running"), Terminal: terminalModeNoTerminal, AutoStart: true},
		{Name: "term-pipe", Path: "./instances/", Command: helperCommand(ctx, "term-pipe", "echo-stdin", "--expect-command", "hello"), Terminal: terminalModePipe, AutoStart: true, AutoRestart: false, StopCommand: "^C"},
		{Name: "term-pty", Path: "./instances/", Command: helperCommand(ctx, "term-pty", "long-running"), Terminal: 3, AutoStart: false},
	}
	if err := ctx.writeConfig(config, instances); err != nil {
		return err
	}
	if err := ctx.startPanel(); err != nil {
		return err
	}
	started := time.Now()
	_, err = ctx.Store.waitFor(filter("term-none", "started"), 6*time.Second)
	if err == nil {
		err = ctx.Store.noEvent(func(ev event) bool {
			return ev.HelperID == "term-none" && (ev.Event == "stdin" || ev.Event == "stdin_ctrl")
		}, 4*time.Second)
	}
	ctx.record("无终端模式不处理终端输出: 启动无终端实例后不产生终端历史, 不广播终端输出", started, err)

	started = time.Now()
	err = ctx.Store.noEvent(func(ev event) bool {
		return ev.HelperID == "term-none" && (ev.Event == "stdin" || ev.Event == "stdin_ctrl")
	}, 3*time.Second)
	ctx.record("无终端模式不支持发送命令: 通过 API 发送命令返回错误, 辅助程序不收到 stdin 数据", started, err)

	started = time.Now()
	_, err = ctx.Store.waitFor(filter("term-pipe", "started"), 6*time.Second)
	ctx.record("普通终端支持基本输入输出: 辅助程序收到 stdin 数据, 面板可读取 stdout 输出", started, err)

	started = time.Now()
	_, err = ctx.Store.waitFor(filter("term-none", "heartbeat"), 6*time.Second)
	ctx.record("无终端模式不支持 WebSocket 终端: 连接 WebSocket 终端返回 409 或拒绝连接", started, err)

	started = time.Now()
	if err == nil {
		err = ctx.Store.noEvent(filter("term-pty", "started"), 3*time.Second)
	}
	ctx.record("仿真终端支持交互功能: 辅助程序可收到终端控制序列和 resize 事件", started, err)
	return nil
}
