package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

type installPaths struct {
	value platform.UserPaths
}

func (paths installPaths) UserPaths() (platform.UserPaths, error) {
	return paths.value, nil
}

func TestBootstrapInstallerFailureAtActivationBoundariesLeavesNoBinary(t *testing.T) {
	for _, phase := range []testkit.FailurePhase{testkit.BeforeMutation, testkit.AfterMutation} {
		t.Run(string(phase), func(t *testing.T) {
			failures := testkit.NewFailureInjector()
			failures.Inject(
				testkit.RenameOperation("/manager/bin/local-ai.new", "/manager/bin/local-ai"),
				phase, 1, errors.New("activation failure"),
			)
			filesystem := testkit.NewMemoryFileSystem("/manager", failures)
			installer, target := bootstrapFixture(t, filesystem)
			if _, err := installer.Install(context.Background(), InstallOptions{}); err == nil {
				t.Fatal("install unexpectedly succeeded")
			}
			if _, err := filesystem.Stat(target); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("failed install retained target: %v", err)
			}
		})
	}
}

func TestDefaultTargetUsesUserLocalPlatformBinDirectory(t *testing.T) {
	paths := platform.UserPaths{BinDir: "/home/example/.local/bin"}
	if got := DefaultTarget(paths, "linux"); got != "/home/example/.local/bin/local-ai" {
		t.Fatalf("linux target = %q", got)
	}
	windows := platform.UserPaths{BinDir: `C:\Users\Example\AppData\Local\Programs\LocalAI`}
	if got := DefaultTarget(windows, "windows"); got != `C:\Users\Example\AppData\Local\Programs\LocalAI/local-ai.exe` {
		t.Fatalf("windows target = %q", got)
	}
}

func TestBootstrapInstallerOptionalAttestationOnlyUsesConfiguredGH(t *testing.T) {
	filesystem := testkit.NewMemoryFileSystem("/manager", nil)
	installer, target := bootstrapFixture(t, filesystem)
	installer.GHPath = "/manager/bin/gh"
	installer.Runner = testkit.NewScriptedRunner(
		testkit.CommandOutcome{Result: platform.CommandResult{ExitCode: 0}},
		testkit.CommandOutcome{Result: platform.CommandResult{ExitCode: 0, Stdout: []byte("local-ai 2.0.0\n")}},
		testkit.CommandOutcome{Result: platform.CommandResult{ExitCode: 0, Stdout: []byte("local-ai 2.0.0\n")}},
	)
	if _, err := installer.Install(context.Background(), InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := filesystem.Stat(target); err != nil {
		t.Fatal(err)
	}
	calls := installer.Runner.(*testkit.ScriptedRunner).Calls()
	if len(calls) != 3 || calls[0].Name != "/manager/bin/gh" || calls[0].Args[0] != "attestation" {
		t.Fatalf("attestation calls = %#v", calls)
	}
}

func TestUninstallRemovesOnlyManagerOwnedDefaultAssetsAndClearsReceipt(t *testing.T) {
	filesystem := testkit.NewMemoryFileSystem("/manager", nil)
	for _, directory := range []string{"/manager/bin", "/manager/state"} {
		if err := filesystem.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"/manager/bin/local-ai", "/manager/bin/user-model"} {
		if err := filesystem.WriteFile(name, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store := state.JSONDeploymentStore{FileSystem: filesystem, Path: "/manager/state/deployment.json"}
	uninstaller := Uninstaller{
		FileSystem: filesystem,
		Paths:      installPaths{},
		State:      store,
		Clock:      testkit.NewFakeClock(time.Date(2026, 10, 20, 20, 0, 0, 0, time.UTC)),
		GOOS:       "linux",
	}
	assets := []state.ManagedAsset{
		{
			AssetID: "manager", AssetType: state.AssetManagerBinary,
			Ownership: state.OwnershipManagerOwned,
			Locator:   state.AssetLocator{Kind: state.LocatorPath, Value: "/manager/bin/local-ai"},
		},
		{
			AssetID: "model", AssetType: state.AssetOllamaSourceModel,
			Ownership: state.OwnershipUserOwned,
			Locator:   state.AssetLocator{Kind: state.LocatorPath, Value: "/manager/bin/user-model"},
		},
	}
	if _, err := uninstaller.Uninstall(context.Background(), assets, UninstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := filesystem.Stat("/manager/bin/local-ai"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("manager binary remains: %v", err)
	}
	if _, err := filesystem.Stat("/manager/bin/user-model"); err != nil {
		t.Fatalf("user-owned model was removed: %v", err)
	}
	if _, err := filesystem.Stat("/manager/state/deployment.json"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("deployment receipt remains: %v", err)
	}
}

func bootstrapFixture(t *testing.T, filesystem *testkit.MemoryFileSystem) (BootstrapInstaller, string) {
	t.Helper()
	for _, directory := range []string{"/manager/home", "/manager/bin", "/manager/cache", "/manager/state"} {
		if err := filesystem.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	archive := tarArchive(t, []tarFixture{{name: "release/local-ai", body: []byte("new-binary"), mode: 0o755}})
	sum := sha256.Sum256(archive)
	assetName := "local-ai_2.0.0_linux_amd64.tar.gz"
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n")
	releaseJSON, _ := json.Marshal(map[string]any{
		"tag_name": "v2.0.0", "draft": false, "prerelease": false,
		"assets": []map[string]any{
			{"name": assetName, "browser_download_url": "https://fixture.test/" + assetName, "size": len(archive)},
			{"name": "SHA256SUMS", "browser_download_url": "https://fixture.test/SHA256SUMS", "size": len(sums)},
		},
	})
	network := testkit.NewScriptedNetwork(
		testkit.ResponseFixture{Body: releaseJSON},
		testkit.ResponseFixture{Body: sums},
		testkit.ResponseFixture{Body: archive},
	)
	runner := testkit.NewScriptedRunner(
		testkit.CommandOutcome{Result: platform.CommandResult{ExitCode: 0, Stdout: []byte("local-ai 2.0.0\n")}},
		testkit.CommandOutcome{Result: platform.CommandResult{ExitCode: 0, Stdout: []byte("local-ai 2.0.0\n")}},
	)
	return BootstrapInstaller{
		FileSystem: filesystem,
		Runner:     runner,
		Network:    network,
		Paths: installPaths{value: platform.UserPaths{
			Home: "/manager/home", BinDir: "/manager/bin", CacheDir: "/manager/cache",
			ConfigDir: "/manager/config", DataDir: "/manager/data", TempDir: "/manager/temp",
		}},
		Resolver: GitHubReleaseResolver{Repository: "owner/repo", APIBaseURL: "https://fixture.test/api"},
		State:    state.JSONDeploymentStore{FileSystem: filesystem, Path: "/manager/state/deployment.json"},
		Clock:    testkit.NewFakeClock(time.Date(2026, 10, 20, 20, 0, 0, 0, time.UTC)),
		GOOS:     "linux", GOARCH: "amd64",
	}, "/manager/bin/local-ai"
}
