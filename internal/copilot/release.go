package copilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/install"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

const (
	defaultGitHubAPIBase = "https://api.github.com"
	maxReleaseMetadata   = int64(2 << 20)
	maxChecksumManifest  = int64(4 << 20)
	maxReleaseArchive    = int64(512 << 20)
	maxCopilotExecutable = int64(512 << 20)
)

type Error struct {
	Code app.IssueCode
	Op   string
	Err  error
}

func (err *Error) Error() string {
	if err == nil {
		return "Copilot lifecycle operation failed"
	}
	if err.Err == nil {
		return err.Op
	}
	return fmt.Sprintf("%s: %v", err.Op, err.Err)
}

func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

type ReleaseAsset struct {
	Name   string
	URL    string
	Size   int64
	Digest string
}

type StableRelease struct {
	Version string
	Tag     string
	Assets  []ReleaseAsset
}

type ReleaseResolver struct {
	Network    platform.Network
	APIBaseURL string
}

func (resolver ReleaseResolver) ResolveStable(ctx context.Context) (StableRelease, error) {
	if resolver.Network == nil {
		return StableRelease{}, copilotError(app.IssueInternal, "resolve official Copilot release", errors.New("network is not configured"))
	}
	base := strings.TrimRight(resolver.APIBaseURL, "/")
	if base == "" {
		base = defaultGitHubAPIBase
	}
	endpoint := base + "/repos/" + OfficialReleaseRepository + "/releases/latest"
	if err := requireHTTPS(endpoint); err != nil {
		return StableRelease{}, copilotError(app.IssueHTTPSPolicy, "resolve official Copilot release", err)
	}
	response, err := resolver.Network.Do(ctx, platform.Request{
		Method:           http.MethodGet,
		URL:              endpoint,
		Headers:          map[string]string{"Accept": "application/vnd.github+json", "User-Agent": "local-ai"},
		Timeout:          30 * time.Second,
		MaxResponseBytes: maxReleaseMetadata,
	})
	if err != nil {
		return StableRelease{}, copilotError(app.IssueNetworkUnavailable, "resolve official Copilot release", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return StableRelease{}, copilotError(app.IssueUpdateUnavailable, "resolve official Copilot release", fmt.Errorf("GitHub API returned HTTP %d", response.StatusCode))
	}
	var payload struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&payload); err != nil {
		return StableRelease{}, copilotError(app.IssueMetadataInconsistent, "decode official Copilot release", err)
	}
	if payload.TagName == "" || payload.Draft || payload.Prerelease {
		return StableRelease{}, copilotError(app.IssueMetadataInconsistent, "validate official Copilot release", errors.New("latest release is not a published stable release"))
	}
	version, err := ParseVersion(payload.TagName)
	if err != nil {
		return StableRelease{}, copilotError(app.IssueMetadataInconsistent, "validate official Copilot release", err)
	}
	if !version.AtLeast(minimumVersion()) {
		return StableRelease{}, copilotError(app.IssueCopilotVersion, "validate official Copilot release", errors.New("latest stable release is below the supported minimum"))
	}
	release := StableRelease{Version: version.Raw, Tag: payload.TagName}
	for _, source := range payload.Assets {
		if source.Name == "" || source.URL == "" || source.Size <= 0 {
			return StableRelease{}, copilotError(app.IssueMetadataInconsistent, "validate official Copilot release", errors.New("release asset metadata is incomplete"))
		}
		if err := requireHTTPS(source.URL); err != nil {
			return StableRelease{}, copilotError(app.IssueHTTPSPolicy, "validate official Copilot release", err)
		}
		digest, err := normalizeDigest(source.Digest)
		if err != nil {
			return StableRelease{}, copilotError(app.IssueMetadataInconsistent, "validate official Copilot release provenance", err)
		}
		release.Assets = append(release.Assets, ReleaseAsset{Name: source.Name, URL: source.URL, Size: source.Size, Digest: digest})
	}
	return release, nil
}

func SelectReleaseAssets(release StableRelease, goos, goarch string) (ReleaseAsset, ReleaseAsset, error) {
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	architecture := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if architecture == "" {
		return ReleaseAsset{}, ReleaseAsset{}, copilotError(app.IssueArchitectureUnsupported, "select Copilot release asset", fmt.Errorf("unsupported architecture %q", goarch))
	}
	var assetName string
	switch goos {
	case "windows":
		assetName = "copilot-win32-" + architecture + ".zip"
	case "darwin":
		assetName = "copilot-darwin-" + architecture + ".tar.gz"
	case "linux":
		assetName = "copilot-linux-" + architecture + ".tar.gz"
	default:
		return ReleaseAsset{}, ReleaseAsset{}, copilotError(app.IssueHostUnsupported, "select Copilot release asset", fmt.Errorf("unsupported OS %q", goos))
	}
	var selected, checksums ReleaseAsset
	for _, asset := range release.Assets {
		switch asset.Name {
		case assetName:
			if selected.Name != "" {
				return ReleaseAsset{}, ReleaseAsset{}, copilotError(app.IssueMetadataInconsistent, "select Copilot release asset", errors.New("release contains duplicate platform assets"))
			}
			selected = asset
		case "SHA256SUMS.txt":
			if checksums.Name != "" {
				return ReleaseAsset{}, ReleaseAsset{}, copilotError(app.IssueMetadataInconsistent, "select Copilot checksum manifest", errors.New("release contains duplicate checksum manifests"))
			}
			checksums = asset
		}
	}
	if selected.Name == "" || checksums.Name == "" {
		return ReleaseAsset{}, ReleaseAsset{}, copilotError(app.IssueUpdateUnavailable, "select Copilot release asset", errors.New("release lacks a compatible archive or SHA256SUMS.txt"))
	}
	return selected, checksums, nil
}

func downloadAsset(ctx context.Context, network platform.Network, asset ReleaseAsset, limit int64) ([]byte, error) {
	if network == nil {
		return nil, copilotError(app.IssueInternal, "download official Copilot asset", errors.New("network is not configured"))
	}
	if err := requireHTTPS(asset.URL); err != nil {
		return nil, copilotError(app.IssueHTTPSPolicy, "download official Copilot asset", err)
	}
	response, err := network.Do(ctx, platform.Request{
		Method:           http.MethodGet,
		URL:              asset.URL,
		Headers:          map[string]string{"Accept": "application/octet-stream", "User-Agent": "local-ai"},
		Timeout:          5 * time.Minute,
		MaxResponseBytes: limit,
	})
	if err != nil {
		return nil, copilotError(app.IssueNetworkUnavailable, "download official Copilot asset", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, copilotError(app.IssueDownloadInvalid, "download official Copilot asset", fmt.Errorf("asset returned HTTP %d", response.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, copilotError(app.IssueDownloadInvalid, "read official Copilot asset", err)
	}
	if int64(len(data)) > limit || (asset.Size > 0 && int64(len(data)) != asset.Size) {
		return nil, copilotError(app.IssueDownloadInvalid, "read official Copilot asset", errors.New("asset was truncated or exceeded its bound"))
	}
	if err := verifyDigest(data, asset.Digest); err != nil {
		return nil, copilotError(app.IssueChecksumMismatch, "verify GitHub release asset digest", err)
	}
	return data, nil
}

func verifyReleaseArchive(archive ReleaseAsset, archiveData, checksumsData []byte) (string, error) {
	sums, err := install.ParseSHA256SUMS(checksumsData)
	if err != nil {
		return "", copilotError(app.IssueMetadataInconsistent, "parse official Copilot checksum manifest", err)
	}
	digest, err := install.VerifySHA256(archive.Name, archiveData, sums)
	if err != nil {
		return "", copilotError(app.IssueChecksumMismatch, "verify official Copilot checksum manifest", err)
	}
	apiDigest := strings.TrimPrefix(archive.Digest, "sha256:")
	if !strings.EqualFold(digest, apiDigest) {
		return "", copilotError(app.IssueMetadataInconsistent, "cross-check official Copilot provenance", errors.New("GitHub API digest and checksum manifest disagree"))
	}
	return "sha256:" + digest, nil
}

func normalizeDigest(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return "", errors.New("release asset lacks a SHA-256 digest")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:")); err != nil {
		return "", errors.New("release asset has an invalid SHA-256 digest")
	}
	return value, nil
}

func verifyDigest(data []byte, expected string) error {
	normalized, err := normalizeDigest(expected)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	actual := "sha256:" + hex.EncodeToString(sum[:])
	if actual != normalized {
		return errors.New("SHA-256 digest mismatch")
	}
	return nil
}

func requireHTTPS(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Scheme, "https") {
		return errors.New("official release URLs must use HTTPS")
	}
	return nil
}

func copilotError(code app.IssueCode, operation string, err error) error {
	return &Error{Code: code, Op: operation, Err: err}
}
