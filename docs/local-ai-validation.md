# Local AI setup: validation and troubleshooting

The setup wizards are independent of the shell/Termux bootstrap. They never run
that bootstrap or change its component defaults.

## Automated checks

Run from the repository root:

```sh
bash -n scripts/setup-local-ai.sh && python3 -m unittest discover -s tests -p 'test_local_ai.py' -v
```

With PowerShell installed, run:

```powershell
pwsh -NoProfile -File tests/test-local-ai.ps1
```

Exercise the real PowerShell download loop against a local server (Python and
`pwsh` required; alternatively set `PWSH` to the PowerShell executable):

```sh
python3 -m unittest discover -s tests -p 'test_windows_download.py' -v
```

This covers readable app-labelled download progress, known and unknown total
sizes, byte-for-byte transfer, HTTP failures, truncated-transfer cleanup, and
HTTPS source validation. It downloads fixtures, not actual installers.
The Windows wizard uses built-in progress rendering and needs no UI module.

On native Windows PowerShell:

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\tests\test-local-ai.ps1
```

Python is a **test-runner prerequisite only**, not a wizard prerequisite. Bash
tests also require existing curl, jq and Git. PowerShell tests have no Pester
dependency. They dot-source the wizard rather than running its installer entry
point. Fixtures and fake executables prevent real installs, downloads or edits
to user profiles. The Bash full-flow test uses a terminal fixture and a fake
loopback Ollama server on a dynamically assigned port.

Coverage includes model input validation, cancellation, GGUF validation,
context-budget aliases, storage limits, explicit download/HTTP/tool failures,
cloud metadata rejection, hardware field allowlisting and profile-scoped
commands. Bash also covers translated Macs, Intel warnings, unsupported
OS/architecture, old VS Code, failed elevation, existing apps outside PATH,
repeated runs and preservation of default settings.

Validation performed during implementation: Bash syntax and mocked tests on
Linux, and PowerShell parser/mocked tests using an isolated PowerShell 7 runtime
on Linux. These do **not** establish native macOS/Windows support, Windows
PowerShell 5.1 behavior, installer UX, GPU compatibility, model quality, or actual
VS Code Agent operation. No real application/model installation or four-platform
hardware acceptance run was performed.

## Real-machine acceptance checklist

Record OS/build, architecture, VS Code/Ollama/extension versions, model/tag,
quantization, context size, RAM/VRAM, and results. Do not include machine
identifiers or repository contents in shared reports.

| Platform | Required exercise | Native result at implementation |
|---|---|---|
| Apple Silicon macOS 14+ | Native and translated terminal; unified memory report; actual GPU offload | Not run |
| Intel macOS 14+ | CPU-only warning; realistic small-model agent usability | Not run |
| Windows x64 10 22H2+ | Windows PowerShell 5.1 and PowerShell 7; user installers; actual supported GPU/CPU | Not run |
| Ubuntu / Debian desktop, x86-64 / ARM64 | Official .deb and Ollama service; APT permissions; actual GPU/CPU | Not run |

For each platform, exercise these steps:

1. Run as a normal user with a new installation and again with preinstalled
   apps, including applications not on PATH. Confirm downloads use the correct
   architecture. Cancel installs/elevation and confirm the wizard stops clearly.
2. Confirm model-storage volume selection, disk estimates and the allowlisted
   hardware report. Review missing memory fields before sharing with Gemini.
   Confirm the wizard makes no Gemini call.
    Check that its prompt requires Gemini to put `MODEL`, `CONTEXT_TOKENS` and
    `DOWNLOAD_GIB` first, ahead of analysis, and to distinguish actual structured
    tool-call evidence from coding benchmarks or JSON returned as ordinary text.
3. Pull a real local tool-capable model. Try a compatible single-file GGUF
   import with a space-containing path and an incompatible model. Confirm
   advertised tools **and** actual expected tool-call output are checked.
   Confirm cloud tags/metadata are rejected.
4. Exercise low space, failed download, unavailable server, memory exhaustion
   and timeout. No failure should print a successful check or silently choose
   a cloud model.
5. Verify extensions are enabled in Local AI, original profile settings are
   unchanged, and your selected existing checkout opens. Trust it manually.
   Run **Ollama: Diagnose Models** to confirm the loopback endpoint and alias.
6. Choose Agent and the exact local alias, configure local utility models, and
   approve a small edit and existing relevant test. Review the diff and
   tool approvals. Do not disable approval safeguards.
7. Switch to the previous profile and a cloud model, then back to Local AI
   and Ollama. Confirm the active model each time; profiles do not enforce
   billing boundaries.
8. Rerun: confirm applications are reused, model replacement is disclosed,
   and default-profile settings remain unchanged.

## Scope and recovery

- **Storage:** the wizard asks about the running server's model directory.
  `OLLAMA_MODELS` in a terminal does not necessarily configure a separately
  running service. Supply that service's actual storage directory for disk
  checks. Unknown download sizes are explicitly warned about; leave space for
  the OS, caches and temporary/imported copies too.
- **Model choice:** the Gemini prompt is advice, not a benchmark. Some models
  advertise tools but fail the smoke test or perform poorly as agents. Try
  the fallback or a different tool-capable template. GGUF magic validation
  only identifies the format; Ollama still validates actual compatibility.
- **Context:** the wizard creates a `local-ai-...` model with `num_ctx`.
  It defaults to 8192 and accepts 2048-1048576 tokens, not as a hardware
  guarantee. Larger context increases memory use. The selected model/backend
  must support it. Existing aliases can be replaced after confirmation;
  source weights are reused rather than downloaded again for the alias.
  The official extension recommends at least 65536 tokens for agents and warns
  below that. The conservative wizard default prioritizes memory headroom, not
  full agent capability; Gemini is asked to identify hardware that cannot
  meet the recommendation. Smaller-context setups are constrained.
- **Slow/no response:** the smoke test allows 180 seconds. Windows allows 1024
  output tokens initially; Bash currently allows 256. A slow/reasoning model
  may exhaust this budget without returning a tool call. Windows reports the
  model and stop reason, offers a 2048-token retry, or explicitly allows
  continuing to VS Code with tool use **UNVERIFIED**. This is not a passed
  check; test a real approved edit and switch models if tools fail. HTTP and
  inference errors still stop setup rather than being treated as unverified
  successful inference. Inspect Ollama
  logs, choose a smaller/faster model or reduce context, then rerun.
  `ollama ps` displays actual CPU/GPU offload after the check; an installed
  driver alone is not evidence of GPU inference. Intel Mac Ollama is CPU-only.
- **Missing model in VS Code:** verify Ollama is responding at
  `http://127.0.0.1:11434`, run **Ollama: Refresh Models**, then
  **Ollama: Diagnose Models**. Verify VS Code 1.127+ and extension versions.
  Remove deprecated built-in Ollama provider configuration in the Local AI
  profile; provider-specific configuration can override its endpoint setting.
  Enable `chat.agentHost.byokModels.enabled` if required for your harness.
- **Metered utility tasks:** the chat model does not automatically control
  background utility models. In Local AI's Settings, choose the local model
  for `chat.utilityModel` and `chat.utilitySmallModel`. Do not leave cloud
  defaults or assume Auto is local. Cloud-model access, account-dependent
  features and organization policies remain separate.
- **Windows limits:** this wizard deliberately rejects ARM64 and administrator
  terminals. Installers are checked for a valid Authenticode signature and
  expected publisher. If enterprise policy blocks installation or scripts,
  consult IT; do not bypass managed policy.
- **Linux limits:** use a current Ubuntu/Debian desktop with supported
  upstream runtime libraries, Git and curl. No other distro, driver, headless
  server, WSL/remote workflow or obsolete OS is automated.
- **Persistent files:** the reviewed prompt is saved as
  `Local-AI-Gemini-Prompt.txt` in your **Documents** folder. Windows uses the
  system Documents location (including OneDrive redirection); Linux respects
  `xdg-user-dir DOCUMENTS` when available, otherwise uses `~/Documents`.
  Each run replaces that prompt file. The full path is printed. A synced
  Documents folder may sync this hardware-only file through your normal cloud
  backup service; the wizard itself does not upload it. jq, if needed,
  lives only under the Mac/Linux setup folder's `bin`. Temporary installer
  files are cleaned up at exit; installed apps, models and Local AI remain.
  Nothing is uploaded automatically.

## Source maintenance

See [SOURCES.md](../SOURCES.md#local-ai-vs-code-wizards). Before changing the
minimum VS Code version, extension IDs, settings, installer endpoints or model
capability handling, verify upstream documentation/manifests and repeat the
relevant mocked and native acceptance checks.
