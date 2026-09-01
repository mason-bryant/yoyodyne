package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/action"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/separation"
)

const runstatePackage = "../runstate"

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

func TestEveryRegisteredActionWrapsAFunctionThisPackageHas(t *testing.T) {
	t.Parallel()

	declared := methodsDeclaredIn(t, ".")
	for _, step := range deliverySteps() {
		if !declared[step.action.Wraps] {
			t.Errorf("%q wraps %s, and this package declares no such method",
				step.action.Name, step.action.Wraps)
		}
	}
}

// sequencers are the functions that decide which steps a delivery run performs
// and in what order. They are the delivery loop's control flow and nothing else:
// everything a run does, it does because one of these called it or because a
// step one of these called did.
var sequencers = []string{
	"(Pipeline).Run",
	"(Pipeline).resumeRun",
	"(*activeRun).verifyReviewAndFinish",
	"(*activeRun).repairLoop",
}

// TestEveryStepTheDeliveryLoopCallsIsRegistered is the coverage claim, held
// against the pipeline's own calls rather than against a proxy for them.
//
// It walks the sequencers above and every registered step, and collects each
// *activeRun method they call directly. A step acts on a run — that is what the
// registry is a registry of — so an operation added to the delivery loop is a
// call to one of those methods, and it has to be either registered or written
// into the exemption table below with a reason. Adding one and doing neither
// fails here, which is the silent unregistered step this test exists to stop.
//
// Walking one level into each registered step as well as the sequencers is what
// catches the case the registry already contains: publishAttempt is called by
// develop rather than by Run, so a test rooted only at the control flow would
// not see it, and a second step added the same way would slip past for the same
// reason.
func TestEveryStepTheDeliveryLoopCallsIsRegistered(t *testing.T) {
	t.Parallel()

	registered := map[string]string{}
	for _, step := range deliverySteps() {
		registered[step.action.Wraps] = step.action.Name
	}
	roots := slices.Clone(sequencers)
	for wraps := range registered {
		roots = append(roots, wraps)
	}

	called := activeRunMethodsCalledBy(t, roots)
	for _, method := range slices.Sorted(maps.Keys(called)) {
		key := "(*activeRun)." + method
		if _, isStep := registered[key]; isStep {
			continue
		}
		// A sequencer calling another sequencer is the delivery loop's shape rather
		// than a step of it, and it is already named above.
		if slices.Contains(sequencers, key) {
			continue
		}
		if _, excused := notAStep[method]; excused {
			continue
		}
		t.Errorf("the delivery loop calls %s (from %s) and nothing registers it; "+
			"register an action for it, or add it to notAStep with the reason it is not a step",
			key, strings.Join(called[method], ", "))
	}

	// An exemption for something nothing calls any more is a sentence that has
	// stopped being true, and the next person reads it as a statement about code
	// that is still there.
	for method := range notAStep {
		if _, isCalled := called[method]; !isCalled {
			t.Errorf("notAStep excuses %q and the delivery loop no longer calls it; remove the exemption", method)
		}
	}
	// And an exemption for something that is registered is two answers to one
	// question.
	for method := range notAStep {
		if name, isStep := registered["(*activeRun)."+method]; isStep {
			t.Errorf("notAStep excuses %q and %q registers it", method, name)
		}
	}
}

// TestAStepDeclaresWhatTheStepsInsideItRequire holds a declaration to what
// performing it actually reaches.
//
// Capabilities are what an action requires, not what the lines of its own
// function require, and one step calling another is where the two come apart:
// candidate.develop ends by calling publishAttempt, so going through its door
// reaches the forge even though nothing in develop's own body mentions it. A
// declaration that missed that would understate the authority the action needs
// in the one place authority is written down, which is worse than not writing it
// down at all.
//
// The claim is only about steps calling steps, because that is the case where
// the answer is already recorded: the inner action has declared what it needs,
// so the outer one can be held to it without anybody having to re-derive it.
func TestAStepDeclaresWhatTheStepsInsideItRequire(t *testing.T) {
	t.Parallel()

	steps := deliverySteps()
	byWraps := map[string]action.Action[*activeRun]{}
	for _, step := range steps {
		byWraps[step.action.Wraps] = step.action
	}
	for _, step := range steps {
		for _, method := range slices.Sorted(maps.Keys(activeRunMethodsCalledBy(t, []string{step.action.Wraps}))) {
			inner, isStep := byWraps["(*activeRun)."+method]
			if !isStep || inner.Name == step.action.Name {
				continue
			}
			for _, required := range inner.Capabilities {
				if !slices.Contains(step.action.Capabilities, required) {
					t.Errorf("%q performs %q and does not declare %q, which %q requires",
						step.action.Name, inner.Name, required, inner.Name)
				}
			}
		}
	}
}

// TestEveryCapabilityTheseActionsRequireHasAHolder joins the two registries the
// authority workstream builds: what an action requires, and who holds it.
//
// Nothing checks one against the other at run time yet — a definition is compiled
// under a grant its caller assembled, not under any role's bundle — so this is the
// claim that the join will be possible at all. A capability a step requires and no
// role holds is authority nothing could ever satisfy, and the two capabilities the
// promotion needs are held by the harness rather than by a role, which is the
// answer the invariant demands rather than a hole in the table.
func TestEveryCapabilityTheseActionsRequireHasAHolder(t *testing.T) {
	t.Parallel()

	holders, err := rolecapability.Default()
	if err != nil {
		t.Fatalf("rolecapability.Default() error = %v", err)
	}
	for _, step := range deliverySteps() {
		for _, required := range step.action.Capabilities {
			if len(holders.RolesHolding(required)) > 0 {
				continue
			}
			if _, harness := holders.HarnessHolds(required); harness {
				continue
			}
			t.Errorf("%q requires %q and nothing holds it: no role's bundle carries it and it is not recorded as the harness's own",
				step.action.Name, required)
		}
	}
}

// TestEveryRegisteredActionPassesTheSeparationPolicies is the parity claim for
// the separation workstream, made where a definition cannot reach: the compiler
// holds the actions a definition selects to these policies, and this holds every
// action the harness registers to them whether a definition selects it or not.
//
// It is worth having separately because the compiler only ever sees what was
// selected. An action registered with a combination the policies refuse would sit
// in the registry unnoticed until the first definition named it, and the point of
// writing the rules over the vocabulary is that they can be asked of the table
// itself.
func TestEveryRegisteredActionPassesTheSeparationPolicies(t *testing.T) {
	t.Parallel()

	for _, step := range deliverySteps() {
		operation := separation.Operation{Name: step.action.Name, Requires: step.action.Capabilities}
		if err := separation.CheckOperation("the registered action", operation); err != nil {
			t.Errorf("%q: %v", step.action.Name, err)
		}
	}
}

// TestTheRolesThatAuthorizeAPromotionCannotPerformOne is the role half of the
// same rule, held against the bundles this repository ships.
func TestTheRolesThatAuthorizeAPromotionCannotPerformOne(t *testing.T) {
	t.Parallel()

	holders, err := rolecapability.Default()
	if err != nil {
		t.Fatalf("rolecapability.Default() error = %v", err)
	}
	if err := separation.CheckHolders(holders); err != nil {
		t.Errorf("CheckHolders() error = %v", err)
	}
}

// TestEveryRunPhaseHasARegisteredAction is the second guard, and it catches what
// the first cannot: a step whose call the walk above sees as ordinary control
// flow, but which a run can be found sitting in. A run's phase is the durable
// record of the step it reached, so a phase no registered action names is a
// place a run stops that this registry says nothing about.
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

// TestPerformingClaimReachesTheClaim is what makes the door more than
// documentation, and it pins the one thing the extraction of claim out of Run
// could have changed: the run has to end up holding the item the tracker
// returned from the claim, not the one Run read before it.
func TestPerformingClaimReachesTheClaim(t *testing.T) {
	t.Parallel()

	const itemID = "yoyodyne-ifd.209.2"
	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	claim, found := registry.Lookup("work-item.claim")
	if !found {
		t.Fatal(`Lookup("work-item.claim") found nothing`)
	}

	// The item the tracker holds before the claim is open, and fakeTracker's claim
	// is what moves it to in_progress. So a run left holding an open item is a run
	// that kept what Run read rather than what the claim returned.
	tracker := &fakeTracker{item: beads.WorkItem{
		ID:     itemID,
		Title:  "Action and capability registries wrapping the existing pipeline steps",
		Status: "open",
	}}
	run := &activeRun{
		pipeline: Pipeline{Tracker: tracker, Repository: t.TempDir()},
		state:    runstate.State{WorkItemID: itemID},
	}
	if err := claim.Perform(context.Background(), run); err != nil {
		t.Fatalf("Perform() error = %v", err)
	}
	if !run.claimed {
		t.Error("the run does not hold the item it claimed")
	}
	if run.item.Status != "in_progress" {
		t.Errorf("run.item.Status = %q, want in_progress: the run kept the item from before the claim", run.item.Status)
	}
	if run.context == "" {
		t.Error("the run was given no context to hand its developer")
	}
	if !strings.Contains(run.context, itemID) {
		t.Errorf("the run's context does not name %s", itemID)
	}
}

func TestPerformingARefusedClaimReportsTheRefusal(t *testing.T) {
	t.Parallel()

	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	claim, _ := registry.Lookup("work-item.claim")
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

// parseNonTestFiles is every non-test Go file in a directory, parsed. Test files
// are left out throughout: an action wrapping something only the tests have
// would be a door onto code no run executes.
func parseNonTestFiles(t *testing.T, directory string) []*ast.File {
	t.Helper()

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read %s: %v", directory, err)
	}
	set := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(set, filepath.Join(directory, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, parsed)
	}
	if len(files) == 0 {
		t.Fatalf("%s holds no non-test Go files; the test is reading the wrong directory", directory)
	}
	return files
}

// methodsDeclaredIn is every method a directory's package declares, keyed the
// way an action's Wraps writes it.
func methodsDeclaredIn(t *testing.T, directory string) map[string]bool {
	t.Helper()

	declared := map[string]bool{}
	for _, file := range parseNonTestFiles(t, directory) {
		for _, declaration := range file.Decls {
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
		t.Fatalf("%s declares no methods; the test is reading the wrong directory", directory)
	}
	return declared
}

// activeRunMethodsCalledBy is every *activeRun method called directly from each
// of the named functions, and which of them calls it.
//
// A call is recognized by its selector alone — the method name — and kept only
// when this package declares an *activeRun method of that name. That is what
// keeps `p.Store.Save(...)` and `lease.Release()` out of the answer without the
// test having to resolve types: the collaborators' methods are named differently
// from the run's, and one that were not would be a name worth looking at anyway.
func activeRunMethodsCalledBy(t *testing.T, roots []string) map[string][]string {
	t.Helper()

	files := parseNonTestFiles(t, ".")
	bodies := map[string]*ast.FuncDecl{}
	onTheRun := map[string]bool{}
	for _, file := range files {
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || function.Recv == nil || len(function.Recv.List) != 1 {
				continue
			}
			receiver, named := receiverName(function.Recv.List[0].Type)
			if !named {
				continue
			}
			bodies[fmt.Sprintf("(%s).%s", receiver, function.Name.Name)] = function
			if receiver == "*activeRun" {
				onTheRun[function.Name.Name] = true
			}
		}
	}
	called := map[string][]string{}
	for _, root := range roots {
		body, found := bodies[root]
		if !found {
			t.Fatalf("%s is named as a root and this package declares no such method", root)
		}
		ast.Inspect(body, func(node ast.Node) bool {
			call, isCall := node.(*ast.CallExpr)
			if !isCall {
				return true
			}
			selector, isSelector := call.Fun.(*ast.SelectorExpr)
			if !isSelector || !onTheRun[selector.Sel.Name] {
				return true
			}
			if !slices.Contains(called[selector.Sel.Name], root) {
				called[selector.Sel.Name] = append(called[selector.Sel.Name], root)
			}
			return true
		})
	}
	return called
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

// phasesDeclaredInRunstate is every run phase, read from every non-test file in
// the package that declares them rather than from a list kept here. A list here
// would be a second thing to update, and a step added with a new phase and no
// action for it would update neither.
func phasesDeclaredInRunstate(t *testing.T) []runstate.Phase {
	t.Helper()

	var phases []runstate.Phase
	for _, file := range parseNonTestFiles(t, runstatePackage) {
		for _, declaration := range file.Decls {
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
						t.Fatalf("read the value of a Phase constant in %s: %v", runstatePackage, err)
					}
					phases = append(phases, runstate.Phase(unquoted))
				}
			}
		}
	}
	if len(phases) == 0 {
		t.Fatalf("%s declares no Phase constants; the test is reading the wrong thing", runstatePackage)
	}
	return phases
}

// notAStep is every *activeRun method the delivery loop calls that is not a step
// of it, and why.
//
// It is a table rather than a heuristic because the distinction is a judgement
// somebody made: everything here was decided not to be an operation a workflow
// definition should be able to order, gate, or skip. Writing the judgement down
// is the point — a method that arrives and is neither registered nor excused
// fails the test, so the next person has to make the same judgement out loud
// instead of the question never being asked.
var notAStep = map[string]string{
	// How a run ends. None of these advances the work; they turn a stopped,
	// refused, or finished step into the outcome the run reports, and a definition
	// that could reorder them would be reordering the reporting rather than the
	// delivery.
	"fail":                       "turns a failure into the run's outcome",
	"stop":                       "turns a stopped step into the run's outcome, which for a pause or a hold leaves the run in flight",
	"finish":                     "records the outcome, closes an integrated item, prices it, and makes the run terminal before calling run.clean-up. Its tracker writes are real delivery work and it is a candidate for an action of its own; it is excused here because the seven steps this registry was scoped to do not include it",
	"blockOnFailingCheck":        "hands a spent repair budget to a person",
	"blockOnRefusedPaths":        "hands a spent repair budget to a person",
	"blockOnUnresolvedFindings":  "hands a spent repair budget to a person",
	"blockOnSpentRelaunchBudget": "hands a spent relaunch budget to a person",

	// The runtime envelope. Holds, directives, dependency waits, operator stops
	// and provider pauses are guarantees wrapped around every step rather than
	// steps between them, and the design puts them outside what a definition can
	// reach for exactly that reason: a sequence that could omit one would be a
	// sequence that could spend through a pause the operator placed.
	"stopRequested":              "reads whether the operator has asked this run to stop",
	"holdForOperator":            "waits out the operator's hold on spending",
	"holdForDirective":           "waits out a directive that pauses this work",
	"holdForDependency":          "waits out work this item was made to depend on",
	"pauseForUsageLimit":         "waits out a provider usage limit",
	"pauseForServerOverload":     "waits out a provider that could not serve the invocation",
	"awaitRecordedUsageLimit":    "serves a usage-limit deadline an earlier process recorded",
	"clearDirectivePause":        "consumes a directive pause the run recorded",
	"clearDependencyPause":       "consumes a dependency pause the run recorded",
	"clearOperatorHold":          "consumes an operator hold the run recorded",
	"recordProviderStop":         "records that the harness stopped the provider on time",
	"recordRelaunch":             "spends one of the run's relaunches",
	"mayRelaunch":                "reads whether the run has a relaunch left",
	"recordEnvironmentalRefusal": "records that the machine, not the work, refused the round",

	// Inside a step rather than beside one. Actions are coarse by design — a
	// promotion is one operation that takes the lease, checks the remote, moves the
	// branch and merges the request — so the parts of a registered step are not
	// separately orderable and must not become so.
	"attemptDevelopment":      "one provider invocation inside candidate.develop",
	"attemptReview":           "one provider invocation inside candidate.review",
	"countReviewRound":        "charges the item for a verdict inside candidate.review",
	"gateProtectedPaths":      "the scope refusal candidate.check makes before it spends a suite",
	"settleRemoteTarget":      "the pre-promotion remote check inside candidate.integrate",
	"publishIntegration":      "the merge candidate.integrate asks the forge for once the promotion stands",
	"repair":                  "records one repair attempt and re-enters candidate.develop with the findings",
	"prepareIntegrationRetry": "replays a change whose promotion lost its race, so candidate.integrate can be re-earned",
	"verifyHandback":          "checks a resumed run still has the change it preserved",

	// Recording and derivation. These write down what happened or read back what
	// the run already knows; none of them does anything to the work.
	"recordWorktree":      "records the worktree the run was given",
	"recordHarnessCommit": "records the commit the harness made of a developer's change",
	"recordCheckFailure":  "records the failing check as the run's outstanding repair input",
	"recordPathRefusal":   "records the refused paths as the run's outstanding repair input",
	"recordDevelopment":   "records what a developer invocation produced and cost",
	"deliveredInvariants": "selects the invariants a developer is given",
	"repairBudget":        "reads how many repair attempts this run may still make",
}
