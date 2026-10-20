package install

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"testing"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

func TestGitHubStableResolverSelectsInjectedPlatformAsset(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"tag_name": "v1.2.3", "draft": false, "prerelease": false,
		"assets": []map[string]any{
			{"name": "local-ai_1.2.3_linux_arm64.tar.gz", "browser_download_url": "https://downloads.example.test/linux", "size": 12},
			{"name": "SHA256SUMS", "browser_download_url": "https://downloads.example.test/sums", "size": 80},
		},
		"ignored_by_the_resolver": true,
	})
	network := testkit.NewScriptedNetwork(testkit.ResponseFixture{Body: payload})
	resolver := GitHubReleaseResolver{
		Network: network, Repository: "owner/repo", APIBaseURL: "https://github.example.test/api",
	}
	release, err := resolver.ResolveStable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	asset, sums, err := SelectReleaseAssets(release, "linux", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if release.Version != "1.2.3" || asset.Name != "local-ai_1.2.3_linux_arm64.tar.gz" || sums.Name != "SHA256SUMS" {
		t.Fatalf("release selection = %#v, %#v, %#v", release, asset, sums)
	}
	if got := network.Calls()[0].URL; got != "https://github.example.test/api/repos/owner/repo/releases/latest" {
		t.Fatalf("release API URL = %q", got)
	}
}

func TestReleaseResolverAndDownloadRejectNonHTTPSAndTruncation(t *testing.T) {
	resolver := GitHubReleaseResolver{
		Network: testkit.NewScriptedNetwork(), APIBaseURL: "http://fixture.test",
	}
	_, err := resolver.ResolveStable(context.Background())
	assertDeploymentCode(t, err, app.IssueHTTPSPolicy)

	network := testkit.NewScriptedNetwork(testkit.ResponseFixture{Body: []byte("short")})
	_, err = Download(context.Background(), network, ReleaseAsset{
		Name: "asset", URL: "https://fixture.test/asset", Size: 99,
	}, 100)
	assertDeploymentCode(t, err, app.IssueDownloadInvalid)
}

func TestSHA256SUMSRequiresExactUniqueFilenameAndHash(t *testing.T) {
	data := []byte("verified")
	sum := sha256.Sum256(data)
	sums, err := ParseSHA256SUMS([]byte(hex.EncodeToString(sum[:]) + "  local-ai_linux_amd64.tar.gz\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySHA256("local-ai_linux_amd64.tar.gz", data, sums); err != nil {
		t.Fatal(err)
	}
	_, err = VerifySHA256("local-ai_linux_arm64.tar.gz", data, sums)
	assertDeploymentCode(t, err, app.IssueMetadataInconsistent)
	_, err = VerifySHA256("local-ai_linux_amd64.tar.gz", []byte("corrupt"), sums)
	assertDeploymentCode(t, err, app.IssueChecksumMismatch)
	_, err = ParseSHA256SUMS([]byte(
		hex.EncodeToString(sum[:]) + "  asset\n" +
			hex.EncodeToString(sum[:]) + "  asset\n",
	))
	assertDeploymentCode(t, err, app.IssueMetadataInconsistent)
}

func TestSafeArchiveExtractionRejectsTraversalSymlinksDevicesAndTruncation(t *testing.T) {
	t.Run("valid tar", func(t *testing.T) {
		archive := tarArchive(t, []tarFixture{{name: "release/local-ai", body: []byte("binary"), mode: 0o755, kind: tar.TypeReg}})
		data, err := ExtractExecutable(archive, "local-ai_linux_amd64.tar.gz", "local-ai")
		if err != nil || string(data) != "binary" {
			t.Fatalf("data = %q, err = %v", data, err)
		}
	})
	for _, test := range []struct {
		name     string
		fixtures []tarFixture
	}{
		{"traversal", []tarFixture{{name: "../local-ai", body: []byte("bad"), kind: tar.TypeReg}}},
		{"symlink", []tarFixture{{name: "local-ai", kind: tar.TypeSymlink}}},
		{"device", []tarFixture{{name: "local-ai", kind: tar.TypeChar}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ExtractExecutable(tarArchive(t, test.fixtures), "local-ai_linux_amd64.tar.gz", "local-ai")
			assertDeploymentCode(t, err, app.IssueDownloadInvalid)
		})
	}
	t.Run("truncated", func(t *testing.T) {
		archive := tarArchive(t, []tarFixture{{name: "local-ai", body: []byte("binary"), kind: tar.TypeReg}})
		_, err := ExtractExecutable(archive[:len(archive)/2], "local-ai_linux_amd64.tar.gz", "local-ai")
		assertDeploymentCode(t, err, app.IssueDownloadInvalid)
	})
	t.Run("zip symlink", func(t *testing.T) {
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		header := &zip.FileHeader{Name: "local-ai.exe", Method: zip.Store}
		header.SetMode(0o777 | fs.ModeSymlink)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write([]byte("target"))
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		_, err = ExtractExecutable(buffer.Bytes(), "local-ai_windows_amd64.zip", "local-ai.exe")
		assertDeploymentCode(t, err, app.IssueDownloadInvalid)
	})
}

type tarFixture struct {
	name string
	body []byte
	mode int64
	kind byte
}

func tarArchive(t *testing.T, fixtures []tarFixture) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	writer := tar.NewWriter(compressed)
	for _, fixture := range fixtures {
		mode := fixture.mode
		if mode == 0 {
			mode = 0o644
		}
		kind := fixture.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: fixture.name, Mode: mode, Size: int64(len(fixture.body)), Typeflag: kind}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			if _, err := writer.Write(fixture.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func assertDeploymentCode(t *testing.T, err error, code app.IssueCode) {
	t.Helper()
	var deploymentError *DeploymentError
	if !errors.As(err, &deploymentError) || deploymentError.Code != code {
		t.Fatalf("error = %v, want deployment code %s", err, code)
	}
}
