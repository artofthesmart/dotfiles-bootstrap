//go:build windows

package main

import (
	"github.com/artofthesmart/dotfiles-bootstrap/internal/hardware"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	platformwindows "github.com/artofthesmart/dotfiles-bootstrap/internal/platform/windows"
)

func newHardwareDiscoverer(_ platform.FileSystem, runner platform.Runner, _ string) hardware.Discoverer {
	return platformwindows.NewDiscoverer(runner)
}
