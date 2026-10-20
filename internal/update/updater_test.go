package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/install"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

type fixedPaths struct {
	paths platform.UserPaths
}

func (provider fixedPaths) UserPaths() (platform.UserPaths, error) {
	return provider.paths, nil
}

func TestSelfUpdaterAtomicUpdateAndExplicitRollback(t *testing.T) {
	filesystem := testkit.NewMemoryFileSystem("/manager", nil)
	updater, target, backup, receiptPath := updateFixture(t, filesystem, nil)
	if err := updater.Apply(context.Background(), Release{}); err != nil {
		t.Fatal(err)
	}
	data, err := filesystem.ReadFile(target)
	if err != nil || string(data) != "new-binary" {
		t.Fatalf("installed binary = %q, err = %v", data, err)
	}
	if old, err := filesystem.ReadFile(backup); err != nil || string(old) != "old-binary" {
		t.Fatalf("rollback binary = %q, err = %v", old, err)
	}
	receiptStore := state.JSONDeploymentStore{FileSystem: filesystem, Path: receiptPath}
	receipt, err := receiptStore.LoadDeployment(context.Background())
	if err != nil || receipt.Stage != state.DeploymentSucceeded || receipt.BackupVersion != "1.0.0" {
		t.Fatalf("receipt = %#v, err = %v", receipt, err)
	}
	updater.Runner.(*testkit.ScriptedRunner).Queue(successVersion("1.0.0"))
	if err := updater.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err = filesystem.ReadFile(target)
	if err != nil || string(data) != "old-binary" {
		t.Fatalf("rolled back binary = %q, err = %v", data, err)
	}
}

func TestSelfUpdaterFailureInjectionAtEverySwapBoundaryRestoresOldBinary(t *testing.T) {
	for _, boundary := range []struct {
		name  string
		from  string
		to    string
		phase testkit.FailurePhase
	}{
		{"backup before", "/manager/bin/local-ai", "/manager/bin/local-ai.rollback-1.0.0", testkit.BeforeMutation},
		{"backup after", "/manager/bin/local-ai", "/manager/bin/local-ai.rollback-1.0.0", testkit.AfterMutation},
		{"activate before", "/manager/bin/local-ai.new", "/manager/bin/local-ai", testkit.BeforeMutation},
		{"activate after", "/manager/bin/local-ai.new", "/manager/bin/local-ai", testkit.AfterMutation},
	} {
		t.Run(boundary.name, func(t *testing.T) {
			failures := testkit.NewFailureInjector()
			failures.Inject(testkit.RenameOperation(boundary.from, boundary.to), boundary.phase, 1, errors.New("swap boundary failure"))
			filesystem := testkit.NewMemoryFileSystem("/manager", failures)
			updater, target, _, _ := updateFixture(t, filesystem, failures)
			err := updater.Apply(context.Background(), Release{})
			if err == nil {
				t.Fatal("update unexpectedly succeeded")
			}
			data, readErr := filesystem.ReadFile(target)
			if readErr != nil || string(data) != "old-binary" {
				backupData, _ := filesystem.ReadFile(target + ".rollback-1.0.0")
				failedData, _ := filesystem.ReadFile(target + ".failed")
				t.Fatalf("old binary was not restored: data = %q, backup = %q, failed = %q, paths = %v, err = %v, update err = %v", data, backupData, failedData, filesystem.Paths(), readErr, err)
			}
		})
	}
}

func TestSelfUpdaterAutomaticallyRollsBackFailedNewBinaryStart(t *testing.T) {
	filesystem := testkit.NewMemoryFileSystem("/manager", nil)
	updater, target, _, _ := updateFixture(t, filesystem, nil)
	updater.Runner = testkit.NewScriptedRunner(
		successVersion("1.0.0"),
		successVersion("2.0.0"),
		testkit.CommandOutcome{Result: platform.CommandResult{ExitCode: 9}},
		successVersion("1.0.0"),
	)
	if err := updater.Apply(context.Background(), Release{}); err == nil {
		t.Fatal("failed post-swap self-check unexpectedly succeeded")
	}
	data, err := filesystem.ReadFile(target)
	if err != nil || string(data) != "old-binary" {
		t.Fatalf("automatic rollback binary = %q, err = %v", data, err)
	}
}

func updateFixture(t *testing.T, filesystem *testkit.MemoryFileSystem, _ *testkit.FailureInjector) (SelfUpdater, string, string, string) {
	t.Helper()
	for _, directory := range []string{"/manager/home", "/manager/bin", "/manager/cache", "/manager/state"} {
		if err := filesystem.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	target := "/manager/bin/local-ai"
	if err := filesystem.WriteFile(target, []byte("old-binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	archive := managerTar(t, []byte("new-binary"))
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
		successVersion("2.0.0"),
		successVersion("1.0.0"),
		successVersion("2.0.0"),
		successVersion("1.0.0"),
		successVersion("1.0.0"),
	)
	receiptPath := "/manager/state/deployment.json"
	return SelfUpdater{
		FileSystem: filesystem,
		Runner:     runner,
		Network:    network,
		Paths: fixedPaths{paths: platform.UserPaths{
			Home: "/manager/home", BinDir: "/manager/bin", CacheDir: "/manager/cache",
			ConfigDir: "/manager/config", DataDir: "/manager/data", TempDir: "/manager/temp",
		}},
		Resolver: install.GitHubReleaseResolver{
			Repository: "owner/repo", APIBaseURL: "https://fixture.test/api",
		},
		State: state.JSONDeploymentStore{FileSystem: filesystem, Path: receiptPath},
		Clock: testkit.NewFakeClock(time.Date(2026, 10, 20, 20, 0, 0, 0, time.UTC)),
		GOOS:  "linux", GOARCH: "amd64",
	}, target, target + ".rollback-1.0.0", receiptPath
}

func successVersion(version string) testkit.CommandOutcome {
	return testkit.CommandOutcome{Result: platform.CommandResult{
		ExitCode: 0, Stdout: []byte("local-ai " + version + "\ncommit: test\nbuilt: test\n"),
	}}
}

func managerTar(t *testing.T, binary []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	if err := writer.WriteHeader(&tar.Header{
		Name: "release/local-ai", Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func assertNotExist(t *testing.T, filesystem platform.FileSystem, name string) {
	t.Helper()
	if _, err := filesystem.Stat(filepath.Clean(name)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s exists or returned unexpected error: %v", name, err)
	}
}
