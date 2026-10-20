package manager

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
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
	service := Service{
		Clock: managerClock{}, FileSystem: filesystem,
		Build: app.BuildInfo{Version: "1.2.3", Commit: "token=super-secret"},
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
	for _, required := range []string{`"schema_version": "local-ai.support-report/v1"`, `"architecture": "amd64"`, `"system_memory_bytes"`} {
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
