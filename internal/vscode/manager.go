package vscode

import "context"

type Status struct {
	Version      string
	ProfileReady bool
}

type Manager interface {
	Status(context.Context) (Status, error)
	Open(context.Context, string) error
}
