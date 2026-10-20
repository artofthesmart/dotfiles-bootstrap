package ollama

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

const (
	configDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	modelDigest  = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestRegistryExactManifestSizeAndBoundedModelRange(t *testing.T) {
	manifest := fmt.Sprintf(`{
		"schemaVersion":2,
		"mediaType":"application/vnd.docker.distribution.manifest.v2+json",
		"config":{"mediaType":"application/vnd.docker.container.image.v1+json","digest":"%s","size":123},
		"layers":[
			{"mediaType":"application/vnd.ollama.image.model","digest":"%s","size":5000},
			{"mediaType":"application/vnd.ollama.image.template","digest":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","size":77}
		]
	}`, configDigest, modelDigest)
	network := testkit.NewScriptedNetwork(
		testkit.ResponseFixture{StatusCode: http.StatusOK, Body: []byte(manifest)},
		testkit.ResponseFixture{
			StatusCode: http.StatusPartialContent,
			Headers: map[string][]string{
				"Content-Range": {"bytes 0-4999/5000"},
			},
			Body: paddedGGUF(131072, 5000),
		},
	)
	client := NewRegistryClient(network)
	info, err := client.Inspect(context.Background(), "llama3.1:8b")
	if err != nil {
		t.Fatal(err)
	}
	if info.TotalBytes != 5200 {
		t.Fatalf("TotalBytes = %d, want 5200", info.TotalBytes)
	}
	if info.ContextTokens != 131072 || info.ModelLayer.Digest != modelDigest {
		t.Fatalf("inspection = %#v", info)
	}
	calls := network.Calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if calls[0].URL != "https://registry.ollama.ai/v2/library/llama3.1/manifests/8b" {
		t.Fatalf("manifest URL = %s", calls[0].URL)
	}
	if calls[1].URL != "https://registry.ollama.ai/v2/library/llama3.1/blobs/"+modelDigest {
		t.Fatalf("blob URL = %s", calls[1].URL)
	}
	if calls[1].Headers["Range"] != "bytes=0-4999" {
		t.Fatalf("Range = %q", calls[1].Headers["Range"])
	}
}

func TestRegistryRejectsRedirectDowngradeAndUnboundedOrMalformedResponses(t *testing.T) {
	t.Run("downgrade", func(t *testing.T) {
		network := testkit.NewScriptedNetwork(testkit.RedirectFixture(http.StatusFound, "http://registry.ollama.ai/insecure"))
		client := NewRegistryClient(network)
		_, err := client.Manifest(context.Background(), "llama3.1:8b")
		if err == nil || !strings.Contains(err.Error(), "HTTPS") {
			t.Fatalf("Manifest() error = %v", err)
		}
	})

	t.Run("manifest limit", func(t *testing.T) {
		network := testkit.NewScriptedNetwork(testkit.ResponseFixture{
			StatusCode: http.StatusOK,
			Body:       bytes.Repeat([]byte("x"), 33),
		})
		client := NewRegistryClient(network)
		client.ManifestLimit = 32
		_, err := client.Manifest(context.Background(), "llama3.1:8b")
		var operationError *Error
		if !errors.As(err, &operationError) || operationError.Kind != ErrorLimit {
			t.Fatalf("Manifest() error = %#v, want ErrorLimit", err)
		}
	})

	t.Run("range required", func(t *testing.T) {
		network := testkit.NewScriptedNetwork(testkit.ResponseFixture{
			StatusCode: http.StatusOK,
			Body:       paddedGGUF(131072, 5000),
		})
		client := NewRegistryClient(network)
		_, err := client.ModelMetadata(context.Background(), "llama3.1:8b", RegistryDescriptor{
			MediaType: "application/vnd.ollama.image.model",
			Digest:    modelDigest,
			Size:      5000,
		})
		if err == nil || !strings.Contains(err.Error(), "HTTP status 200") {
			t.Fatalf("ModelMetadata() error = %v", err)
		}
	})

	t.Run("invalid GGUF", func(t *testing.T) {
		network := testkit.NewScriptedNetwork(testkit.ResponseFixture{
			StatusCode: http.StatusPartialContent,
			Headers: map[string][]string{
				"Content-Range": {"bytes 0-4999/5000"},
			},
			Body: paddedBytes([]byte("not GGUF"), 5000),
		})
		client := NewRegistryClient(network)
		_, err := client.ModelMetadata(context.Background(), "llama3.1:8b", RegistryDescriptor{
			MediaType: "application/vnd.ollama.image.model",
			Digest:    modelDigest,
			Size:      5000,
		})
		var operationError *Error
		if !errors.As(err, &operationError) || operationError.Kind != ErrorCompatibility {
			t.Fatalf("ModelMetadata() error = %#v", err)
		}
	})

	t.Run("multiple model layers", func(t *testing.T) {
		manifest := fmt.Sprintf(`{
			"schemaVersion":2,
			"config":{"mediaType":"config","digest":"%s","size":1},
			"layers":[
				{"mediaType":"application/vnd.ollama.image.model","digest":"%s","size":2},
				{"mediaType":"application/vnd.ollama.image.model","digest":"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","size":3}
			]
		}`, configDigest, modelDigest)
		network := testkit.NewScriptedNetwork(testkit.ResponseFixture{StatusCode: http.StatusOK, Body: []byte(manifest)})
		client := NewRegistryClient(network)
		if _, err := client.Inspect(context.Background(), "llama3.1:8b"); err == nil {
			t.Fatal("Inspect() accepted multiple model layers")
		}
	})
}

func TestRegistryRequiresExactSafeTags(t *testing.T) {
	client := NewRegistryClient(testkit.NewScriptedNetwork())
	for _, reference := range []string{
		"llama3.1",
		"llama3.1:",
		"llama3.1:8b\nx",
		"https://example/model:tag",
		"model:tag@sha256:abc",
		"owner/model",
		"x:cloud",
		"CLOUD-model:tag",
		"--help:tag",
	} {
		if _, err := client.Manifest(context.Background(), reference); err == nil {
			t.Fatalf("Manifest(%q) accepted a non-exact or unsafe tag", reference)
		}
	}
}

func ggufContextFixture(contextTokens uint64) []byte {
	var buffer bytes.Buffer
	buffer.WriteString("GGUF")
	_ = binary.Write(&buffer, binary.LittleEndian, uint32(3))
	_ = binary.Write(&buffer, binary.LittleEndian, uint64(0))
	_ = binary.Write(&buffer, binary.LittleEndian, uint64(2))
	writeGGUFString(&buffer, "general.architecture")
	_ = binary.Write(&buffer, binary.LittleEndian, uint32(8))
	writeGGUFString(&buffer, "llama")
	writeGGUFString(&buffer, "llama.context_length")
	_ = binary.Write(&buffer, binary.LittleEndian, uint32(10))
	_ = binary.Write(&buffer, binary.LittleEndian, contextTokens)
	return buffer.Bytes()
}

func writeGGUFString(buffer *bytes.Buffer, value string) {
	_ = binary.Write(buffer, binary.LittleEndian, uint64(len(value)))
	buffer.WriteString(value)
}

func paddedGGUF(contextTokens uint64, size int) []byte {
	return paddedBytes(ggufContextFixture(contextTokens), size)
}

func paddedBytes(value []byte, size int) []byte {
	result := make([]byte, size)
	copy(result, value)
	return result
}
