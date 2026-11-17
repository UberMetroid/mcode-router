package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const modulePrefix = "github.com/UberMetroid/mcode-router"

var (
	skipWalkDirs  = map[string]bool{".git": true, ".worktrees": true, "__pycache__": true}
	homePathRe    = regexp.MustCompile("/" + "(?:home|Users)/[A-Za-z0-9._-]+")
	shellInlineRe = regexp.MustCompile(`\b(?:ba)?` + `sh\s+` + `-c\b`)
	secretChecks  = []struct {
		label string
		re    *regexp.Regexp
	}{
		{"GitHub classic token", regexp.MustCompile("gh" + "p_[A-Za-z0-9]{20,}")},
		{"GitHub fine-grained PAT", regexp.MustCompile("github" + "_pat_[A-Za-z0-9_]{20,}")},
		{"API secret prefix", regexp.MustCompile(`\bs` + `k-[A-Za-z0-9_-]{20,}`)},
		{"Bearer token", regexp.MustCompile(`\bBea` + `rer\s+[A-Za-z0-9._-]{16,}`)},
		{"Private key block", regexp.MustCompile("-----BEGIN " + "(?:RSA |EC |OPENSSH )?PRIVATE KEY-----")},
	}
	shellBinNames = map[string]bool{
		"sh": true, "bash": true, "/bin/sh": true, "/bin/bash": true,
	}
)

func walkRepoFiles(root string, goOnly bool) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if skipWalkDirs[d.Name()] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !goOnly || strings.HasSuffix(p, ".go") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func splitLines(raw string) []string {
	if raw == "" {
		return nil
	}
	clean := strings.ReplaceAll(strings.TrimSuffix(raw, "\n"), "\r\n", "\n")
	clean = strings.TrimSuffix(clean, "\n")
	return strings.Split(clean, "\n")
}

func strLitValue(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return lit.Value, true
	}
	return s, true
}

func checkShellArgPair(el []ast.Expr, fset *token.FileSet, rel string, viols *[]string) {
	for i := 0; i+1 < len(el); i++ {
		s0, ok0 := strLitValue(el[i])
		s1, ok1 := strLitValue(el[i+1])
		if ok0 && ok1 && shellBinNames[s0] && s1 == "-c" {
			ln := fset.Position(el[i].Pos()).Line
			*viols = append(*viols, fmt.Sprintf("%s:%d: forbidden shell -c argv", rel, ln))
		}
	}
}

func checkPortable(root string) []string {
	var viols []string
	if rawMod, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		for idx, ln := range splitLines(string(rawMod)) {
			t := strings.TrimSpace(ln)
			if strings.HasPrefix(t, "require") {
				viols = append(viols, fmt.Sprintf("go.mod:%d: forbidden external require directive", idx+1))
			}
		}
	}
	for _, p := range walkRepoFiles(root, false) {
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		raw, err := os.ReadFile(p)
		if err != nil || bytes.IndexByte(raw, 0) >= 0 {
			continue
		}
		for idx, ln := range splitLines(string(raw)) {
			if homePathRe.MatchString(ln) {
				viols = append(viols, fmt.Sprintf("%s:%d: author-machine home path detected", rel, idx+1))
			}
			for _, sc := range secretChecks {
				if sc.re.MatchString(ln) {
					viols = append(viols, fmt.Sprintf("%s:%d: possible secret (%s)", rel, idx+1, sc.label))
				}
			}
		}
		if strings.HasSuffix(p, ".go") {
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, p, raw, 0)
			if perr != nil {
				viols = append(viols, fmt.Sprintf("%s: parse error: %v", rel, perr))
				continue
			}
			for _, imp := range f.Imports {
				val, _ := strconv.Unquote(imp.Path.Value)
				first := strings.SplitN(val, "/", 2)[0]
				if strings.Contains(first, ".") && !strings.HasPrefix(val, modulePrefix) {
					ln := fset.Position(imp.Pos()).Line
					viols = append(viols, fmt.Sprintf("%s:%d: external import %q", rel, ln, val))
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.BasicLit:
					if s, ok := strLitValue(node); ok && shellInlineRe.MatchString(s) {
						ln := fset.Position(node.Pos()).Line
						viols = append(viols, fmt.Sprintf("%s:%d: forbidden inline shell command", rel, ln))
					}
				case *ast.CompositeLit:
					checkShellArgPair(node.Elts, fset, rel, &viols)
				case *ast.CallExpr:
					checkShellArgPair(node.Args, fset, rel, &viols)
				}
				return true
			})
		}
	}
	return viols
}

func checkLineCap(root string) []string {
	var viols []string
	for _, p := range walkRepoFiles(root, true) {
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		lines := splitLines(string(raw))
		if len(lines) < 16 {
			viols = append(viols, fmt.Sprintf("%s: %d lines below floor 16", rel, len(lines)))
		} else if len(lines) > 256 {
			viols = append(viols, fmt.Sprintf("%s: %d lines exceeds cap 256", rel, len(lines)))
		}
		for i, ln := range lines {
			col := 0
			for _, r := range ln {
				if r == '\t' {
					col += 4 - (col % 4)
				} else {
					col++
				}
			}
			if col > 128 {
				viols = append(viols, fmt.Sprintf("%s:%d: width %d cols exceeds 128", rel, i+1, col))
			}
		}
	}
	return viols
}

func checkDensity(root string) []string {
	var viols []string
	roots := []string{"cmd", "internal", filepath.Join("plugins", "mcode-router"), "qa"}
	for _, sub := range roots {
		base := filepath.Join(root, sub)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		_ = filepath.WalkDir(base, func(dir string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if skipWalkDirs[d.Name()] {
				return filepath.SkipDir
			}
			ents, rerr := os.ReadDir(dir)
			if rerr != nil {
				return nil
			}
			var kept []string
			for _, ent := range ents {
				if !skipWalkDirs[ent.Name()] {
					kept = append(kept, ent.Name())
				}
			}
			if len(kept) > 8 {
				rel, _ := filepath.Rel(root, dir)
				viols = append(viols, fmt.Sprintf("%s: %d entries exceeds limit 8 (%s)",
					filepath.ToSlash(rel), len(kept), strings.Join(kept, ", ")))
			}
			return nil
		})
	}
	return viols
}
