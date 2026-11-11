# termux-setup
A shell script for setting up Termux the way I like.

## Dotfiles setup

Run the interactive bootstrap on macOS (requires [Homebrew](https://brew.sh))
or Ubuntu/Debian Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/artofthesmart/dotfiles-bootstrap/main/bootstrap.sh -o bootstrap.sh && bash ./bootstrap.sh
```

On Windows, install an Ubuntu distribution in WSL, then run this in PowerShell:

```powershell
wsl.exe -e bash -lc 'curl -fsSL https://raw.githubusercontent.com/artofthesmart/dotfiles-bootstrap/main/bootstrap.sh -o bootstrap.sh && bash ./bootstrap.sh'
```

macOS uses Homebrew for the shared tools. Linux-specific options (such as
Flatpak, Docker Engine, NVIDIA/CUDA, Steam, and GNOME tools) are only offered
on Ubuntu/Debian. The Windows command runs the Linux setup inside WSL.

## Local AI in VS Code

These independent setup wizards install/reuse VS Code and Ollama, prepare a
hardware-only prompt for you to paste into Gemini, check a local tool-capable
model, and configure a separate **Local AI** profile where you can open or clone
any repository.
The guides explain switching back to cloud Copilot. Local inference has no
per-request cloud-model fee; cloud features and billing remain separate.

| Your computer | One-page executive guide |
|---|---|
| M-series Mac | [Apple Silicon](docs/local-ai-macos-apple-silicon.md) |
| Intel Mac (CPU-only Ollama) | [Intel macOS](docs/local-ai-macos-intel.md) |
| Windows x64 | [Windows](docs/local-ai-windows.md) |
| Ubuntu / Debian desktop | [Ubuntu / Debian](docs/local-ai-ubuntu-debian.md) |

From this downloaded/extracted repository, inspect the script and launch it:

Mac / Linux:

```sh
bash ./scripts/setup-local-ai.sh
```

Windows PowerShell (normal user):

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\scripts\setup-local-ai.ps1
```

See [validation and troubleshooting](docs/local-ai-validation.md) for testing,
platform limitations and manual acceptance checks. These scripts do not change
telemetry/firewall settings and are not an offline/privacy solution.
The original `bootstrap.sh`, `setup.py`, and `setup_termux.sh` remain separate.
