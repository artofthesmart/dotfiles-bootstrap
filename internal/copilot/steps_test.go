package copilot

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

type interruptAfterLauncherWriteFS struct {
	*testkit.MemoryFileSystem
	interrupted bool
}

func (filesystem *interruptAfterLauncherWriteFS) WriteFileAtomic(name string, data []byte, mode fs.FileMode) error {
	if err := filesystem.MemoryFileSystem.WriteFileAtomic(name, data, mode); err != nil {
		return err
	}
	if !filesystem.interrupted && strings.HasSuffix(filepath.ToSlash(name), "/copilot-local") {
		filesystem.interrupted = true
		return context.Canceled
	}
	return nil
}

func TestConfigureStepsDryRunDoesNotInstallOrWriteLauncher(t *testing.T) {
	memory, paths := copilotTestFilesystem(t)
	external := filepath.Join(paths.Home, "tools", "copilot")
	if err := memory.MkdirAll(filepath.Dir(external), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := memory.WriteFile(external, []byte("external"), 0o700); err != nil {
		t.Fatal(err)
	}
	network := testkit.NewScriptedNetwork()
	manager := newCopilotTestManager(
		memory,
		paths,
		&versionRunner{version: "1.0.101"},
		network,
		fakeLocator{application: platform.Application{Name: "copilot", Executable: external, Source: "path"}},
		fakeOllama{version: "0.12.6", models: []string{"local-ai-model:latest"}},
	)
	engine := state.Engine{
		Store: state.JSONStore{
			FileSystem:           memory,
			Path:                 "/sandbox/state/dry-run.json",
			PrivacySafePath:      "${HOME}/.local-ai/dry-run.json",
			ArchivePrivacyPrefix: "${HOME}/.local-ai/archive",
		},
		Clock: testkit.NewFakeClock(time.Date(2026, time.October, 20, 20, 0, 0, 0, time.UTC)),
	}
	transaction, err := engine.Execute(context.Background(), state.ExecuteRequest{
		Spec:    copilotTransactionSpec("00000000-0000-4000-8000-000000000301"),
		Steps:   NewConfigureSteps(manager, Request{Alias: "local-ai-model:latest"}),
		Options: state.ExecuteOptions{DryRun: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if transaction.Status != state.StatusPlanned {
		t.Fatalf("dry-run transaction status = %s", transaction.Status)
	}
	if _, err := memory.Stat(LauncherPath(paths, "linux")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dry-run wrote launcher: %v", err)
	}
	if len(network.Calls()) != 0 {
		t.Fatal("dry-run downloaded Copilot CLI despite an existing supported executable")
	}
}

func TestConfigureStepsPersistReceiptsAndResumeInterruptedLauncher(t *testing.T) {
	base, paths := copilotTestFilesystem(t)
	filesystem := &interruptAfterLauncherWriteFS{MemoryFileSystem: base}
	external := filepath.Join(paths.Home, "tools", "copilot")
	if err := filesystem.MkdirAll(filepath.Dir(external), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.WriteFile(external, []byte("external"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := newCopilotTestManager(
		filesystem,
		paths,
		&versionRunner{version: "1.0.101"},
		testkit.NewScriptedNetwork(),
		fakeLocator{application: platform.Application{Name: "copilot", Executable: external, Source: "path"}},
		fakeOllama{version: "0.12.6", models: []string{"local-ai-model:latest"}},
	)
	store := state.JSONStore{
		FileSystem:           filesystem,
		Path:                 "/sandbox/state/copilot.json",
		PrivacySafePath:      "${HOME}/.local-ai/copilot.json",
		ArchivePrivacyPrefix: "${HOME}/.local-ai/archive",
	}
	engine := state.Engine{
		Store: store,
		Clock: testkit.NewFakeClock(time.Date(2026, time.October, 20, 20, 0, 0, 0, time.UTC)),
	}
	request := Request{Alias: "local-ai-model:latest"}
	execution := state.ExecuteRequest{
		Spec:  copilotTransactionSpec("00000000-0000-4000-8000-000000000302"),
		Steps: NewConfigureSteps(manager, request),
		Options: state.ExecuteOptions{Consent: map[string]state.ConsentDecision{
			StepInstallOfficial: state.ConsentAccepted,
		}},
	}
	interrupted, err := engine.Execute(context.Background(), execution)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute() error = %v, want context.Canceled", err)
	}
	if interrupted.Status != state.StatusInterrupted ||
		interrupted.Steps[1].Phases.Apply.Status != state.PhaseRunning {
		t.Fatalf("interrupted receipt = %#v", interrupted)
	}
	if len(interrupted.ManagedAssets) != 1 ||
		interrupted.ManagedAssets[0].Ownership != state.OwnershipExternalDetected {
		t.Fatalf("interruption recorded optimistic launcher ownership: %#v", interrupted.ManagedAssets)
	}

	execution.Steps = NewConfigureSteps(manager, request)
	execution.Options.Resume = true
	resumed, err := engine.Execute(context.Background(), execution)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status != state.StatusSucceeded || resumed.Steps[1].Status != state.StepVerified {
		t.Fatalf("resumed transaction = %#v", resumed)
	}
	if resumed.Steps[1].Phases.Apply.Status != state.PhaseSucceeded ||
		len(resumed.Steps[1].MutationAssetIDs) != 1 ||
		resumed.Steps[1].MutationAssetIDs[0] != CopilotLauncherAssetID {
		t.Fatalf("recovered apply receipt = %#v", resumed.Steps[1])
	}
	var launcher state.ManagedAsset
	for _, asset := range resumed.ManagedAssets {
		if asset.AssetType == state.AssetCopilotLauncher {
			launcher = asset
		}
	}
	if launcher.Ownership != state.OwnershipManagerOwned ||
		launcher.Locator.Value != "${HOME}/.local/bin/copilot-local" ||
		len(launcher.Dependencies) != 1 ||
		launcher.Dependencies[0] != CopilotApplicationAssetID {
		t.Fatalf("launcher asset = %#v", launcher)
	}
}

func copilotTransactionSpec(id string) state.TransactionSpec {
	distribution := state.LinuxUbuntu
	alias := "local-ai-model:latest"
	return state.TransactionSpec{
		ManagerVersion:        "0.1.0",
		TransactionID:         id,
		Command:               state.CommandCopilotConfigure,
		EffectiveConfigDigest: "sha256:" + strings.Repeat("a", 64),
		GeneratedAlias:        &alias,
		Host: state.Host{
			OS:                state.HostLinux,
			Architecture:      state.ArchitectureAMD64,
			OSVersion:         "24.04",
			LinuxDistribution: &distribution,
			DesktopSession:    true,
			CapabilityDigest:  "sha256:" + strings.Repeat("b", 64),
		},
	}
}
