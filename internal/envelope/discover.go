package envelope

import (
	"os"
	"regexp"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

var tierSuffixRe = regexp.MustCompile(`-(low|medium|high|xhigh|max)$`)

// LaneCap summarizes discovered capabilities and resolved defaults for one lane.
type LaneCap struct {
	DispatchModel   string
	EffortMode      string
	EffectiveModel  string
	EffectiveEffort string
	Models          []string
	AliasEfforts    map[string]string
}

// LaneSpec holds final argv and display model/effort values for worker launch.
type LaneSpec struct {
	ArgvModel     string
	DisplayModel  string
	ArgvEffort    string
	DisplayEffort string
}

// DiscoverOpts configures DiscoverFleetCaps execution.
type DiscoverOpts struct {
	Quota        *fleet.QuotaState
	Home         string
	ForceRefresh bool
}

func buildLaneCap(
	lane string, info *fleet.ProviderConfig, devin DevinConfig,
	singles map[string]CLIConfig, cliMods map[string][]string,
) LaneCap {
	pinM, pinE := "", ""
	if info != nil {
		pinM, pinE = ptrVal(info.Model), ptrVal(info.Effort)
	}
	if strings.HasPrefix(lane, "devin-") {
		dm := devin.LaneModels[lane]
		return LaneCap{
			DispatchModel: orStr(pinM, dm), EffortMode: devin.EffortMode,
			EffectiveModel:  orStr(orStr(pinM, dm), "default"),
			EffectiveEffort: orStr(orStr(pinE, devin.LaneEfforts[lane]), "default"),
			Models:          dedupStrings(append(append([]string(nil), devin.Models...), cliMods["devin"]...)),
			AliasEfforts:    map[string]string{},
		}
	}
	cfg := singles[lane]
	effM := orStr(orStr(pinM, cfg.ActiveModel), "default")
	mods := cfg.Models
	if lane == "grok" {
		mods = dedupStrings(append(append([]string(nil), mods...), cliMods["grok"]...))
	}
	return LaneCap{
		DispatchModel: pinM, EffortMode: cfg.EffortMode, EffectiveModel: effM,
		EffectiveEffort: orStr(pinE, orStr(cfg.AliasEfforts[effM], orStr(cfg.ActiveEffort, "default"))),
		Models:          mods, AliasEfforts: cfg.AliasEfforts,
	}
}

func ptrVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// DiscoverFleetCaps combines local CLI configs and model caches.
func DiscoverFleetCaps(opts DiscoverOpts) map[string]LaneCap {
	q := opts.Quota
	if q == nil {
		q, _ = fleet.LoadQuota("")
		if q == nil {
			q = fleet.DefaultQuotaData()
		}
	}
	devin := ProbeDevinConfig(opts.Home)
	singles := map[string]CLIConfig{
		"muse": ProbeMuseConfig(opts.Home), "mcode": ProbeMinimaxConfig(opts.Home),
		"grok": ProbeGrokConfig(opts.Home), "mistral": ProbeVibeConfig(opts.Home),
	}
	var cliMods map[string][]string
	if opts.Home == "" || os.Getenv("MCODE_ROUTER_DISCOVERY_FILE") != "" || os.Getenv("OODA_DISCOVERY_FILE") != "" {
		cliMods = LoadDiscoveryCache(opts.ForceRefresh)
	}
	caps := make(map[string]LaneCap, len(fleet.ProviderOrder))
	for _, lane := range fleet.ProviderOrder {
		caps[lane] = buildLaneCap(lane, q.Providers[lane], devin, singles, cliMods)
	}
	return caps
}

// EffectiveLaneSpec resolves argv and display model/effort with Gemini guardrail.
func EffectiveLaneSpec(
	provider string, info *fleet.ProviderConfig, expModel, expEffort, complexity, home string,
) (LaneSpec, error) {
	rawModel := orStr(expModel, orStr(os.Getenv("AGENT_MODEL"), os.Getenv("OODA_MODEL")))
	if rawModel == "" && info != nil && info.Model != nil {
		rawModel = *info.Model
	}
	normModel, err := fleet.NormalizeModel(provider, rawModel)
	if err != nil {
		return LaneSpec{}, err
	}
	explicitEff := fleet.ResolveEffort(info, expEffort)
	cap := DiscoverFleetCaps(DiscoverOpts{Home: home})[provider]
	regMod, regEff := ResolveRegimeSpec(provider, complexity, cap)
	argvModel := orStr(normModel, orStr(regMod, cap.DispatchModel))
	dispModel := orStr(argvModel, orStr(cap.EffectiveModel, "default"))
	inferred := ""
	if strings.HasPrefix(provider, "devin-") {
		if em := tierSuffixRe.FindStringSubmatch(strings.ToLower(dispModel)); len(em) == 2 {
			inferred = em[1]
		}
	}
	argvEff := explicitEff
	if argvEff == "" && (provider == "mcode" || provider == "grok" || provider == "muse") {
		argvEff = regEff
	}
	if !SupportsEffort(provider, argvModel) {
		argvEff = ""
	}
	effFb := orStr(inferred, orStr(regEff, orStr(cap.EffectiveEffort, "default")))
	dispEff := orStr(explicitEff, orStr(cap.AliasEfforts[dispModel], effFb))
	if !SupportsEffort(provider, dispModel) {
		dispEff = "default"
	}
	return LaneSpec{
		ArgvModel: argvModel, DisplayModel: dispModel,
		ArgvEffort: argvEff, DisplayEffort: dispEff,
	}, nil
}

// McodeNoEffortModels lists mcode models whose reasoning is a thinking variant
// (thinking_config) rather than an effort level; the CLI rejects --effort for them.
var McodeNoEffortModels = map[string]bool{"minimax/minimax-m3": true}

// SupportsEffort reports whether a provider/model pair accepts an effort flag.
func SupportsEffort(provider, model string) bool {
	if provider != "mcode" {
		return true
	}
	return !McodeNoEffortModels[strings.ToLower(strings.TrimSpace(model))]
}

// ModelFamilyOf derives the model family of a model id ("" when unknown).
func ModelFamilyOf(model string) string {
	m := strings.ToLower(model)
	for _, fam := range []string{"swe-2-flash", "swe-2", "minimax", "muse", "grok", "glm"} {
		if strings.Contains(m, fam) {
			return fam
		}
	}
	return ""
}

// FallbackModel returns the model a pinned stage keeps when it fails over to
// another lane: a same-family model survives, a foreign one is scrubbed so the
// fallback lane never receives a model it cannot serve.
func FallbackModel(model, lane string) string {
	fam := ModelFamilyOf(model)
	if fam != "" && fam == fleet.ModelFamilies[lane] {
		return model
	}
	return ""
}
