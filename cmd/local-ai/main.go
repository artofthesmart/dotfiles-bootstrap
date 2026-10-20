package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/cli"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/update"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func main() {
	filesystem := platform.OSFileSystem{}
	runner := platform.NewCommandRunner()
	network := platform.NewHTTPNetwork()
	paths := platform.NewStandardPathProvider()
	deps := cli.Dependencies{
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Clock:   platform.SystemClock{},
		FS:      filesystem,
		Runner:  runner,
		Network: network,
		Build: app.BuildInfo{
			Version: Version,
			Commit:  Commit,
			Date:    Date,
		},
	}
	if userPaths, err := paths.UserPaths(); err == nil {
		deploymentStore := state.JSONDeploymentStore{
			FileSystem: filesystem,
			Path:       filepath.Join(userPaths.DataDir, "state", "deployment.json"),
		}
		selfUpdater := update.SelfUpdater{
			FileSystem: filesystem,
			Runner:     runner,
			Network:    network,
			Paths:      paths,
			State:      deploymentStore,
			Clock:      platform.SystemClock{},
		}
		if os.Getenv("LOCAL_AI_VERIFY_ATTESTATION") == "1" {
			if ghPath, lookupErr := exec.LookPath("gh"); lookupErr == nil {
				selfUpdater.GHPath = ghPath
			}
		}
		deps.Updater = selfUpdater
	}

	os.Exit(cli.Run(context.Background(), os.Args[1:], deps))
}
