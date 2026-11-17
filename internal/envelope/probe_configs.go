package envelope

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

var (
	vibeActRe = regexp.MustCompile(`(?m)^\s*active_model\s*=\s*"([^"]+)"`)
	vibeInlRe = regexp.MustCompile(`\{([^{}]+)\}`)
	vibeAlsRe = regexp.MustCompile(`\balias\s*=\s*"([^"]+)"`)
	vibeThkRe = regexp.MustCompile(`\bthinking\s*=\s*"([^"]+)"`)
	vibeSecRe = regexp.MustCompile(`(?m)^\s*\[\[models\]\]\s*$`)
	mmDefRe   = regexp.MustCompile(`(?m)^\s*defaultModel\s*:\s*["']?([^"'#\s]+)`)
	mmEffRe   = regexp.MustCompile(
		`(?m)^\s*defaultModelThinking\s*:\s*\n(?:\s*(?:#.*)?\n)*\s+effort\s*:\s*["']?([A-Za-z0-9_-]+)`,
	)
	mmModRe = regexp.MustCompile(`^\s*(?:-\s*)?((?:minimax/)?MiniMax-[A-Za-z0-9._-]+)\s*:?\s*$`)
)

// CLIConfig holds non-secret discovered model/effort config for a single-pool CLI.
type CLIConfig struct {
	ActiveModel  string
	ActiveEffort string
	Models       []string
	AliasEfforts map[string]string
	EffortMode   string
}

// IsGeminiModel reports whether a model name belongs to Gemini or Orchestrator.
func IsGeminiModel(name string) bool {
	return name != "" && fleet.GeminiModelRe.MatchString(name)
}

func homeDir(override string) string {
	if override != "" {
		return override
	}
	h, _ := os.UserHomeDir()
	return h
}

func readCfgJSON(path string) (string, map[string]any) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", map[string]any{}
	}
	text := string(raw)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return text, map[string]any{}
	}
	return text, m
}

// ProbeVibeConfig reads active_model and model/thinking entries from config.toml.
func ProbeVibeConfig(home string) CLIConfig {
	base := homeDir(home)
	var cands []string
	if home == "" && os.Getenv("VIBE_HOME") != "" {
		vh := os.Getenv("VIBE_HOME")
		if strings.HasPrefix(vh, "~/") {
			vh = filepath.Join(base, vh[2:])
		}
		cands = append(cands, filepath.Join(vh, "config.toml"))
	}
	cands = append(cands, filepath.Join(base, ".vibe", "config.toml"),
		filepath.Join(base, ".config", "vibe", "config.toml"))
	text := ""
	for _, c := range cands {
		if t, _ := readCfgJSON(c); t != "" {
			text = t
			break
		}
	}
	active := ""
	if m := vibeActRe.FindStringSubmatch(text); len(m) == 2 {
		active = strings.TrimSpace(m[1])
	}
	var chunks []string
	for _, m := range vibeInlRe.FindAllStringSubmatch(text, -1) {
		chunks = append(chunks, m[1])
	}
	for _, sec := range vibeSecRe.Split(text, -1)[1:] {
		chunks = append(chunks, strings.SplitN(sec, "\n[", 2)[0])
	}
	aliasEff, seen := make(map[string]string), make(map[string]bool)
	var models []string
	for _, ch := range chunks {
		am := vibeAlsRe.FindStringSubmatch(ch)
		if len(am) < 2 {
			continue
		}
		alias, think := strings.TrimSpace(am[1]), ""
		if tm := vibeThkRe.FindStringSubmatch(ch); len(tm) == 2 {
			think = strings.TrimSpace(tm[1])
		}
		if think != "" && aliasEff[alias] == "" {
			aliasEff[alias] = think
		}
		tag := alias
		if think != "" {
			tag += "[" + think + "]"
		}
		if !seen[tag] {
			seen[tag] = true
			models = append(models, tag)
		}
	}
	if active != "" && aliasEff[active] == "" && !seen[active] {
		models = append([]string{active}, models...)
	}
	return CLIConfig{
		ActiveModel: active, ActiveEffort: aliasEff[active],
		Models: models, AliasEfforts: aliasEff,
		EffortMode: "via-model (no --effort flag)",
	}
}

// ProbeMuseConfig reads non-secret model and per-model efforts from Muse settings.json.
func ProbeMuseConfig(home string) CLIConfig {
	base := homeDir(home)
	_, cfg := readCfgJSON(filepath.Join(base, ".config", "muse", "settings.json"))
	model, _ := cfg["model"].(string)
	eff, _ := cfg["reasoning_effort"].(string)
	aliasEff := make(map[string]string)
	var models []string
	if perRaw, ok := cfg["model_reasoning_efforts"].(map[string]any); ok {
		for k, v := range perRaw {
			if vs, ok := v.(string); ok {
				aliasEff[k] = vs
				models = append(models, k+"["+vs+"]")
			}
		}
	}
	if model != "" && aliasEff[model] == "" {
		models = append([]string{model}, models...)
	}
	return CLIConfig{
		ActiveModel: model, ActiveEffort: orStr(aliasEff[model], eff),
		Models: models, AliasEfforts: aliasEff,
		EffortMode: "--reasoning-effort (none..ultra)",
	}
}

// ProbeMinimaxConfig reads defaultModel and effort from mcode config.yaml.
func ProbeMinimaxConfig(home string) CLIConfig {
	base := homeDir(home)
	mmDir := filepath.Join(base, ".minimax")
	if home == "" && os.Getenv("MINIMAX_DATA_DIR") != "" {
		md := os.Getenv("MINIMAX_DATA_DIR")
		if strings.HasPrefix(md, "~/") {
			md = filepath.Join(base, md[2:])
		}
		mmDir = md
	}
	text, _ := readCfgJSON(filepath.Join(mmDir, "config.yaml"))
	actMod, actEff := "", ""
	if m := mmDefRe.FindStringSubmatch(text); len(m) == 2 {
		actMod = strings.TrimSpace(m[1])
	}
	if m := mmEffRe.FindStringSubmatch(text); len(m) == 2 {
		actEff = strings.ToLower(strings.TrimSpace(m[1]))
	}
	seen := make(map[string]bool)
	var models []string
	if actMod != "" {
		seen[actMod] = true
		models = append(models, actMod)
	}
	for _, ln := range strings.Split(text, "\n") {
		clean := strings.SplitN(ln, "#", 2)[0]
		if mm := mmModRe.FindStringSubmatch(clean); len(mm) == 2 {
			id := mm[1]
			if !strings.HasPrefix(id, "minimax/") {
				id = "minimax/" + id
			}
			if !seen[id] {
				seen[id] = true
				models = append(models, id)
			}
		}
	}
	return CLIConfig{
		ActiveModel: actMod, ActiveEffort: actEff, Models: models,
		AliasEfforts: map[string]string{},
		EffortMode:   "--effort (low|medium|high|xhigh|max)",
	}
}
