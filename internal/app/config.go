package app

const ConfigSchemaVersion = "local-ai.config/v1"

type Config struct {
	SchemaVersion string         `json:"schema_version"`
	Behavior      BehaviorConfig `json:"behavior"`
	Install       InstallConfig  `json:"install"`
	Model         ModelConfig    `json:"model"`
	Ollama        OllamaConfig   `json:"ollama"`
	VSCode        VSCodeConfig   `json:"vscode"`
	Repair        RepairConfig   `json:"repair"`
	Update        UpdateConfig   `json:"update"`
	Report        ReportConfig   `json:"report"`
}

type BehaviorConfig struct {
	AcceptSafeDefaults bool   `json:"accept_safe_defaults"`
	DryRun             bool   `json:"dry_run"`
	Output             string `json:"output"`
	Color              string `json:"color"`
	ResumePolicy       string `json:"resume_policy"`
}

type InstallConfig struct {
	ManagerDirectory        string `json:"manager_directory,omitempty"`
	ManagePath              bool   `json:"manage_path"`
	AllowApplicationInstall bool   `json:"allow_application_install"`
}

type ModelConfig struct {
	CatalogID                    string `json:"catalog_id,omitempty"`
	FallbackCatalogID            string `json:"fallback_catalog_id,omitempty"`
	ContextTokens                uint64 `json:"context_tokens"`
	AllowExperimental            bool   `json:"allow_experimental"`
	AllowedLicenseUse            string `json:"allowed_license_use"`
	MaxDownloadBytes             uint64 `json:"max_download_bytes,omitempty"`
	MinimumFreeBytesAfterInstall uint64 `json:"minimum_free_bytes_after_install"`
	PreferExecution              string `json:"prefer_execution"`
}

type OllamaConfig struct {
	Endpoint              string `json:"endpoint"`
	ModelStorage          string `json:"model_storage,omitempty"`
	ExistingServerRestart string `json:"existing_server_restart"`
	ToolCheckOutputTokens int    `json:"tool_check_output_tokens"`
}

type VSCodeConfig struct {
	ProfileName                    string `json:"profile_name"`
	OpenAfterSetup                 bool   `json:"open_after_setup"`
	InstallOfficialOllamaExtension bool   `json:"install_official_ollama_extension"`
	UtilityModelGuidance           string `json:"utility_model_guidance"`
	ToolLimitGuidance              string `json:"tool_limit_guidance"`
}

type RepairConfig struct {
	SafeOnly  bool     `json:"safe_only"`
	RepairIDs []string `json:"repair_ids,omitempty"`
}

type UpdateConfig struct {
	Channel                              string `json:"channel"`
	CheckOnStatus                        bool   `json:"check_on_status"`
	VerifyGitHubAttestationWhenAvailable bool   `json:"verify_github_attestation_when_available"`
}

type ReportConfig struct {
	IncludeHardwareSummary    bool `json:"include_hardware_summary"`
	IncludeRecentIssueCodes   bool `json:"include_recent_issue_codes"`
	IncludeRecentRedactedLogs bool `json:"include_recent_redacted_logs"`
	MaximumLogEvents          int  `json:"maximum_log_events"`
}

func DefaultConfig() Config {
	return Config{
		SchemaVersion: ConfigSchemaVersion,
		Behavior: BehaviorConfig{
			Output:       "human",
			Color:        "auto",
			ResumePolicy: "ask",
		},
		Install: InstallConfig{
			ManagePath:              true,
			AllowApplicationInstall: true,
		},
		Model: ModelConfig{
			ContextTokens:                65536,
			AllowedLicenseUse:            "personal",
			MinimumFreeBytesAfterInstall: 2 << 30,
			PreferExecution:              "auto",
		},
		Ollama: OllamaConfig{
			Endpoint:              "http://127.0.0.1:11434",
			ExistingServerRestart: "ask",
			ToolCheckOutputTokens: 1024,
		},
		VSCode: VSCodeConfig{
			ProfileName:                    "Local AI",
			OpenAfterSetup:                 true,
			InstallOfficialOllamaExtension: true,
			UtilityModelGuidance:           "show",
			ToolLimitGuidance:              "show",
		},
		Repair: RepairConfig{SafeOnly: true},
		Update: UpdateConfig{
			Channel:                              "stable",
			VerifyGitHubAttestationWhenAvailable: true,
		},
		Report: ReportConfig{
			IncludeHardwareSummary:    true,
			IncludeRecentIssueCodes:   true,
			IncludeRecentRedactedLogs: true,
			MaximumLogEvents:          200,
		},
	}
}
