// Tests for the status views: live worker telemetry and the stage routing matrix.
package main

import (
	"bytes"
	"encoding/json"
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

// writeMuseOnlyQuota stages a quota state where only the muse lane is active.
func writeMuseOnlyQuota(t *testing.T, dir string) {
	t.Helper()
	src := filepath.Join("..", "..", "plugins", "mcode-router", "config", "quota.default.json")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read default quota: %v", err)
	}
	var q map[string]any
	if err := json.Unmarshal(raw, &q); err != nil {
		t.Fatalf("parse default quota: %v", err)
	}
	provs, _ := q["providers"].(map[string]any)
	for name, raw := range provs {
		entry, _ := raw.(map[string]any)
		if name != "muse" {
			entry["status"] = "cooldown"
			entry["cooldown_until"] = "2099-01-01T00:00:00Z"
			continue
		}
		entry["model"] = "muse-ember-1.0"
		entry["effort"] = "max"
	}
	q["stages"] = map[string]any{}
	encoded, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("encode quota: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "quota.json"), encoded, 0o600); err != nil {
		t.Fatalf("write quota: %v", err)
	}
}

// writeFakeCLI drops an executable stub so the lane resolves on PATH.
func writeFakeCLI(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake cli: %v", err)
	}
}

func TestStageMatrixResolvesAuto(t *testing.T) {
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	writeFakeCLI(t, bin, "muse")
	writeMuseOnlyQuota(t, tmp)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MCODE_ROUTER_QUOTA_FILE", filepath.Join(tmp, "quota.json"))
	t.Setenv("MCODE_ROUTER_DISCOVERY_FILE", filepath.Join(tmp, "discovery.json"))
	t.Setenv("MCODE_ROUTER_SCRATCH_DIR", filepath.Join(tmp, "scratch"))
	t.Setenv("OODA_NO_AUTO_PROBE", "1")
	var out bytes.Buffer
	if code := cli.RunCLI("mcode-router", []string{"--status"}, &out, io.Discard); code != 0 {
		t.Fatalf("status exited %d", code)
	}
	text := out.String()
	if !strings.Contains(text, "muse (muse-ember-1.0/max)") {
		t.Fatalf("matrix did not resolve an unpinned stage to a concrete model:\n%s", text)
	}
	if strings.Contains(text, "auto (quota-aware)") {
		t.Fatalf("matrix still shows the unresolved placeholder:\n%s", text)
	}
}
