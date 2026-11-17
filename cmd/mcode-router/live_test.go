// Tests for the live worker telemetry surface behind --live and ooda_live.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/UberMetroid/mcode-router/internal/cli"
)

// writeMarker stages a worker marker in an isolated scratch directory.
func writeMarker(t *testing.T, dir, name, body string) {
	t.Helper()
	active := filepath.Join(dir, "active")
	if err := os.MkdirAll(active, 0o700); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(active, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}
}

// runLive invokes the CLI live verb and returns its output.
func runLive(t *testing.T) string {
	t.Helper()
	var out bytes.Buffer
	if code := cli.RunCLI("mcode-router", []string{"--live"}, &out, io.Discard); code != 0 {
		t.Fatalf("live verb exited %d", code)
	}
	return out.String()
}

func TestLiveReportsActiveWorker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCODE_ROUTER_SCRATCH_DIR", dir)
	started := float64(time.Now().UnixNano()) / 1e9
	writeMarker(t, dir, "1-task1.json", fmt.Sprintf(
		`{"pid":1,"task_id":"task1","lane":"muse","cli":"muse","model":"muse-ember-1.0",`+
			`"effort":"max","phase":"thinking","tok_s":42.5,"tok_exact":true,"started_at":%f}`, started))
	text := runLive(t)
	for _, want := range []string{
		"Live Worker Telemetry", "ACTIVE", "lane=muse", "model=muse-ember-1.0",
		"effort=max", "phase=thinking", "tok/s=42.5",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("live output missing %q:\n%s", want, text)
		}
	}
}

func TestLiveReportsLastRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MCODE_ROUTER_SCRATCH_DIR", dir)
	last := `{"mode":"act","lane":"devin-fast","model":"swe-2-flash-max","effort":"max",` +
		`"exit_code":0,"duration_s":17.8}`
	if err := os.WriteFile(filepath.Join(dir, "last.json"), []byte(last), 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	text := runLive(t)
	if !strings.Contains(text, "LAST") || !strings.Contains(text, "swe-2-flash-max") {
		t.Fatalf("live output missing last run:\n%s", text)
	}
	if !strings.Contains(text, "no active workers") {
		t.Fatalf("live output should report no active workers:\n%s", text)
	}
}
