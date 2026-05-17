package main

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

func waitUntil(timeout time.Duration, fn func() error) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if err := fn(); err != nil {
			last = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		return nil
	}
	if last == nil {
		last = errors.New("超时")
	}
	return last
}

func assertHelperOrder(events []event, expected []string) error {
	if len(events) < len(expected) {
		return fmt.Errorf("事件数量不足: %d/%d", len(events), len(expected))
	}
	for i, name := range expected {
		if events[i].HelperID != name {
			return fmt.Errorf("顺序错误: 第 %d 个期望 %s, 实际 %s", i+1, name, events[i].HelperID)
		}
	}
	return nil
}

func assertMinimumIntervals(events []event, min time.Duration) error {
	for i := 1; i < len(events); i++ {
		delta := events[i].Time.Sub(events[i-1].Time)
		if delta < min {
			return fmt.Errorf("%s -> %s 间隔过短: %s", events[i-1].HelperID, events[i].HelperID, delta)
		}
	}
	return nil
}

func assertRestartDelayFromExit(events []event, min time.Duration) error {
	var exiting, started event
	for _, ev := range events {
		if ev.Event == "exiting" && exiting.Time.IsZero() {
			exiting = ev
			continue
		}
		if !exiting.Time.IsZero() && ev.Event == "started" && ev.Time.After(exiting.Time) {
			started = ev
			break
		}
	}
	if exiting.Time.IsZero() || started.Time.IsZero() {
		return errors.New("缺少退出后重启事件")
	}
	if delta := started.Time.Sub(exiting.Time); delta < min {
		return fmt.Errorf("重启延迟过短: %s", delta)
	}
	return nil
}

func formatHelperTimes(events []event) string {
	if len(events) == 0 {
		return ""
	}
	base := events[0].Time
	parts := make([]string, 0, len(events))
	for _, ev := range events {
		parts = append(parts, fmt.Sprintf("%s/%s=%dms", ev.HelperID, ev.Event, ev.Time.Sub(base).Milliseconds()))
	}
	return strings.Join(parts, ", ")
}

func countEvents(events []event, helperID, eventName string) int {
	count := 0
	for _, ev := range events {
		if ev.HelperID == helperID && ev.Event == eventName {
			count++
		}
	}
	return count
}
func lastEvent(events []event, helperID, eventName string) event {
	var last event
	for _, ev := range events {
		if ev.HelperID == helperID && ev.Event == eventName {
			last = ev
		}
	}
	return last
}
