//go:build windows

package terminal

import (
	"os/exec"
	"syscall"
)

const createNoWindow uint32 = 0x08000000

// PreventConsoleInheritance 阻止子进程继承父进程控制台.
// 这可以防止控制台程序通过 SetConsoleTitle, SetConsoleCP, SetConsoleMode 等 API 污染守护进程终端.
func PreventConsoleInheritance(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
