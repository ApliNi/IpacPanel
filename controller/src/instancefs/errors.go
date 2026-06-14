package instancefs

import (
	"IpacPanel/controller/src/msg"
	"errors"
)

type PathAccessErrorKind string

const (
	PathAccessErrorResolve    PathAccessErrorKind = "resolve"
	PathAccessErrorRequired   PathAccessErrorKind = "required"
	PathAccessErrorStat       PathAccessErrorKind = "stat"
	PathAccessErrorWithinRoot PathAccessErrorKind = "within_root"
	PathAccessErrorDirectory  PathAccessErrorKind = "directory"
)

type PathAccessError struct {
	Kind PathAccessErrorKind
	Err  error
}

var (
	ErrPathOutsideInstanceRoot  = errors.New(msg.PathOutsideInstanceRoot)
	ErrArchiveInvalidPath       = errors.New(msg.ArchiveContainsInvalidPath)
	ErrExtractTargetInvalidPath = errors.New(msg.FilePathInvalid)
	ErrUploadTargetIsDirectory  = errors.New(msg.UploadTargetIsDirectory)
)

func (e *PathAccessError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *PathAccessError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
