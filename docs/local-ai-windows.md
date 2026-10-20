# Local coding AI: Windows

**Outcome:** use VS Code's Chat and Agent tools with Ollama running a model on
your computer, without per-request cloud-model charges. Model quality and speed depend on your hardware. Cloud Copilot remains separate.

## Start here

Requires **Windows x64, Windows 10 22H2+**, a desktop session, internet for setup
and storage. ARM is not automated. GPU acceleration requires supported drivers;
the wizard does not replace drivers. Models can need tens of gigabytes.

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

1. **Install missing apps.** Existing VS Code and Ollama installations are reused.
   Approve official user installers when needed; signatures are checked.
   VS Code must be 1.127 or newer.
   Downloads show the app name, MB/GB, percentage when available, and speed.
   Ollama includes GPU libraries: allow 4 GB installed, plus separate model space.
2. **Ask Gemini what fits.** Review and paste the hardware-only prompt into
   Gemini yourself; nothing is sent automatically. It requests a tool-capable
   local model, exact Ollama name and link, storage size, context budget
   and smaller fallback. The prompt is saved in **Documents/Local-AI-Gemini-Prompt.txt**.
   A recommendation is not a performance guarantee.
3. **Download in Ollama Desktop.** Open Ollama from Start. Click the model
   selector and type Gemini's exact recommended name, including its size tag
   (such as `:7b`; use Gemini's exact tag). Select the
   matching **local**, not cloud, model. Click its download icon if shown.
   Type **Say hello** in the message box and press Enter; Ollama downloads
   the model if needed. Wait for a reply, then return to the wizard and enter
   the same name, storage estimate and context budget.
   Alternatively, enter the name directly in the wizard to download there.
   Existing model files are reused. The wizard checks tools and creates a
   `local-ai-...` context-budget copy.
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

Require **65536+ context tokens**; choose a smaller compatible model if memory runs short.
Profiles are not billing
boundaries; gray-text autocomplete is separate. Telemetry/internet settings are
unchanged.

**Sources:** [Windows requirements](https://docs.ollama.com/windows),
[VS Code models](https://code.visualstudio.com/docs/agent-customization/language-models),
[Ollama extension](https://github.com/ollama/ollama-vscode).
