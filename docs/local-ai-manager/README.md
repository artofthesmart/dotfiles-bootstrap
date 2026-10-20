# Local AI Manager contracts

Contract baseline: **v1.0.0**, approved October 20, 2026.

These documents define the product boundary for the independently implemented `local-ai` manager. The manager is independent of `bootstrap.sh`, `setup.py`, and `setup_termux.sh`; it does not import their state, invoke them, or change their defaults.

## Contract set

- [Architecture and domain contract](architecture.md)
- [Supported-host capability matrix](supported-hosts.md)
- [Current-script parity inventory](parity-inventory.md)
- [Issue-code and exit-code contract](issue-and-exit-codes.md)
- [Asset ownership and uninstall rules](asset-ownership.md)
- [Official Copilot CLI local-provider contract](copilot-local.md)

Machine-readable contracts:

- [Transaction state v1](../../schemas/local-ai-manager/transaction-state-v1.schema.json)
- [JSON event/result stream v1](../../schemas/local-ai-manager/event-stream-v1.schema.json)
- [Configuration v1](../../schemas/local-ai-manager/config-v1.schema.json)
- [Model catalog v1](../../schemas/local-ai-manager/model-catalog-v1.schema.json)

The release-embedded reviewed data is in [`catalog/models.json`](../../catalog/models.json). `internal/catalog` performs strict schema-shaped and domain validation, deterministic eligibility/scoring, separate-pool memory accounting, and primary/smaller-fallback selection. Run `make catalog-drift` for a read-only exact-tag registry report; it never rewrites the catalog. Registry and runtime verification remain authoritative.

User-local bootstrap, explicit update/rollback, PATH ownership, and friend-preview signing limitations are defined in [manager self-deployment](self-deployment.md).

`internal/ollama` implements the production Ollama domain behind later CLI/TUI adapters: bounded loopback API calls, HTTPS-only exact-manifest and GGUF range inspection, exact-tag reuse/acquisition, local GGUF import, safe 65,536-token aliases, live structured-tool and `/api/ps` verification, diagnosis and consent-aware repair planning, ownership-safe removal, and resumable state-engine steps.

`internal/copilot` implements official Copilot CLI discovery, conservative version gating, verified user-local stable-release installation, exact offline Ollama provider launchers, resumable receipts, diagnosis, repair, rollback, redaction, and ownership-aware uninstall. The CLI/TUI adapter exposes typed `copilot configure` and `copilot open` commands without duplicating those decisions.

## Versioning policy

The contract version and each schema discriminator are independent of the application version.

- Additive documentation clarifications and optional schema fields are backward-compatible within v1.
- New required fields, changed meanings, removed enum values, or reused issue/exit codes require a new major contract or schema version.
- Readers must reject unknown major schema versions and may preserve but must not interpret newer compatible minor application data.
- Stable issue codes and exit-code meanings are never reassigned. Deprecated codes remain reserved.
- Persisted state migrations must retain a previous copy and must never silently discard ownership or verification evidence.

The JSON schemas use JSON Schema Draft 2020-12. Every persisted or streamed object carries an explicit `schema_version` discriminator.

## Scaffold development

Manager development requires Go 1.22 or newer:

```sh
make test
make race
make integration
make vet
make fmt-check
make build
```

Use `make cross-build` to compile Windows, macOS, and Linux binaries for amd64 and arm64. Generated binaries are written under `build/`; remove them with `make clean`.

`internal/vscode` now implements the production VS Code lifecycle boundary: supported application discovery, the official Ollama extension's reviewed version gate, documented profile creation/readiness polling, profile-scoped extension repair, redacted diagnostics, state-engine receipts/resume, and protected UI handoff as `awaiting_manual`. CLI/TUI command wiring remains intentionally separate.
