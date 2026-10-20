package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

type Store interface {
	Load(context.Context) (Transaction, error)
	Save(context.Context, *Transaction) error
	Archive(context.Context) (StateLocator, error)
}

type Migrator interface {
	FromSchema() string
	Migrate(context.Context, []byte) ([]byte, error)
}

type JSONStore struct {
	FileSystem           platform.FileSystem
	Path                 string
	PrivacySafePath      string
	ArchivePrivacyPrefix string
	Migrators            []Migrator
}

func (s JSONStore) Load(ctx context.Context) (Transaction, error) {
	if err := ctx.Err(); err != nil {
		return Transaction{}, err
	}
	data, err := s.FileSystem.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Transaction{}, ErrNoState
		}
		return Transaction{}, fmt.Errorf("read transaction state: %w", err)
	}
	transaction, _, err := s.decode(ctx, data)
	if err != nil {
		return Transaction{}, err
	}
	return transaction, nil
}

func (s JSONStore) Save(ctx context.Context, transaction *Transaction) error {
	if transaction == nil {
		return fmt.Errorf("%w: nil transaction", ErrInvalidState)
	}
	if err := s.validateConfiguration(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	candidate, err := cloneTransaction(*transaction)
	if err != nil {
		return err
	}

	var previous []byte
	previous, err = s.FileSystem.ReadFile(s.Path)
	switch {
	case err == nil:
		if _, _, decodeErr := s.decode(ctx, previous); decodeErr != nil {
			return fmt.Errorf("refuse to replace invalid current state: %w", decodeErr)
		}
		candidate.PreviousState = &StateLocator{
			Path:   s.PrivacySafePath + ".previous",
			Digest: digest(previous),
		}
	case errors.Is(err, fs.ErrNotExist):
		previous = nil
		candidate.PreviousState = nil
	default:
		return fmt.Errorf("read current transaction state: %w", err)
	}

	if err := candidate.Validate(); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return fmt.Errorf("encode transaction state: %w", err)
	}
	encoded = append(encoded, '\n')

	parent := filepath.Dir(s.Path)
	nextPath := s.Path + ".next"
	previousPath := s.Path + ".previous"
	previousNextPath := previousPath + ".next"
	cleanup := func() {
		_ = s.FileSystem.Remove(nextPath)
		_ = s.FileSystem.Remove(previousNextPath)
	}
	defer cleanup()

	if err := s.boundary(ctx, func() error {
		return s.FileSystem.MkdirAll(parent, 0o700)
	}); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	if err := s.boundary(ctx, func() error {
		return s.FileSystem.Sync(parent)
	}); err != nil {
		return fmt.Errorf("sync state directory: %w", err)
	}
	if err := s.boundary(ctx, func() error {
		return s.FileSystem.WriteFile(nextPath, encoded, 0o600)
	}); err != nil {
		return fmt.Errorf("write next transaction state: %w", err)
	}
	if err := s.boundary(ctx, func() error {
		return s.FileSystem.Sync(nextPath)
	}); err != nil {
		return fmt.Errorf("sync next transaction state: %w", err)
	}

	if previous != nil {
		if err := s.boundary(ctx, func() error {
			return s.FileSystem.WriteFile(previousNextPath, previous, 0o600)
		}); err != nil {
			return fmt.Errorf("write retained previous state: %w", err)
		}
		if err := s.boundary(ctx, func() error {
			return s.FileSystem.Sync(previousNextPath)
		}); err != nil {
			return fmt.Errorf("sync retained previous state: %w", err)
		}
		if err := s.boundary(ctx, func() error {
			return s.FileSystem.Rename(previousNextPath, previousPath)
		}); err != nil {
			return fmt.Errorf("replace retained previous state: %w", err)
		}
		if err := s.boundary(ctx, func() error {
			return s.FileSystem.Sync(previousPath)
		}); err != nil {
			return fmt.Errorf("sync retained previous state: %w", err)
		}
		if err := s.boundary(ctx, func() error {
			return s.FileSystem.Sync(parent)
		}); err != nil {
			return fmt.Errorf("sync retained previous state directory entry: %w", err)
		}
	}

	if err := s.boundary(ctx, func() error {
		return s.FileSystem.Rename(nextPath, s.Path)
	}); err != nil {
		return fmt.Errorf("replace transaction state: %w", err)
	}
	if err := s.boundary(ctx, func() error {
		return s.FileSystem.Sync(s.Path)
	}); err != nil {
		return fmt.Errorf("sync transaction state: %w", err)
	}
	if err := s.boundary(ctx, func() error {
		return s.FileSystem.Sync(parent)
	}); err != nil {
		return fmt.Errorf("sync transaction state directory entry: %w", err)
	}

	*transaction = candidate
	return nil
}

func (s JSONStore) Archive(ctx context.Context) (StateLocator, error) {
	if err := s.validateConfiguration(); err != nil {
		return StateLocator{}, err
	}
	transaction, err := s.Load(ctx)
	if err != nil {
		return StateLocator{}, err
	}
	if transaction.Status != StatusArchived {
		return StateLocator{}, fmt.Errorf("%w: state must be archived before file archival", ErrInvalidState)
	}
	data, err := s.FileSystem.ReadFile(s.Path)
	if err != nil {
		return StateLocator{}, fmt.Errorf("read state for archival: %w", err)
	}

	archiveDirectory := filepath.Join(filepath.Dir(s.Path), "archive")
	archivePath := filepath.Join(archiveDirectory, transaction.TransactionID+".json")
	archivePreviousPath := filepath.Join(archiveDirectory, transaction.TransactionID+".previous.json")
	if err := s.boundary(ctx, func() error {
		return s.FileSystem.MkdirAll(archiveDirectory, 0o700)
	}); err != nil {
		return StateLocator{}, fmt.Errorf("create state archive directory: %w", err)
	}
	if err := s.boundary(ctx, func() error {
		return s.FileSystem.Sync(filepath.Dir(archiveDirectory))
	}); err != nil {
		return StateLocator{}, fmt.Errorf("sync state directory after archive creation: %w", err)
	}
	if err := s.boundary(ctx, func() error {
		return s.FileSystem.Sync(archiveDirectory)
	}); err != nil {
		return StateLocator{}, fmt.Errorf("sync state archive directory: %w", err)
	}

	previousPath := s.Path + ".previous"
	if _, statErr := s.FileSystem.Stat(previousPath); statErr == nil {
		if err := s.boundary(ctx, func() error {
			return s.FileSystem.Rename(previousPath, archivePreviousPath)
		}); err != nil {
			return StateLocator{}, fmt.Errorf("archive retained previous state: %w", err)
		}
		if err := s.boundary(ctx, func() error {
			return s.FileSystem.Sync(archivePreviousPath)
		}); err != nil {
			return StateLocator{}, fmt.Errorf("sync archived previous state: %w", err)
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return StateLocator{}, fmt.Errorf("inspect retained previous state: %w", statErr)
	}
	if err := s.boundary(ctx, func() error {
		return s.FileSystem.Rename(s.Path, archivePath)
	}); err != nil {
		return StateLocator{}, fmt.Errorf("archive transaction state: %w", err)
	}
	if err := s.boundary(ctx, func() error {
		return s.FileSystem.Sync(archivePath)
	}); err != nil {
		return StateLocator{}, fmt.Errorf("sync archived transaction state: %w", err)
	}
	if err := s.boundary(ctx, func() error {
		return s.FileSystem.Sync(archiveDirectory)
	}); err != nil {
		return StateLocator{}, fmt.Errorf("sync archive directory entry: %w", err)
	}
	if err := s.boundary(ctx, func() error {
		return s.FileSystem.Sync(filepath.Dir(s.Path))
	}); err != nil {
		return StateLocator{}, fmt.Errorf("sync active state directory after archival: %w", err)
	}

	return StateLocator{
		Path:   strings.TrimSuffix(s.ArchivePrivacyPrefix, "/") + "/" + transaction.TransactionID + ".json",
		Digest: digest(data),
	}, nil
}

func (s JSONStore) decode(ctx context.Context, data []byte) (Transaction, []byte, error) {
	current := append([]byte(nil), data...)
	for attempts := 0; attempts <= len(s.Migrators); attempts++ {
		var envelope struct {
			SchemaVersion string `json:"schema_version"`
		}
		if err := json.Unmarshal(current, &envelope); err != nil {
			return Transaction{}, nil, fmt.Errorf("%w: decode schema discriminator: %v", ErrInvalidState, err)
		}
		if envelope.SchemaVersion == "" {
			return Transaction{}, nil, fmt.Errorf("%w: missing schema discriminator", ErrInvalidState)
		}
		if envelope.SchemaVersion == TransactionSchemaVersion {
			var transaction Transaction
			if err := strictDecode(current, &transaction); err != nil {
				return Transaction{}, nil, fmt.Errorf("%w: decode transaction: %v", ErrInvalidState, err)
			}
			if err := transaction.Validate(); err != nil {
				return Transaction{}, nil, err
			}
			return transaction, current, nil
		}

		migrator := s.migrator(envelope.SchemaVersion)
		if migrator == nil {
			return Transaction{}, nil, fmt.Errorf("%w: %s", ErrUnsupportedSchema, envelope.SchemaVersion)
		}
		migrated, err := migrator.Migrate(ctx, current)
		if err != nil {
			return Transaction{}, nil, fmt.Errorf("migrate transaction state from %s: %w", envelope.SchemaVersion, err)
		}
		if bytes.Equal(migrated, current) {
			return Transaction{}, nil, fmt.Errorf("%w: migration from %s made no progress", ErrUnsupportedSchema, envelope.SchemaVersion)
		}
		current = migrated
	}
	return Transaction{}, nil, fmt.Errorf("%w: migration chain exceeded registered hooks", ErrUnsupportedSchema)
}

func (s JSONStore) migrator(schema string) Migrator {
	for _, migrator := range s.Migrators {
		if migrator != nil && migrator.FromSchema() == schema {
			return migrator
		}
	}
	return nil
}

func (s JSONStore) validateConfiguration() error {
	if s.FileSystem == nil || s.Path == "" {
		return fmt.Errorf("%w: state store filesystem and path are required", ErrInvalidState)
	}
	for field, value := range map[string]string{
		"privacy-safe path":      s.PrivacySafePath,
		"archive privacy prefix": s.ArchivePrivacyPrefix,
	} {
		if !strings.HasPrefix(value, "${HOME}/") && !strings.HasPrefix(value, `${HOME}\`) {
			return fmt.Errorf("%w: %s must use ${HOME}", ErrInvalidState, field)
		}
	}
	return nil
}

func (s JSONStore) boundary(ctx context.Context, operation func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := operation(); err != nil {
		return err
	}
	return ctx.Err()
}

func strictDecode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func cloneTransaction(transaction Transaction) (Transaction, error) {
	encoded, err := json.Marshal(transaction)
	if err != nil {
		return Transaction{}, fmt.Errorf("clone transaction: %w", err)
	}
	var cloned Transaction
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return Transaction{}, fmt.Errorf("clone transaction: %w", err)
	}
	return cloned, nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
