package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDriftCheckerCleanAndReadOnly(t *testing.T) {
	value := MustLoadEmbedded()
	value.Entries = value.Entries[:1]
	entry := value.Entries[0]
	fixture := ggufFixture(t, entry.Context.AdvertisedNativeTokens)
	transport := &registryFixtureTransport{
		t:        t,
		entry:    entry,
		gguf:     fixture,
		requests: []string{},
	}
	checker := DriftChecker{
		Client: &http.Client{Transport: transport},
		Now: func() time.Time {
			return time.Date(2026, time.October, 20, 20, 0, 0, 0, time.UTC)
		},
	}
	before := MustJSON(t, value)
	report, err := checker.Check(context.Background(), value)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Clean || len(report.Entries) != 1 || report.Entries[0].Status != "clean" {
		t.Fatalf("report = %+v", report)
	}
	if !bytes.Equal(before, MustJSON(t, value)) {
		t.Fatal("drift checker mutated the catalog")
	}
	if len(transport.requests) != 2 {
		t.Fatalf("requests = %v", transport.requests)
	}
	for _, request := range transport.requests {
		if !strings.HasPrefix(request, "GET https://") {
			t.Fatalf("non-read-only or non-HTTPS request: %s", request)
		}
	}
}

func TestDriftCheckerReportsDigestSizeAndContextChanges(t *testing.T) {
	value := MustLoadEmbedded()
	value.Entries = value.Entries[:1]
	entry := value.Entries[0]
	transport := &registryFixtureTransport{
		t:             t,
		entry:         entry,
		gguf:          ggufFixture(t, RequiredContextTokens),
		changedDigest: true,
		changedSize:   true,
		requests:      []string{},
	}
	report, err := (DriftChecker{Client: &http.Client{Transport: transport}}).Check(context.Background(), value)
	if err != nil {
		t.Fatal(err)
	}
	if report.Clean {
		t.Fatal("expected drift")
	}
	issues := report.Entries[0].Issues
	for _, code := range []string{"model_digest_changed", "download_size_changed", "native_context_changed"} {
		if !hasExplanation(issues, code) {
			t.Errorf("issues %v lack %s", issues, code)
		}
	}
}

type registryFixtureTransport struct {
	t             *testing.T
	entry         Entry
	gguf          []byte
	changedDigest bool
	changedSize   bool
	requests      []string
}

func (r *registryFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	r.requests = append(r.requests, request.Method+" "+request.URL.String())
	if strings.Contains(request.URL.Path, "/manifests/") {
		digest := *r.entry.Source.ExpectedModelDigest
		if r.changedDigest {
			digest = "sha256:" + strings.Repeat("0", 64)
		}
		size := r.entry.Download.MinimumBytes
		if r.changedSize {
			size = r.entry.Download.MaximumBytes + 1
		}
		body := registryManifest{
			Layers: []registryLayer{
				{MediaType: "application/vnd.ollama.image.model", Digest: digest, Size: size - 2},
				{MediaType: "application/vnd.ollama.image.params", Digest: "sha256:" + strings.Repeat("1", 64), Size: 2},
			},
		}
		data, err := json.Marshal(body)
		if err != nil {
			r.t.Fatal(err)
		}
		return fixtureResponse(request, data), nil
	}
	if strings.Contains(request.URL.Path, "/blobs/") {
		if request.Header.Get("Range") != "bytes=0-1048575" {
			r.t.Fatalf("Range = %q", request.Header.Get("Range"))
		}
		return fixtureResponse(request, r.gguf), nil
	}
	r.t.Fatalf("unexpected request %s", request.URL)
	return nil, nil
}

func fixtureResponse(request *http.Request, body []byte) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    request,
	}
}
