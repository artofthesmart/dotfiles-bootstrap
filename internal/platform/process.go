package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

type OSProcessManager struct {
	Environment EnvironmentBuilder
}

func NewOSProcessManager() OSProcessManager {
	return OSProcessManager{Environment: NewEnvironmentBuilder()}
}

func (manager OSProcessManager) Start(ctx context.Context, command Command) (ProcessState, error) {
	if command.Name == "" {
		return ProcessState{}, &CommandStartError{Name: command.Name, Err: errors.New("empty command name")}
	}
	process := exec.CommandContext(ctx, command.Name, command.Args...)
	process.Dir = command.Dir
	process.Env = manager.Environment.Build(command.Env)
	process.Stdin = command.Stdin
	process.Stdout = command.Stdout
	process.Stderr = command.Stderr
	if err := process.Start(); err != nil {
		return ProcessState{}, &CommandStartError{Name: command.Name, Err: err}
	}
	go func() {
		_ = process.Wait()
	}()
	return ProcessState{PID: process.Process.Pid, Running: true}, nil
}

func (OSProcessManager) Inspect(pid int) (ProcessState, error) {
	if pid <= 0 {
		return ProcessState{}, errors.New("invalid process id")
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return ProcessState{PID: pid}, err
	}
	running, err := processRunning(process)
	return ProcessState{PID: pid, Running: running}, err
}

func (OSProcessManager) Terminate(ctx context.Context, pid int) error {
	if pid <= 0 {
		return errors.New("invalid process id")
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := process.Signal(os.Interrupt); err != nil {
		if err := process.Kill(); err != nil {
			return err
		}
		return nil
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		running, inspectErr := processRunning(process)
		if inspectErr == nil && !running {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type CommandService struct {
	Runner      Runner
	StatusCalls map[string]Command
	StartCalls  map[string]Command
	StopCalls   map[string]Command
}

func (service CommandService) Status(ctx context.Context, name string) (ServiceState, error) {
	command, ok := service.StatusCalls[name]
	if !ok {
		return ServiceUnknown, fmt.Errorf("%w: service %q", ErrUnavailable, name)
	}
	result, err := service.Runner.Run(ctx, command)
	if err != nil {
		return ServiceUnknown, err
	}
	if result.ExitCode == 0 {
		return ServiceRunning, nil
	}
	return ServiceStopped, nil
}

func (service CommandService) Start(ctx context.Context, name string) error {
	return service.run(ctx, service.StartCalls, name)
}

func (service CommandService) Stop(ctx context.Context, name string) error {
	return service.run(ctx, service.StopCalls, name)
}

func (service CommandService) run(ctx context.Context, calls map[string]Command, name string) error {
	command, ok := calls[name]
	if !ok {
		return fmt.Errorf("%w: service %q", ErrUnavailable, name)
	}
	result, err := service.Runner.Run(ctx, command)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("service %q command exited with code %d", name, result.ExitCode)
	}
	return nil
}
