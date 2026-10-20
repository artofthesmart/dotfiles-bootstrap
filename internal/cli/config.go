package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

const (
	envConfigPath     = "LOCAL_AI_CONFIG"
	envOllamaEndpoint = "LOCAL_AI_OLLAMA_ENDPOINT"
	envNoColor        = "NO_COLOR"
	envAttestation    = "LOCAL_AI_VERIFY_ATTESTATION"
)

var (
	catalogIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	repairIDPattern  = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)
	homePathPattern  = regexp.MustCompile(`^\$\{HOME\}[/\\].+`)
)

type fileConfig struct {
	SchemaVersion string          `json:"schema_version"`
	Behavior      *behaviorConfig `json:"behavior,omitempty"`
	Install       *installConfig  `json:"install,omitempty"`
	Model         *modelConfig    `json:"model,omitempty"`
	Ollama        *ollamaConfig   `json:"ollama,omitempty"`
	VSCode        *vscodeConfig   `json:"vscode,omitempty"`
	Repair        *repairConfig   `json:"repair,omitempty"`
	Update        *updateConfig   `json:"update,omitempty"`
	Report        *reportConfig   `json:"report,omitempty"`
}

type behaviorConfig struct {
	AcceptSafeDefaults *bool   `json:"accept_safe_defaults,omitempty"`
	DryRun             *bool   `json:"dry_run,omitempty"`
	Output             *string `json:"output,omitempty"`
	Color              *string `json:"color,omitempty"`
	ResumePolicy       *string `json:"resume_policy,omitempty"`
}

type installConfig struct {
	ManagerDirectory        *string `json:"manager_directory,omitempty"`
	ManagePath              *bool   `json:"manage_path,omitempty"`
	AllowApplicationInstall *bool   `json:"allow_application_install,omitempty"`
}

type modelConfig struct {
	CatalogID                    *string `json:"catalog_id,omitempty"`
	FallbackCatalogID            *string `json:"fallback_catalog_id,omitempty"`
	ContextTokens                *uint64 `json:"context_tokens,omitempty"`
	AllowExperimental            *bool   `json:"allow_experimental,omitempty"`
	AllowedLicenseUse            *string `json:"allowed_license_use,omitempty"`
	MaxDownloadBytes             *uint64 `json:"max_download_bytes,omitempty"`
	MinimumFreeBytesAfterInstall *uint64 `json:"minimum_free_bytes_after_install,omitempty"`
	PreferExecution              *string `json:"prefer_execution,omitempty"`
}

type ollamaConfig struct {
	Endpoint              *string `json:"endpoint,omitempty"`
	ModelStorage          *string `json:"model_storage,omitempty"`
	ExistingServerRestart *string `json:"existing_server_restart,omitempty"`
	ToolCheckOutputTokens *int    `json:"tool_check_output_tokens,omitempty"`
}

type vscodeConfig struct {
	ProfileName                    *string `json:"profile_name,omitempty"`
	OpenAfterSetup                 *bool   `json:"open_after_setup,omitempty"`
	InstallOfficialOllamaExtension *bool   `json:"install_official_ollama_extension,omitempty"`
	UtilityModelGuidance           *string `json:"utility_model_guidance,omitempty"`
	ToolLimitGuidance              *string `json:"tool_limit_guidance,omitempty"`
}

type repairConfig struct {
	SafeOnly  *bool    `json:"safe_only,omitempty"`
	RepairIDs []string `json:"repair_ids,omitempty"`
}

type updateConfig struct {
	Channel                              *string `json:"channel,omitempty"`
	CheckOnStatus                        *bool   `json:"check_on_status,omitempty"`
	VerifyGitHubAttestationWhenAvailable *bool   `json:"verify_github_attestation_when_available,omitempty"`
}

type reportConfig struct {
	IncludeHardwareSummary    *bool `json:"include_hardware_summary,omitempty"`
	IncludeRecentIssueCodes   *bool `json:"include_recent_issue_codes,omitempty"`
	IncludeRecentRedactedLogs *bool `json:"include_recent_redacted_logs,omitempty"`
	MaximumLogEvents          *int  `json:"maximum_log_events,omitempty"`
}

func loadConfig(fs platform.FileSystem, path string, environment map[string]string) (app.Config, string, error) {
	config := app.DefaultConfig()
	if path == "" {
		path = strings.TrimSpace(environment[envConfigPath])
	}
	if path != "" {
		if fs == nil {
			return app.Config{}, path, errors.New("filesystem is unavailable")
		}
		data, err := fs.ReadFile(path)
		if err != nil {
			return app.Config{}, path, fmt.Errorf("read configuration: %w", err)
		}
		var candidate fileConfig
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&candidate); err != nil {
			return app.Config{}, path, fmt.Errorf("decode configuration: %w", err)
		}
		if err := requireConfigEOF(decoder); err != nil {
			return app.Config{}, path, err
		}
		if candidate.SchemaVersion != app.ConfigSchemaVersion {
			return app.Config{}, path, fmt.Errorf("unsupported configuration schema %q", candidate.SchemaVersion)
		}
		mergeFileConfig(&config, candidate)
	}
	if err := applyEnvironment(&config, environment); err != nil {
		return app.Config{}, path, err
	}
	if err := validateConfig(config); err != nil {
		return app.Config{}, path, err
	}
	return config, path, nil
}

func requireConfigEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("decode configuration: trailing JSON value")
	}
	return fmt.Errorf("decode configuration: %w", err)
}

func mergeFileConfig(config *app.Config, file fileConfig) {
	if value := file.Behavior; value != nil {
		assignBool(&config.Behavior.AcceptSafeDefaults, value.AcceptSafeDefaults)
		assignBool(&config.Behavior.DryRun, value.DryRun)
		assignString(&config.Behavior.Output, value.Output)
		assignString(&config.Behavior.Color, value.Color)
		assignString(&config.Behavior.ResumePolicy, value.ResumePolicy)
	}
	if value := file.Install; value != nil {
		assignString(&config.Install.ManagerDirectory, value.ManagerDirectory)
		assignBool(&config.Install.ManagePath, value.ManagePath)
		assignBool(&config.Install.AllowApplicationInstall, value.AllowApplicationInstall)
	}
	if value := file.Model; value != nil {
		assignString(&config.Model.CatalogID, value.CatalogID)
		assignString(&config.Model.FallbackCatalogID, value.FallbackCatalogID)
		assignUint64(&config.Model.ContextTokens, value.ContextTokens)
		assignBool(&config.Model.AllowExperimental, value.AllowExperimental)
		assignString(&config.Model.AllowedLicenseUse, value.AllowedLicenseUse)
		assignUint64(&config.Model.MaxDownloadBytes, value.MaxDownloadBytes)
		assignUint64(&config.Model.MinimumFreeBytesAfterInstall, value.MinimumFreeBytesAfterInstall)
		assignString(&config.Model.PreferExecution, value.PreferExecution)
	}
	if value := file.Ollama; value != nil {
		assignString(&config.Ollama.Endpoint, value.Endpoint)
		assignString(&config.Ollama.ModelStorage, value.ModelStorage)
		assignString(&config.Ollama.ExistingServerRestart, value.ExistingServerRestart)
		if value.ToolCheckOutputTokens != nil {
			config.Ollama.ToolCheckOutputTokens = *value.ToolCheckOutputTokens
		}
	}
	if value := file.VSCode; value != nil {
		assignString(&config.VSCode.ProfileName, value.ProfileName)
		assignBool(&config.VSCode.OpenAfterSetup, value.OpenAfterSetup)
		assignBool(&config.VSCode.InstallOfficialOllamaExtension, value.InstallOfficialOllamaExtension)
		assignString(&config.VSCode.UtilityModelGuidance, value.UtilityModelGuidance)
		assignString(&config.VSCode.ToolLimitGuidance, value.ToolLimitGuidance)
	}
	if value := file.Repair; value != nil {
		assignBool(&config.Repair.SafeOnly, value.SafeOnly)
		if value.RepairIDs != nil {
			config.Repair.RepairIDs = append([]string(nil), value.RepairIDs...)
		}
	}
	if value := file.Update; value != nil {
		assignString(&config.Update.Channel, value.Channel)
		assignBool(&config.Update.CheckOnStatus, value.CheckOnStatus)
		assignBool(&config.Update.VerifyGitHubAttestationWhenAvailable, value.VerifyGitHubAttestationWhenAvailable)
	}
	if value := file.Report; value != nil {
		assignBool(&config.Report.IncludeHardwareSummary, value.IncludeHardwareSummary)
		assignBool(&config.Report.IncludeRecentIssueCodes, value.IncludeRecentIssueCodes)
		assignBool(&config.Report.IncludeRecentRedactedLogs, value.IncludeRecentRedactedLogs)
		if value.MaximumLogEvents != nil {
			config.Report.MaximumLogEvents = *value.MaximumLogEvents
		}
	}
}

func applyEnvironment(config *app.Config, environment map[string]string) error {
	if value := strings.TrimSpace(environment[envOllamaEndpoint]); value != "" {
		config.Ollama.Endpoint = value
	}
	if _, ok := environment[envNoColor]; ok {
		config.Behavior.Color = "never"
	}
	if value := strings.TrimSpace(environment[envAttestation]); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%s must be a boolean", envAttestation)
		}
		config.Update.VerifyGitHubAttestationWhenAvailable = parsed
	}
	return nil
}

func validateConfig(config app.Config) error {
	if config.SchemaVersion != app.ConfigSchemaVersion {
		return errors.New("configuration schema version is invalid")
	}
	if !oneOf(config.Behavior.Output, "human", "json") ||
		!oneOf(config.Behavior.Color, "auto", "always", "never") ||
		!oneOf(config.Behavior.ResumePolicy, "ask", "resume", "restart", "fail") {
		return errors.New("configuration behavior value is invalid")
	}
	if config.Install.ManagerDirectory != "" && !homePathPattern.MatchString(config.Install.ManagerDirectory) {
		return errors.New("install.manager_directory must use the literal ${HOME} prefix")
	}
	if config.Model.CatalogID != "" && !catalogIDPattern.MatchString(config.Model.CatalogID) {
		return errors.New("model.catalog_id is invalid")
	}
	if config.Model.FallbackCatalogID != "" && !catalogIDPattern.MatchString(config.Model.FallbackCatalogID) {
		return errors.New("model.fallback_catalog_id is invalid")
	}
	if config.Model.ContextTokens < 65536 || config.Model.ContextTokens > 1048576 {
		return errors.New("model.context_tokens must be between 65536 and 1048576")
	}
	if !oneOf(config.Model.AllowedLicenseUse, "personal", "commercial", "research", "unknown") ||
		!oneOf(config.Model.PreferExecution, "auto", "gpu", "cpu") {
		return errors.New("configuration model policy is invalid")
	}
	if config.Model.MinimumFreeBytesAfterInstall < 2<<30 {
		return errors.New("model.minimum_free_bytes_after_install must be at least 2147483648")
	}
	if err := validateLoopbackEndpoint(config.Ollama.Endpoint); err != nil {
		return err
	}
	if config.Ollama.ModelStorage != "" && !homePathPattern.MatchString(config.Ollama.ModelStorage) {
		return errors.New("ollama.model_storage must use the literal ${HOME} prefix")
	}
	if !oneOf(config.Ollama.ExistingServerRestart, "ask", "never") ||
		(config.Ollama.ToolCheckOutputTokens != 1024 && config.Ollama.ToolCheckOutputTokens != 2048) {
		return errors.New("configuration Ollama policy is invalid")
	}
	if config.VSCode.ProfileName != "Local AI" || !config.VSCode.InstallOfficialOllamaExtension ||
		!oneOf(config.VSCode.UtilityModelGuidance, "show", "suppress") ||
		!oneOf(config.VSCode.ToolLimitGuidance, "show", "suppress") {
		return errors.New("configuration VS Code policy is invalid")
	}
	if !config.Repair.SafeOnly {
		return errors.New("repair.safe_only must remain true")
	}
	repairIDs := map[string]bool{}
	for _, id := range config.Repair.RepairIDs {
		if !repairIDPattern.MatchString(id) {
			return fmt.Errorf("repair id %q is invalid", id)
		}
		if repairIDs[id] {
			return fmt.Errorf("repair id %q is duplicated", id)
		}
		repairIDs[id] = true
	}
	if config.Update.Channel != "stable" {
		return errors.New("update.channel must be stable")
	}
	if config.Report.MaximumLogEvents < 0 || config.Report.MaximumLogEvents > 1000 {
		return errors.New("report.maximum_log_events must be between 0 and 1000")
	}
	return nil
}

func validateLoopbackEndpoint(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("ollama.endpoint must be an unauthenticated loopback HTTP URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "localhost" && host != "127.0.0.1" {
		return errors.New("ollama.endpoint must use localhost or 127.0.0.1")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return errors.New("ollama.endpoint port must be between 1 and 65535")
		}
	}
	return nil
}

func assignString(target *string, source *string) {
	if source != nil {
		*target = *source
	}
}

func assignBool(target *bool, source *bool) {
	if source != nil {
		*target = *source
	}
}

func assignUint64(target *uint64, source *uint64) {
	if source != nil {
		*target = *source
	}
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
