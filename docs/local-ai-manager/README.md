# Local AI Manager contracts

Contract baseline: **v1.0.0**, approved October 20, 2026.

These documents define the product boundary for the future `local-ai` manager before any Go module or host-mutating implementation exists. The manager is independent of `bootstrap.sh`, `setup.py`, and `setup_termux.sh`; it must not import their state, invoke them, or change their defaults.

## Contract set

- [Architecture and domain contract](architecture.md)
- [Supported-host capability matrix](supported-hosts.md)
- [Current-script parity inventory](parity-inventory.md)
- [Issue-code and exit-code contract](issue-and-exit-codes.md)
- [Asset ownership and uninstall rules](asset-ownership.md)

Machine-readable contracts:

- [Transaction state v1](../../schemas/local-ai-manager/transaction-state-v1.schema.json)
- [JSON event/result stream v1](../../schemas/local-ai-manager/event-stream-v1.schema.json)
- [Configuration v1](../../schemas/local-ai-manager/config-v1.schema.json)
- [Model catalog v1](../../schemas/local-ai-manager/model-catalog-v1.schema.json)

## Versioning policy

The contract version and each schema discriminator are independent of the application version.

- Additive documentation clarifications and optional schema fields are backward-compatible within v1.
- New required fields, changed meanings, removed enum values, or reused issue/exit codes require a new major contract or schema version.
- Readers must reject unknown major schema versions and may preserve but must not interpret newer compatible minor application data.
- Stable issue codes and exit-code meanings are never reassigned. Deprecated codes remain reserved.
- Persisted state migrations must retain a previous copy and must never silently discard ownership or verification evidence.

The JSON schemas use JSON Schema Draft 2020-12. Every persisted or streamed object carries an explicit `schema_version` discriminator.

## Scaffold development

The manager scaffold requires only Go 1.22 or newer:

```sh
make test
make race
make integration
make vet
make fmt-check
make build
```

Use `make cross-build` to compile Windows, macOS, and Linux binaries for amd64 and arm64. Generated binaries are written under `build/`; remove them with `make clean`.
