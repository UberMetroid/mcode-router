package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGateFile(t *testing.T, rel, body string) {
	t.Helper()
	p := filepath.Join(rel)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func padGoLines(pkg string, n int) string {
	var b strings.Builder
	b.WriteString("package " + pkg + "\n\nfunc ok() {\n")
	for i := 0; i < n-4; i++ {
		b.WriteString("\t_ = 1\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func TestPortableAndLineCapGates(t *testing.T) {
	root := t.TempDir()
	writeGateFile(t, filepath.Join(root, "go.mod"), "module example.invalid/clean\n\ngo 1.22\n")
	writeGateFile(t, filepath.Join(root, ".git"), "gitdir: /"+"home/alice/x\n")
	writeGateFile(t, filepath.Join(root, "internal", "p", "ok.go"), padGoLines("p", 16))
	if v := checkPortable(root); len(v) != 0 {
		t.Fatalf("clean repo failed portable: %v", v)
	}
	if v := checkLineCap(root); len(v) != 0 {
		t.Fatalf("clean repo failed line-cap: %v", v)
	}

	writeGateFile(t, filepath.Join(root, "internal", "p", "bad.go"),
		"package p\n\nimport _ \"github.com/evil/dep\"\n"+
			"var x = \"/"+"home/alice/x\"\n"+
			"var k = \"s"+"k-12345678901234567890abcd\"\n"+
			"var c = \"bash \"+\"-c hi\"\n")
	writeGateFile(t, filepath.Join(root, "go.mod"), "module m\nrequire example.com/x v1.0.0\n")
	pv := checkPortable(root)
	if len(pv) < 4 {
		t.Fatalf("expected >=4 portable violations, got %v", pv)
	}

	writeGateFile(t, filepath.Join(root, "internal", "p", "short.go"), "package p\n")
	writeGateFile(t, filepath.Join(root, "internal", "p", "wide.go"),
		padGoLines("p", 15)+"// "+strings.Repeat("w", 126)+"\n")
	lv := checkLineCap(root)
	if len(lv) < 2 {
		t.Fatalf("expected line-cap violations, got %v", lv)
	}
}

func TestDensityAndVocabGates(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 8; i++ {
		writeGateFile(t, filepath.Join(root, "internal", "ok", fmt.Sprintf("f%d.go", i)), padGoLines("ok", 16))
	}
	if v := checkDensity(root); len(v) != 0 {
		t.Fatalf("8 files should pass density: %v", v)
	}
	writeGateFile(t, filepath.Join(root, "internal", "ok", "f8.go"), padGoLines("ok", 16))
	if v := checkDensity(root); len(v) == 0 {
		t.Fatalf("9 files must fail density")
	}

	vRoot := t.TempDir()
	writeGateFile(t, filepath.Join(vRoot, "utils.go"), "package main\nfunc okIdentifier() {}\n")
	writeGateFile(t, filepath.Join(vRoot, "foo_part2.go"),
		"package main\nfunc "+strings.Repeat("x", 33)+"() {}\n")
	vv := checkVocab(vRoot)
	if len(vv) < 3 {
		t.Fatalf("expected >=3 vocab violations, got %v", vv)
	}
}

func TestShapeGateChecks(t *testing.T) {
	root := t.TempDir()
	writeGateFile(t, filepath.Join(root, "good.go"), padGoLines("main", 18))
	if v := checkShape(root); len(v) != 0 {
		t.Fatalf("clean shape failed: %v", v)
	}

	var many strings.Builder
	many.WriteString("package main\n")
	for i := 0; i < 9; i++ {
		many.WriteString(fmt.Sprintf("func f%d() {}\n", i))
	}
	many.WriteString("func deep(a, b, c, d, e, f, g, h, i int) {\n")
	many.WriteString("  x := 1; _ = x\n")
	many.WriteString("  if true { if true { if true { if true { if true { _ = 1 } } } } }\n}\n")
	writeGateFile(t, filepath.Join(root, "bad.go"), many.String())
	sv := checkShape(root)
	if len(sv) < 4 {
		t.Fatalf("expected func count, params, stmt/line, and nesting violations; got %v", sv)
	}
}
