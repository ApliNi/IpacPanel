package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type helperOptions struct {
	ID            string
	Case          string
	EventDir      string
	Heartbeat     time.Duration
	ExitAfter     time.Duration
	ExitCode      int
	ExpectCommand string
	StopOnCommand bool
	MemoryMB      int
	CPULoad       int
}

func runHelper(opts helperOptions) error {
	if opts.ID == "" {
		opts.ID = "helper-" + strconv.Itoa(os.Getpid())
	}
	if opts.EventDir == "" {
		return errors.New("辅助模式缺少 --event-dir")
	}
	writer := newHelperEventWriter(opts.EventDir, opts.ID)
	writer.write("started", map[string]string{"case": opts.Case})
	if opts.MemoryMB > 0 {
		mem := make([]byte, opts.MemoryMB*1024*1024)
		for i := range mem {
			mem[i] = byte(i)
		}
		writer.write("memory_allocated", map[string]string{"mb": strconv.Itoa(opts.MemoryMB)})
	}
	if opts.CPULoad > 0 {
		go burnCPU(opts.CPULoad)
	}
	cmdCh := make(chan string, 8)
	ctrlCh := make(chan byte, 8)
	go readHelperStdin(writer, cmdCh, ctrlCh)
	if opts.Case == "exit-normally" || opts.Case == "exit-abnormally" {
		delay := opts.ExitAfter
		if delay <= 0 {
			delay = 500 * time.Millisecond
		}
		time.Sleep(delay)
		writer.write("exiting", map[string]string{"exit_code": strconv.Itoa(opts.ExitCode)})
		os.Exit(opts.ExitCode)
	}
	if opts.Case == "slow-start" {
		delay := opts.ExitAfter
		if delay <= 0 {
			delay = time.Second
		}
		time.Sleep(delay)
		writer.write("ready", nil)
	}
	ticker := time.NewTicker(opts.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case command := <-cmdCh:
			if opts.ExpectCommand == "" || command == opts.ExpectCommand {
				writer.write("expected_command", map[string]string{"text": command})
				if opts.StopOnCommand {
					writer.write("exiting", map[string]string{"reason": "command", "exit_code": strconv.Itoa(opts.ExitCode)})
					os.Exit(opts.ExitCode)
				}
			}
		case ctrl := <-ctrlCh:
			if ctrl == 3 {
				writer.write("ctrl_c_received", nil)
				if opts.StopOnCommand {
					writer.write("exiting", map[string]string{"reason": "ctrl_c", "exit_code": strconv.Itoa(opts.ExitCode)})
					os.Exit(opts.ExitCode)
				}
			}
		case <-ticker.C:
			writer.write("heartbeat", nil)
		}
	}
}

type helperEventWriter struct {
	path string
	mu   sync.Mutex
}

func newHelperEventWriter(eventDir, helperID string) *helperEventWriter {
	_ = os.MkdirAll(eventDir, 0755)
	return &helperEventWriter{path: filepath.Join(eventDir, safeFileName(helperID)+"-"+strconv.Itoa(os.Getpid())+".jsonl")}
}

func (w *helperEventWriter) write(eventName string, data map[string]string) {
	if strings.TrimSpace(eventName) != eventName || strings.ContainsAny(eventName, " \t\r\n:") || eventName == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	ev := event{Time: time.Now(), ElapsedMS: time.Since(runStartedAt).Milliseconds(), Source: "helper", HelperID: helperIDFromPath(w.path), PID: os.Getpid(), Event: eventName, Data: data}
	encoded, err := json.Marshal(ev)
	if err != nil {
		return
	}
	line := append([]byte(":"+eventName+": "), encoded...)
	line = append(line, '\n')
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_, _ = f.Write(line)
	_ = f.Close()
}

func helperIDFromPath(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	idx := strings.LastIndex(base, "-")
	if idx <= 0 {
		return base
	}
	return base[:idx]
}

func readHelperStdin(writer *helperEventWriter, cmdCh chan<- string, ctrlCh chan<- byte) {
	reader := bufio.NewReader(os.Stdin)
	var buf []byte
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return
		}
		if b == 3 {
			writer.write("stdin_ctrl", map[string]string{"key": "ctrl_c", "raw_hex": "03"})
			ctrlCh <- b
			continue
		}
		if b == '\r' || b == '\n' {
			text := string(buf)
			writer.write("stdin", map[string]string{"text": text, "raw_hex": hex.EncodeToString(append(buf, b))})
			cmdCh <- text
			buf = nil
			continue
		}
		buf = append(buf, b)
	}
}

func burnCPU(percent int) {
	if percent > 100 {
		percent = 100
	}
	busy := time.Duration(percent) * time.Millisecond
	idle := 100*time.Millisecond - busy
	for {
		start := time.Now()
		for time.Since(start) < busy {
		}
		if idle > 0 {
			time.Sleep(idle)
		}
	}
}
