#!/bin/bash
set -e

# Setup Script Bootstrap
# Download and run with:
# ```bash
# wget https://raw.githubusercontent.com/artofthesmart/dotfiles-bootstrap/main/bootstrap.sh -O bootstrap.sh
# chmod +x bootstrap.sh
# ./bootstrap.sh
# ```
#
# For unattended use (e.g. a container init script), set BOOTSTRAP_AUTO=1 to
# skip the tty/sudo-prompt guards below, and SETUP_NONINTERACTIVE=1 so
# setup.py installs its default component set without prompting.

# Prevent piping to bash, unless explicitly running unattended
if [ ! -t 0 ] && [ -z "$BOOTSTRAP_AUTO" ]; then
    echo "ERROR: This script must be downloaded and run directly, not piped to bash."
    echo "Please run: wget <URL> -O bootstrap.sh && chmod +x bootstrap.sh && ./bootstrap.sh"
    exit 1
fi

echo "--- Bootstrapping shell-setup ---"

case "$(uname -s)" in
    Darwin)
        if ! command -v brew >/dev/null 2>&1; then
            echo "ERROR: Homebrew is required for macOS. Install it from https://brew.sh, then rerun this script."
            exit 1
        fi
        brew update
        brew install git
        ;;
    Linux)
        if ! command -v apt-get >/dev/null 2>&1; then
            echo "ERROR: Linux setup requires an Ubuntu/Debian host with apt-get."
            exit 1
        fi
        if [ "$EUID" -ne 0 ]; then
            if ! command -v sudo >/dev/null 2>&1; then
                echo "ERROR: You are not root and sudo is not installed. Please run as root."
                exit 1
            fi
            if [ -z "$BOOTSTRAP_AUTO" ]; then
                sudo -v
            fi
        fi
        echo "Installing base dependencies..."
        sudo apt-get update -y
        sudo apt-get install -y curl python3 python3-pip python3-venv git zsh
        ;;
    *)
        echo "ERROR: Supported systems are macOS and Ubuntu/Debian Linux (including WSL)."
        exit 1
        ;;
esac

# Install uv if not present
if ! command -v uv >/dev/null 2>&1; then
    echo "Installing uv..."
    curl -LsSf https://astral.sh/uv/install.sh | sh
    export PATH="$HOME/.local/bin:$PATH"
fi

# Make sure zsh is available and the default shell.
USER_NAME="${USER:-$(whoami)}"
ZSH_PATH="$(command -v zsh || true)"
if [ -z "$ZSH_PATH" ]; then
    if [ "$(uname -s)" = "Darwin" ]; then
        brew install zsh
        ZSH_PATH="$(command -v zsh)"
    else
        echo "ERROR: zsh installation failed."
        exit 1
    fi
fi
if [ "$SHELL" != "$ZSH_PATH" ]; then
    echo "Setting zsh as default shell..."
    if [ "$(uname -s)" = "Darwin" ]; then
        chsh -s "$ZSH_PATH" "$USER_NAME"
    else
        sudo chsh -s "$ZSH_PATH" "$USER_NAME"
    fi
fi

# Ensure setup.py exists, downloading it if this is a standalone bootstrap.sh
if [ ! -f "setup.py" ]; then
    if [ -f "$(dirname "$0")/setup.py" ]; then
        cd "$(dirname "$0")"
    else
        echo "Downloading setup.py..."
        curl -LsSf https://raw.githubusercontent.com/artofthesmart/dotfiles-bootstrap/main/setup.py -o setup.py
    fi
fi

# Re-execute in zsh and run the Python component selector. Optional tools,
# including GitHub Copilot CLI, are installed by setup.py when selected.
echo "Launching modern hybrid setup..."
exec zsh -c "export PATH=\"\$HOME/.local/bin:\$PATH\"; uv run --with rich --with questionary setup.py"
