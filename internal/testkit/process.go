package testkit

import (
	"context"
	"sync"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

type ProcessStart struct {
	Command platform.Command
	State   platform.ProcessState
	Err     error
}

type ScriptedProcessManager struct {
	mu          sync.Mutex
	Starts      []ProcessStart
	StartCalls  []platform.Command
	Inspections map[int]platform.ProcessState
	Terminated  []int
}

func (manager *ScriptedProcessManager) Start(ctx context.Context, command platform.Command) (platform.ProcessState, error) {
	if err := ctx.Err(); err != nil {
		return platform.ProcessState{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.Starts) == 0 {
		return platform.ProcessState{}, ErrNoScriptedResult
	}
	result := manager.Starts[0]
	manager.Starts = manager.Starts[1:]
	manager.StartCalls = append(manager.StartCalls, cloneCommand(command))
	return result.State, result.Err
}

func (manager *ScriptedProcessManager) Inspect(pid int) (platform.ProcessState, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	state, ok := manager.Inspections[pid]
	if !ok {
		return platform.ProcessState{PID: pid}, ErrNoScriptedResult
	}
	return state, nil
}

func (manager *ScriptedProcessManager) Terminate(ctx context.Context, pid int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.Terminated = append(manager.Terminated, pid)
	return nil
}

func (manager *ScriptedProcessManager) StartedCommands() []platform.Command {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	commands := make([]platform.Command, 0, len(manager.StartCalls))
	for _, command := range manager.StartCalls {
		commands = append(commands, cloneCommand(command))
	}
	return commands
}

type ScriptedService struct {
	mu          sync.Mutex
	State       platform.ServiceState
	StatusErr   error
	StartErr    error
	StopErr     error
	StatusCalls int
	StartCalls  int
	StopCalls   int
}

func (service *ScriptedService) Status(ctx context.Context, _ string) (platform.ServiceState, error) {
	if err := ctx.Err(); err != nil {
		return platform.ServiceUnknown, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	service.StatusCalls++
	return service.State, service.StatusErr
}

func (service *ScriptedService) Start(ctx context.Context, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	service.StartCalls++
	if service.StartErr == nil {
		service.State = platform.ServiceRunning
	}
	return service.StartErr
}

func (service *ScriptedService) Stop(ctx context.Context, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	service.StopCalls++
	if service.StopErr == nil {
		service.State = platform.ServiceStopped
	}
	return service.StopErr
}

func cloneCommand(command platform.Command) platform.Command {
	command.Args = append([]string(nil), command.Args...)
	command.Env = append([]string(nil), command.Env...)
	return command
}
