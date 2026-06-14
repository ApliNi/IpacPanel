//go:build !windows

package compat

import (
	"errors"
	"syscall"
)

func IsCrossDeviceRenameError(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}
