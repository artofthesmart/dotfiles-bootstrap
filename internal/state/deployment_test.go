package state

import (
	"context"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

func TestDeploymentReceiptPersistsAtomicResumeBoundary(t *testing.T) {
	filesystem := testkit.NewMemoryFileSystem("/manager", nil)
	store := JSONDeploymentStore{FileSystem: filesystem, Path: "/manager/state/deployment.json"}
	receipt := DeploymentReceipt{
		SchemaVersion: DeploymentReceiptSchemaVersion,
		Operation:     DeploymentUpdate,
		Stage:         DeploymentSwapped,
		Version:       "1.2.3",
		Digest:        "sha256:abc",
		BackupVersion: "1.2.2",
		BackupDigest:  "sha256:def",
		UpdatedAt:     time.Date(2026, 10, 20, 20, 0, 0, 0, time.UTC),
	}
	if err := store.SaveDeployment(context.Background(), receipt); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadDeployment(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded != receipt {
		t.Fatalf("loaded receipt = %#v, want %#v", loaded, receipt)
	}
	if err := store.ClearDeployment(context.Background()); err != nil {
		t.Fatal(err)
	}
}
