# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **The README and the GitHub page say OODA, and say what OODA is.** Both now open with
  the phases — **Observe → Orient → Decide → Act** — and explain that Decide is the
  orchestrator's own phase, so it never spends a worker lane. The four worker-facing
  pins (`observe`, `orient`, `act`, `verify`) are described as stage pins, and `verify`
  is named as the off-family review that OODA keeps inside Act, promoted to a stage of
  its own. The page's pipeline strip and its version strings move to v1.0.19, and the
  pipeline block lists the phases in order — observe, orient, decide, act — with `verify`
  shown as the gate inside act rather than a fifth phase after it. The routing card is
  renamed **Lane Priority by Stage** and says outright that its chains are lane orders, not
  the phase order. The pipeline cards are now the four phases in order — observe, orient,
  decide, act — with a card for Decide, the orchestrator's own phase, and the off-family
  review folded into Act as the gate it is.

## [Unreleased]

### Changed

- **The stage pins are no longer called "OODA stages".** The router has four worker
  stages — `observe`, `orient`, `act`, `verify` — and four OODA *phases* — Observe,
  Orient, Decide, Act. Calling the pins "OODA stages" is what let the stage list read
  as a phase list, so the wording is now "worker stage" everywhere it appeared:
  - the status header becomes **Worker Stage Routing Matrix**, the stage selector
    becomes **Worker Stage Matrix**, and the `--select` help, the `ooda_set_stage` MCP
    description, the plugin manifest, the MCP server description and the `lane` and
    `mcode-router` skills all say "worker stage"
  - the `ooda-auto` agent profile no longer calls a four-step list "the 4-phase OODA
    loop": it now runs observe → orient → decide → act, with the review as the gate on
    act
  - the `mcode-router` skill's tool reference gains a DECIDE row, so the phase and
    its four phases line up

  The context hook that injects the matrix now matches `/Stage Routing Matrix/,$p`
  instead of the full header, so it keeps working against both the old and the new
  binary. **Reinstall after upgrading** so the installed hook file is rewritten.

## [1.0.19] — 2026-11-18

### Changed

- **The file length cap is back at 256 lines.** Every `.go` source and test file
  is now inside the 256-line house law again, and `make line-cap` enforces it.
  The cap was raised to 320 in v1.0.14 because every affected package sat at
  the directory cap, so an oversized file could not be split; with density now
  counting modules, the files split instead:
  - `fleet`: `catalog.go` into `catalog.go` + `providers.go` + `safe_text.go`,
    `leases.go` into `leases.go` + `muse_probe.go`, `paths.go` into `paths.go` +
    `state_io.go`
  - `verify`: `gather_diff.go` into `gather_diff.go` + `git_state.go`,
    `snapshot.go` into `snapshot.go` + `fingerprint.go`, `hygiene.go` into
    `hygiene.go` + `repo_hygiene.go`
  - `worktree`: `stages.go` into `stages.go` + `stage_pins.go`
  - `dispatch`: `telemetry.go` into `telemetry.go` + `hud.go`
  - `cli`: `run_cli.go` into `run_cli.go` + `run_dispatch.go`, `report_fleet.go`
    into `report_fleet.go` + `report_usage.go`, `cli_test.go` into `cli_test.go` +
    `cli_review_test.go`
- **Directory density counts modules, not files.** The limit moves from 8 to 16
  — the next rung of the power-of-two ladder — and a `*_test.go` file no longer
  consumes a module slot. Test files are companions to a module, and counting
  them is what left packages unable to split.

## [1.0.18] — 2026-11-18

### Changed

- **The directory-density gate counts modules, not test files.** A test file is a
  companion to a module, so counting it against the eight-entry budget let test
  files eat the slots that let a package split an oversized file into its own.
  That is what forced the file-length cap from 256 to 320 in v1.0.14: every
  affected package was already at both the function and the directory cap, so
  the guards had to live beside the code they protect. With the count fixed, a
  package can split again.
- **The native review lives in its own module.** `internal/verify/native_review.go`
  holds the native envelope, the note that asks the native reviewer for its
  findings, and the mapping of the reviewer's own verdict onto the router's
  contract. `verdict.go` drops to 237 lines, the dispatch lane builders move to
  `internal/dispatch/build_lanes.go`, and every file this touches now stays
  inside the 256-line house law.

## [1.0.17] — 2026-11-18

### Fixed

- **The mcode native review now returns a verdict the router can read.** The native
  review path asked the CLI for plain text and then hunted for the router's
  `VERDICT: ACCEPT|REJECT` line in it. The native reviewer does not emit that line —
  it returns its own structured result — so the verdict came back `UNKNOWN` and the
  review rotated off-family to another lane. Every native review therefore ran on the
  pinned lane and was thrown away, and a `verify` pinned to `mcode` never actually
  reviewed anything on `mcode`. The native path now asks for JSON and maps the
  reviewer's own verdict: `pass` accepts, `needs_changes` rejects, and the findings
  come back as `[P*]` lines with their path and line number. A `pass` that still
  carries findings is a contradiction and is refused rather than accepted.

## [1.0.16] — 2026-11-18

### Added

- **A `fusion` preset, and presets that can name a model per stage.** The preset
  table moved out of `stages.go` into `auto_select.go`, next to the rest of the
  automatic selection, and it grew a `fusion` preset: observe and orient on
  `minimax/MiniMax-M3.1-Flash`, act on `devin-fast` with `swe-2-flash-high`, and
  verify on `mcode` with `M3.1-Flash` at max effort. A preset may now pin a model
  per stage, not only a lane, and the stage selector and the `lane` skill list
  the new preset. Moving the table also takes `stages.go` from 319 lines to 303.

### Fixed

- **A pinned model now survives a same-family failover.** When a pinned stage fell
  over to another lane, the router scrubbed the model *and* the effort, so the
  fallback lane silently ran its own default model: pin `act` to
  `devin-fast`/`swe-2-high` and lose `devin-fast` to a cooldown, and the job
  landed on `devin-pro` running `swe-2-flash-high` — a different model family
  than the one that was pinned. A model whose family matches the fallback lane
  is now kept (`swe-2-high` stays `swe-2-high` across the devin lanes); a model
  the fallback lane cannot serve is still scrubbed, so no lane ever receives a
  foreign model id. The same rule now applies to the verify stage.
- **Models whose reasoning is a variant no longer get `--effort`.** `MiniMax-M3`
  exposes reasoning as a thinking variant rather than an effort level, so the
  CLI rejects `--effort` for it ("does not support reasoning effort selection",
  or "Invalid model thinking.effort" from the API). The router passed an effort
  anyway and read the rejection as a mismatch, silently pivoting the stage to a
  different model family. The router now omits the effort for models that cannot
  take one and reports `default` instead of a fictional effort level.

## [1.0.15] — 2026-11-17

### Security

- **A worker can no longer steer the router through the files it can write.**
  A second harden pass over the router found eight more trust-boundary
  defects. All are now closed, each with a refusal test that fails against the
  unfixed code:
  - The lane decides which binary a worker runs. The mutable quota file named
    it, so a forged `cli` entry could point a worker at any executable on
    `PATH`. A lane the router does not know is now refused outright.
  - The paid-lane gate read the quota file's `tier`, so a forged tier could
    make a credits lane look free and get it dispatched without `--lane`. The
    gate now reads the static provider spec.
  - A stage pin — in the workspace file or in the quota file — was trusted as
    written. Both are re-validated on read now: known lanes only, no paid or
    PAYG lane, no reserved model, and no expired pin.
  - A numeric timeout had no upper bound, so a huge value overflowed the
    duration arithmetic and turned into an instant or endless run. It is capped
    now, and a sub-second value rounds up instead of truncating to zero.
  - A worker export could report negative tokens or cost, walking the quota
    counter backwards, or a runaway figure that broke the next quota write.
    Both are clamped now.
  - A Gemini or orchestrator model named in the devin config could reach the
    worker argv, past the reserved-model guard.
  - The review prompt inlined the diff with no separation from its own
    instructions, so a crafted file could forge a verdict. The patch is fenced
    now, and the verdict contract and the finding tags inside it are
    neutralised — in the inline prompt and in the native review path alike.
  - Text that came out of mutable state reached the terminal unsanitised, so a
    forged worker marker or a forged quota field could repaint the screen. It
    is stripped of escape sequences and control characters everywhere it is
    printed.

### Added

- `qa/acceptance` grew to twenty-seven black-box refusal tests, all of them
  driving the public API only, so a guard cannot be bypassed by reaching into
  package internals.

## [1.0.14] — 2026-11-17

### Security

- **A read-only worker can no longer quietly change what the router believes.**
  Eight trust-boundary defects were confirmed by an off-family review and are
  now closed. Each one has a refusal test that fails against the unfixed code:
  - A `--task` run against a directory that is not a git repository refused to
    diff or review its own change. It now refuses outright, unless
    `--no-review` is given.
  - A worker that had drifted from its baseline had its output printed before
    the drift was noticed. The output is now withheld until the drift check
    passes.
  - A worker inherited every provider's credentials from the environment. It
    now carries only the credentials of the provider it is running as.
  - A worker's prompt sat in the process argument list, where any local user
    could read it from `/proc`. The mcode lane now carries the prompt on
    standard input instead, and writes no prompt file at all.
  - The telemetry export was read without a size bound. The read is now capped,
    and an oversized export is refused rather than silently truncated.
  - The shared state roots were not bound to the current user, so another local
    account could pre-create them and swap the router's cache and slots
    underneath it. They are now uid-scoped, and a root this user does not own,
    or one another user can write to, is refused outright.
  - A cached stage result was served without checking that it still matched the
    content it claimed. Cache entries now carry a digest, and a mismatch
    evicts the entry.
  - A diff was scanned for credentials only on its added lines. Context and
    removed lines travel to the reviewer too, so they are scanned as well, and
    six more credential shapes are recognised.
- **Git state outside the worktree is now watched.** A read-only worker could
  plant a git hook or rewrite `.git/config` — neither shows up in `git status` —
  and the hook still ran on the user's next commit. The snapshot now
  fingerprints the real git directory (the shared one, for a linked worktree),
  the per-worktree configuration, the hook directories including a configured
  `core.hooksPath`, and the files hidden by `.gitignore`. The hook walk is
  bounded in depth, in file count and in reach, because `core.hooksPath` is
  worker-controlled and could otherwise point at an arbitrary tree.
- The state root check refuses a root another user can write to rather than
  tightening its mode, so a path the user chose is never silently
  re-permissioned.

### Changed

- The file length cap moved from 256 to 320 lines, because every affected
  package was already at both the function and the directory cap, so the guards
  had to live beside the code they protect rather than in files of their own.

### Added

- `qa/acceptance` holds fifteen black-box refusal tests that drive the public
  API only, so a guard cannot be bypassed by reaching into package internals.

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

[1.0.19]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.19
[1.0.18]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.18
[1.0.17]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.17
[1.0.16]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.16
[1.0.15]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.15
[1.0.14]: https://github.com/UberMetroid/mcode-router/releases/tag/v1.0.14
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
