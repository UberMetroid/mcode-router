# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.13] — 2026-11-17

### Changed

- **The status bar shows every running worker.** The custom status line was
  capped at three rows, so a fourth parallel worker was invisible while it ran.
  The cap is now six rows, which covers the four parallel workers the Devin
  SWE Fast lane allows.

## [1.0.12] — 2026-11-17

### Fixed

- **The fastest workers now show their colour.** MiniMax Code filters the custom
  status line through its own SGR parser, and it keeps only reset, default,
  standard and 256-colour parameters — it drops any other sequence whole. The
  top four ramp steps were bold (`1;38;5;47` through `1;38;5;50`), so every
  worker running above 150 tokens per second rendered its rate with **no colour
  at all**: the quickest workers looked plain while merely busy ones looked
  green. The ramp is now plain 256-colour throughout, running from deep green
  through neon green and mint to white, and the model name is coloured with a
  plain foreground too.
- The model name on the worker row lost its accent colour for the same reason.

### Added

- A regression test mirrors MiniMax Code's SGR filter and fails if the status
  line ever emits a colour the host would strip.

## [1.0.11] — 2026-11-17

### Changed

- **The worker row is now just the rate and the model.** It no longer carries the
  lane prefix, the project directory, the phase or the elapsed time — a worker
  showed as `⚡ ~181 tok/s · devin-fast/swe-2-flash-max/max · stream · 24s ·
  mcode-act-demo`, and now shows as `⚡ ~181 tok/s · swe-2-flash-max`. The lane is
  implied by the model, and the phase and clock were noise on a glanceable row.
- **Eight colour steps instead of four.** The rate now grades across a deeper
  green ramp — deep green, green, pure green, then bold mint — with thresholds at
  15, 40, 80, 150, 300, 500 and 750 tokens per second. A fast worker is now
  distinguishable from a merely busy one.

## [1.0.10] — 2026-11-17

### Changed

- **The worker row leads with the live number, coloured by throughput.** The row
  previously opened with a literal `###` placeholder. It now shows the worker's
  token rate itself — `⚡ ~181 tok/s · muse/muse-ember-1.0/max · stream · 24s` —
  and the rate is coloured by speed: muted while the worker is idle, then
  progressively brighter green as throughput climbs, with the lane name in the
  theme's accent colour.
- **The rate uses MiniMax Code's own lightning-bolt format.** The `⚡` marker and
  the `⚡ ~181 tok/s` layout match the token rate MCode shows for its session
  model, so the two rates on screen read the same way.

### Fixed

- The elapsed time on the worker row can no longer render as `-0s` when the
  clock rounds a fraction below zero.

## [1.0.9] — 2026-11-17

### Added

- **Live token rate for the Devin lanes.** The Devin CLI printed its whole answer in
  one burst at the end of a run, so the worker row sat at `~0 tok/s` for the entire
  dispatch and the phase never left `wait`. The Devin lanes now run with
  `--verbosity 3`, which streams the model text as it is produced, and the stream
  reader samples that text instead of waiting for a line break the Devin CLI never
  emits. A Devin worker now reports real throughput — `### tok/s ~71 ·
  devin-fast/swe-2-flash-max/max · stream` — and its phase moves to `stream` as soon
  as the model writes.

### Changed

- The Devin worker's output now carries the model's intermediate messages alongside
  its final answer, because the verbose stream is what the router reads. The final
  answer is unchanged and still ends the output.

## [1.0.8] — 2026-11-17

### Changed

- **The worker indicator moved to its own row.** MiniMax Code now renders the
  router's worker as a block below its own status line, so worker traffic is a
  separate row instead of another item in the session status. The
  `tui.customStatusLine.display` setting moves from `inline` to `block`, and
  `install.sh` prints the updated snippet.
- **The worker row leads with its own token rate.** The row starts with
  `### tok/s` and then names the worker model — `### tok/s ~181 ·
  muse/muse-ember-1.0/max · stream · 24s` — so the rate on that row can never
  be read as MiniMax Code's session telemetry.

## [1.0.7] — 2026-11-17

### Added

- **Version drift fails CI.** `make release-check` now runs on every push to
  `main` and on every pull request. It compares the CLI version constant, the
  plugin manifest version and the changelog release link, and fails the build
  when they disagree — the drift that shipped the v1.0.5 tag with a binary
  still reporting v1.0.4.

### Changed

- The release workflow runs the same check before it cross-compiles and
  publishes, so a tag whose version strings disagree never reaches a download.
- `RELEASE.md` now requires the changelog prose and the version bump to land in
  one commit, because a commit that names an unreleased version is a red build.

## [1.0.6] — 2026-11-17

### Added

- **One-command version bump.** `make bump-version VERSION=1.0.6` rewrites the CLI
  version constant, the MiniMax Code plugin manifest and the changelog release
  heading in a single pass, so the three places that carry the version can no
  longer drift apart. The command refuses a version that is not
  `major.minor.patch`, is idempotent, and re-reads every file after writing, so a
  half-applied bump fails loudly instead of shipping.
- **Release verification.** `make release-check` prints the version found in each
  release file and exits non-zero when they disagree, which catches a manifest
  that was edited without the CLI constant.
- **Release checklist.** `RELEASE.md` documents the full release path — prepare,
  bump, verify, gate, tag, publish and refresh the local install — including the
  ordering trap that made the v1.0.5 tag ship a stale version string.

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

[1.0.13]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.13
[1.0.12]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.12
[1.0.11]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.11
[1.0.10]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.10
[1.0.9]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.9
[1.0.8]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.8
[1.0.7]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.7
[1.0.6]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.6
[1.0.5]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.5
[1.0.4]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.4
[1.0.3]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.3
[1.0.2]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.2
[1.0.1]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.1
[1.0.0]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.0
