package oneline

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A record that carries prose is written as JSON and checked against its byte
// bounds when it is read back. JSON encoding replaces each byte of a broken rune
// with the three-byte replacement character, so text cut to its bound in the
// middle of a rune comes back longer than the bound, and the whole record is
// refused on read: yoyodyne-ifd.428.24 found an intake hold that could not be
// read for exactly that. So every place that cuts text against a byte bound a
// JSON-carrying package validates must move the cut back to a rune start, or
// leave the cut to Fold or Bound, which already do. A new cut that does neither
// fails here rather than in a record.
func TestEveryCutAgainstAValidatedByteBoundFallsOnARuneBoundary(t *testing.T) {
	tree := parseTree(t, filepath.Join("..", ".."))
	bounds := validatedByteBounds(tree)
	for _, want := range []string{"internal/runstate.MaxBrakeTextBytes", "internal/runstate.MaxSweepTextBytes", "internal/execution.MaxEventTextBytes"} {
		if !bounds[want] {
			t.Fatalf("the sweep no longer finds %s as a validated byte bound, so it no longer covers the cuts made against it", want)
		}
	}

	derived := boundDerivedNames(tree, bounds)
	var offenders []string
	cuts := 0
	for _, file := range tree.files {
		for _, fn := range file.funcs {
			names := derived[fn.key]
			ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
				slice, ok := node.(*ast.SliceExpr)
				if !ok {
					return true
				}
				for _, index := range []ast.Expr{slice.Low, slice.High} {
					if index == nil || !mentionsBound(index, file, bounds, names) {
						continue
					}
					cuts++
					if !cutMovedToRuneStart(fn.decl.Body, index) {
						offenders = append(offenders, tree.fset.Position(slice.Pos()).String()+" in "+fn.decl.Name.Name)
					}
				}
				return true
			})
		}
	}
	if cuts == 0 {
		t.Fatal("found no cut against a validated byte bound, so the sweep is no longer looking at anything")
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("text cut against a validated byte bound without moving the cut to a rune start (use oneline.Fold or oneline.Bound, or step the cut back with utf8.RuneStart):\n%s", strings.Join(offenders, "\n"))
	}
}

type parsedTree struct {
	fset  *token.FileSet
	files []*parsedFile
	// consts is every package-level constant, as package.Name.
	consts map[string]bool
	// jsonPackages is every package with a file that imports encoding/json.
	jsonPackages map[string]bool
}

type parsedFile struct {
	pkg     string
	imports map[string]string
	funcs   []parsedFunc
}

type parsedFunc struct {
	key  string
	decl *ast.FuncDecl
}

// parseTree reads every non-test source file under cmd and internal.
func parseTree(t *testing.T, root string) parsedTree {
	t.Helper()
	tree := parsedTree{fset: token.NewFileSet(), consts: map[string]bool{}, jsonPackages: map[string]bool{}}
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := entry.Name()
			if entry.IsDir() {
				if name == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			parsed, err := parser.ParseFile(tree.fset, path, nil, 0)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			file := &parsedFile{pkg: filepath.ToSlash(rel), imports: map[string]string{}}
			for _, spec := range parsed.Imports {
				imported, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if imported == "encoding/json" {
					tree.jsonPackages[file.pkg] = true
				}
				rest, ok := strings.CutPrefix(imported, modulePath)
				if !ok {
					continue
				}
				alias := rest[strings.LastIndex(rest, "/")+1:]
				if spec.Name != nil {
					alias = spec.Name.Name
				}
				file.imports[alias] = rest
			}
			for _, decl := range parsed.Decls {
				switch decl := decl.(type) {
				case *ast.GenDecl:
					if decl.Tok != token.CONST {
						continue
					}
					for _, spec := range decl.Specs {
						for _, ident := range spec.(*ast.ValueSpec).Names {
							tree.consts[file.pkg+"."+ident.Name] = true
						}
					}
				case *ast.FuncDecl:
					if decl.Body != nil {
						file.funcs = append(file.funcs, parsedFunc{key: funcKey(file.pkg, decl), decl: decl})
					}
				}
			}
			tree.files = append(tree.files, file)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", top, err)
		}
	}
	return tree
}

// funcKey names a function so a call to it can be matched: package.Name for a
// plain function, package.Receiver.Name for a method.
func funcKey(pkg string, decl *ast.FuncDecl) string {
	if decl.Recv == nil || len(decl.Recv.List) == 0 {
		return pkg + "." + decl.Name.Name
	}
	recv := decl.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if index, ok := recv.(*ast.IndexExpr); ok {
		recv = index.X
	}
	name := "?"
	if ident, ok := recv.(*ast.Ident); ok {
		name = ident.Name
	}
	return pkg + "." + name + "." + decl.Name.Name
}

// constant resolves expr to a package-level constant of this module, as
// package.Name, or "" when it is not one.
func (f *parsedFile) constant(expr ast.Expr, consts map[string]bool) string {
	switch expr := expr.(type) {
	case *ast.Ident:
		if consts[f.pkg+"."+expr.Name] {
			return f.pkg + "." + expr.Name
		}
	case *ast.SelectorExpr:
		if pkg, ok := expr.X.(*ast.Ident); ok {
			if path, ok := f.imports[pkg.Name]; ok && consts[path+"."+expr.Sel.Name] {
				return path + "." + expr.Sel.Name
			}
		}
	case *ast.ParenExpr:
		return f.constant(expr.X, consts)
	}
	return ""
}

// validatedByteBounds is every constant a package that encodes JSON compares a
// length against: the bounds a record is refused for exceeding when it is read.
// A byte bound is named for its unit, ...Bytes, which is what tells it apart
// from a bound on how many entries a list keeps; cutting a list short cannot
// break a rune.
func validatedByteBounds(tree parsedTree) map[string]bool {
	bounds := map[string]bool{}
	for _, file := range tree.files {
		if !tree.jsonPackages[file.pkg] {
			continue
		}
		for _, fn := range file.funcs {
			ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
				binary, ok := node.(*ast.BinaryExpr)
				if !ok {
					return true
				}
				switch binary.Op {
				case token.GTR, token.GEQ, token.LSS, token.LEQ:
				default:
					return true
				}
				for _, pair := range [][2]ast.Expr{{binary.X, binary.Y}, {binary.Y, binary.X}} {
					if isLenCall(pair[0]) {
						if name := file.constant(pair[1], tree.consts); strings.HasSuffix(name, "Bytes") {
							bounds[name] = true
						}
					}
				}
				return true
			})
		}
	}
	return bounds
}

func isLenCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	ident, ok := call.Fun.(*ast.Ident)
	return ok && ident.Name == "len"
}

// boundDerivedNames is, for each function, the local names whose value is taken
// from a validated bound: a variable assigned from an expression mentioning one,
// or a parameter some call passes one to. It follows calls between functions so
// a helper that is handed the bound and cuts by its parameter is still found.
func boundDerivedNames(tree parsedTree, bounds map[string]bool) map[string]map[string]bool {
	derived := map[string]map[string]bool{}
	mark := func(key, name string) bool {
		if name == "_" || derived[key][name] {
			return false
		}
		if derived[key] == nil {
			derived[key] = map[string]bool{}
		}
		derived[key][name] = true
		return true
	}
	params := map[string][]string{}
	for _, file := range tree.files {
		for _, fn := range file.funcs {
			for _, field := range fn.decl.Type.Params.List {
				for _, name := range field.Names {
					params[fn.key] = append(params[fn.key], name.Name)
				}
				if len(field.Names) == 0 {
					params[fn.key] = append(params[fn.key], "_")
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, file := range tree.files {
			for _, fn := range file.funcs {
				names := derived[fn.key]
				ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
					switch node := node.(type) {
					case *ast.AssignStmt:
						if len(node.Lhs) != len(node.Rhs) {
							return true
						}
						for i, rhs := range node.Rhs {
							if ident, ok := node.Lhs[i].(*ast.Ident); ok && mentionsBound(rhs, file, bounds, names) {
								changed = mark(fn.key, ident.Name) || changed
							}
						}
					case *ast.ValueSpec:
						for i, value := range node.Values {
							if i < len(node.Names) && mentionsBound(value, file, bounds, names) {
								changed = mark(fn.key, node.Names[i].Name) || changed
							}
						}
					case *ast.CallExpr:
						callee := file.callee(node.Fun)
						for i, arg := range node.Args {
							if i < len(params[callee]) && mentionsBound(arg, file, bounds, names) {
								changed = mark(callee, params[callee][i]) || changed
							}
						}
					}
					return true
				})
			}
		}
	}
	return derived
}

// callee resolves a call to a plain function of this module, or "" when it is
// not one; a method call cannot be resolved without types and is not followed.
func (f *parsedFile) callee(fun ast.Expr) string {
	switch fun := fun.(type) {
	case *ast.Ident:
		return f.pkg + "." + fun.Name
	case *ast.SelectorExpr:
		if pkg, ok := fun.X.(*ast.Ident); ok {
			if path, ok := f.imports[pkg.Name]; ok {
				return path + "." + fun.Sel.Name
			}
		}
	}
	return ""
}

// mentionsBound reports whether expr reads a validated bound, directly or
// through a name derived from one.
func mentionsBound(expr ast.Expr, file *parsedFile, bounds map[string]bool, names map[string]bool) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if found {
			return false
		}
		switch node := node.(type) {
		case *ast.SelectorExpr:
			if bounds[file.qualified(node)] {
				found = true
			}
			return !found
		case *ast.Ident:
			if bounds[file.pkg+"."+node.Name] || names[node.Name] {
				found = true
			}
		case *ast.FuncLit:
			return false
		}
		return !found
	})
	return found
}

func (f *parsedFile) qualified(selector *ast.SelectorExpr) string {
	if pkg, ok := selector.X.(*ast.Ident); ok {
		if path, ok := f.imports[pkg.Name]; ok {
			return path + "." + selector.Sel.Name
		}
	}
	return ""
}

// cutMovedToRuneStart reports whether the cut index is a name the function steps
// to a rune start: a loop whose condition asks utf8.RuneStart and reads it.
func cutMovedToRuneStart(body *ast.BlockStmt, index ast.Expr) bool {
	ident, ok := index.(*ast.Ident)
	if !ok {
		return false
	}
	moved := false
	ast.Inspect(body, func(node ast.Node) bool {
		loop, ok := node.(*ast.ForStmt)
		if !ok || loop.Cond == nil || moved {
			return !moved
		}
		asksRuneStart, readsIndex := false, false
		ast.Inspect(loop.Cond, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.SelectorExpr:
				if pkg, ok := node.X.(*ast.Ident); ok && pkg.Name == "utf8" && node.Sel.Name == "RuneStart" {
					asksRuneStart = true
				}
			case *ast.Ident:
				if node.Name == ident.Name {
					readsIndex = true
				}
			}
			return true
		})
		moved = asksRuneStart && readsIndex
		return !moved
	})
	return moved
}
