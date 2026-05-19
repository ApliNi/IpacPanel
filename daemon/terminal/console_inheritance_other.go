//go:build !windows

package terminal

import (
	"os/exec"
)

// PreventConsoleInheritance 在非 Windows 平台不需要额外处理.
func PreventConsoleInheritance(cmd *exec.Cmd) {
	// no-op
}
