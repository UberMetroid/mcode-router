package fleet

import (
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	// QuotaErrorPats detects rate-limit or credit errors on non-zero worker exits.
	QuotaErrorPats = []string{
		"http 429", "status 429", "error 429", "code 429", "429 too many", `"status":429`, `"code":429`,
		"rate limit", "rate_limit", "quota exceeded", "insufficient_quota", "usage limit", "too many requests",
		"credit balance", "insufficient credits", "out of credits", "credits exhausted", "limit reached",
		"resource_exhausted", "out of quota", "model quota", "quota exhausted", "limit exceeded",
		"status 402", "status_code\":402", "budget exhausted", "budget_exhausted",
	}
	// AuthErrorPats detects expired or missing CLI auth on non-zero exits.
	AuthErrorPats = []string{
		"authentication required", "not logged in", "run 'agent login'",
		"invalid api key", "please log in", "auth login",
	}
	LaneAliases = map[string]string{
		"auto": "auto", "default": "auto",
		"claude": "devin-fusion", "opus": "devin-fusion",
		"devin-fusion": "devin-fusion", "fusion": "devin-fusion",
		"devin": "devin-pro", "devin-pro": "devin-pro", "swe-2-pro": "devin-pro",
		"swe-2-max": "devin-pro", "pro": "devin-pro",
		"devin-fast": "devin-fast", "swe-2-fast": "devin-fast",
		"swe-2-flash": "devin-fast", "swe-2-flash-max": "devin-fast",
		"fast": "devin-fast", "flash": "devin-fast",
		"mcode": "mcode", "mcode-review": "mcode", "minimax": "mcode",
		"m3.1": "mcode", "m3": "mcode", "mcode-code": "mcode",
		"muse": "muse", "ember": "muse", "muse-ember": "muse",
		"muse-wonder": "muse", "muse-spark": "muse",
		"grok": "grok", "mistral": "mistral", "vibe": "mistral",
	}
	McodeModelAliases = map[string]string{
		"minimax/m3.1-flash": "minimax/MiniMax-M3.1-Flash", "m3.1-flash": "minimax/MiniMax-M3.1-Flash",
		"m3.1": "minimax/MiniMax-M3.1-Flash", "flash": "minimax/MiniMax-M3.1-Flash",
		"minimax/m3": "minimax/MiniMax-M3", "m3": "minimax/MiniMax-M3",
	}
	DevinModelAliases = map[string]string{
		"fusion": DefaultFusionModel, "fusion-opus": DefaultFusionModel,
		"fusion-sonnet": "fusion-claude-sonnet-5-5-high-sidekick-swe-2-medium",
	}
	MuseModelAliases = map[string]string{
		"ember": "muse-ember-1.0", "muse-ember": "muse-ember-1.0",
		"wonder": "muse-wonder-1.0", "wonder-full": "muse-wonder-full-1.0",
		"spark": "muse-spark-1.3", "spark-1.3": "muse-spark-1.3",
	}
	MistralModelAliases = map[string]string{
		"glm": "glm-5-3", "glm-5.3": "glm-5-3", "glm5.3": "glm-5-3",
		"zai-glm-5-3": "glm-5-3", "ml4": "mistral-large-4-0-3",
		"mistral-large-4": "mistral-large-4-0-3", "mistral-large-4-0": "mistral-large-4-0-3",
	}
	GrokEffortMap = map[string]string{
		"none": "medium", "minimal": "medium", "low": "medium",
		"medium": "medium", "high": "high", "xhigh": "xhigh",
		"max": "xhigh", "ultra": "xhigh",
	}
	McodeEffortMap = map[string]string{
		"default": "default", "none": "low", "minimal": "low", "low": "low",
		"medium": "medium", "high": "high", "xhigh": "xhigh",
		"max": "max", "ultra": "max",
	}
	MuseValidEfforts = map[string]bool{
		"none": true, "minimal": true, "low": true, "medium": true,
		"high": true, "xhigh": true, "max": true, "ultra": true,
	}
	durationMult = map[string]float64{
		"": 1, "s": 1, "m": 60, "h": 3600, "d": 86400,
	}
)

// NormalizeLane resolves user aliases to a canonical provider name.
func NormalizeLane(raw string, allowAuto bool) (string, error) {
	key := strings.ToLower(strings.TrimSpace(raw))
	if key == "" {
		key = "auto"
	}
	canon, ok := LaneAliases[key]
	if !ok || (!allowAuto && canon == "auto") {
		valid := append([]string(nil), ProviderOrder...)
		if allowAuto {
			valid = append([]string{"auto"}, valid...)
		}
		return "", UsageErr("error: unknown provider/lane %q (valid: %s)", raw, strings.Join(valid, ", "))
	}
	return canon, nil
}

// ParseLanes splits "auto" or CSV lane names into deduplicated canonical lanes.
func ParseLanes(raw string, allowAuto bool) ([]string, error) {
	text := strings.TrimSpace(raw)
	if text == "" || !strings.Contains(text, ",") {
		if text == "" {
			text = "auto"
		}
		canon, err := NormalizeLane(text, allowAuto)
		if err != nil {
			return nil, err
		}
		return []string{canon}, nil
	}
	seen := make(map[string]bool)
	var lanes []string
	for _, part := range strings.Split(text, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		canon, err := NormalizeLane(part, false)
		if err != nil {
			return nil, err
		}
		if !seen[canon] {
			seen[canon] = true
			lanes = append(lanes, canon)
		}
	}
	if len(lanes) == 0 {
		canon, err := NormalizeLane("", false)
		if err != nil {
			return nil, err
		}
		return []string{canon}, nil
	}
	return lanes, nil
}

// CheckModelGuard rejects Gemini/Orchestrator models on worker lanes.
func CheckModelGuard(model string) error {
	if model != "" && GeminiModelRe.MatchString(model) {
		return UsageErr(
			"error: Gemini/Orchestrator model %q is forbidden on worker lanes "+
				"(reserved for top-level orchestrator)",
			model,
		)
	}
	return nil
}

// NormalizeModel expands shorthand aliases and enforces the Gemini guardrail.
func NormalizeModel(provider, model string) (string, error) {
	raw := strings.TrimSpace(model)
	key := strings.ToLower(raw)
	if key == "" || key == "auto" || key == "default" || key == "cli-default" {
		return "", nil
	}
	if err := CheckModelGuard(raw); err != nil {
		return "", err
	}
	var table map[string]string
	switch provider {
	case "mcode":
		table = McodeModelAliases
	case "devin-fusion":
		table = DevinModelAliases
	case "muse":
		table = MuseModelAliases
	case "mistral":
		table = MistralModelAliases
	}
	if table != nil {
		if mapped, ok := table[key]; ok {
			return mapped, nil
		}
	}
	return raw, nil
}

// ResolveEffort picks effort from explicit flag, env var, or quota config.
func ResolveEffort(info *ProviderConfig, explicit string) string {
	chosen := strings.TrimSpace(explicit)
	if chosen == "" {
		if v := os.Getenv("AGENT_EFFORT"); v != "" {
			chosen = v
		} else if v := os.Getenv("OODA_EFFORT"); v != "" {
			chosen = v
		} else if info != nil && info.Effort != nil {
			chosen = *info.Effort
		}
	}
	key := strings.ToLower(strings.TrimSpace(chosen))
	if key == "" || key == "auto" || key == "default" || key == "cli-default" {
		return ""
	}
	return key
}

// ParseTimeout parses seconds or duration suffixes (s, m, h, d).
func ParseTimeout(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	m := DurationRe.FindStringSubmatch(trimmed)
	if len(m) != 3 {
		return 0, UsageErr("error: invalid timeout %q (use seconds or e.g. 10m, 45s)", raw)
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil || math.IsNaN(val) || math.IsInf(val, 0) {
		return 0, UsageErr("error: invalid timeout %q", raw)
	}
	secs := val * durationMult[strings.ToLower(m[2])]
	if math.IsNaN(secs) || math.IsInf(secs, 0) || secs <= 0 || secs > float64(math.MaxInt32) {
		return 0, UsageErr("error: timeout out of range (got %q)", raw)
	}
	res := int(math.Ceil(secs))
	if res < 1 {
		res = 1
	}
	return res, nil
}

// HasErrorPattern detects quota/auth patterns on non-zero exits.
func HasErrorPattern(out string, code int, pats []string) bool {
	if code == 0 || out == "" {
		return false
	}
	lower := strings.ToLower(out)
	for _, pat := range pats {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// ExtractResetTS extracts a future ISO or relative reset timestamp formatted locally.
func ExtractResetTS(out string) *string {
	if out == "" {
		return nil
	}
	nowUTC := time.Now().UTC()
	for _, m := range ResetTSRe.FindAllStringSubmatch(out, -1) {
		if len(m) < 2 {
			continue
		}
		if t, ok := ParseISOUTC(m[1]); ok && t.After(nowUTC) && t.Sub(nowUTC) <= 45*24*time.Hour {
			s := FormatLocalISO(t)
			return &s
		}
	}
	return ParseRelativeReset(out, time.Now())
}
