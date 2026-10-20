package manager

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
)

var (
	safeCommitPattern  = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
	safeVersionPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,4}$`)
)

type supportReport struct {
	SchemaVersion string               `json:"schema_version"`
	Manager       map[string]any       `json:"manager"`
	Hardware      map[string]any       `json:"hardware,omitempty"`
	Copilot       map[string]any       `json:"copilot,omitempty"`
	IssueCodes    []app.IssueCode      `json:"issue_codes"`
	Transactions  []supportTransaction `json:"transactions"`
}

type supportTransaction struct {
	Command string `json:"command"`
	Status  string `json:"status"`
	Steps   int    `json:"steps"`
}

func (service Service) report(ctx context.Context, operation app.Operation) app.Result {
	report := supportReport{
		SchemaVersion: "local-ai.support-report/v1",
		Manager: map[string]any{
			"version": normalizedVersion(service.Build.Version),
			"commit":  safeBuildValue(service.Build.Commit),
		},
		IssueCodes:   []app.IssueCode{},
		Transactions: []supportTransaction{},
	}
	if operation.Config.Report.IncludeHardwareSummary {
		capabilities, err := service.discover(ctx)
		if err != nil {
			return service.errorResult(operation.Command, err)
		}
		report.Hardware = map[string]any{
			"os":                  capabilities.OS.Family,
			"os_version":          safeOSVersion(capabilities.OS.Version),
			"distribution":        capabilities.OS.Distribution,
			"architecture":        capabilities.Architecture,
			"system_memory_bytes": capabilities.Memory.InstalledBytes,
			"acceleration":        capabilities.Acceleration,
			"gpu_count":           len(capabilities.GPUs),
			"free_storage_bytes":  capabilities.Storage.FreeBytes,
		}
	}
	transaction, err := service.loadState(ctx)
	if err == nil {
		report.Transactions = append(report.Transactions, supportTransaction{
			Command: string(transaction.Command), Status: string(transaction.Status), Steps: len(transaction.Steps),
		})
		if operation.Config.Report.IncludeRecentIssueCodes {
			for _, value := range transaction.Issues {
				report.IssueCodes = append(report.IssueCodes, app.IssueCode(value.Code))
			}
		}
	} else if !errors.Is(err, state.ErrNoState) {
		return service.errorResult(operation.Command, err)
	}
	if service.Copilot != nil {
		alias := aliasFromTransaction(transaction)
		if alias == "" && operation.Config.Model.CatalogID != "" {
			alias = aliasFor(operation.Config.Model.CatalogID)
		}
		diagnosis := service.Copilot.Diagnose(ctx, service.copilotRequest(operation, transaction, alias))
		report.Copilot = map[string]any{
			"copilot_present":   diagnosis.Status.CopilotPresent,
			"version_supported": diagnosis.Status.VersionSupported,
			"launcher_present":  diagnosis.Status.LauncherPresent,
			"launcher_valid":    diagnosis.Status.LauncherValid,
			"offline_enforced":  diagnosis.Status.OfflineEnforced,
			"alias_ready":       diagnosis.Status.AliasReady,
		}
		if operation.Config.Report.IncludeRecentIssueCodes {
			for _, value := range diagnosis.Issues {
				report.IssueCodes = append(report.IssueCodes, value.Code)
			}
		}
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return service.errorResult(operation.Command, err)
	}
	data = append(data, '\n')
	path := operation.Options.OutputPath
	if path == "" {
		if service.Paths == nil {
			return service.failure(operation.Command, app.IssueInternal, "A support report destination could not be resolved.")
		}
		paths, pathErr := service.Paths.UserPaths()
		if pathErr != nil {
			return service.errorResult(operation.Command, pathErr)
		}
		path = filepath.Join(paths.DataDir, "reports", "support-report.json")
	}
	if operation.DryRun {
		result := service.base(operation.Command)
		result.Outcome, result.ExitCode, result.Summary = app.OutcomeSuccess, app.ExitSuccess, "Dry run assembled the privacy-allowlisted support report without writing a file."
		result.Components = append(result.Components, component("manager", "ready", true, map[string]any{"report_bytes": len(data)}))
		return result
	}
	if service.FileSystem == nil {
		return service.failure(operation.Command, app.IssueInternal, "The report filesystem is unavailable.")
	}
	if err := service.FileSystem.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return service.errorResult(operation.Command, err)
	}
	if err := service.FileSystem.WriteFile(path, data, 0o600); err != nil {
		return service.errorResult(operation.Command, err)
	}
	result := service.base(operation.Command)
	result.Outcome, result.ExitCode, result.Summary = app.OutcomeSuccess, app.ExitSuccess, "A redacted support report was created."
	result.Components = append(result.Components, component("manager", "ready", true, map[string]any{
		"report_path": privacyPath(path), "report_bytes": len(data),
	}))
	return result
}

func safeBuildValue(value string) string {
	value = strings.TrimSpace(value)
	if !safeCommitPattern.MatchString(value) {
		return "unknown"
	}
	return strings.ToLower(value)
}

func safeOSVersion(value string) string {
	value = strings.TrimSpace(value)
	if !safeVersionPattern.MatchString(value) {
		return "unknown"
	}
	return value
}

func privacyPath(value string) string {
	clean := filepath.Clean(value)
	parts := strings.Split(clean, string(filepath.Separator))
	for index, part := range parts {
		if part == "home" && index+1 < len(parts) {
			parts[index+1] = "${HOME}"
			return strings.Join(parts[index+1:], string(filepath.Separator))
		}
		if strings.EqualFold(part, "Users") && index+1 < len(parts) {
			parts[index+1] = "${HOME}"
			return strings.Join(parts[index+1:], string(filepath.Separator))
		}
	}
	if filepath.IsAbs(clean) {
		return "${HOME}/support-report.json"
	}
	return clean
}
