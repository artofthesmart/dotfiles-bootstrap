package linux

import (
	"errors"
	"testing"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
)

func TestLinuxSnapshotNormalizesNVIDIAAndUnknownVRAM(t *testing.T) {
	capabilities, err := ParseSnapshot(Snapshot{
		OSRelease:        "ID=ubuntu\nVERSION_ID=\"24.04\"\n",
		Architecture:     "x86_64",
		CPUInfo:          "model name : AMD Ryzen 9\n",
		MemoryInfo:       "MemTotal:       65790592 kB\n",
		PCIOutput:        "01:00.0 VGA compatible controller: NVIDIA Corporation NVIDIA GeForce RTX 4090\n02:00.0 VGA compatible controller: Advanced Micro Devices, Inc. Radeon RX 6800\n",
		NVIDIAOutput:     "NVIDIA GeForce RTX 4090, 24564\n",
		DesktopAvailable: false,
		ModelVolumeTotal: 2 << 40,
		ModelVolumeFree:  1 << 40,
	})
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.Acceleration != hardware.AccelerationCUDA {
		t.Fatalf("acceleration = %q", capabilities.Acceleration)
	}
	if capabilities.GPUs[0].MemoryBytes == nil || *capabilities.GPUs[0].MemoryBytes != 24564*1024*1024 {
		t.Fatalf("NVIDIA memory = %#v", capabilities.GPUs[0])
	}
	if capabilities.GPUs[1].MemoryBytes != nil || capabilities.GPUs[1].MemoryKind != hardware.MemoryUnknown {
		t.Fatalf("unknown AMD VRAM was invented: %#v", capabilities.GPUs[1])
	}
	if capabilities.Desktop.Available || capabilities.Desktop.Kind != "" {
		t.Fatalf("desktop = %#v", capabilities.Desktop)
	}
}

func TestLinuxSnapshotRejectsUnsupportedHost(t *testing.T) {
	_, err := ParseSnapshot(Snapshot{
		OSRelease:    "ID=fedora\nVERSION_ID=42\n",
		Architecture: "x86_64",
		MemoryInfo:   "MemTotal: 1024 kB\n",
	})
	if !errors.Is(err, hardware.ErrUnsupportedHost) {
		t.Fatalf("error = %v, want unsupported host", err)
	}
}

func TestLinuxIntegratedMemoryIsSharedButUnquantified(t *testing.T) {
	capabilities, err := ParseSnapshot(Snapshot{
		OSRelease:        "ID=debian\nVERSION_ID=13\n",
		Architecture:     "aarch64",
		CPUInfo:          "processor : ARMv8 Processor\n",
		MemoryInfo:       "MemTotal: 16777216 kB\n",
		PCIOutput:        "00:02.0 VGA compatible controller: Intel Corporation Arc Graphics\n",
		DesktopAvailable: true,
		DesktopKind:      "GNOME",
	})
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.GPUs[0].MemoryKind != hardware.MemoryShared || capabilities.GPUs[0].MemoryBytes != nil {
		t.Fatalf("shared GPU = %#v", capabilities.GPUs[0])
	}
	if capabilities.Desktop.Kind != "gnome" {
		t.Fatalf("desktop = %#v", capabilities.Desktop)
	}
}
