package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

var ErrCancelled = errors.New("guided flow cancelled")

func GuidedArguments(ctx context.Context, terminal Terminal) ([]string, error) {
	if terminal == nil {
		return nil, errors.New("terminal is unavailable")
	}
	terminal.Write("Local AI Manager\n\n")
	terminal.Write("Choose an action:\n")
	terminal.Write("  1. Set up or resume Local AI\n")
	terminal.Write("  2. Show status\n")
	terminal.Write("  3. Diagnose problems\n")
	terminal.Write("  4. Repair safe issues\n")
	terminal.Write("  5. Manage models\n")
	terminal.Write("  6. Configure or open VS Code\n")
	terminal.Write("  7. Verify the current setup\n")
	terminal.Write("  8. Create a redacted support report\n")
	terminal.Write("  9. Configure or open Copilot CLI offline mode\n")
	terminal.Write("  q. Cancel\n\n")

	choice, err := terminal.Prompt(ctx, "Selection [1]: ")
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, ErrCancelled
		}
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "", "1":
		return guidedSetup(ctx, terminal)
	case "2":
		return []string{"status"}, nil
	case "3":
		return []string{"doctor"}, nil
	case "4":
		return []string{"repair"}, nil
	case "5":
		return guidedModels(ctx, terminal)
	case "6":
		return guidedVSCode(ctx, terminal)
	case "7":
		return []string{"verify"}, nil
	case "8":
		return []string{"report"}, nil
	case "9":
		return guidedCopilot(ctx, terminal)
	case "q", "quit", "cancel":
		return nil, ErrCancelled
	default:
		return nil, fmt.Errorf("unknown selection %q", choice)
	}
}

func guidedCopilot(ctx context.Context, terminal Terminal) ([]string, error) {
	choice, err := terminal.Prompt(ctx, "Copilot CLI: 1=configure offline launcher 2=open q=back: ")
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "1", "configure":
		return []string{"copilot", "configure"}, nil
	case "2", "open":
		return []string{"copilot", "open"}, nil
	case "q", "back", "cancel":
		return nil, ErrCancelled
	default:
		return nil, fmt.Errorf("unknown Copilot CLI selection %q", choice)
	}
}

func guidedSetup(ctx context.Context, terminal Terminal) ([]string, error) {
	terminal.Write("\nSetup phases: host checks → model → runtime verification → VS Code → manual checklist.\n")
	model, err := terminal.Prompt(ctx, "Catalog model ID [automatic recommendation]: ")
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	resume, err := terminal.Prompt(ctx, "Transaction choice [auto/resume/restart]: ")
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	args := []string{"setup"}
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", strings.TrimSpace(model))
	}
	switch strings.ToLower(strings.TrimSpace(resume)) {
	case "", "auto":
	case "resume", "r":
		args = append(args, "--resume")
	case "restart":
		args = append(args, "--restart")
	default:
		return nil, fmt.Errorf("unknown resume choice %q", resume)
	}
	apply, err := terminal.Prompt(ctx, "Apply documented non-destructive changes after checks? [y/N]: ")
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(apply)) {
	case "y", "yes":
		args = append(args, "--yes")
	case "", "n", "no":
	default:
		return nil, fmt.Errorf("unknown apply choice %q", apply)
	}
	return args, nil
}

func guidedModels(ctx context.Context, terminal Terminal) ([]string, error) {
	choice, err := terminal.Prompt(ctx, "Models: 1=list 2=recommend 3=install 4=remove q=back: ")
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "1", "list":
		return []string{"models", "list"}, nil
	case "2", "recommend":
		return []string{"models", "recommend"}, nil
	case "3", "install":
		id, err := terminal.Prompt(ctx, "Catalog model ID: ")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(id) == "" {
			return nil, errors.New("catalog model ID is required")
		}
		return []string{"models", "install", strings.TrimSpace(id)}, nil
	case "4", "remove":
		name, err := terminal.Prompt(ctx, "Exact manager-owned model or alias: ")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("model name is required")
		}
		return []string{"models", "remove", strings.TrimSpace(name)}, nil
	case "q", "back", "cancel":
		return nil, ErrCancelled
	default:
		return nil, fmt.Errorf("unknown model selection %q", choice)
	}
}

func guidedVSCode(ctx context.Context, terminal Terminal) ([]string, error) {
	choice, err := terminal.Prompt(ctx, "VS Code: 1=configure 2=open q=back: ")
	if err != nil {
		return nil, err
	}
	command := ""
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "1", "configure":
		command = "configure"
	case "2", "open":
		command = "open"
	case "q", "back", "cancel":
		return nil, ErrCancelled
	default:
		return nil, fmt.Errorf("unknown VS Code selection %q", choice)
	}
	repository, err := terminal.Prompt(ctx, "Optional repository path [blank window]: ")
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	args := []string{"vscode", command}
	if strings.TrimSpace(repository) != "" {
		args = append(args, "--repository", strings.TrimSpace(repository))
	}
	return args, nil
}
