package main

import (
	"context"
	"os"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/cli"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func main() {
	deps := cli.Dependencies{
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Clock:   platform.SystemClock{},
		FS:      platform.OSFileSystem{},
		Runner:  platform.NewCommandRunner(),
		Network: platform.NewHTTPNetwork(),
		Build: app.BuildInfo{
			Version: Version,
			Commit:  Commit,
			Date:    Date,
		},
	}

	os.Exit(cli.Run(context.Background(), os.Args[1:], deps))
}
