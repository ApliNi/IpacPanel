package terminal

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPipeStartAndOutput(t *testing.T) {
	var argv []string
	if runtime.GOOS == "windows" {
		argv = []string{"cmd.exe", "/c", "echo out_marker & echo err_marker 1>&2"}
	} else {
		argv = []string{"/bin/sh", "-c", "echo out_marker && echo err_marker >&2"}
	}

	proxy, err := Start("", argv, false, "utf-8", "utf-8", 80, 24)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer proxy.Close()

	if proxy.PID() <= 0 {
		t.Errorf("expected positive PID, got %d", proxy.PID())
	}

	var buf bytes.Buffer
	readDone := make(chan error, 1)
	go func() {
		tmp := make([]byte, 1024)
		for {
			n, rerr := proxy.Read(tmp)
			if n > 0 {
				buf.Write(tmp[:n])
			}
			if rerr != nil {
				if errors.Is(rerr, io.EOF) || strings.Contains(rerr.Error(), "pipe") {
					readDone <- nil
				} else {
					readDone <- rerr
				}
				return
			}
		}
	}()

	waitErr := proxy.Wait()
	if waitErr != nil {
		t.Fatalf("proxy.Wait failed: %v", waitErr)
	}

	select {
	case rerr := <-readDone:
		if rerr != nil {
			t.Fatalf("proxy.Read error: %v", rerr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("proxy.Read timed out waiting for EOF")
	}

	out := buf.String()
	if !strings.Contains(out, "out_marker") {
		t.Errorf("expected output to contain out_marker, got: %q", out)
	}
	if !strings.Contains(out, "err_marker") {
		t.Errorf("expected output to contain err_marker, got: %q", out)
	}
}

func TestPipeCloseAndKill(t *testing.T) {
	var argv []string
	if runtime.GOOS == "windows" {
		argv = []string{"ping.exe", "-n", "30", "127.0.0.1"}
	} else {
		argv = []string{"sleep", "30"}
	}

	proxy, err := Start("", argv, false, "utf-8", "utf-8", 80, 24)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait briefly to ensure child process is alive.
	time.Sleep(50 * time.Millisecond)

	if err := proxy.Kill(); err != nil {
		t.Fatalf("proxy.Kill failed: %v", err)
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- proxy.Wait()
	}()

	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy.Wait timed out after Kill")
	}
}
