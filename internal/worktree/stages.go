package worktree

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// PresetStages maps named presets (e.g. conserve, deep, devin) to 4-stage maps.
func PresetStages(name string) (map[string]fleet.StageRoute, bool) {
	mk := func(obs, ori, act, ver string, oriEff *string) map[string]fleet.StageRoute {
		return map[string]fleet.StageRoute{"observe": {Lane: obs}, "orient": {Lane: ori, Effort: oriEff},
			"act": {Lane: act}, "verify": {Lane: ver}}
	}
	res, ok := map[string]map[string]fleet.StageRoute{
		"auto": mk("auto", "auto", "auto", "auto", nil), "devin": mk("devin-fast", "devin-pro", "devin-pro", "mcode", nil),
		"conserve": mk("devin-fast", "devin-pro", "devin-pro,devin-fast", "mcode", nil),
		"deep":     mk("devin-fast", "muse,grok", "devin-pro,muse", "mcode", fleet.Ptr("xhigh")),
		"mcode":    mk("mcode", "mcode", "mcode", "devin-fast", nil), "grok": mk("devin-fast", "grok", "grok", "mcode", nil),
		"muse": mk("devin-fast", "muse", "muse", "mcode", nil), "mistral": mk("devin-fast", "mistral", "mistral", "mcode", nil),
	}[strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "ooda-")]
	return res, ok
}

// WorkspaceStagesPath resolves the out-of-git stage override file for cwd.
func WorkspaceStagesPath(cwd string) string {
	root, _ := filepath.Abs(strings.TrimSpace(cwd))
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	for d := root; d != filepath.Dir(d); d = filepath.Dir(d) {
		gp := filepath.Join(d, ".git")
		fi, err := os.Stat(gp)
		if err == nil && fi.IsDir() {
			root = d
			break
		}
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		raw, _ := os.ReadFile(gp)
		if gd, ok := strings.CutPrefix(strings.SplitN(string(raw), "\n", 2)[0], "gitdir:"); ok {
			if gd = strings.TrimSpace(gd); !filepath.IsAbs(gd) {
				gd = filepath.Join(d, gd)
			}
			if gd = filepath.Clean(gd); filepath.Base(filepath.Dir(gd)) == "worktrees" &&
				filepath.Base(filepath.Dir(filepath.Dir(gd))) == ".git" {
				root = filepath.Dir(filepath.Dir(filepath.Dir(gd)))
				break
			}
		}
	}
	sum := sha256.Sum256([]byte(root))
	id := fleet.SafeID(filepath.Base(root)) + "-" + hex.EncodeToString(sum[:6])
	return filepath.Join(filepath.Dir(fleet.QuotaFilePath("")), "workspaces", id+".json")
}

// warnedPins keeps a stage pin warning to one line per stage per process.
var warnedPins = func() func(string, string) {
	var mu sync.Mutex
	seen := map[string]bool{}
	return func(key, msg string) {
		mu.Lock()
		defer mu.Unlock()
		if seen[key] {
			return
		}
		seen[key] = true
		fmt.Fprintln(os.Stderr, msg)
	}
}()

// sanitizeStageRoute re-validates a stage route that came from mutable state,
// applying the same invariants as the write path: known lanes only, no paid
// or PAYG lane, and no reserved model.
var sanitizeStageRoute = func(r fleet.StageRoute) (fleet.StageRoute, bool) {
	lanes, err := fleet.ParseLanes(r.Lane, true)
	if err != nil || len(lanes) == 0 {
		return fleet.StageRoute{}, false
	}
	for _, l := range lanes {
		spec, known := fleet.ProviderSpecs()[l]
		if l == "devin-fusion" || (known && spec.Tier == "credits") ||
			(l == "muse" && fleet.MusePayAsYouGoCached()) {
			return fleet.StageRoute{}, false
		}
	}
	if r.ExpiresAt != nil && *r.ExpiresAt != "" {
		dt, ok := fleet.ParseISOUTC(*r.ExpiresAt)
		if !ok || !dt.After(time.Now().UTC()) {
			return fleet.StageRoute{}, false
		}
	}
	if r.Model != nil {
		if err := fleet.CheckModelGuard(*r.Model); err != nil {
			return fleet.StageRoute{}, false
		}
	}
	if len(lanes) == 1 && lanes[0] == "auto" && (r.Model != nil || r.Effort != nil) {
		// An auto route may not carry a model or an effort.
		return fleet.StageRoute{}, false
	}
	r.Lane = strings.Join(lanes, ",")
	return r, true
}

// LoadWorkspaceStages reads repo-scoped overrides and ignores expired pins.
func LoadWorkspaceStages(cwd string) map[string]fleet.StageRoute {
	out := make(map[string]fleet.StageRoute, len(fleet.StageNames))
	p, parsed := WorkspaceStagesPath(cwd), map[string]fleet.StageRoute(nil)
	if raw, err := os.ReadFile(p); err != nil {
		return out
	} else if json.Unmarshal(raw, &parsed) != nil || parsed == nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring corrupt workspace stage file %s\n", p)
		return out
	}
	for _, st := range fleet.StageNames {
		if r := parsed[st]; r.Lane != "" {
			if r.ExpiresAt != nil && *r.ExpiresAt != "" {
				if dt, ok := fleet.ParseISOUTC(*r.ExpiresAt); ok && !dt.After(time.Now().UTC()) {
					continue
				}
			}
			clean, ok := sanitizeStageRoute(r)
			if !ok {
				warnedPins("workspace-"+st, "warning: ignoring invalid workspace stage pin for "+st)
				continue
			}
			out[st] = clean
		}
	}
	return out
}

// SaveWorkspaceStages writes repo stage overrides outside git or deletes when empty.
func SaveWorkspaceStages(cwd string, stages map[string]fleet.StageRoute) error {
	p, kept := WorkspaceStagesPath(cwd), make(map[string]fleet.StageRoute)
	for _, st := range fleet.StageNames {
		if r := stages[st]; r.Lane != "" {
			kept[st] = r
		}
	}
	if len(kept) == 0 {
		return os.RemoveAll(p)
	}
	return fleet.AtomicWriteJSON(p, kept, false, true)
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
