# Stable issue-code and exit-code contract

Contract version: **1.0.0**

Issue codes identify a condition; exit codes identify the overall command result. A command may emit multiple issues but exactly one exit code. Codes are stable API: meanings are never reassigned, and deprecated values remain reserved.

## Issue object rules

Every issue contains:

- `code`: one stable code below;
- `severity`: `info`, `warning`, or `error`;
- `domain` and optional `component`;
- a concise `summary` with no secrets or machine identity;
- at least one structured remediation when action is possible;
- optional `repair_id` only when `local-ai repair` implements an idempotent documented repair;
- optional allowlisted `evidence` values, never raw command output, repository paths/content, prompts/responses, environment dumps, tokens, host/user names, serials, or network identifiers.

Remediation kinds are `automatic`, `command`, `ui`, `policy`, `documentation`, or `retry`. Commands must use placeholders rather than embedding usernames, secrets, or unredacted user paths.

## Stable issue codes

| Code | Meaning | Default severity | Default exit |
|---|---|---:|---:|
| `LAI-HOST-001` | Unsupported operating system | Error | 10 |
| `LAI-HOST-002` | Unsupported architecture | Error | 10 |
| `LAI-HOST-003` | Desktop/session requirement not met | Error | 10 |
| `LAI-HOST-004` | Host version is below the supported minimum | Error | 10 |
| `LAI-PREREQ-001` | Required application is missing | Error | 11 |
| `LAI-PREREQ-002` | Required application version is too old | Error | 11 |
| `LAI-PREREQ-003` | Application exists but cannot be executed or queried | Error | 11 |
| `LAI-POLICY-001` | Privilege or organization policy blocked an operation | Error | 12 |
| `LAI-POLICY-002` | Required consent was not granted | Info | 2 |
| `LAI-POLICY-003` | A protected UI approval must be completed manually | Warning | 3 |
| `LAI-NET-001` | Network endpoint was unavailable or timed out | Error | 20 |
| `LAI-NET-002` | HTTPS redirect policy was violated | Error | 21 |
| `LAI-NET-003` | Download was incomplete or malformed | Error | 20 |
| `LAI-INTEGRITY-001` | Checksum did not match | Error | 21 |
| `LAI-INTEGRITY-002` | Publisher signature or attestation was invalid | Error | 21 |
| `LAI-INTEGRITY-003` | Release or registry metadata was inconsistent | Error | 21 |
| `LAI-CAPACITY-001` | Insufficient free storage | Error | 22 |
| `LAI-CAPACITY-002` | Conservative memory budget does not fit | Error | 22 |
| `LAI-CAPACITY-003` | Required capacity fact is unknown | Warning | 22 |
| `LAI-MODEL-001` | Catalog entry is unknown, blocked, or ineligible | Error | 30 |
| `LAI-MODEL-002` | Exact source context is below 65,536 | Error | 30 |
| `LAI-MODEL-003` | Model is remote/cloud-routed | Error | 30 |
| `LAI-MODEL-004` | Model does not advertise required tool capability | Error | 30 |
| `LAI-MODEL-005` | Model acquisition/import failed | Error | 31 |
| `LAI-MODEL-006` | Alias creation or dependency resolution failed | Error | 31 |
| `LAI-RUNTIME-001` | Ollama API is unavailable or invalid | Error | 32 |
| `LAI-RUNTIME-002` | Structured tool-call verification failed | Error | 32 |
| `LAI-RUNTIME-003` | Allocated context is below 65,536 | Error | 32 |
| `LAI-RUNTIME-004` | Runtime offload or loaded-model evidence is unavailable | Warning | 32 |
| `LAI-RUNTIME-005` | Existing Ollama process requires a consented restart/configuration change | Warning | 3 |
| `LAI-VSCODE-001` | VS Code profile is unavailable or not ready | Error | 40 |
| `LAI-VSCODE-002` | Official Ollama extension installation/verification failed | Error | 40 |
| `LAI-VSCODE-003` | Exact local alias is not discoverable/selectable | Warning | 3 |
| `LAI-VSCODE-004` | Workspace trust or Restricted Mode blocks local behavior | Warning | 3 |
| `LAI-VSCODE-005` | More than 128 tools require Configure Tools | Warning | 3 |
| `LAI-VSCODE-006` | BYOK or utility-model selection requires supported UI action | Warning | 3 |
| `LAI-STATE-001` | State file is unreadable, invalid, or corrupt | Error | 50 |
| `LAI-STATE-002` | State schema is unsupported or migration failed | Error | 50 |
| `LAI-STATE-003` | Transaction is incompatible with resume | Error | 50 |
| `LAI-STATE-004` | Atomic state write or rollback failed | Error | 50 |
| `LAI-CONFIG-001` | Configuration file is invalid | Error | 60 |
| `LAI-CONFIG-002` | Configuration conflicts with command-line values or policy | Error | 60 |
| `LAI-ASSET-001` | Asset ownership is unknown or conflicts with state | Error | 50 |
| `LAI-ASSET-002` | Destructive removal needs an explicit flag/confirmation | Warning | 3 |
| `LAI-UPDATE-001` | No compatible verified update asset is available | Error | 21 |
| `LAI-UPDATE-002` | Updated binary failed self-check; rollback attempted | Error | 70 |
| `LAI-INTERNAL-001` | Unexpected internal invariant failure | Error | 70 |

New codes may be appended within an existing category. Implementations must not derive behavior by parsing the human summary.

## Stable exit codes

| Exit | Symbolic name | Meaning |
|---:|---|---|
| 0 | `success` | Requested operation completed and all required verification passed. Warnings may remain. |
| 2 | `cancelled` | User cancelled or declined required consent before completion. |
| 3 | `manual_required` | Machine work completed as far as safely possible, but one or more explicit manual actions remain. |
| 10 | `unsupported_host` | OS, architecture, host version, or desktop topology is unsupported. |
| 11 | `prerequisite_failure` | A required application/runtime is missing, too old, or unusable. |
| 12 | `policy_or_permission_failure` | Privilege, organization policy, or protected OS control blocked the operation. |
| 20 | `network_failure` | Network, timeout, or incomplete-transfer failure without evidence of integrity compromise. |
| 21 | `integrity_failure` | HTTPS policy, checksum, signature, attestation, release, or registry integrity failed. |
| 22 | `insufficient_capacity` | Disk, memory, or required capacity evidence is insufficient. |
| 30 | `model_incompatible` | Model/catalog/context/tool/locality contract is incompatible before acquisition. |
| 31 | `model_acquisition_failure` | Pull, import, alias creation, or model dependency operation failed. |
| 32 | `runtime_verification_failure` | Ollama availability, actual context, structured tool call, or runtime evidence failed. |
| 40 | `vscode_integration_failure` | Supported VS Code CLI/profile/extension operation failed. |
| 50 | `state_or_transaction_failure` | State, ownership, resume, persistence, migration, or rollback metadata failed. |
| 60 | `configuration_failure` | Config syntax, schema, precedence, or policy validation failed. |
| 70 | `internal_failure` | Unexpected invariant, self-update, or unrecoverable internal failure. |

## Exit selection

1. Cancellation before a prior failure returns 2.
2. If no error exists but required manual issues remain, return 3.
3. If errors exist, choose the exit for the blocking issue nearest the root cause, not the last cleanup error.
4. An integrity failure outranks network failure; state write/rollback failure outranks the operation that triggered it.
5. `doctor` returns 0 when no error-severity issue exists, 3 when only manual/warning issues require action, and the mapped nonzero error exit otherwise.
6. JSON `result.exit_code`, process exit status, and human summary must agree.

