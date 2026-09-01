package conformance

// The release-readiness sequence as the harness runs it: the checks registered
// as actions, the authority the gate holds, the definition that orders them, and
// the one call that walks it.
//
// Everything below the registry is deliberately thin. The definition decides the
// order and where each answer goes; this file decides nothing about the sequence
// and could not — an action it does not register cannot be selected, and an
// action requiring authority the grant does not confer is refused before an
// instance exists. That is the trade the workflow runtime makes, and this is the
// first place in the repository where a real sequence takes it.

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/action"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/workflow"
)

// WorkflowID is the workflow this package runs, by the name an instance records
// it under. A definition declaring another id is refused rather than run: the
// gate names what it is running, and a file answering to something else is not
// that thing however well formed it is.
const WorkflowID = "release-readiness"

// ProjectDefinitionPath is where a project's own copy of the definition lives,
// relative to its Yoyodyne configuration directory. A project that wants a
// different sequence copies the built-in file there and edits it; nothing is
// merged, so the copy is the whole definition and what runs it says which of the
// two it read.
const ProjectDefinitionPath = "workflows/" + WorkflowID + ".yaml"

// The terminals the definition may end in. They are constants because what a
// caller does with the answer turns on which one was reached, and a terminal
// renamed in the file without being renamed here would be a gate that stopped
// refusing.
const (
	TerminalReady    = "ready"
	TerminalMismatch = "mismatch"
)

// BuiltinDefinition is the sequence this build ships, in the same format a
// project's own copy is written in. It is embedded rather than read from disk so
// that a checkout is not a prerequisite for running the gate, and it is the same
// bytes that are read when a project has no copy of its own.
//
//go:embed release-readiness.yaml
var BuiltinDefinition []byte

// BuiltinDefinitionSource is what a report calls the definition when it came
// from the executable rather than from a file somebody can open.
const BuiltinDefinitionSource = "built in"

// Registry is the checks this build registers, each a second door onto the
// function that performs it and each declaring what performing it requires.
//
// The capabilities are the two a read-only gate needs and nothing else, and that
// is load-bearing rather than bookkeeping: a definition that tried to sequence a
// step which writes anything would be selecting an action this registry does not
// hold, and one that selected a heavier action would be refused against the
// grant below. The gate cannot be made to write by editing a file.
func Registry() (action.Registry[*Assessment], error) {
	return action.New(registered()...)
}

// registered is the table Registry is built from, with each Perform one call to
// the check it wraps and nothing in between.
func registered() []action.Action[*Assessment] {
	return []action.Action[*Assessment]{
		{
			Name:         "conformance.artifacts",
			Summary:      "read the canonical artifacts and hold their relationships and their revisions to what the chain requires",
			Wraps:        "(*Assessment).checkArtifacts",
			Capabilities: []capability.Capability{capability.RepositoryRead},
			Perform:      func(_ context.Context, a *Assessment) error { return a.checkArtifacts() },
		},
		{
			Name:         "conformance.references",
			Summary:      "resolve every link the repository's documentation makes to itself, both the path and the heading it names",
			Wraps:        "(*Assessment).checkReferences",
			Capabilities: []capability.Capability{capability.RepositoryRead},
			Perform:      func(_ context.Context, a *Assessment) error { return a.checkReferences() },
		},
		{
			Name:         "conformance.invariants",
			Summary:      "read the architectural invariants the way a run is delivered them, and report whatever could not be read as one",
			Wraps:        "(*Assessment).checkInvariants",
			Capabilities: []capability.Capability{capability.RepositoryRead},
			Perform:      func(_ context.Context, a *Assessment) error { return a.checkInvariants() },
		},
		{
			Name:    "conformance.goals",
			Summary: "hold every admitted work item's attribution against the goals the repository records",
			Wraps:   "(*Assessment).checkGoals",
			// The tracker read is declared even though the items were read before the
			// sequence started. What this step judges is the tracker's own record of
			// what is admitted, and an action understating the authority its work
			// depends on is the one thing the registry exists not to do.
			Capabilities: []capability.Capability{capability.RepositoryRead, capability.WorkItemRead},
			Perform:      func(_ context.Context, a *Assessment) error { return a.checkGoals() },
		},
		{
			Name:         "conformance.staleness",
			Summary:      "survey what a change to an upstream document left unanswered downstream, refusing nothing",
			Wraps:        "(*Assessment).surveyStaleness",
			Capabilities: []capability.Capability{capability.RepositoryRead, capability.WorkItemRead},
			Perform:      func(_ context.Context, a *Assessment) error { return a.surveyStaleness() },
		},
	}
}

// answersWith is what each registered check can produce, in sorted order.
//
// A registered action declares its capabilities and not yet its outcomes, so the
// workflow package cannot ask whether a definition handles the outcomes the
// action it selected can return — its own Validate says so. This package can:
// the checks are its own, and it knows that four of them answer conforms or
// diverges and that the staleness survey answers only noted. Holding a
// definition to it at compile is what keeps "a definition that is wrong is
// refused whole, before a check runs" true rather than nearly true — the case it
// closes is a definition that routes the staleness survey on `diverges`, which
// would otherwise compile, run four checks, and die on the fifth.
var answersWith = map[string][]string{
	"conformance.artifacts":  {OutcomeConforms, OutcomeDiverges},
	"conformance.references": {OutcomeConforms, OutcomeDiverges},
	"conformance.invariants": {OutcomeConforms, OutcomeDiverges},
	"conformance.goals":      {OutcomeConforms, OutcomeDiverges},
	"conformance.staleness":  {OutcomeNoted},
}

// Grant is the authority a release-readiness workflow is compiled and performed
// under: reading the repository, and reading the work tracker.
//
// It is stated here rather than taken from whatever the process happens to hold,
// because a gate is exactly the place where a widening nobody noticed would go
// unnoticed. A definition selecting anything heavier is refused at compile,
// before an instance is recorded and before a check runs.
func Grant() (workflow.Grant, error) {
	return workflow.NewGrant(capability.RepositoryRead, capability.WorkItemRead)
}

// Definition is a compiled release-readiness sequence and where it was read
// from. The two travel together because a result has to say which sequence
// produced it: this build's and a project's own copy are different claims about
// what was checked.
type Definition struct {
	Graph workflow.Graph[*Assessment]
	// Source is the path the definition was read from, or BuiltinDefinitionSource.
	Source string
}

// Compile reads a definition and produces the sequence the gate runs, or refuses
// the whole of it.
//
// An empty path is the definition this build ships. Anything else is a project's
// own copy, read from the file it names and refused naming that file — which is
// the point of validating before running: a project that mis-edits its sequence
// is told so, instead of finding out from a release that checked four things
// where it meant to check five.
func Compile(path string) (Definition, error) {
	registry, err := Registry()
	if err != nil {
		// The registry is a literal table in this package, so this is a defect in
		// the build rather than anything wrong with the file being read.
		return Definition{}, fmt.Errorf("the checks this build registers are not a registry: %w", err)
	}
	grant, err := Grant()
	if err != nil {
		return Definition{}, fmt.Errorf("the authority the release-readiness gate holds could not be stated: %w", err)
	}
	loader := workflow.Loader[*Assessment]{Registry: registry, Grant: grant}

	definition := Definition{Source: definitionSource(path)}
	if path == "" {
		definition.Graph, err = loader.Load(bytes.NewReader(BuiltinDefinition))
	} else {
		definition.Graph, err = loader.LoadFile(path)
	}
	if err != nil {
		return Definition{}, err
	}
	// A definition that validated and compiled and answers to another name is not
	// this gate's. Nothing downstream would notice: it would run whatever it said
	// perfectly well, under this grant, and report a conformance result for a
	// sequence nobody asked for.
	if definition.Graph.ID() != WorkflowID {
		return Definition{}, fmt.Errorf("%s declares the workflow %q; the release-readiness gate runs %q", definition.Source, definition.Graph.ID(), WorkflowID)
	}
	// The gate decides from the terminal an instance ended in, so a definition
	// that cannot reach one of them is one whose answer this could not read.
	for _, terminal := range []string{TerminalReady, TerminalMismatch} {
		if !slices.Contains(definition.Graph.Terminals(), terminal) {
			return Definition{}, fmt.Errorf("%s declares no %q terminal; the gate reads its answer from the terminal an instance ends in", definition.Source, terminal)
		}
	}
	if err := handlesEveryOutcome(definition); err != nil {
		return Definition{}, err
	}
	return definition, nil
}

// handlesEveryOutcome refuses a definition whose states do not route exactly the
// outcomes the checks they selected can produce.
//
// Everything wrong is reported together, for the reason the workflow package
// reports its own problems together: a definition is written by hand, and
// answering one question per reload is how somebody gives up on a format. The
// walk is over the graph's sorted states, so one definition refused twice is
// refused identically.
func handlesEveryOutcome(definition Definition) error {
	var problems []error
	for _, state := range definition.Graph.States() {
		node, _ := definition.Graph.Node(state)
		answers, declared := answersWith[node.Action().Name]
		if !declared {
			// The registry and the table above are both literals in this package, so
			// this is a defect in the build rather than in the file being read.
			problems = append(problems, fmt.Errorf("the state %q selects %q, whose outcomes this build does not declare", state, node.Action().Name))
			continue
		}
		if handled := node.Outcomes(); !slices.Equal(handled, answers) {
			problems = append(problems, fmt.Errorf("the state %q selects %q, which answers with %s, and handles %s; every outcome a check can produce needs somewhere to go and an outcome it never produces is a transition nothing takes",
				state, node.Action().Name, strings.Join(answers, " or "), strings.Join(handled, " and ")))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s: %w", definition.Source, errors.Join(problems...))
	}
	return nil
}

// Result is one assessment, run: which definition was walked, where it ended,
// and everything the checks found on the way.
//
// It is what both the operator's report and the release's notes are rendered
// from, so what somebody reads on the terminal during a cut and what ships in
// the version's notes are one answer rather than two renderings of two.
type Result struct {
	// Workflow, Schema and Digest say what was run and which version of it. The
	// digest is the whole hash rather than a readable prefix, because it is the
	// pin an instance records and a truncation would be a claim nobody can check.
	Workflow string `json:"workflow"`
	Schema   int    `json:"schema"`
	Digest   string `json:"digest"`
	// Definition is where the sequence was read from: a path, or that it was
	// built in.
	Definition string `json:"definition"`
	// Instance is the durable record of this walk, under the harness's own state
	// root, so a result printed once can be read again from where it was written.
	Instance string `json:"instance"`
	// Terminal is where the instance ended, and Conforms is that terminal read as
	// the answer: the gate decides from where the sequence arrived rather than
	// from a tally kept beside it.
	Terminal string    `json:"terminal"`
	Conforms bool      `json:"conforms"`
	Findings []Finding `json:"findings"`
	// At is when the assessment finished, which is what a release's notes date
	// the result to.
	At time.Time `json:"at"`
}

// Mismatches is every mismatch every check found, each named with the step that
// found it. It is what a refusal reports.
func (r Result) Mismatches() []string {
	var named []string
	for _, finding := range r.Findings {
		for _, mismatch := range finding.Mismatches {
			named = append(named, finding.Step+": "+mismatch)
		}
	}
	return named
}

// Assess walks the compiled sequence over gathered sources and reports what it
// found.
//
// The instance is recorded before the first check and every transition is
// recorded as it is crossed, which is the runtime's guarantee rather than
// anything this adds: a cut killed halfway through the gate leaves a record
// naming the state boundary it reached rather than the middle of a check. Every
// check is a read, so re-performing one after a death costs a repeat read and
// nothing else — which is why this sequence is one the executor can run today
// while the delivery loop, whose actions have side effects, still waits for the
// step-attempt record the design describes.
func Assess(ctx context.Context, definition Definition, instances *runstate.Store, sources Sources, now func() time.Time) (Result, error) {
	if now == nil {
		now = time.Now
	}
	grant, err := Grant()
	if err != nil {
		return Result{}, err
	}
	executor := workflow.Executor[*Assessment]{
		Graph:     definition.Graph,
		Instances: instances,
		Grant:     grant,
		Outcome:   Outcome,
		Now:       func() time.Time { return now().UTC() },
	}
	instanceID := InstanceID(now())
	if _, err := executor.Start(instanceID); err != nil {
		return Result{}, err
	}
	assessment := New(sources)
	instance, err := executor.Run(ctx, instanceID, assessment)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Workflow:   definition.Graph.ID(),
		Schema:     definition.Graph.Schema(),
		Digest:     definition.Graph.Digest(),
		Definition: definition.Source,
		Instance:   instanceID,
		Terminal:   instance.State,
		Conforms:   instance.State == TerminalReady,
		Findings:   assessment.Findings(),
		At:         now().UTC(),
	}, nil
}

// InstanceID is what one assessment is recorded under: the workflow's name and
// the instant it started, to the nanosecond.
//
// Instances are never resumed here — a gate that ran half a release ago is a
// result rather than something to continue — so what the identifier has to do is
// be new every time and be legible in a directory listing afterwards. The
// nanosecond is what makes two cuts within one second two records rather than a
// refusal.
func InstanceID(at time.Time) string {
	moment := at.UTC()
	return fmt.Sprintf("%s-%s-%09d", WorkflowID, moment.Format("20060102t150405"), moment.Nanosecond())
}

// definitionSource is how a report and a refusal name the definition, so a
// message read without the command in front of somebody still says which file to
// open.
func definitionSource(path string) string {
	if path == "" {
		return BuiltinDefinitionSource
	}
	return path
}
