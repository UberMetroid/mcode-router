package dispatch

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/verify"
	"github.com/UberMetroid/mcode-router/internal/worktree"
)

func setEnvs(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestBuildWorkerCommandAllCLIs(t *testing.T) {
	wt, slot := t.TempDir(), t.TempDir()
	setEnvs(t, map[string]string{"MCODE_ROUTER_DEPTH": "0", "ROUTER_DEPTH": "2", "GH_TOKEN": "sec",
		"GITHUB_TOKEN": "sec", "AWS_SECRET_ACCESS_KEY": "sec", "OPENAI_API_KEY": "sec", "ANTHROPIC_API_KEY": "sec",
		"NPM_TOKEN": "sec", "SSH_AUTH_SOCK": "/tmp/s", "ACME_API_TOKEN": "sec", "STRIPE_SECRET_KEY": "sec"})
	for _, lane := range []string{"devin-pro", "mcode", "grok", "muse", "mistral"} {
		built, err := BuildWorkerCommand(CmdReq{Provider: lane, CLIPath: "/bin/" + lane,
			Prompt: "fix bug", Worktree: wt, SlotDir: slot, TaskID: "t1"})
		if err != nil || slices.Contains(built.Argv, "--model") || slices.Contains(built.Argv, "--effort") ||
			slices.Contains(built.Argv, "--reasoning-effort") {
			t.Fatalf("%s unpinned failed/leaked: err=%v argv=%v", lane, err, built.Argv)
		}
		for _, kv := range []string{"ROUTER_DEPTH=3", "MCODE_ROUTER_DEPTH=3",
			"GIT_TERMINAL_PROMPT=0", "GH_TOKEN=sec", "GITHUB_TOKEN=sec"} {
			if !slices.Contains(built.Env, kv) {
				t.Fatalf("%s missing env %s: %v", lane, kv, built.Env)
			}
		}
		ro, err := BuildWorkerCommand(CmdReq{Provider: lane, CLIPath: "/bin/" + lane, Model: "m-ovr",
			Effort: "low", Prompt: "audit", Worktree: wt, SlotDir: slot, TaskID: "t2", ReadOnly: true})
		if err != nil || !slices.Contains(ro.Argv, "m-ovr") {
			t.Fatalf("%s ro failed: err=%v argv=%v", lane, err, ro.Argv)
		}
		for _, k := range []string{"GH_TOKEN=", "GITHUB_TOKEN=", "AWS_SECRET_ACCESS_KEY=", "OPENAI_API_KEY=",
			"ANTHROPIC_API_KEY=", "NPM_TOKEN=", "SSH_AUTH_SOCK=", "ACME_API_TOKEN=", "STRIPE_SECRET_KEY="} {
			if slices.ContainsFunc(ro.Env, func(s string) bool { return strings.HasPrefix(s, k) }) {
				t.Fatalf("%s read-only leaked %s", lane, k)
			}
		}
	}
	for _, tc := range [][3]string{{"", "a", "20"}, {"inline", "a", "35"},
		{"inline", ".. <diff capped", "45"}, {"native", "a", "review"}} {
		b, _ := BuildWorkerCommand(CmdReq{Provider: "mcode", CLIPath: "/bin/m", Prompt: tc[1],
			Worktree: wt, SlotDir: slot, TaskID: "t3", ReadOnly: true, ReviewMode: tc[0]})
		if !slices.Contains(b.Argv, tc[2]) || (tc[0] == "native" && b.StreamFmt != "plain") {
			t.Fatalf("mcode mode=%q want %q got %v", tc[0], tc[2], b.Argv)
		}
	}
}

func TestNDJSONStreamRate(t *testing.T) {
	st := &streamTracker{phase: "wait"}
	st.consumeLine(`{"type":"content_block_delta","delta":{"text":"Checking files now. "}}`, 100.0)
	st.consumeLine(`{"type":"content_block_delta","delta":{"thinking":"Verifying invariants carefully."}}`, 100.5)
	rate, exact := st.currentRate(100.6)
	if st.phase != "stream" || exact || rate <= 0 {
		t.Fatalf("expected active ~tok/s > 0, got phase=%s rate=%.1f exact=%v", st.phase, rate, exact)
	}
	if dec, _ := st.currentRate(102.0); dec >= rate {
		t.Fatalf("expected sliding-window rate decay on pause, got %.1f >= %.1f", dec, rate)
	}
	beforeSamples := len(st.samples)
	st.consumeLine(`{"type":"tool_result","error":"err","content":"`+strings.Repeat("HUGE ", 500)+`"}`, 100.8)
	if st.phase != "tool" || len(st.samples) != beforeSamples {
		t.Fatalf("tool_result corrupted phase=%s or samples=%d!=%d", st.phase, len(st.samples), beforeSamples)
	}
	st.consumeLine(`{"status":"success","response":"Final answer","usage":{"total_tokens":420},"cost_usd":0.012}`, 101.0)
	if st.isError || !st.hasFinal || st.finalResp != "Final answer" || st.usage.Tokens == nil || *st.usage.Tokens != 420 {
		t.Fatalf("failed to capture final result/usage: %+v", st)
	}
	errSt := &streamTracker{phase: "wait"}
	errSt.consumeLine(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"Error: boom"}`, 101.0)
	if out, code, _ := finalizeOutput(BuiltCmd{StreamFmt: "ndjson"}, errSt, "", 0, false, 10); code != 1 ||
		!strings.Contains(out, "[error] Error: boom") {
		t.Fatalf("expected [error] + exit 1 on is_error result event, got rc=%d out=%q", code, out)
	}
	if _, isErr, qKill := extractJSONErr(map[string]any{"status": "429"}, nil); !isErr || !qKill {
		t.Fatalf("expected string status 429 to abort: isErr=%v qKill=%v", isErr, qKill)
	}
}

func TestNDJSONRealCLISchemas(t *testing.T) {
	st := &streamTracker{phase: "wait"}
	st.consumeLine(`{"type":"item.started","item":{"type":"reasoning","contentDelta":"Reviewing diff lines"}}`, 10.0)
	st.consumeLine(`{"type":"item.updated","item":{"type":"reasoning","contentDelta":" across packages now"}}`, 10.4)
	st.consumeLine(`{"type":"item.started","item":{"type":"message","contentDelta":"VERDICT: "}}`, 10.8)
	st.consumeLine(`{"type":"item.completed","item":{"type":"agent_message","content":"VERDICT: ACCEPT"}}`, 11.0)
	st.consumeLine(`{"type":"turn.completed","usage":{"inputTokens":1030,"outputTokens":20,"totalTokens":1050}}`, 11.1)
	st.consumeLine(`{"type":"exec.completed","result":{"status":"succeeded",`+
		`"output":"VERDICT: ACCEPT","usage":{"totalTokens":1050}}}`, 11.2)
	if rate, _ := st.currentRate(11.2); rate <= 0 || !st.hasFinal || st.finalResp != "VERDICT: ACCEPT" ||
		st.usage.Tokens == nil || *st.usage.Tokens != 1050 {
		t.Fatalf("mcode schema mismatch: hasFinal=%v resp=%q usage=%+v", st.hasFinal, st.finalResp, st.usage)
	}
	grok := &streamTracker{phase: "wait"}
	grok.consumeLine(`{"type":"thought","data":"Checking safety rules and intent carefully."}`, 20.0)
	grok.consumeLine(`{"type":"text","data":"VERDICT: ACCEPT"}`, 20.5)
	grok.consumeLine(`{"type":"end","usage":{"input_tokens":50,"output_tokens":10},"total_cost_usd":0.002}`, 20.6)
	if rate, _ := grok.currentRate(20.6); rate <= 0 || grok.cleanBuf.str() != "VERDICT: ACCEPT" ||
		grok.usage.Tokens == nil || *grok.usage.Tokens != 60 || grok.usage.CostUSD == nil {
		t.Fatalf("grok schema mismatch: buf=%q usage=%+v", grok.cleanBuf.str(), grok.usage)
	}
	muse := &streamTracker{phase: "wait"}
	muse.consumeLine(`{"payload_type":"run.output.delta","payload":{"text":"Streaming OK"}}`, 30.0)
	if muse.cleanBuf.str() != "Streaming OK" || muse.phase != "stream" {
		t.Fatalf("muse delta parse mismatch: phase=%s buf=%q", muse.phase, muse.cleanBuf.str())
	}
}

func TestExecStreamResult(t *testing.T) {
	tmp, fakeHome := t.TempDir(), t.TempDir()
	setEnvs(t, map[string]string{"HOME": fakeHome, "ROUTER_SCRATCH_DIR": tmp, "MCODE_ROUTER_DEPTH": "0", "ROUTER_DEPTH": "0"})
	script := filepath.Join(tmp, "s.sh")
	_ = os.WriteFile(script, []byte("#!/bin/sh\n"+
		"echo '{\"type\":\"result\",\"result\":\"ok_stream\",\"usage\":{\"total_tokens\":99}}'\n"), 0o755)
	mPath := HudWorkerStart(WorkerMarker{PID: os.Getpid(), TaskID: "s1", Mode: "observe", Lane: "mcode", Cwd: tmp})
	defer HudWorkerFinish(mPath, LastRunMarker{Mode: "observe", Lane: "mcode", ExitCode: 0})
	if res := ExecuteOnce(ExecReq{Provider: "mcode", CLIPath: script, Prompt: "hi", Worktree: tmp, SlotDir: tmp,
		TaskID: "s1", TimeoutSec: 10, ReadOnly: true, MarkerPath: mPath}); res.ExitCode != 0 ||
		!strings.Contains(res.Output, "ok_stream") || res.ExportInfo == nil || *res.ExportInfo.Tokens != 99 {
		t.Fatalf("unexpected ExecuteOnce stream result: %+v", res)
	}
}

func TestManifestScopeAndReject(t *testing.T) {
	repo, rows := t.TempDir(), []BatchRecord{}
	runGit := func(dir string, args ...string) {
		b := []string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@invalid", "-c", "commit.gpgsign=false"}
		if out, err := exec.Command("git", append(b, args...)...).CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	}
	runGit(repo, "init", "-q")
	_ = os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644)
	runGit(repo, "add", "a.go")
	runGit(repo, "commit", "-q", "-m", "init")
	qPath := filepath.Join(repo, ".quota.json")
	_ = fleet.AtomicWriteJSON(qPath, fleet.DefaultQuotaData(), false, true)
	setEnvs(t, map[string]string{"AGENT_QUOTA_FILE": qPath, "ROUTER_SCRATCH_DIR": filepath.Join(repo, ".hud"),
		"ROUTER_SLOTS_DIR": filepath.Join(repo, ".slots"), "MCODE_ROUTER_DEPTH": "0", "ROUTER_DEPTH": "0"})
	tasks := []worktree.TaskSpec{{ID: "1", Prompt: "p", Files: []string{"a.go"}, Lane: "mcode"},
		{ID: "2", Prompt: "p", Files: []string{"a.go"}, Lane: "mcode"}}
	rc := RunManifestBatch(repo, tasks, BatchOpts{
		Lane: "grok", Model: "grok-4.7", Effort: "xhigh", Jobs: 1, NoStripe: true, QuotaPath: qPath,
		ResolveCLI: func(string) string { return "/bin/true" },
		TaskRunner: func(r ExecReq) ExecRes {
			if r.Provider != "mcode" || r.Model == "grok-4.7" || r.Effort == "xhigh" {
				t.Fatalf("batch act-pin leaked onto task lane: %+v", r)
			}
			if r.TaskID == "2" {
				_ = os.RemoveAll(filepath.Join(r.Worktree, ".git"))
				return ExecRes{ExitCode: 0, Output: "done"}
			}
			if _, err := os.Stat(filepath.Join(repo, ".worktrees", "2")); !os.IsNotExist(err) {
				t.Fatalf("lazy worktree violated: .worktrees/2 exists while task 1 runs")
			}
			_ = os.WriteFile(filepath.Join(r.Worktree, "a.go"), []byte("package a\nvar X = 1\n"), 0o644)
			runGit(r.Worktree, "commit", "-am", "worker commit")
			return ExecRes{ExitCode: 0, Output: "done"}
		},
		ReviewRunner: func(r verify.ReviewExecReq) verify.ReviewExecRes {
			if r.TimeoutSec != fleet.ReviewTimeout {
				t.Fatalf("batch timeout=%d want %d", r.TimeoutSec, fleet.ReviewTimeout)
			}
			return verify.ReviewExecRes{ExitCode: 0, Output: "[P1] bad var\nVERDICT: REJECT"}
		},
	})
	raw, _ := os.ReadFile(filepath.Join(repo, ".worktrees", "results.json"))
	if rc != 1 || json.Unmarshal(raw, &rows) != nil || len(rows) != 2 || rows[0].Review.Verdict != "REJECT" ||
		rows[1].Review.Verdict != "REJECT" || !slices.Contains(rows[1].ScopeViolations, "@GIT_BINDING_TAMPERED") {
		t.Fatalf("unexpected batch rc=%d json=%s", rc, string(raw))
	}
}

func TestCorruptQuotaAndBoundedStream(t *testing.T) {
	badQ := filepath.Join(t.TempDir(), "bad_quota.json")
	_ = os.WriteFile(badQ, []byte("{corrupt"), 0o600)
	if _, err := RunTaskWithFallback(TaskReq{Task: worktree.TaskSpec{ID: "c1", Prompt: "hi"},
		Worktree: t.TempDir(), SlotDir: t.TempDir(), QuotaPath: badQ}); err == nil {
		t.Fatal("expected RunTaskWithFallback to fail closed on corrupt quota file")
	}
	var tb tailBuf
	for i := 0; i < 80; i++ {
		tb.addStr(strings.Repeat("é", 4096))
	}
	tb.addStr("END_TAIL")
	if out := tb.str(); len(out) > maxStreamBytes || len(tb.buf) > 2*maxStreamBytes ||
		!utf8.ValidString(out) || !strings.HasSuffix(out, "END_TAIL") {
		t.Fatalf("tailBuf invariant failed: out=%d buf=%d valid=%v", len(out), len(tb.buf), utf8.ValidString(out))
	}
	br := bufio.NewReaderSize(strings.NewReader(strings.Repeat("a", 2048)+"\ntail\n"), 64)
	if ln, err := readBoundedLine(br, 128); err != nil || len(ln) > 129 || maxLineBytes < 1<<20 {
		t.Fatalf("bounded line check failed: len=%d max=%d err=%v", len(ln), maxLineBytes, err)
	} else if next, nerr := readBoundedLine(br, 128); nerr != nil || next != "tail\n" {
		t.Fatalf("expected next line 'tail\\n' after drain, got %q (%v)", next, nerr)
	}
	for _, tc := range [][2]string{{"benign note", "VERDICT:"}, {"error: disk", "[stderr] error: disk\n"}} {
		var s streamTracker
		s.rawBuf.addStr("VERDICT: ACCEPT\n")
		if fo, fc, _ := finalizeOutput(BuiltCmd{StreamFmt: "plain"}, &s, tc[0], 0, false, 60); fc != 0 ||
			!strings.HasPrefix(fo, tc[1]) || !strings.HasSuffix(strings.TrimSpace(fo), "VERDICT: ACCEPT") {
			t.Fatalf("finalizeOutput err=%q rc=%d out=%q", tc[0], fc, fo)
		}
	}
	if ts, _ := ResolveTaskTimeout("", 0, false, "deep", true); ts != fleet.RegimeTimeouts["deep"] ||
		!fleet.HasErrorPattern(`Error: Status 402 {"type":"billing_vibe_budget_exhausted"}`, 2, fleet.QuotaErrorPats) {
		t.Fatalf("expected vibe 402 match and deep timeout %d, got %d", fleet.RegimeTimeouts["deep"], ts)
	}
}

func TestMidStreamQuotaAbortAndLine(t *testing.T) {
	tmp := t.TempDir()
	setEnvs(t, map[string]string{"ROUTER_SCRATCH_DIR": filepath.Join(tmp, "hud"),
		"MCODE_ROUTER_DEPTH": "0", "ROUTER_DEPTH": "0"})
	defer func(p float64) { stallIdleSec = p }(stallIdleSec)
	stallIdleSec = 0.3
	for _, tc := range [][4]string{{"ok.sh", "devin-fast", "echo 'Handled HTTP 429 retry cleanly.'", "HTTP 429"},
		{"h.sh", "mcode", "echo '{\"error\":\"insufficient_quota\"}'\nsleep 30", "insufficient_quota"},
		{"st.sh", "mcode", "echo '{\"delta\":{\"text\":\"x\"}}'\nsleep 30", "stream stalled"}} {
		p := filepath.Join(tmp, tc[0])
		_ = os.WriteFile(p, []byte("#!/usr/bin/env bash\n"+tc[2]+"\n"), 0o755)
		if r := ExecuteOnce(ExecReq{Provider: tc[1], CLIPath: p, Prompt: "hi", Worktree: tmp, SlotDir: tmp,
			TaskID: tc[0], TimeoutSec: 25}); (r.ExitCode != 0) != (tc[0] != "ok.sh") || r.TimedOut != (tc[0] == "st.sh") ||
			r.DurationS >= 5 || !strings.Contains(r.Output, tc[3]) {
			t.Fatalf("%s mismatch: dur=%.2fs %+v", tc[0], r.DurationS, r)
		}
	}
	if _, isErr, qKill := extractJSONErr(map[string]any{"error": "unexpected field 429"}, nil); !isErr || qKill {
		t.Fatalf("expected non-quota JSON error not to trigger qKill: isErr=%v qKill=%v", isErr, qKill)
	}
}
