package process

import (
	"testing"
)

func TestValidateWindowsBatchCommandArgs(t *testing.T) {
	safeArgs := []string{"run.bat", "arg1", "param_2", "path/to/file"}
	if err := validateWindowsBatchCommandArgs(safeArgs); err != nil {
		t.Fatalf("expected safe args to pass, got: %v", err)
	}

	dangerous := [][]string{
		{"run.bat & calc.exe"},
		{"run.bat", "arg1 & whoami"},
		{"run.bat", "arg1|whoami"},
		{"run.bat", "arg1<input.txt"},
		{"run.bat", "arg1>output.txt"},
		{"run.bat", "arg1^name"},
		{"run.bat", "%PATH%"},
		{"run.bat", "!VAR!"},
		{"run.bat", "quote\"test"},
		{"run.bat", "new\nline"},
		{"run.bat", "new\rline"},
	}

	for _, args := range dangerous {
		if err := validateWindowsBatchCommandArgs(args); err == nil {
			t.Errorf("expected unsafe args %v to fail validation", args)
		}
	}
}
