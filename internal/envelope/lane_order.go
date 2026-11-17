package envelope

import (
	"sort"
	"strings"
	"time"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

var conserveLeaders = []string{"devin-pro", "devin-fast", "mcode"}

func dedupStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it != "" && !seen[it] {
			seen[it] = true
			out = append(out, it)
		}
	}
	return out
}

func rotateSlice(s []string, offset int) []string {
	if len(s) == 0 {
		return nil
	}
	k := ((offset % len(s)) + len(s)) % len(s)
	return append(append([]string(nil), s[k:]...), s[:k]...)
}

func cappedActiveSorted(q *fleet.QuotaState, nowUTC time.Time) []string {
	if !q.DrainCappedFirst {
		return nil
	}
	type item struct {
		urgent, tariff, hasClock, idx int
		cycleH                        float64
		dt                            time.Time
		name                          string
	}
	caps := DiscoverFleetCaps(DiscoverOpts{Quota: q})
	var list []item
	for idx, name := range fleet.ProviderOrder {
		info := q.Providers[name]
		if info == nil || info.Tier != "capped" || info.Status != "active" {
			continue
		}
		urgent, hasClock, dtVal, ch := 1, 1, time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), 9999.0
		ts := orStr(ptrVal(info.NextResetAt), ptrVal(info.CooldownUntil))
		if parsed, ok := fleet.ParseISOUTC(ts); ok {
			hasClock, dtVal = 0, parsed
			if dtVal.After(nowUTC) && dtVal.Sub(nowUTC) <= 6*time.Hour {
				urgent = 0
			}
		}
		if info.CycleHours != nil {
			ch = *info.CycleHours
		}
		tb, mult := 0, 1.0
		mult, _ = LaneTariff(name, info, caps[name].EffectiveModel, nowUTC)
		if mult < 1.0 {
			tb = -1
		} else if mult > 1.0 {
			tb = 1
		}
		list = append(list, item{urgent, tb, hasClock, idx, ch, dtVal, name})
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.urgent != b.urgent {
			return a.urgent < b.urgent
		}
		if a.urgent == 0 && !a.dt.Equal(b.dt) {
			return a.dt.Before(b.dt)
		}
		if a.tariff != b.tariff {
			return a.tariff < b.tariff
		}
		if a.hasClock != b.hasClock {
			return a.hasClock < b.hasClock
		}
		if a.hasClock == 0 && !a.dt.Equal(b.dt) {
			return a.dt.Before(b.dt)
		}
		if a.cycleH != b.cycleH {
			return a.cycleH < b.cycleH
		}
		return a.idx < b.idx
	})
	out := make([]string, len(list))
	for i, it := range list {
		out[i] = it.name
	}
	return out
}

func fastOrder(q *fleet.QuotaState, chain []string, em EMEnvelope) []string {
	var ordered, deferred []string
	for _, name := range dedupStrings(append(append([]string(nil), fleet.FastOrder...), chain...)) {
		info := q.Providers[name]
		if info != nil && info.Tier == "credits" {
			continue
		}
		if info != nil && info.Tier == "capped" {
			deferred = append(deferred, name)
		} else {
			ordered = append(ordered, name)
		}
	}
	tail := dedupStrings(append(append([]string(nil), em.CornerLanes...), deferred...))
	tailSet := make(map[string]bool, len(tail))
	for _, t := range tail {
		tailSet[t] = true
	}
	var out []string
	for _, n := range ordered {
		if !tailSet[n] {
			out = append(out, n)
		}
	}
	return append(out, tail...)
}

func normalOrder(
	comp string, q *fleet.QuotaState, chain, pool []string,
	em EMEnvelope, capped []string, stripeIdx *int,
) []string {
	if comp == "review" {
		var out []string
		for _, n := range append(append([]string(nil), fleet.ReviewOrder...), chain...) {
			if info := q.Providers[n]; info == nil || info.Tier != "credits" {
				out = append(out, n)
			}
		}
		return dedupStrings(out)
	}
	if fleet.DeepComplexities[comp] || comp == "heavy" {
		base := fleet.DefaultPool
		if fleet.DeepComplexities[comp] {
			base = fleet.DeepOrder
		}
		raw := append(append(append([]string(nil), em.CornerLanes...), capped...), base...)
		return dedupStrings(append(raw, chain...))
	}
	if em.ConserveCapped {
		raw := append(append(append([]string(nil), em.CornerLanes...), conserveLeaders...), capped...)
		return dedupStrings(append(raw, chain...))
	}
	if stripeIdx == nil {
		raw := append(append(append([]string(nil), em.CornerLanes...), capped...), fleet.DefaultPool...)
		return dedupStrings(append(raw, chain...))
	}
	cornerSet := make(map[string]bool, len(em.CornerLanes))
	for _, c := range em.CornerLanes {
		cornerSet[c] = true
	}
	var regular []string
	for _, c := range capped {
		if !cornerSet[c] {
			regular = append(regular, c)
		}
	}
	base := rotateSlice(pool, *stripeIdx)
	if len(base) == 0 {
		base = fleet.DefaultPool
	}
	raw := append(append(append([]string(nil), em.CornerLanes...), rotateSlice(regular, *stripeIdx)...), base...)
	return dedupStrings(append(raw, chain...))
}

// BuildCandidateOrder ranks candidate lanes using quota posture and family bias.
func BuildCandidateOrder(
	requestedLane, complexity string, q *fleet.QuotaState,
	stripeIdx *int, avoidFamilies map[string]bool,
) ([]string, error) {
	if q == nil {
		q = fleet.DefaultQuotaData()
	}
	chain := q.DefaultFallbackChain
	if len(chain) == 0 {
		chain = fleet.DefaultPool
	}
	pool := q.ParallelStripePool
	if len(pool) == 0 {
		pool = fleet.StripePool
	}
	if requestedLane == "" {
		requestedLane = "auto"
	}
	lanes, err := fleet.ParseLanes(requestedLane, true)
	if err != nil {
		return nil, err
	}
	comp := strings.ToLower(strings.TrimSpace(complexity))
	nowUTC := time.Now().UTC()
	em := ComputeEMEnvelope(q, nowUTC)
	var ordered []string
	if fleet.FastComplexities[comp] {
		ordered = fastOrder(q, chain, em)
	} else {
		ordered = normalOrder(comp, q, chain, pool, em, cappedActiveSorted(q, nowUTC), stripeIdx)
	}
	var noCreds []string
	for _, p := range ordered {
		if info := q.Providers[p]; info == nil || info.Tier != "credits" {
			noCreds = append(noCreds, p)
		}
	}
	if len(lanes) != 1 || lanes[0] != "auto" {
		ordered = dedupStrings(append(append([]string(nil), lanes...), noCreds...))
	} else {
		ordered = demoteUnhealthy(noCreds, q)
	}
	if len(avoidFamilies) > 0 {
		var head, tail []string
		for _, p := range ordered {
			if avoidFamilies[ModelFamily(p)] {
				tail = append(tail, p)
			} else {
				head = append(head, p)
			}
		}
		ordered = append(head, tail...)
	}
	return ordered, nil
}

func demoteUnhealthy(order []string, q *fleet.QuotaState) []string {
	if q == nil {
		return order
	}
	var okL, weak []string
	for _, name := range order {
		info := q.Providers[name]
		if info != nil && info.HealthEWMA != nil && *info.HealthEWMA < 0.55 {
			weak = append(weak, name)
		} else {
			okL = append(okL, name)
		}
	}
	sort.SliceStable(weak, func(i, j int) bool {
		return *q.Providers[weak[i]].HealthEWMA > *q.Providers[weak[j]].HealthEWMA
	})
	return append(okL, weak...)
}
