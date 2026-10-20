# Supported-host capability matrix

Contract version: **1.0.0**

Support labels:

- **Supported**: in the first-stable contract and release-blocking once the required hosted and physical acceptance row passes.
- **Manual**: the manager diagnoses and gives exact instructions, but a person must perform or approve the action through a supported UI or OS surface.
- **Out**: explicitly outside the first-stable product.

No row may be advertised as stable-release verified until its physical acceptance evidence is attached to that release. Hosted CI and mocks do not substitute for hardware, installer, GPU, or VS Code UI evidence.

## Host matrix

| Host | Architecture | Manager binary | VS Code/Ollama lifecycle | Model/runtime verification | Acceleration contract | Release evidence |
|---|---|---:|---:|---:|---|---|
| Windows 10 22H2+ / Windows 11 desktop | x86-64 | Supported | Supported; elevation and installer UI may be Manual | Supported | Detect NVIDIA/AMD/CPU without inventing shared VRAM; actual `/api/ps` evidence required | Native Windows PowerShell/installer plus physical GPU or CPU acceptance |
| macOS 14+ Apple Silicon | arm64 | Supported | Supported; dragging an app or OS security confirmation may be Manual | Supported | Unified memory is one pool; actual Metal/offload evidence required | Native Apple Silicon acceptance |
| macOS 14+ Intel | x86-64 | Supported | Supported; dragging an app or OS security confirmation may be Manual | Supported | CPU-only warning; no GPU claim | Native Intel Mac usability acceptance |
| Ubuntu desktop, maintained release with APT | x86-64 | Supported | Supported; `sudo` consent is Manual | Supported | NVIDIA/AMD/CPU may be detected; driver changes are Out | Physical accelerated case and CPU-fallback case |
| Ubuntu desktop, maintained release with APT | arm64 | Supported | Supported when official VS Code/Ollama assets exist for the release | Supported | Capability-driven; no assumed accelerator | Native ARM64 acceptance before advertising |
| Debian desktop, maintained release with APT | x86-64 | Supported | Supported; `sudo` consent is Manual | Supported | NVIDIA/AMD/CPU may be detected; driver changes are Out | Physical accelerated or CPU acceptance |
| Debian desktop, maintained release with APT | arm64 | Supported | Supported when official VS Code/Ollama assets exist for the release | Supported | Capability-driven; no assumed accelerator | Native ARM64 acceptance before advertising |
| Windows on ARM | arm64 | Out | Out | Out | Not validated | Not applicable |
| WSL, SSH/VS Code Remote, containers, headless servers | Any | Out | Out | Out | Remote topology is not modeled | Not applicable |
| Linux distributions without the supported APT contract | Any | Out | Out | Out | No generic package-manager fallback | Not applicable |
| Android/Termux | Any | Out | Out | Out | Separate dotfiles installer only | Not applicable |

## Capability matrix

| Capability | Windows | macOS | Ubuntu/Debian |
|---|---:|---:|---:|
| Discover applications on PATH and standard locations | Supported | Supported | Supported |
| Install/reuse VS Code and Ollama from official sources | Supported | Supported/Manual app UI | Supported/Manual `sudo` |
| Enforce HTTPS-only redirects and bounded reads | Supported | Supported | Supported |
| Verify installer publisher signature | Supported Authenticode | Out until native signing/notarization work exists | Out; package/release integrity contract applies |
| Start a missing Ollama server | Supported | Supported | Supported |
| Restart an existing app/service for context changes | Manual consent | Manual consent | Manual consent |
| Discover privacy-allowlisted hardware/storage | Supported | Supported | Supported |
| Install GPU drivers or change firmware allocation | Out | Out | Out |
| Deterministic catalog recommendation | Supported | Supported | Supported |
| Exact registry/GGUF metadata preflight | Supported | Supported | Supported |
| Catalog model pull, alias creation, and reuse | Supported | Supported | Supported |
| Arbitrary Hugging Face discovery/download/import | Out | Out | Out |
| Validate local-only metadata, tools, 65K allocation, and offload | Supported | Supported | Supported |
| Create/reuse Local AI profile and install `Ollama.ollama` | Supported | Supported | Supported |
| Select the sparkle-picker model, workspace trust, Configure Tools, and approvals | Manual | Manual | Manual |
| Edit undocumented VS Code profile storage | Out | Out | Out |
| User-local manager install, PATH marker, update, rollback | Supported | Supported | Supported |
| Remove external VS Code/Ollama applications | Manual explicit flag and confirmation | Manual explicit flag and confirmation | Manual explicit flag and confirmation |
| Native publisher signing/notarization | Out for first stable | Out for first stable | Not applicable |

## Universal exclusions

The manager does not clone repositories, approve workspace trust or agent tools, change organization policy, disable telemetry or security controls, manage firewalls, choose cloud fallback, install GPU drivers, alter firmware, or claim offline/privacy guarantees beyond its explicit local operations and data exclusions.

