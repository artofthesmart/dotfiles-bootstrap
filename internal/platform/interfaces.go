package platform

import (
	"context"
	"io"
	"io/fs"
	"time"
)

type FileSystem interface {
	Open(string) (io.ReadCloser, error)
	Stat(string) (fs.FileInfo, error)
	ReadFile(string) ([]byte, error)
	WriteFile(string, []byte, fs.FileMode) error
	MkdirAll(string, fs.FileMode) error
	Rename(string, string) error
	Remove(string) error
}

type Command struct {
	Name  string
	Args  []string
	Dir   string
	Env   []string
	Stdin io.Reader
}

type CommandResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

type Runner interface {
	Run(context.Context, Command) (CommandResult, error)
}

type Request struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    io.Reader
}

type Response struct {
	StatusCode int
	Headers    map[string][]string
	Body       io.ReadCloser
}

type Network interface {
	Do(context.Context, Request) (Response, error)
}

type Capabilities struct {
	FileSystem FileSystem
	Runner     Runner
	Network    Network
}

type SystemClock struct{}

func (SystemClock) Now() time.Time {
	return time.Now()
}
