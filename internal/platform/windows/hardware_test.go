package windows

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
)

func TestParseSnapshotKeepsInstalledAndWindowsVisibleMemorySeparate(t *testing.T) {
	data := []byte(`{
		"version":"10.0.26100",
		"architecture":"9",
		"cpu_model":"Intel Core Ultra",
		"installed_memory":[17179869184,17179869184],
		"visible_memory":30064771072,
		"gpu_models":["Intel Arc Graphics","NVIDIA GeForce RTX 4070"],
		"desktop":true,
		"model_volume_total":1000000,
		"model_volume_free":500000
	}`)
	capabilities, err := ParseSnapshot(data, []byte("NVIDIA GeForce RTX 4070, 12282\n"))
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.Memory.InstalledBytes != 32<<30 {
		t.Fatalf("installed memory = %d", capabilities.Memory.InstalledBytes)
	}
	if capabilities.Memory.VisibleBytes != 30064771072 {
		t.Fatalf("visible memory = %d", capabilities.Memory.VisibleBytes)
	}
	if capabilities.GPUs[0].MemoryKind != hardware.MemoryShared || capabilities.GPUs[0].MemoryBytes != nil {
		t.Fatalf("integrated GPU = %#v", capabilities.GPUs[0])
	}
	if capabilities.GPUs[1].MemoryBytes == nil || *capabilities.GPUs[1].MemoryBytes != 12282*1024*1024 {
		t.Fatalf("NVIDIA GPU = %#v", capabilities.GPUs[1])
	}
}

func TestParseSnapshotDoesNotUseGenericAdapterRAMOrIdentity(t *testing.T) {
	data := []byte(`{
		"version":"10.0.26100",
		"architecture":"9",
		"cpu_model":"AMD Ryzen",
		"installed_memory":[34359738368],
		"visible_memory":33285996544,
		"gpu_models":["AMD Radeon Graphics"],
		"desktop":false,
		"model_volume_total":1000000,
		"model_volume_free":500000,
		"AdapterRAM":4293918720,
		"serial_number":"private",
		"hostname":"private"
	}`)
	capabilities, err := ParseSnapshot(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.GPUs[0].MemoryBytes != nil {
		t.Fatalf("unknown VRAM was invented: %#v", capabilities.GPUs[0])
	}
	encoded, _ := json.Marshal(capabilities)
	for _, excluded := range []string{"adapterram", "serial", "hostname", "username", "mac_address", "ip_address"} {
		if strings.Contains(strings.ToLower(string(encoded)), excluded) {
			t.Fatalf("privacy exclusion %q leaked in %s", excluded, encoded)
		}
	}
	if capabilities.Desktop.Available {
		t.Fatal("desktop unexpectedly available")
	}
}

func TestParseSnapshotRejectsUnsupportedWindowsArchitecture(t *testing.T) {
	_, err := ParseSnapshot([]byte(`{
		"architecture":"12",
		"installed_memory":[8589934592]
	}`), nil)
	if !errors.Is(err, hardware.ErrUnsupportedHost) {
		t.Fatalf("error = %v, want unsupported host", err)
	}
}
