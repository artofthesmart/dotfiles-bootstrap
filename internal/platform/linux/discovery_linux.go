//go:build linux

package linux

import (
	"context"
	"errors"
	"os"
	"runtime"
	"syscall"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

type Discoverer struct {
	FileSystem platform.FileSystem
	Runner     platform.Runner
	ModelPath  string
	LookupEnv  func(string) (string, bool)
}

func NewDiscoverer(filesystem platform.FileSystem, runner platform.Runner, modelPath string) Discoverer {
	return Discoverer{
		FileSystem: filesystem,
		Runner:     runner,
		ModelPath:  modelPath,
		LookupEnv:  os.LookupEnv,
	}
}

func (discoverer Discoverer) Discover(ctx context.Context) (hardware.Capabilities, error) {
	osRelease, err := discoverer.FileSystem.ReadFile("/etc/os-release")
	if err != nil {
		return hardware.Capabilities{}, err
	}
	memory, err := discoverer.FileSystem.ReadFile("/proc/meminfo")
	if err != nil {
		return hardware.Capabilities{}, err
	}
	cpu, err := discoverer.FileSystem.ReadFile("/proc/cpuinfo")
	if err != nil {
		return hardware.Capabilities{}, err
	}
	pci := discoverer.optionalCommand(ctx, "lspci", "-nn")
	nvidia := discoverer.optionalCommand(ctx, "nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits")
	rocmResult, rocmErr := discoverer.Runner.Run(ctx, platform.Command{
		Name:        "rocminfo",
		Args:        []string{"--version"},
		OutputLimit: 16 << 10,
	})
	rocm := rocmErr == nil && rocmResult.ExitCode == 0 && !rocmResult.StdoutTruncated
	total, free, err := volumeCapacity(discoverer.ModelPath)
	if err != nil {
		return hardware.Capabilities{}, err
	}
	desktop, kind := discoverer.desktop()
	return ParseSnapshot(Snapshot{
		OSRelease:        string(osRelease),
		Architecture:     runtime.GOARCH,
		CPUInfo:          string(cpu),
		MemoryInfo:       string(memory),
		PCIOutput:        string(pci),
		NVIDIAOutput:     string(nvidia),
		ROCmAvailable:    rocm,
		DesktopAvailable: desktop,
		DesktopKind:      kind,
		ModelVolumeTotal: total,
		ModelVolumeFree:  free,
	})
}

func (discoverer Discoverer) optionalCommand(ctx context.Context, name string, args ...string) []byte {
	result, err := discoverer.Runner.Run(ctx, platform.Command{
		Name:        name,
		Args:        args,
		OutputLimit: 256 << 10,
	})
	if err != nil || result.ExitCode != 0 || result.StdoutTruncated {
		return nil
	}
	return result.Stdout
}

func (discoverer Discoverer) desktop() (bool, string) {
	lookup := discoverer.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	kind, _ := lookup("XDG_CURRENT_DESKTOP")
	if kind == "" {
		kind, _ = lookup("DESKTOP_SESSION")
	}
	_, display := lookup("DISPLAY")
	_, wayland := lookup("WAYLAND_DISPLAY")
	return (display || wayland) && kind != "", kind
}

func volumeCapacity(name string) (uint64, uint64, error) {
	if name == "" {
		name = "/"
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(name, &stat); err != nil {
		return 0, 0, err
	}
	if stat.Blocks < 0 || stat.Bavail < 0 {
		return 0, 0, errors.New("invalid filesystem capacity")
	}
	blockSize := uint64(stat.Bsize)
	return uint64(stat.Blocks) * blockSize, uint64(stat.Bavail) * blockSize, nil
}
