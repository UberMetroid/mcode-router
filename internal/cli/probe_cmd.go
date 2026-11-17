package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/UberMetroid/mcode-router/internal/dispatch"
	"github.com/UberMetroid/mcode-router/internal/envelope"
	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/verify"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

const probeTTLSecs = 900.0

func shouldSkipProbe(lane string, info *fleet.ProviderConfig, nowUTC time.Time, musePayg bool) string {
	if info == nil {
		return "not configured"
	}
	if info.Tier == "credits" {
		return "paid credits (opt-in --lane only; never probed)"
	}
	if info.ManualDisable {
		return "manually disabled"
	}
	if fleet.ResolveCLI(info.CLI) == "" {
		return "cli not installed"
	}
	if lane == "muse" && musePayg {
		return "PAYG api_key active (skipped)"
	}
	if info.Status == "cooldown" {
		rem := fleet.FormatCountdown(info.CooldownUntil, nowUTC)
		if rem != "" && rem != "due now" {
			return fmt.Sprintf("cooldown (%s remaining)", rem)
		}
	}
	if info.LastProbedAt != nil && *info.LastProbedAt != "" {
		if lp, ok := fleet.ParseISOUTC(*info.LastProbedAt); ok && nowUTC.Sub(lp).Seconds() < probeTTLSecs {
			return fmt.Sprintf("fresh (%ds ago)", int(nowUTC.Sub(lp).Seconds()))
		}
	}
	return ""
}

func probeOneLane(lane string, info *fleet.ProviderConfig, cwd string) (string, bool) {
	cliPath := fleet.ResolveCLI(info.CLI)
	if cliPath == "" {
		return "cli missing", false
	}
	spec, err := envelope.EffectiveLaneSpec(lane, info, "", "", "recon", "")
	if err != nil {
		return err.Error(), false
	}
	slot, serr := worktree.EnsureSlot("probe-" + lane)
	if serr != nil {
		return serr.Error(), false
	}
	res := dispatch.ExecuteOnce(dispatch.ExecReq{
		Provider: lane, Info: info, CLIPath: cliPath,
		Model: spec.ArgvModel, Effort: spec.ArgvEffort,
		Prompt: "Reply with 1.", Worktree: cwd, SlotDir: slot,
		TaskID: "probe-" + lane, TimeoutSec: 12, ReadOnly: true,
	})
	qHit := fleet.HasErrorPattern(res.Output, res.ExitCode, fleet.QuotaErrorPats)
	aHit := fleet.HasErrorPattern(res.Output, res.ExitCode, fleet.AuthErrorPats)
	if qHit || aHit {
		kind := "auth error"
		if qHit {
			kind = "quota/rate-limit error"
		}
		updated, werr := fleet.MarkProviderCooldown(
			lane, "Live probe "+kind+" on "+fleet.NowISO(), qHit, res.Output, "",
		)
		if werr != nil {
			return fmt.Sprintf("cooldown (%s; quota write error: %v)", kind, werr), false
		}
		if updated != nil {
			cd := fleet.FormatCountdown(updated.CooldownUntil, time.Now().UTC())
			return fmt.Sprintf("cooldown (%s, resets %s)", kind, nonEmptyOr(cd, "unknown")), false
		}
		return "cooldown (" + kind + ")", false
	}
	if res.ExitCode == 0 {
		if _, _, werr := fleet.SetProviderStatus(lane, "active", "probed", nil, false, ""); werr != nil {
			return fmt.Sprintf("probe ok, quota write failed: %v", werr), false
		}
		return fmt.Sprintf("active (%s, %.1fs)", dispatch.ShortModel(spec.DisplayModel), res.DurationS), true
	}
	return fmt.Sprintf("error exit=%d (%.1fs)", res.ExitCode, res.DurationS), false
}

// DoProbe runs lightweight read-only provider canaries and updates quota.json.
func DoProbe(outW, errW io.Writer) int {
	if outW == nil {
		outW = os.Stdout
	}
	if errW == nil {
		errW = os.Stderr
	}
	mp := fleet.ProbeMuseUsage(5)
	fmt.Fprintf(outW, "muse usage --json : %s\n", nonEmptyOr(mp.Summary, "unavailable"))
	q, err := fleet.LoadQuota("")
	if err != nil {
		fmt.Fprintln(errW, err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	nowUTC, anyFail := time.Now().UTC(), false
	targets := []string{"mcode", "grok", "mistral", "devin-fast", "muse"}
	for _, lane := range targets {
		info := q.Providers[lane]
		if why := shouldSkipProbe(lane, info, nowUTC, mp.PayAsYouGo); why != "" {
			fmt.Fprintf(outW, "  %-11s : skipped (%s)\n", lane, why)
			continue
		}
		msg, ok := probeOneLane(lane, info, cwd)
		if !ok {
			anyFail = true
		}
		fmt.Fprintf(outW, "  %-11s : %s\n", lane, msg)
	}
	if anyFail {
		return 1
	}
	return 0
}

// DoHygiene runs a deterministic 0-token P0 secret & P1 hardening scan.
func DoHygiene(targetArg string, opts CLIOpts, outW, errW io.Writer) int {
	if outW == nil {
		outW = os.Stdout
	}
	if errW == nil {
		errW = os.Stderr
	}
	if targetArg == "." && opts.Manifest != "" {
		targetArg = opts.Manifest
	}
	if targetArg == "" {
		targetArg = "."
	}
	absT, err := filepath.Abs(targetArg)
	if err != nil {
		fmt.Fprintf(errW, "error: invalid target %q: %v\n", targetArg, err)
		return 2
	}
	if st, serr := os.Stat(absT); serr != nil || !st.IsDir() {
		fmt.Fprintf(errW, "error: hygiene target is not a directory: %s\n", absT)
		return 2
	}
	_, _, files, err := validateRunArgs(opts, fleet.ReviewTimeout, true)
	if err != nil {
		fmt.Fprintln(errW, err)
		return 2
	}
	findings, err := verify.ScanRepoHygiene(absT, files)
	if err != nil {
		fmt.Fprintf(errW, "error: %v\n", err)
		return 2
	}
	fmt.Fprintln(outW, verify.FormatHygieneReport(findings))
	if len(findings) > 0 {
		return 1
	}
	return 0
}

// firstOpts returns the first CLI option set, or the zero value when none was given.
func firstOpts(opts []CLIOpts) CLIOpts {
	if len(opts) == 0 {
		return CLIOpts{}
	}
	return opts[0]
}

// readActiveWorkers loads the live worker markers written by running dispatches.
func readActiveWorkers() []dispatch.WorkerMarker {
	dir := filepath.Join(fleet.ScratchDirPath(), "active")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []dispatch.WorkerMarker
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(dir, ent.Name()))
		if rerr != nil {
			continue
		}
		var m dispatch.WorkerMarker
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt < out[j].StartedAt })
	return out
}

// readLastWorker loads the marker for the most recently completed dispatch.
func readLastWorker() (dispatch.LastRunMarker, bool) {
	raw, err := os.ReadFile(filepath.Join(fleet.ScratchDirPath(), "last.json"))
	if err != nil {
		return dispatch.LastRunMarker{}, false
	}
	var m dispatch.LastRunMarker
	if json.Unmarshal(raw, &m) != nil {
		return dispatch.LastRunMarker{}, false
	}
	return m, true
}

// printLiveWorkers renders live dispatch telemetry: active workers and the last run.
func printLiveWorkers(w io.Writer) {
	fmt.Fprintln(w, "\nLive Worker Telemetry:")
	active := readActiveWorkers()
	if len(active) == 0 {
		fmt.Fprintln(w, "  (no active workers)")
	}
	for _, m := range active {
		phase := strings.TrimSpace(m.Phase)
		if phase == "" {
			phase = "idle"
		}
		rate := fmt.Sprintf("%.1f", m.TokS)
		if !m.TokExact {
			rate = "~" + rate
		}
		elapsed := 0.0
		if m.StartedAt > 0 {
			elapsed = time.Since(time.Unix(0, int64(m.StartedAt*1e9))).Seconds()
		}
		fmt.Fprintf(w, "  ACTIVE lane=%s cli=%s model=%s effort=%s phase=%s tok/s=%s elapsed=%.1fs\n",
			m.Lane, m.CLI, dispatch.ShortModel(m.Model), m.Effort, phase, rate, elapsed)
		fmt.Fprintf(w, "         task=%s cwd=%s skills=%s\n",
			m.TaskID, m.Cwd, strings.Join(dispatch.VisibleExtraSkills(m.Skills), ","))
	}
	if last, ok := readLastWorker(); ok {
		fmt.Fprintf(w, "  LAST   lane=%s model=%s effort=%s exit=%d duration=%.1fs\n",
			last.Lane, dispatch.ShortModel(last.Model), last.Effort, last.ExitCode, last.DurationS)
	}
}
