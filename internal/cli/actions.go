package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/dispatch"
	"github.com/UberMetroid/mcode-router/internal/envelope"
	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/verify"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

// ResolveCWD resolves the stage working directory, honouring --cwd and
// falling back to the process working directory.
func ResolveCWD(opts CLIOpts, errW io.Writer) (string, bool) {
	target := strings.TrimSpace(opts.Cwd)
	if target == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(errW, "error: resolve working directory: %v\n", err)
			return "", false
		}
		return cwd, true
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(errW, "error: invalid --cwd %q: %v\n", target, err)
		return "", false
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(errW, "error: --cwd is not a directory: %s\n", abs)
		return "", false
	}
	return abs, true
}

// resolveAutoRoute resolves the lane and model an unpinned stage would use right now.
func resolveAutoRoute(stage string, q *fleet.QuotaState) string {
	comp := map[string]string{
		"observe": "recon", "orient": "deep", "act": "act", "verify": "review",
	}[stage]
	sel, err := envelope.SelectProvider(envelope.SelectReq{
		RequestedLane: "auto", Complexity: comp, Quota: q,
	})
	if err != nil || sel.Lane == "" {
		return "auto (quota-aware)"
	}
	spec, serr := envelope.EffectiveLaneSpec(sel.Lane, sel.Info, "", "", comp, "")
	if serr != nil {
		return sel.Lane + " (model unresolved)"
	}
	return fmt.Sprintf("%s (%s/%s)", sel.Lane,
		dispatch.ShortModel(spec.DisplayModel), nonEmptyOr(spec.DisplayEffort, "default"))
}

// DoStandaloneReview runs read-only cross-model review against targetArg.
func DoStandaloneReview(targetArg string, opts CLIOpts, outW, errW io.Writer) int {
	if targetArg == "" {
		targetArg = "."
	}
	absT, err := filepath.Abs(targetArg)
	if err != nil {
		fmt.Fprintf(errW, "error: invalid target %q: %v\n", targetArg, err)
		return 2
	}
	if st, serr := os.Stat(absT); serr != nil || !st.IsDir() {
		fmt.Fprintf(errW, "error: review target is not a directory: %s\n", absT)
		return 2
	}
	_, timeout, files, err := validateRunArgs(opts, fleet.ReviewTimeout, true)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	excl := make(map[string]bool)
	for _, x := range worktree.ParseCSVArg(opts.ExcludeLanes) {
		canon, nerr := fleet.NormalizeLane(x, false)
		if nerr != nil {
			fmt.Fprintln(errW, nerr)
			return 2
		}
		excl[canon] = true
	}
	root, err := fleet.RepoRoot(absT)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	slot, err := worktree.EnsureSlot("review-standalone")
	if err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	dc := verify.CollectDiffContext(root, true)
	if !dc.HasChanges {
		fmt.Fprintf(outW, "No uncommitted changes or commit diff found to review in %s.\n", root)
		return 0
	}
	verSt := cliResolveStage("verify", opts, root, nil)
	fmt.Fprintf(outW, "Reviewing %s in %s (cross-model, lane=%s)...\n", dc.Source, root, verSt.Lane)
	res := dispatch.ExecReview(verify.ReviewReq{
		TargetDir: root, SlotDir: slot, DiffCtx: dc, TimeoutSec: timeout,
		Declared: files, LogWriter: outW, Exclude: excl,
		ReqLane: verSt.Lane, Model: verSt.Model, Effort: verSt.Effort, NoCache: opts.NoCache,
	})
	if res.Verdict == "SKIPPED" && res.Lane == "" {
		fmt.Fprintf(errW, "error: no review provider available (%s)\n", res.Reason)
		return 2
	}
	fmt.Fprintln(outW, res.Output)
	fmt.Fprintf(outW, "\n[review verdict: %s | lane=%s model=%s]\n", res.Verdict, res.Lane, res.Model)
	if res.ExitCode == 0 && res.Verdict == "ACCEPT" {
		return 0
	}
	if res.ExitCode > 0 {
		return res.ExitCode
	}
	return 1
}

// DoRecon runs a single read-only observation query in CWD.
func DoRecon(opts CLIOpts, outW, errW io.Writer) int {
	return runReadOnlyStage("observe", *opts.Recon, opts, outW, errW)
}

func reviewAndTrackTask(
	opts CLIOpts, res dispatch.TaskRes, cwd, baseRef string,
	files []string, baseline map[string]string, outW io.Writer,
) bool {
	verdict, revFailed, revReason := "", false, "review slot setup failed"
	var viols []string
	if fleet.TryRepoRoot(cwd) != "" {
		var changed []string
		var dc verify.DiffContext
		if baseRef != "" && verify.HeadRef(cwd) != baseRef {
			dc = verify.CollectDiffContext(cwd, false, baseRef)
			changed = dc.ChangedFiles
		} else {
			changed = worktree.ChangedFilesIn(cwd, baseline)
		}
		if viols = worktree.FindScopeViolations(changed, files); len(viols) > 0 {
			fmt.Fprintf(outW, "SCOPE VIOLATIONS: %s\n", strings.Join(viols, ", "))
		}
		if !opts.NoReview && res.ExitCode == 0 && len(viols) == 0 && len(changed) > 0 {
			if revSlot, err := worktree.EnsureSlot("task-review"); err != nil {
				fmt.Fprintf(outW, "error: review slot setup failed (%v) — failing closed\n", err)
				revFailed = true
			} else {
				if dc.Source == "" {
					dc = verify.CollectDiffContext(cwd, false)
				}
				verSt := cliResolveStage("verify", CLIOpts{Profile: opts.Profile}, cwd, nil)
				rev := dispatch.ExecReview(verify.ReviewReq{
					TargetDir: cwd, SlotDir: revSlot, DiffCtx: dc, TimeoutSec: fleet.ReviewTimeout,
					ScopeViols: viols, Declared: files, LogWriter: outW, AuthorLane: res.Lane,
					AuthorLanes: res.UsedLanes(), ReqLane: verSt.Lane, Model: verSt.Model,
					Effort: verSt.Effort, NoCache: opts.NoCache,
				})
				verdict = rev.Verdict
				revFailed = verdict != "REJECT" && (verdict != "ACCEPT" || rev.ExitCode != 0)
				revReason = "failed, skipped, or inconclusive verify"
				fmt.Fprintf(outW, "\n--- Verify [%s] (%s) ---\n%s\n[review verdict: %s]\n",
					rev.Lane, rev.Verdict, rev.Output, rev.Verdict)
			}
		}
	}
	if res.ExitCode != 0 {
		envelope.RecordMismatch(cwd, files, res.Lane, fmt.Sprintf("exited %d", res.ExitCode))
	} else if len(viols) > 0 {
		envelope.RecordMismatch(cwd, files, res.Lane, "violated scope: "+strings.Join(viols, ", "))
	} else if revFailed {
		envelope.RecordMismatch(cwd, files, res.Lane, revReason)
	} else if verdict == "REJECT" {
		envelope.RecordMismatch(cwd, files, res.Lane, "was REJECTED by verify")
	} else {
		envelope.ClearMismatch(cwd, files)
	}
	return len(viols) > 0 || revFailed || verdict == "REJECT"
}

// DoSingleTask executes a single routed coding task directly in CWD.
func DoSingleTask(opts CLIOpts, outW, errW io.Writer) int {
	_, _, files, err := validateRunArgs(opts, fleet.DefaultTimeout, true)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	slot, err := worktree.EnsureSlot("task")
	if err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	cwd, ok := ResolveCWD(opts, errW)
	if !ok {
		return 1
	}
	var baseline map[string]string
	var baseRef string
	if fleet.TryRepoRoot(cwd) != "" {
		baseline = worktree.DirtyState(cwd)
		baseRef = verify.HeadRef(cwd)
	} else if !opts.NoReview {
		fmt.Fprintln(errW, "refusing: --task needs a git repository target so the change can be "+
			"diffed and reviewed (use --no-review to run without verification)")
		return 1
	}
	actSt := cliResolveStage("act", opts, cwd, nil)
	avoid := map[string]bool{}
	if (actSt.Lane == "auto" || strings.HasPrefix(actSt.Source, "profile:")) && !opts.NoPivot {
		if mm := envelope.LoadRecentMismatch(cwd, files); mm != nil && envelope.HasLaneFamily(actSt.Lane, mm.Family) {
			avoid[mm.Family] = true
			fmt.Fprintf(outW, "▸ [OODA:MISMATCH PIVOT] prior loop on %s (%s) %s — rotating model family\n",
				mm.Lane, mm.Family, mm.Reason)
		}
	}
	comp := opts.Complexity
	if comp == "" {
		comp = worktree.InferComplexity(*opts.Task, files, false)
	}
	opts.Lane, opts.Model, opts.Effort = actSt.Lane, actSt.Model, actSt.Effort
	task := buildTaskSpec("task", *opts.Task, comp, files, opts, cwd, false)
	res, err := dispatch.RunTaskWithFallback(dispatch.TaskReq{
		Task: task, Worktree: cwd, SlotDir: slot, Lane: actSt.Lane, Model: actSt.Model,
		Effort: actSt.Effort, TimeoutRaw: opts.Timeout, Exclude: worktree.ParseCSVArg(opts.ExcludeLanes),
		AvoidFam: avoid, NoPivot: opts.NoPivot, SoftPin: actSt.Source != "cli", LogWriter: outW,
	})
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	fmt.Fprintf(outW, "[OODA:ACT lane=%s model=%s effort=%s %.1fs exit=%d]\n",
		res.Lane, res.Model, res.Effort, res.DurationS, res.ExitCode)
	if res.FallbackFrom != "" {
		fmt.Fprintf(outW, "routing note: %s\n", res.FallbackFrom)
	}
	if res.Output != "" {
		fmt.Fprintln(outW, res.Output)
	}
	revFailed := reviewAndTrackTask(opts, res, cwd, baseRef, files, baseline, outW)
	if res.ExitCode > 0 {
		return res.ExitCode
	}
	if res.ExitCode != 0 || revFailed {
		return 1
	}
	return 0
}
