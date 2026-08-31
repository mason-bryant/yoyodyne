package orchestrator

// The registry's own tests, and the repository test that holds it to the
// pipeline it claims to cover.
//
// Two things can go wrong with a registry that wraps code somebody else keeps
// changing, and neither of them shows up in a diff that only touches the
// pipeline. A step's function can be renamed or removed under an action that
// still names it, which leaves the registry describing a door onto nothing. And
// a step can be added to the pipeline with no action registered for it, which
// leaves the registry looking complete while a definition built from it would
// skip work the hard-coded pipeline does. The two tests below fail on each,
// against the source rather than against a second list written here.

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// runstateSource is where the run phases are declared, reached from this
// package's directory. It is read where it lives rather than through the
// runstate package's own API because the phases have no exported listing: what
// this test needs is every phase somebody declared, including one added this
// morning and wired to nothing yet.
const runstateSource = "../runstate/state.go"

func TestTheDeliveryRegistryIsBuildable(t *testing.T) {
	t.Parallel()

	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	want := []string{
		"work-item.claim",
		"candidate.develop",
		"candidate.publish",
		"candidate.check",
		"candidate.review",
		"candidate.integrate",
		"run.clean-up",
	}
	if names := registry.Names(); !slices.Equal(names, want) {
		t.Errorf("Names() = %v, want %v", names, want)
	}
}

// TestEveryRegisteredActionWrapsAFunctionThisPackageHas is the first half of the
// coverage claim: every action is a second door onto trusted code that is
// actually there. A step renamed without its registration being updated fails
// here rather than at whatever later moment something first tried to dispatch
// it.
func TestEveryRegisteredActionWrapsAFunctionThisPackageHas(t *testing.T) {
	t.Parallel()

	declared := methodsDeclaredHere(t)
	for _, step := range deliverySteps() {
		if !declared[step.action.Wraps] {
			t.Errorf("%q wraps %s, and this package declares no such method",
				step.action.Name, step.action.Wraps)
		}
	}
}

// TestEveryRunPhaseHasARegisteredAction is the second half, and the one that
// stops a step being added unregistered. A run's phase is the durable record of
// the step it reached, so a step a run can actually be found in is a step with a
// phase — and a phase no registered action names is a step nothing here covers.
func TestEveryRunPhaseHasARegisteredAction(t *testing.T) {
	t.Parallel()

	covering := map[runstate.Phase][]string{}
	for _, step := range deliverySteps() {
		for _, phase := range step.phases {
			covering[phase] = append(covering[phase], step.action.Name)
		}
	}
	declared := phasesDeclaredInRunstate(t)
	for _, phase := range declared {
		// The terminal phase is where a run has finished rather than a step it
		// performs, so nothing registers an action for it. It is exempted by name so
		// that a reader can see it was decided rather than missed.
		if phase == runstate.PhaseComplete {
			if named := covering[phase]; len(named) > 0 {
				t.Errorf("%v registered for the terminal phase %q, which is not a step", named, phase)
			}
			continue
		}
		if len(covering[phase]) == 0 {
			t.Errorf("runstate declares the phase %q and no registered action names it; a run reaches a step this registry does not cover", phase)
		}
	}
	for phase, named := range covering {
		if !slices.Contains(declared, phase) {
			t.Errorf("%v name the phase %q, which runstate does not declare", named, phase)
		}
	}
}

// TestPerformingClaimReachesTheClaim and its cleanup counterpart are what make
// the doors more than documentation. They are deliberately driven through a
// refusal: the failure each step wraps its own errors in is unique to it, so a
// door that reached some other function could not produce it.
func TestPerformingClaimReachesTheClaim(t *testing.T) {
	t.Parallel()

	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	claim, found := registry.Lookup("work-item.claim")
	if !found {
		t.Fatal(`Lookup("work-item.claim") found nothing`)
	}
	refused := errors.New("the tracker refused")
	run := &activeRun{
		pipeline: Pipeline{Tracker: &fakeTracker{onClaim: func() error { return refused }}},
		state:    runstate.State{WorkItemID: "yoyodyne-ifd.209.2"},
	}
	err = claim.Perform(context.Background(), run)
	if !errors.Is(err, refused) {
		t.Fatalf("Perform() error = %v, want the tracker's refusal", err)
	}
	if !strings.Contains(err.Error(), "claim work item") {
		t.Errorf("Perform() error = %v, and it is not the one claim wraps", err)
	}
	if run.claimed {
		t.Error("a refused claim left the run holding the item")
	}
}

func TestPerformingCleanUpReachesTheCleanUp(t *testing.T) {
	t.Parallel()

	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	cleanUp, found := registry.Lookup("run.clean-up")
	if !found {
		t.Fatal(`Lookup("run.clean-up") found nothing`)
	}
	run := &activeRun{
		pipeline: Pipeline{Worktrees: partialWorktreeManager{}},
		outcome: Outcome{Integration: &gitworktree.Integration{
			TargetBranch: "main",
			SourceCommit: "b0bb1e5",
		}},
	}
	err = cleanUp.Perform(context.Background(), run)
	if err == nil {
		t.Fatal("Perform() returned no error, and the worktree manager cannot clean up")
	}
	if !strings.Contains(err.Error(), "clean up integrated run artifacts") {
		t.Errorf("Perform() error = %v, and it is not the one cleanUp wraps", err)
	}
}

// methodsDeclaredHere is every method this package declares, keyed the way an
// action's Wraps writes it. Test files are left out: an action wrapping
// something only the tests have would be a door onto code no run executes.
func methodsDeclaredHere(t *testing.T) map[string]bool {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read this package's directory: %v", err)
	}
	set := token.NewFileSet()
	declared := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(set, filepath.Clean(name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, declaration := range parsed.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || function.Recv == nil || len(function.Recv.List) != 1 {
				continue
			}
			receiver, named := receiverName(function.Recv.List[0].Type)
			if !named {
				continue
			}
			declared[fmt.Sprintf("(%s).%s", receiver, function.Name.Name)] = true
		}
	}
	if len(declared) == 0 {
		t.Fatal("this package declares no methods; the test is reading the wrong directory")
	}
	return declared
}

// receiverName renders a method receiver the way Wraps writes it: `*activeRun`
// for a pointer receiver and `Pipeline` for a value one. Anything else — a
// generic receiver, say — is not named rather than guessed at.
func receiverName(expression ast.Expr) (string, bool) {
	switch receiver := expression.(type) {
	case *ast.Ident:
		return receiver.Name, true
	case *ast.StarExpr:
		pointed, isIdent := receiver.X.(*ast.Ident)
		if !isIdent {
			return "", false
		}
		return "*" + pointed.Name, true
	default:
		return "", false
	}
}

// phasesDeclaredInRunstate is every run phase, read from where they are
// declared. Reading the source rather than a list kept here is the whole point:
// a list here would be a second thing to update, and a step added with a new
// phase and no action for it would update neither.
func phasesDeclaredInRunstate(t *testing.T) []runstate.Phase {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), runstateSource, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", runstateSource, err)
	}
	var phases []runstate.Phase
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
			if !isNamed || named.Name != "Phase" {
				continue
			}
			for _, assigned := range value.Values {
				literal, isLiteral := assigned.(*ast.BasicLit)
				if !isLiteral || literal.Kind != token.STRING {
					continue
				}
				unquoted, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatalf("read the value of a Phase constant in %s: %v", runstateSource, err)
				}
				phases = append(phases, runstate.Phase(unquoted))
			}
		}
	}
	if len(phases) == 0 {
		t.Fatalf("%s declares no Phase constants; the test is reading the wrong thing", runstateSource)
	}
	return phases
}
