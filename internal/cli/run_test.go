package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

var testTime = time.Date(2026, time.October, 20, 19, 30, 0, 0, time.UTC)

type fixedClock struct{}

func (fixedClock) Now() time.Time {
	return testTime
}

type panicFileSystem struct {
	platform.FileSystem
}

type panicRunner struct{}

func (panicRunner) Run(context.Context, platform.Command) (platform.CommandResult, error) {
	panic("runner must not be called by scaffold commands")
}

type panicNetwork struct{}

func (panicNetwork) Do(context.Context, platform.Request) (platform.Response, error) {
	panic("network must not be called by scaffold commands")
}

func testDependencies(stdout, stderr *bytes.Buffer) Dependencies {
	return Dependencies{
		Stdout:  stdout,
		Stderr:  stderr,
		Clock:   fixedClock{},
		FS:      panicFileSystem{},
		Runner:  panicRunner{},
		Network: panicNetwork{},
		Build: app.BuildInfo{
			Version: "1.2.3",
			Commit:  "abc123",
			Date:    "2026-10-20T19:00:00Z",
		},
	}
}

func TestVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exit := Run(context.Background(), []string{"version"}, testDependencies(&stdout, &stderr))

	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	want := "local-ai 1.2.3\ncommit: abc123\nbuilt: 2026-10-20T19:00:00Z\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestHelpListsStableCommands(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exit := Run(context.Background(), []string{"help"}, testDependencies(&stdout, &stderr))

	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	for _, command := range []string{
		"setup", "status", "doctor", "repair", "models list",
		"models recommend", "models install", "models remove",
		"vscode configure", "vscode open", "verify", "update",
		"uninstall", "report", "version",
	} {
		if !strings.Contains(stdout.String(), command) {
			t.Errorf("help does not contain %q", command)
		}
	}
}

func TestPlaceholderJSONResult(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exit := Run(context.Background(), []string{"models", "list", "--json"}, testDependencies(&stdout, &stderr))

	if exit != int(app.ExitInternalFailure) {
		t.Fatalf("exit = %d, want %d", exit, app.ExitInternalFailure)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var result app.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if result.SchemaVersion != app.EventSchemaVersion || result.RecordType != "result" {
		t.Fatalf("unexpected result envelope: %#v", result)
	}
	if result.Command != app.CommandModelsList {
		t.Fatalf("command = %q, want %q", result.Command, app.CommandModelsList)
	}
	if result.Timestamp != testTime {
		t.Fatalf("timestamp = %s, want %s", result.Timestamp, testTime)
	}
	if result.Outcome != app.OutcomeFailed || result.ExitCode != app.ExitInternalFailure {
		t.Fatalf("outcome/exit = %q/%d", result.Outcome, result.ExitCode)
	}
	if len(result.Issues) != 1 || result.Issues[0].Code != app.IssueInternal {
		t.Fatalf("issues = %#v, want one typed internal issue", result.Issues)
	}
	if result.Components == nil || result.ManualActions == nil {
		t.Fatal("required result arrays must encode as arrays, not null")
	}
}

func TestStableCommandsAreNoMutationPlaceholders(t *testing.T) {
	commands := [][]string{
		{"setup"}, {"status"}, {"doctor"}, {"repair"},
		{"models", "recommend"}, {"models", "install", "example"},
		{"models", "remove", "example"}, {"vscode", "configure"},
		{"vscode", "open"}, {"verify"}, {"update"},
		{"update", "--rollback"}, {"uninstall"}, {"report"},
	}
	for _, arguments := range commands {
		t.Run(strings.Join(arguments, "_"), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exit := Run(context.Background(), arguments, testDependencies(&stdout, &stderr))
			if exit != int(app.ExitInternalFailure) {
				t.Fatalf("exit = %d, want %d", exit, app.ExitInternalFailure)
			}
		})
	}
}
