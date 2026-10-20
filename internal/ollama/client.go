package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultLocalAPIURL       = "http://127.0.0.1:11434"
	DefaultAPIResponseLimit  = int64(4 << 20)
	DefaultAPIRequestTimeout = 2 * time.Minute
)

type ErrorKind string

const (
	ErrorInvalidInput  ErrorKind = "invalid_input"
	ErrorRequest       ErrorKind = "request"
	ErrorStatus        ErrorKind = "status"
	ErrorResponse      ErrorKind = "response"
	ErrorLimit         ErrorKind = "limit"
	ErrorCompatibility ErrorKind = "compatibility"
	ErrorCapacity      ErrorKind = "capacity"
	ErrorCommand       ErrorKind = "command"
	ErrorOwnership     ErrorKind = "ownership"
	ErrorConsent       ErrorKind = "consent"
)

type Error struct {
	Kind       ErrorKind
	Operation  string
	StatusCode int
	Err        error
}

func (err *Error) Error() string {
	if err == nil {
		return "ollama operation failed"
	}
	if err.StatusCode != 0 {
		return fmt.Sprintf("ollama %s failed with HTTP status %d", err.Operation, err.StatusCode)
	}
	if err.Err != nil {
		return fmt.Sprintf("ollama %s failed: %v", err.Operation, err.Err)
	}
	return fmt.Sprintf("ollama %s failed", err.Operation)
}

func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

type RuntimeStatus struct {
	Version   string
	Available bool
}

type VersionResponse struct {
	Version string `json:"version"`
}

type ModelDetails struct {
	Format            string   `json:"format,omitempty"`
	Family            string   `json:"family,omitempty"`
	Families          []string `json:"families,omitempty"`
	ParameterSize     string   `json:"parameter_size,omitempty"`
	QuantizationLevel string   `json:"quantization_level,omitempty"`
	ContextLength     uint64   `json:"context_length,omitempty"`
}

type ModelSummary struct {
	Name       string       `json:"name"`
	Model      string       `json:"model,omitempty"`
	ModifiedAt time.Time    `json:"modified_at,omitempty"`
	Size       uint64       `json:"size,omitempty"`
	Digest     string       `json:"digest,omitempty"`
	Details    ModelDetails `json:"details,omitempty"`
}

type TagsResponse struct {
	Models []ModelSummary `json:"models"`
}

type ShowRequest struct {
	Model string `json:"model"`
}

type ShowResponse struct {
	License      string         `json:"license,omitempty"`
	Modelfile    string         `json:"modelfile,omitempty"`
	Parameters   string         `json:"parameters,omitempty"`
	Template     string         `json:"template,omitempty"`
	Details      ModelDetails   `json:"details,omitempty"`
	ModelInfo    map[string]any `json:"model_info,omitempty"`
	Capabilities []string       `json:"capabilities,omitempty"`
	RemoteModel  string         `json:"remote_model,omitempty"`
	RemoteHost   string         `json:"remote_host,omitempty"`
}

type ChatMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Tools    []Tool        `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
}

type ChatResponse struct {
	Model      string      `json:"model,omitempty"`
	CreatedAt  time.Time   `json:"created_at,omitempty"`
	Message    ChatMessage `json:"message"`
	Done       bool        `json:"done,omitempty"`
	DoneReason string      `json:"done_reason,omitempty"`
}

type RunningModel struct {
	Name          string       `json:"name"`
	Model         string       `json:"model,omitempty"`
	Size          uint64       `json:"size,omitempty"`
	Digest        string       `json:"digest,omitempty"`
	Details       ModelDetails `json:"details,omitempty"`
	ExpiresAt     time.Time    `json:"expires_at,omitempty"`
	SizeVRAM      uint64       `json:"size_vram,omitempty"`
	ContextLength uint64       `json:"context_length,omitempty"`
	Processor     string       `json:"processor,omitempty"`
}

type PSResponse struct {
	Models []RunningModel `json:"models"`
}

type RuntimeAPI interface {
	Status(context.Context) (RuntimeStatus, error)
	ListModels(context.Context) ([]string, error)
	Version(context.Context) (VersionResponse, error)
	Tags(context.Context) (TagsResponse, error)
	Show(context.Context, ShowRequest) (ShowResponse, error)
	Chat(context.Context, ChatRequest) (ChatResponse, error)
	PS(context.Context) (PSResponse, error)
}

type Client interface {
	RuntimeAPI
}

type LocalAPIClient struct {
	baseURL          *url.URL
	client           *http.Client
	maxResponseBytes int64
}

func NewLocalAPIClient(baseURL string, client *http.Client) (*LocalAPIClient, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultLocalAPIURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return nil, &Error{Kind: ErrorInvalidInput, Operation: "configure local API", Err: errors.New("invalid local API URL")}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, &Error{Kind: ErrorInvalidInput, Operation: "configure local API", Err: errors.New("local API URL must use HTTP or HTTPS")}
	}
	host := parsed.Hostname()
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, &Error{Kind: ErrorInvalidInput, Operation: "configure local API", Err: errors.New("local API URL must use a loopback host")}
		}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if client == nil {
		client = &http.Client{Timeout: DefaultAPIRequestTimeout}
	} else {
		cloned := *client
		client = &cloned
		if client.Timeout <= 0 {
			client.Timeout = DefaultAPIRequestTimeout
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &LocalAPIClient{
		baseURL:          parsed,
		client:           client,
		maxResponseBytes: DefaultAPIResponseLimit,
	}, nil
}

func (client *LocalAPIClient) WithResponseLimit(limit int64) *LocalAPIClient {
	copyClient := *client
	if limit > 0 {
		copyClient.maxResponseBytes = limit
	}
	return &copyClient
}

func (client *LocalAPIClient) Status(ctx context.Context) (RuntimeStatus, error) {
	version, err := client.Version(ctx)
	if err != nil {
		return RuntimeStatus{}, err
	}
	return RuntimeStatus{Version: version.Version, Available: true}, nil
}

func (client *LocalAPIClient) ListModels(ctx context.Context) ([]string, error) {
	tags, err := client.Tags(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(tags.Models))
	for _, model := range tags.Models {
		name := model.Name
		if name == "" {
			name = model.Model
		}
		if name != "" {
			models = append(models, name)
		}
	}
	return models, nil
}

func (client *LocalAPIClient) Version(ctx context.Context) (VersionResponse, error) {
	var response VersionResponse
	if err := client.doJSON(ctx, http.MethodGet, "/api/version", nil, &response); err != nil {
		return VersionResponse{}, err
	}
	if strings.TrimSpace(response.Version) == "" {
		return VersionResponse{}, &Error{Kind: ErrorResponse, Operation: "read version", Err: errors.New("version response was empty")}
	}
	return response, nil
}

func (client *LocalAPIClient) Tags(ctx context.Context) (TagsResponse, error) {
	var response TagsResponse
	if err := client.doJSON(ctx, http.MethodGet, "/api/tags", nil, &response); err != nil {
		return TagsResponse{}, err
	}
	if response.Models == nil {
		response.Models = []ModelSummary{}
	}
	return response, nil
}

func (client *LocalAPIClient) Show(ctx context.Context, request ShowRequest) (ShowResponse, error) {
	if err := validateModelReference(request.Model); err != nil {
		return ShowResponse{}, err
	}
	var response ShowResponse
	if err := client.doJSON(ctx, http.MethodPost, "/api/show", request, &response); err != nil {
		return ShowResponse{}, err
	}
	if response.ModelInfo == nil {
		response.ModelInfo = map[string]any{}
	}
	if response.Capabilities == nil {
		response.Capabilities = []string{}
	}
	return response, nil
}

func (client *LocalAPIClient) Chat(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	if err := validateModelReference(request.Model); err != nil {
		return ChatResponse{}, err
	}
	if len(request.Messages) == 0 {
		return ChatResponse{}, &Error{Kind: ErrorInvalidInput, Operation: "chat", Err: errors.New("at least one message is required")}
	}
	request.Stream = false
	var response ChatResponse
	if err := client.doJSON(ctx, http.MethodPost, "/api/chat", request, &response); err != nil {
		return ChatResponse{}, err
	}
	return response, nil
}

func (client *LocalAPIClient) PS(ctx context.Context) (PSResponse, error) {
	var response PSResponse
	if err := client.doJSON(ctx, http.MethodGet, "/api/ps", nil, &response); err != nil {
		return PSResponse{}, err
	}
	if response.Models == nil {
		response.Models = []RunningModel{}
	}
	return response, nil
}

func (client *LocalAPIClient) doJSON(ctx context.Context, method, endpoint string, input, output any) error {
	if client == nil || client.baseURL == nil || client.client == nil {
		return &Error{Kind: ErrorInvalidInput, Operation: endpoint, Err: errors.New("local API client is not configured")}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return &Error{Kind: ErrorInvalidInput, Operation: endpoint, Err: err}
		}
		body = bytes.NewReader(encoded)
	}
	target := *client.baseURL
	target.Path = strings.TrimRight(target.Path, "/") + endpoint
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return &Error{Kind: ErrorRequest, Operation: endpoint, Err: err}
	}
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return &Error{Kind: ErrorRequest, Operation: endpoint, Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return &Error{Kind: ErrorStatus, Operation: endpoint, StatusCode: response.StatusCode}
	}
	limit := client.maxResponseBytes
	if limit <= 0 {
		limit = DefaultAPIResponseLimit
	}
	limited := io.LimitReader(response.Body, limit+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return &Error{Kind: ErrorResponse, Operation: endpoint, Err: err}
	}
	if int64(len(data)) > limit {
		return &Error{Kind: ErrorLimit, Operation: endpoint, Err: fmt.Errorf("JSON response exceeds %d bytes", limit)}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(output); err != nil {
		return &Error{Kind: ErrorResponse, Operation: endpoint, Err: fmt.Errorf("decode bounded JSON: %w", err)}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return &Error{Kind: ErrorResponse, Operation: endpoint, Err: err}
	}
	return nil
}
