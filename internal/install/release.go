package install

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
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

const (
	DefaultRepository    = "artofthesmart/dotfiles-bootstrap"
	DefaultGitHubAPIBase = "https://api.github.com"
	MaxReleaseMetadata   = int64(1 << 20)
	MaxChecksums         = int64(4 << 20)
	MaxReleaseAsset      = int64(256 << 20)
)

type DeploymentError struct {
	Code app.IssueCode
	Err  error
}

func (err *DeploymentError) Error() string {
	return fmt.Sprintf("%s: %v", err.Code, err.Err)
}

func (err *DeploymentError) Unwrap() error {
	return err.Err
}

type ReleaseAsset struct {
	Name string
	URL  string
	Size int64
}

type StableRelease struct {
	Version string
	Tag     string
	Assets  []ReleaseAsset
}

type GitHubReleaseResolver struct {
	Network      platform.Network
	Repository   string
	APIBaseURL   string
	AssetBaseURL string
}

func (resolver GitHubReleaseResolver) ResolveStable(ctx context.Context) (StableRelease, error) {
	if resolver.Network == nil {
		return StableRelease{}, typed(app.IssueInternal, errors.New("release network is not configured"))
	}
	repository := strings.Trim(resolver.Repository, "/")
	if repository == "" {
		repository = DefaultRepository
	}
	base := strings.TrimRight(resolver.APIBaseURL, "/")
	if base == "" {
		base = DefaultGitHubAPIBase
	}
	endpoint := base + "/repos/" + repository + "/releases/latest"
	if err := requireHTTPS(endpoint); err != nil {
		return StableRelease{}, typed(app.IssueHTTPSPolicy, err)
	}
	response, err := resolver.Network.Do(ctx, platform.Request{
		Method:           http.MethodGet,
		URL:              endpoint,
		Headers:          map[string]string{"Accept": "application/vnd.github+json", "User-Agent": "local-ai"},
		Timeout:          30 * time.Second,
		MaxResponseBytes: MaxReleaseMetadata,
	})
	if err != nil {
		return StableRelease{}, typed(app.IssueNetworkUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return StableRelease{}, typed(app.IssueUpdateUnavailable, fmt.Errorf("release API returned HTTP %d", response.StatusCode))
	}
	var payload struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&payload); err != nil {
		return StableRelease{}, typed(app.IssueMetadataInconsistent, fmt.Errorf("decode stable release: %w", err))
	}
	if payload.TagName == "" || payload.Draft || payload.Prerelease {
		return StableRelease{}, typed(app.IssueMetadataInconsistent, errors.New("latest release is not a published stable release"))
	}
	release := StableRelease{Tag: payload.TagName, Version: strings.TrimPrefix(payload.TagName, "v")}
	for _, asset := range payload.Assets {
		assetURL := asset.URL
		if resolver.AssetBaseURL != "" {
			assetURL = strings.TrimRight(resolver.AssetBaseURL, "/") + "/" + url.PathEscape(asset.Name)
		}
		if asset.Name == "" || assetURL == "" {
			return StableRelease{}, typed(app.IssueMetadataInconsistent, errors.New("release asset metadata is incomplete"))
		}
		if err := requireHTTPS(assetURL); err != nil {
			return StableRelease{}, typed(app.IssueHTTPSPolicy, err)
		}
		release.Assets = append(release.Assets, ReleaseAsset{Name: asset.Name, URL: assetURL, Size: asset.Size})
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
	if goos != "windows" && goos != "darwin" && goos != "linux" {
		return ReleaseAsset{}, ReleaseAsset{}, typed(app.IssueHostUnsupported, fmt.Errorf("unsupported OS %q", goos))
	}
	if goarch != "amd64" && goarch != "arm64" {
		return ReleaseAsset{}, ReleaseAsset{}, typed(app.IssueArchitectureUnsupported, fmt.Errorf("unsupported architecture %q", goarch))
	}
	extension := ".tar.gz"
	if goos == "windows" {
		extension = ".zip"
	}
	candidates := []string{
		"local-ai_" + goos + "_" + goarch + extension,
		"local-ai_" + release.Version + "_" + goos + "_" + goarch + extension,
	}
	var selected, checksums ReleaseAsset
	for _, asset := range release.Assets {
		if asset.Name == candidates[0] || asset.Name == candidates[1] {
			if selected.Name != "" {
				return ReleaseAsset{}, ReleaseAsset{}, typed(app.IssueMetadataInconsistent, errors.New("release contains ambiguous platform assets"))
			}
			selected = asset
		}
		if asset.Name == "SHA256SUMS" {
			checksums = asset
		}
	}
	if selected.Name == "" || checksums.Name == "" {
		return ReleaseAsset{}, ReleaseAsset{}, typed(app.IssueUpdateUnavailable, fmt.Errorf("release lacks a compatible platform asset or SHA256SUMS"))
	}
	return selected, checksums, nil
}

func Download(ctx context.Context, network platform.Network, asset ReleaseAsset, limit int64) ([]byte, error) {
	if network == nil || asset.URL == "" {
		return nil, typed(app.IssueInternal, errors.New("download is not configured"))
	}
	if err := requireHTTPS(asset.URL); err != nil {
		return nil, typed(app.IssueHTTPSPolicy, err)
	}
	if limit <= 0 {
		limit = MaxReleaseAsset
	}
	response, err := network.Do(ctx, platform.Request{
		Method:           http.MethodGet,
		URL:              asset.URL,
		Headers:          map[string]string{"Accept": "application/octet-stream", "User-Agent": "local-ai"},
		Timeout:          2 * time.Minute,
		MaxResponseBytes: limit,
	})
	if err != nil {
		return nil, typed(app.IssueNetworkUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, typed(app.IssueDownloadInvalid, fmt.Errorf("asset download returned HTTP %d", response.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, typed(app.IssueDownloadInvalid, fmt.Errorf("read asset: %w", err))
	}
	if int64(len(data)) > limit || (asset.Size > 0 && int64(len(data)) != asset.Size) {
		return nil, typed(app.IssueDownloadInvalid, errors.New("asset download was truncated or exceeded its bound"))
	}
	return data, nil
}

func ParseSHA256SUMS(data []byte) (map[string]string, error) {
	result := map[string]string{}
	for lineNumber, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != 64 {
			return nil, typed(app.IssueMetadataInconsistent, fmt.Errorf("invalid SHA256SUMS line %d", lineNumber+1))
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return nil, typed(app.IssueMetadataInconsistent, fmt.Errorf("invalid SHA-256 on line %d", lineNumber+1))
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == "" || path.Base(name) != name {
			return nil, typed(app.IssueMetadataInconsistent, fmt.Errorf("invalid checksum filename on line %d", lineNumber+1))
		}
		if _, exists := result[name]; exists {
			return nil, typed(app.IssueMetadataInconsistent, fmt.Errorf("duplicate checksum for %s", name))
		}
		result[name] = strings.ToLower(fields[0])
	}
	if len(result) == 0 {
		return nil, typed(app.IssueMetadataInconsistent, errors.New("SHA256SUMS is empty"))
	}
	return result, nil
}

func VerifySHA256(name string, data []byte, sums map[string]string) (string, error) {
	expected, ok := sums[name]
	if !ok {
		return "", typed(app.IssueMetadataInconsistent, fmt.Errorf("SHA256SUMS has no exact entry for %s", name))
	}
	sum := sha256.Sum256(data)
	actual := hex.EncodeToString(sum[:])
	if !strings.EqualFold(actual, expected) {
		return "", typed(app.IssueChecksumMismatch, fmt.Errorf("SHA-256 mismatch for %s", name))
	}
	return actual, nil
}

func requireHTTPS(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Scheme, "https") {
		return errors.New("release URLs must use HTTPS")
	}
	return nil
}

func typed(code app.IssueCode, err error) error {
	return &DeploymentError{Code: code, Err: err}
}
