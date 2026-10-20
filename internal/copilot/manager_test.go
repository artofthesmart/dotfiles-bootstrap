package copilot

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/ollama"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

type fakePaths struct {
	paths platform.UserPaths
	err   error
}

func (paths fakePaths) UserPaths() (platform.UserPaths, error) {
	return paths.paths, paths.err
}

type fakeLocator struct {
	application platform.Application
	err         error
}

func (locator fakeLocator) FindExecutable(string) (platform.Application, error) {
	return locator.application, locator.err
}

func (locator fakeLocator) FindApplication(string) (platform.Application, error) {
	return platform.Application{}, platform.ErrApplicationNotFound
}

type versionRunner struct {
	mu      sync.Mutex
	version string
	calls   []platform.Command
}

type openRunner struct {
	mu      sync.Mutex
	version string
	calls   []platform.Command
}

func (runner *openRunner) Run(ctx context.Context, command platform.Command) (platform.CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return platform.CommandResult{}, err
	}
	runner.mu.Lock()
	runner.calls = append(runner.calls, command)
	runner.mu.Unlock()
	if len(command.Args) == 1 && command.Args[0] == "--version" {
		return platform.CommandResult{Stdout: []byte(runner.version + "\n")}, nil
	}
	return platform.CommandResult{}, nil
}

func (runner *openRunner) Calls() []platform.Command {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return append([]platform.Command(nil), runner.calls...)
}

type selectiveVersionRunner struct {
	mu       sync.Mutex
	versions map[string]string
	calls    []platform.Command
}

func (runner *selectiveVersionRunner) Run(ctx context.Context, command platform.Command) (platform.CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return platform.CommandResult{}, err
	}
	runner.mu.Lock()
	runner.calls = append(runner.calls, command)
	runner.mu.Unlock()
	version, ok := runner.versions[command.Name]
	if !ok {
		return platform.CommandResult{Stdout: []byte("unrelated executable\n")}, nil
	}
	return platform.CommandResult{Stdout: []byte(version + "\n")}, nil
}

func (runner *versionRunner) Run(ctx context.Context, command platform.Command) (platform.CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return platform.CommandResult{}, err
	}
	runner.mu.Lock()
	runner.calls = append(runner.calls, command)
	runner.mu.Unlock()
	if len(command.Args) == 1 && command.Args[0] == "--version" {
		return platform.CommandResult{Stdout: []byte(runner.version + "\n")}, nil
	}
	return platform.CommandResult{ExitCode: 2}, nil
}

func (runner *versionRunner) Calls() []platform.Command {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return append([]platform.Command(nil), runner.calls...)
}

type fakeOllama struct {
	version    string
	models     []string
	versionErr error
	tagsErr    error
}

func (runtime fakeOllama) Status(ctx context.Context) (ollama.RuntimeStatus, error) {
	version, err := runtime.Version(ctx)
	return ollama.RuntimeStatus{Version: version.Version, Available: err == nil}, err
}

func (runtime fakeOllama) ListModels(ctx context.Context) ([]string, error) {
	tags, err := runtime.Tags(ctx)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, model := range tags.Models {
		result = append(result, model.Name)
	}
	return result, nil
}

func (runtime fakeOllama) Version(context.Context) (ollama.VersionResponse, error) {
	if runtime.versionErr != nil {
		return ollama.VersionResponse{}, runtime.versionErr
	}
	return ollama.VersionResponse{Version: runtime.version}, nil
}

func (runtime fakeOllama) Tags(context.Context) (ollama.TagsResponse, error) {
	if runtime.tagsErr != nil {
		return ollama.TagsResponse{}, runtime.tagsErr
	}
	response := ollama.TagsResponse{Models: []ollama.ModelSummary{}}
	for _, name := range runtime.models {
		response.Models = append(response.Models, ollama.ModelSummary{Name: name})
	}
	return response, nil
}

func (fakeOllama) Show(context.Context, ollama.ShowRequest) (ollama.ShowResponse, error) {
	return ollama.ShowResponse{}, nil
}

func (fakeOllama) Chat(context.Context, ollama.ChatRequest) (ollama.ChatResponse, error) {
	return ollama.ChatResponse{}, nil
}

func (fakeOllama) PS(context.Context) (ollama.PSResponse, error) {
	return ollama.PSResponse{}, nil
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func TestDiscoveryPrefersSupportedExistingOfficialExecutable(t *testing.T) {
	filesystem, paths := copilotTestFilesystem(t)
	external := filepath.Join(paths.Home, "tools", "copilot")
	if err := filesystem.MkdirAll(filepath.Dir(external), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.WriteFile(external, []byte("external"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &versionRunner{version: "1.0.101"}
	network := testkit.NewScriptedNetwork()
	manager := newCopilotTestManager(filesystem, paths, runner, network, fakeLocator{
		application: platform.Application{Name: "copilot", Executable: external, Source: "path"},
	}, fakeOllama{version: "0.12.6", models: []string{"local-ai-model:latest"}})

	status, err := manager.Status(context.Background(), Request{Alias: "local-ai-model:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.CopilotPresent || !status.VersionSupported || status.DiscoverySource != "path" ||
		status.Ownership != state.OwnershipExternalDetected {
		t.Fatalf("unexpected discovery status: %+v", status)
	}
	if len(network.Calls()) != 0 {
		t.Fatal("existing supported official executable triggered a release lookup")
	}
	if strings.Contains(status.ExecutableLocator, paths.Home) {
		t.Fatalf("status leaked a literal home path: %+v", status)
	}
}

func TestInstallConfigureDiagnoseRepairAndUninstallManagerOwnedAssets(t *testing.T) {
	filesystem, paths := copilotTestFilesystem(t)
	runner := &versionRunner{version: "1.0.101"}
	network := officialReleaseNetwork(t, "linux", "amd64", []byte("official copilot binary"))
	manager := newCopilotTestManager(
		filesystem,
		paths,
		runner,
		network,
		fakeLocator{err: platform.ErrApplicationNotFound},
		fakeOllama{version: "0.12.6", models: []string{"local-ai-model:latest"}},
	)
	request := Request{Alias: "local-ai-model:latest"}
	installation, err := manager.InstallOfficial(context.Background(), request, "00000000-0000-4000-8000-000000000201")
	if err != nil {
		t.Fatal(err)
	}
	if !installation.Created || installation.Version != "1.0.101" {
		t.Fatalf("installation = %+v", installation)
	}
	launcher, err := manager.ConfigureLauncher(context.Background(), request, "00000000-0000-4000-8000-000000000201")
	if err != nil {
		t.Fatal(err)
	}
	if !launcher.Created {
		t.Fatalf("launcher = %+v", launcher)
	}
	diagnosis := manager.Diagnose(context.Background(), request)
	if !diagnosis.Healthy || len(diagnosis.Issues) != 0 {
		t.Fatalf("diagnosis = %+v", diagnosis)
	}
	launcherData, err := filesystem.ReadFile(launcher.Path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(launcherData)
	for _, exact := range []string{
		"COPILOT_PROVIDER_BASE_URL='http://127.0.0.1:11434/v1'",
		"COPILOT_PROVIDER_TYPE='openai'",
		"COPILOT_PROVIDER_WIRE_API='responses'",
		"COPILOT_PROVIDER_WIRE_MODEL='local-ai-model:latest'",
		"COPILOT_MODEL='local-ai-model:latest'",
		"COPILOT_OFFLINE='true'",
		"--disable-client-telemetry \"$@\"",
	} {
		if !strings.Contains(text, exact) {
			t.Fatalf("launcher missing %q:\n%s", exact, text)
		}
	}
	if strings.Contains(text, "github.com") {
		t.Fatal("launcher contains a cloud fallback")
	}
	markerData, err := filesystem.ReadFile(manager.markerPath(paths))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(markerData), paths.Home) || strings.Contains(string(markerData), request.Alias) {
		t.Fatalf("ownership marker contains user path or model data: %s", markerData)
	}

	launcherLocator := privacySafePath(paths.Home, launcher.Path)
	copilotLocator := privacySafePath(paths.Home, installation.Path)
	assets := []state.ManagedAsset{
		{AssetType: state.AssetCopilotApplication, Ownership: state.OwnershipManagerOwned, Locator: state.AssetLocator{Kind: state.LocatorPath, Value: copilotLocator}},
		{AssetType: state.AssetCopilotLauncher, Ownership: state.OwnershipManagerOwned, Locator: state.AssetLocator{Kind: state.LocatorPath, Value: launcherLocator}},
	}
	if err := filesystem.WriteFile(launcher.Path, []byte("corrupt\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	request.ExistingAssets = assets
	repaired, err := manager.Repair(context.Background(), request, "00000000-0000-4000-8000-000000000202")
	if err != nil {
		t.Fatal(err)
	}
	if !repaired.LauncherValid {
		t.Fatalf("repair did not restore launcher: %+v", repaired)
	}
	if err := manager.Uninstall(context.Background(), assets); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{installation.Path, launcher.Path} {
		if _, err := filesystem.Stat(target); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("manager-owned asset remains at privacy-safe target: %v", err)
		}
	}
}

func TestUnsupportedOrUnrelatedExecutableDoesNotBlockVerifiedUserLocalInstall(t *testing.T) {
	for _, externalVersion := range []string{"0.9.9", ""} {
		name := "unsupported"
		if externalVersion == "" {
			name = "unrelated"
		}
		t.Run(name, func(t *testing.T) {
			filesystem, paths := copilotTestFilesystem(t)
			external := filepath.Join(paths.Home, "tools", "copilot")
			if err := filesystem.MkdirAll(filepath.Dir(external), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := filesystem.WriteFile(external, []byte("external"), 0o700); err != nil {
				t.Fatal(err)
			}
			target := OfficialExecutablePath(paths, "linux")
			runner := &selectiveVersionRunner{versions: map[string]string{
				target:          "1.0.101",
				target + ".new": "1.0.101",
			}}
			if externalVersion != "" {
				runner.versions[external] = externalVersion
			}
			manager := newCopilotTestManager(
				filesystem,
				paths,
				runner,
				officialReleaseNetwork(t, "linux", "amd64", []byte("official copilot binary")),
				fakeLocator{application: platform.Application{Name: "copilot", Executable: external, Source: "path"}},
				fakeOllama{version: "0.12.6", models: []string{"local-ai-model:latest"}},
			)
			installation, err := manager.InstallOfficial(context.Background(), Request{Alias: "local-ai-model:latest"}, "00000000-0000-4000-8000-000000000203")
			if err != nil {
				t.Fatal(err)
			}
			if !installation.Created || installation.Path != target {
				t.Fatalf("installation = %+v", installation)
			}
			status, err := manager.Status(context.Background(), Request{Alias: "local-ai-model:latest"})
			if err != nil {
				t.Fatal(err)
			}
			if !status.VersionSupported || status.Ownership != state.OwnershipManagerOwned || status.DiscoverySource != "manager_bin" {
				t.Fatalf("installed executable was not selected: %+v", status)
			}
		})
	}
}

func TestConfigureLauncherRefusesUnownedTarget(t *testing.T) {
	filesystem, paths := copilotTestFilesystem(t)
	external := filepath.Join(paths.Home, "tools", "copilot")
	if err := filesystem.MkdirAll(filepath.Dir(external), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := filesystem.WriteFile(external, []byte("external"), 0o700); err != nil {
		t.Fatal(err)
	}
	launcher := LauncherPath(paths, "linux")
	if err := filesystem.WriteFile(launcher, []byte("#!/bin/sh\nunowned\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := newCopilotTestManager(
		filesystem,
		paths,
		&versionRunner{version: "1.0.101"},
		testkit.NewScriptedNetwork(),
		fakeLocator{application: platform.Application{Name: "copilot", Executable: external, Source: "path"}},
		fakeOllama{version: "0.12.6", models: []string{"local-ai-model:latest"}},
	)
	_, err := manager.ConfigureLauncher(context.Background(), Request{Alias: "local-ai-model:latest"}, "00000000-0000-4000-8000-000000000204")
	assertCopilotErrorCode(t, err, app.IssueAssetOwnership)
	data, readErr := filesystem.ReadFile(launcher)
	if readErr != nil || string(data) != "#!/bin/sh\nunowned\n" {
		t.Fatalf("unowned launcher was modified: %q, %v", data, readErr)
	}
}

func TestOpenExecutesVerifiedLauncherWithUnchangedArgumentsAndStreams(t *testing.T) {
	filesystem, paths := copilotTestFilesystem(t)
	executable := filepath.Join(paths.Home, "tools", "copilot")
	if err := filesystem.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.WriteFile(executable, []byte("official"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &openRunner{version: "1.0.101"}
	manager := newCopilotTestManager(
		filesystem, paths, runner, testkit.NewScriptedNetwork(),
		fakeLocator{application: platform.Application{Name: "copilot", Executable: executable, Source: "path"}},
		fakeOllama{version: "0.12.6", models: []string{"local-ai-model:latest"}},
	)
	request := Request{Alias: "local-ai-model:latest"}
	if _, err := manager.ConfigureLauncher(context.Background(), request, "00000000-0000-4000-8000-000000000205"); err != nil {
		t.Fatal(err)
	}
	arguments := []string{"prompt with spaces", "--flag=value", `literal"$*'argument`}
	var input, output, errorOutput bytes.Buffer
	if err := manager.Open(context.Background(), request, arguments, &input, &output, &errorOutput); err != nil {
		t.Fatal(err)
	}
	calls := runner.Calls()
	last := calls[len(calls)-1]
	if last.Name != LauncherPath(paths, "linux") || strings.Join(last.Args, "\x00") != strings.Join(arguments, "\x00") {
		t.Fatalf("open command = %#v", last)
	}
	if !last.NoTimeout || last.Stdin != &input || last.Stdout != &output || last.Stderr != &errorOutput {
		t.Fatalf("open streams = %#v", last)
	}
}

func TestLifecycleMutationsRequireTransactionIdentity(t *testing.T) {
	filesystem, paths := copilotTestFilesystem(t)
	manager := newCopilotTestManager(filesystem, paths, &versionRunner{}, testkit.NewScriptedNetwork(), fakeLocator{}, fakeOllama{})
	request := Request{Alias: "local-ai-model:latest"}
	_, err := manager.InstallOfficial(context.Background(), request, "")
	assertCopilotErrorCode(t, err, app.IssueStateResume)
	_, err = manager.ConfigureLauncher(context.Background(), request, "")
	assertCopilotErrorCode(t, err, app.IssueStateResume)
}

func TestDiagnosisNeverFallsBackWhenLocalPrerequisitesFail(t *testing.T) {
	filesystem, paths := copilotTestFilesystem(t)
	external := filepath.Join(paths.Home, "bin", "copilot")
	if err := filesystem.MkdirAll(filepath.Dir(external), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.WriteFile(external, []byte("external"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := newCopilotTestManager(
		filesystem,
		paths,
		&versionRunner{version: "1.0.101"},
		testkit.NewScriptedNetwork(),
		fakeLocator{application: platform.Application{Name: "copilot", Executable: external, Source: "path"}},
		fakeOllama{versionErr: errors.New("offline")},
	)
	diagnosis := manager.Diagnose(context.Background(), Request{Alias: "local-ai-model:latest"})
	if diagnosis.Healthy || len(diagnosis.Issues) == 0 || diagnosis.Issues[0].Code != "LAI-COPILOT-006" {
		t.Fatalf("diagnosis = %+v", diagnosis)
	}
	if diagnosis.Status.OfflineEnforced != true || diagnosis.Status.ProviderType != "openai" {
		t.Fatalf("offline provider contract was weakened: %+v", diagnosis.Status)
	}
}

func TestUninstallRetainsExternallyDetectedCopilot(t *testing.T) {
	filesystem, paths := copilotTestFilesystem(t)
	external := filepath.Join(paths.Home, "bin", "copilot")
	if err := filesystem.MkdirAll(filepath.Dir(external), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.WriteFile(external, []byte("external"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := newCopilotTestManager(filesystem, paths, &versionRunner{version: "1.0.101"}, testkit.NewScriptedNetwork(), fakeLocator{}, fakeOllama{})
	if err := manager.saveMarker(paths, ownershipMarker{
		SchemaVersion: ownershipMarkerSchema,
		TransactionID: "00000000-0000-4000-8000-000000000301",
		Stage:         "verified",
		UpdatedAt:     "2026-10-20T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	assets := []state.ManagedAsset{{
		AssetType: state.AssetCopilotApplication,
		Ownership: state.OwnershipExternalDetected,
		Locator:   state.AssetLocator{Kind: state.LocatorPath, Value: privacySafePath(paths.Home, external)},
	}}
	if err := manager.Uninstall(context.Background(), assets); err != nil {
		t.Fatal(err)
	}
	if _, err := filesystem.Stat(external); err != nil {
		t.Fatalf("external Copilot executable was removed: %v", err)
	}
	if _, err := filesystem.Stat(manager.markerPath(paths)); err != nil {
		t.Fatalf("marker unrelated to the external asset was removed: %v", err)
	}
}

func TestRollbackLauncherRestoresBackupAndUpdatesMarker(t *testing.T) {
	filesystem, paths := copilotTestFilesystem(t)
	manager := newCopilotTestManager(filesystem, paths, &versionRunner{}, testkit.NewScriptedNetwork(), fakeLocator{}, fakeOllama{})
	launcher := LauncherPath(paths, "linux")
	if err := filesystem.WriteFile(launcher, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := filesystem.WriteFile(launcher+".previous", []byte("original"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := manager.saveMarker(paths, ownershipMarker{
		SchemaVersion:   ownershipMarkerSchema,
		TransactionID:   "00000000-0000-4000-8000-000000000302",
		Stage:           "launcher-staged",
		LauncherManaged: true,
		LauncherDigest:  digestBytes([]byte("replacement")),
		UpdatedAt:       "2026-10-20T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	assets := []state.ManagedAsset{{
		AssetType: state.AssetCopilotLauncher,
		Ownership: state.OwnershipManagerOwned,
		Locator:   state.AssetLocator{Kind: state.LocatorPath, Value: privacySafePath(paths.Home, launcher)},
	}}
	if err := manager.RollbackLauncher(context.Background(), assets); err != nil {
		t.Fatal(err)
	}
	data, err := filesystem.ReadFile(launcher)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("rolled back launcher = %q", data)
	}
	marker, ok := manager.loadMarker(paths)
	if !ok {
		t.Fatal("rollback marker was not readable")
	}
	if marker.Stage != "verified" || marker.LauncherDigest != digestBytes(data) || !marker.LauncherManaged {
		t.Fatalf("rollback marker was not reconciled: %+v", marker)
	}
}

func TestRollbackLauncherRejectsMissingBackupWithoutMutation(t *testing.T) {
	filesystem, paths := copilotTestFilesystem(t)
	manager := newCopilotTestManager(filesystem, paths, &versionRunner{}, testkit.NewScriptedNetwork(), fakeLocator{}, fakeOllama{})
	launcher := LauncherPath(paths, "linux")
	if err := filesystem.WriteFile(launcher, []byte("current"), 0o700); err != nil {
		t.Fatal(err)
	}
	asset := state.ManagedAsset{
		AssetType: state.AssetCopilotLauncher,
		Ownership: state.OwnershipManagerOwned,
		Locator:   state.AssetLocator{Kind: state.LocatorPath, Value: privacySafePath(paths.Home, launcher)},
	}
	err := manager.RollbackLauncher(context.Background(), []state.ManagedAsset{asset})
	assertCopilotErrorCode(t, err, app.IssueAssetOwnership)
	data, readErr := filesystem.ReadFile(launcher)
	if readErr != nil || string(data) != "current" {
		t.Fatalf("failed rollback changed launcher: %q, %v", data, readErr)
	}
}

func TestPartialUninstallPreservesRemainingOwnershipMarker(t *testing.T) {
	filesystem, paths := copilotTestFilesystem(t)
	manager := newCopilotTestManager(filesystem, paths, &versionRunner{}, testkit.NewScriptedNetwork(), fakeLocator{}, fakeOllama{})
	launcher := LauncherPath(paths, "linux")
	executable := OfficialExecutablePath(paths, "linux")
	for _, path := range []string{launcher, executable} {
		if err := filesystem.WriteFile(path, []byte("managed"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.saveMarker(paths, ownershipMarker{
		SchemaVersion:   ownershipMarkerSchema,
		TransactionID:   "00000000-0000-4000-8000-000000000303",
		Stage:           "verified",
		CopilotVersion:  "1.0.101",
		CopilotDigest:   digestBytes([]byte("managed")),
		CopilotManaged:  true,
		LauncherDigest:  digestBytes([]byte("managed")),
		LauncherManaged: true,
		UpdatedAt:       "2026-10-20T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	launcherAsset := state.ManagedAsset{
		AssetType: state.AssetCopilotLauncher,
		Ownership: state.OwnershipManagerOwned,
		Locator:   state.AssetLocator{Kind: state.LocatorPath, Value: privacySafePath(paths.Home, launcher)},
	}
	if err := manager.Uninstall(context.Background(), []state.ManagedAsset{launcherAsset}); err != nil {
		t.Fatal(err)
	}
	if _, err := filesystem.Stat(executable); err != nil {
		t.Fatalf("remaining manager executable was removed: %v", err)
	}
	marker, ok := manager.loadMarker(paths)
	if !ok || !marker.CopilotManaged || marker.LauncherManaged || marker.CopilotDigest == "" || marker.LauncherDigest != "" {
		t.Fatalf("partial uninstall marker = %+v, present=%t", marker, ok)
	}
}

func copilotTestFilesystem(t *testing.T) (*testkit.MemoryFileSystem, platform.UserPaths) {
	t.Helper()
	memory := testkit.NewMemoryFileSystem("/sandbox", nil)
	paths := platform.UserPaths{
		Home:      "/sandbox/home",
		ConfigDir: "/sandbox/config",
		DataDir:   "/sandbox/data",
		CacheDir:  "/sandbox/cache",
		BinDir:    "/sandbox/home/.local/bin",
		TempDir:   "/sandbox/temp",
	}
	for _, directory := range []string{paths.Home, paths.ConfigDir, paths.DataDir, paths.CacheDir, paths.BinDir, paths.TempDir} {
		if err := memory.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return memory, paths
}

func newCopilotTestManager(
	filesystem platform.FileSystem,
	paths platform.UserPaths,
	runner platform.Runner,
	network platform.Network,
	locator platform.ApplicationLocator,
	runtimeAPI ollama.RuntimeAPI,
) *Manager {
	return NewManager(Options{
		Platform: platform.Capabilities{
			FileSystem:   filesystem,
			Runner:       runner,
			Network:      network,
			Paths:        fakePaths{paths: paths},
			Applications: locator,
		},
		Ollama: runtimeAPI,
		Resolver: ReleaseResolver{
			Network: network,
		},
		Clock:  fixedClock{now: time.Date(2026, time.October, 20, 20, 0, 0, 0, time.UTC)},
		GOOS:   "linux",
		GOARCH: "amd64",
	})
}

func officialReleaseNetwork(t *testing.T, goos, goarch string, executable []byte) *testkit.ScriptedNetwork {
	t.Helper()
	assetName := "copilot-linux-x64.tar.gz"
	executableName := "copilot"
	if goos == "windows" {
		assetName = "copilot-win32-x64.zip"
		executableName = "copilot.exe"
	}
	if goarch == "arm64" {
		assetName = strings.Replace(assetName, "x64", "arm64", 1)
	}
	archive := makeArchive(t, assetName, executableName, executable)
	checksums := []byte(digestOf(archive)[len("sha256:"):] + "  " + assetName + "\n")
	payload := map[string]any{
		"tag_name": "v1.0.101", "draft": false, "prerelease": false,
		"assets": []map[string]any{
			{"name": assetName, "browser_download_url": "https://github.com/github/copilot-cli/releases/download/v1.0.101/" + assetName, "size": len(archive), "digest": digestOf(archive)},
			{"name": "SHA256SUMS.txt", "browser_download_url": "https://github.com/github/copilot-cli/releases/download/v1.0.101/SHA256SUMS.txt", "size": len(checksums), "digest": digestOf(checksums)},
		},
	}
	metadata, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return testkit.NewScriptedNetwork(
		testkit.ResponseFixture{StatusCode: http.StatusOK, Body: metadata},
		testkit.ResponseFixture{StatusCode: http.StatusOK, Body: checksums},
		testkit.ResponseFixture{StatusCode: http.StatusOK, Body: archive},
	)
}

func makeArchive(t *testing.T, assetName, executableName string, executable []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if strings.HasSuffix(assetName, ".zip") {
		writer := zip.NewWriter(&buffer)
		header := &zip.FileHeader{Name: executableName, Method: zip.Deflate}
		header.SetMode(0o700)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(executable); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return buffer.Bytes()
	}
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: executableName, Mode: 0o700, Size: int64(len(executable)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(tarWriter, bytes.NewReader(executable)); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
