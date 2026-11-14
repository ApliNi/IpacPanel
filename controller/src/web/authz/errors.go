package authz

// ErrorCode identifies authorization and authentication failure classes.
type ErrorCode string

const (
	ErrorCodeCSRFInvalid        ErrorCode = "csrf_invalid"
	ErrorCodeInstanceRequired   ErrorCode = "instance_required"
	ErrorCodeInstanceNotFound   ErrorCode = "instance_not_found"
	ErrorCodeInvalidToken       ErrorCode = "invalid_token"
)

// Error is a typed package error carrying an API-friendly code and message.
type Error struct {
	Code    ErrorCode
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Code)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func newError(code ErrorCode, message string, err error) *Error {
	if message == "" {
		message = string(code)
	}
	return &Error{Code: code, Message: message, Err: err}
}
