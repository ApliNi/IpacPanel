package compat

import (
	"IpacPanel/controller/src/msg"
	"errors"
)

var (
	errEmptyPath       = errors.New(msg.CompatPathEmpty)
	errEmptyCommand    = errors.New(msg.CompatCommandEmpty)
	errInvalidEncoding = errors.New(msg.CompatTerminalEncodingInvalid)
)

func IsInvalidEncodingError(err error) bool {
	return errors.Is(err, errInvalidEncoding)
}
