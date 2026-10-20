package ollama

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

type lifecycleRunner struct {
	mu                   sync.Mutex
	api                  *fakeRuntimeAPI
	files                platform.FileSystem
	cancelAfterFirstPull bool
	pullCalls            int
	createCalls          int
	removeCalls          int
	modelfiles           [][]byte
}

func (runner *lifecycleRunner) Run(ctx context.Context, command platform.Command) (platform.CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return platform.CommandResult{}, err
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(command.Args) == 0 {
		return platform.CommandResult{ExitCode: 2}, nil
	}
	switch command.Args[0] {
	case "pull":
		runner.pullCalls++
		model := command.Args[1]
		runner.api.addModel(model, compatibleShow(131072), 5<<30)
		if runner.cancelAfterFirstPull && runner.pullCalls == 1 {
			return platform.CommandResult{ExitCode: -1}, context.Canceled
		}
	case "create":
		runner.createCalls++
		model := command.Args[1]
		if len(command.Args) != 4 || command.Args[2] != "-f" {
			return platform.CommandResult{ExitCode: 2}, nil
		}
		data, err := runner.files.ReadFile(command.Args[3])
		if err != nil {
			return platform.CommandResult{}, err
		}
		runner.modelfiles = append(runner.modelfiles, append([]byte(nil), data...))
		show := compatibleShow(131072)
		show.Modelfile = string(data)
		runner.api.addModel(model, show, 5<<30)
		if strings.HasPrefix(model, "local-ai-") {
			runner.api.mu.Lock()
			runner.api.ps = PSResponse{Models: []RunningModel{{
				Name:          model,
				ContextLength: RequiredContextTokens,
				Processor:     "100% GPU",
				Size:          5 << 30,
				SizeVRAM:      5 << 30,
			}}}
			runner.api.mu.Unlock()
		}
	case "rm":
		runner.removeCalls++
		runner.api.mu.Lock()
		delete(runner.api.models, command.Args[1])
		delete(runner.api.sizes, command.Args[1])
		runner.api.mu.Unlock()
	default:
		return platform.CommandResult{ExitCode: 2}, nil
	}
	return platform.CommandResult{ExitCode: 0}, nil
}

func TestInstallLifecycleReceiptsResumeAfterInterruptedPull(t *testing.T) {
	memory := testkit.NewMemoryFileSystem("/sandbox", nil)
	if err := memory.MkdirAll("/sandbox/data", 0o700); err != nil {
		t.Fatal(err)
	}
	api := newCompatibleAPI()
	runner := &lifecycleRunner{
		api:                  api,
		files:                memory,
		cancelAfterFirstPull: true,
	}
	manager := Manager{
		API:        api,
		Registry:   fakeRegistry{info: ManifestInfo{TotalBytes: 5 << 30, ContextTokens: 131072}},
		Runner:     runner,
		FileSystem: memory,
		Now: func() time.Time {
			return time.Date(2026, time.October, 20, 20, 0, 0, 0, time.UTC)
		},
	}
	free := uint64(20 << 30)
	request := InstallRequest{
		Source:           SourceSpec{Kind: SourceRegistry, Reference: "llama3.1:8b"},
		Alias:            "local-ai-llama31:latest",
		DataDirectory:    "/sandbox/data",
		FreeStorageBytes: &free,
	}
	steps := NewInstallSteps(manager, request)
	store := lifecycleStore(memory, "/sandbox/install.json", "${HOME}/.local-ai/install.json")
	engine := state.Engine{
		Store: store,
		Clock: testkit.NewFakeClock(time.Date(2026, time.October, 20, 20, 0, 0, 0, time.UTC)),
	}
	alias := request.Alias
	execute := state.ExecuteRequest{
		Spec: state.TransactionSpec{
			ManagerVersion:        "0.1.0",
			TransactionID:         "00000000-0000-4000-8000-000000000100",
			Command:               state.CommandModelsInstall,
			EffectiveConfigDigest: "sha256:" + strings.Repeat("a", 64),
			GeneratedAlias:        &alias,
			Host:                  lifecycleHost(),
		},
		Steps: steps,
		Options: state.ExecuteOptions{Consent: map[string]state.ConsentDecision{
			StepAcquireModel: state.ConsentAccepted,
			StepCreateAlias:  state.ConsentAccepted,
		}},
	}
	interrupted, err := engine.Execute(context.Background(), execute)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
	if interrupted.Status != state.StatusInterrupted ||
		interrupted.Steps[0].Phases.Apply.Status != state.PhaseRunning ||
		len(interrupted.ManagedAssets) != 0 {
		t.Fatalf("interrupted transaction recorded optimistic state: %#v", interrupted)
	}

	execute.Options.Resume = true
	resumed, err := engine.Execute(context.Background(), execute)
	if err != nil {
		t.Fatalf("resume error = %v", err)
	}
	if resumed.Status != state.StatusSucceeded {
		t.Fatalf("resumed status = %s", resumed.Status)
	}
	if runner.pullCalls != 1 || runner.createCalls != 1 {
		t.Fatalf("resume repeated acquisition: pulls=%d creates=%d", runner.pullCalls, runner.createCalls)
	}
	if len(resumed.ManagedAssets) != 2 {
		t.Fatalf("managed assets = %#v", resumed.ManagedAssets)
	}
	source, aliasAsset := resumed.ManagedAssets[0], resumed.ManagedAssets[1]
	if source.Ownership != state.OwnershipExternalDetected {
		t.Fatalf("interrupted source was optimistically adopted: %#v", source)
	}
	if aliasAsset.Ownership != state.OwnershipManagerOwned ||
		len(aliasAsset.Dependencies) != 1 ||
		aliasAsset.Dependencies[0] != source.AssetID {
		t.Fatalf("alias ownership/dependency = %#v", aliasAsset)
	}
	if len(runner.modelfiles) != 1 {
		t.Fatalf("Modelfiles = %d", len(runner.modelfiles))
	}
	modelfile := string(runner.modelfiles[0])
	if modelfile != "FROM llama3.1:8b\nPARAMETER num_ctx 65536\n" {
		t.Fatalf("alias Modelfile = %q", modelfile)
	}
	if resumed.Steps[2].Status != state.StepSkippedVerified ||
		resumed.Steps[2].Phases.Verify.Status != state.PhaseSucceeded {
		t.Fatalf("runtime verification receipt = %#v", resumed.Steps[2])
	}
}

func TestRemoveLifecycleWritesDestructiveReceiptsAndVerifiesAbsence(t *testing.T) {
	memory := testkit.NewMemoryFileSystem("/sandbox", nil)
	api := newCompatibleAPI()
	api.addModel("llama3.1:8b", compatibleShow(131072), 5<<30)
	api.addModel("local-ai-llama31:latest", compatibleShow(131072), 5<<30)
	runner := &lifecycleRunner{api: api, files: memory}
	manager := Manager{API: api, Runner: runner, FileSystem: memory}
	assets := []state.ManagedAsset{
		{
			AssetID:      "asset-source",
			AssetType:    state.AssetOllamaSourceModel,
			Ownership:    state.OwnershipManagerOwned,
			Locator:      state.AssetLocator{Kind: state.LocatorModelName, Value: "llama3.1:8b"},
			Dependencies: []string{},
		},
		{
			AssetID:      "asset-alias",
			AssetType:    state.AssetOllamaAlias,
			Ownership:    state.OwnershipManagerOwned,
			Locator:      state.AssetLocator{Kind: state.LocatorModelName, Value: "local-ai-llama31:latest"},
			Dependencies: []string{"asset-source"},
		},
	}
	request := RemovalRequest{
		Alias:         "local-ai-llama31:latest",
		Source:        "llama3.1:8b",
		AliasAssetID:  "asset-alias",
		SourceAssetID: "asset-source",
		Assets:        assets,
		RemoveSource:  true,
		Consent:       true,
	}
	steps := NewRemoveSteps(manager, request)
	engine := state.Engine{
		Store: lifecycleStore(memory, "/sandbox/remove.json", "${HOME}/.local-ai/remove.json"),
		Clock: testkit.NewFakeClock(time.Date(2026, time.October, 20, 21, 0, 0, 0, time.UTC)),
	}
	transaction, err := engine.Execute(context.Background(), state.ExecuteRequest{
		Spec: state.TransactionSpec{
			ManagerVersion:        "0.1.0",
			TransactionID:         "00000000-0000-4000-8000-000000000101",
			Command:               state.CommandModelsRemove,
			EffectiveConfigDigest: "sha256:" + strings.Repeat("b", 64),
			Host:                  lifecycleHost(),
		},
		Steps: steps,
		Options: state.ExecuteOptions{Consent: map[string]state.ConsentDecision{
			StepRemoveAlias:  state.ConsentAccepted,
			StepRemoveSource: state.ConsentAccepted,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if transaction.Status != state.StatusSucceeded || runner.removeCalls != 2 {
		t.Fatalf("remove transaction = %#v, calls=%d", transaction, runner.removeCalls)
	}
	for _, receipt := range transaction.Steps {
		if receipt.Status != state.StepVerified ||
			receipt.Phases.Apply.Status != state.PhaseSucceeded ||
			receipt.Phases.Verify.Status != state.PhaseSucceeded {
			t.Fatalf("removal receipt = %#v", receipt)
		}
	}

	if tags, err := api.Tags(context.Background()); err != nil || len(tags.Models) != 0 {
		t.Fatalf("models remain after verified removal: %#v err=%v", tags.Models, err)
	}
}

type failAliasApplyReceiptStore struct {
	state.Store
	failed bool
}

func (store *failAliasApplyReceiptStore) Save(ctx context.Context, transaction *state.Transaction) error {
	if !store.failed {
		for _, receipt := range transaction.Steps {
			if receipt.StepID == StepCreateAlias &&
				receipt.Phases.Apply.Status == state.PhaseSucceeded &&
				len(transaction.ManagedAssets) == 1 {
				store.failed = true
				return fmt.Errorf("%w: injected alias apply receipt failure", state.ErrPersistence)
			}
		}
	}
	return store.Store.Save(ctx, transaction)
}

func TestInstallLifecyclePersistenceFailureAfterAliasMutationResumesWithoutOptimisticOwnership(t *testing.T) {
	memory := testkit.NewMemoryFileSystem("/sandbox", nil)
	if err := memory.MkdirAll("/sandbox/data", 0o700); err != nil {
		t.Fatal(err)
	}
	api := newCompatibleAPI()
	runner := &lifecycleRunner{api: api, files: memory}
	manager := Manager{
		API:        api,
		Registry:   fakeRegistry{info: ManifestInfo{TotalBytes: 5 << 30, ContextTokens: 131072}},
		Runner:     runner,
		FileSystem: memory,
	}
	free := uint64(20 << 30)
	request := InstallRequest{
		Source:           SourceSpec{Kind: SourceRegistry, Reference: "llama3.1:8b"},
		Alias:            "local-ai-llama31:latest",
		DataDirectory:    "/sandbox/data",
		FreeStorageBytes: &free,
	}
	baseStore := lifecycleStore(memory, "/sandbox/persistence.json", "${HOME}/.local-ai/persistence.json")
	store := &failAliasApplyReceiptStore{Store: baseStore}
	engine := state.Engine{
		Store: store,
		Clock: testkit.NewFakeClock(time.Date(2026, time.October, 20, 22, 0, 0, 0, time.UTC)),
	}
	alias := request.Alias
	execute := state.ExecuteRequest{
		Spec: state.TransactionSpec{
			ManagerVersion:        "0.1.0",
			TransactionID:         "00000000-0000-4000-8000-000000000102",
			Command:               state.CommandModelsInstall,
			EffectiveConfigDigest: "sha256:" + strings.Repeat("d", 64),
			GeneratedAlias:        &alias,
			Host:                  lifecycleHost(),
		},
		Steps: NewInstallSteps(manager, request),
		Options: state.ExecuteOptions{Consent: map[string]state.ConsentDecision{
			StepAcquireModel: state.ConsentAccepted,
			StepCreateAlias:  state.ConsentAccepted,
		}},
	}
	interrupted, err := engine.Execute(context.Background(), execute)
	if !errors.Is(err, state.ErrPersistence) {
		t.Fatalf("Execute() error = %v, want ErrPersistence", err)
	}
	if interrupted.Status != state.StatusInterrupted ||
		interrupted.Steps[1].Phases.Apply.Status != state.PhaseRunning ||
		len(interrupted.ManagedAssets) != 1 {
		t.Fatalf("persistence failure recorded optimistic alias state: %#v", interrupted)
	}
	if runner.pullCalls != 1 || runner.createCalls != 1 {
		t.Fatalf("unexpected mutations before persistence failure: pulls=%d creates=%d", runner.pullCalls, runner.createCalls)
	}

	execute.Options.Resume = true
	resumed, err := engine.Execute(context.Background(), execute)
	if err != nil {
		t.Fatalf("resume error = %v", err)
	}
	if resumed.Status != state.StatusSucceeded || runner.pullCalls != 1 || runner.createCalls != 1 {
		t.Fatalf("resume repeated mutation: status=%s pulls=%d creates=%d", resumed.Status, runner.pullCalls, runner.createCalls)
	}
	for _, asset := range resumed.ManagedAssets {
		if asset.AssetType == state.AssetOllamaAlias && asset.Ownership == state.OwnershipManagerOwned {
			t.Fatalf("alias was optimistically manager-owned after lost apply receipt: %#v", asset)
		}
	}
}

func lifecycleStore(memory *testkit.MemoryFileSystem, path, privacyPath string) state.JSONStore {
	return state.JSONStore{
		FileSystem:           memory,
		Path:                 path,
		PrivacySafePath:      privacyPath,
		ArchivePrivacyPrefix: "${HOME}/.local-ai/archive",
	}
}

func lifecycleHost() state.Host {
	distribution := state.LinuxUbuntu
	return state.Host{
		OS:                state.HostLinux,
		Architecture:      state.ArchitectureAMD64,
		OSVersion:         "24.04",
		LinuxDistribution: &distribution,
		DesktopSession:    true,
		CapabilityDigest:  "sha256:" + strings.Repeat("c", 64),
	}
}
