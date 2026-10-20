#!/bin/bash
# Native macOS (including Bash 3.2) and Ubuntu/Debian setup.
set -euo pipefail

fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
ask() {
    printf '\n%s (q to quit): ' "$1"
    IFS= read -r ANSWER || fail "Input closed; setup stopped."
    [ "$ANSWER" != q ] || exit 0
}
confirm() {
    ask "$1 [y/N]"
    case "$ANSWER" in y|Y|yes|YES) return 0 ;; *) return 1 ;; esac
}
download() {
    curl --fail --location --show-error --proto '=https' --tlsv1.2 \
        --connect-timeout 20 --max-time 1800 "$1" -o "$2"
}
cleanup() { [ -z "${WORK:-}" ] || rm -rf -- "$WORK"; }
detect_platform() {
    OS=$(uname -s)
    ARCH=$(uname -m)
    case "$OS" in
        Darwin)
            if [ "$(sysctl -in hw.optional.arm64 2>/dev/null || printf 0)" = 1 ]; then ARCH=arm64; fi
            OS_VERSION=$(sw_vers -productVersion)
            [ "${OS_VERSION%%.*}" -ge 14 ] || fail "Ollama requires macOS 14 or newer."
            ;;
        Linux)
            [ -r /etc/os-release ] || fail "Cannot identify this Linux distribution."
            # Read only the two allowlisted fields, not shell code from the file.
            DISTRO=$(sed -n 's/^ID=//p' /etc/os-release | tr -d '"')
            OS_VERSION=$(sed -n 's/^VERSION_ID=//p' /etc/os-release | tr -d '"')
            case "$DISTRO" in ubuntu|debian) ;; *) fail "Only Ubuntu and Debian are supported by this wizard." ;; esac
            command -v apt-get >/dev/null || fail "APT is required."
            [ -n "${DISPLAY:-}${WAYLAND_DISPLAY:-}" ] || fail "Run this wizard in a desktop session, not a headless server."
            ;;
        *) fail "Use the Windows PowerShell wizard on Windows; this OS is unsupported." ;;
    esac
    case "$ARCH" in
        x86_64|amd64) ARCH=amd64 ;;
        arm64|aarch64) ARCH=arm64 ;;
        *) fail "Unsupported architecture: $ARCH. Use a supported 64-bit desktop." ;;
    esac
}
find_code() {
    CODE=$(command -v code || true)
    if [ -z "$CODE" ] && [ "$OS" = Darwin ]; then
        for candidate in "/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code" \
            "$HOME/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code"; do
            if [ -x "$candidate" ]; then CODE=$candidate; break; fi
        done
    fi
}
find_ollama() {
    OLLAMA=$(command -v ollama || true)
    if [ -z "$OLLAMA" ] && [ "$OS" = Darwin ]; then
        for candidate in "/Applications/Ollama.app/Contents/Resources/ollama" \
            "$HOME/Applications/Ollama.app/Contents/Resources/ollama"; do
            if [ -x "$candidate" ]; then OLLAMA=$candidate; break; fi
        done
    fi
}
ensure_code() {
    find_code
    if [ -z "$CODE" ]; then
        if [ "$OS" = Darwin ]; then
            confirm "Download and open the official VS Code ZIP? Move the extracted app into Applications" || fail "VS Code is required."
            download "https://update.code.visualstudio.com/latest/$([ "$ARCH" = arm64 ] && printf darwin-arm64 || printf darwin)/stable" "$WORK/vscode.zip"
            open "$WORK/vscode.zip"
            ask "After copying Visual Studio Code.app to Applications, press Enter"
        else
            confirm "Download Microsoft's VS Code .deb and install with sudo/APT? This may offer Microsoft's update repository" || fail "VS Code is required."
            download "https://update.code.visualstudio.com/latest/linux-deb-$([ "$ARCH" = arm64 ] && printf arm64 || printf x64)/stable" "$WORK/code.deb"
            # APT's sandbox user must be able to read this specific package.
            chmod 755 "$WORK"
            chmod 644 "$WORK/code.deb"
            sudo apt-get install "$WORK/code.deb"
            chmod 700 "$WORK"
        fi
        find_code
        [ -n "$CODE" ] || fail "VS Code was not found. Finish installing it and rerun."
    fi
    local version major minor
    version=$("$CODE" --version | head -n 1)
    major=${version%%.*}
    minor=${version#*.}; minor=${minor%%.*}
    [[ "$major" =~ ^[0-9]+$ && "$minor" =~ ^[0-9]+$ ]] || fail "Cannot read VS Code version: $version"
    if [ "$major" -lt 1 ] || { [ "$major" -eq 1 ] && [ "$minor" -lt 127 ]; }; then
        fail "VS Code $version is too old for the official Ollama extension. Update to 1.127+ and rerun."
    fi
}
ensure_ollama() {
    find_ollama
    if [ -z "$OLLAMA" ]; then
        if [ "$OS" = Darwin ]; then
            confirm "Download the official Ollama disk image and open it? Drag Ollama into Applications" || fail "Ollama is required."
            download https://ollama.com/download/Ollama.dmg "$WORK/Ollama.dmg"
            open "$WORK/Ollama.dmg"
            ask "After copying Ollama.app to Applications, press Enter"
        else
            confirm "Download Ollama's official install script to a temporary file? It installs system software/service using sudo" || fail "Ollama is required."
            download https://ollama.com/install.sh "$WORK/ollama-install.sh"
            printf 'Installer saved at: %s\n' "$WORK/ollama-install.sh"
            confirm "Run that official installer now (you can inspect the file in another terminal first)" || fail "Installation cancelled."
            sh "$WORK/ollama-install.sh"
        fi
        find_ollama
        [ -n "$OLLAMA" ] || fail "Ollama was not found. Finish installation and rerun."
    fi
    "$OLLAMA" --version
}
ensure_jq() {
    JQ=$(command -v jq || true)
    [ -n "$JQ" ] || { [ ! -x "$STATE/bin/jq" ] || JQ="$STATE/bin/jq"; }
    if [ -z "$JQ" ]; then
        local asset hash actual
        case "$OS-$ARCH" in
            Linux-amd64) asset=jq-linux-amd64; hash=b1c22172dd303f3be49e935aa56aa48a8b7a46e0bc838b4997d3bb451495870f ;;
            Linux-arm64) asset=jq-linux-arm64; hash=8b85c817833814ddca00a144c33705546355afccf0cf39b188f3cdb48b852309 ;;
            Darwin-amd64) asset=jq-macos-amd64; hash=e94b266e3c26690550006abe63152b782280f4e14374accdf04cbde844f00bc0 ;;
            Darwin-arm64) asset=jq-macos-arm64; hash=2d75340ba57a4b4b4c8708a21c2dc8e958a48aaa8bba13b27f77f6e4c0eca07e ;;
        esac
        confirm "Download jq 1.8.2, a small JSON reader, only into $STATE/bin (no global install)" || fail "A JSON reader is required."
        download "https://github.com/jqlang/jq/releases/download/jq-1.8.2/$asset" "$WORK/jq"
        if [ "$OS" = Darwin ]; then actual=$(shasum -a 256 "$WORK/jq"); else actual=$(sha256sum "$WORK/jq"); fi
        [ "${actual%% *}" = "$hash" ] || fail "jq checksum mismatch; refusing to execute it."
        mkdir -p "$STATE/bin"
        chmod 700 "$WORK/jq"
        mv "$WORK/jq" "$STATE/bin/jq"
        JQ="$STATE/bin/jq"
    fi
    "$JQ" --version
}
api() {
    local endpoint=$1
    shift
    curl --fail --silent --show-error --noproxy '*' --connect-timeout 3 \
        --max-time 180 "http://127.0.0.1:11434/api/$endpoint" "$@"
}
ensure_server() {
    if ! api version > "$WORK/version.json" 2>/dev/null; then
        if [ "$OS" = Darwin ]; then
            printf 'Open Ollama from Applications and approve its first-run prompts.\n'
            open -a Ollama
        else
            printf 'Start Ollama in another terminal with: ollama serve\nIf installed as a service, use: sudo systemctl start ollama\n'
        fi
        ask "When Ollama is running, press Enter"
        api version > "$WORK/version.json" || fail "Ollama did not respond at 127.0.0.1:11434. Start it and rerun."
    fi
    "$JQ" -e '.version | strings | length > 0' "$WORK/version.json" >/dev/null || fail "Invalid Ollama server response."
}
free_kib() { df -Pk "$MODEL_VOLUME" | awk 'END {print $4}'; }
prompt_path() {
    local documents="$HOME/Documents"
    if [ "$OS" = Linux ] && command -v xdg-user-dir >/dev/null; then
        documents=$(xdg-user-dir DOCUMENTS) || fail "Cannot locate your Documents folder."
        [[ "$documents" = /* ]] || fail "Documents location must be an absolute path."
        if [ "$documents" = "$HOME" ] || [ "$documents" = "$HOME/" ]; then documents="$HOME/Documents"; fi
    fi
    mkdir -p "$documents"
    printf '%s/Local-AI-Gemini-Prompt.txt' "$documents"
}
hardware_prompt() {
    local cpu ram gpu acceleration free_gib vram prompt_file
    prompt_file=$(prompt_path)
    vram=unknown
    if [ "$OS" = Darwin ]; then
        cpu=$(sysctl -n machdep.cpu.brand_string)
        ram=$(sysctl -n hw.memsize | awk '{printf "%.1f GiB", $1/1073741824}')
        if [ "$ARCH" = arm64 ]; then
            gpu="Apple integrated GPU; unified memory shared with RAM (not extra VRAM)"
            acceleration="Apple Silicon GPU supported; confirm actual offload with ollama ps"
        else
            gpu="Intel Mac: GPU acceleration is not supported by Ollama"
            acceleration="CPU only; agent work may be very slow"
            printf '\nWARNING: Intel Macs use CPU-only Ollama inference. Start small; expect slow agents.\n'
        fi
    else
        cpu=$(awk -F: '/^model name|^Model/ {sub(/^[ \t]+/,"",$2); print $2; exit}' /proc/cpuinfo)
        [ -n "$cpu" ] || cpu="$ARCH CPU (model unknown)"
        ram=$(awk '/MemTotal:/ {printf "%.1f GiB", $2/1048576}' /proc/meminfo)
        gpu=unknown
        acceleration="Unknown; no GPU acceleration established"
        if command -v nvidia-smi >/dev/null; then
            if gpu=$(nvidia-smi --query-gpu=name,memory.total,driver_version --format=csv,noheader 2>/dev/null); then
                acceleration="NVIDIA driver responds; report lists GPU, dedicated MiB, driver"
                vram="See NVIDIA report; GPUs have separate memory pools"
            else
                gpu=unknown
                printf 'WARNING: nvidia-smi failed; GPU memory/driver status is unknown.\n'
            fi
        elif command -v lspci >/dev/null; then
            gpu=$(lspci | awk '/VGA compatible controller|3D controller|Display controller/ {sub(/^[^ ]+ /,""); print}')
            [ -n "$gpu" ] || gpu=unknown
        fi
    fi
    printf '\nGPU information: %s\n' "$gpu"
    ask "Optional GPU memory in GiB from your hardware specification (Enter if unknown; unified memory is not extra VRAM)"
    if [ -n "$ANSWER" ]; then
        [[ "$ANSWER" =~ ^[0-9]+([.][0-9]+)?$ ]] || fail "GPU memory must be a number in GiB."
        vram="$ANSWER GiB (user supplied; not automatically verified)"
    fi
    free_gib=$(free_kib | awk '{printf "%.1f", $1/1048576}')
    {
        printf 'Recommend a practical LOCAL Ollama coding model for VS Code Agent mode.\n'
        printf 'OS: %s %s; architecture: %s\nCPU: %s\nRAM: %s\nGPU: %s\nGPU memory: %s\nAcceleration: %s\nFree model-volume space: %s GiB\n' \
            "$OS" "$OS_VERSION" "$ARCH" "$cpu" "$ram" "$gpu" "$vram" "$acceleration" "$free_gib"
        cat <<'PROMPT'

REQUIRED RESPONSE FORMAT: Start your response with this copy-back block, before
any greeting, explanation, heading, or analysis. Replace placeholders with values:
MODEL: <exact local Ollama name:tag; no command, URL, backticks, or cloud model>
DOWNLOAD_GIB: <numeric additional storage estimate in GiB; no units>
CONTEXT_TOKENS: <integer tokens, at least 65536; no commas or 64k shorthand>
These are the values the user must copy into the setup wizard. Put MODEL first
then DOWNLOAD_GIB, then CONTEXT_TOKENS, matching its question order exactly.
Put ALL analysis, links, caveats and fallback choices
AFTER this block. Do not invent a runnable recommendation if none is suitable:
use MODEL: NONE and explain the blocker afterward.

Use current official sources and provide links, not an assumed universal optimum.
Recommend a primary and smaller fallback with reliable TOOL CALLING, exact LOCAL
Ollama Library tag, model-page/download link, quantization, download size in GiB,
license/commercial-use caveats, realistic speed expectations, and memory headroom
for OS, VS Code, model weights AND KV cache. Account for shared versus dedicated
memory; do not add GPUs' VRAM together. Recommend a practical context size for
agents and explain the quality/speed tradeoff. Do not suggest cloud inference.
Require evidence of structured Ollama API tool_calls for the exact recommended
variant/template, not just coding benchmarks, a tools label, or JSON in chat text.
If exact-variant tool behavior is unverified, state that prominently after the
copy-back block; do not promise VS Code Agent compatibility.
MANDATORY FOR THIS SETUP: We need at least 65536 tokens of context for the
VS Code Ollama extension. Recommend only models whose supported context window
is at least 65536 AND whose weights plus KV cache at that context fit this
hardware with OS/VS Code memory headroom. A small download alone is not enough.
Do not recommend a 32768-context model and simply set num_ctx to 65536; setting
a larger number does not establish model support. Verify any extended-context
requirements against the exact model variant. Do not lower context to make it fit.
If no suitable model fits, use MODEL: NONE, DOWNLOAD_GIB: N/A,
CONTEXT_TOKENS: N/A in the first block and explain the hardware/support blocker.
Prefer https://ollama.com/library models. If none fit, give an exact supported
GGUF file and source page on https://huggingface.co, including architecture and
chat-template/tool support caveats. Do not suggest executing repository code.
State uncertainties and ask about missing hardware details instead of guessing.
PROMPT
    } > "$prompt_file"
    printf '\nReview this hardware-only prompt before sharing it with Gemini:\n\n'
    cat "$prompt_file"
    printf '\nSaved to %s\n' "$prompt_file"
    if confirm "Copy the reviewed prompt to the clipboard"; then
        if command -v pbcopy >/dev/null; then pbcopy < "$prompt_file"
        elif command -v wl-copy >/dev/null; then wl-copy < "$prompt_file"
        elif command -v xclip >/dev/null; then xclip -selection clipboard < "$prompt_file"
        else printf 'No clipboard utility available. Copy the displayed text instead.\n'; fi
    fi
    ask "Paste it into Gemini yourself; return here with a model tag, size, and context recommendation, then press Enter"
}
valid_model() {
    [[ "$1" =~ ^[a-zA-Z0-9][a-zA-Z0-9._-]*(:[a-zA-Z0-9][a-zA-Z0-9._-]*)?$ ]] &&
        [[ ! "$1" =~ [cC][lL][oO][uU][dD] ]]
}
choose_model() {
    local source size needed available gguf import_path context
    printf '\nChoose a small, tool-capable model from https://ollama.com/library to run on this computer.\n'
    printf 'Use the Gemini recommendation to check that it fits your memory and supports Agent tools.\n'
    if [ "$OS" = Darwin ]; then
        printf 'Download it in Ollama Desktop:\n'
        printf '  1. Open Ollama from Applications.\n'
        printf '  2. Click the model selector and type the exact name Gemini recommended, including any size tag like :32b.\n'
        printf '     Keep the exact tag Gemini gave you; a size tag alone does not establish tool or 65536-context support.\n'
        printf '  3. Select the matching LOCAL model and click its download icon if shown. Do not choose a cloud model.\n'
        printf '  4. Type "Say hello" in the message box and press Enter. If needed, Ollama downloads the model before replying.\n'
        printf '  5. Wait for the download and reply. Return here and enter that same model name.\n'
    fi
    printf 'To download through this wizard instead, enter the exact model name and size tag below.\n'
    printf 'If already downloaded in Desktop, the wizard checks/reuses those files; it does not download an extra copy.\n'
    printf 'Choose local model weights, not a cloud model. The download may be several GB.\n'
    ask "Model source: 1 = Ollama Library (recommended), 2 = import a downloaded Hugging Face GGUF"
    source=$ANSWER
    case "$source" in
        1)
            ask 'MODEL: paste the exact model name and tag from Gemini (without "ollama run")'
            MODEL=$ANSWER
            [ "$MODEL" != NONE ] || fail "Gemini found no suitable model at 65536+ context. Review its explanation; do not download an unsuitable model."
            valid_model "$MODEL" || fail "Use a simple Library model[:tag], not a command, URL, namespace, or cloud model."
            ;;
        2)
            printf 'Download a compatible single-file GGUF from its Hugging Face page yourself.\nCheck license, architecture, quantization and tool/chat-template support. Arbitrary models may not work.\n'
            ask "Full path to your downloaded single-file .gguf"
            gguf=$ANSWER
            [ -f "$gguf" ] && [[ "$gguf" = /*.gguf ]] || fail "Choose an existing absolute .gguf path."
            [[ "$gguf" != *$'\n'* && "$gguf" != *$'\r'* ]] || fail "Newlines in paths are unsupported."
            [ "$(head -c 4 "$gguf")" = GGUF ] || fail "The file is not a GGUF."
            import_path=$(printf '%s' "$gguf" | sed 's/\\/\\\\/g; s/"/\\"/g')
            ask "Local name for this import (use a new name to preserve previous models)"
            MODEL=$ANSWER
            valid_model "$MODEL" || fail "Invalid local model name or cloud tag."
            ;;
        *) fail "Choose 1 or 2." ;;
    esac
    ask "DOWNLOAD_GIB: paste the storage estimate from Gemini (number only; Enter if unknown; include an extra copy for GGUF import)"
    size=$ANSWER
    if [ -n "$size" ]; then
        [[ "$size" =~ ^[0-9]+([.][0-9]+)?$ ]] || fail "Model size must be numeric GiB."
        needed=$(awk -v s="$size" 'BEGIN {printf "%.0f", (s+2)*1048576}')
    else
        printf 'WARNING: Download size unknown. Check free disk space against the model page before proceeding.\n'
        needed=2097152
    fi
    available=$(free_kib)
    [ "$available" -ge "$needed" ] || fail "Not enough model-volume space (allowing 2 GiB spare). Free space or choose a smaller model."
    ask "CONTEXT_TOKENS: paste the context from Gemini (minimum 65536; Enter for 65536)"
    context=${ANSWER:-65536}
    [[ "$context" =~ ^[0-9]+$ ]] && [ "${#context}" -le 7 ] && [ "$context" -ge 65536 ] && [ "$context" -le 1048576 ] ||
        fail "Choose an integer context size from 65536 to 1048576. Ask Gemini for a compatible model rather than reducing context."
    CONTEXT=$((10#$context))
    confirm "Acquire $MODEL with context $CONTEXT? Downloads may be large. Existing models with this name may be updated/replaced" || fail "Model acquisition cancelled."
    if [ "$source" = 1 ]; then
        "$OLLAMA" pull "$MODEL" || fail "Download failed. Check the tag, disk space, and network, then rerun."
    else
        printf 'FROM "%s"\n' "$import_path" > "$WORK/Modelfile"
        "$OLLAMA" create "$MODEL" -f "$WORK/Modelfile" || fail "Import failed. Verify architecture, file completeness and Ollama compatibility."
    fi
    # Give VS Code a distinct locally configured model with the selected budget.
    "$JQ" -n --arg model "$MODEL" '{model:$model}' > "$WORK/show-request.json"
    api show -H 'Content-Type: application/json' --data-binary "@$WORK/show-request.json" > "$WORK/show.json"
    "$JQ" -e '(.remote_host // "") == "" and (.remote_model // "") == ""' "$WORK/show.json" >/dev/null ||
        fail "This model routes to cloud inference; choose a local weights model."
    local limit
    limit=$("$JQ" '[.details.context_length,
        (.model_info as $info | $info[($info["general.architecture"] // "") + ".context_length"])]
        | map(select(type == "number" and . > 0)) | min // 0' "$WORK/show.json")
    if [ "$limit" -gt 0 ] && [ "$CONTEXT" -gt "$limit" ]; then
        fail "This model advertises $limit context tokens, below the requested $CONTEXT. Ask Gemini for 65536+ support; downloaded files are preserved."
    fi
    [ "$limit" -gt 0 ] || printf 'WARNING: Supported context limit unknown. Verify 65536+ support on the exact model page.\n'
    "$JQ" -e '(.capabilities // []) | index("tools") != null' "$WORK/show.json" >/dev/null ||
        fail "Model does not advertise tools. Choose a tool-capable model; do not force-enable unsupported tools."
    LOCAL_MODEL="local-ai-${MODEL//:/-}"
    printf 'FROM %s\nPARAMETER num_ctx %s\n' "$MODEL" "$CONTEXT" > "$WORK/Modelfile"
    "$OLLAMA" create "$LOCAL_MODEL" -f "$WORK/Modelfile"
}
check_model() {
    "$JQ" -n --arg model "$LOCAL_MODEL" --argjson context "$CONTEXT" '{
        model:$model, stream:false, options:{num_ctx:$context,num_predict:256,temperature:0},
        messages:[{role:"user",content:"What is the temperature in New York?"}],
        tools:[{type:"function",function:{name:"get_temperature",
          description:"Get the current temperature for a city",
          parameters:{type:"object",properties:{city:{type:"string",description:"The name of the city"}},required:["city"]}}}]
    }' > "$WORK/chat-request.json"
    printf '\nChecking local inference and a harmless tool request (180-second limit; no tool will execute)...\n'
    api chat -H 'Content-Type: application/json' --data-binary "@$WORK/chat-request.json" > "$WORK/chat.json" ||
        fail "Inference failed/timed out. Check Ollama logs; try a smaller model that supports 65536+ context or retry after loading."
    "$JQ" -e '.done == true and any(.message.tool_calls[]?;
        .function.name == "get_temperature" and (.function.arguments.city | ascii_downcase) == "new york")' "$WORK/chat.json" >/dev/null ||
        fail "Model did not produce the expected tool call. Try a reliable tool-capable model or compatible GGUF template."
    printf 'Local model tool-call check passed. Actual runtime/offload:\n'
    "$OLLAMA" ps
    printf 'CPU-only offload can be slow even if a GPU is installed. No GPU drivers were changed.\n'
}
connect_code() {
    printf '\nInstalling official chat and Ollama extensions only for the Local AI profile.\n'
    confirm "Create/reuse Local AI and install GitHub.copilot-chat and Ollama.ollama" || fail "Profile setup cancelled."
    "$CODE" --profile "Local AI" --install-extension GitHub.copilot-chat
    "$CODE" --profile "Local AI" --install-extension Ollama.ollama
    "$CODE" --profile "Local AI" --list-extensions > "$WORK/extensions.txt"
    grep -qi '^GitHub.copilot-chat$' "$WORK/extensions.txt" &&
        grep -qi '^Ollama.ollama$' "$WORK/extensions.txt" || fail "Required profile extensions were not installed."
    local repo
    ask "Full path to an existing locally checked-out repository"
    repo=$ANSWER
    [[ "$repo" = /* ]] && [ -d "$repo" ] || fail "Choose an existing absolute directory path."
    command -v git >/dev/null || fail "Git is needed to verify the checkout. Install Git and rerun."
    git -C "$repo" rev-parse --show-toplevel >/dev/null 2>&1 || fail "That directory is not in a Git checkout."
    "$CODE" --new-window --profile "Local AI" "$repo"
    cat <<INSTRUCTIONS

Finish in the Local AI window (these UI steps are not automated):
1. Trust the workspace only if you trust this repository.
2. Open Chat, select Agent mode, then select $LOCAL_MODEL under Ollama.
   Do NOT select Auto or a cloud model. If missing, run "Ollama: Refresh Models"
   or "Ollama: Diagnose Models" from the Command Palette.
3. In this profile's Settings, enable chat.agentHost.byokModels.enabled if
   required for the Copilot harness. Set chat.utilityModel and
   chat.utilitySmallModel to your local model using their model pickers.
   Verify Ollama's endpoint is http://127.0.0.1:11434. Remote provider
   configurations take precedence; remove obsolete built-in Ollama configs.
4. Ask: "Suggest one small documentation improvement, apply it after approval,
   then run an existing relevant test if available." Review each tool approval
   and the resulting diff. Keep changes only if you want them.
5. For metered cloud Copilot: Manage (gear) > Profiles > your previous profile
   (usually Default), then select a cloud model in Chat. Your normal account,
   plan and organization policy apply. Return via Profiles > Local AI and
   explicitly select $LOCAL_MODEL again. A profile is not a billing boundary.

The model check passed; VS Code Agent behavior still needs your confirmation.
Local inference has no per-request cloud-model fee, but uses your hardware.
Other Copilot features, including gray-text completions, are separate.
No offline, telemetry, firewall, auto-approval, or update settings were changed.
INSTRUCTIONS
}
main() {
    [ -t 0 ] && [ -t 1 ] || fail "Download/inspect the script, then run it in an interactive terminal; do not pipe it to bash."
    [ "$(id -u)" -ne 0 ] || fail "Run as your normal desktop user, not root; individual installs can request sudo."
    printf 'LOCAL AI SETUP\n1. Check/install apps\n2. Hardware prompt for Gemini\n3. Get and test model\n4. Connect VS Code\nEnter q at any prompt to stop; rerunning is safe with confirmations.\n'
    detect_platform
    command -v curl >/dev/null || fail "curl is missing. Install it with your OS package manager and rerun."
    STATE="$HOME/.local/share/local-ai-setup"
    mkdir -p "$STATE"
    chmod 700 "$STATE"
    WORK=$(mktemp -d "${TMPDIR:-/tmp}/local-ai-setup.XXXXXXXX")
    trap cleanup EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    export OLLAMA_HOST=127.0.0.1:11434
    ensure_code
    ensure_ollama
    ensure_jq
    ensure_server
    MODEL_VOLUME=${OLLAMA_MODELS:-"$HOME/.ollama/models"}
    if [ "$OS" = Linux ] && [ -z "${OLLAMA_MODELS:-}" ] && [ -d /usr/share/ollama ]; then
        MODEL_VOLUME=/usr/share/ollama
    fi
    printf '\nModel storage default: %s\n' "$MODEL_VOLUME"
    ask "If your running Ollama server uses different model storage, enter its absolute directory (Enter to keep default)"
    if [ -n "$ANSWER" ]; then
        [[ "$ANSWER" = /* ]] && [ -d "$ANSWER" ] || fail "Choose an existing absolute model-storage directory."
        MODEL_VOLUME=$ANSWER
    fi
    while [ ! -d "$MODEL_VOLUME" ]; do MODEL_VOLUME=$(dirname "$MODEL_VOLUME"); done
    hardware_prompt
    choose_model
    check_model
    connect_code
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then main "$@"; fi
