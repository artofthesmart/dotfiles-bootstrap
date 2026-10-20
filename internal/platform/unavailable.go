package platform

import (
	"context"
	"errors"
	"io"
	"io/fs"
)

var ErrUnavailable = errors.New("platform capability is not implemented")

// UnavailableFileSystem keeps the scaffold incapable of filesystem mutation.
type UnavailableFileSystem struct{}

func (UnavailableFileSystem) Open(string) (io.ReadCloser, error) {
	return nil, ErrUnavailable
}

func (UnavailableFileSystem) Stat(string) (fs.FileInfo, error) {
	return nil, ErrUnavailable
}

func (UnavailableFileSystem) ReadFile(string) ([]byte, error) {
	return nil, ErrUnavailable
}

func (UnavailableFileSystem) WriteFile(string, []byte, fs.FileMode) error {
	return ErrUnavailable
}

func (UnavailableFileSystem) MkdirAll(string, fs.FileMode) error {
	return ErrUnavailable
}

func (UnavailableFileSystem) Rename(string, string) error {
	return ErrUnavailable
}

func (UnavailableFileSystem) Remove(string) error {
	return ErrUnavailable
}

// UnavailableRunner keeps the scaffold incapable of starting host commands.
type UnavailableRunner struct{}

func (UnavailableRunner) Run(context.Context, Command) (CommandResult, error) {
	return CommandResult{}, ErrUnavailable
}

// UnavailableNetwork keeps the scaffold incapable of network access.
type UnavailableNetwork struct{}

func (UnavailableNetwork) Do(context.Context, Request) (Response, error) {
	return Response{}, ErrUnavailable
}
