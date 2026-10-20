package ollama

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"testing"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

type fakeRuntimeAPI struct {
	mu        sync.Mutex
	version   VersionResponse
	statusErr error
	models    map[string]ShowResponse
	sizes     map[string]uint64
	chat      ChatResponse
	chatErr   error
	ps        PSResponse
	psErr     error
	statuses  []error
	chatCalls []ChatRequest
}

func (api *fakeRuntimeAPI) Status(context.Context) (RuntimeStatus, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.statuses) > 0 {
		err := api.statuses[0]
		api.statuses = api.statuses[1:]
		if err != nil {
			return RuntimeStatus{}, err
		}
	}
	if api.statusErr != nil {
		return RuntimeStatus{}, api.statusErr
	}
	return RuntimeStatus{Version: api.version.Version, Available: true}, nil
}

func (api *fakeRuntimeAPI) ListModels(ctx context.Context) ([]string, error) {
	tags, err := api.Tags(ctx)
	if err != nil {
		return nil, err
	}
	return sortedModelNames(tags.Models), nil
}

func (api *fakeRuntimeAPI) Version(context.Context) (VersionResponse, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.statusErr != nil {
		return VersionResponse{}, api.statusErr
	}
	return api.version, nil
}

func (api *fakeRuntimeAPI) Tags(context.Context) (TagsResponse, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	models := make([]ModelSummary, 0, len(api.models))
	for name := range api.models {
		models = append(models, ModelSummary{Name: name, Size: api.sizes[name]})
	}
	return TagsResponse{Models: models}, nil
}

func (api *fakeRuntimeAPI) Show(_ context.Context, request ShowRequest) (ShowResponse, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	response, ok := api.models[request.Model]
	if !ok {
		return ShowResponse{}, &Error{Kind: ErrorStatus, Operation: "show", StatusCode: 404}
	}
	return response, nil
}

func (api *fakeRuntimeAPI) Chat(_ context.Context, request ChatRequest) (ChatResponse, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.chatCalls = append(api.chatCalls, request)
	return api.chat, api.chatErr
}

func (api *fakeRuntimeAPI) PS(context.Context) (PSResponse, error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	return api.ps, api.psErr
}

func (api *fakeRuntimeAPI) addModel(name string, response ShowResponse, size uint64) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.models[name] = response
	api.sizes[name] = size
}

type fakeRegistry struct {
	info ManifestInfo
	err  error
}

func (registry fakeRegistry) Inspect(context.Context, string) (ManifestInfo, error) {
	return registry.info, registry.err
}

func (registry fakeRegistry) Manifest(context.Context, string) (RegistryManifest, error) {
	return registry.info.Manifest, registry.err
}

func (registry fakeRegistry) ModelMetadata(context.Context, string, RegistryDescriptor) (uint64, error) {
	return registry.info.ContextTokens, registry.err
}

func TestManagerReusesExactInstalledTagWithoutPull(t *testing.T) {
	api := newCompatibleAPI()
	api.addModel("llama3.1:8b", compatibleShow(131072), 5<<30)
	runner := testkit.NewScriptedRunner()
	free := uint64(10 << 30)
	manager := Manager{API: api, Registry: fakeRegistry{}, Runner: runner}
	request := InstallRequest{
		Source:           SourceSpec{Kind: SourceRegistry, Reference: "llama3.1:8b"},
		Alias:            "local-ai-llama31:latest",
		FreeStorageBytes: &free,
	}
	inspection, created, err := manager.Acquire(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if created || !inspection.AlreadyLocal || len(runner.Calls()) != 0 {
		t.Fatalf("installed exact tag was not reused: inspection=%#v created=%t calls=%v", inspection, created, runner.Calls())
	}
}

func TestManagerPreflightRejectsContextDiskRemoteAndUnknownCapacityBeforeMutation(t *testing.T) {
	cases := []struct {
		name     string
		registry fakeRegistry
		free     *uint64
		wantKind ErrorKind
	}{
		{
			name:     "context",
			registry: fakeRegistry{info: ManifestInfo{TotalBytes: 5 << 30, ContextTokens: 32768}},
			free:     pointerUint64(20 << 30),
			wantKind: ErrorCompatibility,
		},
		{
			name:     "disk",
			registry: fakeRegistry{info: ManifestInfo{TotalBytes: 5 << 30, ContextTokens: 131072}},
			free:     pointerUint64(6 << 30),
			wantKind: ErrorCapacity,
		},
		{
			name:     "unknown-disk",
			registry: fakeRegistry{info: ManifestInfo{TotalBytes: 5 << 30, ContextTokens: 131072}},
			wantKind: ErrorCapacity,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			runner := testkit.NewScriptedRunner()
			manager := Manager{API: newCompatibleAPI(), Registry: test.registry, Runner: runner}
			_, _, err := manager.Acquire(context.Background(), InstallRequest{
				Source:           SourceSpec{Kind: SourceRegistry, Reference: "llama3.1:8b"},
				Alias:            "local-ai-test:latest",
				FreeStorageBytes: test.free,
			})
			var operationError *Error
			if !errors.As(err, &operationError) || operationError.Kind != test.wantKind {
				t.Fatalf("Acquire() error = %#v, want %s", err, test.wantKind)
			}
			if len(runner.Calls()) != 0 {
				t.Fatalf("preflight failure mutated via %v", runner.Calls())
			}
		})
	}

	remote := compatibleShow(131072)
	remote.RemoteHost = "https://cloud.example"
	if _, err := validateLocalMetadata(remote); err == nil {
		t.Fatal("remote model metadata was accepted")
	}
	cloud := compatibleShow(131072)
	cloud.Capabilities = []string{"tools", "cloud"}
	if _, err := validateLocalMetadata(cloud); err == nil {
		t.Fatal("cloud capability was accepted")
	}
}

func TestManagerImportsGGUFWithSpacesAndCreatesSafeAlias(t *testing.T) {
	memory := testkit.NewMemoryFileSystem("/sandbox", nil)
	if err := memory.MkdirAll("/sandbox/models", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := memory.MkdirAll("/sandbox/state", 0o700); err != nil {
		t.Fatal(err)
	}
	ggufPath := "/sandbox/models/model with spaces.gguf"
	gguf := ggufContextFixture(131072)
	if err := memory.WriteFile(ggufPath, gguf, 0o600); err != nil {
		t.Fatal(err)
	}
	api := newCompatibleAPI()
	runner := testkit.NewScriptedRunner(
		testkit.CommandOutcome{Result: platform.CommandResult{ExitCode: 0}},
		testkit.CommandOutcome{Result: platform.CommandResult{ExitCode: 0}},
	)
	manager := Manager{API: api, Runner: runner, FileSystem: memory}
	free := uint64(10 << 30)
	request := InstallRequest{
		Source: SourceSpec{
			Kind:         SourceGGUF,
			GGUFPath:     ggufPath,
			ImportedName: "local-import:model",
		},
		Alias:            "local-ai-import:latest",
		DataDirectory:    "/sandbox/state",
		FreeStorageBytes: &free,
	}
	modelfileBytes, err := localGGUFModelfile(ggufPath)
	if err != nil || string(modelfileBytes) != `FROM "/sandbox/models/model with spaces.gguf"`+"\n" {
		t.Fatalf("local GGUF Modelfile = %q, err %v", modelfileBytes, err)
	}
	inspection, created, err := manager.Acquire(context.Background(), request)
	if err == nil {
		t.Fatal("Acquire() unexpectedly verified a fake API model that the runner did not publish")
	}

	if !created || inspection.ModelName != "" {
		t.Fatalf("unexpected failed acquisition boundary: inspection=%#v created=%t", inspection, created)
	}
	calls := runner.Calls()
	if len(calls) != 1 || calls[0].Name != "ollama" || len(calls[0].Args) != 4 || calls[0].Args[0] != "create" {
		t.Fatalf("GGUF import call = %#v", calls)
	}
	modelfile := calls[0].Args[3]
	data, readErr := memory.ReadFile(modelfile)
	if !errors.Is(readErr, fs.ErrNotExist) {
		t.Fatalf("temporary Modelfile was retained: data=%q err=%v", data, readErr)
	}

	api.addModel("local-import:model", compatibleShow(131072), uint64(len(gguf)))
	created, err = manager.CreateAlias(context.Background(), "local-import:model", request.Alias, request.DataDirectory)
	if err != nil || !created {
		t.Fatalf("CreateAlias() = created %t, err %v", created, err)
	}
	calls = runner.Calls()
	if len(calls) != 2 || calls[1].Args[0] != "create" || calls[1].Args[1] != request.Alias {
		t.Fatalf("alias call = %#v", calls)
	}
	if len(api.chatCalls) != 0 {
		t.Fatal("model creation executed a generated tool")
	}
}

func TestExistingAliasMustMatchExactSourceAndContext(t *testing.T) {
	api := newCompatibleAPI()
	alias := "local-ai-test:latest"
	mismatch := compatibleShow(131072)
	mismatch.Modelfile = "FROM mistral-nemo:12b\nPARAMETER num_ctx 65536\n"
	api.addModel(alias, mismatch, 5<<30)
	runner := testkit.NewScriptedRunner()
	manager := Manager{API: api, Runner: runner}
	if _, err := manager.CreateAlias(context.Background(), "llama3.1:8b", alias, "/state"); err == nil {
		t.Fatal("CreateAlias() reused an alias for a different exact source")
	}
	if len(runner.Calls()) != 0 {
		t.Fatalf("mismatched alias was mutated: %#v", runner.Calls())
	}

	match := compatibleShow(131072)
	match.Modelfile = "FROM llama3.1:8b\nPARAMETER num_ctx 65536\n"
	api.addModel(alias, match, 5<<30)
	created, err := manager.CreateAlias(context.Background(), "llama3.1:8b", alias, "/state")
	if err != nil || created {
		t.Fatalf("matching alias reuse = created %t, err %v", created, err)
	}
}

func TestRuntimeVerificationRequiresStructuredCallAndExactPSAlias(t *testing.T) {
	api := newCompatibleAPI()
	alias := "local-ai-test:latest"
	api.addModel(alias, compatibleShow(131072), 5<<30)
	api.chat = ChatResponse{
		Done: true,
		Message: ChatMessage{
			Role:    "assistant",
			Content: `{"name":"get_current_temperature","arguments":{"location":"New York","format":"celsius"}}`,
		},
	}
	api.ps = PSResponse{Models: []RunningModel{{Name: alias, ContextLength: 65536, Size: 100, SizeVRAM: 100}}}
	manager := Manager{API: api}
	_, err := manager.VerifyRuntime(context.Background(), alias)
	if err == nil || !strings.Contains(err.Error(), "structured tool") {
		t.Fatalf("VerifyRuntime() accepted prose JSON: %v", err)
	}

	api.chat = compatibleToolChat()
	api.ps = PSResponse{Models: []RunningModel{{Name: alias + "-other", ContextLength: 131072, Processor: "100% GPU"}}}
	if _, err := manager.VerifyRuntime(context.Background(), alias); err == nil {
		t.Fatal("VerifyRuntime() accepted a non-exact /api/ps alias")
	}

	api.ps = PSResponse{Models: []RunningModel{{Name: alias, ContextLength: 32768, Processor: "100% GPU"}}}
	if _, err := manager.VerifyRuntime(context.Background(), alias); err == nil {
		t.Fatal("VerifyRuntime() accepted a lowered runtime context")
	}

	api.ps = PSResponse{Models: []RunningModel{{Name: alias, ContextLength: 65536, Processor: "75% GPU"}}}
	verification, err := manager.VerifyRuntime(context.Background(), alias)
	if err != nil {
		t.Fatal(err)
	}
	if verification.OffloadPercent != 75 || verification.Processor != "75% GPU" || !verification.StructuredTool {
		t.Fatalf("verification = %#v", verification)
	}
	if len(api.chatCalls) == 0 {
		t.Fatal("chat verification was not called")
	}
}

func TestRemovalRequiresOwnershipConsentAndNoDependentAliases(t *testing.T) {
	manager := Manager{API: newCompatibleAPI(), Runner: testkit.NewScriptedRunner()}
	request := RemovalRequest{
		Alias:         "local-ai-test:latest",
		AliasAssetID:  "asset-alias",
		Source:        "llama3.1:8b",
		SourceAssetID: "asset-source",
		RemoveSource:  true,
		Assets: []state.ManagedAsset{
			{
				AssetID:      "asset-source",
				AssetType:    state.AssetOllamaSourceModel,
				Ownership:    state.OwnershipManagerOwned,
				Locator:      state.AssetLocator{Value: "llama3.1:8b"},
				Dependencies: []string{},
			},
			{
				AssetID:      "asset-alias",
				AssetType:    state.AssetOllamaAlias,
				Ownership:    state.OwnershipManagerOwned,
				Locator:      state.AssetLocator{Value: "local-ai-test:latest"},
				Dependencies: []string{"asset-source"},
			},
			{
				AssetID:      "asset-other-alias",
				AssetType:    state.AssetOllamaAlias,
				Ownership:    state.OwnershipManagerOwned,
				Locator:      state.AssetLocator{Value: "local-ai-other:latest"},
				Dependencies: []string{"asset-source"},
			},
		},
	}
	if err := manager.RemoveModel(context.Background(), request); err == nil {
		t.Fatal("RemoveModel() accepted missing consent")
	}
	request.Consent = true
	if err := manager.RemoveModel(context.Background(), request); err == nil || !strings.Contains(err.Error(), "retained alias") {
		t.Fatalf("RemoveModel() dependency error = %v", err)
	}
	request.Assets = request.Assets[:2]
	request.AliasAssetID = "asset-unowned"
	if err := manager.RemoveModel(context.Background(), request); err == nil {
		t.Fatal("RemoveModel() accepted unproven ownership")
	}
}

func newCompatibleAPI() *fakeRuntimeAPI {
	return &fakeRuntimeAPI{
		version: VersionResponse{Version: "0.12.7"},
		models:  map[string]ShowResponse{},
		sizes:   map[string]uint64{},
		chat:    compatibleToolChat(),
	}
}

func compatibleShow(contextTokens uint64) ShowResponse {
	return ShowResponse{
		Details:      ModelDetails{ContextLength: contextTokens},
		ModelInfo:    map[string]any{"llama.context_length": float64(contextTokens)},
		Capabilities: []string{"completion", "tools"},
	}
}

func compatibleToolChat() ChatResponse {
	return ChatResponse{
		Done: true,
		Message: ChatMessage{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				Function: ToolCallFunction{
					Name: "get_current_temperature",
					Arguments: map[string]any{
						"location": "New York",
						"format":   "celsius",
					},
				},
			}},
		},
	}
}

func pointerUint64(value uint64) *uint64 {
	return &value
}
