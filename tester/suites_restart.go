package main

import (
	"time"
)

func runRestartSuite(env *testEnv) error {
	ctx, err := newSuiteContext(env, "自动重启")
	if err != nil {
		return err
	}
	defer ctx.stopPanel()
	listen, err := freeListenAddress()
	if err != nil {
		return err
	}
	interval := 1000
	config := defaultPanelConfig(listen)
	instances := []instanceConfig{
		{Name: "natural", Path: "./instances/", Command: helperCommand(ctx, "natural", "exit-normally", "--exit-after", "300ms", "--exit-code", "0"), Terminal: terminalModePipe, AutoStart: true, AutoRestart: true, RestartInterval: &interval},
		{Name: "abnormal", Path: "./instances/", Command: helperCommand(ctx, "abnormal", "exit-abnormally", "--exit-after", "300ms", "--exit-code", "2"), Terminal: terminalModePipe, AutoStart: true, AutoRestart: true, RestartInterval: &interval},
	}
	if err := ctx.writeConfig(config, instances); err != nil {
		return err
	}
	if err := ctx.startPanel(); err != nil {
		return err
	}
	started := time.Now()
	err = waitRestartSequence(ctx, "natural")
	naturalEvents, _ := ctx.Store.matching(func(ev event) bool { return ev.HelperID == "natural" })
	ctx.record("实例自然退出后等待指定的时间间隔重新启动实例", started, err, formatHelperTimes(naturalEvents))
	started = time.Now()
	err = waitRestartSequence(ctx, "abnormal")
	abnormalEvents, _ := ctx.Store.matching(func(ev event) bool { return ev.HelperID == "abnormal" })
	ctx.record("实例异常退出后等待指定的时间间隔重新启动实例", started, err, formatHelperTimes(abnormalEvents))
	return nil
}

func waitRestartSequence(ctx *suiteContext, helperID string) error {
	if _, err := ctx.Store.waitFor(filter(helperID, "started"), 6*time.Second); err != nil {
		return err
	}
	if _, err := ctx.Store.waitFor(filter(helperID, "exiting"), 6*time.Second); err != nil {
		return err
	}
	if err := ctx.Store.waitUntilAfter(filter(helperID, "started"), time.Now().Add(-10*time.Millisecond), 6*time.Second); err != nil {
		return err
	}
	events, _ := ctx.Store.matching(func(ev event) bool { return ev.HelperID == helperID })
	return assertRestartDelayFromExit(events, 800*time.Millisecond)
}
