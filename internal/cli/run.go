package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/install"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/ui"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/update"
)

type Dependencies struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Clock   app.Clock
	FS      platform.FileSystem
	Runner  platform.Runner
	Network platform.Network
	Build   app.BuildInfo
	Service app.Service
	Updater update.Updater
}

func Run(ctx context.Context, args []string, deps Dependencies) int {
	if deps.Stdout == nil || deps.Stderr == nil || deps.Clock == nil ||
		deps.FS == nil || deps.Runner == nil || deps.Network == nil {
		return int(app.ExitInternalFailure)
	}

	jsonMode, args := extractFlag(args, "--json")
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printHelp(deps.Stdout)
		return int(app.ExitSuccess)
	}
	if args[0] == "version" {
		printVersion(deps.Stdout, deps.Build)
		return int(app.ExitSuccess)
	}

	operation, ok := parseOperation(args)
	if !ok {
		fmt.Fprintln(deps.Stderr, "local-ai: invalid command; run 'local-ai help'")
		return int(app.ExitConfigurationFailure)
	}
	if (operation.Command == app.CommandUpdate || operation.Command == app.CommandUpdateRollback) && deps.Updater != nil {
		result := runUpdate(ctx, operation, deps)
		if jsonMode {
			encoder := json.NewEncoder(deps.Stdout)
			encoder.SetEscapeHTML(false)
			if err := encoder.Encode(result); err != nil {
				return int(app.ExitInternalFailure)
			}
		} else {
			ui.RenderResult(deps.Stderr, result)
		}
		return int(result.ExitCode)
	}

	service := deps.Service
	if service == nil {
		service = app.PlaceholderService{
			Clock: deps.Clock,
			Platform: platform.Capabilities{
				FileSystem: deps.FS,
				Runner:     deps.Runner,
				Network:    deps.Network,
			},
		}
	}
	result := service.Execute(ctx, operation)

	if jsonMode {
		encoder := json.NewEncoder(deps.Stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(result); err != nil {
			fmt.Fprintln(deps.Stderr, "local-ai: could not write JSON result")
			return int(app.ExitInternalFailure)
		}
	} else {
		ui.RenderResult(deps.Stderr, result)
	}

	return int(result.ExitCode)
}

func runUpdate(ctx context.Context, operation app.Operation, deps Dependencies) app.Result {
	var err error
	if operation.Command == app.CommandUpdateRollback {
		err = deps.Updater.Rollback(ctx)
	} else {
		var release update.Release
		release, err = deps.Updater.Check(ctx)
		if err == nil {
			err = deps.Updater.Apply(ctx, release)
		}
	}
	result := app.Result{
		SchemaVersion: app.EventSchemaVersion,
		RecordType:    "result",
		Timestamp:     deps.Clock.Now().UTC(),
		Command:       operation.Command,
		Components:    []app.Component{},
		Issues:        []app.Issue{},
		ManualActions: []app.ManualAction{},
	}
	if err == nil {
		result.Outcome = app.OutcomeSuccess
		result.ExitCode = app.ExitSuccess
		result.Summary = "The manager deployment operation completed successfully."
		return result
	}
	code := app.IssueInternal
	var deploymentError *install.DeploymentError
	if errors.As(err, &deploymentError) {
		code = deploymentError.Code
	}
	issue := app.Issue{
		Code: code, Severity: app.SeverityError, Domain: "update",
		Summary: "The manager deployment operation failed.",
		Remediations: []app.Remediation{{
			Kind: "retry", Summary: "Review the typed issue and retry the explicit command.", SafeForYes: false,
		}},
	}
	result.Outcome = app.OutcomeFailed
	result.ExitCode = app.ExitForIssue(code)
	result.Summary = issue.Summary
	result.Issues = []app.Issue{issue}
	return result
}

func extractFlag(args []string, flag string) (bool, []string) {
	found := false
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == flag {
			found = true
			continue
		}
		filtered = append(filtered, arg)
	}
	return found, filtered
}

func parseOperation(args []string) (app.Operation, bool) {
	command := args[0]
	arguments := args[1:]

	switch command {
	case "setup":
		return operation(app.CommandSetup, arguments), true
	case "status":
		return operation(app.CommandStatus, arguments), true
	case "doctor":
		return operation(app.CommandDoctor, arguments), true
	case "repair":
		return operation(app.CommandRepair, arguments), true
	case "verify":
		return operation(app.CommandVerify, arguments), true
	case "uninstall":
		return operation(app.CommandUninstall, arguments), true
	case "report":
		return operation(app.CommandReport, arguments), true
	case "models":
		return parseNested(arguments, map[string]app.Command{
			"list":      app.CommandModelsList,
			"recommend": app.CommandModelsRecommend,
			"install":   app.CommandModelsInstall,
			"remove":    app.CommandModelsRemove,
		})
	case "vscode":
		return parseNested(arguments, map[string]app.Command{
			"configure": app.CommandVSCodeConfigure,
			"open":      app.CommandVSCodeOpen,
		})
	case "update":
		if contains(arguments, "--rollback") {
			return operation(app.CommandUpdateRollback, arguments), true
		}
		return operation(app.CommandUpdate, arguments), true
	default:
		return app.Operation{}, false
	}
}

func parseNested(arguments []string, commands map[string]app.Command) (app.Operation, bool) {
	if len(arguments) == 0 {
		return app.Operation{}, false
	}
	command, ok := commands[arguments[0]]
	if !ok {
		return app.Operation{}, false
	}
	return operation(command, arguments[1:]), true
}

func operation(command app.Command, arguments []string) app.Operation {
	return app.Operation{Command: command, Arguments: append([]string(nil), arguments...)}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
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
  local-ai <command> [options]

Commands:
  setup                 Run or resume setup
  status                Show component status
  doctor                Diagnose problems without mutation
  repair                Apply selected safe repairs
  models list           List catalog models
  models recommend      Recommend primary and fallback models
  models install <id>   Install a catalog model
  models remove <name>  Remove a selected model
  vscode configure      Configure the Local AI profile
  vscode open           Open the Local AI profile
  verify                Run read-only verification
  update [--rollback]   Update or restore the previous binary
  uninstall             Remove manager-owned assets
  report                Create a redacted support report
  version               Print build metadata
  help                  Show this help

Global options:
  --json                Emit a versioned JSON result for stable commands

Commands without a configured domain service remain non-mutating placeholders.
`)
}
