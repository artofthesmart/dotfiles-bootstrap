package catalog

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	idPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	modelPattern  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*:[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
)

func Validate(value Catalog) error {
	if value.SchemaVersion != SchemaVersion {
		return fieldError("schema_version", "must be %q", SchemaVersion)
	}
	if !semverPattern.MatchString(value.CatalogVersion) {
		return fieldError("catalog_version", "must be a semantic version")
	}
	reviewedAt, err := parseDate(value.ReviewedAt)
	if err != nil {
		return fieldError("reviewed_at", "%v", err)
	}
	if len(value.Entries) == 0 {
		return fieldError("entries", "must contain at least one entry")
	}

	ids := make(map[string]struct{}, len(value.Entries))
	models := make(map[string]struct{}, len(value.Entries))
	for i := range value.Entries {
		path := fmt.Sprintf("entries[%d]", i)
		entry := value.Entries[i]
		if err := validateEntry(path, entry, reviewedAt); err != nil {
			return err
		}
		if _, exists := ids[entry.ID]; exists {
			return fieldError(path+".id", "duplicates stable ID %q", entry.ID)
		}
		ids[entry.ID] = struct{}{}
		modelKey := strings.ToLower(entry.Model.OllamaName)
		if _, exists := models[modelKey]; exists {
			return fieldError(path+".model.ollama_name", "duplicates exact tag %q", entry.Model.OllamaName)
		}
		models[modelKey] = struct{}{}
	}
	return nil
}

func validateEntry(path string, entry Entry, reviewedAt time.Time) error {
	if !idPattern.MatchString(entry.ID) {
		return fieldError(path+".id", "must be a stable lowercase catalog ID")
	}
	if !oneOf(string(entry.Status), "recommended", "fallback", "experimental", "deprecated", "blocked") {
		return fieldError(path+".status", "is not recognized")
	}
	if !modelPattern.MatchString(entry.Model.OllamaName) {
		return fieldError(path+".model.ollama_name", "must be an exact Ollama name and tag")
	}
	if strings.Contains(strings.ToLower(entry.Model.OllamaName), "cloud") {
		return fieldError(path+".model.ollama_name", "cloud-only tags are forbidden")
	}
	if err := requiredText(path+".model.family", entry.Model.Family, 80); err != nil {
		return err
	}
	if entry.Model.ParameterScaleBillion <= 0 {
		return fieldError(path+".model.parameter_scale_billion", "must be positive")
	}
	if err := requiredText(path+".model.quantization", entry.Model.Quantization, 64); err != nil {
		return err
	}
	if err := requiredText(path+".model.architecture", entry.Model.Architecture, 64); err != nil {
		return err
	}
	if err := requiredText(path+".source.publisher", entry.Source.Publisher, 120); err != nil {
		return err
	}
	if err := httpsURL(path+".source.model_page", entry.Source.ModelPage); err != nil {
		return err
	}
	if err := httpsURL(path+".source.registry_manifest", entry.Source.RegistryManifest); err != nil {
		return err
	}
	if entry.Source.ExpectedModelDigest == nil || !digestPattern.MatchString(*entry.Source.ExpectedModelDigest) {
		return fieldError(path+".source.expected_model_digest", "must pin one sha256 model-layer digest")
	}
	if err := byteRange(path+".download", entry.Download); err != nil {
		return err
	}
	if entry.Context.AdvertisedNativeTokens < RequiredContextTokens {
		return fieldError(path+".context.advertised_native_tokens", "must be at least %d", RequiredContextTokens)
	}
	if entry.Context.RequiredTokens != RequiredContextTokens {
		return fieldError(path+".context.required_tokens", "must be %d", RequiredContextTokens)
	}
	if !oneOf(entry.Context.VerificationMethod, "registry_gguf_metadata", "official_model_metadata", "both") {
		return fieldError(path+".context.verification_method", "is not recognized")
	}
	if err := textList(path+".context.extended_context_requirements", entry.Context.ExtendedContextRequirements, 320, false); err != nil {
		return err
	}
	if !entry.Tools.Advertised {
		return fieldError(path+".tools.advertised", "must be true")
	}
	if !oneOf(entry.Tools.EvidenceLevel, "official_claim", "maintainer_reproduction", "physical_acceptance") {
		return fieldError(path+".tools.evidence_level", "is not recognized")
	}
	if !oneOf(entry.Tools.LargeToolSet.Status, "verified", "claimed", "unknown", "failed") {
		return fieldError(path+".tools.large_tool_set.status", "is not recognized")
	}
	if entry.Tools.LargeToolSet.TestedDefinitionCount != nil && *entry.Tools.LargeToolSet.TestedDefinitionCount == 0 {
		return fieldError(path+".tools.large_tool_set.tested_definition_count", "must be positive")
	}
	if !oneOf(entry.Tools.ComplexSchema, "verified", "claimed", "unknown", "failed") {
		return fieldError(path+".tools.complex_schema", "is not recognized")
	}
	if err := textList(path+".tools.known_template_caveats", entry.Tools.KnownTemplateCaveats, 320, false); err != nil {
		return err
	}
	if err := urlList(path+".tools.evidence_references", entry.Tools.EvidenceReferences); err != nil {
		return err
	}
	if !semverPattern.MatchString(entry.OllamaCompatibility.MinimumVersion) {
		return fieldError(path+".ollama_compatibility.minimum_version", "must be a semantic version")
	}
	if entry.OllamaCompatibility.MaximumTestedVersion != nil {
		if !semverPattern.MatchString(*entry.OllamaCompatibility.MaximumTestedVersion) {
			return fieldError(path+".ollama_compatibility.maximum_tested_version", "must be a semantic version")
		}
		if compareSemver(*entry.OllamaCompatibility.MaximumTestedVersion, entry.OllamaCompatibility.MinimumVersion) < 0 {
			return fieldError(path+".ollama_compatibility", "maximum tested version precedes minimum version")
		}
	}
	if err := validateLicense(path+".license", entry.License); err != nil {
		return err
	}
	if err := validateEligibility(path+".eligibility", entry.Eligibility); err != nil {
		return err
	}
	if err := validateHostTiers(path+".host_tiers", entry.HostTiers, entry.Eligibility.ExecutionModes); err != nil {
		return err
	}
	if err := validateMemory(path+".memory_estimate", entry.MemoryEstimate); err != nil {
		return err
	}
	if err := validateRecommendation(path+".recommendation", entry.Recommendation); err != nil {
		return err
	}
	if err := textList(path+".known_caveats", entry.KnownCaveats, 400, false); err != nil {
		return err
	}
	evidenceDate, err := parseDate(entry.EvidenceDate)
	if err != nil {
		return fieldError(path+".evidence_date", "%v", err)
	}
	if evidenceDate.After(reviewedAt) {
		return fieldError(path+".evidence_date", "cannot be after reviewed_at")
	}
	if err := urlList(path+".references", entry.References); err != nil {
		return err
	}
	return nil
}

func validateLicense(path string, value License) error {
	if err := requiredText(path+".identifier", value.Identifier, 80); err != nil {
		return err
	}
	if err := requiredText(path+".name", value.Name, 160); err != nil {
		return err
	}
	if !oneOf(value.CommercialUse, "allowed", "restricted", "prohibited", "unclear") {
		return fieldError(path+".commercial_use", "is not recognized")
	}
	if err := requiredText(path+".summary", value.Summary, 500); err != nil {
		return err
	}
	return httpsURL(path+".source", value.Source)
}

func validateEligibility(path string, value Eligibility) error {
	if err := enumList(path+".operating_systems", value.OperatingSystems, []string{"windows", "macos", "linux"}); err != nil {
		return err
	}
	if err := enumList(path+".architectures", value.Architectures, []string{"amd64", "arm64"}); err != nil {
		return err
	}
	modes := make([]string, len(value.ExecutionModes))
	for i := range value.ExecutionModes {
		modes[i] = string(value.ExecutionModes[i])
	}
	if err := enumList(path+".execution_modes", modes, []string{"cpu_only", "apple_unified", "integrated_shared", "dedicated_gpu"}); err != nil {
		return err
	}
	allowedDisqualifiers := []string{
		"unsupported_architecture", "cloud_only", "native_context_below_minimum",
		"license_incompatible", "tool_capability_failed", "insufficient_disk",
		"insufficient_memory_headroom", "ollama_version_incompatible", "blocked_status",
	}
	return enumListAllowEmpty(path+".disqualifiers", value.Disqualifiers, allowedDisqualifiers)
}

func validateHostTiers(path string, tiers []HostTier, modes []ExecutionMode) error {
	if len(tiers) == 0 {
		return fieldError(path, "must contain at least one tier")
	}
	wanted := make(map[ExecutionMode]struct{}, len(modes))
	for _, mode := range modes {
		wanted[mode] = struct{}{}
	}
	seen := make(map[ExecutionMode]struct{}, len(tiers))
	for i, tier := range tiers {
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		if _, ok := wanted[tier.ExecutionMode]; !ok {
			return fieldError(itemPath+".execution_mode", "has no matching eligibility execution mode")
		}
		if _, exists := seen[tier.ExecutionMode]; exists {
			return fieldError(itemPath+".execution_mode", "duplicates %q", tier.ExecutionMode)
		}
		seen[tier.ExecutionMode] = struct{}{}
		if tier.MinimumSystemMemoryBytes == 0 {
			return fieldError(itemPath+".minimum_system_memory_bytes", "must be positive")
		}
		if tier.MinimumFreeStorageBytes < 2*1024*1024*1024 {
			return fieldError(itemPath+".minimum_free_storage_bytes", "must be at least 2 GiB")
		}
		if tier.ExecutionMode == ExecutionDedicatedGPU {
			if tier.MinimumAcceleratorMemoryBytes == nil || *tier.MinimumAcceleratorMemoryBytes == 0 {
				return fieldError(itemPath+".minimum_accelerator_memory_bytes", "must be positive for dedicated GPU mode")
			}
		} else if tier.MinimumAcceleratorMemoryBytes != nil {
			return fieldError(itemPath+".minimum_accelerator_memory_bytes", "must be null for shared, unified, or CPU-only modes")
		}
		if !oneOf(string(tier.ExpectedOffload), "cpu", "partial_gpu", "full_gpu", "unknown") {
			return fieldError(itemPath+".expected_offload", "is not recognized")
		}
		if err := textList(itemPath+".notes", tier.Notes, 320, false); err != nil {
			return err
		}
	}
	if len(seen) != len(wanted) {
		return fieldError(path, "must define exactly one tier for each eligible execution mode")
	}
	return nil
}

func validateMemory(path string, value MemoryEstimate) error {
	if value.FormulaVersion != 1 {
		return fieldError(path+".formula_version", "must be 1")
	}
	if err := byteRange(path+".model_weights_bytes", value.ModelWeightsBytes); err != nil {
		return err
	}
	if err := byteRange(path+".kv_cache_bytes_per_token", value.KVCacheBytesPerToken); err != nil {
		return err
	}
	if err := byteRange(path+".runtime_overhead_bytes", value.RuntimeOverheadBytes); err != nil {
		return err
	}
	if value.MinimumOSEditorHeadroomBytes < 1024*1024*1024 {
		return fieldError(path+".minimum_os_editor_headroom_bytes", "must be at least 1 GiB")
	}
	return nil
}

func validateRecommendation(path string, value Recommendation) error {
	for name, tier := range map[string]int{
		"agent_reliability_tier": value.AgentReliabilityTier,
		"tool_schema_tier":       value.ToolSchemaTier,
		"cpu_fallback_tier":      value.CPUFallbackTier,
	} {
		if tier < 0 || tier > 5 {
			return fieldError(path+"."+name, "must be between 0 and 5")
		}
	}
	for i, adjustment := range value.ScoreAdjustments {
		itemPath := fmt.Sprintf("%s.score_adjustments[%d]", path, i)
		if err := requiredText(itemPath+".reason", adjustment.Reason, 240); err != nil {
			return err
		}
		if adjustment.Points < -100 || adjustment.Points > 100 {
			return fieldError(itemPath+".points", "must be between -100 and 100")
		}
	}
	return nil
}

func byteRange(path string, value ByteRange) error {
	if value.MinimumBytes == 0 || value.MaximumBytes == 0 {
		return fieldError(path, "bounds must be positive")
	}
	if value.MinimumBytes > value.MaximumBytes {
		return fieldError(path, "minimum_bytes cannot exceed maximum_bytes")
	}
	return nil
}

func requiredText(path, value string, maximum int) error {
	length := len(strings.TrimSpace(value))
	if length == 0 {
		return fieldError(path, "must not be empty")
	}
	if length > maximum {
		return fieldError(path, "must be at most %d bytes", maximum)
	}
	return nil
}

func textList(path string, values []string, maximum int, requireOne bool) error {
	if requireOne && len(values) == 0 {
		return fieldError(path, "must contain at least one value")
	}
	for i, value := range values {
		if err := requiredText(fmt.Sprintf("%s[%d]", path, i), value, maximum); err != nil {
			return err
		}
	}
	return nil
}

func urlList(path string, values []string) error {
	if len(values) == 0 {
		return fieldError(path, "must contain at least one URL")
	}
	seen := make(map[string]struct{}, len(values))
	for i, value := range values {
		if err := httpsURL(fmt.Sprintf("%s[%d]", path, i), value); err != nil {
			return err
		}
		if _, exists := seen[value]; exists {
			return fieldError(path, "contains duplicate URL %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func httpsURL(path, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fieldError(path, "must be an absolute HTTPS URL without credentials")
	}
	return nil
}

func enumList(path string, values, allowed []string) error {
	if len(values) == 0 {
		return fieldError(path, "must contain at least one value")
	}
	return enumListAllowEmpty(path, values, allowed)
}

func enumListAllowEmpty(path string, values, allowed []string) error {
	seen := make(map[string]struct{}, len(values))
	for i, value := range values {
		if !containsString(allowed, value) {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), "is not recognized")
		}
		if _, exists := seen[value]; exists {
			return fieldError(path, "contains duplicate value %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func parseDate(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("must be an RFC 3339 full-date")
	}
	return parsed, nil
}

func fieldError(path, format string, arguments ...any) error {
	return fmt.Errorf("invalid model catalog %s: %s", path, fmt.Sprintf(format, arguments...))
}

func oneOf(value string, allowed ...string) bool {
	return containsString(allowed, value)
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
