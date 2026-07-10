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

# Require sudo privileges upfront
if [ "$EUID" -ne 0 ]; then
    if ! command -v sudo >/dev/null 2>&1; then
        echo "ERROR: You are not root and sudo is not installed. Please run as root."
        exit 1
    fi
    if [ -z "$BOOTSTRAP_AUTO" ]; then
        # Prompt for password now to cache credentials
        sudo -v
    fi
fi

# Install dependencies needed for python/uv
echo "Installing base dependencies..."
sudo apt-get update -y
sudo apt-get install -y curl python3 python3-pip python3-venv git zsh

# Install uv if not present
if ! command -v uv >/dev/null 2>&1; then
    echo "Installing uv..."
    curl -LsSf https://astral.sh/uv/install.sh | sh
    export PATH="$HOME/.local/bin:$PATH"
fi

# Make sure zsh is the default shell (honors $USER override, e.g. when run
# as root on behalf of another account via BOOTSTRAP_AUTO)
USER_NAME="${USER:-$(whoami)}"
ZSH_PATH="$(which zsh)"
if [ "$SHELL" != "$ZSH_PATH" ]; then
    echo "Setting zsh as default shell..."
    sudo chsh -s "$ZSH_PATH" "$USER_NAME"
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

# Re-execute in zsh and run the Python script
echo "Launching modern hybrid setup..."
exec zsh -c "export PATH=\"\$HOME/.local/bin:\$PATH\"; uv run --with rich --with questionary setup.py"
