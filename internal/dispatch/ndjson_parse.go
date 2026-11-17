package dispatch

import (
	"encoding/json"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

const (
	rateWindowSec = 4.0
	rateStaleSec  = 2.5
	rateMinSpan   = 0.35
	rateMinBytes  = 16
)

type streamSample struct {
	at    float64
	bytes int
}

type streamTracker struct {
	phase, finalResp, errText                    string
	sawDelta, hasFinal, isError, qAbort, stalled bool
	cleanBuf, turnBuf, rawBuf                    tailBuf
	usage                                        fleet.ExportUsage
	samples                                      []streamSample
	firstDelta, lastDelta, lastIO                float64
}

func (st *streamTracker) addRateBytes(now float64, n int, isDelta bool) {
	if n <= 0 {
		return
	}
	if isDelta {
		st.sawDelta = true
	} else if st.sawDelta {
		return
	}
	if st.firstDelta == 0 || (now-st.lastDelta) > rateStaleSec {
		st.firstDelta = now
	}
	st.phase, st.lastDelta = "stream", now
	st.samples = append(st.samples, streamSample{at: now, bytes: n})
}

func (st *streamTracker) currentRate(now float64) (float64, bool) {
	cut := now - rateWindowSec
	keep := 0
	for keep < len(st.samples) && st.samples[keep].at < cut {
		keep++
	}
	if keep > 0 {
		st.samples = st.samples[keep:]
	}
	if len(st.samples) == 0 || (now-st.lastDelta) > rateStaleSec {
		return 0, false
	}
	tot := 0
	for _, s := range st.samples {
		tot += s.bytes
	}
	if tot < rateMinBytes {
		return 0, false
	}
	span := min(rateWindowSec, max(now-st.firstDelta, rateMinSpan))
	return math.Round((float64(tot) / 4.0) / span), false
}

func isToolEvent(ev map[string]any) bool {
	typ, _ := ev["type"].(string)
	role, _ := ev["role"].(string)
	if role == "tool" || role == "toolResult" {
		return true
	}
	item, _ := ev["item"].(map[string]any)
	it, _ := item["type"].(string)
	for _, t := range []string{typ, it} {
		switch t {
		case "tool_use", "tool_call", "tool_calls", "function_call",
			"command_execution", "mcp_tool_call", "tool_result":
			return true
		}
	}
	for _, k := range []string{"tool_calls", "toolCall", "tool_result"} {
		if v, exists := ev[k]; exists && v != nil {
			return true
		}
	}
	return false
}

func (st *streamTracker) consumeDelta(ev map[string]any, now float64) bool {
	var txt, think string
	if d, ok := ev["delta"].(map[string]any); ok {
		txt, _ = d["text"].(string)
		think, _ = d["thinking"].(string)
		if think == "" {
			think, _ = d["reasoning_content"].(string)
		}
	}
	typ, _ := ev["type"].(string)
	pt, _ := ev["payload_type"].(string)
	item, _ := ev["item"].(map[string]any)
	it, _ := item["type"].(string)
	cd, _ := item["contentDelta"].(string)
	switch {
	case txt == "" && (typ == "content_block_delta" || strings.HasSuffix(typ, ".delta")):
		txt, _ = ev["text"].(string)
	case typ == "thought" && think == "":
		think, _ = ev["data"].(string)
	case typ == "text" && txt == "":
		txt, _ = ev["data"].(string)
	case pt == "run.output.delta":
		p, _ := ev["payload"].(map[string]any)
		txt, _ = p["text"].(string)
	case cd != "" && it == "reasoning":
		think = cd
	case cd != "" && (it == "agent_message" || it == "message"):
		txt = cd
	}
	if txt == "" && think == "" {
		return false
	}
	st.cleanBuf.addStr(txt)
	st.addRateBytes(now, utf8.RuneCountInString(txt)+utf8.RuneCountInString(think), true)
	return true
}

func (st *streamTracker) consumeContentItems(items []any, now float64) {
	hasText, hasTool := false, false
	for _, raw := range items {
		blk, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		btype, _ := blk["type"].(string)
		switch btype {
		case "tool_use", "tool_result", "tool_call":
			hasTool = true
		case "thinking", "reasoning":
			if th, ok := blk["thinking"].(string); ok && th != "" {
				st.addRateBytes(now, utf8.RuneCountInString(th), false)
				hasText = true
			}
		case "text", "output_text", "":
			if txt, ok := blk["text"].(string); ok && txt != "" {
				if len(st.turnBuf.buf) > 0 {
					st.turnBuf.addStr("\n")
				}
				st.turnBuf.addStr(txt)
				st.addRateBytes(now, utf8.RuneCountInString(txt), false)
				hasText = true
			}
		}
	}
	if hasTool && !hasText {
		st.phase = "tool"
	}
}

func (st *streamTracker) consumeMeta(ev map[string]any) {
	resMap, _ := ev["result"].(map[string]any)
	u, _ := ev["usage"].(map[string]any)
	for _, alt := range []any{ev["stats"], resMap["usage"]} {
		if u == nil {
			u, _ = alt.(map[string]any)
		}
	}
	if n, ok := extractUsageTokens(u); ok && (st.usage.Tokens == nil || n > *st.usage.Tokens) {
		st.usage.Tokens = fleet.Ptr(n)
	}
	for _, k := range []string{"total_cost_usd", "cost_usd"} {
		if c, ok := jsonNum(ev[k]); ok && c > 0 {
			st.usage.CostUSD = fleet.Ptr(math.Round(c*1e6) / 1e6)
			break
		}
	}
	if errMsg, isErr, qKill := extractJSONErr(ev, resMap); isErr {
		st.isError, st.errText, st.qAbort = true, errMsg, st.qAbort || qKill
		return
	}
	for _, v := range []any{resMap["output"], ev["response"], ev["result"]} {
		if s, ok := v.(string); ok && s != "" {
			st.finalResp, st.hasFinal = s, true
			status, _ := ev["status"].(string)
			if status == "" {
				status, _ = resMap["status"].(string)
			}
			lowSt := strings.ToLower(status)
			if !st.qAbort && (lowSt == "success" || lowSt == "succeeded" || lowSt == "completed") {
				st.isError, st.errText = false, ""
			}
			break
		}
	}
}

func (st *streamTracker) consumeAssistantContent(content any, now float64) {
	if items, ok := content.([]any); ok {
		st.consumeContentItems(items, now)
		return
	}
	if s, ok := content.(string); ok && s != "" {
		if len(st.turnBuf.buf) > 0 {
			st.turnBuf.addStr("\n")
		}
		st.turnBuf.addStr(s)
		st.addRateBytes(now, utf8.RuneCountInString(s), false)
	}
}

func (st *streamTracker) consumeLine(line string, now float64) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}
	var ev map[string]any
	if !strings.HasPrefix(trimmed, "{") || json.Unmarshal([]byte(trimmed), &ev) != nil || ev == nil {
		st.rawBuf.addStr(line + "\n")
		if !st.sawDelta && IsLiveQuotaLine(trimmed) {
			st.isError, st.errText, st.qAbort = true, trimmed, true
		}
		return
	}
	if isToolEvent(ev) {
		st.phase = "tool"
		return
	}
	st.consumeMeta(ev)
	if st.consumeDelta(ev, now) {
		return
	}
	typ, _ := ev["type"].(string)
	msg, _ := ev["message"].(map[string]any)
	if r, _ := msg["role"].(string); r == "assistant" {
		st.consumeAssistantContent(msg["content"], now)
		return
	}
	if role, _ := ev["role"].(string); role == "assistant" || typ == "assistant" {
		st.consumeAssistantContent(ev["content"], now)
		return
	}
	item, _ := ev["item"].(map[string]any)
	if it, _ := item["type"].(string); it == "agent_message" || it == "message" {
		val := item["content"]
		if val == nil {
			val = item["text"]
		}
		st.consumeAssistantContent(val, now)
	}
}
