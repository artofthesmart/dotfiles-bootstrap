package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/ui"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/update"
)

type Dependencies struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Clock       app.Clock
	FS          platform.FileSystem
	Runner      platform.Runner
	Network     platform.Network
	Build       app.BuildInfo
	Service     app.Service
	Updater     update.Updater
	Terminal    ui.Terminal
	Environment map[string]string
}

func Run(ctx context.Context, args []string, deps Dependencies) int {
	if deps.Stdout == nil || deps.Stderr == nil || deps.Clock == nil {
		return int(app.ExitInternalFailure)
	}
	guidedNeeded := len(args) == 0
	if !guidedNeeded {
		preliminary, err := parseInvocation(args)
		if err != nil {
			return writeInvocationError(deps, commandHint(args), err, containsArg(args, "--json"))
		}
		guidedNeeded = len(preliminary.commandTokens) == 0 && !preliminary.help && !preliminary.version
	}
	if guidedNeeded {
		terminal := deps.Terminal
		if terminal == nil {
			if deps.Stdin == nil {
				return writeInvocationError(deps, app.CommandSetup, errors.New("interactive input is unavailable"), false)
			}
			output := deps.Stdout
			if len(args) > 0 {
				output = deps.Stderr
			}
			terminal = ui.NewPlainTerminal(deps.Stdin, output)
		}
		guided, err := ui.GuidedArguments(ctx, terminal)
		if err != nil {
			if errors.Is(err, ui.ErrCancelled) || errors.Is(err, context.Canceled) {
				result := resultForInvocationError(deps.Clock, app.CommandSetup, app.OutcomeCancelled, app.ExitCancelled, app.IssueConsentDeclined, "The guided operation was cancelled.")
				ui.RenderResult(deps.Stderr, result)
				return int(result.ExitCode)
			}
			return writeInvocationError(deps, app.CommandSetup, err, false)
		}
		args = append(args, guided...)
	}

	parsed, err := parseInvocation(args)
	if err != nil {
		return writeInvocationError(deps, commandHint(args), err, containsArg(args, "--json"))
	}
	if parsed.help || (len(parsed.commandTokens) > 0 && parsed.commandTokens[0] == "help") {
		printHelp(deps.Stdout)
		return int(app.ExitSuccess)
	}
	if parsed.version || (len(parsed.commandTokens) > 0 && parsed.commandTokens[0] == "version") {
		printVersion(deps.Stdout, deps.Build)
		return int(app.ExitSuccess)
	}

	config, _, err := loadConfig(deps.FS, parsed.configPath, deps.Environment)
	if err != nil {
		return writeInvocationError(deps, commandHint(parsed.commandTokens), err, parsed.json)
	}
	parsed.apply(&config)
	if err := validateConfig(config); err != nil {
		return writeInvocationError(deps, commandHint(parsed.commandTokens), err, parsed.json || config.Behavior.Output == "json")
	}
	operation, err := parsed.operation(config)
	if err != nil {
		return writeInvocationError(deps, commandHint(parsed.commandTokens), err, parsed.json || config.Behavior.Output == "json")
	}
	if deps.Service == nil {
		result := resultForInvocationError(deps.Clock, operation.Command, app.OutcomeFailed, app.ExitInternalFailure, app.IssueInternal, "The application service is unavailable.")
		if config.Behavior.Output == "json" {
			if err := newJSONStream(deps.Stdout).Result(result); err != nil {
				return int(app.ExitInternalFailure)
			}
		} else {
			ui.RenderResult(deps.Stderr, result)
		}
		return int(result.ExitCode)
	}

	jsonMode := config.Behavior.Output == "json"
	operation.Input = deps.Stdin
	operation.Output = deps.Stdout
	operation.Error = deps.Stderr
	if jsonMode {
		operation.Output = deps.Stderr
	}
	var stream *jsonStream
	if jsonMode {
		stream = newJSONStream(deps.Stdout)
		operation.Events = stream
		if err := stream.Emit(app.Event{
			Timestamp:  deps.Clock.Now().UTC(),
			Command:    operation.Command,
			Kind:       "operation_started",
			Level:      "info",
			Message:    "The command started.",
			Attributes: map[string]any{"dry_run": operation.DryRun},
		}); err != nil {
			return int(app.ExitInternalFailure)
		}
	}

	result := deps.Service.Execute(ctx, operation)
	result.SchemaVersion = app.EventSchemaVersion
	result.RecordType = "result"
	result.Command = operation.Command
	if result.Timestamp.IsZero() {
		result.Timestamp = deps.Clock.Now().UTC()
	}
	normalizeResult(&result)

	if jsonMode {
		if err := stream.Result(result); err != nil {
			fmt.Fprintln(deps.Stderr, "local-ai: could not write JSON result")
			return int(app.ExitInternalFailure)
		}
	} else {
		ui.RenderResult(deps.Stdout, result)
	}
	return int(result.ExitCode)
}

func writeInvocationError(deps Dependencies, command app.Command, err error, jsonMode bool) int {
	if command == "" {
		fmt.Fprintf(deps.Stderr, "local-ai: %v; run 'local-ai help'\n", err)
		return int(app.ExitConfigurationFailure)
	}
	result := resultForInvocationError(deps.Clock, command, app.OutcomeFailed, app.ExitConfigurationFailure, app.IssueConfigurationInvalid, "The command configuration is invalid.")
	if jsonMode {
		stream := newJSONStream(deps.Stdout)
		if streamErr := stream.Result(result); streamErr != nil {
			return int(app.ExitInternalFailure)
		}
	} else {
		fmt.Fprintf(deps.Stderr, "local-ai: %v\n", err)
		ui.RenderResult(deps.Stderr, result)
	}
	return int(result.ExitCode)
}

func resultForInvocationError(clock app.Clock, command app.Command, outcome app.Outcome, exit app.ExitCode, code app.IssueCode, summary string) app.Result {
	return app.Result{
		SchemaVersion: app.EventSchemaVersion,
		RecordType:    "result",
		Timestamp:     clock.Now().UTC(),
		Command:       command,
		Outcome:       outcome,
		ExitCode:      exit,
		Summary:       summary,
		Issues: []app.Issue{{
			Code: code, Severity: severityForOutcome(outcome), Domain: invocationDomain(code), Summary: summary,
			Remediations: []app.Remediation{{Kind: "documentation", Summary: "Review local-ai help and the versioned configuration contract.", SafeForYes: false}},
		}},
		Components:    []app.Component{},
		ManualActions: []app.ManualAction{},
	}
}

func invocationDomain(code app.IssueCode) string {
	switch code {
	case app.IssueConsentDeclined, app.IssueManualApproval, app.IssuePolicyBlocked:
		return "policy"
	case app.IssueInternal:
		return "internal"
	default:
		return "config"
	}
}

func severityForOutcome(outcome app.Outcome) app.Severity {
	if outcome == app.OutcomeCancelled {
		return app.SeverityInfo
	}
	return app.SeverityError
}

func commandHint(args []string) app.Command {
	tokens := make([]string, 0, len(args))
	for _, argument := range args {
		if strings.HasPrefix(argument, "-") {
			continue
		}
		tokens = append(tokens, argument)
	}
	if len(tokens) == 0 {
		return ""
	}
	command, _, err := parseCommand(tokens)
	if err != nil {
		return ""
	}
	return command
}

func containsArg(args []string, wanted string) bool {
	for _, value := range args {
		if value == wanted || strings.HasPrefix(value, wanted+"=") {
			return true
		}
	}
	return false
}

func printVersion(w io.Writer, build app.BuildInfo) {
	fmt.Fprintf(w, "local-ai %s\ncommit: %s\nbuilt: %s\n", valueOr(build.Version, "dev"), valueOr(build.Commit, "none"), valueOr(build.Date, "unknown"))
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `Local AI Manager

Usage:
  local-ai [global options] <command> [command options]
  local-ai

Running without a command opens the guided terminal interface.

Commands:
  setup                         Run or resume setup
  status [--check-updates]      Show component status
  doctor                        Diagnose problems without mutation
  repair                        Apply selected documented safe repairs
  models list                   List catalog models and installed state
  models recommend              Recommend primary and fallback models
  models install <catalog-id>   Install and verify a catalog model
  models remove <name>          Remove an exact manager-owned model
  vscode configure              Configure the Local AI profile
  vscode open                   Open the Local AI profile
  copilot configure             Configure the official Copilot CLI local launcher
  copilot open [args...]        Run the verified offline launcher with unchanged arguments
  verify                        Run read-only runtime and VS Code checks
  update [--rollback]           Update or restore the previous binary
  uninstall                     Remove manager-owned assets
  report [--output <file>]      Create a privacy-allowlisted support report
  version                       Print build metadata
  help                          Show this help

Global options:
  --config <file>       Load local-ai.config/v1 JSON
  --yes, -y             Accept documented non-destructive defaults only
  --json                Emit newline-delimited local-ai.event-stream/v1 records
  --dry-run             Discover and plan without host mutation
  --resume              Resume the latest compatible transaction
  --restart             Archive it and begin a new transaction
  --model <catalog-id>  Override the configured catalog model
  --endpoint <url>      Override the loopback Ollama endpoint
  --color <mode>        auto, always, or never
  --no-color            Equivalent to --color never

Protected VS Code trust, model selection, tool configuration, and approvals
remain manual. Destructive uninstall flags require --confirm-destructive even
when --yes is supplied. Place manager options before 'copilot open'; all later
arguments are forwarded unchanged to the verified launcher.
`)
}
