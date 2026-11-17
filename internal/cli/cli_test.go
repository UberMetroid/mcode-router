package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UberMetroid/mcode-router/internal/fleet"
	"github.com/UberMetroid/mcode-router/internal/theme"
)

func writeExecFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func setupCLIEnv(t *testing.T) (string, string, string, func(...string) (int, string, string)) {
	t.Helper()
	tmp := t.TempDir()
	home, repo, binDir := filepath.Join(tmp, "home"), filepath.Join(tmp, "repo"), filepath.Join(tmp, "bin")
	for _, d := range []string{home, repo, binDir} {
		_ = os.MkdirAll(d, 0o755)
	}
	runGit := func(args ...string) {
		full := append([]string{"-c", "user.name=t", "-c", "user.email=t@invalid", "-c", "commit.gpgsign=false"}, args...)
		cmd := exec.Command("git", full...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	_ = os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n"), 0o644)
	runGit("add", "a.go")
	runGit("commit", "-q", "-m", "seed")
	qPath := filepath.Join(tmp, "quota.json")
	if err := fleet.AtomicWriteJSON(qPath, fleet.DefaultQuotaData(), false, true); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"HOME": home, "AGENT_QUOTA_FILE": qPath, "OODA_NO_AUTO_PROBE": "1", "NO_COLOR": "1",
		"OODA_DISCOVERY_FILE": filepath.Join(tmp, "discovery.json"), "ROUTER_SCRATCH_DIR": filepath.Join(tmp, "hud"),
		"ROUTER_SLOTS_DIR": filepath.Join(tmp, "slots"), "MCODE_ROUTER_DEPTH": "0", "ROUTER_DEPTH": "0",
	} {
		t.Setenv(k, v)
	}
	gitBin, _ := exec.LookPath("git")
	t.Setenv("PATH", binDir+":"+filepath.Dir(gitBin)+":/bin:/usr/bin")
	oldWd, _ := os.Getwd()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	return repo, qPath, binDir, func(argv ...string) (int, string, string) {
		var out, errB bytes.Buffer
		return RunCLI("mcode-router", argv, &out, &errB), out.String(), errB.String()
	}
}

func TestParseArgsPermutations(t *testing.T) {
	var helpOut bytes.Buffer
	if opts, err := ParseArgs("mcode-router", []string{"-h"}, &helpOut); err != nil || !opts.Help ||
		!strings.Contains(helpOut.String(), "usage: mcode-router") {
		t.Fatalf("unexpected help parse: opts=%+v err=%v", opts, err)
	}
	for _, tc := range []struct {
		argv  []string
		check func(CLIOpts) bool
	}{
		{[]string{"--version"}, func(o CLIOpts) bool { return o.Version }},
		{[]string{"--review"}, func(o CLIOpts) bool { return o.Review != nil && *o.Review == "." }},
		{[]string{"--hygiene"}, func(o CLIOpts) bool { return o.Hygiene != nil && *o.Hygiene == "." }},
		{[]string{"--", "--weird"}, func(o CLIOpts) bool { return o.Manifest == "--weird" }},
		{[]string{"--probe"}, func(o CLIOpts) bool { return o.Probe }},
		{[]string{"--orient", "why"}, func(o CLIOpts) bool { return o.Orient != nil && *o.Orient == "why" }},
		{[]string{"--stripe"}, func(o CLIOpts) bool { return o.Stripe }},
	} {
		if o, err := ParseArgs("ooda", tc.argv, nil); err != nil || !tc.check(o) {
			t.Fatalf("%v parse failed: opts=%+v err=%v", tc.argv, o, err)
		}
	}
	o2, err := ParseArgs("ooda", []string{"--review", "sub", "-j6", "--files", "a.go,b.go", "--files", "c.go"}, nil)
	if err != nil || o2.Review == nil || *o2.Review != "sub" || o2.Jobs != 6 || len(o2.Files) != 2 {
		t.Fatalf("unexpected flag mix parse: %+v err=%v", o2, err)
	}
	for _, bad := range [][]string{{"--bogus-flag"}, {"--probe", "--status"}, {"--hygiene", "--status"},
		{"--version", "--status"}, {"--profile", "bogus"}} {
		if _, err := ParseArgs("ooda", bad, nil); err == nil {
			t.Fatalf("expected parse error for %v", bad)
		}
	}
}

func TestStatusUsageAndQuotaAdmin(t *testing.T) {
	_, _, _, run := setupCLIEnv(t)
	for _, tc := range [][]string{
		{"mcode-router v" + fleet.Version, "--version"}, {"LANE", "--status"},
		{"(refreshed)", "--discover"}, {"Cycle Usage Telemetry", "--usage"}, {"hygiene: clean", "--hygiene"},
		{"", "--disable", "grok", "--reason", "maint", "--hours", "1"}, {"", "--enable", "grok"},
		{"configured mcode:", "--set-cycle", "mcode", "--effort", "high", "--token-budget", "5000"},
		{"devin-pro", "--set-stage", "preset=conserve"},
	} {
		if rc, out, errS := run(tc[1:]...); rc != 0 || !strings.Contains(out, tc[0]) {
			t.Fatalf("%v failed rc=%d out=%q err=%q", tc[1:], rc, out, errS)
		}
	}
	for _, bad := range [][]string{
		{"--set-cycle", "muse", "--model", "gemini-4-carbon-low"},
		{"--recon", "hi", "--model", "gemini-4-carbon-high"},
	} {
		if rc, _, errS := run(bad...); rc != 2 || strings.Contains(errS, "pivoting") {
			t.Fatalf("%v want rc=2 err=%q", bad, errS)
		}
	}
	t.Setenv("MCODE_ROUTER_THEME", "light")
	var out, errB bytes.Buffer
	if rc := RunSelectTUI(CLIOpts{}, strings.NewReader("b\rBjjjjj\rq"), &out, &errB); rc != 0 ||
		!strings.Contains(out.String(), theme.PaletteFor("light").TUIHeader) ||
		strings.Contains(out.String(), "REPO-LOCAL") || !strings.Contains(out.String(), "✓ Reset all stages to auto") {
		t.Fatalf("RunSelectTUI b/reset failed rc=%d err=%q", rc, errB.String())
	}
	out.Reset()
	if rc := RunSelectTUI(CLIOpts{}, strings.NewReader("\tq"), &out, &errB); rc != 0 ||
		!strings.Contains(out.String(), "REPO-LOCAL") {
		t.Fatal(rc, out.String())
	}
	if q, e := fleet.LoadQuota(""); e != nil || q.Stages["act"].Lane != "auto" {
		t.Fatal(q, e)
	}
}

func TestRouterDepthGuard(t *testing.T) {
	_, _, _, run := setupCLIEnv(t)
	if rc, _, errS := run("--status", "--enable", "grok"); rc != 2 || !strings.Contains(errS, "conflicting") {
		t.Fatalf("mixed modes at ROUTER_DEPTH=0: rc=%d err=%q", rc, errS)
	}
	t.Setenv("MCODE_ROUTER_DEPTH", "0")
	t.Setenv("ROUTER_DEPTH", "1")
	for _, argv := range [][]string{{"--recon", "hi"}, {"--task", "hi"}, {"--review", "."}, {"--probe"},
		{"--enable", "grok"}, {"--disable", "grok"}, {"--set-cycle", "grok"}, {"--cleanup"}} {
		if rc, _, errS := run(argv...); rc != 2 || !strings.Contains(errS, "ROUTER_DEPTH>=1") {
			t.Fatalf("%v under ROUTER_DEPTH=1: rc=%d err=%q", argv, rc, errS)
		}
	}
	t.Setenv("MCODE_ROUTER_DEPTH", "1")
	t.Setenv("ROUTER_DEPTH", "0")
	if rc, _, errS := run("--recon", "hi"); rc != 2 || !strings.Contains(errS, "MCODE_ROUTER_DEPTH>=1") {
		t.Fatalf("--recon under MCODE_ROUTER_DEPTH=1: rc=%d err=%q", rc, errS)
	}
	for _, argv := range [][]string{{"--status"}, {"--hygiene"}} {
		if rc, _, errS := run(argv...); rc != 0 {
			t.Fatalf("%v should work when MCODE_ROUTER_DEPTH=1, got %d (%q)", argv, rc, errS)
		}
	}
}

func TestSingleTaskScopeAndReject(t *testing.T) {
	repo, _, binDir, run := setupCLIEnv(t)
	_ = os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("keep me\n"), 0o644)
	writeExecFile(t, binDir, "grok", "echo 'package main' > escape.txt\necho '{\"content\":\"done\"}'\n")
	if rc, out, errS := run("--task", "edit", "--lane", "grok", "--files", "a.go", "--no-review"); rc != 1 ||
		!strings.Contains(out, "SCOPE VIOLATIONS: escape.txt") || strings.Contains(out, "dirty.txt") {
		t.Fatalf("unexpected scope violation result rc=%d out=%q err=%q", rc, out, errS)
	}
	_ = os.Remove(filepath.Join(repo, "escape.txt"))
	writeExecFile(t, binDir, "grok", "echo 'package a\nvar X = 1' > a.go\necho '{\"content\":\"edited a.go\"}'\n")
	for i, tc := range [][2]string{
		{"printf '[P1] bad var\\nVERDICT: REJECT\\n'\n", "[review verdict: REJECT]"},
		{"printf 'Looks plausible without verdict line.\\n'\n", "[review verdict: UNKNOWN]"},
	} {
		_ = os.WriteFile(filepath.Join(repo, "a.go"), []byte(string(rune('a'+i))+"\n"), 0o644)
		writeExecFile(t, binDir, "mcode", tc[0])
		if rc, out, errS := run("--task", "edit", "--lane", "grok", "--files", "a.go"); rc != 1 ||
			!strings.Contains(out, tc[1]) {
			t.Fatalf("expected %s exit 1, rc=%d out=%q err=%q", tc[1], rc, out, errS)
		}
	}
	_ = os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\nvar X = 2\n"), 0o644)
	writeExecFile(t, binDir, "mcode", "printf 'VERDICT: ACCEPT\\n'\n")
	if rc, out, errS := run("--review", repo, "--files", "a.go", "--lane", "mcode"); rc != 0 ||
		!strings.Contains(out, "ACCEPT") {
		t.Fatalf("standalone --review --files failed: rc=%d out=%q err=%q", rc, out, errS)
	}
	revSlotPath := filepath.Join(os.Getenv("ROUTER_SLOTS_DIR"), "slot-task-review")
	_ = os.RemoveAll(revSlotPath)
	_ = os.WriteFile(revSlotPath, []byte("block"), 0o600)
	if rc, out, errS := run("--task", "edit a2", "--lane", "grok", "--files", "a.go"); rc != 1 ||
		!strings.Contains(out, "failing") {
		t.Fatalf("expected EnsureSlot fail-closed exit 1, got rc=%d out=%q err=%q", rc, out, errS)
	}
}

func TestSkippedReviewFailsClosed(t *testing.T) {
	_, _, binDir, run := setupCLIEnv(t)
	writeExecFile(t, binDir, "grok", "echo 'package a\nvar Y = 1' > a.go\necho '{\"content\":\"edited\"}'\n")
	var out, errB bytes.Buffer
	if rc := RunCLI("router", []string{"--task", "edit", "--lane", "grok", "--files", "a.go"}, &out, &errB); rc != 1 ||
		!strings.Contains(out.String(), "[review verdict: SKIPPED]") {
		t.Fatalf("expected SKIPPED review fail-closed exit 1, got rc=%d out=%q err=%q", rc, out.String(), errB.String())
	}
	for _, bad := range [][]string{
		{"--disable", "grok", "--hours", "NaN"}, {"--disable", "grok", "--hours", "+Inf"},
		{"--disable", "grok", "--hours", "0"}, {"--set-cycle", "grok", "--cost-budget", "NaN"},
		{"--set-cycle", "devin-fast", "--max-parallel", "100000"},
	} {
		if rc, _, errS := run(bad...); rc != 2 {
			t.Fatalf("expected exit 2 for %v, got rc=%d err=%q", bad, rc, errS)
		}
	}
}

func TestOrientCommand(t *testing.T) {
	_, _, binDir, run := setupCLIEnv(t)
	writeExecFile(t, binDir, "grok", "printf '{\"role\":\"assistant\",\"content\":\"architecture root cause\"}\\n'\n")
	if rc, out, errS := run("--orient", "trace root cause", "--lane", "grok"); rc != 0 ||
		!strings.Contains(errS, "OODA:ORIENT") || !strings.Contains(out, "root cause") {
		t.Fatalf("--orient failed rc=%d out=%q err=%q", rc, out, errS)
	}
	for _, tc := range [][]string{
		{"profile:conserve", "--status", "--profile", "conserve"}, {"verify    auto", "--status", "--lane", "grok"},
		{"orient   -> grok", "--set-stage", "orient=grok", "--local"}, {"orient    local          grok", "--status"},
	} {
		if rc, out, errS := run(tc[1:]...); rc != 0 || !strings.Contains(out, tc[0]) {
			t.Fatalf("%v rc=%d out=%q err=%q", tc[1:], rc, out, errS)
		}
	}
	writeExecFile(t, binDir, "grok", "echo bad > rogue_recon.txt\necho '{\"content\":\"sneaky\"}'\n")
	if rc, _, errS := run("--recon", "check", "--lane", "grok"); rc != 1 || !strings.Contains(errS, "zero-trust") {
		t.Fatalf("expected rogue write during --recon to fail closed, rc=%d err=%q", rc, errS)
	}
	writeExecFile(t, binDir, "grok", "exit 1\n")
	writeExecFile(t, binDir, "mcode", "printf 'softpin pivoted %s\\n' \"$*\"\n")
	for _, tc := range [][]string{
		{"softpin pivoted", "--orient", "why", "--files", "a.go"},
		{"b.go", "--orient", "why", "--files", "b.go"}, {"(inherit)", "--reset-stages", "--local"},
		{"hygiene: clean", "--hygiene", ".", "--files", "a.go"},
	} {
		if rc, out, errS := run(tc[1:]...); rc != 0 || !strings.Contains(out, tc[0]) {
			t.Fatalf("%v rc=%d out=%q err=%q", tc[1:], rc, out, errS)
		}
	}
	if rc, _, errS := run("--hygiene", filepath.Join(t.TempDir(), "missing")); rc != 2 || !strings.Contains(errS, "directory") {
		t.Fatalf("expected --hygiene missing path exit 2, got rc=%d err=%q", rc, errS)
	}
	_ = os.WriteFile("bad.go", []byte("package a\nvar m = 0o777\n"), 0o644)
	if rc, out, _ := run("--hygiene"); rc != 1 || !strings.Contains(out, "world-writable-mode") {
		t.Fatalf("--hygiene should return exit 1 on P1 issue, rc=%d out=%q", rc, out)
	}
}
