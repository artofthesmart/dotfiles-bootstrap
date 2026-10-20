package manager

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/copilot"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/install"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/ollama"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/vscode"
)

var managerTestTime = time.Date(2026, time.October, 20, 20, 0, 0, 0, time.UTC)

type managerClock struct{}

func (managerClock) Now() time.Time { return managerTestTime }

type fakeDiscoverer struct {
	value hardware.Capabilities
	err   error
}

func (discoverer fakeDiscoverer) Discover(context.Context) (hardware.Capabilities, error) {
	return discoverer.value, discoverer.err
}

type scriptedStep struct {
	contract state.StepContract
	checks   int
	plans    int
	applies  int
	verifies int
	applyErr error
}

func (step *scriptedStep) Contract() state.StepContract { return step.contract }
func (step *scriptedStep) Check(context.Context, state.StepContext) (state.CheckResult, error) {
	step.checks++
	return state.CheckResult{Facts: map[string]state.SafeScalar{}}, nil
}
func (step *scriptedStep) Plan(context.Context, state.StepContext) (state.PlanResult, error) {
	step.plans++
	return state.PlanResult{Summary: "planned"}, nil
}
func (step *scriptedStep) Apply(context.Context, state.StepContext) (state.ApplyResult, error) {
	step.applies++
	if step.applyErr != nil {
		return state.ApplyResult{}, step.applyErr
	}
	return state.ApplyResult{Summary: "applied"}, nil
}
func (step *scriptedStep) Verify(context.Context, state.StepContext) (state.VerifyResult, error) {
	step.verifies++
	facts := map[string]state.SafeScalar{}
	for _, id := range step.contract.ProducedFactIDs {
		facts[id] = true
	}
	return state.VerifyResult{Summary: "verified", Facts: facts, Assets: []state.ManagedAsset{}}, nil
}
func (step *scriptedStep) Undo(context.Context, state.StepContext) (state.UndoResult, error) {
	return state.UndoResult{Summary: "undone"}, nil
}

type panicStore struct{}

func (panicStore) Load(context.Context) (state.Transaction, error) {
	panic("dry run must not load state")
}

type noStatePanicMutationStore struct{}

func (noStatePanicMutationStore) Load(context.Context) (state.Transaction, error) {
	return state.Transaction{}, state.ErrNoState
}
func (noStatePanicMutationStore) Save(context.Context, *state.Transaction) error {
	panic("dry run must not save state")
}
func (noStatePanicMutationStore) Archive(context.Context) (state.StateLocator, error) {
	panic("dry run must not archive state")
}

type fixedStore struct {
	transaction state.Transaction
}

func (store fixedStore) Load(context.Context) (state.Transaction, error) {
	return store.transaction, nil
}
func (fixedStore) Save(context.Context, *state.Transaction) error { return nil }
func (fixedStore) Archive(context.Context) (state.StateLocator, error) {
	return state.StateLocator{}, nil
}

type fakeCopilotLifecycle struct {
	steps           []state.Step
	status          copilot.Status
	statusErr       error
	diagnosis       copilot.Diagnosis
	requests        []copilot.Request
	openCalls       int
	openArguments   []string
	openInput       io.Reader
	openOutput      io.Writer
	openError       io.Writer
	uninstallAssets []state.ManagedAsset
}

func (lifecycle *fakeCopilotLifecycle) ConfigureSteps(request copilot.Request) ([]state.Step, error) {
	lifecycle.requests = append(lifecycle.requests, request)
	return lifecycle.steps, nil
}

func (lifecycle *fakeCopilotLifecycle) Status(context.Context, copilot.Request) (copilot.Status, error) {
	return lifecycle.status, lifecycle.statusErr
}

func (lifecycle *fakeCopilotLifecycle) Diagnose(context.Context, copilot.Request) copilot.Diagnosis {
	return lifecycle.diagnosis
}

func (lifecycle *fakeCopilotLifecycle) Open(_ context.Context, _ copilot.Request, arguments []string, input io.Reader, output, errorOutput io.Writer) error {
	lifecycle.openCalls++
	lifecycle.openArguments = append([]string(nil), arguments...)
	lifecycle.openInput, lifecycle.openOutput, lifecycle.openError = input, output, errorOutput
	return nil
}

func (lifecycle *fakeCopilotLifecycle) Uninstall(_ context.Context, assets []state.ManagedAsset) error {
	lifecycle.uninstallAssets = append([]state.ManagedAsset(nil), assets...)
	return nil
}

type fakeUninstaller struct {
	assets []state.ManagedAsset
}

func (uninstaller *fakeUninstaller) Uninstall(_ context.Context, assets []state.ManagedAsset, _ install.UninstallOptions) (state.DeploymentReceipt, error) {
	uninstaller.assets = append([]state.ManagedAsset(nil), assets...)
	return state.DeploymentReceipt{}, nil
}
func (panicStore) Save(context.Context, *state.Transaction) error {
	panic("dry run must not save state")
}
func (panicStore) Archive(context.Context) (state.StateLocator, error) {
	panic("dry run must not archive state")
}

func TestDryRunPlansWithoutApplyVerifyOrStateMutation(t *testing.T) {
	step := &scriptedStep{contract: state.StepContract{
		ID: "test.mutation", Owner: state.OwnerApp, Action: "test mutation", Risk: state.RiskUserLocal,
		ConsentRequired: true, Reversible: false, NotReversibleReason: "test", ProducedFactIDs: []string{"test.ready"},
	}}
	service := Service{Clock: managerClock{}, State: panicStore{}}
	result := service.executeTransaction(context.Background(), app.Operation{
		Command: app.CommandRepair, Config: app.DefaultConfig(), DryRun: true,
	}, state.CommandRepair, []state.Step{step}, supportedCapabilities(), nil, nil)
	if result.ExitCode != app.ExitSuccess || result.Outcome != app.OutcomeSuccess {
		t.Fatalf("result = %#v", result)
	}
	if step.checks != 1 || step.plans != 1 || step.applies != 0 || step.verifies != 0 {
		t.Fatalf("step calls = check %d plan %d apply %d verify %d", step.checks, step.plans, step.applies, step.verifies)
	}
}

func TestTransactionManualPauseResumeAndRestart(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "restart"}[restart], func(t *testing.T) {
			filesystem := testkit.NewMemoryFileSystem("/sandbox", nil)
			store := state.JSONStore{
				FileSystem: filesystem, Path: "/sandbox/state/transaction.json",
				PrivacySafePath: "${HOME}/state/transaction.json", ArchivePrivacyPrefix: "${HOME}/state/archive",
			}
			service := Service{Clock: managerClock{}, State: store, Build: app.BuildInfo{Version: "1.2.3"}}
			firstStep := consentStep()
			first := service.executeTransaction(context.Background(), app.Operation{
				Command: app.CommandRepair, Config: app.DefaultConfig(),
			}, state.CommandRepair, []state.Step{firstStep}, supportedCapabilities(), nil, nil)
			if first.ExitCode != app.ExitManualRequired || first.Outcome != app.OutcomeManualRequired {
				t.Fatalf("first result = %#v", first)
			}

			secondStep := consentStep()
			operation := app.Operation{Command: app.CommandRepair, Config: app.DefaultConfig(), Yes: true, Resume: !restart, Restart: restart}
			second := service.executeTransaction(context.Background(), operation, state.CommandRepair, []state.Step{secondStep}, supportedCapabilities(), nil, nil)
			if second.ExitCode != app.ExitSuccess || second.Outcome != app.OutcomeSuccess {
				t.Fatalf("second result = %#v", second)
			}
			if secondStep.applies != 1 || secondStep.verifies != 1 {
				t.Fatalf("second step calls = %#v", secondStep)
			}
			if restart {
				entries := 0
				for _, name := range []string{"/sandbox/state/archive"} {
					if _, err := filesystem.Stat(name); err == nil {
						entries++
					}
				}
				if entries != 1 {
					t.Fatal("restart did not create the transaction archive")
				}
			}
		})
	}
}

func TestCancellationMapsToStableCancelledResult(t *testing.T) {
	filesystem := testkit.NewMemoryFileSystem("/sandbox", nil)
	store := state.JSONStore{
		FileSystem: filesystem, Path: "/sandbox/state/transaction.json",
		PrivacySafePath: "${HOME}/state/transaction.json", ArchivePrivacyPrefix: "${HOME}/state/archive",
	}
	step := consentStep()
	step.applyErr = context.Canceled
	service := Service{Clock: managerClock{}, State: store, Build: app.BuildInfo{Version: "1.2.3"}}
	result := service.executeTransaction(context.Background(), app.Operation{
		Command: app.CommandRepair, Config: app.DefaultConfig(), Yes: true,
	}, state.CommandRepair, []state.Step{step}, supportedCapabilities(), nil, nil)
	if result.ExitCode != app.ExitCancelled || result.Outcome != app.OutcomeCancelled {
		t.Fatalf("result = %#v", result)
	}
}

func TestSupportReportIsAllowlistedAndRedacted(t *testing.T) {
	filesystem := testkit.NewMemoryFileSystem("/sandbox", nil)
	lifecycle := &fakeCopilotLifecycle{diagnosis: copilot.Diagnosis{
		Status: copilot.Status{CopilotPresent: true, VersionSupported: true, LauncherPresent: true, LauncherValid: true, OfflineEnforced: true, AliasReady: true},
		Issues: []app.Issue{issue(app.IssueCopilotLauncher, app.SeverityError, "copilot", "redacted diagnostic")},
	}}
	service := Service{
		Clock: managerClock{}, FileSystem: filesystem,
		Build:   app.BuildInfo{Version: "1.2.3", Commit: "token=super-secret"},
		Copilot: lifecycle,
		Hardware: fakeDiscoverer{value: hardware.Capabilities{
			OS:           hardware.OperatingSystem{Family: "linux", Distribution: "ubuntu", Version: "24.04-hostname-private"},
			Architecture: "amd64",
			CPU:          hardware.Processor{Model: "username@example private CPU"},
			Memory:       hardware.Memory{InstalledBytes: 32 << 30},
			GPUs:         []hardware.GPU{{Model: "serial-1234", Acceleration: hardware.AccelerationCUDA}},
			Acceleration: hardware.AccelerationCUDA,
			Desktop:      hardware.Desktop{Available: true},
			Storage:      hardware.Storage{Volume: "/home/private-user/repository", FreeBytes: 100 << 30},
		}},
	}
	path := "/sandbox/report.json"
	result := service.report(context.Background(), app.Operation{
		Command: app.CommandReport, Config: app.DefaultConfig(), Options: app.OperationOptions{OutputPath: path},
	})
	if result.ExitCode != app.ExitSuccess {
		t.Fatalf("result = %#v", result)
	}
	data, err := filesystem.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"super-secret", "private-user", "repository", "username@example", "serial-1234", "token="} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("report contains forbidden value %q:\n%s", forbidden, text)
		}
	}
	for _, required := range []string{`"schema_version": "local-ai.support-report/v1"`, `"architecture": "amd64"`, `"system_memory_bytes"`, `"copilot_present": true`, string(app.IssueCopilotLauncher)} {
		if !strings.Contains(text, required) {
			t.Fatalf("report missing %q:\n%s", required, text)
		}
	}
}

func TestCopilotCommandsUseTypedOptionalLifecycle(t *testing.T) {
	service := Service{Clock: managerClock{}}
	for _, command := range []app.Command{app.CommandCopilotConfigure, app.CommandCopilotOpen} {
		result := service.Execute(context.Background(), app.Operation{Command: command, Config: app.DefaultConfig()})
		if result.ExitCode != app.ExitPrerequisiteFailure || len(result.Issues) != 1 || result.Issues[0].Code != app.IssuePrerequisiteMissing {
			t.Fatalf("%s result = %#v", command, result)
		}
	}
}

func TestCopilotConfigureUsesGeneratedAliasEndpointAndTransaction(t *testing.T) {
	filesystem := testkit.NewMemoryFileSystem("/sandbox", nil)
	step := consentStep()
	lifecycle := &fakeCopilotLifecycle{steps: []state.Step{step}}
	service := Service{
		Clock: managerClock{}, Copilot: lifecycle, Hardware: fakeDiscoverer{value: supportedCapabilities()},
		State: state.JSONStore{
			FileSystem: filesystem, Path: "/sandbox/state/transaction.json",
			PrivacySafePath: "${HOME}/state/transaction.json", ArchivePrivacyPrefix: "${HOME}/state/archive",
		},
		Build: app.BuildInfo{Version: "1.2.3"},
	}
	config := app.DefaultConfig()
	config.Model.CatalogID = "qwen.test"
	config.Ollama.Endpoint = "http://127.0.0.1:11435"
	result := service.Execute(context.Background(), app.Operation{
		Command: app.CommandCopilotConfigure, Config: config, Yes: true,
	})
	if result.ExitCode != app.ExitSuccess || step.applies != 1 || step.verifies != 1 {
		t.Fatalf("result = %#v, step = %#v", result, step)
	}
	if len(lifecycle.requests) != 1 || lifecycle.requests[0].Alias != "local-ai-qwen.test:latest" ||
		lifecycle.requests[0].Endpoint != config.Ollama.Endpoint {
		t.Fatalf("requests = %#v", lifecycle.requests)
	}
}

func TestCopilotConfigureDryRunDoesNotMutate(t *testing.T) {
	step := consentStep()
	lifecycle := &fakeCopilotLifecycle{steps: []state.Step{step}}
	service := Service{
		Clock: managerClock{}, Copilot: lifecycle, Hardware: fakeDiscoverer{value: supportedCapabilities()},
		State: noStatePanicMutationStore{},
	}
	config := app.DefaultConfig()
	config.Model.CatalogID = "qwen.test"
	result := service.Execute(context.Background(), app.Operation{
		Command: app.CommandCopilotConfigure, Config: config, DryRun: true,
	})
	if result.ExitCode != app.ExitSuccess || step.checks != 1 || step.plans != 1 || step.applies != 0 || step.verifies != 0 {
		t.Fatalf("result = %#v, step = %#v", result, step)
	}
}

func TestCopilotOpenDryRunAndArgumentForwarding(t *testing.T) {
	lifecycle := &fakeCopilotLifecycle{status: healthyCopilotStatus("local-ai-qwen.test:latest")}
	service := Service{Clock: managerClock{}, Copilot: lifecycle, State: noStatePanicMutationStore{}}
	config := app.DefaultConfig()
	config.Model.CatalogID = "qwen.test"
	arguments := []string{"prompt with spaces", "--flag=value", `literal"$*'argument`}
	dryRun := service.Execute(context.Background(), app.Operation{
		Command: app.CommandCopilotOpen, Config: config, DryRun: true, Arguments: arguments,
	})
	if dryRun.ExitCode != app.ExitSuccess || lifecycle.openCalls != 0 {
		t.Fatalf("dry-run result = %#v, calls = %d", dryRun, lifecycle.openCalls)
	}
	var input bytes.Buffer
	var output, errorOutput bytes.Buffer
	result := service.Execute(context.Background(), app.Operation{
		Command: app.CommandCopilotOpen, Config: config, Arguments: arguments,
		Input: &input, Output: &output, Error: &errorOutput,
	})
	if result.ExitCode != app.ExitSuccess || lifecycle.openCalls != 1 {
		t.Fatalf("result = %#v, calls = %d", result, lifecycle.openCalls)
	}
	if strings.Join(lifecycle.openArguments, "\x00") != strings.Join(arguments, "\x00") ||
		lifecycle.openInput != &input || lifecycle.openOutput != &output || lifecycle.openError != &errorOutput {
		t.Fatalf("forwarded open call = %#v", lifecycle)
	}
}

func TestCopilotMissingAliasDiagnosticsRepairAndUninstall(t *testing.T) {
	t.Run("missing alias", func(t *testing.T) {
		service := Service{Clock: managerClock{}, Copilot: &fakeCopilotLifecycle{}, State: noStatePanicMutationStore{}, Hardware: fakeDiscoverer{value: supportedCapabilities()}}
		result := service.Execute(context.Background(), app.Operation{Command: app.CommandCopilotConfigure, Config: app.DefaultConfig()})
		if result.ExitCode != app.ExitRuntimeVerificationFailure || len(result.Issues) != 1 || result.Issues[0].Code != app.IssueCopilotAlias {
			t.Fatalf("result = %#v", result)
		}
	})

	t.Run("diagnostics and repair", func(t *testing.T) {
		step := consentStep()
		copilotIssue := issue(app.IssueCopilotLauncher, app.SeverityError, "copilot", "The launcher needs repair.")
		lifecycle := &fakeCopilotLifecycle{
			steps:  []state.Step{step},
			status: copilot.Status{Alias: "local-ai-qwen.test:latest", Issues: []app.Issue{copilotIssue}},
		}
		service := Service{
			Clock: managerClock{}, Copilot: lifecycle, State: noStatePanicMutationStore{},
			Hardware: fakeDiscoverer{value: supportedCapabilities()},
		}
		config := app.DefaultConfig()
		config.Model.CatalogID = "qwen.test"
		doctor := service.Execute(context.Background(), app.Operation{Command: app.CommandDoctor, Config: config})
		if doctor.ExitCode != app.ExitPrerequisiteFailure || !containsIssue(doctor.Issues, app.IssueCopilotLauncher) {
			t.Fatalf("doctor = %#v", doctor)
		}
		repair := service.Execute(context.Background(), app.Operation{Command: app.CommandRepair, Config: config, DryRun: true})
		if repair.ExitCode != app.ExitSuccess || step.plans != 1 {
			t.Fatalf("repair = %#v, step = %#v", repair, step)
		}
	})

	t.Run("ownership-aware uninstall", func(t *testing.T) {
		copilotAsset := state.ManagedAsset{AssetType: state.AssetCopilotLauncher, Ownership: state.OwnershipManagerOwned}
		managerAsset := state.ManagedAsset{AssetType: state.AssetManagerBinary, Ownership: state.OwnershipManagerOwned}
		lifecycle := &fakeCopilotLifecycle{}
		uninstaller := &fakeUninstaller{}
		service := Service{
			Clock: managerClock{}, Copilot: lifecycle, Uninstaller: uninstaller,
			State: fixedStore{transaction: state.Transaction{ManagedAssets: []state.ManagedAsset{copilotAsset, managerAsset}}},
		}
		result := service.Execute(context.Background(), app.Operation{Command: app.CommandUninstall, Config: app.DefaultConfig()})
		if result.ExitCode != app.ExitSuccess || len(lifecycle.uninstallAssets) != 2 {
			t.Fatalf("result = %#v, copilot assets = %#v", result, lifecycle.uninstallAssets)
		}
		if len(uninstaller.assets) != 1 || uninstaller.assets[0].AssetType != state.AssetManagerBinary {
			t.Fatalf("generic uninstall assets = %#v", uninstaller.assets)
		}
	})
}

func TestSetupSequencesCopilotAfterVerifiedAlias(t *testing.T) {
	copilotStep := &scriptedStep{contract: state.StepContract{ID: "copilot.configure", Owner: state.OwnerCopilot, Action: "configure", Risk: state.RiskUserLocal, Reversible: true}}
	lifecycle := &fakeCopilotLifecycle{steps: []state.Step{copilotStep}}
	service := Service{Copilot: lifecycle, VSCode: &vscode.Manager{}}
	config := app.DefaultConfig()
	config.Ollama.Endpoint = "http://127.0.0.1:11435"
	steps, err := service.setupLifecycleSteps(app.Operation{Config: config}, ollama.InstallRequest{
		Source: ollama.SourceSpec{Kind: ollama.SourceRegistry, Reference: "example/model:latest"},
		Alias:  "local-ai-example:latest",
	}, state.Transaction{})
	if err != nil {
		t.Fatal(err)
	}
	verifyIndex, copilotIndex := -1, -1
	for index, step := range steps {
		switch step.Contract().ID {
		case ollama.StepVerifyModel:
			verifyIndex = index
		case "copilot.configure":
			copilotIndex = index
		}
	}
	manualIndex := -1
	for index, step := range steps {
		if step.Contract().ID == "vscode.complete-manual-handoff" {
			manualIndex = index
		}
	}
	if verifyIndex < 0 || copilotIndex <= verifyIndex || manualIndex <= copilotIndex {
		t.Fatalf("step order verify=%d copilot=%d total=%d", verifyIndex, copilotIndex, len(steps))
	}
	if len(lifecycle.requests) != 1 || lifecycle.requests[0].Alias != "local-ai-example:latest" || lifecycle.requests[0].Endpoint != config.Ollama.Endpoint {
		t.Fatalf("request = %#v", lifecycle.requests)
	}
}

func healthyCopilotStatus(alias string) copilot.Status {
	return copilot.Status{
		CopilotPresent: true, VersionSupported: true, Alias: alias, AliasReady: true, OllamaReady: true,
		LauncherPresent: true, LauncherValid: true, LauncherOwnership: state.OwnershipManagerOwned,
		ProviderValid: true, OfflineEnforced: true, ProviderType: "openai", WireAPI: "responses",
	}
}

func containsIssue(issues []app.Issue, code app.IssueCode) bool {
	for _, value := range issues {
		if value.Code == code {
			return true
		}
	}
	return false
}

func consentStep() *scriptedStep {
	return &scriptedStep{contract: state.StepContract{
		ID: "test.consent", Owner: state.OwnerApp, Action: "test consent", Risk: state.RiskUserLocal,
		ConsentRequired: true, Reversible: false, NotReversibleReason: "test", ProducedFactIDs: []string{"test.ready"},
	}}
}

func supportedCapabilities() hardware.Capabilities {
	return hardware.Capabilities{
		OS:           hardware.OperatingSystem{Family: "linux", Distribution: "ubuntu", Version: "24.04"},
		Architecture: "amd64", Memory: hardware.Memory{InstalledBytes: 32 << 30},
		Acceleration: hardware.AccelerationCPU, Desktop: hardware.Desktop{Available: true},
		Storage: hardware.Storage{Volume: "/", TotalBytes: 200 << 30, FreeBytes: 100 << 30},
	}
}
