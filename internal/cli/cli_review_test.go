package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	// The mcode lane carries its prompt on stdin, so the fake echoes both argv
	// and the prompt it reads.
	writeExecFile(t, binDir, "mcode", "printf 'softpin pivoted %s\\n' \"$*\"\ncat\n")
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
