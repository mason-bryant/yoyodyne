package oneline

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/mason-bryant/yoyodyne/"

// The conversation reads text through every package internal/chat imports, and
// a private fold in any of them is a byte cut that can land inside a rune and
// hand a role broken text. So no function there may collapse whitespace with
// strings.Fields and then cut the result by its own slice: the cut is Fold's.
func TestNoPackageTheConversationReadsThroughCutsASingleLineOfItsOwn(t *testing.T) {
	root := filepath.Join("..", "..")
	packages := importedFrom(t, root, "internal/chat")
	if len(packages) < 2 {
		t.Fatalf("found %d package(s) under internal/chat, want the chat package and what it imports", len(packages))
	}
	for _, want := range []string{"internal/research", "internal/repositoryread"} {
		if !packages[want] {
			t.Fatalf("internal/chat no longer reaches %s, so this sweep no longer covers it", want)
		}
	}

	var offenders []string
	for pkg := range packages {
		if pkg == "internal/oneline" {
			continue
		}
		for _, file := range sourceFiles(t, filepath.Join(root, pkg)) {
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, file, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
			for _, decl := range parsed.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				if cut := privateFoldCut(fn.Body); cut.IsValid() {
					offenders = append(offenders, fset.Position(cut).String()+" in "+fn.Name.Name)
				}
			}
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("single-line folds cut by a private slice rather than oneline.Fold:\n%s", strings.Join(offenders, "\n"))
	}
}

// privateFoldCut is where body cuts the text it folded, or no position when it
// does not: a value assigned from strings.Fields, later sliced to an upper bound.
func privateFoldCut(body *ast.BlockStmt) token.Pos {
	folded := map[string]bool{}
	ast.Inspect(body, func(node ast.Node) bool {
		if assign, ok := node.(*ast.AssignStmt); ok && len(assign.Lhs) == len(assign.Rhs) {
			for i, rhs := range assign.Rhs {
				if callsFields(rhs) {
					folded[types.ExprString(assign.Lhs[i])] = true
				}
			}
		}
		return true
	})
	var cut token.Pos
	ast.Inspect(body, func(node ast.Node) bool {
		if slice, ok := node.(*ast.SliceExpr); ok && slice.High != nil && !cut.IsValid() && folded[types.ExprString(slice.X)] {
			cut = slice.Pos()
		}
		return !cut.IsValid()
	})
	return cut
}

func callsFields(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Fields" {
				if ident, ok := selector.X.(*ast.Ident); ok && ident.Name == "strings" {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// importedFrom is start and every package of this module it imports, directly
// or not, as paths relative to the module root.
func importedFrom(t *testing.T, root, start string) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	queue := []string{start}
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		if seen[pkg] {
			continue
		}
		seen[pkg] = true
		for _, file := range sourceFiles(t, filepath.Join(root, pkg)) {
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
			for _, spec := range parsed.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatalf("import in %s: %v", file, err)
				}
				if rest, ok := strings.CutPrefix(path, modulePath); ok {
					queue = append(queue, rest)
				}
			}
		}
	}
	return seen
}

func sourceFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	return files
}
