package process

import (
	"bytes"
	"testing"
)

func TestSanitizeTerminalStartupOutputRemovesDestructiveSequences(t *testing.T) {
	input := []byte("\x1bc\x1b[H\x1b[2J\x1b[3J\x1b[?1049hnew output\r\n")
	out, pending, changed, completed := sanitizeTerminalStartupOutput(input)
	if len(pending) != 0 {
		t.Fatalf("sanitize should not leave pending bytes, got %q", pending)
	}
	if !changed {
		t.Fatal("sanitize should report changed output")
	}
	if !completed {
		t.Fatal("sanitize should stop startup protection at the first ordinary byte")
	}
	if bytes.Contains(out, []byte("\x1bc")) || bytes.Contains(out, []byte("\x1b[2J")) || bytes.Contains(out, []byte("\x1b[3J")) || bytes.Contains(out, []byte("\x1b[?1049h")) {
		t.Fatalf("sanitize should remove destructive startup sequences, got %q", out)
	}
	if !bytes.Equal(out, []byte("new output\r\n")) {
		t.Fatalf("sanitize should preserve normal output, got %q", out)
	}
}

func TestSanitizeTerminalStartupOutputStopsAtFirstOrdinaryByte(t *testing.T) {
	out, pending, changed, completed := sanitizeTerminalStartupOutput([]byte("ready\x1b[2J"))
	if len(pending) != 0 {
		t.Fatalf("ordinary output should not leave pending bytes, got %q", pending)
	}
	if changed {
		t.Fatal("sanitize should not filter sequences after first ordinary byte")
	}
	if !completed {
		t.Fatal("ordinary byte should complete startup protection")
	}
	if !bytes.Equal(out, []byte("ready\x1b[2J")) {
		t.Fatalf("sanitize should preserve data after first ordinary byte, got %q", out)
	}
}

func TestSanitizeTerminalStartupOutputDoesNotPrefixBlankLine(t *testing.T) {
	sp := &InstanceProcess{TerminalStartupProtecting: true}
	out := sp.sanitizeTerminalStartupOutputLocked([]byte("\x1b[2Jfirst log\r\n"))
	if !bytes.Equal(out, []byte("first log\r\n")) {
		t.Fatalf("sanitize should not prefix a blank line before first output, got %q", out)
	}
	if sp.TerminalStartupProtecting {
		t.Fatal("first ordinary output should complete startup protection")
	}
}

func TestSanitizeTerminalStartupOutputKeepsSplitEscapePending(t *testing.T) {
	first, pending, changed, completed := sanitizeTerminalStartupOutput([]byte("\r\n\x1b["))
	if changed || completed {
		t.Fatal("incomplete escape sequence before ordinary output should remain in startup protection")
	}
	if !bytes.Equal(first, []byte("\r\n")) {
		t.Fatalf("sanitize should emit bytes before pending escape, got %q", first)
	}
	if !bytes.Equal(pending, []byte("\x1b[")) {
		t.Fatalf("sanitize should keep incomplete escape as pending, got %q", pending)
	}

	secondInput := append(append([]byte(nil), pending...), []byte("2Jafter")...)
	second, nextPending, changed, completed := sanitizeTerminalStartupOutput(secondInput)
	if !changed {
		t.Fatal("completed split clear sequence should be removed")
	}
	if !completed {
		t.Fatal("ordinary output after split clear sequence should complete startup protection")
	}
	if len(nextPending) != 0 {
		t.Fatalf("completed sequence should not leave pending bytes, got %q", nextPending)
	}
	if !bytes.Equal(second, []byte("after")) {
		t.Fatalf("sanitize should preserve output after split escape, got %q", second)
	}
}
