# Installer sources and update guide

Last reviewed: 2026-10-20

This inventory covers `bootstrap.sh`, `setup.py`, `setup_termux.sh`, and the
independent local-AI wizards under `scripts/`.
Prefer each project's official documentation or release page when changing
installer URLs, package names, versions, or platform assumptions.

`bootstrap.sh` installs Ubuntu/APT prerequisites and launches `setup.py`, which
provides the component selector. It does not install every optional component
itself; for example, Copilot CLI is installed only when selected in `setup.py`.
When run from a checkout containing `setup.py`, the bootstrap uses that local
copy; when run standalone, it downloads `setup.py` from the repository's
`main` branch.

## Platform assessment

`bootstrap.sh` and `setup.py` are Ubuntu/Linux installers, not cross-platform
installers. The bootstrap immediately uses `apt-get`; the Python installer also
uses APT repositories and packages, `dpkg`, `/etc/apt`, Linux Neovim tarballs
installed under `/opt`, and Linux-only service/network tools. macOS has none of
these interfaces by default, and the Linux `sed -i` syntax is not portable to
macOS. Some individual projects below support macOS, but that does not make
these scripts equally effective there; a macOS path would need a separate
Homebrew-based implementation and platform-specific handling. See
[Homebrew's macOS installation requirements](https://docs.brew.sh/Installation)
before designing that path.

On a headless Ubuntu host, the default components can work if the host has
working `sudo` access and the required packages/network access. For unattended
bootstrap, set both `BOOTSTRAP_AUTO=1` and `SETUP_NONINTERACTIVE=1`; ensure
`sudo` will not require an interactive password (or run in an environment
where it is already authorized). Optional Docker installation still asks an
interactive confirmation, so do not select it in a headless run without
changing that behavior. The default set also runs `apt-get upgrade -y`, which
may update the whole host. GUI-oriented options (GNOME tools, codecs, Steam,
fonts) are generally not useful on a headless server. GitHub Copilot CLI is
usable in a terminal on a headless host, but authentication must be completed
separately.

## `bootstrap.sh` and Python installer

| Component / current installer behavior | Maintainer source and update reference |
|---|---|
| Ubuntu base packages: `curl`, `python3`, `python3-pip`, `python3-venv`, `git`, `zsh`; `setup.py` additionally installs `vim`, `gpg`, `make`, `man-db`, `gcc`, `unzip`, `wget`, `ripgrep`, `fd-find`, and `fontconfig` | [Ubuntu package search](https://packages.ubuntu.com/); packages and versions come from the host's configured Ubuntu repositories |
| Python UI dependencies: `rich`, `questionary` (declared in the PEP 723 script metadata and supplied to `uv run`) | [Rich](https://github.com/Textualize/rich) · [Questionary](https://github.com/tmbo/questionary) · [uv script dependencies](https://docs.astral.sh/uv/guides/scripts/) |
| uv installer: `https://astral.sh/uv/install.sh` | [uv installation](https://docs.astral.sh/uv/getting-started/installation/) |
| Zsh, Oh My Zsh, and Powerlevel10k | [Zsh](https://www.zsh.org/) · [Oh My Zsh install/update](https://github.com/ohmyzsh/ohmyzsh) · [Powerlevel10k](https://github.com/romkatv/powerlevel10k) |
| Build tools and libraries: `build-essential`, SSL/FFI, ncurses, zlib, bz2, readline, SQLite, LLVM, xz, Tcl/Tk, XML, and lzma development packages | [Ubuntu package search](https://packages.ubuntu.com/); build-essential and library package names are distribution-specific |
| GitHub CLI: signed GitHub APT repository and `gh` package | [Official Linux installation and upgrade instructions](https://github.com/cli/cli/blob/trunk/docs/install_linux.md) |
| Neovim: latest Linux x86_64/arm64 release tarball installed in `/opt` | [Neovim install instructions and supported archives](https://neovim.io/doc/install/) · [Neovim releases](https://github.com/neovim/neovim/releases) |
| LazyVim: clones the starter configuration and removes its `.git` directory | [LazyVim starter](https://github.com/LazyVim/starter) · [LazyVim documentation](https://www.lazyvim.org/) |
| Node.js (optional): removes the existing `~/.nvm` directory, installs nvm from the **v0.40.4** install script, then hard-codes `nvm install 25` | [nvm repository and releases](https://github.com/nvm-sh/nvm) · [Node.js release schedule](https://nodejs.org/en/about/previous-releases) |
| Gemini CLI (optional): global npm package `@google/gemini-cli` | [Gemini CLI repository](https://github.com/google-gemini/gemini-cli) · [Google quick guide](https://codelabs.developers.google.com/codelabs/quick-guide-to-gemini-cli) |
| Tailscale (optional): official Linux install script; exit-node mode also writes Linux sysctl/network-dispatcher configuration | [Linux installation](https://tailscale.com/docs/install/linux) · [Exit nodes](https://tailscale.com/docs/features/exit-nodes) |
| Nerd Fonts (optional): downloads the latest RobotoMono archive and runs `fc-cache` | [Nerd Fonts releases and downloads](https://github.com/ryanoasis/nerd-fonts/releases) |
| Docker Engine and Compose (optional): currently Ubuntu `docker.io` and `docker-compose` packages, then enables `docker` with `systemctl` | [Ubuntu Docker Engine instructions](https://docs.docker.com/engine/install/ubuntu/) · [Docker Linux post-installation](https://docs.docker.com/engine/install/linux-postinstall/) |
| Zsh plugins (optional): clones autosuggestions and syntax-highlighting repositories | [zsh-autosuggestions](https://github.com/zsh-users/zsh-autosuggestions) · [zsh-syntax-highlighting](https://github.com/zsh-users/zsh-syntax-highlighting) |
| NVIDIA drivers/CUDA (optional): Ubuntu graphics-drivers PPA, `ubuntu-drivers`, and `nvidia-cuda-toolkit` | [Ubuntu NVIDIA driver guidance](https://ubuntu.com/server/docs/nvidia-drivers-installation/) · [NVIDIA CUDA Linux installation guide](https://docs.nvidia.com/cuda/cuda-installation-guide-linux/) |
| Steam (optional): Steam's Debian APT repository plus 32-bit Mesa packages; x86_64 only | [Steam for Linux](https://store.steampowered.com/about/) · [Steam Debian repository](https://repo.steampowered.com/steam/) |
| GitHub Copilot CLI (optional, installed by `setup.py`): installs the latest release with GitHub's Linux/macOS install script (`https://gh.io/copilot-install`); run as the user, so the installer defaults to `$HOME/.local` | [Official installation and update instructions](https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/install-copilot-cli) · [Copilot CLI repository](https://github.com/github/copilot-cli) |
| GNOME tools (optional): `gnome-tweaks` and `gnome-shell-extension-manager` | [GNOME Tweaks](https://gitlab.gnome.org/GNOME/gnome-tweaks) · [Extension Manager](https://github.com/mjakeman/extension-manager) |
| Extra utilities (optional): `ncdu`, `vlc`, `ufw`; enables UFW | [Ubuntu package search](https://packages.ubuntu.com/) · [UFW](https://launchpad.net/ufw) |
| `bat` (optional): Ubuntu package and a `batcat` alias | [bat project](https://github.com/sharkdp/bat) |
| `fzf` (optional): shallow clone and runs the project's install script | [fzf project and install/update instructions](https://github.com/junegunn/fzf) |
| `htop` and `btop` (optional) | [htop](https://github.com/htop-dev/htop) · [btop](https://github.com/aristocratos/btop) |
| `fastfetch` (optional) | [fastfetch project](https://github.com/fastfetch-cli/fastfetch) |

### Version/update notes

- Most APT packages track the configured Ubuntu release rather than upstream
  latest. Check package availability and release compatibility before changing
  package names or adding third-party repositories.
- Neovim, Nerd Fonts, fzf, and several Git clones use moving `latest` or
  default-branch targets. For reproducible setups, consider pinning releases or
  commits and documenting the intended upgrade cadence.
- The Node component installs Node.js 25, which reached end of life on
  2026-03-31. Review the official Node.js release schedule before retaining or
  changing this pin; select a maintained release appropriate to the use case.
- Copilot CLI's official install script supports macOS and Linux and can install
  without Node.js. The `bootstrap.sh`/`setup.py` combination remains
  Ubuntu/APT-specific; the Copilot CLI component alone does not make the
  bootstrap macOS-compatible. On first launch, run `copilot` and use `/login`
  (or `copilot login`); an active Copilot plan is required, and organization
  policy can disable CLI access. Headless environments use device-code
  authentication; if no Linux keyring is available, credentials may be stored
  in plaintext in `~/.copilot/config.json`.
- Docker's documentation distinguishes Docker Engine's official APT repository
  from Ubuntu's `docker.io` / `docker-compose` packages. Reassess the package
  source and Compose v1/v2 behavior when updating this component.

## `setup_termux.sh`

This is a separate Android/Termux installer; it uses Termux's `pkg` package
manager and should not be treated as either the Ubuntu or macOS path.

| Component / current installer behavior | Maintainer source and update reference |
|---|---|
| Termux packages: `man`, `neovim`, `wget`, `python`, `zsh`, `git`, `gitui`, `mc` | [Termux package management](https://wiki.termux.com/wiki/Package_Management) · [Termux packages](https://github.com/termux/termux-packages) |
| Oh My Zsh and Powerlevel10k | [Oh My Zsh](https://github.com/ohmyzsh/ohmyzsh) · [Powerlevel10k](https://github.com/romkatv/powerlevel10k) |
| Roboto Mono Nerd Font, downloaded from the Nerd Fonts repository's `master` branch | [Nerd Fonts](https://github.com/ryanoasis/nerd-fonts) |
| LazyVim starter for Neovim | [LazyVim starter](https://github.com/LazyVim/starter) |
| Termux font/settings reload command | [Termux application](https://github.com/termux/termux-app) |

## Maintenance checklist

When updating an installer, verify the upstream installation page, supported
OS/architecture matrix, signing-key/repository instructions, and upgrade path.
Prefer signed package repositories or versioned releases over unpinned remote
scripts where practical. After changing an installer source, test the selected
component on the target OS; test unattended mode separately if the script is
used on a headless host.

## Local AI VS Code wizards

`scripts/setup-local-ai.sh` supports macOS 14+ on Apple Silicon/Intel and
Ubuntu/Debian desktops on x86-64/ARM64. `scripts/setup-local-ai.ps1` targets
Windows x64 10 22H2+ and Windows PowerShell 5.1+/PowerShell 7. Native installers,
GPU behavior and VS Code Agent workflows require real-machine acceptance;
mocked test success is not proof of platform compatibility.

| Component / behavior | Primary reference |
|---|---|
| VS Code platform downloads, official Debian package installation, app/CLI paths | [macOS](https://code.visualstudio.com/docs/setup/mac), [Windows](https://code.visualstudio.com/docs/setup/windows), [Linux](https://code.visualstudio.com/docs/setup/linux) |
| VS Code update endpoints: `latest/darwin/stable` (Intel ZIP), `latest/darwin-arm64/stable`, `latest/linux-deb-x64/stable`, `latest/linux-deb-arm64/stable`, `latest/win32-x64-user/stable`, under `https://update.code.visualstudio.com/` | [VS Code download API](https://code.visualstudio.com/docs/supporting/faq#_previous-release-versions); verify actual redirects before changing format/architecture assumptions |
| Ollama `.dmg`, Windows user installer, Linux installer downloaded for review before execution | [macOS](https://docs.ollama.com/macos), [Windows](https://docs.ollama.com/windows), [Linux](https://docs.ollama.com/linux), [official releases](https://github.com/ollama/ollama/releases) |
| Official `Ollama.ollama` provider extension; current manifest requires VS Code 1.127+; default loopback discovery | [Ollama extension](https://github.com/ollama/ollama-vscode), [manifest](https://github.com/ollama/ollama-vscode/blob/main/package.json) |
| Per-model `num_ctx` discovery and 65536-token recommendation for agents; this wizard enforces it as the minimum and checks reported model support | [Provider](https://github.com/ollama/ollama-vscode/blob/main/src/provider.ts), [context handling](https://github.com/ollama/ollama-vscode/blob/main/src/contextLength.ts) |
| VS Code 1.127+ bundles `GitHub.copilot-chat` and enables automatic built-in updates; the wizard must not reinstall it from the marketplace | [VS Code 1.127 product configuration](https://github.com/microsoft/vscode/blob/1.127.0/product.json), [bundled Copilot manifest](https://github.com/microsoft/vscode/blob/1.127.0/extensions/copilot/package.json), [upstream built-in install failure](https://github.com/microsoft/vscode/issues/327484) |
| Windows download progress uses built-in `Write-Progress` with streamed .NET responses; no PowerShell 7 UI-module dependency | [Write-Progress](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.utility/write-progress), [HTTP request timeouts](https://learn.microsoft.com/en-us/dotnet/api/system.net.httpwebrequest.readwritetimeout) |
| Installer and registry requests require HTTPS initially and after redirects; Bash uses curl protocol restrictions and Windows validates the final response URI | [curl protocol restrictions](https://curl.se/docs/manpage.html#--proto-redir), [HttpWebResponse.ResponseUri](https://learn.microsoft.com/en-us/dotnet/api/system.net.httpwebresponse.responseuri) |
| Named Local AI profile creation by launching VS Code with `--profile`, followed by profile-scoped extension installation and verification | [CLI](https://code.visualstudio.com/docs/configure/command-line), [profiles](https://code.visualstudio.com/docs/configure/profiles) |
| BYOK agents, tool capability, utility models, deprecated built-in Ollama provider, account-dependent feature limits | [VS Code language models](https://code.visualstudio.com/docs/agent-customization/language-models) |
| Local-harness **Configure Tools**, the 128-tool per-request limit, tool approvals, and experimental virtual-tool grouping | [Use tools with agents](https://code.visualstudio.com/docs/agents/run/tools), [VS Code 1.103 tool grouping notes](https://code.visualstudio.com/updates/v1_103#_tool-grouping-experimental) |
| Local model pulls, GGUF import and context-budget aliases | [CLI](https://docs.ollama.com/cli), [import](https://docs.ollama.com/import), [Modelfile](https://docs.ollama.com/modelfile) |
| Exact-tag preflight reads the Ollama registry manifest for layer size and a 1 MiB byte range from the GGUF model layer for `*.context_length`; failure stops before `ollama pull` | [Ollama registry/download implementation](https://github.com/ollama/ollama/blob/main/server/download.go), [Ollama GGUF metadata implementation](https://github.com/ollama/ollama/tree/main/fs/gguf), [GGUF specification](https://github.com/ggml-org/ggml/blob/master/docs/gguf.md) |
| Runtime Gemini candidate shortlist; each exact tag is still registry-preflighted and locally tool-tested | [Llama 3.1 8B](https://ollama.com/library/llama3.1:8b), [Mistral Nemo 12B](https://ollama.com/library/mistral-nemo:12b), [GPT-OSS 20B](https://ollama.com/library/gpt-oss:20b), [Devstral 24B](https://ollama.com/library/devstral:24b), [Qwen3 Coder 30B](https://ollama.com/library/qwen3-coder:30b) |
| Go manager catalog review dated October 20, 2026: exact local tags, coding/tool claims, native context, and current lifecycle status | [Ollama local coding guidance](https://ollama.com/blog/launch), [Qwen3-Coder](https://ollama.com/library/qwen3-coder), [GLM-4.7-Flash](https://ollama.com/library/glm-4.7-flash), [GPT-OSS](https://ollama.com/library/gpt-oss), [Mistral NeMo](https://ollama.com/library/mistral-nemo), [Devstral Small 2 lifecycle](https://mistral.ai/news/devstral-2-vibe-cli/) |
| Go manager catalog upstream model and license evidence | [Qwen3-Coder model card and Apache-2.0 license](https://huggingface.co/Qwen/Qwen3-Coder-30B-A3B-Instruct), [GLM-4.7-Flash model card](https://huggingface.co/zai-org/GLM-4.7-Flash), [GPT-OSS model card and Apache-2.0 license](https://huggingface.co/openai/gpt-oss-20b), [Mistral NeMo model card and Apache-2.0 declaration](https://huggingface.co/mistralai/Mistral-Nemo-Instruct-2407) |
| Catalog maximum tested Ollama version and release date | [Ollama v0.40.6](https://github.com/ollama/ollama/releases/tag/v0.40.6) |
| Loopback API `/api/version`, `/api/show`, `/api/chat`, `/api/ps`, cloud metadata, tool-call validation, and actual allocated context | [Official API reference](https://github.com/ollama/ollama/blob/main/docs/api.md), [response types](https://github.com/ollama/ollama/blob/main/api/types.go), [context length](https://docs.ollama.com/context-length) |
| Natural-language `get_temperature` smoke test; no actual tool execution | [Ollama tool-calling example](https://docs.ollama.com/capabilities/tool-calling) |
| Windows dynamic shared-memory accounting; 50% of system RAM used only as a planning ceiling, not an Ollama guarantee | [Microsoft DirectX: GPUs in Task Manager](https://devblogs.microsoft.com/directx/gpus-in-the-task-manager/) |
| Installed RAM is summed from memory-device capacities and labelled separately from Windows-visible RAM | [Win32_PhysicalMemory.Capacity](https://learn.microsoft.com/en-us/windows/win32/cimwin32prov/win32-physicalmemory), [TotalPhysicalMemory caveat](https://learn.microsoft.com/en-us/windows/win32/cimwin32prov/win32-computersystem) |
| Optional jq 1.8.2 standalone binary, installed privately, checked against embedded architecture-specific SHA-256 | [jq downloads](https://jqlang.org/download/), [upstream checksums](https://raw.githubusercontent.com/jqlang/jq/master/sig/v1.8.2/sha256sum.txt) |

The wizard does not call Gemini: it prepares an allowlisted prompt for the user
to review and paste. Model choices are deliberately not pinned to a supposedly
universal optimum. Prompt guidance asks Gemini for current model-source links,
licenses, size, quantization, context and a smaller fallback. Gemini is advisory:
the wizard first builds a runtime-verified exact-tag shortlist and requires
Gemini to choose from it, then reuses that metadata before an Ollama Library
pull. Hugging Face is a
guided user download of a compatible single-file GGUF, not arbitrary repository
execution.

Only the optional jq helper is pinned and checksum-verified by the Bash wizard.
App downloads follow upstream stable/current installer URLs over HTTPS; Windows
also checks Authenticode publisher signatures. Do not describe all installer
paths as release-pinned or independently checksum-verified.
