# Local coding AI: Intel Mac

**Outcome:** use VS Code Chat and Agent tools with an Ollama model running on
your Mac, without per-request cloud-model charges. **Important: Ollama on Intel
macOS uses the CPU, not GPU acceleration.** Agents may be very slow; this path
is useful for experimentation, not a promise of cloud-level performance.

## Start here

You need an Intel Mac running macOS 14+, internet,
and space for the applications and model. An unsupported older Mac is not
upgraded or worked around by this script. Large models can require tens of
gigabytes; start with a small model.

Download this repository using GitHub's **Code > Download ZIP**, extract it,
and open Terminal in the extracted folder, or use an existing checkout.
Inspect the script, then run it as your normal user:

```sh
bash ./scripts/setup-local-ai.sh
```

## Follow the wizard

1. **Check/install VS Code and Ollama.** Existing apps are reused even if their
   commands are missing from your search path. Approve official downloads only
   when needed and move apps into Applications. Use VS Code
   1.127 or newer. Checksum-verified jq is installed privately if needed;
   Python and Homebrew are not prerequisites.
2. **Ask Gemini what fits.** Review the hardware-only prompt before copying it
   into Gemini yourself. It contains CPU, RAM, GPU limitations and available
   storage, not repository contents. The wizard verifies an exact-tag
   catalog with 65K+ GGUF context; Gemini must choose from that list or return
   `MODEL: NONE`. It compares CPU-only fit, a smaller fallback and license caveats.
   Nothing is sent automatically; Gemini does not prove model performance.
3. **Verify and download.** Paste only Gemini's exact listed model tag. The
   wizard gets the exact size itself, fixes context at `65536`, and rejects
   out-of-catalog tags before download. It
   checks free disk and shows a review. Installed models are reused. The wizard
   tests structured tools and verifies Ollama allocated 65K. If it reports 32K,
   set Ollama **Settings > Context length** to 64K, restart it, and rerun.
   CPU-only performance may still be poor.
4. **Use the Local AI profile.** Copilot Chat is bundled with supported VS Code.
   The wizard installs the Ollama extension in Local AI. Open or clone a
   repository there.
   In Chat, choose **Agent** in the mode control and your `local-ai-...` model
   with the sparkle icon. Choose utility models in Settings.
   If warned about 128 tools, use **Configure Tools** and disable unused groups.
   Review tools individually. Start with a documentation
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

## Use the same model in the terminal

Install the official GitHub Copilot CLI once with `curl -fsSL https://gh.io/copilot-install | bash`, then run `scripts/copilot-local.sh` from this repository inside any project folder. It uses your `local-ai-...` model in offline mode, so nothing is sent to GitHub-hosted models. Any arguments, such as `-p "explain this repo"`, are passed straight to Copilot.
