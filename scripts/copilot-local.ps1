# Run the official GitHub Copilot CLI against the local Ollama model created by
# setup-local-ai.ps1. Offline mode: no GitHub-hosted inference, no telemetry.
$ErrorActionPreference = 'Stop'

if (-not (Get-Command ollama -ErrorAction SilentlyContinue)) { throw 'Ollama is not installed. Run scripts\setup-local-ai.ps1 first.' }
if (-not (Get-Command copilot -ErrorAction SilentlyContinue)) { throw 'GitHub Copilot CLI is not installed. Install it with: winget install GitHub.Copilot' }

$model = $env:LOCAL_AI_MODEL
if (-not $model) {
    $model = ollama list 2>$null | Select-Object -Skip 1 | ForEach-Object { ($_ -split '\s+')[0] } | Where-Object { $_ -like 'local-ai-*' } | Select-Object -First 1
}
if (-not $model) { throw 'No local-ai-... model found. Run scripts\setup-local-ai.ps1 first.' }

$env:COPILOT_PROVIDER_BASE_URL = if ($env:OLLAMA_V1_URL) { $env:OLLAMA_V1_URL } else { 'http://127.0.0.1:11434/v1' }
$env:COPILOT_PROVIDER_TYPE = 'openai'
$env:COPILOT_PROVIDER_API_KEY = 'ollama'
$env:COPILOT_PROVIDER_WIRE_API = 'responses'
$env:COPILOT_MODEL = $model
$env:COPILOT_OFFLINE = 'true'
& copilot @args
exit $LASTEXITCODE
