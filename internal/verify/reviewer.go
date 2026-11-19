package verify

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/UberMetroid/mcode-router/internal/envelope"
	"github.com/UberMetroid/mcode-router/internal/fleet"
)

// ReviewExecReq parameterizes one headless reviewer execution.
type ReviewExecReq struct {
	Provider, CLIPath, Model, Effort, Prompt, TargetDir, SlotDir string
	TaskID, ReviewMode, MarkerPath                               string
	Info                                                         *fleet.ProviderConfig
	TimeoutSec                                                   int
}

// ReviewExecRes captures raw output and exit status from one reviewer run.
type ReviewExecRes struct {
	ExitCode   int
	Output     string
	TimedOut   bool
	ExportInfo *fleet.ExportUsage
}

// ReviewRunner executes one review attempt cleanly without circular imports.
type ReviewRunner func(req ReviewExecReq) ReviewExecRes

// ReviewReq configures calibrated cross-model diff review with fallback.
type ReviewReq struct {
	TargetDir, SlotDir, AuthorLane, ReqLane string
	Model, Effort, QuotaPath                string
	NoCache                                 bool
	DiffCtx                                 DiffContext
	ScopeViols, Declared, AuthorLanes       []string
	TimeoutSec                              int
	LogWriter                               io.Writer
	Exclude                                 map[string]bool
	Runner                                  ReviewRunner
	ResolveCLI                              func(string) string
	OnStart                                 func(mode, tid, lane, cli, mod, eff string, sk []string, cwd string) string
	OnFinish                                func(mark, mode, lane, mod, eff string, sk []string, rc int, dur float64)
}

// ReviewResult reports final review verdict, chosen lane, model, and output.
type ReviewResult struct {
	Ran, TimedOut                bool
	Lane, Model, Verdict, Reason string
	ExitCode                     int
	Output                       string
}

func hasAltReviewer(q *fleet.QuotaState, tried, avoid map[string]bool, cur string, res func(string) string) bool {
	ex := map[string]bool{cur: true}
	maps.Copy(ex, tried)
	alt, _ := envelope.SelectProvider(envelope.SelectReq{
		RequestedLane: "auto", Complexity: "review", Quota: q, Exclude: ex,
		AvoidFamilies: avoid, StrictAvoid: true, DeferAtCap: true, ResolveCLI: res,
	})
	return alt.Lane != "" && !avoid[envelope.ModelFamily(alt.Lane)]
}

func reviewLease(prov string, info *fleet.ProviderConfig, q *fleet.QuotaState,
	tried, avoid map[string]bool, timeout int, req ReviewReq) (*os.File, bool) {
	if info == nil || info.MaxParallel == nil || *info.MaxParallel <= 0 {
		return nil, false
	}
	maxP := *info.MaxParallel
	if lease, _, _ := fleet.AcquireParallelLease(prov, maxP, false, 0); lease != nil {
		return lease, false
	}
	if hasAltReviewer(q, tried, avoid, prov, req.ResolveCLI) {
		tried[prov] = true
		return nil, true
	}
	if req.LogWriter != nil {
		fmt.Fprintf(req.LogWriter, "[verify] waiting for free %s slot (%d/%d busy)\n", prov, maxP, maxP)
	}
	lease, timedOut, lerr := fleet.AcquireParallelLease(prov, maxP, true, timeout)
	if timedOut || lerr != nil {
		tried[prov] = true
		return nil, true
	}
	return lease, false
}

func resolveReviewModel(prov string, req ReviewReq) string {
	if req.Model != "" {
		return req.Model
	}
	if envMod := fleet.ReviewModelEnv(); prov == "mcode" && envMod != "" && envMod != "cli-default" {
		return envMod
	}
	return ""
}

func invokeReviewer(req ReviewReq, sel envelope.SelectRes, lease *os.File, prompt string) (ReviewExecRes, string) {
	rawM, rawEff := resolveReviewModel(sel.Lane, req), req.Effort
	if prim := strings.Split(req.ReqLane, ",")[0]; req.ReqLane != "" && req.ReqLane != "auto" {
		if cp, err := fleet.NormalizeLane(prim, true); err == nil && cp != "" {
			prim = cp
		}
		if sel.Lane != prim {
			rawM, rawEff = envelope.FallbackModel(rawM, sel.Lane), ""
			if req.LogWriter != nil {
				fmt.Fprintf(req.LogWriter, "[verify] fallback: requested %q -> %q (off-family/cooldown)\n", prim, sel.Lane)
			}
		}
	}
	spec, specErr := envelope.EffectiveLaneSpec(sel.Lane, sel.Info, rawM, rawEff, "review", "")
	if specErr != nil {
		fleet.ReleaseParallelLease(lease)
		return ReviewExecRes{ExitCode: 2, Output: specErr.Error()}, rawM
	}
	taskID := "review-" + filepath.Base(req.SlotDir)
	if req.LogWriter != nil {
		fmt.Fprintf(req.LogWriter, "▸ [OODA:ACT verify] agent=%s (%s:%s) | effort=%s\n",
			sel.Lane, sel.Info.CLI, filepath.Base(spec.DisplayModel), spec.DisplayEffort)
	}
	marker, dm, de, revExit, start := "", spec.DisplayModel, spec.DisplayEffort, -1, time.Now()
	defer func() {
		fleet.ReleaseParallelLease(lease)
		if req.OnFinish != nil {
			req.OnFinish(marker, "act:verify", sel.Lane, dm, de, nil, revExit, time.Since(start).Seconds())
		}
	}()
	if req.OnStart != nil {
		marker = req.OnStart("act:verify", taskID, sel.Lane, sel.Info.CLI, dm, de, nil, req.TargetDir)
	}
	rmode, callPrompt := "inline", prompt
	if sel.Lane == "mcode" && req.DiffCtx.Source == "working-tree" && len(req.DiffCtx.Diff) <= NativeReviewMaxBytes {
		rmode, callPrompt = "native", BuildNativeReviewNote(req.TargetDir, req.DiffCtx, req.ScopeViols, req.Declared)
	}
	res := req.Runner(ReviewExecReq{
		Provider: sel.Lane, Info: sel.Info, CLIPath: sel.CLIPath, Model: spec.ArgvModel, Effort: spec.ArgvEffort,
		Prompt: callPrompt, TargetDir: req.TargetDir, SlotDir: req.SlotDir, TaskID: taskID,
		TimeoutSec: max(1, req.TimeoutSec), ReviewMode: rmode, MarkerPath: marker,
	})
	revExit = res.ExitCode
	return res, spec.DisplayModel
}

func shouldRetryReview(sel envelope.SelectRes, res ReviewExecRes, verdict string,
	q *fleet.QuotaState, tried, avoid map[string]bool, req ReviewReq) bool {
	if verdict == "REJECT" || (!res.TimedOut && res.ExitCode == 0 && verdict != "UNKNOWN") ||
		!hasAltReviewer(q, tried, avoid, sel.Lane, req.ResolveCLI) {
		return false
	}
	tried[sel.Lane], avoid[envelope.ModelFamily(sel.Lane)] = true, true
	if req.LogWriter != nil {
		fmt.Fprintf(req.LogWriter, "[verify] retrying off-family after %s (exit=%d timedOut=%t verdict=%s)\n",
			sel.Lane, res.ExitCode, res.TimedOut, verdict)
	}
	return true
}

func handleReviewErr(lane, prompt, verdict string, res ReviewExecRes, qPath string, tried map[string]bool) bool {
	u := fleet.ExportUsage{}
	if res.ExportInfo != nil {
		u = *res.ExportInfo
	}
	u.Failed = res.ExitCode != 0 || res.TimedOut || verdict == "UNKNOWN"
	_, _ = fleet.RecordUsage(lane, prompt, res.Output, &u, qPath)
	qHit := fleet.HasErrorPattern(res.Output, res.ExitCode, fleet.QuotaErrorPats)
	if !qHit && !fleet.HasErrorPattern(res.Output, res.ExitCode, fleet.AuthErrorPats) {
		return false
	}
	kind := map[bool]string{true: "quota/rate-limit", false: "auth"}[qHit]
	_, _ = fleet.MarkProviderCooldown(lane, "Auto-detected "+kind+" error on "+fleet.NowISO(), qHit, res.Output, qPath)
	tried[lane] = true
	return true
}

func reviewPreflight(req ReviewReq) (ReviewResult, bool) {
	if req.Runner == nil {
		return ReviewResult{Verdict: "SKIPPED", Reason: "no review runner configured"}, true
	}
	if len(req.ScopeViols) > 0 {
		msg := "[P0] scope violation: " + strings.Join(req.ScopeViols, ", ") + "\nVERDICT: REJECT - scope violation"
		return ReviewResult{Verdict: "REJECT", ExitCode: 1, Reason: "scope violation", Output: msg}, true
	}
	if hf := CheckDiffHygiene(req.DiffCtx); len(hf) > 0 {
		msg := FormatHygieneReport(hf) + "\nVERDICT: REJECT - diff hygiene violation"
		return ReviewResult{Verdict: "REJECT", ExitCode: 1, Reason: "diff hygiene violation", Output: msg}, true
	}
	return ReviewResult{}, false
}

// RunReview performs calibrated cross-model read-only diff review with off-family fallback.
func RunReview(req ReviewReq) ReviewResult {
	if pre, done := reviewPreflight(req); done {
		return pre
	}
	if req.TimeoutSec <= 0 {
		req.TimeoutSec = fleet.ReviewTimeout
	}
	avoid, tried, preSnap := map[string]bool{}, make(map[string]bool), SnapshotRepo(req.TargetDir, req.QuotaPath)
	maps.Copy(tried, req.Exclude)
	for _, a := range append([]string{req.AuthorLane}, req.AuthorLanes...) {
		if a != "" {
			avoid[envelope.ModelFamily(a)] = true
		}
	}
	req.DiffCtx.Diff = PrioritizeDiff(req.DiffCtx.Diff, req.Declared, MaxDiffRunes)
	prompt := BuildReviewPrompt(req.TargetDir, req.DiffCtx, req.ScopeViols, req.Declared)
	cKey, hit, ok := TryReviewCache(req, prompt, preSnap)
	if ok {
		return hit
	}
	for {
		q, qerr := fleet.LoadQuota(req.QuotaPath)
		if qerr != nil {
			return ReviewResult{Model: fleet.ReviewModelEnv(), Verdict: "SKIPPED", ExitCode: 2, Reason: qerr.Error()}
		}
		sel, err := envelope.SelectProvider(envelope.SelectReq{
			RequestedLane: req.ReqLane, Complexity: "review", Quota: q, Exclude: tried,
			AvoidFamilies: avoid, StrictAvoid: true, DeferAtCap: true, ResolveCLI: req.ResolveCLI,
		})
		if err != nil || sel.Lane == "" {
			if err != nil {
				sel.SkippedNote = err.Error()
			}
			return ReviewResult{Model: fleet.ReviewModelEnv(), Verdict: "SKIPPED", Reason: sel.SkippedNote}
		}
		lease, retry := reviewLease(sel.Lane, sel.Info, q, tried, avoid, req.TimeoutSec, req)
		if retry {
			continue
		}
		res, model := invokeReviewer(req, sel, lease, prompt)
		if drift := SnapshotDrift(preSnap, SnapshotRepo(req.TargetDir, req.QuotaPath)); len(drift) > 0 {
			msg := "[P0] zero-trust: reviewer mutated: " + strings.Join(drift, ", ") + "\nVERDICT: REJECT"
			return ReviewResult{Ran: true, Lane: sel.Lane, Model: model, Verdict: "REJECT", ExitCode: 1,
				Reason: "reviewer mutated workspace", Output: msg}
		}
		verdict := ParseReviewVerdict(res.Output)
		if nv, report, ok := ParseNativeVerdict(res.Output); ok {
			verdict, res.Output = nv, report
		}
		if res.ExitCode != 0 && verdict == "ACCEPT" {
			verdict = "UNKNOWN"
		}
		if handleReviewErr(sel.Lane, prompt, verdict, res, req.QuotaPath, tried) {
			continue
		}
		if shouldRetryReview(sel, res, verdict, q, tried, avoid, req) {
			continue
		}
		SaveReviewCache(cKey, req, sel.Lane, model, verdict, res)
		return ReviewResult{Ran: true, Lane: sel.Lane, Model: model, Verdict: verdict,
			ExitCode: res.ExitCode, TimedOut: res.TimedOut, Output: res.Output}
	}
}
