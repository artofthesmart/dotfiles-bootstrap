# Local coding AI: Windows

**Outcome:** use VS Code's Chat and Agent tools with Ollama running a model on
your computer, without per-request cloud-model charges. Model quality and speed depend on your hardware. Cloud Copilot remains separate.

## Start here

The wizard supports **Windows x64, Windows 10 22H2 or newer**, with a desktop
session, internet access for setup and space for the applications and model.
Windows ARM devices are not automated by this wizard. NVIDIA/AMD acceleration
depends on supported hardware and drivers; the script does not replace drivers.
Models can require tens of gigabytes.

Download this repository using **Code > Download ZIP** on GitHub and extract
it, or use an existing checkout. Open **PowerShell**, not Command Prompt, in
that folder as your normal user, not Administrator. Inspect the script, then:

```powershell
powershell.exe -NoProfile -ExecutionPolicy RemoteSigned -File .\scripts\setup-local-ai.ps1
```

This execution policy applies only to that process. If Windows marks the
downloaded script as blocked, review it and use its **Properties > Unblock**
option. If company policy blocks scripts or installations, ask IT; do not
override managed controls.

## Follow the wizard

1. **Install missing apps.** Existing VS Code and Ollama installations are reused,
   Approve official user installers when needed; signatures are checked.
   VS Code must be 1.127 or newer.
   Downloads show the app name, MB/GB, percentage when available, and speed.
   Ollama includes GPU libraries: allow 4 GB installed, plus separate model space.
2. **Ask Gemini what fits.** Review and paste the hardware-only prompt into
   Gemini yourself; nothing is sent automatically. It requests a tool-capable
   local    model, exact Ollama name and link, storage size, context budget
   and smaller fallback. The prompt is saved in **Documents/Local-AI-Gemini-Prompt.txt**.
   A recommendation is not a performance guarantee.
3. **Choose and download locally.** Browse [Ollama's catalog](https://ollama.com/library)
   for the small, tool-capable model Gemini recommended. Copy the name after
   `ollama run`, including its size tag. Enter that name, storage estimate and
   context budget here; the wizard downloads it. No manual model download is needed.
   The wizard checks local inference and a harmless tool request without
   executing that tool, then provides a `local-ai-...` model copy with your
   context budget. Compatible Hugging Face GGUF files have a guided manual
   download/import alternative.
4. **Open your checkout.** Official Chat/Ollama extensions go into a separate
   **Local AI** profile. Follow the final UI steps: select **Agent**, choose
   your local Ollama model explicitly, and select local utility models in
   Settings. Approve tools individually. Try a small documentation edit and
   an existing test; inspect the diff.

## Switch local or cloud

Use **Manage (gear) > Profiles > your previous profile** (usually Default),
then select a cloud Chat model for metered Copilot. Normal cloud account,
organization and billing rules apply. Return via **Profiles > Local AI** and
select Ollama, not **Auto**.

If slow or out of memory, try a smaller model/context. Profiles are not billing
boundaries; gray-text autocomplete is separate. Telemetry/internet settings are
unchanged.
Small context budgets save memory but constrain agents; heed warnings.

**Sources:** [Windows requirements](https://docs.ollama.com/windows),
[VS Code models](https://code.visualstudio.com/docs/agent-customization/language-models),
[Ollama extension](https://github.com/ollama/ollama-vscode).
