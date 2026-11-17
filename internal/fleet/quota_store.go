package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var quotaMu sync.Mutex

func migratePool(cur []string, def []string, oldVer int) ([]string, bool) {
	changed := false
	var cleaned []string
	for _, p := range cur {
		if p == "cursor" || p == "devin-fusion" {
			changed = true
			continue
		}
		cleaned = append(cleaned, p)
	}
	if oldVer < 8 || len(cleaned) == 0 {
		return append([]string(nil), def...), true
	}
	return cleaned, changed
}

func migrateProvider(name string, info *ProviderConfig, tmpl *ProviderConfig, oldVer int, legacyUpd time.Time) bool {
	changed := false
	if oldVer < 7 {
		if name == "mcode" && info.Tier == "unlimited" {
			info.Tier, info.Role, changed = "high-cap", tmpl.Role, true
		}
		if name == "muse" && info.Tier == "unlimited" {
			info.Tier, changed = "capped", true
		}
		if name == "devin-fast" && info.MaxParallel == nil {
			info.MaxParallel, changed = Ptr(DevinFastMaxPar), true
		}
		if name == "mcode" && info.MaxParallel == nil {
			info.MaxParallel, info.Role, changed = Ptr(McodeMaxPar), tmpl.Role, true
		}
	}
	if oldVer < 8 {
		if len(tmpl.Windows) > 0 {
			info.Windows, changed = append([]string(nil), tmpl.Windows...), true
		}
		if tmpl.CycleHours != nil && (info.CycleHours == nil || *info.CycleHours == 5.0 || *info.CycleHours == 1.0) {
			info.CycleHours, changed = Ptr(*tmpl.CycleHours), true
		}
		if tmpl.OuterHours != nil {
			info.OuterHours, changed = Ptr(*tmpl.OuterHours), true
		}
		info.Role = tmpl.Role
		if (name == "grok" || name == "mistral") && info.Status == "active" && info.NextResetAt != nil {
			info.NextResetAt, info.CycleStartedAt, changed = nil, nil, true
			info.CycleRuns, info.CycleTokens, info.CycleTokensExact, info.CycleCostUSD = 0, 0, false, 0
		}
	}
	if info.Status == "cooldown" && info.CooldownUntil == nil && info.CycleHours != nil && !info.ManualDisable {
		if legacyUpd.IsZero() {
			legacyUpd = time.Now().UTC()
		}
		wake := FormatLocalISO(legacyUpd.Add(time.Duration(min(*info.CycleHours, 5.0) * float64(time.Hour))))
		info.CooldownUntil, changed = &wake, true
		if *info.CycleHours <= 6.0 && (info.NextResetAt == nil || *info.NextResetAt == "") {
			info.NextResetAt = &wake
		}
		if info.CycleStartedAt == nil || *info.CycleStartedAt == "" {
			info.CycleStartedAt = Ptr(FormatLocalISO(legacyUpd))
		}
	}
	if name == "devin-fusion" && strings.Contains(info.Role, "reserve") {
		info.Role, changed = tmpl.Role, true
	}
	return changed
}

// MigrateQuota upgrades legacy quota state in-place to QuotaSchemaVersion.
func MigrateQuota(q *QuotaState) bool {
	changed := false
	oldVer := max(1, q.Version)
	if oldVer < QuotaSchemaVersion {
		q.Version = QuotaSchemaVersion
		changed = true
	}
	if q.Providers == nil {
		q.Providers = make(map[string]*ProviderConfig)
		changed = true
	}
	if _, hasCursor := q.Providers["cursor"]; hasCursor {
		delete(q.Providers, "cursor")
		changed = true
	}
	var c1, c2 bool
	q.DefaultFallbackChain, c1 = migratePool(q.DefaultFallbackChain, DefaultPool, oldVer)
	q.ParallelStripePool, c2 = migratePool(q.ParallelStripePool, StripePool, oldVer)
	var legacyUpd time.Time
	if q.UpdatedAt != nil {
		legacyUpd, _ = ParseISOUTC(*q.UpdatedAt)
	}
	for name, tmpl := range DefaultProviders() {
		if cur, ok := q.Providers[name]; !ok || cur == nil {
			cp := *tmpl
			q.Providers[name], changed = &cp, true
		} else if migrateProvider(name, cur, tmpl, oldVer, legacyUpd) {
			changed = true
		}
	}
	if q.Stages == nil {
		q.Stages, changed = make(map[string]StageRoute, len(StageNames)), true
	}
	for _, st := range StageNames {
		if r := q.Stages[st]; r.Lane == "" {
			q.Stages[st], changed = StageRoute{Lane: "auto"}, true
		}
	}
	for _, info := range q.Providers {
		if info == nil || info.MaxParallel == nil {
			continue
		}
		if *info.MaxParallel <= 0 {
			info.MaxParallel, changed = nil, true
		} else if *info.MaxParallel > MaxParallelCap {
			info.MaxParallel, changed = Ptr(MaxParallelCap), true
		}
	}
	return changed || c1 || c2
}

// ExpireAndReset clears elapsed cycle/outer counters and expired non-manual cooldowns.
func ExpireAndReset(q *QuotaState, nowUTC time.Time) bool {
	changed := false
	if nowUTC.IsZero() {
		nowUTC = time.Now().UTC()
	}
	for st, sr := range q.Stages {
		if sr.ExpiresAt != nil && *sr.ExpiresAt != "" {
			if dt, ok := ParseISOUTC(*sr.ExpiresAt); ok && !dt.After(nowUTC) {
				q.Stages[st], changed = StageRoute{Lane: "auto"}, true
			}
		}
	}
	for _, info := range q.Providers {
		if info == nil {
			continue
		}
		if info.OuterResetAt != nil {
			if dt, ok := ParseISOUTC(*info.OuterResetAt); ok && !dt.After(nowUTC) {
				info.OuterTokens, info.OuterStartedAt, info.OuterResetAt, info.ConsecCooldowns, changed = 0, nil, nil, 0, true
			}
		}
		if info.NextResetAt != nil {
			if dt, ok := ParseISOUTC(*info.NextResetAt); ok && !dt.After(nowUTC) {
				info.CycleRuns, info.CycleTokens, info.CycleTokensExact, info.CycleCostUSD = 0, 0, false, 0
				info.CycleStartedAt, info.NextResetAt, changed = nil, nil, true
				if info.Status == "cooldown" && !info.ManualDisable {
					info.Status, info.CooldownUntil, info.Reason = "active", nil, nil
				}
			}
		}
		if info.Status == "cooldown" && info.CooldownUntil != nil {
			if dt, ok := ParseISOUTC(*info.CooldownUntil); ok && !dt.After(nowUTC) {
				info.Status, info.CooldownUntil, info.Reason, info.ManualDisable, changed = "active", nil, nil, false, true
			}
		}
	}
	return changed
}

// SaveQuotaLocked atomically persists quota state while holding WithQuotaLock.
func SaveQuotaLocked(q *QuotaState, qPath string) error {
	now := NowISO()
	q.UpdatedAt = &now
	return AtomicWriteJSON(QuotaFilePath(qPath), q, false, true)
}

func readAndPrepLocked(path string) (*QuotaState, error) {
	var q QuotaState
	if raw, err := os.ReadFile(path); err == nil {
		if jerr := json.Unmarshal(raw, &q); jerr != nil || q.Providers == nil {
			return nil, UsageErr("error: quota file %s must contain valid JSON with providers", path)
		}
		MigrateQuota(&q)
		ExpireAndReset(&q, time.Now().UTC())
		return &q, nil
	} else if !os.IsNotExist(err) {
		return nil, UsageErr("error: cannot read quota file %s: %v", path, err)
	}
	seedPath := filepath.Join(PluginRootPath(), "config", "quota.default.json")
	if raw, serr := os.ReadFile(seedPath); serr == nil && json.Unmarshal(raw, &q) == nil && q.Providers != nil {
		MigrateQuota(&q)
		ExpireAndReset(&q, time.Now().UTC())
		return &q, nil
	}
	dq := DefaultQuotaData()
	MigrateQuota(dq)
	ExpireAndReset(dq, time.Now().UTC())
	return dq, nil
}

// WithQuotaLock acquires the process mutex and OS flock around quota.json I/O.
func WithQuotaLock(qPath string, fn func(string) error) error {
	resolved := QuotaFilePath(qPath)
	ext := filepath.Ext(resolved)
	lockPath := strings.TrimSuffix(resolved, ext) + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	quotaMu.Lock()
	defer quotaMu.Unlock()
	fh, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer fh.Close()
	if err := syscall.Flock(int(fh.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(fh.Fd()), syscall.LOCK_UN) }()
	return fn(resolved)
}

// LoadQuota loads quota.json (seeding if missing), migrates, expires, and saves.
func LoadQuota(qPath string) (*QuotaState, error) {
	var out *QuotaState
	err := WithQuotaLock(qPath, func(path string) error {
		st, serr := os.Stat(path)
		if serr != nil || st.IsDir() {
			q, rerr := readAndPrepLocked(path)
			if rerr != nil {
				return rerr
			}
			out = q
			return SaveQuotaLocked(q, path)
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return UsageErr("error: cannot read quota file %s: %v", path, rerr)
		}
		var q QuotaState
		if jerr := json.Unmarshal(raw, &q); jerr != nil || q.Providers == nil {
			return UsageErr("error: quota file %s must contain valid JSON with providers", path)
		}
		mig, exp := MigrateQuota(&q), ExpireAndReset(&q, time.Now().UTC())
		out = &q
		if mig || exp {
			return SaveQuotaLocked(&q, path)
		}
		return nil
	})
	return out, err
}
