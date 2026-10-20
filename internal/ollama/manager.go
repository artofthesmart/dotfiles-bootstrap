package ollama

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/catalog"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
)

const (
	RequiredContextTokens = uint64(65536)
	DefaultDiskHeadroom   = uint64(2 << 30)
)

var aliasPattern = regexp.MustCompile(`^local-ai-[a-z0-9][a-z0-9._-]{0,79}:[a-z0-9][a-z0-9._-]{0,39}$`)

type SourceKind string

const (
	SourceRegistry SourceKind = "registry"
	SourceGGUF     SourceKind = "gguf"
)

type SourceSpec struct {
	Kind          SourceKind
	Reference     string
	GGUFPath      string
	ImportedName  string
	ExpectedBytes uint64
}

type InstallRequest struct {
	Source           SourceSpec
	Alias            string
	DataDirectory    string
	FreeStorageBytes *uint64
}

type SourceInspection struct {
	ModelName     string
	Bytes         uint64
	ContextTokens uint64
	AlreadyLocal  bool
	LocalMetadata ShowResponse
}

type RuntimeVerification struct {
	Alias          string
	ContextTokens  uint64
	Processor      string
	OffloadPercent int
	ToolName       string
	StructuredTool bool
}

type Manager struct {
	API              RuntimeAPI
	Registry         RegistryAPI
	Runner           platform.Runner
	FileSystem       platform.FileSystem
	Processes        platform.ProcessManager
	Services         platform.ServiceController
	OllamaExecutable string
	ServiceName      string
	Now              func() time.Time
	Sleep            func(context.Context, time.Duration) error
}

func (manager Manager) now() time.Time {
	if manager.Now != nil {
		return manager.Now().UTC()
	}
	return time.Now().UTC()
}

func (manager Manager) sleep(ctx context.Context, duration time.Duration) error {
	if manager.Sleep != nil {
		return manager.Sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (manager Manager) InspectSource(ctx context.Context, request InstallRequest) (SourceInspection, error) {
	if err := validateInstallRequest(request); err != nil {
		return SourceInspection{}, err
	}
	localName := request.Source.Reference
	if request.Source.Kind == SourceGGUF {
		localName = request.Source.ImportedName
	}
	local, found, err := manager.findLocalModel(ctx, localName)
	if err != nil {
		return SourceInspection{}, err
	}
	if found {
		if request.Source.ExpectedBytes > 0 && local.Size != request.Source.ExpectedBytes {
			return SourceInspection{}, &Error{Kind: ErrorCompatibility, Operation: "verify installed model size", Err: errors.New("installed exact tag size does not match the expected size")}
		}
		metadata, err := manager.API.Show(ctx, ShowRequest{Model: localName})
		if err != nil {
			return SourceInspection{}, err
		}
		contextTokens, err := validateLocalMetadata(metadata)
		if err != nil {
			return SourceInspection{}, err
		}
		return SourceInspection{
			ModelName:     localName,
			Bytes:         local.Size,
			ContextTokens: contextTokens,
			AlreadyLocal:  true,
			LocalMetadata: metadata,
		}, nil
	}

	var inspection SourceInspection
	inspection.ModelName = localName
	switch request.Source.Kind {
	case SourceRegistry:
		if manager.Registry == nil {
			return SourceInspection{}, &Error{Kind: ErrorInvalidInput, Operation: "inspect registry model", Err: errors.New("registry client is not configured")}
		}
		info, err := manager.Registry.Inspect(ctx, request.Source.Reference)
		if err != nil {
			return SourceInspection{}, err
		}
		inspection.Bytes = info.TotalBytes
		inspection.ContextTokens = info.ContextTokens
	case SourceGGUF:
		if manager.FileSystem == nil {
			return SourceInspection{}, &Error{Kind: ErrorInvalidInput, Operation: "inspect GGUF", Err: errors.New("filesystem is not configured")}
		}
		fileInfo, err := manager.FileSystem.Stat(request.Source.GGUFPath)
		if err != nil {
			return SourceInspection{}, &Error{Kind: ErrorInvalidInput, Operation: "stat GGUF", Err: err}
		}
		if fileInfo.IsDir() || fileInfo.Size() <= 0 {
			return SourceInspection{}, &Error{Kind: ErrorInvalidInput, Operation: "inspect GGUF", Err: errors.New("GGUF source must be a non-empty regular file")}
		}
		inspection.Bytes = uint64(fileInfo.Size())
		reader, err := manager.FileSystem.Open(request.Source.GGUFPath)
		if err != nil {
			return SourceInspection{}, &Error{Kind: ErrorInvalidInput, Operation: "open GGUF", Err: err}
		}
		defer reader.Close()
		contextTokens, err := catalog.ParseGGUFContext(io.LimitReader(reader, DefaultGGUFMetadataLimit))
		if err != nil {
			return SourceInspection{}, &Error{Kind: ErrorCompatibility, Operation: "parse local GGUF metadata", Err: err}
		}
		inspection.ContextTokens = contextTokens
	default:
		return SourceInspection{}, &Error{Kind: ErrorInvalidInput, Operation: "inspect model source", Err: errors.New("unsupported source kind")}
	}
	if request.Source.ExpectedBytes > 0 && request.Source.ExpectedBytes != inspection.Bytes {
		return SourceInspection{}, &Error{Kind: ErrorCompatibility, Operation: "verify model size", Err: errors.New("source size does not match expected exact size")}
	}
	if inspection.ContextTokens < RequiredContextTokens {
		return SourceInspection{}, &Error{Kind: ErrorCompatibility, Operation: "verify source context", Err: fmt.Errorf("source advertises %d context tokens; %d are required", inspection.ContextTokens, RequiredContextTokens)}
	}
	required, err := requiredDiskBytes(inspection.Bytes)
	if err != nil {
		return SourceInspection{}, err
	}
	if request.FreeStorageBytes == nil {
		return SourceInspection{}, &Error{Kind: ErrorCapacity, Operation: "check free storage", Err: errors.New("free model-volume storage is unknown")}
	}
	if *request.FreeStorageBytes < required {
		return SourceInspection{}, &Error{Kind: ErrorCapacity, Operation: "check free storage", Err: fmt.Errorf("%d bytes available; %d required", *request.FreeStorageBytes, required)}
	}
	return inspection, nil
}

func (manager Manager) Acquire(ctx context.Context, request InstallRequest) (SourceInspection, bool, error) {
	inspection, err := manager.InspectSource(ctx, request)
	if err != nil {
		return SourceInspection{}, false, err
	}
	if inspection.AlreadyLocal {
		return inspection, false, nil
	}
	switch request.Source.Kind {
	case SourceRegistry:
		if err := manager.run(ctx, "pull exact model tag", "pull", request.Source.Reference); err != nil {
			return SourceInspection{}, false, err
		}
	case SourceGGUF:
		modelfile, err := localGGUFModelfile(request.Source.GGUFPath)
		if err != nil {
			return SourceInspection{}, false, err
		}
		if err := manager.createFromModelfile(ctx, request.DataDirectory, "import", request.Source.ImportedName, modelfile); err != nil {
			return SourceInspection{}, false, err
		}
	default:
		return SourceInspection{}, false, &Error{Kind: ErrorInvalidInput, Operation: "acquire model", Err: errors.New("unsupported source kind")}
	}
	metadata, err := manager.API.Show(ctx, ShowRequest{Model: inspection.ModelName})
	if err != nil {
		return SourceInspection{}, true, err
	}
	contextTokens, err := validateLocalMetadata(metadata)
	if err != nil {
		return SourceInspection{}, true, err
	}
	inspection.AlreadyLocal = true
	inspection.LocalMetadata = metadata
	inspection.ContextTokens = contextTokens
	return inspection, true, nil
}

func (manager Manager) CreateAlias(ctx context.Context, source, alias, dataDirectory string) (bool, error) {
	if err := validateModelReference(source); err != nil {
		return false, err
	}
	if !aliasPattern.MatchString(alias) {
		return false, &Error{Kind: ErrorInvalidInput, Operation: "create alias", Err: errors.New("alias must be an exact safe local-ai tag")}
	}
	_, found, err := manager.findLocalModel(ctx, alias)
	if err != nil {
		return false, err
	}
	if found {
		metadata, err := manager.API.Show(ctx, ShowRequest{Model: alias})
		if err != nil {
			return false, err
		}
		if _, err := validateLocalMetadata(metadata); err != nil {
			return false, err
		}
		if !aliasModelfileMatches(metadata.Modelfile, source) {
			return false, &Error{Kind: ErrorCompatibility, Operation: "reuse existing alias", Err: errors.New("existing alias does not match the exact source and 65,536-token contract")}
		}
		return false, nil
	}
	modelfile := []byte("FROM " + source + "\nPARAMETER num_ctx " + strconv.FormatUint(RequiredContextTokens, 10) + "\n")
	if err := manager.createFromModelfile(ctx, dataDirectory, "alias", alias, modelfile); err != nil {
		return false, err
	}
	return true, nil
}

func (manager Manager) VerifyRuntime(ctx context.Context, alias string) (RuntimeVerification, error) {
	if !aliasPattern.MatchString(alias) {
		return RuntimeVerification{}, &Error{Kind: ErrorInvalidInput, Operation: "verify runtime", Err: errors.New("invalid exact alias")}
	}
	show, err := manager.API.Show(ctx, ShowRequest{Model: alias})
	if err != nil {
		return RuntimeVerification{}, err
	}
	if _, err := validateLocalMetadata(show); err != nil {
		return RuntimeVerification{}, err
	}

	tool := temperatureTool()
	chat, err := manager.API.Chat(ctx, ChatRequest{
		Model: alias,
		Messages: []ChatMessage{{
			Role:    "user",
			Content: "What is the weather in New York in Celsius? Use the provided tool.",
		}},
		Tools:  []Tool{tool},
		Stream: false,
	})
	if err != nil {
		return RuntimeVerification{}, err
	}
	if !chat.Done {
		return RuntimeVerification{}, &Error{Kind: ErrorResponse, Operation: "verify structured tool call", Err: errors.New("chat response did not report completion")}
	}
	if err := validateStructuredTemperatureCall(chat.Message.ToolCalls); err != nil {
		return RuntimeVerification{}, &Error{Kind: ErrorCompatibility, Operation: "verify structured tool call", Err: err}
	}
	running, err := manager.API.PS(ctx)
	if err != nil {
		return RuntimeVerification{}, err
	}
	model, found := exactRunningModel(running.Models, alias)
	if !found {
		return RuntimeVerification{}, &Error{Kind: ErrorCompatibility, Operation: "verify allocated runtime", Err: errors.New("exact alias is not present in /api/ps")}
	}
	if model.ContextLength < RequiredContextTokens {
		return RuntimeVerification{}, &Error{Kind: ErrorCompatibility, Operation: "verify allocated context", Err: fmt.Errorf("runtime allocated %d context tokens; %d are required", model.ContextLength, RequiredContextTokens)}
	}
	offload := offloadPercent(model)
	return RuntimeVerification{
		Alias:          alias,
		ContextTokens:  model.ContextLength,
		Processor:      normalizedProcessor(model),
		OffloadPercent: offload,
		ToolName:       tool.Function.Name,
		StructuredTool: true,
	}, nil
}

func (manager Manager) RemoveModel(ctx context.Context, request RemovalRequest) error {
	if err := validateRemoval(request); err != nil {
		return err
	}
	if !request.Consent {
		return &Error{Kind: ErrorConsent, Operation: "remove model", Err: errors.New("explicit destructive consent is required")}
	}
	if request.RemoveSource {
		for _, asset := range request.Assets {
			if asset.AssetType == state.AssetOllamaAlias && asset.Locator.Value != request.Alias && containsString(asset.Dependencies, request.SourceAssetID) {
				return &Error{Kind: ErrorOwnership, Operation: "remove source model", Err: errors.New("a retained alias depends on the source model")}
			}
		}
	}
	if request.Alias != "" {
		if err := manager.run(ctx, "remove exact alias", "rm", request.Alias); err != nil {
			return err
		}
		if _, found, err := manager.findLocalModel(ctx, request.Alias); err != nil {
			return err
		} else if found {
			return &Error{Kind: ErrorCommand, Operation: "verify alias removal", Err: errors.New("alias remains installed")}
		}
	}
	if request.RemoveSource {
		if err := manager.run(ctx, "remove exact source model", "rm", request.Source); err != nil {
			return err
		}
		if _, found, err := manager.findLocalModel(ctx, request.Source); err != nil {
			return err
		} else if found {
			return &Error{Kind: ErrorCommand, Operation: "verify source removal", Err: errors.New("source model remains installed")}
		}
	}
	return nil
}

type RemovalRequest struct {
	Alias         string
	Source        string
	AliasAssetID  string
	SourceAssetID string
	Assets        []state.ManagedAsset
	RemoveSource  bool
	Consent       bool
}

func validateRemoval(request RemovalRequest) error {
	if request.Alias == "" && !request.RemoveSource {
		return &Error{Kind: ErrorInvalidInput, Operation: "remove model", Err: errors.New("no model removal was requested")}
	}
	if request.Alias != "" && !aliasPattern.MatchString(request.Alias) {
		return &Error{Kind: ErrorInvalidInput, Operation: "remove model", Err: errors.New("invalid alias")}
	}
	if request.RemoveSource {
		if err := validateModelReference(request.Source); err != nil {
			return err
		}
	}
	owned := map[string]bool{}
	for _, asset := range request.Assets {
		if asset.Ownership == state.OwnershipManagerOwned {
			owned[asset.AssetID] = true
		}
	}
	if request.Alias != "" && (!owned[request.AliasAssetID] || request.AliasAssetID == "") {
		return &Error{Kind: ErrorOwnership, Operation: "remove alias", Err: errors.New("manager ownership of the exact alias is not proven")}
	}
	if request.RemoveSource && (!owned[request.SourceAssetID] || request.SourceAssetID == "") {
		return &Error{Kind: ErrorOwnership, Operation: "remove source model", Err: errors.New("manager ownership of the exact source is not proven")}
	}
	return nil
}

func (manager Manager) findLocalModel(ctx context.Context, exact string) (ModelSummary, bool, error) {
	if manager.API == nil {
		return ModelSummary{}, false, &Error{Kind: ErrorInvalidInput, Operation: "list local models", Err: errors.New("runtime API is not configured")}
	}
	tags, err := manager.API.Tags(ctx)
	if err != nil {
		return ModelSummary{}, false, err
	}
	for _, model := range tags.Models {
		name := model.Name
		if name == "" {
			name = model.Model
		}
		if name == exact {
			return model, true, nil
		}
	}
	return ModelSummary{}, false, nil
}

func (manager Manager) createFromModelfile(ctx context.Context, dataDirectory, kind, model string, contents []byte) error {
	if manager.FileSystem == nil {
		return &Error{Kind: ErrorInvalidInput, Operation: "write Modelfile", Err: errors.New("filesystem is not configured")}
	}
	if strings.TrimSpace(dataDirectory) == "" {
		return &Error{Kind: ErrorInvalidInput, Operation: "write Modelfile", Err: errors.New("manager data directory is required")}
	}
	if err := manager.FileSystem.MkdirAll(dataDirectory, 0o700); err != nil {
		return &Error{Kind: ErrorCommand, Operation: "create manager data directory", Err: err}
	}
	name := safeFileToken(model)
	modelfilePath := filepath.Join(dataDirectory, kind+"-"+name+".Modelfile")
	if err := manager.FileSystem.WriteFile(modelfilePath, contents, 0o600); err != nil {
		return &Error{Kind: ErrorCommand, Operation: "write bounded Modelfile", Err: err}
	}
	defer manager.FileSystem.Remove(modelfilePath)
	if err := manager.run(ctx, "create exact local model", "create", model, "-f", modelfilePath); err != nil {
		return err
	}
	return nil
}

func (manager Manager) run(ctx context.Context, operation string, arguments ...string) error {
	if manager.Runner == nil {
		return &Error{Kind: ErrorInvalidInput, Operation: operation, Err: errors.New("command runner is not configured")}
	}
	executable := manager.OllamaExecutable
	if executable == "" {
		executable = "ollama"
	}
	result, err := manager.Runner.Run(ctx, platform.Command{
		Name:        executable,
		Args:        append([]string(nil), arguments...),
		Timeout:     2 * time.Hour,
		OutputLimit: 1 << 20,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return &Error{Kind: ErrorCommand, Operation: operation, Err: err}
	}
	if result.ExitCode != 0 {
		return &Error{Kind: ErrorCommand, Operation: operation, Err: fmt.Errorf("command exited with code %d", result.ExitCode)}
	}
	if result.StdoutTruncated || result.StderrTruncated {
		return &Error{Kind: ErrorLimit, Operation: operation, Err: errors.New("command diagnostic output exceeded its safety limit")}
	}
	return nil
}

func validateInstallRequest(request InstallRequest) error {
	if !aliasPattern.MatchString(request.Alias) {
		return &Error{Kind: ErrorInvalidInput, Operation: "validate install", Err: errors.New("alias must be an exact safe local-ai tag")}
	}
	switch request.Source.Kind {
	case SourceRegistry:
		if err := validateModelReference(request.Source.Reference); err != nil {
			return err
		}
	case SourceGGUF:
		if strings.TrimSpace(request.Source.GGUFPath) == "" || strings.ContainsAny(request.Source.GGUFPath, "\r\n\x00") {
			return &Error{Kind: ErrorInvalidInput, Operation: "validate install", Err: errors.New("GGUF path is invalid")}
		}
		if err := validateModelReference(request.Source.ImportedName); err != nil {
			return err
		}
	default:
		return &Error{Kind: ErrorInvalidInput, Operation: "validate install", Err: errors.New("source kind is required")}
	}
	return nil
}

func validateLocalMetadata(response ShowResponse) (uint64, error) {
	if strings.TrimSpace(response.RemoteHost) != "" || strings.TrimSpace(response.RemoteModel) != "" {
		return 0, &Error{Kind: ErrorCompatibility, Operation: "validate local model metadata", Err: errors.New("model is remote or cloud-routed")}
	}
	for _, capability := range response.Capabilities {
		lower := strings.ToLower(strings.TrimSpace(capability))
		if lower == "cloud" || lower == "remote" {
			return 0, &Error{Kind: ErrorCompatibility, Operation: "validate local model metadata", Err: errors.New("model is remote or cloud-routed")}
		}
	}
	contextTokens := response.Details.ContextLength
	for key, value := range response.ModelInfo {
		if !strings.HasSuffix(strings.ToLower(key), ".context_length") {
			continue
		}
		parsed, ok := positiveUint64(value)
		if ok && parsed > contextTokens {
			contextTokens = parsed
		}
	}
	if contextTokens == 0 {
		return 0, &Error{Kind: ErrorCompatibility, Operation: "validate local model metadata", Err: errors.New("local context metadata is missing")}
	}
	if contextTokens < RequiredContextTokens {
		return 0, &Error{Kind: ErrorCompatibility, Operation: "validate local model metadata", Err: fmt.Errorf("local metadata reports %d context tokens; %d are required", contextTokens, RequiredContextTokens)}
	}
	return contextTokens, nil
}

func positiveUint64(value any) (uint64, bool) {
	switch typed := value.(type) {
	case float64:
		if typed <= 0 || typed > math.MaxUint64 || math.Trunc(typed) != typed {
			return 0, false
		}
		return uint64(typed), true
	case int:
		return uint64(typed), typed > 0
	case int64:
		return uint64(typed), typed > 0
	case uint64:
		return typed, typed > 0
	default:
		return 0, false
	}
}

func requiredDiskBytes(modelBytes uint64) (uint64, error) {
	headroom := DefaultDiskHeadroom
	if tenPercent := modelBytes / 10; tenPercent > headroom {
		headroom = tenPercent
	}
	if modelBytes > math.MaxUint64-headroom {
		return 0, &Error{Kind: ErrorCapacity, Operation: "calculate disk requirement", Err: errors.New("disk requirement overflow")}
	}
	return modelBytes + headroom, nil
}

func localGGUFModelfile(filePath string) ([]byte, error) {
	if strings.TrimSpace(filePath) == "" || strings.ContainsAny(filePath, "\r\n\x00") {
		return nil, &Error{Kind: ErrorInvalidInput, Operation: "create GGUF Modelfile", Err: errors.New("GGUF path is invalid")}
	}
	return []byte("FROM " + strconv.Quote(filePath) + "\n"), nil
}

func aliasModelfileMatches(modelfile, source string) bool {
	lines := strings.Split(strings.ReplaceAll(modelfile, "\r\n", "\n"), "\n")
	hasSource := false
	hasContext := false
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && strings.EqualFold(fields[0], "FROM") {
			reference := strings.Trim(fields[1], `"`)
			hasSource = reference == source
		}
		if len(fields) == 3 && strings.EqualFold(fields[0], "PARAMETER") &&
			strings.EqualFold(fields[1], "num_ctx") && fields[2] == "65536" {
			hasContext = true
		}
	}
	return hasSource && hasContext
}

func temperatureTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "get_current_temperature",
			Description: "Get the current temperature for a location.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"location": map[string]any{"type": "string"},
					"format": map[string]any{
						"type": "string",
						"enum": []string{"celsius", "fahrenheit"},
					},
				},
				"required":             []string{"location", "format"},
				"additionalProperties": false,
			},
		},
	}
}

func validateStructuredTemperatureCall(calls []ToolCall) error {
	if len(calls) == 0 {
		return errors.New("response did not contain a structured tool call")
	}
	for _, call := range calls {
		if call.Function.Name != "get_current_temperature" {
			continue
		}
		location, locationOK := call.Function.Arguments["location"].(string)
		format, formatOK := call.Function.Arguments["format"].(string)
		if !locationOK || strings.TrimSpace(location) == "" || !formatOK || (format != "celsius" && format != "fahrenheit") {
			return errors.New("structured tool arguments did not match the declared schema")
		}
		for key := range call.Function.Arguments {
			if key != "location" && key != "format" {
				return errors.New("structured tool arguments contained an undeclared field")
			}
		}
		return nil
	}
	return errors.New("response did not call the expected structured tool")
}

func exactRunningModel(models []RunningModel, alias string) (RunningModel, bool) {
	for _, model := range models {
		name := model.Name
		if name == "" {
			name = model.Model
		}
		if name == alias {
			return model, true
		}
	}
	return RunningModel{}, false
}

func normalizedProcessor(model RunningModel) string {
	processor := strings.TrimSpace(model.Processor)
	if processor != "" {
		return processor
	}
	if model.SizeVRAM == 0 {
		return "CPU"
	}
	if model.Size > 0 && model.SizeVRAM >= model.Size {
		return "100% GPU"
	}
	return "CPU/GPU"
}

func offloadPercent(model RunningModel) int {
	lower := strings.ToLower(model.Processor)
	if index := strings.Index(lower, "%"); index > 0 {
		start := index - 1
		for start >= 0 && lower[start] >= '0' && lower[start] <= '9' {
			start--
		}
		value, err := strconv.Atoi(lower[start+1 : index])
		if err == nil && value >= 0 && value <= 100 {
			return value
		}
	}
	if model.Size == 0 {
		return -1
	}
	value := int(math.Round(float64(model.SizeVRAM) * 100 / float64(model.Size)))
	if value > 100 {
		value = 100
	}
	return value
}

func safeFileToken(value string) string {
	var builder strings.Builder
	for _, char := range strings.ToLower(value) {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			builder.WriteRune(char)
		default:
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sortedModelNames(models []ModelSummary) []string {
	names := make([]string, 0, len(models))
	for _, model := range models {
		name := model.Name
		if name == "" {
			name = model.Model
		}
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
