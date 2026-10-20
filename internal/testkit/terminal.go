package testkit

import (
	"bytes"
	"context"
	"errors"
	"sync"
)

type Prompt struct {
	ID      string
	Message string
	Secret  bool
}

type PromptResult struct {
	Value string
	Err   error
}

type PromptCall struct {
	Prompt Prompt
}

type FakeTerminal struct {
	mu      sync.Mutex
	inputs  []PromptResult
	prompts []PromptCall
	stdout  bytes.Buffer
	stderr  bytes.Buffer
	IsTTY   bool
	Width   int
	Height  int
}

func NewFakeTerminal(inputs ...PromptResult) *FakeTerminal {
	return &FakeTerminal{
		inputs: append([]PromptResult(nil), inputs...),
		IsTTY:  true,
		Width:  100,
		Height: 30,
	}
}

func (t *FakeTerminal) Prompt(ctx context.Context, prompt Prompt) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prompts = append(t.prompts, PromptCall{Prompt: prompt})
	if len(t.inputs) == 0 {
		return "", ErrNoScriptedResult
	}
	result := t.inputs[0]
	t.inputs = t.inputs[1:]
	return result.Value, result.Err
}

func (t *FakeTerminal) WriteOutput(value string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stdout.WriteString(value)
}

func (t *FakeTerminal) WriteError(value string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stderr.WriteString(value)
}

func (t *FakeTerminal) Output() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stdout.String()
}

func (t *FakeTerminal) ErrorOutput() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stderr.String()
}

func (t *FakeTerminal) Prompts() []PromptCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]PromptCall(nil), t.prompts...)
}

type ConsentRequest struct {
	ID      string
	Summary string
	Risk    string
}

type ConsentDecision string

const (
	ConsentAccepted ConsentDecision = "accepted"
	ConsentDeclined ConsentDecision = "declined"
)

type ConsentResult struct {
	Decision ConsentDecision
	Err      error
}

type FakeConsent struct {
	mu       sync.Mutex
	results  []ConsentResult
	requests []ConsentRequest
}

func NewFakeConsent(results ...ConsentResult) *FakeConsent {
	return &FakeConsent{results: append([]ConsentResult(nil), results...)}
}

func (c *FakeConsent) Request(ctx context.Context, request ConsentRequest) (ConsentDecision, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, request)
	if len(c.results) == 0 {
		return "", ErrNoScriptedResult
	}
	result := c.results[0]
	c.results = c.results[1:]
	return result.Decision, result.Err
}

func (c *FakeConsent) Requests() []ConsentRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ConsentRequest(nil), c.requests...)
}

type EventRecorder struct {
	mu     sync.Mutex
	events []any
	err    error
}

func (r *EventRecorder) Emit(event any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return r.err
}

func (r *EventRecorder) Events() []any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]any(nil), r.events...)
}

func (r *EventRecorder) SetError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

var ErrConsentUnavailable = errors.New("consent decision unavailable")
