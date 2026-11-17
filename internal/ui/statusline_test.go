// Tests for the MiniMax Code status line rendering.
package ui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UberMetroid/mcode-router/internal/dispatch"
)

// writeWorker stages a live worker marker in an isolated scratch directory.
func writeWorker(t *testing.T, dir string, m dispatch.WorkerMarker) {
	t.Helper()
	active := filepath.Join(dir, "active")
	if err := os.MkdirAll(active, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(active, "1-task.json"), raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestStatusLineShowsActiveWorker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCODE_ROUTER_SCRATCH_DIR", dir)
	writeWorker(t, dir, dispatch.WorkerMarker{
		TaskID: "task", Lane: "muse", CLI: "muse", Model: "muse-ember-1.0",
		Effort: "max", Phase: "stream", TokS: 181,
		StartedAt: float64(time.Now().UnixNano()) / 1e9, Cwd: "/tmp/mcode-act-demo",
	})
	payload, _ := json.Marshal(StatusContext{
		Protocol: 1, Event: "interval", WorkspaceDir: "/tmp/mcode-live-session",
	})
	var out bytes.Buffer
	if code := StatusLine(bytes.NewReader(payload), &out); code != 0 {
		t.Fatalf("status line exited %d", code)
	}
	text := stripANSI(out.String())
	for _, want := range []string{"tok/s", "muse-ember-1.0"} {
		if !strings.Contains(text, want) {
			t.Fatalf("status line missing %q:\n%s", want, text)
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "\u26a1 ~181 tok/s") {
		t.Fatalf("row must start with the worker token rate:\n%s", text)
	}
	if strings.Index(text, "muse-ember-1.0") < strings.Index(text, "\u26a1 ~181 tok/s") {
		t.Fatalf("model must follow the token rate:\n%s", text)
	}
}

// TestRateColorBrightens checks that a faster worker gets a brighter colour,
// that every ramp step is distinct, and that an idle worker stays muted.
func TestRateColorBrightens(t *testing.T) {
	pal := Palette{Muted: "\033[38;5;244m"}
	if got := rateColor(pal, 0); got != pal.Muted {
		t.Fatalf("idle worker should stay muted, got %q", got)
	}
	prev := ""
	for _, r := range []float64{1, 20, 50, 100, 200, 400, 600, 900} {
		c := rateColor(pal, r)
		if c == prev {
			t.Fatalf("rate %v reuses the previous colour %q", r, c)
		}
		prev = c
	}
	if rateColor(pal, 900) != rateRamp[len(rateRamp)-1] {
		t.Fatalf("fastest worker should use the brightest ramp colour")
	}
}

// stripANSI removes colour escapes so assertions can match plain text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\033' {
			b.WriteByte(s[i])
			continue
		}
		for i < len(s) && s[i] != 'm' {
			i++
		}
	}
	return b.String()
}

func TestStatusLineSilentWhenIdle(t *testing.T) {
	t.Setenv("MCODE_ROUTER_SCRATCH_DIR", t.TempDir())
	var out bytes.Buffer
	if code := StatusLine(strings.NewReader(`{"protocol":1,"event":"interval"}`), &out); code != 0 {
		t.Fatalf("status line exited %d", code)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no output when idle, got %q", out.String())
	}
}

// TestFormatWorkerLineIsMinimal checks that the row carries only the rate and
// the model, with no lane, project, phase or elapsed noise.
func TestFormatWorkerLineIsMinimal(t *testing.T) {
	m := dispatch.WorkerMarker{
		TaskID: "task", Lane: "devin-fast", CLI: "devin", Model: "swe-2-flash-max",
		Effort: "xhigh", Phase: "stream", TokS: 120, Cwd: "/tmp/demo",
	}
	line := stripANSI(FormatWorkerLine(Palette{}, m, time.Now()))
	if line != "\u26a1 ~120 tok/s · swe-2-flash-max" {
		t.Fatalf("row should be the rate and model only, got %q", line)
	}
	for _, noise := range []string{"devin-fast", "demo", "stream", "xhigh"} {
		if strings.Contains(line, noise) {
			t.Fatalf("row should not mention %q: %q", noise, line)
		}
	}
}

func TestReadStatusContextParses(t *testing.T) {
	payload := `{"protocol":1,"event":"interval","model":"M3.1-Flash","workspace_dir":"/tmp/x"}`
	ctx := ReadStatusContext(strings.NewReader(payload))
	if ctx.Model != "M3.1-Flash" || ctx.WorkspaceDir != "/tmp/x" {
		t.Fatalf("context not parsed: %+v", ctx)
	}
}
