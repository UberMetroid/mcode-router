package worktree

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

func TestRelPathAndManifest(t *testing.T) {
	for _, bad := range []string{"../escape.py", "src/../../x.go", "/etc/passwd", `C:\foo`, `\\etc\pw`} {
		if _, err := NormalizeRelPath(bad); err == nil {
			t.Fatalf("expected refusal for unsafe path %q", bad)
		}
	}
	if got, err := NormalizeRelPath("./a/b/../c.go"); err != nil || got != "a/c.go" {
		t.Fatalf("unexpected normalize result: %q (%v)", got, err)
	}
	dir := t.TempDir()
	overlap := filepath.Join(dir, "tasks.json")
	content := `[{"id":"a","prompt":"x","files":["src"]},{"id":"b","prompt":"y","files":["src/a.go"]}]`
	if err := os.WriteFile(overlap, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(overlap); err == nil {
		t.Fatal("expected overlap refusal between src and src/a.go")
	}
	valid := filepath.Join(dir, "ok.json")
	okBody := `{"tasks":[{"id":"t1","prompt":"hi","files":["cmd/a.go"],"timeout":"2m"}]}`
	if err := os.WriteFile(valid, []byte(okBody), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks, err := LoadManifest(valid)
	if err != nil || len(tasks) != 1 || tasks[0].ID != "t1" {
		t.Fatalf("expected valid manifest load, got %v (%v)", tasks, err)
	}
}

func TestSkillsAndFrontmatter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	skDir := filepath.Join(ws, ".agents", "skills", "demo-skill")
	if err := os.MkdirAll(skDir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: demo-skill\ndescription: >-\n  First line\n  second line.\n---\nBody text.\n"
	if err := os.WriteFile(filepath.Join(skDir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	skills := DiscoverSkills(ws)
	found := false
	for _, sk := range skills {
		if sk.Name == "demo-skill" && sk.Desc == "First line second line." && !sk.Core {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected discovered demo-skill with folded description, got %+v", skills)
	}
	block, err := LoadSkillBlocks([]string{"ooda:demo-skill"}, ws)
	if err != nil || !strings.Contains(block, "Body text.") || strings.Contains(block, "First line") {
		t.Fatalf("expected stripped frontmatter body in LoadSkillBlocks, err=%v got=%q", err, block)
	}
	bigDir := filepath.Join(ws, ".agents", "skills", "big-skill")
	_ = os.MkdirAll(bigDir, 0o755)
	_ = os.WriteFile(filepath.Join(bigDir, "SKILL.md"), []byte(strings.Repeat("x", 9000)), 0o644)
	if big, berr := LoadSkillBlocks([]string{"big-skill"}, ws); berr != nil || !strings.Contains(big, "...[truncated]...") {
		t.Fatalf("expected 8192-rune truncation on huge skill, err=%v len=%d", berr, len(big))
	}
	if _, err := LoadSkillBlocks([]string{"../bad"}, ws); err == nil {
		t.Fatal("expected invalid skill name error")
	}
}

func TestDirtyStateAndScope(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		res, err := fleet.RunCmd(fleet.CmdSpec{Argv: append([]string{"git"}, args...), Cwd: repo})
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("git %v failed: %v %s", args, err, res.Combined)
		}
	}
	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Tester")
	if err := os.WriteFile(filepath.Join(repo, "init.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "init.txt")
	runGit("commit", "-m", "init")
	pre := filepath.Join(repo, "pre space.txt")
	if err := os.WriteFile(pre, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := DirtyState(repo)
	if len(ChangedFilesIn(repo, base)) != 0 {
		t.Fatalf("expected zero changes vs matching baseline")
	}
	if err := os.WriteFile(pre, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := ChangedFilesIn(repo, base)
	if len(changed) != 1 || changed[0] != "pre space.txt" {
		t.Fatalf("expected mutated pre-dirty file detected, got %v", changed)
	}
	_ = os.Remove(pre)
	if del := ChangedFilesIn(repo, base); len(del) != 1 || del[0] != "pre space.txt" {
		t.Fatalf("expected deletion of pre-dirty file detected symmetrically, got %v", del)
	}
	if err := os.WriteFile(pre, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if viols := FindScopeViolations(changed, []string{"other.txt"}); len(viols) != 1 {
		t.Fatalf("expected scope violation, got %v", viols)
	}
	if _, _, err := EnsureWorktree(repo, "../bad"); err == nil {
		t.Fatal("expected EnsureWorktree to refuse unsafe task id")
	}
	wt, _, err := EnsureWorktree(repo, "wt-clean")
	if err != nil {
		t.Fatalf("EnsureWorktree failed: %v", err)
	}
	// Make stale commit on branch ooda/wt-clean, then verify fresh clean recreation resets to HEAD.
	_ = os.WriteFile(filepath.Join(wt, "old.txt"), []byte("stale\n"), 0o644)
	_, _ = fleet.RunCmd(fleet.CmdSpec{Argv: []string{"git", "add", "old.txt"}, Cwd: wt})
	_, _ = fleet.RunCmd(fleet.CmdSpec{Argv: []string{"git", "-c", "user.email=t@e.com", "-c", "user.name=T",
		"commit", "-m", "stale"}, Cwd: wt})
	wt2, _, err := EnsureWorktree(repo, "wt-clean")
	if err != nil {
		t.Fatalf("recreate clean worktree failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt2, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected -B HEAD to reset stale branch commit, stat=%v", err)
	}
	// Dirty existing worktree must fail closed.
	_ = os.WriteFile(filepath.Join(wt2, "wip.txt"), []byte("unsaved\n"), 0o644)
	if _, _, err := EnsureWorktree(repo, "wt-clean"); err == nil {
		t.Fatal("expected dirty worktree refusal")
	}
}

func TestInferComplexityAndSkills(t *testing.T) {
	if c := InferComplexity("Check layout", []string{"a.go"}, true); c != "recon" {
		t.Fatalf("expected recon for readOnly, got %q", c)
	}
	if c := InferComplexity("Fix typo in readme", []string{"README.md"}, false); c != "fast" {
		t.Fatalf("expected fast for doc typo, got %q", c)
	}
	if c := InferComplexity("Redesign security protocol", []string{"a.go"}, false); c != "deep" {
		t.Fatalf("expected deep for security protocol, got %q", c)
	}
	if c := InferComplexity("Edit files", []string{"a.go", "b.go", "c.go", "d.go"}, false); c != "deep" {
		t.Fatalf("expected deep for 4+ files, got %q", c)
	}
	if c := InferComplexity("Add helper", []string{"a.go"}, false); c != "act" {
		t.Fatalf("expected act default, got %q", c)
	}
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	for name, desc := range map[string]string{
		"sql-tuning": "Optimize database SQL indexes and query plans.",
		"devin":      "Run devin wrapper on database SQL indexes.",
	} {
		dir := filepath.Join(ws, ".agents", "skills", name)
		_ = os.MkdirAll(dir, 0o755)
		md := "---\nname: " + name + "\ndescription: " + desc + "\n---\nBody.\n"
		_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644)
	}
	got := AutoMatchSkills("Optimize database SQL indexes on orders table", []string{"db.sql"}, ws)
	if len(got) != 1 || got[0] != "sql-tuning" {
		t.Fatalf("expected [sql-tuning] without wrapper skill, got %v", got)
	}
	if none := AutoMatchSkills("Say hello world", []string{"foo.txt"}, ws); len(none) != 0 {
		t.Fatalf("expected no matched skills, got %v", none)
	}
	off := ResolveTaskSkills([]string{"none"}, "Optimize database SQL indexes", []string{"db.sql"}, ws)
	if len(off) != 0 {
		t.Fatalf("expected --skill none to opt out of auto-matching, got %v", off)
	}
}

func TestSafeFilesAndControlPaths(t *testing.T) {
	wt := t.TempDir()
	_ = os.WriteFile(filepath.Join(wt, "a.go"), []byte("package a\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(wt, "sub"), 0o755)
	got := SafeWorktreeFiles([]string{"a.go", "sub"}, wt)
	if len(got) != 1 || filepath.Base(got[0]) != "a.go" {
		t.Fatalf("expected SafeWorktreeFiles to return only regular file a.go, got %v", got)
	}
	for _, bad := range []string{"dir/bad\n+++ /dev/null\n+++ b/.env", "dir/bad\rname"} {
		if _, err := NormalizeRelPath(bad); err == nil {
			t.Fatalf("expected NormalizeRelPath to reject control char path %q", bad)
		}
	}
}

func TestWorktreeBindingAndCleanupIDs(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@e.com"},
		{"config", "user.name", "T"}} {
		_, _ = fleet.RunCmd(fleet.CmdSpec{Argv: append([]string{"git"}, args...), Cwd: repo})
	}
	_ = os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644)
	_, _ = fleet.RunCmd(fleet.CmdSpec{Argv: []string{"git", "add", "a.go"}, Cwd: repo})
	_, _ = fleet.RunCmd(fleet.CmdSpec{Argv: []string{"git", "commit", "-q", "-m", "seed"}, Cwd: repo})
	wt, _, err := EnsureWorktree(repo, "wt-bind")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyWorktreeBinding(repo, wt); err != nil {
		t.Fatalf("valid worktree binding rejected: %v", err)
	}
	_ = os.RemoveAll(filepath.Join(wt, ".git"))
	if err := VerifyWorktreeBinding(repo, wt); err == nil {
		t.Fatal("expected VerifyWorktreeBinding to fail when wt/.git missing")
	}
	_, _ = fleet.RunCmd(fleet.CmdSpec{Argv: []string{"git", "branch", "ooda/keep/nested"}, Cwd: repo})
	_ = os.WriteFile(filepath.Join(repo, ".worktrees", "results.json"),
		[]byte(`[{"id":"keep/nested"},{"id":"wt-bind"}]`), 0o644)
	var out bytes.Buffer
	Cleanup(repo, &out)
	if chk, _ := fleet.RunCmd(fleet.CmdSpec{
		Argv: []string{"git", "rev-parse", "--verify", "refs/heads/ooda/keep/nested"}, Cwd: repo,
	}); chk.ExitCode != 0 {
		t.Fatalf("Cleanup deleted non-SafeID branch ooda/keep/nested from results.json")
	}
}
