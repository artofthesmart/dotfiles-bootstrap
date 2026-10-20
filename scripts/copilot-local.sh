#!/usr/bin/env bash
# Run the official GitHub Copilot CLI against the local Ollama model created by
# setup-local-ai.sh. Offline mode: no GitHub-hosted inference, no telemetry.
set -euo pipefail

command -v ollama >/dev/null || { echo "Ollama is not installed. Run scripts/setup-local-ai.sh first." >&2; exit 1; }
command -v copilot >/dev/null || {
    echo "GitHub Copilot CLI is not installed. Install it with:" >&2
    echo "  curl -fsSL https://gh.io/copilot-install | bash" >&2
    exit 1
}

model="${LOCAL_AI_MODEL:-$(ollama list 2>/dev/null | awk 'NR>1 && $1 ~ /^local-ai-/ {print $1; exit}')}"
[ -n "$model" ] || { echo "No local-ai-... model found. Run scripts/setup-local-ai.sh first." >&2; exit 1; }

export COPILOT_PROVIDER_BASE_URL="${OLLAMA_V1_URL:-http://127.0.0.1:11434/v1}"
export COPILOT_PROVIDER_TYPE=openai
export COPILOT_PROVIDER_API_KEY=ollama
export COPILOT_PROVIDER_WIRE_API=responses
export COPILOT_MODEL="$model"
export COPILOT_OFFLINE=true
exec copilot "$@"
