package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DriftSchemaVersion = "local-ai.catalog-drift/v1"

type DriftReport struct {
	SchemaVersion  string             `json:"schema_version"`
	CatalogVersion string             `json:"catalog_version"`
	CheckedAt      time.Time          `json:"checked_at"`
	Clean          bool               `json:"clean"`
	Entries        []DriftEntryReport `json:"entries"`
}

type DriftEntryReport struct {
	CatalogID             string        `json:"catalog_id"`
	OllamaName            string        `json:"ollama_name"`
	Status                string        `json:"status"`
	ExpectedModelDigest   string        `json:"expected_model_digest"`
	ObservedModelDigest   string        `json:"observed_model_digest,omitempty"`
	ObservedDownloadBytes uint64        `json:"observed_download_bytes,omitempty"`
	ObservedContextTokens uint64        `json:"observed_context_tokens,omitempty"`
	Issues                []Explanation `json:"issues"`
}

type DriftChecker struct {
	Client *http.Client
	Now    func() time.Time
}

func (c DriftChecker) Check(ctx context.Context, value Catalog) (DriftReport, error) {
	if err := Validate(value); err != nil {
		return DriftReport{}, err
	}
	client := c.Client
	if client == nil {
		client = &http.Client{
			Timeout: 45 * time.Second,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if request.URL.Scheme != "https" {
					return fmt.Errorf("redirected to non-HTTPS URL")
				}
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		}
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	report := DriftReport{
		SchemaVersion:  DriftSchemaVersion,
		CatalogVersion: value.CatalogVersion,
		CheckedAt:      now().UTC(),
		Clean:          true,
		Entries:        make([]DriftEntryReport, 0, len(value.Entries)),
	}
	for _, entry := range value.Entries {
		entryReport := c.checkEntry(ctx, client, entry)
		if entryReport.Status != "clean" {
			report.Clean = false
		}
		report.Entries = append(report.Entries, entryReport)
	}
	return report, nil
}

type registryManifest struct {
	Layers []registryLayer `json:"layers"`
}

type registryLayer struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      uint64 `json:"size"`
}

func (c DriftChecker) checkEntry(ctx context.Context, client *http.Client, entry Entry) DriftEntryReport {
	result := DriftEntryReport{
		CatalogID:  entry.ID,
		OllamaName: entry.Model.OllamaName,
		Status:     "clean",
		Issues:     []Explanation{},
	}
	if entry.Source.ExpectedModelDigest != nil {
		result.ExpectedModelDigest = *entry.Source.ExpectedModelDigest
	}

	body, err := getBounded(ctx, client, entry.Source.RegistryManifest, "", 2*1024*1024)
	if err != nil {
		addDriftIssue(&result, "manifest_unavailable", err.Error())
		return result
	}
	var manifest registryManifest
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&manifest); err != nil {
		addDriftIssue(&result, "manifest_invalid", fmt.Sprintf("decode registry manifest: %v", err))
		return result
	}

	var modelLayers []registryLayer
	for _, layer := range manifest.Layers {
		result.ObservedDownloadBytes = saturatingAdd(result.ObservedDownloadBytes, layer.Size)
		if layer.MediaType == "application/vnd.ollama.image.model" {
			modelLayers = append(modelLayers, layer)
		}
	}
	if len(modelLayers) != 1 {
		addDriftIssue(&result, "model_layer_count_changed", fmt.Sprintf("expected one model layer, observed %d", len(modelLayers)))
		return result
	}
	result.ObservedModelDigest = modelLayers[0].Digest
	if result.ExpectedModelDigest != result.ObservedModelDigest {
		addDriftIssue(&result, "model_digest_changed", fmt.Sprintf("expected %s, observed %s", result.ExpectedModelDigest, result.ObservedModelDigest))
	}
	if result.ObservedDownloadBytes < entry.Download.MinimumBytes || result.ObservedDownloadBytes > entry.Download.MaximumBytes {
		addDriftIssue(&result, "download_size_changed", fmt.Sprintf("observed %d bytes is outside reviewed range %d..%d", result.ObservedDownloadBytes, entry.Download.MinimumBytes, entry.Download.MaximumBytes))
	}

	blobURL, err := registryBlobURL(entry.Source.RegistryManifest, modelLayers[0].Digest)
	if err != nil {
		addDriftIssue(&result, "blob_url_invalid", err.Error())
		return result
	}
	prefix, err := getBounded(ctx, client, blobURL, "bytes=0-1048575", 1024*1024+1)
	if err != nil {
		addDriftIssue(&result, "model_metadata_unavailable", err.Error())
		return result
	}
	contextTokens, err := ParseGGUFContext(bytes.NewReader(prefix))
	if err != nil {
		addDriftIssue(&result, "model_metadata_invalid", err.Error())
		return result
	}
	result.ObservedContextTokens = contextTokens
	if contextTokens != entry.Context.AdvertisedNativeTokens {
		addDriftIssue(&result, "native_context_changed", fmt.Sprintf("expected %d tokens, observed %d", entry.Context.AdvertisedNativeTokens, contextTokens))
	}
	if contextTokens < RequiredContextTokens {
		addDriftIssue(&result, "native_context_below_minimum", fmt.Sprintf("observed native context %d is below %d", contextTokens, RequiredContextTokens))
	}
	return result
}

func getBounded(ctx context.Context, client *http.Client, rawURL, rangeHeader string, maximum int64) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("refusing non-HTTPS or invalid URL %q", rawURL)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if rangeHeader != "" {
		request.Header.Set("Range", rangeHeader)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.Request == nil || response.Request.URL == nil || response.Request.URL.Scheme != "https" {
		return nil, fmt.Errorf("final response URL is not HTTPS")
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP status %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maximum {
		return nil, fmt.Errorf("response exceeds %d-byte limit", maximum)
	}
	return body, nil
}

func registryBlobURL(manifestURL, digest string) (string, error) {
	if !digestPattern.MatchString(digest) {
		return "", fmt.Errorf("invalid model digest %q", digest)
	}
	parsed, err := url.Parse(manifestURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("invalid registry manifest URL")
	}
	index := strings.LastIndex(parsed.Path, "/manifests/")
	if index < 0 {
		return "", fmt.Errorf("registry manifest URL does not contain /manifests/")
	}
	parsed.Path = parsed.Path[:index] + "/blobs/" + digest
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func addDriftIssue(result *DriftEntryReport, code, detail string) {
	result.Status = "drift"
	result.Issues = append(result.Issues, Explanation{Code: code, Detail: detail})
}
