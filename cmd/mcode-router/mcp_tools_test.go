// Tests for the MCP tool surface: catalogue shape and argv assembly.
package main

import (
	"strings"
	"testing"
)

func TestToolCatalogShape(t *testing.T) {
	specs := mcpToolSpecs()
	if len(specs) != 7 {
		t.Fatalf("expected 7 tools, got %d", len(specs))
	}
	seen := map[string]bool{}
	for _, spec := range specs {
		if spec.Name == "" {
			t.Fatal("tool with empty name")
		}
		if seen[spec.Name] {
			t.Fatalf("duplicate tool name %q", spec.Name)
		}
		seen[spec.Name] = true
		if strings.TrimSpace(spec.Description) == "" {
			t.Fatalf("%s has empty description", spec.Name)
		}
		if spec.InputSchema["type"] != "object" {
			t.Fatalf("%s schema is not an object", spec.Name)
		}
		if _, ok := spec.InputSchema["properties"]; !ok {
			t.Fatalf("%s schema missing properties", spec.Name)
		}
	}
	for _, want := range []string{"ooda_recon", "ooda_orient", "ooda_task", "ooda_review"} {
		if !seen[want] {
			t.Fatalf("missing stage tool %q", want)
		}
	}
}

func TestStageArgvRequiresPrompt(t *testing.T) {
	if _, ok := mcpStageArgv("--recon", map[string]any{}); ok {
		t.Fatal("expected missing prompt to be rejected")
	}
	if _, ok := mcpStageArgv("--recon", map[string]any{"prompt": "   "}); ok {
		t.Fatal("expected blank prompt to be rejected")
	}
}

func TestStageArgvOrderAndFlags(t *testing.T) {
	params := map[string]any{
		"prompt":     "find the parser",
		"lane":       "devin-fast",
		"complexity": "recon",
		"timeout":    "180s",
		"model":      "swe-2-flash-high",
		"effort":     "high",
	}
	argv, ok := mcpStageArgv("--recon", params)
	if !ok {
		t.Fatal("expected argv to build")
	}
	if argv[0] != "--recon" || argv[1] != "find the parser" {
		t.Fatalf("unexpected argv head: %v", argv)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{"--lane devin-fast", "--complexity recon", "--timeout 180s"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv missing %q: %v", want, argv)
		}
	}
	if strings.Contains(joined, "--files") {
		t.Fatalf("recon argv must not carry --files: %v", argv)
	}
}

func TestTaskArgvCarriesFiles(t *testing.T) {
	params := map[string]any{"prompt": "build it", "files": "a.py,b.py"}
	argv, ok := mcpStageArgv("--task", params)
	if !ok {
		t.Fatal("expected argv to build")
	}
	if !strings.Contains(strings.Join(argv, " "), "--files a.py,b.py") {
		t.Fatalf("task argv missing --files: %v", argv)
	}
}

func TestReviewArgvDefaultsToCwd(t *testing.T) {
	argv := mcpReviewArgv(map[string]any{})
	if len(argv) != 1 || argv[0] != "--review" {
		t.Fatalf("unexpected review argv: %v", argv)
	}
	withPath := mcpReviewArgv(map[string]any{"path": "/tmp/repo"})
	if len(withPath) != 2 || withPath[1] != "/tmp/repo" {
		t.Fatalf("unexpected review argv with path: %v", withPath)
	}
}

func TestSetStageArgv(t *testing.T) {
	if _, ok := mcpSetArgv(map[string]any{"stage": "act"}); ok {
		t.Fatal("expected missing lane to be rejected")
	}
	argv, ok := mcpSetArgv(map[string]any{"stage": "act", "lane": "devin-pro"})
	if !ok {
		t.Fatal("expected set-stage argv to build")
	}
	if argv[0] != "--set-stage" || argv[1] != "act=devin-pro" {
		t.Fatalf("unexpected set-stage argv: %v", argv)
	}
	local, _ := mcpSetArgv(map[string]any{"stage": "act", "lane": "auto", "local": true})
	if !strings.Contains(strings.Join(local, " "), "--local") {
		t.Fatalf("expected --local modifier: %v", local)
	}
}
