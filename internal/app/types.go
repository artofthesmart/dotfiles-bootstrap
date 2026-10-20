package app

import "time"

const EventSchemaVersion = "local-ai.event-stream/v1"

type Command string

const (
	CommandSetup            Command = "setup"
	CommandStatus           Command = "status"
	CommandDoctor           Command = "doctor"
	CommandRepair           Command = "repair"
	CommandModelsList       Command = "models_list"
	CommandModelsRecommend  Command = "models_recommend"
	CommandModelsInstall    Command = "models_install"
	CommandModelsRemove     Command = "models_remove"
	CommandVSCodeConfigure  Command = "vscode_configure"
	CommandVSCodeOpen       Command = "vscode_open"
	CommandCopilotConfigure Command = "copilot_configure"
	CommandCopilotOpen      Command = "copilot_open"
	CommandVerify           Command = "verify"
	CommandUpdate           Command = "update"
	CommandUpdateRollback   Command = "update_rollback"
	CommandUninstall        Command = "uninstall"
	CommandReport           Command = "report"
)

type ExitCode int

const (
	ExitSuccess                    ExitCode = 0
	ExitCancelled                  ExitCode = 2
	ExitManualRequired             ExitCode = 3
	ExitUnsupportedHost            ExitCode = 10
	ExitPrerequisiteFailure        ExitCode = 11
	ExitPolicyOrPermissionFailure  ExitCode = 12
	ExitNetworkFailure             ExitCode = 20
	ExitIntegrityFailure           ExitCode = 21
	ExitInsufficientCapacity       ExitCode = 22
	ExitModelIncompatible          ExitCode = 30
	ExitModelAcquisitionFailure    ExitCode = 31
	ExitRuntimeVerificationFailure ExitCode = 32
	ExitVSCodeIntegrationFailure   ExitCode = 40
	ExitStateOrTransactionFailure  ExitCode = 50
	ExitConfigurationFailure       ExitCode = 60
	ExitInternalFailure            ExitCode = 70
)

type IssueCode string

const (
	IssueHostUnsupported          IssueCode = "LAI-HOST-001"
	IssueArchitectureUnsupported  IssueCode = "LAI-HOST-002"
	IssueDesktopRequired          IssueCode = "LAI-HOST-003"
	IssueHostVersionUnsupported   IssueCode = "LAI-HOST-004"
	IssuePrerequisiteMissing      IssueCode = "LAI-PREREQ-001"
	IssuePrerequisiteVersion      IssueCode = "LAI-PREREQ-002"
	IssuePrerequisiteUnusable     IssueCode = "LAI-PREREQ-003"
	IssuePolicyBlocked            IssueCode = "LAI-POLICY-001"
	IssueConsentDeclined          IssueCode = "LAI-POLICY-002"
	IssueManualApproval           IssueCode = "LAI-POLICY-003"
	IssueNetworkUnavailable       IssueCode = "LAI-NET-001"
	IssueHTTPSPolicy              IssueCode = "LAI-NET-002"
	IssueDownloadInvalid          IssueCode = "LAI-NET-003"
	IssueChecksumMismatch         IssueCode = "LAI-INTEGRITY-001"
	IssuePublisherInvalid         IssueCode = "LAI-INTEGRITY-002"
	IssueMetadataInconsistent     IssueCode = "LAI-INTEGRITY-003"
	IssueCapacityInsufficient     IssueCode = "LAI-CAPACITY-001"
	IssueMemoryInsufficient       IssueCode = "LAI-CAPACITY-002"
	IssueCapacityUnknown          IssueCode = "LAI-CAPACITY-003"
	IssueModelIncompatible        IssueCode = "LAI-MODEL-001"
	IssueModelContextInsufficient IssueCode = "LAI-MODEL-002"
	IssueModelRemote              IssueCode = "LAI-MODEL-003"
	IssueModelToolsUnsupported    IssueCode = "LAI-MODEL-004"
	IssueModelAcquisition         IssueCode = "LAI-MODEL-005"
	IssueModelAlias               IssueCode = "LAI-MODEL-006"
	IssueRuntimeUnavailable       IssueCode = "LAI-RUNTIME-001"
	IssueRuntimeToolCall          IssueCode = "LAI-RUNTIME-002"
	IssueRuntimeContext           IssueCode = "LAI-RUNTIME-003"
	IssueRuntimeOffloadUnknown    IssueCode = "LAI-RUNTIME-004"
	IssueRuntimeRestartRequired   IssueCode = "LAI-RUNTIME-005"
	IssueVSCodeIntegration        IssueCode = "LAI-VSCODE-001"
	IssueVSCodeExtension          IssueCode = "LAI-VSCODE-002"
	IssueVSCodeAliasUnavailable   IssueCode = "LAI-VSCODE-003"
	IssueVSCodeTrust              IssueCode = "LAI-VSCODE-004"
	IssueVSCodeToolLimit          IssueCode = "LAI-VSCODE-005"
	IssueVSCodeModelSelection     IssueCode = "LAI-VSCODE-006"
	IssueStateFailure             IssueCode = "LAI-STATE-001"
	IssueStateSchema              IssueCode = "LAI-STATE-002"
	IssueStateResume              IssueCode = "LAI-STATE-003"
	IssueStateWrite               IssueCode = "LAI-STATE-004"
	IssueConfigurationInvalid     IssueCode = "LAI-CONFIG-001"
	IssueConfigurationConflict    IssueCode = "LAI-CONFIG-002"
	IssueAssetOwnership           IssueCode = "LAI-ASSET-001"
	IssueAssetConfirmation        IssueCode = "LAI-ASSET-002"
	IssueUpdateUnavailable        IssueCode = "LAI-UPDATE-001"
	IssueUpdateSelfCheck          IssueCode = "LAI-UPDATE-002"
	IssueInternal                 IssueCode = "LAI-INTERNAL-001"
)

type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

type Outcome string

const (
	OutcomeSuccess        Outcome = "success"
	OutcomeCancelled      Outcome = "cancelled"
	OutcomeManualRequired Outcome = "manual_required"
	OutcomeFailed         Outcome = "failed"
)

type Remediation struct {
	Kind       string  `json:"kind"`
	Summary    string  `json:"summary"`
	Command    *string `json:"command,omitempty"`
	SafeForYes bool    `json:"safe_for_yes"`
}

type Issue struct {
	Code         IssueCode     `json:"code"`
	Severity     Severity      `json:"severity"`
	Domain       string        `json:"domain"`
	Component    *string       `json:"component,omitempty"`
	Summary      string        `json:"summary"`
	RepairID     *string       `json:"repair_id,omitempty"`
	Remediations []Remediation `json:"remediations"`
}

type Component struct {
	Component  string         `json:"component"`
	State      string         `json:"state"`
	Verified   bool           `json:"verified"`
	Attributes map[string]any `json:"attributes"`
}

type ManualAction struct {
	ActionID  string    `json:"action_id"`
	IssueCode IssueCode `json:"issue_code"`
	Surface   string    `json:"surface"`
	Summary   string    `json:"summary"`
}

type Result struct {
	SchemaVersion string         `json:"schema_version"`
	RecordType    string         `json:"record_type"`
	Sequence      int            `json:"sequence"`
	Timestamp     time.Time      `json:"timestamp"`
	Command       Command        `json:"command"`
	TransactionID *string        `json:"transaction_id"`
	Outcome       Outcome        `json:"outcome"`
	ExitCode      ExitCode       `json:"exit_code"`
	Summary       string         `json:"summary"`
	Issues        []Issue        `json:"issues"`
	Components    []Component    `json:"components"`
	ManualActions []ManualAction `json:"manual_actions"`
}

type Operation struct {
	Command   Command
	Arguments []string
	Config    Config
	Yes       bool
	DryRun    bool
	Resume    bool
	Restart   bool
	Options   OperationOptions
	Events    EventSink
}

type OperationOptions struct {
	Repository            string
	OutputPath            string
	ManualCompleted       bool
	CheckUpdates          bool
	Rollback              bool
	RemoveAliases         bool
	RemoveModels          bool
	RemoveVSCodeExtension bool
	RemoveVSCode          bool
	RemoveOllama          bool
	ConfirmDestructive    bool
}

type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

type Clock interface {
	Now() time.Time
}
