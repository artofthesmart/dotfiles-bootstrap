package testkit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

type RequestCall struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
}

type ResponseFixture struct {
	StatusCode          int
	Headers             map[string][]string
	Body                []byte
	Err                 error
	BodyError           error
	BodyErrorAfter      int
	TimedOut            bool
	Cancelled           bool
	WaitForCancellation bool
}

func RedirectFixture(status int, location string) ResponseFixture {
	if status == 0 {
		status = http.StatusFound
	}
	return ResponseFixture{
		StatusCode: status,
		Headers: map[string][]string{
			"Location": {location},
		},
	}
}

type ScriptedNetwork struct {
	mu       sync.Mutex
	fixtures []ResponseFixture
	calls    []RequestCall
}

func NewScriptedNetwork(fixtures ...ResponseFixture) *ScriptedNetwork {
	return &ScriptedNetwork{fixtures: append([]ResponseFixture(nil), fixtures...)}
}

func (n *ScriptedNetwork) Queue(fixtures ...ResponseFixture) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.fixtures = append(n.fixtures, fixtures...)
}

func (n *ScriptedNetwork) Do(ctx context.Context, request platform.Request) (platform.Response, error) {
	if err := ctx.Err(); err != nil {
		return platform.Response{}, err
	}
	var body []byte
	var err error
	if request.Body != nil {
		body, err = io.ReadAll(request.Body)
		if err != nil {
			return platform.Response{}, err
		}
	}

	n.mu.Lock()
	n.calls = append(n.calls, RequestCall{
		Method:  request.Method,
		URL:     request.URL,
		Headers: cloneStringMap(request.Headers),
		Body:    append([]byte(nil), body...),
	})
	if len(n.fixtures) == 0 {
		n.mu.Unlock()
		return platform.Response{}, ErrNoScriptedResult
	}
	fixture := n.fixtures[0]
	n.fixtures = n.fixtures[1:]
	n.mu.Unlock()

	if fixture.WaitForCancellation {
		<-ctx.Done()
		return platform.Response{}, ctx.Err()
	}
	if fixture.Cancelled {
		return platform.Response{}, context.Canceled
	}
	if fixture.TimedOut {
		return platform.Response{}, context.DeadlineExceeded
	}
	if fixture.Err != nil {
		return platform.Response{}, fixture.Err
	}
	status := fixture.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	var responseBody io.ReadCloser = io.NopCloser(newPartialReader(
		fixture.Body,
		fixture.BodyErrorAfter,
		fixture.BodyError,
	))
	return platform.Response{
		StatusCode: status,
		Headers:    cloneHeaderMap(fixture.Headers),
		Body:       responseBody,
	}, nil
}

func (n *ScriptedNetwork) Calls() []RequestCall {
	n.mu.Lock()
	defer n.mu.Unlock()
	calls := make([]RequestCall, len(n.calls))
	for index, call := range n.calls {
		calls[index] = RequestCall{
			Method:  call.Method,
			URL:     call.URL,
			Headers: cloneStringMap(call.Headers),
			Body:    append([]byte(nil), call.Body...),
		}
	}
	return calls
}

type partialReader struct {
	data      []byte
	position  int
	failAfter int
	err       error
	failed    bool
}

func newPartialReader(data []byte, failAfter int, err error) io.Reader {
	if err == nil {
		return &partialReader{data: append([]byte(nil), data...), failAfter: -1}
	}
	if failAfter < 0 || failAfter > len(data) {
		failAfter = len(data)
	}
	return &partialReader{
		data:      append([]byte(nil), data...),
		failAfter: failAfter,
		err:       err,
	}
}

func (r *partialReader) Read(target []byte) (int, error) {
	if r.failAfter >= 0 && r.position >= r.failAfter && !r.failed {
		r.failed = true
		return 0, r.err
	}
	if r.position >= len(r.data) {
		return 0, io.EOF
	}
	end := len(r.data)
	if r.failAfter >= 0 && end > r.failAfter {
		end = r.failAfter
	}
	count := copy(target, r.data[r.position:end])
	r.position += count
	if count == 0 && r.failAfter >= 0 && !r.failed {
		r.failed = true
		return 0, r.err
	}
	return count, nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneHeaderMap(values map[string][]string) map[string][]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string][]string, len(values))
	for key, value := range values {
		cloned[key] = append([]string(nil), value...)
	}
	return cloned
}

var ErrPartialBody = errors.New("scripted partial body failure")
