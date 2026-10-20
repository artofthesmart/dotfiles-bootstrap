package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

var testTime = time.Date(2026, 10, 20, 20, 0, 0, 0, time.UTC)

func TestJSONStoreLoadsContractFixture(t *testing.T) {
	data := mustReadFixture(t, "../../testdata/local-ai-manager/transactions/planned.json")
	memory := testkit.NewMemoryFileSystem("/sandbox", nil)
	if err := memory.WriteFile("/sandbox/transaction.json", data, 0o600); err != nil {
		t.Fatal(err)
	}
	store := testStore(memory)

	transaction, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if transaction.SchemaVersion != TransactionSchemaVersion || transaction.Status != StatusPlanned {
		t.Fatalf("loaded unexpected transaction: %#v", transaction)
	}
}

func TestJSONStoreRejectsUnknownSchemaAndFields(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"schema": func(document map[string]any) {
			document["schema_version"] = "local-ai.transaction-state/v2"
		},
		"field": func(document map[string]any) {
			document["raw_output"] = "must not be accepted"
		},
	} {
		t.Run(name, func(t *testing.T) {
			document := transactionDocument(t)
			mutate(document)
			data, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			memory := testkit.NewMemoryFileSystem("/sandbox", nil)
			if err := memory.WriteFile("/sandbox/transaction.json", data, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = testStore(memory).Load(context.Background())
			if name == "schema" && !errors.Is(err, ErrUnsupportedSchema) {
				t.Fatalf("Load() error = %v, want ErrUnsupportedSchema", err)
			}
			if name == "field" && !errors.Is(err, ErrInvalidState) {
				t.Fatalf("Load() error = %v, want ErrInvalidState", err)
			}
		})
	}
}

func TestJSONStoreRunsRegisteredMigrationHook(t *testing.T) {
	document := transactionDocument(t)
	document["schema_version"] = "local-ai.transaction-state/v0"
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	memory := testkit.NewMemoryFileSystem("/sandbox", nil)
	if err := memory.WriteFile("/sandbox/transaction.json", data, 0o600); err != nil {
		t.Fatal(err)
	}
	store := testStore(memory)
	store.Migrators = []Migrator{schemaMigrator{from: "local-ai.transaction-state/v0"}}

	transaction, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if transaction.SchemaVersion != TransactionSchemaVersion {
		t.Fatalf("schema = %q", transaction.SchemaVersion)
	}
}

func TestAtomicSaveFailureAtEveryBoundaryLeavesValidState(t *testing.T) {
	firstSaveBoundaries := []failureBoundary{
		{"fs.sync", 1}, {"fs.write", 1}, {"fs.sync", 2},
		{"fs.rename", 1}, {"fs.sync", 3}, {"fs.sync", 4},
	}
	updateBoundaries := []failureBoundary{
		{"fs.sync", 5}, {"fs.write", 2}, {"fs.sync", 6},
		{"fs.write", 3}, {"fs.sync", 7}, {"fs.rename", 2},
		{"fs.sync", 8}, {"fs.sync", 9}, {"fs.rename", 3},
		{"fs.sync", 10}, {"fs.sync", 11},
	}
	for _, phase := range []testkit.FailurePhase{testkit.BeforeMutation, testkit.AfterMutation} {
		for _, boundary := range firstSaveBoundaries {
			name := string(phase) + "-initial-" + boundary.operation + "-" + itoa(boundary.occurrence)
			t.Run(name, func(t *testing.T) {
				injector := testkit.NewFailureInjector()
				injector.Inject(boundary.operation, phase, boundary.occurrence, nil)
				memory := testkit.NewMemoryFileSystem("/sandbox", injector)
				store := testStore(memory)
				transaction := baseTransaction()
				if err := store.Save(context.Background(), &transaction); err == nil {
					t.Fatal("Save() succeeded with injected failure")
				}
				assertActiveStateValidOrAbsent(t, store)
			})
		}
		for _, boundary := range updateBoundaries {
			name := string(phase) + "-update-" + boundary.operation + "-" + itoa(boundary.occurrence)
			t.Run(name, func(t *testing.T) {
				injector := testkit.NewFailureInjector()
				memory := testkit.NewMemoryFileSystem("/sandbox", injector)
				store := testStore(memory)
				transaction := baseTransaction()
				if err := store.Save(context.Background(), &transaction); err != nil {
					t.Fatal(err)
				}
				injector.Inject(boundary.operation, phase, boundary.occurrence, nil)
				if err := transaction.Transition(StatusRunning, testTime.Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
				if err := store.Save(context.Background(), &transaction); err == nil {
					t.Fatal("Save() succeeded with injected failure")
				}
				assertActiveStateValidOrAbsent(t, store)
				if previous, err := memory.ReadFile("/sandbox/transaction.json.previous"); err == nil && !json.Valid(previous) {
					t.Fatal("retained previous state is invalid JSON")
				}
			})
		}
		for _, phase := range []testkit.FailurePhase{testkit.BeforeMutation, testkit.AfterMutation} {
			t.Run("mkdir-"+string(phase), func(t *testing.T) {
				injector := testkit.NewFailureInjector()
				injector.Inject("fs.mkdir", phase, 1, nil)
				memory := testkit.NewMemoryFileSystem("/sandbox", injector)
				store := testStore(memory)
				store.Path = "/sandbox/state/transaction.json"
				if err := store.Save(context.Background(), ptrTransaction(baseTransaction())); err == nil {
					t.Fatal("Save() succeeded with injected directory creation failure")
				}
				if _, err := memory.ReadFile(store.Path); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("active state exists after failed directory creation: %v", err)
				}
			})
		}
	}
}

func TestAtomicSaveRetainsPreviousCopyAndDigest(t *testing.T) {
	memory := testkit.NewMemoryFileSystem("/sandbox", nil)
	store := testStore(memory)
	transaction := baseTransaction()
	if err := store.Save(context.Background(), &transaction); err != nil {
		t.Fatal(err)
	}
	original, err := memory.ReadFile("/sandbox/transaction.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Transition(StatusRunning, testTime.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), &transaction); err != nil {
		t.Fatal(err)
	}
	previous, err := memory.ReadFile("/sandbox/transaction.json.previous")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(previous, original) {
		t.Fatal("previous copy does not match prior durable state")
	}
	if transaction.PreviousState == nil || transaction.PreviousState.Digest != digest(original) {
		t.Fatalf("previous locator = %#v", transaction.PreviousState)
	}
	if unsynced := memory.UnsyncedPaths(); len(unsynced) != 0 {
		t.Fatalf("unsynced paths after successful save: %v", unsynced)
	}
}

func TestTransactionTransitionsRejectInvalidMovement(t *testing.T) {
	transaction := baseTransaction()
	if err := transaction.Transition(StatusSucceeded, testTime.Add(time.Minute)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Transition() error = %v", err)
	}
	if transaction.Status != StatusPlanned {
		t.Fatalf("status changed after invalid transition: %s", transaction.Status)
	}
	if err := transaction.Transition(StatusRunning, testTime.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Transition(StatusVerifying, testTime.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Transition(StatusSucceeded, testTime.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if transaction.CompletedAt == nil {
		t.Fatal("terminal transition did not set completed_at")
	}
}

func TestEngineDryRunDoesNotMutate(t *testing.T) {
	step := newScriptedStep("install.runtime", "asset-runtime")
	engine, _, _ := testEngine(t)
	transaction, err := engine.Execute(context.Background(), ExecuteRequest{
		Spec:    testSpec("00000000-0000-4000-8000-000000000010", CommandSetup, configDigest("a")),
		Steps:   []Step{step},
		Options: ExecuteOptions{DryRun: true},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if transaction.Status != StatusPlanned {
		t.Fatalf("status = %s, want planned", transaction.Status)
	}
	if step.applyCalls != 0 || step.verifyCalls != 0 || step.undoCalls != 0 || step.mutated {
		t.Fatalf("dry-run mutated: apply=%d verify=%d undo=%d mutated=%t", step.applyCalls, step.verifyCalls, step.undoCalls, step.mutated)
	}
	if transaction.Steps[0].Phases.Plan.Status != PhaseSucceeded || transaction.Steps[0].Phases.Apply.Status != PhaseNotRun {
		t.Fatalf("unexpected dry-run receipt: %#v", transaction.Steps[0])
	}
}

func TestEnginePersistsManagerOwnershipOnlyAfterVerification(t *testing.T) {
	step := newScriptedStep("install.runtime", "asset-runtime")
	engine, _, _ := testEngine(t)
	transaction, err := engine.Execute(context.Background(), ExecuteRequest{
		Spec:  testSpec("00000000-0000-4000-8000-000000000011", CommandSetup, configDigest("b")),
		Steps: []Step{step},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if transaction.Status != StatusSucceeded || transaction.Steps[0].Status != StepVerified {
		t.Fatalf("unexpected final state: %s / %s", transaction.Status, transaction.Steps[0].Status)
	}
	if len(transaction.ManagedAssets) != 1 || transaction.ManagedAssets[0].Ownership != OwnershipManagerOwned {
		t.Fatalf("managed assets = %#v", transaction.ManagedAssets)
	}
	if transaction.Steps[0].Phases.Verify.Status != PhaseSucceeded {
		t.Fatal("manager ownership was recorded without verification")
	}
}

func TestInterruptedMutationResumesIdempotentlyWithoutOptimisticReceipt(t *testing.T) {
	step := newScriptedStep("install.runtime", "asset-runtime")
	step.failures = testkit.NewFailureInjector()
	step.failures.Inject("step.apply", testkit.AfterMutation, 1, context.Canceled)
	step.recoverMutation = true
	engine, store, _ := testEngine(t)
	request := ExecuteRequest{
		Spec:  testSpec("00000000-0000-4000-8000-000000000012", CommandSetup, configDigest("c")),
		Steps: []Step{step},
	}
	transaction, err := engine.Execute(context.Background(), request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
	if transaction.Status != StatusInterrupted || transaction.Steps[0].Phases.Apply.Status != PhaseRunning {
		t.Fatalf("interrupted receipt is optimistic: %#v", transaction.Steps[0])
	}
	if len(transaction.ManagedAssets) != 0 {
		t.Fatalf("interrupted mutation claimed assets: %#v", transaction.ManagedAssets)
	}
	durable, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if durable.Steps[0].Phases.Apply.Status != PhaseRunning || len(durable.ManagedAssets) != 0 {
		t.Fatalf("durable interrupted receipt is optimistic: %#v", durable)
	}

	request.Options.Resume = true
	resumed, err := engine.Execute(context.Background(), request)
	if err != nil {
		t.Fatalf("resume error = %v", err)
	}
	if resumed.Status != StatusSucceeded || step.applyCalls != 1 {
		t.Fatalf("resume was not idempotent: status=%s apply_calls=%d", resumed.Status, step.applyCalls)
	}
	if resumed.Steps[0].Status != StepVerified || len(resumed.ManagedAssets) != 1 {
		t.Fatalf("resume did not verify recovered mutation: %#v", resumed)
	}
}

func TestMutationBoundaryFailuresAreResumableAndNotOptimistic(t *testing.T) {
	for _, phase := range []testkit.FailurePhase{testkit.BeforeMutation, testkit.AfterMutation} {
		t.Run("apply-"+string(phase), func(t *testing.T) {
			step := newScriptedStep("install.runtime", "asset-runtime")
			step.failures = testkit.NewFailureInjector()
			step.failures.Inject("step.apply", phase, 1, context.Canceled)
			step.recoverMutation = true
			engine, _, _ := testEngine(t)
			request := ExecuteRequest{
				Spec:  testSpec("00000000-0000-4000-8000-000000000019", CommandSetup, configDigest("3")),
				Steps: []Step{step},
			}
			interrupted, err := engine.Execute(context.Background(), request)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Execute() error = %v", err)
			}
			if interrupted.Status != StatusInterrupted || interrupted.Steps[0].Phases.Apply.Status != PhaseRunning || len(interrupted.ManagedAssets) != 0 {
				t.Fatalf("optimistic apply receipt after %s failure: %#v", phase, interrupted)
			}
			request.Options.Resume = true
			resumed, err := engine.Execute(context.Background(), request)
			if err != nil {
				t.Fatalf("resume error = %v", err)
			}
			if resumed.Status != StatusSucceeded || len(resumed.ManagedAssets) != 1 {
				t.Fatalf("resume state = %#v", resumed)
			}
			wantCalls := 2
			if phase == testkit.AfterMutation {
				wantCalls = 1
			}
			if step.applyCalls != wantCalls {
				t.Fatalf("apply calls = %d, want %d", step.applyCalls, wantCalls)
			}
		})

		t.Run("undo-"+string(phase), func(t *testing.T) {
			step := newScriptedStep("install.runtime", "asset-runtime")
			step.failures = testkit.NewFailureInjector()
			step.failures.Inject("step.undo", phase, 1, context.Canceled)
			failing := newScriptedStep("verify.failure", "asset-failure")
			failing.verifyErr = errors.New("verification failed")
			engine, _, _ := testEngine(t)
			request := ExecuteRequest{
				Spec:    testSpec("00000000-0000-4000-8000-000000000020", CommandSetup, configDigest("4")),
				Steps:   []Step{step, failing},
				Options: ExecuteOptions{RollbackOnFailure: true},
			}
			interrupted, err := engine.Execute(context.Background(), request)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Execute() error = %v", err)
			}
			if interrupted.Status != StatusInterrupted || interrupted.Rollback.Status != RollbackRunning || interrupted.Steps[0].Phases.Undo.Status != PhaseRunning {
				t.Fatalf("optimistic undo receipt after %s failure: %#v", phase, interrupted)
			}
			request.Options.Resume = true
			resumed, err := engine.Execute(context.Background(), request)
			if err != nil {
				t.Fatalf("rollback resume error = %v", err)
			}
			if resumed.Status != StatusRolledBack || len(resumed.ManagedAssets) != 0 || step.undoCalls != 2 {
				t.Fatalf("rollback resume state = %#v, undo calls = %d", resumed, step.undoCalls)
			}
		})
	}
}

func TestPersistenceFailureAfterMutationDoesNotCreateOptimisticReceipt(t *testing.T) {
	step := newScriptedStep("install.runtime", "asset-runtime")
	step.recoverMutation = true
	memory := testkit.NewMemoryFileSystem("/sandbox", nil)
	jsonStore := testStore(memory)
	store := &failNthStore{Store: jsonStore, failSave: 8}
	engine := Engine{Store: store, Clock: testkit.NewFakeClock(testTime)}
	request := ExecuteRequest{
		Spec:  testSpec("00000000-0000-4000-8000-000000000021", CommandSetup, configDigest("5")),
		Steps: []Step{step},
	}

	interrupted, err := engine.Execute(context.Background(), request)
	if !errors.Is(err, ErrPersistence) {
		t.Fatalf("Execute() error = %v, want ErrPersistence", err)
	}
	if !step.mutated || interrupted.Status != StatusInterrupted || interrupted.Steps[0].Phases.Apply.Status != PhaseRunning {
		t.Fatalf("persistence failure produced optimistic state: %#v", interrupted)
	}
	if len(interrupted.Steps[0].MutationAssetIDs) != 0 || len(interrupted.ManagedAssets) != 0 {
		t.Fatalf("persistence failure recorded unverified mutations: %#v", interrupted)
	}

	request.Options.Resume = true
	resumed, err := engine.Execute(context.Background(), request)
	if err != nil {
		t.Fatalf("resume error = %v", err)
	}
	if resumed.Status != StatusSucceeded || step.applyCalls != 1 || len(resumed.ManagedAssets) != 1 {
		t.Fatalf("resume did not recover persisted boundary safely: %#v", resumed)
	}
}

func TestResumeRejectsIncompatibleCommandFamilyAndConfig(t *testing.T) {
	for name, mutate := range map[string]func(*ExecuteRequest){
		"family": func(request *ExecuteRequest) {
			request.Spec.Command = CommandUninstall
		},
		"config": func(request *ExecuteRequest) {
			request.Spec.EffectiveConfigDigest = configDigest("d")
		},
	} {
		t.Run(name, func(t *testing.T) {
			step := newScriptedStep("install.runtime", "asset-runtime")
			step.cancelAfterMutation = true
			engine, _, _ := testEngine(t)
			request := ExecuteRequest{
				Spec:  testSpec("00000000-0000-4000-8000-000000000013", CommandSetup, configDigest("c")),
				Steps: []Step{step},
			}
			if _, err := engine.Execute(context.Background(), request); !errors.Is(err, context.Canceled) {
				t.Fatalf("initial error = %v", err)
			}
			request.Options.Resume = true
			mutate(&request)
			if _, err := engine.Execute(context.Background(), request); !errors.Is(err, ErrIncompatibleResume) {
				t.Fatalf("resume error = %v", err)
			}
		})
	}
}

func TestRestartArchivesPriorTransaction(t *testing.T) {
	first := newScriptedStep("install.runtime", "asset-runtime")
	engine, _, memory := testEngine(t)
	if _, err := engine.Execute(context.Background(), ExecuteRequest{
		Spec:  testSpec("00000000-0000-4000-8000-000000000014", CommandSetup, configDigest("e")),
		Steps: []Step{first},
	}); err != nil {
		t.Fatal(err)
	}
	second := newScriptedStep("inspect.runtime", "")
	second.contract.Risk = RiskReadOnly
	second.contract.Reversible = false
	second.contract.NotReversibleReason = ""
	transaction, err := engine.Execute(context.Background(), ExecuteRequest{
		Spec:    testSpec("00000000-0000-4000-8000-000000000015", CommandRepair, configDigest("f")),
		Steps:   []Step{second},
		Options: ExecuteOptions{Restart: true},
	})
	if err != nil {
		t.Fatalf("restart error = %v", err)
	}
	if transaction.TransactionID != "00000000-0000-4000-8000-000000000015" {
		t.Fatalf("active transaction = %s", transaction.TransactionID)
	}
	archivePath := "/sandbox/archive/00000000-0000-4000-8000-000000000014.json"
	archived, err := memory.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("archive missing: %v", err)
	}
	var prior Transaction
	if err := strictDecode(archived, &prior); err != nil {
		t.Fatalf("archive invalid: %v", err)
	}
	if prior.Status != StatusArchived {
		t.Fatalf("archived status = %s", prior.Status)
	}
}

func TestRollbackUsesOnlyVerifiedManagerMutationsAndRecordsPartialFailure(t *testing.T) {
	first := newScriptedStep("install.first", "asset-first")
	second := newScriptedStep("install.second", "asset-second")
	second.undoErr = errors.New("undo failed")
	third := newScriptedStep("install.third", "asset-third")
	third.verifyErr = errors.New("verification failed")
	engine, _, _ := testEngine(t)
	transaction, err := engine.Execute(context.Background(), ExecuteRequest{
		Spec:    testSpec("00000000-0000-4000-8000-000000000016", CommandSetup, configDigest("1")),
		Steps:   []Step{first, second, third},
		Options: ExecuteOptions{RollbackOnFailure: true},
	})
	if err == nil {
		t.Fatal("Execute() succeeded despite verification and rollback failures")
	}
	if transaction.Status != StatusRollbackFailed || transaction.Rollback.Status != RollbackPartial {
		t.Fatalf("rollback state = %s / %s", transaction.Status, transaction.Rollback.Status)
	}
	if first.undoCalls != 1 || second.undoCalls != 1 || third.undoCalls != 0 {
		t.Fatalf("unexpected undo calls: first=%d second=%d third=%d", first.undoCalls, second.undoCalls, third.undoCalls)
	}
	if len(transaction.ManagedAssets) != 1 || transaction.ManagedAssets[0].AssetID != "asset-second" {
		t.Fatalf("partial rollback retained wrong assets: %#v", transaction.ManagedAssets)
	}
}

func TestSensitiveFailureOutputIsNeverSerialized(t *testing.T) {
	step := newScriptedStep("install.runtime", "asset-runtime")
	step.applyErr = errors.New("stdout: token=super-secret raw_output")
	engine, _, memory := testEngine(t)
	_, err := engine.Execute(context.Background(), ExecuteRequest{
		Spec:  testSpec("00000000-0000-4000-8000-000000000017", CommandSetup, configDigest("2")),
		Steps: []Step{step},
	})
	if err == nil {
		t.Fatal("Execute() unexpectedly succeeded")
	}
	data, readErr := memory.ReadFile("/sandbox/transaction.json")
	if readErr != nil {
		t.Fatal(readErr)
	}
	lower := strings.ToLower(string(data))
	for _, forbidden := range []string{"super-secret", "raw_output", `"stdout"`} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("serialized state contains %q: %s", forbidden, data)
		}
	}

	transaction := baseTransaction()
	transaction.Issues = append(transaction.Issues, Issue{
		Code:         "LAI-INTERNAL-001",
		Severity:     SeverityError,
		Domain:       "internal",
		Summary:      "raw_output must never be persisted",
		Remediations: []Remediation{},
	})
	if err := transaction.Validate(); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestStructuredEventsUseOnlyAllowlistedFieldsAndStableSequence(t *testing.T) {
	var output bytes.Buffer
	writer := NewJSONEventWriter(&output)
	transactionID := "00000000-0000-4000-8000-000000000018"
	for _, message := range []EventMessage{MessageOperationStarted, MessageOperationCompleted} {
		if err := writer.Emit(Event{
			SchemaVersion: EventSchemaVersion,
			RecordType:    "event",
			Timestamp:     testTime,
			Command:       CommandSetup,
			TransactionID: &transactionID,
			Kind:          EventOperationStarted,
			Level:         EventInfo,
			Message:       message,
			Attributes:    EventAttributes{Status: "running"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("event lines = %d", len(lines))
	}
	for index, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if int(record["sequence"].(float64)) != index {
			t.Fatalf("sequence = %v, want %d", record["sequence"], index)
		}
		for _, forbidden := range []string{"stdout", "stderr", "raw_output", "prompt", "response"} {
			if strings.Contains(strings.ToLower(line), forbidden) {
				t.Fatalf("event contains forbidden field %q: %s", forbidden, line)
			}
		}
	}
	if err := writer.Emit(Event{
		SchemaVersion: EventSchemaVersion,
		RecordType:    "event",
		Timestamp:     testTime,
		Command:       CommandSetup,
		TransactionID: &transactionID,
		Kind:          EventIssue,
		Level:         EventError,
		Message:       EventMessage("stdout: secret"),
		Attributes:    EventAttributes{},
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Emit() error = %v", err)
	}
}

type failureBoundary struct {
	operation  string
	occurrence int
}

type schemaMigrator struct {
	from string
}

type failNthStore struct {
	Store
	saveCalls int
	failSave  int
}

func (s *failNthStore) Save(ctx context.Context, transaction *Transaction) error {
	s.saveCalls++
	if s.saveCalls == s.failSave {
		return testkit.ErrInjectedFailure
	}
	return s.Store.Save(ctx, transaction)
}

func (m schemaMigrator) FromSchema() string { return m.from }

func (m schemaMigrator) Migrate(_ context.Context, data []byte) ([]byte, error) {
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	document["schema_version"] = TransactionSchemaVersion
	return json.Marshal(document)
}

type scriptedStep struct {
	contract            StepContract
	assetID             string
	mutated             bool
	recoverMutation     bool
	cancelAfterMutation bool
	failures            *testkit.FailureInjector
	applyErr            error
	verifyErr           error
	undoErr             error
	checkCalls          int
	planCalls           int
	applyCalls          int
	verifyCalls         int
	undoCalls           int
}

func newScriptedStep(id, assetID string) *scriptedStep {
	return &scriptedStep{
		contract: StepContract{
			ID:              id,
			Owner:           OwnerState,
			Action:          "Apply a test-managed state mutation.",
			Risk:            RiskUserLocal,
			Reversible:      true,
			ProducedFactIDs: []string{id + ".ready"},
		},
		assetID: assetID,
	}
}

func (s *scriptedStep) Contract() StepContract { return s.contract }

func (s *scriptedStep) Check(_ context.Context, _ StepContext) (CheckResult, error) {
	s.checkCalls++
	result := CheckResult{
		AlreadySatisfied: s.mutated,
		Summary:          "The current state was checked.",
		Evidence:         []Evidence{},
		Facts:            map[string]SafeScalar{},
	}
	if s.mutated && s.recoverMutation && s.assetID != "" {
		result.RecoveredMutationAssetIDs = []string{s.assetID}
	}
	return result, nil
}

func (s *scriptedStep) Plan(_ context.Context, _ StepContext) (PlanResult, error) {
	s.planCalls++
	return PlanResult{Summary: "The mutation was planned.", Evidence: []Evidence{}}, nil
}

func (s *scriptedStep) Apply(_ context.Context, _ StepContext) (ApplyResult, error) {
	s.applyCalls++
	if s.failures != nil {
		if err := s.failures.Fail("step.apply", testkit.BeforeMutation); err != nil {
			return ApplyResult{}, err
		}
	}
	if s.applyErr != nil {
		return ApplyResult{}, s.applyErr
	}
	s.mutated = true
	if s.failures != nil {
		if err := s.failures.Fail("step.apply", testkit.AfterMutation); err != nil {
			return ApplyResult{}, err
		}
	}
	if s.cancelAfterMutation {
		s.cancelAfterMutation = false
		return ApplyResult{}, context.Canceled
	}
	ids := []string{}
	if s.assetID != "" {
		ids = append(ids, s.assetID)
	}
	return ApplyResult{Summary: "The mutation was applied.", Evidence: []Evidence{}, MutationAssetIDs: ids}, nil
}

func (s *scriptedStep) Verify(_ context.Context, _ StepContext) (VerifyResult, error) {
	s.verifyCalls++
	if s.verifyErr != nil {
		return VerifyResult{}, s.verifyErr
	}
	if !s.mutated && s.contract.Risk != RiskReadOnly {
		return VerifyResult{}, errors.New("mutation is absent")
	}
	result := VerifyResult{
		Summary:  "The postcondition was independently verified.",
		Evidence: []Evidence{},
		Facts:    map[string]SafeScalar{s.contract.ProducedFactIDs[0]: true},
		Assets:   []ManagedAsset{},
	}
	if s.assetID != "" {
		stepID := s.contract.ID
		result.Assets = append(result.Assets, ManagedAsset{
			AssetID:                s.assetID,
			AssetType:              AssetStateFile,
			DisplayName:            "Test managed state",
			Origin:                 OriginCreatedByManager,
			Ownership:              OwnershipManagerOwned,
			Locator:                AssetLocator{Kind: LocatorPath, Value: "${HOME}/.local-ai/test-state"},
			CreatedOrAdoptedByStep: &stepID,
			Dependencies:           []string{},
			Uninstall:              UninstallRule{Policy: UninstallRemoveWithManager, Reversible: true},
			Verification: []Evidence{{
				Kind:       EvidenceFileState,
				ObservedAt: testTime,
				Attributes: SafeAttributes{"state": "present"},
			}},
		})
	}
	return result, nil
}

func (s *scriptedStep) Undo(_ context.Context, _ StepContext) (UndoResult, error) {
	s.undoCalls++
	if s.failures != nil {
		if err := s.failures.Fail("step.undo", testkit.BeforeMutation); err != nil {
			return UndoResult{}, err
		}
	}
	if s.undoErr != nil {
		return UndoResult{}, s.undoErr
	}
	s.mutated = false
	if s.failures != nil {
		if err := s.failures.Fail("step.undo", testkit.AfterMutation); err != nil {
			return UndoResult{}, err
		}
	}
	return UndoResult{Summary: "The verified manager mutation was undone.", Evidence: []Evidence{}}, nil
}

func testEngine(t *testing.T) (Engine, JSONStore, *testkit.MemoryFileSystem) {
	t.Helper()
	memory := testkit.NewMemoryFileSystem("/sandbox", nil)
	store := testStore(memory)
	clock := testkit.NewFakeClock(testTime)
	return Engine{Store: store, Clock: clock}, store, memory
}

func testStore(memory *testkit.MemoryFileSystem) JSONStore {
	return JSONStore{
		FileSystem:           memory,
		Path:                 "/sandbox/transaction.json",
		PrivacySafePath:      "${HOME}/.local-ai/transaction.json",
		ArchivePrivacyPrefix: "${HOME}/.local-ai/archive",
	}
}

func testSpec(id string, command Command, digest string) TransactionSpec {
	distribution := LinuxUbuntu
	return TransactionSpec{
		ManagerVersion:        "0.1.0",
		TransactionID:         id,
		Command:               command,
		EffectiveConfigDigest: digest,
		Host: Host{
			OS:                HostLinux,
			Architecture:      ArchitectureAMD64,
			OSVersion:         "24.04",
			LinuxDistribution: &distribution,
			DesktopSession:    true,
			CapabilityDigest:  configDigest("9"),
		},
	}
}

func baseTransaction() Transaction {
	return newTransaction(
		testSpec("00000000-0000-4000-8000-000000000001", CommandSetup, configDigest("1")),
		nil,
		testTime,
	)
}

func configDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func transactionDocument(t *testing.T) map[string]any {
	t.Helper()
	data, err := json.Marshal(baseTransaction())
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func assertActiveStateValidOrAbsent(t *testing.T, store JSONStore) {
	t.Helper()
	transaction, err := store.Load(context.Background())
	if errors.Is(err, ErrNoState) {
		return
	}
	if err != nil {
		t.Fatalf("active state is neither valid nor absent: %v", err)
	}
	if err := transaction.Validate(); err != nil {
		t.Fatalf("active state is invalid: %v", err)
	}
}

func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

func ptrTransaction(transaction Transaction) *Transaction {
	return &transaction
}
