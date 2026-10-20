package state

import (
	"fmt"
	"time"
)

var transactionTransitions = map[TransactionStatus]map[TransactionStatus]bool{
	StatusPlanned: {
		StatusRunning:  true,
		StatusArchived: true,
	},
	StatusRunning: {
		StatusVerifying:      true,
		StatusAwaitingManual: true,
		StatusInterrupted:    true,
		StatusFailed:         true,
		StatusArchived:       true,
	},
	StatusVerifying: {
		StatusSucceeded:      true,
		StatusAwaitingManual: true,
		StatusInterrupted:    true,
		StatusFailed:         true,
		StatusArchived:       true,
	},
	StatusAwaitingManual: {
		StatusRunning:     true,
		StatusInterrupted: true,
		StatusArchived:    true,
	},
	StatusInterrupted: {
		StatusRunning:     true,
		StatusRollingBack: true,
		StatusArchived:    true,
	},
	StatusFailed: {
		StatusRollingBack: true,
		StatusArchived:    true,
	},
	StatusRollingBack: {
		StatusRolledBack:     true,
		StatusRollbackFailed: true,
		StatusInterrupted:    true,
		StatusArchived:       true,
	},
	StatusRolledBack: {
		StatusArchived: true,
	},
	StatusRollbackFailed: {
		StatusRollingBack: true,
		StatusArchived:    true,
	},
	StatusSucceeded: {
		StatusArchived: true,
	},
}

func (t *Transaction) Transition(to TransactionStatus, now time.Time) error {
	if t == nil {
		return fmt.Errorf("%w: nil transaction", ErrInvalidTransition)
	}
	if !transactionTransitions[t.Status][to] {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, t.Status, to)
	}
	now = now.UTC()
	t.Status = to
	t.UpdatedAt = now
	if isTerminal(to) {
		t.CompletedAt = &now
	} else if to != StatusArchived {
		t.CompletedAt = nil
	}
	return nil
}

func CommandFamily(command Command) string {
	switch command {
	case CommandSetup, CommandRepair, CommandVSCodeConfigure:
		return "environment"
	case CommandModelsInstall, CommandModelsRemove:
		return "models"
	case CommandUpdate, CommandUpdateRollback:
		return "update"
	case CommandUninstall:
		return "uninstall"
	default:
		return ""
	}
}

func ValidateResume(transaction Transaction, command Command, configDigest string) error {
	if transaction.SchemaVersion != TransactionSchemaVersion {
		return fmt.Errorf("%w: %w: %s", ErrIncompatibleResume, ErrUnsupportedSchema, transaction.SchemaVersion)
	}
	if transaction.Status != StatusInterrupted && transaction.Status != StatusAwaitingManual {
		return fmt.Errorf("%w: status %s is not resumable", ErrIncompatibleResume, transaction.Status)
	}
	if CommandFamily(transaction.Command) == "" || CommandFamily(transaction.Command) != CommandFamily(command) {
		return fmt.Errorf("%w: command families differ", ErrIncompatibleResume)
	}
	if transaction.EffectiveConfigDigest != configDigest {
		return fmt.Errorf("%w: effective configuration digest differs", ErrIncompatibleResume)
	}
	return nil
}
