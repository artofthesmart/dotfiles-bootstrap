# Manager self-deployment

Contract version: **1.0.0**

The top-level `install.sh` and `install.ps1` are minimal user-local bootstraps for the future `local-ai` manager. They do not replace or invoke `scripts/setup-local-ai.sh` or `scripts/setup-local-ai.ps1`; after installing the manager they run `local-ai setup`.

## Integrity and platform behavior

- The bootstrap resolves GitHub's published stable release through an injectable HTTPS API base, selects the exact OS/architecture archive, downloads with time and size bounds, and requires an exact `SHA256SUMS` entry.
- SHA-256 is verified before archive parsing. ZIP and tar.gz readers reject absolute paths, `..` traversal, symlinks, devices, and duplicate or oversized manager executables.
- Linux and macOS install to `~/.local/bin/local-ai`. Windows installs to `%LOCALAPPDATA%\Programs\LocalAI\local-ai.exe`.
- If `LOCAL_AI_VERIFY_ATTESTATION=1` and `gh` is already installed, the bootstrap also runs `gh attestation verify`. It never installs `gh`, and checksum verification never depends on it.
- PATH changes are previewed and require consent. Only a clearly marked block is appended, the original file is backed up without overwriting an existing backup, and uninstall removes only that exact marker.

## Updates, rollback, and removal

Updates are explicit. The manager never performs background or silent checks. A candidate is downloaded, checksum-verified, safely extracted beside the destination, self-checked, and swapped on the same filesystem. The previous executable is retained as a versioned, digest-verified rollback. A failed post-swap start restores it automatically; `local-ai update --rollback` performs the same verified restoration explicitly.

Deployment receipts persist each boundary so interrupted install, update, rollback, or uninstall work can be diagnosed and resumed. Plain uninstall removes the manager executable, recorded rollback/cache/state files, and the exact manager-owned PATH marker. Applications, profiles, extensions, aliases, models, and user files are retained unless their separate explicit removal flags and ownership checks permit removal.

## Friend-preview warning

Friend-preview binaries are currently unsigned and may trigger Gatekeeper or SmartScreen. Verify the release URL and SHA-256 receipt, use the operating system's normal review prompts, and report unexpected warnings. Do **not** disable Gatekeeper, SmartScreen, Defender, antivirus, or organization policy.
