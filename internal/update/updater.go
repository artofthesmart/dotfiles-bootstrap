package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/install"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
)

type Release struct {
	Version string
	Asset   string
	Digest  string
}

type Updater interface {
	Check(context.Context) (Release, error)
	Apply(context.Context, Release) error
	Rollback(context.Context) error
}

type SelfUpdater struct {
	FileSystem platform.FileSystem
	Runner     platform.Runner
	Network    platform.Network
	Paths      platform.PathProvider
	Resolver   install.GitHubReleaseResolver
	State      state.DeploymentStore
	Clock      app.Clock
	GOOS       string
	GOARCH     string
	GHPath     string
}

func (updater SelfUpdater) Check(ctx context.Context) (Release, error) {
	release, err := updater.resolver().ResolveStable(ctx)
	if err != nil {
		return Release{}, err
	}
	asset, checksums, err := install.SelectReleaseAssets(release, updater.goos(), updater.goarch())
	if err != nil {
		return Release{}, err
	}
	sumsData, err := install.Download(ctx, updater.Network, checksums, install.MaxChecksums)
	if err != nil {
		return Release{}, err
	}
	sums, err := install.ParseSHA256SUMS(sumsData)
	if err != nil {
		return Release{}, err
	}
	digest, ok := sums[asset.Name]
	if !ok {
		return Release{}, &install.DeploymentError{Code: app.IssueMetadataInconsistent, Err: fmt.Errorf("SHA256SUMS has no exact entry for %s", asset.Name)}
	}
	return Release{Version: release.Version, Asset: asset.Name, Digest: digest}, nil
}

func (updater SelfUpdater) Apply(ctx context.Context, requested Release) error {
	if err := updater.validate(); err != nil {
		return err
	}
	release, err := updater.resolver().ResolveStable(ctx)
	if err != nil {
		return err
	}
	asset, checksums, err := install.SelectReleaseAssets(release, updater.goos(), updater.goarch())
	if err != nil {
		return err
	}
	if requested.Version != "" && requested.Version != release.Version {
		return &install.DeploymentError{Code: app.IssueMetadataInconsistent, Err: errors.New("resolved release changed after update check")}
	}
	if requested.Asset != "" && requested.Asset != asset.Name {
		return &install.DeploymentError{Code: app.IssueMetadataInconsistent, Err: errors.New("resolved release asset changed after update check")}
	}
	now := updater.now()
	receipt := state.DeploymentReceipt{
		SchemaVersion: state.DeploymentReceiptSchemaVersion,
		Operation:     state.DeploymentUpdate,
		Stage:         state.DeploymentStarted,
		Version:       release.Version,
		UpdatedAt:     now,
	}
	if err := updater.save(ctx, receipt); err != nil {
		return err
	}

	sumsData, err := install.Download(ctx, updater.Network, checksums, install.MaxChecksums)
	if err != nil {
		return updater.fail(ctx, receipt, err)
	}
	sums, err := install.ParseSHA256SUMS(sumsData)
	if err != nil {
		return updater.fail(ctx, receipt, err)
	}
	archive, err := install.Download(ctx, updater.Network, asset, install.MaxReleaseAsset)
	if err != nil {
		return updater.fail(ctx, receipt, err)
	}
	archiveDigest, err := install.VerifySHA256(asset.Name, archive, sums)
	if err != nil {
		return updater.fail(ctx, receipt, err)
	}
	if requested.Digest != "" && !strings.EqualFold(requested.Digest, archiveDigest) {
		return updater.fail(ctx, receipt, &install.DeploymentError{Code: app.IssueMetadataInconsistent, Err: errors.New("checked release digest changed")})
	}
	executableName := "local-ai"
	if updater.goos() == "windows" {
		executableName += ".exe"
	}
	binary, err := install.ExtractExecutable(archive, asset.Name, executableName)
	if err != nil {
		return updater.fail(ctx, receipt, err)
	}
	paths, err := updater.Paths.UserPaths()
	if err != nil {
		return updater.fail(ctx, receipt, err)
	}
	if updater.GHPath != "" {
		archivePath := filepath.Join(paths.CacheDir, asset.Name)
		if err := updater.FileSystem.MkdirAll(paths.CacheDir, 0o700); err != nil {
			return updater.fail(ctx, receipt, err)
		}
		if err := updater.FileSystem.WriteFile(archivePath, archive, 0o600); err != nil {
			return updater.fail(ctx, receipt, err)
		}
		result, verifyErr := updater.Runner.Run(ctx, platform.Command{
			Name:    updater.GHPath,
			Args:    []string{"attestation", "verify", archivePath, "--repo", updater.resolver().Repository},
			Timeout: 2 * time.Minute, OutputLimit: 1 << 20,
		})
		_ = updater.FileSystem.Remove(archivePath)
		if verifyErr != nil || result.ExitCode != 0 {
			return updater.fail(ctx, receipt, &install.DeploymentError{Code: app.IssuePublisherInvalid, Err: errors.New("optional GitHub attestation verification failed")})
		}
	}
	target := filepath.Join(paths.BinDir, executableName)
	stagePath := target + ".new"
	if err := updater.FileSystem.MkdirAll(paths.BinDir, 0o700); err != nil {
		return updater.fail(ctx, receipt, err)
	}
	if err := updater.FileSystem.WriteFile(stagePath, binary, 0o700); err != nil {
		return updater.fail(ctx, receipt, err)
	}
	if err := updater.FileSystem.Chmod(stagePath, 0o700); err != nil {
		return updater.fail(ctx, receipt, err)
	}
	if err := updater.FileSystem.Sync(stagePath); err != nil {
		return updater.fail(ctx, receipt, err)
	}
	if err := updater.selfCheck(ctx, stagePath); err != nil {
		_ = updater.FileSystem.Remove(stagePath)
		return updater.fail(ctx, receipt, err)
	}
	receipt.Stage = state.DeploymentStaged
	receipt.Digest = digest(binary)
	receipt.UpdatedAt = updater.now()
	if err := updater.save(ctx, receipt); err != nil {
		return err
	}

	var backupPath string
	if _, err := updater.FileSystem.Stat(target); err == nil {
		backupVersion, versionErr := updater.installedVersion(ctx, target)
		if versionErr != nil {
			return updater.fail(ctx, receipt, versionErr)
		}
		backupPath = target + ".rollback-" + safeVersion(backupVersion)
		if _, statErr := updater.FileSystem.Stat(backupPath); statErr == nil {
			if removeErr := updater.FileSystem.Remove(backupPath); removeErr != nil {
				return updater.fail(ctx, receipt, removeErr)
			}
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return updater.fail(ctx, receipt, statErr)
		}
		if err := updater.FileSystem.Rename(target, backupPath); err != nil {
			if updater.exists(backupPath) && !updater.exists(target) {
				_ = updater.FileSystem.Rename(backupPath, target)
			}
			return updater.fail(ctx, receipt, err)
		}
		backupData, err := updater.FileSystem.ReadFile(backupPath)
		if err != nil {
			_ = updater.restore(ctx, target, backupPath)
			return updater.fail(ctx, receipt, err)
		}
		receipt.BackupVersion = backupVersion
		receipt.BackupDigest = digest(backupData)
		receipt.Stage = state.DeploymentBackedUp
		receipt.UpdatedAt = updater.now()
		if err := updater.save(ctx, receipt); err != nil {
			_ = updater.restore(ctx, target, backupPath)
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return updater.fail(ctx, receipt, err)
	}

	if err := updater.FileSystem.Rename(stagePath, target); err != nil {
		if backupPath != "" {
			if updater.exists(target) {
				_ = updater.rollbackSwap(ctx, target, backupPath)
			} else {
				_ = updater.restore(ctx, target, backupPath)
			}
		}
		return updater.fail(ctx, receipt, err)
	}
	receipt.Stage = state.DeploymentSwapped
	receipt.UpdatedAt = updater.now()
	if err := updater.save(ctx, receipt); err != nil {
		if backupPath != "" {
			_ = updater.rollbackSwap(ctx, target, backupPath)
		}
		return err
	}
	if err := updater.FileSystem.Sync(target); err != nil {
		return updater.rollbackFailure(ctx, receipt, target, backupPath, err)
	}
	if err := updater.FileSystem.Sync(paths.BinDir); err != nil {
		return updater.rollbackFailure(ctx, receipt, target, backupPath, err)
	}
	if err := updater.selfCheck(ctx, target); err != nil {
		return updater.rollbackFailure(ctx, receipt, target, backupPath, err)
	}
	receipt.Stage = state.DeploymentVerified
	receipt.UpdatedAt = updater.now()
	if err := updater.save(ctx, receipt); err != nil {
		return updater.rollbackFailure(ctx, receipt, target, backupPath, err)
	}
	receipt.Stage = state.DeploymentSucceeded
	receipt.UpdatedAt = updater.now()
	return updater.save(ctx, receipt)
}

func (updater SelfUpdater) Rollback(ctx context.Context) error {
	if err := updater.validate(); err != nil {
		return err
	}
	receipt, err := updater.State.LoadDeployment(ctx)
	if err != nil {
		return &install.DeploymentError{Code: app.IssueStateResume, Err: fmt.Errorf("load update receipt: %w", err)}
	}
	if receipt.BackupVersion == "" || receipt.BackupDigest == "" {
		return &install.DeploymentError{Code: app.IssueUpdateUnavailable, Err: errors.New("no verified rollback is recorded")}
	}
	paths, err := updater.Paths.UserPaths()
	if err != nil {
		return err
	}
	name := "local-ai"
	if updater.goos() == "windows" {
		name += ".exe"
	}
	target := filepath.Join(paths.BinDir, name)
	backup := target + ".rollback-" + safeVersion(receipt.BackupVersion)
	data, err := updater.FileSystem.ReadFile(backup)
	if err != nil || digest(data) != receipt.BackupDigest {
		return &install.DeploymentError{Code: app.IssueChecksumMismatch, Err: errors.New("rollback binary digest does not match its receipt")}
	}
	failed := target + ".failed"
	_ = updater.FileSystem.Remove(failed)
	if err := updater.FileSystem.Rename(target, failed); err != nil {
		if updater.exists(failed) && !updater.exists(target) {
			_ = updater.FileSystem.Rename(failed, target)
		}
		return err
	}
	if err := updater.FileSystem.Rename(backup, target); err != nil {
		if updater.exists(target) && !updater.exists(backup) {
			if checkErr := updater.selfCheck(ctx, target); checkErr == nil {
				_ = updater.FileSystem.Remove(failed)
				goto rollbackSucceeded
			}
		}
		_ = updater.FileSystem.Rename(failed, target)
		return err
	}
	if err := updater.selfCheck(ctx, target); err != nil {
		_ = updater.FileSystem.Rename(target, backup)
		_ = updater.FileSystem.Rename(failed, target)
		return &install.DeploymentError{Code: app.IssueUpdateSelfCheck, Err: fmt.Errorf("rollback self-check: %w", err)}
	}
	_ = updater.FileSystem.Remove(failed)
rollbackSucceeded:
	receipt.Operation = state.DeploymentRollback
	receipt.Stage = state.DeploymentSucceeded
	receipt.Version = receipt.BackupVersion
	receipt.Digest = receipt.BackupDigest
	receipt.BackupVersion = ""
	receipt.BackupDigest = ""
	receipt.UpdatedAt = updater.now()
	return updater.save(ctx, receipt)
}

func (updater SelfUpdater) resolver() install.GitHubReleaseResolver {
	resolver := updater.Resolver
	resolver.Network = updater.Network
	return resolver
}

func (updater SelfUpdater) validate() error {
	if updater.FileSystem == nil || updater.Runner == nil || updater.Network == nil ||
		updater.Paths == nil || updater.State == nil || updater.Clock == nil {
		return &install.DeploymentError{Code: app.IssueInternal, Err: errors.New("self updater dependencies are incomplete")}
	}
	return nil
}

func (updater SelfUpdater) selfCheck(ctx context.Context, binary string) error {
	result, err := updater.Runner.Run(ctx, platform.Command{
		Name: binary, Args: []string{"version"}, Timeout: 15 * time.Second, OutputLimit: 64 << 10,
	})
	if err != nil || result.ExitCode != 0 || !strings.HasPrefix(strings.TrimSpace(string(result.Stdout)), "local-ai ") {
		if err == nil {
			err = fmt.Errorf("exit %d", result.ExitCode)
		}
		return &install.DeploymentError{Code: app.IssueUpdateSelfCheck, Err: fmt.Errorf("manager self-check failed: %w", err)}
	}
	return nil
}

func (updater SelfUpdater) installedVersion(ctx context.Context, binary string) (string, error) {
	result, err := updater.Runner.Run(ctx, platform.Command{
		Name: binary, Args: []string{"version"}, Timeout: 15 * time.Second, OutputLimit: 64 << 10,
	})
	if err != nil || result.ExitCode != 0 {
		return "", &install.DeploymentError{Code: app.IssuePrerequisiteUnusable, Err: errors.New("installed manager version is unavailable")}
	}
	line := strings.Split(strings.TrimSpace(string(result.Stdout)), "\n")[0]
	version := strings.TrimSpace(strings.TrimPrefix(line, "local-ai "))
	if version == "" || version == line {
		return "", &install.DeploymentError{Code: app.IssueMetadataInconsistent, Err: errors.New("installed manager version output is invalid")}
	}
	return version, nil
}

func (updater SelfUpdater) rollbackFailure(ctx context.Context, receipt state.DeploymentReceipt, target, backup string, cause error) error {
	if backup != "" {
		if err := updater.rollbackSwap(ctx, target, backup); err != nil {
			return &install.DeploymentError{Code: app.IssueStateWrite, Err: fmt.Errorf("update failed and rollback failed: %v: %w", cause, err)}
		}
	}
	return updater.fail(ctx, receipt, &install.DeploymentError{Code: app.IssueUpdateSelfCheck, Err: cause})
}

func (updater SelfUpdater) rollbackSwap(ctx context.Context, target, backup string) error {
	failed := target + ".failed"
	_ = updater.FileSystem.Remove(failed)
	if _, err := updater.FileSystem.Stat(target); err == nil {
		if err := updater.FileSystem.Rename(target, failed); err != nil {
			return err
		}
	}
	if err := updater.FileSystem.Rename(backup, target); err != nil {
		_ = updater.FileSystem.Rename(failed, target)
		return err
	}
	if err := updater.selfCheck(ctx, target); err != nil {
		return err
	}
	_ = updater.FileSystem.Remove(failed)
	return nil
}

func (updater SelfUpdater) restore(ctx context.Context, target, backup string) error {
	if _, err := updater.FileSystem.Stat(target); errors.Is(err, fs.ErrNotExist) {
		if err := updater.FileSystem.Rename(backup, target); err != nil {
			return err
		}
		return updater.selfCheck(ctx, target)
	}
	return nil
}

func (updater SelfUpdater) exists(name string) bool {
	_, err := updater.FileSystem.Stat(name)
	return err == nil
}

func (updater SelfUpdater) fail(ctx context.Context, receipt state.DeploymentReceipt, cause error) error {
	receipt.Stage = state.DeploymentFailed
	receipt.UpdatedAt = updater.now()
	var deploymentError *install.DeploymentError
	if errors.As(cause, &deploymentError) {
		receipt.IssueCode = state.IssueCode(deploymentError.Code)
	}
	if err := updater.save(ctx, receipt); err != nil {
		return &install.DeploymentError{Code: app.IssueStateWrite, Err: fmt.Errorf("persist failure receipt: %w", err)}
	}
	return cause
}

func (updater SelfUpdater) save(ctx context.Context, receipt state.DeploymentReceipt) error {
	if err := updater.State.SaveDeployment(ctx, receipt); err != nil {
		return &install.DeploymentError{Code: app.IssueStateWrite, Err: err}
	}
	return nil
}

func (updater SelfUpdater) now() time.Time {
	return updater.Clock.Now().UTC()
}

func (updater SelfUpdater) goos() string {
	if updater.GOOS != "" {
		return updater.GOOS
	}
	return runtime.GOOS
}

func (updater SelfUpdater) goarch() string {
	if updater.GOARCH != "" {
		return updater.GOARCH
	}
	return runtime.GOARCH
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func safeVersion(version string) string {
	var builder strings.Builder
	for _, character := range version {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '.' || character == '-' || character == '_' {
			builder.WriteRune(character)
		}
	}
	if builder.Len() == 0 {
		return "unknown"
	}
	return builder.String()
}
