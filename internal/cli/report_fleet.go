package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/UberMetroid/mcode-router/internal/dispatch"
	"github.com/UberMetroid/mcode-router/internal/envelope"
	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/ui"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

func clipStr(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen])
}

func nonEmptyOr(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

func formatCommas(n int) string {
	s, out := fmt.Sprintf("%d", n), ""
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	for len(s) > 3 {
		out, s = ","+s[len(s)-3:]+out, s[:len(s)-3]
	}
	if neg {
		return "-" + s + out
	}
	return s + out
}

func statusRow(
	name string, info *fleet.ProviderConfig, nowUTC time.Time, lc envelope.LaneCap, musePayg bool,
) string {
	cliName, cliErr := fleet.LaneCLIName(name, info.CLI)
	binNote := "MISSING"
	if cliErr == nil {
		binNote = nonEmptyOr(fleet.ResolveCLI(cliName), "MISSING")
	}
	eff, mod := lc.EffectiveEffort, lc.EffectiveModel
	if info.Effort != nil && *info.Effort != "" {
		eff = *info.Effort
	}
	if info.Model != nil && *info.Model != "" {
		mod = *info.Model
	}
	eff, mod = nonEmptyOr(eff, "default"), nonEmptyOr(mod, "default")
	resetTS := info.NextResetAt
	if info.CooldownUntil != nil && *info.CooldownUntil != "" {
		resetTS = info.CooldownUntil
	}
	resetCD, detail := fleet.FormatCountdown(resetTS, nowUTC), binNote
	if info.Status != "active" {
		why := derefOr(info.Reason, "cooldown")
		if info.CooldownUntil != nil && resetCD != "" {
			detail = fmt.Sprintf("%s | resets in %s | %s", binNote, resetCD, why)
		} else if info.ManualDisable {
			detail = fmt.Sprintf("%s | [manual] %s", binNote, why)
		} else {
			detail = fmt.Sprintf("%s | %s", binNote, why)
		}
	} else if resetCD != "" {
		detail = fmt.Sprintf("%s | cycle resets in %s", binNote, resetCD)
	}
	if name == "muse" && musePayg {
		detail += " | [PAYG api_key — skipped in auto]"
	}
	if _, badge := envelope.LaneTariff(name, info, mod, nowUTC); badge != "" {
		detail += " | [" + badge + "]"
	}
	if info.MaxParallel != nil && *info.MaxParallel > 0 {
		mp := *info.MaxParallel
		detail += fmt.Sprintf(" | [max %d parallel, %d/%d busy]", mp, fleet.CountActiveLeases(name, mp), mp)
	}
	return fmt.Sprintf("%-13s %-9s %-10s %-9s %-9s %-28s %s",
		name, fleet.SafeText(info.Status), fleet.SafeText(info.Tier),
		clipStr(fleet.SafeText(eff), 9),
		fleet.SafeText(info.CLI), clipStr(dispatch.ShortModel(fleet.SafeText(mod)), 28),
		fleet.SafeText(detail))
}

// ReportStatus prints fleet readiness or refreshes CLI capability discovery caches.
func ReportStatus(w io.Writer, refresh bool, cliOpts ...CLIOpts) int {
	if w == nil {
		w = os.Stdout
	}
	co := firstOpts(cliOpts)
	if co.Live && !co.Status && !co.Discover {
		ui.PrintLiveTelemetry(w)
		return 0
	}
	q, err := fleet.LoadQuota("")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	caps := envelope.DiscoverFleetCaps(envelope.DiscoverOpts{Quota: q, ForceRefresh: refresh})
	skills, nowUTC := worktree.DiscoverSkills(""), time.Now().UTC()
	if refresh {
		fmt.Fprintf(w, "Discovery cache    : %s (refreshed)\n", fleet.DiscoveryFilePath())
	} else {
		chain, pool := q.DefaultFallbackChain, q.ParallelStripePool
		if len(chain) == 0 {
			chain = fleet.DefaultPool
		}
		if len(pool) == 0 {
			pool = fleet.StripePool
		}
		fmt.Fprintf(w, "Quota state        : %s (v%d, updated: %s)\n",
			fleet.SafeText(fleet.QuotaFilePath("")), q.Version,
			fleet.SafeText(derefOr(q.UpdatedAt, "")))
		fmt.Fprintf(w, "Drain capped first : %t (expiring clocks, 0.5x off-peak & short windows first)\n",
			q.DrainCappedFirst)
		fmt.Fprintf(w, "Parallel stripe    : %s\nFallback chain     : %s\n\n",
			fleet.SafeText(strings.Join(pool, " -> ")),
			fleet.SafeText(strings.Join(chain, " -> ")))
	}
	fmt.Fprintf(w, "%-13s %-9s %-10s %-9s %-9s %-28s %s\n",
		"LANE", "STATUS", "TIER", "EFFORT", "CLI", "MODEL", "RESET / BINARY")
	fmt.Fprintln(w, strings.Repeat("-", 119))
	musePayg := fleet.CachedMuseProbe(900).PayAsYouGo
	for _, name := range fleet.ProviderOrder {
		if info := q.Providers[name]; info != nil {
			fmt.Fprintln(w, statusRow(name, info, nowUTC, caps[name], musePayg))
		}
	}
	if !refresh {
		defP, _ := envelope.SelectProvider(envelope.SelectReq{RequestedLane: "auto", Quota: q})
		fastP, _ := envelope.SelectProvider(envelope.SelectReq{RequestedLane: "auto", Complexity: "fast", Quota: q})
		deepP, _ := envelope.SelectProvider(envelope.SelectReq{RequestedLane: "auto", Complexity: "deep", Quota: q})
		fmt.Fprintf(w, "\nEffective default/heavy lane : %s\nEffective fast/recon lane    : %s\n",
			nonEmptyOr(defP.Lane, "NONE"), nonEmptyOr(fastP.Lane, "NONE"))
		fmt.Fprintf(w, "Effective deep/arch lane     : %s\n", nonEmptyOr(deepP.Lane, "NONE"))
		revLbl := fleet.ReviewModelEnv()
		if revLbl == "cli-default" {
			revLbl = "cli default unless overridden"
		}
		fmt.Fprintf(w, "Auto-review mcode (%s): %s\n", revLbl, nonEmptyOr(fleet.ResolveCLI("mcode"), "MISSING"))
		cwd, ok := ResolveCWD(co, os.Stderr)
		if !ok {
			cwd = "."
		}
		printStageMatrix(w, cwd, co, q)
		ui.PrintLiveTelemetry(w)
	}
	printDiscoveryFooter(w, caps, skills, refresh)
	return 0
}

func usageRow(
	name string, info *fleet.ProviderConfig, regimes map[string]string, nowUTC time.Time, activeMod string, musePayg bool,
) string {
	cycStr, pref := "creds", "~"
	if len(info.Windows) > 0 {
		cycStr = fleet.SafeText(strings.Join(info.Windows, "+"))
	} else if info.CycleHours != nil {
		cycStr = fmt.Sprintf("%gh", *info.CycleHours)
	}
	if info.CycleTokensExact || info.CycleTokens == 0 {
		pref = ""
	}
	tokStr, costStr := pref+formatCommas(info.CycleTokens), "-"
	if info.CycleTokenBudget != nil && *info.CycleTokenBudget > 0 {
		tokStr += "/" + formatCommas(*info.CycleTokenBudget)
	}
	if info.CycleCostBudget != nil && *info.CycleCostBudget > 0 {
		costStr = fmt.Sprintf("$%.2f/$%.2f", info.CycleCostUSD, *info.CycleCostBudget)
	} else if info.CycleCostUSD > 0 {
		costStr = fmt.Sprintf("$%.4f", info.CycleCostUSD)
	}
	resetTS := info.NextResetAt
	if info.CooldownUntil != nil && *info.CooldownUntil != "" {
		resetTS = info.CooldownUntil
	}
	resetCD := fleet.FormatCountdown(resetTS, nowUTC)
	if resetCD == "" {
		resetCD = "idle"
		if info.Tier == "credits" && info.Status != "active" {
			resetCD = "top-up"
		} else if info.ManualDisable {
			resetCD = "manual"
		}
	}
	reg := nonEmptyOr(regimes[name], "-")
	note := ""
	if info.Model != nil && *info.Model != "" {
		activeMod = *info.Model
	}
	if name == "muse" && musePayg {
		reg, note = "PAYG", "api_key active (skipped in auto)"
	} else if info.Reason != nil && *info.Reason != "" {
		note = *info.Reason
	} else if info.CycleRuns == 0 && info.Status == "active" {
		note = "fresh"
	} else if info.Model != nil {
		note = dispatch.ShortModel(*info.Model)
	}
	if _, badge := envelope.LaneTariff(name, info, activeMod, nowUTC); badge != "" {
		note = strings.TrimSpace("[" + badge + "] " + note)
	}
	return fmt.Sprintf("%-13s %-9s %-9s %-7s %-5d %-14s %-9s %-12s %s",
		name, fleet.SafeText(info.Status), fleet.SafeText(reg), cycStr, info.CycleRuns,
		tokStr, costStr, resetCD, fleet.SafeText(note))
}

// DoUsage prints cycle token/cost telemetry, quota state, and live muse usage probe.
func DoUsage(w io.Writer) int {
	if w == nil {
		w = os.Stdout
	}
	museProbe := fleet.ProbeMuseUsage(10)
	q, err := fleet.LoadQuota("")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	caps := envelope.DiscoverFleetCaps(envelope.DiscoverOpts{Quota: q})
	nowUTC := time.Now().UTC()
	em := envelope.ComputeEMEnvelope(q, nowUTC)
	fmt.Fprintf(w, "Cycle Usage Telemetry (%s, v%d)\n\n", fleet.QuotaFilePath(""), q.Version)
	fmt.Fprintf(w, "%-13s %-9s %-9s %-7s %-5s %-14s %-9s %-12s %s\n",
		"LANE", "STATUS", "STATE", "WINDOW", "RUNS", "TOKENS", "COST", "RESETS IN", "NOTE")
	fmt.Fprintln(w, strings.Repeat("-", 116))
	for _, name := range fleet.ProviderOrder {
		if info := q.Providers[name]; info != nil {
			fmt.Fprintln(w, usageRow(name, info, em.Regimes, nowUTC, caps[name].EffectiveModel, museProbe.PayAsYouGo))
		}
	}
	fastCap := fleet.DevinFastMaxPar
	if fi := q.Providers["devin-fast"]; fi != nil && fi.MaxParallel != nil && *fi.MaxParallel > 0 {
		fastCap = *fi.MaxParallel
	}
	posture := "NORMAL (using expiring & 0.5x off-peak pools first)"
	if em.ConserveCapped {
		posture = "CONSERVING (<=1 capped lane left, wake >45m; routing routine work to free/high-cap pools)"
	}
	fmt.Fprintf(w, "\nQuota posture           : %s | devin-fast slots: %d/%d busy\n",
		posture, fleet.CountActiveLeases("devin-fast", fastCap), fastCap)
	fmt.Fprintf(w, "Live probe [muse usage] : %s\n", museProbe.Summary)
	fmt.Fprintln(w, "Headless probes         : grok/devin/mcode omit remaining balance; "+
		"vibe reports exact per-run tokens")
	return 0
}
