package macos

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
)

type Snapshot struct {
	Version          string
	Architecture     string
	CPUModel         string
	Translated       bool
	InstalledMemory  uint64
	DisplayOutput    string
	Desktop          bool
	ModelVolumeTotal uint64
	ModelVolumeFree  uint64
}

func ParseSnapshot(snapshot Snapshot) (hardware.Capabilities, error) {
	architecture := hardware.NormalizeArchitecture(snapshot.Architecture)
	if snapshot.Translated && architecture == "amd64" {
		architecture = "arm64"
	}
	if architecture != "amd64" && architecture != "arm64" {
		return hardware.Capabilities{}, fmt.Errorf("%w: macOS architecture %q", hardware.ErrUnsupportedHost, snapshot.Architecture)
	}
	if snapshot.InstalledMemory == 0 {
		return hardware.Capabilities{}, fmt.Errorf("%w: installed memory unavailable", hardware.ErrInvalidEvidence)
	}
	var gpus []hardware.GPU
	acceleration := hardware.AccelerationCPU
	unified := architecture == "arm64"
	if unified {
		model := parseDisplayModel(snapshot.DisplayOutput)
		if model == "" {
			model = "Apple integrated GPU"
		}
		memory := snapshot.InstalledMemory
		gpus = []hardware.GPU{{
			Model:        model,
			Vendor:       "apple",
			Acceleration: hardware.AccelerationMetal,
			MemoryBytes:  &memory,
			MemoryKind:   hardware.MemoryUnified,
		}}
		acceleration = hardware.AccelerationMetal
	}
	return hardware.Capabilities{
		OS: hardware.OperatingSystem{
			Family:       "darwin",
			Distribution: "macos",
			Version:      strings.TrimSpace(snapshot.Version),
		},
		Architecture: architecture,
		CPU: hardware.Processor{
			Model:      strings.TrimSpace(snapshot.CPUModel),
			Vendor:     hardware.VendorFromModel(snapshot.CPUModel),
			Translated: snapshot.Translated,
		},
		Memory: hardware.Memory{
			InstalledBytes: snapshot.InstalledMemory,
			VisibleBytes:   snapshot.InstalledMemory,
			Unified:        unified,
		},
		GPUs:         gpus,
		Acceleration: acceleration,
		Desktop:      hardware.Desktop{Available: snapshot.Desktop, Kind: desktopKind(snapshot.Desktop)},
		Storage: hardware.Storage{
			Volume:     "model-volume",
			TotalBytes: snapshot.ModelVolumeTotal,
			FreeBytes:  snapshot.ModelVolumeFree,
		},
	}, nil
}

func ParseUintEvidence(data []byte) (uint64, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: numeric macOS evidence", hardware.ErrInvalidEvidence)
	}
	return value, nil
}

func parseDisplayModel(output string) string {
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "chipset model" || key == "model" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func desktopKind(available bool) string {
	if available {
		return "aqua"
	}
	return ""
}
