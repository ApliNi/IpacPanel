package main

import (
	"time"
)

func runAutoStartSuite(env *testEnv) error {
	ctx, err := newSuiteContext(env, "启动实例")
	if err != nil {
		return err
	}
	defer ctx.stopPanel()
	listen, err := freeListenAddress()
	if err != nil {
		return err
	}
	config := defaultPanelConfig(listen)
	config.AutoStartInterval = 300
	instances := []instanceConfig{
		{Name: "p-high", Path: "./instances/", Command: helperCommand(ctx, "p-high", "long-running"), Terminal: terminalModePipe, AutoStart: true, StartPriority: intPtr(100)},
		{Name: "p-mid", Path: "./instances/", Command: helperCommand(ctx, "p-mid", "long-running"), Terminal: terminalModePipe, AutoStart: true, StartPriority: intPtr(10)},
		{Name: "p-default", Path: "./instances/", Command: helperCommand(ctx, "p-default", "long-running"), Terminal: terminalModePipe, AutoStart: true},
		{Name: "p-low", Path: "./instances/", Command: helperCommand(ctx, "p-low", "long-running"), Terminal: terminalModePipe, AutoStart: true, StartPriority: intPtr(-1)},
	}
	if err := ctx.writeConfig(config, instances); err != nil {
		return err
	}
	if err := ctx.startPanel(); err != nil {
		return err
	}
	started := time.Now()
	events, waitErr := ctx.Store.waitForN(func(ev event) bool { return ev.Event == "started" }, 4, 12*time.Second)
	intervalErr := waitErr
	if intervalErr == nil {
		intervalErr = assertMinimumIntervals(events, 250*time.Millisecond)
	}
	ctx.record("自动启动实例: 按照指定时间间隔启动实例", started, intervalErr, formatHelperTimes(events))
	started = time.Now()
	orderErr := waitErr
	if orderErr == nil {
		orderErr = assertHelperOrder(events, []string{"p-high", "p-mid", "p-default", "p-low"})
	}
	ctx.record("自动启动优先级: 按照指定的优先级顺序启动实例", started, orderErr, formatHelperTimes(events))
	return nil
}
