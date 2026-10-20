package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
)

type parsedInvocation struct {
	help                  bool
	version               bool
	json                  bool
	yes                   bool
	dryRun                bool
	resume                bool
	restart               bool
	configPath            string
	color                 string
	endpoint              string
	model                 string
	repository            string
	outputPath            string
	manualCompleted       bool
	checkUpdates          bool
	rollback              bool
	removeAliases         bool
	removeModels          bool
	removeVSCodeExtension bool
	removeVSCode          bool
	removeOllama          bool
	confirmDestructive    bool
	commandTokens         []string
}

func parseInvocation(args []string) (parsedInvocation, error) {
	var parsed parsedInvocation
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if len(parsed.commandTokens) >= 2 && parsed.commandTokens[0] == "copilot" && parsed.commandTokens[1] == "open" {
			parsed.commandTokens = append(parsed.commandTokens, argument)
			continue
		}
		if argument == "--" {
			parsed.commandTokens = append(parsed.commandTokens, args[index+1:]...)
			break
		}
		name, inline, hasInline := strings.Cut(argument, "=")
		switch name {
		case "--help", "-h":
			parsed.help = true
		case "--version":
			parsed.version = true
		case "--json":
			parsed.json = true
		case "--yes", "-y":
			parsed.yes = true
		case "--dry-run":
			parsed.dryRun = true
		case "--resume":
			parsed.resume = true
		case "--restart":
			parsed.restart = true
		case "--no-color":
			parsed.color = "never"
		case "--manual-complete":
			parsed.manualCompleted = true
		case "--check-updates":
			parsed.checkUpdates = true
		case "--rollback":
			parsed.rollback = true
		case "--remove-aliases":
			parsed.removeAliases = true
		case "--remove-models":
			parsed.removeModels = true
		case "--remove-vscode-extension":
			parsed.removeVSCodeExtension = true
		case "--remove-vscode":
			parsed.removeVSCode = true
		case "--remove-ollama":
			parsed.removeOllama = true
		case "--confirm-destructive":
			parsed.confirmDestructive = true
		case "--config", "--color", "--endpoint", "--model", "--repository", "--output":
			value := inline
			if !hasInline {
				index++
				if index >= len(args) {
					return parsedInvocation{}, fmt.Errorf("%s requires a value", name)
				}
				value = args[index]
			}
			if strings.TrimSpace(value) == "" {
				return parsedInvocation{}, fmt.Errorf("%s requires a non-empty value", name)
			}
			switch name {
			case "--config":
				parsed.configPath = value
			case "--color":
				parsed.color = value
			case "--endpoint":
				parsed.endpoint = value
			case "--model":
				parsed.model = value
			case "--repository":
				parsed.repository = value
			case "--output":
				parsed.outputPath = value
			}
		default:
			if strings.HasPrefix(argument, "-") {
				return parsedInvocation{}, fmt.Errorf("unknown option %q", argument)
			}
			parsed.commandTokens = append(parsed.commandTokens, argument)
		}
	}
	if parsed.resume && parsed.restart {
		return parsedInvocation{}, errors.New("--resume and --restart are mutually exclusive")
	}
	if parsed.color != "" && !oneOf(parsed.color, "auto", "always", "never") {
		return parsedInvocation{}, errors.New("--color must be auto, always, or never")
	}
	return parsed, nil
}

func (parsed parsedInvocation) apply(config *app.Config) {
	if parsed.json {
		config.Behavior.Output = "json"
	}
	if parsed.yes {
		config.Behavior.AcceptSafeDefaults = true
	}
	if parsed.dryRun {
		config.Behavior.DryRun = true
	}
	if parsed.color != "" {
		config.Behavior.Color = parsed.color
	}
	if parsed.endpoint != "" {
		config.Ollama.Endpoint = parsed.endpoint
	}
	if parsed.model != "" {
		config.Model.CatalogID = parsed.model
	}
	if parsed.resume {
		config.Behavior.ResumePolicy = "resume"
	}
	if parsed.restart {
		config.Behavior.ResumePolicy = "restart"
	}
}

func (parsed parsedInvocation) operation(config app.Config) (app.Operation, error) {
	tokens := parsed.commandTokens
	if len(tokens) == 0 {
		return app.Operation{}, errors.New("no command was selected")
	}
	if tokens[0] == "help" {
		return app.Operation{}, errors.New("help")
	}
	if tokens[0] == "version" {
		return app.Operation{}, errors.New("version")
	}
	command, consumed, err := parseCommand(tokens)
	if err != nil {
		return app.Operation{}, err
	}
	arguments := append([]string(nil), tokens[consumed:]...)
	switch command {
	case app.CommandModelsInstall, app.CommandModelsRemove:
		if len(arguments) != 1 {
			return app.Operation{}, fmt.Errorf("%s requires exactly one model identifier", strings.ReplaceAll(string(command), "_", " "))
		}
	case app.CommandStatus, app.CommandDoctor, app.CommandRepair, app.CommandModelsList,
		app.CommandModelsRecommend, app.CommandVSCodeConfigure, app.CommandVSCodeOpen,
		app.CommandCopilotConfigure,
		app.CommandVerify, app.CommandUpdate, app.CommandUpdateRollback, app.CommandUninstall,
		app.CommandReport, app.CommandSetup:
		if len(arguments) != 0 {
			return app.Operation{}, fmt.Errorf("%s does not accept positional arguments", strings.ReplaceAll(string(command), "_", " "))
		}
	}
	if parsed.rollback && command == app.CommandUpdate {
		command = app.CommandUpdateRollback
	}
	if parsed.rollback && command != app.CommandUpdateRollback {
		return app.Operation{}, errors.New("--rollback is valid only with update")
	}
	if parsed.manualCompleted && command != app.CommandSetup && command != app.CommandVSCodeConfigure {
		return app.Operation{}, errors.New("--manual-complete is valid only with setup or vscode configure")
	}
	if (parsed.removeAliases || parsed.removeModels || parsed.removeVSCodeExtension || parsed.removeVSCode || parsed.removeOllama) && command != app.CommandUninstall {
		return app.Operation{}, errors.New("asset removal options are valid only with uninstall")
	}
	if parsed.confirmDestructive && command != app.CommandUninstall && command != app.CommandModelsRemove {
		return app.Operation{}, errors.New("--confirm-destructive is valid only with uninstall or models remove")
	}
	return app.Operation{
		Command:   command,
		Arguments: arguments,
		Config:    config,
		Yes:       config.Behavior.AcceptSafeDefaults,
		DryRun:    config.Behavior.DryRun,
		Resume:    config.Behavior.ResumePolicy == "resume",
		Restart:   config.Behavior.ResumePolicy == "restart",
		Options: app.OperationOptions{
			Repository:            parsed.repository,
			OutputPath:            parsed.outputPath,
			ManualCompleted:       parsed.manualCompleted,
			CheckUpdates:          parsed.checkUpdates,
			Rollback:              command == app.CommandUpdateRollback,
			RemoveAliases:         parsed.removeAliases,
			RemoveModels:          parsed.removeModels,
			RemoveVSCodeExtension: parsed.removeVSCodeExtension,
			RemoveVSCode:          parsed.removeVSCode,
			RemoveOllama:          parsed.removeOllama,
			ConfirmDestructive:    parsed.confirmDestructive,
		},
	}, nil
}

func parseCommand(tokens []string) (app.Command, int, error) {
	switch tokens[0] {
	case "setup":
		return app.CommandSetup, 1, nil
	case "status":
		return app.CommandStatus, 1, nil
	case "doctor":
		return app.CommandDoctor, 1, nil
	case "repair":
		return app.CommandRepair, 1, nil
	case "verify":
		return app.CommandVerify, 1, nil
	case "uninstall":
		return app.CommandUninstall, 1, nil
	case "report":
		return app.CommandReport, 1, nil
	case "update":
		return app.CommandUpdate, 1, nil
	case "models":
		if len(tokens) < 2 {
			return "", 0, errors.New("models requires list, recommend, install, or remove")
		}
		switch tokens[1] {
		case "list":
			return app.CommandModelsList, 2, nil
		case "recommend":
			return app.CommandModelsRecommend, 2, nil
		case "install":
			return app.CommandModelsInstall, 2, nil
		case "remove":
			return app.CommandModelsRemove, 2, nil
		default:
			return "", 0, fmt.Errorf("unknown models command %q", tokens[1])
		}
	case "vscode":
		if len(tokens) < 2 {
			return "", 0, errors.New("vscode requires configure or open")
		}
		switch tokens[1] {
		case "configure":
			return app.CommandVSCodeConfigure, 2, nil
		case "open":
			return app.CommandVSCodeOpen, 2, nil
		default:
			return "", 0, fmt.Errorf("unknown vscode command %q", tokens[1])
		}
	case "copilot":
		if len(tokens) < 2 {
			return "", 0, errors.New("copilot requires configure or open")
		}
		switch tokens[1] {
		case "configure":
			return app.CommandCopilotConfigure, 2, nil
		case "open":
			return app.CommandCopilotOpen, 2, nil
		default:
			return "", 0, fmt.Errorf("unknown copilot command %q", tokens[1])
		}
	default:
		return "", 0, fmt.Errorf("unknown command %q", tokens[0])
	}
}
