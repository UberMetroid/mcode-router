# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.1] — 2026-11-17

### Added

- **`--cwd` CLI flag and `cwd` MCP parameter.** The observe, orient and act stages
  resolve their working directory from the process CWD, so an MCP caller could not
  target a specific project: the MCP server runs with the session's start folder, not
  the repo being worked on. `ooda_recon`, `ooda_orient` and `ooda_task` now accept a
  `cwd` argument, and the matching CLI verbs accept `--cwd DIR`. The flag is wired
  into `--task`, `--recon`, `--orient`, `--set-stage`, `--status` and manifest and
  cleanup mode. A `--cwd` that is not an existing directory is rejected before any
  worker is dispatched.

### Changed

- README and the `mcode-router` skill document the `cwd` parameter and explain that it
  should always be passed.

## [1.0.0] — 2026-11-17

First public release of `mcode-router`: a zero-dependency Go OODA multi-agent router for
MiniMax Code.

### Added

- **MCP server** (`bin/mcode-router-mcp`) exposing seven tools that map 1:1 to the OODA
  stages: `ooda_recon`, `ooda_orient`, `ooda_task`, `ooda_review`, `ooda_status`,
  `ooda_usage`, `ooda_set_stage`.
- **MiniMax Code plugin package** under `plugins/mcode-router/`: host manifest, raster
  icon, MCP server declaration, context hooks, three skills (`mcode-router`, `lane`,
  `harden`) and eight agent profiles (`ooda-auto`, `ooda-conserve`, `ooda-deep`,
  `ooda-devin`, `ooda-grok`, `ooda-mcode`, `ooda-mistral`, `ooda-muse`).
- **Automatic routing**: `SessionStart` and `UserPromptSubmit` hooks inject the live fleet
  state and the stage routing matrix into every session, so lane, model and effort are
  chosen without user input.
- **Installer** `mcode-router __mcode install|uninstall|status`, wired into `install.sh`
  and `uninstall.sh`, with atomic writes, idempotent merges into `~/.minimax/mcp.json` and
  `~/.minimax/hooks.json`, and mode-600 secrets.
- **Quota-aware fleet** of seven lanes (`devin-fusion`, `devin-pro`, `devin-fast`, `muse`,
  `mcode`, `grok`, `mistral`) with 5h/7d/30d reset windows, off-peak tariffs, EWMA health
  scoring, soft-pin failover, and off-family verification.
- **House-law gates** (`make portable`, `line-cap`, `density`, `vocab`, `shape`,
  `mcode-plugin`, `test`, `dogfood`), including a MiniMax Code packaging gate that
  validates the manifest, icon, referenced files, and rejects symlinks, executable-bit
  files, and secrets inside the package.
- **Zero-Go web installer** at `docs/install.sh` with SHA-256 verification of the downloaded
  static binary.

[1.0.1]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.1
[1.0.0]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.0
