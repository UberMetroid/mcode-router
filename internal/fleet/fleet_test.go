package fleet

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func setupFleetEnv(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	qPath := filepath.Join(dir, "quota.json")
	hudDir := filepath.Join(dir, "hud")
	t.Setenv("AGENT_QUOTA_FILE", qPath)
	t.Setenv("ROUTER_SCRATCH_DIR", hudDir)
	return qPath, hudDir
}

func TestLanesAndGeminiGuard(t *testing.T) {
	lanes, err := ParseLanes("mcode, flash, vibe, mcode", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"mcode", "devin-fast", "mistral"}
	if len(lanes) != len(want) || lanes[0] != want[0] || lanes[1] != want[1] || lanes[2] != want[2] {
		t.Fatalf("got lanes %v, want %v", lanes, want)
	}
	if _, err := ParseLanes("auto,mcode", true); err == nil {
		t.Fatal("expected error when mixing auto with explicit lanes")
	}
	for _, bad := range []string{"gemini-4-carbon-high", "Orchestrator (Carbon)", "gemini-3.8-flash-low"} {
		if err := CheckModelGuard(bad); err == nil {
			t.Fatalf("expected Gemini guardrail error for %q", bad)
		} else {
			var ee *ExitError
			if !errors.As(err, &ee) || ee.Code != 2 {
				t.Fatalf("expected ExitError(2) for %q, got %v", bad, err)
			}
		}
	}
	if m, err := NormalizeModel("muse", "ember"); err != nil || m != "muse-ember-1.0" {
		t.Fatalf("got %q (%v)", m, err)
	}
}

func TestResetTSAndMigrate(t *testing.T) {
	near := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	future := ExtractResetTS("HTTP 429 Too Many Requests: resets at " + near.Format(time.RFC3339))
	if future == nil {
		t.Fatal("expected parsed near-future reset timestamp")
	}
	if ExtractResetTS("HTTP 429 Too Many Requests: resets at 2099-01-02T03:04:05Z") != nil {
		t.Fatal("expected far-future reset timestamp (>45d) to be ignored")
	}
	if dt, ok := ParseISOUTC(*future); !ok || dt.Unix() != near.Unix() {
		t.Fatalf("unexpected parsed UTC timestamp: %v (%v)", dt, ok)
	}
	if ExtractResetTS("429 quota exceeded, resets at 2001-01-01T00:00:00Z") != nil {
		t.Fatal("expected past reset timestamp to be ignored")
	}
	wake := FormatLocalISO(time.Now().Add(90 * time.Minute))
	q := &QuotaState{
		Version:              5,
		DefaultFallbackChain: []string{"cursor", "muse", "devin-fast"},
		Providers: map[string]*ProviderConfig{
			"cursor": {CLI: "cursor"},
			"mcode":  {CLI: "mcode", Tier: "unlimited", MaxParallel: Ptr(99999)},
			"muse":   {CLI: "muse", Tier: "capped", Status: "cooldown", CooldownUntil: &wake, CycleHours: Ptr(5.0)},
		},
	}
	if !MigrateQuota(q) || q.Version != QuotaSchemaVersion || q.Providers["cursor"] != nil {
		t.Fatalf("unexpected migration result: version=%d", q.Version)
	}
	if mp := q.Providers["mcode"].MaxParallel; q.Providers["mcode"].Tier != "high-cap" || mp == nil || *mp > 32 {
		t.Fatalf("mcode upgrade/clamp failed: %+v", q.Providers["mcode"])
	}
	if q.Providers["muse"].Status != "cooldown" || q.Providers["muse"].CooldownUntil == nil {
		t.Fatalf("expected active future cooldown preserved: %+v", q.Providers["muse"])
	}
}

func TestUsageBudgetsAndLock(t *testing.T) {
	qPath, _ := setupFleetEnv(t)
	if _, _, err := ConfigureCycle(CyclePatch{Provider: "grok", TokenBudget: Ptr(50), QuotaPath: qPath}); err != nil {
		t.Fatalf("ConfigureCycle failed: %v", err)
	}
	if _, _, err := ConfigureCycle(CyclePatch{
		Provider: "muse", Model: Ptr("gemini-4-argon-low"), QuotaPath: qPath,
	}); err == nil {
		t.Fatal("expected ConfigureCycle to block gemini model")
	}
	if nan := math.NaN(); true {
		if _, _, err := SetProviderStatus("grok", "cooldown", "m", &nan, false, qPath); err == nil {
			t.Fatal("expected SetProviderStatus to reject NaN hours")
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = RecordUsage("devin-fast", "prompt", "out", &ExportUsage{Tokens: Ptr(10)}, qPath)
		}()
	}
	wg.Wait()
	q, err := LoadQuota(qPath)
	if err != nil {
		t.Fatalf("LoadQuota failed: %v", err)
	}
	if q.Providers["devin-fast"].CycleRuns != 6 || q.Providers["devin-fast"].CycleTokens != 60 {
		t.Fatalf("lost concurrent usage updates: runs=%d tokens=%d",
			q.Providers["devin-fast"].CycleRuns, q.Providers["devin-fast"].CycleTokens)
	}
	entry, err := RecordUsage("grok", "big prompt", "big out", &ExportUsage{Tokens: Ptr(55)}, qPath)
	if err != nil {
		t.Fatalf("RecordUsage failed: %v", err)
	}
	if entry.Status != "cooldown" {
		t.Fatalf("expected grok to auto-cooldown on token budget, got %q", entry.Status)
	}
}

func TestParallelLeasesAndTimeout(t *testing.T) {
	setupFleetEnv(t)
	if sec, err := ParseTimeout("10m"); err != nil || sec != 600 {
		t.Fatalf("got timeout %d (%v)", sec, err)
	}
	if sec, err := ParseTimeout("0.5s"); err != nil || sec != 1 {
		t.Fatalf("got sub-second timeout %d (%v)", sec, err)
	}
	if _, err := ParseTimeout("0s"); err == nil {
		t.Fatal("expected refusal for 0s timeout")
	}
	var held []*os.File
	for i := 0; i < 4; i++ {
		fh, timedOut, err := AcquireParallelLease("devin-fast", 4, false, 0)
		if err != nil || timedOut || fh == nil {
			t.Fatalf("lease %d failed: fh=%v timedOut=%v err=%v", i, fh, timedOut, err)
		}
		held = append(held, fh)
	}
	if extra, _, err := AcquireParallelLease("devin-fast", 4, false, 0); CountActiveLeases("devin-fast", 4) != 4 ||
		err != nil || extra != nil {
		t.Fatalf("expected 4 active leases and nil 5th, got extra=%v err=%v", extra, err)
	}
	ReleaseParallelLease(held[0])
	if fifth, _, err := AcquireParallelLease("devin-fast", 4, false, 0); err != nil || fifth == nil {
		t.Fatalf("expected lease after releasing slot 0: %v", err)
	} else {
		ReleaseParallelLease(fifth)
	}
	for _, fh := range held[1:] {
		ReleaseParallelLease(fh)
	}
	if CountActiveLeases("devin-fast", 4) != 0 {
		t.Fatalf("expected 0 leases after cleanup")
	}
}

func TestV8WindowsResetAndEscalate(t *testing.T) {
	qPath, _ := setupFleetEnv(t)
	dq := DefaultQuotaData()
	if dq.Version != QuotaSchemaVersion || strings.Join(dq.Providers["grok"].Windows, "+") != "7d" ||
		strings.Join(dq.Providers["devin-pro"].Windows, "+") != "24h+7d" ||
		strings.Join(dq.Providers["mcode"].Windows, "+") != "5h+7d" {
		t.Fatalf("unexpected v8 defaults: v=%d grok=%+v", dq.Version, dq.Providers["grok"])
	}
	_ = AtomicWriteJSON(qPath, dq, false, true)
	now := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	if rel := ParseRelativeReset("rate limit exceeded, resets in 2d 5h", now); rel == nil {
		t.Fatal("expected ParseRelativeReset to match 2d 5h")
	} else if dt, ok := ParseISOUTC(*rel); !ok || dt.Sub(now) != 53*time.Hour {
		t.Fatalf("expected 53h delta, got %s (%v)", *rel, dt.Sub(now))
	}
	if bad := ParseRelativeReset("ordinary completion message", now); bad != nil {
		t.Fatalf("expected nil for non-reset message, got %q", *bad)
	}
	if prose := ExtractResetTS("Please retry in 5 minutes after starting the server"); prose != nil {
		t.Fatalf("expected nil for prose without quota context, got %q", *prose)
	}
	if HasErrorPattern("test failed at line 429", 1, QuotaErrorPats) ||
		!HasErrorPattern("HTTP 429 Too Many Requests", 1, QuotaErrorPats) {
		t.Fatalf("unexpected QuotaErrorPats match on bare 429 vs HTTP 429")
	}
	if _, err := MarkProviderCooldown("grok", "burst", true, "HTTP 429 Too Many Requests", qPath); err != nil {
		t.Fatalf("grok cooldown failed: %v", err)
	}
	q, _ := LoadQuota(qPath)
	if gr := q.Providers["grok"]; gr.NextResetAt != nil || gr.CooldownUntil == nil {
		t.Fatalf("expected grok transient cooldown without NextResetAt overwrite: %+v", gr)
	} else if dt, ok := ParseISOUTC(*gr.CooldownUntil); !ok || dt.Sub(time.Now()) > 2*time.Hour {
		t.Fatalf("expected <=2h transient grok cooldown, got %s", *gr.CooldownUntil)
	}
	if _, err := MarkProviderCooldown("mcode", "limit", true, "weekly usage limit hit", qPath); err != nil {
		t.Fatalf("MarkProviderCooldown failed: %v", err)
	}
	q, _ = LoadQuota(qPath)
	if mc := q.Providers["mcode"]; mc.Status != "cooldown" || mc.Reason == nil ||
		!strings.Contains(*mc.Reason, "outer window") {
		t.Fatalf("expected outer-window cooldown escalation, got %+v", mc)
	}
}

func TestParseMuseUsagePaygAndSub(t *testing.T) {
	soon := time.Now().UTC().Add(24 * time.Hour)
	if p := ParseMuseUsageJSON(`{"allowance":null,"reason":"api_key"}`, 0, false); !p.PayAsYouGo || p.Fresh || !p.Installed {
		t.Fatalf("expected PayAsYouGo=true on api_key payload, got %+v", p)
	}
	sub := ParseMuseUsageJSON(fmt.Sprintf(`{"allowance":{"tier":"Muse Code Power Usage","weekly":{"usedPercent":10},`+
		`"window":{"resetsAtMs":%d,"usedPercent":0}}}`, soon.UnixMilli()), 0, false)
	if sub.PayAsYouGo || !sub.Fresh || sub.ResetAt == nil || !strings.Contains(sub.Summary, "5h: 0% used") ||
		!strings.Contains(sub.Summary, "7d: 10% used") {
		t.Fatalf("expected live Muse Power probe, got %+v", sub)
	}
	if stale := ParseMuseUsageJSON(`{"allowance":{"resetsAt":"`+soon.Format(time.RFC3339)+`"}}`, 1, false); stale.Fresh {
		t.Fatalf("expected Fresh=false on non-zero muse exit, got %+v", stale)
	}
	for _, tc := range [][2]string{{"ember", "muse-ember-1.0"}, {"wonder", "muse-wonder-1.0"}} {
		if got, _ := NormalizeModel("muse", tc[0]); got != tc[1] {
			t.Fatalf("expected %s, got %q", tc[1], got)
		}
	}
	if _, err := NormalizeModel("muse", "gemini-4-carbon-low"); err == nil {
		t.Fatal("expected Gemini guardrail refusal on NormalizeModel")
	}
}

func TestResolveCLIAndBoundedRunCmd(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	_ = os.MkdirAll(sub, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "evil"), []byte("#!/bin/sh\necho evil\n"), 0o755)
	echoBin, _ := exec.LookPath("echo")
	if echoBin == "" {
		t.Skip("echo not found")
	}
	t.Setenv("PATH", sub)
	if ResolveCLI("../evil") != "" || ResolveCLI("/bin/sh") != "" {
		t.Fatalf("expected unsafe CLI paths blocked")
	}
	defer func(p int64) { maxCmdOutBytes = p }(maxCmdOutBytes)
	maxCmdOutBytes = 64
	if res, err := RunCmd(CmdSpec{Argv: []string{echoBin, strings.Repeat("x", 200)}, TimeoutSec: 5}); err != nil ||
		int64(len(res.Stdout)) > maxCmdOutBytes {
		t.Fatalf("expected RunCmd stdout <= %d bytes, got len=%d err=%v", maxCmdOutBytes, len(res.Stdout), err)
	}
}
