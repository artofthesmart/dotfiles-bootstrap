package catalog

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const gibibyte = uint64(1024 * 1024 * 1024)

func Recommend(value Catalog, host Host, options RecommendOptions) (Selection, error) {
	if err := Validate(value); err != nil {
		return Selection{}, err
	}

	candidates := make([]CandidateEvaluation, 0, len(value.Entries))
	entryByID := make(map[string]Entry, len(value.Entries))
	for _, entry := range value.Entries {
		entryByID[entry.ID] = entry
		candidates = append(candidates, evaluate(entry, host, options))
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidateLess(candidates[i], candidates[j], entryByID)
	})

	result := Selection{
		SchemaVersion:  SelectionSchemaVersion,
		CatalogVersion: value.CatalogVersion,
		Candidates:     candidates,
		Notices: []Explanation{
			{
				Code:   "runtime_verification_required",
				Detail: "Catalog eligibility is planning evidence only; exact registry metadata, local metadata, a structured tool call, allocated context, and actual offload must be verified at runtime.",
			},
		},
	}

	for i := range candidates {
		if candidates[i].Eligible {
			primary := candidates[i]
			result.Primary = &primary
			break
		}
	}
	if result.Primary == nil {
		result.Notices = append(result.Notices, Explanation{
			Code:   "no_eligible_primary",
			Detail: "No reviewed entry satisfied the known host, capacity, status, version, and license constraints.",
		})
		return result, nil
	}

	primarySize := entryByID[result.Primary.CatalogID].Download.MaximumBytes
	for i := range candidates {
		entry := entryByID[candidates[i].CatalogID]
		if candidates[i].Eligible && candidates[i].CatalogID != result.Primary.CatalogID && entry.Download.MaximumBytes < primarySize {
			fallback := candidates[i]
			result.Fallback = &fallback
			break
		}
	}
	if result.Fallback == nil {
		result.Notices = append(result.Notices, Explanation{
			Code:   "no_smaller_fallback",
			Detail: "No eligible reviewed entry has a smaller maximum download than the primary recommendation.",
		})
	}
	return result, nil
}

func evaluate(entry Entry, host Host, options RecommendOptions) CandidateEvaluation {
	result := CandidateEvaluation{
		CatalogID:       entry.ID,
		OllamaName:      entry.Model.OllamaName,
		Status:          entry.Status,
		Disqualifiers:   []Explanation{},
		Unknowns:        []Explanation{},
		ScoreComponents: []ScoreComponent{},
	}

	if !containsFold(entry.Eligibility.OperatingSystems, host.OperatingSystem) {
		addDisqualifier(&result, "unsupported_operating_system", fmt.Sprintf("%q is not in the entry operating-system allowlist", host.OperatingSystem))
	}
	if !containsFold(entry.Eligibility.Architectures, host.Architecture) {
		addDisqualifier(&result, "unsupported_architecture", fmt.Sprintf("%q is not in the entry architecture allowlist", host.Architecture))
	}
	if !containsMode(entry.Eligibility.ExecutionModes, host.ExecutionMode) {
		addDisqualifier(&result, "unsupported_execution_mode", fmt.Sprintf("%q is not supported by this entry", host.ExecutionMode))
	}
	if entry.Context.AdvertisedNativeTokens < RequiredContextTokens {
		addDisqualifier(&result, "native_context_below_minimum", "native context is below 65,536 tokens")
	}
	applyStatusPolicy(&result, entry.Status, options)
	applyLicensePolicy(&result, entry.License, options.License)
	applyVersionPolicy(&result, entry.OllamaCompatibility, host.OllamaVersion)

	tier, ok := findTier(entry.HostTiers, host.ExecutionMode)
	if !ok {
		addDisqualifier(&result, "missing_host_tier", fmt.Sprintf("no host tier exists for %q", host.ExecutionMode))
		result.Memory = MemoryAssessment{Accounting: "unavailable"}
		result.Storage = StorageAssessment{DownloadMaximumBytes: entry.Download.MaximumBytes}
		result.Offload = OffloadAssessment{Expected: OffloadUnknown, Practicality: "unknown", Detail: "No matching host tier."}
	} else {
		result.Memory, result.Offload = assessMemory(entry, tier, host, &result)
		result.Storage = assessStorage(entry, tier, host, &result)
	}

	addBaseScores(&result, entry)
	addCapacityScores(&result)
	for _, adjustment := range entry.Recommendation.ScoreAdjustments {
		addScore(&result, "catalog_adjustment", adjustment.Points, adjustment.Reason)
	}
	if len(result.Unknowns) > 0 {
		addScore(&result, "unknown_evidence", -3*len(result.Unknowns), fmt.Sprintf("%d unresolved host or compatibility fact(s)", len(result.Unknowns)))
	}

	result.Eligible = len(result.Disqualifiers) == 0
	return result
}

func assessMemory(entry Entry, tier HostTier, host Host, result *CandidateEvaluation) (MemoryAssessment, OffloadAssessment) {
	kvBytes := saturatingMultiply(entry.MemoryEstimate.KVCacheBytesPerToken.MaximumBytes, entry.Context.RequiredTokens)
	working := saturatingAdd(entry.MemoryEstimate.ModelWeightsBytes.MaximumBytes, kvBytes)
	working = saturatingAdd(working, entry.MemoryEstimate.RuntimeOverheadBytes.MaximumBytes)
	headroom := entry.MemoryEstimate.MinimumOSEditorHeadroomBytes

	assessment := MemoryAssessment{
		WorkingSetBytes:            working,
		HeadroomBytes:              headroom,
		AvailableSystemMemoryBytes: cloneUint64(host.SystemMemoryBytes),
	}
	offload := OffloadAssessment{
		Expected:     tier.ExpectedOffload,
		Practicality: string(tier.ExpectedOffload),
	}

	switch host.ExecutionMode {
	case ExecutionCPUOnly:
		assessment.Accounting = "cpu_only_system_pool"
		assessment.RequiredSystemMemoryBytes = maxUint64(tier.MinimumSystemMemoryBytes, saturatingAdd(working, headroom))
		offload.Expected = OffloadCPU
		offload.Practicality = "cpu"
		offload.Detail = "Weights, KV cache, runtime overhead, editor, and OS headroom share system memory."
	case ExecutionAppleUnified:
		assessment.Accounting = "apple_unified_single_pool"
		assessment.RequiredSystemMemoryBytes = maxUint64(tier.MinimumSystemMemoryBytes, saturatingAdd(working, headroom))
		offload.Expected = tier.ExpectedOffload
		offload.Practicality = "unified_memory"
		offload.Detail = "CPU and GPU use one unified-memory pool; no separate VRAM is added."
	case ExecutionIntegratedShared:
		assessment.Accounting = "integrated_shared_single_pool"
		assessment.RequiredSystemMemoryBytes = maxUint64(tier.MinimumSystemMemoryBytes, saturatingAdd(working, headroom))
		offload.Expected = OffloadUnknown
		offload.Practicality = "runtime_required"
		offload.Detail = "Integrated graphics borrow system memory; no fixed or additional VRAM is invented."
	case ExecutionDedicatedGPU:
		assessment.Accounting = "dedicated_separate_pools"
		assessment.AvailableAcceleratorBytes = cloneUint64(host.AcceleratorMemoryBytes)
		assessment.RequiredAcceleratorBytes = cloneUint64(tier.MinimumAcceleratorMemoryBytes)
		if host.AcceleratorMemoryBytes == nil {
			assessment.RequiredSystemMemoryBytes = maxUint64(tier.MinimumSystemMemoryBytes, saturatingAdd(working, headroom))
			offload.Expected = OffloadUnknown
			offload.Practicality = "accelerator_memory_unknown"
			offload.Detail = "Dedicated accelerator memory is unknown, so the recommendation requires a system-memory CPU fallback budget and does not assume offload."
			addUnknown(result, "accelerator_memory_unknown", "Dedicated accelerator memory was not reported; full or partial offload cannot be predicted.")
		} else if *host.AcceleratorMemoryBytes >= working {
			assessment.RequiredSystemMemoryBytes = tier.MinimumSystemMemoryBytes
			required := working
			assessment.RequiredAcceleratorBytes = &required
			offload.Expected = OffloadFullGPU
			offload.Practicality = "full_gpu_plausible"
			offload.Detail = "Known dedicated memory covers the conservative working-set estimate; runtime proof is still required."
		} else if tier.MinimumAcceleratorMemoryBytes != nil && *host.AcceleratorMemoryBytes >= *tier.MinimumAcceleratorMemoryBytes {
			spill := working - *host.AcceleratorMemoryBytes
			assessment.RequiredSystemMemoryBytes = maxUint64(tier.MinimumSystemMemoryBytes, saturatingAdd(spill, headroom))
			offload.Expected = OffloadPartialGPU
			offload.Practicality = "partial_gpu_plausible"
			offload.Detail = "Known dedicated memory supports the tier minimum but not the complete conservative working set; separate system-memory spill capacity is required."
		} else {
			assessment.RequiredSystemMemoryBytes = tier.MinimumSystemMemoryBytes
			offload.Expected = OffloadUnknown
			offload.Practicality = "accelerator_below_tier"
			offload.Detail = "Known dedicated memory is below the reviewed tier minimum."
			addDisqualifier(result, "insufficient_accelerator_memory", "Known dedicated accelerator memory is below the entry tier minimum.")
		}
	default:
		assessment.Accounting = "unknown"
		offload.Expected = OffloadUnknown
		offload.Practicality = "unknown"
		offload.Detail = "Execution mode is unknown."
		addDisqualifier(result, "unsupported_execution_mode", fmt.Sprintf("execution mode %q is not recognized", host.ExecutionMode))
	}

	if host.SystemMemoryBytes == nil {
		addUnknown(result, "system_memory_unknown", "System memory was not reported; the conservative memory budget cannot be confirmed.")
	} else if *host.SystemMemoryBytes < assessment.RequiredSystemMemoryBytes {
		addDisqualifier(result, "insufficient_memory_headroom", fmt.Sprintf("known system memory %d is below conservative requirement %d", *host.SystemMemoryBytes, assessment.RequiredSystemMemoryBytes))
	}
	return assessment, offload
}

func assessStorage(entry Entry, tier HostTier, host Host, result *CandidateEvaluation) StorageAssessment {
	headroom := maxUint64(2*gibibyte, entry.Download.MaximumBytes/5)
	required := maxUint64(tier.MinimumFreeStorageBytes, saturatingAdd(entry.Download.MaximumBytes, headroom))
	assessment := StorageAssessment{
		DownloadMaximumBytes: entry.Download.MaximumBytes,
		HeadroomBytes:        headroom,
		RequiredFreeBytes:    required,
		AvailableFreeBytes:   cloneUint64(host.FreeStorageBytes),
	}
	if host.FreeStorageBytes == nil {
		addUnknown(result, "free_storage_unknown", "Free model-volume storage was not reported; download headroom cannot be confirmed.")
	} else if *host.FreeStorageBytes < required {
		addDisqualifier(result, "insufficient_disk", fmt.Sprintf("known free storage %d is below conservative requirement %d", *host.FreeStorageBytes, required))
	}
	return assessment
}

func applyStatusPolicy(result *CandidateEvaluation, status Status, options RecommendOptions) {
	switch status {
	case StatusRecommended, StatusFallback:
	case StatusExperimental:
		if !options.IncludeExperimental {
			addDisqualifier(result, "experimental_status_excluded", "Experimental entries require explicit inclusion.")
		}
	case StatusDeprecated:
		if !options.IncludeDeprecated {
			addDisqualifier(result, "deprecated_status_excluded", "Deprecated entries are excluded by default.")
		}
	case StatusBlocked:
		addDisqualifier(result, "blocked_status", "Blocked entries are never recommendation-eligible.")
	default:
		addDisqualifier(result, "unknown_status", fmt.Sprintf("status %q is not recognized", status))
	}
}

func applyLicensePolicy(result *CandidateEvaluation, license License, policy LicensePolicy) {
	if len(policy.AllowedIdentifiers) > 0 && !containsFold(policy.AllowedIdentifiers, license.Identifier) {
		addDisqualifier(result, "license_incompatible", fmt.Sprintf("license %q is outside the allowed identifier set", license.Identifier))
		return
	}
	if !policy.RequireCommercialUse {
		return
	}
	switch license.CommercialUse {
	case "allowed":
	case "restricted":
		if !policy.AllowRestrictedCommercial {
			addDisqualifier(result, "license_incompatible", "commercial use is restricted and restricted licenses were not allowed")
		}
	case "prohibited", "unclear":
		addDisqualifier(result, "license_incompatible", fmt.Sprintf("commercial use is %s", license.CommercialUse))
	default:
		addDisqualifier(result, "license_incompatible", "commercial-use classification is unknown")
	}
}

func applyVersionPolicy(result *CandidateEvaluation, compatibility OllamaCompatibility, version string) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "" {
		addUnknown(result, "ollama_version_unknown", "Ollama version was not reported; minimum compatibility cannot be confirmed.")
		return
	}
	if !semverPattern.MatchString(version) {
		addUnknown(result, "ollama_version_unparseable", fmt.Sprintf("Ollama version %q is not semantic and was not used for exclusion.", version))
		return
	}
	if compareSemver(version, compatibility.MinimumVersion) < 0 {
		addDisqualifier(result, "ollama_version_incompatible", fmt.Sprintf("Ollama %s is older than required %s", version, compatibility.MinimumVersion))
		return
	}
	if compatibility.MaximumTestedVersion != nil && compareSemver(version, *compatibility.MaximumTestedVersion) > 0 {
		addUnknown(result, "ollama_version_newer_than_review", fmt.Sprintf("Ollama %s is newer than catalog-tested %s", version, *compatibility.MaximumTestedVersion))
	}
}

func addBaseScores(result *CandidateEvaluation, entry Entry) {
	addScore(result, "agent_reliability", entry.Recommendation.AgentReliabilityTier*20, fmt.Sprintf("catalog tier %d/5", entry.Recommendation.AgentReliabilityTier))
	addScore(result, "tool_schema", entry.Recommendation.ToolSchemaTier*12, fmt.Sprintf("catalog tier %d/5", entry.Recommendation.ToolSchemaTier))
	addScore(result, "cpu_fallback", entry.Recommendation.CPUFallbackTier*4, fmt.Sprintf("catalog tier %d/5", entry.Recommendation.CPUFallbackTier))

	statusPoints := map[Status]int{
		StatusRecommended:  20,
		StatusFallback:     5,
		StatusExperimental: -10,
		StatusDeprecated:   -35,
		StatusBlocked:      -100,
	}[entry.Status]
	addScore(result, "catalog_status", statusPoints, string(entry.Status))

	sizeGiB := int((entry.Download.MaximumBytes + gibibyte - 1) / gibibyte)
	sizePoints := 20 - sizeGiB
	if sizePoints < 0 {
		sizePoints = 0
	}
	addScore(result, "download_size", sizePoints, fmt.Sprintf("maximum reviewed transfer is %d GiB rounded up", sizeGiB))

	switch result.Offload.Expected {
	case OffloadFullGPU:
		addScore(result, "offload_practicality", 30, result.Offload.Detail)
	case OffloadPartialGPU:
		addScore(result, "offload_practicality", 15, result.Offload.Detail)
	case OffloadCPU:
		addScore(result, "offload_practicality", 0, result.Offload.Detail)
	default:
		addScore(result, "offload_practicality", -10, result.Offload.Detail)
	}
}

func addCapacityScores(result *CandidateEvaluation) {
	if result.Memory.AvailableSystemMemoryBytes != nil && *result.Memory.AvailableSystemMemoryBytes >= result.Memory.RequiredSystemMemoryBytes {
		points := headroomPoints(*result.Memory.AvailableSystemMemoryBytes, result.Memory.RequiredSystemMemoryBytes, 20)
		if result.Memory.RequiredAcceleratorBytes != nil && result.Memory.AvailableAcceleratorBytes != nil &&
			*result.Memory.AvailableAcceleratorBytes >= *result.Memory.RequiredAcceleratorBytes {
			points = minInt(points, headroomPoints(*result.Memory.AvailableAcceleratorBytes, *result.Memory.RequiredAcceleratorBytes, 20))
		}
		addScore(result, "memory_headroom", points, "minimum proportional headroom across required known pools")
	} else {
		addScore(result, "memory_headroom", 0, "memory headroom is unknown or insufficient")
	}
	if result.Storage.AvailableFreeBytes != nil && *result.Storage.AvailableFreeBytes >= result.Storage.RequiredFreeBytes {
		addScore(result, "storage_headroom", headroomPoints(*result.Storage.AvailableFreeBytes, result.Storage.RequiredFreeBytes, 10), "free storage beyond download and retained headroom")
	} else {
		addScore(result, "storage_headroom", 0, "storage headroom is unknown or insufficient")
	}
}

func candidateLess(left, right CandidateEvaluation, entries map[string]Entry) bool {
	if left.Eligible != right.Eligible {
		return left.Eligible
	}
	if left.Score != right.Score {
		return left.Score > right.Score
	}
	leftRank := statusRank(left.Status)
	rightRank := statusRank(right.Status)
	if leftRank != rightRank {
		return leftRank > rightRank
	}
	leftSize := entries[left.CatalogID].Download.MaximumBytes
	rightSize := entries[right.CatalogID].Download.MaximumBytes
	if leftSize != rightSize {
		return leftSize < rightSize
	}
	return left.CatalogID < right.CatalogID
}

func statusRank(value Status) int {
	switch value {
	case StatusRecommended:
		return 5
	case StatusFallback:
		return 4
	case StatusExperimental:
		return 3
	case StatusDeprecated:
		return 2
	case StatusBlocked:
		return 1
	default:
		return 0
	}
}

func findTier(values []HostTier, wanted ExecutionMode) (HostTier, bool) {
	for _, value := range values {
		if value.ExecutionMode == wanted {
			return value, true
		}
	}
	return HostTier{}, false
}

func addScore(result *CandidateEvaluation, name string, points int, detail string) {
	result.Score += points
	result.ScoreComponents = append(result.ScoreComponents, ScoreComponent{Name: name, Points: points, Detail: detail})
}

func addDisqualifier(result *CandidateEvaluation, code, detail string) {
	if !hasExplanation(result.Disqualifiers, code) {
		result.Disqualifiers = append(result.Disqualifiers, Explanation{Code: code, Detail: detail})
	}
}

func addUnknown(result *CandidateEvaluation, code, detail string) {
	if !hasExplanation(result.Unknowns, code) {
		result.Unknowns = append(result.Unknowns, Explanation{Code: code, Detail: detail})
	}
}

func hasExplanation(values []Explanation, code string) bool {
	for _, value := range values {
		if value.Code == code {
			return true
		}
	}
	return false
}

func containsFold(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(value, wanted) {
			return true
		}
	}
	return false
}

func containsMode(values []ExecutionMode, wanted ExecutionMode) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func compareSemver(left, right string) int {
	leftParts := semverNumbers(left)
	rightParts := semverNumbers(right)
	for i := range leftParts {
		if leftParts[i] < rightParts[i] {
			return -1
		}
		if leftParts[i] > rightParts[i] {
			return 1
		}
	}
	return 0
}

func semverNumbers(value string) [3]int {
	value = strings.SplitN(value, "-", 2)[0]
	value = strings.SplitN(value, "+", 2)[0]
	parts := strings.Split(value, ".")
	var result [3]int
	for i := 0; i < len(result) && i < len(parts); i++ {
		result[i], _ = strconv.Atoi(parts[i])
	}
	return result
}

func headroomPoints(available, required uint64, maximum int) int {
	if required == 0 || available <= required {
		return 0
	}
	difference := available - required
	if difference >= required {
		return maximum
	}
	return int(difference * uint64(maximum) / required)
}

func cloneUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func saturatingAdd(left, right uint64) uint64 {
	result := left + right
	if result < left {
		return ^uint64(0)
	}
	return result
}

func saturatingMultiply(left, right uint64) uint64 {
	if left == 0 || right == 0 {
		return 0
	}
	if left > ^uint64(0)/right {
		return ^uint64(0)
	}
	return left * right
}

func maxUint64(left, right uint64) uint64 {
	if left > right {
		return left
	}
	return right
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
