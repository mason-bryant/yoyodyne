package orchestrator

// The parity harness: the built-in delivery definitions walked against the
// recorded baseline of what the hard-coded pipeline actually does.
//
// What it is for is the one claim a definition cannot make about itself. Any
// well-formed state machine compiles; the question is whether this one is the
// pipeline, and the only answer worth anything is the frozen behaviour in
// testdata/baseline. So every recorded path is written down here as a
// transcript -- the states the run stood in and the outcome it produced in each
// -- and each transcript is put through two doors that have to agree.
//
// The witness holds a transcript against the trace it claims to describe. A
// developer invocation in the trace is a `develop` state in the transcript, a
// verdict is a `review` state, `repair_attempts` is the number of times the
// transcript went back to the developer from the gate, `integration_retries` is
// the number of `superseded` outcomes, and the commands the event log records
// are the check states that got as far as running them. A transcript that
// claims a path the trace does not evidence fails there, which is what stops
// this file from being a story about the pipeline rather than a reading of it.
//
// The executor walks the transcript through the real graph. The definition is
// compiled by the same loader, under the same grant, from the same bytes the
// build ships, and stepped by `internal/workflow`'s own executor, so a
// transition the definition does not have is a step that fails rather than a
// mismatch nobody notices. What the doors perform is replaced by nothing at all:
// the actions are the registered ones -- their names, their capabilities and
// what they wrap, taken from the same table `actions.go` builds the delivery
// registry from -- with Perform swapped for a function that does nothing, so the
// walk is over the real topology without claiming a work item or promoting
// anything. That is the whole of what "compiled and executed only under the test
// harness" means here.
//
// What it does not do is compare a run's durable record field for field, and it
// could not: everything the pipeline does outside its seven registered steps --
// the pre-claim questions, the pauses, the budgets, the outcome recorded on the
// item -- is Go control flow behind no door, so there is nothing to execute that
// would produce a record to compare. The traces stay the measure; this measures
// the sequence against them, and the change summary says which paths that
// leaves unmeasured.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/action"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/workflow"
)

// parityStep is one state an instance stood in, and the outcome it produced
// there.
type parityStep struct {
	state   string
	outcome string
}

// parityScenario is one recorded delivery path as a transcript of the built-in
// definition that expresses it.
type parityScenario struct {
	// trace is the recorded baseline this scenario is measured against, by the
	// name of its file without the extension.
	trace string
	// workflow is the built-in definition this path would be bound to.
	workflow string
	// steps is the transcript, in order.
	steps []parityStep
	// terminal is where the transcript ends. A run a process was killed inside
	// reaches none, and names the state it was left standing in instead.
	terminal string
	standing string
	// unexpressible is why no definition walks this path at all, for the paths
	// that stop before the first state. A scenario carrying it has no transcript,
	// and the trace is held to having no run behind it.
	unexpressible string
}

// The states of the built-in definitions, by the names the files declare them
// under. They are constants because the witness counts them and a state renamed
// in a file without being renamed here would be a count that quietly stopped
// measuring anything.
const (
	parityClaim     = "claim"
	parityDevelop   = "develop"
	parityCheck     = "check"
	parityReview    = "review"
	parityIntegrate = "integrate"
	parityCleanUp   = "clean-up"
)

// parityScenarios is every recorded delivery path, said as a transcript.
//
// The order is the order the traces are listed in the baseline document, which
// is the order somebody comparing the two would read them.
func parityScenarios() []parityScenario {
	promoted := []parityStep{
		{parityClaim, "claimed"},
		{parityDevelop, "produced"},
		{parityCheck, "passed"},
		{parityReview, "approved"},
		{parityIntegrate, "integrated"},
		{parityCleanUp, "cleaned"},
	}
	// A pause, an overload, a stop the harness made and a transient death are one
	// transition apiece in the same place: the attempt is reissued and the run is
	// back in the developer. The traces differ in what the run recorded about the
	// wait, which is a durable field rather than a sequence, so the four walk the
	// same path.
	reissuedThenPromoted := append([]parityStep{
		{parityClaim, "claimed"},
		{parityDevelop, "reissued"},
	}, promoted[1:]...)

	return []parityScenario{
		{
			trace:    "human-approved-change-is-preserved-for-its-approver",
			workflow: HumanApprovalWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
			},
			terminal: "preserved",
		},
		{
			trace:    "automatic-run-promotes-reviews-closes-and-cleans-up",
			workflow: DeliveryWorkflowID,
			steps:    promoted,
			terminal: "delivered",
		},
		{
			trace:    "failing-check-is-repaired-and-then-promoted",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "failed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
				{parityIntegrate, "integrated"},
				{parityCleanUp, "cleaned"},
			},
			terminal: "delivered",
		},
		{
			trace:    "failing-check-spends-the-repair-budget-and-blocks",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "failed"},
				{parityDevelop, "produced"},
				{parityCheck, "failed"},
				{parityDevelop, "produced"},
				{parityCheck, "failed-unrepaired"},
			},
			terminal: "blocked",
		},
		{
			trace:    "review-findings-are-repaired-and-then-promoted",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "changes-requested"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
				{parityIntegrate, "integrated"},
				{parityCleanUp, "cleaned"},
			},
			terminal: "delivered",
		},
		{
			trace:    "review-findings-spend-the-repair-budget-and-block",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "changes-requested"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "changes-requested"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "unresolved"},
			},
			terminal: "blocked",
		},
		{
			trace:    "protected-path-refusal-is-repaired-before-any-check-runs",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "refused"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
				{parityIntegrate, "integrated"},
				{parityCleanUp, "cleaned"},
			},
			terminal: "delivered",
		},
		{
			trace:    "protected-path-refusal-spends-the-repair-budget-and-blocks",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "refused"},
				{parityDevelop, "produced"},
				{parityCheck, "refused"},
				{parityDevelop, "produced"},
				{parityCheck, "refused-unrepaired"},
			},
			terminal: "blocked",
		},
		{
			trace:    "protected-path-grant-admits-the-change-it-names",
			workflow: DeliveryWorkflowID,
			steps:    promoted,
			terminal: "delivered",
		},
		{
			trace:    "usage-limit-pause-exits-resumable-and-a-later-invocation-finishes-it",
			workflow: DeliveryWorkflowID,
			steps:    reissuedThenPromoted,
			terminal: "delivered",
		},
		{
			trace:    "server-overload-pause-reissues-the-same-attempt",
			workflow: DeliveryWorkflowID,
			steps:    reissuedThenPromoted,
			terminal: "delivered",
		},
		{
			trace:    "provider-stopped-on-time-is-resumable-and-continues-the-same-attempt",
			workflow: DeliveryWorkflowID,
			steps:    reissuedThenPromoted,
			terminal: "delivered",
		},
		{
			trace:    "operator-stop-cancels-the-run-and-preserves-its-work",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				// The stop is taken at the reviewer's own boundary, before a verdict
				// is bought: the state was entered and produced no judgement, which is
				// why the witness counts it as a review that obtained nothing.
				{parityReview, "stopped"},
			},
			terminal: "abandoned",
		},
		{
			trace:    "verdict-fields-the-schema-does-not-name-are-recorded-as-drift",
			workflow: DeliveryWorkflowID,
			steps:    promoted,
			terminal: "delivered",
		},
		{
			trace:    "partial-cleanup-leaves-a-succeeded-run-reporting-what-survives",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
				{parityIntegrate, "integrated"},
				{parityCleanUp, "partial"},
			},
			terminal: "delivered",
		},
		{
			trace:    "transient-provider-death-relaunches-without-charging-the-developer",
			workflow: DeliveryWorkflowID,
			steps:    reissuedThenPromoted,
			terminal: "delivered",
		},
		{
			trace:    "transient-deaths-spend-the-relaunch-budget-and-block",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "reissued"},
				{parityDevelop, "reissued"},
				{parityDevelop, "relaunches-spent"},
			},
			terminal: "blocked",
		},
		{
			trace:         "unresolved-directive-pauses-the-work-before-anything-is-claimed",
			unexpressible: "the directive is read before the claim, and a definition begins at the claim; nothing was claimed, so there is no first state for an instance to stand in",
		},
		{
			trace:         "unfinished-dependency-pauses-the-work-before-anything-is-claimed",
			unexpressible: "the dependency is read at the same boundary as the directive, before the claim and before any state",
		},
		{
			trace:         "operator-hold-starts-nothing-at-all",
			unexpressible: "the hold is the first question Run asks and it is asked before anything is claimed; a held harness never reaches a state",
		},
		{
			trace:    "operator-hold-parks-a-claimed-run-and-accounts-for-what-it-cost",
			workflow: DeliveryWorkflowID,
			// The hold reached a run that was already developing and was lifted while
			// it waited, so the wait happened inside the developer's own state and the
			// sequence is the ordinary one. What the park cost is a durable field on
			// the run rather than a transition.
			steps:    promoted,
			terminal: "delivered",
		},
		{
			trace:         "intake-hold-starts-nothing-the-harness-chose",
			unexpressible: "the intake hold stops the choosing rather than the work, before a run is reserved and before any state",
		},
		{
			trace:    "promotion-is-replayed-when-the-target-branch-moves",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
				{parityIntegrate, "superseded"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
				{parityIntegrate, "integrated"},
				{parityCleanUp, "cleaned"},
			},
			terminal: "delivered",
		},
		{
			trace:    "integration-retries-are-bounded-and-block-the-item",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
				{parityIntegrate, "contended"},
			},
			terminal: "blocked",
		},
		{
			trace:    "reconciliation-completes-a-run-interrupted-inside-integration",
			workflow: DeliveryWorkflowID,
			// The process died inside the promotion, which is the one boundary
			// durable state cannot describe. The instance is left standing where the
			// executor would leave it: the action was performed and its outcome was
			// never recorded. What settles it is the sweep, and no definition
			// expresses that -- `Reconciler.Reconcile` is not a registered action.
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
				{parityCheck, "passed"},
				{parityReview, "approved"},
			},
			standing: parityIntegrate,
		},
		{
			trace:    "reconciliation-blocks-a-run-interrupted-while-developing",
			workflow: DeliveryWorkflowID,
			steps: []parityStep{
				{parityClaim, "claimed"},
				{parityDevelop, "produced"},
			},
			standing: parityCheck,
		},
	}
}

// TestBuiltinDeliveryDefinitionsCompile is the first half of the item's own
// acceptance: the definitions this build ships compile clean, against the
// registry the pipeline's steps are registered in and under the authority a
// delivery run holds.
func TestBuiltinDeliveryDefinitionsCompile(t *testing.T) {
	t.Parallel()

	for _, builtin := range builtinDeliveryWorkflows() {
		t.Run(builtin.ID, func(t *testing.T) {
			t.Parallel()
			graph, err := compileDelivery(builtin)
			if err != nil {
				t.Fatalf("compileDelivery(%s) error = %v", builtin.Source, err)
			}
			if graph.ID() != builtin.ID {
				t.Errorf("ID() = %q, want %q", graph.ID(), builtin.ID)
			}
			if graph.Schema() != workflow.SchemaVersion {
				t.Errorf("Schema() = %d, want %d", graph.Schema(), workflow.SchemaVersion)
			}
			// A definition an instance pins itself to has to digest the same on
			// every read of the same bytes, or the pin is a refusal waiting to
			// happen.
			again, err := compileDelivery(builtin)
			if err != nil {
				t.Fatalf("compileDelivery(%s) second error = %v", builtin.Source, err)
			}
			if graph.Digest() != again.Digest() {
				t.Errorf("Digest() = %q then %q; one definition digests to one thing", graph.Digest(), again.Digest())
			}
		})
	}
}

// TestBuiltinDeliveryDefinitionsSelectEveryRegisteredStepButPublishing names the
// registered steps no built-in definition selects.
//
// There is exactly one and it is deliberate: `candidate.develop` ends by calling
// publishAttempt, so a definition that also selected `candidate.publish` would
// publish every attempt twice. Pinning the list is what keeps that a decision
// rather than an omission -- a step that stops being selected shows up here
// instead of as a sequence quietly missing a stage.
func TestBuiltinDeliveryDefinitionsSelectEveryRegisteredStepButPublishing(t *testing.T) {
	t.Parallel()

	selected := map[string]bool{}
	for _, builtin := range builtinDeliveryWorkflows() {
		graph, err := compileDelivery(builtin)
		if err != nil {
			t.Fatalf("compileDelivery(%s) error = %v", builtin.Source, err)
		}
		for _, state := range graph.States() {
			node, _ := graph.Node(state)
			selected[node.Action().Name] = true
		}
	}
	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	var unselected []string
	for _, name := range registry.Names() {
		if !selected[name] {
			unselected = append(unselected, name)
		}
	}
	want := []string{"candidate.publish"}
	if !slices.Equal(unselected, want) {
		t.Errorf("the built-in definitions select every registered step except %v; they leave out %v", want, unselected)
	}
}

// TestParityCoversEveryRecordedTrace refuses a recorded path this harness says
// nothing about, and a scenario naming a trace nobody records.
//
// It is the check that stops a path being skipped silently, which is the one
// failure a harness of transcripts cannot report by failing to match: a trace
// with no scenario is measured by nothing at all and looks exactly like one that
// passed.
func TestParityCoversEveryRecordedTrace(t *testing.T) {
	t.Parallel()

	recorded, err := filepath.Glob(filepath.Join(baselineDirectory, "*.json"))
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	measured := map[string]bool{}
	for _, scenario := range parityScenarios() {
		if measured[scenario.trace] {
			t.Errorf("%s is measured by two parity scenarios", scenario.trace)
		}
		measured[scenario.trace] = true
	}
	for _, path := range recorded {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		if !measured[name] {
			t.Errorf("%s is recorded and no parity scenario walks it; write its transcript or name why no definition can express it", path)
		}
		delete(measured, name)
	}
	for name := range measured {
		t.Errorf("the parity scenario %q names a trace nothing records", name)
	}
}

// TestBuiltinDeliveryDefinitionWalksEveryRecordedPath is the harness itself:
// every recorded path witnessed against its trace, and walked through the
// definition that claims to express it.
func TestBuiltinDeliveryDefinitionWalksEveryRecordedPath(t *testing.T) {
	t.Parallel()

	for _, scenario := range parityScenarios() {
		t.Run(scenario.trace, func(t *testing.T) {
			t.Parallel()
			trace := readParityTrace(t, scenario.trace)
			if scenario.unexpressible != "" {
				witnessNoRun(t, scenario, trace)
				return
			}
			witnessTranscript(t, scenario, trace)
			walkTranscript(t, scenario)
		})
	}
}

//
// The trace, as this harness reads one.
//

// parityTrace is the half of a recorded baseline the witness reads: what the run
// asked the provider for, what the harness itself observed, what became of the
// work item, and the durable counters. Everything else a trace carries is left
// out, because what is being measured here is a sequence.
type parityTrace struct {
	Steps           []parityTracedStep `json:"steps"`
	Reconciliations []map[string]any   `json:"reconciliations"`
	Durable         map[string]any     `json:"durable_run_record"`
	Events          []string           `json:"events"`
	WorkItem        struct {
		Calls   []string `json:"calls"`
		Closed  bool     `json:"closed"`
		Blocked bool     `json:"blocked"`
	} `json:"work_item"`
	Provider []string `json:"provider_invocations"`
}

type parityTracedStep struct {
	Ending string `json:"ending"`
}

func readParityTrace(t *testing.T, name string) parityTrace {
	t.Helper()
	path := filepath.Join(baselineDirectory, name+".json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	var trace parityTrace
	if err := json.Unmarshal(content, &trace); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", path, err)
	}
	return trace
}

// counter is a durable counter the trace carries, or zero where it carries none:
// a counter nothing spent is absent from the record rather than recorded as nil.
func (p parityTrace) counter(field string) int {
	value, recorded := p.Durable[field]
	if !recorded {
		return 0
	}
	number, isANumber := value.(float64)
	if !isANumber {
		return 0
	}
	return int(number)
}

// invocations is how many of the run's provider calls were made as a role.
func (p parityTrace) invocations(role string) int {
	made := 0
	for _, invocation := range p.Provider {
		if strings.HasPrefix(invocation, role+":") {
			made++
		}
	}
	return made
}

// events is how many of one kind the harness appended, reading back the counting
// the trace does for consecutive repeats.
func (p parityTrace) events(kind string) int {
	appended := 0
	for _, entry := range p.Events {
		name, suffix, repeated := strings.Cut(entry, " x")
		if name != kind {
			continue
		}
		if !repeated {
			appended++
			continue
		}
		count := 0
		if _, err := fmt.Sscanf(suffix, "%d", &count); err != nil {
			continue
		}
		appended += count
	}
	return appended
}

// ending is the outcome word the last invocation of the pipeline was read as,
// which is what the terminal of a transcript has to agree with.
func (p parityTrace) ending() string {
	if len(p.Steps) == 0 {
		return ""
	}
	return p.Steps[len(p.Steps)-1].Ending
}

//
// The witness.
//

// witnessNoRun holds a path that never reached a state to having nothing behind
// it: no run, no claim, and nothing asked of the provider. A trace that recorded
// any of the three would be one whose scenario says it stops before the claim
// and whose evidence says otherwise.
func witnessNoRun(t *testing.T, scenario parityScenario, trace parityTrace) {
	t.Helper()
	if len(scenario.steps) > 0 {
		t.Errorf("%s is named unexpressible and carries a transcript; it is one or the other", scenario.trace)
	}
	if trace.Durable != nil {
		t.Errorf("%s: %s, and the trace records a run", scenario.trace, scenario.unexpressible)
	}
	if len(trace.WorkItem.Calls) > 0 {
		t.Errorf("%s: %s, and the trace records the tracker calls %v", scenario.trace, scenario.unexpressible, trace.WorkItem.Calls)
	}
	if len(trace.Provider) > 0 {
		t.Errorf("%s: %s, and the trace records the provider invocations %v", scenario.trace, scenario.unexpressible, trace.Provider)
	}
}

// witnessTranscript holds a transcript against the trace it claims to describe.
//
// Each rule is one thing the trace records and the transcript therefore has to
// say. Together they pin every state the transcript can contain: the developer
// invocations pin `develop`, the verdicts pin `review`, the commands pin
// `check`, `repair_attempts` pins which of the transitions into the developer
// are repairs, `integration_retries` pins the replays, and where the item ended
// up pins the terminal.
func witnessTranscript(t *testing.T, scenario parityScenario, trace parityTrace) {
	t.Helper()

	developed := 0
	verdicts := 0
	repairs := 0
	replays := 0
	commanded := 0
	previous := ""
	for _, step := range scenario.steps {
		switch step.state {
		case parityDevelop:
			developed++
			if previous == parityCheck || previous == parityReview {
				repairs++
			}
		case parityReview:
			// A review state that was entered and produced no verdict bought
			// nothing: the run stopped at that boundary before the reviewer was
			// asked, which is what the operator-stop path does.
			if step.outcome != "stopped" {
				verdicts++
			}
		case parityCheck:
			// A change refused for what it touched is refused in front of the
			// checks, so that round ran no commands at all.
			if !strings.HasPrefix(step.outcome, "refused") {
				commanded++
			}
		case parityIntegrate:
			if step.outcome == "superseded" {
				replays++
			}
		}
		previous = step.state
	}
	// A step the process died inside performed its action and never recorded an
	// outcome, so what it did is evidenced in the trace and absent from the tally
	// above.
	if scenario.standing == parityCheck {
		commanded++
	}

	if want := trace.invocations("developer"); developed != want {
		t.Errorf("the transcript stands in %s %d time(s) and the trace records %d developer invocation(s)", parityDevelop, developed, want)
	}
	if want := trace.invocations("reviewer"); verdicts != want {
		t.Errorf("the transcript obtains %d verdict(s) and the trace records %d reviewer invocation(s)", verdicts, want)
	}
	if want := trace.counter("review_rounds"); verdicts != want {
		t.Errorf("the transcript obtains %d verdict(s) and the run recorded review_rounds = %d", verdicts, want)
	}
	if want := trace.counter("repair_attempts"); repairs != want {
		t.Errorf("the transcript returns to %s from the gate %d time(s) and the run recorded repair_attempts = %d", parityDevelop, repairs, want)
	}
	if want := trace.counter("integration_retries"); replays != want {
		t.Errorf("the transcript replays the promotion %d time(s) and the run recorded integration_retries = %d", replays, want)
	}
	if want := trace.events("command.started"); commanded != want {
		t.Errorf("the transcript runs the project's commands in %d %s state(s) and the log records %d command.started event(s)", commanded, parityCheck, want)
	}
	witnessEnding(t, scenario, trace)
}

// witnessEnding holds where a transcript ends against what became of the work
// item. The three terminals are three different answers to "what happened to my
// work", and a transcript ending in the wrong one would be a definition that
// reads a blocked run as a delivered one.
func witnessEnding(t *testing.T, scenario parityScenario, trace parityTrace) {
	t.Helper()
	switch {
	case scenario.terminal == "":
		if len(trace.Reconciliations) == 0 {
			t.Errorf("the transcript is left standing in %q and the trace records no settlement; a run nothing finished is settled by a sweep", scenario.standing)
		}
	case scenario.terminal == "delivered":
		if !trace.WorkItem.Closed {
			t.Errorf("the transcript ends in %q and the trace does not record the item closed", scenario.terminal)
		}
	case scenario.terminal == "blocked":
		if !trace.WorkItem.Blocked {
			t.Errorf("the transcript ends in %q and the trace does not record the item blocked", scenario.terminal)
		}
	case scenario.terminal == "preserved":
		if trace.WorkItem.Closed || trace.WorkItem.Blocked {
			t.Errorf("the transcript ends in %q and the trace records the item closed=%t blocked=%t; a change preserved for its approver is neither",
				scenario.terminal, trace.WorkItem.Closed, trace.WorkItem.Blocked)
		}
		if ending := trace.ending(); ending != "succeeded" {
			t.Errorf("the transcript ends in %q and the run was read as %q", scenario.terminal, ending)
		}
	case scenario.terminal == "abandoned":
		if trace.WorkItem.Closed || trace.WorkItem.Blocked {
			t.Errorf("the transcript ends in %q and the trace records the item closed=%t blocked=%t; an abandoned run leaves neither",
				scenario.terminal, trace.WorkItem.Closed, trace.WorkItem.Blocked)
		}
		if ending := trace.ending(); ending == "succeeded" {
			t.Errorf("the transcript ends in %q and the run was read as %q", scenario.terminal, ending)
		}
	default:
		t.Errorf("the transcript ends in %q, which no built-in definition declares", scenario.terminal)
	}
}

//
// The walk.
//

// walkTranscript steps a transcript through the definition that claims to
// express it, and reads the walk back off the durable record.
//
// The record rather than the return value is deliberate: what a resumed process
// would see is the checkpoints, so comparing those is comparing what the
// executor durably said happened rather than what this test watched it do.
func walkTranscript(t *testing.T, scenario parityScenario) {
	t.Helper()

	graph := parityGraph(t, scenario.workflow)
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	grant, err := deliveryGrant()
	if err != nil {
		t.Fatalf("deliveryGrant() error = %v", err)
	}
	stepped := 0
	executor := workflow.Executor[*activeRun]{
		Graph:     graph,
		Instances: store,
		Grant:     grant,
		Outcome: func(state string, _ *activeRun) (string, error) {
			if stepped >= len(scenario.steps) {
				return "", fmt.Errorf("the definition performed %d states and the transcript has %d", stepped+1, len(scenario.steps))
			}
			step := scenario.steps[stepped]
			stepped++
			if state != step.state {
				return "", fmt.Errorf("step %d of the transcript stands in %q and the definition performed %q", stepped, step.state, state)
			}
			return step.outcome, nil
		},
		Now: func() time.Time { return baseTime },
	}

	instance, err := executor.Start(scenario.trace)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if instance.State != parityClaim {
		t.Fatalf("an instance starts in %q; every delivery definition begins at %q", instance.State, parityClaim)
	}
	// The subject is nil because nothing is performed: the doors this graph holds
	// are the registered ones with their Perform replaced, so the walk is over the
	// real topology and touches nothing outside this test.
	for range scenario.steps {
		instance, err = executor.Step(context.Background(), scenario.trace, nil)
		if err != nil {
			t.Fatalf("Step() error = %v", err)
		}
	}
	if stepped != len(scenario.steps) {
		t.Errorf("the definition performed %d states and the transcript has %d", stepped, len(scenario.steps))
	}

	// The checkpoints are the initial state and then one per transition, so the
	// transcript's step i is the crossing recorded at i+1.
	if want := len(scenario.steps) + 1; len(instance.Checkpoints) != want {
		t.Fatalf("the instance recorded %d checkpoint(s) and the transcript is %d transition(s)", len(instance.Checkpoints), want-1)
	}
	for index, step := range scenario.steps {
		crossing := instance.Checkpoints[index+1]
		if crossing.From != step.state || crossing.Outcome != step.outcome {
			t.Errorf("transition %d was recorded as %q on %q and the transcript is %q on %q",
				index+1, crossing.From, crossing.Outcome, step.state, step.outcome)
		}
	}
	switch {
	case scenario.terminal != "":
		if !instance.Terminal || instance.State != scenario.terminal {
			t.Errorf("the walk ended in %q (terminal=%t) and the transcript ends in %q", instance.State, instance.Terminal, scenario.terminal)
		}
	default:
		if instance.Terminal || instance.State != scenario.standing {
			t.Errorf("the walk ended in %q (terminal=%t) and the transcript leaves the instance standing in %q", instance.State, instance.Terminal, scenario.standing)
		}
	}
}

// parityGraph compiles one built-in definition into a graph whose doors perform
// nothing.
//
// Everything else about the actions is the registered thing: the names, the
// summaries, the capabilities and what each wraps come from the same table
// `actions.go` builds the delivery registry from, so the topology walked here is
// the topology the production registry would compile and the separation policies
// are asked the same question. Only Perform is replaced, which is what makes it
// safe to walk a promotion in a unit test.
func parityGraph(t *testing.T, id string) workflow.Graph[*activeRun] {
	t.Helper()

	builtin, found := builtinDelivery{}, false
	for _, shipped := range builtinDeliveryWorkflows() {
		if shipped.ID == id {
			builtin, found = shipped, true
		}
	}
	if !found {
		t.Fatalf("no built-in definition is shipped as %q", id)
	}
	// The graph compiled against the real registry is what the definition means;
	// this one has to be the same definition, and the digest is what says so.
	compiled, err := compileDelivery(builtin)
	if err != nil {
		t.Fatalf("compileDelivery(%s) error = %v", builtin.Source, err)
	}

	steps := deliverySteps()
	inert := make([]action.Action[*activeRun], 0, len(steps))
	for _, step := range steps {
		door := step.action
		door.Perform = func(context.Context, *activeRun) error { return nil }
		inert = append(inert, door)
	}
	registry, err := action.New(inert...)
	if err != nil {
		t.Fatalf("action.New() error = %v", err)
	}
	grant, err := deliveryGrant()
	if err != nil {
		t.Fatalf("deliveryGrant() error = %v", err)
	}
	loader := workflow.Loader[*activeRun]{Registry: registry, Grant: grant}
	graph, err := loader.Load(strings.NewReader(string(builtin.Definition)))
	if err != nil {
		t.Fatalf("Load(%s) error = %v", builtin.Source, err)
	}
	if graph.Digest() != compiled.Digest() {
		t.Fatalf("the walked graph digests to %s and the shipped definition to %s; they are not the same sequence", graph.Digest(), compiled.Digest())
	}
	return graph
}
