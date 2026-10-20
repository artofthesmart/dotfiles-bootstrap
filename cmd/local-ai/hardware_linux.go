//go:build linux

package main

import (
	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	platformlinux "github.com/artofthesmart/dotfiles-bootstrap/internal/platform/linux"
)

func newHardwareDiscoverer(filesystem platform.FileSystem, runner platform.Runner, modelPath string) hardware.Discoverer {
	return platformlinux.NewDiscoverer(filesystem, runner, modelPath)
}
