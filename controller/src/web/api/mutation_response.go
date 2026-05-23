package api

import (
	web "IpacPanel/controller/src/web"

	cfg "IpacPanel/controller/src/config"

	"net/http"
	"strings"
)

type mutationResponseMeta struct {
	Committed          bool                           `json:"committed"`
	RuntimeSynced      bool                           `json:"runtime_synced"`
	HasRequiredFailure bool                           `json:"has_required_failure,omitempty"`
	Results            []cfg.MutationPostCommitResult `json:"results,omitempty"`
}

type mutationErrorResponse struct {
	OK       bool                 `json:"ok"`
	Message  string               `json:"message,omitempty"`
	Mutation mutationResponseMeta `json:"mutation"`
}

func writeMutationRuntimeSyncError(w http.ResponseWriter, statusCode int, userMessage string, result cfg.MutationRunResult) {
	message := strings.TrimSpace(userMessage)
	web.MarkAPIError(w, statusCode, message, result.Error())
	web.WriteJSONStatus(w, statusCode, mutationErrorResponse{
		OK:      false,
		Message: message,
		Mutation: mutationResponseMeta{
			Committed:          result.Committed,
			RuntimeSynced:      result.RuntimeSynced,
			HasRequiredFailure: result.HasRequiredFailure,
			Results:            append([]cfg.MutationPostCommitResult(nil), result.Results...),
		},
	}, "")
}
