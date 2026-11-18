package envelope

import (
	"fmt"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

// SelectReq parameterizes provider selection for a single routed task.
type SelectReq struct {
	RequestedLane string
	Complexity    string
	Quota         *fleet.QuotaState
	Exclude       map[string]bool
	StripeIndex   *int
	AvoidFamilies map[string]bool
	StrictAvoid   bool
	DeferAtCap    bool
	SoftPin       bool
	ResolveCLI    func(string) string
}

// SelectRes holds the chosen provider lane, config, CLI path, and skip notes.
type SelectRes struct {
	Lane        string
	Info        *fleet.ProviderConfig
	CLIPath     string
	SkippedNote string
}

func checkCandidate(
	name string, req SelectReq, depth int, expSet map[string]bool, resolver func(string) string,
) (SelectRes, string, bool) {
	if req.Exclude != nil && req.Exclude[name] {
		return SelectRes{}, fleet.SafeText(name) + ": already attempted", false
	}
	if req.StrictAvoid && req.AvoidFamilies != nil && req.AvoidFamilies[ModelFamily(name)] {
		return SelectRes{}, fleet.SafeText(name) + ": excluded family (" + fleet.SafeText(ModelFamily(name)) + ")", false
	}
	if !expSet["muse"] && name == "muse" && fleet.CachedMuseProbe(900).PayAsYouGo {
		return SelectRes{}, "muse: skipped (pay-as-you-go api_key active; Power subscription inactive)", false
	}
	var info *fleet.ProviderConfig
	if req.Quota != nil && req.Quota.Providers != nil {
		info = req.Quota.Providers[name]
	}
	if info == nil {
		return SelectRes{}, fleet.SafeText(name) + ": not configured", false
	}
	// The billing gate reads the static spec, not the quota file: a forged
	// tier must not be able to make a paid lane look free.
	tier := info.Tier
	if spec, known := fleet.ProviderSpecs()[name]; known && spec.Tier != "" {
		tier = spec.Tier
	}
	if (!expSet[name] || req.Complexity == "review") && tier == "credits" {
		return SelectRes{}, fleet.SafeText(name) + ": paid credits require explicit --lane " + fleet.SafeText(name), false
	}
	if info.Status != "active" {
		why := info.Status
		if info.Reason != nil && *info.Reason != "" {
			why = *info.Reason
		}
		return SelectRes{}, fleet.SafeText(fmt.Sprintf("%s: %s (%s)", name, info.Status, why)), false
	}
	cliPath := ""
	cliName, cliErr := fleet.LaneCLIName(name, info.CLI)
	if cliErr != nil {
		return SelectRes{}, fleet.SafeText(cliErr.Error()), false
	}
	if cliName != "" {
		cliPath = resolver(cliName)
	}
	if cliName == "" || cliPath == "" {
		return SelectRes{}, fleet.SafeText(fmt.Sprintf("%s: cli '%s' not installed", name, cliName)), false
	}
	return SelectRes{Lane: name, Info: info, CLIPath: cliPath}, "", true
}

// SelectProvider chooses the first active, installed, non-full candidate lane.
func SelectProvider(req SelectReq) (SelectRes, error) {
	if req.Quota == nil {
		req.Quota = fleet.DefaultQuotaData()
	}
	laneArg := orStr(req.RequestedLane, "auto")
	lanes, err := fleet.ParseLanes(laneArg, true)
	if err != nil {
		return SelectRes{}, err
	}
	var expSet map[string]bool
	if !(len(lanes) == 1 && lanes[0] == "auto") && !req.SoftPin {
		expSet = make(map[string]bool, len(lanes))
		for _, l := range lanes {
			expSet[l] = true
		}
	}
	cands, err := BuildCandidateOrder(laneArg, req.Complexity, req.Quota, req.StripeIndex, req.AvoidFamilies)
	if err != nil {
		return SelectRes{}, err
	}
	resolver := req.ResolveCLI
	if resolver == nil {
		resolver = fleet.ResolveCLI
	}
	depth := fleet.RouterDepth()
	var skipped []string
	var atCap []SelectRes
	for _, name := range cands {
		cand, skipMsg, ok := checkCandidate(name, req, depth, expSet, resolver)
		if !ok {
			skipped = append(skipped, skipMsg)
			continue
		}
		maxP := 0
		if cand.Info.MaxParallel != nil {
			maxP = *cand.Info.MaxParallel
		}
		if req.DeferAtCap && maxP > 0 && fleet.CountActiveLeases(name, maxP) >= maxP {
			atCap = append(atCap, cand)
			skipped = append(skipped, fmt.Sprintf("%s: %d/%d parallel slots busy", name, maxP, maxP))
			continue
		}
		cand.SkippedNote = strings.Join(skipped, "; ")
		return cand, nil
	}
	if len(atCap) > 0 {
		pick := atCap[0]
		pick.SkippedNote = strings.Join(skipped, "; ")
		return pick, nil
	}
	return SelectRes{SkippedNote: orStr(strings.Join(skipped, "; "), "no configured providers")}, nil
}

// HasLaneFamily reports whether laneCSV is auto or contains any lane in fam.
func HasLaneFamily(laneCSV, fam string) bool {
	if laneCSV == "" || laneCSV == "auto" {
		return true
	}
	for _, l := range strings.Split(laneCSV, ",") {
		if c, err := fleet.NormalizeLane(l, true); err == nil && ModelFamily(c) == fam {
			return true
		}
	}
	return false
}

func pickCleanModel(models []string, prefs ...string) string {
	for _, want := range prefs {
		if want == "" {
			continue
		}
		for _, m := range models {
			base := strings.SplitN(m, "[", 2)[0]
			if base == want {
				return base
			}
		}
	}
	return ""
}

func pickFastVibeAlias(aliases map[string]string) string {
	for alias, think := range aliases {
		if think == "off" || think == "low" || think == "medium" {
			return alias
		}
	}
	return ""
}

// ResolveRegimeSpec selects model and effort matching the task complexity regime.
func ResolveRegimeSpec(provider, complexity string, cap LaneCap) (string, string) {
	comp := strings.ToLower(strings.TrimSpace(complexity))
	eff := ""
	if fleet.FastComplexities[comp] {
		eff = "medium"
	} else if comp == "review" && provider == "mcode" {
		eff = "max"
	} else if comp != "review" && (fleet.DeepComplexities[comp] || comp == "heavy") {
		if provider == "muse" || provider == "grok" {
			eff = "xhigh"
		} else if provider == "mcode" {
			eff = "max"
		}
	}
	var mod string
	switch provider {
	case "muse":
		if (fleet.DeepComplexities[comp] && comp != "ember") || comp == "heavy" {
			mod = pickCleanModel(cap.Models, "muse-wonder-1.0", "muse-ember-1.0")
		} else if comp != "" && comp != "review" {
			mod = pickCleanModel(cap.Models, "muse-ember-1.0")
		}
	case "devin-fast":
		if fleet.FastComplexities[comp] {
			mod = pickCleanModel(cap.Models, "swe-2-flash-high")
		}
	case "devin-pro":
		if fleet.FastComplexities[comp] {
			mod = pickCleanModel(cap.Models, "swe-2-high")
		}
	case "mistral":
		if fleet.FastComplexities[comp] {
			mod = pickFastVibeAlias(cap.AliasEfforts)
		}
	}
	return mod, eff
}
