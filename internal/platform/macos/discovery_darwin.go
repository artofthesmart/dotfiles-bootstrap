//go:build darwin

package macos

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"syscall"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

type Discoverer struct {
	Runner    platform.Runner
	ModelPath string
}

func NewDiscoverer(runner platform.Runner, modelPath string) Discoverer {
	return Discoverer{Runner: runner, ModelPath: modelPath}
}

func (discoverer Discoverer) Discover(ctx context.Context) (hardware.Capabilities, error) {
	version, err := discoverer.run(ctx, "/usr/bin/sw_vers", "-productVersion")
	if err != nil {
		return hardware.Capabilities{}, err
	}
	memoryOutput, err := discoverer.run(ctx, "/usr/sbin/sysctl", "-n", "hw.memsize")
	if err != nil {
		return hardware.Capabilities{}, err
	}
	memory, err := ParseUintEvidence(memoryOutput)
	if err != nil {
		return hardware.Capabilities{}, err
	}
	cpu, err := discoverer.run(ctx, "/usr/sbin/sysctl", "-n", "machdep.cpu.brand_string")
	if err != nil && runtime.GOARCH == "arm64" {
		cpu = []byte("Apple Silicon")
		err = nil
	}
	if err != nil {
		return hardware.Capabilities{}, err
	}
	translatedOutput, translatedErr := discoverer.run(ctx, "/usr/sbin/sysctl", "-n", "sysctl.proc_translated")
	translated := translatedErr == nil && strings.TrimSpace(string(translatedOutput)) == "1"
	display, displayErr := discoverer.run(ctx, "/usr/sbin/system_profiler", "SPDisplaysDataType")
	if displayErr != nil {
		display = nil
	}
	_, desktopErr := discoverer.run(ctx, "/usr/bin/pgrep", "-x", "Finder")
	total, free, diskErr := volumeCapacity(discoverer.ModelPath)
	if diskErr != nil {
		return hardware.Capabilities{}, diskErr
	}
	return ParseSnapshot(Snapshot{
		Version:          string(version),
		Architecture:     runtime.GOARCH,
		CPUModel:         string(cpu),
		Translated:       translated,
		InstalledMemory:  memory,
		DisplayOutput:    string(display),
		Desktop:          desktopErr == nil,
		ModelVolumeTotal: total,
		ModelVolumeFree:  free,
	})
}

func (discoverer Discoverer) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	result, err := discoverer.Runner.Run(ctx, platform.Command{
		Name:        name,
		Args:        args,
		OutputLimit: 256 << 10,
	})
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 || result.StdoutTruncated {
		return nil, fmt.Errorf("%w: %s exited with code %d", hardware.ErrInvalidEvidence, name, result.ExitCode)
	}
	return result.Stdout, nil
}

func volumeCapacity(name string) (uint64, uint64, error) {
	if name == "" {
		name = "/"
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(name, &stat); err != nil {
		return 0, 0, err
	}
	return uint64(stat.Blocks) * uint64(stat.Bsize), uint64(stat.Bavail) * uint64(stat.Bsize), nil
}
