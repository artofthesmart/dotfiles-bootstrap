package state

import (
	"context"
	"errors"
)

var (
	ErrConsentRequired = errors.New("step consent is required")
	ErrConsentDeclined = errors.New("step consent was declined")
	ErrStepFailed      = errors.New("transaction step failed")
	ErrPersistence     = errors.New("transaction persistence failed")
)

type StepContract struct {
	ID                  string
	Owner               Owner
	Action              string
	Risk                Risk
	PrerequisiteStepIDs []string
	PrerequisiteFactIDs []string
	ProducedFactIDs     []string
	ConsentRequired     bool
	Reversible          bool
	NotReversibleReason string
}

type StepContext struct {
	TransactionID string
	DryRun        bool
	Facts         map[string]SafeScalar
}

type CheckResult struct {
	AlreadySatisfied          bool
	RecoveredMutationAssetIDs []string
	Summary                   string
	Evidence                  []Evidence
	Facts                     map[string]SafeScalar
}

type PlanResult struct {
	Summary  string
	Evidence []Evidence
}

type ApplyResult struct {
	Summary          string
	Evidence         []Evidence
	MutationAssetIDs []string
}

type VerifyResult struct {
	Summary  string
	Evidence []Evidence
	Facts    map[string]SafeScalar
	Assets   []ManagedAsset
}

type UndoResult struct {
	Summary  string
	Evidence []Evidence
}

type Step interface {
	Contract() StepContract
	Check(context.Context, StepContext) (CheckResult, error)
	Plan(context.Context, StepContext) (PlanResult, error)
	Apply(context.Context, StepContext) (ApplyResult, error)
	Verify(context.Context, StepContext) (VerifyResult, error)
	Undo(context.Context, StepContext) (UndoResult, error)
}

type StepError struct {
	Issue Issue
	Err   error
}

func (e *StepError) Error() string {
	if e == nil {
		return ErrStepFailed.Error()
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return ErrStepFailed.Error()
}

func (e *StepError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
