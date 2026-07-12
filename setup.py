# /// script
# requires-python = ">=3.8"
# dependencies = [
#     "rich",
#     "questionary",
# ]
# ///

import os
import sys
import shlex
import platform
import subprocess
import questionary
from rich.console import Console
from rich.panel import Panel

console = Console()
HOME = os.path.expanduser("~")
USER = os.environ.get("USER", "root")

def run_cmd(command, desc=None, sudo=False, allow_fail=False):
    if sudo:
        # Wrap the whole command in `sudo bash -c '...'` rather than just
        # prefixing "sudo " — commands are often multi-line `&&` chains, and
        # a bare prefix only elevates the first sub-command, leaving the
        # rest to run unprivileged (e.g. causing dpkg lock permission errors).
        command = f"sudo bash -c {shlex.quote(command)}"

    # Output is streamed live (stdout/stderr/stdin all inherited from this
    # process) rather than captured, so slow steps stay visible and any
    # prompt a subcommand throws (e.g. a y/n confirmation) actually reaches
    # the user instead of silently blocking forever behind a spinner.
    if desc:
        console.print(f"[bold cyan]→ {desc}...[/bold cyan]")

    process = subprocess.run(command, shell=True, executable="/bin/bash")
    if process.returncode != 0 and not allow_fail:
        if desc:
            console.print(f"[bold red]Error:[/bold red] {desc} failed!")
        else:
            console.print(f"[bold red]Command failed:[/bold red] {command}")
        sys.exit(1)
    elif process.returncode == 0 and desc:
        console.print(f"[green]✓[/green] {desc} completed.")
    return process.returncode == 0

def install_base():
    run_cmd("apt-get update -y && apt-get upgrade -y", "Updating APT packages", sudo=True)
    pkgs = "vim python3 python3-pip python3-venv gpg make man-db gcc unzip wget curl git ripgrep fd-find fontconfig"
    run_cmd(f"apt-get install -y {pkgs}", "Installing base utilities", sudo=True)

def install_zsh():
    run_cmd("apt-get install -y zsh", "Installing Zsh", sudo=True)

    omz_dir = os.path.join(HOME, ".oh-my-zsh")
    zshrc = os.path.join(HOME, ".zshrc")
    if os.path.isdir(omz_dir) and not os.path.isfile(zshrc):
        # oh-my-zsh clones its repo before writing ~/.zshrc, so an install
        # interrupted partway (e.g. killed while stuck on a hidden prompt)
        # leaves this directory behind without ever finishing setup. Its
        # installer also refuses to run again while the directory exists,
        # so wipe the partial clone rather than falsely reporting "done".
        console.print("[yellow]![/yellow] Found an incomplete Oh My Zsh install, removing it before reinstalling.")
        run_cmd(f"rm -rf {omz_dir}", "Removing incomplete Oh My Zsh install")

    if not os.path.isdir(omz_dir):
        run_cmd("CHSH=no RUNZSH=no sh -c \"$(curl -fsSL https://raw.githubusercontent.com/ohmyzsh/ohmyzsh/master/tools/install.sh)\"", "Installing Oh My Zsh")
    else:
        console.print("[green]✓[/green] Oh My Zsh already installed.")

    zsh_custom = os.path.join(HOME, ".oh-my-zsh/custom")
    p10k_dir = os.path.join(zsh_custom, "themes/powerlevel10k")
    if not os.path.isdir(p10k_dir):
        run_cmd(f"git clone --depth=1 https://github.com/romkatv/powerlevel10k.git {p10k_dir}", "Installing Powerlevel10k")
        zshrc = os.path.join(HOME, ".zshrc")
        if os.path.isfile(zshrc):
            run_cmd(f"sed -i 's/ZSH_THEME=\".*\"/ZSH_THEME=\"powerlevel10k\\/powerlevel10k\"/g' {zshrc}", "Configuring P10k theme")
    else:
        console.print("[green]✓[/green] Powerlevel10k already installed.")

def install_build_essentials():
    pkgs = ("build-essential libssl-dev libffi-dev libncurses5-dev zlib1g-dev "
            "libbz2-dev libreadline-dev libsqlite3-dev llvm libncursesw5-dev "
            "xz-utils tk-dev libxml2-dev libxmlsec1-dev liblzma-dev")
    run_cmd(f"apt-get install -y {pkgs}", "Installing build essentials", sudo=True)

def install_uv():
    if run_cmd("command -v uv", allow_fail=True):
        console.print("[green]✓[/green] uv already installed.")
    else:
        run_cmd("curl -LsSf https://astral.sh/uv/install.sh | sh", "Installing Python uv")
        run_cmd("echo 'export PATH=\"$HOME/.local/bin:$PATH\"' >> ~/.zshrc", "Adding uv to Zsh PATH")

def install_gh():
    if run_cmd("command -v gh", allow_fail=True):
        console.print("[green]✓[/green] GitHub CLI already installed.")
        return
    cmds = """
    mkdir -p -m 755 /etc/apt/keyrings && \
    wget -nv -O /tmp/gh.gpg https://cli.github.com/packages/githubcli-archive-keyring.gpg && \
    cat /tmp/gh.gpg | tee /etc/apt/keyrings/githubcli-archive-keyring.gpg > /dev/null && \
    chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg && \
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | tee /etc/apt/sources.list.d/github-cli.list > /dev/null && \
    apt-get update -y && \
    apt-get install gh -y && \
    rm -f /tmp/gh.gpg
    """
    run_cmd(cmds, "Installing GitHub CLI", sudo=True)

def install_neovim():
    if run_cmd("command -v nvim", allow_fail=True):
        console.print("[green]✓[/green] Neovim already installed.")
    else:
        # Neovim only ships prebuilt tarballs for x86_64 and arm64 (Pi 3/4/
        # Zero 2 W and other 64-bit ARM boards) — grabbing the wrong one
        # downloads a binary the kernel can't execute (exec format error).
        arch_map = {"x86_64": "x86_64", "aarch64": "arm64", "arm64": "arm64"}
        nvim_arch = arch_map.get(platform.machine())
        if not nvim_arch:
            console.print(f"[bold red]Error:[/bold red] No prebuilt Neovim binary for architecture '{platform.machine()}'.")
            sys.exit(1)
        pkg = f"nvim-linux-{nvim_arch}"
        cmds = f"""
        curl -LO https://github.com/neovim/neovim/releases/latest/download/{pkg}.tar.gz && \
        rm -rf /opt/{pkg} && \
        tar -C /opt -xzf {pkg}.tar.gz && \
        rm {pkg}.tar.gz && \
        ln -sf /opt/{pkg}/bin/nvim /usr/local/bin/nvim
        """
        run_cmd(cmds, "Installing Neovim", sudo=True)

    nvim_config = os.path.join(HOME, ".config/nvim")
    if not os.path.isdir(nvim_config):
        run_cmd(f"git clone https://github.com/LazyVim/starter {nvim_config} && rm -rf {nvim_config}/.git", "Setting up LazyVim")
    else:
        console.print("[green]✓[/green] Neovim config exists. Skipping LazyVim.")

def install_nvm():
    nvm_dir = os.path.join(HOME, ".nvm")
    if os.path.exists(nvm_dir):
        run_cmd(f"rm -rf {nvm_dir}", sudo=True)
    run_cmd(f"mkdir -p {nvm_dir} && chown -R {USER} {nvm_dir}", sudo=True)
    run_cmd("curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.4/install.sh | bash", "Installing NVM")
    # Install Node 25
    cmds = f"[ -s \"{nvm_dir}/nvm.sh\" ] && \\. \"{nvm_dir}/nvm.sh\" && nvm install 25"
    run_cmd(cmds, "Installing Node.js via NVM")

def install_flatpak_codecs():
    run_cmd("apt-get install -y flatpak gnome-software-plugin-flatpak ubuntu-restricted-extras fonts-crosextra-caladea fonts-crosextra-carlito ffmpegthumbnailer unrar p7zip-full p7zip-rar", "Installing Flatpak & Codecs", sudo=True)
    run_cmd("flatpak remote-add --if-not-exists flathub https://dl.flathub.org/repo/flathub.flatpakrepo", "Adding Flathub Repo", sudo=True)

def install_tailscale(exit_node=False):
    if run_cmd("command -v tailscale", allow_fail=True):
        console.print("[green]✓[/green] Tailscale already installed.")
    else:
        run_cmd("curl -fsSL https://tailscale.com/install.sh | sh", "Installing Tailscale")

    if exit_node:
        cmds = """
        tee /etc/sysctl.d/99-tailscale.conf > /dev/null <<EOF
net.ipv4.ip_forward = 1
net.ipv6.conf.all.forwarding = 1
EOF
        sysctl -p /etc/sysctl.d/99-tailscale.conf
        """
        run_cmd(cmds, "Enabling IP Forwarding", sudo=True)

        # We need a dynamic script for the net interface
        dispatcher_script = """
        mkdir -p /etc/networkd-dispatcher/routable.d/
        tee /etc/networkd-dispatcher/routable.d/50-tailscale > /dev/null <<'EOF'
#!/bin/sh
NET_INTERFACE=$(ip -o route get 8.8.8.8 | cut -f 5 -d " ")
if [ -n "$NET_INTERFACE" ]; then
    ethtool -K "$NET_INTERFACE" rx-udp-gro-forwarding on rx-gro-list off || true
fi
EOF
        chmod 755 /etc/networkd-dispatcher/routable.d/50-tailscale
        """
        run_cmd(dispatcher_script, "Setting up Network Dispatcher for Tailscale", sudo=True)

        console.print("[yellow]Tailscale requires manual routing approval via admin console.[/yellow]")
        subnet_cmd = "SUBNET_ROUTE=$(ip route show dev $(ip -o route get 8.8.8.8 | cut -f 5 -d \" \") | grep -v default | awk '{print $1}' | head -n 1) && tailscale up --advertise-exit-node --advertise-routes=\"$SUBNET_ROUTE\" --accept-routes"
        run_cmd(subnet_cmd, "Activating Tailscale Exit Node", sudo=True, allow_fail=True)

def install_gemini():
    if run_cmd("command -v gemini", allow_fail=True):
        console.print("[green]✓[/green] Gemini CLI already installed.")
    else:
        nvm_dir = os.path.join(HOME, ".nvm")
        cmds = f"[ -s \"{nvm_dir}/nvm.sh\" ] && \\. \"{nvm_dir}/nvm.sh\" && npm install -g @google/gemini-cli"
        run_cmd(cmds, "Installing Gemini CLI")

def install_fonts():
    font_dir = os.path.join(HOME, ".local/share/fonts")
    if os.path.isfile(os.path.join(font_dir, "RobotoMonoNerdFont-Regular.ttf")):
        console.print("[green]✓[/green] Nerd Fonts already installed.")
    else:
        cmds = f"""
        mkdir -p {font_dir} && \
        wget -nv -O /tmp/RobotoMono.zip https://github.com/ryanoasis/nerd-fonts/releases/latest/download/RobotoMono.zip && \
        unzip -q -o /tmp/RobotoMono.zip -d {font_dir} && \
        rm /tmp/RobotoMono.zip && \
        fc-cache -fv
        """
        run_cmd(cmds, "Installing Roboto Nerd Font")

def install_bat():
    run_cmd("apt-get install -y bat", "Installing bat", sudo=True)
    run_cmd("echo \"alias bat='batcat'\" >> ~/.zshrc", "Adding bat alias")

def install_fzf():
    run_cmd(f"git clone --depth 1 https://github.com/junegunn/fzf.git {HOME}/.fzf && {HOME}/.fzf/install --all", "Installing fzf")

def install_monitors():
    run_cmd("apt-get install -y htop btop", "Installing htop & btop", sudo=True)

def install_fastfetch():
    run_cmd("apt-get install -y fastfetch", "Installing fastfetch", sudo=True)

def install_docker():
    run_cmd("apt-get install -y docker.io docker-compose", "Installing Docker & Compose", sudo=True)
    run_cmd(f"usermod -aG docker {USER}", "Adding user to docker group", sudo=True)
    run_cmd("systemctl enable docker && systemctl start docker", "Starting Docker service", sudo=True)
    
    console.print(Panel.fit("[yellow]Docker is installed! Please create your docker-compose.yml file in another terminal or editor.[/yellow]"))
    questionary.confirm("Press Enter to continue once you have created the file.", default=True).ask()

def install_zsh_plugins():
    zsh_custom = os.path.join(HOME, ".oh-my-zsh/custom")
    plugins_dir = os.path.join(zsh_custom, "plugins")
    run_cmd(f"git clone https://github.com/zsh-users/zsh-autosuggestions {plugins_dir}/zsh-autosuggestions", "Installing zsh-autosuggestions", allow_fail=True)
    run_cmd(f"git clone https://github.com/zsh-users/zsh-syntax-highlighting.git {plugins_dir}/zsh-syntax-highlighting", "Installing zsh-syntax-highlighting", allow_fail=True)
    
    zshrc = os.path.join(HOME, ".zshrc")
    if os.path.isfile(zshrc):
        run_cmd(f"sed -i 's/plugins=(git)/plugins=(git zsh-autosuggestions zsh-syntax-highlighting)/g' {zshrc}", "Configuring Zsh plugins")

def install_nvidia_cuda():
    if platform.machine() != "x86_64":
        # The graphics-drivers PPA and ubuntu-drivers tooling only target
        # x86_64 Ubuntu; NVIDIA's ARM (Jetson) stack is a different install
        # path entirely, so there's no equivalent package to fall back to.
        console.print(f"[yellow]![/yellow] Skipping NVIDIA/CUDA: not supported on architecture '{platform.machine()}'.")
        return
    run_cmd("add-apt-repository ppa:graphics-drivers/ppa -y && apt-get update -y", "Adding NVIDIA PPA", sudo=True)
    run_cmd("ubuntu-drivers install", "Installing recommended NVIDIA drivers", sudo=True)
    run_cmd("apt-get install -y nvidia-cuda-toolkit", "Installing CUDA Toolkit", sudo=True)
    run_cmd("echo 'export PATH=/usr/local/cuda/bin:$PATH' >> ~/.zshrc", "Adding CUDA bin to PATH")
    run_cmd("echo 'export LD_LIBRARY_PATH=/usr/local/cuda/lib64:$LD_LIBRARY_PATH' >> ~/.zshrc", "Adding CUDA lib to LD_LIBRARY_PATH")

def install_steam():
    if platform.machine() != "x86_64":
        # Valve doesn't ship a Linux Steam client for ARM at all, so there's
        # no alternate package to install here — just fail fast and clearly.
        console.print(f"[yellow]![/yellow] Skipping Steam: no Linux build exists for architecture '{platform.machine()}'.")
        return
    cmds = """
    dpkg --add-architecture i386 && \
    apt-get update -y && \
    wget -qO /tmp/steam.gpg https://repo.steampowered.com/steam/archive/stable/steam.gpg && \
    cp /tmp/steam.gpg /usr/share/keyrings/steam.gpg && \
    rm /tmp/steam.gpg && \
    tee /etc/apt/sources.list.d/steam-stable.list > /dev/null <<'EOF'
deb [arch=amd64,i386 signed-by=/usr/share/keyrings/steam.gpg] https://repo.steampowered.com/steam/ stable steam
deb-src [arch=amd64,i386 signed-by=/usr/share/keyrings/steam.gpg] https://repo.steampowered.com/steam/ stable steam
EOF
    apt-get update -y && \
    apt-get install -y libgl1-mesa-dri:amd64 libgl1-mesa-dri:i386 libgl1:amd64 libgl1:i386 steam-launcher
    """
    run_cmd(cmds, "Installing Steam & Mesa drivers", sudo=True)

def install_antigravity():
    cmds = """
    mkdir -p /etc/apt/keyrings && \
    curl -fsSL https://us-central1-apt.pkg.dev/doc/repo-signing-key.gpg | gpg --dearmor --yes -o /etc/apt/keyrings/antigravity-repo-key.gpg && \
    echo "deb [signed-by=/etc/apt/keyrings/antigravity-repo-key.gpg] https://us-central1-apt.pkg.dev/projects/antigravity-auto-updater-dev/ antigravity-debian main" | tee /etc/apt/sources.list.d/antigravity.list > /dev/null && \
    apt-get update -y && \
    apt-get install -y antigravity
    """
    run_cmd(cmds, "Installing Antigravity Auto Updater", sudo=True)

def install_gnome_tools():
    run_cmd("apt-get install -y gnome-tweaks gnome-shell-extension-manager", "Installing GNOME Customization Tools", sudo=True)

def install_extra_utilities():
    run_cmd("apt-get install -y ncdu vlc", "Installing ncdu & vlc", sudo=True)
    run_cmd("ufw enable", "Enabling UFW Firewall", sudo=True)

def main():
    console.print(Panel.fit("[bold blue]Antigravity Shell Setup[/bold blue]", subtitle="Modern Hybrid Installer"))
    
    choices = [
        questionary.Choice("Base System Utilities & Updates", checked=True),
        questionary.Choice("Zsh, Oh My Zsh, Powerlevel10k", checked=True),
        questionary.Choice("Build Essentials (Compilers & Libraries)", checked=True),
        questionary.Choice("Python 'uv' Setup", checked=True),
        questionary.Choice("GitHub CLI", checked=True),
        questionary.Choice("Neovim & LazyVim", checked=True),
        questionary.Choice("Node.js (NVM)", checked=False),
        questionary.Choice("Flatpak Setup & System Codecs", checked=False),
        questionary.Choice("Tailscale", checked=False),
        questionary.Choice("Tailscale Exit Node Configuration", checked=False),
        questionary.Choice("Gemini CLI", checked=False),
        questionary.Choice("Nerd Fonts", checked=False),
        questionary.Choice("Docker Engine & Compose", checked=False),
        questionary.Choice("Zsh Plugins (Autosuggestions & Syntax Highlighting)", checked=False),
        questionary.Choice("NVIDIA Drivers & CUDA Toolkit", checked=False),
        questionary.Choice("Steam & Gaming Setup", checked=False),
        questionary.Choice("Antigravity Package", checked=False),
        questionary.Choice("GNOME Customization Tools", checked=False),
        questionary.Choice("Extra Utilities (ncdu, vlc, UFW firewall)", checked=False),
        questionary.Choice("Advanced CLI: bat (Syntax Cat)", checked=False),
        questionary.Choice("Advanced CLI: fzf (Fuzzy Finder)", checked=False),
        questionary.Choice("Advanced CLI: htop & btop (Monitors)", checked=False),
        questionary.Choice("Advanced CLI: fastfetch (System Info)", checked=False),
    ]

    if os.environ.get("SETUP_NONINTERACTIVE"):
        selected = [c.title for c in choices if c.checked]
        console.print(f"[cyan]Non-interactive mode: installing defaults ({len(selected)} components).[/cyan]")
    else:
        selected = questionary.checkbox(
            "Select the components you want to install:",
            choices=choices
        ).ask()

        if selected is None:
            console.print("[yellow]Installation cancelled.[/yellow]")
            sys.exit(0)

    if "Base System Utilities & Updates" in selected:
        install_base()
    if "Zsh, Oh My Zsh, Powerlevel10k" in selected:
        install_zsh()
    if "Build Essentials (Compilers & Libraries)" in selected:
        install_build_essentials()
    if "Python 'uv' Setup" in selected:
        install_uv()
    if "GitHub CLI" in selected:
        install_gh()
    if "Neovim & LazyVim" in selected:
        install_neovim()
    if "Node.js (NVM)" in selected:
        install_nvm()
    if "Flatpak Setup & System Codecs" in selected:
        install_flatpak_codecs()
    if "Tailscale" in selected or "Tailscale Exit Node Configuration" in selected:
        install_tailscale("Tailscale Exit Node Configuration" in selected)
    if "Gemini CLI" in selected:
        install_gemini()
    if "Nerd Fonts" in selected:
        install_fonts()
    if "Docker Engine & Compose" in selected:
        install_docker()
    if "Zsh Plugins (Autosuggestions & Syntax Highlighting)" in selected:
        install_zsh_plugins()
    if "NVIDIA Drivers & CUDA Toolkit" in selected:
        install_nvidia_cuda()
    if "Steam & Gaming Setup" in selected:
        install_steam()
    if "Antigravity Package" in selected:
        install_antigravity()
    if "GNOME Customization Tools" in selected:
        install_gnome_tools()
    if "Extra Utilities (ncdu, vlc, UFW firewall)" in selected:
        install_extra_utilities()
    if "Advanced CLI: bat (Syntax Cat)" in selected:
        install_bat()
    if "Advanced CLI: fzf (Fuzzy Finder)" in selected:
        install_fzf()
    if "Advanced CLI: htop & btop (Monitors)" in selected:
        install_monitors()
    if "Advanced CLI: fastfetch (System Info)" in selected:
        install_fastfetch()

    console.print(Panel.fit("[bold green]Setup Complete![/bold green]\nRestart your terminal or re-login for the default shell and font changes to take effect."))

if __name__ == "__main__":
    main()
