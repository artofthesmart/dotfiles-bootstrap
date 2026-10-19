# Local coding AI: Ubuntu / Debian

**Outcome:** use VS Code Chat and Agent tools with an Ollama model running on
your desktop, rather than paying a cloud provider per model request. Hardware
sets the speed, memory limit and practical model quality. Cloud Copilot services
remain separate.

## Start here

Use a supported **Ubuntu or Debian desktop**, x86-64 or ARM64, with internet
access for setup, working `sudo`, Git, curl and sufficient storage. Other
distributions, headless servers, Termux and remote/WSL workflows are outside this
wizard's scope. GPU acceleration requires supported hardware/drivers; the script
does not install GPU drivers. Models can need tens of gigabytes.

Download this repository through GitHub's **Code > Download ZIP** and extract
it, or use an existing checkout. Open a desktop terminal in that folder.
Inspect the script, then run it as your normal user:

```sh
bash ./scripts/setup-local-ai.sh
```

## Follow the wizard

1. **Install missing apps.** Existing VS Code and Ollama installations are
   reused. Approve Microsoft's official VS Code `.deb` installation through
   APT; it may offer Microsoft's update repository. Approve the official Ollama
   installer separately; it can use `sudo` and create a system service. The
   script does not upgrade the whole computer. VS Code must be 1.127 or newer.
   A small jq JSON utility, if missing, is installed privately without Python.
2. **Ask Gemini what fits.** Review the hardware-only prompt and paste it into
   Gemini yourself. It includes RAM, CPU, GPU/known graphics memory and free
   model-storage space, not repository data. Nothing is sent automatically.
   Obtain a tool-capable local Ollama Library tag, download link, expected
   storage, practical context budget and smaller fallback. Supply missing GPU
   memory from specifications if available; advice is not a performance
   guarantee.
3. **Get and check the model.** Enter the recommendation, storage estimate and
   context budget. The wizard downloads the model, creates a `local-ai-...`
   context-budget copy, and checks a harmless tool request without executing
   that tool. Compatible, manually downloaded Hugging Face GGUF files have a
   guided import alternative. Start Ollama when prompted if its service is not
   already responding.
4. **Use your checkout.** Official Chat/Ollama extensions are installed into a
   separate **Local AI** profile. Follow the final UI steps: choose **Agent**,
   explicitly select the local Ollama model, and set utility models to local
   choices. Approve tools individually; try a small documentation edit and an
   existing test, then inspect the diff.

## Switch local or cloud

Choose **Manage (gear) > Profiles > your previous profile** (usually Default)
and select a cloud Chat model for metered Copilot. Normal account, organization
and billing rules apply. Return through **Profiles > Local AI**, explicitly
selecting Ollama rather than **Auto**.

For memory errors or slow CPU inference, try a smaller model/context. Profiles
are not billing boundaries; autocomplete is separate. Telemetry and internet
access are unchanged.
Small context budgets save memory but constrain agents; heed warnings.

**Sources:** [Linux installation](https://docs.ollama.com/linux),
[VS Code Linux](https://code.visualstudio.com/docs/setup/linux),
[VS Code models](https://code.visualstudio.com/docs/agent-customization/language-models).
