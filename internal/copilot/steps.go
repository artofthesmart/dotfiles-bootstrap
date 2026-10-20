package copilot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
)

const (
	StepInstallOfficial = "copilot.install"
	StepCreateLauncher  = "copilot.launcher"

	FactCopilotReady  = "copilot.ready"
	FactLauncherReady = "copilot.launcher-ready"

	CopilotApplicationAssetID = "asset-copilot.application"
	CopilotLauncherAssetID    = "asset-copilot.launcher"
)

type configureLifecycle struct {
	manager              *Manager
	request              Request
	copilotCreated       bool
	copilotInstallation  Installation
	launcherCreated      bool
	launcherInstallation LauncherInstallation
}

func NewConfigureSteps(manager *Manager, request Request) []state.Step {
	lifecycle := &configureLifecycle{manager: manager, request: request}
	return []state.Step{
		&installStep{lifecycle: lifecycle},
		&launcherStep{lifecycle: lifecycle},
	}
}

type installStep struct {
	lifecycle *configureLifecycle
}

func (step *installStep) Contract() state.StepContract {
	return state.StepContract{
		ID:              StepInstallOfficial,
		Owner:           state.OwnerCopilot,
		Action:          "Install or reuse the official GitHub Copilot CLI.",
		Risk:            state.RiskLargeDownload,
		ProducedFactIDs: []string{FactCopilotReady},
		ConsentRequired: true,
		Reversible:      true,
	}
}

func (step *installStep) Check(ctx context.Context, stepContext state.StepContext) (state.CheckResult, error) {
	if step.lifecycle == nil || step.lifecycle.manager == nil {
		return state.CheckResult{}, stepFailure(app.IssueInternal, "The Copilot lifecycle manager is unavailable.", errors.New("nil Copilot manager"))
	}
	inspected, err := step.lifecycle.manager.inspect(ctx, step.lifecycle.request)
	if err != nil {
		var lifecycleError *Error
		if !errors.As(err, &lifecycleError) ||
			(lifecycleError.Code != app.IssueCopilotMissing && lifecycleError.Code != app.IssueCopilotVersion && lifecycleError.Code != app.IssuePrerequisiteUnusable) {
			return state.CheckResult{}, stepFailureForError(err)
		}
	}
	satisfied := inspected.CopilotPresent && inspected.VersionSupported
	recovered := []string{}
	if satisfied && inspected.markerPresent && inspected.marker.TransactionID == stepContext.TransactionID &&
		inspected.marker.CopilotManaged && inspected.marker.CopilotDigest != "" {
		step.lifecycle.copilotCreated = true
		recovered = append(recovered, CopilotApplicationAssetID)
	}
	return state.CheckResult{
		AlreadySatisfied:          satisfied,
		RecoveredMutationAssetIDs: recovered,
		Summary:                   "Checked official Copilot CLI discovery, provenance ownership, and minimum version.",
		Evidence:                  copilotEvidence(step.lifecycle.manager.now(), inspected.Status),
		Facts:                     map[string]state.SafeScalar{FactCopilotReady: satisfied},
	}, nil
}

func (step *installStep) Plan(context.Context, state.StepContext) (state.PlanResult, error) {
	return state.PlanResult{Summary: "Reuse a supported official executable or download the matching stable github/copilot-cli asset and verify both GitHub API and SHA256SUMS.txt digests."}, nil
}

func (step *installStep) Apply(ctx context.Context, stepContext state.StepContext) (state.ApplyResult, error) {
	installation, err := step.lifecycle.manager.InstallOfficial(ctx, step.lifecycle.request, stepContext.TransactionID)
	if err != nil {
		return state.ApplyResult{}, stepFailureForError(err)
	}
	step.lifecycle.copilotInstallation = installation
	step.lifecycle.copilotCreated = installation.Created
	mutations := []string{}
	if installation.Created {
		mutations = append(mutations, CopilotApplicationAssetID)
	}
	return state.ApplyResult{
		Summary:          "The official GitHub Copilot CLI install or reuse operation completed.",
		MutationAssetIDs: mutations,
	}, nil
}

func (step *installStep) Verify(ctx context.Context, _ state.StepContext) (state.VerifyResult, error) {
	inspected, err := step.lifecycle.manager.inspect(ctx, step.lifecycle.request)
	if err != nil {
		return state.VerifyResult{}, stepFailureForError(err)
	}
	if !inspected.CopilotPresent || !inspected.VersionSupported {
		return state.VerifyResult{}, stepFailure(app.IssueCopilotVersion, "The official Copilot CLI minimum version was not verified.", errors.New("Copilot version verification failed"))
	}
	evidence := copilotEvidence(step.lifecycle.manager.now(), inspected.Status)
	asset := state.ManagedAsset{
		AssetID:      CopilotApplicationAssetID,
		AssetType:    state.AssetCopilotApplication,
		DisplayName:  "GitHub Copilot CLI",
		Origin:       state.OriginDetectedExisting,
		Ownership:    state.OwnershipExternalDetected,
		Locator:      state.AssetLocator{Kind: state.LocatorApplicationID, Value: "github-copilot-cli"},
		Dependencies: []string{},
		Uninstall:    state.UninstallRule{Policy: state.UninstallRetain, Reversible: true},
		Verification: evidence,
	}
	assets := []state.ManagedAsset{asset}
	if step.lifecycle.copilotCreated {
		stepID := StepInstallOfficial
		asset.Origin = state.OriginInstalledByManager
		asset.Ownership = state.OwnershipManagerOwned
		asset.Locator = state.AssetLocator{Kind: state.LocatorPath, Value: inspected.ExecutableLocator}
		asset.CreatedOrAdoptedByStep = &stepID
		asset.Uninstall = state.UninstallRule{Policy: state.UninstallRemoveWithManager, Reversible: true}
		assets[0] = asset
	}
	return state.VerifyResult{
		Summary:  "Verified a supported official GitHub Copilot CLI executable.",
		Evidence: evidence,
		Facts:    map[string]state.SafeScalar{FactCopilotReady: true},
		Assets:   assets,
	}, nil
}

func (step *installStep) Undo(ctx context.Context, _ state.StepContext) (state.UndoResult, error) {
	if !step.lifecycle.copilotCreated {
		return state.UndoResult{Summary: "The externally detected GitHub Copilot CLI was retained."}, nil
	}
	asset := state.ManagedAsset{
		AssetType: state.AssetCopilotApplication,
		Ownership: state.OwnershipManagerOwned,
		Locator: state.AssetLocator{
			Kind:  state.LocatorPath,
			Value: privacySafePath(step.lifecycle.managerPathsHome(), step.lifecycle.copilotInstallation.Path),
		},
	}
	if err := step.lifecycle.manager.Uninstall(ctx, []state.ManagedAsset{asset}); err != nil {
		return state.UndoResult{}, err
	}
	return state.UndoResult{Summary: "Removed the manager-installed GitHub Copilot CLI."}, nil
}

type launcherStep struct {
	lifecycle *configureLifecycle
}

func (step *launcherStep) Contract() state.StepContract {
	return state.StepContract{
		ID:                  StepCreateLauncher,
		Owner:               state.OwnerCopilot,
		Action:              "Create the manager-owned offline Ollama Copilot launcher.",
		Risk:                state.RiskUserLocal,
		PrerequisiteFactIDs: []string{FactCopilotReady},
		ProducedFactIDs:     []string{FactLauncherReady},
		Reversible:          true,
	}
}

func (step *launcherStep) Check(ctx context.Context, stepContext state.StepContext) (state.CheckResult, error) {
	inspected, err := step.lifecycle.manager.inspect(ctx, step.lifecycle.request)
	if err != nil {
		return state.CheckResult{}, stepFailureForError(err)
	}
	if !inspected.OllamaReady {
		return state.CheckResult{}, stepFailure(app.IssueCopilotOllama, "Ollama is unavailable.", errors.New("Ollama readiness check failed"))
	}
	if !inspected.AliasReady {
		return state.CheckResult{}, stepFailure(app.IssueCopilotAlias, "The exact selected local alias is unavailable.", errors.New("selected alias is missing"))
	}
	owned := inspected.LauncherOwnership == state.OwnershipManagerOwned
	satisfied := inspected.LauncherPresent && inspected.LauncherValid && owned
	recovered := []string{}
	if satisfied && inspected.markerPresent && inspected.marker.TransactionID == stepContext.TransactionID &&
		inspected.marker.LauncherManaged && inspected.marker.LauncherDigest != "" {
		step.lifecycle.launcherCreated = true
		recovered = append(recovered, CopilotLauncherAssetID)
	}
	return state.CheckResult{
		AlreadySatisfied:          satisfied,
		RecoveredMutationAssetIDs: recovered,
		Summary:                   "Checked Ollama, the exact alias, and the offline Copilot launcher.",
		Evidence:                  launcherEvidence(step.lifecycle.manager.now(), inspected.Status),
		Facts:                     map[string]state.SafeScalar{},
	}, nil
}

func (step *launcherStep) Plan(context.Context, state.StepContext) (state.PlanResult, error) {
	return state.PlanResult{Summary: "Atomically write copilot-local with loopback /v1, openai, responses, the exact alias, offline mode, telemetry disabled, and unchanged argument forwarding."}, nil
}

func (step *launcherStep) Apply(ctx context.Context, stepContext state.StepContext) (state.ApplyResult, error) {
	installation, err := step.lifecycle.manager.ConfigureLauncher(ctx, step.lifecycle.request, stepContext.TransactionID)
	if err != nil {
		return state.ApplyResult{}, stepFailureForError(err)
	}
	step.lifecycle.launcherInstallation = installation
	step.lifecycle.launcherCreated = installation.Created
	mutations := []string{}
	if installation.Created {
		mutations = append(mutations, CopilotLauncherAssetID)
	}
	return state.ApplyResult{
		Summary:          "The offline Copilot launcher configuration completed.",
		MutationAssetIDs: mutations,
	}, nil
}

func (step *launcherStep) Verify(ctx context.Context, _ state.StepContext) (state.VerifyResult, error) {
	inspected, err := step.lifecycle.manager.inspect(ctx, step.lifecycle.request)
	if err != nil {
		return state.VerifyResult{}, stepFailureForError(err)
	}
	if !inspected.LauncherPresent || !inspected.LauncherValid || !inspected.OfflineEnforced ||
		inspected.ProviderType != "openai" || inspected.WireAPI != "responses" {
		return state.VerifyResult{}, stepFailure(app.IssueCopilotLauncher, "The offline Copilot launcher contract was not verified.", errors.New("launcher verification failed"))
	}
	evidence := launcherEvidence(step.lifecycle.manager.now(), inspected.Status)
	assets := []state.ManagedAsset{}
	if step.lifecycle.launcherCreated {
		stepID := StepCreateLauncher
		assets = append(assets, state.ManagedAsset{
			AssetID:                CopilotLauncherAssetID,
			AssetType:              state.AssetCopilotLauncher,
			DisplayName:            LauncherName,
			Origin:                 state.OriginCreatedByManager,
			Ownership:              state.OwnershipManagerOwned,
			Locator:                state.AssetLocator{Kind: state.LocatorPath, Value: inspected.LauncherLocator},
			CreatedOrAdoptedByStep: &stepID,
			Dependencies:           []string{CopilotApplicationAssetID},
			Uninstall:              state.UninstallRule{Policy: state.UninstallRemoveWithManager, Reversible: true},
			Verification:           evidence,
		})
	}
	return state.VerifyResult{
		Summary:  "Verified the exact offline Ollama provider and copilot-local launcher.",
		Evidence: evidence,
		Facts:    map[string]state.SafeScalar{FactLauncherReady: true},
		Assets:   assets,
	}, nil
}

func (step *launcherStep) Undo(ctx context.Context, _ state.StepContext) (state.UndoResult, error) {
	if !step.lifecycle.launcherCreated {
		return state.UndoResult{Summary: "The pre-existing verified launcher was retained."}, nil
	}
	if step.lifecycle.launcherInstallation.PreviousBackup != "" {
		asset := state.ManagedAsset{
			AssetType: state.AssetCopilotLauncher,
			Ownership: state.OwnershipManagerOwned,
			Locator: state.AssetLocator{
				Kind:  state.LocatorPath,
				Value: privacySafePath(step.lifecycle.managerPathsHome(), step.lifecycle.launcherInstallation.Path),
			},
		}
		if err := step.lifecycle.manager.RollbackLauncher(ctx, []state.ManagedAsset{asset}); err != nil {
			return state.UndoResult{}, err
		}
		return state.UndoResult{Summary: "Restored the previous verified Copilot launcher."}, nil
	}
	asset := state.ManagedAsset{
		AssetType: state.AssetCopilotLauncher,
		Ownership: state.OwnershipManagerOwned,
		Locator: state.AssetLocator{
			Kind:  state.LocatorPath,
			Value: privacySafePath(step.lifecycle.managerPathsHome(), step.lifecycle.launcherInstallation.Path),
		},
	}
	if err := step.lifecycle.manager.Uninstall(ctx, []state.ManagedAsset{asset}); err != nil {
		return state.UndoResult{}, err
	}
	return state.UndoResult{Summary: "Removed the manager-created Copilot launcher."}, nil
}

func (lifecycle *configureLifecycle) managerPathsHome() string {
	if lifecycle == nil || lifecycle.manager == nil || lifecycle.manager.platform.Paths == nil {
		return ""
	}
	paths, err := lifecycle.manager.platform.Paths.UserPaths()
	if err != nil {
		return ""
	}
	return paths.Home
}

func copilotEvidence(observedAt time.Time, status Status) []state.Evidence {
	version := status.Version
	if version == "" {
		version = "not-installed"
	}
	return []state.Evidence{{
		Kind:       state.EvidenceApplicationState,
		ObservedAt: observedAt.UTC(),
		Attributes: state.SafeAttributes{
			"application_id": "github-copilot-cli",
			"present":        status.CopilotPresent,
			"verified":       status.VersionSupported,
			"version":        version,
			"ownership":      string(status.Ownership),
		},
	}}
}

func launcherEvidence(observedAt time.Time, status Status) []state.Evidence {
	return []state.Evidence{{
		Kind:       state.EvidenceFileState,
		ObservedAt: observedAt.UTC(),
		Attributes: state.SafeAttributes{
			"asset_id":   CopilotLauncherAssetID,
			"file_state": launcherState(status),
			"model_name": status.Alias,
			"present":    status.LauncherPresent,
			"verified":   status.LauncherValid && status.OfflineEnforced,
		},
	}}
}

func launcherState(status Status) string {
	if status.LauncherValid && status.ProviderValid && status.OfflineEnforced &&
		status.ProviderType == "openai" && status.WireAPI == "responses" {
		return "offline_openai_responses"
	}
	if status.LauncherPresent {
		return "invalid"
	}
	return "missing"
}

func stepFailureForError(err error) error {
	code := app.IssueInternal
	var lifecycleError *Error
	if errors.As(err, &lifecycleError) {
		code = lifecycleError.Code
	}
	return stepFailure(code, "The Copilot lifecycle step failed.", err)
}

func stepFailure(code app.IssueCode, summary string, err error) error {
	component := "github-copilot-cli"
	return &state.StepError{
		Issue: state.Issue{
			Code:      state.IssueCode(code),
			Severity:  state.SeverityError,
			Domain:    state.IssueDomain("copilot"),
			Component: &component,
			Summary:   summary,
			Remediations: []state.Remediation{{
				Kind:       state.RemediationKind("automatic"),
				Summary:    "Run the documented Copilot diagnosis and repair workflow.",
				SafeForYes: code == app.IssueCopilotLauncher || code == app.IssueCopilotProvider,
			}},
		},
		Err: fmt.Errorf("%w", err),
	}
}
