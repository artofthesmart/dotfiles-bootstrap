package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
)

type BootstrapInstaller struct {
	FileSystem platform.FileSystem
	Runner     platform.Runner
	Network    platform.Network
	Paths      platform.PathProvider
	Resolver   GitHubReleaseResolver
	State      state.DeploymentStore
	Clock      app.Clock
	GOOS       string
	GOARCH     string
	GHPath     string
}

type InstallOptions struct {
	PathConfigPath string
	PathConsent    bool
}

func DefaultTarget(paths platform.UserPaths, goos string) string {
	name := "local-ai"
	if goos == "windows" {
		name += ".exe"
	}
	return filepath.Join(paths.BinDir, name)
}

func DefaultShellConfig(paths platform.UserPaths, goos, shell string) string {
	if goos == "windows" {
		return filepath.Join(paths.Home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1")
	}
	switch filepath.Base(shell) {
	case "zsh":
		return filepath.Join(paths.Home, ".zshrc")
	case "bash":
		return filepath.Join(paths.Home, ".bashrc")
	default:
		return filepath.Join(paths.Home, ".profile")
	}
}

func (installer BootstrapInstaller) Install(ctx context.Context, options InstallOptions) (state.DeploymentReceipt, error) {
	if err := installer.validate(); err != nil {
		return state.DeploymentReceipt{}, err
	}
	release, err := installer.resolver().ResolveStable(ctx)
	if err != nil {
		return state.DeploymentReceipt{}, err
	}
	asset, checksums, err := SelectReleaseAssets(release, installer.goos(), installer.goarch())
	if err != nil {
		return state.DeploymentReceipt{}, err
	}
	receipt := state.DeploymentReceipt{
		SchemaVersion: state.DeploymentReceiptSchemaVersion,
		Operation:     state.DeploymentInstall,
		Stage:         state.DeploymentStarted,
		Version:       release.Version,
		UpdatedAt:     installer.now(),
	}
	if err := installer.State.SaveDeployment(ctx, receipt); err != nil {
		return receipt, typed(app.IssueStateWrite, err)
	}
	fail := func(cause error) (state.DeploymentReceipt, error) {
		receipt.Stage = state.DeploymentFailed
		receipt.UpdatedAt = installer.now()
		var deploymentError *DeploymentError
		if errors.As(cause, &deploymentError) {
			receipt.IssueCode = state.IssueCode(deploymentError.Code)
		}
		if saveErr := installer.State.SaveDeployment(ctx, receipt); saveErr != nil {
			return receipt, typed(app.IssueStateWrite, saveErr)
		}
		return receipt, cause
	}

	sumsData, err := Download(ctx, installer.Network, checksums, MaxChecksums)
	if err != nil {
		return fail(err)
	}
	sums, err := ParseSHA256SUMS(sumsData)
	if err != nil {
		return fail(err)
	}
	archive, err := Download(ctx, installer.Network, asset, MaxReleaseAsset)
	if err != nil {
		return fail(err)
	}
	if _, err := VerifySHA256(asset.Name, archive, sums); err != nil {
		return fail(err)
	}
	executableName := "local-ai"
	if installer.goos() == "windows" {
		executableName += ".exe"
	}
	binary, err := ExtractExecutable(archive, asset.Name, executableName)
	if err != nil {
		return fail(err)
	}
	paths, err := installer.Paths.UserPaths()
	if err != nil {
		return fail(err)
	}
	target := DefaultTarget(paths, installer.goos())
	if _, err := installer.FileSystem.Stat(target); err == nil {
		return fail(typed(app.IssueConfigurationConflict, errors.New("manager target already exists; use local-ai update")))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fail(err)
	}
	if err := installer.FileSystem.MkdirAll(paths.BinDir, 0o700); err != nil {
		return fail(err)
	}
	stagePath := target + ".new"
	if installer.GHPath != "" {
		archivePath := filepath.Join(paths.CacheDir, asset.Name)
		if err := installer.FileSystem.MkdirAll(paths.CacheDir, 0o700); err != nil {
			return fail(err)
		}
		if err := installer.FileSystem.WriteFile(archivePath, archive, 0o600); err != nil {
			return fail(err)
		}
		result, verifyErr := installer.Runner.Run(ctx, platform.Command{
			Name:    installer.GHPath,
			Args:    []string{"attestation", "verify", archivePath, "--repo", installer.resolver().Repository},
			Timeout: 2 * time.Minute, OutputLimit: 1 << 20,
		})
		_ = installer.FileSystem.Remove(archivePath)
		if verifyErr != nil || result.ExitCode != 0 {
			return fail(typed(app.IssuePublisherInvalid, errors.New("optional GitHub attestation verification failed")))
		}
	}
	if err := installer.FileSystem.WriteFile(stagePath, binary, 0o700); err != nil {
		return fail(err)
	}
	if err := installer.FileSystem.Chmod(stagePath, 0o700); err != nil {
		return fail(err)
	}
	if err := installer.FileSystem.Sync(stagePath); err != nil {
		return fail(err)
	}
	if err := installer.selfCheck(ctx, stagePath); err != nil {
		_ = installer.FileSystem.Remove(stagePath)
		return fail(err)
	}
	receipt.Stage = state.DeploymentStaged
	receipt.Digest = binaryDigest(binary)
	receipt.UpdatedAt = installer.now()
	if err := installer.State.SaveDeployment(ctx, receipt); err != nil {
		return receipt, typed(app.IssueStateWrite, err)
	}
	if err := installer.FileSystem.Rename(stagePath, target); err != nil {
		if _, statErr := installer.FileSystem.Stat(target); statErr == nil {
			_ = installer.FileSystem.Remove(target)
		}
		return fail(err)
	}
	receipt.Stage = state.DeploymentSwapped
	receipt.UpdatedAt = installer.now()
	if err := installer.State.SaveDeployment(ctx, receipt); err != nil {
		_ = installer.FileSystem.Remove(target)
		return receipt, typed(app.IssueStateWrite, err)
	}
	if err := installer.FileSystem.Sync(target); err != nil {
		_ = installer.FileSystem.Remove(target)
		return fail(err)
	}
	if err := installer.FileSystem.Sync(paths.BinDir); err != nil {
		_ = installer.FileSystem.Remove(target)
		return fail(err)
	}
	if err := installer.selfCheck(ctx, target); err != nil {
		_ = installer.FileSystem.Remove(target)
		return fail(err)
	}
	if options.PathConfigPath != "" {
		editor := platform.PathMarker{FileSystem: installer.FileSystem}
		change, err := editor.Plan(options.PathConfigPath, paths.BinDir, installer.goos() == "windows")
		if err != nil {
			return fail(typed(app.IssueAssetOwnership, err))
		}
		if err := editor.Apply(change, options.PathConsent); err != nil {
			return fail(typed(app.IssueConsentDeclined, err))
		}
	}
	receipt.Stage = state.DeploymentVerified
	receipt.UpdatedAt = installer.now()
	if err := installer.State.SaveDeployment(ctx, receipt); err != nil {
		return receipt, typed(app.IssueStateWrite, err)
	}
	receipt.Stage = state.DeploymentSucceeded
	receipt.UpdatedAt = installer.now()
	if err := installer.State.SaveDeployment(ctx, receipt); err != nil {
		return receipt, typed(app.IssueStateWrite, err)
	}
	return receipt, nil
}

type UninstallOptions struct {
	PathConfigPath        string
	RemoveAliases         bool
	RemoveModels          bool
	RemoveVSCodeExtension bool
	RemoveVSCode          bool
	RemoveOllama          bool
	ConfirmDestructive    bool
}

type Uninstaller struct {
	FileSystem platform.FileSystem
	Paths      platform.PathProvider
	State      state.DeploymentStore
	Clock      app.Clock
	GOOS       string
}

func (uninstaller Uninstaller) Uninstall(ctx context.Context, assets []state.ManagedAsset, options UninstallOptions) (state.DeploymentReceipt, error) {
	receipt := state.DeploymentReceipt{
		SchemaVersion: state.DeploymentReceiptSchemaVersion,
		Operation:     state.DeploymentUninstall,
		Stage:         state.DeploymentStarted,
		UpdatedAt:     uninstaller.Clock.Now().UTC(),
	}
	if err := uninstaller.State.SaveDeployment(ctx, receipt); err != nil {
		return receipt, typed(app.IssueStateWrite, err)
	}
	for _, asset := range assets {
		if asset.Ownership != state.OwnershipManagerOwned {
			continue
		}
		if !defaultRemoval(asset.AssetType) && !explicitRemoval(asset.AssetType, options) {
			continue
		}
		if !defaultRemoval(asset.AssetType) && !options.ConfirmDestructive {
			return receipt, typed(app.IssueAssetConfirmation, errors.New("explicit asset removal requires confirmation"))
		}
		if asset.Locator.Kind != state.LocatorPath {
			continue
		}
		if asset.AssetType == state.AssetStateFile {
			continue
		}
		if asset.AssetType == state.AssetPathEntry {
			if options.PathConfigPath == "" || asset.Locator.Value != options.PathConfigPath {
				return receipt, typed(app.IssueAssetOwnership, errors.New("PATH marker locator does not match the requested shell configuration"))
			}
			if err := (platform.PathMarker{FileSystem: uninstaller.FileSystem}).Remove(options.PathConfigPath); err != nil {
				return receipt, typed(app.IssueAssetOwnership, err)
			}
			continue
		}
		info, err := uninstaller.FileSystem.Stat(asset.Locator.Value)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return receipt, err
		}
		if info.IsDir() {
			if asset.AssetType != state.AssetManagerDirectory {
				continue
			}
		}
		if err := uninstaller.FileSystem.Remove(asset.Locator.Value); err != nil {
			return receipt, err
		}
	}
	receipt.Stage = state.DeploymentSucceeded
	receipt.UpdatedAt = uninstaller.Clock.Now().UTC()
	if err := uninstaller.State.SaveDeployment(ctx, receipt); err != nil {
		return receipt, typed(app.IssueStateWrite, err)
	}
	if err := uninstaller.State.ClearDeployment(ctx); err != nil {
		return receipt, typed(app.IssueStateWrite, err)
	}
	return receipt, nil
}

func defaultRemoval(assetType state.AssetType) bool {
	switch assetType {
	case state.AssetManagerBinary, state.AssetManagerDirectory, state.AssetDownloadCache, state.AssetRollbackBinary, state.AssetStateFile, state.AssetPathEntry, state.AssetCopilotApplication, state.AssetCopilotLauncher:
		return true
	default:
		return false
	}
}

func explicitRemoval(assetType state.AssetType, options UninstallOptions) bool {
	switch assetType {
	case state.AssetOllamaAlias:
		return options.RemoveAliases
	case state.AssetOllamaSourceModel:
		return options.RemoveModels
	case state.AssetVSCodeExtension:
		return options.RemoveVSCodeExtension
	case state.AssetVSCodeApplication:
		return options.RemoveVSCode
	case state.AssetOllamaApplication, state.AssetOllamaService:
		return options.RemoveOllama
	default:
		return false
	}
}

func (installer BootstrapInstaller) selfCheck(ctx context.Context, binary string) error {
	result, err := installer.Runner.Run(ctx, platform.Command{
		Name: binary, Args: []string{"version"}, Timeout: 15 * time.Second, OutputLimit: 64 << 10,
	})
	if err != nil || result.ExitCode != 0 || !strings.HasPrefix(strings.TrimSpace(string(result.Stdout)), "local-ai ") {
		return typed(app.IssueUpdateSelfCheck, errors.New("manager self-check failed"))
	}
	return nil
}

func (installer BootstrapInstaller) resolver() GitHubReleaseResolver {
	resolver := installer.Resolver
	resolver.Network = installer.Network
	if resolver.Repository == "" {
		resolver.Repository = DefaultRepository
	}
	return resolver
}

func (installer BootstrapInstaller) validate() error {
	if installer.FileSystem == nil || installer.Runner == nil || installer.Network == nil ||
		installer.Paths == nil || installer.State == nil || installer.Clock == nil {
		return typed(app.IssueInternal, errors.New("bootstrap installer dependencies are incomplete"))
	}
	return nil
}

func (installer BootstrapInstaller) goos() string {
	if installer.GOOS != "" {
		return installer.GOOS
	}
	return runtime.GOOS
}

func (installer BootstrapInstaller) goarch() string {
	if installer.GOARCH != "" {
		return installer.GOARCH
	}
	return runtime.GOARCH
}

func (installer BootstrapInstaller) now() time.Time {
	return installer.Clock.Now().UTC()
}

func binaryDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
