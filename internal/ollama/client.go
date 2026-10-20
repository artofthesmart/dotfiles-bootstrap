package ollama

import "context"

type RuntimeStatus struct {
	Version   string
	Available bool
}

type Client interface {
	Status(context.Context) (RuntimeStatus, error)
	ListModels(context.Context) ([]string, error)
}
