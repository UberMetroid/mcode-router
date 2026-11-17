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
	for _, want := range []string{
		"tok/s", "muse/muse-ember-1.0/max", "stream", "mcode-act-demo",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("status line missing %q:\n%s", want, text)
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "\u26a1 ~181 tok/s") {
		t.Fatalf("row must start with the worker token rate:\n%s", text)
	}
	if strings.Index(text, "muse/muse-ember-1.0/max") < strings.Index(text, "\u26a1 ~181 tok/s") {
		t.Fatalf("model must follow the token rate:\n%s", text)
	}
}

// TestRateColorBrightens checks that a faster worker gets a brighter colour
// and that an idle worker stays in the theme's muted tone.
func TestRateColorBrightens(t *testing.T) {
	pal := Palette{Muted: "\033[38;5;244m"}
	if got := rateColor(pal, 0); got != pal.Muted {
		t.Fatalf("idle worker should stay muted, got %q", got)
	}
	low, high := rateColor(pal, 10), rateColor(pal, 200)
	if low == high {
		t.Fatalf("rate colour does not brighten with throughput: %q", low)
	}
	if rateColor(pal, 200) != rateRamp[3] {
		t.Fatalf("fast worker should use the brightest ramp colour")
	}
	if strings.Contains(FormatWorkerLine(pal, dispatch.WorkerMarker{Lane: "muse", Model: "m",
		Phase: "stream", TokS: 5, StartedAt: float64(time.Now().UnixNano()) / 1e9}, time.Now(),
		StatusContext{}), "-0s") {
		t.Fatalf("elapsed must never render as negative zero")
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

// TestFormatWorkerLineProjectLabel checks that the project segment is dropped
// when the worker already runs inside the session workspace.
func TestFormatWorkerLineProjectLabel(t *testing.T) {
	m := dispatch.WorkerMarker{
		TaskID: "task", Lane: "muse", CLI: "muse", Model: "muse-ember-1.0",
		Effort: "max", Phase: "stream", Cwd: "/tmp/demo",
	}
	now := time.Now()
	same := stripANSI(FormatWorkerLine(Palette{}, m, now,
		StatusContext{WorkspaceDir: "/tmp/demo"}))
	if strings.Contains(same, "demo") {
		t.Fatalf("expected project label to be dropped, got %q", same)
	}
	if !strings.Contains(same, "muse/muse-ember-1.0/max") {
		t.Fatalf("worker lane/model missing: %q", same)
	}
	other := stripANSI(FormatWorkerLine(Palette{}, m, now,
		StatusContext{WorkspaceDir: "/tmp/session"}))
	if !strings.Contains(other, "demo") {
		t.Fatalf("expected foreign project label, got %q", other)
	}
}

func TestReadStatusContextParses(t *testing.T) {
	payload := `{"protocol":1,"event":"interval","model":"M3.1-Flash","workspace_dir":"/tmp/x"}`
	ctx := ReadStatusContext(strings.NewReader(payload))
	if ctx.Model != "M3.1-Flash" || ctx.WorkspaceDir != "/tmp/x" {
		t.Fatalf("context not parsed: %+v", ctx)
	}
}
