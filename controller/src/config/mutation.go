package config

import (
	"IpacPanel/controller/src/msg"
	"errors"
	"fmt"
	"strings"
)

type MutationStepSeverity string

const (
	MutationStepRequired   MutationStepSeverity = "required"
	MutationStepBestEffort MutationStepSeverity = "best_effort"
)

type MutationPostCommitStep struct {
	Name     string
	Severity MutationStepSeverity
	Run      func() error
}

type MutationPostCommitResult struct {
	Name     string               `json:"name"`
	Severity MutationStepSeverity `json:"severity"`
	OK       bool                 `json:"ok"`
	Error    string               `json:"error,omitempty"`
}

type MutationRunResult struct {
	Committed          bool                       `json:"committed"`
	RuntimeSynced      bool                       `json:"runtime_synced"`
	HasRequiredFailure bool                       `json:"has_required_failure"`
	Results            []MutationPostCommitResult `json:"results,omitempty"`
}

type MutationPlan struct {
	NextCfg    Config
	Publish    func()
	PostCommit []MutationPostCommitStep
}

func (p *MutationPlan) AddPostCommit(name string, run func() error) {
	p.AddRequiredPostCommit(name, run)
}

func (p *MutationPlan) AddRequiredPostCommit(name string, run func() error) {
	p.addPostCommit(name, MutationStepRequired, run)
}

func (p *MutationPlan) addPostCommit(name string, severity MutationStepSeverity, run func() error) {
	if run == nil {
		return
	}
	if severity == "" {
		severity = MutationStepRequired
	}
	p.PostCommit = append(p.PostCommit, MutationPostCommitStep{Name: name, Severity: severity, Run: run})
}

func CommitMutationPlan(plan MutationPlan) error {
	if err := SaveConfigSnapshot(plan.NextCfg); err != nil {
		return err
	}
	if plan.Publish != nil {
		plan.Publish()
	}
	return nil
}

func RunMutationPostCommit(plan MutationPlan) MutationRunResult {
	result := MutationRunResult{Committed: true, RuntimeSynced: true}
	if len(plan.PostCommit) == 0 {
		return result
	}
	result.Results = make([]MutationPostCommitResult, 0, len(plan.PostCommit))
	for _, step := range plan.PostCommit {
		if step.Run == nil {
			continue
		}
		stepResult := MutationPostCommitResult{
			Name:     strings.TrimSpace(step.Name),
			Severity: step.Severity,
			OK:       true,
		}
		if stepResult.Severity == "" {
			stepResult.Severity = MutationStepRequired
		}
		if err := step.Run(); err != nil {
			stepResult.OK = false
			name := strings.TrimSpace(step.Name)
			if name == "" {
				stepResult.Error = err.Error()
			} else {
				stepResult.Error = fmt.Sprintf("%s: %v", name, err)
			}
			if stepResult.Severity == MutationStepRequired {
				result.HasRequiredFailure = true
			}
		}
		result.Results = append(result.Results, stepResult)
	}
	if result.HasRequiredFailure {
		result.RuntimeSynced = false
	}
	return result
}

func (r MutationRunResult) Error() error {
	if !r.HasRequiredFailure {
		return nil
	}
	errParts := make([]string, 0, len(r.Results))
	for _, item := range r.Results {
		if item.OK || item.Severity != MutationStepRequired {
			continue
		}
		message := strings.TrimSpace(item.Error)
		if message == "" {
			message = strings.TrimSpace(item.Name)
		}
		if message == "" {
			message = msg.RuntimeSyncFailed
		}
		errParts = append(errParts, message)
	}
	if len(errParts) == 0 {
		return errors.New(msg.RuntimeSyncFailed)
	}
	return errors.New(strings.Join(errParts, "; "))
}
