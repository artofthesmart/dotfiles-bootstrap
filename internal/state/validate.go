package state

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	semanticVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	uuidPattern            = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	digestPattern          = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	stepIDPattern          = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`)
	assetIDPattern         = regexp.MustCompile(`^asset-[a-z0-9][a-z0-9._-]{0,95}$`)
	factIDPattern          = stepIDPattern
	attributeKeyPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
	catalogIDPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	aliasPattern           = regexp.MustCompile(`^local-ai-[a-zA-Z0-9][a-zA-Z0-9._-]*(?::[a-zA-Z0-9][a-zA-Z0-9._-]*)?$`)
)

var (
	ErrInvalidState       = errors.New("invalid transaction state")
	ErrInvalidTransition  = errors.New("invalid transaction transition")
	ErrUnsupportedSchema  = errors.New("unsupported transaction schema")
	ErrIncompatibleResume = errors.New("transaction is incompatible with resume request")
	ErrActiveTransaction  = errors.New("an active transaction already exists")
	ErrNoState            = errors.New("transaction state does not exist")
)

var validIssueCodes = stringSet(
	"LAI-HOST-001", "LAI-HOST-002", "LAI-HOST-003", "LAI-HOST-004",
	"LAI-PREREQ-001", "LAI-PREREQ-002", "LAI-PREREQ-003",
	"LAI-POLICY-001", "LAI-POLICY-002", "LAI-POLICY-003",
	"LAI-NET-001", "LAI-NET-002", "LAI-NET-003",
	"LAI-INTEGRITY-001", "LAI-INTEGRITY-002", "LAI-INTEGRITY-003",
	"LAI-CAPACITY-001", "LAI-CAPACITY-002", "LAI-CAPACITY-003",
	"LAI-MODEL-001", "LAI-MODEL-002", "LAI-MODEL-003", "LAI-MODEL-004", "LAI-MODEL-005", "LAI-MODEL-006",
	"LAI-RUNTIME-001", "LAI-RUNTIME-002", "LAI-RUNTIME-003", "LAI-RUNTIME-004", "LAI-RUNTIME-005",
	"LAI-VSCODE-001", "LAI-VSCODE-002", "LAI-VSCODE-003", "LAI-VSCODE-004", "LAI-VSCODE-005", "LAI-VSCODE-006",
	"LAI-STATE-001", "LAI-STATE-002", "LAI-STATE-003", "LAI-STATE-004",
	"LAI-CONFIG-001", "LAI-CONFIG-002",
	"LAI-ASSET-001", "LAI-ASSET-002",
	"LAI-UPDATE-001", "LAI-UPDATE-002",
	"LAI-INTERNAL-001",
)

var safeAttributeKeys = stringSet(
	"application_id", "asset_id", "asset_type", "attempt", "available_bytes",
	"bytes", "capability", "command_family", "completed", "context_tokens",
	"digest", "dry_run", "extension_id", "file_state", "model_name",
	"offload_percent", "operation", "ownership", "phase", "present",
	"profile_name", "ready", "required_bytes", "risk", "schema_version",
	"service_id", "state", "status", "structured", "tool_name", "total",
	"unit", "verified", "version",
)

func (t Transaction) Validate() error {
	if t.SchemaVersion != TransactionSchemaVersion {
		return invalid("schema_version", string(t.SchemaVersion))
	}
	if !semanticVersionPattern.MatchString(t.ManagerVersion) {
		return invalid("manager_version", t.ManagerVersion)
	}
	if !uuidPattern.MatchString(t.TransactionID) {
		return invalid("transaction_id", t.TransactionID)
	}
	if !validCommand(t.Command) {
		return invalid("command", string(t.Command))
	}
	if !validTransactionStatus(t.Status) {
		return invalid("status", string(t.Status))
	}
	if err := validateUTC("created_at", t.CreatedAt); err != nil {
		return err
	}
	if err := validateUTC("updated_at", t.UpdatedAt); err != nil {
		return err
	}
	if t.UpdatedAt.Before(t.CreatedAt) {
		return invalid("updated_at", "precedes created_at")
	}
	if t.CompletedAt != nil {
		if err := validateUTC("completed_at", *t.CompletedAt); err != nil {
			return err
		}
		if t.CompletedAt.Before(t.CreatedAt) {
			return invalid("completed_at", "precedes created_at")
		}
	}
	if isTerminal(t.Status) != (t.CompletedAt != nil) && t.Status != StatusArchived {
		return invalid("completed_at", "must be set exactly for terminal transaction states")
	}
	if !digestPattern.MatchString(t.EffectiveConfigDigest) {
		return invalid("effective_config_digest", t.EffectiveConfigDigest)
	}
	if t.SelectedCatalogID != nil && !catalogIDPattern.MatchString(*t.SelectedCatalogID) {
		return invalid("selected_catalog_id", *t.SelectedCatalogID)
	}
	if t.GeneratedAlias != nil && !aliasPattern.MatchString(*t.GeneratedAlias) {
		return invalid("generated_alias", *t.GeneratedAlias)
	}
	if err := t.Host.validate(); err != nil {
		return err
	}
	if t.Steps == nil || t.ManagedAssets == nil || t.Issues == nil || t.ManualActions == nil || t.Rollback.StepIDs == nil {
		return invalid("transaction collections", "required arrays must not be null")
	}

	stepByID := make(map[string]StepReceipt, len(t.Steps))
	for index := range t.Steps {
		step := t.Steps[index]
		if _, exists := stepByID[step.StepID]; exists {
			return invalid("steps", "duplicate step_id "+step.StepID)
		}
		if err := step.validate(); err != nil {
			return fmt.Errorf("%w: steps[%d]: %v", ErrInvalidState, index, err)
		}
		stepByID[step.StepID] = step
	}
	for _, step := range t.Steps {
		for _, prerequisite := range step.PrerequisiteStepIDs {
			if _, exists := stepByID[prerequisite]; !exists {
				return invalid("steps", "unknown prerequisite "+prerequisite)
			}
			if prerequisite == step.StepID {
				return invalid("steps", "step cannot require itself: "+step.StepID)
			}
		}
	}
	if err := validateStepGraph(t.Steps); err != nil {
		return err
	}

	assetIDs := make(map[string]struct{}, len(t.ManagedAssets))
	for index := range t.ManagedAssets {
		asset := t.ManagedAssets[index]
		if _, exists := assetIDs[asset.AssetID]; exists {
			return invalid("managed_assets", "duplicate asset_id "+asset.AssetID)
		}
		if err := asset.validate(stepByID); err != nil {
			return fmt.Errorf("%w: managed_assets[%d]: %v", ErrInvalidState, index, err)
		}
		assetIDs[asset.AssetID] = struct{}{}
	}
	for _, asset := range t.ManagedAssets {
		for _, dependency := range asset.Dependencies {
			if _, exists := assetIDs[dependency]; !exists {
				return invalid("managed_assets", "unknown dependency "+dependency)
			}
			if dependency == asset.AssetID {
				return invalid("managed_assets", "asset cannot depend on itself: "+asset.AssetID)
			}
		}
	}
	for _, issue := range t.Issues {
		if err := issue.validate(); err != nil {
			return err
		}
	}
	for _, action := range t.ManualActions {
		if !stepIDPattern.MatchString(action.ActionID) || !validIssueCodes[string(action.IssueCode)] {
			return invalid("manual_actions", action.ActionID)
		}
		switch action.Surface {
		case "vscode", "ollama", "os", "organization_policy", "terminal":
		default:
			return invalid("manual_actions.surface", string(action.Surface))
		}
		if err := validateSafeText("manual_actions.summary", action.Summary, 320); err != nil {
			return err
		}
	}
	if err := t.Rollback.validate(stepByID); err != nil {
		return err
	}
	if t.PreviousState != nil {
		if err := t.PreviousState.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (h Host) validate() error {
	if h.OS != HostWindows && h.OS != HostMacOS && h.OS != HostLinux {
		return invalid("host.os", string(h.OS))
	}
	if h.Architecture != ArchitectureAMD64 && h.Architecture != ArchitectureARM64 {
		return invalid("host.architecture", string(h.Architecture))
	}
	if err := validateSafeText("host.os_version", h.OSVersion, 64); err != nil {
		return err
	}
	if h.OS == HostLinux {
		if h.LinuxDistribution == nil || (*h.LinuxDistribution != LinuxUbuntu && *h.LinuxDistribution != LinuxDebian) {
			return invalid("host.linux_distribution", "required for Linux")
		}
	} else if h.LinuxDistribution != nil {
		return invalid("host.linux_distribution", "only valid for Linux")
	}
	if !digestPattern.MatchString(h.CapabilityDigest) {
		return invalid("host.capability_digest", h.CapabilityDigest)
	}
	return nil
}

func (s StepReceipt) validate() error {
	if s.ReceiptVersion != 1 || !stepIDPattern.MatchString(s.StepID) || len(s.StepID) > 96 {
		return invalid("step receipt identity", s.StepID)
	}
	if !validOwner(s.Owner) || !validRisk(s.Risk) || !validStepStatus(s.Status) {
		return invalid("step receipt enum", s.StepID)
	}
	if err := validateSafeText("step.action", s.Action, 160); err != nil {
		return err
	}
	if err := validateIDs("prerequisite_step_ids", s.PrerequisiteStepIDs, stepIDPattern); err != nil {
		return err
	}
	if err := validateIDs("produced_fact_ids", s.ProducedFactIDs, factIDPattern); err != nil {
		return err
	}
	if err := validateIDs("mutation_asset_ids", s.MutationAssetIDs, assetIDPattern); err != nil {
		return err
	}
	for field, missing := range map[string]bool{
		"prerequisite_step_ids": s.PrerequisiteStepIDs == nil,
		"produced_fact_ids":     s.ProducedFactIDs == nil,
		"mutation_asset_ids":    s.MutationAssetIDs == nil,
		"issues":                s.Issues == nil,
	} {
		if missing {
			return invalid("step."+field, "required array must not be null")
		}
	}
	if s.Consent != nil {
		if err := s.Consent.validate(); err != nil {
			return err
		}
	}
	for _, phase := range []PhaseName{PhaseCheck, PhasePlan, PhaseApply, PhaseVerify, PhaseUndo} {
		if err := s.Phases.Get(phase).validate(string(phase)); err != nil {
			return err
		}
	}
	for _, issue := range s.Issues {
		if err := issue.validate(); err != nil {
			return err
		}
	}
	switch s.Status {
	case StepVerified:
		if s.Phases.Apply.Status != PhaseSucceeded || s.Phases.Verify.Status != PhaseSucceeded {
			return invalid("step.status", "verified requires successful apply and verify")
		}
	case StepSkippedVerified:
		if s.Phases.Check.Status != PhaseSucceeded || s.Phases.Apply.Status != PhaseSkipped || s.Phases.Verify.Status != PhaseSucceeded {
			return invalid("step.status", "skipped_verified requires check and verify evidence")
		}
	case StepUndone:
		if s.Phases.Undo.Status != PhaseSucceeded {
			return invalid("step.status", "undone requires successful undo")
		}
	case StepUndoFailed:
		if s.Phases.Undo.Status != PhaseFailed {
			return invalid("step.status", "undo_failed requires failed undo")
		}
	}
	return nil
}

func (c Consent) validate() error {
	switch c.Decision {
	case ConsentNotRequired:
		if c.Required || c.DecidedAt != nil {
			return invalid("consent", "not_required must not record a decision time")
		}
	case ConsentPending:
		if !c.Required || c.DecidedAt != nil {
			return invalid("consent", "pending consent is required and undecided")
		}
	case ConsentAccepted, ConsentDeclined:
		if !c.Required || c.DecidedAt == nil {
			return invalid("consent", "decision requires a timestamp")
		}
		if err := validateUTC("consent.decided_at", *c.DecidedAt); err != nil {
			return err
		}
	default:
		return invalid("consent.decision", string(c.Decision))
	}
	return nil
}

func (p PhaseReceipt) validate(name string) error {
	if p.Evidence == nil {
		return invalid(name+".evidence", "required array must not be null")
	}
	switch p.Status {
	case PhaseNotRun, PhaseNotApplicable:
		if p.StartedAt != nil || p.CompletedAt != nil || len(p.Evidence) != 0 {
			return invalid(name, "unrun phase cannot have timing or evidence")
		}
	case PhaseRunning:
		if p.StartedAt == nil || p.CompletedAt != nil {
			return invalid(name, "running phase requires only started_at")
		}
	case PhaseSucceeded, PhaseFailed, PhaseSkipped:
		if p.CompletedAt == nil {
			return invalid(name, "completed phase requires completed_at")
		}
		if p.Status != PhaseSkipped && p.StartedAt == nil {
			return invalid(name, "completed phase requires started_at")
		}
	default:
		return invalid(name+".status", string(p.Status))
	}
	if p.StartedAt != nil {
		if err := validateUTC(name+".started_at", *p.StartedAt); err != nil {
			return err
		}
	}
	if p.CompletedAt != nil {
		if err := validateUTC(name+".completed_at", *p.CompletedAt); err != nil {
			return err
		}
		if p.StartedAt != nil && p.CompletedAt.Before(*p.StartedAt) {
			return invalid(name+".completed_at", "precedes started_at")
		}
	}
	if p.Summary != nil {
		if err := validateSafeText(name+".summary", *p.Summary, 240); err != nil {
			return err
		}
	}
	for _, evidence := range p.Evidence {
		if err := evidence.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (e Evidence) validate() error {
	if !validEvidenceKind(e.Kind) {
		return invalid("evidence.kind", string(e.Kind))
	}
	if err := validateUTC("evidence.observed_at", e.ObservedAt); err != nil {
		return err
	}
	if e.Attributes == nil {
		return invalid("evidence.attributes", "required object must not be null")
	}
	return validateAttributes(e.Attributes)
}

func (a ManagedAsset) validate(steps map[string]StepReceipt) error {
	if !assetIDPattern.MatchString(a.AssetID) || !validAssetType(a.AssetType) || !validOrigin(a.Origin) || !validOwnership(a.Ownership) {
		return invalid("asset", a.AssetID)
	}
	if err := validateSafeText("asset.display_name", a.DisplayName, 120); err != nil {
		return err
	}
	if err := a.Locator.validate(); err != nil {
		return err
	}
	if err := validateIDs("asset.dependencies", a.Dependencies, assetIDPattern); err != nil {
		return err
	}
	if a.Dependencies == nil || a.Verification == nil {
		return invalid("asset collections", "required arrays must not be null")
	}
	if err := a.Uninstall.validate(); err != nil {
		return err
	}
	if a.Backup != nil {
		if err := a.Backup.validate(); err != nil {
			return err
		}
	}
	for _, evidence := range a.Verification {
		if err := evidence.validate(); err != nil {
			return err
		}
	}
	if a.Ownership == OwnershipManagerOwned {
		if a.Origin != OriginInstalledByManager && a.Origin != OriginCreatedByManager && a.Origin != OriginAdopted {
			return invalid("asset.origin", "manager-owned asset has incompatible origin")
		}
		if a.CreatedOrAdoptedByStep == nil {
			return invalid("asset.created_or_adopted_by_step", "required for manager-owned asset")
		}
		step, exists := steps[*a.CreatedOrAdoptedByStep]
		stepRetainsVerifiedMutation := step.Status == StepVerified ||
			step.Status == StepUndoFailed ||
			(step.Status == StepRunning && step.Phases.Undo.Status == PhaseRunning)
		if !exists || !stepRetainsVerifiedMutation || step.Phases.Apply.Status != PhaseSucceeded || step.Phases.Verify.Status != PhaseSucceeded {
			return invalid("asset.ownership", "manager ownership requires verified manager mutation")
		}
		if !contains(step.MutationAssetIDs, a.AssetID) || len(a.Verification) == 0 {
			return invalid("asset.ownership", "manager ownership requires linked mutation and verification evidence")
		}
	} else if a.Origin == OriginInstalledByManager || a.Origin == OriginCreatedByManager {
		return invalid("asset.origin", "verified manager-created assets must be manager-owned")
	}
	return nil
}

func (l AssetLocator) validate() error {
	switch l.Kind {
	case LocatorPath, LocatorApplicationID, LocatorServiceID, LocatorProfileName, LocatorExtensionID, LocatorModelName, LocatorPathMarker:
	default:
		return invalid("asset.locator.kind", string(l.Kind))
	}
	if err := validateSafeText("asset.locator.value", l.Value, 512); err != nil {
		return err
	}
	if l.Kind == LocatorPath && strings.Contains(l.Value, "/home/") {
		return invalid("asset.locator.value", "home paths must use ${HOME}")
	}
	return nil
}

func (u UninstallRule) validate() error {
	switch u.Policy {
	case UninstallRemoveWithManager, UninstallRetain, UninstallGuidedUIOnly:
		if u.ExplicitFlag != nil {
			return invalid("uninstall.explicit_flag", "only valid for explicit_flag policy")
		}
	case UninstallExplicitFlag:
		if u.ExplicitFlag == nil || !stringSet("--remove-aliases", "--remove-models", "--remove-vscode-extension", "--remove-vscode", "--remove-ollama")[*u.ExplicitFlag] {
			return invalid("uninstall.explicit_flag", "unsupported flag")
		}
	default:
		return invalid("uninstall.policy", string(u.Policy))
	}
	if !u.Reversible && (u.NotReversibleReason == nil || strings.TrimSpace(*u.NotReversibleReason) == "") {
		return invalid("uninstall.not_reversible_reason", "required when not reversible")
	}
	if u.NotReversibleReason != nil {
		return validateSafeText("uninstall.not_reversible_reason", *u.NotReversibleReason, 240)
	}
	return nil
}

func (i Issue) validate() error {
	if !validIssueCodes[string(i.Code)] {
		return invalid("issue.code", string(i.Code))
	}
	if i.Severity != SeverityInfo && i.Severity != SeverityWarning && i.Severity != SeverityError {
		return invalid("issue.severity", string(i.Severity))
	}
	switch i.Domain {
	case "host", "prerequisite", "policy", "network", "integrity", "capacity", "model", "runtime", "vscode", "state", "config", "asset", "update", "internal":
	default:
		return invalid("issue.domain", string(i.Domain))
	}
	if err := validateSafeText("issue.summary", i.Summary, 320); err != nil {
		return err
	}
	if i.Component != nil {
		if err := validateSafeText("issue.component", *i.Component, 80); err != nil {
			return err
		}
	}
	if i.RepairID != nil && !stepIDPattern.MatchString(*i.RepairID) {
		return invalid("issue.repair_id", *i.RepairID)
	}
	if i.Remediations == nil {
		return invalid("issue.remediations", "required array must not be null")
	}
	for _, remediation := range i.Remediations {
		switch remediation.Kind {
		case "automatic", "command", "ui", "policy", "documentation", "retry":
		default:
			return invalid("remediation.kind", string(remediation.Kind))
		}
		if err := validateSafeText("remediation.summary", remediation.Summary, 320); err != nil {
			return err
		}
		if remediation.Command != nil {
			if err := validateSafeText("remediation.command", *remediation.Command, 320); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r Rollback) validate(steps map[string]StepReceipt) error {
	switch r.Status {
	case RollbackNotNeeded, RollbackAvailable, RollbackRunning, RollbackSucceeded, RollbackPartial, RollbackFailed, RollbackNotAvailable:
	default:
		return invalid("rollback.status", string(r.Status))
	}
	if err := validateIDs("rollback.step_ids", r.StepIDs, stepIDPattern); err != nil {
		return err
	}
	for _, id := range r.StepIDs {
		if _, exists := steps[id]; !exists {
			return invalid("rollback.step_ids", "unknown step "+id)
		}
	}
	if r.Available != (r.Status == RollbackAvailable || r.Status == RollbackRunning) {
		return invalid("rollback.available", "does not match rollback status")
	}
	return nil
}

func (s StateLocator) validate() error {
	if !strings.HasPrefix(s.Path, "${HOME}/") && !strings.HasPrefix(s.Path, `${HOME}\`) {
		return invalid("state locator path", "must use ${HOME}")
	}
	if len(s.Path) > 512 || !digestPattern.MatchString(s.Digest) {
		return invalid("state locator", s.Path)
	}
	return nil
}

func validateAttributes(attributes SafeAttributes) error {
	for key, value := range attributes {
		if !attributeKeyPattern.MatchString(key) || !safeAttributeKeys[key] {
			return invalid("attributes", "field is not privacy allowlisted: "+key)
		}
		switch typed := value.(type) {
		case nil, bool:
		case string:
			if err := validateSafeText("attributes."+key, typed, 256); err != nil {
				return err
			}
		case int:
		case int8:
		case int16:
		case int32:
		case int64:
		case uint:
		case uint8:
		case uint16:
		case uint32:
		case uint64:
		case float32:
			if math.IsInf(float64(typed), 0) || math.IsNaN(float64(typed)) {
				return invalid("attributes."+key, "non-finite number")
			}
		case float64:
			if math.IsInf(typed, 0) || math.IsNaN(typed) {
				return invalid("attributes."+key, "non-finite number")
			}
		default:
			return invalid("attributes."+key, fmt.Sprintf("unsupported scalar type %T", value))
		}
	}
	return nil
}

func validateSafeText(field, value string, max int) error {
	if value == "" || utf8.RuneCountInString(value) > max || strings.ContainsAny(value, "\r\n\x00") {
		return invalid(field, "empty, too long, or contains control characters")
	}
	lower := strings.ToLower(value)
	for _, forbidden := range []string{
		"raw_output", "stdout", "stderr", "prompt:", "response:", "password=", "token=", "secret=",
		"/home/", `c:\users\`, "repository content", "chat content",
	} {
		if strings.Contains(lower, forbidden) {
			return invalid(field, "contains privacy-forbidden content")
		}
	}
	return nil
}

func validateUTC(field string, value time.Time) error {
	if value.IsZero() {
		return invalid(field, "zero timestamp")
	}
	_, offset := value.Zone()
	if offset != 0 {
		return invalid(field, "timestamp must be UTC")
	}
	return nil
}

func validateIDs(field string, values []string, pattern *regexp.Regexp) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !pattern.MatchString(value) {
			return invalid(field, value)
		}
		if _, exists := seen[value]; exists {
			return invalid(field, "duplicate "+value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateStepGraph(steps []StepReceipt) error {
	dependencies := make(map[string][]string, len(steps))
	for _, step := range steps {
		dependencies[step.StepID] = step.PrerequisiteStepIDs
	}
	visiting := make(map[string]bool, len(steps))
	visited := make(map[string]bool, len(steps))
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return invalid("steps", "prerequisite cycle at "+id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dependency := range dependencies[id] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		delete(visiting, id)
		visited[id] = true
		return nil
	}
	for id := range dependencies {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func invalid(field, value string) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalidState, field, value)
}

func stringSet(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validCommand(value Command) bool {
	switch value {
	case CommandSetup, CommandRepair, CommandModelsInstall, CommandModelsRemove, CommandVSCodeConfigure, CommandUpdate, CommandUpdateRollback, CommandUninstall:
		return true
	default:
		return false
	}
}

func validTransactionStatus(value TransactionStatus) bool {
	switch value {
	case StatusPlanned, StatusRunning, StatusVerifying, StatusAwaitingManual, StatusInterrupted, StatusFailed, StatusRollingBack, StatusRolledBack, StatusRollbackFailed, StatusSucceeded, StatusArchived:
		return true
	default:
		return false
	}
}

func validStepStatus(value StepStatus) bool {
	switch value {
	case StepPending, StepBlocked, StepRunning, StepAwaitingManual, StepFailed, StepVerified, StepUndone, StepUndoFailed, StepSkippedVerified:
		return true
	default:
		return false
	}
}

func validOwner(value Owner) bool {
	switch value {
	case OwnerApp, OwnerState, OwnerPlatform, OwnerHardware, OwnerCatalog, OwnerOllama, OwnerVSCode, OwnerInstall, OwnerUpdate:
		return true
	default:
		return false
	}
}

func validRisk(value Risk) bool {
	switch value {
	case RiskReadOnly, RiskUserLocal, RiskLargeDownload, RiskPrivileged, RiskDestructive, RiskExternalAppRestart:
		return true
	default:
		return false
	}
}

func validEvidenceKind(value EvidenceKind) bool {
	switch value {
	case EvidenceVersion, EvidenceDigest, EvidenceFileState, EvidenceApplicationState, EvidenceServiceState, EvidenceRegistryMetadata, EvidenceModelMetadata, EvidenceContextAllocation, EvidenceToolCall, EvidenceOffload, EvidenceProfileState, EvidenceExtensionState, EvidenceCapacity:
		return true
	default:
		return false
	}
}

func validAssetType(value AssetType) bool {
	switch value {
	case AssetManagerBinary, AssetManagerDirectory, AssetPathEntry, AssetDownloadCache, AssetRollbackBinary, AssetVSCodeApplication, AssetOllamaApplication, AssetOllamaService, AssetVSCodeProfile, AssetVSCodeExtension, AssetOllamaSourceModel, AssetOllamaAlias, AssetStateFile, AssetBackupFile:
		return true
	default:
		return false
	}
}

func validOrigin(value AssetOrigin) bool {
	switch value {
	case OriginInstalledByManager, OriginCreatedByManager, OriginDetectedExisting, OriginUserDeclared, OriginAdopted:
		return true
	default:
		return false
	}
}

func validOwnership(value Ownership) bool {
	return value == OwnershipManagerOwned || value == OwnershipUserOwned || value == OwnershipExternalDetected
}

func isTerminal(status TransactionStatus) bool {
	switch status {
	case StatusSucceeded, StatusRolledBack, StatusRollbackFailed:
		return true
	default:
		return false
	}
}
