package copilot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/install"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/ollama"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
)

const (
	ownershipMarkerSchema = "local-ai.copilot-receipt/v1"
	defaultCommandTimeout = 30 * time.Second
	maxVersionOutput      = int64(64 << 10)
)

type Clock interface {
	Now() time.Time
}

type Options struct {
	Platform       platform.Capabilities
	Ollama         ollama.RuntimeAPI
	Resolver       ReleaseResolver
	Clock          Clock
	GOOS           string
	GOARCH         string
	CommandTimeout time.Duration
}

type Manager struct {
	platform       platform.Capabilities
	ollama         ollama.RuntimeAPI
	resolver       ReleaseResolver
	clock          Clock
	goos           string
	goarch         string
	commandTimeout time.Duration
}

type Request struct {
	Alias          string
	Endpoint       string
	ExistingAssets []state.ManagedAsset
}

type Status struct {
	CopilotPresent    bool
	Version           string
	VersionSupported  bool
	MinimumVersion    string
	DiscoverySource   string
	Ownership         state.Ownership
	ExecutableLocator string
	OllamaReady       bool
	OllamaVersion     string
	AliasReady        bool
	Alias             string
	LauncherPresent   bool
	LauncherValid     bool
	LauncherOwnership state.Ownership
	LauncherLocator   string
	ProviderValid     bool
	OfflineEnforced   bool
	WireAPI           string
	ProviderType      string
	RequirementDate   string
	RequirementDocs   []string
	Issues            []app.Issue
}

type Diagnosis struct {
	Status  Status
	Issues  []app.Issue
	Healthy bool
}

type Installation struct {
	Path           string
	Version        string
	Digest         string
	Created        bool
	PreviousBackup string
}

type LauncherInstallation struct {
	Path           string
	Digest         string
	Created        bool
	PreviousBackup string
}

type inspection struct {
	Status
	paths          platform.UserPaths
	executable     string
	launcher       string
	marker         ownershipMarker
	markerPresent  bool
	provider       ProviderEnvironment
	expectedLaunch []byte
}

type ownershipMarker struct {
	SchemaVersion   string `json:"schema_version"`
	TransactionID   string `json:"transaction_id"`
	Stage           string `json:"stage"`
	CopilotVersion  string `json:"copilot_version,omitempty"`
	CopilotDigest   string `json:"copilot_digest,omitempty"`
	CopilotManaged  bool   `json:"copilot_managed"`
	LauncherDigest  string `json:"launcher_digest,omitempty"`
	LauncherManaged bool   `json:"launcher_managed"`
	UpdatedAt       string `json:"updated_at"`
}

func NewManager(options Options) *Manager {
	goos := options.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := options.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	timeout := options.CommandTimeout
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	resolver := options.Resolver
	if resolver.Network == nil {
		resolver.Network = options.Platform.Network
	}
	return &Manager{
		platform:       options.Platform,
		ollama:         options.Ollama,
		resolver:       resolver,
		clock:          options.Clock,
		goos:           goos,
		goarch:         goarch,
		commandTimeout: timeout,
	}
}

func (manager *Manager) Status(ctx context.Context, request Request) (Status, error) {
	inspected, err := manager.inspect(ctx, request)
	status := inspected.Status
	status.Issues = diagnoseStatus(status)
	return status, err
}

func (manager *Manager) Diagnose(ctx context.Context, request Request) Diagnosis {
	inspected, err := manager.inspect(ctx, request)
	status := inspected.Status
	issues := diagnoseStatus(status)
	if err != nil && len(issues) == 0 {
		issues = []app.Issue{issueForError(err)}
	}
	status.Issues = issues
	return Diagnosis{Status: status, Issues: issues, Healthy: len(issues) == 0}
}

func (manager *Manager) Repair(ctx context.Context, request Request, transactionID string) (Status, error) {
	if transactionID == "" {
		return Status{}, copilotError(app.IssueStateResume, "repair Copilot integration", errors.New("transaction identity is required"))
	}
	if _, err := manager.InstallOfficial(ctx, request, transactionID); err != nil {
		return Status{}, err
	}
	if _, err := manager.ConfigureLauncher(ctx, request, transactionID); err != nil {
		return Status{}, err
	}
	status, err := manager.Status(ctx, request)
	if err != nil {
		return status, err
	}
	if issues := diagnoseStatus(status); len(issues) > 0 {
		return status, copilotError(issues[0].Code, "verify repaired Copilot integration", errors.New(issues[0].Summary))
	}
	return status, nil
}

func (manager *Manager) InstallOfficial(ctx context.Context, request Request, transactionID string) (Installation, error) {
	if transactionID == "" {
		return Installation{}, copilotError(app.IssueStateResume, "install official Copilot CLI", errors.New("transaction identity is required"))
	}
	inspected, err := manager.inspect(ctx, request)
	if err != nil && !errors.Is(err, platform.ErrApplicationNotFound) {
		var typed *Error
		if !errors.As(err, &typed) ||
			(typed.Code != app.IssueCopilotMissing && typed.Code != app.IssueCopilotVersion && typed.Code != app.IssuePrerequisiteUnusable) {
			return Installation{}, err
		}
	}
	if inspected.CopilotPresent && inspected.VersionSupported {
		return Installation{Path: inspected.executable, Version: inspected.Version, Created: false}, nil
	}
	if manager.platform.FileSystem == nil || manager.platform.Network == nil || manager.platform.Runner == nil || manager.platform.Paths == nil {
		return Installation{}, copilotError(app.IssueInternal, "install official Copilot CLI", errors.New("platform dependencies are incomplete"))
	}
	release, err := manager.resolver.ResolveStable(ctx)
	if err != nil {
		return Installation{}, err
	}
	archiveAsset, checksumAsset, err := SelectReleaseAssets(release, manager.goos, manager.goarch)
	if err != nil {
		return Installation{}, err
	}
	checksums, err := downloadAsset(ctx, manager.platform.Network, checksumAsset, maxChecksumManifest)
	if err != nil {
		return Installation{}, err
	}
	archive, err := downloadAsset(ctx, manager.platform.Network, archiveAsset, maxReleaseArchive)
	if err != nil {
		return Installation{}, err
	}
	if _, err := verifyReleaseArchive(archiveAsset, archive, checksums); err != nil {
		return Installation{}, err
	}
	executableName := "copilot"
	if manager.goos == "windows" {
		executableName += ".exe"
	}
	binary, err := install.ExtractExecutableWithLimit(archive, archiveAsset.Name, executableName, maxCopilotExecutable)
	if err != nil {
		return Installation{}, copilotError(app.IssueDownloadInvalid, "extract official Copilot CLI", err)
	}
	target := OfficialExecutablePath(inspected.paths, manager.goos)
	safeTarget := privacySafePath(inspected.paths.Home, target)
	owned := assetOwned(request.ExistingAssets, state.AssetCopilotApplication, safeTarget) ||
		(inspected.markerPresent && inspected.marker.CopilotManaged)
	var backup string
	if _, statErr := manager.platform.FileSystem.Stat(target); statErr == nil {
		if !owned && !(inspected.markerPresent && inspected.marker.TransactionID == transactionID) {
			return Installation{}, copilotError(app.IssueAssetOwnership, "install official Copilot CLI", errors.New("manager target already contains an unowned executable"))
		}
		backup = target + ".previous"
		current, readErr := manager.platform.FileSystem.ReadFile(target)
		if readErr != nil {
			return Installation{}, copilotError(app.IssuePrerequisiteUnusable, "back up existing manager-owned Copilot CLI", readErr)
		}
		if err := writeAtomic(manager.platform.FileSystem, backup, current, 0o700); err != nil {
			return Installation{}, copilotError(app.IssueStateWrite, "back up existing manager-owned Copilot CLI", err)
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return Installation{}, copilotError(app.IssuePrerequisiteUnusable, "inspect Copilot install target", statErr)
	}
	if err := manager.platform.FileSystem.MkdirAll(inspected.paths.BinDir, 0o700); err != nil {
		return Installation{}, copilotError(app.IssuePrerequisiteUnusable, "create manager bin directory", err)
	}
	marker := inspected.marker
	marker.SchemaVersion = ownershipMarkerSchema
	marker.TransactionID = transactionID
	marker.Stage = "installing"
	marker.CopilotVersion = release.Version
	marker.CopilotDigest = digestBytes(binary)
	marker.CopilotManaged = true
	marker.UpdatedAt = manager.now().Format(time.RFC3339Nano)
	if err := manager.saveMarker(inspected.paths, marker); err != nil {
		return Installation{}, err
	}
	stage := target + ".new"
	if err := manager.platform.FileSystem.WriteFile(stage, binary, 0o700); err != nil {
		return Installation{}, copilotError(app.IssuePrerequisiteUnusable, "stage official Copilot CLI", err)
	}
	if err := manager.platform.FileSystem.Chmod(stage, 0o700); err != nil {
		_ = manager.platform.FileSystem.Remove(stage)
		return Installation{}, copilotError(app.IssuePrerequisiteUnusable, "set official Copilot CLI permissions", err)
	}
	if err := manager.platform.FileSystem.Sync(stage); err != nil {
		_ = manager.platform.FileSystem.Remove(stage)
		return Installation{}, copilotError(app.IssueStateWrite, "sync staged official Copilot CLI", err)
	}
	stagedVersion, err := manager.probeVersion(ctx, stage)
	if err != nil || stagedVersion.Raw != release.Version {
		_ = manager.platform.FileSystem.Remove(stage)
		if err == nil {
			err = errors.New("staged executable version does not match the release")
		}
		return Installation{}, copilotError(app.IssuePublisherInvalid, "verify staged official Copilot CLI", err)
	}
	if err := manager.platform.FileSystem.Rename(stage, target); err != nil {
		_ = manager.platform.FileSystem.Remove(stage)
		return Installation{}, copilotError(app.IssueStateWrite, "activate official Copilot CLI", err)
	}
	if err := manager.platform.FileSystem.Sync(target); err != nil {
		return Installation{}, copilotError(app.IssueStateWrite, "sync official Copilot CLI", err)
	}
	if err := manager.platform.FileSystem.Sync(inspected.paths.BinDir); err != nil {
		return Installation{}, copilotError(app.IssueStateWrite, "sync manager bin directory", err)
	}
	installedVersion, err := manager.probeVersion(ctx, target)
	if err != nil || !installedVersion.AtLeast(minimumVersion()) {
		_ = manager.restoreFile(target, backup)
		if err == nil {
			err = errors.New("installed executable is below the supported minimum")
		}
		return Installation{}, copilotError(app.IssueCopilotVersion, "verify installed official Copilot CLI", err)
	}
	marker.Stage = "copilot_verified"
	marker.CopilotVersion = installedVersion.Raw
	marker.UpdatedAt = manager.now().Format(time.RFC3339Nano)
	if err := manager.saveMarker(inspected.paths, marker); err != nil {
		_ = manager.restoreFile(target, backup)
		return Installation{}, err
	}
	return Installation{
		Path:           target,
		Version:        installedVersion.Raw,
		Digest:         marker.CopilotDigest,
		Created:        true,
		PreviousBackup: backup,
	}, nil
}

func (manager *Manager) ConfigureLauncher(ctx context.Context, request Request, transactionID string) (LauncherInstallation, error) {
	if transactionID == "" {
		return LauncherInstallation{}, copilotError(app.IssueStateResume, "configure Copilot local launcher", errors.New("transaction identity is required"))
	}
	inspected, err := manager.inspect(ctx, request)
	if err != nil {
		return LauncherInstallation{}, err
	}
	if !inspected.CopilotPresent {
		return LauncherInstallation{}, copilotError(app.IssueCopilotMissing, "configure Copilot local launcher", errors.New("official Copilot CLI is missing"))
	}
	if !inspected.VersionSupported {
		return LauncherInstallation{}, copilotError(app.IssueCopilotVersion, "configure Copilot local launcher", errors.New("official Copilot CLI is below the supported minimum"))
	}
	if !inspected.OllamaReady {
		return LauncherInstallation{}, copilotError(app.IssueCopilotOllama, "configure Copilot local launcher", errors.New("Ollama is unavailable"))
	}
	if !inspected.AliasReady {
		return LauncherInstallation{}, copilotError(app.IssueCopilotAlias, "configure Copilot local launcher", errors.New("the exact selected local alias is unavailable"))
	}
	expected, err := LauncherBytes(manager.goos, inspected.executable, inspected.provider)
	if err != nil {
		return LauncherInstallation{}, err
	}
	launcher := inspected.launcher
	safeLauncher := privacySafePath(inspected.paths.Home, launcher)
	owned := assetOwned(request.ExistingAssets, state.AssetCopilotLauncher, safeLauncher) ||
		(inspected.markerPresent && inspected.marker.LauncherManaged)
	var backup string
	if current, readErr := manager.platform.FileSystem.ReadFile(launcher); readErr == nil {
		if !owned && !(inspected.markerPresent && inspected.marker.TransactionID == transactionID) {
			return LauncherInstallation{}, copilotError(app.IssueAssetOwnership, "configure Copilot local launcher", errors.New("an unowned copilot-local launcher already exists"))
		}
		if string(current) == string(expected) && owned {
			return LauncherInstallation{Path: launcher, Digest: digestBytes(expected), Created: false}, nil
		}
		backup = launcher + ".previous"
		if err := writeAtomic(manager.platform.FileSystem, backup, current, 0o700); err != nil {
			return LauncherInstallation{}, copilotError(app.IssueStateWrite, "back up Copilot local launcher", err)
		}
	} else if !errors.Is(readErr, fs.ErrNotExist) {
		return LauncherInstallation{}, copilotError(app.IssueCopilotLauncher, "inspect Copilot local launcher", readErr)
	}
	marker := inspected.marker
	marker.SchemaVersion = ownershipMarkerSchema
	marker.TransactionID = transactionID
	marker.Stage = "launcher_installing"
	marker.LauncherDigest = digestBytes(expected)
	marker.LauncherManaged = true
	marker.UpdatedAt = manager.now().Format(time.RFC3339Nano)
	if err := manager.saveMarker(inspected.paths, marker); err != nil {
		return LauncherInstallation{}, err
	}
	if err := manager.platform.FileSystem.MkdirAll(inspected.paths.BinDir, 0o700); err != nil {
		return LauncherInstallation{}, copilotError(app.IssueCopilotLauncher, "create manager bin directory", err)
	}
	if err := writeAtomic(manager.platform.FileSystem, launcher, expected, 0o700); err != nil {
		return LauncherInstallation{}, copilotError(app.IssueCopilotLauncher, "write Copilot local launcher", err)
	}
	if manager.goos != "windows" {
		if err := manager.platform.FileSystem.Chmod(launcher, 0o700); err != nil {
			_ = manager.restoreFile(launcher, backup)
			return LauncherInstallation{}, copilotError(app.IssueCopilotLauncher, "set Copilot local launcher permissions", err)
		}
	}
	actual, err := manager.platform.FileSystem.ReadFile(launcher)
	if err != nil || string(actual) != string(expected) {
		_ = manager.restoreFile(launcher, backup)
		if err == nil {
			err = errors.New("launcher contents did not verify")
		}
		return LauncherInstallation{}, copilotError(app.IssueCopilotLauncher, "verify Copilot local launcher", err)
	}
	marker.Stage = "verified"
	marker.UpdatedAt = manager.now().Format(time.RFC3339Nano)
	if err := manager.saveMarker(inspected.paths, marker); err != nil {
		_ = manager.restoreFile(launcher, backup)
		return LauncherInstallation{}, err
	}
	return LauncherInstallation{Path: launcher, Digest: marker.LauncherDigest, Created: true, PreviousBackup: backup}, nil
}

func (manager *Manager) RollbackLauncher(ctx context.Context, assets []state.ManagedAsset) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	paths, err := manager.platform.Paths.UserPaths()
	if err != nil {
		return copilotError(app.IssueStateWrite, "resolve Copilot launcher paths", err)
	}
	launcher := LauncherPath(paths, manager.goos)
	safe := privacySafePath(paths.Home, launcher)
	if !assetOwned(assets, state.AssetCopilotLauncher, safe) {
		return copilotError(app.IssueAssetOwnership, "roll back Copilot local launcher", errors.New("manager ownership is not proven"))
	}
	backup := launcher + ".previous"
	if _, err := manager.platform.FileSystem.Stat(backup); err != nil {
		return copilotError(app.IssueAssetOwnership, "roll back Copilot local launcher", errors.New("verified launcher backup is unavailable"))
	}
	if err := manager.restoreFile(launcher, backup); err != nil {
		return copilotError(app.IssueCopilotLauncher, "roll back Copilot local launcher", err)
	}
	data, err := manager.platform.FileSystem.ReadFile(launcher)
	if err != nil {
		return copilotError(app.IssueCopilotLauncher, "verify rolled back Copilot local launcher", err)
	}
	if marker, present := manager.loadMarker(paths); present {
		marker.Stage = "verified"
		marker.LauncherDigest = digestBytes(data)
		marker.LauncherManaged = true
		marker.UpdatedAt = manager.now().Format(time.RFC3339Nano)
		return manager.saveMarker(paths, marker)
	}
	return nil
}

func (manager *Manager) Uninstall(ctx context.Context, assets []state.ManagedAsset) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if manager.platform.Paths == nil || manager.platform.FileSystem == nil {
		return copilotError(app.IssueInternal, "uninstall Copilot integration", errors.New("platform dependencies are incomplete"))
	}
	paths, err := manager.platform.Paths.UserPaths()
	if err != nil {
		return copilotError(app.IssueStateWrite, "resolve Copilot uninstall paths", err)
	}
	targets := []struct {
		assetType state.AssetType
		path      string
	}{
		{state.AssetCopilotLauncher, LauncherPath(paths, manager.goos)},
		{state.AssetCopilotApplication, OfficialExecutablePath(paths, manager.goos)},
	}
	marker, markerPresent := manager.loadMarker(paths)
	removed := false
	for _, target := range targets {
		safe := privacySafePath(paths.Home, target.path)
		if !assetOwned(assets, target.assetType, safe) {
			continue
		}
		if err := manager.platform.FileSystem.Remove(target.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return copilotError(app.IssueAssetOwnership, "remove manager-owned Copilot asset", err)
		}
		removed = true
		switch target.assetType {
		case state.AssetCopilotLauncher:
			marker.LauncherManaged = false
			marker.LauncherDigest = ""
		case state.AssetCopilotApplication:
			marker.CopilotManaged = false
			marker.CopilotDigest = ""
			marker.CopilotVersion = ""
		}
		_ = manager.platform.FileSystem.Remove(target.path + ".previous")
	}
	if !removed || !markerPresent {
		return nil
	}
	if !marker.CopilotManaged && !marker.LauncherManaged {
		if err := manager.platform.FileSystem.Remove(manager.markerPath(paths)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return copilotError(app.IssueStateWrite, "remove Copilot ownership receipt", err)
		}
		return nil
	}
	marker.Stage = "verified"
	marker.UpdatedAt = manager.now().Format(time.RFC3339Nano)
	return manager.saveMarker(paths, marker)
}

func (manager *Manager) Open(ctx context.Context, request Request, arguments []string, input io.Reader, output, errorOutput io.Writer) error {
	status, err := manager.Status(ctx, request)
	if err != nil && len(status.Issues) == 0 {
		return err
	}
	if len(status.Issues) > 0 {
		first := status.Issues[0]
		return copilotError(first.Code, "open Copilot local launcher", errors.New(first.Summary))
	}
	if status.LauncherOwnership != state.OwnershipManagerOwned {
		return copilotError(app.IssueAssetOwnership, "open Copilot local launcher", errors.New("manager ownership is not proven"))
	}
	paths, err := manager.platform.Paths.UserPaths()
	if err != nil {
		return copilotError(app.IssueStateWrite, "resolve Copilot launcher path", err)
	}
	result, err := manager.platform.Runner.Run(ctx, platform.Command{
		Name:      LauncherPath(paths, manager.goos),
		Args:      append([]string(nil), arguments...),
		Stdin:     input,
		Stdout:    output,
		Stderr:    errorOutput,
		NoTimeout: true,
	})
	if err != nil {
		return copilotError(app.IssueCopilotLauncher, "execute Copilot local launcher", err)
	}
	if result.ExitCode != 0 {
		return copilotError(app.IssueCopilotLauncher, "execute Copilot local launcher", fmt.Errorf("launcher exited with code %d", result.ExitCode))
	}
	return nil
}

func (manager *Manager) inspect(ctx context.Context, request Request) (inspection, error) {
	result := inspection{Status: Status{
		MinimumVersion:  MinimumVersion,
		Alias:           request.Alias,
		RequirementDate: RequirementEvidenceDate,
		RequirementDocs: []string{GitHubDocsBYOKURL, GitHubDocsInstallURL, OllamaIntegrationDocsURL},
	}}
	if manager.platform.Paths == nil || manager.platform.FileSystem == nil || manager.platform.Runner == nil {
		return result, copilotError(app.IssueInternal, "inspect Copilot integration", errors.New("platform dependencies are incomplete"))
	}
	paths, err := manager.platform.Paths.UserPaths()
	if err != nil {
		return result, copilotError(app.IssuePrerequisiteUnusable, "resolve user-local Copilot paths", err)
	}
	result.paths = paths
	result.launcher = LauncherPath(paths, manager.goos)
	result.LauncherLocator = privacySafePath(paths.Home, result.launcher)
	result.marker, result.markerPresent = manager.loadMarker(paths)
	provider, providerErr := NewProviderEnvironment(request.Alias, request.Endpoint)
	if providerErr == nil {
		result.provider = provider
		result.ProviderValid = true
		result.OfflineEnforced = provider.Offline == "true"
		result.ProviderType = provider.Type
		result.WireAPI = provider.WireAPI
	}

	if manager.ollama != nil {
		if version, versionErr := manager.ollama.Version(ctx); versionErr == nil {
			result.OllamaReady = true
			result.OllamaVersion = version.Version
			if tags, tagsErr := manager.ollama.Tags(ctx); tagsErr == nil {
				for _, model := range tags.Models {
					name := model.Name
					if name == "" {
						name = model.Model
					}
					if name == request.Alias {
						result.AliasReady = true
						break
					}
				}
			}
		}
	}

	candidates := []platform.Application{}
	if manager.platform.Applications != nil {
		if application, locateErr := manager.platform.Applications.FindExecutable("copilot"); locateErr == nil {
			candidates = append(candidates, application)
		}
	}
	managedTarget := OfficialExecutablePath(paths, manager.goos)
	if _, statErr := manager.platform.FileSystem.Stat(managedTarget); statErr == nil {
		duplicate := false
		for _, candidate := range candidates {
			if filepath.Clean(candidate.Executable) == filepath.Clean(managedTarget) {
				duplicate = true
			}
		}
		if !duplicate {
			candidates = append(candidates, platform.Application{Name: "copilot", Executable: managedTarget, Source: "manager_bin"})
		}
	}
	var probeErr error
	for _, candidate := range candidates {
		version, candidateErr := manager.probeVersion(ctx, candidate.Executable)
		if candidateErr != nil {
			probeErr = candidateErr
			continue
		}
		result.CopilotPresent = true
		result.Version = version.Raw
		result.VersionSupported = version.AtLeast(minimumVersion())
		result.DiscoverySource = candidate.Source
		result.executable = candidate.Executable
		result.ExecutableLocator = privacySafePath(paths.Home, candidate.Executable)
		result.Ownership = state.OwnershipExternalDetected
		if filepath.Clean(candidate.Executable) == filepath.Clean(managedTarget) &&
			(result.marker.CopilotManaged || assetOwned(request.ExistingAssets, state.AssetCopilotApplication, result.ExecutableLocator)) {
			result.Ownership = state.OwnershipManagerOwned
		}
		if result.VersionSupported {
			break
		}
	}
	if result.executable != "" && providerErr == nil {
		expected, launcherErr := LauncherBytes(manager.goos, result.executable, provider)
		if launcherErr == nil {
			result.expectedLaunch = expected
			if actual, readErr := manager.platform.FileSystem.ReadFile(result.launcher); readErr == nil {
				result.LauncherPresent = true
				result.LauncherValid = string(actual) == string(expected)
				result.LauncherOwnership = state.OwnershipExternalDetected
				if result.marker.LauncherManaged || assetOwned(request.ExistingAssets, state.AssetCopilotLauncher, result.LauncherLocator) {
					result.LauncherOwnership = state.OwnershipManagerOwned
				}
			}
		}
	}
	if providerErr != nil {
		return result, providerErr
	}
	if len(candidates) == 0 {
		return result, copilotError(app.IssueCopilotMissing, "discover official Copilot CLI", platform.ErrApplicationNotFound)
	}
	if result.executable == "" && probeErr != nil {
		return result, copilotError(app.IssuePrerequisiteUnusable, "query official Copilot CLI", probeErr)
	}
	return result, nil
}

func (manager *Manager) probeVersion(ctx context.Context, executable string) (Version, error) {
	result, err := manager.platform.Runner.Run(ctx, platform.Command{
		Name:        executable,
		Args:        []string{"--version"},
		Timeout:     manager.commandTimeout,
		OutputLimit: maxVersionOutput,
	})
	if err != nil {
		return Version{}, err
	}
	if result.ExitCode != 0 || result.StdoutTruncated || result.StderrTruncated {
		return Version{}, errors.New("Copilot CLI version query failed")
	}
	return ParseVersion(string(result.Stdout))
}

func (manager *Manager) markerPath(paths platform.UserPaths) string {
	return filepath.Join(paths.DataDir, "copilot", "receipt.json")
}

func (manager *Manager) loadMarker(paths platform.UserPaths) (ownershipMarker, bool) {
	data, err := manager.platform.FileSystem.ReadFile(manager.markerPath(paths))
	if err != nil {
		return ownershipMarker{}, false
	}
	var marker ownershipMarker
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&marker) != nil || marker.SchemaVersion != ownershipMarkerSchema {
		return ownershipMarker{}, false
	}
	return marker, true
}

func (manager *Manager) saveMarker(paths platform.UserPaths, marker ownershipMarker) error {
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return copilotError(app.IssueStateWrite, "encode Copilot ownership receipt", err)
	}
	data = append(data, '\n')
	path := manager.markerPath(paths)
	if err := manager.platform.FileSystem.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return copilotError(app.IssueStateWrite, "create Copilot ownership receipt directory", err)
	}
	if err := writeAtomic(manager.platform.FileSystem, path, data, 0o600); err != nil {
		return copilotError(app.IssueStateWrite, "write Copilot ownership receipt", err)
	}
	return nil
}

func (manager *Manager) restoreFile(target, backup string) error {
	if backup == "" {
		if err := manager.platform.FileSystem.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := manager.platform.FileSystem.ReadFile(backup)
	if err != nil {
		return err
	}
	return writeAtomic(manager.platform.FileSystem, target, data, 0o700)
}

func (manager *Manager) now() time.Time {
	if manager.clock != nil {
		return manager.clock.Now().UTC()
	}
	return time.Now().UTC()
}

func diagnoseStatus(status Status) []app.Issue {
	var issues []app.Issue
	add := func(code app.IssueCode, summary, repair string, safe bool) {
		issues = append(issues, app.Issue{
			Code: code, Severity: app.SeverityError, Domain: "copilot", Summary: summary,
			RepairID: &repair,
			Remediations: []app.Remediation{{
				Kind: "automatic", Summary: "Run local-ai repair for the Copilot integration.", SafeForYes: safe,
			}},
		})
	}
	if !status.OllamaReady {
		add(app.IssueCopilotOllama, "Ollama is unavailable on the configured loopback endpoint.", "repair.copilot.ollama", true)
		return issues
	}
	if !status.AliasReady {
		add(app.IssueCopilotAlias, "The exact selected local-ai alias is not available in Ollama.", "repair.copilot.alias", false)
	}
	if !status.CopilotPresent {
		add(app.IssueCopilotMissing, "The official GitHub Copilot CLI was not discovered.", "repair.copilot.install", false)
		return issues
	}
	if !status.VersionSupported {
		add(app.IssueCopilotVersion, fmt.Sprintf("GitHub Copilot CLI %s or newer is required.", status.MinimumVersion), "repair.copilot.install", false)
	}
	if !status.ProviderValid || !status.OfflineEnforced || status.ProviderType != "openai" || status.WireAPI != "responses" {
		add(app.IssueCopilotProvider, "The documented offline Ollama OpenAI Responses provider contract is incomplete.", "repair.copilot.launcher", true)
	}
	if !status.LauncherPresent || !status.LauncherValid {
		add(app.IssueCopilotLauncher, "The manager-owned copilot-local launcher is missing or does not match the selected alias.", "repair.copilot.launcher", true)
	} else if status.LauncherOwnership != state.OwnershipManagerOwned {
		add(app.IssueAssetOwnership, "The copilot-local launcher exists but manager ownership is not proven.", "repair.copilot.launcher", false)
	}
	return issues
}

func issueForError(err error) app.Issue {
	code := app.IssueInternal
	var lifecycleError *Error
	if errors.As(err, &lifecycleError) {
		code = lifecycleError.Code
	}
	repair := "repair.copilot.retry"
	return app.Issue{
		Code: code, Severity: app.SeverityError, Domain: "copilot", Summary: "The Copilot integration could not be verified.",
		RepairID:     &repair,
		Remediations: []app.Remediation{{Kind: "retry", Summary: "Retry the redacted Copilot diagnosis.", SafeForYes: true}},
	}
}

func assetOwned(assets []state.ManagedAsset, assetType state.AssetType, locator string) bool {
	for _, asset := range assets {
		if asset.AssetType == assetType && asset.Ownership == state.OwnershipManagerOwned &&
			asset.Locator.Kind == state.LocatorPath && asset.Locator.Value == locator {
			return true
		}
	}
	return false
}

func privacySafePath(home, value string) string {
	cleanHome := filepath.Clean(home)
	cleanValue := filepath.Clean(value)
	relative, err := filepath.Rel(cleanHome, cleanValue)
	if err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(filepath.Join("${HOME}", relative))
	}
	if cleanValue == cleanHome {
		return "${HOME}"
	}
	return "<external>/" + filepath.Base(cleanValue)
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
