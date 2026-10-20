package catalog

const (
	SchemaVersion          = "local-ai.model-catalog/v1"
	SelectionSchemaVersion = "local-ai.model-selection/v1"
	RequiredContextTokens  = uint64(65536)
)

type Status string

const (
	StatusRecommended  Status = "recommended"
	StatusFallback     Status = "fallback"
	StatusExperimental Status = "experimental"
	StatusDeprecated   Status = "deprecated"
	StatusBlocked      Status = "blocked"
)

type ExecutionMode string

const (
	ExecutionCPUOnly          ExecutionMode = "cpu_only"
	ExecutionAppleUnified     ExecutionMode = "apple_unified"
	ExecutionIntegratedShared ExecutionMode = "integrated_shared"
	ExecutionDedicatedGPU     ExecutionMode = "dedicated_gpu"
)

type Offload string

const (
	OffloadCPU        Offload = "cpu"
	OffloadPartialGPU Offload = "partial_gpu"
	OffloadFullGPU    Offload = "full_gpu"
	OffloadUnknown    Offload = "unknown"
)

type Catalog struct {
	SchemaVersion  string  `json:"schema_version"`
	CatalogVersion string  `json:"catalog_version"`
	ReviewedAt     string  `json:"reviewed_at"`
	Entries        []Entry `json:"entries"`
}

type Entry struct {
	ID                  string              `json:"id"`
	Status              Status              `json:"status"`
	Model               Model               `json:"model"`
	Source              Source              `json:"source"`
	Download            ByteRange           `json:"download"`
	Context             Context             `json:"context"`
	Tools               Tools               `json:"tools"`
	OllamaCompatibility OllamaCompatibility `json:"ollama_compatibility"`
	License             License             `json:"license"`
	Eligibility         Eligibility         `json:"eligibility"`
	HostTiers           []HostTier          `json:"host_tiers"`
	MemoryEstimate      MemoryEstimate      `json:"memory_estimate"`
	Recommendation      Recommendation      `json:"recommendation"`
	KnownCaveats        []string            `json:"known_caveats"`
	EvidenceDate        string              `json:"evidence_date"`
	References          []string            `json:"references"`
}

type Model struct {
	OllamaName            string  `json:"ollama_name"`
	Family                string  `json:"family"`
	ParameterScaleBillion float64 `json:"parameter_scale_billion"`
	Quantization          string  `json:"quantization"`
	Architecture          string  `json:"architecture"`
}

type Source struct {
	Publisher           string  `json:"publisher"`
	ModelPage           string  `json:"model_page"`
	RegistryManifest    string  `json:"registry_manifest"`
	ExpectedModelDigest *string `json:"expected_model_digest,omitempty"`
}

type ByteRange struct {
	MinimumBytes uint64 `json:"minimum_bytes"`
	MaximumBytes uint64 `json:"maximum_bytes"`
}

type Context struct {
	AdvertisedNativeTokens      uint64   `json:"advertised_native_tokens"`
	RequiredTokens              uint64   `json:"required_tokens"`
	VerificationMethod          string   `json:"verification_method"`
	ExtendedContextRequirements []string `json:"extended_context_requirements"`
}

type Tools struct {
	Advertised             bool         `json:"advertised"`
	EvidenceLevel          string       `json:"evidence_level"`
	StructuredCallVerified bool         `json:"structured_call_verified"`
	LargeToolSet           LargeToolSet `json:"large_tool_set"`
	ComplexSchema          string       `json:"complex_schema"`
	KnownTemplateCaveats   []string     `json:"known_template_caveats"`
	EvidenceReferences     []string     `json:"evidence_references"`
}

type LargeToolSet struct {
	Status                string  `json:"status"`
	TestedDefinitionCount *uint64 `json:"tested_definition_count"`
}

type OllamaCompatibility struct {
	MinimumVersion       string  `json:"minimum_version"`
	MaximumTestedVersion *string `json:"maximum_tested_version"`
}

type License struct {
	Identifier    string `json:"identifier"`
	Name          string `json:"name"`
	CommercialUse string `json:"commercial_use"`
	Summary       string `json:"summary"`
	Source        string `json:"source"`
}

type Eligibility struct {
	OperatingSystems []string        `json:"operating_systems"`
	Architectures    []string        `json:"architectures"`
	ExecutionModes   []ExecutionMode `json:"execution_modes"`
	Disqualifiers    []string        `json:"disqualifiers"`
}

type HostTier struct {
	ExecutionMode                 ExecutionMode `json:"execution_mode"`
	MinimumSystemMemoryBytes      uint64        `json:"minimum_system_memory_bytes"`
	MinimumAcceleratorMemoryBytes *uint64       `json:"minimum_accelerator_memory_bytes"`
	MinimumFreeStorageBytes       uint64        `json:"minimum_free_storage_bytes"`
	ExpectedOffload               Offload       `json:"expected_offload"`
	Notes                         []string      `json:"notes"`
}

type MemoryEstimate struct {
	FormulaVersion               int       `json:"formula_version"`
	ModelWeightsBytes            ByteRange `json:"model_weights_bytes"`
	KVCacheBytesPerToken         ByteRange `json:"kv_cache_bytes_per_token"`
	RuntimeOverheadBytes         ByteRange `json:"runtime_overhead_bytes"`
	MinimumOSEditorHeadroomBytes uint64    `json:"minimum_os_editor_headroom_bytes"`
}

type Recommendation struct {
	AgentReliabilityTier int               `json:"agent_reliability_tier"`
	ToolSchemaTier       int               `json:"tool_schema_tier"`
	CPUFallbackTier      int               `json:"cpu_fallback_tier"`
	ScoreAdjustments     []ScoreAdjustment `json:"score_adjustments"`
}

type ScoreAdjustment struct {
	Reason string `json:"reason"`
	Points int    `json:"points"`
}

type Host struct {
	OperatingSystem        string        `json:"operating_system"`
	Architecture           string        `json:"architecture"`
	ExecutionMode          ExecutionMode `json:"execution_mode"`
	SystemMemoryBytes      *uint64       `json:"system_memory_bytes,omitempty"`
	AcceleratorMemoryBytes *uint64       `json:"accelerator_memory_bytes,omitempty"`
	FreeStorageBytes       *uint64       `json:"free_storage_bytes,omitempty"`
	OllamaVersion          string        `json:"ollama_version,omitempty"`
}

type LicensePolicy struct {
	RequireCommercialUse      bool     `json:"require_commercial_use"`
	AllowedIdentifiers        []string `json:"allowed_identifiers,omitempty"`
	AllowRestrictedCommercial bool     `json:"allow_restricted_commercial"`
}

type RecommendOptions struct {
	License             LicensePolicy `json:"license"`
	IncludeDeprecated   bool          `json:"include_deprecated"`
	IncludeExperimental bool          `json:"include_experimental"`
}

type Selection struct {
	SchemaVersion  string                `json:"schema_version"`
	CatalogVersion string                `json:"catalog_version"`
	Primary        *CandidateEvaluation  `json:"primary"`
	Fallback       *CandidateEvaluation  `json:"fallback"`
	Candidates     []CandidateEvaluation `json:"candidates"`
	Notices        []Explanation         `json:"notices"`
}

type CandidateEvaluation struct {
	CatalogID       string            `json:"catalog_id"`
	OllamaName      string            `json:"ollama_name"`
	Status          Status            `json:"status"`
	Eligible        bool              `json:"eligible"`
	Score           int               `json:"score"`
	ScoreComponents []ScoreComponent  `json:"score_components"`
	Memory          MemoryAssessment  `json:"memory"`
	Storage         StorageAssessment `json:"storage"`
	Offload         OffloadAssessment `json:"offload"`
	Disqualifiers   []Explanation     `json:"disqualifiers"`
	Unknowns        []Explanation     `json:"unknowns"`
}

type ScoreComponent struct {
	Name   string `json:"name"`
	Points int    `json:"points"`
	Detail string `json:"detail"`
}

type Explanation struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type MemoryAssessment struct {
	Accounting                 string  `json:"accounting"`
	WorkingSetBytes            uint64  `json:"working_set_bytes"`
	HeadroomBytes              uint64  `json:"headroom_bytes"`
	RequiredSystemMemoryBytes  uint64  `json:"required_system_memory_bytes"`
	AvailableSystemMemoryBytes *uint64 `json:"available_system_memory_bytes,omitempty"`
	RequiredAcceleratorBytes   *uint64 `json:"required_accelerator_memory_bytes,omitempty"`
	AvailableAcceleratorBytes  *uint64 `json:"available_accelerator_memory_bytes,omitempty"`
}

type StorageAssessment struct {
	DownloadMaximumBytes uint64  `json:"download_maximum_bytes"`
	HeadroomBytes        uint64  `json:"headroom_bytes"`
	RequiredFreeBytes    uint64  `json:"required_free_bytes"`
	AvailableFreeBytes   *uint64 `json:"available_free_bytes,omitempty"`
}

type OffloadAssessment struct {
	Expected     Offload `json:"expected"`
	Practicality string  `json:"practicality"`
	Detail       string  `json:"detail"`
}
