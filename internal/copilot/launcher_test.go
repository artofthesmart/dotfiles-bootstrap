package copilot

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

func TestProviderEnvironmentIsExactOfflineOllamaContract(t *testing.T) {
	environment, err := NewProviderEnvironment("local-ai-qwen3-coder:latest", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"COPILOT_PROVIDER_BASE_URL=http://127.0.0.1:11434/v1",
		"COPILOT_PROVIDER_TYPE=openai",
		"COPILOT_PROVIDER_API_KEY=",
		"COPILOT_PROVIDER_WIRE_API=responses",
		"COPILOT_PROVIDER_WIRE_MODEL=local-ai-qwen3-coder:latest",
		"COPILOT_MODEL=local-ai-qwen3-coder:latest",
		"COPILOT_OFFLINE=true",
	}
	if got := environment.Variables(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("provider environment = %#v, want %#v", got, want)
	}
}

func TestProviderEnvironmentRejectsCloudAndNonV1Endpoints(t *testing.T) {
	for _, endpoint := range []string{
		"https://api.github.com/v1",
		"http://example.com:11434/v1",
		"http://127.0.0.1:11434/api",
		"http://user:secret@127.0.0.1:11434/v1",
	} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := NewProviderEnvironment("local-ai-model:latest", endpoint); err == nil {
				t.Fatal("unsafe endpoint was accepted")
			}
		})
	}
}

func TestPOSIXLauncherForwardsEveryArgumentUnchanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher execution is covered on POSIX hosts")
	}
	paths := testkit.NewTempManagerPaths(t)
	official := filepath.Join(paths.Root, "official copilot")
	captureArgs := filepath.Join(paths.Root, "arguments")
	captureEnv := filepath.Join(paths.Root, "environment")
	script := `#!/bin/sh
printf '%s\n' "$COPILOT_PROVIDER_BASE_URL" "$COPILOT_PROVIDER_TYPE" "$COPILOT_PROVIDER_API_KEY" "$COPILOT_PROVIDER_WIRE_API" "$COPILOT_PROVIDER_WIRE_MODEL" "$COPILOT_MODEL" "$COPILOT_OFFLINE" > "$CAPTURE_ENV"
printf '%s\000' "$@" > "$CAPTURE_ARGS"
`
	if err := os.WriteFile(official, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	environment, err := NewProviderEnvironment("local-ai-model:latest", "")
	if err != nil {
		t.Fatal(err)
	}
	launcher, err := LauncherBytes("linux", official, environment)
	if err != nil {
		t.Fatal(err)
	}
	launcherPath := filepath.Join(paths.Root, "copilot-local")
	if err := os.WriteFile(launcherPath, launcher, 0o700); err != nil {
		t.Fatal(err)
	}
	arguments := []string{"two words", "--flag=value", "semi;colon", "$dollar", "quote'arg", ""}
	command := exec.Command(launcherPath, arguments...)
	command.Env = append(os.Environ(), "CAPTURE_ARGS="+captureArgs, "CAPTURE_ENV="+captureEnv)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("launcher failed: %v\n%s", err, output)
	}
	rawArguments, err := os.ReadFile(captureArgs)
	if err != nil {
		t.Fatal(err)
	}
	parts := bytes.Split(rawArguments, []byte{0})
	parts = parts[:len(parts)-1]
	got := make([]string, len(parts))
	for index, part := range parts {
		got[index] = string(part)
	}
	wantArgs := append([]string{"--disable-client-telemetry"}, arguments...)
	if strings.Join(got, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("forwarded args = %#v, want %#v", got, wantArgs)
	}
	rawEnvironment, err := os.ReadFile(captureEnv)
	if err != nil {
		t.Fatal(err)
	}
	wantEnvironment := "http://127.0.0.1:11434/v1\nopenai\n\nresponses\nlocal-ai-model:latest\nlocal-ai-model:latest\ntrue\n"
	if string(rawEnvironment) != wantEnvironment {
		t.Fatalf("environment = %q, want %q", rawEnvironment, wantEnvironment)
	}
}

func TestWindowsLauncherUsesPercentStarAndEscapesExecutablePath(t *testing.T) {
	environment, err := NewProviderEnvironment("local-ai-model:latest", "")
	if err != nil {
		t.Fatal(err)
	}
	launcher, err := LauncherBytes("windows", `C:\Users\100% Dev\Copilot ^ Tools\copilot.exe`, environment)
	if err != nil {
		t.Fatal(err)
	}
	text := string(launcher)
	for _, required := range []string{
		`setlocal DisableDelayedExpansion`,
		`set "COPILOT_PROVIDER_BASE_URL=http://127.0.0.1:11434/v1"`,
		`set "COPILOT_PROVIDER_TYPE=openai"`,
		`set "COPILOT_PROVIDER_WIRE_API=responses"`,
		`set "COPILOT_OFFLINE=true"`,
		`"C:\Users\100%% Dev\Copilot ^^ Tools\copilot.exe" --disable-client-telemetry %*`,
		`exit /b %ERRORLEVEL%`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("Windows launcher is missing %q:\n%s", required, text)
		}
	}
	if strings.Contains(text, "github.com") || strings.Contains(text, "%1 %2") {
		t.Fatalf("Windows launcher permits cloud fallback or lossy forwarding:\n%s", text)
	}
}
