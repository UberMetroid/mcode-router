package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

var bannedGoNames = map[string]bool{
	"util.go": true, "utils.go": true, "helper.go": true, "helpers.go": true,
	"common.go": true, "misc.go": true, "shared.go": true, "base.go": true, "core.go": true,
}

func checkVocab(root string) []string {
	var viols []string
	for _, p := range walkRepoFiles(root, true) {
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		name := filepath.Base(p)
		if bannedGoNames[name] {
			viols = append(viols, fmt.Sprintf("%s: banned drawer filename %q", rel, name))
		}
		if strings.Contains(name, "_part") || strings.Contains(name, "_ext") {
			viols = append(viols, fmt.Sprintf("%s: banned split filename %q", rel, name))
		}
		if len(name) > 32 {
			viols = append(viols, fmt.Sprintf("%s: filename length %d exceeds 32", rel, len(name)))
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			viols = append(viols, fmt.Sprintf("%s: parse error: %v", rel, err))
			continue
		}
		checkID := func(id *ast.Ident) {
			if id != nil && len(id.Name) > 32 {
				ln := fset.Position(id.Pos()).Line
				viols = append(viols, fmt.Sprintf("%s:%d: identifier %q (%d) exceeds 32 chars",
					rel, ln, id.Name, len(id.Name)))
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncDecl:
				checkID(node.Name)
			case *ast.TypeSpec:
				checkID(node.Name)
			case *ast.ValueSpec:
				for _, id := range node.Names {
					checkID(id)
				}
			case *ast.Field:
				for _, id := range node.Names {
					checkID(id)
				}
			}
			return true
		})
	}
	return viols
}

func countFieldParams(fl *ast.FieldList) int {
	if fl == nil {
		return 0
	}
	n := 0
	for _, f := range fl.List {
		if len(f.Names) == 0 {
			n++
		} else {
			n += len(f.Names)
		}
	}
	return n
}

func countCodeLines(lines []string, start, end int) int {
	n := 0
	for ln := start + 1; ln < end; ln++ {
		if ln-1 < 0 || ln-1 >= len(lines) {
			continue
		}
		t := strings.TrimSpace(lines[ln-1])
		if t != "" && !strings.HasPrefix(t, "//") {
			n++
		}
	}
	return n
}

func checkStmtLineDup(fset *token.FileSet, stmts []ast.Stmt, rel string, viols *[]string) {
	seen := make(map[int]bool, len(stmts))
	for _, st := range stmts {
		ln := fset.Position(st.Pos()).Line
		if seen[ln] {
			*viols = append(*viols, fmt.Sprintf("%s:%d: multiple statements on one line", rel, ln))
		}
		seen[ln] = true
	}
}

func walkStmtDepth(fset *token.FileSet, st ast.Stmt, cur int, rel string, viols *[]string) int {
	switch s := st.(type) {
	case *ast.BlockStmt:
		return walkBlockDepth(fset, s.List, cur+1, rel, viols)
	case *ast.IfStmt:
		best := walkBlockDepth(fset, s.Body.List, cur+1, rel, viols)
		if s.Else != nil {
			best = max(best, walkStmtDepth(fset, s.Else, cur, rel, viols))
		}
		return best
	case *ast.ForStmt:
		return walkBlockDepth(fset, s.Body.List, cur+1, rel, viols)
	case *ast.RangeStmt:
		return walkBlockDepth(fset, s.Body.List, cur+1, rel, viols)
	case *ast.SwitchStmt:
		return walkBlockDepth(fset, s.Body.List, cur+1, rel, viols)
	case *ast.TypeSwitchStmt:
		return walkBlockDepth(fset, s.Body.List, cur+1, rel, viols)
	case *ast.SelectStmt:
		return walkBlockDepth(fset, s.Body.List, cur+1, rel, viols)
	case *ast.CaseClause:
		return walkBlockDepth(fset, s.Body, cur, rel, viols)
	case *ast.CommClause:
		return walkBlockDepth(fset, s.Body, cur, rel, viols)
	}
	return cur
}

func walkBlockDepth(fset *token.FileSet, stmts []ast.Stmt, cur int, rel string, viols *[]string) int {
	checkStmtLineDup(fset, stmts, rel, viols)
	best := cur
	for _, st := range stmts {
		best = max(best, walkStmtDepth(fset, st, cur, rel, viols))
	}
	return best
}

func inspectFuncShape(
	fset *token.FileSet, fn *ast.FuncDecl, lines []string, rel string, viols *[]string,
) {
	start := fset.Position(fn.Pos()).Line
	if p := countFieldParams(fn.Type.Params); p > 8 {
		*viols = append(*viols, fmt.Sprintf("%s:%d: %q has %d params (max 8)", rel, start, fn.Name.Name, p))
	}
	if fn.Body == nil {
		return
	}
	bStart := fset.Position(fn.Body.Lbrace).Line
	bEnd := fset.Position(fn.Body.Rbrace).Line
	if cl := countCodeLines(lines, bStart, bEnd); cl > 64 {
		*viols = append(*viols, fmt.Sprintf("%s:%d: %q has %d code lines (max 64)", rel, start, fn.Name.Name, cl))
	}
	if d := walkBlockDepth(fset, fn.Body.List, 0, rel, viols); d > 4 {
		*viols = append(*viols, fmt.Sprintf("%s:%d: %q block nesting depth %d (max 4)", rel, start, fn.Name.Name, d))
	}
}

func checkShape(root string) []string {
	var viols []string
	for _, p := range walkRepoFiles(root, true) {
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		lines := splitLines(string(raw))
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, p, raw, 0)
		if perr != nil {
			viols = append(viols, fmt.Sprintf("%s: parse error: %v", rel, perr))
			continue
		}
		funcCount := 0
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				funcCount++
				inspectFuncShape(fset, fn, lines, rel, &viols)
			}
		}
		if funcCount > 8 {
			viols = append(viols, fmt.Sprintf("%s: %d func decls exceeds limit 8", rel, funcCount))
		}
	}
	return viols
}
