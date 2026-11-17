# mcode-router

**Static Go OODA multi-agent router, quota-aware scheduler, and zero-trust cross-model verifier for MiniMax Code.**

`UberMetroid/mcode-router` is a zero-dependency Go 1.22+ (`CGO_ENABLED=0`, zero external
`require`s) engine that plugs into MiniMax Code through an MCP server. It splits a request
into **observe → orient → act → verify**, picks the **lane, model and effort** for each stage
from live quota windows and task capability, dispatches work across the local agent fleet,
and verifies every diff with an **off-family reviewer**.

Nothing asks you to pick a model. The `SessionStart` and `UserPromptSubmit` context hooks
inject the live fleet state and the stage routing matrix into every session, so routing is
informed before you even ask.

- **Site & web installer:** <https://ubermetroid.github.io/mcode-router/>
- **Releases & static binaries (`SHA256SUMS`):** <https://github.com/UberMetroid/mcode-router/releases/latest>

## Quick Install (Zero Go Required)

```bash
curl -fsSL https://ubermetroid.github.io/mcode-router/install.sh | bash
```

Installs or updates the project in `${XDG_DATA_HOME:-~/.local/share}/mcode-router`
(override with `MCODE_ROUTER_HOME`). If Go 1.22+ is on `PATH`, it builds `bin/mcode-router`
locally with `CGO_ENABLED=0`; otherwise it downloads the matching static GitHub Release
binary (`linux`/`darwin`, `amd64`/`arm64`) and verifies its SHA-256 digest against
`SHA256SUMS` before installing.

The installer wires five things into MiniMax Code:

| Artifact | Destination |
|---|---|
| CLI binary | `~/.local/bin/mcode-router` |
| Skills | `~/.minimax/skills/{mcode-router,lane,harden}/SKILL.md` |
| Agent profiles | `~/.minimax/agents/ooda-*/agent.md` |
| MCP server | `agent-ooda` → `mcode-router` entry in `~/.minimax/mcp.json` |
| Context hooks | `SessionStart` + `UserPromptSubmit` in `~/.minimax/hooks.json` |

## The Four OODA Stages

| Stage | Role | Default lane |
|---|---|---|
| `observe` | Read-only recon: exact paths, line numbers, findings | `auto` |
| `orient` | Root-cause analysis and a concrete plan | `auto` |
| `act` | Coding and worktree execution | `auto` |
| `verify` | Cross-model diff review, off-family | `auto` |

Every stage resolves through **CLI flag → repo-local pin → profile → global pin →
automatic**. Soft-pin failover moves down the lane priority chain on a `429` or cooldown
and scrubs the primary lane's model and effort overrides. `verify` always excludes the
authoring model family, even when pinned identically.

## The Fleet

| Lane | CLI | Effort control & defaults | Tier | Cycle / cap |
|---|---|---|---|---|
| `devin-fusion` | `devin` | `via-model`; family `fusion` | `credits` | opt-in only |
| `devin-pro` | `devin` | `via-model`; family `swe-2` | `unlimited` | `24h + 7d` |
| `devin-fast` | `devin` | `via-model`; family `swe-2-flash`, max 4 parallel | `unlimited` | `24h + 7d` |
| `muse` | `muse` | `--reasoning-effort (none..ultra)`; `muse-ember-1.0` on act, `muse-wonder-1.0` on deep | `capped` | `5h + 7d` |
| `mcode` | `mcode` | `--effort (low\|medium\|high\|xhigh\|max)`, max 4 parallel; primary reviewer | `high-cap` | `5h + 7d` |
| `grok` | `grok` | `--reasoning-effort (medium\|high\|xhigh)` | `capped` | `7d` rolling weekly |
| `mistral` | `vibe` | `via-model` (`glm-5-3[high]`, …) | `capped` | `30d` monthly, 0.5x off-peak |

`devin-fusion` is paid credits and never runs unattended or in review. `muse` is skipped
when a pay-as-you-go API key is active. MiniMax Code is the only supported host.

## MCP Tools

The MCP server (`mcode-router-mcp`) exposes eight tools, one per concern:

| Tool | Purpose |
|---|---|
| `ooda_recon` | Observe stage — read-only reconnaissance |
| `ooda_orient` | Orient stage — root cause and plan |
| `ooda_task` | Act stage — implementation in an isolated worktree |
| `ooda_review` | Verify stage — off-family diff review |
| `ooda_status` | Live fleet table: lane, tier, model, reset windows |
| `ooda_usage` | Token and cost telemetry per lane |
| `ooda_live` | Live worker telemetry: model, effort, phase, tok/s of running dispatches |
| `ooda_set_stage` | Pin or reset a stage's lane binding |

Each maps 1:1 to the matching CLI verb, so `lane`, `model`, `effort`, `profile`,
`complexity`, `files`, `skill`, `cwd` and `timeout` overrides all apply.

`cwd` sets the directory the stage runs in. Without it a stage runs in the working
directory of the process that started the MCP server, which is rarely the project
you want — always pass `cwd` to `ooda_recon`, `ooda_orient` and `ooda_task`.

## Live Worker Indicator

`mcode-router --live` prints the model, effort, phase and tokens-per-second of every
dispatch running right now. The same view can live in the MiniMax Code status bar, on its
own row below MCode's own model and token quota, by pointing a custom status line at the
router:

```yaml
tui:
  statusLine:
    - current-dir
    - session-title
    - git-branch
    - model-with-reasoning
    - context-window
    - token-quota
    - context-remaining
    - custom-command
  customStatusLine:
    command: mcode-router --statusline
    display: block
    position: below
    colorMode: ansi
    maxLines: 3
    timeoutMs: 2000
    intervalSeconds: 2
```

MiniMax Code writes its session context to the command's stdin and renders its stdout.
With `display: block` the worker gets its own row under the status bar, leading with its
own token rate so it is never confused with MCode's session telemetry:

```
/tmp/project │ ✦ M3.1-Flash · Effort max │ Context 512K
### tok/s ~181 · muse/muse-ember-1.0/max · stream · 24s
```

The router entry is silent while nothing is dispatched, so the bar never adds noise to an
idle session. The `tui:` block is merged into `~/.minimax/config.yaml`; `install.sh` prints
the snippet on every run.

## CLI

```bash
mcode-router --status            # live fleet table with quota windows
mcode-router --usage             # token + cost telemetry per lane
mcode-router --live              # model, effort, phase and tok/s of running dispatches
mcode-router --recon "how does the build work?"
mcode-router --orient "why does the test fail?"
mcode-router --task "fix the failing test"
mcode-router --task "fix the failing test" --cwd /path/to/project
mcode-router --review            # off-family diff review
mcode-router --discover          # probe local CLIs for models and effort control
mcode-router --select            # interactive terminal lane selector
mcode-router --set-stage act=devin-pro --local
```

`--local` pins a stage for the current repository only; without it the pin is global and
lives in `~/.local/state/mcode-router/quota.json`.

## Plugin Package

`plugins/mcode-router/` is the distributable MiniMax Code package:

```
.minimax-plugin/plugin.json   # host manifest (name, icon, example queries, agents)
icon.png                      # 256×256 raster
mcode-router.mcp.json         # MCP server declaration
hooks/hooks.json              # SessionStart + UserPromptSubmit context hooks
skills/{mcode-router,lane,harden}/SKILL.md
agents/ooda-{auto,conserve,deep,devin,grok,mcode,mistral,muse}.md
config/quota.default.json     # packaged fleet + quota defaults
```

## Engine Guarantees

- **First-principles quota scheduling.** Prioritises `0.5x` off-peak tariffs and
  soonest-expiring bounded windows before they reset, applies EWMA health scoring to demote
  repeatedly failing lanes while recovering on clean runs, and spares the last active bounded
  lane by shifting routine coding to `devin-pro`, then `devin-fast`, then `mcode`.
- **Zero-trust verification.** No model family ever grades its own code. On cooldown, a
  nonzero exit, `SKIPPED`, `UNKNOWN`, or `[P0-3]` findings, review rotates off-family
  (`mcode → devin-fast → devin-pro → grok → mistral → muse`) or fails nonzero.
- **Worker guardrails.** Worker lanes reject `gemini*` and `orchestrator*` models outright.
- **Recursion guard.** Workers run with `MCODE_ROUTER_DEPTH>=1` and `ROUTER_DEPTH>=1`, which
  blocks nested dispatch.
- **Read-only enforcement.** Observe and orient stages snapshot the repository before and
  after and fail closed on any unexpected write.

## House Laws

The repository is machine-checked by `make all`:

| Gate | Rule |
|---|---|
| `make portable` | No third-party imports, no embedded credentials, no `sh -c`/`bash -c` |
| `make line-cap` | Every `.go` file is 16–256 lines, every line ≤ 128 columns |
| `make density` | At most 8 entries per directory under `cmd/`, `internal/`, `plugins/`, `qa/` |
| `make vocab` | No banned drawer filenames; identifiers ≤ 32 characters |
| `make shape` | ≤ 8 function declarations per file, ≤ 8 params, ≤ 64 body lines, ≤ 4 nesting levels |
| `make mcode-plugin` | The package satisfies the MiniMax Code manifest and packaging rules |
| `make test` | `go vet ./...` and `go test -race -count=1 ./...` |
| `make dogfood` | The built binary answers `--version`, `--status`, `--discover`, `--usage` and refuses recursive dispatch |

See [`AGENTS.md`](AGENTS.md) for the full engineering standards.

## Releasing

The version lives in three files: the CLI constant, the plugin manifest and the
changelog. One command keeps them aligned:

```sh
make bump-version VERSION=1.0.6
make release-check
```

See [`RELEASE.md`](RELEASE.md) for the full release checklist.

## Configuration

| Variable | Purpose |
|---|---|
| `MCODE_ROUTER_QUOTA_FILE` | Override the quota state path |
| `MCODE_ROUTER_DISCOVERY_FILE` | Override the CLI discovery cache path |
| `MCODE_ROUTER_SCRATCH_DIR` | Override the router scratch directory |
| `MCODE_ROUTER_PLUGIN_ROOT` | Override the plugin package root |
| `MCODE_ROUTER_DEPTH` / `ROUTER_DEPTH` | Recursion guard |
| `MCODE_ROUTER_NO_BUILD=1` | Use the existing binary instead of rebuilding |
| `MINIMAX_DATA_DIR` | MiniMax Code data directory (default `~/.minimax`) |

## Uninstall

```bash
curl -fsSL https://ubermetroid.github.io/mcode-router/install.sh | bash -s -- --uninstall
./install.sh --uninstall --purge   # also delete ~/.local/state/mcode-router
```

Uninstall removes the CLI symlink, the skills, the agent profiles, the MCP server entry and
the context hooks, leaving every other MiniMax Code server and hook in place.

## License

Apache-2.0. See [`LICENSE`](LICENSE).
