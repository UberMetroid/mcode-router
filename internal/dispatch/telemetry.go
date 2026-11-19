package dispatch

import (
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

// maxExportBytes bounds how much of a worker-produced export file is read.
const maxExportBytes = 1 << 20

var (
	fusionModelRe = regexp.MustCompile(
		`^fusion-claude-([a-z]+)-(\d+)-(\d+)-(high|medium|low)(?:-sidekick-([a-z]+)-(\d+))?`,
	)
	effortAbbr = map[string]string{"high": "hi", "medium": "med", "low": "lo"}
)

func jsonNum(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return x, true
	case int:
		return float64(x), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	default:
		return 0, false
	}
}

// clampTokens keeps a child-reported token count in a sane, non-negative range
// so a malformed export can never shrink the quota counter or overflow it.
var clampTokens = func(n float64) int {
	if n <= 0 {
		return 0
	}
	if n > 1e9 {
		return 1000000000
	}
	return int(n)
}

func extractUsageTokens(u map[string]any) (int, bool) {
	if u == nil {
		return 0, false
	}
	for _, k := range []string{"total_tokens", "totalTokens"} {
		if n, ok := jsonNum(u[k]); ok && n > 0 {
			return clampTokens(n), true
		}
	}
	for i, k := range []string{"output_tokens", "outputTokens"} {
		if n, ok := jsonNum(u[k]); ok && n > 0 {
			inTok, _ := jsonNum(u[[]string{"input_tokens", "inputTokens"}[i]])
			if inTok < 0 {
				inTok = 0
			}
			return clampTokens(n + inTok), true
		}
	}
	return 0, false
}

// ParseVibeExport extracts summed token and USD cost totals from vibe export.json.
func ParseVibeExport(exportDir string) *fleet.ExportUsage {
	if exportDir == "" {
		return nil
	}
	fh, oerr := os.Open(filepath.Join(exportDir, "export.json"))
	if oerr != nil {
		return nil
	}
	defer fh.Close()
	raw, err := io.ReadAll(io.LimitReader(fh, maxExportBytes+1))
	if err != nil {
		return nil
	}
	if len(raw) > maxExportBytes {
		return nil
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil || data == nil {
		return nil
	}
	var totTok int
	var hasTok, hasCost bool
	var totCost float64
	collect := func(src map[string]any) {
		if u, ok := src["usage"].(map[string]any); ok {
			if n, ok := extractUsageTokens(u); ok {
				totTok += n
				hasTok = true
			}
		}
		if c, ok := jsonNum(src["cost_usd"]); ok && c > 0 {
			// A zero or negative cost counts as no cost data, and a runaway
			// figure is capped: a child must never be able to walk the spend
			// counter backwards, or blow it up past the budget arithmetic.
			totCost += min(c, 1e6)
			hasCost = true
		}
	}
	collect(data)
	if subs, ok := data["subagents"].([]any); ok {
		for _, item := range subs {
			if sub, ok := item.(map[string]any); ok {
				collect(sub)
			}
		}
	}
	if !hasTok && !hasCost {
		return nil
	}
	exp := &fleet.ExportUsage{}
	if hasTok {
		exp.Tokens = fleet.Ptr(totTok)
	}
	if hasCost {
		exp.CostUSD = fleet.Ptr(math.Round(totCost*1e6) / 1e6)
	}
	return exp
}
