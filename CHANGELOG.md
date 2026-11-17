# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.5] — 2026-11-17

### Changed

- **The status line no longer repeats the project name.** The worker segment used to be
  labelled with the project basename even when the worker was running inside the session's
  own workspace, where MiniMax Code already shows the directory in its status bar. The
  label is now only added when the worker targets a different project than the session.

## [1.0.4] — 2026-11-17

### Added

- **MiniMax Code status line indicator.** `mcode-router --statusline` renders the active
  worker — lane, model, effort, phase and tokens-per-second — as a single status bar
  entry, so the router's worker traffic is visible next to MCode's own model and token
  quota instead of being confused with them. MiniMax Code passes its session context on
  stdin and renders the command's stdout; the indicator stays silent while no dispatch is
  running. `install.sh` prints the `tui:` block to merge into `~/.minimax/config.yaml`.

### Changed

- The `internal/theme` palette package is now `internal/ui` and owns the terminal
  surfaces: the ANSI palette and the status line renderer. The live telemetry reader
  moved there from `internal/cli`.

## [1.0.3] — 2026-11-17

### Changed

- **The stage routing matrix now names the model.** Every unpinned stage used to render as
  `auto (quota-aware)`, which stated the policy but not the pick, so the MiniMax Code
  context hook could not show which worker model a stage would actually use. Unpinned
  stages now resolve their lane, model and effort against live quota state and render
  them — for example `act  auto  muse (muse-ember-1.0/max)`. Pinned stages are unchanged.

## [1.0.2] — 2026-11-17

### Added

- **Live worker telemetry.** The dispatch layer already recorded the model, effort, phase
  and tokens-per-second of every running worker, but nothing rendered it after the HUD was
  removed with the agy split. `mcode-router --live` now prints the active workers and the
  last completed run, and the new `ooda_live` MCP tool exposes the same view inside MiniMax
  Code. The `--status` output also carries a compact live section, which the
  `UserPromptSubmit` context hook surfaces on every prompt.

### Changed

- The MCP server now advertises eight tools; the README, landing page and the
  `mcode-router` skill list `ooda_live`.

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

[1.0.5]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.5
[1.0.4]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.4
[1.0.3]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.3
[1.0.2]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.2
[1.0.1]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.1
[1.0.0]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.0
