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
HTTPS source validation. HTTPS redirect-downgrade rejection is covered by the
mocked script suites. It downloads fixtures, not actual installers.
The Windows wizard uses built-in progress rendering and needs no UI module.

On native Windows PowerShell:

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\tests\test-local-ai.ps1
```

Python is a **test-runner prerequisite only**, not a wizard prerequisite. Bash
tests also require existing curl and jq. PowerShell tests have no Pester
dependency. They dot-source the wizard rather than running its installer entry
point. Fixtures and fake executables prevent real installs, downloads or edits
to user profiles. The Bash full-flow test uses a terminal fixture and a fake
loopback Ollama server on a dynamically assigned port.

Coverage includes automatic Ollama-tag versus Hugging Face URL detection,
model input validation, cancellation, GGUF validation,
exact-tag registry preflight, pre-download context rejection, installed-model
reuse, context-budget aliases, storage limits, explicit download/HTTP/tool
failures, cloud metadata rejection, hardware field allowlisting and profile-scoped
commands. It also verifies that the wizard does not try to reinstall bundled
Copilot Chat, installs only the Ollama extension into Local AI, rejects
non-HTTPS redirect destinations, and preserves native stderr and exit codes for
diagnosis. Bash covers translated Macs, Intel warnings, unsupported
OS/architecture, old VS Code, failed elevation, existing apps outside PATH,
repeated runs and preservation of default settings.
It also verifies that context, exact model size, clipboard handling, model
storage, private jq installation, Ollama startup and Local AI profile setup do
not require redundant user answers.

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
   Confirm the prompt contains a freshly preflighted exact-tag catalog. Check
   that Gemini is restricted to that list and instructed to put only `MODEL`
   first. The wizard derives exact size and fixes context at 65536 itself.
   Context must fit hardware with model weights plus KV cache and OS headroom.
   Distinguish structured tool-call evidence from coding benchmarks or JSON prose.
3. Preflight a compatible tag and a known 32K tag. Confirm exact registry layer
   size and GGUF context are shown before confirmation, and that the 32K tag is
   rejected before `ollama pull`. Pull a real tool-capable model. Try a
   space-containing GGUF import and an incompatible model. Confirm advertised
   tools **and** actual expected tool-call output are checked. Confirm cloud
   tags/metadata are rejected.
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
8. Rerun: confirm applications and the exact installed model are reused without
   an update pull, while the context alias is recreated only after confirmation.
   Confirm default-profile settings remain unchanged.

## Scope and recovery

- **Windows iGPU memory:** report installed RAM from the sum of
  `Win32_PhysicalMemory.Capacity`, separately from Windows-visible
  `Win32_ComputerSystem.TotalPhysicalMemory`. The latter can exclude reserved
  memory and must not be labelled installed capacity. Neither is free RAM.
  Fail explicitly if installed capacity cannot be determined; do not substitute
  a smaller Windows-visible number. Do not infer the cause of a discrepancy.
  Treat installed RAM as one physical pool and use up to
  50% as a dynamic shared-memory planning ceiling, not fixed VRAM or guaranteed
  Ollama capacity. Actual Windows/driver availability still constrains models.
  The Windows wizard no longer asks for an ambiguous GPU-memory
  amount. Gemini must not infer BIOS reservations, add shared memory to RAM,
  promise exact free RAM, or assume full GPU offload. Dedicated NVIDIA memory
  remains separately identified when detected. The 50% rule is not applied to
  macOS or to discrete VRAM.
- **Tool smoke test:** both wizards now use Ollama's documented natural-language
  `get_temperature` example, asking about New York. No weather function executes
  and no external weather request occurs. Only an actual structured `tool_calls`
  entry with the expected name and city passes; JSON in response text does not.
  Live diagnosis on the installed Nemo 12B showed the old synthetic request
  returning JSON prose while the documented example produced structured calls.
  This establishes basic tool calling, not full VS Code Agent reliability.
- **Storage and exact-tag preflight:** the wizard asks about the running server's model directory.
  `OLLAMA_MODELS` in a terminal does not necessarily configure a separately
  running service. Supply that service's actual storage directory for disk
  checks. For Ollama Library models, Gemini's size is advisory: the wizard reads
  the exact tag's registry manifest and uses the actual layer total for the disk
  check. It also range-reads only the first MiB of the GGUF layer to inspect
  context metadata before a multi-gigabyte pull. If registry or context metadata
  cannot be verified, it stops without downloading. Leave space for the OS,
  caches and temporary/imported copies too.
- **Model choice:** before showing the Gemini prompt, the wizard preflights a
  small maintainer shortlist and embeds only exact tags whose current registry
  GGUF metadata reports at least 65536 context tokens. Gemini may choose only
  from that generated list; an out-of-catalog answer is rejected before pull.
  This makes Gemini the hardware-fit advisor, not the source of truth for tag
  eligibility. Some shortlisted models can still fail the local tool smoke test
  or perform poorly as agents. GGUF magic validation alone only identifies the
  format; Ollama still validates actual compatibility. The wizard infers the
  source from the first answer: a simple model name/tag uses the Ollama Library,
  while an `https://huggingface.co/...` URL starts the guided GGUF import. It no
  longer asks the user to classify the source manually.
- **Context:** the wizard creates a `local-ai-...` model with `num_ctx` fixed at
  65536 rather than asking the user to repeat Gemini's value. This is not a
  hardware guarantee. The official extension recommends at least 65536 tokens; this
  workflow treats that as its minimum requirement. Smaller input is rejected,
  not silently accepted with a warning. Larger context increases memory use.
  Ollama Library tags advertising a smaller supported limit are rejected before
  download. Local metadata is checked again after acquisition; an unknown local
  limit produces an explicit warning. Setting `num_ctx` beyond a model's
  supported window does not prove extended-context support. Exact installed
  source models are reused without a pull/update. Existing aliases can be
  replaced after confirmation; source weights are reused for the alias.
  If nothing fits at 65536+, Gemini must return `MODEL: NONE` and explain why,
  rather than recommending a 32K model or lowering the context. Entering NONE
  stops the wizard with that explanation instead of trying to download it.
- **Slow/no response:** the smoke test allows 180 seconds. Both wizards allow 1024
  output tokens initially. A slow/reasoning model
  may exhaust this budget without returning a tool call. Windows reports the
  model and stop reason, offers a 2048-token retry, or explicitly allows
  continuing to VS Code with tool use **UNVERIFIED**. This is not a passed
  check; test a real approved edit and switch models if tools fail. HTTP and
  inference errors still stop setup rather than being treated as unverified
  successful inference. Inspect Ollama
  logs and choose a smaller/faster model that still supports and fits 65536+
  context, then rerun. Do not reduce context below the minimum.
  `ollama ps` displays actual CPU/GPU offload after the check; an installed
  driver alone is not evidence of GPU inference. Intel Mac Ollama is CPU-only.
- **VS Code profile setup:** VS Code 1.127+ bundles `GitHub.copilot-chat` and
  enables its built-in extension with automatic updates. Do not pass that ID to
  `--install-extension`; current VS Code can return exit 1 for a built-in
  extension. The wizard installs only `Ollama.ollama` into Local AI. Native
  command failures now include the command, exit code, stdout and stderr for
  diagnosis, including version and extension-list checks. On a first run, the
  wizard opens a blank window with `--profile "Local AI"` before installing the
  extension, because launching creates a missing profile while
  `--install-extension --profile` alone can fail with "Profile not found." It
  waits briefly for profile creation, then installs and verifies the extension.
  The user can then open or clone any repository from VS Code; setup does not
  require, inspect or validate a repository path.
- **Download transport:** application installers and Ollama registry metadata
  must begin and remain on HTTPS across redirects. A downgrade to HTTP is
  rejected. This protects transport integrity but does not pin release content;
  Windows installer signatures provide an additional publisher check.
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
