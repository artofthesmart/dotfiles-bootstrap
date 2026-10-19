# Local coding AI: Apple Silicon Mac

**Outcome:** use VS Code's familiar Chat and Agent tools with a model running
on your Mac, rather than paying a cloud provider for each model request.
Your hardware sets the speed and capacity; this is not free cloud Copilot.

## Start here

Use a Mac with an M-series chip, macOS 14 or newer, an internet connection for
setup, and enough free space for the apps and model. Models can require tens of
gigabytes. GPU and CPU share your Mac's memory: 16 GB of unified memory does
**not** mean 16 GB of RAM plus another 16 GB of graphics memory.

Download this repository from GitHub using **Code > Download ZIP**, extract it,
and open Terminal in the extracted folder. Alternatively, use your existing
checkout. Inspect the script before running it as your normal user:

```sh
bash ./scripts/setup-local-ai.sh
```

## Follow the wizard

1. **Install missing apps.** The wizard detects VS Code and Ollama, including
   apps not on your command search path. If missing, it downloads official
   installers after asking. Move the apps into Applications when prompted.
   VS Code must be version 1.127 or newer. If needed, the wizard offers a small
   private installation of the jq JSON utility; no Python or Homebrew is needed.
2. **Choose a sensible model.** Review the displayed hardware-only prompt, then
   paste it into Gemini yourself. Nothing is sent automatically. Ask for a
   tool-capable local coding model, an exact Ollama Library tag, download link,
   size, context budget, and smaller fallback. Return with the recommendation;
   advice is a starting point, not a guarantee of an optimal model.
3. **Download in Ollama.** Open Ollama from Applications. Click the model
   selector; type Gemini's exact name including its size tag (such as `:32b`).
   Select the local model, click its download icon if shown, type **Say hello**
   and press Enter. Wait for a reply. Return to the wizard with that same name,
   storage estimate and context budget. It reuses downloaded files and checks
   tools. Alternatively, let the wizard download it.
4. **Open your repository.** The wizard installs official Chat/Ollama extensions
   into a separate **Local AI** profile and opens your existing checkout.
   Follow its final instructions: select **Agent**, explicitly choose your
   `local-ai-...` Ollama model, and set the two utility-model settings to local
   models too. Approve tools individually and review resulting changes.

## Switch local or cloud

For cloud Copilot, choose **Manage (gear) > Profiles > your previous profile**
(usually Default), then choose your cloud model in Chat. Normal account,
organization and billing rules apply. To return, choose **Local AI** and select
the Ollama model again. **Auto is not a local-only choice.**

Try one small documentation edit and an existing test before larger work.
If slow or out of memory, rerun with Gemini's smaller fallback or less context.
Small context budgets save memory but constrain agents; heed warnings.
Profiles are not billing/security boundaries; gray-text autocomplete is separate.
This setup does not disable telemetry or internet access.

**Sources:** [VS Code models](https://code.visualstudio.com/docs/agent-customization/language-models),
[Ollama extension](https://github.com/ollama/ollama-vscode),
[Mac requirements](https://docs.ollama.com/macos).
