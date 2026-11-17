// Tests for the working-directory flag shared by the CLI and the MCP tools.
package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/UberMetroid/mcode-router/internal/cli"
)

func TestParseArgsCapturesCwd(t *testing.T) {
	opts, err := cli.ParseArgs("mcode-router",
		[]string{"--task", "fix it", "--cwd", "/tmp/repo"}, io.Discard)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if opts.Cwd != "/tmp/repo" {
		t.Fatalf("unexpected cwd %q", opts.Cwd)
	}
	if opts.Task == nil || *opts.Task != "fix it" {
		t.Fatalf("task prompt was not parsed: %+v", opts.Task)
	}
}

func TestResolveCWDHonoursFlag(t *testing.T) {
	dir := t.TempDir()
	got, ok := cli.ResolveCWD(cli.CLIOpts{Cwd: dir}, io.Discard)
	if !ok {
		t.Fatal("expected cwd to resolve")
	}
	if got != dir {
		t.Fatalf("expected %q, got %q", dir, got)
	}
}

func TestResolveCWDRejectsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	var errBuf bytes.Buffer
	if _, ok := cli.ResolveCWD(cli.CLIOpts{Cwd: file}, &errBuf); ok {
		t.Fatal("expected a file to be rejected")
	}
	if !strings.Contains(errBuf.String(), "not a directory") {
		t.Fatalf("unexpected error text: %q", errBuf.String())
	}
}

func TestStageArgvCarriesCwd(t *testing.T) {
	argv, ok := mcpStageArgv("--task", map[string]any{
		"prompt": "build it", "cwd": "/tmp/demo",
	})
	if !ok {
		t.Fatal("expected argv to build")
	}
	if !strings.Contains(strings.Join(argv, " "), "--cwd /tmp/demo") {
		t.Fatalf("task argv missing --cwd: %v", argv)
	}
	plain, _ := mcpStageArgv("--recon", map[string]any{"prompt": "look"})
	if strings.Contains(strings.Join(plain, " "), "--cwd") {
		t.Fatalf("argv must omit an empty --cwd: %v", plain)
	}
}
