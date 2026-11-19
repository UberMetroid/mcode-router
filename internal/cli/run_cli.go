package cli

import (
	"fmt"
	"io"
	"os"

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
	if err := fleet.EnsureStateRoots(); err != nil {
		fmt.Fprintf(errW, "error: %v\n", err)
		return 2
	}
	if opts.Status || opts.Discover || opts.Live {
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
		root, err := fleet.RepoRoot(opts.Cwd)
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
