package cli

import (
	"fmt"
	"github.com/UberMetroid/mcode-router/internal/dispatch"
	"github.com/UberMetroid/mcode-router/internal/envelope"
	"github.com/UberMetroid/mcode-router/internal/fleet"
	"io"
	"os"
	"strings"
	"time"
)

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
