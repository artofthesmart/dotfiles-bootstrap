package windows

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
)

type Snapshot struct {
	Version          string   `json:"version"`
	Architecture     string   `json:"architecture"`
	CPUModel         string   `json:"cpu_model"`
	InstalledMemory  []uint64 `json:"installed_memory"`
	VisibleMemory    uint64   `json:"visible_memory"`
	GPUModels        []string `json:"gpu_models"`
	Desktop          bool     `json:"desktop"`
	ModelVolumeTotal uint64   `json:"model_volume_total"`
	ModelVolumeFree  uint64   `json:"model_volume_free"`
}

func ParseSnapshot(data []byte, nvidiaSMI []byte) (hardware.Capabilities, error) {
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return hardware.Capabilities{}, fmt.Errorf("%w: Windows snapshot: %v", hardware.ErrInvalidEvidence, err)
	}
	architecture := hardware.NormalizeArchitecture(snapshot.Architecture)
	if architecture != "amd64" {
		return hardware.Capabilities{}, fmt.Errorf("%w: Windows architecture %q", hardware.ErrUnsupportedHost, snapshot.Architecture)
	}
	var installed uint64
	for _, module := range snapshot.InstalledMemory {
		installed += module
	}
	if installed == 0 {
		return hardware.Capabilities{}, fmt.Errorf("%w: installed memory unavailable", hardware.ErrInvalidEvidence)
	}
	nvidiaMemory := parseNVIDIAMemory(nvidiaSMI)
	gpus := make([]hardware.GPU, 0, len(snapshot.GPUModels))
	for _, model := range snapshot.GPUModels {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		vendor := hardware.VendorFromModel(model)
		gpu := hardware.GPU{
			Model:        model,
			Vendor:       vendor,
			Acceleration: hardware.AccelerationUnknown,
			MemoryKind:   hardware.MemoryUnknown,
		}
		if vendor == "nvidia" {
			gpu.Acceleration = hardware.AccelerationCUDA
			if memory, ok := matchNVIDIAMemory(model, nvidiaMemory); ok {
				gpu.MemoryBytes = &memory
				gpu.MemoryKind = hardware.MemoryDedicated
			}
		} else if vendor == "intel" || strings.Contains(strings.ToLower(model), "integrated") {
			gpu.MemoryKind = hardware.MemoryShared
		}
		gpus = append(gpus, gpu)
	}
	return hardware.Capabilities{
		OS: hardware.OperatingSystem{
			Family:       "windows",
			Distribution: "windows",
			Version:      strings.TrimSpace(snapshot.Version),
		},
		Architecture: architecture,
		CPU: hardware.Processor{
			Model:  strings.TrimSpace(snapshot.CPUModel),
			Vendor: hardware.VendorFromModel(snapshot.CPUModel),
		},
		Memory: hardware.Memory{
			InstalledBytes: installed,
			VisibleBytes:   snapshot.VisibleMemory,
		},
		GPUs:         gpus,
		Acceleration: hardware.NormalizeAcceleration(gpus, hardware.AccelerationCPU),
		Desktop:      hardware.Desktop{Available: snapshot.Desktop, Kind: desktopKind(snapshot.Desktop)},
		Storage: hardware.Storage{
			Volume:     "model-volume",
			TotalBytes: snapshot.ModelVolumeTotal,
			FreeBytes:  snapshot.ModelVolumeFree,
		},
	}, nil
}

func parseNVIDIAMemory(data []byte) map[string]uint64 {
	result := map[string]uint64{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) != 2 {
			continue
		}
		mib, err := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 64)
		if err != nil || mib == 0 {
			continue
		}
		result[normalizeGPUName(fields[0])] = mib * 1024 * 1024
	}
	return result
}

func normalizeGPUName(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func matchNVIDIAMemory(model string, values map[string]uint64) (uint64, bool) {
	model = normalizeGPUName(model)
	for name, memory := range values {
		if model == name || strings.Contains(model, name) || strings.Contains(name, model) {
			return memory, true
		}
	}
	if len(values) == 1 {
		for _, memory := range values {
			return memory, true
		}
	}
	return 0, false
}

func desktopKind(available bool) string {
	if available {
		return "windows-shell"
	}
	return ""
}
