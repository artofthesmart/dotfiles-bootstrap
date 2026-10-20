package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestLocalAPIClientTypedEndpointsAndNoPerRequestContext(t *testing.T) {
	var mu sync.Mutex
	var endpoints []string
	client, err := NewLocalAPIClient(DefaultLocalAPIURL, &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			mu.Lock()
			endpoints = append(endpoints, request.URL.Path)
			mu.Unlock()
			var body string
			switch request.URL.Path {
			case "/api/version":
				body = `{"version":"0.12.7"}`
			case "/api/tags":
				body = `{"models":[{"name":"llama3.1:8b","size":5}]}`
			case "/api/show":
				body = `{"details":{"context_length":131072},"model_info":{"llama.context_length":131072},"capabilities":["tools"]}`
			case "/api/chat":
				data, readErr := io.ReadAll(request.Body)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if strings.Contains(string(data), "num_ctx") || strings.Contains(string(data), `"options"`) {
					t.Fatalf("chat request lowered or overrode context: %s", data)
				}
				var typed ChatRequest
				if err := json.Unmarshal(data, &typed); err != nil {
					t.Fatal(err)
				}
				if typed.Stream {
					t.Fatal("chat request unexpectedly streamed")
				}
				body = `{"done":true,"message":{"role":"assistant","content":"ignored prose","tool_calls":[{"function":{"name":"get_current_temperature","arguments":{"location":"New York","format":"celsius"}}}]}}`
			case "/api/ps":
				body = `{"models":[{"name":"local-ai-test:latest","context_length":65536,"processor":"100% GPU"}]}`
			default:
				t.Fatalf("unexpected endpoint %s", request.URL.Path)
			}
			return jsonResponse(http.StatusOK, body), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := client.Version(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Tags(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Show(ctx, ShowRequest{Model: "llama3.1:8b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Chat(ctx, ChatRequest{
		Model:    "local-ai-test:latest",
		Messages: []ChatMessage{{Role: "user", Content: "weather"}},
		Tools:    []Tool{temperatureTool()},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PS(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"/api/version", "/api/tags", "/api/show", "/api/chat", "/api/ps"}
	if len(endpoints) != len(want) {
		t.Fatalf("endpoints = %v", endpoints)
	}
	for index := range want {
		if endpoints[index] != want[index] {
			t.Fatalf("endpoint[%d] = %q, want %q", index, endpoints[index], want[index])
		}
	}
}

func TestLocalAPIClientCancellationBoundsAndActionableErrors(t *testing.T) {
	t.Run("cancellation", func(t *testing.T) {
		started := make(chan struct{})
		client, err := NewLocalAPIClient(DefaultLocalAPIURL, &http.Client{
			Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				close(started)
				<-request.Context().Done()
				return nil, request.Context().Err()
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-started
			cancel()
		}()
		_, err = client.Version(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Version() error = %v, want context.Canceled", err)
		}
	})

	t.Run("bounded JSON", func(t *testing.T) {
		client, err := NewLocalAPIClient(DefaultLocalAPIURL, &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{"version":"`+strings.Repeat("x", 64)+`"}`), nil
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.WithResponseLimit(16).Version(context.Background())
		var operationError *Error
		if !errors.As(err, &operationError) || operationError.Kind != ErrorLimit {
			t.Fatalf("Version() error = %#v, want bounded ErrorLimit", err)
		}
	})

	t.Run("status omits response body", func(t *testing.T) {
		client, err := NewLocalAPIClient(DefaultLocalAPIURL, &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"error":"secret diagnostic"}`), nil
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Version(context.Background())
		if err == nil || !strings.Contains(err.Error(), "HTTP status 500") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("Version() error is not actionable and redacted: %v", err)
		}
	})

	t.Run("trailing JSON rejected", func(t *testing.T) {
		client, err := NewLocalAPIClient(DefaultLocalAPIURL, &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{"version":"1"} {"version":"2"}`), nil
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Version(context.Background()); err == nil {
			t.Fatal("Version() accepted multiple JSON values")
		}
	})
}

func TestLocalAPIClientRejectsNonLoopbackAndUnsafeModelReferences(t *testing.T) {
	for _, endpoint := range []string{
		"http://example.com:11434",
		"file:///api",
		"http://192.0.2.1:11434",
	} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := NewLocalAPIClient(endpoint, nil); err == nil {
				t.Fatalf("NewLocalAPIClient(%q) succeeded", endpoint)
			}
		})
	}
	client, err := NewLocalAPIClient(DefaultLocalAPIURL, &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{}`), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{"latest", "model:tag\nrm", "https://cloud/model:tag", "model name:tag"} {
		if _, err := client.Show(context.Background(), ShowRequest{Model: reference}); err == nil {
			t.Fatalf("Show(%q) accepted unsafe reference", reference)
		}
	}
}

func TestLocalAPIContextDeadlinePropagates(t *testing.T) {
	client, err := NewLocalAPIClient(DefaultLocalAPIURL, &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := client.Version(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Version() error = %v, want deadline exceeded", err)
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
