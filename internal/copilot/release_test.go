package copilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

func TestSelectReleaseAssetsForEverySupportedPlatform(t *testing.T) {
	release := StableRelease{Version: "1.0.101", Assets: []ReleaseAsset{
		{Name: "SHA256SUMS.txt", Digest: digestOf([]byte("sums"))},
		{Name: "copilot-win32-x64.zip", Digest: digestOf([]byte("windows"))},
		{Name: "copilot-win32-arm64.zip", Digest: digestOf([]byte("windows-arm"))},
		{Name: "copilot-darwin-x64.tar.gz", Digest: digestOf([]byte("darwin"))},
		{Name: "copilot-darwin-arm64.tar.gz", Digest: digestOf([]byte("darwin-arm"))},
		{Name: "copilot-linux-x64.tar.gz", Digest: digestOf([]byte("linux"))},
		{Name: "copilot-linux-arm64.tar.gz", Digest: digestOf([]byte("linux-arm"))},
	}}
	for _, test := range []struct {
		goos, goarch, name string
	}{
		{"windows", "amd64", "copilot-win32-x64.zip"},
		{"windows", "arm64", "copilot-win32-arm64.zip"},
		{"darwin", "amd64", "copilot-darwin-x64.tar.gz"},
		{"darwin", "arm64", "copilot-darwin-arm64.tar.gz"},
		{"linux", "amd64", "copilot-linux-x64.tar.gz"},
		{"linux", "arm64", "copilot-linux-arm64.tar.gz"},
	} {
		asset, checksums, err := SelectReleaseAssets(release, test.goos, test.goarch)
		if err != nil {
			t.Fatalf("%s/%s: %v", test.goos, test.goarch, err)
		}
		if asset.Name != test.name || checksums.Name != "SHA256SUMS.txt" {
			t.Fatalf("%s/%s selected %#v and %#v", test.goos, test.goarch, asset, checksums)
		}
	}
}

func TestReleaseResolverRequiresStableOfficialDigestMetadata(t *testing.T) {
	assetData := []byte("archive")
	checksumData := []byte(fmt.Sprintf("%x  copilot-linux-x64.tar.gz\n", sha256.Sum256(assetData)))
	payload := map[string]any{
		"tag_name":   "v1.0.101",
		"draft":      false,
		"prerelease": false,
		"extra":      "GitHub API fields are intentionally ignored",
		"assets": []map[string]any{
			{
				"name":                 "copilot-linux-x64.tar.gz",
				"browser_download_url": "https://github.com/github/copilot-cli/releases/download/v1.0.101/copilot-linux-x64.tar.gz",
				"size":                 len(assetData),
				"digest":               digestOf(assetData),
			},
			{
				"name":                 "SHA256SUMS.txt",
				"browser_download_url": "https://github.com/github/copilot-cli/releases/download/v1.0.101/SHA256SUMS.txt",
				"size":                 len(checksumData),
				"digest":               digestOf(checksumData),
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	network := testkit.NewScriptedNetwork(testkit.ResponseFixture{StatusCode: http.StatusOK, Body: body})
	release, err := (ReleaseResolver{Network: network}).ResolveStable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release.Version != "1.0.101" || len(release.Assets) != 2 {
		t.Fatalf("resolved release = %#v", release)
	}

	payload["assets"].([]map[string]any)[0]["digest"] = ""
	body, _ = json.Marshal(payload)
	network = testkit.NewScriptedNetwork(testkit.ResponseFixture{StatusCode: http.StatusOK, Body: body})
	_, err = (ReleaseResolver{Network: network}).ResolveStable(context.Background())
	assertCopilotErrorCode(t, err, app.IssueMetadataInconsistent)
}

func TestChecksumAndProvenanceFailuresAreFatal(t *testing.T) {
	archive := []byte("official archive")
	asset := ReleaseAsset{Name: "copilot-linux-x64.tar.gz", Digest: digestOf(archive)}

	t.Run("checksum manifest mismatch", func(t *testing.T) {
		manifest := []byte(fmt.Sprintf("%064x  %s\n", 1, asset.Name))
		_, err := verifyReleaseArchive(asset, archive, manifest)
		assertCopilotErrorCode(t, err, app.IssueChecksumMismatch)
	})

	t.Run("API and manifest disagree", func(t *testing.T) {
		manifest := []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(archive), asset.Name))
		asset.Digest = digestOf([]byte("different"))
		_, err := verifyReleaseArchive(asset, archive, manifest)
		assertCopilotErrorCode(t, err, app.IssueMetadataInconsistent)
	})

	t.Run("download digest mismatch", func(t *testing.T) {
		network := testkit.NewScriptedNetwork(testkit.ResponseFixture{StatusCode: http.StatusOK, Body: archive})
		asset.URL = "https://github.com/github/copilot-cli/releases/download/v1.0.101/copilot-linux-x64.tar.gz"
		asset.Size = int64(len(archive))
		asset.Digest = digestOf([]byte("different"))
		_, err := downloadAsset(context.Background(), network, asset, maxReleaseArchive)
		assertCopilotErrorCode(t, err, app.IssueChecksumMismatch)
	})

	t.Run("truncated download", func(t *testing.T) {
		truncated := archive[:len(archive)-1]
		network := testkit.NewScriptedNetwork(testkit.ResponseFixture{StatusCode: http.StatusOK, Body: truncated})
		asset.URL = "https://github.com/github/copilot-cli/releases/download/v1.0.101/copilot-linux-x64.tar.gz"
		asset.Size = int64(len(archive))
		asset.Digest = digestOf(truncated)
		_, err := downloadAsset(context.Background(), network, asset, maxReleaseArchive)
		assertCopilotErrorCode(t, err, app.IssueDownloadInvalid)
	})
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func assertCopilotErrorCode(t *testing.T, err error, want app.IssueCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error", want)
	}
	var lifecycleError *Error
	if !errors.As(err, &lifecycleError) || lifecycleError.Code != want {
		t.Fatalf("error = %v, want code %s", err, want)
	}
}
