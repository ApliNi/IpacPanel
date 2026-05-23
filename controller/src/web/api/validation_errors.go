package api

import (
	"IpacPanel/controller/src/msg"
	web "IpacPanel/controller/src/web"
	"net/http"
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
	switch err.Error() {
	case msg.InputEncodingInvalid:
		web.WriteAPIError(w, http.StatusBadRequest, msg.InputEncodingInvalid, err)
	case msg.OutputEncodingInvalid:
		web.WriteAPIError(w, http.StatusBadRequest, msg.OutputEncodingInvalid, err)
	case msg.InstanceConfigRequired:
		web.WriteAPIError(w, http.StatusBadRequest, msg.InstanceConfigRequired, err)
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
