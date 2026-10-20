//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/copilot"
)

func TestCopilotLocalLauncherForwardsArgumentsAndEnforcesOfflineProvider(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher execution is covered here; Windows launcher syntax is unit-tested and requires a hosted Windows run")
	}
	sandbox, err := os.MkdirTemp(".", ".copilot-local-integration-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sandbox) })

	officialDir := filepath.Join(sandbox, "official cli's")
	if err := os.MkdirAll(officialDir, 0o700); err != nil {
		t.Fatal(err)
	}
	argumentsPath := filepath.Join(sandbox, "arguments")
	environmentPath := filepath.Join(sandbox, "environment")
	officialPath := filepath.Join(officialDir, "copilot")
	official := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + shellLiteral(argumentsPath) + "\n" +
		"env > " + shellLiteral(environmentPath) + "\n"
	if err := os.WriteFile(officialPath, []byte(official), 0o700); err != nil {
		t.Fatal(err)
	}

	provider, err := copilot.NewProviderEnvironment("local-ai-integration:latest", "")
	if err != nil {
		t.Fatal(err)
	}
	launcher, err := copilot.LauncherBytes(runtime.GOOS, officialPath, provider)
	if err != nil {
		t.Fatal(err)
	}
	launcherPath := filepath.Join(sandbox, "copilot-local")
	if err := os.WriteFile(launcherPath, launcher, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(launcherPath, "prompt with spaces", "--flag=value", `literal"$*'argument`)
	command.Env = append(os.Environ(),
		"COPILOT_OFFLINE=false",
		"COPILOT_PROVIDER_BASE_URL=https://api.githubcopilot.com",
		"COPILOT_MODEL=cloud-model",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("launcher failed: %v\n%s", err, output)
	}

	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	wantArguments := "--disable-client-telemetry\nprompt with spaces\n--flag=value\nliteral\"$*'argument\n"
	if string(arguments) != wantArguments {
		t.Fatalf("arguments changed:\n got %q\nwant %q", arguments, wantArguments)
	}
	environment, err := os.ReadFile(environmentPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, variable := range provider.Variables() {
		if !containsEnvironmentLine(string(environment), variable) {
			t.Fatalf("launcher environment missing %q", variable)
		}
	}
	if strings.Contains(string(launcher), "api.githubcopilot.com") {
		t.Fatal("launcher contains a cloud fallback")
	}
}

func shellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func containsEnvironmentLine(environment, expected string) bool {
	for _, line := range strings.Split(environment, "\n") {
		if line == expected {
			return true
		}
	}
	return false
}
