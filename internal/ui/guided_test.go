package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type scriptedTerminal struct {
	inputs []string
	output strings.Builder
}

func (terminal *scriptedTerminal) Prompt(context.Context, string) (string, error) {
	if len(terminal.inputs) == 0 {
		return "", errors.New("no scripted input")
	}
	value := terminal.inputs[0]
	terminal.inputs = terminal.inputs[1:]
	return value, nil
}

func (terminal *scriptedTerminal) Write(value string) {
	terminal.output.WriteString(value)
}

func TestGuidedSetupReturnsOnlyTypedCLIChoices(t *testing.T) {
	terminal := &scriptedTerminal{inputs: []string{"1", "qwen3-coder", "restart", "yes"}}
	args, err := GuidedArguments(context.Background(), terminal)
	if err != nil {
		t.Fatal(err)
	}
	want := "setup --model qwen3-coder --restart --yes"
	if strings.Join(args, " ") != want {
		t.Fatalf("args = %q, want %q", strings.Join(args, " "), want)
	}
	if !strings.Contains(terminal.output.String(), "host checks") {
		t.Fatalf("output = %q", terminal.output.String())
	}
}

func TestGuidedVSCodePreservesRepositoryAsOneArgument(t *testing.T) {
	terminal := &scriptedTerminal{inputs: []string{"6", "2", "/workspace/path with spaces"}}
	args, err := GuidedArguments(context.Background(), terminal)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 4 || args[3] != "/workspace/path with spaces" {
		t.Fatalf("args = %#v", args)
	}
}

func TestGuidedCancel(t *testing.T) {
	terminal := &scriptedTerminal{inputs: []string{"q"}}
	if _, err := GuidedArguments(context.Background(), terminal); !errors.Is(err, ErrCancelled) {
		t.Fatalf("error = %v", err)
	}
}

func TestGuidedCopilotUsesTypedCommand(t *testing.T) {
	terminal := &scriptedTerminal{inputs: []string{"9", "1"}}
	args, err := GuidedArguments(context.Background(), terminal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(args, " ") != "copilot configure" {
		t.Fatalf("args = %#v", args)
	}
}
