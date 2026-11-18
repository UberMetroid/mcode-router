package envelope

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

func testLane(tier, status string, resetAt, cool *string) *fleet.ProviderConfig {
	return &fleet.ProviderConfig{
		CLI: tier, Tier: tier, Status: status, NextResetAt: resetAt, CooldownUntil: cool,
	}
}

func TestRegimesAndOrder(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ts60 := now.Add(60 * time.Minute).Format(time.RFC3339)
	qCorner := &fleet.QuotaState{Providers: map[string]*fleet.ProviderConfig{
		"grok": testLane("capped", "active", &ts60, nil),
		"muse": testLane("capped", "active", nil, nil),
	}}
	em := ComputeEMEnvelope(qCorner, now)
	if len(em.CornerLanes) != 1 || em.CornerLanes[0] != "grok" {
		t.Fatalf("expected grok corner, got %v", em.CornerLanes)
	}
	if em.Regimes["grok"] != "EXPIRING" || em.Regimes["muse"] != "READY" {
		t.Fatalf("unexpected regimes: %v", em.Regimes)
	}
	qConserve := &fleet.QuotaState{Providers: map[string]*fleet.ProviderConfig{
		"grok": testLane("capped", "active", nil, nil),
		"muse": testLane("capped", "cooldown", nil, &ts60),
	}}
	if !ComputeEMEnvelope(qConserve, now).ConserveCapped {
		t.Fatal("expected ConserveCapped=true")
	}
	dq := fleet.DefaultQuotaData()
	rev, err := BuildCandidateOrder("auto", "review", dq, nil, map[string]bool{"swe-2": true})
	if err != nil || len(rev) == 0 || rev[0] != "mcode" {
		t.Fatalf("expected mcode first in review order, got %v err=%v", rev, err)
	}
	t.Setenv("MCODE_ROUTER_DEPTH", "0")
	t.Setenv("ROUTER_DEPTH", "0")
	t.Setenv("ROUTER_SCRATCH_DIR", t.TempDir())
	sel, err := SelectProvider(SelectReq{RequestedLane: "auto", Complexity: "review", Quota: dq,
		Exclude: map[string]bool{"mcode": true}, AvoidFamilies: map[string]bool{"swe-2": true},
		ResolveCLI: func(string) string { return "/opt/fake/cli" }})
	if err != nil || sel.Lane != "grok" {
		t.Fatalf("expected grok selection, got %+v (%v)", sel, err)
	}
	pinFast, err := BuildCandidateOrder("devin-fast", "fast", dq, nil, nil)
	if err != nil || len(pinFast) < 3 || pinFast[0] != "devin-fast" ||
		slices.Contains(pinFast, "devin-fusion") || slices.Index(pinFast, "mcode") > slices.Index(pinFast, "grok") {
		t.Fatalf("expected pinned fast quota-aware fallback without credits, got %v err=%v", pinFast, err)
	}
	pinPivot, _ := BuildCandidateOrder("grok", "act", dq, nil, map[string]bool{ModelFamily("grok"): true})
	if len(pinPivot) < 2 || pinPivot[0] == "grok" {
		t.Fatalf("expected mismatch pivot to rotate grok behind off-family lane, got %v", pinPivot)
	}
	if !HasLaneFamily("devin-fast,mcode", "minimax") || HasLaneFamily("devin-fast,grok", "minimax") {
		t.Fatal("HasLaneFamily CSV check failed")
	}
	autoAct, _ := BuildCandidateOrder("auto", "act", dq, nil, nil)
	autoDeep, _ := BuildCandidateOrder("auto", "deep", dq, nil, nil)
	if slices.Contains(autoAct, "devin-fusion") || slices.Contains(autoDeep, "devin-fusion") {
		t.Fatalf("auto order must exclude credits lane: act=%v deep=%v", autoAct, autoDeep)
	}
}

func TestProbeConfigsAllCLIs(t *testing.T) {
	home := t.TempDir()
	mustWrite := func(rel, body string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(".vibe/config.toml", `active_model = "glm-5-3"`+"\n"+`models = [{alias = "glm-5-3", thinking = "high"}]`)
	mustWrite(".config/muse/settings.json", `{"model":"muse-spark-1.3","model_reasoning_efforts":{"muse-spark-1.3":"high"}}`)
	mustWrite(".minimax/config.yaml", "defaultModel: minimax/MiniMax-M3.1-Flash\ndefaultModelThinking:\n  effort: max\n")
	mustWrite(".grok/config.toml", `default = "grok-4.7"`+"\n"+`default_reasoning_effort = "high"`)
	devinCfg := `{"agent":{"preferred_family_models":{"fusion":"f-1",` +
		`"swe-2":"swe-2-max","swe-2-flash":"swe-2-flash-max"}}}`
	mustWrite(".config/devin/config.json", devinCfg)

	if v := ProbeVibeConfig(home); v.ActiveModel != "glm-5-3" || v.ActiveEffort != "high" {
		t.Fatalf("unexpected vibe probe: %+v", v)
	}
	if m := ProbeMuseConfig(home); m.ActiveModel != "muse-spark-1.3" || m.ActiveEffort != "high" {
		t.Fatalf("unexpected muse probe: %+v", m)
	}
	if mm := ProbeMinimaxConfig(home); mm.ActiveEffort != "max" {
		t.Fatalf("unexpected mcode probe: %+v", mm)
	}
	if g := ProbeGrokConfig(home); g.ActiveModel != "grok-4.7" || g.ActiveEffort != "high" {
		t.Fatalf("unexpected grok probe: %+v", g)
	}
	if d := ProbeDevinConfig(home); d.LaneModels["devin-pro"] != "swe-2-max" {
		t.Fatalf("unexpected devin probe: %+v", d)
	}
}

func TestDiscoveryCacheAndSpec(t *testing.T) {
	home := t.TempDir()
	discFile := filepath.Join(home, "discovery.json")
	t.Setenv("OODA_DISCOVERY_FILE", discFile)
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	t.Setenv("ROUTER_SCRATCH_DIR", filepath.Join(home, "hud"))
	_ = fleet.AtomicWriteJSON(discFile, DiscoveryCache{
		Version: 1, TS: float64(time.Now().Unix()),
		CLIModels: map[string][]string{"devin": {"swe-2-max", "swe-2-flash-max"}},
	}, false, false)

	dq := fleet.DefaultQuotaData()
	spec, err := EffectiveLaneSpec("muse", dq.Providers["muse"], "", "", "", home)
	if err != nil {
		t.Fatalf("expected muse lane spec, got %+v (%v)", spec, err)
	}
	if _, err := EffectiveLaneSpec("muse", dq.Providers["muse"], "gemini-4-carbon-high", "", "", home); err == nil {
		t.Fatal("expected Gemini guardrail refusal on explicit model")
	}
	RecordMismatch(home, []string{"a.go"}, "mcode", "failed")
	if mm := LoadRecentMismatch(home, []string{"a.go"}); mm == nil || mm.Family != "minimax" {
		t.Fatalf("expected recent minimax mismatch, got %+v", mm)
	}
	ClearMismatch(home, []string{"a.go"})
	if LoadRecentMismatch(home, []string{"a.go"}) != nil {
		t.Fatal("expected cleared mismatch")
	}
}

func TestTariffsAndMusePaygSkip(t *testing.T) {
	t.Setenv("ZAI_API_KEY", "zai-test-token")
	t.Setenv("ZAI_BASE_URL", "")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("DEEPSEEK_BASE_URL", "")
	monPeak := time.Date(2026, 3, 2, 7, 0, 0, 0, time.UTC)
	monOff := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	monJam := time.Date(2026, 3, 2, 16, 0, 0, 0, time.UTC)
	if mult, badge := LaneTariff("mistral", nil, "glm-5", monPeak); mult != 2.0 || badge != "PEAK 2x" {
		t.Fatalf("expected Z.ai peak 2.0x, got %.2f %q", mult, badge)
	}
	if mult, badge := LaneTariff("mistral", nil, "glm-5", monOff); mult != 0.5 || badge != "0.5x off-peak" {
		t.Fatalf("expected Z.ai off-peak 0.5x, got %.2f %q", mult, badge)
	}
	if mult, badge := LaneTariff("mcode", nil, "MiniMax-M3.1", monJam); mult != 1.25 || badge != "peak-jam" {
		t.Fatalf("expected mcode peak-jam 1.25x, got %.2f %q", mult, badge)
	}
	t.Setenv("DEEPSEEK_BASE_URL", "https://api.deepseek.com")
	if mult, _ := LaneTariff("mistral", nil, "", monOff); mult != 0.5 {
		t.Fatalf("expected mistral DEEPSEEK_BASE_URL 0.5x off-peak, got %.2f", mult)
	}
	if mult, _ := LaneTariff("grok", nil, "", monOff); mult != 1.0 {
		t.Fatalf("expected DEEPSEEK_BASE_URL not to leak onto grok, got %.2f", mult)
	}
	hudDir := t.TempDir()
	t.Setenv("ROUTER_SCRATCH_DIR", hudDir)
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	t.Setenv("MCODE_ROUTER_DEPTH", "0")
	t.Setenv("ROUTER_DEPTH", "0")
	_ = fleet.AtomicWriteJSON(filepath.Join(hudDir, "muse_usage.json"), fleet.MuseProbe{
		Installed: true, PayAsYouGo: true, CheckedAt: float64(time.Now().Unix()),
	}, false, false)
	dq := fleet.DefaultQuotaData()
	fakeCLI := func(string) string { return "/opt/fake/cli" }
	excl := map[string]bool{"mcode": true, "grok": true, "mistral": true}
	selAct := func(l string) (SelectRes, error) {
		return SelectProvider(SelectReq{RequestedLane: l, Complexity: "act", Quota: dq, Exclude: excl, ResolveCLI: fakeCLI})
	}
	if s, err := selAct("auto"); err != nil || s.Lane == "muse" || !strings.Contains(s.SkippedNote, "muse") {
		t.Fatalf("expected auto to skip PAYG muse, got %+v (%v)", s, err)
	}
	if s, err := selAct("grok"); err != nil || s.Lane == "muse" || s.Lane == "devin-fusion" ||
		!strings.Contains(s.SkippedNote, "muse") {
		t.Fatalf("expected fallback off explicit grok to skip PAYG muse & credits, got %+v (%v)", s, err)
	}
	expSel, err := SelectProvider(SelectReq{RequestedLane: "muse", Complexity: "act", Quota: dq, ResolveCLI: fakeCLI})
	if err != nil || expSel.Lane != "muse" {
		t.Fatalf("expected explicit --lane muse to work despite PAYG, got %+v (%v)", expSel, err)
	}
	if s, _ := SelectProvider(SelectReq{RequestedLane: "devin-fusion", Complexity: "act",
		Quota: dq, ResolveCLI: fakeCLI}); s.Lane != "devin-fusion" {
		t.Fatalf("expected explicit --lane devin-fusion on act, got %+v", s)
	}
	if s, _ := SelectProvider(SelectReq{RequestedLane: "devin-fusion", Complexity: "review",
		Quota: dq, ResolveCLI: fakeCLI}); s.Lane == "devin-fusion" {
		t.Fatalf("credits lane must stay blocked on review, got %+v", s)
	}
}

func TestRegimeModelAndEffort(t *testing.T) {
	museCap := LaneCap{Models: []string{"muse-ember-1.0", "muse-wonder-1.0", "muse-spark-1.3-contributor"}}
	if m, eff := ResolveRegimeSpec("muse", "act", museCap); m != "muse-ember-1.0" || eff != "" {
		t.Fatalf("expected muse-ember-1.0 default eff on act, got %q %q", m, eff)
	}
	if m, eff := ResolveRegimeSpec("muse", "deep", museCap); m != "muse-wonder-1.0" || eff != "xhigh" {
		t.Fatalf("expected muse-wonder-1.0 xhigh on deep, got %q %q", m, eff)
	}
	if _, eff := ResolveRegimeSpec("mcode", "review", LaneCap{}); eff != "max" {
		t.Fatalf("expected mcode review max effort, got %q", eff)
	}
	home := t.TempDir()
	t.Setenv("OODA_DISCOVERY_FILE", filepath.Join(home, "disc.json"))
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	spec, err := EffectiveLaneSpec("muse", fleet.DefaultQuotaData().Providers["muse"], "", "", "deep", home)
	if err != nil {
		t.Fatalf("expected muse deep spec, got %+v (%v)", spec, err)
	}
}

func TestEWMAHealthDemotion(t *testing.T) {
	qp := filepath.Join(t.TempDir(), "q.json")
	_ = fleet.AtomicWriteJSON(qp, fleet.DefaultQuotaData(), false, true)
	rec := func(fail bool) (*fleet.QuotaState, []string) {
		_, _ = fleet.RecordUsage("mcode", "p", "x", &fleet.ExportUsage{Failed: fail}, qp)
		q, _ := fleet.LoadQuota(qp)
		ord, _ := BuildCandidateOrder("auto", "review", q, nil, nil)
		return q, ord
	}
	if q, ord := rec(true); ord[0] != "mcode" || *q.Providers["mcode"].HealthEWMA != 0.65 {
		t.Fatalf("1st fail stayed healthy: ord=%v ewma=%v", ord, q.Providers["mcode"].HealthEWMA)
	}
	if q, ord := rec(true); ord[0] == "mcode" {
		t.Fatalf("2nd fail (%.3f) must demote: ord=%v", *q.Providers["mcode"].HealthEWMA, ord)
	} else if exp, _ := BuildCandidateOrder("mcode", "review", q, nil, nil); exp[0] != "mcode" {
		t.Fatalf("explicit --lane mcode should stay first: %v", exp)
	}
	if q, ord := rec(false); *q.Providers["mcode"].HealthEWMA < 0.55 || ord[0] != "mcode" {
		t.Fatalf("clean run should recover >= 0.55 to head: ord=%v", ord)
	}
}

func TestEffortAndFamilyGuards(t *testing.T) {
	if SupportsEffort("mcode", "minimax/MiniMax-M3") {
		t.Fatal("MiniMax-M3 must not take effort")
	}
	if m := FallbackModel("swe-2-high", "devin-pro"); m != "swe-2-high" {
		t.Fatalf("same-family model dropped: %q", m)
	}
	if m := FallbackModel("minimax/MiniMax-M3", "devin-fast"); m != "" {
		t.Fatalf("foreign model kept: %q", m)
	}
	spec, err := EffectiveLaneSpec("mcode", testLane("mcode", "active", nil, nil),
		"minimax/MiniMax-M3", "max", "act", t.TempDir())
	if err != nil || spec.ArgvEffort != "" {
		t.Fatalf("effort leaked to MiniMax-M3: %+v %v", spec, err)
	}
}
