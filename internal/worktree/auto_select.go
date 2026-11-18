package worktree

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

var (
	wrapperSkills = map[string]bool{
		"devin": true, "mcode": true,
		"grok": true, "muse": true, "vibe": true, "mistral": true,
	}
	skillStopWords = map[string]bool{
		"with": true, "from": true, "this": true, "that": true,
		"have": true, "when": true, "what": true, "into": true,
		"code": true, "task": true, "tasks": true, "file": true,
		"files": true, "agent": true, "agents": true, "work": true,
		"only": true, "runs": true, "make": true, "your": true,
	}
	deepCues = []string{
		"architect", "concurren", "deadlock", "security", "cryptograph",
		"protocol", "redesign", "migrate", "multi-window", "race ",
	}
	fastCues = []string{
		"typo", "rename", "comment", "readme", "changelog",
		"format", "lint", "spelling", "docstring",
	}
)

// InferComplexity estimates task regime ("recon", "fast", "act", "deep") from prompt and files.
func InferComplexity(prompt string, files []string, readOnly bool) string {
	if readOnly {
		return "recon"
	}
	low := strings.ToLower(prompt)
	if len(files) >= 4 {
		return "deep"
	}
	for _, cue := range deepCues {
		if strings.Contains(low, cue) {
			return "deep"
		}
	}
	if len(files) <= 1 && len(prompt) < 280 {
		for _, cue := range fastCues {
			if strings.Contains(low, cue) {
				return "fast"
			}
		}
	}
	return "act"
}

func wordSet(text string) map[string]bool {
	out := make(map[string]bool)
	var cur strings.Builder
	flush := func() {
		if cur.Len() >= 2 {
			out[cur.String()] = true
		}
		cur.Reset()
	}
	for _, r := range strings.ToLower(text) {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlnum || r == '-' || r == '_' {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func scoreSkillMatch(sk SkillMeta, words map[string]bool, hay string) int {
	score := 0
	skLow := strings.ToLower(sk.Name)
	if words[skLow] {
		score += 3
	}
	seen := make(map[string]bool)
	for tok := range wordSet(sk.Name + " " + sk.Desc) {
		if len(tok) < 4 || skillStopWords[tok] || seen[tok] {
			continue
		}
		seen[tok] = true
		if words[tok] || (len(tok) >= 5 && strings.Contains(hay, tok)) {
			score++
		}
	}
	return score
}

// AutoMatchSkills returns up to 2 discovered non-core domain skills matching prompt or files.
func AutoMatchSkills(prompt string, files []string, cwd string) []string {
	var parts []string
	parts = append(parts, prompt)
	for _, f := range files {
		parts = append(parts, f, strings.TrimPrefix(filepath.Ext(f), "."))
	}
	hay := strings.ToLower(strings.Join(parts, " "))
	words := wordSet(hay)
	type cand struct {
		name  string
		score int
	}
	var cands []cand
	for _, sk := range DiscoverSkills(cwd) {
		if sk.Core || wrapperSkills[strings.ToLower(sk.Name)] {
			continue
		}
		if sc := scoreSkillMatch(sk, words, hay); sc >= 3 {
			cands = append(cands, cand{name: sk.Name, score: sc})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].name < cands[j].name
	})
	limit := min(2, len(cands))
	out := make([]string, limit)
	for i := 0; i < limit; i++ {
		out[i] = cands[i].name
	}
	return out
}

// ResolveTaskSkills honors explicit --skill flags or --skill none / OODA_NO_AUTO_SKILL opt-outs.
func ResolveTaskSkills(explicit []string, prompt string, files []string, cwd string) []string {
	for _, s := range explicit {
		low := strings.ToLower(strings.TrimSpace(s))
		if low == "none" || low == "off" {
			return nil
		}
	}
	if len(explicit) > 0 {
		return explicit
	}
	if os.Getenv("OODA_NO_AUTO_SKILL") == "1" {
		return nil
	}
	return AutoMatchSkills(prompt, files, cwd)
}

// PresetStages maps named presets (e.g. conserve, deep, fusion) to 4-stage maps.
func PresetStages(name string) (map[string]fleet.StageRoute, bool) {
	mk := func(obs, ori, act, ver string, oriEff, verEff *string, mods ...string) map[string]fleet.StageRoute {
		modAt := func(i int) *string {
			if i < len(mods) && mods[i] != "" {
				return fleet.Ptr(mods[i])
			}
			return nil
		}
		return map[string]fleet.StageRoute{
			"observe": {Lane: obs, Model: modAt(0)},
			"orient":  {Lane: ori, Effort: oriEff, Model: modAt(1)},
			"act":     {Lane: act, Model: modAt(2)},
			"verify":  {Lane: ver, Effort: verEff, Model: modAt(3)},
		}
	}
	res, ok := map[string]map[string]fleet.StageRoute{
		"auto":     mk("auto", "auto", "auto", "auto", nil, nil),
		"devin":    mk("devin-fast", "devin-pro", "devin-pro", "mcode", nil, nil),
		"conserve": mk("devin-fast", "devin-pro", "devin-pro,devin-fast", "mcode", nil, nil),
		"deep":     mk("devin-fast", "muse,grok", "devin-pro,muse", "mcode", fleet.Ptr("xhigh"), nil),
		"mcode":    mk("mcode", "mcode", "mcode", "devin-fast", nil, nil),
		"grok":     mk("devin-fast", "grok", "grok", "mcode", nil, nil),
		"muse":     mk("devin-fast", "muse", "muse", "mcode", nil, nil),
		"mistral":  mk("devin-fast", "mistral", "mistral", "mcode", nil, nil),
		"fusion": mk("mcode", "mcode", "devin-fast", "mcode", fleet.Ptr("max"), fleet.Ptr("max"),
			"minimax/MiniMax-M3.1-Flash", "minimax/MiniMax-M3.1-Flash",
			"swe-2-flash-high", "minimax/MiniMax-M3.1-Flash"),
	}[strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "ooda-")]
	return res, ok
}
