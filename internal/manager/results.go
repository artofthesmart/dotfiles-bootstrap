package manager

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/catalog"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/install"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/ollama"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/vscode"
)

type stateEventAdapter struct {
	sink app.EventSink
}

func (adapter stateEventAdapter) Emit(event state.Event) error {
	if adapter.sink == nil {
		return nil
	}
	var issueValue *app.Issue
	if event.Issue != nil {
		value := appIssue(*event.Issue)
		issueValue = &value
	}
	attributes := map[string]any{}
	copyEventAttribute(attributes, "application_id", event.Attributes.ApplicationID)
	copyEventAttribute(attributes, "attempt", event.Attributes.Attempt)
	copyEventAttribute(attributes, "bytes", event.Attributes.Bytes)
	copyEventAttribute(attributes, "context_tokens", event.Attributes.ContextTokens)
	copyEventAttribute(attributes, "dry_run", event.Attributes.DryRun)
	copyEventAttribute(attributes, "structured", event.Attributes.Structured)
	copyEventAttribute(attributes, "verified", event.Attributes.Verified)
	for key, value := range map[string]string{
		"asset_id": event.Attributes.AssetID, "asset_type": event.Attributes.AssetType,
		"command_family": event.Attributes.CommandFamily, "extension_id": event.Attributes.ExtensionID,
		"model_name": event.Attributes.ModelName, "operation": event.Attributes.Operation,
		"ownership": event.Attributes.Ownership, "phase": event.Attributes.Phase,
		"profile_name": event.Attributes.ProfileName, "risk": event.Attributes.Risk,
		"service_id": event.Attributes.ServiceID, "status": event.Attributes.Status,
		"tool_name": event.Attributes.ToolName, "version": event.Attributes.Version,
	} {
		if value != "" {
			attributes[key] = value
		}
	}
	var progress *app.EventProgress
	if event.Progress != nil {
		progress = &app.EventProgress{Unit: event.Progress.Unit, Completed: event.Progress.Completed, Total: event.Progress.Total}
	}
	return adapter.sink.Emit(app.Event{
		Timestamp: event.Timestamp, Command: app.Command(event.Command), TransactionID: event.TransactionID,
		Kind: string(event.Kind), Level: string(event.Level), StepID: event.StepID,
		Message: string(event.Message), Progress: progress, Issue: issueValue, Attributes: attributes,
	})
}

func copyEventAttribute[T any](target map[string]any, name string, value *T) {
	if value != nil {
		target[name] = *value
	}
}

func hardwareComponent(value hardware.Capabilities) app.Component {
	attributes := map[string]any{
		"os":                  value.OS.Family,
		"architecture":        value.Architecture,
		"desktop":             value.Desktop.Available,
		"system_memory_bytes": value.Memory.InstalledBytes,
		"free_storage_bytes":  value.Storage.FreeBytes,
		"acceleration":        string(value.Acceleration),
		"gpu_count":           len(value.GPUs),
	}
	if value.OS.Distribution != "" {
		attributes["distribution"] = value.OS.Distribution
	}
	return component("host", "ready", value.Desktop.Available, attributes)
}

func transactionComponent(value state.Transaction) app.Component {
	verified := value.Status == state.StatusSucceeded
	stateName := "degraded"
	switch value.Status {
	case state.StatusSucceeded:
		stateName = "ready"
	case state.StatusAwaitingManual:
		stateName = "manual_required"
	case state.StatusFailed, state.StatusRollbackFailed:
		stateName = "failed"
	case state.StatusArchived:
		stateName = "not_applicable"
	}
	return component("state", stateName, verified, map[string]any{
		"status":         string(value.Status),
		"command_family": string(value.Command),
		"step_count":     len(value.Steps),
	})
}

func vscodeComponents(value vscode.Status) []app.Component {
	applicationState := "absent"
	if value.Executable != "" {
		applicationState = "present"
	}
	if value.VersionSupported {
		applicationState = "ready"
	}
	profileState := "absent"
	if value.ProfileReady {
		profileState = "ready"
	}
	extensionState := "absent"
	if value.OllamaExtensionInstalled {
		extensionState = "present"
	}
	if value.OllamaExtensionCurrent {
		extensionState = "ready"
	}
	return []app.Component{
		component("vscode", applicationState, value.VersionSupported, map[string]any{
			"version": value.Version, "required_version": value.RequiredVSCodeVersion,
		}),
		component("vscode_profile", profileState, value.ProfileReady, map[string]any{"profile_name": value.ProfileName}),
		component("vscode_extension", extensionState, value.OllamaExtensionCurrent, map[string]any{
			"extension_id": vscode.OfficialOllamaExtensionID, "version": value.OllamaExtensionVersion,
		}),
	}
}

func recommendationComponent(value catalog.CandidateEvaluation, role string) app.Component {
	attributes := map[string]any{
		"role": role, "catalog_id": value.CatalogID, "model_name": value.OllamaName,
		"score": value.Score, "expected_offload": string(value.Offload.Expected),
		"working_set_bytes": value.Memory.WorkingSetBytes, "required_free_bytes": value.Storage.RequiredFreeBytes,
	}
	if len(value.Disqualifiers) > 0 {
		attributes["disqualifier_count"] = len(value.Disqualifiers)
	}
	return component("source_model", "ready", value.Eligible, attributes)
}

func component(name, stateName string, verified bool, attributes map[string]any) app.Component {
	if attributes == nil {
		attributes = map[string]any{}
	}
	return app.Component{Component: name, State: stateName, Verified: verified, Attributes: attributes}
}

func appendIssue(result *app.Result, value app.Issue) {
	for _, existing := range result.Issues {
		if existing.Code == value.Code && existing.Summary == value.Summary {
			return
		}
	}
	result.Issues = append(result.Issues, value)
}

func issue(code app.IssueCode, severity app.Severity, domain, summary string) app.Issue {
	return app.Issue{
		Code: code, Severity: severity, Domain: domain, Summary: summary,
		Remediations: []app.Remediation{{
			Kind: "retry", Summary: "Review the typed issue, run local-ai doctor, and retry only the documented operation.", SafeForYes: false,
		}},
	}
}

func issueForError(err error) app.Issue {
	if err == nil {
		return issue(app.IssueInternal, app.SeverityError, "internal", "An unexpected empty error was reported.")
	}
	var stepError *state.StepError
	if errors.As(err, &stepError) {
		return appIssue(stepError.Issue)
	}
	var deploymentError *install.DeploymentError
	if errors.As(err, &deploymentError) {
		return issue(deploymentError.Code, app.SeverityError, domainForIssue(deploymentError.Code), safeErrorSummary(deploymentError.Code))
	}
	var ollamaError *ollama.Error
	if errors.As(err, &ollamaError) {
		code := app.IssueRuntimeUnavailable
		switch ollamaError.Kind {
		case ollama.ErrorInvalidInput:
			code = app.IssueConfigurationInvalid
		case ollama.ErrorRequest, ollama.ErrorStatus:
			code = app.IssueNetworkUnavailable
		case ollama.ErrorResponse, ollama.ErrorLimit:
			code = app.IssueMetadataInconsistent
		case ollama.ErrorCompatibility:
			code = app.IssueModelIncompatible
		case ollama.ErrorCapacity:
			code = app.IssueCapacityInsufficient
		case ollama.ErrorCommand:
			if strings.Contains(strings.ToLower(ollamaError.Operation), "server") || strings.Contains(strings.ToLower(ollamaError.Operation), "service") {
				code = app.IssueRuntimeUnavailable
			} else {
				code = app.IssueModelAcquisition
			}
		case ollama.ErrorOwnership:
			code = app.IssueAssetOwnership
		case ollama.ErrorConsent:
			code = app.IssueConsentDeclined
		}
		severity := app.SeverityError
		if code == app.IssueConsentDeclined {
			severity = app.SeverityInfo
		}
		return issue(code, severity, domainForIssue(code), safeErrorSummary(code))
	}
	switch {
	case errors.Is(err, hardware.ErrUnsupportedHost):
		return issue(app.IssueHostUnsupported, app.SeverityError, "host", "This host is outside the supported Local AI manager matrix.")
	case errors.Is(err, hardware.ErrInvalidEvidence):
		return issue(app.IssuePrerequisiteUnusable, app.SeverityError, "prerequisite", "Required privacy-allowlisted host evidence could not be collected.")
	case errors.Is(err, state.ErrActiveTransaction), errors.Is(err, state.ErrIncompatibleResume):
		return issue(app.IssueStateResume, app.SeverityError, "state", "The active transaction is incompatible with this invocation; use --resume or --restart as appropriate.")
	case errors.Is(err, state.ErrInvalidState), errors.Is(err, state.ErrPersistence):
		return issue(app.IssueStateFailure, app.SeverityError, "state", "Transaction state could not be safely loaded or persisted.")
	case errors.Is(err, context.Canceled):
		return issue(app.IssueConsentDeclined, app.SeverityInfo, "policy", "The operation was cancelled.")
	default:
		return issue(app.IssueInternal, app.SeverityError, "internal", "An internal operation failed without exposing raw command output.")
	}
}

func appIssue(value state.Issue) app.Issue {
	remediations := make([]app.Remediation, 0, len(value.Remediations))
	for _, remediation := range value.Remediations {
		remediations = append(remediations, app.Remediation{
			Kind: string(remediation.Kind), Summary: remediation.Summary, Command: remediation.Command, SafeForYes: remediation.SafeForYes,
		})
	}
	return app.Issue{
		Code: app.IssueCode(value.Code), Severity: app.Severity(value.Severity), Domain: string(value.Domain),
		Component: value.Component, Summary: value.Summary, RepairID: value.RepairID, Remediations: remediations,
	}
}

func safeErrorSummary(code app.IssueCode) string {
	switch code {
	case app.IssueConfigurationInvalid, app.IssueConfigurationConflict:
		return "The effective configuration is invalid or conflicts with policy."
	case app.IssueNetworkUnavailable, app.IssueDownloadInvalid:
		return "A required network operation failed or returned incomplete data."
	case app.IssueHTTPSPolicy, app.IssueChecksumMismatch, app.IssuePublisherInvalid, app.IssueMetadataInconsistent:
		return "A required integrity or metadata check failed."
	case app.IssueCapacityInsufficient, app.IssueMemoryInsufficient, app.IssueCapacityUnknown:
		return "Required storage or memory capacity is insufficient or unknown."
	case app.IssueModelIncompatible, app.IssueModelContextInsufficient, app.IssueModelRemote, app.IssueModelToolsUnsupported:
		return "The exact model does not meet the local context and tool contract."
	case app.IssueModelAcquisition, app.IssueModelAlias:
		return "The exact model acquisition or alias operation failed."
	case app.IssueRuntimeUnavailable, app.IssueRuntimeToolCall, app.IssueRuntimeContext, app.IssueRuntimeOffloadUnknown:
		return "Local Ollama runtime verification failed."
	case app.IssueAssetOwnership, app.IssueAssetConfirmation:
		return "Asset ownership or destructive confirmation requirements were not satisfied."
	case app.IssueUpdateUnavailable, app.IssueUpdateSelfCheck:
		return "The verified update or rollback operation could not complete."
	case app.IssueStateFailure, app.IssueStateSchema, app.IssueStateResume, app.IssueStateWrite:
		return "Transaction state could not be safely resumed or persisted."
	default:
		return "The requested operation failed."
	}
}

func domainForIssue(code app.IssueCode) string {
	value := string(code)
	switch {
	case strings.HasPrefix(value, "LAI-HOST"):
		return "host"
	case strings.HasPrefix(value, "LAI-PREREQ"):
		return "prerequisite"
	case strings.HasPrefix(value, "LAI-POLICY"):
		return "policy"
	case strings.HasPrefix(value, "LAI-NET"):
		return "network"
	case strings.HasPrefix(value, "LAI-INTEGRITY"):
		return "integrity"
	case strings.HasPrefix(value, "LAI-CAPACITY"):
		return "capacity"
	case strings.HasPrefix(value, "LAI-MODEL"):
		return "model"
	case strings.HasPrefix(value, "LAI-RUNTIME"):
		return "runtime"
	case strings.HasPrefix(value, "LAI-VSCODE"):
		return "vscode"
	case strings.HasPrefix(value, "LAI-STATE"):
		return "state"
	case strings.HasPrefix(value, "LAI-CONFIG"):
		return "config"
	case strings.HasPrefix(value, "LAI-ASSET"):
		return "asset"
	case strings.HasPrefix(value, "LAI-UPDATE"):
		return "update"
	default:
		return "internal"
	}
}

func finalizeReadResult(result *app.Result, successSummary string) {
	for _, value := range result.Issues {
		if value.Severity == app.SeverityError {
			finalizeFailure(result)
			return
		}
	}
	if len(result.Issues) > 0 {
		result.Outcome, result.ExitCode, result.Summary = app.OutcomeManualRequired, app.ExitManualRequired, "Status was collected; manual attention is required."
		return
	}
	result.Outcome, result.ExitCode, result.Summary = app.OutcomeSuccess, app.ExitSuccess, successSummary
}

func finalizeDoctorResult(result *app.Result) {
	hasWarning := false
	for _, value := range result.Issues {
		if value.Severity == app.SeverityError {
			finalizeFailure(result)
			return
		}
		hasWarning = true
	}
	if hasWarning {
		result.Outcome, result.ExitCode = app.OutcomeManualRequired, app.ExitManualRequired
		return
	}
	result.Outcome, result.ExitCode = app.OutcomeSuccess, app.ExitSuccess
}

func finalizeFailure(result *app.Result) {
	result.Outcome = app.OutcomeFailed
	result.ExitCode = app.ExitInternalFailure
	result.Summary = "The requested operation failed."
	for _, value := range result.Issues {
		if value.Severity == app.SeverityError {
			result.ExitCode = app.ExitForIssue(value.Code)
			result.Summary = value.Summary
			return
		}
	}
	if len(result.Issues) > 0 {
		result.Outcome, result.ExitCode, result.Summary = app.OutcomeManualRequired, app.ExitManualRequired, result.Issues[0].Summary
	}
}

func catalogHost(value hardware.Capabilities) catalog.Host {
	execution := catalog.ExecutionCPUOnly
	var acceleratorMemory *uint64
	if value.Memory.Unified {
		execution = catalog.ExecutionAppleUnified
	} else {
		for _, gpu := range value.GPUs {
			if gpu.MemoryKind == hardware.MemoryDedicated {
				execution = catalog.ExecutionDedicatedGPU
				if gpu.MemoryBytes != nil && (acceleratorMemory == nil || *gpu.MemoryBytes > *acceleratorMemory) {
					memory := *gpu.MemoryBytes
					acceleratorMemory = &memory
				}
			} else if gpu.MemoryKind == hardware.MemoryShared {
				execution = catalog.ExecutionIntegratedShared
			}
		}
	}
	systemMemory := value.Memory.InstalledBytes
	freeStorage := value.Storage.FreeBytes
	return catalog.Host{
		OperatingSystem: value.OS.Family, Architecture: value.Architecture, ExecutionMode: execution,
		SystemMemoryBytes: &systemMemory, AcceleratorMemoryBytes: acceleratorMemory, FreeStorageBytes: &freeStorage,
	}
}

func hostState(value hardware.Capabilities) state.Host {
	hostOS := state.HostOS(value.OS.Family)
	if hostOS == "darwin" {
		hostOS = state.HostMacOS
	}
	var distribution *state.LinuxDistribution
	if value.OS.Distribution == "ubuntu" || value.OS.Distribution == "debian" {
		parsed := state.LinuxDistribution(value.OS.Distribution)
		distribution = &parsed
	}
	digestInput := fmt.Sprintf("%s|%s|%d|%s|%d|%t", value.OS.Family, value.Architecture, value.Memory.InstalledBytes, value.Acceleration, value.Storage.FreeBytes, value.Desktop.Available)
	return state.Host{
		OS: hostOS, Architecture: state.Architecture(value.Architecture), OSVersion: value.OS.Version,
		LinuxDistribution: distribution, DesktopSession: value.Desktop.Available, CapabilityDigest: sha256String(digestInput),
	}
}

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + fmt.Sprintf("%x", sum[:])
}

func stringPointer(value string) *string {
	return &value
}
