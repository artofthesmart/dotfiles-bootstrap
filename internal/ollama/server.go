package ollama

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

type ServerStartOptions struct {
	AllowStart     bool
	ConsentRestart bool
	Attempts       int
	RetryDelay     time.Duration
}

type ServerState struct {
	Ready          bool
	Version        string
	StartedProcess bool
	StartedService bool
	Restarted      bool
	PID            int
}

func (manager Manager) EnsureServer(ctx context.Context, options ServerStartOptions) (ServerState, error) {
	if manager.API == nil {
		return ServerState{}, &Error{Kind: ErrorInvalidInput, Operation: "ensure server readiness", Err: errors.New("runtime API is not configured")}
	}
	if status, err := manager.API.Status(ctx); err == nil && status.Available {
		return ServerState{Ready: true, Version: status.Version}, nil
	}
	if !options.AllowStart {
		return ServerState{}, &Error{Kind: ErrorConsent, Operation: "start Ollama server", Err: errors.New("server start was not authorized")}
	}

	state := ServerState{}
	serviceName := manager.ServiceName
	if serviceName == "" {
		serviceName = "ollama"
	}
	if manager.Services != nil {
		serviceState, err := manager.Services.Status(ctx, serviceName)
		if err == nil {
			switch serviceState {
			case platform.ServiceStopped:
				if err := manager.Services.Start(ctx, serviceName); err != nil {
					return ServerState{}, &Error{Kind: ErrorCommand, Operation: "start Ollama service", Err: err}
				}
				state.StartedService = true
			case platform.ServiceRunning:
				if !options.ConsentRestart {
					return ServerState{}, &Error{Kind: ErrorConsent, Operation: "restart existing Ollama service", Err: errors.New("the running service is not ready and restart consent is required")}
				}
				if err := manager.Services.Stop(ctx, serviceName); err != nil {
					return ServerState{}, &Error{Kind: ErrorCommand, Operation: "stop Ollama service", Err: err}
				}
				if err := manager.Services.Start(ctx, serviceName); err != nil {
					return ServerState{}, &Error{Kind: ErrorCommand, Operation: "restart Ollama service", Err: err}
				}
				state.Restarted = true
				state.StartedService = true
			}
		}
	}
	if !state.StartedService {
		if manager.Processes == nil {
			return ServerState{}, &Error{Kind: ErrorCommand, Operation: "start Ollama server", Err: errors.New("no process or service start capability is available")}
		}
		executable := manager.OllamaExecutable
		if executable == "" {
			executable = "ollama"
		}
		process, err := manager.Processes.Start(ctx, platform.Command{
			Name: executable,
			Args: []string{"serve"},
			Env:  []string{"OLLAMA_CONTEXT_LENGTH=65536"},
		})
		if err != nil {
			return ServerState{}, &Error{Kind: ErrorCommand, Operation: "start manager-owned Ollama server", Err: err}
		}
		state.StartedProcess = true
		state.PID = process.PID
	}

	attempts := options.Attempts
	if attempts <= 0 {
		attempts = 20
	}
	delay := options.RetryDelay
	if delay <= 0 {
		delay = 250 * time.Millisecond
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		status, err := manager.API.Status(ctx)
		if err == nil && status.Available {
			state.Ready = true
			state.Version = status.Version
			return state, nil
		}
		lastErr = err
		if attempt+1 < attempts {
			if err := manager.sleep(ctx, delay); err != nil {
				return state, err
			}
		}
	}
	if lastErr == nil {
		lastErr = errors.New("readiness probe did not succeed")
	}
	return state, &Error{Kind: ErrorCommand, Operation: "wait for Ollama server readiness", Err: fmt.Errorf("server did not become ready after %d attempts: %w", attempts, lastErr)}
}
