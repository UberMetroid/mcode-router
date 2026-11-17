---
name: mcode-router
description: >-
  Unified 4-phase Observe-Orient-Decide-Act (OODA) engineering methodology and
  zero-trust, quota-aware multi-agent router for MiniMax Code. Use for
  multi-file changes, non-obvious bugs, architectural refactors, parallel
  worktree execution, and cross-model verification. Splits a request into
  observe/orient/act/verify stages and picks the lane, model and effort
  automatically from quota windows and task capability. Skip heavy ceremony for
  single-line typos or pure Q&A.
---

# mcode-router — OODA Discipline & Multi-Agent Router

Use the `ooda_*` tools for multi-file changes, non-obvious bugs, architectural
refactors, and cross-model verification. Skip heavy ceremony for single-line
typos or pure Q&A.

The router owns model selection. You own the plan, the spec, and the final
verdict.

## Part I — 4-Phase OODA Engineering Methodology

### Phase 1 — OBSERVE (Zero Edits)
1. Read `AGENTS.md` (if present), `git status`, failing tests, and compiler or
   runtime error output.
2. Offload broad codebase reconnaissance instead of reading dozens of files
   inline: call `ooda_recon` with a specific question.
3. Synthesize verified facts into `<= 5` concise bullets before orienting.

### Phase 2 — ORIENT (Zero Edits)
Determine root cause, invariants, blast radius, and file coupling. Offload the
deep read-only synthesis with `ooda_orient` when the question is architectural.

- **Coupled / overlapping files:** shared files or interdependent signatures
  -> **Sequential**.
- **Disjoint modules:** zero overlapping `files` -> **Parallel Worktrees**.

**Direct Orient -> Act fast path:** when OBSERVE plus ORIENT isolate an
unambiguous 1-2 file fix with known invariants and a clear test gate, move
straight to Act and keep the orchestrator thin (`<10%` of total token burn).

### Phase 3 — DECIDE (Zero Edits)
Lock in before any code edits:

- **Files in scope** and **off-limits files**
- **Execution mode:** Sequential vs. Parallel Worktrees
- **Build/test gate:** the exact gate command (`make all`,
  `go test -race ./...`, `pytest -q`).

Routing is automatic. Call `ooda_status` when you need to explain a choice or
force one; otherwise let the router decide. The `complexity` parameter is the
capability axis and the router's default for each stage already matches the
right regime:

| Stage | Default complexity | Typical resolution |
|---|---|---|
| observe | `recon` | cheap read-only lane, e.g. `devin-fast` on `swe-2-flash-high` |
| orient | `deep` | strong reasoning lane at `xhigh`, e.g. `muse` on `muse-wonder-1.0` |
| act | `act` | building model, e.g. `muse` on `muse-ember-1.0` or `devin-pro` on `swe-2-max` |
| verify | `review` | off-family reviewer, never the authoring family |

Override per call with `lane`, `model`, `effort`, or `profile`. Pass `complexity`
when the task shape is unusual (`fast`, `tests`, `docs`, `deep`, `architecture`,
`reasoning`, `audit`, `ember`, `heavy`, `review`).

Always pass `cwd` to `ooda_recon`, `ooda_orient` and `ooda_task`. Without it a
stage runs in the MCP server's working directory, which is the session start
folder rather than the project you are working on.

### Phase 4 — ACT (Max 3 Loops)

#### Mode A — Sequential (1-2 Coupled Files)
1. Execute the scoped change with `ooda_task`, passing `cwd` (the project
   directory), the exact spec and the in-scope `files`.
2. Run the repo build/test gate command yourself.
3. Run an independent read-only cross-model diff review with `ooda_review`.

#### Mode B — Parallel Worktrees (2+ Disjoint Modules)
1. Write `.worktrees/tasks.json`
   (`{id, prompt, files, complexity?, lane?, exclude_lanes?, timeout?, review?}`)
   with strictly non-overlapping `files`. Workers must inspect first, touch
   only the listed files, run the repo gate before exiting, and end with a diff
   summary.
2. Execute the parallel worktree runner (`mcode-router .worktrees/tasks.json`,
   `--jobs` up to `8`; `devin-fast` and `mcode` are capped at 4 concurrent
   slots).
3. Inspect `.worktrees/results.json` and the real `git diff` on every
   `ooda/<id>` branch; reject non-empty `scope_violations` or
   `review.verdict != "ACCEPT"`.
4. Merge clean accepted branches (`git merge --no-ff ooda/<id>`), clean
   worktrees (`mcode-router --cleanup`), and re-run the gate on the merged branch.

#### Loop Exit & Mismatch Pivoting (Zero-Trust Family Rotation)
- If the merged gate is green and reviews return `VERDICT: ACCEPT`, stop
  cleanly.
- Otherwise feed the exact compiler or test failures, or the `REJECT` findings,
  back into Phase 1 and Phase 2, increment the loop counter, and pivot model
  families (automatic 30 minute mismatch memory; pass `exclude_lane` after
  downstream gate failures).
- Hard-stop and reassess with the user after 3 failed loops.

---

## Part II — Fleet Routing, Windows & Tariffs

Runtime quota state lives in `~/.local/state/mcode-router/quota.json` (`v9`,
`$XDG_STATE_HOME/mcode-router`; overridable via `MCODE_ROUTER_QUOTA_FILE`), and
`--local` per-repo stage pins persist out-of-git in
`~/.local/state/mcode-router/workspaces/<id>.json`.

### Per-Stage Routing Matrix
Each stage (`observe`, `orient`, `act`, `verify`) defaults to automatic
selection and resolves via **CLI flag > repo-local pin > profile > global pin >
automatic**. Soft-pin failover moves down the lane priority chain on a 429 or
cooldown and scrubs the primary lane's model and effort overrides. `verify`
always excludes the authoring model family, even if pinned identically.

| Lane | CLI | Effort Control & Defaults | Tier | Cycle / Cap |
|---|---|---|---|---|
| `devin-fusion` | `devin` | `via-model` (no `--effort`); family `fusion` | `credits` | opt-in only |
| `devin-pro` | `devin` | `via-model`; family `swe-2` | `unlimited` | `24h + 7d` |
| `devin-fast` | `devin` | `via-model`; family `swe-2-flash`, max 4 parallel | `unlimited` | `24h + 7d` |
| `muse` | `muse` | `--reasoning-effort (none..ultra)`; `muse-ember-1.0` on act, `muse-wonder-1.0` on deep | `capped` | `5h + 7d` |
| `mcode` | `mcode` | `--effort (low..max)`, max 4 parallel; primary reviewer | `high-cap` | `5h + 7d` |
| `grok` | `grok` | `--reasoning-effort (medium\|high\|xhigh)` | `capped` | `7d` rolling weekly |
| `mistral` | `vibe` | `via-model` (`glm-5-3[high]`, etc.) | `capped` | `30d` monthly, 0.5x off-peak |

### Key Engine Guarantees
- **First-principles quota scheduling:** prioritises `0.5x` off-peak tariffs and
  soonest-expiring bounded windows before they reset, applies EWMA health
  scoring to demote repeatedly failing lanes while recovering on clean runs, and
  spares the last active bounded lane by shifting routine coding to
  `devin-pro`, then `devin-fast`, then `mcode`.
- **Automatic complexity and skill matching:** an omitted `complexity` and up to
  two non-core worker skills are inferred from the prompt and the files.
- **Gemini worker guardrail:** worker lanes reject `gemini*` and `orchestrator*`
  models outright.
- **Recursion guard:** workers run with `MCODE_ROUTER_DEPTH>=1` and
  `ROUTER_DEPTH>=1`, which blocks nested `mcode-router` dispatch.
- **Live quota and stall kill, Merkle cache, cross-model review:** unambiguous
  mid-stream quota errors and idle stream stalls trigger failover; read-only
  stages snapshot the repo and fail closed on unexpected writes.

## Tool Reference

| Action | Tool |
|---|---|
| OBSERVE: read-only recon | `ooda_recon` |
| ORIENT: read-only root cause and plan | `ooda_orient` |
| ACT: scoped coding task | `ooda_task` |
| VERIFY: off-family diff review | `ooda_review` |
| Fleet status, stage matrix, models | `ooda_status` |
| Per-lane token and cost telemetry | `ooda_usage` |
| Live worker telemetry (model, effort, phase, tok/s) | `ooda_live` |
| Pin or reset a stage lane | `ooda_set_stage` |

The same verbs exist as CLI flags for scripted and parallel use:
`mcode-router --recon`, `--orient`, `--task`, `--review`, `--status`, `--usage`,
`--live`, `--set-stage`, `--select`, `--discover`, `--probe`, `--hygiene`, `--cleanup`.
