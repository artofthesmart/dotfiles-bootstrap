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
	Chmod(string, fs.FileMode) error
	Rename(string, string) error
	Remove(string) error
	Sync(string) error
}

type AtomicFileSystem interface {
	FileSystem
	WriteFileAtomic(string, []byte, fs.FileMode) error
}

type Command struct {
	Name        string
	Args        []string
	Dir         string
	Env         []string
	Stdin       io.Reader
	Timeout     time.Duration
	OutputLimit int64
}

type CommandResult struct {
	ExitCode        int
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
}

type Runner interface {
	Run(context.Context, Command) (CommandResult, error)
}

type Request struct {
	Method           string
	URL              string
	Headers          map[string]string
	Body             io.Reader
	Timeout          time.Duration
	MaxResponseBytes int64
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
	FileSystem   FileSystem
	Runner       Runner
	Network      Network
	Paths        PathProvider
	Applications ApplicationLocator
	Processes    ProcessManager
	Services     ServiceController
}

type UserPaths struct {
	Home      string
	ConfigDir string
	DataDir   string
	CacheDir  string
	BinDir    string
	TempDir   string
}

type PathProvider interface {
	UserPaths() (UserPaths, error)
}

type Application struct {
	Name       string
	Executable string
	Source     string
}

type ApplicationLocator interface {
	FindExecutable(string) (Application, error)
	FindApplication(string) (Application, error)
}

type ProcessState struct {
	PID     int
	Running bool
}

type ProcessManager interface {
	Start(context.Context, Command) (ProcessState, error)
	Inspect(int) (ProcessState, error)
	Terminate(context.Context, int) error
}

type ServiceState string

const (
	ServiceUnknown ServiceState = "unknown"
	ServiceStopped ServiceState = "stopped"
	ServiceRunning ServiceState = "running"
)

type ServiceController interface {
	Status(context.Context, string) (ServiceState, error)
	Start(context.Context, string) error
	Stop(context.Context, string) error
}

type SystemClock struct{}

func (SystemClock) Now() time.Time {
	return time.Now()
}
