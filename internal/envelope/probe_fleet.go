package envelope

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

const discoveryTTLSecs = 21600

var (
	grokDefRe   = regexp.MustCompile(`(?m)^\s*default\s*=\s*"([^"]+)"`)
	grokEffRe   = regexp.MustCompile(`(?m)^\s*default_reasoning_effort\s*=\s*"([^"]+)"`)
	devinEffRe  = regexp.MustCompile(`-(low|medium|high|xhigh|max)$`)
	devinLineRe = regexp.MustCompile(`^  ([a-z0-9][a-z0-9._-]+)\s{2,}.+?\[([^\]]+)\]`)
	grokLineRe  = regexp.MustCompile(`^\s*[*-]\s+([A-Za-z0-9._-]+)`)
)

// DevinConfig holds discovered pool-specific models for devin-{fusion,pro,fast}.
type DevinConfig struct {
	LaneModels  map[string]string
	LaneEfforts map[string]string
	Models      []string
	EffortMode  string
}

// DiscoveryCache stores live `<cli> models` results under discovery.json.
type DiscoveryCache struct {
	Version   int                 `json:"version"`
	UpdatedAt string              `json:"updated_at"`
	TS        float64             `json:"ts"`
	CLIModels map[string][]string `json:"cli_models"`
}

// ProbeGrokConfig reads default model, reasoning effort, and cached models for grok.
func ProbeGrokConfig(home string) CLIConfig {
	base := homeDir(home)
	tomlTxt, _ := readCfgJSON(filepath.Join(base, ".grok", "config.toml"))
	actMod, actEff := "", ""
	if m := grokDefRe.FindStringSubmatch(tomlTxt); len(m) == 2 {
		actMod = strings.TrimSpace(m[1])
	}
	if m := grokEffRe.FindStringSubmatch(tomlTxt); len(m) == 2 {
		actEff = strings.ToLower(strings.TrimSpace(m[1]))
	}
	_, cache := readCfgJSON(filepath.Join(base, ".grok", "models_cache.json"))
	seen := make(map[string]bool)
	var models []string
	if actMod != "" && !IsGeminiModel(actMod) {
		seen[actMod] = true
		models = append(models, actMod)
	}
	if rawM, ok := cache["models"].(map[string]any); ok {
		for mid, meta := range rawM {
			if metaMap, ok := meta.(map[string]any); ok {
				if info, ok := metaMap["info"].(map[string]any); ok && info["hidden"] == true {
					continue
				}
			}
			if !seen[mid] && !IsGeminiModel(mid) {
				seen[mid] = true
				models = append(models, mid)
			}
		}
	}
	return CLIConfig{
		ActiveModel: actMod, ActiveEffort: actEff, Models: models,
		AliasEfforts: map[string]string{},
		EffortMode:   "--reasoning-effort (medium|high|xhigh)",
	}
}

func mapStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return v
}

// ProbeDevinConfig reads per-pool models and inferred efforts from devin config.json.
func ProbeDevinConfig(home string) DevinConfig {
	base := homeDir(home)
	_, cfg := readCfgJSON(filepath.Join(base, ".config", "devin", "config.json"))
	agent, _ := cfg["agent"].(map[string]any)
	prefs, _ := agent["preferred_family_models"].(map[string]any)
	pro := orStr(mapStr(prefs, "swe-2"), mapStr(agent, "model"))
	fast := orStr(mapStr(prefs, "swe-2-flash"), orStr(mapStr(agent, "subagent_model"), mapStr(cfg, "model")))
	laneMods := map[string]string{
		"devin-fusion": mapStr(prefs, "fusion"), "devin-pro": pro, "devin-fast": fast,
	}
	for lane, m := range laneMods {
		// A reserved Gemini or orchestrator id must never reach the worker
		// argv, whatever the devin config says.
		if IsGeminiModel(m) {
			delete(laneMods, lane)
		}
	}
	laneEff := make(map[string]string)
	var models []string
	seen := make(map[string]bool)
	for _, lane := range []string{"devin-fusion", "devin-pro", "devin-fast"} {
		m := laneMods[lane]
		if m == "" {
			continue
		}
		if em := devinEffRe.FindStringSubmatch(strings.ToLower(m)); len(em) == 2 {
			laneEff[lane] = em[1]
		}
		if !seen[m] && !IsGeminiModel(m) {
			seen[m] = true
			models = append(models, m)
		}
	}
	return DevinConfig{
		LaneModels: laneMods, LaneEfforts: laneEff,
		Models: models, EffortMode: "via-model (no --effort flag)",
	}
}

func parseCLIModels(cliName, stdout string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, ln := range strings.Split(stdout, "\n") {
		item := ""
		switch cliName {
		case "devin":
			if m := devinLineRe.FindStringSubmatch(ln); len(m) == 3 && !IsGeminiModel(m[1]) {
				item = m[1]
				if strings.Contains(strings.ToLower(m[2]), "free") {
					item += "[free]"
				}
			}
		case "grok":
			if m := grokLineRe.FindStringSubmatch(ln); len(m) == 2 && !IsGeminiModel(m[1]) {
				item = m[1]
			}
		}
		if item != "" && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func probeCLIModels(cliName string) []string {
	cliPath := fleet.ResolveCLI(cliName)
	if cliPath == "" {
		return nil
	}
	res, err := fleet.RunCmd(fleet.CmdSpec{Argv: []string{cliPath, "models"}, TimeoutSec: 5})
	if err != nil || res.ExitCode != 0 {
		return nil
	}
	return parseCLIModels(cliName, res.Stdout)
}

// LoadDiscoveryCache loads or refreshes cached `devin|grok models` output.
func LoadDiscoveryCache(forceRefresh bool) map[string][]string {
	cpath := fleet.DiscoveryFilePath()
	var old DiscoveryCache
	if raw, err := os.ReadFile(cpath); err == nil {
		_ = json.Unmarshal(raw, &old)
	}
	if old.CLIModels == nil {
		old.CLIModels = make(map[string][]string)
	}
	now := float64(time.Now().UnixNano()) / 1e9
	fresh := len(old.CLIModels) > 0 && (now-old.TS) < discoveryTTLSecs
	if !forceRefresh && (fresh || os.Getenv("OODA_NO_AUTO_PROBE") == "1") {
		return old.CLIModels
	}
	cliMods := make(map[string][]string, 2)
	for k, v := range old.CLIModels {
		cliMods[k] = v
	}
	for _, cli := range []string{"devin", "grok"} {
		cliMods[cli] = probeCLIModels(cli)
	}
	_ = fleet.AtomicWriteJSON(cpath, DiscoveryCache{
		Version: 1, UpdatedAt: fleet.NowISO(), TS: now, CLIModels: cliMods,
	}, false, false)
	return cliMods
}
