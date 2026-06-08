package api

import (
	"IpacPanel/controller/src/msg"
	web "IpacPanel/controller/src/web"
	"net/http"
	"strings"
)

func writeFileNameValidationError(w http.ResponseWriter, err error) {
	switch err.Error() {
	case msg.FileNameRequired:
		web.WriteAPIError(w, http.StatusBadRequest, msg.FileNameRequired, err)
	case msg.FileNameTooLong:
		web.WriteAPIError(w, http.StatusBadRequest, msg.FileNameTooLong, err)
	case msg.FileNameInvalid:
		web.WriteAPIError(w, http.StatusBadRequest, msg.FileNameInvalid, err)
	case msg.FileNameInvalidChars:
		web.WriteAPIError(w, http.StatusBadRequest, msg.FileNameInvalidChars, err)
	default:
		web.WriteAPIError(w, http.StatusBadRequest, msg.FileNameInvalid, err)
	}
}

func writeUserNameValidationError(w http.ResponseWriter, err error) {
	switch err.Error() {
	case msg.UserNameTooLong:
		web.WriteAPIError(w, http.StatusBadRequest, msg.UserNameTooLong, err)
	default:
		web.WriteAPIError(w, http.StatusBadRequest, msg.UsernameInvalid, err)
	}
}

func writeUserPasswordValidationError(w http.ResponseWriter, err error) {
	switch err.Error() {
	case msg.InvalidPasswordLength:
		web.WriteAPIError(w, http.StatusBadRequest, msg.InvalidPasswordLength, err)
	default:
		web.WriteAPIError(w, http.StatusBadRequest, msg.InvalidPasswordLength, err)
	}
}

func writeInstanceConfigValidationError(w http.ResponseWriter, err error) {
	errText := err.Error()
	switch {
	case errText == msg.InstanceNameRequired:
		web.WriteAPIError(w, http.StatusBadRequest, msg.InstanceNameRequired, err)
	case errText == msg.InstanceNameTooLong:
		web.WriteAPIError(w, http.StatusBadRequest, msg.InstanceNameTooLong, err)
	case errText == msg.InstanceNameInvalidChars:
		web.WriteAPIError(w, http.StatusBadRequest, msg.InstanceNameInvalidChars, err)
	case errText == msg.GroupNameTooLong:
		web.WriteAPIError(w, http.StatusBadRequest, msg.GroupNameTooLong, err)
	case errText == msg.GroupNameInvalidChars:
		web.WriteAPIError(w, http.StatusBadRequest, msg.GroupNameInvalidChars, err)
	case errText == msg.PathTooLong:
		web.WriteAPIError(w, http.StatusBadRequest, msg.PathTooLong, err)
	case errText == msg.CommandTooLong:
		web.WriteAPIError(w, http.StatusBadRequest, msg.CommandTooLong, err)
	case errText == msg.NoTerminalCommandRequired:
		web.WriteAPIError(w, http.StatusBadRequest, msg.NoTerminalCommandRequired, err)
	case errText == msg.InvalidTerminalMode:
		web.WriteAPIError(w, http.StatusBadRequest, msg.InvalidTerminalMode, err)
	case errText == msg.StopCommandTooLong:
		web.WriteAPIError(w, http.StatusBadRequest, msg.StopCommandTooLong, err)
	case errText == msg.CleanupCommandTooLong:
		web.WriteAPIError(w, http.StatusBadRequest, msg.CleanupCommandTooLong, err)
	case errText == msg.AccessLinksTooLong:
		web.WriteAPIError(w, http.StatusBadRequest, msg.AccessLinksTooLong, err)
	case errText == msg.StartPriorityInvalid:
		web.WriteAPIError(w, http.StatusBadRequest, msg.StartPriorityInvalid, err)
	case errText == msg.RestartIntervalInvalid:
		web.WriteAPIError(w, http.StatusBadRequest, msg.RestartIntervalInvalid, err)
	case errText == msg.InputEncodingInvalid:
		web.WriteAPIError(w, http.StatusBadRequest, msg.InputEncodingInvalid, err)
	case errText == msg.OutputEncodingInvalid:
		web.WriteAPIError(w, http.StatusBadRequest, msg.OutputEncodingInvalid, err)
	case errText == msg.InstanceConfigRequired:
		web.WriteAPIError(w, http.StatusBadRequest, msg.InstanceConfigRequired, err)
	case errText == msg.TaskNameRequired:
		web.WriteAPIError(w, http.StatusBadRequest, msg.TaskNameRequired, err)
	case errText == msg.TaskNameTooLong:
		web.WriteAPIError(w, http.StatusBadRequest, msg.TaskNameTooLong, err)
	case errText == msg.TaskNameNotUnique:
		web.WriteAPIError(w, http.StatusBadRequest, msg.TaskNameNotUnique, err)
	case strings.HasPrefix(errText, msg.TaskExprRequired+":"):
		web.WriteAPIError(w, http.StatusBadRequest, errText, err)
	case errText == msg.TaskExprRequired:
		web.WriteAPIError(w, http.StatusBadRequest, msg.TaskExprRequired, err)
	case strings.HasPrefix(errText, msg.TaskExprTooLong+":"):
		web.WriteAPIError(w, http.StatusBadRequest, errText, err)
	case errText == msg.TaskExprTooLong:
		web.WriteAPIError(w, http.StatusBadRequest, msg.TaskExprTooLong, err)
	case strings.HasPrefix(errText, msg.TaskExprInvalid+":"):
		web.WriteAPIError(w, http.StatusBadRequest, errText, err)
	case errText == msg.TaskExprInvalid:
		web.WriteAPIError(w, http.StatusBadRequest, msg.TaskExprInvalid, err)
	case errText == msg.TaskActionInvalid:
		web.WriteAPIError(w, http.StatusBadRequest, msg.TaskActionInvalid, err)
	case errText == msg.TaskCommandRequired:
		web.WriteAPIError(w, http.StatusBadRequest, msg.TaskCommandRequired, err)
	case errText == msg.TaskCommandTooLong:
		web.WriteAPIError(w, http.StatusBadRequest, msg.TaskCommandTooLong, err)
	default:
		web.WriteAPIError(w, http.StatusBadRequest, msg.InstanceConfigRequired, err)
	}
}

func writeControllerUpdatePackageNameValidationError(w http.ResponseWriter, err error) {
	switch err.Error() {
	case msg.ControllerUpdatePackageNameRequired:
		web.WriteAPIError(w, http.StatusBadRequest, msg.ControllerUpdatePackageNameRequired, err)
	case msg.ControllerUpdatePackageTypeInvalid:
		web.WriteAPIError(w, http.StatusBadRequest, msg.ControllerUpdatePackageTypeInvalid, err)
	case msg.ControllerUpdatePackageNameInvalid:
		web.WriteAPIError(w, http.StatusBadRequest, msg.ControllerUpdatePackageNameInvalid, err)
	default:
		web.WriteAPIError(w, http.StatusBadRequest, msg.ControllerUpdatePackageNameInvalid, err)
	}
}

func writeControllerUpdateSessionError(w http.ResponseWriter, err error) {
	switch err.Error() {
	case msg.UploadSessionNotFound:
		web.WriteAPIError(w, http.StatusNotFound, msg.UploadSessionNotFound, err)
	case msg.UploadSessionForbidden:
		web.WriteAPIError(w, http.StatusForbidden, msg.UploadSessionForbidden, err)
	default:
		web.WriteAPIError(w, http.StatusForbidden, msg.UploadSessionForbidden, err)
	}
}
