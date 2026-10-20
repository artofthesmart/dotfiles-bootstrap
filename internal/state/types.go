package state

import "time"

const (
	TransactionSchemaVersion = "local-ai.transaction-state/v1"
	EventSchemaVersion       = "local-ai.event-stream/v1"
)

type Command string

const (
	CommandSetup           Command = "setup"
	CommandRepair          Command = "repair"
	CommandModelsInstall   Command = "models_install"
	CommandModelsRemove    Command = "models_remove"
	CommandVSCodeConfigure Command = "vscode_configure"
	CommandUpdate          Command = "update"
	CommandUpdateRollback  Command = "update_rollback"
	CommandUninstall       Command = "uninstall"
)

type TransactionStatus string

const (
	StatusPlanned        TransactionStatus = "planned"
	StatusRunning        TransactionStatus = "running"
	StatusVerifying      TransactionStatus = "verifying"
	StatusAwaitingManual TransactionStatus = "awaiting_manual"
	StatusInterrupted    TransactionStatus = "interrupted"
	StatusFailed         TransactionStatus = "failed"
	StatusRollingBack    TransactionStatus = "rolling_back"
	StatusRolledBack     TransactionStatus = "rolled_back"
	StatusRollbackFailed TransactionStatus = "rollback_failed"
	StatusSucceeded      TransactionStatus = "succeeded"
	StatusArchived       TransactionStatus = "archived"
)

type StepStatus string

const (
	StepPending         StepStatus = "pending"
	StepBlocked         StepStatus = "blocked"
	StepRunning         StepStatus = "running"
	StepAwaitingManual  StepStatus = "awaiting_manual"
	StepFailed          StepStatus = "failed"
	StepVerified        StepStatus = "verified"
	StepUndone          StepStatus = "undone"
	StepUndoFailed      StepStatus = "undo_failed"
	StepSkippedVerified StepStatus = "skipped_verified"
)

type PhaseStatus string

const (
	PhaseNotRun        PhaseStatus = "not_run"
	PhaseRunning       PhaseStatus = "running"
	PhaseSucceeded     PhaseStatus = "succeeded"
	PhaseFailed        PhaseStatus = "failed"
	PhaseSkipped       PhaseStatus = "skipped"
	PhaseNotApplicable PhaseStatus = "not_applicable"
)

type PhaseName string

const (
	PhaseCheck  PhaseName = "check"
	PhasePlan   PhaseName = "plan"
	PhaseApply  PhaseName = "apply"
	PhaseVerify PhaseName = "verify"
	PhaseUndo   PhaseName = "undo"
)

type Owner string

const (
	OwnerApp      Owner = "app"
	OwnerState    Owner = "state"
	OwnerPlatform Owner = "platform"
	OwnerHardware Owner = "hardware"
	OwnerCatalog  Owner = "catalog"
	OwnerOllama   Owner = "ollama"
	OwnerVSCode   Owner = "vscode"
	OwnerInstall  Owner = "install"
	OwnerUpdate   Owner = "update"
)

type Risk string

const (
	RiskReadOnly           Risk = "read_only"
	RiskUserLocal          Risk = "user_local"
	RiskLargeDownload      Risk = "large_download"
	RiskPrivileged         Risk = "privileged"
	RiskDestructive        Risk = "destructive"
	RiskExternalAppRestart Risk = "external_app_restart"
)

type ConsentDecision string

const (
	ConsentNotRequired ConsentDecision = "not_required"
	ConsentPending     ConsentDecision = "pending"
	ConsentAccepted    ConsentDecision = "accepted"
	ConsentDeclined    ConsentDecision = "declined"
)

type HostOS string

const (
	HostWindows HostOS = "windows"
	HostMacOS   HostOS = "macos"
	HostLinux   HostOS = "linux"
)

type Architecture string

const (
	ArchitectureAMD64 Architecture = "amd64"
	ArchitectureARM64 Architecture = "arm64"
)

type LinuxDistribution string

const (
	LinuxUbuntu LinuxDistribution = "ubuntu"
	LinuxDebian LinuxDistribution = "debian"
)

type Ownership string

const (
	OwnershipManagerOwned     Ownership = "manager_owned"
	OwnershipUserOwned        Ownership = "user_owned"
	OwnershipExternalDetected Ownership = "external_detected"
)

type AssetOrigin string

const (
	OriginInstalledByManager AssetOrigin = "installed_by_manager"
	OriginCreatedByManager   AssetOrigin = "created_by_manager"
	OriginDetectedExisting   AssetOrigin = "detected_existing"
	OriginUserDeclared       AssetOrigin = "user_declared"
	OriginAdopted            AssetOrigin = "adopted"
)

type AssetType string

const (
	AssetManagerBinary     AssetType = "manager_binary"
	AssetManagerDirectory  AssetType = "manager_directory"
	AssetPathEntry         AssetType = "path_entry"
	AssetDownloadCache     AssetType = "download_cache"
	AssetRollbackBinary    AssetType = "rollback_binary"
	AssetVSCodeApplication AssetType = "vscode_application"
	AssetOllamaApplication AssetType = "ollama_application"
	AssetOllamaService     AssetType = "ollama_service"
	AssetVSCodeProfile     AssetType = "vscode_profile"
	AssetVSCodeExtension   AssetType = "vscode_extension"
	AssetOllamaSourceModel AssetType = "ollama_source_model"
	AssetOllamaAlias       AssetType = "ollama_alias"
	AssetStateFile         AssetType = "state_file"
	AssetBackupFile        AssetType = "backup_file"
)

type LocatorKind string

const (
	LocatorPath          LocatorKind = "path"
	LocatorApplicationID LocatorKind = "application_id"
	LocatorServiceID     LocatorKind = "service_id"
	LocatorProfileName   LocatorKind = "profile_name"
	LocatorExtensionID   LocatorKind = "extension_id"
	LocatorModelName     LocatorKind = "model_name"
	LocatorPathMarker    LocatorKind = "path_marker"
)

type UninstallPolicy string

const (
	UninstallRemoveWithManager UninstallPolicy = "remove_with_manager"
	UninstallRetain            UninstallPolicy = "retain"
	UninstallExplicitFlag      UninstallPolicy = "explicit_flag"
	UninstallGuidedUIOnly      UninstallPolicy = "guided_ui_only"
)

type EvidenceKind string

const (
	EvidenceVersion           EvidenceKind = "version"
	EvidenceDigest            EvidenceKind = "digest"
	EvidenceFileState         EvidenceKind = "file_state"
	EvidenceApplicationState  EvidenceKind = "application_state"
	EvidenceServiceState      EvidenceKind = "service_state"
	EvidenceRegistryMetadata  EvidenceKind = "registry_metadata"
	EvidenceModelMetadata     EvidenceKind = "model_metadata"
	EvidenceContextAllocation EvidenceKind = "context_allocation"
	EvidenceToolCall          EvidenceKind = "tool_call"
	EvidenceOffload           EvidenceKind = "offload"
	EvidenceProfileState      EvidenceKind = "profile_state"
	EvidenceExtensionState    EvidenceKind = "extension_state"
	EvidenceCapacity          EvidenceKind = "capacity"
)

type IssueCode string
type Severity string
type IssueDomain string
type RemediationKind string
type ManualSurface string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

type SafeScalar any
type SafeAttributes map[string]SafeScalar

type Transaction struct {
	SchemaVersion         string            `json:"schema_version"`
	ManagerVersion        string            `json:"manager_version"`
	TransactionID         string            `json:"transaction_id"`
	Command               Command           `json:"command"`
	Status                TransactionStatus `json:"status"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
	CompletedAt           *time.Time        `json:"completed_at,omitempty"`
	EffectiveConfigDigest string            `json:"effective_config_digest"`
	SelectedCatalogID     *string           `json:"selected_catalog_id,omitempty"`
	GeneratedAlias        *string           `json:"generated_alias,omitempty"`
	Host                  Host              `json:"host"`
	Steps                 []StepReceipt     `json:"steps"`
	ManagedAssets         []ManagedAsset    `json:"managed_assets"`
	Issues                []Issue           `json:"issues"`
	ManualActions         []ManualAction    `json:"manual_actions"`
	Rollback              Rollback          `json:"rollback"`
	PreviousState         *StateLocator     `json:"previous_state,omitempty"`
}

type Host struct {
	OS                HostOS             `json:"os"`
	Architecture      Architecture       `json:"architecture"`
	OSVersion         string             `json:"os_version"`
	LinuxDistribution *LinuxDistribution `json:"linux_distribution,omitempty"`
	DesktopSession    bool               `json:"desktop_session"`
	CapabilityDigest  string             `json:"capability_digest"`
}

type StepReceipt struct {
	ReceiptVersion      int           `json:"receipt_version"`
	StepID              string        `json:"step_id"`
	Owner               Owner         `json:"owner"`
	Action              string        `json:"action"`
	Risk                Risk          `json:"risk"`
	Status              StepStatus    `json:"status"`
	PrerequisiteStepIDs []string      `json:"prerequisite_step_ids"`
	ProducedFactIDs     []string      `json:"produced_fact_ids"`
	Consent             *Consent      `json:"consent,omitempty"`
	Phases              PhaseReceipts `json:"phases"`
	MutationAssetIDs    []string      `json:"mutation_asset_ids"`
	Issues              []Issue       `json:"issues"`
}

type Consent struct {
	Required  bool            `json:"required"`
	Decision  ConsentDecision `json:"decision"`
	DecidedAt *time.Time      `json:"decided_at,omitempty"`
}

type PhaseReceipts struct {
	Check  PhaseReceipt `json:"check"`
	Plan   PhaseReceipt `json:"plan"`
	Apply  PhaseReceipt `json:"apply"`
	Verify PhaseReceipt `json:"verify"`
	Undo   PhaseReceipt `json:"undo"`
}

func (p *PhaseReceipts) Get(name PhaseName) *PhaseReceipt {
	switch name {
	case PhaseCheck:
		return &p.Check
	case PhasePlan:
		return &p.Plan
	case PhaseApply:
		return &p.Apply
	case PhaseVerify:
		return &p.Verify
	case PhaseUndo:
		return &p.Undo
	default:
		return nil
	}
}

type PhaseReceipt struct {
	Status      PhaseStatus `json:"status"`
	StartedAt   *time.Time  `json:"started_at,omitempty"`
	CompletedAt *time.Time  `json:"completed_at,omitempty"`
	Summary     *string     `json:"summary,omitempty"`
	Evidence    []Evidence  `json:"evidence"`
}

type Evidence struct {
	Kind       EvidenceKind   `json:"kind"`
	ObservedAt time.Time      `json:"observed_at"`
	Attributes SafeAttributes `json:"attributes"`
}

type ManagedAsset struct {
	AssetID                string        `json:"asset_id"`
	AssetType              AssetType     `json:"asset_type"`
	DisplayName            string        `json:"display_name"`
	Origin                 AssetOrigin   `json:"origin"`
	Ownership              Ownership     `json:"ownership"`
	Locator                AssetLocator  `json:"locator"`
	CreatedOrAdoptedByStep *string       `json:"created_or_adopted_by_step,omitempty"`
	Dependencies           []string      `json:"dependencies"`
	Uninstall              UninstallRule `json:"uninstall"`
	Backup                 *StateLocator `json:"backup,omitempty"`
	Verification           []Evidence    `json:"verification"`
}

type AssetLocator struct {
	Kind  LocatorKind `json:"kind"`
	Value string      `json:"value"`
}

type UninstallRule struct {
	Policy              UninstallPolicy `json:"policy"`
	ExplicitFlag        *string         `json:"explicit_flag,omitempty"`
	Reversible          bool            `json:"reversible"`
	NotReversibleReason *string         `json:"not_reversible_reason,omitempty"`
}

type Issue struct {
	Code         IssueCode     `json:"code"`
	Severity     Severity      `json:"severity"`
	Domain       IssueDomain   `json:"domain"`
	Component    *string       `json:"component,omitempty"`
	Summary      string        `json:"summary"`
	RepairID     *string       `json:"repair_id,omitempty"`
	Remediations []Remediation `json:"remediations"`
}

type Remediation struct {
	Kind       RemediationKind `json:"kind"`
	Summary    string          `json:"summary"`
	Command    *string         `json:"command,omitempty"`
	SafeForYes bool            `json:"safe_for_yes"`
}

type ManualAction struct {
	ActionID  string        `json:"action_id"`
	IssueCode IssueCode     `json:"issue_code"`
	Surface   ManualSurface `json:"surface"`
	Summary   string        `json:"summary"`
	Completed bool          `json:"completed"`
}

type RollbackStatus string

const (
	RollbackNotNeeded    RollbackStatus = "not_needed"
	RollbackAvailable    RollbackStatus = "available"
	RollbackRunning      RollbackStatus = "running"
	RollbackSucceeded    RollbackStatus = "succeeded"
	RollbackPartial      RollbackStatus = "partial"
	RollbackFailed       RollbackStatus = "failed"
	RollbackNotAvailable RollbackStatus = "not_available"
)

type Rollback struct {
	Available bool           `json:"available"`
	Status    RollbackStatus `json:"status"`
	StepIDs   []string       `json:"step_ids"`
}

type StateLocator struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}
