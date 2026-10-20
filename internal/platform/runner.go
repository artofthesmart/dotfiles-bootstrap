package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const (
	DefaultCommandTimeout = 30 * time.Second
	DefaultOutputLimit    = int64(1 << 20)
)

type CommandStartError struct {
	Name string
	Err  error
}

func (err *CommandStartError) Error() string {
	return fmt.Sprintf("start command %q: %v", err.Name, err.Err)
}

func (err *CommandStartError) Unwrap() error {
	return err.Err
}

type CommandRunner struct {
	Environment EnvironmentBuilder
}

func NewCommandRunner() CommandRunner {
	return CommandRunner{Environment: NewEnvironmentBuilder()}
}

func (runner CommandRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	if command.Name == "" {
		return CommandResult{ExitCode: -1}, &CommandStartError{Name: command.Name, Err: errors.New("empty command name")}
	}
	if command.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, command.Timeout)
		defer cancel()
	} else if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultCommandTimeout)
		defer cancel()
	}
	limit := command.OutputLimit
	if limit <= 0 {
		limit = DefaultOutputLimit
	}
	stdout := newBoundedBuffer(limit)
	stderr := newBoundedBuffer(limit)
	process := exec.CommandContext(ctx, command.Name, command.Args...)
	process.Dir = command.Dir
	process.Env = runner.Environment.Build(command.Env)
	process.Stdin = command.Stdin
	process.Stdout = stdout
	process.Stderr = stderr
	err := process.Run()
	result := CommandResult{
		ExitCode:        0,
		Stdout:          stdout.Bytes(),
		Stderr:          stderr.Bytes(),
		StdoutTruncated: stdout.Truncated(),
		StderrTruncated: stderr.Truncated(),
	}
	if process.ProcessState != nil {
		result.ExitCode = process.ProcessState.ExitCode()
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if result.ExitCode == 0 {
			result.ExitCode = -1
		}
		return result, ctxErr
	}
	if err == nil {
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return result, nil
	}
	result.ExitCode = -1
	return result, &CommandStartError{Name: command.Name, Err: err}
}

type boundedBuffer struct {
	limit     int64
	buffer    bytes.Buffer
	written   int64
	truncated bool
}

func newBoundedBuffer(limit int64) *boundedBuffer {
	return &boundedBuffer{limit: limit}
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	count := len(data)
	remaining := buffer.limit - buffer.written
	if remaining > 0 {
		if int64(len(data)) > remaining {
			data = data[:remaining]
		}
		_, _ = buffer.buffer.Write(data)
		buffer.written += int64(len(data))
	}
	if int64(count) > remaining {
		buffer.truncated = true
	}
	return count, nil
}

func (buffer *boundedBuffer) Bytes() []byte {
	return append([]byte(nil), buffer.buffer.Bytes()...)
}

func (buffer *boundedBuffer) Truncated() bool {
	return buffer.truncated
}
