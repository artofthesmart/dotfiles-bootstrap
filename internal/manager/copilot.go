package manager

import (
	"context"
	"io"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/copilot"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/ollama"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
)

type CopilotLifecycle interface {
	ConfigureSteps(copilot.Request) ([]state.Step, error)
	Status(context.Context, copilot.Request) (copilot.Status, error)
	Diagnose(context.Context, copilot.Request) copilot.Diagnosis
	Open(context.Context, copilot.Request, []string, io.Reader, io.Writer, io.Writer) error
	Uninstall(context.Context, []state.ManagedAsset) error
}

type CopilotAdapter struct {
	Platform platform.Capabilities
	Clock    app.Clock
	GOOS     string
	GOARCH   string
}

func (adapter CopilotAdapter) ConfigureSteps(request copilot.Request) ([]state.Step, error) {
	manager, err := adapter.manager(request.Endpoint)
	if err != nil {
		return nil, err
	}
	return copilot.NewConfigureSteps(manager, request), nil
}

func (adapter CopilotAdapter) Status(ctx context.Context, request copilot.Request) (copilot.Status, error) {
	manager, err := adapter.manager(request.Endpoint)
	if err != nil {
		return copilot.Status{}, err
	}
	return manager.Status(ctx, request)
}

func (adapter CopilotAdapter) Diagnose(ctx context.Context, request copilot.Request) copilot.Diagnosis {
	manager, err := adapter.manager(request.Endpoint)
	if err != nil {
		return copilot.Diagnosis{Issues: []app.Issue{copilotAdapterIssue(err)}}
	}
	return manager.Diagnose(ctx, request)
}

func (adapter CopilotAdapter) Open(ctx context.Context, request copilot.Request, arguments []string, input io.Reader, output, errorOutput io.Writer) error {
	manager, err := adapter.manager(request.Endpoint)
	if err != nil {
		return err
	}
	return manager.Open(ctx, request, arguments, input, output, errorOutput)
}

func (adapter CopilotAdapter) Uninstall(ctx context.Context, assets []state.ManagedAsset) error {
	manager, err := adapter.manager("")
	if err != nil {
		return err
	}
	return manager.Uninstall(ctx, assets)
}

func (adapter CopilotAdapter) manager(endpoint string) (*copilot.Manager, error) {
	api, err := ollama.NewLocalAPIClient(endpoint, nil)
	if err != nil {
		return nil, err
	}
	return copilot.NewManager(copilot.Options{
		Platform: adapter.Platform,
		Ollama:   api,
		Resolver: copilot.ReleaseResolver{Network: adapter.Platform.Network},
		Clock:    adapter.Clock,
		GOOS:     adapter.GOOS,
		GOARCH:   adapter.GOARCH,
	}), nil
}

func copilotAdapterIssue(err error) app.Issue {
	value := issueForError(err)
	value.Domain = "copilot"
	return value
}
