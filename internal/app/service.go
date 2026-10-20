package app

import (
	"context"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

type Service interface {
	Execute(context.Context, Operation) Result
}

type PlaceholderService struct {
	Clock    Clock
	Platform platform.Capabilities
}

func (s PlaceholderService) Execute(_ context.Context, operation Operation) Result {
	issue := Issue{
		Code:     IssueInternal,
		Severity: SeverityError,
		Domain:   "internal",
		Summary:  "This command is not implemented in the application scaffold.",
		Remediations: []Remediation{{
			Kind:       "documentation",
			Summary:    "Use the existing setup scripts until the corresponding manager lifecycle is implemented.",
			SafeForYes: false,
		}},
	}

	return Result{
		SchemaVersion: EventSchemaVersion,
		RecordType:    "result",
		Sequence:      0,
		Timestamp:     s.Clock.Now().UTC(),
		Command:       operation.Command,
		Outcome:       OutcomeFailed,
		ExitCode:      ExitForIssue(issue.Code),
		Summary:       "The requested command is not implemented.",
		Issues:        []Issue{issue},
		Components:    []Component{},
		ManualActions: []ManualAction{},
	}
}
