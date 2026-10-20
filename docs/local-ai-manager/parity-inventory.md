# Current-script parity inventory

Contract version: **1.0.0**

This inventory maps behavior in `scripts/setup-local-ai.sh`, `scripts/setup-local-ai.ps1`, their tests, and current Local AI guides to the planned manager. Status means:

- **Stable**: required first-stable manager behavior.
- **Manual**: retained as an explicit human/UI handoff with diagnosis and remediation.
- **Out**: intentionally not carried forward as a manager capability.

## Host, application, and transport

| Current capability | Planned command | Domain owner | Status | Contract note |
|---|---|---|---:|---|
| Reject unsupported OS/architecture and non-desktop Linux | `setup`, `doctor` | `hardware`, `platform` | Stable | Emit host issue codes before mutation. |
| Refuse root/administrator and noninteractive wizard misuse | `setup` | `cli`, `platform` | Stable | TUI requires a terminal; unattended mode uses explicit flags/config. Elevation is step-scoped. |
| Discover VS Code/Ollama on PATH and standard app locations | `status`, `setup`, `doctor` | `platform` | Stable | Missing PATH is not absence. |
| Enforce minimum VS Code version and bundled Copilot Chat behavior | `setup`, `doctor`, `verify` | `vscode` | Stable | Never marketplace-install bundled `GitHub.copilot-chat`. |
| Download official application installers with progress/timeouts | `setup`, `repair` | `install` | Stable | Requires large-download consent. |
| Reject HTTPS redirect downgrade | All networked commands | `install`, network clients | Stable | Applies to every remote client. |
| Validate Windows installer Authenticode publisher | `setup`, `repair` | `platform/windows`, `install` | Stable | Integrity failure is never bypassed by `--yes`. |
| Use APT/sudo or macOS app-install UI | `setup`, `repair` | platform adapters | Manual | Manager prepares and verifies; privilege/app UI remains explicit. |
| Install private jq helper | None | None | Out | Go uses native JSON; no helper binary. |
| Preserve command, exit code, stdout/stderr diagnostics | All | `platform` | Stable | Raw output is rendered transiently; persisted data is redacted/allowlisted. |

## Ollama service and storage

| Current capability | Planned command | Domain owner | Status | Contract note |
|---|---|---|---:|---|
| Detect loopback Ollama with `/api/version` | `status`, `doctor`, `setup` | `ollama` | Stable | Endpoint defaults to loopback and config cannot embed credentials. |
| Start a missing Ollama app/service automatically | `setup`, `repair` | `ollama`, platform adapter | Stable | Starting a new process is documented and receipt-backed. |
| Set `OLLAMA_CONTEXT_LENGTH=65536` for a newly started server | `setup`, `repair` | `ollama` | Stable | Existing services are diagnosed before a consented restart/change. |
| Select model storage from `OLLAMA_MODELS` or service defaults | `status`, `setup`, `doctor` | `ollama`, `platform` | Stable | Arbitrary environment dumps are forbidden; only this allowlisted value is read. |
| Require download/import size plus 2 GiB spare | `models install`, `setup` | `ollama`, `hardware` | Stable | Catalog may define a larger conservative reserve. |
| Persist ad-hoc Ollama serve logs under setup state | `doctor`, structured logs | `state`, `ollama` | Stable | New logs are structured/redacted and bounded. |

## Hardware and recommendation

| Current capability | Planned command | Domain owner | Status | Contract note |
|---|---|---|---:|---|
| Allowlisted CPU/RAM/GPU/storage discovery | `status`, `models recommend`, `report` | `hardware` | Stable | No hostname, username, serial, network ID, arbitrary env, or repository data. |
| Distinguish Windows installed RAM from visible RAM | `status`, `models recommend` | `hardware/windows` | Stable | Never substitute or infer the discrepancy. |
| Treat Windows iGPU shared memory and Apple unified memory as one pool | `models recommend` | `hardware`, `catalog` | Stable | No double counting or fixed-reservation claims. |
| Detect NVIDIA evidence without promising offload | `status`, `verify` | `hardware` | Stable | Runtime `/api/ps` remains authoritative. |
| Warn that Intel Mac is CPU-only | `status`, `models recommend` | `hardware/macos` | Stable | Recommendation accounts for practical CPU fallback. |
| Generate, save, display, and copy a Gemini hardware prompt | None | None | Out | Replaced by deterministic embedded catalog scoring; no Gemini dependency. |
| Build a runtime-preflighted five-model shortlist | `models list`, `models recommend` | `catalog` | Stable | Replaced by reviewed versioned catalog plus live revalidation. |

## Model acquisition and verification

| Current capability | Planned command | Domain owner | Status | Contract note |
|---|---|---|---:|---|
| Validate simple local Ollama names and reject cloud metadata | `models install`, `doctor` | `ollama` | Stable | First stable installs by catalog ID; exact model names remain facts. |
| Infer model source from tag, URL, or local path | None | None | Out | Arbitrary Hugging Face discovery/import is outside first stable. |
| Guided single-file GGUF import with derived name | None | None | Out | Existing imports are detected as user-owned; registry GGUF parsing remains internal preflight. |
| Exact-tag registry manifest and layer-size preflight | `models install`, `verify` | `ollama` | Stable | Stops before pull if the exact source cannot be verified. |
| Bounded first-MiB GGUF context read | `models install`, catalog drift check | `ollama` | Stable | Bounded parsing must be fuzzed and reject malformed lengths. |
| Reject source context below 65,536 before pull | `models install`, `models recommend` | `catalog`, `ollama` | Stable | No misleading oversized alias. |
| Reuse an installed exact source without pulling | `models install`, `setup` | `ollama` | Stable | Existing source remains user-owned unless explicitly adopted. |
| Pull a catalog model after exact review/consent | `models install`, `setup` | `ollama` | Stable | Receipt records catalog ID, exact tag, size, and resulting asset. |
| Recheck local metadata, local-only routing, and `tools` capability | `models install`, `verify`, `doctor` | `ollama` | Stable | Catalog evidence never replaces local checks. |
| Create/recreate `local-ai-...` alias with `num_ctx 65536` | `models install`, `setup`, `repair` | `ollama` | Stable | Alias is manager-owned only when creation succeeds and is recorded. |
| Natural-language structured `get_temperature` tool smoke test | `verify`, `setup` | `ollama` | Stable | No generated tool executes; JSON-looking prose fails. |
| Omit request-level `num_ctx`, require `/api/ps` allocation ≥65,536 | `verify`, `doctor` | `ollama` | Stable | Validates the server behavior used by VS Code. |
| Show actual `ollama ps` offload | `status`, `verify` | `ollama` | Stable | Normalize evidence without persisting raw process output. |
| Retry reasoning model with a larger output budget | `verify`, `repair` | `ollama` | Stable | Bounded retry policy is deterministic. |
| Continue with tool use unverified | `setup` result | `app` | Manual | Outcome is `manual_required`, never success, with a stable runtime-verification issue. |

## VS Code integration and user handoff

| Current capability | Planned command | Domain owner | Status | Contract note |
|---|---|---|---:|---|
| Open `Local AI` first so the profile exists | `vscode configure`, `setup` | `vscode` | Stable | Use documented CLI surfaces only. |
| Wait for profile readiness and preserve native diagnostics | `vscode configure`, `doctor` | `vscode` | Stable | Bounded retry with typed timeout issue. |
| Install/verify only `Ollama.ollama` in the profile | `vscode configure`, `verify` | `vscode` | Stable | Existing profile settings remain untouched. |
| Open a blank profile without requiring a repository | `vscode open`, `setup` | `vscode` | Stable | Optional repository path is user supplied and never persisted in support data. |
| Explain workspace trust/Restricted Mode | `vscode configure`, `doctor` | `vscode` | Manual | Manager cannot grant trust. |
| Explain Agent mode versus sparkle-icon model picker | `vscode configure`, `doctor` | `vscode` | Manual | Exact manager alias is required. |
| Refresh/diagnose missing Ollama models | `doctor`, `vscode configure` | `vscode` | Manual | Command Palette action remains UI-only unless a supported CLI appears. |
| Configure BYOK/utility model settings | `vscode configure`, `doctor` | `vscode` | Manual | Do not edit undocumented profile storage. |
| Reduce enabled tools to ≤128 and preserve approvals | `doctor`, `vscode configure` | `vscode` | Manual | Explain Configure Tools and experimental virtual grouping. |
| Approve a small edit/test and inspect the diff | `verify` guidance | `vscode` | Manual | Tool approval cannot be automated. |
| Switch between local and cloud profiles/models | `vscode open`, status guidance | `vscode` | Manual | Profiles are not billing boundaries. |

## Lifecycle, safety, and testing gaps

| Current capability or gap | Planned command | Domain owner | Status | Contract note |
|---|---|---|---:|---|
| Rerun-safe checks and confirmation before downloads | All mutating commands | `app`, `state` | Stable | Generalized into durable transactions and receipts. |
| Persisted resume/restart and atomic journal | `setup --resume/--restart` | `state` | Stable | New capability defined by state schema v1. |
| Read-only status and layered diagnosis | `status`, `doctor` | `app` | Stable | New capability with stable issue codes. |
| Idempotent selected repairs | `repair` | `app` | Stable | New capability; no undocumented fixes. |
| Explicit model removal | `models remove` | `ollama` | Stable | Show dependent aliases and require destructive confirmation. |
| Self-update and one verified rollback | `update` | `update` | Stable | New capability; never silent/background. |
| Ownership-aware uninstall | `uninstall` | `install`, `state` | Stable | Follows [asset ownership rules](asset-ownership.md). |
| Redacted support report | `report` | `app` | Stable | Allowlisted facts only. |
| Preserve no-change boundaries for drivers, firewall, global telemetry settings, approvals, trust, and global policy | All | `app` policy | Stable | The documented per-process Copilot CLI telemetry opt-out does not modify a global setting; other violations are release blockers. |
| Mocked Bash/PowerShell coverage | Go unit/contract/integration suites | All owners | Stable | Existing tests define parity evidence, not the new implementation harness. |
| Native four-platform hardware and UI validation | Release acceptance | Maintainers | Manual | Required before stable release; never inferred from mocks. |

## GitHub Copilot CLI local-provider lifecycle

| Capability | Planned command | Domain owner | Status | Contract note |
|---|---|---|---:|---|
| Prefer an existing supported official `copilot` executable | `copilot configure`, `status`, `doctor` | `copilot`, `platform` | Stable | Probe the executable version and classify it as externally detected; never replace an unrelated executable. |
| Install official Copilot CLI user-locally | `copilot configure`, `repair` | `copilot`, `install` | Stable | Use official `github/copilot-cli` stable release assets, GitHub API SHA-256 digests, and `SHA256SUMS.txt`; never globally install npm packages. |
| Enforce supported local-provider minimum | `copilot configure`, `doctor`, `verify` | `copilot` | Stable | Minimum `1.0.100`, verified October 20, 2026, guarantees the required no-telemetry flag; current stable installation remains preferred. |
| Create cross-platform `copilot-local` launcher | `copilot configure`, `copilot open` | `copilot` | Stable | Atomically sets loopback `/v1`, `openai`, `responses`, exact alias, and `COPILOT_OFFLINE=true`, then forwards all user arguments unchanged. |
| Prevent silent GitHub-hosted inference fallback | `copilot open`, `doctor` | `copilot` | Stable | Missing Ollama, alias, provider, launcher, or supported CLI is a hard diagnosable failure. |
| Resume, repair, rollback, and uninstall | `copilot configure --resume`, `repair`, `uninstall` | `copilot`, `state` | Stable | Transaction receipts recover verified interrupted mutations; only manager-owned executable and launcher assets are removed. |
| Verify Copilot Chat capability in Local AI | `vscode configure`, `verify` | `vscode` | Stable | Supported VS Code builds bundle Copilot Chat; the manager never marketplace-installs it. No supported non-bundled path exists in the current minimum-version contract. |
