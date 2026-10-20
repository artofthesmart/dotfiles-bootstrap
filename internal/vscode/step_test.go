package vscode

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

func TestConfigureStepPersistsInterruptedReceiptAndResumesConservatively(t *testing.T) {
	now := time.Date(2026, 10, 20, 21, 0, 0, 0, time.UTC)
	filesystem := testkit.NewMemoryFileSystem("/sandbox", nil)
	if err := filesystem.MkdirAll("/sandbox/state", 0o700); err != nil {
		t.Fatal(err)
	}
	store := state.JSONStore{
		FileSystem:           filesystem,
		Path:                 "/sandbox/state/current.json",
		PrivacySafePath:      "${HOME}/.local/state/local-ai/current.json",
		ArchivePrivacyPrefix: "${HOME}/.local/state/local-ai/archive",
	}
	engine := state.Engine{Store: store, Clock: testkit.NewFakeClock(now)}
	spec := state.TransactionSpec{
		ManagerVersion:        "1.0.0",
		TransactionID:         "00000000-0000-4000-8000-000000000101",
		Command:               state.CommandVSCodeConfigure,
		EffectiveConfigDigest: "sha256:" + strings.Repeat("a", 64),
		GeneratedAlias:        stringPointerForTest("local-ai-model:latest"),
		Host: state.Host{
			OS:                state.HostLinux,
			Architecture:      state.ArchitectureAMD64,
			OSVersion:         "ubuntu-24.04",
			LinuxDistribution: linuxDistributionPointer(state.LinuxUbuntu),
			DesktopSession:    true,
			CapabilityDigest:  "sha256:" + strings.Repeat("b", 64),
		},
	}

	firstRunner := testkit.NewScriptedRunner(
		success("1.127.0\ncommit\nx64\n"),
		failure(1, "Profile 'Local AI' was not found."),
		success("1.127.0\ncommit\nx64\n"),
		failure(1, "Profile 'Local AI' was not found."),
		success(""),
		success(""),
		testkit.CommandOutcome{Cancelled: true, Result: platform.CommandResult{Stderr: []byte("cancelled while marketplace completed installation")}},
	)
	firstManager := newTestManager(firstRunner, pathLocator("/usr/bin/code"), filesystem, &immediateClock{now: now})
	firstStep := &ConfigureStep{Manager: firstManager, Request: Request{Alias: "local-ai-model:latest"}}
	interrupted, err := engine.Execute(context.Background(), state.ExecuteRequest{
		Spec:  spec,
		Steps: []state.Step{firstStep},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got transaction=%+v err=%v", interrupted, err)
	}
	if interrupted.Status != state.StatusInterrupted || interrupted.Steps[0].Phases.Apply.Status != state.PhaseRunning {
		t.Fatalf("interrupted receipt was not persisted: %+v", interrupted)
	}

	resumeRunner := testkit.NewScriptedRunner(
		success("1.127.0\ncommit\nx64\n"),
		success("Ollama.ollama@0.0.13\n"),
		success("1.127.0\ncommit\nx64\n"),
		success("Ollama.ollama@0.0.13\n"),
	)
	resumeManager := newTestManager(resumeRunner, pathLocator("/usr/bin/code"), filesystem, &immediateClock{now: now.Add(time.Minute)})
	resumed, err := engine.Execute(context.Background(), state.ExecuteRequest{
		Spec:    spec,
		Steps:   []state.Step{&ConfigureStep{Manager: resumeManager, Request: Request{Alias: "local-ai-model:latest"}}},
		Options: state.ExecuteOptions{Resume: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Status != state.StatusSucceeded || resumed.Steps[0].Status != state.StepSkippedVerified {
		t.Fatalf("resume did not converge idempotently: %+v", resumed)
	}
	if len(resumed.ManagedAssets) != 3 {
		t.Fatalf("expected application/profile/extension receipts, got %+v", resumed.ManagedAssets)
	}
	for _, asset := range resumed.ManagedAssets {
		if asset.Ownership == state.OwnershipManagerOwned {
			t.Fatalf("uncertain interrupted mutation must be conservatively external: %+v", asset)
		}
	}
	if err := resumed.Validate(); err != nil {
		t.Fatalf("resumed transaction is invalid: %v", err)
	}
}

func TestLifecycleStepsPersistManualActionsAndResumeFromAwaitingManual(t *testing.T) {
	now := time.Date(2026, 10, 20, 22, 0, 0, 0, time.UTC)
	filesystem := testkit.NewMemoryFileSystem("/sandbox", nil)
	if err := filesystem.MkdirAll("/sandbox/state", 0o700); err != nil {
		t.Fatal(err)
	}
	store := state.JSONStore{
		FileSystem:           filesystem,
		Path:                 "/sandbox/state/manual.json",
		PrivacySafePath:      "${HOME}/.local/state/local-ai/manual.json",
		ArchivePrivacyPrefix: "${HOME}/.local/state/local-ai/archive",
	}
	engine := state.Engine{Store: store, Clock: testkit.NewFakeClock(now)}
	alias := "local-ai-model:latest"
	spec := state.TransactionSpec{
		ManagerVersion:        "1.0.0",
		TransactionID:         "00000000-0000-4000-8000-000000000102",
		Command:               state.CommandVSCodeConfigure,
		EffectiveConfigDigest: "sha256:" + strings.Repeat("c", 64),
		GeneratedAlias:        &alias,
		Host: state.Host{
			OS:                state.HostLinux,
			Architecture:      state.ArchitectureAMD64,
			OSVersion:         "ubuntu-24.04",
			LinuxDistribution: linuxDistributionPointer(state.LinuxUbuntu),
			DesktopSession:    true,
			CapabilityDigest:  "sha256:" + strings.Repeat("d", 64),
		},
	}
	request := Request{Alias: alias}
	firstRunner := testkit.NewScriptedRunner(
		success("1.127.0\ncommit\nx64\n"),
		success("Ollama.ollama@0.0.13\n"),
		success("1.127.0\ncommit\nx64\n"),
		success("Ollama.ollama@0.0.13\n"),
	)
	firstManager := newTestManager(firstRunner, pathLocator("/usr/bin/code"), filesystem, &immediateClock{now: now})
	awaiting, err := engine.Execute(context.Background(), state.ExecuteRequest{
		Spec:  spec,
		Steps: LifecycleSteps(firstManager, request, false),
	})
	if !errors.Is(err, state.ErrConsentRequired) {
		t.Fatalf("expected manual handoff, got transaction=%+v err=%v", awaiting, err)
	}
	if awaiting.Status != state.StatusAwaitingManual || awaiting.Steps[1].Status != state.StepAwaitingManual {
		t.Fatalf("UI-only work must persist awaiting_manual: %+v", awaiting)
	}
	if len(awaiting.ManualActions) != 8 {
		t.Fatalf("expected complete manual action model, got %+v", awaiting.ManualActions)
	}
	for _, action := range awaiting.ManualActions {
		if action.Completed || !strings.Contains(strings.Join(manualSummaries(awaiting.ManualActions), "\n"), alias) {
			t.Fatalf("unexpected pending action: %+v", action)
		}
	}

	resumeRunner := testkit.NewScriptedRunner(
		success("1.127.0\ncommit\nx64\n"),
		success("Ollama.ollama@0.0.13\n"),
		success("1.127.0\ncommit\nx64\n"),
		success("Ollama.ollama@0.0.13\n"),
	)
	resumeManager := newTestManager(resumeRunner, pathLocator("/usr/bin/code"), filesystem, &immediateClock{now: now.Add(time.Minute)})
	completed, err := engine.Execute(context.Background(), state.ExecuteRequest{
		Spec:    spec,
		Steps:   LifecycleSteps(resumeManager, request, true),
		Options: state.ExecuteOptions{Resume: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != state.StatusSucceeded {
		t.Fatalf("manual completion did not resume transaction: %+v", completed)
	}
	for _, action := range completed.ManualActions {
		if !action.Completed {
			t.Fatalf("completed manual action was not persisted: %+v", action)
		}
	}
}

func manualSummaries(actions []state.ManualAction) []string {
	result := make([]string, 0, len(actions))
	for _, action := range actions {
		result = append(result, action.Summary)
	}
	return result
}

func stringPointerForTest(value string) *string { return &value }

func linuxDistributionPointer(value state.LinuxDistribution) *state.LinuxDistribution { return &value }
