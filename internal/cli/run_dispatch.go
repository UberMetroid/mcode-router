package cli

import (
	"fmt"
	"github.com/UberMetroid/mcode-router/internal/dispatch"
	"github.com/UberMetroid/mcode-router/internal/envelope"
	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/worktree"
	"io"
	"strings"
)

func dispatchManifest(opts CLIOpts, outW, errW io.Writer) int {
	if err := fleet.CheckModelGuard(opts.Model); err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	root, err := fleet.RepoRoot(opts.Cwd)
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
