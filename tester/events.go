package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type eventStore struct{ dir string }

func (s *eventStore) events() ([]event, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var events []event
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		file, err := os.Open(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			ev, err := parseEventFrame(scanner.Bytes())
			if err == nil {
				events = append(events, ev)
			}
		}
		_ = file.Close()
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Time.Before(events[j].Time) })
	return events, nil
}

func parseEventFrame(line []byte) (event, error) {
	if len(line) == 0 || line[0] != ':' {
		return event{}, errors.New("事件行缺少帧前缀")
	}
	separator := bytes.IndexByte(line[1:], ':')
	if separator < 0 {
		return event{}, errors.New("事件行缺少帧分隔符")
	}
	separator++
	if separator == 1 {
		return event{}, errors.New("事件行事件名为空")
	}
	if len(line) <= separator+1 || line[separator+1] != ' ' {
		return event{}, errors.New("事件行帧分隔符格式错误")
	}
	var ev event
	if err := json.Unmarshal(line[separator+2:], &ev); err != nil {
		return event{}, err
	}
	ev.Event = string(line[1:separator])
	return ev, nil
}

func (s *eventStore) waitFor(filter eventFilter, timeout time.Duration) (event, error) {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		events, err := s.events()
		if err != nil {
			last = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		for _, ev := range events {
			if filter(ev) {
				return ev, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if last != nil {
		return event{}, last
	}
	return event{}, errors.New("等待事件超时")
}

func (s *eventStore) waitForN(filter eventFilter, n int, timeout time.Duration) ([]event, error) {
	deadline := time.Now().Add(timeout)
	var matched []event
	for time.Now().Before(deadline) {
		events, err := s.events()
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		matched = matched[:0]
		for _, ev := range events {
			if filter(ev) {
				matched = append(matched, ev)
			}
		}
		if len(matched) >= n {
			return append([]event(nil), matched...), nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return matched, fmt.Errorf("等待 %d 个事件超时", n)
}

func (s *eventStore) matching(filter eventFilter) ([]event, error) {
	events, err := s.events()
	if err != nil {
		return nil, err
	}
	matched := make([]event, 0)
	for _, ev := range events {
		if filter(ev) {
			matched = append(matched, ev)
		}
	}
	return matched, nil
}

func (s *eventStore) waitUntilAfter(filter eventFilter, after time.Time, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		events, _ := s.events()
		for _, ev := range events {
			if filter(ev) && ev.Time.After(after.Add(10*time.Millisecond)) {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("等待后续事件超时")
}

func (s *eventStore) noEvent(filter eventFilter, duration time.Duration) error {
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		events, _ := s.events()
		for _, ev := range events {
			if filter(ev) {
				return fmt.Errorf("出现非预期事件: %s/%s", ev.HelperID, ev.Event)
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

func filter(helperID, eventName string) eventFilter {
	return func(ev event) bool {
		return (helperID == "" || ev.HelperID == helperID) && (eventName == "" || ev.Event == eventName)
	}
}
