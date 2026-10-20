# Architecture and domain contract

Contract version: **1.0.0**

## Product boundary

`local-ai` is a user-local lifecycle manager for a private Ollama-backed VS Code **Local AI** profile. It owns discovery, planning, setup, verification, diagnosis, safe repair, model lifecycle, explicit update/rollback, and ownership-aware uninstall. The guided TUI, ordinary CLI, unattended mode, and JSON mode are adapters over the same domain operations.

The manager is not part of the dotfiles bootstrap. It must not call or modify `bootstrap.sh`, `setup.py`, `setup_termux.sh`, their selections, or their state. Existing full setup scripts remain unchanged until the later migration todo.

## Invariants

1. Domain decisions are deterministic and do not depend on terminal presentation.
2. Host changes occur only through platform adapters and only after a typed plan identifies consent and privilege requirements.
3. Every mutation follows `Check → Plan → Apply → Verify`; reversible mutations also define `Undo`.
4. A successful receipt is written only after verification. Interrupted work resumes from verified receipts, not optimistic intent.
5. `--dry-run` may discover and perform live read-only checks, but cannot download, install, pull, create, delete, edit PATH, restart a process/service, or change a profile.
6. `--yes` accepts documented non-destructive defaults only. It cannot approve workspace trust, agent tool execution, organization-policy exceptions, elevation, external-app removal, or model deletion.
7. Runtime evidence is authoritative over catalog claims. The exact `local-ai-...` alias must report at least 65,536 allocated context tokens and a structured tool call.
8. Detected software is preferred over reinstalling it. Missing PATH entries alone do not prove an application is absent.
9. Existing assets are never silently adopted. Ownership is explicit and follows [asset ownership rules](asset-ownership.md).
10. No domain, state, log, event, result, or report may contain repository content, prompts, model responses, chat content, secrets, tokens, arbitrary environment values, literal usernames/hostnames, device serials, or network identifiers.

## Planned package boundaries

| Domain owner | Responsibility | Forbidden responsibility |
|---|---|---|
| `internal/app` | Command policies, operation orchestration, typed facts, issues, results, and lifecycle state machine | Direct filesystem, process, network, or terminal access |
| `internal/state` | Versioned journal, atomic persistence, migrations, receipt lookup, redacted logs, rollback metadata | Inferring ownership or declaring verification success |
| `internal/platform` | Interfaces for host paths, files, commands, processes, privileges, clipboard, PATH, and discovery | Product policy or model recommendation |
| `internal/platform/{windows,macos,linux}` | Narrow native implementations | Cross-platform orchestration |
| `internal/hardware` | Privacy-allowlisted CPU, RAM, GPU, acceleration, and storage facts | Serial numbers, machine identity, telemetry, or invented VRAM |
| `internal/catalog` | Catalog validation, eligibility, deterministic scoring, exclusions, and explanations | Network mutation or unreviewed catalog updates |
| `internal/ollama` | Registry/API clients, bounded GGUF metadata, service behavior, acquisition, aliases, context/offload and tool verification | Selecting cloud fallback or changing GPU drivers |
| `internal/vscode` | Supported CLI discovery, version/profile/extension checks, open behavior, and UI handoff | Editing undocumented profile storage, workspace trust, or approvals |
| `internal/copilot` | Official Copilot CLI lifecycle, manager-owned offline launcher, and typed CLI readiness/open behavior | Model-generated tool execution, approval bypass, or undocumented VS Code storage edits |
| `internal/install` | Official installers, HTTPS-only redirect policy, integrity hooks, progress, and rerun safety | Silent elevation or disabling OS protections |
| `internal/update` | Verified release selection, atomic replacement, one rollback copy | Background or silent updating |
| `internal/cli` | Flags, config precedence, subcommands, event writer, and exit mapping | Business decisions |
| `internal/ui` | Accessible TUI and plain-prompt rendering | Host mutations or duplicated decision logic |

## Aggregate roots and value objects

| Type | Contract |
|---|---|
| `Operation` | One command invocation with immutable effective configuration and optional transaction identity. |
| `Transaction` | Durable state for one mutating operation. It contains step receipts, managed assets, issues, manual actions, and rollback metadata as defined by [transaction state v1](../../schemas/local-ai-manager/transaction-state-v1.schema.json). |
| `Step` | A stable step ID, domain owner, prerequisites, produced facts, risk class, consent requirement, and `Check/Plan/Apply/Verify/Undo` behaviors. |
| `StepReceipt` | Durable evidence for completed phases, linked mutations, verification evidence, and any issue. A receipt cannot contain raw command output. |
| `HostCapabilities` | Allowlisted normalized OS, architecture, memory, accelerator, desktop/session, and storage facts with source and confidence. Unknown is a valid value and must not be guessed. |
| `Catalog` | Reviewed immutable model entries conforming to [model catalog v1](../../schemas/local-ai-manager/model-catalog-v1.schema.json). |
| `ModelSelection` | Eligible catalog ID, score components, disqualifiers, selected fallback, and unresolved unknowns. |
| `ModelInstallation` | Exact source model, manager alias, acquisition origin, local metadata, allocated context, offload evidence, and tool-verification state. |
| `VSCodeIntegration` | VS Code discovery/version, Local AI profile readiness, official extension state, and unavoidable UI actions. |
| `ManagedAsset` | Stable asset identity, origin, ownership, locator, uninstall policy, and optional backup/rollback locator. |
| `Issue` | Stable issue code, severity, affected domain/component, human summary, structured remediation, and optional safe automated repair ID. |
| `OperationResult` | Outcome, exit code, issues, component states, pending manual actions, and transaction identity. |

## Transaction state machine

Mutating commands use these states:

```text
planned → running → verifying → succeeded
                  ↘ awaiting_manual
         ↘ interrupted → running
         ↘ failed → rolling_back → rolled_back | rollback_failed
planned/running/awaiting_manual/interrupted/failed → archived (only by --restart)
```

- `succeeded` requires all required verification steps.
- `awaiting_manual` is a completed machine operation with explicit UI or policy actions remaining; it maps to exit code 3.
- `interrupted` is resumable and does not imply failure or rollback.
- A failed transaction may retain useful verified assets. Rollback removes only mutations with successful apply receipts and an applicable undo contract.
- `--resume` accepts only a compatible schema major version and command family. `--restart` archives the prior transaction without deleting its assets.

## Step contract

Each step declares:

- stable `step_id` and domain owner;
- prerequisite fact IDs and produced fact IDs;
- mutation risk: `read_only`, `user_local`, `large_download`, `privileged`, `destructive`, or `external_app_restart`;
- consent and privilege requirements;
- idempotent `Check`, deterministic `Plan`, bounded `Apply`, and independent `Verify`;
- undo behavior or an explicit `not_reversible` reason;
- issue codes that can be emitted;
- asset records created or changed.

`Check` may skip an already verified step only when current evidence still satisfies the same postcondition. File existence or a prior attempt alone is insufficient.

## Stable command ownership

| Command | Primary domain owner | Transactional |
|---|---|---|
| `setup` | `app` orchestration | Yes |
| `status` | `app` read model | No |
| `doctor` | `app` diagnosis policy | No |
| `repair` | `app` repair policy | Yes |
| `models list`, `models recommend` | `catalog` | No |
| `models install`, `models remove` | `ollama` | Yes |
| `vscode configure` | `vscode` | Yes |
| `vscode open` | `vscode` | No, except an explicit supported profile-creation plan |
| `copilot configure` | `copilot` | Yes |
| `copilot open` | `copilot` | No |
| `verify` | `app` verification policy | No |
| `update`, `update --rollback` | `update` | Yes |
| `uninstall` | `install` plus ownership policy | Yes |
| `report` | `app` privacy allowlist | No |

## Configuration and output

- Configuration files conform to [config v1](../../schemas/local-ai-manager/config-v1.schema.json).
- Precedence is platform-aware defaults, then config file, then the explicit environment allowlist, then command line.
- The environment allowlist is `LOCAL_AI_CONFIG`, `LOCAL_AI_OLLAMA_ENDPOINT`, `LOCAL_AI_VERIFY_ATTESTATION`, and `NO_COLOR`. No arbitrary environment values are copied into configuration, state, events, results, or reports.
- JSON mode emits one JSON object per line conforming to [event/result stream v1](../../schemas/local-ai-manager/event-stream-v1.schema.json). Exactly one final `result` record is required.
- Human output may change presentation, but stable issue codes, exit codes, command names, schema discriminators, and semantic outcomes follow [the issue and exit contract](issue-and-exit-codes.md).
