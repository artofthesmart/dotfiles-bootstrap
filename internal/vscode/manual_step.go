package vscode

import (
	"context"
	"fmt"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
)

const (
	ManualHandoffStepID = "vscode.complete-manual-handoff"
	FactVSCodeUIReady   = "vscode.ui-handoff-complete"
)

type ManualHandoffStep struct {
	Request   Request
	Completed bool
}

func LifecycleSteps(manager *Manager, request Request, manualCompleted bool) []state.Step {
	return []state.Step{
		&ConfigureStep{Manager: manager, Request: request},
		&ManualHandoffStep{Request: request, Completed: manualCompleted},
	}
}

func (s *ManualHandoffStep) Contract() state.StepContract {
	return state.StepContract{
		ID:                  ManualHandoffStepID,
		Owner:               state.OwnerVSCode,
		Action:              "complete_protected_vscode_ui_actions",
		Risk:                state.RiskReadOnly,
		PrerequisiteStepIDs: []string{ConfigureStepID},
		PrerequisiteFactIDs: []string{FactVSCodeReady, FactVSCodeManualTasks},
		ProducedFactIDs:     []string{FactVSCodeUIReady},
		ConsentRequired:     true,
		Reversible:          false,
		NotReversibleReason: "Workspace trust, model selection, tool configuration, and approvals are protected user decisions.",
	}
}

func (s *ManualHandoffStep) ManualActions() []state.ManualAction {
	actions := manualActions(s.Request.Alias)
	result := make([]state.ManualAction, 0, len(actions))
	for _, action := range actions {
		result = append(result, state.ManualAction{
			ActionID:  action.ActionID,
			IssueCode: state.IssueCode(action.IssueCode),
			Surface:   state.ManualSurface("vscode"),
			Summary:   action.Summary,
			Completed: s.Completed,
		})
	}
	return result
}

func (s *ManualHandoffStep) Check(context.Context, state.StepContext) (state.CheckResult, error) {
	return state.CheckResult{
		AlreadySatisfied: s.Completed,
		Summary:          "Checked whether the protected VS Code UI handoff was completed by the user.",
		Evidence:         []state.Evidence{},
		Facts: map[string]state.SafeScalar{
			FactVSCodeUIReady: s.Completed,
		},
	}, nil
}

func (s *ManualHandoffStep) Plan(context.Context, state.StepContext) (state.PlanResult, error) {
	return state.PlanResult{
		Summary:  "Pause for workspace trust, reload, exact model selection, tool configuration, and approval-safe verification in VS Code.",
		Evidence: []state.Evidence{},
	}, nil
}

func (s *ManualHandoffStep) Apply(context.Context, state.StepContext) (state.ApplyResult, error) {
	if !s.Completed {
		return state.ApplyResult{}, stepFailure(app.IssueManualApproval, "Protected VS Code UI actions must be completed manually.", fmt.Errorf("manual VS Code handoff is incomplete"))
	}
	return state.ApplyResult{
		Summary:          "Recorded the user's completion of protected VS Code UI actions.",
		Evidence:         []state.Evidence{},
		MutationAssetIDs: []string{},
	}, nil
}

func (s *ManualHandoffStep) Verify(context.Context, state.StepContext) (state.VerifyResult, error) {
	if !s.Completed {
		return state.VerifyResult{}, stepFailure(app.IssueManualApproval, "Protected VS Code UI actions have not been attested as complete.", fmt.Errorf("manual VS Code handoff is incomplete"))
	}
	return state.VerifyResult{
		Summary:  "The user attested that protected VS Code UI actions are complete.",
		Evidence: []state.Evidence{},
		Facts:    map[string]state.SafeScalar{FactVSCodeUIReady: true},
		Assets:   []state.ManagedAsset{},
	}, nil
}

func (s *ManualHandoffStep) Undo(context.Context, state.StepContext) (state.UndoResult, error) {
	return state.UndoResult{
		Summary:  "Protected VS Code UI decisions are never undone automatically.",
		Evidence: []state.Evidence{},
	}, nil
}
