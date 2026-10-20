package testkit

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

var ErrNoScriptedResult = errors.New("no scripted result remains")

type CommandCall struct {
	Name  string
	Args  []string
	Dir   string
	Env   []string
	Stdin []byte
}

type CommandOutcome struct {
	Result              platform.CommandResult
	Err                 error
	TimedOut            bool
	Cancelled           bool
	WaitForCancellation bool
	AfterError          error
}

type ScriptedRunner struct {
	mu       sync.Mutex
	outcomes []CommandOutcome
	calls    []CommandCall
}

func NewScriptedRunner(outcomes ...CommandOutcome) *ScriptedRunner {
	return &ScriptedRunner{outcomes: append([]CommandOutcome(nil), outcomes...)}
}

func (r *ScriptedRunner) Queue(outcomes ...CommandOutcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outcomes = append(r.outcomes, outcomes...)
}

func (r *ScriptedRunner) Run(ctx context.Context, command platform.Command) (platform.CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return platform.CommandResult{}, err
	}

	var stdin []byte
	var err error
	if command.Stdin != nil {
		stdin, err = io.ReadAll(command.Stdin)
		if err != nil {
			return platform.CommandResult{}, err
		}
	}

	r.mu.Lock()
	r.calls = append(r.calls, CommandCall{
		Name:  command.Name,
		Args:  append([]string(nil), command.Args...),
		Dir:   command.Dir,
		Env:   append([]string(nil), command.Env...),
		Stdin: append([]byte(nil), stdin...),
	})
	if len(r.outcomes) == 0 {
		r.mu.Unlock()
		return platform.CommandResult{}, ErrNoScriptedResult
	}
	outcome := r.outcomes[0]
	r.outcomes = r.outcomes[1:]
	r.mu.Unlock()

	if outcome.WaitForCancellation {
		<-ctx.Done()
		return cloneCommandResult(outcome.Result), ctx.Err()
	}
	if outcome.Cancelled {
		return cloneCommandResult(outcome.Result), context.Canceled
	}
	if outcome.TimedOut {
		return cloneCommandResult(outcome.Result), context.DeadlineExceeded
	}
	if outcome.AfterError != nil {
		return cloneCommandResult(outcome.Result), outcome.AfterError
	}
	return cloneCommandResult(outcome.Result), outcome.Err
}

func (r *ScriptedRunner) Calls() []CommandCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	calls := make([]CommandCall, len(r.calls))
	for index, call := range r.calls {
		calls[index] = CommandCall{
			Name:  call.Name,
			Args:  append([]string(nil), call.Args...),
			Dir:   call.Dir,
			Env:   append([]string(nil), call.Env...),
			Stdin: append([]byte(nil), call.Stdin...),
		}
	}
	return calls
}

func (r *ScriptedRunner) Remaining() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.outcomes)
}

func cloneCommandResult(result platform.CommandResult) platform.CommandResult {
	return platform.CommandResult{
		ExitCode: result.ExitCode,
		Stdout:   append([]byte(nil), result.Stdout...),
		Stderr:   append([]byte(nil), result.Stderr...),
	}
}
