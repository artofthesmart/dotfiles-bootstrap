package ollama

import (
	"context"
	"errors"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
)

const (
	IssueServerUnavailable = state.IssueCode("LAI-RUNTIME-001")
	IssueToolsFailed       = state.IssueCode("LAI-RUNTIME-002")
	IssueContextFailed     = state.IssueCode("LAI-RUNTIME-003")
	IssueOffloadUnknown    = state.IssueCode("LAI-RUNTIME-004")
	IssueRestartRequired   = state.IssueCode("LAI-RUNTIME-005")
	IssueModelIncompatible = state.IssueCode("LAI-MODEL-001")
	IssueModelContext      = state.IssueCode("LAI-MODEL-002")
	IssueModelRemote       = state.IssueCode("LAI-MODEL-003")
	IssueModelTools        = state.IssueCode("LAI-MODEL-004")
	IssueModelAcquire      = state.IssueCode("LAI-MODEL-005")
	IssueModelAlias        = state.IssueCode("LAI-MODEL-006")
	IssueCapacity          = state.IssueCode("LAI-CAPACITY-001")
	IssueCapacityUnknown   = state.IssueCode("LAI-CAPACITY-003")
	IssueOwnership         = state.IssueCode("LAI-ASSET-001")
)

type Diagnosis struct {
	ServerReady  bool
	ModelLocal   bool
	ToolsValid   bool
	ContextValid bool
	OffloadKnown bool
	Verification *RuntimeVerification
	Issues       []state.Issue
}

func (manager Manager) Diagnose(ctx context.Context, alias string) Diagnosis {
	diagnosis := Diagnosis{Issues: []state.Issue{}}
	if manager.API == nil {
		diagnosis.Issues = append(diagnosis.Issues, runtimeIssue(
			IssueServerUnavailable,
			state.SeverityError,
			"Ollama API is not configured.",
			"repair.ollama.start",
			"Configure or start the local Ollama API and retry.",
			false,
		))
		return diagnosis
	}
	if _, err := manager.API.Version(ctx); err != nil {
		diagnosis.Issues = append(diagnosis.Issues, runtimeIssue(
			IssueServerUnavailable,
			state.SeverityError,
			"Ollama API is unavailable or returned an invalid version response.",
			"repair.ollama.start",
			"Start Ollama and retry the readiness check.",
			true,
		))
		return diagnosis
	}
	diagnosis.ServerReady = true

	if _, found, err := manager.findLocalModel(ctx, alias); err != nil || !found {
		diagnosis.Issues = append(diagnosis.Issues, modelIssue(
			IssueModelAlias,
			"The exact local alias is not installed.",
			"repair.ollama.alias",
			"Recreate the exact alias from its verified local source.",
		))
		return diagnosis
	}
	diagnosis.ModelLocal = true

	verification, err := manager.VerifyRuntime(ctx, alias)
	if err == nil {
		diagnosis.ToolsValid = verification.StructuredTool
		diagnosis.ContextValid = verification.ContextTokens >= RequiredContextTokens
		diagnosis.OffloadKnown = verification.OffloadPercent >= 0
		diagnosis.Verification = &verification
		if !diagnosis.OffloadKnown {
			diagnosis.Issues = append(diagnosis.Issues, offloadIssue())
		}
		return diagnosis
	}

	var operationError *Error
	if errors.As(err, &operationError) {
		switch operationError.Operation {
		case "verify structured tool call":
			diagnosis.Issues = append(diagnosis.Issues, runtimeIssue(
				IssueToolsFailed,
				state.SeverityError,
				"The model did not return the required structured tool call.",
				"repair.ollama.tools",
				"Recreate the alias or select a catalog model with verified tool support.",
				false,
			))
		case "verify allocated context":
			diagnosis.Issues = append(diagnosis.Issues, runtimeIssue(
				IssueContextFailed,
				state.SeverityError,
				"The exact alias has less than 65,536 allocated context tokens.",
				"repair.ollama.restart",
				"Restart Ollama with the required context configuration, then reload the exact alias.",
				false,
			))
		case "validate local model metadata":
			code := IssueModelIncompatible
			if operationError.Err != nil {
				switch {
				case containsRemote(operationError.Err.Error()):
					code = IssueModelRemote
				case containsFold(operationError.Err.Error(), "context"):
					code = IssueModelContext
				}
			}
			diagnosis.Issues = append(diagnosis.Issues, modelIssue(
				code,
				"Local model metadata is incompatible with the private 65,536-token contract.",
				"repair.ollama.model",
				"Install an exact catalog model with verified local metadata.",
			))
		case "verify allocated runtime":
			diagnosis.Issues = append(diagnosis.Issues, runtimeIssue(
				IssueOffloadUnknown,
				state.SeverityWarning,
				"The exact alias is not loaded, so context and processor allocation cannot be verified.",
				"repair.ollama.reload",
				"Load the exact alias locally and repeat verification.",
				true,
			))
		default:
			diagnosis.Issues = append(diagnosis.Issues, runtimeIssue(
				IssueServerUnavailable,
				state.SeverityError,
				"Ollama runtime verification failed.",
				"repair.ollama.retry",
				"Retry the bounded local runtime checks.",
				true,
			))
		}
	} else {
		diagnosis.Issues = append(diagnosis.Issues, runtimeIssue(
			IssueServerUnavailable,
			state.SeverityError,
			"Ollama runtime verification failed.",
			"repair.ollama.retry",
			"Retry the bounded local runtime checks.",
			true,
		))
	}
	return diagnosis
}

type RepairAction struct {
	ID              string
	Summary         string
	Risk            state.Risk
	ConsentRequired bool
	SafeForYes      bool
}

type RepairPlan struct {
	Actions []RepairAction
}

func PlanRepair(diagnosis Diagnosis, existingServerRunning bool) RepairPlan {
	plan := RepairPlan{Actions: []RepairAction{}}
	seen := map[string]bool{}
	add := func(action RepairAction) {
		if !seen[action.ID] {
			seen[action.ID] = true
			plan.Actions = append(plan.Actions, action)
		}
	}
	for _, issue := range diagnosis.Issues {
		switch issue.Code {
		case IssueServerUnavailable:
			if existingServerRunning {
				add(RepairAction{
					ID:              "repair.ollama.restart",
					Summary:         "Restart the existing Ollama app or service and verify readiness.",
					Risk:            state.RiskExternalAppRestart,
					ConsentRequired: true,
					SafeForYes:      false,
				})
			} else {
				add(RepairAction{
					ID:         "repair.ollama.start",
					Summary:    "Start a manager-owned Ollama server with 65,536 default context.",
					Risk:       state.RiskUserLocal,
					SafeForYes: true,
				})
			}
		case IssueRestartRequired, IssueContextFailed:
			add(RepairAction{
				ID:              "repair.ollama.restart",
				Summary:         "Restart the existing Ollama app or service and verify the exact alias allocation.",
				Risk:            state.RiskExternalAppRestart,
				ConsentRequired: true,
				SafeForYes:      false,
			})
		case IssueModelAlias:
			add(RepairAction{
				ID:              "repair.ollama.alias",
				Summary:         "Recreate the exact local alias from verified local source metadata.",
				Risk:            state.RiskUserLocal,
				ConsentRequired: true,
				SafeForYes:      true,
			})
		case IssueToolsFailed, IssueModelIncompatible, IssueModelContext, IssueModelTools, IssueModelRemote:
			add(RepairAction{
				ID:              "repair.ollama.model",
				Summary:         "Replace or recreate the model using a compatible exact local source.",
				Risk:            state.RiskLargeDownload,
				ConsentRequired: true,
				SafeForYes:      false,
			})
		case IssueOffloadUnknown:
			add(RepairAction{
				ID:         "repair.ollama.reload",
				Summary:    "Load the exact alias locally and repeat processor and offload verification.",
				Risk:       state.RiskReadOnly,
				SafeForYes: true,
			})
		}
	}
	return plan
}

func runtimeIssue(code state.IssueCode, severity state.Severity, summary, repairID, remediation string, safe bool) state.Issue {
	component := "ollama"
	return state.Issue{
		Code:      code,
		Severity:  severity,
		Domain:    "runtime",
		Component: &component,
		Summary:   summary,
		RepairID:  &repairID,
		Remediations: []state.Remediation{{
			Kind:       "automatic",
			Summary:    remediation,
			SafeForYes: safe,
		}},
	}
}

func modelIssue(code state.IssueCode, summary, repairID, remediation string) state.Issue {
	component := "model"
	return state.Issue{
		Code:      code,
		Severity:  state.SeverityError,
		Domain:    "model",
		Component: &component,
		Summary:   summary,
		RepairID:  &repairID,
		Remediations: []state.Remediation{{
			Kind:       "automatic",
			Summary:    remediation,
			SafeForYes: false,
		}},
	}
}

func offloadIssue() state.Issue {
	return runtimeIssue(
		IssueOffloadUnknown,
		state.SeverityWarning,
		"Processor and offload allocation could not be determined.",
		"repair.ollama.reload",
		"Reload the exact alias and repeat /api/ps verification.",
		true,
	)
}

func containsRemote(value string) bool {
	return value == "remote" || value == "cloud" ||
		len(value) >= 6 && (containsFold(value, "remote") || containsFold(value, "cloud"))
}

func containsFold(value, target string) bool {
	for index := 0; index+len(target) <= len(value); index++ {
		if equalFoldASCII(value[index:index+len(target)], target) {
			return true
		}
	}
	return false
}

func equalFoldASCII(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		a, b := left[index], right[index]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}
