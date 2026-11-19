package verify

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

func runGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	full := append([]string{"-c", "user.name=ooda-test", "-c", "user.email=ooda@example.invalid",
		"-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func TestCollectDiffContext(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	_ = os.MkdirAll(filepath.Join(repo, ".worktrees"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, ".worktrees", "junk.go"), []byte("package junk\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "new.txt"), []byte("héllo wörld\nsecond\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "bin.dat"), []byte{0x00, 0x01, 0x02, 'x'}, 0o644)
	ctx := CollectDiffContext(repo, false)
	for _, noise := range []string{"fatal", "bad revision", "unknown revision", "ambiguous argument"} {
		if strings.Contains(ctx.Diff, noise) {
			t.Fatalf("unborn diff leaked git noise %q:\n%s", noise, ctx.Diff)
		}
	}
	if !ctx.HasChanges || ctx.DiffStat != "(no tracked diff)" {
		t.Fatalf("unexpected unborn ctx: %+v", ctx)
	}
	if !strings.Contains(ctx.Diff, "--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1,2 @@\n+héllo wörld\n+second\n") {
		t.Fatalf("missing utf8 untracked diff:\n%s", ctx.Diff)
	}
	if !strings.Contains(ctx.Diff, "Binary files /dev/null and b/bin.dat differ") {
		t.Fatalf("missing binary notice:\n%s", ctx.Diff)
	}
	if strings.Contains(ctx.Diff, ".worktrees/junk.go") {
		t.Fatalf("diff included ignored .worktrees file")
	}
	_ = os.WriteFile(filepath.Join(repo, "staged.txt"), []byte("hello staged\n"), 0o644)
	runGit(t, repo, "add", "staged.txt")
	if stagedCtx := CollectDiffContext(repo, false); !strings.Contains(stagedCtx.Diff, "+++ b/staged.txt") {
		t.Fatalf("expected unborn HEAD staged diff, got %q", stagedCtx.Diff)
	}
	_ = os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("a := 1\n"), 0o644)
	runGit(t, repo, "add", "tracked.go")
	runGit(t, repo, "commit", "-q", "-m", "seed")
	_ = os.WriteFile(filepath.Join(repo, "tracked.go"), []byte("a := 2\n"), 0o644)
	ctx2 := CollectDiffContext(repo, false)
	if !strings.Contains(ctx2.Diff, "-a := 1\n+a := 2") {
		t.Fatalf("missing tracked diff:\n%s", ctx2.Diff)
	}
}

func TestVerdictRulesMatrix(t *testing.T) {
	bad := []string{
		"[P1] bug", "[p1] lower bug", "[P0-3] issue: bad", "- [P1] bullet bug",
		"### [P1] heading", "**Finding:** [P1] desc", "src/f.go:12: [P1] bug",
		"[P1] crash when no config exists", "No workaround exists for [P1] null dereference",
		"No [P0] issues, but [P1] bug", "VERDICT: REJECT - x",
	}
	for _, s := range bad {
		if got := ParseReviewVerdict(s + "\nVERDICT: ACCEPT"); got != "REJECT" {
			t.Fatalf("expected REJECT for %q, got %s", s, got)
		}
	}
	clean := []string{
		"None of [P0-3] apply.", "No [P0-3] issues found.",
		"No [P1] issues found.", "There are no [P0] or [P1] findings.",
	}
	for _, s := range clean {
		if got := ParseReviewVerdict(s + "\nVERDICT: ACCEPT"); got != "ACCEPT" {
			t.Fatalf("expected ACCEPT for %q, got %s", s, got)
		}
	}
	if ParseReviewVerdict("No reportable issues were found.") != "ACCEPT" {
		t.Fatalf("expected clean short note to ACCEPT")
	}
	unk := []string{
		"1. bug\n2. more\n3. gap\nNo reportable issues were found.",
		"VERDICT: ACCEPT\ntrailing noise",
		"VERDICT: ACCEPT - conditional caveat",
		"Looks reasonable overall.",
		"",
	}
	for _, s := range unk {
		if got := ParseReviewVerdict(s); got != "UNKNOWN" {
			t.Fatalf("expected UNKNOWN for %q, got %s", s, got)
		}
	}
}

func fakeCLIResolver(installed map[string]string) func(string) string {
	return func(name string) string {
		return installed[name]
	}
}

func TestReviewFallbackAndUTF8Inline(t *testing.T) {
	tmp := t.TempDir()
	qPath := filepath.Join(tmp, "quota.json")
	for k, v := range map[string]string{
		"AGENT_QUOTA_FILE": qPath, "ROUTER_SCRATCH_DIR": filepath.Join(tmp, "hud"),
		"MCODE_ROUTER_DEPTH": "0", "ROUTER_DEPTH": "0", "OODA_NO_AUTO_PROBE": "1",
	} {
		t.Setenv(k, v)
	}
	_ = fleet.AtomicWriteJSON(qPath, fleet.DefaultQuotaData(), false, true)
	installed := map[string]string{"devin": "/bin/true", "mcode": "/bin/true", "grok": "/bin/true"}
	var calls []ReviewExecReq
	idx := 0
	outputs := []ReviewExecRes{
		{ExitCode: 1, Output: "HTTP 429 Too Many Requests: rate limit exceeded"},
		{ExitCode: 0, Output: "No issues found.\nVERDICT: ACCEPT"},
	}
	smallDiff := DiffContext{Source: "working-tree", Status: "M a.go", DiffStat: "a.go", Diff: "-a\n+b\n", HasChanges: true}
	res := RunReview(ReviewReq{
		TargetDir: tmp, SlotDir: filepath.Join(tmp, "slot"), DiffCtx: smallDiff,
		AuthorLane: "devin-pro", QuotaPath: qPath, ResolveCLI: fakeCLIResolver(installed),
		Runner: func(r ReviewExecReq) ReviewExecRes {
			calls = append(calls, r)
			idx++
			return outputs[idx-1]
		},
	})
	if len(calls) != 2 || calls[0].Provider != "mcode" || calls[1].Provider != "grok" {
		t.Fatalf("unexpected fallback calls: %+v", calls)
	}
	if calls[0].ReviewMode != "native" || calls[1].ReviewMode != "inline" {
		t.Fatalf("unexpected review modes: %s, %s", calls[0].ReviewMode, calls[1].ReviewMode)
	}
	if !res.Ran || res.Lane != "grok" || res.Verdict != "ACCEPT" {
		t.Fatalf("unexpected review result: %+v", res)
	}
	after, _ := fleet.LoadQuota(qPath)
	if after.Providers["mcode"].Status != "cooldown" {
		t.Fatalf("expected mcode in cooldown, got %s", after.Providers["mcode"].Status)
	}
	// Multibyte UTF-8 diff (>8192 bytes, <8192 runes) must stay inline, and [P1] must halt fallback.
	mbDiff := DiffContext{Source: "working-tree", Status: "M u.go", Diff: "+" + strings.Repeat("é", 5000), HasChanges: true}
	calls = nil
	res2 := RunReview(ReviewReq{
		TargetDir: tmp, SlotDir: filepath.Join(tmp, "slot2"), DiffCtx: mbDiff,
		QuotaPath: qPath, ResolveCLI: fakeCLIResolver(installed), ReqLane: "mcode,grok",
		Runner: func(r ReviewExecReq) ReviewExecRes {
			calls = append(calls, r)
			return ReviewExecRes{ExitCode: 1, Output: "[P1] defect"}
		},
	})
	if len(calls) != 1 || calls[0].ReviewMode != "inline" || res2.Verdict != "REJECT" ||
		calls[0].TimeoutSec != fleet.ReviewTimeout {
		t.Fatalf("expected single inline REJECT with ReviewTimeout, calls=%d res=%+v", len(calls), res2)
	}
}

func TestCorruptQuotaReviewFails(t *testing.T) {
	tmp := t.TempDir()
	badQuota := filepath.Join(tmp, "corrupt.json")
	_ = os.WriteFile(badQuota, []byte("{not-json"), 0o600)
	res := RunReview(ReviewReq{
		TargetDir: tmp, SlotDir: tmp, QuotaPath: badQuota,
		DiffCtx: DiffContext{HasChanges: true, Source: "working-tree", Diff: "+x\n"},
		Runner:  func(ReviewExecReq) ReviewExecRes { return ReviewExecRes{ExitCode: 0, Output: "VERDICT: ACCEPT"} },
	})
	if res.ExitCode != 2 || res.Verdict != "SKIPPED" || res.Reason == "" {
		t.Fatalf("expected exit 2 SKIPPED on corrupt quota file, got %+v", res)
	}
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	goodQ := filepath.Join(tmp, "good.json")
	_ = fleet.AtomicWriteJSON(goodQ, fleet.DefaultQuotaData(), false, true)
	selfRes := RunReview(ReviewReq{
		TargetDir: tmp, SlotDir: tmp, DiffCtx: DiffContext{HasChanges: true, Source: "working-tree", Diff: "+x\n"},
		AuthorLane: "mcode", QuotaPath: goodQ, ResolveCLI: fakeCLIResolver(map[string]string{"mcode": "/bin/true"}),
		Runner: func(ReviewExecReq) ReviewExecRes { t.Fatal("self-family reviewer ran"); return ReviewExecRes{} },
	})
	if selfRes.Ran || selfRes.Verdict != "SKIPPED" {
		t.Fatalf("expected strict avoid-family SKIPPED, got %+v", selfRes)
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	base := HeadRef(repo)
	_ = os.WriteFile(filepath.Join(repo, "c.go"), []byte("package c\n"), 0o644)
	runGit(t, repo, "add", "c.go")
	runGit(t, repo, "commit", "-q", "-m", "committed work")
	cdc := CollectDiffContext(repo, false, base)
	if !cdc.HasChanges || len(cdc.ChangedFiles) != 1 || cdc.ChangedFiles[0] != "c.go" ||
		!strings.Contains(cdc.Diff, "+++ b/c.go") {
		t.Fatalf("committed work against baseRef missed: %+v", cdc)
	}
}

func TestVerifyPinOffFamilyScrub(t *testing.T) {
	tmp, qPath := t.TempDir(), filepath.Join(t.TempDir(), "quota.json")
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	_ = fleet.AtomicWriteJSON(qPath, fleet.DefaultQuotaData(), false, true)
	installed := map[string]string{"mcode": "/bin/true", "devin": "/bin/true"}
	var calls []ReviewExecReq
	diff := DiffContext{HasChanges: true, Source: "working-tree", Diff: "+x\n"}
	run := func(r ReviewExecReq) ReviewExecRes {
		calls = append(calls, r)
		return ReviewExecRes{ExitCode: 0, Output: "No issues found.\nVERDICT: ACCEPT"}
	}
	res := RunReview(ReviewReq{
		TargetDir: tmp, SlotDir: filepath.Join(tmp, "slot"), AuthorLane: "mcode", ReqLane: "mcode",
		Model: "minimax/MiniMax-M3.1-Flash", Effort: "high", QuotaPath: qPath,
		DiffCtx: diff, ResolveCLI: fakeCLIResolver(installed), Runner: run,
	})
	if !res.Ran || res.Lane != "devin-fast" || len(calls) != 1 ||
		calls[0].Model == "minimax/MiniMax-M3.1-Flash" || calls[0].Effort != "" {
		t.Fatalf("expected off-family fallback + scrub, got res=%+v calls=%+v", res, calls)
	}
	calls = nil
	res2 := RunReview(ReviewReq{
		TargetDir: tmp, SlotDir: filepath.Join(tmp, "slot2"), AuthorLane: "mcode", ReqLane: "devin",
		Model: "fusion", QuotaPath: qPath, DiffCtx: diff, ResolveCLI: fakeCLIResolver(installed), Runner: run,
	})
	if !res2.Ran || res2.Lane != "devin-pro" || len(calls) != 1 || calls[0].Model != "fusion" {
		t.Fatalf("expected alias devin to preserve fusion model, got res=%+v calls=%+v", res2, calls)
	}
	ins3 := map[string]string{"mcode": "/bin/true", "grok": "/bin/true", "devin": "/bin/true"}
	res3 := RunReview(ReviewReq{
		TargetDir: tmp, SlotDir: filepath.Join(tmp, "slot3"), AuthorLane: "mcode", ReqLane: "grok",
		AuthorLanes: []string{"grok", "mcode"}, QuotaPath: qPath, DiffCtx: diff,
		ResolveCLI: fakeCLIResolver(ins3), Runner: run,
	})
	if !res3.Ran || res3.Lane != "devin-fast" {
		t.Fatalf("expected earlier attempt family grok excluded from verify, got res=%+v", res3)
	}
	// Scope violations short-circuit with 0 LLM calls; rogue writes during review REJECT.
	repo2 := t.TempDir()
	runGit(t, repo2, "init", "-q")
	calls = nil
	sv := RunReview(ReviewReq{TargetDir: repo2, ScopeViols: []string{"bad.go"}, QuotaPath: qPath, Runner: run})
	if sv.Verdict != "REJECT" || sv.ExitCode != 1 || len(calls) != 0 {
		t.Fatalf("expected scope short-circuit 0 calls, got res=%+v calls=%d", sv, len(calls))
	}
	rogue := RunReview(ReviewReq{TargetDir: repo2, SlotDir: tmp, QuotaPath: qPath, DiffCtx: diff,
		ResolveCLI: fakeCLIResolver(installed), Runner: func(ReviewExecReq) ReviewExecRes {
			_ = os.WriteFile(filepath.Join(repo2, "rogue.txt"), []byte("x"), 0o644)
			return ReviewExecRes{ExitCode: 0, Output: "VERDICT: ACCEPT"}
		}})
	if rogue.Verdict != "REJECT" || !strings.Contains(rogue.Output, "rogue.txt") {
		t.Fatalf("expected rogue write REJECT, got %+v", rogue)
	}
}

func TestParseNativeVerdict(t *testing.T) {
	pass := `{"output":{"type":"review.result","verdict":"pass","summary":"No issues.","findings":[]}}`
	if v, _, ok := ParseNativeVerdict(pass); !ok || v != "ACCEPT" {
		t.Fatalf("pass envelope: v=%q ok=%v", v, ok)
	}
	fail := `{"output":{"type":"review.result","verdict":"needs_changes","summary":"Bad.",` +
		`"findings":[{"priority":"P1","path":"b.py","startLine":6,"title":"boom","content":"why"}]}}`
	v, rep, ok := ParseNativeVerdict(fail)
	if !ok || v != "REJECT" || !strings.Contains(rep, "[P1] b.py:6: boom") {
		t.Fatalf("needs_changes envelope: v=%q ok=%v rep=%q", v, ok, rep)
	}
	if _, _, ok := ParseNativeVerdict("VERDICT: ACCEPT"); ok {
		t.Fatal("plain text must not parse as a native envelope")
	}
	odd := `{"output":{"type":"review.result","verdict":"pass","summary":"s",` +
		`"findings":[{"priority":"P3","path":"x.py"}]}}`
	if _, _, ok := ParseNativeVerdict(odd); ok {
		t.Fatal("pass carrying findings must be refused")
	}
}
