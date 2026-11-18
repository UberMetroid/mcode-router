package dispatch

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/envelope"
	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/verify"
)

func runTaskAttempt(st *taskState, sel envelope.SelectRes, lease *os.File) (ExecRes, string, string) {
	req := st.req
	prim := strings.Split(st.canonReq, ",")[0]
	reqP, _ := fleet.NormalizeLane(strings.Split(req.Lane, ",")[0], true)
	rawMod, rawEff := req.Task.Model, req.Task.Effort
	if (req.Task.Lane == "" || prim == reqP) && rawMod == "" {
		rawMod = req.Model
	}
	if (req.Task.Lane == "" || prim == reqP) && rawEff == "" {
		rawEff = req.Effort
	}
	if st.canonReq != "auto" && prim != "" && sel.Lane != prim {
		rawMod, rawEff = envelope.FallbackModel(rawMod, sel.Lane), ""
	}
	spec, err := envelope.EffectiveLaneSpec(sel.Lane, sel.Info, rawMod, rawEff, req.Task.Complexity, "")
	if err != nil {
		fleet.ReleaseParallelLease(lease)
		return ExecRes{ExitCode: 2, Output: err.Error()}, rawMod, rawEff
	}
	extra := VisibleExtraSkills(req.Task.Skills)
	parRun := req.IsParallel || req.StripeIdx != nil
	modeTag, stage := "act", "OODA:ACT"
	if req.Task.ID == "orient" || req.Task.Complexity == "orient" {
		modeTag, stage = "orient", "OODA:ORIENT"
	} else if req.Task.ReadOnly {
		modeTag, stage = "observe", "OODA:OBSERVE"
	} else if parRun {
		modeTag, stage = "act:parallel", "OODA:ACT parallel:"+req.Task.ID
	}
	if req.LogWriter != nil {
		if st.skipped != "" && len(st.attempts) == 0 {
			fmt.Fprintf(req.LogWriter, "[%s] fallback: requested %q -> using %q (%s)\n",
				req.Task.ID, st.canonReq, sel.Lane, st.skipped)
		}
		skSuf := ""
		if len(extra) > 0 {
			skSuf = " | skill=" + strings.Join(extra, ",")
		}
		fmt.Fprintf(req.LogWriter, "▸ [%s] agent=%s (%s:%s) | effort=%s%s\n",
			stage, sel.Lane, sel.Info.CLI, ShortModel(spec.DisplayModel), spec.DisplayEffort, skSuf)
	}
	marker := WorkerMarker{PID: os.Getpid(), TaskID: req.Task.ID, Mode: modeTag, Lane: sel.Lane,
		CLI: sel.Info.CLI, Model: spec.DisplayModel, Effort: spec.DisplayEffort, Skills: extra,
		Cwd: req.Worktree, StartedAt: nowFloatSec()}
	mPath := HudWorkerStart(marker)
	runner := req.Runner
	if runner == nil {
		runner = ExecuteOnce
	}
	res := runner(ExecReq{Provider: sel.Lane, Info: sel.Info, CLIPath: sel.CLIPath, Model: spec.ArgvModel,
		Effort: spec.ArgvEffort, Prompt: st.prompt, Files: req.Task.Files, Worktree: req.Worktree,
		SlotDir: req.SlotDir, TaskID: req.Task.ID, TimeoutSec: st.timeout, ReadOnly: req.Task.ReadOnly,
		MarkerPath: mPath, Marker: marker})
	fleet.ReleaseParallelLease(lease)
	HudWorkerFinish(mPath, LastRunMarker{Mode: modeTag, Lane: sel.Lane, Model: spec.DisplayModel,
		Effort: spec.DisplayEffort, Skills: extra, ExitCode: res.ExitCode, DurationS: res.DurationS})
	_, _ = fleet.RecordUsage(sel.Lane, st.prompt, res.Output, res.ExportInfo, req.QuotaPath)
	st.attempts = append(st.attempts, TaskAttempt{Lane: sel.Lane, Model: spec.DisplayModel,
		Effort: spec.DisplayEffort, ExitCode: res.ExitCode, TimedOut: res.TimedOut,
		DurationS: math.Round(res.DurationS*10) / 10})
	return res, spec.DisplayModel, spec.DisplayEffort
}

func handleTaskOutcome(st *taskState, prov string, res ExecRes) bool {
	var note, msg string
	if fleet.HasErrorPattern(res.Output, res.ExitCode, fleet.QuotaErrorPats) {
		_, _ = fleet.MarkProviderCooldown(prov, "Auto-detected quota/credit error on "+fleet.NowISO(),
			true, res.Output, st.req.QuotaPath)
		note, msg = "quota/credit limit detected", fmt.Sprintf("quota limit hit on %q; switching to fallback lane", prov)
	} else if fleet.HasErrorPattern(res.Output, res.ExitCode, fleet.AuthErrorPats) {
		_, _ = fleet.MarkProviderCooldown(prov, "Authentication required on "+fleet.NowISO(), false, "", st.req.QuotaPath)
		note, msg = "authentication required", fmt.Sprintf("auth error on %q; switching to fallback lane", prov)
	} else if res.ExitCode != 0 && (st.canonReq == "auto" || st.req.SoftPin) &&
		!st.req.NoPivot && !st.pivoted {
		fam := envelope.ModelFamily(prov)
		ex := map[string]bool{prov: true}
		for k, v := range st.tried {
			ex[k] = v
		}
		av := map[string]bool{fam: true}
		for k, v := range st.avoid {
			av[k] = v
		}
		q, qerr := fleet.LoadQuota(st.req.QuotaPath)
		if qerr != nil {
			return false
		}
		probe, _ := envelope.SelectProvider(envelope.SelectReq{RequestedLane: st.canonReq,
			Complexity: st.req.Task.Complexity, Quota: q, Exclude: ex, StripeIndex: st.req.StripeIdx,
			AvoidFamilies: av, DeferAtCap: true, SoftPin: st.req.SoftPin, ResolveCLI: st.req.ResolveCLI})
		if probe.Lane == "" || envelope.ModelFamily(probe.Lane) == fam {
			return false
		}
		st.pivoted, st.avoid[fam] = true, true
		note, msg = fmt.Sprintf("exit=%d mismatch pivot", res.ExitCode),
			fmt.Sprintf("mismatch/timeout on %q; pivoting to fresh model family", prov)
	} else {
		return false
	}
	st.tried[prov] = true
	st.notes = append(st.notes, prov+": "+note)
	if st.req.LogWriter != nil {
		fmt.Fprintf(st.req.LogWriter, "[%s] %s\n", st.req.Task.ID, msg)
	}
	return true
}

// RunTaskWithFallback runs one task with leases, quota-aware fallback, and family pivot retries.
func RunTaskWithFallback(req TaskReq) (TaskRes, error) {
	st, err := initTaskState(req)
	if err != nil {
		return TaskRes{}, err
	}
	for {
		q, qerr := fleet.LoadQuota(req.QuotaPath)
		if qerr != nil {
			return TaskRes{}, qerr
		}
		sel, serr := envelope.SelectProvider(envelope.SelectReq{RequestedLane: st.canonReq,
			Complexity: req.Task.Complexity, Quota: q, Exclude: st.tried, StripeIndex: req.StripeIdx,
			AvoidFamilies: st.avoid, DeferAtCap: st.canDeferCap(), SoftPin: st.req.SoftPin, ResolveCLI: req.ResolveCLI})
		if serr != nil {
			return TaskRes{}, serr
		}
		st.skipped = sel.SkippedNote
		if st.skipped != "" && len(st.attempts) == 0 && len(st.notes) == 0 {
			st.notes = append(st.notes, st.skipped)
		}
		if sel.Lane == "" {
			msg := fmt.Sprintf("error: no available provider for task %s (%s)", req.Task.ID, st.skipped)
			return TaskRes{RequestedLane: st.canonReq, FallbackFrom: strings.Join(st.notes, "; "),
				Attempts: st.attempts, ExitCode: 2, Output: msg}, nil
		}
		lease, outcome, early := acquireTaskLease(st, sel, q)
		if outcome == "retry" {
			continue
		}
		if early != nil {
			early.FallbackFrom = strings.Join(st.notes, "; ")
			return *early, nil
		}
		res, model, effort := runTaskAttempt(st, sel, lease)
		if handleTaskOutcome(st, sel.Lane, res) {
			continue
		}
		var tot float64
		for _, a := range st.attempts {
			tot += a.DurationS
		}
		return TaskRes{RequestedLane: st.canonReq, Lane: sel.Lane, CLI: sel.Info.CLI,
			Model: model, Effort: effort, FallbackFrom: strings.Join(st.notes, "; "),
			Attempts: st.attempts, ExitCode: res.ExitCode, TimedOut: res.TimedOut,
			Output: res.Output, DurationS: math.Round(tot*10) / 10}, nil
	}
}

// ExecReview runs verify.RunReview wired with ExecuteOnce and live HUD marker hooks.
func ExecReview(req verify.ReviewReq) verify.ReviewResult {
	if req.OnStart == nil {
		req.OnStart = func(mode, tid, lane, cli, mod, eff string, sk []string, cwd string) string {
			return HudWorkerStart(WorkerMarker{PID: os.Getpid(), TaskID: tid, Mode: mode, Lane: lane,
				CLI: cli, Model: mod, Effort: eff, Skills: sk, Cwd: cwd, StartedAt: nowFloatSec()})
		}
	}
	if req.OnFinish == nil {
		req.OnFinish = func(mark, mode, lane, mod, eff string, sk []string, rc int, dur float64) {
			HudWorkerFinish(mark, LastRunMarker{Mode: mode, Lane: lane, Model: mod,
				Effort: eff, Skills: sk, ExitCode: rc, DurationS: dur})
		}
	}
	if req.Runner == nil {
		req.Runner = func(r verify.ReviewExecReq) verify.ReviewExecRes {
			m := WorkerMarker{PID: os.Getpid(), TaskID: r.TaskID, Mode: "act:verify", Lane: r.Provider,
				CLI: r.Info.CLI, Model: r.Model, Effort: r.Effort, Cwd: r.TargetDir, StartedAt: nowFloatSec()}
			if raw, rerr := os.ReadFile(r.MarkerPath); rerr == nil {
				_ = json.Unmarshal(raw, &m)
			}
			res := ExecuteOnce(ExecReq{Provider: r.Provider, Info: r.Info, CLIPath: r.CLIPath,
				Model: r.Model, Effort: r.Effort, Prompt: r.Prompt, Worktree: r.TargetDir, SlotDir: r.SlotDir,
				TaskID: r.TaskID, TimeoutSec: r.TimeoutSec, ReadOnly: true, ReviewMode: r.ReviewMode,
				MarkerPath: r.MarkerPath, Marker: m})
			return verify.ReviewExecRes{ExitCode: res.ExitCode, Output: res.Output,
				TimedOut: res.TimedOut, ExportInfo: res.ExportInfo}
		}
	}
	return verify.RunReview(req)
}

func extractJSONErr(ev, resMap map[string]any) (string, bool, bool) {
	status, _ := ev["status"].(string)
	if status == "" {
		status, _ = resMap["status"].(string)
	}
	typ, _ := ev["type"].(string)
	code, hasCode := jsonNum(ev["status"])
	if !hasCode {
		code, hasCode = jsonNum(ev["status_code"])
	}
	errSet := (ev["error"] != nil && ev["error"] != "" && ev["error"] != false && ev["error"] != 0.0 && ev["error"] != 0) ||
		(resMap["error"] != nil && resMap["error"] != "" && resMap["error"] != false &&
			resMap["error"] != 0.0 && resMap["error"] != 0)
	topType := typ == "" || strings.EqualFold(typ, "error") || typ == "result" || typ == "end" ||
		strings.HasSuffix(typ, ".failed") || strings.HasSuffix(typ, ".error") || strings.HasSuffix(typ, ".completed")
	isErr := strings.EqualFold(status, "error") || strings.EqualFold(status, "failed") ||
		strings.EqualFold(typ, "error") || ev["is_error"] == true || (topType && errSet) || (hasCode && code >= 400)
	if !isErr {
		return "", false, false
	}
	errObj, _ := ev["error"].(map[string]any)
	msg := ""
	for _, v := range []any{ev["error"], errObj["message"], errObj["type"],
		ev["message"], ev["response"], ev["result"], resMap["error"]} {
		if s, ok := v.(string); ok && s != "" {
			msg = s
			break
		}
	}
	if msg == "" && hasCode {
		msg = fmt.Sprintf("HTTP status %d", int(code))
	}
	qKill := code == 429 || code == 402 || IsLiveQuotaLine(msg) || fleet.HasErrorPattern(msg, 1, fleet.AuthErrorPats)
	return msg, true, qKill
}

// IsLiveQuotaLine detects unambiguous quota/rate-limit lines without false-positive bare "429" matches.
func IsLiveQuotaLine(raw string) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "{") {
		return fleet.HasErrorPattern(s, 1, []string{"insufficient_quota", "quota_exceeded",
			"rate_limit_exceeded", "budget_exhausted", `"status":429`, `"status_code":429`, `"code":429`})
	}
	if !fleet.HasErrorPattern(s, 1, []string{"error", "fatal", "429", "402", "exceeded", "exhausted", "insufficient_quota"}) {
		return false
	}
	pats := []string{"insufficient_quota", "quota exceeded", "quota_exceeded", "quota exhausted", "rate_limit_exceeded",
		"rate limit exceeded", "rate limit reached", "credits exhausted", "out of credits", "budget_exhausted",
		"http 429", "status 429", "error 429", "429 too many requests"}
	return fleet.HasErrorPattern(s, 1, pats)
}
