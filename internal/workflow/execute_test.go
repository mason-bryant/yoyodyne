package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/capability"
)

// deliveryOutcomes is what each state of the delivery fixture produces in these
// tests: the path a run takes when nothing goes wrong. A test about something
// going wrong scripts its own.
var deliveryOutcomes = map[string]string{
	"claim":     "claimed",
	"develop":   "produced",
	"publish":   "published",
	"check":     "passed",
	"review":    "approved",
	"integrate": "integrated",
	"clean-up":  "cleaned",
}

// deliveredPath is where those outcomes take an instance, in order: the fixture's
// seven states and the terminal they end in.
var deliveredPath = []string{"claim", "develop", "publish", "check", "review", "integrate", "clean-up", "delivered"}

// journal is what the actions in these tests act on: a file every performed
// action is appended to.
//
// It is a file rather than a field because what these tests measure has to
// outlive the process that measured it. An instance whose process is killed
// halfway is resumed by another one, and "the second process carried on from
// exactly where the first stopped" is a sentence about two processes' work in one
// order — which nothing held in either of their memories can say.
type journal struct {
	// path is the file performed actions are appended to.
	path string
	// dieAt is the action this process is killed while performing, and is empty in
	// a process nothing kills.
	dieAt string
}

// perform records one action and, in the process that was told to, dies where it
// stands rather than returning.
func (j *journal) perform(name string) error {
	if err := j.record(name); err != nil {
		return err
	}
	if j.dieAt == name {
		j.die()
	}
	return nil
}

func (j *journal) record(name string) error {
	file, err := os.OpenFile(j.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open the journal: %w", err)
	}
	if _, err := file.WriteString(name + "\n"); err != nil {
		return fmt.Errorf("append to the journal: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync the journal: %w", err)
	}
	return file.Close()
}

// die kills this process where it stands. It is a kill rather than an exit
// because an exit unwinds — deferred closes run, buffers flush — and what the
// test is about is a process that got no chance to tidy anything up.
func (j *journal) die() {
	process, err := os.FindProcess(os.Getpid())
	if err == nil {
		_ = process.Kill()
	}
	// The signal arrives asynchronously, so this waits for it rather than
	// returning into the executor and recording a checkpoint the test is about to
	// assert does not exist.
	for waited := 0; waited < 60; waited++ {
		time.Sleep(time.Second)
	}
	panic("the process was told to die while performing " + j.dieAt + " and is still running")
}

// performed is every action recorded in a journal, in the order they were
// performed, across however many processes wrote it.
func performed(t *testing.T, path string) []string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read the journal: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

// journalGraph is the delivery fixture compiled over a subject that records what
// it performed to a file.
func journalGraph(t *testing.T) Graph[*journal] {
	t.Helper()

	registry := deliveryActions(t, func(name string) func(context.Context, *journal) error {
		return func(_ context.Context, acting *journal) error {
			return acting.perform(name)
		}
	})
	grant, err := NewGrant(capability.All()...)
	if err != nil {
		t.Fatalf("NewGrant() error = %v", err)
	}
	graph, err := Loader[*journal]{Registry: registry, Grant: grant}.LoadFile("testdata/delivery.yaml")
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	return graph
}

// checkpointing is an executor over that graph: instances recorded under root,
// and the scripted outcomes above.
func checkpointing(t *testing.T, root string) Executor[*journal] {
	t.Helper()

	store, err := NewInstanceStore(root)
	if err != nil {
		t.Fatalf("NewInstanceStore() error = %v", err)
	}
	grant, err := NewGrant(capability.All()...)
	if err != nil {
		t.Fatalf("NewGrant() error = %v", err)
	}
	return Executor[*journal]{
		Graph:     journalGraph(t),
		Instances: store,
		Grant:     grant,
		Outcome:   scripted(deliveryOutcomes),
	}
}

// scripted reads an outcome out of a table, refusing a state nothing scripts
// rather than inventing one.
func scripted(outcomes map[string]string) func(string, *journal) (string, error) {
	return func(state string, _ *journal) (string, error) {
		outcome, isScripted := outcomes[state]
		if !isScripted {
			return "", fmt.Errorf("nothing scripts what the state %q produces", state)
		}
		return outcome, nil
	}
}

// actionsAlong is the action each state on a path performs, which is what the
// journal of an uninterrupted run holds. It is read off the graph rather than
// written out again, so a fixture whose states change does not need a second
// table changing with it.
func actionsAlong(t *testing.T, graph Graph[*journal], states []string) []string {
	t.Helper()

	performs := make([]string, 0, len(states))
	for _, state := range states {
		node, isAState := graph.Node(state)
		if !isAState {
			continue
		}
		performs = append(performs, node.Action().Name)
	}
	return performs
}

// TestAnInstanceRecordsItsPinItsPositionAndEveryBoundary is the record this whole
// milestone is for: what the instance is running, where it stands, and the whole
// path it took to get there, durable at every boundary rather than at the end.
func TestAnInstanceRecordsItsPinItsPositionAndEveryBoundary(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	executor := checkpointing(t, filepath.Join(root, "instances"))
	logged := filepath.Join(root, "performed")

	started, err := executor.Start("delivery")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if started.Digest != executor.Graph.Digest() {
		t.Errorf("a new instance is pinned to %q, want the graph's %q", started.Digest, executor.Graph.Digest())
	}
	if started.WorkflowID != "delivery" || started.Schema != SchemaVersion {
		t.Errorf("a new instance runs %q under schema %d", started.WorkflowID, started.Schema)
	}
	if started.State != "claim" || started.Done() {
		t.Errorf("a new instance stands in %q (done %t), want the initial state", started.State, started.Done())
	}
	if walked := started.Path(); !slices.Equal(walked, []string{"claim"}) {
		t.Errorf("a new instance has walked %v, want only the state it was created in", walked)
	}
	// Creating an instance performs nothing: the record of what is about to run is
	// durable before anything runs.
	if acted := performed(t, logged); len(acted) != 0 {
		t.Errorf("Start() performed %v", acted)
	}

	finished, err := executor.Run(context.Background(), "delivery", &journal{path: logged})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if finished.State != "delivered" || !finished.Done() {
		t.Errorf("the instance ended in %q (done %t), want the delivered terminal", finished.State, finished.Done())
	}
	if walked := finished.Path(); !slices.Equal(walked, deliveredPath) {
		t.Errorf("Path() = %v, want %v", walked, deliveredPath)
	}
	if acted, want := performed(t, logged), actionsAlong(t, executor.Graph, deliveredPath); !slices.Equal(acted, want) {
		t.Errorf("the run performed %v, want %v", acted, want)
	}

	// What was returned is what was recorded: a caller reading the record and a
	// caller holding the value are reading one instance.
	recorded, err := executor.Instances.Load("delivery")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !slices.Equal(recorded.Path(), finished.Path()) || recorded.State != finished.State {
		t.Errorf("the record walked %v and ended in %q; the run reported %v and %q", recorded.Path(), recorded.State, finished.Path(), finished.State)
	}
	// Each checkpoint says what brought the instance there, so the history reads as
	// the transitions that happened rather than as a list of places.
	for index, checkpoint := range recorded.Checkpoints {
		if index == 0 {
			continue
		}
		from := deliveredPath[index-1]
		if checkpoint.From != from || checkpoint.Outcome != deliveryOutcomes[from] {
			t.Errorf("checkpoint %d arrived from %q on %q, want %q on %q", index, checkpoint.From, checkpoint.Outcome, from, deliveryOutcomes[from])
		}
		if checkpoint.At.Before(recorded.Checkpoints[index-1].At) {
			t.Errorf("checkpoint %d was recorded before the one it followed", index)
		}
	}
	if last, standing := recorded.Position(); !standing || !last.Terminal {
		t.Errorf("Position() = %+v (standing %t), want the terminal it ended in", last, standing)
	}
}

// TestEveryTransitionIsDurableBeforeTheNextOne is the guarantee a resume rests
// on. It is not enough that the record is right at the end: it has to be right
// after every step, because a step is where a process dies.
func TestEveryTransitionIsDurableBeforeTheNextOne(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	executor := checkpointing(t, filepath.Join(root, "instances"))
	subject := &journal{path: filepath.Join(root, "performed")}
	if _, err := executor.Start("delivery"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	for step, state := range deliveredPath[:len(deliveredPath)-1] {
		stepped, err := executor.Step(context.Background(), "delivery", subject)
		if err != nil {
			t.Fatalf("Step() from %q error = %v", state, err)
		}
		// A second executor over the same store is a second process: what it reads
		// is the whole of what the first one would leave behind.
		elsewhere := checkpointing(t, executor.Instances.Root())
		recorded, err := elsewhere.Resume("delivery")
		if err != nil {
			t.Fatalf("Resume() after %q error = %v", state, err)
		}
		if recorded.State != stepped.State || recorded.Terminal != stepped.Terminal {
			t.Fatalf("after %q the record stands in %q (terminal %t) and the step reported %q (terminal %t)", state, recorded.State, recorded.Terminal, stepped.State, stepped.Terminal)
		}
		if want := step + 2; len(recorded.Checkpoints) != want {
			t.Fatalf("after %q the record holds %d checkpoints, want %d", state, len(recorded.Checkpoints), want)
		}
		if recorded.State != deliveredPath[step+1] {
			t.Fatalf("after %q the record stands in %q, want %q", state, recorded.State, deliveredPath[step+1])
		}
	}

	// A terminal is where an instance stops rather than a state with a step in it.
	if _, err := executor.Step(context.Background(), "delivery", subject); err == nil {
		t.Fatal("Step() performed something in a terminal")
	}
}

// TestADefinitionChangedUnderAnInstanceIsRefused is the pin held at the end the
// loader cannot hold it at. A definition somebody edits mid-flight compiles to a
// perfectly good graph; what must not happen is an instance already running the
// old one carrying on under the new one.
func TestADefinitionChangedUnderAnInstanceIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	executor := checkpointing(t, filepath.Join(root, "instances"))
	subject := &journal{path: filepath.Join(root, "performed")}
	if _, err := executor.Start("delivery"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := executor.Step(context.Background(), "delivery", subject); err != nil {
		t.Fatalf("Step() error = %v", err)
	}

	registry := deliveryActions(t, func(name string) func(context.Context, *journal) error {
		return func(_ context.Context, acting *journal) error { return acting.perform(name) }
	})
	grant, err := NewGrant(capability.All()...)
	if err != nil {
		t.Fatalf("NewGrant() error = %v", err)
	}
	amended, err := Loader[*journal]{Registry: registry, Grant: grant}.LoadFile("testdata/delivery-amended.yaml")
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if amended.Digest() == executor.Graph.Digest() {
		t.Fatal("the amended fixture digests the same as the one the instance pinned")
	}
	changed := executor
	changed.Graph = amended

	if _, err := changed.Resume("delivery"); err == nil {
		t.Fatal("Resume() adopted a definition the instance is not pinned to")
	} else {
		if !strings.Contains(err.Error(), executor.Graph.Digest()) || !strings.Contains(err.Error(), amended.Digest()) {
			t.Errorf("Resume() error = %v, and it does not say which two definitions disagree", err)
		}
	}
	if _, err := changed.Step(context.Background(), "delivery", subject); err == nil {
		t.Fatal("Step() performed a step of a definition the instance is not pinned to")
	}

	// The instance is where it was, still pinned to what it started on, and the
	// executor holding that definition steps it exactly as before.
	recorded, err := executor.Instances.Load("delivery")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.Digest != executor.Graph.Digest() || recorded.State != "develop" {
		t.Fatalf("the instance is pinned to %q and stands in %q", recorded.Digest, recorded.State)
	}
	if _, err := executor.Step(context.Background(), "delivery", subject); err != nil {
		t.Errorf("Step() error = %v after a changed definition was refused", err)
	}
}

// TestTheCapabilitiesEnforcedAtAStepAreTheRegistrysOwn is the safety half at
// execution. Compiling answered for the whole graph under the grant it was
// compiled with; this answers for the one step about to happen under the grant
// held now — and what it holds it against is what the Go table says the action
// requires, which is not something the definition has any way to state.
func TestTheCapabilitiesEnforcedAtAStepAreTheRegistrysOwn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	executor := checkpointing(t, filepath.Join(root, "instances"))
	logged := filepath.Join(root, "performed")
	subject := &journal{path: logged}
	if _, err := executor.Start("delivery"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// The same compiled graph, performed by something that may do everything
	// except promote. Narrowing the grant is not a recompile: the instance keeps
	// its pin, and the authority is a fact about what is performing it now.
	granted := slices.DeleteFunc(capability.All(), func(held capability.Capability) bool {
		return held == capability.PromotionLease || held == capability.TargetBranchMutate
	})
	grant, err := NewGrant(granted...)
	if err != nil {
		t.Fatalf("NewGrant() error = %v", err)
	}
	narrowed := executor
	narrowed.Grant = grant

	_, err = narrowed.Run(context.Background(), "delivery", subject)
	if err == nil {
		t.Fatal("Run() integrated a change under a grant that may not promote")
	}
	if !strings.Contains(err.Error(), `the state "integrate" performs "candidate.integrate"`) {
		t.Errorf("Run() error = %v, and it does not name the state and action it refused", err)
	}
	for _, missing := range []capability.Capability{capability.PromotionLease, capability.TargetBranchMutate} {
		if !strings.Contains(err.Error(), string(missing)) {
			t.Errorf("Run() error = %v, and it does not name the missing %q", err, missing)
		}
	}

	// The capabilities it refused for are nowhere in the definition that selected
	// the action: a file selects a name, and what performing that name requires is
	// declared in code.
	definition, err := os.ReadFile("testdata/delivery.yaml")
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}
	for _, missing := range []capability.Capability{capability.PromotionLease, capability.TargetBranchMutate} {
		if strings.Contains(string(definition), string(missing)) {
			t.Errorf("the fixture names %q; this test is no longer about what the registry declares", missing)
		}
	}

	// The refusal happened before the action, so the instance stands where it
	// stood and the step nothing was authorized for was never performed.
	recorded, err := executor.Instances.Load("delivery")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.State != "integrate" {
		t.Fatalf("the instance stands in %q, want the state its authority ran out at", recorded.State)
	}
	if acted := performed(t, logged); slices.Contains(acted, "candidate.integrate") {
		t.Errorf("the run performed %v, and integrating was refused", acted)
	}
	// The executor that may promote carries the same instance on from there.
	finished, err := executor.Run(context.Background(), "delivery", subject)
	if err != nil {
		t.Fatalf("Run() error = %v under the grant that may promote", err)
	}
	if finished.State != "delivered" {
		t.Errorf("the instance ended in %q, want delivered", finished.State)
	}
}

// TestAnOutcomeThePinnedDefinitionSendsNowhereStopsWhereItIs: an action's
// outcomes are not declared yet, so an outcome nothing maps is something the
// runtime meets rather than something validation caught. Meeting it stops the
// instance where it is, which is the position a person can act on.
func TestAnOutcomeThePinnedDefinitionSendsNowhereStopsWhereItIs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	executor := checkpointing(t, filepath.Join(root, "instances"))
	logged := filepath.Join(root, "performed")
	unmapped := map[string]string{}
	for state, outcome := range deliveryOutcomes {
		unmapped[state] = outcome
	}
	unmapped["review"] = "rubber-stamped"
	executor.Outcome = scripted(unmapped)
	if _, err := executor.Start("delivery"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	_, err := executor.Run(context.Background(), "delivery", &journal{path: logged})
	if err == nil {
		t.Fatal("Run() transitioned on an outcome the definition sends nowhere")
	}
	if !strings.Contains(err.Error(), `"rubber-stamped"`) || !strings.Contains(err.Error(), `"review"`) {
		t.Errorf("Run() error = %v, and it does not name the state and the outcome", err)
	}
	recorded, err := executor.Instances.Load("delivery")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.State != "review" {
		t.Errorf("the instance stands in %q, want the state whose outcome went nowhere", recorded.State)
	}
}

// TestAnActionThatFailsLeavesTheInstanceWhereItWas: a failure is not an outcome,
// and a definition maps outcomes. The instance keeps the position it durably
// stood in, so whatever resumes it performs that step again.
func TestAnActionThatFailsLeavesTheInstanceWhereItWas(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	registry := deliveryActions(t, func(name string) func(context.Context, *journal) error {
		return func(_ context.Context, acting *journal) error {
			if name == "candidate.check" {
				return fmt.Errorf("the checks could not be run at all")
			}
			return acting.perform(name)
		}
	})
	grant, err := NewGrant(capability.All()...)
	if err != nil {
		t.Fatalf("NewGrant() error = %v", err)
	}
	graph, err := Loader[*journal]{Registry: registry, Grant: grant}.LoadFile("testdata/delivery.yaml")
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	executor := checkpointing(t, filepath.Join(root, "instances"))
	executor.Graph = graph
	if _, err := executor.Start("delivery"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if _, err := executor.Run(context.Background(), "delivery", &journal{path: filepath.Join(root, "performed")}); err == nil {
		t.Fatal("Run() carried on past an action that failed")
	} else if !strings.Contains(err.Error(), "the checks could not be run at all") {
		t.Errorf("Run() error = %v, and it does not carry what failed", err)
	}
	recorded, err := executor.Instances.Load("delivery")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.State != "check" {
		t.Errorf("the instance stands in %q, want the state whose action failed", recorded.State)
	}
}

// TestAnInstanceThatLoopsStopsAtItsBound. A definition may loop, and a run of one
// that never leaves the loop has to stop somewhere a person can see rather than
// go round forever.
func TestAnInstanceThatLoopsStopsAtItsBound(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	executor := checkpointing(t, filepath.Join(root, "instances"))
	looping := map[string]string{}
	for state, outcome := range deliveryOutcomes {
		looping[state] = outcome
	}
	// A check that always fails sends the instance back to the developer, which is
	// the loop the delivery fixture already has.
	looping["check"] = "failed"
	executor.Outcome = scripted(looping)
	executor.Bound = 5
	if _, err := executor.Start("delivery"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	_, err := executor.Run(context.Background(), "delivery", &journal{path: filepath.Join(root, "performed")})
	if err == nil {
		t.Fatal("Run() went round a loop that never ends")
	}
	if !strings.Contains(err.Error(), "looping") {
		t.Errorf("Run() error = %v, want it to say the instance is going round", err)
	}
	recorded, err := executor.Instances.Load("delivery")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Every transition it did make is durable: stopping is not undoing.
	if want := executor.Bound + 1; len(recorded.Checkpoints) != want {
		t.Errorf("the record holds %d checkpoints, want %d", len(recorded.Checkpoints), want)
	}
}

// TestAnExecutorMissingWhatItNeedsRefusesEverything is the fail-closed default,
// the same one the compiler has. A zero executor is a caller that said nothing
// about what it runs, where it records it, or what it may do.
func TestAnExecutorMissingWhatItNeedsRefusesEverything(t *testing.T) {
	t.Parallel()

	var executor Executor[*journal]
	_, err := executor.Start("delivery")
	if err == nil {
		t.Fatal("Start() started an instance of nothing")
	}
	for _, missing := range []string{"no compiled graph", "nowhere to record", "cannot read what an action produced", "confers no capabilities"} {
		if !strings.Contains(err.Error(), missing) {
			t.Errorf("Start() error = %v, and it does not report %q", err, missing)
		}
	}

	// A grant conferring nothing is refused on its own, so an executor that
	// narrowed one down to nothing hears about it before a step does.
	root := t.TempDir()
	unauthorized := checkpointing(t, filepath.Join(root, "instances"))
	unauthorized.Grant = Grant{}
	if _, err := unauthorized.Start("delivery"); err == nil {
		t.Fatal("Start() started an instance under a grant conferring nothing")
	}
}

// TestStartingAnInstanceTwiceIsRefused: two instances under one identifier are
// two positions in one workflow, and a resume would not say which it meant.
func TestStartingAnInstanceTwiceIsRefused(t *testing.T) {
	t.Parallel()

	executor := checkpointing(t, t.TempDir())
	if _, err := executor.Start("delivery"); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := executor.Start("delivery"); err == nil {
		t.Fatal("Start() created a second instance under one identifier")
	}
	if _, err := executor.Start("Delivery Run"); err == nil {
		t.Fatal("Start() accepted an identifier that is not one")
	}
}
