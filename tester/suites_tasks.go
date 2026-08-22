package main

import (
	"errors"
	"time"
)

func runTasksSuite(env *testEnv) error {
	ctx, err := newSuiteContext(env, "计划任务")
	if err != nil {
		return err
	}
	defer ctx.stopPanel()
	listen, err := freeListenAddress()
	if err != nil {
		return err
	}
	config := defaultPanelConfig(listen)
	interval := 700
	expr := "* * * * *"
	instances := []instanceConfig{
		{Name: "task-cycle", Path: "./instances/", Command: helperCommand(ctx, "task-cycle", "exit-normally", "--exit-after", "100ms"), Terminal: terminalModePipe, Tasks: []taskConfig{{Name: "周期", Enabled: true, Expr: expr, Action: "start"}}},
		{Name: "task-start", Path: "./instances/", Command: helperCommand(ctx, "task-start", "long-running"), Terminal: terminalModePipe, Tasks: []taskConfig{{Name: "启动", Enabled: true, Expr: expr, Action: "start"}}},
		{Name: "task-stop", Path: "./instances/", Command: helperCommand(ctx, "task-stop", "long-running"), Terminal: terminalModePipe, AutoStart: true, Tasks: []taskConfig{{Name: "停止", Enabled: true, Expr: expr, Action: "stop"}}},
		{Name: "task-restart", Path: "./instances/", Command: helperCommand(ctx, "task-restart", "long-running"), Terminal: terminalModePipe, AutoStart: true, RestartInterval: &interval, Tasks: []taskConfig{{Name: "重启", Enabled: true, Expr: expr, Action: "restart"}}},
		{Name: "task-strict-running", Path: "./instances/", Command: helperCommand(ctx, "task-strict-running", "long-running"), Terminal: terminalModePipe, AutoStart: true, RestartInterval: &interval, Tasks: []taskConfig{{Name: "严格重启", Enabled: true, Expr: expr, Action: "restart", StrictRestart: true}}},
		{Name: "task-strict-stopped", Path: "./instances/", Command: helperCommand(ctx, "task-strict-stopped", "long-running"), Terminal: terminalModePipe, RestartInterval: &interval, Tasks: []taskConfig{{Name: "严格重启停止", Enabled: true, Expr: expr, Action: "restart", StrictRestart: true}}},
		{Name: "task-command", Path: "./instances/", Command: helperCommand(ctx, "task-command", "echo-stdin", "--expect-command", "reload"), Terminal: terminalModePipe, AutoStart: true, Tasks: []taskConfig{{Name: "命令", Enabled: true, Expr: expr, Action: "command", Command: "reload"}}},
		{Name: "task-ctrl", Path: "./instances/", Command: helperCommand(ctx, "task-ctrl", "echo-stdin"), Terminal: terminalModePipe, AutoStart: true, Tasks: []taskConfig{{Name: "键盘事件", Enabled: true, Expr: expr, Action: "command", Command: "^C"}}},
	}
	if err := ctx.writeConfig(config, instances); err != nil {
		return err
	}
	if err := ctx.startPanel(); err != nil {
		return err
	}
	started := time.Now()
	cycleEvents, err := ctx.Store.waitForN(filter("task-cycle", "started"), 2, 8*time.Second)
	ctx.record("计划任务按照正确的周期运行", started, err, formatHelperTimes(cycleEvents))
	started = time.Now()
	startEvents, err := ctx.Store.waitForN(filter("task-start", "started"), 1, 6*time.Second)
	if err == nil {
		err = ctx.Store.noEvent(func(ev event) bool {
			return ev.HelperID == "task-start" && ev.Event == "started" && ev.Time.After(startEvents[0].Time.Add(500*time.Millisecond))
		}, 3*time.Second)
	}
	ctx.record("启动: 正确完成启动, 对于已启动的实例无反应, 与其他计划任务冲突时不会重复执行", started, err, formatHelperTimes(startEvents))
	started = time.Now()
	err = waitUntil(8*time.Second, func() error {
		events, _ := ctx.Store.events()
		if countEvents(events, "task-stop", "heartbeat") == 0 {
			return errors.New("尚未观察到 task-stop 心跳")
		}
		last := lastEvent(events, "task-stop", "heartbeat")
		if time.Since(last.Time) < 1500*time.Millisecond {
			return errors.New("task-stop 仍在运行")
		}
		return nil
	})
	ctx.record("停止: 正确完成停止, 对于已停止的实例无反应, 与其他计划任务冲突时不会重复执行", started, err)
	started = time.Now()
	restartEvents, err := ctx.Store.waitForN(filter("task-restart", "started"), 2, 8*time.Second)
	ctx.record("重启: 正确完成重启, 对于已启动的实例先停止再等待时间最后启动, 对于已停止的实例等待时间后启动, 与其他计划任务冲突时不会重复执行", started, err, formatHelperTimes(restartEvents))
	started = time.Now()
	strictEvents, err := ctx.Store.waitForN(filter("task-strict-running", "started"), 2, 8*time.Second)
	if err == nil {
		err = ctx.Store.noEvent(filter("task-strict-stopped", "started"), 4*time.Second)
	}
	ctx.record("严格重启: 正确完成重启, 对于已启动的实例则进行重启, 对于已停止的实例则不进行重启", started, err, formatHelperTimes(strictEvents))
	started = time.Now()
	_, err = ctx.Store.waitFor(filter("task-command", "expected_command"), 8*time.Second)
	if err == nil {
		_, err = ctx.Store.waitFor(filter("task-ctrl", "ctrl_c_received"), 8*time.Second)
	}
	ctx.record("命令: 正确发送键盘事件, 正确发送命令文本且被辅助程序接收", started, err)
	return nil
}
