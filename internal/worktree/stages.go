package worktree

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

// StageSpec describes the effective route and source resolved for one OODA stage.
type StageSpec struct {
	Stage, Source, Lane, Primary, Model, Effort string
	ExpiresAt                                   *string
}

// StagePatch configures one stage, all stages, or a named workflow preset.
type StagePatch struct {
	Stage, Lane, Model, Effort, Cwd, QuotaPath string
	UntilReset, Local                          bool
}

func validateStagePatch(p StagePatch, q *fleet.QuotaState) (fleet.StageRoute, error) {
	lanes, err := fleet.ParseLanes(p.Lane, true)
	if err != nil {
		return fleet.StageRoute{}, err
	}
	route := fleet.StageRoute{Lane: strings.Join(lanes, ",")}
	if route.Lane == "auto" {
		if p.Model != "" || p.Effort != "" {
			return fleet.StageRoute{}, fleet.UsageErr("error: pick a non-auto lane to pin --model or --effort")
		}
		return route, nil
	}
	for _, l := range lanes {
		if (q != nil && q.Providers[l] != nil && q.Providers[l].Tier == "credits") ||
			l == "devin-fusion" || (l == "muse" && fleet.CachedMuseProbe(900).PayAsYouGo) {
			return fleet.StageRoute{}, fleet.UsageErr("error: paid/PAYG lane %q cannot be pinned; pass --lane %s", l, l)
		}
	}
	if m := strings.TrimSpace(p.Model); m != "" {
		if err := fleet.CheckModelGuard(m); err != nil {
			return fleet.StageRoute{}, err
		}
		norm, merr := fleet.NormalizeModel(lanes[0], m)
		if merr != nil {
			return fleet.StageRoute{}, merr
		}
		route.Model = &norm
	}
	if e := strings.ToLower(strings.TrimSpace(p.Effort)); e != "" {
		route.Effort = &e
	}
	if p.UntilReset && q != nil && q.Providers[lanes[0]] != nil {
		info, ts := q.Providers[lanes[0]], fleet.FormatLocalISO(time.Now().Add(5*time.Hour))
		if info.NextResetAt != nil && *info.NextResetAt != "" {
			ts = *info.NextResetAt
		} else if info.CooldownUntil != nil && *info.CooldownUntil != "" {
			ts = *info.CooldownUntil
		} else if info.CycleHours != nil && *info.CycleHours > 0 {
			ts = fleet.FormatLocalISO(time.Now().Add(time.Duration(*info.CycleHours * float64(time.Hour))))
		}
		route.ExpiresAt = &ts
	}
	return route, nil
}

// ConfigureStage validates and stores global or repo-local stage routes/presets.
func ConfigureStage(p StagePatch) (saved map[string]fleet.StageRoute, err error) {
	q, err := fleet.LoadQuota(p.QuotaPath)
	if err != nil {
		return nil, err
	}
	stKey, updates := strings.ToLower(strings.TrimSpace(p.Stage)), make(map[string]fleet.StageRoute)
	if stKey == "preset" {
		if p.Model != "" || p.Effort != "" || p.UntilReset {
			return nil, fleet.UsageErr("error: --model/--effort/--until-reset require a single stage")
		}
		if updates, _ = PresetStages(p.Lane); len(updates) == 0 {
			return nil, fleet.UsageErr("error: unknown stage preset %q", p.Lane)
		}
		for _, sr := range updates {
			if (strings.Contains(sr.Lane, "muse") && fleet.CachedMuseProbe(900).PayAsYouGo) ||
				strings.Contains(sr.Lane, "devin-fusion") {
				return nil, fleet.UsageErr("error: paid/PAYG lane in preset %q; pass --lane explicitly", p.Lane)
			}
		}
	} else if route, rerr := validateStagePatch(p, q); rerr != nil {
		return nil, rerr
	} else if stKey == "all" || stKey == "observe" || stKey == "orient" || stKey == "act" || stKey == "verify" {
		for _, st := range fleet.StageNames {
			if stKey == "all" || st == stKey {
				updates[st] = route
			}
		}
	} else {
		return nil, fleet.UsageErr("error: unknown stage %q (want observe, orient, act, verify, all, preset)", p.Stage)
	}
	if p.Local {
		if stKey == "all" && updates["act"].Lane == "auto" {
			return nil, SaveWorkspaceStages(p.Cwd, nil)
		}
		cur := LoadWorkspaceStages(p.Cwd)
		for k, v := range updates {
			cur[k] = v
		}
		return cur, SaveWorkspaceStages(p.Cwd, cur)
	}
	err = fleet.WithQuotaLock(p.QuotaPath, func(path string) error {
		var state fleet.QuotaState
		if raw, rerr := os.ReadFile(path); rerr != nil || json.Unmarshal(raw, &state) != nil {
			return fleet.UsageErr("error: cannot read/parse quota file %s", path)
		}
		fleet.MigrateQuota(&state)
		for k, v := range updates {
			state.Stages[k] = v
		}
		saved = state.Stages
		return fleet.SaveQuotaLocked(&state, path)
	})
	return saved, err
}

// ResolveStage applies precedence: CLI flag > repo override > profile > global > auto.
func ResolveStage(stage, cliLane, cliModel, cliEffort, cliProfile, cwd string, q *fleet.QuotaState) StageSpec {
	pick := func(src string, r fleet.StageRoute) StageSpec {
		sp := StageSpec{Stage: stage, Source: src, Lane: r.Lane, Primary: strings.Split(r.Lane, ",")[0],
			Model: cliModel, Effort: cliEffort, ExpiresAt: r.ExpiresAt}
		if sp.Model == "" && r.Model != nil {
			sp.Model = *r.Model
		}
		if sp.Effort == "" && r.Effort != nil {
			sp.Effort = *r.Effort
		}
		return sp
	}
	if cliProfile == "" {
		cliProfile = os.Getenv("MCODE_ROUTER_PROFILE")
	}
	ws := LoadWorkspaceStages(cwd)
	pre, hasPre := PresetStages(cliProfile)
	globalRoute, hasGlobal := fleet.StageRoute{}, false
	if q != nil && q.Stages != nil && q.Stages[stage].Lane != "" && q.Stages[stage].Lane != "auto" {
		if clean, ok := sanitizeStageRoute(q.Stages[stage]); ok {
			globalRoute, hasGlobal = clean, true
		} else {
			warnedPins("global-"+stage, "warning: ignoring invalid global stage pin for "+stage)
		}
	}
	switch {
	case cliLane != "":
		return pick("cli", fleet.StageRoute{Lane: cliLane})
	case ws[stage].Lane != "":
		return pick("local", ws[stage])
	case hasPre && pre[stage].Lane != "" && pre[stage].Lane != "auto":
		return pick("profile:"+strings.TrimSpace(cliProfile), pre[stage])
	case hasGlobal:
		return pick("global", globalRoute)
	default:
		return StageSpec{Stage: stage, Source: "auto", Lane: "auto", Primary: "auto", Model: cliModel, Effort: cliEffort}
	}
}

// ActivePinsSummary formats non-auto stage pins concisely for the HUD line.
func ActivePinsSummary(cwd, profile string, q *fleet.QuotaState) string {
	var parts []string
	for _, st := range fleet.StageNames {
		if sp := ResolveStage(st, "", "", "", profile, cwd, q); sp.Primary != "" && sp.Primary != "auto" {
			parts = append(parts, st+":"+sp.Primary)
		}
	}
	return strings.Join(parts, ", ")
}
