//go:build darwin

package main

import (
	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	platformmacos "github.com/artofthesmart/dotfiles-bootstrap/internal/platform/macos"
)

func newHardwareDiscoverer(_ platform.FileSystem, runner platform.Runner, modelPath string) hardware.Discoverer {
	return platformmacos.NewDiscoverer(runner, modelPath)
}
