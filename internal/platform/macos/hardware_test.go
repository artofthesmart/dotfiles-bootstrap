package macos

import (
	"testing"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
)

func TestAppleSiliconUsesUnifiedMemoryAndRecordsTranslation(t *testing.T) {
	capabilities, err := ParseSnapshot(Snapshot{
		Version:          "15.7",
		Architecture:     "x86_64",
		CPUModel:         "Apple M4 Pro",
		Translated:       true,
		InstalledMemory:  48 << 30,
		DisplayOutput:    "Chipset Model: Apple M4 Pro",
		Desktop:          true,
		ModelVolumeTotal: 2 << 40,
		ModelVolumeFree:  1 << 40,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.CPU.Translated || !capabilities.Memory.Unified {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	if len(capabilities.GPUs) != 1 ||
		capabilities.GPUs[0].MemoryKind != hardware.MemoryUnified ||
		capabilities.GPUs[0].MemoryBytes == nil ||
		*capabilities.GPUs[0].MemoryBytes != 48<<30 {
		t.Fatalf("unified GPU = %#v", capabilities.GPUs)
	}
	if capabilities.Acceleration != hardware.AccelerationMetal {
		t.Fatalf("acceleration = %q", capabilities.Acceleration)
	}
}

func TestIntelMacIsCPUOnlyAndCanHaveNoDesktop(t *testing.T) {
	capabilities, err := ParseSnapshot(Snapshot{
		Version:          "14.7",
		Architecture:     "x86_64",
		CPUModel:         "Intel Core i7",
		InstalledMemory:  16 << 30,
		DisplayOutput:    "Chipset Model: AMD Radeon Pro 5500M",
		Desktop:          false,
		ModelVolumeTotal: 1 << 40,
		ModelVolumeFree:  500 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.Acceleration != hardware.AccelerationCPU || len(capabilities.GPUs) != 0 {
		t.Fatalf("Intel Mac must be CPU-only: %#v", capabilities)
	}
	if capabilities.Desktop.Available || capabilities.Desktop.Kind != "" {
		t.Fatalf("desktop = %#v", capabilities.Desktop)
	}
}
