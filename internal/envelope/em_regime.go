// Package envelope implements quota-aware lane routing, tariffs, and discovery.
package envelope

import (
	"os"
	"sort"
	"strings"
	"time"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

// EMEnvelope summarizes active/expiring capped lanes and quota conservation state.
type EMEnvelope struct {
	NowUTC         time.Time
	ActiveCapped   []string
	CornerLanes    []string
	SoonestRegenS  *float64
	ConserveCapped bool
	Regimes        map[string]string
}

type cornerItem struct {
	remS float64
	idx  int
	name string
}

// ModelFamily maps a provider lane to its underlying model family name.
func ModelFamily(provider string) string {
	if fam, ok := fleet.ModelFamilies[provider]; ok {
		return fam
	}
	return provider
}

func baseURLHas(hosts ...string) bool {
	for _, k := range []string{"ZAI_BASE_URL", "OPENAI_BASE_URL", "DEEPSEEK_BASE_URL"} {
		v := strings.ToLower(os.Getenv(k))
		if v == "" {
			continue
		}
		for _, h := range hosts {
			if strings.Contains(v, h) {
				return true
			}
		}
	}
	return false
}

// LaneTariff computes the time-of-day cost/congestion multiplier and badge at nowUTC.
func LaneTariff(lane string, info *fleet.ProviderConfig, activeModel string, nowUTC time.Time) (float64, string) {
	if nowUTC.IsZero() {
		nowUTC = time.Now().UTC()
	}
	lowMod := strings.ToLower(activeModel)
	if lowMod == "" && info != nil && info.Model != nil {
		lowMod = strings.ToLower(*info.Model)
	}
	wd := nowUTC.Weekday()
	weekend := wd == time.Saturday || wd == time.Sunday
	mins := nowUTC.Hour()*60 + nowUTC.Minute()
	useEnvURL := lane == "mistral" && lowMod == ""
	isZai := (lane == "mistral" && strings.Contains(lowMod, "glm") && os.Getenv("ZAI_API_KEY") != "") ||
		strings.Contains(lowMod, "zai") || (useEnvURL && baseURLHas("z.ai", "bigmodel.cn"))
	if isZai {
		if !weekend && mins >= 360 && mins < 600 {
			return 2.0, "PEAK 2x"
		}
		return 0.5, "0.5x off-peak"
	}
	if strings.Contains(lowMod, "deepseek") || (useEnvURL && baseURLHas("deepseek.com")) {
		if !weekend && ((mins >= 60 && mins < 240) || (mins >= 360 && mins < 600)) {
			return 1.0, "peak"
		}
		return 0.5, "0.5x off-peak"
	}
	if lane == "mcode" && !weekend && mins >= 900 && mins < 1050 {
		return 1.25, "peak-jam"
	}
	return 1.0, ""
}

func cornerWindowFor(info *fleet.ProviderConfig, nowUTC time.Time) float64 {
	if info == nil || info.CycleHours == nil {
		return fleet.CornerWindowSecs
	}
	ch := *info.CycleHours
	if ch <= 6.0 {
		if info.OuterHours != nil && info.OuterResetAt != nil {
			rem := remainingSec(info.OuterResetAt, nil, nowUTC)
			if rem != nil && *rem > 64800 {
				return 0
			}
		}
		return fleet.CornerWindowSecs
	}
	if ch <= 24.0 {
		return 7200
	}
	if ch <= 168.0 {
		return 43200
	}
	return 129600
}

func scanProviders(
	providers map[string]*fleet.ProviderConfig, nowUTC time.Time,
) ([]string, []string, *float64) {
	var activeCapped []string
	var corners []cornerItem
	var soonest *float64
	names := make([]string, 0, len(providers))
	for k := range providers {
		names = append(names, k)
	}
	sort.Strings(names)
	for idx, name := range names {
		info := providers[name]
		if info == nil {
			continue
		}
		if info.Tier == "capped" && info.Status == "active" {
			activeCapped = append(activeCapped, name)
			rem := remainingSec(info.NextResetAt, info.CooldownUntil, nowUTC)
			cw := cornerWindowFor(info, nowUTC)
			if rem != nil && *rem > 0 && cw > 0 && *rem <= cw {
				corners = append(corners, cornerItem{remS: *rem, idx: idx, name: name})
			}
			continue
		}
		if info.Status != "active" && !info.ManualDisable {
			rem := remainingSec(info.CooldownUntil, info.NextResetAt, nowUTC)
			if rem != nil && *rem > 0 && (soonest == nil || *rem < *soonest) {
				v := *rem
				soonest = &v
			}
		}
	}
	sort.Slice(corners, func(i, j int) bool {
		if corners[i].remS != corners[j].remS {
			return corners[i].remS < corners[j].remS
		}
		return corners[i].name < corners[j].name
	})
	cornerNames := make([]string, len(corners))
	for i, c := range corners {
		cornerNames[i] = c.name
	}
	return activeCapped, cornerNames, soonest
}

func remainingSec(primary, fallback *string, nowUTC time.Time) *float64 {
	ts := ""
	if primary != nil && *primary != "" {
		ts = *primary
	} else if fallback != nil && *fallback != "" {
		ts = *fallback
	}
	if ts == "" {
		return nil
	}
	dt, ok := fleet.ParseISOUTC(ts)
	if !ok {
		return nil
	}
	rem := dt.Sub(nowUTC).Seconds()
	return &rem
}

func assignRegimes(
	providers map[string]*fleet.ProviderConfig, cornerSet map[string]bool, conserve bool,
) map[string]string {
	regimes := make(map[string]string, len(providers))
	for name, info := range providers {
		if info == nil {
			continue
		}
		switch {
		case info.Status != "active":
			regimes[name] = "COOLING"
		case info.Tier == "credits":
			regimes[name] = "CREDITS"
		case cornerSet[name]:
			regimes[name] = "EXPIRING"
		case info.Tier == "capped" && conserve:
			regimes[name] = "SPARING"
		default:
			regimes[name] = "READY"
		}
	}
	return regimes
}

// ComputeEMEnvelope classifies each lane into EXPIRING/READY/SPARING/CREDITS/COOLING.
func ComputeEMEnvelope(q *fleet.QuotaState, nowUTC time.Time) EMEnvelope {
	if nowUTC.IsZero() {
		nowUTC = time.Now().UTC()
	}
	activeCapped, corners, soonest := scanProviders(q.Providers, nowUTC)
	conserve := len(activeCapped) <= 1 && len(corners) == 0 &&
		(soonest == nil || *soonest > fleet.SoonWakeSecs)
	cset := make(map[string]bool, len(corners))
	for _, c := range corners {
		cset[c] = true
	}
	return EMEnvelope{
		NowUTC: nowUTC, ActiveCapped: activeCapped, CornerLanes: corners,
		SoonestRegenS: soonest, ConserveCapped: conserve,
		Regimes: assignRegimes(q.Providers, cset, conserve),
	}
}
