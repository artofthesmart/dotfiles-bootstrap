//go:build integration

package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

func TestRealBinaryStableReadOnlyCommandsDoNotMutateOutsideSandbox(t *testing.T) {
	repository := findRepositoryRoot(t)
	paths := testkit.NewTempManagerPaths(t)
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	pathValue := os.Getenv("PATH")
	build := exec.Command(goBinary, "build", "-o", paths.Bin, "./cmd/local-ai")
	build.Dir = repository
	build.Env = appendPlatformEnvironment(paths.Environment(pathValue))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build local-ai: %v\n%s", err, output)
	}

	guard, err := os.MkdirTemp(filepath.Join(repository, "build", "test-sandboxes"), "outside-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(guard, 0o700); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("restore guard permissions: %v", err)
		}
		if err := os.RemoveAll(guard); err != nil {
			t.Errorf("remove guard: %v", err)
		}
	})
	sentinel := filepath.Join(guard, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("outside must remain unchanged\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(guard, 0o500); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, guard)

	assertCommand(t, paths, guard, []string{"help"}, 0, "Local AI Manager")
	assertCommand(t, paths, guard, []string{"version"}, 0, "local-ai dev")
	for _, arguments := range [][]string{
		{"models", "list", "--json"},
		{"report", "--dry-run", "--json"},
	} {
		output := assertCommand(t, paths, guard, arguments, int(app.ExitSuccess), `"record_type":"result"`)
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		var result app.Result
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &result); err != nil {
			t.Fatalf("%v did not emit valid JSON: %v\n%s", arguments, err, output)
		}
		if result.Outcome != app.OutcomeSuccess || result.ExitCode != app.ExitSuccess {
			t.Fatalf("%v result = %#v", arguments, result)
		}
	}
	assertCommand(t, paths, guard, []string{"status", "--endpoint", "https://example.com"}, int(app.ExitConfigurationFailure), "configuration")
	copilotOutput := assertCommand(t, paths, guard,
		[]string{"--dry-run", "--json", "copilot", "open", "--help", "prompt with spaces"},
		int(app.ExitRuntimeVerificationFailure), string(app.IssueCopilotAlias))
	copilotLines := strings.Split(strings.TrimSpace(string(copilotOutput)), "\n")
	var copilotResult app.Result
	if err := json.Unmarshal([]byte(copilotLines[len(copilotLines)-1]), &copilotResult); err != nil {
		t.Fatalf("copilot open did not emit a valid result: %v\n%s", err, copilotOutput)
	}
	if copilotResult.Command != app.CommandCopilotOpen || copilotResult.Issues[0].Code != app.IssueCopilotAlias {
		t.Fatalf("copilot result = %#v", copilotResult)
	}

	after := snapshot(t, guard)
	if !equalSnapshots(before, after) {
		t.Fatalf("binary mutated outside sandbox\nbefore: %#v\nafter: %#v", before, after)
	}
}

func assertCommand(t *testing.T, paths testkit.ManagerPaths, workingDirectory string, arguments []string, wantExit int, contains string) []byte {
	t.Helper()
	command := exec.Command(paths.Bin, arguments...)
	command.Dir = workingDirectory
	command.Env = appendPlatformEnvironment(paths.Environment(os.Getenv("PATH")))
	output, err := command.CombinedOutput()
	exit := 0
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("%v failed to start: %v", arguments, err)
		}
		exit = exitError.ExitCode()
	}
	if exit != wantExit {
		t.Fatalf("%v exit = %d, want %d\n%s", arguments, exit, wantExit, output)
	}
	if !strings.Contains(string(output), contains) {
		t.Fatalf("%v output does not contain %q\n%s", arguments, contains, output)
	}
	return output
}

type fileSnapshot struct {
	Name string
	Mode os.FileMode
	Size int64
	Hash string
}

func snapshot(t *testing.T, root string) []fileSnapshot {
	t.Helper()
	var result []fileSnapshot
	err := filepath.Walk(root, func(name string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		entry := fileSnapshot{Name: relative, Mode: info.Mode(), Size: info.Size()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(data)
			entry.Hash = hex.EncodeToString(digest[:])
		}
		result = append(result, entry)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result
}

func equalSnapshots(left, right []fileSnapshot) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func appendPlatformEnvironment(environment []string) []string {
	for _, key := range []string{"SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value := os.Getenv(key); value != "" {
			environment = append(environment, key+"="+value)
		}
	}
	if runtime.GOOS == "windows" {
		environment = append(environment, "GOOS=windows")
	}
	return environment
}

func findRepositoryRoot(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatal("could not find repository root")
		}
		current = parent
	}
}
