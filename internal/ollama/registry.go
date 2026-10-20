package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/catalog"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

const (
	DefaultRegistryURL       = "https://registry.ollama.ai"
	DefaultManifestLimit     = int64(2 << 20)
	DefaultGGUFMetadataLimit = int64(1 << 20)
	maxRegistryRedirects     = 10
)

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var contentRangePattern = regexp.MustCompile(`^bytes 0-([0-9]+)/([0-9]+|\*)$`)

type RegistryManifest struct {
	SchemaVersion int                  `json:"schemaVersion"`
	MediaType     string               `json:"mediaType"`
	Config        RegistryDescriptor   `json:"config"`
	Layers        []RegistryDescriptor `json:"layers"`
}

type RegistryDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      uint64 `json:"size"`
}

type ManifestInfo struct {
	Reference     string
	Manifest      RegistryManifest
	ModelLayer    RegistryDescriptor
	TotalBytes    uint64
	ContextTokens uint64
}

type RegistryAPI interface {
	Inspect(context.Context, string) (ManifestInfo, error)
	Manifest(context.Context, string) (RegistryManifest, error)
	ModelMetadata(context.Context, string, RegistryDescriptor) (uint64, error)
}

type RegistryClient struct {
	Network        platform.Network
	BaseURL        string
	ManifestLimit  int64
	MetadataLimit  int64
	RequestTimeout time.Duration
}

func NewRegistryClient(network platform.Network) RegistryClient {
	return RegistryClient{
		Network:        network,
		BaseURL:        DefaultRegistryURL,
		ManifestLimit:  DefaultManifestLimit,
		MetadataLimit:  DefaultGGUFMetadataLimit,
		RequestTimeout: 30 * time.Second,
	}
}

func (client RegistryClient) Inspect(ctx context.Context, reference string) (ManifestInfo, error) {
	manifest, err := client.Manifest(ctx, reference)
	if err != nil {
		return ManifestInfo{}, err
	}
	var total uint64
	descriptors := make([]RegistryDescriptor, 0, len(manifest.Layers)+1)
	descriptors = append(descriptors, manifest.Config)
	descriptors = append(descriptors, manifest.Layers...)
	for _, descriptor := range descriptors {
		if descriptor.Size > math.MaxUint64-total {
			return ManifestInfo{}, &Error{Kind: ErrorResponse, Operation: "account registry manifest", Err: errors.New("manifest byte count overflow")}
		}
		total += descriptor.Size
	}
	var modelLayer RegistryDescriptor
	for _, layer := range manifest.Layers {
		if strings.Contains(strings.ToLower(layer.MediaType), "model") {
			if modelLayer.Digest != "" {
				return ManifestInfo{}, &Error{Kind: ErrorResponse, Operation: "select model layer", Err: errors.New("manifest contains multiple model layers")}
			}
			modelLayer = layer
		}
	}
	if modelLayer.Digest == "" {
		return ManifestInfo{}, &Error{Kind: ErrorResponse, Operation: "select model layer", Err: errors.New("manifest does not contain a model layer")}
	}
	contextTokens, err := client.ModelMetadata(ctx, reference, modelLayer)
	if err != nil {
		return ManifestInfo{}, err
	}
	return ManifestInfo{
		Reference:     reference,
		Manifest:      manifest,
		ModelLayer:    modelLayer,
		TotalBytes:    total,
		ContextTokens: contextTokens,
	}, nil
}

func (client RegistryClient) Manifest(ctx context.Context, reference string) (RegistryManifest, error) {
	namespace, name, tag, err := splitExactReference(reference)
	if err != nil {
		return RegistryManifest{}, err
	}
	base, err := client.registryBase()
	if err != nil {
		return RegistryManifest{}, err
	}
	base.Path = path.Join(base.Path, "v2", namespace, name, "manifests", tag)
	response, err := client.do(ctx, platform.Request{
		Method: http.MethodGet,
		URL:    base.String(),
		Headers: map[string]string{
			"Accept": "application/vnd.docker.distribution.manifest.v2+json",
		},
		Timeout:          client.RequestTimeout,
		MaxResponseBytes: positiveLimit(client.ManifestLimit, DefaultManifestLimit),
	})
	if err != nil {
		return RegistryManifest{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return RegistryManifest{}, &Error{Kind: ErrorStatus, Operation: "fetch exact registry manifest", StatusCode: response.StatusCode}
	}
	data, err := readBounded(response.Body, positiveLimit(client.ManifestLimit, DefaultManifestLimit))
	if err != nil {
		return RegistryManifest{}, err
	}
	var manifest RegistryManifest
	if err := decodeOneJSON(data, &manifest); err != nil {
		return RegistryManifest{}, &Error{Kind: ErrorResponse, Operation: "decode exact registry manifest", Err: err}
	}
	if manifest.SchemaVersion != 2 || len(manifest.Layers) == 0 {
		return RegistryManifest{}, &Error{Kind: ErrorResponse, Operation: "validate exact registry manifest", Err: errors.New("unsupported or empty manifest")}
	}
	for _, descriptor := range append([]RegistryDescriptor{manifest.Config}, manifest.Layers...) {
		if !digestPattern.MatchString(descriptor.Digest) || descriptor.Size == 0 || strings.TrimSpace(descriptor.MediaType) == "" {
			return RegistryManifest{}, &Error{Kind: ErrorResponse, Operation: "validate exact registry manifest", Err: errors.New("manifest descriptor is incomplete")}
		}
	}
	return manifest, nil
}

func (client RegistryClient) ModelMetadata(ctx context.Context, reference string, descriptor RegistryDescriptor) (uint64, error) {
	if !digestPattern.MatchString(descriptor.Digest) || descriptor.Size == 0 {
		return 0, &Error{Kind: ErrorInvalidInput, Operation: "read model metadata", Err: errors.New("invalid model descriptor")}
	}
	namespace, name, _, err := splitExactReference(reference)
	if err != nil {
		return 0, err
	}
	base, err := client.registryBase()
	if err != nil {
		return 0, err
	}
	base.Path = path.Join(base.Path, "v2", namespace, name, "blobs", descriptor.Digest)
	limit := positiveLimit(client.MetadataLimit, DefaultGGUFMetadataLimit)
	if descriptor.Size < uint64(limit) {
		limit = int64(descriptor.Size)
	}
	response, err := client.do(ctx, platform.Request{
		Method: http.MethodGet,
		URL:    base.String(),
		Headers: map[string]string{
			"Range": "bytes=0-" + strconv.FormatInt(limit-1, 10),
		},
		Timeout:          client.RequestTimeout,
		MaxResponseBytes: limit,
	})
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent {
		return 0, &Error{Kind: ErrorStatus, Operation: "read model-layer metadata range", StatusCode: response.StatusCode, Err: errors.New("registry did not honor bounded range request")}
	}
	expectedBytes, err := validateContentRange(headerValue(response.Headers, "Content-Range"), limit, descriptor.Size)
	if err != nil {
		return 0, &Error{Kind: ErrorResponse, Operation: "validate model-layer metadata range", Err: err}
	}
	data, err := readBounded(response.Body, limit)
	if err != nil {
		return 0, err
	}
	if uint64(len(data)) != expectedBytes {
		return 0, &Error{Kind: ErrorResponse, Operation: "read model-layer metadata range", Err: errors.New("registry returned an incomplete range body")}
	}
	contextTokens, err := catalog.ParseGGUFContext(bytes.NewReader(data))
	if err != nil {
		return 0, &Error{Kind: ErrorCompatibility, Operation: "parse model-layer GGUF metadata", Err: err}
	}
	return contextTokens, nil
}

func (client RegistryClient) registryBase() (*url.URL, error) {
	baseURL := client.BaseURL
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultRegistryURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Scheme, "https") {
		return nil, &Error{Kind: ErrorInvalidInput, Operation: "configure registry", Err: errors.New("registry base URL must be HTTPS")}
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func (client RegistryClient) do(ctx context.Context, request platform.Request) (platform.Response, error) {
	if client.Network == nil {
		return platform.Response{}, &Error{Kind: ErrorInvalidInput, Operation: "registry request", Err: errors.New("network is not configured")}
	}
	current := request
	for redirects := 0; redirects <= maxRegistryRedirects; redirects++ {
		parsed, err := url.Parse(current.URL)
		if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Scheme, "https") {
			return platform.Response{}, &Error{Kind: ErrorRequest, Operation: "registry request", Err: errors.New("registry request or redirect is not HTTPS")}
		}
		response, err := client.Network.Do(ctx, current)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return platform.Response{}, ctxErr
			}
			return platform.Response{}, &Error{Kind: ErrorRequest, Operation: "registry request", Err: err}
		}
		if response.StatusCode < 300 || response.StatusCode >= 400 {
			return response, nil
		}
		location := headerValue(response.Headers, "Location")
		_ = response.Body.Close()
		if location == "" {
			return platform.Response{}, &Error{Kind: ErrorResponse, Operation: "follow registry redirect", Err: errors.New("redirect omitted Location")}
		}
		next, err := parsed.Parse(location)
		if err != nil || !strings.EqualFold(next.Scheme, "https") {
			return platform.Response{}, &Error{Kind: ErrorRequest, Operation: "follow registry redirect", Err: errors.New("registry redirect must remain HTTPS")}
		}
		current.URL = next.String()
	}
	return platform.Response{}, &Error{Kind: ErrorRequest, Operation: "follow registry redirect", Err: errors.New("too many redirects")}
}

func splitExactReference(reference string) (namespace, name, tag string, err error) {
	if strings.ContainsAny(reference, "\r\n\t @") || strings.Contains(reference, "://") {
		return "", "", "", &Error{Kind: ErrorInvalidInput, Operation: "parse exact model reference", Err: errors.New("model reference contains unsafe characters")}
	}
	slash := strings.LastIndex(reference, "/")
	colon := strings.LastIndex(reference, ":")
	if colon <= slash || colon == len(reference)-1 {
		return "", "", "", &Error{Kind: ErrorInvalidInput, Operation: "parse exact model reference", Err: errors.New("an exact model tag is required")}
	}
	repository := reference[:colon]
	tag = reference[colon+1:]
	components := strings.Split(repository, "/")
	if len(components) != 1 {
		return "", "", "", &Error{Kind: ErrorInvalidInput, Operation: "parse exact model reference", Err: errors.New("only local Ollama Library names are supported")}
	}
	for _, component := range append(components, tag) {
		if !safeModelComponent(component) {
			return "", "", "", &Error{Kind: ErrorInvalidInput, Operation: "parse exact model reference", Err: errors.New("model reference is not registry-safe")}
		}
	}
	return "library", components[0], tag, nil
}

func validateModelReference(reference string) error {
	_, _, _, err := splitExactReference(reference)
	return err
}

func safeModelComponent(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	first := value[0]
	if !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || (first >= '0' && first <= '9')) {
		return false
	}
	if strings.Contains(strings.ToLower(value), "cloud") {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, &Error{Kind: ErrorInvalidInput, Operation: "read bounded response", Err: errors.New("invalid response limit")}
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, &Error{Kind: ErrorResponse, Operation: "read bounded response", Err: err}
	}
	if int64(len(data)) > limit {
		return nil, &Error{Kind: ErrorLimit, Operation: "read bounded response", Err: fmt.Errorf("response exceeds %d bytes", limit)}
	}
	return data, nil
}

func decodeOneJSON(data []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func headerValue(headers map[string][]string, name string) string {
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func positiveLimit(value, fallback int64) int64 {
	if value > 0 {
		return value
	}
	return fallback
}

func validateContentRange(value string, requested int64, total uint64) (uint64, error) {
	matches := contentRangePattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(matches) != 3 {
		return 0, errors.New("registry range response omitted a valid Content-Range")
	}
	end, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil || end+1 > uint64(requested) {
		return 0, errors.New("registry range response exceeded the requested bound")
	}
	if matches[2] != "*" {
		reportedTotal, err := strconv.ParseUint(matches[2], 10, 64)
		if err != nil || reportedTotal != total {
			return 0, errors.New("registry range response size disagrees with the manifest")
		}
	}
	return end + 1, nil
}
