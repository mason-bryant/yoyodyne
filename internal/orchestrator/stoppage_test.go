package orchestrator

// What every way of handing a run to a person has in common.
//
// Which of the fixed outcome words a run is described by is read from
// State.Blocker: a terminal run carrying one stopped on something somebody has
// to decide, and one carrying none is a run the harness failed to carry. Every
// surface reads that distinction — `yoyo status`, the price breakdown, the
// recovery view, and the channel, where it is also the difference between a
// critical message and a warning.
//
// So a stoppage that recorded only a failure string is a stoppage described to
// the operator as a breakage, one severity quieter than it should be. The
// guarantee that none exists is not a property of any one path, and checking the
// paths one at a time is how a new one arrives uncovered. This reads them all.

import (
	"go/ast"
	"strings"
	"testing"
)

// orchestratorPackage is this package's own directory, read as source so that a
// stoppage path added later is read too.
const orchestratorPackage = "."

// blockRecorder is the one method that writes the durable blocker: it asks the
// tracker first and records on the run only what the tracker took, so a blocker
// on a run is always one the item carries too.
const blockRecorder = "block"

// Every way a run is handed to a person goes through the one method that writes
// the durable blocker. A path that ended a run with a failure string and no
// blocker would be reported as a run that broke — the word this vocabulary
// exists to stop putting over preserved work, and in the channel a warning where
// the operator is owed a critical.
//
// It is read from the source rather than exercised through the pipeline because
// what is being guaranteed is that there is no such path, and a test per path is
// a test that says nothing about the next one somebody writes.
func TestEveryWayARunIsHandedToAPersonRecordsTheDurableBlocker(t *testing.T) {
	t.Parallel()

	handedOver := 0
	for _, file := range parseNonTestFiles(t, orchestratorPackage) {
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || !strings.HasPrefix(function.Name.Name, "blockOn") {
				continue
			}
			receiver, named := receiverNameOf(function)
			if !named {
				t.Fatalf("%s is not a method, so nothing says which run it stops", function.Name.Name)
			}
			handedOver++
			if !callsMethod(function, receiver, blockRecorder) {
				t.Errorf("%s hands a run to a person without calling %s.%s, so the run ends carrying a failure and no durable blocker — every surface then describes a stoppage as a run that broke",
					function.Name.Name, receiver, blockRecorder)
			}
		}
	}
	// A rename that emptied this loop would pass it silently, which is the one
	// way a structural check like this fails without saying so.
	if handedOver == 0 {
		t.Fatalf("%s declares no blockOn... method; the test is reading the wrong thing", orchestratorPackage)
	}
}

// receiverNameOf is what a method calls itself inside its own body, or that it
// has no receiver to name.
func receiverNameOf(function *ast.FuncDecl) (string, bool) {
	if function.Recv == nil || len(function.Recv.List) != 1 || len(function.Recv.List[0].Names) != 1 {
		return "", false
	}
	return function.Recv.List[0].Names[0].Name, true
}

// callsMethod reports the function calling one method on its own receiver
// anywhere in its body, at any depth: a call inside an `if` is the ordinary
// shape here, since the blocker write is the thing whose failure is reported.
func callsMethod(function *ast.FuncDecl, receiver, method string) bool {
	called := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		selector, isSelector := call.Fun.(*ast.SelectorExpr)
		if !isSelector || selector.Sel.Name != method {
			return true
		}
		if named, isNamed := selector.X.(*ast.Ident); isNamed && named.Name == receiver {
			called = true
			return false
		}
		return true
	})
	return called
}
