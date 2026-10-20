package testkit

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

var (
	_ platform.FileSystem       = (*MemoryFileSystem)(nil)
	_ platform.AtomicFileSystem = (*MemoryFileSystem)(nil)
	_ platform.Runner           = (*ScriptedRunner)(nil)
	_ platform.Network          = (*ScriptedNetwork)(nil)
)

func TestMemoryFileSystemModelsAtomicRenamePermissionsAndSync(t *testing.T) {
	filesystem := NewMemoryFileSystem("/manager", nil)
	if err := filesystem.MkdirAll("/manager/state", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.WriteFile("/manager/state/transaction.tmp", []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.Sync("/manager/state/transaction.tmp"); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.Rename("/manager/state/transaction.tmp", "/manager/state/transaction.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := filesystem.Stat("/manager/state/transaction.tmp"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("old path error = %v, want not exist", err)
	}
	data, err := filesystem.ReadFile("/manager/state/transaction.json")
	if err != nil || string(data) != "new" {
		t.Fatalf("renamed data = %q, %v", data, err)
	}
	if got := filesystem.UnsyncedPaths(); !containsString(got, "/manager/state/transaction.json") {
		t.Fatalf("unsynced paths = %v, want renamed file", got)
	}
	if err := filesystem.Sync("/manager/state/transaction.json"); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.Sync("/manager/state"); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.Chmod("/manager/state/transaction.json", 0o400); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.WriteFile("/manager/state/transaction.json", []byte("overwrite"), 0o600); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("write read-only file error = %v, want permission", err)
	}
	if err := filesystem.WriteFile("/outside/file", []byte("bad"), 0o600); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside write error = %v, want outside-root error", err)
	}
}

func TestMemoryFileSystemFailureInjectionBeforeAndAfterMutation(t *testing.T) {
	before := NewFailureInjector()
	before.Inject("fs.write", BeforeMutation, 1, errors.New("disk full"))
	beforeFS := NewMemoryFileSystem("/manager", before)
	if err := beforeFS.WriteFile("/manager/state.json", []byte("state"), 0o600); err == nil {
		t.Fatal("before-mutation failure unexpectedly succeeded")
	}
	if _, err := beforeFS.Stat("/manager/state.json"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("before failure left file: %v", err)
	}

	after := NewFailureInjector()
	after.Inject("fs.write", AfterMutation, 1, errors.New("lost acknowledgement"))
	afterFS := NewMemoryFileSystem("/manager", after)
	if err := afterFS.WriteFile("/manager/state.json", []byte("state"), 0o600); err == nil {
		t.Fatal("after-mutation failure unexpectedly succeeded")
	}
	data, err := afterFS.ReadFile("/manager/state.json")
	if err != nil || string(data) != "state" {
		t.Fatalf("after failure must retain mutation, data = %q, err = %v", data, err)
	}

	renameFailures := NewFailureInjector()
	renameFailures.Inject("fs.rename", AfterMutation, 1, errors.New("directory sync failed"))
	renameFS := NewMemoryFileSystem("/manager", renameFailures)
	if err := renameFS.WriteFile("/manager/old", []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := renameFS.Rename("/manager/old", "/manager/new"); err == nil {
		t.Fatal("after-rename failure unexpectedly succeeded")
	}
	if _, err := renameFS.Stat("/manager/new"); err != nil {
		t.Fatalf("rename was not atomically applied before injected error: %v", err)
	}
}

func TestEveryMemoryFileSystemMutationSupportsAfterFailure(t *testing.T) {
	injected := errors.New("crash after mutation")
	tests := []struct {
		name      string
		operation string
		mutate    func(*MemoryFileSystem) error
		verify    func(*testing.T, *MemoryFileSystem)
	}{
		{
			name:      "mkdir",
			operation: "fs.mkdir",
			mutate: func(filesystem *MemoryFileSystem) error {
				return filesystem.MkdirAll("/manager/new", 0o700)
			},
			verify: func(t *testing.T, filesystem *MemoryFileSystem) {
				if info, err := filesystem.Stat("/manager/new"); err != nil || !info.IsDir() {
					t.Fatalf("directory was not retained: %v, %v", info, err)
				}
			},
		},
		{
			name:      "write",
			operation: "fs.write",
			mutate: func(filesystem *MemoryFileSystem) error {
				return filesystem.WriteFile("/manager/new", []byte("value"), 0o600)
			},
			verify: func(t *testing.T, filesystem *MemoryFileSystem) {
				if data, err := filesystem.ReadFile("/manager/new"); err != nil || string(data) != "value" {
					t.Fatalf("file was not retained: %q, %v", data, err)
				}
			},
		},
		{
			name:      "rename",
			operation: "fs.rename",
			mutate: func(filesystem *MemoryFileSystem) error {
				if err := filesystem.WriteFile("/manager/old", []byte("value"), 0o600); err != nil {
					return err
				}
				return filesystem.Rename("/manager/old", "/manager/new")
			},
			verify: func(t *testing.T, filesystem *MemoryFileSystem) {
				if _, err := filesystem.Stat("/manager/new"); err != nil {
					t.Fatalf("renamed file was not retained: %v", err)
				}
			},
		},
		{
			name:      "remove",
			operation: "fs.remove",
			mutate: func(filesystem *MemoryFileSystem) error {
				if err := filesystem.WriteFile("/manager/old", []byte("value"), 0o600); err != nil {
					return err
				}
				return filesystem.Remove("/manager/old")
			},
			verify: func(t *testing.T, filesystem *MemoryFileSystem) {
				if _, err := filesystem.Stat("/manager/old"); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("removed file remains: %v", err)
				}
			},
		},
		{
			name:      "sync",
			operation: "fs.sync",
			mutate: func(filesystem *MemoryFileSystem) error {
				if err := filesystem.WriteFile("/manager/new", []byte("value"), 0o600); err != nil {
					return err
				}
				return filesystem.Sync("/manager/new")
			},
			verify: func(t *testing.T, filesystem *MemoryFileSystem) {
				if containsString(filesystem.UnsyncedPaths(), "/manager/new") {
					t.Fatal("file was not marked synced")
				}
			},
		},
		{
			name:      "chmod",
			operation: "fs.chmod",
			mutate: func(filesystem *MemoryFileSystem) error {
				if err := filesystem.WriteFile("/manager/new", []byte("value"), 0o600); err != nil {
					return err
				}
				return filesystem.Chmod("/manager/new", 0o400)
			},
			verify: func(t *testing.T, filesystem *MemoryFileSystem) {
				info, err := filesystem.Stat("/manager/new")
				if err != nil || info.Mode().Perm() != 0o400 {
					t.Fatalf("mode = %v, err = %v", info, err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failures := NewFailureInjector()
			failures.Inject(test.operation, AfterMutation, 1, injected)
			filesystem := NewMemoryFileSystem("/manager", failures)
			if err := test.mutate(filesystem); !errors.Is(err, injected) {
				t.Fatalf("mutation error = %v, want injected error", err)
			}
			test.verify(t, filesystem)
			records := failures.Records()
			if len(records) == 0 || !records[len(records)-1].Triggered {
				t.Fatalf("failure records = %#v, want triggered after-mutation record", records)
			}
		})
	}
}

func TestMemoryFileSystemIsConcurrencySafe(t *testing.T) {
	filesystem := NewMemoryFileSystem("/manager", nil)
	if err := filesystem.MkdirAll("/manager/data", 0o700); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for index := 0; index < 64; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			name := filepath.ToSlash(filepath.Join("/manager/data", time.Unix(int64(index), 0).Format("150405")))
			if err := filesystem.WriteFile(name, []byte(name), 0o600); err != nil {
				t.Errorf("write %s: %v", name, err)
			}
			if _, err := filesystem.ReadFile(name); err != nil {
				t.Errorf("read %s: %v", name, err)
			}
		}(index)
	}
	wait.Wait()
	if got := len(filesystem.Paths()); got != 66 {
		t.Fatalf("path count = %d, want 66", got)
	}
}

func TestScriptedRunnerRecordsCallsAndResults(t *testing.T) {
	runner := NewScriptedRunner(
		CommandOutcome{Result: platform.CommandResult{
			ExitCode: 7,
			Stdout:   []byte("stdout"),
			Stderr:   []byte("stderr"),
		}},
		CommandOutcome{TimedOut: true},
		CommandOutcome{Cancelled: true},
		CommandOutcome{
			Result:     platform.CommandResult{ExitCode: 0, Stdout: []byte("applied")},
			AfterError: errors.New("post-command verification failed"),
		},
	)
	result, err := runner.Run(context.Background(), platform.Command{
		Name:  "ollama",
		Args:  []string{"show", "local-ai-example:latest"},
		Dir:   "/manager/work",
		Env:   []string{"OLLAMA_CONTEXT_LENGTH=65536"},
		Stdin: strings.NewReader("input"),
	})
	if err != nil || result.ExitCode != 7 {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	calls := runner.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	want := CommandCall{
		Name:  "ollama",
		Args:  []string{"show", "local-ai-example:latest"},
		Dir:   "/manager/work",
		Env:   []string{"OLLAMA_CONTEXT_LENGTH=65536"},
		Stdin: []byte("input"),
	}
	if !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("call = %#v, want %#v", calls[0], want)
	}
	if _, err := runner.Run(context.Background(), platform.Command{Name: "slow"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
	if _, err := runner.Run(context.Background(), platform.Command{Name: "cancelled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	result, err = runner.Run(context.Background(), platform.Command{Name: "mutating-command"})
	if err == nil || string(result.Stdout) != "applied" {
		t.Fatalf("after-command result = %#v, err = %v", result, err)
	}
}

func TestScriptedRunnerHonorsLiveCancellation(t *testing.T) {
	runner := NewScriptedRunner(CommandOutcome{WaitForCancellation: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, platform.Command{Name: "wait"})
		done <- err
	}()
	for len(runner.Calls()) == 0 {
		runtime.Gosched()
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancelled", err)
	}
}

func TestFakeClockAdvancesDeterministically(t *testing.T) {
	start := time.Date(2026, time.October, 20, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(start)
	wake := clock.After(10 * time.Second)
	clock.Advance(9 * time.Second)
	select {
	case <-wake:
		t.Fatal("clock woke too early")
	default:
	}
	if got := clock.Advance(time.Second); got != start.Add(10*time.Second) {
		t.Fatalf("now = %s", got)
	}
	if got := <-wake; got != start.Add(10*time.Second) {
		t.Fatalf("wake = %s", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := clock.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleep error = %v, want cancelled", err)
	}
}

func TestScriptedNetworkRecordsRedirectPartialBodyAndCancellation(t *testing.T) {
	network := NewScriptedNetwork(
		RedirectFixture(302, "https://registry.example.test/v2/model"),
		ResponseFixture{
			StatusCode:     200,
			Body:           []byte("partial-body"),
			BodyErrorAfter: 7,
			BodyError:      ErrPartialBody,
		},
		ResponseFixture{WaitForCancellation: true},
		ResponseFixture{Err: errors.New("network unavailable")},
	)
	response, err := network.Do(context.Background(), platform.Request{
		Method:  "POST",
		URL:     "https://registry.example.test/start",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    strings.NewReader(`{"request":"fixture"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 302 || response.Headers["Location"][0] != "https://registry.example.test/v2/model" {
		t.Fatalf("redirect response = %#v", response)
	}
	response, err = network.Do(context.Background(), platform.Request{
		Method: "GET",
		URL:    "https://registry.example.test/v2/model",
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if !errors.Is(err, ErrPartialBody) || string(body) != "partial" {
		t.Fatalf("partial body = %q, err = %v", body, err)
	}
	calls := network.Calls()
	if len(calls) != 2 || string(calls[0].Body) != `{"request":"fixture"}` {
		t.Fatalf("calls = %#v", calls)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := network.Do(ctx, platform.Request{Method: "GET", URL: "https://api.example.test/wait"})
		done <- err
	}()
	for len(network.Calls()) < 3 {
		runtime.Gosched()
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("network cancellation error = %v", err)
	}
	if _, err := network.Do(context.Background(), platform.Request{
		Method: "GET",
		URL:    "https://api.example.test/error",
	}); err == nil {
		t.Fatal("scripted network error unexpectedly succeeded")
	}
}

func TestTerminalConsentAndEventCapture(t *testing.T) {
	terminal := NewFakeTerminal(PromptResult{Value: "local-ai-example:latest"})
	value, err := terminal.Prompt(context.Background(), Prompt{ID: "model", Message: "Model"})
	if err != nil || value != "local-ai-example:latest" {
		t.Fatalf("prompt value = %q, err = %v", value, err)
	}
	terminal.WriteOutput("ready")
	terminal.WriteError("warning")
	if terminal.Output() != "ready" || terminal.ErrorOutput() != "warning" {
		t.Fatal("terminal output was not captured")
	}

	consent := NewFakeConsent(ConsentResult{Decision: ConsentAccepted})
	decision, err := consent.Request(context.Background(), ConsentRequest{
		ID: "download", Summary: "Download reviewed model", Risk: "large_download",
	})
	if err != nil || decision != ConsentAccepted || len(consent.Requests()) != 1 {
		t.Fatalf("consent = %q, err = %v, requests = %v", decision, err, consent.Requests())
	}

	events := &EventRecorder{}
	if err := events.Emit(map[string]any{"record_type": "event"}); err != nil {
		t.Fatal(err)
	}
	if len(events.Events()) != 1 {
		t.Fatal("event was not captured")
	}
}

func TestTempManagerPathsAreIsolatedFromRealHome(t *testing.T) {
	paths := NewTempManagerPaths(t)
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	memory := MemoryManagerPaths("/manager")
	for _, candidate := range []string{
		memory.Home,
		memory.Config,
		memory.Data,
		memory.Cache,
		memory.LocalAppData,
		memory.Temp,
	} {
		if candidate != memory.Root && !strings.HasPrefix(candidate, memory.Root+string(os.PathSeparator)) {
			t.Fatalf("manager path %s is outside root %s", candidate, memory.Root)
		}
	}
	if paths.Home == realHome {
		t.Fatalf("sandbox home %s is the real home", paths.Home)
	}
	environment := strings.Join(paths.Environment("/safe/bin"), "\n")
	for _, expected := range []string{
		"HOME=" + paths.Home,
		"XDG_CONFIG_HOME=" + paths.Config,
		"LOCALAPPDATA=" + paths.LocalAppData,
		"TMPDIR=" + paths.Temp,
	} {
		if !strings.Contains(environment, expected) {
			t.Fatalf("environment does not contain %q", expected)
		}
	}
}

func TestFixtureInventoryIsPrivacySafe(t *testing.T) {
	root := filepath.Join(findRepositoryRoot(t), "testdata", "local-ai-manager")
	required := []string{
		"ollama/version.json",
		"ollama/tags.json",
		"ollama/show.json",
		"ollama/chat-tool-call.json",
		"ollama/ps.json",
		"registry/manifest.json",
		"gguf/valid.gguf.prefix",
		"gguf/truncated.gguf.prefix",
		"gguf/hostile-length.gguf.prefix",
		"vscode/version.txt",
		"vscode/extensions.txt",
		"vscode/profile-missing.stderr",
		"vscode/extension-install.stderr",
		"release/latest.json",
		"release/SHA256SUMS",
		"transactions/planned.json",
		"transactions/interrupted.json",
		"transactions/failed.json",
	}
	for _, name := range required {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("required fixture %s: %v", name, err)
		}
	}

	host, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	banned := []string{
		"authorization:",
		"bearer ",
		"api_key",
		"password",
		"secret",
		"192.168.",
		"10.0.0.",
		"/users/",
		"c:\\users\\",
	}
	if host != "" {
		banned = append(banned, strings.ToLower(host))
	}
	if home != "" {
		banned = append(banned, strings.ToLower(home))
	}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		lower := strings.ToLower(string(data))
		for _, value := range banned {
			if value != "" && strings.Contains(lower, value) {
				t.Errorf("fixture %s contains privacy-sensitive marker %q", name, value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
