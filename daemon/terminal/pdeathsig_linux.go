//go:build linux

package terminal

import "syscall"

func setSysProcAttrPdeathsig(attr *syscall.SysProcAttr, sig syscall.Signal) {
	if attr != nil {
		attr.Pdeathsig = sig
	}
}
