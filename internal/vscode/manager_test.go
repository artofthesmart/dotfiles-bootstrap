package vscode

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

type fakeLocator struct {
	executable  platform.Application
	application platform.Application
	execErr     error
	appErr      error
}

func (l fakeLocator) FindExecutable(string) (platform.Application, error) {
	return l.executable, l.execErr
}

func (l fakeLocator) FindApplication(string) (platform.Application, error) {
	return l.application, l.appErr
}

type immediateClock struct {
	now    time.Time
	sleeps int
}

func (c *immediateClock) Now() time.Time { return c.now }
func (c *immediateClock) Sleep(ctx context.Context, _ time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.sleeps++
	return nil
}

type writeSpyFileSystem struct {
	*testkit.MemoryFileSystem
	writes int
}

func (s *writeSpyFileSystem) WriteFile(name string, data []byte, mode fs.FileMode) error {
	s.writes++
	return s.MemoryFileSystem.WriteFile(name, data, mode)
}

func success(stdout string) testkit.CommandOutcome {
	return testkit.CommandOutcome{Result: platform.CommandResult{Stdout: []byte(stdout)}}
}

func failure(exit int, stderr string) testkit.CommandOutcome {
	return testkit.CommandOutcome{Result: platform.CommandResult{ExitCode: exit, Stderr: []byte(stderr)}}
}

func newTestManager(runner *testkit.ScriptedRunner, locator platform.ApplicationLocator, filesystem platform.FileSystem, clock Clock) *Manager {
	return NewManager(Options{
		Platform: platform.Capabilities{
			Runner:       runner,
			Applications: locator,
			FileSystem:   filesystem,
		},
		Clock:             clock,
		ReadinessAttempts: 3,
		ReadinessDelay:    time.Nanosecond,
	})
}

func pathLocator(path string) fakeLocator {
	return fakeLocator{executable: platform.Application{Name: "code", Executable: path, Source: "path"}}
}

func TestFirstRunCreatesProfileBeforeInstallingOfficialExtension(t *testing.T) {
	runner := testkit.NewScriptedRunner(
		success("1.127.0\ncommit\nx64\n"),
		failure(1, "Profile 'Local AI' was not found."),
		success(""),
		failure(1, "Profile 'Local AI' is still starting."),
		success(""),
		success(""),
		success("1.127.0\ncommit\nx64\n"),
		success("Ollama.ollama@0.0.13\n"),
	)
	clock := &immediateClock{now: time.Date(2026, 10, 20, 20, 0, 0, 0, time.UTC)}
	manager := newTestManager(runner, pathLocator("/opt/VS Code/bin/code"), testkit.NewMemoryFileSystem("/sandbox", nil), clock)

	result := manager.Configure(context.Background(), Request{Alias: "local-ai-devstral-24b:latest"})
	if result.Outcome != app.OutcomeManualRequired || result.ExitCode != app.ExitManualRequired {
		t.Fatalf("expected awaiting manual exit 3, got %+v", result)
	}
	if clock.sleeps != 1 {
		t.Fatalf("expected one bounded readiness wait, got %d", clock.sleeps)
	}
	calls := runner.Calls()
	createIndex, installIndex := -1, -1
	for index, call := range calls {
		if reflect.DeepEqual(call.Args, []string{"--new-window", "--profile", ProfileName}) {
			createIndex = index
		}
		if containsArg(call.Args, "--install-extension") {
			installIndex = index
			if containsArg(call.Args, BundledCopilotChatExtensionID) {
				t.Fatal("bundled Copilot Chat must never be marketplace-installed")
			}
			if !reflect.DeepEqual(call.Args, []string{"--profile", ProfileName, "--install-extension", OfficialOllamaExtensionID, "--force"}) {
				t.Fatalf("unexpected install args: %#v", call.Args)
			}
		}
	}
	if createIndex < 0 || installIndex <= createIndex {
		t.Fatalf("profile creation must precede installation: %#v", calls)
	}
}

func TestDiscoveryFallsBackToSupportedApplicationLocation(t *testing.T) {
	runner := testkit.NewScriptedRunner(
		success("1.127.2\ncommit\narm64\n"),
		success("Ollama.ollama@0.0.13\n"),
	)
	locator := fakeLocator{
		execErr:     platform.ErrApplicationNotFound,
		application: platform.Application{Name: "code", Executable: "/Applications/Visual Studio Code.app/Contents/MacOS/Electron", Source: "standard_location"},
	}
	manager := newTestManager(runner, locator, testkit.NewMemoryFileSystem("/sandbox", nil), &immediateClock{now: time.Now().UTC()})
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.DiscoverySource != "standard_location" || !status.ProfileReady {
		t.Fatalf("unexpected status: %+v", status)
	}
	if got := runner.Calls()[0].Name; got != locator.application.Executable {
		t.Fatalf("used %q, want %q", got, locator.application.Executable)
	}
}

func TestDiscoveryFindsCodeOutsidePATHInStandardLocation(t *testing.T) {
	filesystem := testkit.NewMemoryFileSystem("/usr", nil)
	if err := filesystem.MkdirAll("/usr/share/code/bin", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.WriteFile("/usr/share/code/bin/code", []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	locator := platform.NewStandardApplicationLocator(filesystem)
	locator.OS = "linux"
	locator.Environment = platform.NewEnvironmentBuilderFrom([]string{"PATH=/not-installed", "HOME=/usr/home"})
	runner := testkit.NewScriptedRunner(
		success("1.127.0\ncommit\nx64\n"),
		success("Ollama.ollama@0.0.13\n"),
	)
	manager := newTestManager(runner, locator, filesystem, &immediateClock{now: time.Now().UTC()})
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Executable != "/usr/share/code/bin/code" || status.DiscoverySource != "standard_location" {
		t.Fatalf("unexpected standard-location discovery: %+v", status)
	}
}

func TestOldVSCodeIsVersionGatedFromOfficialEvidence(t *testing.T) {
	runner := testkit.NewScriptedRunner(success("1.126.9\ncommit\nx64\n"))
	manager := newTestManager(runner, pathLocator("/usr/bin/code"), nil, &immediateClock{now: time.Now().UTC()})
	result := manager.Configure(context.Background(), Request{Alias: "local-ai-model:latest"})
	if result.ExitCode != app.ExitPrerequisiteFailure || result.Issues[0].Code != app.IssuePrerequisiteVersion {
		t.Fatalf("unexpected old-version result: %+v", result)
	}
	for _, call := range runner.Calls() {
		if containsArg(call.Args, "--install-extension") || containsArg(call.Args, "--new-window") {
			t.Fatalf("old VS Code must not be mutated: %#v", call)
		}
	}
}

func TestRerunReusesProfileAndDoesNotReinstallOrTouchSettings(t *testing.T) {
	runner := testkit.NewScriptedRunner(
		success("1.128.0\ncommit\nx64\n"),
		success("publisher.other@9.0.0\nOllama.ollama@0.0.14\n"),
		success(""),
	)
	memory := testkit.NewMemoryFileSystem("/sandbox", nil)
	spy := &writeSpyFileSystem{MemoryFileSystem: memory}
	manager := newTestManager(runner, pathLocator("/usr/bin/code"), spy, &immediateClock{now: time.Now().UTC()})
	result := manager.Configure(context.Background(), Request{Alias: "local-ai-model:latest"})
	if result.ExitCode != app.ExitManualRequired {
		t.Fatalf("unexpected rerun result: %+v", result)
	}
	if spy.writes != 0 {
		t.Fatalf("manager wrote %d files; settings must remain untouched", spy.writes)
	}
	calls := runner.Calls()
	if len(calls) != 3 || !reflect.DeepEqual(calls[2].Args, []string{"--new-window", "--profile", ProfileName}) {
		t.Fatalf("unexpected rerun calls: %#v", calls)
	}
	for _, call := range calls {
		if containsArg(call.Args, "--install-extension") || containsArg(call.Args, BundledCopilotChatExtensionID) || containsSubstring(call.Args, "settings") {
			t.Fatalf("rerun performed forbidden mutation: %#v", call)
		}
	}
}

func TestOptionalRepositoryWithSpacesIsOneExactArgument(t *testing.T) {
	repository := "/sandbox/repos/Project With Spaces"
	memory := testkit.NewMemoryFileSystem("/sandbox", nil)
	if err := memory.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := testkit.NewScriptedRunner(
		success("1.127.0\ncommit\nx64\n"),
		success("Ollama.ollama@0.0.13\n"),
		success(""),
	)
	manager := newTestManager(runner, pathLocator("/opt/Visual Studio Code/code"), memory, &immediateClock{now: time.Now().UTC()})
	result := manager.Open(context.Background(), Request{Alias: "local-ai-model:latest", Repository: repository})
	if result.ExitCode != app.ExitManualRequired {
		t.Fatalf("unexpected open result: %+v", result)
	}
	want := []string{"--new-window", "--profile", ProfileName, repository}
	if got := runner.Calls()[2].Args; !reflect.DeepEqual(got, want) {
		t.Fatalf("open args = %#v, want %#v", got, want)
	}
}

func TestProfileScopedCommandsPreserveIsolation(t *testing.T) {
	runner := testkit.NewScriptedRunner(
		success("1.127.0\ncommit\nx64\n"),
		success("Ollama.ollama@0.0.12\nGitHub.copilot-chat@9.9.9\n"),
		success(""),
		success("1.127.0\ncommit\nx64\n"),
		success("Ollama.ollama@0.0.13\n"),
		success(""),
	)
	manager := newTestManager(runner, pathLocator("/usr/bin/code"), nil, &immediateClock{now: time.Now().UTC()})
	result := manager.Configure(context.Background(), Request{Alias: "local-ai-model:latest"})
	if result.ExitCode != app.ExitManualRequired {
		t.Fatalf("unexpected configure result: %+v", result)
	}
	for _, call := range runner.Calls() {
		if containsArg(call.Args, "--list-extensions") || containsArg(call.Args, "--install-extension") {
			if !containsPair(call.Args, "--profile", ProfileName) {
				t.Fatalf("extension operation escaped Local AI profile: %#v", call)
			}
		}
		if containsArg(call.Args, BundledCopilotChatExtensionID) {
			t.Fatalf("bundled extension was used as a CLI argument: %#v", call)
		}
	}
}

func TestCLIDiagnosticsArePreservedBoundedAndRedacted(t *testing.T) {
	secret := "token=super-secret Authorization: Bearer abc123 /home/alice/project"
	runner := testkit.NewScriptedRunner(
		success("1.127.0\ncommit\nx64\n"),
		failure(7, secret),
	)
	manager := newTestManager(runner, pathLocator("/usr/bin/code"), nil, &immediateClock{now: time.Now().UTC()})
	status, err := manager.Status(context.Background())
	if err == nil || len(status.Diagnostics) != 1 {
		t.Fatalf("expected preserved diagnostic, status=%+v err=%v", status, err)
	}
	diagnostic := status.Diagnostics[0]
	if diagnostic.ExitCode != 7 || diagnostic.Operation != "list Local AI profile extensions" {
		t.Fatalf("unexpected diagnostic: %+v", diagnostic)
	}
	for _, forbidden := range []string{"super-secret", "abc123", "/home/alice"} {
		if strings.Contains(diagnostic.Stderr, forbidden) {
			t.Fatalf("diagnostic leaked %q: %q", forbidden, diagnostic.Stderr)
		}
	}
	if !strings.Contains(diagnostic.Stderr, "<redacted>") || !strings.Contains(diagnostic.Stderr, "~/project") {
		t.Fatalf("diagnostic lost useful redacted context: %q", diagnostic.Stderr)
	}
}

func TestCancellationAndTimeoutAreDistinctFailures(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		runner := testkit.NewScriptedRunner(testkit.CommandOutcome{Cancelled: true})
		manager := newTestManager(runner, pathLocator("/usr/bin/code"), nil, &immediateClock{now: time.Now().UTC()})
		result := manager.Configure(context.Background(), Request{Alias: "local-ai-model:latest"})
		if result.Outcome != app.OutcomeCancelled || result.ExitCode != app.ExitCancelled {
			t.Fatalf("unexpected cancellation result: %+v", result)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		runner := testkit.NewScriptedRunner(testkit.CommandOutcome{TimedOut: true, Result: platform.CommandResult{Stderr: []byte("timed out")}})
		manager := newTestManager(runner, pathLocator("/usr/bin/code"), nil, &immediateClock{now: time.Now().UTC()})
		result := manager.Configure(context.Background(), Request{Alias: "local-ai-model:latest"})
		if result.Outcome != app.OutcomeFailed || result.ExitCode != app.ExitVSCodeIntegrationFailure {
			t.Fatalf("unexpected timeout result: %+v", result)
		}
	})
}

func TestManualActionModelIsExactAndNeverReportsUISuccess(t *testing.T) {
	runner := testkit.NewScriptedRunner(
		success("1.127.0\ncommit\nx64\n"),
		success("Ollama.ollama@0.0.13\n"),
		success(""),
	)
	alias := "local-ai-devstral-24b:latest"
	manager := newTestManager(runner, pathLocator("/usr/bin/code"), nil, &immediateClock{now: time.Now().UTC()})
	result := manager.Open(context.Background(), Request{Alias: alias})
	if result.Outcome != app.OutcomeManualRequired || result.ExitCode != 3 {
		t.Fatalf("UI handoff must be awaiting_manual/3: %+v", result)
	}
	joined := ""
	for _, action := range result.ManualActions {
		joined += action.Summary + "\n"
	}
	for _, required := range []string{
		"Restricted Mode", "Developer: Reload Window", "Agent mode", "sparkle",
		alias, "Ollama: Refresh Models", "Ollama: Diagnose Models", "128",
		"virtualTools.threshold", "utility", "32K", "cloud", "approval",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("manual guidance missing %q:\n%s", required, joined)
		}
	}
}

func TestOfficialRequirementFixtureMatchesEncodedGate(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "local-ai-manager", "vscode", "ollama-extension-package-2026-10-20.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		EvidenceDate string `json:"evidence_date"`
		SourceURL    string `json:"source_url"`
		Publisher    string `json:"publisher"`
		Version      string `json:"version"`
		Engines      struct {
			VSCode string `json:"vscode"`
		} `json:"engines"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.EvidenceDate != OfficialRequirementEvidenceDate ||
		fixture.SourceURL != OfficialRequirementEvidenceURL ||
		fixture.Publisher != "Ollama" ||
		fixture.Version != MinimumOllamaExtensionVersion ||
		fixture.Engines.VSCode != "^"+MinimumVSCodeVersion {
		t.Fatalf("fixture and encoded requirement diverged: %+v", fixture)
	}
}

func TestVersionAndExtensionParsers(t *testing.T) {
	version, err := ParseVersion("1.127.3\ncommit\nx64\n")
	if err != nil || !version.AtLeast(mustVersion("1.127.0")) {
		t.Fatalf("unexpected version parse: %+v %v", version, err)
	}
	extensions := ParseExtensions("Ollama.ollama@0.0.13\r\npublisher.other@1.2.3\r\n")
	if len(extensions) != 2 || extensions[0].Version != "0.0.13" {
		t.Fatalf("unexpected extension parse: %+v", extensions)
	}
}

func TestExactLocalAliasIsRequired(t *testing.T) {
	for _, alias := range []string{"", "devstral:latest", "local-ai-model", "local-ai-model:", "local-ai-model:latest extra"} {
		t.Run(alias, func(t *testing.T) {
			manager := newTestManager(testkit.NewScriptedRunner(), pathLocator("/usr/bin/code"), nil, &immediateClock{now: time.Now().UTC()})
			result := manager.Configure(context.Background(), Request{Alias: alias})
			if result.ExitCode != app.ExitConfigurationFailure || result.Issues[0].Code != app.IssueConfigurationInvalid {
				t.Fatalf("alias %q was not rejected: %+v", alias, result)
			}
		})
	}
}

func TestMissingRepositoryIsRejectedWithoutLaunchingOrCloning(t *testing.T) {
	memory := testkit.NewMemoryFileSystem("/sandbox", nil)
	runner := testkit.NewScriptedRunner()
	manager := newTestManager(runner, pathLocator("/usr/bin/code"), memory, &immediateClock{now: time.Now().UTC()})
	result := manager.Open(context.Background(), Request{Alias: "local-ai-model:latest", Repository: "/sandbox/missing repo"})
	if result.Issues[0].Code != app.IssueConfigurationInvalid {
		t.Fatalf("unexpected repository failure: %+v", result)
	}
	if len(runner.Calls()) != 0 {
		t.Fatal("missing repository must not trigger clone, trust, or launch commands")
	}
}

func containsArg(args []string, value string) bool {
	for _, arg := range args {
		if arg == value {
			return true
		}
	}
	return false
}

func containsSubstring(args []string, value string) bool {
	for _, arg := range args {
		if strings.Contains(strings.ToLower(arg), strings.ToLower(value)) {
			return true
		}
	}
	return false
}

func containsPair(args []string, first, second string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == first && args[index+1] == second {
			return true
		}
	}
	return false
}
