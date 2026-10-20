package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

const DeploymentReceiptSchemaVersion = "local-ai.deployment-receipt/v1"

type DeploymentOperation string
type DeploymentStage string

const (
	DeploymentInstall   DeploymentOperation = "install"
	DeploymentUpdate    DeploymentOperation = "update"
	DeploymentRollback  DeploymentOperation = "rollback"
	DeploymentUninstall DeploymentOperation = "uninstall"

	DeploymentStarted   DeploymentStage = "started"
	DeploymentStaged    DeploymentStage = "staged"
	DeploymentBackedUp  DeploymentStage = "backed_up"
	DeploymentSwapped   DeploymentStage = "swapped"
	DeploymentVerified  DeploymentStage = "verified"
	DeploymentSucceeded DeploymentStage = "succeeded"
	DeploymentFailed    DeploymentStage = "failed"
)

type DeploymentReceipt struct {
	SchemaVersion string              `json:"schema_version"`
	Operation     DeploymentOperation `json:"operation"`
	Stage         DeploymentStage     `json:"stage"`
	Version       string              `json:"version,omitempty"`
	Digest        string              `json:"digest,omitempty"`
	BackupVersion string              `json:"backup_version,omitempty"`
	BackupDigest  string              `json:"backup_digest,omitempty"`
	IssueCode     IssueCode           `json:"issue_code,omitempty"`
	UpdatedAt     time.Time           `json:"updated_at"`
}

type DeploymentStore interface {
	LoadDeployment(context.Context) (DeploymentReceipt, error)
	SaveDeployment(context.Context, DeploymentReceipt) error
	ClearDeployment(context.Context) error
}

type JSONDeploymentStore struct {
	FileSystem platform.FileSystem
	Path       string
}

func (store JSONDeploymentStore) LoadDeployment(ctx context.Context) (DeploymentReceipt, error) {
	if err := ctx.Err(); err != nil {
		return DeploymentReceipt{}, err
	}
	data, err := store.FileSystem.ReadFile(store.Path)
	if err != nil {
		return DeploymentReceipt{}, err
	}
	var receipt DeploymentReceipt
	if err := strictDecode(data, &receipt); err != nil {
		return DeploymentReceipt{}, fmt.Errorf("%w: deployment receipt: %v", ErrInvalidState, err)
	}
	if err := receipt.validate(); err != nil {
		return DeploymentReceipt{}, err
	}
	return receipt, nil
}

func (store JSONDeploymentStore) SaveDeployment(ctx context.Context, receipt DeploymentReceipt) error {
	if store.FileSystem == nil || store.Path == "" {
		return fmt.Errorf("%w: deployment store is not configured", ErrInvalidState)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := receipt.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := store.FileSystem.MkdirAll(filepathDir(store.Path), 0o700); err != nil {
		return err
	}
	if atomic, ok := store.FileSystem.(platform.AtomicFileSystem); ok {
		return atomic.WriteFileAtomic(store.Path, data, 0o600)
	}
	return store.FileSystem.WriteFile(store.Path, data, 0o600)
}

func (store JSONDeploymentStore) ClearDeployment(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := store.FileSystem.Remove(store.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (receipt DeploymentReceipt) validate() error {
	if receipt.SchemaVersion != DeploymentReceiptSchemaVersion {
		return fmt.Errorf("%w: invalid deployment receipt schema", ErrInvalidState)
	}
	switch receipt.Operation {
	case DeploymentInstall, DeploymentUpdate, DeploymentRollback, DeploymentUninstall:
	default:
		return fmt.Errorf("%w: invalid deployment operation", ErrInvalidState)
	}
	switch receipt.Stage {
	case DeploymentStarted, DeploymentStaged, DeploymentBackedUp, DeploymentSwapped, DeploymentVerified, DeploymentSucceeded, DeploymentFailed:
	default:
		return fmt.Errorf("%w: invalid deployment stage", ErrInvalidState)
	}
	if receipt.UpdatedAt.IsZero() || receipt.UpdatedAt.Location() != time.UTC {
		return fmt.Errorf("%w: deployment receipt time must be UTC", ErrInvalidState)
	}
	if receipt.IssueCode != "" && !validIssueCodes[string(receipt.IssueCode)] {
		return fmt.Errorf("%w: invalid deployment issue code", ErrInvalidState)
	}
	return nil
}

func filepathDir(name string) string {
	for index := len(name) - 1; index >= 0; index-- {
		if name[index] == '/' || name[index] == '\\' {
			if index == 0 {
				return name[:1]
			}
			return name[:index]
		}
	}
	return "."
}
