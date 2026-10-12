//go:build !linux

package terminal

import "syscall"

func setSysProcAttrPdeathsig(attr *syscall.SysProcAttr, sig syscall.Signal) {
}
