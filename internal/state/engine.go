package state

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Clock interface {
	Now() time.Time
}

type TransactionSpec struct {
	ManagerVersion        string
	TransactionID         string
	Command               Command
	EffectiveConfigDigest string
	Host                  Host
	SelectedCatalogID     *string
	GeneratedAlias        *string
}

type ExecuteOptions struct {
	DryRun            bool
	Resume            bool
	Restart           bool
	RollbackOnFailure bool
	Consent           map[string]ConsentDecision
}

type ExecuteRequest struct {
	Spec    TransactionSpec
	Steps   []Step
	Options ExecuteOptions
}

type Engine struct {
	Store  Store
	Clock  Clock
	Events EventSink
}

func (e Engine) Execute(ctx context.Context, request ExecuteRequest) (Transaction, error) {
	if e.Store == nil || e.Clock == nil {
		return Transaction{}, fmt.Errorf("%w: engine store and clock are required", ErrInvalidState)
	}
	if request.Options.Resume && request.Options.Restart {
		return Transaction{}, fmt.Errorf("%w: resume and restart are mutually exclusive", ErrInvalidState)
	}
	if err := validateStepContracts(request.Steps); err != nil {
		return Transaction{}, err
	}

	current, loadErr := e.Store.Load(ctx)
	switch {
	case loadErr == nil:
		if request.Options.Restart {
			if current.Status != StatusArchived {
				if err := e.mutateAndSave(ctx, &current, func() error {
					return current.Transition(StatusArchived, e.now())
				}); err != nil {
					return current, err
				}
			}
			if _, err := e.Store.Archive(ctx); err != nil {
				return current, err
			}
			current = newTransaction(request.Spec, request.Steps, e.now())
			if err := e.Store.Save(ctx, &current); err != nil {
				return current, err
			}
		} else if request.Options.Resume {
			if err := validateResumeIdentity(current, request.Spec.Command, request.Spec.EffectiveConfigDigest); err != nil {
				return current, err
			}
			if current.Status == StatusRunning || current.Status == StatusVerifying {
				if err := e.mutateAndSave(ctx, &current, func() error {
					resetInterruptedPhases(&current)
					return current.Transition(StatusInterrupted, e.now())
				}); err != nil {
					return current, err
				}
			}
			if err := ValidateResume(current, request.Spec.Command, request.Spec.EffectiveConfigDigest); err != nil {
				return current, err
			}
			if err := ensureResumeContracts(current, request.Steps); err != nil {
				return current, err
			}
			if current.Rollback.Status == RollbackRunning {
				return e.rollback(ctx, current, request.Steps)
			}
		} else {
			return current, ErrActiveTransaction
		}
	case errors.Is(loadErr, ErrNoState):
		if request.Options.Resume {
			return Transaction{}, fmt.Errorf("%w: no state to resume", ErrIncompatibleResume)
		}
		current = newTransaction(request.Spec, request.Steps, e.now())
		if err := e.Store.Save(ctx, &current); err != nil {
			return current, err
		}
	default:
		return Transaction{}, loadErr
	}

	if err := e.syncManualActions(ctx, &current, request.Steps); err != nil {
		return current, err
	}

	if request.Options.DryRun {
		return e.executeDryRun(ctx, current, request.Steps)
	}

	if current.Status == StatusPlanned || current.Status == StatusInterrupted || current.Status == StatusAwaitingManual {
		if err := e.mutateAndSave(ctx, &current, func() error {
			return current.Transition(StatusRunning, e.now())
		}); err != nil {
			return current, err
		}
	}
	if err := e.emit(current, "", EventOperationStarted, MessageOperationStarted, EventAttributes{Status: string(current.Status)}); err != nil {
		return current, err
	}

	facts := factsFromReceipts(current)
	for index, step := range request.Steps {
		receipt := &current.Steps[index]
		if receipt.Status == StepVerified || receipt.Status == StepSkippedVerified {
			for _, factID := range receipt.ProducedFactIDs {
				facts[factID] = true
			}
			continue
		}
		if err := requirePrerequisites(current, step.Contract(), facts); err != nil {
			if persistErr := e.failStep(ctx, &current, index, PhaseCheck, err); persistErr != nil {
				return current, persistErr
			}
			return e.rollbackIfRequested(ctx, current, request.Steps, request.Options, err)
		}
		if err := ctx.Err(); err != nil {
			return e.interrupt(ctx, current, err)
		}

		stepContext := StepContext{TransactionID: current.TransactionID, Facts: cloneFacts(facts)}
		check, err := e.runCheck(ctx, &current, index, step, stepContext)
		if err != nil {
			return e.handleStepError(ctx, current, request.Steps, request.Options, index, PhaseCheck, err)
		}
		mergeFacts(facts, check.Facts)

		skippedMutation := check.AlreadySatisfied
		if check.AlreadySatisfied {
			if err := e.skipPhase(ctx, &current, index, PhasePlan, "Already satisfied; planning was not required."); err != nil {
				return current, err
			}
			if len(check.RecoveredMutationAssetIDs) > 0 {
				if err := e.recoverApply(ctx, &current, index, check.RecoveredMutationAssetIDs); err != nil {
					return current, err
				}
				skippedMutation = false
			} else {
				if err := e.skipPhase(ctx, &current, index, PhaseApply, "Already satisfied; no mutation was performed."); err != nil {
					return current, err
				}
			}
		} else {
			if _, err := e.runPlan(ctx, &current, index, step, stepContext); err != nil {
				return e.handleStepError(ctx, current, request.Steps, request.Options, index, PhasePlan, err)
			}
			decision := request.Options.Consent[step.Contract().ID]
			if step.Contract().ConsentRequired {
				switch decision {
				case ConsentAccepted:
					if err := e.recordConsent(ctx, &current, index, ConsentAccepted); err != nil {
						return current, err
					}
				case ConsentDeclined:
					if err := e.recordConsent(ctx, &current, index, ConsentDeclined); err != nil {
						return current, err
					}
					return e.handleStepError(ctx, current, request.Steps, request.Options, index, PhasePlan, ErrConsentDeclined)
				default:
					if err := e.awaitConsent(ctx, &current, index); err != nil {
						return current, err
					}
					return current, ErrConsentRequired
				}
			}
			if _, err := e.runApply(ctx, &current, index, step, stepContext); err != nil {
				return e.handleStepError(ctx, current, request.Steps, request.Options, index, PhaseApply, err)
			}
		}

		verify, err := e.runVerify(ctx, &current, index, step, stepContext)
		if err != nil {
			return e.handleStepError(ctx, current, request.Steps, request.Options, index, PhaseVerify, err)
		}
		if err := e.commitVerification(ctx, &current, index, step.Contract(), verify, skippedMutation); err != nil {
			return current, err
		}
		mergeFacts(facts, verify.Facts)
	}

	if err := e.mutateAndSave(ctx, &current, func() error {
		return current.Transition(StatusVerifying, e.now())
	}); err != nil {
		return current, err
	}
	if err := verifyTransactionComplete(current); err != nil {
		if persistErr := e.mutateAndSave(ctx, &current, func() error {
			current.Issues = append(current.Issues, issueForError(err))
			return current.Transition(StatusFailed, e.now())
		}); persistErr != nil {
			return current, persistErr
		}
		return e.rollbackIfRequested(ctx, current, request.Steps, request.Options, err)
	}
	if err := e.mutateAndSave(ctx, &current, func() error {
		return current.Transition(StatusSucceeded, e.now())
	}); err != nil {
		return current, err
	}
	if err := e.emit(current, "", EventOperationCompleted, MessageOperationCompleted, EventAttributes{Status: string(current.Status), Verified: boolPointer(true)}); err != nil {
		return current, err
	}
	return current, nil
}

func (e Engine) executeDryRun(ctx context.Context, transaction Transaction, steps []Step) (Transaction, error) {
	facts := factsFromReceipts(transaction)
	for index, step := range steps {
		receipt := &transaction.Steps[index]
		if receipt.Status == StepVerified || receipt.Status == StepSkippedVerified {
			continue
		}
		if err := requirePrerequisites(transaction, step.Contract(), facts); err != nil {
			return transaction, err
		}
		stepContext := StepContext{TransactionID: transaction.TransactionID, DryRun: true, Facts: cloneFacts(facts)}
		check, err := e.runCheck(ctx, &transaction, index, step, stepContext)
		if err != nil {
			return transaction, err
		}
		mergeFacts(facts, check.Facts)
		if check.AlreadySatisfied {
			if err := e.skipPhase(ctx, &transaction, index, PhasePlan, "Already satisfied; planning was not required."); err != nil {
				return transaction, err
			}
		} else if _, err := e.runPlan(ctx, &transaction, index, step, stepContext); err != nil {
			return transaction, err
		}
		if err := e.mutateAndSave(ctx, &transaction, func() error {
			transaction.Steps[index].Status = StepPending
			return nil
		}); err != nil {
			return transaction, err
		}
	}
	return transaction, nil
}

func (e Engine) runCheck(ctx context.Context, transaction *Transaction, index int, step Step, stepContext StepContext) (CheckResult, error) {
	if err := e.startPhase(ctx, transaction, index, PhaseCheck); err != nil {
		return CheckResult{}, err
	}
	result, err := step.Check(ctx, stepContext)
	if err != nil {
		return CheckResult{}, err
	}
	if err := validateEvidence(result.Evidence); err != nil {
		return CheckResult{}, err
	}
	if err := validateFacts(result.Facts, step.Contract()); err != nil {
		return CheckResult{}, err
	}
	if err := validateIDs("recovered mutation asset ids", result.RecoveredMutationAssetIDs, assetIDPattern); err != nil {
		return CheckResult{}, err
	}
	if err := e.completePhase(ctx, transaction, index, PhaseCheck, "The check completed.", result.Evidence); err != nil {
		return CheckResult{}, err
	}
	return result, nil
}

func (e Engine) runPlan(ctx context.Context, transaction *Transaction, index int, step Step, stepContext StepContext) (PlanResult, error) {
	if err := e.startPhase(ctx, transaction, index, PhasePlan); err != nil {
		return PlanResult{}, err
	}
	result, err := step.Plan(ctx, stepContext)
	if err != nil {
		return PlanResult{}, err
	}
	if err := validateEvidence(result.Evidence); err != nil {
		return PlanResult{}, err
	}
	if err := e.completePhase(ctx, transaction, index, PhasePlan, "The plan completed.", result.Evidence); err != nil {
		return PlanResult{}, err
	}
	return result, nil
}

func (e Engine) runApply(ctx context.Context, transaction *Transaction, index int, step Step, stepContext StepContext) (ApplyResult, error) {
	if err := e.startPhase(ctx, transaction, index, PhaseApply); err != nil {
		return ApplyResult{}, err
	}
	result, err := step.Apply(ctx, stepContext)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := validateEvidence(result.Evidence); err != nil {
		return ApplyResult{}, err
	}
	if err := validateIDs("mutation_asset_ids", result.MutationAssetIDs, assetIDPattern); err != nil {
		return ApplyResult{}, err
	}
	if err := e.mutateAndSave(ctx, transaction, func() error {
		receipt := &transaction.Steps[index]
		phase := receipt.Phases.Get(PhaseApply)
		now := e.now()
		phase.Status = PhaseSucceeded
		phase.CompletedAt = &now
		phase.Summary = stringPointer("The apply contract completed.")
		phase.Evidence = cloneEvidence(result.Evidence)
		receipt.MutationAssetIDs = cloneStrings(result.MutationAssetIDs)
		return nil
	}); err != nil {
		return ApplyResult{}, err
	}
	return result, nil
}

func (e Engine) recoverApply(ctx context.Context, transaction *Transaction, index int, assetIDs []string) error {
	return e.mutateAndSave(ctx, transaction, func() error {
		receipt := &transaction.Steps[index]
		phase := receipt.Phases.Get(PhaseApply)
		if phase.Status != PhaseRunning {
			return invalid("apply recovery", "only an interrupted running apply can be recovered")
		}
		now := e.now()
		phase.Status = PhaseSucceeded
		phase.CompletedAt = &now
		phase.Summary = stringPointer("The interrupted mutation was rediscovered and requires independent verification.")
		phase.Evidence = []Evidence{}
		receipt.MutationAssetIDs = cloneStrings(assetIDs)
		return nil
	})
}

func (e Engine) runVerify(ctx context.Context, transaction *Transaction, index int, step Step, stepContext StepContext) (VerifyResult, error) {
	if err := e.startPhase(ctx, transaction, index, PhaseVerify); err != nil {
		return VerifyResult{}, err
	}
	result, err := step.Verify(ctx, stepContext)
	if err != nil {
		return VerifyResult{}, err
	}
	if err := validateEvidence(result.Evidence); err != nil {
		return VerifyResult{}, err
	}
	if err := validateFacts(result.Facts, step.Contract()); err != nil {
		return VerifyResult{}, err
	}
	for _, factID := range step.Contract().ProducedFactIDs {
		if _, exists := result.Facts[factID]; !exists {
			return VerifyResult{}, invalid("produced facts", "verification did not produce "+factID)
		}
	}
	if err := e.completePhase(ctx, transaction, index, PhaseVerify, "The verification contract completed.", result.Evidence); err != nil {
		return VerifyResult{}, err
	}
	return result, nil
}

func (e Engine) commitVerification(ctx context.Context, transaction *Transaction, index int, contract StepContract, result VerifyResult, skipped bool) error {
	return e.mutateAndSave(ctx, transaction, func() error {
		receipt := &transaction.Steps[index]
		if skipped {
			receipt.Status = StepSkippedVerified
		} else {
			receipt.Status = StepVerified
		}
		for _, asset := range result.Assets {
			if asset.Ownership == OwnershipManagerOwned {
				if skipped {
					return invalid("asset.ownership", "a skipped mutation cannot create manager ownership")
				}
				if asset.CreatedOrAdoptedByStep == nil || *asset.CreatedOrAdoptedByStep != contract.ID {
					return invalid("asset.ownership", "manager-owned asset must identify its verified creating step")
				}
				if !contains(receipt.MutationAssetIDs, asset.AssetID) {
					return invalid("asset.ownership", "verified asset was not declared by apply")
				}
			}
			if err := upsertAsset(transaction, asset); err != nil {
				return err
			}
		}
		if contract.Reversible && len(managerMutationIDs(*transaction, contract.ID)) > 0 {
			transaction.Rollback.Available = true
			transaction.Rollback.Status = RollbackAvailable
			if !contains(transaction.Rollback.StepIDs, contract.ID) {
				transaction.Rollback.StepIDs = append(transaction.Rollback.StepIDs, contract.ID)
			}
		}
		return nil
	})
}

func (e Engine) startPhase(ctx context.Context, transaction *Transaction, index int, phaseName PhaseName) error {
	if err := e.mutateAndSave(ctx, transaction, func() error {
		receipt := &transaction.Steps[index]
		phase := receipt.Phases.Get(phaseName)
		now := e.now()
		phase.Status = PhaseRunning
		phase.StartedAt = &now
		phase.CompletedAt = nil
		phase.Summary = nil
		phase.Evidence = []Evidence{}
		receipt.Status = StepRunning
		return nil
	}); err != nil {
		return err
	}
	return e.emit(*transaction, transaction.Steps[index].StepID, EventPhaseStarted, MessagePhaseStarted, EventAttributes{Phase: string(phaseName), Status: string(PhaseRunning)})
}

func (e Engine) completePhase(ctx context.Context, transaction *Transaction, index int, phaseName PhaseName, summary string, evidence []Evidence) error {
	if err := e.mutateAndSave(ctx, transaction, func() error {
		phase := transaction.Steps[index].Phases.Get(phaseName)
		now := e.now()
		phase.Status = PhaseSucceeded
		phase.CompletedAt = &now
		phase.Summary = safeSummary(summary)
		phase.Evidence = cloneEvidence(evidence)
		return nil
	}); err != nil {
		return err
	}
	return e.emit(*transaction, transaction.Steps[index].StepID, EventPhaseCompleted, MessagePhaseCompleted, EventAttributes{Phase: string(phaseName), Status: string(PhaseSucceeded)})
}

func (e Engine) skipPhase(ctx context.Context, transaction *Transaction, index int, phaseName PhaseName, summary string) error {
	return e.mutateAndSave(ctx, transaction, func() error {
		phase := transaction.Steps[index].Phases.Get(phaseName)
		now := e.now()
		phase.Status = PhaseSkipped
		phase.StartedAt = nil
		phase.CompletedAt = &now
		phase.Summary = safeSummary(summary)
		phase.Evidence = []Evidence{}
		return nil
	})
}

func (e Engine) recordConsent(ctx context.Context, transaction *Transaction, index int, decision ConsentDecision) error {
	return e.mutateAndSave(ctx, transaction, func() error {
		now := e.now()
		transaction.Steps[index].Consent = &Consent{Required: true, Decision: decision, DecidedAt: &now}
		return nil
	})
}

func (e Engine) awaitConsent(ctx context.Context, transaction *Transaction, index int) error {
	return e.mutateAndSave(ctx, transaction, func() error {
		transaction.Steps[index].Status = StepAwaitingManual
		transaction.Steps[index].Consent = &Consent{Required: true, Decision: ConsentPending}
		return transaction.Transition(StatusAwaitingManual, e.now())
	})
}

func (e Engine) failStep(ctx context.Context, transaction *Transaction, index int, phaseName PhaseName, failure error) error {
	return e.mutateAndSave(ctx, transaction, func() error {
		receipt := &transaction.Steps[index]
		phase := receipt.Phases.Get(phaseName)
		now := e.now()
		if phase.Status == PhaseRunning {
			phase.Status = PhaseFailed
			phase.CompletedAt = &now
			phase.Summary = stringPointer("The phase did not complete successfully.")
			phase.Evidence = []Evidence{}
		}
		receipt.Status = StepFailed
		issue := issueForError(failure)
		receipt.Issues = append(receipt.Issues, issue)
		transaction.Issues = append(transaction.Issues, issue)
		return transaction.Transition(StatusFailed, now)
	})
}

func (e Engine) handleStepError(ctx context.Context, transaction Transaction, steps []Step, options ExecuteOptions, index int, phase PhaseName, failure error) (Transaction, error) {
	if errors.Is(failure, context.Canceled) || errors.Is(failure, context.DeadlineExceeded) || errors.Is(failure, ErrPersistence) || ctx.Err() != nil {
		return e.interrupt(context.WithoutCancel(ctx), transaction, failure)
	}
	if err := e.failStep(ctx, &transaction, index, phase, failure); err != nil {
		return transaction, err
	}
	return e.rollbackIfRequested(ctx, transaction, steps, options, failure)
}

func (e Engine) interrupt(ctx context.Context, transaction Transaction, cause error) (Transaction, error) {
	saveContext := context.WithoutCancel(ctx)
	if transaction.Status == StatusRunning || transaction.Status == StatusVerifying || transaction.Status == StatusRollingBack {
		if err := e.mutateAndSave(saveContext, &transaction, func() error {
			return transaction.Transition(StatusInterrupted, e.now())
		}); err != nil {
			return transaction, errors.Join(cause, err)
		}
	}
	return transaction, cause
}

func (e Engine) rollbackIfRequested(ctx context.Context, transaction Transaction, steps []Step, options ExecuteOptions, cause error) (Transaction, error) {
	if !options.RollbackOnFailure {
		return transaction, cause
	}
	rolledBack, err := e.rollback(ctx, transaction, steps)
	if err != nil {
		return rolledBack, errors.Join(cause, err)
	}
	return rolledBack, cause
}

func (e Engine) rollback(ctx context.Context, transaction Transaction, steps []Step) (Transaction, error) {
	stepByID := make(map[string]Step, len(steps))
	for _, step := range steps {
		stepByID[step.Contract().ID] = step
	}
	eligible := make([]int, 0)
	for index := len(transaction.Steps) - 1; index >= 0; index-- {
		receipt := transaction.Steps[index]
		step := stepByID[receipt.StepID]
		if step == nil || !step.Contract().Reversible || (receipt.Status != StepVerified && receipt.Status != StepRunning && receipt.Status != StepUndoFailed) {
			continue
		}
		if receipt.Phases.Apply.Status != PhaseSucceeded || receipt.Phases.Verify.Status != PhaseSucceeded {
			continue
		}
		if len(managerMutationIDs(transaction, receipt.StepID)) == 0 {
			continue
		}
		eligible = append(eligible, index)
	}
	if len(eligible) == 0 {
		return transaction, nil
	}
	if err := e.mutateAndSave(ctx, &transaction, func() error {
		transaction.Rollback.Available = true
		transaction.Rollback.Status = RollbackRunning
		transaction.Rollback.StepIDs = []string{}
		for _, index := range eligible {
			transaction.Rollback.StepIDs = append(transaction.Rollback.StepIDs, transaction.Steps[index].StepID)
		}
		return transaction.Transition(StatusRollingBack, e.now())
	}); err != nil {
		return transaction, err
	}

	failures := 0
	successes := 0
	for _, index := range eligible {
		step := stepByID[transaction.Steps[index].StepID]
		if err := e.startPhase(ctx, &transaction, index, PhaseUndo); err != nil {
			return transaction, err
		}
		result, err := step.Undo(ctx, StepContext{TransactionID: transaction.TransactionID, Facts: factsFromReceipts(transaction)})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				return e.interrupt(context.WithoutCancel(ctx), transaction, err)
			}
			failures++
			if persistErr := e.mutateAndSave(ctx, &transaction, func() error {
				receipt := &transaction.Steps[index]
				phase := receipt.Phases.Get(PhaseUndo)
				now := e.now()
				phase.Status = PhaseFailed
				phase.CompletedAt = &now
				phase.Summary = stringPointer("The undo contract did not complete successfully.")
				receipt.Status = StepUndoFailed
				return nil
			}); persistErr != nil {
				return transaction, persistErr
			}
			continue
		}
		if err := validateEvidence(result.Evidence); err != nil {
			failures++
			if persistErr := e.mutateAndSave(ctx, &transaction, func() error {
				receipt := &transaction.Steps[index]
				phase := receipt.Phases.Get(PhaseUndo)
				now := e.now()
				phase.Status = PhaseFailed
				phase.CompletedAt = &now
				phase.Summary = stringPointer("The undo evidence was invalid.")
				phase.Evidence = []Evidence{}
				receipt.Status = StepUndoFailed
				return nil
			}); persistErr != nil {
				return transaction, persistErr
			}
			continue
		}
		if err := e.mutateAndSave(ctx, &transaction, func() error {
			receipt := &transaction.Steps[index]
			phase := receipt.Phases.Get(PhaseUndo)
			now := e.now()
			phase.Status = PhaseSucceeded
			phase.CompletedAt = &now
			phase.Summary = stringPointer("The undo contract completed.")
			phase.Evidence = cloneEvidence(result.Evidence)
			receipt.Status = StepUndone
			removeManagerAssets(&transaction, receipt.StepID)
			return nil
		}); err != nil {
			return transaction, err
		}
		successes++
	}

	if failures == 0 {
		if err := e.mutateAndSave(ctx, &transaction, func() error {
			transaction.Rollback.Available = false
			transaction.Rollback.Status = RollbackSucceeded
			return transaction.Transition(StatusRolledBack, e.now())
		}); err != nil {
			return transaction, err
		}
		return transaction, nil
	}
	if err := e.mutateAndSave(ctx, &transaction, func() error {
		transaction.Rollback.Available = false
		if successes > 0 {
			transaction.Rollback.Status = RollbackPartial
		} else {
			transaction.Rollback.Status = RollbackFailed
		}
		return transaction.Transition(StatusRollbackFailed, e.now())
	}); err != nil {
		return transaction, err
	}
	return transaction, fmt.Errorf("%w: %d undo contract(s) failed", ErrStepFailed, failures)
}

func (e Engine) mutateAndSave(ctx context.Context, transaction *Transaction, mutate func() error) error {
	before, err := cloneTransaction(*transaction)
	if err != nil {
		return err
	}
	if err := mutate(); err != nil {
		*transaction = before
		return err
	}
	if err := e.Store.Save(ctx, transaction); err != nil {
		*transaction = before
		return fmt.Errorf("%w: %v", ErrPersistence, err)
	}
	return nil
}

func (e Engine) now() time.Time {
	return e.Clock.Now().UTC()
}

func newTransaction(spec TransactionSpec, steps []Step, now time.Time) Transaction {
	receipts := make([]StepReceipt, 0, len(steps))
	manualActions := make([]ManualAction, 0)
	for _, step := range steps {
		contract := step.Contract()
		consent := &Consent{Required: contract.ConsentRequired, Decision: ConsentNotRequired}
		if contract.ConsentRequired {
			consent.Decision = ConsentPending
		}
		receipts = append(receipts, StepReceipt{
			ReceiptVersion:      1,
			StepID:              contract.ID,
			Owner:               contract.Owner,
			Action:              contract.Action,
			Risk:                contract.Risk,
			Status:              StepPending,
			PrerequisiteStepIDs: cloneStrings(contract.PrerequisiteStepIDs),
			ProducedFactIDs:     cloneStrings(contract.ProducedFactIDs),
			Consent:             consent,
			Phases: PhaseReceipts{
				Check:  emptyPhase(),
				Plan:   emptyPhase(),
				Apply:  emptyPhase(),
				Verify: emptyPhase(),
				Undo:   emptyPhase(),
			},
			MutationAssetIDs: []string{},
			Issues:           []Issue{},
		})
		if provider, ok := step.(ManualActionProvider); ok {
			manualActions = append(manualActions, cloneManualActions(provider.ManualActions())...)
		}
	}
	return Transaction{
		SchemaVersion:         TransactionSchemaVersion,
		ManagerVersion:        spec.ManagerVersion,
		TransactionID:         spec.TransactionID,
		Command:               spec.Command,
		Status:                StatusPlanned,
		CreatedAt:             now.UTC(),
		UpdatedAt:             now.UTC(),
		EffectiveConfigDigest: spec.EffectiveConfigDigest,
		SelectedCatalogID:     spec.SelectedCatalogID,
		GeneratedAlias:        spec.GeneratedAlias,
		Host:                  spec.Host,
		Steps:                 receipts,
		ManagedAssets:         []ManagedAsset{},
		Issues:                []Issue{},
		ManualActions:         manualActions,
		Rollback:              Rollback{Available: false, Status: RollbackNotNeeded, StepIDs: []string{}},
	}
}

func cloneManualActions(actions []ManualAction) []ManualAction {
	if actions == nil {
		return []ManualAction{}
	}
	cloned := make([]ManualAction, len(actions))
	copy(cloned, actions)
	return cloned
}

func manualActionsFromSteps(steps []Step) []ManualAction {
	actions := make([]ManualAction, 0)
	for _, step := range steps {
		if provider, ok := step.(ManualActionProvider); ok {
			actions = append(actions, cloneManualActions(provider.ManualActions())...)
		}
	}
	return actions
}

func (e Engine) syncManualActions(ctx context.Context, transaction *Transaction, steps []Step) error {
	desired := manualActionsFromSteps(steps)
	if len(desired) != len(transaction.ManualActions) {
		return fmt.Errorf("%w: manual action contract changed", ErrIncompatibleResume)
	}
	changed := false
	for index := range desired {
		current := transaction.ManualActions[index]
		next := desired[index]
		if current.ActionID != next.ActionID || current.IssueCode != next.IssueCode || current.Surface != next.Surface || current.Summary != next.Summary {
			return fmt.Errorf("%w: manual action contract changed", ErrIncompatibleResume)
		}
		if current.Completed != next.Completed {
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return e.mutateAndSave(ctx, transaction, func() error {
		transaction.ManualActions = desired
		return nil
	})
}

func emptyPhase() PhaseReceipt {
	return PhaseReceipt{Status: PhaseNotRun, Evidence: []Evidence{}}
}

func validateStepContracts(steps []Step) error {
	ids := make(map[string]StepContract, len(steps))
	facts := make(map[string]string)
	seenSteps := make(map[string]bool, len(steps))
	seenFacts := make(map[string]bool)
	for _, step := range steps {
		if step == nil {
			return fmt.Errorf("%w: nil step", ErrInvalidState)
		}
		contract := step.Contract()
		if !stepIDPattern.MatchString(contract.ID) || !validOwner(contract.Owner) || !validRisk(contract.Risk) {
			return invalid("step contract", contract.ID)
		}
		if err := validateSafeText("step contract action", contract.Action, 160); err != nil {
			return err
		}
		if _, exists := ids[contract.ID]; exists {
			return invalid("step contract", "duplicate "+contract.ID)
		}
		if !contract.Reversible && contract.Risk != RiskReadOnly && contract.NotReversibleReason == "" {
			return invalid("step contract", "non-reversible mutation requires a reason")
		}
		if err := validateIDs("step prerequisites", contract.PrerequisiteStepIDs, stepIDPattern); err != nil {
			return err
		}
		if err := validateIDs("fact prerequisites", contract.PrerequisiteFactIDs, factIDPattern); err != nil {
			return err
		}
		if err := validateIDs("produced facts", contract.ProducedFactIDs, factIDPattern); err != nil {
			return err
		}
		for _, prerequisite := range contract.PrerequisiteStepIDs {
			if !seenSteps[prerequisite] {
				return invalid("step contract", "prerequisite must precede step: "+prerequisite)
			}
		}
		for _, factID := range contract.PrerequisiteFactIDs {
			if !seenFacts[factID] {
				return invalid("step contract", "prerequisite fact must be produced earlier: "+factID)
			}
		}
		for _, factID := range contract.ProducedFactIDs {
			if producer, exists := facts[factID]; exists {
				return invalid("step contract", "fact "+factID+" also produced by "+producer)
			}
			facts[factID] = contract.ID
			seenFacts[factID] = true
		}
		ids[contract.ID] = contract
		seenSteps[contract.ID] = true
	}
	for _, contract := range ids {
		for _, prerequisite := range contract.PrerequisiteStepIDs {
			if _, exists := ids[prerequisite]; !exists {
				return invalid("step contract", "unknown prerequisite "+prerequisite)
			}
		}
		for _, factID := range contract.PrerequisiteFactIDs {
			if _, exists := facts[factID]; !exists {
				return invalid("step contract", "unknown prerequisite fact "+factID)
			}
		}
	}
	return nil
}

func ensureResumeContracts(transaction Transaction, steps []Step) error {
	if len(transaction.Steps) != len(steps) {
		return fmt.Errorf("%w: step count changed", ErrIncompatibleResume)
	}
	for index, step := range steps {
		contract := step.Contract()
		receipt := transaction.Steps[index]
		if receipt.StepID != contract.ID || receipt.Owner != contract.Owner || receipt.Action != contract.Action || receipt.Risk != contract.Risk {
			return fmt.Errorf("%w: step contract changed at %s", ErrIncompatibleResume, receipt.StepID)
		}
	}
	return nil
}

func validateResumeIdentity(transaction Transaction, command Command, digest string) error {
	if transaction.SchemaVersion != TransactionSchemaVersion {
		return fmt.Errorf("%w: %w", ErrIncompatibleResume, ErrUnsupportedSchema)
	}
	if CommandFamily(transaction.Command) != CommandFamily(command) || CommandFamily(command) == "" {
		return fmt.Errorf("%w: command families differ", ErrIncompatibleResume)
	}
	if transaction.EffectiveConfigDigest != digest {
		return fmt.Errorf("%w: effective configuration digest differs", ErrIncompatibleResume)
	}
	return nil
}

func resetInterruptedPhases(transaction *Transaction) {
	for index := range transaction.Steps {
		receipt := &transaction.Steps[index]
		preserveMutation := receipt.Phases.Apply.Status == PhaseRunning || receipt.Phases.Undo.Status == PhaseRunning
		if receipt.Status == StepRunning && !preserveMutation {
			receipt.Status = StepPending
		}
		for _, name := range []PhaseName{PhaseCheck, PhasePlan, PhaseApply, PhaseVerify, PhaseUndo} {
			phase := receipt.Phases.Get(name)
			if phase.Status == PhaseRunning && name != PhaseApply && name != PhaseUndo {
				*phase = emptyPhase()
			}
		}
	}
}

func requirePrerequisites(transaction Transaction, contract StepContract, facts map[string]SafeScalar) error {
	for _, prerequisite := range contract.PrerequisiteStepIDs {
		found := false
		for _, receipt := range transaction.Steps {
			if receipt.StepID == prerequisite {
				found = receipt.Status == StepVerified || receipt.Status == StepSkippedVerified
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: prerequisite step %s is not verified", ErrStepFailed, prerequisite)
		}
	}
	for _, factID := range contract.PrerequisiteFactIDs {
		if _, exists := facts[factID]; !exists {
			return fmt.Errorf("%w: prerequisite fact %s is unavailable", ErrStepFailed, factID)
		}
	}
	return nil
}

func validateFacts(facts map[string]SafeScalar, contract StepContract) error {
	for id, value := range facts {
		if !contains(contract.ProducedFactIDs, id) {
			return invalid("produced facts", "undeclared fact "+id)
		}
		if err := validateAttributes(SafeAttributes{"state": value}); err != nil {
			return err
		}
	}
	return nil
}

func factsFromReceipts(transaction Transaction) map[string]SafeScalar {
	facts := make(map[string]SafeScalar)
	for _, receipt := range transaction.Steps {
		if receipt.Status != StepVerified && receipt.Status != StepSkippedVerified {
			continue
		}
		for _, factID := range receipt.ProducedFactIDs {
			facts[factID] = true
		}
	}
	return facts
}

func cloneFacts(facts map[string]SafeScalar) map[string]SafeScalar {
	cloned := make(map[string]SafeScalar, len(facts))
	for key, value := range facts {
		cloned[key] = value
	}
	return cloned
}

func mergeFacts(target, source map[string]SafeScalar) {
	for key, value := range source {
		target[key] = value
	}
}

func validateEvidence(evidence []Evidence) error {
	for _, item := range evidence {
		if err := item.validate(); err != nil {
			return err
		}
	}
	return nil
}

func cloneEvidence(evidence []Evidence) []Evidence {
	if evidence == nil {
		return []Evidence{}
	}
	cloned := make([]Evidence, len(evidence))
	copy(cloned, evidence)
	return cloned
}

func cloneStrings(values []string) []string {
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func upsertAsset(transaction *Transaction, asset ManagedAsset) error {
	for index := range transaction.ManagedAssets {
		if transaction.ManagedAssets[index].AssetID == asset.AssetID {
			if transaction.ManagedAssets[index].Ownership != asset.Ownership {
				return invalid("asset.ownership", "ownership changes require an explicit adoption transaction")
			}
			transaction.ManagedAssets[index] = asset
			return nil
		}
	}
	transaction.ManagedAssets = append(transaction.ManagedAssets, asset)
	return nil
}

func managerMutationIDs(transaction Transaction, stepID string) []string {
	var ids []string
	for _, asset := range transaction.ManagedAssets {
		if asset.Ownership == OwnershipManagerOwned && asset.CreatedOrAdoptedByStep != nil && *asset.CreatedOrAdoptedByStep == stepID {
			ids = append(ids, asset.AssetID)
		}
	}
	return ids
}

func removeManagerAssets(transaction *Transaction, stepID string) {
	retained := transaction.ManagedAssets[:0]
	for _, asset := range transaction.ManagedAssets {
		if asset.Ownership == OwnershipManagerOwned && asset.CreatedOrAdoptedByStep != nil && *asset.CreatedOrAdoptedByStep == stepID {
			continue
		}
		retained = append(retained, asset)
	}
	transaction.ManagedAssets = retained
}

func verifyTransactionComplete(transaction Transaction) error {
	for _, receipt := range transaction.Steps {
		if receipt.Status != StepVerified && receipt.Status != StepSkippedVerified {
			return fmt.Errorf("%w: step %s is not verified", ErrStepFailed, receipt.StepID)
		}
	}
	return nil
}

func issueForError(err error) Issue {
	var stepError *StepError
	if errors.As(err, &stepError) && stepError.Issue.Code != "" {
		return stepError.Issue
	}
	code := IssueCode("LAI-INTERNAL-001")
	domain := IssueDomain("internal")
	summary := "The step did not complete. No raw failure output was stored."
	if errors.Is(err, ErrConsentDeclined) {
		code = "LAI-POLICY-002"
		domain = "policy"
		summary = "Required consent was declined."
	}
	return Issue{
		Code:     code,
		Severity: SeverityError,
		Domain:   domain,
		Summary:  summary,
		Remediations: []Remediation{{
			Kind:       "retry",
			Summary:    "Review the safe issue details and retry when ready.",
			SafeForYes: false,
		}},
	}
}

func safeSummary(summary string) *string {
	if summary == "" {
		return nil
	}
	return &summary
}

func stringPointer(value string) *string { return &value }
func boolPointer(value bool) *bool       { return &value }
