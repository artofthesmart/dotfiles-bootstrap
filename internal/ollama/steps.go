package ollama

import (
	"context"
	"errors"
	"fmt"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
)

const (
	StepAcquireModel = "models.acquire"
	StepCreateAlias  = "models.alias"
	StepVerifyModel  = "models.verify"
	StepRemoveAlias  = "models.remove-alias"
	StepRemoveSource = "models.remove-source"

	FactSourceReady = "models.source-ready"
	FactAliasReady  = "models.alias-ready"
	FactModelReady  = "models.runtime-verified"
)

type installLifecycle struct {
	manager       Manager
	request       InstallRequest
	sourceAssetID string
	aliasAssetID  string
	sourceCreated bool
	aliasCreated  bool
	inspection    SourceInspection
}

func NewInstallSteps(manager Manager, request InstallRequest) []state.Step {
	lifecycle := &installLifecycle{
		manager:       manager,
		request:       request,
		sourceAssetID: "asset-ollama-source-" + safeFileToken(sourceModelName(request)),
		aliasAssetID:  "asset-ollama-alias-" + safeFileToken(request.Alias),
	}
	return []state.Step{
		&acquireStep{lifecycle: lifecycle},
		&aliasStep{lifecycle: lifecycle},
		&verifyStep{lifecycle: lifecycle},
	}
}

type acquireStep struct {
	lifecycle *installLifecycle
}

func (step *acquireStep) Contract() state.StepContract {
	return state.StepContract{
		ID:              StepAcquireModel,
		Owner:           state.OwnerOllama,
		Action:          "Acquire the exact local model source.",
		Risk:            state.RiskLargeDownload,
		ProducedFactIDs: []string{FactSourceReady},
		ConsentRequired: true,
		Reversible:      true,
	}
}

func (step *acquireStep) Check(ctx context.Context, _ state.StepContext) (state.CheckResult, error) {
	inspection, err := step.lifecycle.manager.InspectSource(ctx, step.lifecycle.request)
	if err != nil {
		var operationError *Error
		if errors.As(err, &operationError) && operationError.Kind == ErrorCapacity {
			return state.CheckResult{}, stepFailure(err)
		}
		return state.CheckResult{}, stepFailure(err)
	}
	step.lifecycle.inspection = inspection
	if inspection.AlreadyLocal {
		step.lifecycle.sourceCreated = false
	}
	return state.CheckResult{
		AlreadySatisfied: inspection.AlreadyLocal,
		Summary:          sourceCheckSummary(inspection.AlreadyLocal),
		Facts:            map[string]state.SafeScalar{},
	}, nil
}

func (step *acquireStep) Plan(context.Context, state.StepContext) (state.PlanResult, error) {
	return state.PlanResult{Summary: "Acquire only the exact verified source after the required download consent."}, nil
}

func (step *acquireStep) Apply(ctx context.Context, _ state.StepContext) (state.ApplyResult, error) {
	inspection, created, err := step.lifecycle.manager.Acquire(ctx, step.lifecycle.request)
	if err != nil {
		return state.ApplyResult{}, stepFailure(err)
	}
	step.lifecycle.inspection = inspection
	step.lifecycle.sourceCreated = created
	mutations := []string{}
	if created {
		mutations = append(mutations, step.lifecycle.sourceAssetID)
	}
	return state.ApplyResult{
		Summary:          "The exact source model acquisition completed.",
		MutationAssetIDs: mutations,
	}, nil
}

func (step *acquireStep) Verify(ctx context.Context, _ state.StepContext) (state.VerifyResult, error) {
	modelName := sourceModelName(step.lifecycle.request)
	metadata, err := step.lifecycle.manager.API.Show(ctx, ShowRequest{Model: modelName})
	if err != nil {
		return state.VerifyResult{}, stepFailure(err)
	}
	contextTokens, err := validateLocalMetadata(metadata)
	if err != nil {
		return state.VerifyResult{}, stepFailure(err)
	}
	observed := step.lifecycle.manager.now()
	evidence := state.Evidence{
		Kind:       state.EvidenceModelMetadata,
		ObservedAt: observed,
		Attributes: state.SafeAttributes{
			"model_name":     modelName,
			"context_tokens": contextTokens,
			"verified":       true,
		},
	}
	ownership := state.OwnershipExternalDetected
	origin := state.OriginDetectedExisting
	var creatingStep *string
	if step.lifecycle.sourceCreated {
		ownership = state.OwnershipManagerOwned
		origin = state.OriginInstalledByManager
		value := StepAcquireModel
		creatingStep = &value
	}
	flag := "--remove-models"
	asset := state.ManagedAsset{
		AssetID:                step.lifecycle.sourceAssetID,
		AssetType:              state.AssetOllamaSourceModel,
		DisplayName:            "Ollama source model",
		Origin:                 origin,
		Ownership:              ownership,
		Locator:                state.AssetLocator{Kind: state.LocatorModelName, Value: modelName},
		CreatedOrAdoptedByStep: creatingStep,
		Dependencies:           []string{},
		Uninstall: state.UninstallRule{
			Policy:       state.UninstallExplicitFlag,
			ExplicitFlag: &flag,
			Reversible:   true,
		},
		Verification: []state.Evidence{evidence},
	}
	return state.VerifyResult{
		Summary:  "The exact source model is local and compatible.",
		Evidence: []state.Evidence{evidence},
		Facts:    map[string]state.SafeScalar{FactSourceReady: true},
		Assets:   []state.ManagedAsset{asset},
	}, nil
}

func (step *acquireStep) Undo(ctx context.Context, _ state.StepContext) (state.UndoResult, error) {
	if !step.lifecycle.sourceCreated {
		return state.UndoResult{Summary: "The pre-existing source model was retained."}, nil
	}
	if err := step.lifecycle.manager.run(ctx, "roll back source acquisition", "rm", sourceModelName(step.lifecycle.request)); err != nil {
		return state.UndoResult{}, err
	}
	return state.UndoResult{Summary: "The manager-created source model was removed."}, nil
}

type aliasStep struct {
	lifecycle *installLifecycle
}

func (step *aliasStep) Contract() state.StepContract {
	return state.StepContract{
		ID:                  StepCreateAlias,
		Owner:               state.OwnerOllama,
		Action:              "Create the exact 65,536-token local alias.",
		Risk:                state.RiskUserLocal,
		PrerequisiteStepIDs: []string{StepAcquireModel},
		PrerequisiteFactIDs: []string{FactSourceReady},
		ProducedFactIDs:     []string{FactAliasReady},
		ConsentRequired:     true,
		Reversible:          true,
	}
}

func (step *aliasStep) Check(ctx context.Context, _ state.StepContext) (state.CheckResult, error) {
	_, found, err := step.lifecycle.manager.findLocalModel(ctx, step.lifecycle.request.Alias)
	if err != nil {
		return state.CheckResult{}, stepFailure(err)
	}
	if found {
		step.lifecycle.aliasCreated = false
		metadata, err := step.lifecycle.manager.API.Show(ctx, ShowRequest{Model: step.lifecycle.request.Alias})
		if err != nil {
			return state.CheckResult{}, stepFailure(err)
		}
		if !aliasModelfileMatches(metadata.Modelfile, sourceModelName(step.lifecycle.request)) {
			return state.CheckResult{}, stepFailure(&Error{Kind: ErrorCompatibility, Operation: "reuse existing alias", Err: errors.New("existing alias does not match the exact source and context contract")})
		}
	}
	return state.CheckResult{
		AlreadySatisfied: found,
		Summary:          aliasCheckSummary(found),
		Facts:            map[string]state.SafeScalar{},
	}, nil
}

func (step *aliasStep) Plan(context.Context, state.StepContext) (state.PlanResult, error) {
	return state.PlanResult{Summary: "Create a safe exact alias with PARAMETER num_ctx 65536."}, nil
}

func (step *aliasStep) Apply(ctx context.Context, _ state.StepContext) (state.ApplyResult, error) {
	created, err := step.lifecycle.manager.CreateAlias(
		ctx,
		sourceModelName(step.lifecycle.request),
		step.lifecycle.request.Alias,
		step.lifecycle.request.DataDirectory,
	)
	if err != nil {
		return state.ApplyResult{}, stepFailure(err)
	}
	step.lifecycle.aliasCreated = created
	mutations := []string{}
	if created {
		mutations = append(mutations, step.lifecycle.aliasAssetID)
	}
	return state.ApplyResult{
		Summary:          "The exact local alias creation completed.",
		MutationAssetIDs: mutations,
	}, nil
}

func (step *aliasStep) Verify(ctx context.Context, _ state.StepContext) (state.VerifyResult, error) {
	metadata, err := step.lifecycle.manager.API.Show(ctx, ShowRequest{Model: step.lifecycle.request.Alias})
	if err != nil {
		return state.VerifyResult{}, stepFailure(err)
	}
	contextTokens, err := validateLocalMetadata(metadata)
	if err != nil {
		return state.VerifyResult{}, stepFailure(err)
	}
	if !aliasModelfileMatches(metadata.Modelfile, sourceModelName(step.lifecycle.request)) {
		return state.VerifyResult{}, stepFailure(&Error{Kind: ErrorCompatibility, Operation: "verify alias Modelfile", Err: errors.New("alias source or context parameter does not match the transaction")})
	}
	observed := step.lifecycle.manager.now()
	evidence := state.Evidence{
		Kind:       state.EvidenceContextAllocation,
		ObservedAt: observed,
		Attributes: state.SafeAttributes{
			"model_name":     step.lifecycle.request.Alias,
			"context_tokens": contextTokens,
			"verified":       true,
		},
	}
	ownership := state.OwnershipExternalDetected
	origin := state.OriginDetectedExisting
	var creatingStep *string
	if step.lifecycle.aliasCreated {
		ownership = state.OwnershipManagerOwned
		origin = state.OriginCreatedByManager
		value := StepCreateAlias
		creatingStep = &value
	}
	flag := "--remove-aliases"
	asset := state.ManagedAsset{
		AssetID:                step.lifecycle.aliasAssetID,
		AssetType:              state.AssetOllamaAlias,
		DisplayName:            "Local AI Ollama alias",
		Origin:                 origin,
		Ownership:              ownership,
		Locator:                state.AssetLocator{Kind: state.LocatorModelName, Value: step.lifecycle.request.Alias},
		CreatedOrAdoptedByStep: creatingStep,
		Dependencies:           []string{step.lifecycle.sourceAssetID},
		Uninstall: state.UninstallRule{
			Policy:       state.UninstallExplicitFlag,
			ExplicitFlag: &flag,
			Reversible:   true,
		},
		Verification: []state.Evidence{evidence},
	}
	return state.VerifyResult{
		Summary:  "The exact local alias has compatible model metadata.",
		Evidence: []state.Evidence{evidence},
		Facts:    map[string]state.SafeScalar{FactAliasReady: true},
		Assets:   []state.ManagedAsset{asset},
	}, nil
}

func (step *aliasStep) Undo(ctx context.Context, _ state.StepContext) (state.UndoResult, error) {
	if !step.lifecycle.aliasCreated {
		return state.UndoResult{Summary: "The pre-existing alias was retained."}, nil
	}
	if err := step.lifecycle.manager.run(ctx, "roll back alias creation", "rm", step.lifecycle.request.Alias); err != nil {
		return state.UndoResult{}, err
	}
	return state.UndoResult{Summary: "The manager-created alias was removed."}, nil
}

type verifyStep struct {
	lifecycle *installLifecycle
}

func (step *verifyStep) Contract() state.StepContract {
	return state.StepContract{
		ID:                  StepVerifyModel,
		Owner:               state.OwnerOllama,
		Action:              "Verify exact alias tools, context, processor, and offload.",
		Risk:                state.RiskReadOnly,
		PrerequisiteStepIDs: []string{StepCreateAlias},
		PrerequisiteFactIDs: []string{FactAliasReady},
		ProducedFactIDs:     []string{FactModelReady},
		Reversible:          false,
		NotReversibleReason: "Read-only runtime verification has no mutation to undo.",
	}
}

func (step *verifyStep) Check(context.Context, state.StepContext) (state.CheckResult, error) {
	return state.CheckResult{
		AlreadySatisfied: true,
		Summary:          "Runtime verification is always repeated from live API evidence.",
		Facts:            map[string]state.SafeScalar{},
	}, nil
}

func (step *verifyStep) Plan(context.Context, state.StepContext) (state.PlanResult, error) {
	return state.PlanResult{Summary: "Verify structured tools and /api/ps without executing any generated tool."}, nil
}

func (step *verifyStep) Apply(context.Context, state.StepContext) (state.ApplyResult, error) {
	return state.ApplyResult{Summary: "No mutation is required for runtime verification."}, nil
}

func (step *verifyStep) Verify(ctx context.Context, _ state.StepContext) (state.VerifyResult, error) {
	verification, err := step.lifecycle.manager.VerifyRuntime(ctx, step.lifecycle.request.Alias)
	if err != nil {
		return state.VerifyResult{}, stepFailure(err)
	}
	observed := step.lifecycle.manager.now()
	evidence := []state.Evidence{
		{
			Kind:       state.EvidenceToolCall,
			ObservedAt: observed,
			Attributes: state.SafeAttributes{
				"model_name": verification.Alias,
				"tool_name":  verification.ToolName,
				"structured": verification.StructuredTool,
				"verified":   true,
			},
		},
		{
			Kind:       state.EvidenceContextAllocation,
			ObservedAt: observed,
			Attributes: state.SafeAttributes{
				"model_name":     verification.Alias,
				"context_tokens": verification.ContextTokens,
				"verified":       true,
			},
		},
		{
			Kind:       state.EvidenceOffload,
			ObservedAt: observed,
			Attributes: state.SafeAttributes{
				"model_name":      verification.Alias,
				"offload_percent": verification.OffloadPercent,
				"state":           verification.Processor,
			},
		},
	}
	return state.VerifyResult{
		Summary:  "The exact alias passed structured tools and runtime allocation verification.",
		Evidence: evidence,
		Facts:    map[string]state.SafeScalar{FactModelReady: true},
		Assets:   []state.ManagedAsset{},
	}, nil
}

func (step *verifyStep) Undo(context.Context, state.StepContext) (state.UndoResult, error) {
	return state.UndoResult{Summary: "Read-only runtime verification requires no undo."}, nil
}

type removeStep struct {
	manager   Manager
	request   RemovalRequest
	source    bool
	assetID   string
	modelName string
}

func NewRemoveSteps(manager Manager, request RemovalRequest) []state.Step {
	steps := []state.Step{}
	if request.Alias != "" {
		steps = append(steps, &removeStep{
			manager: manager, request: request, assetID: request.AliasAssetID, modelName: request.Alias,
		})
	}
	if request.RemoveSource {
		steps = append(steps, &removeStep{
			manager: manager, request: request, source: true, assetID: request.SourceAssetID, modelName: request.Source,
		})
	}
	return steps
}

func (step *removeStep) Contract() state.StepContract {
	id := StepRemoveAlias
	action := "Remove the exact manager-owned alias."
	prerequisites := []string{}
	if step.source {
		id = StepRemoveSource
		action = "Remove the exact manager-owned source model."
		if step.request.Alias != "" {
			prerequisites = []string{StepRemoveAlias}
		}
	}
	return state.StepContract{
		ID:                  id,
		Owner:               state.OwnerOllama,
		Action:              action,
		Risk:                state.RiskDestructive,
		PrerequisiteStepIDs: prerequisites,
		ConsentRequired:     true,
		Reversible:          false,
		NotReversibleReason: "Ollama model deletion requires reacquisition to reverse.",
	}
}

func (step *removeStep) Check(ctx context.Context, _ state.StepContext) (state.CheckResult, error) {
	request := step.request
	request.Consent = true
	if step.source {
		request.RemoveSource = true
	} else {
		request.RemoveSource = false
	}
	if err := validateRemoval(request); err != nil {
		return state.CheckResult{}, stepFailure(err)
	}
	_, found, err := step.manager.findLocalModel(ctx, step.modelName)
	if err != nil {
		return state.CheckResult{}, stepFailure(err)
	}
	return state.CheckResult{
		AlreadySatisfied: !found,
		Summary:          removalCheckSummary(found),
		Facts:            map[string]state.SafeScalar{},
	}, nil
}

func (step *removeStep) Plan(context.Context, state.StepContext) (state.PlanResult, error) {
	return state.PlanResult{Summary: "Remove only the exact model after ownership, dependency, and consent checks."}, nil
}

func (step *removeStep) Apply(ctx context.Context, _ state.StepContext) (state.ApplyResult, error) {
	if err := step.manager.run(ctx, "remove exact model", "rm", step.modelName); err != nil {
		return state.ApplyResult{}, stepFailure(err)
	}
	return state.ApplyResult{
		Summary:          "The exact model removal command completed.",
		MutationAssetIDs: []string{step.assetID},
	}, nil
}

func (step *removeStep) Verify(ctx context.Context, _ state.StepContext) (state.VerifyResult, error) {
	_, found, err := step.manager.findLocalModel(ctx, step.modelName)
	if err != nil {
		return state.VerifyResult{}, stepFailure(err)
	}
	if found {
		return state.VerifyResult{}, stepFailure(&Error{Kind: ErrorCommand, Operation: "verify model removal", Err: errors.New("exact model remains installed")})
	}
	evidence := state.Evidence{
		Kind:       state.EvidenceModelMetadata,
		ObservedAt: step.manager.now(),
		Attributes: state.SafeAttributes{
			"model_name": step.modelName,
			"present":    false,
			"verified":   true,
		},
	}
	return state.VerifyResult{
		Summary:  "The exact model is absent.",
		Evidence: []state.Evidence{evidence},
		Facts:    map[string]state.SafeScalar{},
		Assets:   []state.ManagedAsset{},
	}, nil
}

func (step *removeStep) Undo(context.Context, state.StepContext) (state.UndoResult, error) {
	return state.UndoResult{}, errors.New("model removal is not reversible")
}

func sourceModelName(request InstallRequest) string {
	if request.Source.Kind == SourceGGUF {
		return request.Source.ImportedName
	}
	return request.Source.Reference
}

func sourceCheckSummary(local bool) string {
	if local {
		return "The exact source model is already local."
	}
	return "The exact source model requires acquisition."
}

func aliasCheckSummary(local bool) string {
	if local {
		return "The exact alias already exists and will be reverified."
	}
	return "The exact alias requires creation."
}

func removalCheckSummary(present bool) string {
	if present {
		return "The exact manager-owned model is present and requires removal."
	}
	return "The exact model is already absent."
}

func stepFailure(err error) error {
	code := IssueModelAcquire
	domain := state.IssueDomain("model")
	summary := "The Ollama model lifecycle step failed."
	var operationError *Error
	if errors.As(err, &operationError) {
		switch operationError.Kind {
		case ErrorCapacity:
			code = IssueCapacity
			domain = "capacity"
			summary = "Free model-volume storage is insufficient or unknown."
		case ErrorCompatibility:
			code = IssueModelIncompatible
			summary = "The exact model is incompatible with the local runtime contract."
		case ErrorOwnership:
			code = IssueOwnership
			domain = "asset"
			summary = "Manager ownership or dependency evidence is insufficient."
		case ErrorConsent:
			code = state.IssueCode("LAI-ASSET-002")
			domain = "asset"
			summary = "Explicit destructive consent is required."
		case ErrorCommand:
			code = IssueModelAcquire
			summary = "The exact Ollama model command failed."
		}
		switch operationError.Operation {
		case "verify structured tool call":
			code = IssueToolsFailed
			domain = "runtime"
			summary = "The exact alias failed structured tool-call verification."
		case "verify allocated context":
			code = IssueContextFailed
			domain = "runtime"
			summary = "The exact alias has less than 65,536 allocated context tokens."
		case "verify allocated runtime":
			code = IssueOffloadUnknown
			domain = "runtime"
			summary = "The exact alias is not loaded with verifiable processor and offload evidence."
		case "create alias", "reuse existing alias", "verify alias Modelfile":
			code = IssueModelAlias
			summary = "The exact local alias does not satisfy its source and context contract."
		}
	}
	component := "ollama"
	return &state.StepError{
		Err: err,
		Issue: state.Issue{
			Code:      code,
			Severity:  state.SeverityError,
			Domain:    domain,
			Component: &component,
			Summary:   summary,
			Remediations: []state.Remediation{{
				Kind:       "retry",
				Summary:    "Resolve the reported condition and resume the transaction.",
				SafeForYes: false,
			}},
		},
	}
}

func installAssetIDs(request InstallRequest) (string, string) {
	return "asset-ollama-source-" + safeFileToken(sourceModelName(request)),
		"asset-ollama-alias-" + safeFileToken(request.Alias)
}

func validateLifecycleAssets(request InstallRequest) error {
	sourceID, aliasID := installAssetIDs(request)
	if sourceID == "asset-ollama-source-" || aliasID == "asset-ollama-alias-" {
		return fmt.Errorf("invalid lifecycle asset identifiers")
	}
	return nil
}
