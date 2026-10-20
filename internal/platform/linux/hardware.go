package linux

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
)

type Snapshot struct {
	OSRelease        string
	Architecture     string
	CPUInfo          string
	MemoryInfo       string
	PCIOutput        string
	NVIDIAOutput     string
	ROCmAvailable    bool
	DesktopAvailable bool
	DesktopKind      string
	ModelVolumeTotal uint64
	ModelVolumeFree  uint64
}

func ParseSnapshot(snapshot Snapshot) (hardware.Capabilities, error) {
	osValues := parseKeyValues(snapshot.OSRelease)
	distribution := strings.ToLower(osValues["ID"])
	if distribution != "ubuntu" && distribution != "debian" {
		return hardware.Capabilities{}, fmt.Errorf("%w: Linux distribution %q", hardware.ErrUnsupportedHost, distribution)
	}
	architecture := hardware.NormalizeArchitecture(snapshot.Architecture)
	if architecture != "amd64" && architecture != "arm64" {
		return hardware.Capabilities{}, fmt.Errorf("%w: Linux architecture %q", hardware.ErrUnsupportedHost, snapshot.Architecture)
	}
	memory := parseMemoryInfo(snapshot.MemoryInfo)
	if memory == 0 {
		return hardware.Capabilities{}, fmt.Errorf("%w: installed memory unavailable", hardware.ErrInvalidEvidence)
	}
	cpuModel := parseCPUModel(snapshot.CPUInfo)
	gpus := parseLinuxGPUs(snapshot.PCIOutput, snapshot.NVIDIAOutput, snapshot.ROCmAvailable)
	return hardware.Capabilities{
		OS: hardware.OperatingSystem{
			Family:       "linux",
			Distribution: distribution,
			Version:      osValues["VERSION_ID"],
		},
		Architecture: architecture,
		CPU: hardware.Processor{
			Model:  cpuModel,
			Vendor: hardware.VendorFromModel(cpuModel),
		},
		Memory: hardware.Memory{
			InstalledBytes: memory,
			VisibleBytes:   memory,
		},
		GPUs:         gpus,
		Acceleration: hardware.NormalizeAcceleration(gpus, hardware.AccelerationCPU),
		Desktop: hardware.Desktop{
			Available: snapshot.DesktopAvailable,
			Kind:      normalizeDesktopKind(snapshot.DesktopKind),
		},
		Storage: hardware.Storage{
			Volume:     "model-volume",
			TotalBytes: snapshot.ModelVolumeTotal,
			FreeBytes:  snapshot.ModelVolumeFree,
		},
	}, nil
}

func parseKeyValues(data string) map[string]string {
	values := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return values
}

func parseMemoryInfo(data string) uint64 {
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kib, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil {
				return kib * 1024
			}
		}
	}
	return 0
}

func parseCPUModel(data string) string {
	for _, line := range strings.Split(data, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(strings.ToLower(key)) {
		case "model name", "hardware", "processor":
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func parseLinuxGPUs(pciOutput, nvidiaOutput string, rocm bool) []hardware.GPU {
	nvidia := parseNVIDIA(nvidiaOutput)
	var gpus []hardware.GPU
	seen := map[string]bool{}
	for _, line := range strings.Split(pciOutput, "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "vga compatible controller") &&
			!strings.Contains(lower, "3d controller") &&
			!strings.Contains(lower, "display controller") {
			continue
		}
		model := strings.TrimSpace(line)
		if _, after, ok := strings.Cut(model, ": "); ok {
			model = strings.TrimSpace(after)
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
			if memory, ok := matchNVIDIAMemory(model, nvidia); ok {
				gpu.MemoryBytes = &memory
				gpu.MemoryKind = hardware.MemoryDedicated
			}
		} else if vendor == "amd" && rocm {
			gpu.Acceleration = hardware.AccelerationROCm
		} else if vendor == "intel" {
			gpu.MemoryKind = hardware.MemoryShared
		}
		gpus = append(gpus, gpu)
		seen[vendor] = true
	}
	if !seen["nvidia"] {
		for name, memory := range nvidia {
			memoryCopy := memory
			gpus = append(gpus, hardware.GPU{
				Model:        name,
				Vendor:       "nvidia",
				Acceleration: hardware.AccelerationCUDA,
				MemoryBytes:  &memoryCopy,
				MemoryKind:   hardware.MemoryDedicated,
			})
		}
	}
	return gpus
}

func parseNVIDIA(output string) map[string]uint64 {
	result := map[string]uint64{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) != 2 {
			continue
		}
		mib, err := strconv.ParseUint(strings.TrimSpace(fields[1]), 10, 64)
		if err == nil && mib > 0 {
			result[normalizeGPUName(fields[0])] = mib * 1024 * 1024
		}
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

func normalizeDesktopKind(value string) string {
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "gnome"):
		return "gnome"
	case strings.Contains(lower, "kde"), strings.Contains(lower, "plasma"):
		return "kde"
	case strings.Contains(lower, "xfce"):
		return "xfce"
	case value != "":
		return "other"
	default:
		return ""
	}
}
