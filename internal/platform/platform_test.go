package platform_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

func TestOSFileSystemWritesAtomicallyInPathWithSpaces(t *testing.T) {
	var _ platform.AtomicFileSystem = platform.OSFileSystem{}
	paths := testkit.NewTempManagerPaths(t)
	directory := filepath.Join(paths.Data, "state with spaces")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(directory, "transaction state.json")
	filesystem := platform.OSFileSystem{}
	if err := filesystem.WriteFileAtomic(name, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.WriteFileAtomic(name, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := filesystem.ReadFile(name)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("atomic data = %q, err = %v", data, err)
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".transaction state.json.partial-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("partial files = %v, err = %v", matches, err)
	}
}

func TestStandardPathsUsePlatformConventionsAndPreserveSpaces(t *testing.T) {
	tests := []struct {
		name      string
		os        string
		home      string
		env       map[string]string
		wantParts []string
	}{
		{
			name: "windows",
			os:   "windows",
			home: `C:\Users\Test User`,
			env: map[string]string{
				"APPDATA":      `C:\Users\Test User\AppData\Roaming`,
				"LOCALAPPDATA": `C:\Users\Test User\AppData\Local`,
				"TEMP":         `C:\Users\Test User\AppData\Local\Temp`,
			},
			wantParts: []string{"Test User", "LocalAI"},
		},
		{
			name:      "macos",
			os:        "darwin",
			home:      "/Users/Test User",
			env:       map[string]string{"TMPDIR": "/private/var/Test User/temp"},
			wantParts: []string{"Library", "Application Support", "Test User"},
		},
		{
			name: "linux xdg",
			os:   "linux",
			home: "/home/Test User",
			env: map[string]string{
				"XDG_CONFIG_HOME": "/home/Test User/custom config",
				"XDG_DATA_HOME":   "/home/Test User/custom data",
				"XDG_CACHE_HOME":  "/home/Test User/custom cache",
				"TMPDIR":          "/home/Test User/temp",
			},
			wantParts: []string{"custom config", "custom data", "Test User"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := platform.StandardPathProvider{
				OS:      test.os,
				HomeDir: func() (string, error) { return test.home, nil },
				LookupEnv: func(key string) (string, bool) {
					value, ok := test.env[key]
					return value, ok
				},
			}
			paths, err := provider.UserPaths()
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(paths)
			for _, part := range test.wantParts {
				if !strings.Contains(string(encoded), part) {
					t.Fatalf("paths = %s, want part %q", encoded, part)
				}
			}
		})
	}
}

func TestEnvironmentBuilderDoesNotCopyArbitraryEnvironment(t *testing.T) {
	builder := platform.NewEnvironmentBuilderFrom([]string{
		"PATH=/safe/bin",
		"HOME=/safe/home",
	})
	got := builder.Build([]string{"OLLAMA_CONTEXT_LENGTH=65536"})
	want := []string{
		"HOME=/safe/home",
		"OLLAMA_CONTEXT_LENGTH=65536",
		"PATH=/safe/bin",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %#v, want %#v", got, want)
	}
	for _, value := range got {
		if strings.Contains(strings.ToLower(value), "secret") {
			t.Fatalf("environment leaked excluded value: %q", value)
		}
	}
}

func TestApplicationLocatorFindsExecutableInPathWithSpaces(t *testing.T) {
	filesystem := testkit.NewMemoryFileSystem("/manager", nil)
	if err := filesystem.MkdirAll("/manager/Program Files/Local AI", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.WriteFile("/manager/Program Files/Local AI/ollama", []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	locator := platform.StandardApplicationLocator{
		FileSystem:  filesystem,
		Environment: platform.NewEnvironmentBuilderFrom([]string{"PATH=/manager/Program Files/Local AI"}),
		OS:          "linux",
	}
	application, err := locator.FindExecutable("ollama")
	if err != nil {
		t.Fatal(err)
	}
	if application.Executable != "/manager/Program Files/Local AI/ollama" || application.Source != "path" {
		t.Fatalf("application = %#v", application)
	}
}

func TestCommandRunnerUsesExactArgumentsBoundsOutputAndReturnsExitCode(t *testing.T) {
	runner := platform.CommandRunner{
		Environment: platform.NewEnvironmentBuilderFrom(nil),
	}
	result, err := runner.Run(context.Background(), platform.Command{
		Name: os.Args[0],
		Args: []string{
			"-test.run=TestCommandHelper",
			"--",
			"argument with spaces",
			`literal;$HOME&|`,
		},
		Env:         []string{"GO_WANT_PLATFORM_HELPER=arguments"},
		OutputLimit: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", result.ExitCode)
	}
	if got := strings.TrimSpace(string(result.Stderr)); got != "argument with spaces\nliteral;$HOME&|" {
		t.Fatalf("exact arguments = %q", got)
	}
	if len(result.Stdout) != 64 || !result.StdoutTruncated {
		t.Fatalf("stdout bytes = %d, truncated = %v", len(result.Stdout), result.StdoutTruncated)
	}
}

func TestCommandRunnerHonorsCancellation(t *testing.T) {
	runner := platform.CommandRunner{
		Environment: platform.NewEnvironmentBuilderFrom(nil),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result, err := runner.Run(ctx, platform.Command{
		Name: os.Args[0],
		Args: []string{"-test.run=TestCommandHelper"},
		Env:  []string{"GO_WANT_PLATFORM_HELPER=wait"},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("cancelled result = %#v", result)
	}
}

func TestCommandRunnerBoundsStdoutAndStderrIndependently(t *testing.T) {
	runner := platform.CommandRunner{
		Environment: platform.NewEnvironmentBuilderFrom(nil),
	}
	result, err := runner.Run(context.Background(), platform.Command{
		Name:        os.Args[0],
		Args:        []string{"-test.run=TestCommandHelper"},
		Env:         []string{"GO_WANT_PLATFORM_HELPER=output"},
		OutputLimit: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Stdout) != 32 || len(result.Stderr) != 32 ||
		!result.StdoutTruncated || !result.StderrTruncated {
		t.Fatalf("bounded result = %#v", result)
	}
}

func TestCommandHelper(t *testing.T) {
	switch os.Getenv("GO_WANT_PLATFORM_HELPER") {
	case "arguments":
		separator := 0
		for index, value := range os.Args {
			if value == "--" {
				separator = index + 1
				break
			}
		}
		_, _ = os.Stderr.WriteString(strings.Join(os.Args[separator:], "\n"))
		_, _ = os.Stdout.WriteString(strings.Repeat("x", 4096))
		os.Exit(7)
	case "wait":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	case "output":
		_, _ = os.Stdout.WriteString(strings.Repeat("o", 4096))
		_, _ = os.Stderr.WriteString(strings.Repeat("e", 4096))
		os.Exit(0)
	}
}

func TestHTTPNetworkEnforcesHTTPSBoundsAndRedirectPolicy(t *testing.T) {
	var plain *httptest.Server
	plain = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("downgraded"))
	}))
	defer plain.Close()

	var secure *httptest.Server
	secure = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/large":
			_, _ = writer.Write([]byte(strings.Repeat("x", 65)))
		case "/wait":
			<-request.Context().Done()
		case "/redirect":
			http.Redirect(writer, request, plain.URL, http.StatusFound)
		default:
			writer.Header().Set("X-Contract", "ok")
			_, _ = writer.Write([]byte("bounded"))
		}
	}))
	defer secure.Close()

	network := platform.NewHTTPNetworkWithTransport(secure.Client().Transport)
	response, err := network.Do(context.Background(), platform.Request{
		URL:              secure.URL + "/ok",
		MaxResponseBytes: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "bounded" || response.Headers["X-Contract"][0] != "ok" {
		t.Fatalf("response = %q, %#v, err = %v", body, response.Headers, err)
	}
	_, err = network.Do(context.Background(), platform.Request{
		URL:              secure.URL + "/large",
		MaxResponseBytes: 64,
	})
	assertNetworkKind(t, err, platform.NetworkResponseLimit)
	_, err = network.Do(context.Background(), platform.Request{URL: secure.URL + "/redirect"})
	assertNetworkKind(t, err, platform.NetworkRedirectPolicy)
	_, err = network.Do(context.Background(), platform.Request{URL: plain.URL + "/private?token=secret"})
	assertNetworkKind(t, err, platform.NetworkHTTPSRequired)
	var networkError *platform.NetworkError
	if !errors.As(err, &networkError) ||
		strings.Contains(networkError.URL, "127.0.0.1") ||
		strings.Contains(networkError.URL, "token") ||
		strings.Contains(networkError.URL, "secret") {
		t.Fatalf("network error URL was not privacy-safe: %#v", networkError)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = network.Do(ctx, platform.Request{URL: secure.URL + "/wait"})
	assertNetworkKind(t, err, platform.NetworkCancelled)
}

func TestCommandServiceUsesTestkitRunnerContract(t *testing.T) {
	runner := testkit.NewScriptedRunner(
		testkit.CommandOutcome{Result: platform.CommandResult{ExitCode: 0}},
		testkit.CommandOutcome{Result: platform.CommandResult{ExitCode: 0}},
		testkit.CommandOutcome{Result: platform.CommandResult{ExitCode: 0}},
	)
	service := platform.CommandService{
		Runner: runner,
		StatusCalls: map[string]platform.Command{
			"ollama": {Name: "systemctl", Args: []string{"--user", "is-active", "ollama"}},
		},
		StartCalls: map[string]platform.Command{
			"ollama": {Name: "systemctl", Args: []string{"--user", "start", "ollama"}},
		},
		StopCalls: map[string]platform.Command{
			"ollama": {Name: "systemctl", Args: []string{"--user", "stop", "ollama"}},
		},
	}
	state, err := service.Status(context.Background(), "ollama")
	if err != nil || state != platform.ServiceRunning {
		t.Fatalf("state = %q, err = %v", state, err)
	}
	if err := service.Start(context.Background(), "ollama"); err != nil {
		t.Fatal(err)
	}
	if err := service.Stop(context.Background(), "ollama"); err != nil {
		t.Fatal(err)
	}
	calls := runner.Calls()
	if len(calls) != 3 || !reflect.DeepEqual(calls[1].Args, []string{"--user", "start", "ollama"}) {
		t.Fatalf("service calls = %#v", calls)
	}
}

func assertNetworkKind(t *testing.T, err error, want platform.NetworkErrorKind) {
	t.Helper()
	var networkError *platform.NetworkError
	if !errors.As(err, &networkError) || networkError.Kind != want {
		t.Fatalf("network error = %v, want kind %q", err, want)
	}
}
