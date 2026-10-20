package manager

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/catalog"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/install"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/ollama"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/update"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/vscode"
)

type Uninstaller interface {
	Uninstall(context.Context, []state.ManagedAsset, install.UninstallOptions) (state.DeploymentReceipt, error)
}

type CopilotLifecycle interface {
	Configure(context.Context, app.Operation) app.Result
	Open(context.Context, app.Operation) app.Result
}

type Service struct {
	Clock          app.Clock
	Build          app.BuildInfo
	FileSystem     platform.FileSystem
	Paths          platform.PathProvider
	Hardware       hardware.Discoverer
	Catalog        catalog.Repository
	Ollama         ollama.Manager
	OllamaFactory  func(string) (ollama.Manager, error)
	VSCode         *vscode.Manager
	Copilot        CopilotLifecycle
	State          state.Store
	Updater        update.Updater
	UpdaterFactory func(bool) update.Updater
	Uninstaller    Uninstaller
}

func (service Service) Execute(ctx context.Context, operation app.Operation) app.Result {
	if service.OllamaFactory != nil {
		configured, err := service.OllamaFactory(operation.Config.Ollama.Endpoint)
		if err != nil {
			return service.errorResult(operation.Command, err)
		}
		service.Ollama = configured
	}
	if service.UpdaterFactory != nil {
		service.Updater = service.UpdaterFactory(operation.Config.Update.VerifyGitHubAttestationWhenAvailable)
	}
	switch operation.Command {
	case app.CommandSetup:
		return service.setup(ctx, operation)
	case app.CommandStatus:
		return service.status(ctx, operation)
	case app.CommandDoctor:
		return service.doctor(ctx, operation)
	case app.CommandRepair:
		return service.repair(ctx, operation)
	case app.CommandModelsList:
		return service.modelsList(ctx, operation)
	case app.CommandModelsRecommend:
		return service.modelsRecommend(ctx, operation)
	case app.CommandModelsInstall:
		return service.modelsInstall(ctx, operation)
	case app.CommandModelsRemove:
		return service.modelsRemove(ctx, operation)
	case app.CommandVSCodeConfigure:
		return service.vscodeConfigure(ctx, operation)
	case app.CommandVSCodeOpen:
		return service.vscodeOpen(ctx, operation)
	case app.CommandCopilotConfigure:
		if service.Copilot == nil {
			return service.failure(operation.Command, app.IssuePrerequisiteMissing, "The typed Copilot CLI lifecycle service is not integrated in this build.")
		}
		return service.Copilot.Configure(ctx, operation)
	case app.CommandCopilotOpen:
		if service.Copilot == nil {
			return service.failure(operation.Command, app.IssuePrerequisiteMissing, "The typed Copilot CLI lifecycle service is not integrated in this build.")
		}
		return service.Copilot.Open(ctx, operation)
	case app.CommandVerify:
		return service.verify(ctx, operation)
	case app.CommandUpdate, app.CommandUpdateRollback:
		return service.update(ctx, operation)
	case app.CommandUninstall:
		return service.uninstall(ctx, operation)
	case app.CommandReport:
		return service.report(ctx, operation)
	default:
		return service.failure(operation.Command, app.IssueConfigurationInvalid, "The requested command is not supported.")
	}
}

func (service Service) setup(ctx context.Context, operation app.Operation) app.Result {
	capabilities, err := service.discover(ctx)
	if err != nil {
		return service.errorResult(operation.Command, err)
	}
	entry, err := service.selectEntry(ctx, operation, capabilities)
	if err != nil {
		return service.errorResult(operation.Command, err)
	}
	if result := service.ensureOllama(ctx, operation); result != nil {
		return *result
	}
	request := service.installRequest(operation, entry, capabilities)
	steps := ollama.NewInstallSteps(service.Ollama, request)
	steps = append(steps, vscode.LifecycleSteps(service.VSCode, vscode.Request{
		Alias: aliasFor(entry.ID), Repository: operation.Options.Repository,
	}, operation.Options.ManualCompleted)...)
	return service.executeTransaction(ctx, operation, state.CommandSetup, steps, capabilities, &entry.ID, stringPointer(request.Alias))
}

func (service Service) status(ctx context.Context, operation app.Operation) app.Result {
	result := service.base(operation.Command)
	capabilities, hardwareErr := service.discover(ctx)
	if hardwareErr != nil {
		appendIssue(&result, issueForError(hardwareErr))
	} else {
		result.Components = append(result.Components, hardwareComponent(capabilities))
	}
	transaction, stateErr := service.loadState(ctx)
	if stateErr != nil && !errors.Is(stateErr, state.ErrNoState) {
		appendIssue(&result, issueForError(stateErr))
	} else if stateErr == nil {
		result.TransactionID = stringPointer(transaction.TransactionID)
		result.Components = append(result.Components, transactionComponent(transaction))
	}
	alias := aliasFromTransaction(transaction)
	result.Components = append(result.Components, service.ollamaComponents(ctx, alias, &result)...)
	if service.VSCode != nil {
		status, err := service.VSCode.Status(ctx)
		if err != nil {
			appendIssue(&result, issueForError(err))
		}
		result.Components = append(result.Components, vscodeComponents(status)...)
	}
	if operation.Options.CheckUpdates || operation.Config.Update.CheckOnStatus {
		if service.Updater == nil {
			appendIssue(&result, issue(app.IssueUpdateUnavailable, app.SeverityWarning, "update", "Update checking is unavailable."))
		} else if release, err := service.Updater.Check(ctx); err != nil {
			appendIssue(&result, issueForError(err))
		} else {
			result.Components = append(result.Components, component("update", "present", true, map[string]any{"available_version": release.Version}))
		}
	}
	finalizeReadResult(&result, "Local AI status was collected.")
	return result
}

func (service Service) doctor(ctx context.Context, operation app.Operation) app.Result {
	result := service.status(ctx, operation)
	result.Command = app.CommandDoctor
	result.Summary = "Diagnostics completed."
	finalizeDoctorResult(&result)
	return result
}

func (service Service) repair(ctx context.Context, operation app.Operation) app.Result {
	diagnosis := service.doctor(ctx, operation)
	if diagnosis.ExitCode == app.ExitSuccess {
		diagnosis.Command = app.CommandRepair
		diagnosis.Summary = "No repair was required."
		return diagnosis
	}
	if operation.DryRun {
		diagnosis.Command = app.CommandRepair
		diagnosis.Outcome = app.OutcomeSuccess
		diagnosis.ExitCode = app.ExitSuccess
		diagnosis.Summary = "Dry run identified repairs without changing the host."
		return diagnosis
	}
	capabilities, err := service.discover(ctx)
	if err != nil {
		return service.errorResult(operation.Command, err)
	}
	transaction, _ := service.loadState(ctx)
	var steps []state.Step
	var selected *string
	if transaction.SelectedCatalogID != nil {
		entry, findErr := service.Catalog.Find(ctx, *transaction.SelectedCatalogID)
		if findErr == nil {
			if result := service.ensureOllama(ctx, operation); result != nil {
				return *result
			}
			request := service.installRequest(operation, entry, capabilities)
			steps = append(steps, ollama.NewInstallSteps(service.Ollama, request)...)
			selected = &entry.ID
		}
	}
	if service.VSCode != nil {
		steps = append(steps, vscode.LifecycleSteps(service.VSCode, vscode.Request{
			Alias: aliasFromTransaction(transaction), Repository: operation.Options.Repository,
		}, operation.Options.ManualCompleted)...)
	}
	if len(steps) == 0 {
		return service.failure(operation.Command, app.IssuePrerequisiteMissing, "No compatible documented repair could be planned.")
	}
	return service.executeTransaction(ctx, operation, state.CommandRepair, steps, capabilities, selected, transaction.GeneratedAlias)
}

func (service Service) modelsList(ctx context.Context, operation app.Operation) app.Result {
	result := service.base(operation.Command)
	if service.Catalog == nil {
		return service.failure(operation.Command, app.IssueInternal, "The embedded model catalog is unavailable.")
	}
	entries, err := service.Catalog.List(ctx)
	if err != nil {
		return service.errorResult(operation.Command, err)
	}
	installed := map[string]bool{}
	if service.Ollama.API != nil {
		if names, listErr := service.Ollama.API.ListModels(ctx); listErr == nil {
			for _, name := range names {
				installed[name] = true
			}
		}
	}
	for _, entry := range entries {
		stateName := "absent"
		if installed[entry.Model.OllamaName] {
			stateName = "present"
		}
		result.Components = append(result.Components, component("source_model", stateName, installed[entry.Model.OllamaName], map[string]any{
			"catalog_id":             entry.ID,
			"model_name":             entry.Model.OllamaName,
			"catalog_status":         string(entry.Status),
			"context_tokens":         entry.Context.AdvertisedNativeTokens,
			"download_minimum_bytes": entry.Download.MinimumBytes,
			"download_maximum_bytes": entry.Download.MaximumBytes,
			"license":                entry.License.Identifier,
			"installed":              installed[entry.Model.OllamaName],
		}))
	}
	result.Outcome = app.OutcomeSuccess
	result.ExitCode = app.ExitSuccess
	result.Summary = "The reviewed model catalog was listed."
	return result
}

func (service Service) modelsRecommend(ctx context.Context, operation app.Operation) app.Result {
	capabilities, err := service.discover(ctx)
	if err != nil {
		return service.errorResult(operation.Command, err)
	}
	selection, err := service.recommend(ctx, operation, capabilities)
	if err != nil {
		return service.errorResult(operation.Command, err)
	}
	result := service.base(operation.Command)
	result.Components = append(result.Components, hardwareComponent(capabilities))
	if selection.Primary != nil {
		result.Components = append(result.Components, recommendationComponent(*selection.Primary, "primary"))
	}
	if selection.Fallback != nil {
		result.Components = append(result.Components, recommendationComponent(*selection.Fallback, "fallback"))
	}
	if selection.Primary == nil {
		return service.failure(operation.Command, app.IssueModelIncompatible, "No catalog model is eligible for this host and policy.")
	}
	result.Outcome = app.OutcomeSuccess
	result.ExitCode = app.ExitSuccess
	result.Summary = "Deterministic model recommendations were computed."
	return result
}

func (service Service) modelsInstall(ctx context.Context, operation app.Operation) app.Result {
	capabilities, err := service.discover(ctx)
	if err != nil {
		return service.errorResult(operation.Command, err)
	}
	entry, err := service.Catalog.Find(ctx, operation.Arguments[0])
	if err != nil {
		return service.failure(operation.Command, app.IssueModelIncompatible, "The requested catalog model is unknown.")
	}
	if result := service.ensureOllama(ctx, operation); result != nil {
		return *result
	}
	request := service.installRequest(operation, entry, capabilities)
	return service.executeTransaction(ctx, operation, state.CommandModelsInstall, ollama.NewInstallSteps(service.Ollama, request), capabilities, &entry.ID, &request.Alias)
}

func (service Service) modelsRemove(ctx context.Context, operation app.Operation) app.Result {
	transaction, err := service.loadState(ctx)
	if err != nil {
		return service.errorResult(operation.Command, err)
	}
	name := operation.Arguments[0]
	request := removalRequest(transaction, name, operation.Options.ConfirmDestructive)
	if request.Alias == "" && !request.RemoveSource {
		return service.failure(operation.Command, app.IssueAssetOwnership, "Manager ownership of the requested model is not proven.")
	}
	if operation.DryRun {
		result := service.base(operation.Command)
		result.Outcome = app.OutcomeSuccess
		result.ExitCode = app.ExitSuccess
		result.Summary = "Dry run verified ownership and planned exact model removal without mutation."
		result.Components = append(result.Components, transactionComponent(transaction))
		return result
	}
	if !operation.Options.ConfirmDestructive {
		return service.manual(operation.Command, app.IssueAssetConfirmation, "Model deletion requires --confirm-destructive after dependency review.")
	}
	capabilities, err := service.discover(ctx)
	if err != nil {
		return service.errorResult(operation.Command, err)
	}
	return service.executeTransaction(ctx, operation, state.CommandModelsRemove, ollama.NewRemoveSteps(service.Ollama, request), capabilities, transaction.SelectedCatalogID, transaction.GeneratedAlias)
}

func (service Service) vscodeConfigure(ctx context.Context, operation app.Operation) app.Result {
	if service.VSCode == nil {
		return service.failure(operation.Command, app.IssueVSCodeIntegration, "The VS Code lifecycle service is unavailable.")
	}
	capabilities, err := service.discover(ctx)
	if err != nil {
		return service.errorResult(operation.Command, err)
	}
	transaction, _ := service.loadState(ctx)
	alias := aliasFromTransaction(transaction)
	if alias == "" && operation.Config.Model.CatalogID != "" {
		alias = aliasFor(operation.Config.Model.CatalogID)
	}
	if alias == "" {
		return service.failure(operation.Command, app.IssueVSCodeAliasUnavailable, "An exact local-ai alias must be installed before VS Code configuration.")
	}
	steps := vscode.LifecycleSteps(service.VSCode, vscode.Request{Alias: alias, Repository: operation.Options.Repository}, operation.Options.ManualCompleted)
	return service.executeTransaction(ctx, operation, state.CommandVSCodeConfigure, steps, capabilities, transaction.SelectedCatalogID, stringPointer(alias))
}

func (service Service) vscodeOpen(ctx context.Context, operation app.Operation) app.Result {
	if service.VSCode == nil {
		return service.failure(operation.Command, app.IssueVSCodeIntegration, "The VS Code lifecycle service is unavailable.")
	}
	transaction, _ := service.loadState(ctx)
	alias := aliasFromTransaction(transaction)
	if operation.DryRun {
		status, err := service.VSCode.Status(ctx)
		if err != nil {
			return service.errorResult(operation.Command, err)
		}
		result := service.base(operation.Command)
		result.Components = append(result.Components, vscodeComponents(status)...)
		result.Outcome, result.ExitCode, result.Summary = app.OutcomeSuccess, app.ExitSuccess, "Dry run verified VS Code readiness without opening a window."
		return result
	}
	return service.fromVSCodeResult(operation.Command, service.VSCode.Open(ctx, vscode.Request{Alias: alias, Repository: operation.Options.Repository}))
}

func (service Service) verify(ctx context.Context, operation app.Operation) app.Result {
	result := service.doctor(ctx, operation)
	result.Command = app.CommandVerify
	if result.ExitCode == app.ExitSuccess {
		result.Summary = "Runtime and VS Code verification passed."
	} else {
		result.Summary = "Runtime or VS Code verification needs attention."
	}
	return result
}

func (service Service) update(ctx context.Context, operation app.Operation) app.Result {
	if service.Updater == nil {
		return service.failure(operation.Command, app.IssueUpdateUnavailable, "The update service is unavailable.")
	}
	if operation.DryRun {
		if operation.Command == app.CommandUpdateRollback {
			return service.manual(operation.Command, app.IssueUpdateUnavailable, "Rollback dry run cannot verify a replacement without applying it.")
		}
		release, err := service.Updater.Check(ctx)
		if err != nil {
			return service.errorResult(operation.Command, err)
		}
		result := service.base(operation.Command)
		result.Components = append(result.Components, component("update", "present", true, map[string]any{"available_version": release.Version, "verified": true}))
		result.Outcome, result.ExitCode, result.Summary = app.OutcomeSuccess, app.ExitSuccess, "Dry run verified the stable update metadata without downloading or replacing the binary."
		return result
	}
	var err error
	if operation.Command == app.CommandUpdateRollback {
		err = service.Updater.Rollback(ctx)
	} else {
		var release update.Release
		release, err = service.Updater.Check(ctx)
		if err == nil {
			err = service.Updater.Apply(ctx, release)
		}
	}
	if err != nil {
		return service.errorResult(operation.Command, err)
	}
	result := service.base(operation.Command)
	result.Outcome, result.ExitCode, result.Summary = app.OutcomeSuccess, app.ExitSuccess, "The verified manager deployment operation completed."
	return result
}

func (service Service) uninstall(ctx context.Context, operation app.Operation) app.Result {
	transaction, err := service.loadState(ctx)
	if err != nil && !errors.Is(err, state.ErrNoState) {
		return service.errorResult(operation.Command, err)
	}
	destructive := operation.Options.RemoveAliases || operation.Options.RemoveModels || operation.Options.RemoveVSCodeExtension || operation.Options.RemoveVSCode || operation.Options.RemoveOllama
	if destructive && !operation.Options.ConfirmDestructive {
		return service.manual(operation.Command, app.IssueAssetConfirmation, "External or model removal requires --confirm-destructive and verified manager ownership.")
	}
	if operation.DryRun {
		result := service.base(operation.Command)
		result.Outcome, result.ExitCode, result.Summary = app.OutcomeSuccess, app.ExitSuccess, "Dry run planned ownership-aware uninstall without removing assets."
		if err == nil {
			result.Components = append(result.Components, transactionComponent(transaction))
		}
		return result
	}
	if service.Uninstaller == nil {
		return service.failure(operation.Command, app.IssueInternal, "The manager uninstall service is unavailable.")
	}
	_, err = service.Uninstaller.Uninstall(ctx, transaction.ManagedAssets, install.UninstallOptions{
		RemoveAliases: operation.Options.RemoveAliases, RemoveModels: operation.Options.RemoveModels,
		RemoveVSCodeExtension: operation.Options.RemoveVSCodeExtension, RemoveVSCode: operation.Options.RemoveVSCode,
		RemoveOllama: operation.Options.RemoveOllama, ConfirmDestructive: operation.Options.ConfirmDestructive,
	})
	if err != nil {
		return service.errorResult(operation.Command, err)
	}
	result := service.base(operation.Command)
	result.Outcome, result.ExitCode, result.Summary = app.OutcomeSuccess, app.ExitSuccess, "Manager-owned default assets were removed; external applications, profiles, models, and aliases were retained unless explicitly requested."
	return result
}

func (service Service) ensureOllama(ctx context.Context, operation app.Operation) *app.Result {
	if service.Ollama.API == nil {
		result := service.failure(operation.Command, app.IssueRuntimeUnavailable, "The local Ollama API service is unavailable.")
		return &result
	}
	if operation.DryRun {
		if _, err := service.Ollama.API.Status(ctx); err != nil {
			result := service.errorResult(operation.Command, err)
			return &result
		}
		return nil
	}
	status, err := service.Ollama.API.Status(ctx)
	if err == nil && status.Available {
		return nil
	}
	if !operation.Yes {
		result := service.manual(operation.Command, app.IssueManualApproval, "Starting Ollama requires explicit consent; rerun with --yes after reviewing the plan.")
		return &result
	}
	_, err = service.Ollama.EnsureServer(ctx, ollama.ServerStartOptions{AllowStart: true, ConsentRestart: false})
	if err != nil {
		result := service.errorResult(operation.Command, err)
		return &result
	}
	return nil
}

func (service Service) executeTransaction(ctx context.Context, operation app.Operation, command state.Command, steps []state.Step, capabilities hardware.Capabilities, selected, alias *string) app.Result {
	if service.State == nil || service.Clock == nil {
		return service.failure(operation.Command, app.IssueInternal, "The transaction service is unavailable.")
	}
	if operation.DryRun {
		return service.dryRunSteps(ctx, operation, steps)
	}
	engine := state.Engine{Store: service.State, Clock: service.Clock, Events: stateEventAdapter{sink: operation.Events}}
	consent := map[string]state.ConsentDecision{}
	for _, step := range steps {
		contract := step.Contract()
		switch {
		case contract.ID == vscode.ManualHandoffStepID && operation.Options.ManualCompleted:
			consent[contract.ID] = state.ConsentAccepted
		case operation.Yes && (contract.Risk == state.RiskReadOnly || contract.Risk == state.RiskUserLocal || contract.Risk == state.RiskLargeDownload):
			consent[contract.ID] = state.ConsentAccepted
		case operation.Options.ConfirmDestructive && contract.Risk == state.RiskDestructive:
			consent[contract.ID] = state.ConsentAccepted
		}
	}
	transaction, err := engine.Execute(ctx, state.ExecuteRequest{
		Spec: state.TransactionSpec{
			ManagerVersion: normalizedVersion(service.Build.Version), TransactionID: newUUID(),
			Command: command, EffectiveConfigDigest: configDigest(operation.Config),
			Host: hostState(capabilities), SelectedCatalogID: selected, GeneratedAlias: alias,
		},
		Steps: steps,
		Options: state.ExecuteOptions{
			Resume: operation.Resume, Restart: operation.Restart, Consent: consent,
		},
	})
	return service.transactionResult(operation.Command, transaction, err)
}

func (service Service) dryRunSteps(ctx context.Context, operation app.Operation, steps []state.Step) app.Result {
	facts := map[string]state.SafeScalar{}
	for _, step := range steps {
		stepContext := state.StepContext{DryRun: true, Facts: cloneFacts(facts)}
		check, err := step.Check(ctx, stepContext)
		if err != nil {
			return service.errorResult(operation.Command, err)
		}
		for key, value := range check.Facts {
			facts[key] = value
		}
		if !check.AlreadySatisfied {
			if _, err := step.Plan(ctx, stepContext); err != nil {
				return service.errorResult(operation.Command, err)
			}
		}
	}
	result := service.base(operation.Command)
	result.Outcome, result.ExitCode, result.Summary = app.OutcomeSuccess, app.ExitSuccess, "Dry run completed discovery and planning without downloads, installs, profile changes, model pulls, service restarts, or state writes."
	return result
}

func (service Service) installRequest(operation app.Operation, entry catalog.Entry, capabilities hardware.Capabilities) ollama.InstallRequest {
	free := capabilities.Storage.FreeBytes
	expectedBytes := uint64(0)
	if entry.Download.MinimumBytes == entry.Download.MaximumBytes {
		expectedBytes = entry.Download.MaximumBytes
	}
	dataDirectory := ""
	if service.Paths != nil {
		if paths, err := service.Paths.UserPaths(); err == nil {
			dataDirectory = filepath.Join(paths.DataDir, "models")
		}
	}
	return ollama.InstallRequest{
		Source: ollama.SourceSpec{Kind: ollama.SourceRegistry, Reference: entry.Model.OllamaName, ExpectedBytes: expectedBytes},
		Alias:  aliasFor(entry.ID), DataDirectory: dataDirectory, FreeStorageBytes: &free,
	}
}

func (service Service) selectEntry(ctx context.Context, operation app.Operation, capabilities hardware.Capabilities) (catalog.Entry, error) {
	id := operation.Config.Model.CatalogID
	if id == "" {
		selection, err := service.recommend(ctx, operation, capabilities)
		if err != nil {
			return catalog.Entry{}, err
		}
		if selection.Primary == nil {
			return catalog.Entry{}, errors.New("no eligible catalog model")
		}
		id = selection.Primary.CatalogID
	}
	entry, err := service.Catalog.Find(ctx, id)
	if err != nil {
		return catalog.Entry{}, err
	}
	if operation.Config.Model.MaxDownloadBytes > 0 && entry.Download.MaximumBytes > operation.Config.Model.MaxDownloadBytes {
		return catalog.Entry{}, &ollama.Error{Kind: ollama.ErrorCapacity, Operation: "apply configured download limit", Err: errors.New("catalog model exceeds max_download_bytes")}
	}
	return entry, nil
}

func (service Service) recommend(ctx context.Context, operation app.Operation, capabilities hardware.Capabilities) (catalog.Selection, error) {
	if service.Catalog == nil {
		return catalog.Selection{}, errors.New("catalog repository is unavailable")
	}
	entries, err := service.Catalog.List(ctx)
	if err != nil {
		return catalog.Selection{}, err
	}
	value := catalog.Catalog{SchemaVersion: catalog.SchemaVersion, CatalogVersion: "embedded", ReviewedAt: time.Now().UTC().Format("2006-01-02"), Entries: entries}
	if repository, ok := service.Catalog.(*catalog.EmbeddedRepository); ok {
		value = repository.Catalog()
	}
	host := catalogHost(capabilities)
	options := catalog.RecommendOptions{IncludeExperimental: operation.Config.Model.AllowExperimental}
	if operation.Config.Model.AllowedLicenseUse == "commercial" {
		options.License.RequireCommercialUse = true
	}
	return catalog.Recommend(value, host, options)
}

func (service Service) discover(ctx context.Context) (hardware.Capabilities, error) {
	if service.Hardware == nil {
		return hardware.Capabilities{}, hardware.ErrUnsupportedHost
	}
	return service.Hardware.Discover(ctx)
}

func (service Service) loadState(ctx context.Context) (state.Transaction, error) {
	if service.State == nil {
		return state.Transaction{}, state.ErrNoState
	}
	return service.State.Load(ctx)
}

func (service Service) ollamaComponents(ctx context.Context, alias string, result *app.Result) []app.Component {
	components := []app.Component{}
	if service.Ollama.API == nil {
		return append(components, component("ollama", "absent", false, map[string]any{}))
	}
	status, err := service.Ollama.API.Status(ctx)
	if err != nil || !status.Available {
		if err != nil {
			appendIssue(result, issueForError(err))
		}
		return append(components, component("ollama", "failed", false, map[string]any{}))
	}
	components = append(components, component("ollama", "ready", true, map[string]any{"version": status.Version}))
	if alias == "" {
		return components
	}
	diagnosis := service.Ollama.Diagnose(ctx, alias)
	for _, value := range diagnosis.Issues {
		appendIssue(result, appIssue(value))
	}
	if diagnosis.Verification != nil {
		verification := diagnosis.Verification
		components = append(components,
			component("local_alias", "ready", true, map[string]any{"model_name": alias}),
			component("runtime_context", "ready", diagnosis.ContextValid, map[string]any{"context_tokens": verification.ContextTokens}),
			component("runtime_tools", "ready", diagnosis.ToolsValid, map[string]any{"structured": verification.StructuredTool, "tool_name": verification.ToolName}),
			component("runtime_offload", "ready", diagnosis.OffloadKnown, map[string]any{"offload_percent": verification.OffloadPercent, "processor": verification.Processor}),
		)
	} else {
		components = append(components, component("local_alias", "degraded", false, map[string]any{"model_name": alias}))
	}
	return components
}

func (service Service) transactionResult(command app.Command, transaction state.Transaction, err error) app.Result {
	result := service.base(command)
	if transaction.TransactionID != "" {
		result.TransactionID = stringPointer(transaction.TransactionID)
		result.Components = append(result.Components, transactionComponent(transaction))
	}
	for _, value := range transaction.Issues {
		appendIssue(&result, appIssue(value))
	}
	for _, value := range transaction.ManualActions {
		if !value.Completed {
			result.ManualActions = append(result.ManualActions, app.ManualAction{
				ActionID: value.ActionID, IssueCode: app.IssueCode(value.IssueCode), Surface: string(value.Surface), Summary: value.Summary,
			})
		}
	}
	if err == nil && transaction.Status == state.StatusSucceeded {
		result.Outcome, result.ExitCode, result.Summary = app.OutcomeSuccess, app.ExitSuccess, "The transaction completed and all required machine verification passed."
		return result
	}
	if errors.Is(err, context.Canceled) {
		result.Outcome, result.ExitCode, result.Summary = app.OutcomeCancelled, app.ExitCancelled, "The transaction was cancelled and remains resumable."
		return result
	}
	if errors.Is(err, state.ErrConsentRequired) || transaction.Status == state.StatusAwaitingManual {
		if len(result.Issues) == 0 {
			appendIssue(&result, issue(app.IssueManualApproval, app.SeverityWarning, "policy", "Explicit consent or protected manual completion is required."))
		}
		if len(result.ManualActions) == 0 {
			result.ManualActions = append(result.ManualActions, app.ManualAction{
				ActionID: "terminal.confirm", IssueCode: app.IssueManualApproval, Surface: "terminal",
				Summary: "Review the planned host change and rerun with the explicit documented consent option.",
			})
		}
		result.Outcome, result.ExitCode, result.Summary = app.OutcomeManualRequired, app.ExitManualRequired, "The transaction paused for explicit consent or protected manual completion."
		return result
	}
	if err != nil && len(result.Issues) == 0 {
		appendIssue(&result, issueForError(err))
	}
	finalizeFailure(&result)
	return result
}

func (service Service) fromVSCodeResult(command app.Command, value vscode.Result) app.Result {
	result := service.base(command)
	result.Outcome, result.ExitCode, result.Summary = value.Outcome, value.ExitCode, value.Summary
	result.Issues = append(result.Issues, value.Issues...)
	result.ManualActions = append(result.ManualActions, value.ManualActions...)
	result.Components = append(result.Components, vscodeComponents(value.Status)...)
	return result
}

func (service Service) base(command app.Command) app.Result {
	now := time.Now().UTC()
	if service.Clock != nil {
		now = service.Clock.Now().UTC()
	}
	return app.Result{
		SchemaVersion: app.EventSchemaVersion, RecordType: "result", Timestamp: now, Command: command,
		Issues: []app.Issue{}, Components: []app.Component{}, ManualActions: []app.ManualAction{},
	}
}

func (service Service) failure(command app.Command, code app.IssueCode, summary string) app.Result {
	result := service.base(command)
	appendIssue(&result, issue(code, app.SeverityError, domainForIssue(code), summary))
	finalizeFailure(&result)
	return result
}

func (service Service) manual(command app.Command, code app.IssueCode, summary string) app.Result {
	result := service.base(command)
	appendIssue(&result, issue(code, app.SeverityWarning, domainForIssue(code), summary))
	result.ManualActions = append(result.ManualActions, app.ManualAction{
		ActionID: "terminal.confirm", IssueCode: code, Surface: "terminal", Summary: summary,
	})
	result.Outcome, result.ExitCode, result.Summary = app.OutcomeManualRequired, app.ExitManualRequired, summary
	return result
}

func (service Service) errorResult(command app.Command, err error) app.Result {
	result := service.base(command)
	appendIssue(&result, issueForError(err))
	finalizeFailure(&result)
	return result
}

func cloneFacts(input map[string]state.SafeScalar) map[string]state.SafeScalar {
	result := make(map[string]state.SafeScalar, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func aliasFor(id string) string {
	safe := regexp.MustCompile(`[^a-z0-9._-]+`).ReplaceAllString(strings.ToLower(id), "-")
	return "local-ai-" + strings.Trim(safe, "-") + ":latest"
}

func aliasFromTransaction(transaction state.Transaction) string {
	if transaction.GeneratedAlias != nil {
		return *transaction.GeneratedAlias
	}
	return ""
}

func removalRequest(transaction state.Transaction, name string, confirmed bool) ollama.RemovalRequest {
	request := ollama.RemovalRequest{Assets: transaction.ManagedAssets, Consent: confirmed}
	for _, asset := range transaction.ManagedAssets {
		if asset.Locator.Kind != state.LocatorModelName || asset.Locator.Value != name {
			continue
		}
		switch asset.AssetType {
		case state.AssetOllamaAlias:
			request.Alias = name
			request.AliasAssetID = asset.AssetID
		case state.AssetOllamaSourceModel:
			request.Source = name
			request.SourceAssetID = asset.AssetID
			request.RemoveSource = true
		}
	}
	return request
}

func configDigest(config app.Config) string {
	data, _ := json.Marshal(config)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func newUUID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		now := sha256.Sum256([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
		copy(value[:], now[:16])
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func normalizedVersion(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "v"))
	matched, _ := regexp.MatchString(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`, value)
	if matched {
		return value
	}
	return "0.0.0-dev"
}
