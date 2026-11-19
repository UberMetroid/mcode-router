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
