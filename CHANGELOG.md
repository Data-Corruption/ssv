# Changelog

example release below. (without commenting the headers)
IMPORTANT - Remove example before you add any real releases!

<!-- ## [v0.1.0] - 2025-09-01 -->

Minimal starter for Go CLI apps with an optional webserver daemon, changelog‑driven GitHub Actions CI/CD, and self‑updating installs.

Added
- CLI scaffold using urfave/cli v3 with common flags and subcommands.
- Daemon subcommand running an HTTP server (default :8080); installer provisions a systemd service.
- Shared atomic data/config directory backed by LMDB, safely used by both CLI and daemon.
- Structured, rotatable logging via stdx/xlog under the per-user data path.
- Changelog-driven release automation; daily lightweight version checks and an update command with opt-in notifications.
- Cross-platform installers:
  - Linux installer with optional version pinning.
  - Windows PowerShell (WSL) installer that bridges PATH and service management.
- Build scripts for reproducible, versioned artifacts.
- Apache-2.0 license and template documentation.

Notes
- Local builds use a placeholder version (vX.X.X) and skips update logic.