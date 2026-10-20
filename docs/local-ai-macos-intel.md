# Local coding AI: Intel Mac

**Outcome:** use VS Code Chat and Agent tools with an Ollama model running on
your Mac, without per-request cloud-model charges. **Important: Ollama on Intel
macOS uses the CPU, not GPU acceleration.** Agents may be very slow; this path
is useful for experimentation, not a promise of cloud-level performance.

## Start here

You need an Intel Mac running macOS 14+, internet for setup,
and space for the applications and model. An unsupported older Mac is not
upgraded or worked around by this script. Large models can require tens of
gigabytes; start with a small tool-capable model.

Download this repository using GitHub's **Code > Download ZIP**, extract it,
and open Terminal in the extracted folder, or use an existing checkout.
Inspect the script, then run it as your normal user:

```sh
bash ./scripts/setup-local-ai.sh
```

## Follow the wizard

1. **Check/install VS Code and Ollama.** Existing apps are reused even if their
   commands are missing from your search path. Approve official downloads only
   when needed and move the downloaded apps into Applications. Use VS Code
   1.127 or newer. A small jq JSON utility may be offered in a private folder;
   Python and Homebrew are not prerequisites.
2. **Ask Gemini what fits.** Review the hardware-only prompt before copying it
   into Gemini yourself. It contains CPU, RAM, GPU limitations and available
   storage, not repository contents. It asks for an exact local Ollama Library
   model tag and download link, a smaller fallback, storage needs, license
   caveats and an agent context budget. Emphasize **CPU-only Intel Mac**.
   No information is sent automatically; Gemini's recommendation is not proof
   that a model will be fast or useful.
3. **Verify and download.** Paste Gemini's model, size estimate and context into
   the wizard. It independently checks the exact registry tag, actual layer
   size, GGUF context limit and free disk before a large pull. A known 32K tag
   is rejected before download. Installed models are reused without updating;
   new downloads also appear in Ollama. The wizard then checks advertised tools
   and a harmless structured tool call. CPU-only performance may still be poor.
4. **Use the Local AI profile.** The wizard installs official Chat/Ollama
   extensions there, preserving your original profile, and opens your checkout.
   Follow the final UI instructions: choose **Agent**, explicitly select your
   `local-ai-...` Ollama model, and choose local utility models in Settings.
   Review and approve tools individually. Start with a small documentation
   edit and an existing test.

## Switch local or cloud

Choose **Manage (gear) > Profiles > your previous profile** (usually Default),
then select a cloud model in Chat for metered Copilot. Return through
**Profiles > Local AI**, then explicitly select Ollama. Avoid **Auto** when you
want local inference. Your cloud account, plan and organization policies apply.

Require **65536+ context tokens**. If memory runs short, choose a smaller
compatible model, not a shorter context. Profiles do not enforce billing
isolation; autocomplete is separate.
This setup does not disable telemetry or internet access.

**Sources:** [Intel CPU-only support](https://docs.ollama.com/macos),
[VS Code models](https://code.visualstudio.com/docs/agent-customization/language-models),
[Ollama extension](https://github.com/ollama/ollama-vscode).
