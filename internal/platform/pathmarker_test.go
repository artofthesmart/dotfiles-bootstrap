package platform_test

import (
	"strings"
	"testing"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

func TestPathMarkerRequiresConsentPreservesUnrelatedConfigAndIsReversible(t *testing.T) {
	filesystem := testkit.NewMemoryFileSystem("/manager", nil)
	if err := filesystem.MkdirAll("/manager/home", 0o700); err != nil {
		t.Fatal(err)
	}
	config := "/manager/home/.profile"
	original := []byte("# user configuration\nexport EDITOR=vi\n")
	if err := filesystem.WriteFile(config, original, 0o600); err != nil {
		t.Fatal(err)
	}
	editor := platform.PathMarker{FileSystem: filesystem}
	change, err := editor.Plan(config, "/manager/home/.local/bin", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(change.Preview, platform.PathMarkerStart) {
		t.Fatalf("preview = %q", change.Preview)
	}
	if err := editor.Apply(change, false); err == nil {
		t.Fatal("PATH change applied without consent")
	}
	if err := editor.Apply(change, true); err != nil {
		t.Fatal(err)
	}
	updated, _ := filesystem.ReadFile(config)
	if !strings.Contains(string(updated), string(original)) || !strings.Contains(string(updated), platform.PathMarkerEnd) {
		t.Fatalf("updated config = %q", updated)
	}
	backup, err := filesystem.ReadFile(config + ".local-ai.backup")
	if err != nil || string(backup) != string(original) {
		t.Fatalf("backup = %q, err = %v", backup, err)
	}
	if err := editor.Remove(config); err != nil {
		t.Fatal(err)
	}
	restored, _ := filesystem.ReadFile(config)
	if string(restored) != string(original) {
		t.Fatalf("restored config = %q, want %q", restored, original)
	}
}
