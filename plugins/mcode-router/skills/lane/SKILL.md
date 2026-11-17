---
name: lane
description: >-
  Inspect and configure which mcode-router lane, model, and effort run at each
  OODA stage (observe, orient, act, verify) globally or for this repository, or
  apply a workflow preset. Use when the user asks to switch, pin, reset, or
  inspect the models used across OODA stages.
---

# Lane — OODA Stage & Model Selector

Use this skill when the user asks to switch, pin, reset, or inspect the lanes
and models used across OODA stages (`observe`, `orient`, `act`, `verify`).

## Procedure

1. Inspect live fleet capabilities and current stage bindings:
   ```bash
   mcode-router --status
   ```
   The `ooda_status` tool returns the same report.

2. Ask the user with `ask_user` so they get native selection:

   - **Prompt 1 — what and where:**
     - `Action`: `Change a single OODA stage (observe / orient / act / verify)`,
       `Apply a workflow preset (auto / conserve / deep / devin / grok / mcode /
       muse / mistral)`, or `Reset all stages to auto`
     - `Scope`: `Global (~/.local/state/mcode-router/quota.json)` or
       `Repo-local (this workspace only)`
   - **Prompt 2 — the lane, model and effort** when a single stage is chosen:
     - Stage, then target lane (`auto` or any non-paid lane from `--status`;
       persistent pins exclude paid `devin-fusion` credits and PAYG `muse`).
     - When the lane is not `auto`, pick one of that lane's discovered models
       from `--status` and the effort control for that CLI: `--effort` for
       `muse`, `mcode`, `grok`; skip effort for `via-model` `devin-*` and
       `mistral`.

3. Apply the change:
   - Single stage: call `ooda_set_stage` with `stage`, `lane`, and `local` when
     the scope is repo-local. Use the CLI form for model, effort, or
     until-reset options:
     ```bash
     mcode-router --set-stage act=muse --model muse-ember-1.0 --effort xhigh --local
     ```
   - Preset:
     ```bash
     mcode-router --set-stage preset=<name> [--local]
     ```
   - Reset:
     ```bash
     mcode-router --reset-stages [--local]
     ```

4. Print the updated stage matrix concisely, and state which lanes changed.

## Notes

- Pinned stages soft-failover on a 429 or cooldown, so a pin is a preference,
  not a guarantee. Automatic selection is the default and is usually correct.
- `devin-fusion` spends paid credits and is only ever used when the user
  explicitly asks for it.
- `ooda_set_stage` needs both `stage` and `lane`. Pass `local: true` for a
  repo-only pin.
