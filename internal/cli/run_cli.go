package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/dispatch"
	"github.com/UberMetroid/mcode-router/internal/envelope"
	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func validateRunArgs(opts CLIOpts, defTimeout int, withFiles bool) ([]string, int, []string, error) {
	if err := fleet.CheckModelGuard(opts.Model); err != nil {
		return nil, 0, nil, err
	}
	lanes, err := fleet.ParseLanes(opts.Lane, true)
	if err != nil {
		return nil, 0, nil, err
	}
	timeout := defTimeout
	if opts.Timeout != "" {
		t, terr := fleet.ParseTimeout(opts.Timeout)
		if terr != nil {
			return nil, 0, nil, terr
		}
		timeout = t
	}
	if !withFiles {
		return lanes, timeout, nil, nil
	}
	var files []string
	for _, item := range worktree.ParseCSVArg(opts.Files) {
		norm, nerr := worktree.NormalizeRelPath(item)
		if nerr != nil {
			return nil, 0, nil, nerr
		}
		if norm != "" {
			files = append(files, norm)
		}
	}
	return lanes, timeout, files, nil
}

func derefOr(p *string, fb string) string {
	if p != nil && *p != "" {
		return *p
	}
	return fb
}

func handleQuotaFlags(opts CLIOpts, outW, errW io.Writer) (int, bool) {
	acted := false
	if opts.Enable != "" {
		canon, _, err := fleet.SetProviderStatus(opts.Enable, "active", "", nil, false, "")
		if err != nil {
			fmt.Fprintln(errW, err)
			return 2, true
		}
		fmt.Fprintf(outW, "enabled %s in %s\n", canon, fleet.QuotaFilePath(""))
		acted = true
	}
	if opts.Disable != "" {
		canon, entry, err := fleet.SetProviderStatus(
			opts.Disable, "cooldown", opts.Reason, opts.Hours, opts.AutoWake, "",
		)
		if err != nil {
			fmt.Fprintln(errW, err)
			return 2, true
		}
		until := ""
		if u := derefOr(entry.CooldownUntil, ""); u != "" {
			until = " until " + u
		}
		fmt.Fprintf(outW, "disabled %s (%s%s) in %s\n",
			canon, derefOr(entry.Reason, ""), until, fleet.QuotaFilePath(""))
		acted = true
	}
	if opts.SetCycle != "" {
		canon, entry, err := fleet.ConfigureCycle(fleet.CyclePatch{
			Provider: opts.SetCycle, Hours: opts.Hours, TokenBudget: opts.TokenBudget,
			CostBudget: opts.CostBudget, ResetNow: opts.ResetNow,
			Effort: strPtr(opts.Effort), Model: strPtr(opts.Model), MaxParallel: opts.MaxParallel,
		})
		if err != nil {
			fmt.Fprintln(errW, err)
			return 2, true
		}
		hrsS := "none"
		if entry.CycleHours != nil {
			hrsS = fmt.Sprintf("%g", *entry.CycleHours)
		}
		fmt.Fprintf(outW, "configured %s: status=%s model=%s effort=%s cycle_hours=%s next_reset_at=%s\n",
			canon, entry.Status, derefOr(entry.Model, "cli-default"),
			derefOr(entry.Effort, "cli-default"), hrsS, derefOr(entry.NextResetAt, "none"))
		acted = true
	}
	return 0, acted
}

func dispatchManifest(opts CLIOpts, outW, errW io.Writer) int {
	if err := fleet.CheckModelGuard(opts.Model); err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	root, err := fleet.RepoRoot("")
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	if _, err := fleet.ParseLanes(opts.Lane, true); err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	if opts.Timeout != "" {
		if _, err := fleet.ParseTimeout(opts.Timeout); err != nil {
			fmt.Fprintln(errW, err)
			return 2
		}
	}
	for _, lane := range worktree.ParseCSVArg(opts.ExcludeLanes) {
		if _, err := fleet.NormalizeLane(lane, false); err != nil {
			fmt.Fprintln(errW, err)
			return 2
		}
	}
	jobs := min(max(1, opts.Jobs), fleet.MaxJobs)
	tasks, err := worktree.LoadManifest(opts.Manifest)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	q, err := fleet.LoadQuota("")
	if err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	actSt := cliResolveStage("act", opts, root, q)
	verSt := cliResolveStage("verify", CLIOpts{Profile: opts.Profile}, root, q)
	sel, _ := envelope.SelectProvider(envelope.SelectReq{RequestedLane: actSt.Lane, Quota: q})
	if sel.Lane == "" {
		fmt.Fprintf(errW, "error: no usable worker CLI found on this host (%s)\n", sel.SkippedNote)
		return 2
	}
	pool := q.ParallelStripePool
	if len(pool) == 0 {
		pool = fleet.StripePool
	}
	stripeStr := "off (homogeneous)"
	if opts.Stripe && !opts.NoStripe {
		stripeStr = "on (" + strings.Join(pool, " -> ") + ")"
	}
	fmt.Fprintf(outW, "default lane: %s -> %s | jobs: %d | tasks: %d | stripe: %s\n",
		actSt.Lane, sel.Lane, jobs, len(tasks), stripeStr)
	if sel.SkippedNote != "" {
		fmt.Fprintf(outW, "routing note: %s\n", sel.SkippedNote)
	}
	return dispatch.RunManifestBatch(root, tasks, dispatch.BatchOpts{
		Lane: actSt.Lane, Model: actSt.Model, Effort: actSt.Effort, Timeout: opts.Timeout,
		VerifyLane: verSt.Lane, VerifyModel: verSt.Model, VerifyEffort: verSt.Effort, Skills: opts.Skills,
		ExcludeLanes: opts.ExcludeLanes, Jobs: jobs, LogWriter: outW, Stripe: opts.Stripe,
		NoStripe: opts.NoStripe, NoReview: opts.NoReview, NoPivot: opts.NoPivot, SoftPin: actSt.Source != "cli",
	})
}

func runDispatchMode(opts CLIOpts, outW, errW io.Writer) int {
	if opts.Recon != nil {
		return DoRecon(opts, outW, errW)
	}
	if opts.Orient != nil {
		return DoOrient(opts, outW, errW)
	}
	if opts.Task != nil {
		return DoSingleTask(opts, outW, errW)
	}
	if opts.Review != nil {
		target := *opts.Review
		if target == "." && opts.Manifest != "" {
			target = opts.Manifest
		}
		return DoStandaloneReview(target, opts, outW, errW)
	}
	return dispatchManifest(opts, outW, errW)
}

// RunCLI parses argv and executes the selected mcode-router command.
func RunCLI(prog string, argv []string, outW, errW io.Writer) int {
	if outW == nil {
		outW = os.Stdout
	}
	if errW == nil {
		errW = os.Stderr
	}
	opts, err := ParseArgs(prog, argv, outW)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	if opts.Help {
		return 0
	}
	if opts.Version {
		fmt.Fprintf(outW, "mcode-router v%s\n", fleet.Version)
		return 0
	}
	if opts.Status || opts.Discover {
		return ReportStatus(outW, opts.Discover, opts)
	}
	if opts.Usage {
		return DoUsage(outW)
	}
	if opts.Hygiene != nil {
		return DoHygiene(*opts.Hygiene, opts, outW, errW)
	}
	if fleet.RouterDepth() >= 1 {
		fmt.Fprintln(errW, "error: recursive mcode-router dispatch blocked (MCODE_ROUTER_DEPTH>=1 / ROUTER_DEPTH>=1). "+
			"Execute directly with native tools instead of invoking mcode-router.")
		return 2
	}
	if opts.Select {
		return RunSelectTUI(opts, nil, outW, errW)
	}
	if opts.Probe {
		return DoProbe(outW, errW)
	}
	if rc, handled := handleStageFlags(opts, outW, errW); handled {
		return rc
	}
	if rc, handled := handleQuotaFlags(opts, outW, errW); handled {
		return rc
	}
	if opts.Cleanup && opts.Recon == nil && opts.Orient == nil && opts.Task == nil && opts.Review == nil {
		root, err := fleet.RepoRoot("")
		if err != nil {
			fmt.Fprintln(errW, err)
			return 2
		}
		worktree.Cleanup(root, outW)
		return 0
	}
	if opts.Recon == nil && opts.Orient == nil && opts.Task == nil && opts.Review == nil && opts.Manifest == "" {
		fmt.Fprintln(errW, "error: manifest path is required (or pass --select, --recon, --orient, --task, "+
			"--review, --hygiene, --status, --usage, --set-stage, --reset-stages, --cleanup)")
		return 2
	}
	return runDispatchMode(opts, outW, errW)
}
