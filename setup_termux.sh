#!/bin/bash

# Exit immediately if a command exits with a non-zero status
set -e
# Treat unset variables as an error
set -u
# Enable the errexit option for pipelines
set -o pipefail

echo "Starting Termux setup script..."

# --- Task 1: Update and Upgrade Packages ---
echo ""
echo "--- Updating and upgrading Termux packages ---"
pkg update -y || { echo "ERROR: pkg update failed."; exit 1; }
pkg upgrade -y || { echo "ERROR: pkg upgrade failed."; exit 1; }
echo "--- Package update and upgrade complete ---"

# --- Task 2: Install Required Packages ---
echo ""
echo "--- Installing core packages ---"
PACKAGES="man neovim wget python zsh git gitui mc"
echo "Installing: $PACKAGES"
pkg install -y $PACKAGES || { echo "ERROR: pkg install failed."; exit 1; }
echo "--- Core packages installation complete ---"

# --- Task 3: Install Oh-My-Zsh ---
echo ""
echo "--- Installing Oh-My-Zsh ---"
# Use the unattended install to avoid prompts and not change the default shell immediately
if [ ! -d "$HOME/.oh-my-zsh" ]; then
  echo "Cloning Oh-My-Zsh repository..."
  # The "" argument is for the optional repo path, --unattended skips interactive prompts
  sh -c "$(curl -fsSL https://raw.githubusercontent.com/ohmyzsh/ohmyzsh/master/tools/install.sh)" "" --unattended || { echo "ERROR: Oh-My-Zsh installation failed."; exit 1; }
  echo "--- Oh-My-Zsh installation complete ---"
  echo "Oh-My-Zsh is installed, but your default shell is likely still bash."
  echo "To switch to zsh, run 'chsh -s zsh' and restart Termux."
else
  echo "Oh-My-Zsh directory already exists ($HOME/.oh-my-zsh). Skipping installation."
  echo "If you need to update Oh-My-Zsh, open a zsh shell and run 'omz update'."
fi

# --- Task 4: Install and Configure Powerlevel10k ---
echo ""
echo "--- Installing Powerlevel10k ---"
# Clone Powerlevel10k into the custom themes directory
if [ ! -d "${ZSH_CUSTOM:-$HOME/.oh-my-zsh/custom}/themes/powerlevel10k" ]; then
  echo "Cloning Powerlevel10k repository..."
  git clone --depth=1 https://github.com/romkatv/powerlevel10k.git "${ZSH_CUSTOM:-$HOME/.oh-my-zsh/custom}/themes/powerlevel10k" || { echo "ERROR: Failed to clone Powerlevel10k."; exit 1; }
  echo "--- Powerlevel10k cloning complete ---"

  echo "Setting ZSH_THEME to powerlevel10k/powerlevel10k in ~/.zshrc"
  # Use sed to find the line starting with ZSH_THEME= and replace it
  # Using # as a delimiter in sed to avoid issues with / in the path
  sed -i 's#^ZSH_THEME=".*"#ZSH_THEME="powerlevel10k/powerlevel10k"#' "$HOME/.zshrc" || { echo "ERROR: Failed to update ZSH_THEME in ~/.zshrc."; exit 1; }
  echo "--- ZSH_THEME updated ---"
else
  echo "Powerlevel10k directory already exists. Skipping cloning and .zshrc modification."
fi

# --- Task 5: Install Nerd Font (Roboto Mono) ---
echo ""
echo "--- Installing Nerd Font (Roboto Mono) ---"
FONT_DIR="$HOME/.termux"
FONT_FILE="font.ttf"
FONT_PATH="$FONT_DIR/$FONT_FILE"
FONT_URL="https://github.com/ryanoasis/nerd-fonts/raw/refs/heads/master/patched-fonts/RobotoMono/Medium/RobotoMonoNerdFontMono-Medium.ttf"
TERMUX_PROPERTIES="$HOME/.termux/termux.properties" # Define variable for clarity

mkdir -p "$FONT_DIR" || { echo "ERROR: Failed to create directory $FONT_DIR."; exit 1; }

if [ -f "$FONT_PATH" ]; then
  echo "Font file already exists at $FONT_PATH. Skipping download."
else
  echo "Downloading Nerd Font from $FONT_URL..."
  wget -O "$FONT_PATH" "$FONT_URL" || { echo "ERROR: Failed to download Nerd Font."; exit 1; }
  echo "Font downloaded successfully to $FONT_PATH."
fi

# --- Add extra-keys=[] to termux.properties ---
echo "Adding 'extra-keys=[]' to $TERMUX_PROPERTIES..."
echo "extra-keys=[]" >> "$TERMUX_PROPERTIES" || { echo "ERROR: Failed to write to $TERMUX_PROPERTIES."; exit 1; }
echo "--- Added extra-keys=[] ---"

echo "Informing Termux to reload settings..."
# This command tells Termux to look for the font file in ~/.termux/font.ttf
termux-reload-settings
echo "--- Nerd Font installation complete ---"
echo "You may need to restart Termux for the new font to take effect."

# --- Task 6: Install LazyVim ---
echo ""
echo "--- Installing LazyVim ---"
NVIM_CONFIG_DIR="$HOME/.config/nvim"

mkdir -p "$HOME/.config" || { echo "ERROR: Failed to create directory $HOME/.config."; exit 1; }

if [ -d "$NVIM_CONFIG_DIR" ]; then
  echo "WARNING: Existing Neovim configuration found at $NVIM_CONFIG_DIR."
  read -p "Do you want to remove the existing config and install LazyVim? (y/N): " confirm_lazyvim
  if [[ "$confirm_lazyvim" != "y" && "$confirm_lazyvim" != "Y" ]]; then
    echo "Skipping LazyVim installation as requested."
  else
    echo "Removing existing Neovim configuration..."
    rm -rf "$NVIM_CONFIG_DIR" || { echo "ERROR: Failed to remove existing Neovim config."; exit 1; }
    echo "Existing config removed. Cloning LazyVim starter..."
    git clone https://github.com/LazyVim/starter "$NVIM_CONFIG_DIR" || { echo "ERROR: Failed to clone LazyVim starter."; exit 1; }
    echo "Removing .git directory from LazyVim starter..."
    rm -rf "$NVIM_CONFIG_DIR/.git" || { echo "ERROR: Failed to remove .git from LazyVim starter."; exit 1; }
    echo "--- LazyVim installation process started ---"
    echo "Run 'nvim' to open Neovim and complete the LazyVim setup (it will download plugins)."
  fi
else
  echo "No existing Neovim configuration found. Cloning LazyVim starter..."
  git clone https://github.com/LazyVim/starter "$NVIM_CONFIG_DIR" || { echo "ERROR: Failed to clone LazyVim starter."; exit 1; }
  echo "Removing .git directory from LazyVim starter..."
  rm -rf "$NVIM_CONFIG_DIR/.git" || { echo "ERROR: Failed to remove .git from LazyVim starter."; exit 1; }
  echo "--- LazyVim installation process started ---"
  echo "Run 'nvim' to open Neovim and complete the LazyVim setup (it will download plugins)."
fi


echo ""
echo "--------------------------------------------------"
echo "Termux setup script finished."
echo "Recommendations:"
echo "1. Restart Termux to apply font and potentially shell changes."
echo "2. If you want zsh as your default shell, run 'chsh -s zsh'."
echo "3. Run 'nvim' to start Neovim and let LazyVim install its plugins."
echo "4. Consider running 'p10k configure' in zsh after switching shell and restarting to set up Powerlevel10k."
echo "--------------------------------------------------"

exit 0
