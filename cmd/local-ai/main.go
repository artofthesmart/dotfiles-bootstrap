package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/catalog"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/cli"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/install"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/manager"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/ollama"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/update"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/vscode"
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
	clock := platform.SystemClock{}
	build := app.BuildInfo{Version: Version, Commit: Commit, Date: Date}
	capabilities := platform.Capabilities{
		FileSystem:   filesystem,
		Runner:       runner,
		Network:      network,
		Paths:        paths,
		Applications: platform.NewStandardApplicationLocator(filesystem),
		Processes:    platform.NewOSProcessManager(),
	}

	deps := cli.Dependencies{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Clock: clock, FS: filesystem, Runner: runner, Network: network,
		Build: build, Environment: supportedEnvironment(),
	}
	if userPaths, err := paths.UserPaths(); err == nil {
		transactionStore := state.JSONStore{
			FileSystem:           filesystem,
			Path:                 filepath.Join(userPaths.DataDir, "state", "transaction.json"),
			PrivacySafePath:      "${HOME}/.local/share/local-ai/state/transaction.json",
			ArchivePrivacyPrefix: "${HOME}/.local/share/local-ai/state/archive",
		}
		deploymentStore := state.JSONDeploymentStore{
			FileSystem: filesystem,
			Path:       filepath.Join(userPaths.DataDir, "state", "deployment.json"),
		}
		selfUpdater := update.SelfUpdater{
			FileSystem: filesystem, Runner: runner, Network: network, Paths: paths,
			State: deploymentStore, Clock: clock,
		}
		ghPath := ""
		if found, lookupErr := exec.LookPath("gh"); lookupErr == nil {
			ghPath = found
		}
		api, apiErr := ollama.NewLocalAPIClient("", nil)
		if apiErr == nil {
			ollamaManager := ollama.Manager{
				API: api, Registry: ollama.NewRegistryClient(network), Runner: runner,
				FileSystem: filesystem, Processes: capabilities.Processes, Services: capabilities.Services,
				Now: clock.Now,
			}
			repository, catalogErr := catalog.NewEmbeddedRepository()
			if catalogErr == nil {
				ollamaFactory := func(endpoint string) (ollama.Manager, error) {
					configuredAPI, err := ollama.NewLocalAPIClient(endpoint, nil)
					if err != nil {
						return ollama.Manager{}, err
					}
					configured := ollamaManager
					configured.API = configuredAPI
					return configured, nil
				}
				updaterFactory := func(verifyAttestation bool) update.Updater {
					configured := selfUpdater
					if verifyAttestation {
						configured.GHPath = ghPath
					}
					return configured
				}
				domainService := manager.Service{
					Clock: clock, Build: build, FileSystem: filesystem, Paths: paths,
					Hardware: newHardwareDiscoverer(filesystem, runner, userPaths.Home),
					Catalog:  repository, Ollama: ollamaManager, OllamaFactory: ollamaFactory,
					VSCode:  vscode.NewManager(vscode.Options{Platform: capabilities, Clock: clock}),
					Copilot: manager.CopilotAdapter{Platform: capabilities, Clock: clock},
					State:   transactionStore, Updater: selfUpdater,
					UpdaterFactory: updaterFactory,
					Uninstaller: install.Uninstaller{
						FileSystem: filesystem, Paths: paths, State: deploymentStore, Clock: clock,
					},
				}
				deps.Service = domainService
				deps.Updater = selfUpdater
			}
		}
	}
	os.Exit(cli.Run(context.Background(), os.Args[1:], deps))
}

func supportedEnvironment() map[string]string {
	result := map[string]string{}
	for _, key := range []string{"LOCAL_AI_CONFIG", "LOCAL_AI_OLLAMA_ENDPOINT", "NO_COLOR", "LOCAL_AI_VERIFY_ATTESTATION"} {
		if value, ok := os.LookupEnv(key); ok {
			result[key] = value
		}
	}
	return result
}
