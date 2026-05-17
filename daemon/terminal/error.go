package terminal

import "errors"

var (
	errEmptyCommand    = errors.New("command is empty")
	errInvalidEncoding = errors.New("terminal encoding is invalid")
)

func IsInvalidEncodingError(err error) bool {
	return errors.Is(err, errInvalidEncoding)
}
