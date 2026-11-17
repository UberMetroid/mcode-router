package fleet

import (
	"fmt"
	"math"
	"strings"
	"time"
)

func mutateProviderLocked(qPath, rawName string, create bool,
	fn func(*ProviderConfig) error) (string, *ProviderConfig, error) {
	canon, err := NormalizeLane(rawName, false)
	if err != nil {
		return "", nil, err
	}
	if create {
		if _, lerr := LoadQuota(qPath); lerr != nil {
			return "", nil, lerr
		}
	}
	var updated ProviderConfig
	err = WithQuotaLock(qPath, func(path string) error {
		q, rerr := readAndPrepLocked(path)
		if rerr != nil {
			return rerr
		}
		entry, ok := q.Providers[canon]
		if !ok || entry == nil {
			return UsageErr("error: provider %q not defined in %s", canon, path)
		}
		if ferr := fn(entry); ferr != nil {
			return ferr
		}
		updated = *entry
		return SaveQuotaLocked(q, path)
	})
	return canon, &updated, err
}

func applyInnerWake(entry *ProviderConfig, now time.Time) string {
	if entry.NextResetAt != nil {
		if dt, ok := ParseISOUTC(*entry.NextResetAt); ok && dt.After(now.UTC()) && dt.Sub(now.UTC()) <= 6*time.Hour {
			entry.CooldownUntil = Ptr(*entry.NextResetAt)
			return "auto-wakes at cycle reset"
		}
	}
	if entry.CycleHours == nil {
		return ""
	}
	ch, hor := *entry.CycleHours, *entry.CycleHours
	if ch > 6.0 {
		hor = min(ch, float64(int(1)<<(2*min(max(0, entry.ConsecCooldowns-1), 2))))
	}
	wake := FormatLocalISO(now.Add(time.Duration(hor * float64(time.Hour))))
	entry.CooldownUntil = &wake
	if ch <= 6.0 && (entry.NextResetAt == nil || *entry.NextResetAt == "") {
		entry.NextResetAt = &wake
	}
	if entry.CycleStartedAt == nil || *entry.CycleStartedAt == "" {
		entry.CycleStartedAt = Ptr(FormatLocalISO(now))
	}
	return fmt.Sprintf("auto-wakes in %gh", hor)
}

func computeQuotaWake(entry *ProviderConfig, prefix, outText string, now time.Time) {
	entry.ConsecCooldowns++
	if srv := ExtractResetTS(outText); srv != nil {
		entry.CooldownUntil, entry.Reason = srv, Ptr(fmt.Sprintf("%s (resets %s)", prefix, *srv))
		if entry.NextResetAt == nil || *entry.NextResetAt == "" {
			entry.NextResetAt = srv
		}
		return
	}
	hasOuter := entry.OuterHours != nil || (entry.CycleHours != nil && *entry.CycleHours >= 168)
	low := strings.ToLower(outText)
	if hasOuter && (strings.Contains(low, "week") || strings.Contains(low, "month") ||
		(entry.CycleRuns == 0 && entry.ConsecCooldowns >= 2)) {
		ref := entry.OuterResetAt
		if ref == nil && entry.CycleHours != nil && *entry.CycleHours >= 168 {
			ref = entry.NextResetAt
		}
		if ref != nil {
			if dt, ok := ParseISOUTC(*ref); ok && dt.After(now.UTC()) {
				entry.CooldownUntil, entry.Reason = Ptr(*ref), Ptr(prefix+" (window limit — auto-wakes at cycle reset)")
				return
			}
		}
		hor := 24.0
		if entry.OuterHours != nil && *entry.OuterHours < hor {
			hor = *entry.OuterHours
		} else if entry.OuterHours == nil && entry.CycleHours != nil && entry.ConsecCooldowns >= 3 {
			hor = min(*entry.CycleHours, 168.0)
		}
		wake := FormatLocalISO(now.Add(time.Duration(hor * float64(time.Hour))))
		entry.CooldownUntil, entry.Reason = &wake, Ptr(fmt.Sprintf("%s (outer window backoff %gh)", prefix, hor))
		return
	}
	if note := applyInnerWake(entry, now); note != "" {
		entry.Reason = Ptr(fmt.Sprintf("%s (%s)", prefix, note))
		return
	}
	entry.CooldownUntil, entry.Reason = nil, Ptr("Credits exhausted — waiting for top-up")
}

// SetProviderStatus marks a provider active or in cooldown inside quota.json.
func SetProviderStatus(name, status, reason string, hours *float64, autoWake bool,
	qPath string) (string, *ProviderConfig, error) {
	if hours != nil && (math.IsNaN(*hours) || math.IsInf(*hours, 0) || *hours <= 0 || *hours > MaxHoursCap) {
		return "", nil, UsageErr("error: --hours must be finite and in (0, %g]", MaxHoursCap)
	}
	return mutateProviderLocked(qPath, name, true, func(entry *ProviderConfig) error {
		entry.Status, entry.ManualDisable = status, false
		if status == "active" {
			if reason == "probed" {
				entry.LastProbedAt = Ptr(FormatLocalISO(time.Now()))
			}
			entry.CooldownUntil, entry.Reason, entry.ConsecCooldowns = nil, nil, 0
			return nil
		}
		reason = map[bool]string{true: reason, false: "Manually set to cooldown"}[reason != ""]
		entry.ManualDisable, entry.Reason, entry.CooldownUntil = !autoWake, &reason, nil
		if hours != nil {
			entry.CooldownUntil = Ptr(FormatLocalISO(time.Now().Add(time.Duration(*hours * float64(time.Hour)))))
		} else if autoWake {
			_ = applyInnerWake(entry, time.Now())
		}
		return nil
	})
}

// MarkProviderCooldown puts a provider into quota or auth cooldown.
func MarkProviderCooldown(name, prefix string, isQuota bool, outText, qPath string) (*ProviderConfig, error) {
	_, entry, err := mutateProviderLocked(qPath, name, false, func(entry *ProviderConfig) error {
		entry.Status, entry.ManualDisable = "cooldown", false
		if isQuota {
			computeQuotaWake(entry, prefix, outText, time.Now())
			return nil
		}
		entry.CooldownUntil = Ptr(FormatLocalISO(time.Now().Add(time.Hour)))
		entry.Reason = Ptr(prefix + " (run login or --enable)")
		return nil
	})
	return entry, err
}

// RecordUsage accumulates cycle/outer runs, tokens, USD cost, and budgets.
func RecordUsage(name, prompt, output string, exp *ExportUsage, qPath string) (*ProviderConfig, error) {
	_, entry, err := mutateProviderLocked(qPath, name, false, func(entry *ProviderConfig) error {
		now := time.Now()
		if entry.CycleHours != nil && (entry.CycleStartedAt == nil || *entry.CycleStartedAt == "") {
			entry.CycleStartedAt = Ptr(FormatLocalISO(now))
			entry.NextResetAt = Ptr(FormatLocalISO(now.Add(time.Duration(*entry.CycleHours * float64(time.Hour)))))
		}
		if entry.OuterHours != nil && (entry.OuterStartedAt == nil || *entry.OuterStartedAt == "") {
			entry.OuterStartedAt = Ptr(FormatLocalISO(now))
			entry.OuterResetAt = Ptr(FormatLocalISO(now.Add(time.Duration(*entry.OuterHours * float64(time.Hour)))))
		}
		entry.CycleRuns++
		added := max(1, (len(prompt)+len(output))/4)
		entry.CycleTokensExact = exp != nil && exp.Tokens != nil && (entry.CycleRuns == 1 || entry.CycleTokensExact)
		if exp != nil && exp.Tokens != nil {
			added = *exp.Tokens
		}
		entry.CycleTokens, entry.OuterTokens = entry.CycleTokens+added, entry.OuterTokens+added
		if exp != nil && exp.CostUSD != nil {
			entry.CycleCostUSD = math.Round((entry.CycleCostUSD+*exp.CostUSD)*1e6) / 1e6
		}
		sH, pH := map[bool]float64{true: 0, false: 1}[exp != nil && exp.Failed], 1.0
		if entry.HealthEWMA != nil {
			pH = *entry.HealthEWMA
		}
		entry.HealthEWMA = Ptr(math.Round((0.65*pH+0.35*sH)*1000) / 1000)
		var hit string
		if entry.CycleTokenBudget != nil && entry.CycleTokens >= *entry.CycleTokenBudget {
			hit = fmt.Sprintf("Cycle token budget (%d) reached", *entry.CycleTokenBudget)
		} else if entry.CycleCostBudget != nil && entry.CycleCostUSD >= *entry.CycleCostBudget {
			hit = fmt.Sprintf("Cycle cost budget ($%.2f) reached", *entry.CycleCostBudget)
		}
		if hit != "" && entry.Status == "active" {
			entry.Status, entry.ManualDisable, entry.Reason, entry.CooldownUntil = "cooldown", false, &hit, entry.NextResetAt
		} else if entry.Status == "active" && output != "" {
			entry.ConsecCooldowns = 0
		}
		return nil
	})
	return entry, err
}

func applyCycleHours(entry *ProviderConfig, hrs float64) {
	if hrs <= 0 {
		entry.CycleHours, entry.NextResetAt, entry.Windows = nil, nil, nil
		return
	}
	win := []string{fmt.Sprintf("%gh", hrs)}
	if entry.OuterHours != nil && *entry.OuterHours >= 24 {
		win = append(win, fmt.Sprintf("%dd", int(*entry.OuterHours/24)))
	}
	entry.CycleHours, entry.Windows = Ptr(hrs), win
	if entry.CycleStartedAt != nil {
		if started, ok := ParseISOUTC(*entry.CycleStartedAt); ok {
			wake := FormatLocalISO(started.Add(time.Duration(hrs * float64(time.Hour))))
			entry.NextResetAt = &wake
			if entry.Status == "cooldown" && !entry.ManualDisable {
				entry.CooldownUntil = &wake
			}
		}
	}
}

// ConfigureCycle updates cycle schedule, budgets, defaults, or resets counters.
func ConfigureCycle(p CyclePatch) (string, *ProviderConfig, error) {
	badF := func(v *float64, hi float64) bool {
		return v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > hi)
	}
	if badF(p.Hours, MaxHoursCap) || badF(p.CostBudget, 1e6) ||
		(p.MaxParallel != nil && (*p.MaxParallel < 0 || *p.MaxParallel > MaxParallelCap)) {
		return "", nil, UsageErr("error: --hours, --cost-budget, or --max-parallel out of range")
	}
	canon, err := NormalizeLane(p.Provider, false)
	if err != nil {
		return "", nil, err
	}
	return mutateProviderLocked(p.QuotaPath, canon, false, func(entry *ProviderConfig) error {
		if p.Hours != nil {
			applyCycleHours(entry, *p.Hours)
		}
		setInt := func(d **int, v *int) {
			if v != nil {
				*d = map[bool]*int{true: Ptr(*v)}[*v > 0]
			}
		}
		setInt(&entry.CycleTokenBudget, p.TokenBudget)
		setInt(&entry.MaxParallel, p.MaxParallel)
		if p.CostBudget != nil {
			entry.CycleCostBudget = map[bool]*float64{true: Ptr(*p.CostBudget)}[*p.CostBudget > 0]
		}
		if p.Effort != nil {
			entry.Effort = Ptr(strings.ToLower(strings.TrimSpace(*p.Effort)))
		}
		if p.Model != nil {
			m, merr := NormalizeModel(canon, *p.Model)
			if merr != nil {
				return merr
			}
			entry.Model = map[bool]*string{true: &m}[m != ""]
		}
		if p.ResetNow {
			entry.CycleRuns, entry.CycleTokens, entry.CycleTokensExact, entry.CycleCostUSD = 0, 0, false, 0
			entry.CycleStartedAt, entry.NextResetAt, entry.Status, entry.HealthEWMA = nil, nil, "active", nil
			entry.OuterTokens, entry.OuterStartedAt, entry.OuterResetAt, entry.ConsecCooldowns = 0, nil, nil, 0
			entry.CooldownUntil, entry.Reason, entry.ManualDisable = nil, nil, false
		}
		return nil
	})
}
