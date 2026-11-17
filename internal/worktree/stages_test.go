package worktree

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/UberMetroid/mcode-router/internal/fleet"
)

func TestStagesPrecedenceAndCleanup(t *testing.T) {
	tmp := t.TempDir()
	qPath := filepath.Join(tmp, "quota.json")
	t.Setenv("AGENT_QUOTA_FILE", qPath)
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("MCODE_ROUTER_PROFILE", "")
	t.Setenv("OODA_NO_AUTO_PROBE", "1")

	repo := filepath.Join(tmp, "repo")
	_ = os.MkdirAll(repo, 0o755)
	res, err := fleet.RunCmd(fleet.CmdSpec{Argv: []string{"git", "init", "-q"}, Cwd: repo})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("git init failed: %v %s", err, res.Combined)
	}
	if _, err := ConfigureStage(StagePatch{Stage: "act", Lane: "mcode", Cwd: repo}); err != nil {
		t.Fatalf("global ConfigureStage failed: %v", err)
	}
	q, err := fleet.LoadQuota(qPath)
	if err != nil {
		t.Fatal(err)
	}
	if sp := ResolveStage("act", "", "", "", "", repo, q); sp.Source != "global" || sp.Primary != "mcode" {
		t.Fatalf("expected global mcode, got %+v", sp)
	}
	if sp := ResolveStage("act", "", "", "", "auto", repo, q); sp.Source != "global" || sp.Primary != "mcode" {
		t.Fatalf("expected profile=auto to honor global pin, got %+v", sp)
	}
	if pins := ActivePinsSummary(repo, "auto", q); !strings.Contains(pins, "act:mcode") {
		t.Fatalf("expected act:mcode in ActivePinsSummary with auto profile, got %q", pins)
	}
	if sp := ResolveStage("act", "", "", "", "devin", repo, q); sp.Source != "profile:devin" || sp.Primary != "devin-pro" {
		t.Fatalf("expected profile:devin override, got %+v", sp)
	}
	if _, err := ConfigureStage(StagePatch{
		Stage: "act", Lane: "grok", Model: "grok-4.7-build-fast", Effort: "xhigh", Local: true, Cwd: repo,
	}); err != nil {
		t.Fatalf("local ConfigureStage failed: %v", err)
	}
	if sp := ResolveStage("act", "", "", "", "devin", repo, q); sp.Source != "local" ||
		sp.Primary != "grok" || sp.Model != "grok-4.7-build-fast" || sp.Effort != "xhigh" {
		t.Fatalf("expected local grok override, got %+v", sp)
	}
	if sp := ResolveStage("act", "mistral", "", "", "devin", repo, q); sp.Source != "cli" || sp.Primary != "mistral" {
		t.Fatalf("expected CLI override, got %+v", sp)
	}
	if sp := ResolveStage("act", "auto", "", "", "devin", repo, q); sp.Source != "cli" || sp.Lane != "auto" {
		t.Fatalf("expected CLI auto override, got %+v", sp)
	}
	if pins := ActivePinsSummary(repo, "", q); !strings.Contains(pins, "act:grok") {
		t.Fatalf("expected act:grok in ActivePinsSummary, got %q", pins)
	}

	wt := filepath.Join(repo, ".worktrees", "dummy")
	_ = os.MkdirAll(wt, 0o755)
	_ = os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: ..\n"), 0o644)
	if WorkspaceStagesPath(wt) != WorkspaceStagesPath(repo) {
		t.Fatalf("linked worktree path mismatch: %s vs %s", WorkspaceStagesPath(wt), WorkspaceStagesPath(repo))
	}
	var cleanOut bytes.Buffer
	Cleanup(repo, &cleanOut)
	if _, serr := os.Stat(WorkspaceStagesPath(repo)); serr != nil {
		t.Fatalf("Cleanup must preserve workspace pins outside .worktrees: %v", serr)
	}
}

func TestLocalAutoMaskAndReset(t *testing.T) {
	tmp := t.TempDir()
	qPath := filepath.Join(tmp, "quota.json")
	t.Setenv("AGENT_QUOTA_FILE", qPath)
	t.Setenv("MCODE_ROUTER_PROFILE", "")
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	if _, err := ConfigureStage(StagePatch{Stage: "act", Lane: "mcode", Cwd: tmp}); err != nil {
		t.Fatal(err)
	}
	q, _ := fleet.LoadQuota(qPath)
	if _, err := ConfigureStage(StagePatch{Stage: "act", Lane: "auto", Local: true, Cwd: tmp}); err != nil {
		t.Fatalf("local auto mask failed: %v", err)
	}
	if sp := ResolveStage("act", "", "", "", "devin", tmp, q); sp.Source != "local" || sp.Lane != "auto" {
		t.Fatalf("expected local auto to mask profile/global, got %+v", sp)
	}
	if pins := ActivePinsSummary(tmp, "", q); strings.Contains(pins, "act:") {
		t.Fatalf("expected local auto to suppress HUD pin, got %q", pins)
	}
	if _, err := ConfigureStage(StagePatch{Stage: "all", Lane: "auto", Local: true, Cwd: tmp}); err != nil {
		t.Fatalf("local reset-all failed: %v", err)
	}
	if sp := ResolveStage("act", "", "", "", "", tmp, q); sp.Source != "global" || sp.Primary != "mcode" {
		t.Fatalf("expected reset-all to restore global mcode, got %+v", sp)
	}
}

func TestStagesExpiryPresetAndRefusal(t *testing.T) {
	tmp := t.TempDir()
	qPath := filepath.Join(tmp, "quota.json")
	t.Setenv("AGENT_QUOTA_FILE", qPath)
	t.Setenv("MCODE_ROUTER_PROFILE", "")
	t.Setenv("OODA_NO_AUTO_PROBE", "1")

	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if err := SaveWorkspaceStages(tmp, map[string]fleet.StageRoute{
		"orient": {Lane: "grok", ExpiresAt: &past},
	}); err != nil {
		t.Fatal(err)
	}
	if ws := LoadWorkspaceStages(tmp); ws["orient"].Lane != "" {
		t.Fatalf("expected expired workspace pin auto-pruned, got %+v", ws)
	}
	if _, err := fleet.LoadQuota(qPath); err != nil {
		t.Fatal(err)
	}
	wake := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	_, _, _ = fleet.SetProviderStatus("grok", "cooldown", "cap", fleet.Ptr(2.0), true, qPath)
	stMap, err := ConfigureStage(StagePatch{Stage: "orient", Lane: "grok", UntilReset: true, Cwd: tmp})
	if err != nil || stMap["orient"].ExpiresAt == nil || *stMap["orient"].ExpiresAt == "" {
		t.Fatalf("expected UntilReset to set ExpiresAt, err=%v got=%+v", err, stMap["orient"])
	}
	_ = wake
	for _, k := range []string{"auto", "conserve", "deep", "devin", "grok", "mcode", "muse", "mistral"} {
		if pre, ok := PresetStages("ooda-" + k); !ok || len(pre) != 4 {
			t.Fatalf("missing preset %s", k)
		}
	}
	if _, err := ConfigureStage(StagePatch{Stage: "preset", Lane: "conserve", Local: true, Cwd: tmp}); err != nil {
		t.Fatalf("local preset failed: %v", err)
	}
	if _, err := ConfigureStage(StagePatch{Stage: "all", Lane: "auto", Local: true, Cwd: tmp}); err != nil {
		t.Fatalf("local reset failed: %v", err)
	}
	if _, err := os.Stat(WorkspaceStagesPath(tmp)); !os.IsNotExist(err) {
		t.Fatalf("expected workspace file deleted after reset-all, stat=%v", err)
	}
}

func TestStageRefusalsAndPAYG(t *testing.T) {
	tmp, hudDir := t.TempDir(), t.TempDir()
	qPath := filepath.Join(tmp, "quota.json")
	t.Setenv("AGENT_QUOTA_FILE", qPath)
	t.Setenv("ROUTER_SCRATCH_DIR", hudDir)
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	mainRepo, wtExt := filepath.Join(tmp, "main-repo"), filepath.Join(tmp, "wt-ext")
	_ = os.MkdirAll(filepath.Join(mainRepo, ".git", "worktrees", "wt-ext"), 0o755)
	_ = os.MkdirAll(wtExt, 0o755)
	_ = os.WriteFile(filepath.Join(wtExt, ".git"), []byte("gitdir: ../main-repo/.git/worktrees/wt-ext\n"), 0o644)
	if got, want := WorkspaceStagesPath(wtExt), WorkspaceStagesPath(mainRepo); got != want {
		t.Fatalf("external .git-file worktree stage path mismatch: %q != %q", got, want)
	}
	_ = fleet.AtomicWriteJSON(filepath.Join(hudDir, "muse_usage.json"),
		fleet.MuseProbe{Installed: true, PayAsYouGo: true, CheckedAt: float64(time.Now().Unix())}, true, true)
	if _, err := ConfigureStage(StagePatch{Stage: "preset", Lane: "muse", Cwd: tmp}); err == nil {
		t.Fatalf("expected preset=muse rejected when muse PAYG active")
	}
	_ = os.Remove(filepath.Join(hudDir, "muse_usage.json"))

	bads := []StagePatch{
		{Stage: "bogus", Lane: "mcode", Cwd: tmp},
		{Stage: "act", Lane: "bogus-lane", Cwd: tmp},
		{Stage: "act", Lane: "muse", Model: "gemini-4-carbon-high", Cwd: tmp},
		{Stage: "act", Lane: "auto", Model: "grok-4.7", Cwd: tmp},
		{Stage: "preset", Lane: "deep", Effort: "high", Cwd: tmp},
		{Stage: "verify", Lane: "devin-fusion", Cwd: tmp},
		{Stage: "act", Lane: "devin-fusion", Cwd: tmp},
	}
	for _, bad := range bads {
		if _, err := ConfigureStage(bad); err == nil {
			t.Fatalf("expected refusal for invalid StagePatch %+v", bad)
		} else {
			var ee *fleet.ExitError
			if !errors.As(err, &ee) || ee.Code != 2 {
				t.Fatalf("expected ExitError(2) for %+v, got %v", bad, err)
			}
		}
	}
}

func TestManifestBoundsAndSkillEscape(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("MCODE_ROUTER_PLUGIN_ROOT", filepath.Join(tmp, "plug"))
	_ = os.MkdirAll(filepath.Join(tmp, "home"), 0o755)

	m1 := filepath.Join(tmp, "many.json")
	var sb strings.Builder
	sb.WriteString(`{"tasks":[{"id":"t0","prompt":"p"}`)
	for i := 1; i < 65; i++ {
		sb.WriteString(`,{"id":"t` + strconv.Itoa(i) + `","prompt":"p"}`)
	}
	sb.WriteString(`]}`)
	if err := os.WriteFile(m1, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(m1); err == nil {
		t.Fatal("expected manifest task-count refusal")
	}

	m2 := filepath.Join(tmp, "big.json")
	big := `{"tasks":[{"id":"big","prompt":"` + strings.Repeat("x", (1<<20)+4096) + `"}]}`
	if err := os.WriteFile(m2, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(m2); err == nil {
		t.Fatal("expected manifest byte-size refusal")
	}

	outside := filepath.Join(tmp, "outside")
	skillMD := "---\nname: esc\ndescription: Escaped skill\n---\nbody\n"
	_ = os.MkdirAll(filepath.Join(outside, "dir", "esc"), 0o755)
	_ = os.WriteFile(filepath.Join(outside, "outside.md"), []byte(skillMD), 0o644)
	_ = os.WriteFile(filepath.Join(outside, "dir", "esc", "SKILL.md"), []byte(skillMD), 0o644)

	ws := filepath.Join(tmp, "ws")
	escDir := filepath.Join(ws, ".agents", "skills", "esc")
	_ = os.MkdirAll(escDir, 0o755)
	if err := os.Symlink(filepath.Join(outside, "outside.md"),
		filepath.Join(escDir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	ws2 := filepath.Join(tmp, "ws2")
	_ = os.MkdirAll(filepath.Join(ws2, ".agents"), 0o755)
	if err := os.Symlink(filepath.Join(outside, "dir"),
		filepath.Join(ws2, ".agents", "skills")); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadSkillBlocks([]string{"esc"}, ws); err == nil {
		t.Fatal("expected escaped SKILL.md refusal")
	}
	for _, w := range []string{ws, ws2} {
		for _, m := range DiscoverSkills(w) {
			if m.Name == "esc" {
				t.Fatalf("DiscoverSkills(%s) leaked escaped skill", w)
			}
		}
	}
}
