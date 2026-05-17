package process

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestStopCommandMarksRuntimeCodeBeforeWritingStdin(t *testing.T) {
	source := readProcessSourceForStaticTest(t)
	stopBody := extractFunctionBodyForStaticTest(t, source, "func (sp *InstanceProcess) Stop(force bool)")
	if !strings.Contains(stopBody, "markDaemonInstanceRuntimeCode") {
		t.Fatal("Stop must call markDaemonInstanceRuntimeCode before writing StopCommand to daemon stdin")
	}
	assertStaticCallOrder(t, stopBody, "markDaemonInstanceRuntimeCode", "writeDaemonInstanceStdin", "Stop must mark manual stop runtime code before StopCommand stdin write")
}

func TestRequestRestartMarksRuntimeCodeBeforeWritingStdin(t *testing.T) {
	source := readProcessSourceForStaticTest(t)
	restartBody := extractFunctionBodyForStaticTest(t, source, "func (sp *InstanceProcess) requestRestartWithKillStop(mode restartRequestMode, useKillStop bool)")
	nonKillStopBranch := extractBranchForStaticTest(t, restartBody, `} else if strings.TrimSpace(stopCommand) != "" {`)
	if !strings.Contains(nonKillStopBranch, "RuntimeCodeRestarting") {
		t.Fatal("RequestRestart must mark restarting runtime code before writing StopCommand to daemon stdin")
	}
	assertStaticCallOrder(t, nonKillStopBranch, "RuntimeCodeRestarting", "writeDaemonInstanceStdin", "RequestRestart must mark restarting runtime code before StopCommand stdin write")
}

func TestRequestRestartMarksRuntimeCodeWhenAlreadyStopping(t *testing.T) {
	source := readProcessSourceForStaticTest(t)
	restartBody := extractFunctionBodyForStaticTest(t, source, "func (sp *InstanceProcess) requestRestartWithKillStop(mode restartRequestMode, useKillStop bool)")
	stoppingBranch := extractBranchForStaticTest(t, restartBody, "if sp.State == processStateStopping {")
	if !strings.Contains(stoppingBranch, "markDaemonInstanceRuntimeCode(instanceName, RuntimeCodeRestarting)") {
		t.Fatal("RequestRestart must mark daemon runtime as restarting when converting a stop into a restart")
	}
}

func TestStopCommandStillWritesStdinWhenRuntimeMarkerFails(t *testing.T) {
	source := readProcessSourceForStaticTest(t)
	if strings.Contains(source, "} else if err := writeDaemonInstanceStdin") {
		t.Fatal("StopCommand stdin write must not be skipped when runtime-code marker fails")
	}
}

func TestRuntimeCodeMarkerWrapperUsesDaemonIPC(t *testing.T) {
	source := readDaemonIPCSourceForStaticTest(t)
	if !strings.Contains(source, "func markDaemonInstanceRuntimeCode") {
		t.Fatal("expected markDaemonInstanceRuntimeCode wrapper in daemon_ipc.go")
	}

	re := regexp.MustCompile(`(?s)func markDaemonInstanceRuntimeCode\([^)]*\).*?daemonRequest\(daemonIPCRequest\{([^}]*)\}\)`)
	matches := re.FindStringSubmatch(source)
	if len(matches) != 2 {
		t.Fatal("markDaemonInstanceRuntimeCode must send a daemon IPC request")
	}
	req := matches[1]
	for _, want := range []string{"Instance: insName", "RuntimeCode: runtimeCode"} {
		if !strings.Contains(req, want) {
			t.Fatalf("markDaemonInstanceRuntimeCode IPC request missing %q", want)
		}
	}
	if !strings.Contains(req, `Type: "mark_runtime_code"`) && !strings.Contains(req, `Type: "set_runtime_code"`) {
		t.Fatal("markDaemonInstanceRuntimeCode IPC request must use an explicit runtime-code marking type")
	}
}

func readProcessSourceForStaticTest(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("process.go")
	if err != nil {
		t.Fatalf("read process.go: %v", err)
	}
	return string(data)
}

func readDaemonIPCSourceForStaticTest(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("daemon_ipc.go")
	if err != nil {
		t.Fatalf("read daemon_ipc.go: %v", err)
	}
	return string(data)
}

func extractFunctionBodyForStaticTest(t *testing.T, source string, signature string) string {
	t.Helper()
	start := strings.Index(source, signature)
	if start == -1 {
		t.Fatalf("function %q not found", signature)
	}
	braceStart := strings.Index(source[start:], "{")
	if braceStart == -1 {
		t.Fatalf("function %q has no body", signature)
	}
	bodyStart := start + braceStart
	depth := 0
	for i := bodyStart; i < len(source); i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[bodyStart : i+1]
			}
		}
	}
	t.Fatalf("function %q body is not closed", signature)
	return ""
}

func assertStaticCallOrder(t *testing.T, source string, before string, after string, message string) {
	t.Helper()
	beforeIndex := strings.Index(source, before)
	if beforeIndex == -1 {
		t.Fatalf("%s: missing %s", message, before)
	}
	afterIndex := strings.Index(source, after)
	if afterIndex == -1 {
		t.Fatalf("%s: missing %s", message, after)
	}
	if beforeIndex > afterIndex {
		t.Fatalf("%s: %s appears after %s", message, before, after)
	}
}

func extractBranchForStaticTest(t *testing.T, source string, branchStart string) string {
	t.Helper()
	start := strings.Index(source, branchStart)
	if start == -1 {
		t.Fatalf("branch %q not found", branchStart)
	}
	braceStart := strings.Index(source[start:], "{")
	if braceStart == -1 {
		t.Fatalf("branch %q has no body", branchStart)
	}
	bodyStart := start + braceStart
	depth := 0
	for i := bodyStart; i < len(source); i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[bodyStart : i+1]
			}
		}
	}
	t.Fatalf("branch %q body is not closed", branchStart)
	return ""
}
