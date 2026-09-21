package api

import (
	web "IpacPanel/controller/src/web"

	cfg "IpacPanel/controller/src/config"

	"errors"
	"log"
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

	results := make([]cfg.MutationPostCommitResult, 0, len(result.Results))
	for _, r := range result.Results {
		if r.Error != "" {
			log.Printf("mutation post-commit step %q failed: %s", r.Name, r.Error)
			r.Error = batchFailureReason(errors.New(r.Error))
		}
		results = append(results, r)
	}

	web.WriteJSONStatus(w, statusCode, mutationErrorResponse{
		OK:      false,
		Message: message,
		Mutation: mutationResponseMeta{
			Committed:          result.Committed,
			RuntimeSynced:      result.RuntimeSynced,
			HasRequiredFailure: result.HasRequiredFailure,
			Results:            results,
		},
	}, "")
}
