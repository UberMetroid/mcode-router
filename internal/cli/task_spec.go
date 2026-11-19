package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/dispatch"
	"github.com/UberMetroid/mcode-router/internal/envelope"
	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/verify"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

func buildTaskSpec(
	id, rawPrompt, comp string, files []string, opts CLIOpts, cwd string, ro bool,
) worktree.TaskSpec {
	skills := worktree.ParseCSVArg(opts.Skills)
	if !ro || len(skills) > 0 {
		skills = worktree.ResolveTaskSkills(skills, rawPrompt, files, cwd)
	}
	prompt := rawPrompt
	if ro {
		if id == "orient" {
			prompt = fleet.OrientPrefix + rawPrompt
		} else {
			prompt = fleet.ReconPrefix + rawPrompt
		}
	}
	return worktree.TaskSpec{
		ID: id, Prompt: prompt, Files: files, Skills: skills,
		Complexity: comp, Model: opts.Model, Effort: opts.Effort, ReadOnly: ro,
	}
}

func cliResolveStage(stage string, opts CLIOpts, cwd string, q *fleet.QuotaState) worktree.StageSpec {
	cliLane := ""
	if opts.LaneSet {
		cliLane = opts.Lane
		if parsed, perr := fleet.ParseLanes(opts.Lane, true); perr == nil && len(parsed) > 0 {
			cliLane = strings.Join(parsed, ",")
		}
	}
	if q == nil && !opts.LaneSet {
		var qerr error
		if q, qerr = fleet.LoadQuota(""); qerr != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", qerr)
		}
	}
	return worktree.ResolveStage(stage, cliLane, opts.Model, opts.Effort, opts.Profile, cwd, q)
}

func printStageMatrix(w io.Writer, cwd string, opts CLIOpts, q *fleet.QuotaState) {
	fmt.Fprintln(w, "\nWorker Stage Routing Matrix (precedence: CLI > --local > --profile > global > auto):")
	fmt.Fprintf(w, "%-9s %-14s %-40s %s\n", "STAGE", "MODE", "ROUTE", "ROLE")
	fmt.Fprintln(w, strings.Repeat("-", 100))
	roles := map[string]string{
		"observe": "Read-only recon (--recon)",
		"orient":  "Root-cause & architecture (--orient)",
		"act":     "Coding & worktree execution (--task / manifest)",
		"verify":  "Cross-model diff review (--review / post-task)",
	}
	for _, st := range fleet.StageNames {
		stOpts := opts
		if st == "verify" && opts.Review == nil && !strings.EqualFold(opts.Complexity, "review") {
			stOpts.Lane, stOpts.LaneSet, stOpts.Model, stOpts.Effort = "", false, "", ""
		}
		sp := cliResolveStage(st, stOpts, cwd, q)
		mode := sp.Source
		route := "auto (quota-aware)"
		if sp.Lane == "auto" {
			route = resolveAutoRoute(st, q)
		} else {
			mod, eff := nonEmptyOr(sp.Model, "default"), nonEmptyOr(sp.Effort, "default")
			route = fmt.Sprintf("%s (%s/%s)", fleet.SafeText(sp.Lane),
				dispatch.ShortModel(fleet.SafeText(mod)), fleet.SafeText(eff))
			if sp.ExpiresAt != nil && *sp.ExpiresAt != "" {
				route += " [until reset]"
			}
		}
		fmt.Fprintf(w, "%-9s %-14s %-40s %s\n", st, fleet.SafeText(mode), route, roles[st])
	}
}

func handleStageFlags(opts CLIOpts, outW, errW io.Writer) (int, bool) {
	if !opts.ResetStages && opts.SetStage == "" {
		return 0, false
	}
	cwd, ok := ResolveCWD(opts, errW)
	if !ok {
		return 1, true
	}
	var stage, lane string
	if opts.ResetStages {
		stage, lane = "all", "auto"
	} else {
		k, v, hasEq := strings.Cut(opts.SetStage, "=")
		if !hasEq || strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			fmt.Fprintln(errW, "error: --set-stage expects STAGE=LANE (or preset=NAME)")
			return 2, true
		}
		stage, lane = strings.TrimSpace(k), strings.TrimSpace(v)
	}
	stages, err := worktree.ConfigureStage(worktree.StagePatch{
		Stage: stage, Lane: lane, Model: opts.Model, Effort: opts.Effort,
		UntilReset: opts.UntilReset, Local: opts.Local, Cwd: cwd,
	})
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2, true
	}
	scope := fleet.QuotaFilePath("")
	if opts.Local {
		scope = worktree.WorkspaceStagesPath(cwd)
	}
	fmt.Fprintf(outW, "updated stages in %s:\n", scope)
	for _, st := range fleet.StageNames {
		r := stages[st]
		if r.Lane == "" && opts.Local {
			r.Lane = "(inherit)"
		} else if r.Lane == "" {
			r.Lane = "auto"
		}
		fmt.Fprintf(outW, "  %-8s -> %s (model=%s, effort=%s)\n",
			st, r.Lane, derefOr(r.Model, "cli-default"), derefOr(r.Effort, "cli-default"))
	}
	return 0, true
}

func runReadOnlyStage(stage, prompt string, opts CLIOpts, outW, errW io.Writer) int {
	slotName, banner, defComp := "recon", "OBSERVE", "recon"
	if stage == "orient" {
		slotName, banner, defComp = "orient", "ORIENT", "deep"
	}
	_, _, files, err := validateRunArgs(opts, fleet.DefaultTimeout, true)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	slot, err := worktree.EnsureSlot(slotName)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	cwd, ok := ResolveCWD(opts, errW)
	if !ok {
		return 1
	}
	comp := nonEmptyOr(opts.Complexity, defComp)
	st := cliResolveStage(stage, opts, cwd, nil)
	opts.Lane, opts.Model, opts.Effort = st.Lane, st.Model, st.Effort
	task := buildTaskSpec(slotName, prompt, comp, files, opts, cwd, true)
	preSnap := verify.SnapshotRepo(cwd)
	excl := worktree.ParseCSVArg(opts.ExcludeLanes)
	skBlocks, _ := worktree.LoadSkillBlocks(task.Skills, cwd)
	cPay := fmt.Sprintf("%s|%s|%s|%t|%s|%v|%t|%v|%v|%s|%s", st.Lane, st.Model, st.Effort,
		st.Source != "cli", comp, excl, opts.NoPivot, task.Files, task.Skills, skBlocks, task.Prompt)
	cKey := verify.CacheKey(stage, cPay, preSnap)
	if hit, ok := verify.LoadStageCache(cKey, opts.NoCache); ok {
		fmt.Fprintf(errW, "[OODA:%s lane=%s model=%s effort=%s 0.0s exit=0 cache=hit]\n",
			banner, hit.Lane, hit.Model, nonEmptyOr(hit.Effort, "default"))
		fmt.Fprintln(outW, hit.Output)
		return 0
	}
	res, err := dispatch.RunTaskWithFallback(dispatch.TaskReq{
		Task: task, Worktree: cwd, SlotDir: slot, Lane: st.Lane, Model: st.Model, Effort: st.Effort,
		TimeoutRaw: opts.Timeout, Exclude: excl, NoPivot: opts.NoPivot, SoftPin: st.Source != "cli", LogWriter: errW,
	})
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	fmt.Fprintf(errW, "[OODA:%s lane=%s model=%s effort=%s %.1fs exit=%d]\n",
		banner, res.Lane, res.Model, res.Effort, res.DurationS, res.ExitCode)
	if drift := verify.SnapshotDrift(preSnap, verify.SnapshotRepo(cwd)); len(drift) > 0 {
		fmt.Fprintf(errW, "[P0] zero-trust: read-only %s modified files: %s\n", slotName, strings.Join(drift, ", "))
		fmt.Fprintln(errW, "[P0] zero-trust: worker output withheld")
		return 1
	}
	if res.Output != "" {
		fmt.Fprintln(outW, res.Output)
	}
	if res.ExitCode == 0 && !res.TimedOut {
		verify.SaveStageCache(cKey, verify.CacheEntry{
			Stage: stage, Lane: res.Lane, Model: res.Model, Effort: res.Effort, Output: res.Output,
		}, opts.NoCache)
		return 0
	}
	if res.ExitCode > 0 {
		return res.ExitCode
	}
	return 1
}

// DoOrient runs a read-only root-cause and architecture orientation pass.
func DoOrient(opts CLIOpts, outW, errW io.Writer) int {
	return runReadOnlyStage("orient", *opts.Orient, opts, outW, errW)
}

func printDiscoveryFooter(
	w io.Writer, caps map[string]envelope.LaneCap, skills []worktree.SkillMeta, verbose bool,
) {
	fmt.Fprintln(w, "\nDiscovered CLI Capabilities (effort control & available models):")
	groups := [][2]string{
		{"devin", "devin-pro"}, {"muse", "muse"},
		{"mcode", "mcode"}, {"grok", "grok"}, {"vibe", "mistral"},
	}
	limit, moreLbl := 6, "more; run --discover"
	if verbose {
		limit, moreLbl = 20, "in discovery.json"
	}
	for _, g := range groups {
		lc := caps[g[1]]
		shown, extra := "(run mcode-router --discover)", ""
		if len(lc.Models) > 0 {
			shown = strings.Join(lc.Models[:min(len(lc.Models), limit)], ", ")
		}
		if len(lc.Models) > limit {
			extra = fmt.Sprintf(" (+%d %s)", len(lc.Models)-limit, moreLbl)
		}
		fmt.Fprintf(w, "  %-7s %-37s models: %s%s\n", g[0], nonEmptyOr(lc.EffortMode, "default"), shown, extra)
	}
	fmt.Fprintln(w, "\nInjectable Worker Skills (--skill <name>):")
	if len(skills) == 0 {
		fmt.Fprintln(w, "  (none discovered)")
		return
	}
	for _, s := range skills {
		tag := ""
		if s.Core {
			tag = " [built-in]"
		}
		fmt.Fprintf(w, "  %-10s : %s%s\n", s.Name, nonEmptyOr(s.Desc, "(no description)"), tag)
	}
}

func tuiLaneModels(lane string, lc envelope.LaneCap) []string {
	out, seen := []string{"cli-default (auto/configured)"}, map[string]bool{}
	for _, m := range lc.Models {
		if m != "" && !fleet.GeminiModelRe.MatchString(m) && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	if len(out) == 1 && lane != "auto" && lc.EffectiveModel != "" && !fleet.GeminiModelRe.MatchString(lc.EffectiveModel) {
		out = append(out, lc.EffectiveModel)
	}
	return out
}
