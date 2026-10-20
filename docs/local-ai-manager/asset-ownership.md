# Asset ownership and uninstall rules

Contract version: **1.0.0**

The manager records every relevant asset with a stable ID, type, origin, ownership class, privacy-safe locator, and uninstall policy. Detection is not ownership.

## Ownership classes

| Class | Meaning | Default uninstall behavior |
|---|---|---|
| `manager_owned` | Created or installed by a successfully applied manager step and recorded in state | Remove when its policy is `remove_with_manager`; otherwise require its documented explicit flag |
| `user_owned` | Supplied, created, or explicitly retained by the user, including assets that predate the manager | Retain |
| `external_detected` | Existing third-party application, model, profile, service, or setting detected through supported interfaces | Retain |

Adoption is an explicit transaction that changes an asset to `manager_owned`, records the prior classification, verifies the exact asset, explains future uninstall behavior, and requires confirmation. Setup cannot imply adoption merely by reusing an asset.

## Required asset record

The [transaction state schema](../../schemas/local-ai-manager/transaction-state-v1.schema.json) requires:

- stable `asset_id` and asset type;
- origin and ownership;
- privacy-safe locator: user-home paths use the literal `${HOME}` token, never a username;
- creating/adopting step ID;
- uninstall policy and explicit flag where applicable;
- dependency asset IDs;
- optional backup locator and verification evidence.

An apply receipt must exist before an asset can be marked manager-owned. Failed or interrupted creation is recorded as a mutation attempt, not owned inventory, until verification succeeds.

## Asset rules

| Asset | New install/create classification | Existing detection | Default uninstall |
|---|---|---|---|
| `local-ai` executable | `manager_owned` | `external_detected` unless explicitly adopted | Remove |
| Manager config/data/state/log directories | `manager_owned` | Existing valid manager state retains recorded class | Remove after final state/archive handling |
| PATH entry with manager marker | `manager_owned` | Unmarked PATH content is `user_owned` | Remove only the exact marked segment; restore marker-owned backup |
| Download cache/partial files | `manager_owned` | Not adopted | Remove |
| Update rollback binary | `manager_owned` | Not adopted | Remove with manager after uninstall succeeds |
| VS Code application | `manager_owned` only if manager installed and verified it | `external_detected` | Retain unless `--remove-vscode`, confirmation, and no ownership conflict |
| Ollama application/service | `manager_owned` only if manager installed and verified it | `external_detected` | Retain unless `--remove-ollama`, confirmation, and no ownership conflict |
| VS Code `Local AI` profile | `manager_owned` only when manager created it through supported CLI and verified readiness | `external_detected` | Guided UI deletion only; never edit undocumented profile storage |
| `Ollama.ollama` extension in Local AI | `manager_owned` only when manager installed it | `external_detected` | Remove only with `--remove-vscode-extension`; preserve other profile data |
| Catalog source model pulled by manager | `manager_owned` only if absent before the pull and verified afterward | `user_owned`/`external_detected` when pre-existing | Retain by default; `--remove-models` plus confirmation and dependency review |
| Generated `local-ai-...` alias | `manager_owned` when created and verified by manager | Existing alias is `user_owned` until adopted or replaced with consent | Retain by default; `--remove-aliases` plus confirmation |
| User-imported GGUF or source file | Never manager-owned in v1 | `user_owned` | Retain |
| Generated support report | `user_owned` at chosen destination | Not tracked after creation | Retain |
| OS security, firewall, telemetry, driver, workspace trust, and tool approval settings | Never managed | Not inventoried as assets | Never change or remove |

## Uninstall modes

Plain `local-ai uninstall`:

1. plans removal of the manager executable, marker-owned PATH change, manager cache, rollback binary, and manager state/log directories;
2. shows retained aliases, models, applications, profiles, extensions, and user files;
3. writes a final redacted uninstall receipt before deleting active state and retains no hidden background service;
4. does not remove VS Code, Ollama, models, aliases, extensions, or profiles.

Optional destructive flags are separate and non-implied:

- `--remove-aliases`
- `--remove-models`
- `--remove-vscode-extension`
- `--remove-vscode`
- `--remove-ollama`

Each flag requires an ownership check, dependency plan, explicit confirmation even with `--yes`, and post-removal verification. External applications can be removed only when state proves the manager installed that exact asset; conflicting or missing ownership emits `LAI-ASSET-001`.

## Dependency and failure behavior

- A source model cannot be removed while a retained alias depends on it.
- Shared applications are removed only after every manager-owned dependent asset is addressed.
- Uninstall never deletes an entire directory to remove one owned file unless the directory itself is manager-owned and contains no untracked entries.
- Backups are restored only when their digest and ownership metadata match the receipt.
- A partial uninstall leaves state sufficient to resume. Failure to persist that state stops further destructive work.
- Unsupported VS Code profile deletion is always a manual action and returns `manual_required` when it is the only remaining requested action.

