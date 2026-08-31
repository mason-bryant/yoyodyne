package capability

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"testing"
)

func TestEveryDeclaredCapabilityIsKnown(t *testing.T) {
	t.Parallel()

	for _, declared := range All() {
		if !declared.Known() {
			t.Errorf("All() returned %q, which Known() does not recognize", declared)
		}
	}
}

func TestAnUndeclaredCapabilityIsNotKnown(t *testing.T) {
	t.Parallel()

	for _, invented := range []Capability{"", "target-branch.delete", "work-item", "WORK-ITEM.READ"} {
		if invented.Known() {
			t.Errorf("Known(%q) = true, and nothing declares it", invented)
		}
	}
}

// TestAllHandsBackACopy is what stops a caller editing the vocabulary. A closed
// set that a caller can append to is not closed.
func TestAllHandsBackACopy(t *testing.T) {
	t.Parallel()

	first := All()
	if len(first) == 0 {
		t.Fatal("All() is empty; the vocabulary is being read from somewhere it is not")
	}
	first[0] = "something else entirely"
	if second := All(); second[0] == first[0] {
		t.Errorf("All() handed back the package's own slice: editing it changed what the next caller sees")
	}
}

// TestEveryCapabilityConstantIsDeclared holds the constants above to the list
// below them, by reading the source rather than the package's own idea of
// itself. A constant added without being listed is usable in code, refused by
// every registry, and looks exactly like a typo somebody made — so it fails
// here, where the two can still be told apart.
func TestEveryCapabilityConstantIsDeclared(t *testing.T) {
	t.Parallel()

	const source = "capability.go"
	parsed, err := parser.ParseFile(token.NewFileSet(), source, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", source, err)
	}
	var constants []Capability
	for _, declaration := range parsed.Decls {
		general, isGeneral := declaration.(*ast.GenDecl)
		if !isGeneral || general.Tok != token.CONST {
			continue
		}
		for _, specification := range general.Specs {
			value, isValue := specification.(*ast.ValueSpec)
			if !isValue {
				continue
			}
			named, isNamed := value.Type.(*ast.Ident)
			if !isNamed || named.Name != "Capability" {
				continue
			}
			for _, assigned := range value.Values {
				literal, isLiteral := assigned.(*ast.BasicLit)
				if !isLiteral || literal.Kind != token.STRING {
					continue
				}
				unquoted, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatalf("read the value of a Capability constant in %s: %v", source, err)
				}
				constants = append(constants, Capability(unquoted))
			}
		}
	}
	if len(constants) == 0 {
		t.Fatalf("%s declares no Capability constants; this test is reading the wrong thing", source)
	}
	for _, constant := range constants {
		if !constant.Known() {
			t.Errorf("%s declares the capability %q and the declared list does not carry it, so nothing can require it", source, constant)
		}
	}
	for _, listed := range All() {
		if !slices.Contains(constants, listed) {
			t.Errorf("the declared list carries %q and %s declares no constant for it", listed, source)
		}
	}
}
