//go:build windows

package windows

import (
	"context"
	"fmt"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

const snapshotScript = `$ErrorActionPreference='Stop';` +
	`$memory=@(Get-CimInstance Win32_PhysicalMemory | ForEach-Object {[uint64]$_.Capacity});` +
	`$computer=Get-CimInstance Win32_ComputerSystem;` +
	`$os=Get-CimInstance Win32_OperatingSystem;` +
	`$cpu=Get-CimInstance Win32_Processor | Select-Object -First 1;` +
	`$gpus=@(Get-CimInstance Win32_VideoController | ForEach-Object {[string]$_.Name});` +
	`$drive=Get-CimInstance Win32_LogicalDisk -Filter ("DeviceID='" + $env:SystemDrive + "'");` +
	`[ordered]@{version=[string]$os.Version;architecture=[string]$cpu.Architecture;cpu_model=[string]$cpu.Name;` +
	`installed_memory=$memory;visible_memory=[uint64]$computer.TotalPhysicalMemory;gpu_models=$gpus;` +
	`desktop=[bool](Get-Process explorer -ErrorAction SilentlyContinue);` +
	`model_volume_total=[uint64]$drive.Size;model_volume_free=[uint64]$drive.FreeSpace}|ConvertTo-Json -Compress`

type Discoverer struct {
	Runner platform.Runner
}

func NewDiscoverer(runner platform.Runner) Discoverer {
	return Discoverer{Runner: runner}
}

func (discoverer Discoverer) Discover(ctx context.Context) (hardware.Capabilities, error) {
	result, err := discoverer.Runner.Run(ctx, platform.Command{
		Name:        "powershell.exe",
		Args:        []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", snapshotScript},
		OutputLimit: 256 << 10,
	})
	if err != nil {
		return hardware.Capabilities{}, err
	}
	if result.ExitCode != 0 || result.StdoutTruncated {
		return hardware.Capabilities{}, fmt.Errorf("%w: Windows discovery command exited with code %d", hardware.ErrInvalidEvidence, result.ExitCode)
	}
	nvidia, nvidiaErr := discoverer.Runner.Run(ctx, platform.Command{
		Name:        "nvidia-smi.exe",
		Args:        []string{"--query-gpu=name,memory.total", "--format=csv,noheader,nounits"},
		OutputLimit: 64 << 10,
	})
	var nvidiaOutput []byte
	if nvidiaErr == nil && nvidia.ExitCode == 0 && !nvidia.StdoutTruncated {
		nvidiaOutput = nvidia.Stdout
	}
	return ParseSnapshot(result.Stdout, nvidiaOutput)
}
