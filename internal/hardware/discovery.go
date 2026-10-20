package hardware

import (
	"context"
	"errors"
	"strings"
)

var (
	ErrUnsupportedHost = errors.New("unsupported host")
	ErrInvalidEvidence = errors.New("invalid hardware evidence")
)

type MemoryKind string

const (
	MemoryUnknown   MemoryKind = "unknown"
	MemoryDedicated MemoryKind = "dedicated"
	MemoryShared    MemoryKind = "shared"
	MemoryUnified   MemoryKind = "unified"
)

type Acceleration string

const (
	AccelerationCPU     Acceleration = "cpu"
	AccelerationCUDA    Acceleration = "cuda"
	AccelerationMetal   Acceleration = "metal"
	AccelerationROCm    Acceleration = "rocm"
	AccelerationDirect  Acceleration = "directml"
	AccelerationUnknown Acceleration = "unknown"
)

type OperatingSystem struct {
	Family       string `json:"family"`
	Distribution string `json:"distribution,omitempty"`
	Version      string `json:"version,omitempty"`
}

type Processor struct {
	Model      string `json:"model,omitempty"`
	Vendor     string `json:"vendor,omitempty"`
	Translated bool   `json:"translated"`
}

type Memory struct {
	InstalledBytes uint64 `json:"installed_bytes"`
	VisibleBytes   uint64 `json:"visible_bytes,omitempty"`
	Unified        bool   `json:"unified"`
}

type GPU struct {
	Model        string       `json:"model"`
	Vendor       string       `json:"vendor,omitempty"`
	Acceleration Acceleration `json:"acceleration"`
	MemoryBytes  *uint64      `json:"memory_bytes,omitempty"`
	MemoryKind   MemoryKind   `json:"memory_kind"`
}

type Storage struct {
	Volume     string `json:"volume"`
	TotalBytes uint64 `json:"total_bytes"`
	FreeBytes  uint64 `json:"free_bytes"`
}

type Desktop struct {
	Available bool   `json:"available"`
	Kind      string `json:"kind,omitempty"`
}

type Capabilities struct {
	OS           OperatingSystem `json:"os"`
	Architecture string          `json:"architecture"`
	CPU          Processor       `json:"cpu"`
	Memory       Memory          `json:"memory"`
	GPUs         []GPU           `json:"gpus"`
	Acceleration Acceleration    `json:"acceleration"`
	Desktop      Desktop         `json:"desktop"`
	Storage      Storage         `json:"storage"`
}

type Discoverer interface {
	Discover(context.Context) (Capabilities, error)
}

func NormalizeArchitecture(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "amd64", "x86_64", "x64", "9":
		return "amd64"
	case "arm64", "aarch64", "12":
		return "arm64"
	default:
		return "unknown"
	}
}

func VendorFromModel(model string) string {
	lower := strings.ToLower(model)
	switch {
	case strings.Contains(lower, "nvidia"):
		return "nvidia"
	case strings.Contains(lower, "amd"), strings.Contains(lower, "radeon"):
		return "amd"
	case strings.Contains(lower, "intel"):
		return "intel"
	case strings.Contains(lower, "apple"):
		return "apple"
	default:
		return ""
	}
}

func NormalizeAcceleration(gpus []GPU, fallback Acceleration) Acceleration {
	for _, gpu := range gpus {
		switch gpu.Acceleration {
		case AccelerationCUDA:
			return AccelerationCUDA
		case AccelerationMetal:
			return AccelerationMetal
		case AccelerationROCm:
			return AccelerationROCm
		case AccelerationDirect:
			return AccelerationDirect
		}
	}
	if fallback == "" {
		return AccelerationUnknown
	}
	return fallback
}
