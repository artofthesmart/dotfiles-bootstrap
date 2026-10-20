package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

var testTime = time.Date(2026, time.October, 20, 19, 30, 0, 0, time.UTC)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return testTime }

type recordingService struct {
	operations []app.Operation
	result     app.Result
	emit       bool
}

func (service *recordingService) Execute(_ context.Context, operation app.Operation) app.Result {
	service.operations = append(service.operations, operation)
	if service.emit && operation.Events != nil {
		_ = operation.Events.Emit(app.Event{
			Timestamp: testTime, Command: operation.Command,
			Kind: "phase_started", Level: "info", Message: "A test phase started.", Attributes: map[string]any{"phase": "test"},
		})
	}
	result := service.result
	if result.Outcome == "" {
		result = app.Result{
			Timestamp: testTime, Command: operation.Command, Outcome: app.OutcomeSuccess,
			ExitCode: app.ExitSuccess, Summary: "completed",
		}
	}
	return result
}

func testDependencies(stdout, stderr *bytes.Buffer, service app.Service) Dependencies {
	fs := testkit.NewMemoryFileSystem("/sandbox", nil)
	return Dependencies{
		Stdin: strings.NewReader(""), Stdout: stdout, Stderr: stderr, Clock: fixedClock{},
		FS: fs, Service: service,
		Build:       app.BuildInfo{Version: "1.2.3", Commit: "abc123", Date: "2026-10-20T19:00:00Z"},
		Environment: map[string]string{},
	}
}

func TestVersionAndHelp(t *testing.T) {
	for _, test := range []struct {
		args     []string
		contains string
	}{
		{args: []string{"version"}, contains: "local-ai 1.2.3"},
		{args: []string{"--version"}, contains: "commit: abc123"},
		{args: []string{"help"}, contains: "Running without a command opens the guided terminal interface."},
	} {
		var stdout, stderr bytes.Buffer
		if exit := Run(context.Background(), test.args, testDependencies(&stdout, &stderr, nil)); exit != 0 {
			t.Fatalf("%v exit = %d", test.args, exit)
		}
		if !strings.Contains(stdout.String(), test.contains) {
			t.Fatalf("%v output = %q", test.args, stdout.String())
		}
	}
}

func TestConfigurationPrecedenceDefaultsFileEnvironmentCLI(t *testing.T) {
	var stdout, stderr bytes.Buffer
	service := &recordingService{}
	deps := testDependencies(&stdout, &stderr, service)
	path := "/sandbox/config.json"
	if err := deps.FS.MkdirAll("/sandbox", 0o700); err != nil {
		t.Fatal(err)
	}
	config := `{
	  "schema_version":"local-ai.config/v1",
	  "behavior":{"accept_safe_defaults":false,"dry_run":false,"output":"human","color":"always","resume_policy":"ask"},
	  "model":{"catalog_id":"from-file"},
	  "ollama":{"endpoint":"http://127.0.0.1:11435"},
	  "update":{"verify_github_attestation_when_available":true}
	}`
	if err := deps.FS.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	deps.Environment = map[string]string{
		envOllamaEndpoint: "http://localhost:11436",
		envNoColor:        "1",
		envAttestation:    "false",
	}
	exit := Run(context.Background(), []string{
		"--config", path, "--endpoint", "http://127.0.0.1:11437",
		"--model", "from-cli", "--color", "auto", "--yes", "--dry-run", "status",
	}, deps)
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	if len(service.operations) != 1 {
		t.Fatalf("operations = %d", len(service.operations))
	}
	operation := service.operations[0]
	if operation.Config.Ollama.Endpoint != "http://127.0.0.1:11437" ||
		operation.Config.Model.CatalogID != "from-cli" ||
		operation.Config.Behavior.Color != "auto" ||
		!operation.Yes || !operation.DryRun ||
		operation.Config.Update.VerifyGitHubAttestationWhenAvailable {
		t.Fatalf("effective operation = %#v", operation)
	}
	if operation.Config.Model.ContextTokens != 65536 ||
		operation.Config.VSCode.ProfileName != "Local AI" ||
		operation.Config.Model.MinimumFreeBytesAfterInstall != 2<<30 {
		t.Fatalf("defaults were not retained: %#v", operation.Config)
	}
}

func TestJSONModeEmitsVersionedNDJSONEventsAndOneFinalResult(t *testing.T) {
	var stdout, stderr bytes.Buffer
	service := &recordingService{emit: true}
	exit := Run(context.Background(), []string{"status", "--json"}, testDependencies(&stdout, &stderr, service))
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	var records []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(stdout.String()))
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if len(records) != 3 {
		t.Fatalf("records = %d\n%s", len(records), stdout.String())
	}
	for index, record := range records {
		if record["schema_version"] != app.EventSchemaVersion || int(record["sequence"].(float64)) != index {
			t.Fatalf("record %d = %#v", index, record)
		}
	}
	if records[0]["record_type"] != "event" || records[1]["record_type"] != "event" || records[2]["record_type"] != "result" {
		t.Fatalf("record types = %#v", records)
	}
	assertRecordShape(t, records[0],
		[]string{"schema_version", "record_type", "sequence", "timestamp", "command", "transaction_id", "kind", "level", "message", "attributes"},
		[]string{"step_id", "progress", "issue"},
	)
	assertRecordShape(t, records[2],
		[]string{"schema_version", "record_type", "sequence", "timestamp", "command", "transaction_id", "outcome", "exit_code", "summary", "issues", "components", "manual_actions"},
		nil,
	)
}

func TestRepresentativeCommandDispatch(t *testing.T) {
	tests := []struct {
		args    []string
		command app.Command
		target  string
	}{
		{[]string{"setup", "--yes"}, app.CommandSetup, ""},
		{[]string{"status", "--check-updates"}, app.CommandStatus, ""},
		{[]string{"doctor"}, app.CommandDoctor, ""},
		{[]string{"repair", "--resume"}, app.CommandRepair, ""},
		{[]string{"models", "list"}, app.CommandModelsList, ""},
		{[]string{"models", "recommend"}, app.CommandModelsRecommend, ""},
		{[]string{"models", "install", "qwen3-coder"}, app.CommandModelsInstall, "qwen3-coder"},
		{[]string{"models", "remove", "local-ai-qwen3:latest", "--confirm-destructive"}, app.CommandModelsRemove, "local-ai-qwen3:latest"},
		{[]string{"vscode", "configure", "--manual-complete"}, app.CommandVSCodeConfigure, ""},
		{[]string{"vscode", "open", "--repository", "/workspace/example"}, app.CommandVSCodeOpen, ""},
		{[]string{"copilot", "configure"}, app.CommandCopilotConfigure, ""},
		{[]string{"copilot", "open"}, app.CommandCopilotOpen, ""},
		{[]string{"verify"}, app.CommandVerify, ""},
		{[]string{"update", "--rollback"}, app.CommandUpdateRollback, ""},
		{[]string{"uninstall", "--remove-models", "--confirm-destructive"}, app.CommandUninstall, ""},
		{[]string{"report", "--output", "report.json"}, app.CommandReport, ""},
	}
	for _, test := range tests {
		t.Run(strings.Join(test.args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			service := &recordingService{}
			if exit := Run(context.Background(), test.args, testDependencies(&stdout, &stderr, service)); exit != 0 {
				t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
			}
			operation := service.operations[0]
			if operation.Command != test.command {
				t.Fatalf("command = %q, want %q", operation.Command, test.command)
			}
			if test.target != "" && (len(operation.Arguments) != 1 || operation.Arguments[0] != test.target) {
				t.Fatalf("arguments = %#v", operation.Arguments)
			}
		})
	}
}

func TestExitCodeAndResultStayAligned(t *testing.T) {
	var stdout, stderr bytes.Buffer
	service := &recordingService{result: app.Result{
		Timestamp: testTime, Outcome: app.OutcomeManualRequired, ExitCode: app.ExitManualRequired,
		Summary: "manual completion remains", Issues: []app.Issue{}, Components: []app.Component{}, ManualActions: []app.ManualAction{},
	}}
	exit := Run(context.Background(), []string{"verify", "--json"}, testDependencies(&stdout, &stderr, service))
	if exit != int(app.ExitManualRequired) {
		t.Fatalf("exit = %d", exit)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var result app.Result
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &result); err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != app.ExitManualRequired || result.Outcome != app.OutcomeManualRequired {
		t.Fatalf("result = %#v", result)
	}
}

func TestMissingApplicationServiceMapsToInternalFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := Run(context.Background(), []string{"status", "--json"}, testDependencies(&stdout, &stderr, nil))
	if exit != int(app.ExitInternalFailure) {
		t.Fatalf("exit = %d", exit)
	}
	var result app.Result
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != app.ExitInternalFailure || len(result.Issues) != 1 || result.Issues[0].Code != app.IssueInternal {
		t.Fatalf("result = %#v", result)
	}
}

func TestInvalidConfigAndConflictingResumeReturnConfigurationFailure(t *testing.T) {
	for _, args := range [][]string{
		{"status", "--resume", "--restart"},
		{"status", "--endpoint", "https://example.com"},
		{"models", "install"},
		{"unknown"},
	} {
		var stdout, stderr bytes.Buffer
		exit := Run(context.Background(), args, testDependencies(&stdout, &stderr, &recordingService{}))
		if exit != int(app.ExitConfigurationFailure) {
			t.Fatalf("%v exit = %d", args, exit)
		}
	}
}

func TestNoSubcommandGuidedCancellationIsStable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	deps := testDependencies(&stdout, &stderr, &recordingService{})
	deps.Stdin = strings.NewReader("q\n")
	exit := Run(context.Background(), nil, deps)
	if exit != int(app.ExitCancelled) {
		t.Fatalf("exit = %d", exit)
	}
	if !strings.Contains(stdout.String(), "Choose an action") || !strings.Contains(stderr.String(), "cancelled") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestGlobalOptionsWithoutSubcommandStillUseGuidedFlow(t *testing.T) {
	var stdout, stderr bytes.Buffer
	service := &recordingService{}
	deps := testDependencies(&stdout, &stderr, service)
	deps.Stdin = strings.NewReader("2\n")
	exit := Run(context.Background(), []string{"--dry-run"}, deps)
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %s", exit, stderr.String())
	}
	if len(service.operations) != 1 || service.operations[0].Command != app.CommandStatus || !service.operations[0].DryRun {
		t.Fatalf("operations = %#v", service.operations)
	}
}

func assertRecordShape(t *testing.T, record map[string]any, required, optional []string) {
	t.Helper()
	allowed := map[string]bool{}
	for _, key := range required {
		allowed[key] = true
		if _, ok := record[key]; !ok {
			t.Fatalf("record missing required key %q: %#v", key, record)
		}
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key := range record {
		if !allowed[key] {
			t.Fatalf("record contains schema-unknown key %q: %#v", key, record)
		}
	}
}
