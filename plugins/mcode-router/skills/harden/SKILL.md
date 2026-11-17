---
name: harden
description: >-
  4-phase OODA trust-boundary verification workflow (harden [path] or harden
  fix [path]) combining 0-token deterministic hygiene, source-to-sink mapping,
  off-family skeptic falsification, and compile-clean Red-to-Green refusal unit
  tests with zero exploit trigger words.
---

# harden — Zero-Hallucination Trust-Boundary Verification

Use `harden` to audit a repository, module directory, or branch for real
trust-boundary defects without fabricating noise.

**Modes:**

- `harden [path]` *(default)*: runs Phases 1 to 2, then pauses at Phase 3 with
  `ask_user` (*Fix all confirmed issues*, *Select subset*, *Report only*).
- `harden fix [path]`: runs Phases 1 to 2 falsification first, then proceeds
  straight into Phase 4 fixes plus refusal unit tests for confirmed items.

**Vocabulary rule:** frame every `ooda_recon`, `ooda_orient`, `ooda_task`, and
`ooda_review` prompt strictly in systems-engineering terms (*trust boundaries*,
*input invariants*, *path and command containment*, *capability-token
integrity*, *ambient-authority bypass*, *arithmetic and overflow invariants*,
*spatial and temporal memory-safety and lifetime invariants*, *fail-closed
error handling*, *refusal unit tests*). Do **not** use offensive or dual-use
trigger words across any fleet CLI.

The MCP tools `ooda_recon`, `ooda_orient`, `ooda_task`, and `ooda_review` are
equivalent to the CLI verbs below. Use whichever is convenient; the CLI form
carries the extra flags shown here.

---

## Gate 1 — OBSERVE (`0` edits): deterministic hygiene plus boundary recon

1. Run the `0`-token deterministic hygiene scanner first:
   ```bash
   mcode-router --hygiene [target]
   ```
   Collect all `P0` credential, key, and file findings plus `P1` hardening
   footguns (`0777` and `0666` permissions, `InsecureSkipVerify: true` outside
   tests, `curl|sh` outside docs, `usage:` markers, or `# hygiene:allow`, and
   non-loopback `http://` fetches).
2. Run a snapshot-guarded read-only boundary reconnaissance pass, scoped to
   target production files, with a 300 second timeout so workers do not crawl
   unrelated tests:
   ```bash
   mcode-router --recon "Map external untrusted input sources (CLI args, env vars, HTTP/RPC/IPC requests, git/file reads) and sensitive sinks in [target] with exact file:line locations. Read-only, cite only verified paths; skip test files." --timeout 300 --skill harden
   ```

---

## Gate 2 — ORIENT Pass A: structured finder (`0` edits)

Propose at most **`8`** concrete candidate chains (`H1..Hn`) via `ooda_orient`
with a 480 second timeout:

```bash
mcode-router --orient "Inspect verified entry points and sinks from recon in [target]. Propose <= 8 candidate trust-boundary defects H1..Hn (skip test files). Every candidate MUST include:
  SOURCE: <file:line> (external/untrusted input only; compile-time constants and test mocks forbidden)
  MISSING_GUARD: <exact unchecked invariant along the call chain>
  SINK: <file:line> (concrete failure impact)
Allowed SINK categories:
  1. Path escape or symlink traversal outside workspace/root
  2. Subprocess command/argv injection or unsafe child environment inheritance
  3. Capability-token forgery/widening or ambient-authority bypass
  4. Fail-open auth, signature, snapshot, or review verdict handling
  5. Unbounded memory, file, goroutine, or stream growth from untrusted input
  6. Credential/secret leakage into diffs, logs, prompts, or child processes
  7. State-corrupting concurrency or lock races
  8. Arithmetic, overflow, and numerical invariants
  9. Spatial/temporal memory-safety and lifetime invariants" --timeout 480 --skill harden
```

Disqualify any claim missing an exact `SOURCE -> MISSING_GUARD -> SINK` chain
or referencing constants and test doubles. Read-only runs return cached results
at `0` tokens when files have not changed.

---

## Gate 3 — ORIENT Pass B: off-family skeptic falsification (`0` edits)

Whenever the finder produces one or more candidates `H1..Hn`, launch a
**different model family** (`--exclude-lane <finder_lane> --skill none
--timeout 480`; use `--effort high` when `mcode` is selected so multi-file
checks finish well within budget) and **inline the finder's concise `H1..Hn`
chains directly in the prompt**, because read-only worker runs are stateless
across invocations:

```bash
mcode-router --orient "Skeptic falsification pass (do not crawl unrelated test suites). Read callers, type invariants, bounds checks, capability enforcement, and sanitizer helpers around these inlined candidates H1..Hn in [target] and try to DISPROVE each one:
<paste the finder's H1..Hn SOURCE -> MISSING_GUARD -> SINK summary with exact line ranges>
Classify strictly as:
  - DISPROVED: cite the exact file:line upstream guard preventing reachability
  - THEORETICAL: unreachable or requires the caller to violate non-external contracts
  - CONFIRMED: verified reachable source-to-sink path with no missing-guard doubt
Keep only CONFIRMED candidates with verified file:line evidence." --exclude-lane <finder_lane> --skill none --timeout 480
```

Discard all `DISPROVED` and `THEORETICAL` items immediately.

---

## Phase 3 — DECIDE (`0` edits)

1. If `--hygiene` is clean **and** no `CONFIRMED` chains remain, report a clean
   status summary and stop.
2. Otherwise present a concise verification table (`ID`, `Severity`,
   `Source -> Sink`, `Proposed Guard + Refusal Test`) and:
   - In `harden` mode: invoke `ask_user` (*Fix all confirmed issues*,
     *Select specific IDs*, *Report only*).
   - In `harden fix` mode: lock scope (`files`, off-limits files, repo test
     gate) and proceed directly to Gate 4.

---

## Gate 4 — ACT plus VERIFY: compile-clean Red-to-Green proof (max `3` loops)

For each approved finding, use sequential `ooda_task` with explicit `files`, or
a parallel `.worktrees/tasks.json` when findings live in disjoint modules.

1. **Compile-clean Red proof (mandatory before editing production code):**
   - Write a standard boundary or refusal unit test (`Test...`) using safe
     malformed or out-of-scope inputs (`../outside`, a forged or missing
     capability scope, an oversized payload, a malformed header, an
     integer-boundary wraparound, an out-of-bounds or lifetime-escape slice)
     against **untouched production code**.
   - Run the unit test command directly, with `-race`, ASan/UBSan, or Zig
     `ReleaseSafe` and `std.testing.allocator` leak and bounds checks where the
     target language supports them.
   - It **must compile cleanly** (`0` missing-symbol, signature, or syntax
     errors) against the existing production code and **fail at runtime** via a
     test assertion (*Red*).
   - If it fails to compile, fix the test to match real symbols before
     continuing.
   - If it compiles cleanly and **already passes** on untouched production code,
     abort the production edit, revert the test, and reclassify the finding as
     `FALSE_POSITIVE`.
2. **Minimal Green guard:**
   - Add the minimal fail-closed check at the invariant boundary and confirm the
     refusal unit test passes (*Green*).
3. **Final verification gates:**
   - Run the repository's full test and build suite (`make all`,
     `go test -race ./...`, `pytest -q`).
   - Re-run `mcode-router --hygiene [target]`; it must exit `0` for `P0` and for
     the fixed `P1` items.
   - Run the off-family `ooda_review`; require `VERDICT: ACCEPT`.
