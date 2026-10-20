# Official Copilot CLI local-provider contract

Evidence review date: **October 20, 2026**.

## Official-source conclusions

- GitHub Docs supports Copilot CLI BYOK with local Ollama through provider environment variables and documents `COPILOT_OFFLINE=true` as the mode that prevents contact with GitHub servers when the configured provider is local.
- Ollama's Copilot CLI integration documents the OpenAI-compatible `http://localhost:11434/v1` endpoint, an empty provider API key, `COPILOT_PROVIDER_WIRE_API=responses`, and the selected Ollama model in `COPILOT_MODEL`.
- The manager additionally sets the documented `COPILOT_PROVIDER_TYPE=openai` and `COPILOT_PROVIDER_WIRE_MODEL` to the exact selected `local-ai-...` alias so the model sent on the wire cannot drift from the selected model.
- GitHub Docs supports WinGet, Homebrew, npm, the official install script, and direct official release downloads. The manager uses the direct `github/copilot-cli` stable release path because it permits a verified user-local install without a global Node/npm package or elevation.
- The `github/copilot-cli` release API published stable `v1.0.101` on October 20, 2026 with platform archives and SHA-256 digests. The official installer also verifies `SHA256SUMS.txt` when checksum tooling is available. The manager requires both the API digest and the exact manifest entry to agree before extraction.
- The supported floor is `1.0.100`: the official changelog added `--disable-client-telemetry` in that release. The manager installs the current stable release and uses the floor only when deciding whether an existing executable can be reused.
- VS Code's language-model documentation supports fully local Ollama chat through the official Ollama extension. In the supported VS Code `1.127+` contract, Copilot Chat is bundled; there is no current supported non-bundled branch that also satisfies the official Ollama extension minimum, so the manager never marketplace-installs `GitHub.copilot-chat`.

Primary sources:

- [GitHub Copilot CLI BYOK and offline mode](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-byok-models)
- [GitHub Copilot CLI installation](https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/install-copilot-cli)
- [Official Copilot CLI releases](https://github.com/github/copilot-cli/releases)
- [Official Copilot CLI changelog](https://github.com/github/copilot-cli/blob/main/changelog.md)
- [Ollama Copilot CLI integration](https://docs.ollama.com/integrations/copilot-cli)
- [VS Code language models](https://code.visualstudio.com/docs/agent-customization/language-models)

## Lifecycle behavior

`internal/copilot`:

1. probes an existing `copilot` executable and reuses it only when its version meets the supported floor;
2. otherwise selects the official archive for the current OS/architecture, verifies HTTPS metadata, GitHub API digest, `SHA256SUMS.txt`, archive safety, and the staged executable version;
3. installs only into the manager's user-local bin directory and records explicit manager ownership;
4. atomically creates `copilot-local` (`copilot-local.cmd` on Windows), preserving a verified rollback copy when replacing a manager-owned launcher;
5. sets loopback `/v1`, provider type `openai`, Responses API, exact wire/model alias, empty local API key, and `COPILOT_OFFLINE=true`;
6. invokes the official CLI with `--disable-client-telemetry` and forwards every user argument unchanged;
7. refuses unowned target replacement and diagnoses missing Ollama, alias, CLI, minimum version, provider contract, and launcher;
8. resumes interrupted state-engine operations from privacy-safe ownership receipts and removes only manager-owned Copilot assets.

The launcher contains no cloud endpoint and offline mode is mandatory. A local-provider failure is returned as a failure; the manager does not select a GitHub-hosted model.

`local-ai setup` verifies the Ollama alias before configuring the supported VS Code surface and the Copilot launcher. `local-ai copilot configure` uses the same resumable transaction steps independently. `local-ai copilot open [args...]` verifies manager ownership and the complete offline provider contract before execution; manager flags must precede `copilot open`, and every following argument is passed to the launcher unchanged.
