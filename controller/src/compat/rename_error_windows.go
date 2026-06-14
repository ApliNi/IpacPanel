//go:build windows

package compat

import (
	"errors"

	"golang.org/x/sys/windows"
)

func IsCrossDeviceRenameError(err error) bool {
	return errors.Is(err, windows.ERROR_NOT_SAME_DEVICE)
}
