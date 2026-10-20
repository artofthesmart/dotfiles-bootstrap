package vscode

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
)

const (
	ConfigureStepID       = "vscode.configure-local-ai"
	VSCodeApplicationID   = "asset-vscode.application"
	VSCodeProfileID       = "asset-vscode.profile.local-ai"
	VSCodeExtensionID     = "asset-vscode.extension.ollama"
	FactVSCodeReady       = "vscode.ready"
	FactVSCodeManualTasks = "vscode.manual-actions-pending"
)

type ConfigureStep struct {
	Manager *Manager
	Request Request

	profileWasReady   bool
	extensionWasReady bool
}

func (s *ConfigureStep) Contract() state.StepContract {
	return state.StepContract{
		ID:                  ConfigureStepID,
		Owner:               state.OwnerVSCode,
		Action:              "configure_local_ai_profile",
		Risk:                state.RiskUserLocal,
		PrerequisiteStepIDs: []string{},
		PrerequisiteFactIDs: []string{},
		ProducedFactIDs:     []string{FactVSCodeReady, FactVSCodeManualTasks},
		ConsentRequired:     false,
		Reversible:          false,
		NotReversibleReason: "The manager never deletes a VS Code profile or extension without an explicit supported removal workflow.",
	}
}

func (s *ConfigureStep) Check(ctx context.Context, _ state.StepContext) (state.CheckResult, error) {
	if s.Manager == nil {
		return state.CheckResult{}, stepFailure(app.IssueVSCodeIntegration, "The VS Code lifecycle manager is unavailable.", fmt.Errorf("nil VS Code manager"))
	}
	if err := validateRequest(s.Request); err != nil {
		return state.CheckResult{}, stepFailure(app.IssueConfigurationInvalid, err.Error(), err)
	}
	if err := s.Manager.validateRepository(s.Request); err != nil {
		return state.CheckResult{}, stepFailure(app.IssueConfigurationInvalid, err.Error(), err)
	}
	status, err := s.Manager.Status(ctx)
	if err != nil && !isProfileCLIError(err) {
		return state.CheckResult{}, stepFailure(issueCodeForError(err), "VS Code readiness could not be checked.", err)
	}
	s.profileWasReady = status.ProfileReady
	s.extensionWasReady = status.OllamaExtensionInstalled && status.OllamaExtensionCurrent
	ready := status.VersionSupported && status.ProfileReady && s.extensionWasReady
	return state.CheckResult{
		AlreadySatisfied: ready,
		Summary:          "Checked VS Code, Local AI profile, and official Ollama extension readiness.",
		Evidence:         evidenceForStatus(s.Manager.clock.Now(), status),
		Facts: map[string]state.SafeScalar{
			FactVSCodeReady:       ready,
			FactVSCodeManualTasks: true,
		},
	}, nil
}

func (s *ConfigureStep) Plan(context.Context, state.StepContext) (state.PlanResult, error) {
	return state.PlanResult{
		Summary:  "Create or reuse the Local AI profile through documented VS Code CLI options, then install or update only Ollama.ollama in that profile.",
		Evidence: []state.Evidence{},
	}, nil
}

func (s *ConfigureStep) Apply(ctx context.Context, _ state.StepContext) (state.ApplyResult, error) {
	result := s.Manager.Configure(ctx, s.Request)
	if result.Outcome == app.OutcomeCancelled {
		return state.ApplyResult{}, context.Canceled
	}
	if result.Outcome == app.OutcomeFailed {
		code := app.IssueVSCodeIntegration
		summary := result.Summary
		if len(result.Issues) > 0 {
			code = result.Issues[0].Code
			summary = result.Issues[0].Summary
		}
		return state.ApplyResult{}, stepFailure(code, summary, fmt.Errorf("%s", result.Summary))
	}
	mutations := []string{}
	if !s.profileWasReady {
		mutations = append(mutations, VSCodeProfileID)
	}
	if !s.extensionWasReady {
		mutations = append(mutations, VSCodeExtensionID)
	}
	return state.ApplyResult{
		Summary:          "Configured the Local AI profile and verified the official Ollama extension; protected UI actions remain manual.",
		Evidence:         evidenceForStatus(s.Manager.clock.Now(), result.Status),
		MutationAssetIDs: mutations,
	}, nil
}

func (s *ConfigureStep) Verify(ctx context.Context, _ state.StepContext) (state.VerifyResult, error) {
	status, err := s.Manager.Status(ctx)
	if err != nil {
		return state.VerifyResult{}, stepFailure(issueCodeForError(err), "VS Code verification failed.", err)
	}
	if issues := healthIssues(status); len(issues) > 0 {
		return state.VerifyResult{}, stepFailure(issues[0].Code, issues[0].Summary, fmt.Errorf("%s", issues[0].Summary))
	}
	now := s.Manager.clock.Now().UTC()
	evidence := evidenceForStatus(now, status)
	return state.VerifyResult{
		Summary:  "Verified VS Code, Local AI profile isolation, and the official Ollama extension.",
		Evidence: evidence,
		Facts: map[string]state.SafeScalar{
			FactVSCodeReady:       true,
			FactVSCodeManualTasks: true,
		},
		Assets: s.assets(evidence),
	}, nil
}

func (s *ConfigureStep) Undo(context.Context, state.StepContext) (state.UndoResult, error) {
	return state.UndoResult{
		Summary:  "No automatic undo was performed; profile deletion is UI-only and extension removal requires an explicit removal workflow.",
		Evidence: []state.Evidence{},
	}, nil
}

func (s *ConfigureStep) assets(evidence []state.Evidence) []state.ManagedAsset {
	stepID := ConfigureStepID
	explicitExtensionFlag := "--remove-vscode-extension"
	notReversibleProfile := "VS Code profile deletion is guided UI-only; undocumented profile storage is never edited."
	assets := []state.ManagedAsset{{
		AssetID:      VSCodeApplicationID,
		AssetType:    state.AssetVSCodeApplication,
		DisplayName:  "Visual Studio Code",
		Origin:       state.OriginDetectedExisting,
		Ownership:    state.OwnershipExternalDetected,
		Locator:      state.AssetLocator{Kind: state.LocatorApplicationID, Value: "visual-studio-code"},
		Dependencies: []string{},
		Uninstall: state.UninstallRule{
			Policy:     state.UninstallRetain,
			Reversible: true,
		},
		Verification: evidence,
	}}

	profile := state.ManagedAsset{
		AssetID:      VSCodeProfileID,
		AssetType:    state.AssetVSCodeProfile,
		DisplayName:  ProfileName,
		Origin:       state.OriginDetectedExisting,
		Ownership:    state.OwnershipExternalDetected,
		Locator:      state.AssetLocator{Kind: state.LocatorProfileName, Value: ProfileName},
		Dependencies: []string{VSCodeApplicationID},
		Uninstall: state.UninstallRule{
			Policy:              state.UninstallGuidedUIOnly,
			Reversible:          false,
			NotReversibleReason: &notReversibleProfile,
		},
		Verification: evidence,
	}
	if !s.profileWasReady {
		profile.Origin = state.OriginCreatedByManager
		profile.Ownership = state.OwnershipManagerOwned
		profile.CreatedOrAdoptedByStep = &stepID
	}
	assets = append(assets, profile)

	extension := state.ManagedAsset{
		AssetID:      VSCodeExtensionID,
		AssetType:    state.AssetVSCodeExtension,
		DisplayName:  OfficialOllamaExtensionID,
		Origin:       state.OriginDetectedExisting,
		Ownership:    state.OwnershipExternalDetected,
		Locator:      state.AssetLocator{Kind: state.LocatorExtensionID, Value: OfficialOllamaExtensionID},
		Dependencies: []string{VSCodeProfileID},
		Uninstall: state.UninstallRule{
			Policy:       state.UninstallExplicitFlag,
			ExplicitFlag: &explicitExtensionFlag,
			Reversible:   true,
		},
		Verification: evidence,
	}
	if !s.extensionWasReady {
		extension.Origin = state.OriginInstalledByManager
		extension.Ownership = state.OwnershipManagerOwned
		extension.CreatedOrAdoptedByStep = &stepID
	}
	assets = append(assets, extension)
	return assets
}

func evidenceForStatus(observedAt time.Time, status Status) []state.Evidence {
	observedAt = observedAt.UTC()
	extensionVersion := status.OllamaExtensionVersion
	if extensionVersion == "" {
		extensionVersion = "not-installed"
	}
	return []state.Evidence{
		{
			Kind:       state.EvidenceApplicationState,
			ObservedAt: observedAt,
			Attributes: state.SafeAttributes{
				"application_id": "visual-studio-code",
				"present":        status.Executable != "",
				"verified":       status.VersionSupported,
				"version":        status.Version,
			},
		},
		{
			Kind:       state.EvidenceProfileState,
			ObservedAt: observedAt,
			Attributes: state.SafeAttributes{
				"profile_name": ProfileName,
				"ready":        status.ProfileReady,
			},
		},
		{
			Kind:       state.EvidenceExtensionState,
			ObservedAt: observedAt,
			Attributes: state.SafeAttributes{
				"extension_id": OfficialOllamaExtensionID,
				"present":      status.OllamaExtensionInstalled,
				"verified":     status.OllamaExtensionCurrent,
				"version":      extensionVersion,
			},
		},
		{
			Kind:       state.EvidenceExtensionState,
			ObservedAt: observedAt,
			Attributes: state.SafeAttributes{
				"extension_id": BundledCopilotChatExtensionID,
				"present":      status.CopilotChatAvailable,
				"verified":     status.CopilotChatAvailable,
				"state":        status.CopilotChatSource,
			},
		},
	}
}

func stepFailure(code app.IssueCode, summary string, err error) error {
	return &state.StepError{
		Issue: state.Issue{
			Code:         state.IssueCode(code),
			Severity:     state.SeverityError,
			Domain:       state.IssueDomain("vscode"),
			Summary:      summary,
			Remediations: []state.Remediation{{Kind: state.RemediationKind("command"), Summary: "Run local-ai doctor, review redacted diagnostics, and retry repair.", SafeForYes: true}},
		},
		Err: err,
	}
}

func issueCodeForError(err error) app.IssueCode {
	switch {
	case errors.Is(err, ErrVSCodeTooOld):
		return app.IssuePrerequisiteVersion
	case errors.Is(err, ErrInvalidAlias), errors.Is(err, ErrRepositoryUnavailable):
		return app.IssueConfigurationInvalid
	default:
		return app.IssueVSCodeIntegration
	}
}
