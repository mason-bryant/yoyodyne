package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DefaultBound is how many transitions one Run performs before it stops and says
// so. A definition may loop — a review asking for changes goes back to the
// developer, and nothing in the schema says how often — so a runtime with no
// bound is one a definition can hang. The design's answer is budgets a cycle has
// to cross, which is a later milestone; until then this is the backstop, set far
// above any sequence somebody would write on purpose.
const DefaultBound = 1000

// Executor runs one instance of one compiled graph, one durable transition at a
// time.
//
// Every step is the same five things in the same order: read the instance's
// durable record, refuse it if the graph in hand is not the definition it pinned,
// check the authority the action requires against the grant this executor
// performs under, check that the record can still hold the boundary this step
// would produce, and perform it — then record that boundary before anything else
// happens. So the record is never ahead of what was performed, nothing is
// performed that could not then be recorded, and a process that dies leaves a
// record naming a state boundary rather than the middle of a step.
//
// What that costs, and it is worth being plain about it: an action that was
// performed and whose outcome was not yet recorded is performed again by whatever
// resumes the instance. Making that safe for an action with a side effect is the
// step-attempt record, the idempotency key, and the reconciliation the design
// describes, and none of the three is here — which is why nothing in the delivery
// path performs anything through this yet. What does step it over a real run is
// the orchestrator's declarative path, which every run takes by default, and it
// steps a graph whose doors perform nothing, precisely because re-performing one
// is what is not yet safe; the graphs this package is exercised against are
// otherwise fixtures.
//
// It holds no state of its own. Two executors over one store and one graph are
// interchangeable, which is what makes resuming an instance in a second process
// the same operation as stepping it in the first.
type Executor[S any] struct {
	// Graph is the compiled workflow being run. An instance is pinned to its
	// digest at creation, and every later step is refused unless the graph
	// presented digests the same.
	Graph Graph[S]
	// Instances is where the durable records live: the harness's own state store,
	// which is where every other durable record it keeps is written.
	Instances *runstate.Store
	// Grant is the authority this executor performs under. It is checked at every
	// state boundary rather than once at the start, because authority is a fact
	// about the thing performing the work now and not a property the instance
	// carries: an executor whose grant was narrowed refuses the next step it
	// cannot cover, and the instance stays exactly where it was.
	Grant Grant
	// Outcome reads what performing a state produced, so the definition can say
	// where that outcome goes.
	//
	// It is supplied by the caller because a registered action returns an error
	// and not yet an outcome: the action descriptor that declares its outcomes is
	// a later milestone, and inventing an outcome here would be this package
	// deciding something the registry is meant to. An outcome the state's
	// transitions do not handle stops the instance where it is.
	Outcome func(state string, subject S) (string, error)
	// Now is where checkpoint timestamps come from. The zero value is the wall
	// clock in UTC.
	Now func() time.Time
	// Gates reads whether a person has recorded the act a gated state waits for.
	// It is satisfied by *runstate.Store.
	//
	// It is a reader and never a writer, which is the whole shape of the thing: an
	// executor can find out that a person acted and has no way to make it true. So
	// no outcome of any action passes a gate, no definition passes one by routing
	// around it, and an instance standing at a gated state stands there until
	// somebody records the act — which is what a gate is for, and what an
	// item-closed dependency could never be.
	//
	// A gated graph with no reader wired is refused before anything runs; see
	// ready.
	Gates HumanGates
	// Bound is how many transitions one Run performs before it refuses to go on.
	// The zero value is DefaultBound.
	Bound int
}

// HumanGates is the recorded human acts, as the executor reads them. Only the
// question is here: whether the act that passes one gate is on the record. There
// is deliberately no way to answer it from in here.
type HumanGates interface {
	HumanActRecorded(subject, gate string) (bool, error)
}

// Start creates the durable record of a new instance, standing on the graph's
// initial state and pinned to its digest.
//
// Nothing is performed: an instance exists before its first action, so that the
// record of what is about to run is durable before anything runs. Creating a
// second instance under one identifier is refused.
func (e Executor[S]) Start(id string) (runstate.WorkflowInstance, error) {
	if err := e.ready(); err != nil {
		return runstate.WorkflowInstance{}, err
	}
	instance := runstate.WorkflowInstance{
		SchemaVersion:    runstate.WorkflowInstanceSchemaVersion,
		InstanceID:       id,
		WorkflowID:       e.Graph.ID(),
		DefinitionSchema: e.Graph.Schema(),
		Digest:           e.Graph.Digest(),
		State:            e.Graph.Initial(),
		Checkpoints: []runstate.WorkflowCheckpoint{{
			Sequence: 0,
			State:    e.Graph.Initial(),
			At:       e.now(),
		}},
	}
	if err := e.Instances.CreateWorkflowInstance(instance); err != nil {
		return runstate.WorkflowInstance{}, err
	}
	return instance, nil
}

// Resume is the instance this executor is entitled to act on: the durable record
// under an identifier, refused when it is pinned to a definition this executor
// does not hold.
//
// The refusal is the pin doing its work at the other end from the loader's. A
// loader refuses to compile a file that changed under a caller already running
// it; this refuses to step an instance with the graph a changed file compiled to.
// Either way the instance keeps running the definition it started on, or nothing
// runs it at all — what does not happen is the instance quietly continuing under
// a definition somebody edited while it was in flight.
func (e Executor[S]) Resume(id string) (runstate.WorkflowInstance, error) {
	if err := e.ready(); err != nil {
		return runstate.WorkflowInstance{}, err
	}
	instance, err := e.Instances.LoadWorkflowInstance(id)
	if err != nil {
		return runstate.WorkflowInstance{}, err
	}
	if instance.Digest != e.Graph.Digest() {
		return runstate.WorkflowInstance{}, fmt.Errorf("workflow instance %s is pinned to %s and this executor holds %s; the definition changed under an instance already running it, and an instance in flight is never migrated", id, instance.Digest, e.Graph.Digest())
	}
	if instance.WorkflowID != e.Graph.ID() {
		return runstate.WorkflowInstance{}, fmt.Errorf("workflow instance %s is running %q and this executor holds %q", id, instance.WorkflowID, e.Graph.ID())
	}
	return instance, nil
}

// Step performs the action of the state an instance stands in and records the
// boundary it arrives at.
//
// Everything that can refuse, refuses before the action is performed, and every
// refusal leaves the instance exactly where it was — so a caller that fixes what
// was refused steps the same state again rather than finding the instance
// somewhere it never durably stood.
func (e Executor[S]) Step(ctx context.Context, id string, subject S) (runstate.WorkflowInstance, error) {
	instance, err := e.Resume(id)
	if err != nil {
		return runstate.WorkflowInstance{}, err
	}
	if instance.Terminal {
		return runstate.WorkflowInstance{}, fmt.Errorf("workflow instance %s ended in %q; a terminal is where an instance stops rather than a state with a step in it", id, instance.State)
	}
	node, isAState := e.Graph.Node(instance.State)
	if !isAState {
		// The graph was compiled from the definition this instance pinned, so a
		// state the record names and the graph does not hold is a defect in this
		// build rather than in the record.
		return runstate.WorkflowInstance{}, fmt.Errorf("workflow instance %s stands in %q, which the definition it is pinned to does not declare", id, instance.State)
	}
	if err := withinGrant(e.Grant, instance.State, node); err != nil {
		return runstate.WorkflowInstance{}, fmt.Errorf("workflow instance %s: %w", id, err)
	}
	// The gate is asked after the authority and before anything else, because it
	// is the same kind of question and has the same answer when it refuses: the
	// instance stays exactly where it is, and whoever fixes what was refused steps
	// this state again. What differs is who can fix it — an authority refusal
	// waits on a wider grant, and this waits on a person.
	if err := gateOpen(e.Gates, id, instance.State, node); err != nil {
		return runstate.WorkflowInstance{}, fmt.Errorf("workflow instance %s: %w", id, err)
	}
	// Whether the boundary this step would produce can be recorded is asked here,
	// before the action, because the answer stops being useful afterwards: an
	// action performed and not recordable is an instance nothing can move on.
	if err := instance.RoomForAnotherCheckpoint(widestCheckpoint(instance, node, e.now())); err != nil {
		return runstate.WorkflowInstance{}, err
	}
	if err := node.Action().Perform(ctx, subject); err != nil {
		return runstate.WorkflowInstance{}, fmt.Errorf("workflow instance %s performing %q in the state %q: %w", id, node.Action().Name, instance.State, err)
	}
	outcome, err := e.Outcome(instance.State, subject)
	if err != nil {
		return runstate.WorkflowInstance{}, fmt.Errorf("workflow instance %s: what %q produced in the state %q could not be read: %w", id, node.Action().Name, instance.State, err)
	}
	destination, handled := node.Next(outcome)
	if !handled {
		return runstate.WorkflowInstance{}, fmt.Errorf("workflow instance %s: the state %q produced the outcome %q, which the definition it is pinned to sends nowhere; it handles %v", id, instance.State, outcome, node.Outcomes())
	}

	instance.Checkpoints = append(instance.Checkpoints, runstate.WorkflowCheckpoint{
		Sequence: len(instance.Checkpoints),
		State:    destination.Name,
		Terminal: destination.Terminal,
		From:     instance.State,
		Outcome:  outcome,
		At:       e.now(),
	})
	instance.State = destination.Name
	instance.Terminal = destination.Terminal
	if err := e.Instances.SaveWorkflowInstance(instance); err != nil {
		return runstate.WorkflowInstance{}, err
	}
	return instance, nil
}

// Run steps an instance until it reaches a terminal, or until something refuses.
//
// It is a loop over Step and nothing more: every transition it makes is as
// durable as one made a step at a time, so a process running this and a process
// stepping by hand leave records a third process resumes identically.
func (e Executor[S]) Run(ctx context.Context, id string, subject S) (runstate.WorkflowInstance, error) {
	instance, err := e.Resume(id)
	if err != nil {
		return runstate.WorkflowInstance{}, err
	}
	bound := e.Bound
	if bound <= 0 {
		bound = DefaultBound
	}
	for transitions := 0; !instance.Terminal; transitions++ {
		if transitions == bound {
			return runstate.WorkflowInstance{}, fmt.Errorf("workflow instance %s made %d transitions in this run without reaching a terminal and stands in %q; it is looping, and the run stops rather than going round again", id, bound, instance.State)
		}
		if err := ctx.Err(); err != nil {
			return runstate.WorkflowInstance{}, fmt.Errorf("workflow instance %s stopped in %q: %w", id, instance.State, err)
		}
		instance, err = e.Step(ctx, id, subject)
		if err != nil {
			return runstate.WorkflowInstance{}, err
		}
	}
	return instance, nil
}

// ready refuses an executor that could not run anything, once, rather than
// leaving each of the four to be discovered by whichever step first needed it.
func (e Executor[S]) ready() error {
	var problems []error
	if e.Graph.digest == "" {
		problems = append(problems, errors.New("the executor holds no compiled graph; an instance runs a definition that was validated and compiled"))
	}
	if e.Instances == nil {
		problems = append(problems, errors.New("the executor has nowhere to record instances; a transition nothing durably records is not a checkpoint"))
	}
	if e.Outcome == nil {
		problems = append(problems, errors.New("the executor cannot read what an action produced; a transition is chosen by an outcome"))
	}
	// The same fail-closed default the compiler has: a caller that did not say
	// what authority it holds has not said "everything".
	if len(e.Grant.granted) == 0 {
		problems = append(problems, errors.New("the grant the executor performs under confers no capabilities; a workflow is performed under the authority its actions require"))
	}
	// A graph with a gate in it and nothing to read the record with would stop at
	// the first gated state and say so there. Saying it here instead costs the
	// caller nothing and is the difference between an instance that never starts
	// and one that is created, pinned, and stuck.
	if gated := e.Graph.Gates(); len(gated) > 0 && e.Gates == nil {
		problems = append(problems, fmt.Errorf("this workflow waits on the human gates %v and the executor has no way to read whether anybody has passed them", gated))
	}
	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	return nil
}

func (e Executor[S]) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now().UTC()
}

// withinGrant is the authority check at execution: everything the action a state
// performs requires, held against the grant the executor performs under.
//
// What it reads is the registry's declaration, carried on the compiled node, and
// never anything the definition said — a definition selects which action runs and
// has no way to state what performing it needs, so the authority checked here is
// the one the Go table declares whatever file selected the action. It is the same
// refusal the compiler makes and it is deliberately made twice: compiling answers
// for the whole graph under the grant it was compiled with, and this answers for
// the one step about to happen under the grant held now, which is what keeps a
// narrowed authority from being carried by an instance that started under a wider
// one.
// gateOpen is the human gate check at execution: a gated state performs nothing
// until a person's act against that gate is on the record.
//
// It refuses rather than waits. An instance standing at a gated state is not
// blocked on a clock or on another process, so there is nothing to sleep for and
// nothing that would wake it — what changes is somebody typing `yoyo gate
// record`, and until they do, the honest thing for a step to say is that it is
// waiting on them. A caller stepping the state again after that finds the gate
// open.
//
// A reader that cannot answer is a refusal too, not a pass. A gate whose record
// could not be read is a gate nobody has shown was passed, and treating an
// unreadable answer as an open gate is exactly the shape of the failure gates
// exist to end.
func gateOpen[S any](gates HumanGates, instanceID, state string, node Node[S]) error {
	gate := node.Gate()
	if gate == "" {
		return nil
	}
	if gates == nil {
		return fmt.Errorf("the state %q waits on the human gate %q and this executor has no way to read whether anybody has passed it; a gate nothing can read is never treated as open", state, gate)
	}
	recorded, err := gates.HumanActRecorded(instanceID, gate)
	if err != nil {
		return fmt.Errorf("the state %q waits on the human gate %q and whether it was passed could not be read: %w", state, gate, err)
	}
	if !recorded {
		return fmt.Errorf("the state %q waits on the human gate %q, which no recorded act has passed on this instance; nothing machinery does passes it, closing a work item included, and `yoyo gate record %s --for %s` is what does", state, gate, gate, instanceID)
	}
	return nil
}

func withinGrant[S any](grant Grant, state string, node Node[S]) error {
	var problems []error
	for _, needed := range node.Action().Capabilities {
		if !grant.confers(needed) {
			problems = append(problems, fmt.Errorf("the state %q performs %q, which requires %q; it is performed under %s and nothing the definition says widens that", state, node.Action().Name, needed, grantedList(grant)))
		}
	}
	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	return nil
}

// widestCheckpoint is the largest checkpoint the state about to be performed
// could produce: the longest outcome it handles, arriving at the longest
// destination it leads to, and terminal because a terminal boundary is written
// with one field more than an ordinary one.
//
// It is a worst case rather than the real thing because the real thing is not
// known until the action has been performed, which is after the point where
// knowing it is any use. A step that fits the widest boundary fits whichever one
// it actually produces. That holds for the time as well as for the names: a
// checkpoint's timestamp is written to a fixed width, so the instant recorded
// after the action costs exactly what the one measured here does.
func widestCheckpoint[S any](instance runstate.WorkflowInstance, node Node[S], at time.Time) runstate.WorkflowCheckpoint {
	widest := runstate.WorkflowCheckpoint{
		Sequence: len(instance.Checkpoints),
		From:     instance.State,
		Terminal: true,
		At:       at,
	}
	for _, outcome := range node.Outcomes() {
		if len(outcome) > len(widest.Outcome) {
			widest.Outcome = outcome
		}
		destination, _ := node.Next(outcome)
		if len(destination.Name) > len(widest.State) {
			widest.State = destination.Name
		}
	}
	return widest
}
