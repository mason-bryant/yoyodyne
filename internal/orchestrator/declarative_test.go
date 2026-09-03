package orchestrator

// The declarative trial, driven against real runs.
//
// The parity harness beside this file walks transcripts somebody wrote down and
// holds them against the recorded baseline. That answers whether the definitions
// can express what the pipeline did; it cannot answer whether a run happening
// now produces those transitions, because a transcript is written by hand and a
// run is not. This drives the real pipeline into each path with the trial turned
// on and reads the instance back off the store, so what is compared is a
// sequence the run actually produced against the transcript the harness measures
// that same path by. A divergence here is the observation being wrong about the
// pipeline, which is the one thing a soak of it must not be.
//
// The traces those transcripts belong to are the same ones baseline_test.go
// drives, and these scenarios drive them the same way. They are re-driven rather
// than shared because the baseline scenarios must stay exactly as they are: the
// trial writes a `workflow_instance_id` onto the run record, and a trace
// re-recorded with one in it would be the baseline moving because something was
// watching it.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// trial is this fixture's pipeline with the opt-in turned on and somewhere to
// record instances, which is the whole of what a project does to enter it.
func (f *baselineFixture) trial(t *testing.T, provider *fakeBackend, commands []string) Pipeline {
	t.Helper()
	pipeline := f.pipeline(t, provider, commands)
	pipeline.Instances = f.store
	pipeline.Config.Execution.DeclarativeDelivery = true
	return pipeline
}

// automaticTrial is the same under automatic integration, which is the policy
// every path but the human-approval one runs under.
func (f *baselineFixture) automaticTrial(t *testing.T, provider *fakeBackend, commands []string) Pipeline {
	t.Helper()
	return automatic(f.trial(t, provider, commands), provider)
}

// observedRun reads back the instance a run was observed through, and fails if
// the run was never observed at all.
func observedRun(t *testing.T, store *runstate.Store) (runstate.State, runstate.WorkflowInstance) {
	t.Helper()
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.WorkflowInstanceID == "" {
		t.Fatalf("the run records no workflow instance; it was not observed")
	}
	instance, err := store.LoadWorkflowInstance(state.WorkflowInstanceID)
	if err != nil {
		t.Fatalf("LoadWorkflowInstance(%s) error = %v", state.WorkflowInstanceID, err)
	}
	return state, instance
}

// deliveryTranscript is an instance's path said the way the parity harness says
// one: the state each action was performed in and the outcome it produced there,
// which is what makes a declarative run's record diffable against the recorded
// baseline. The initial checkpoint is skipped because nothing transitioned into
// it — an instance is created standing on its first state.
func deliveryTranscript(instance runstate.WorkflowInstance) []string {
	transcript := make([]string, 0, len(instance.Checkpoints))
	for _, checkpoint := range instance.Checkpoints {
		if checkpoint.From == "" {
			continue
		}
		transcript = append(transcript, checkpoint.From+" "+checkpoint.Outcome)
	}
	return transcript
}

// deliveryTrialSteps is what the observation can report, by state, and it is
// this test's enumeration rather than the code's: the classifiers in
// declarative.go decide an outcome each from the ending they are handed, and
// nothing in them can be asked what the whole set is. Holding it against the
// registry is what says the two vocabularies have not drifted apart — an outcome
// the trial can report and no step produces would reach a run as a divergence
// against a definition that was right.
func deliveryTrialSteps() map[string][]string {
	return map[string][]string{
		deliveryClaim:     {"claimed", "unavailable"},
		deliveryDevelop:   {"produced", "reissued", "relaunches-spent", "stopped"},
		deliveryCheck:     {"failed", "failed-unrepaired", "passed", "refused", "refused-unrepaired", "unrunnable"},
		deliveryReview:    {"approved", "changes-requested", "stopped", "unresolved"},
		deliveryIntegrate: {"conflicted", "contended", "integrated", "superseded"},
		deliveryComplete:  {"completed"},
		deliveryCleanUp:   {"cleaned", "partial"},
	}
}

// transcriptOf is a parity scenario said the way an instance says its own path,
// so the two can be compared as one list against another.
func transcriptOf(t *testing.T, trace string) ([]string, string) {
	t.Helper()
	for _, scenario := range parityScenarios() {
		if scenario.trace != trace {
			continue
		}
		transcript := make([]string, 0, len(scenario.steps))
		for _, step := range scenario.steps {
			transcript = append(transcript, step.state+" "+step.outcome)
		}
		return transcript, scenario.terminal
	}
	t.Fatalf("no parity scenario measures the trace %q", trace)
	return nil, ""
}

// declarativeScenario is one delivery path driven with the trial on, named by
// the recorded trace whose transcript it has to reproduce.
type declarativeScenario struct {
	// trace is the recorded path this run is one of, and the transcript the
	// parity harness measures that path by is what the instance must record.
	trace string
	drive func(t *testing.T) *baselineFixture
}

func declarativeScenarios() []declarativeScenario {
	return []declarativeScenario{
		{
			trace: "human-approved-change-is-preserved-for-its-approver",
			drive: func(t *testing.T) *baselineFixture {
				fixture := newBaselineFixture(t, baselineItem())
				provider := roleBackend(baselineImplements, approveVerdict)
				fixture.invoke(t, "run", fixture.trial(t, provider, []string{"test -f feature.txt"}))
				return fixture
			},
		},
		{
			trace: "automatic-run-promotes-reviews-closes-and-cleans-up",
			drive: func(t *testing.T) *baselineFixture {
				fixture := newBaselineFixture(t, baselineItem())
				provider := roleBackend(baselineImplements, approveVerdict)
				fixture.invoke(t, "run", fixture.automaticTrial(t, provider, []string{"test -f feature.txt"}))
				return fixture
			},
		},
		{
			trace: "failing-check-is-repaired-and-then-promoted",
			drive: func(t *testing.T) *baselineFixture {
				fixture := newBaselineFixture(t, baselineItem())
				attempts := 0
				provider := roleBackend(func(request backend.RunRequest) error {
					attempts++
					if attempts == 1 {
						return baselineImplements(request)
					}
					return os.WriteFile(filepath.Join(request.WorkingDirectory, "repaired.txt"), []byte("repaired\n"), 0o600)
				}, approveVerdict)
				fixture.invoke(t, "run", fixture.automaticTrial(t, provider, []string{"test -f repaired.txt"}))
				return fixture
			},
		},
		{
			trace: "failing-check-spends-the-repair-budget-and-blocks",
			drive: func(t *testing.T) *baselineFixture {
				fixture := newBaselineFixture(t, baselineItem())
				provider := roleBackend(baselineImplements, approveVerdict)
				fixture.invoke(t, "run", fixture.automaticTrial(t, provider, []string{"exit 1"}))
				return fixture
			},
		},
		{
			trace: "review-findings-are-repaired-and-then-promoted",
			drive: func(t *testing.T) *baselineFixture {
				fixture := newBaselineFixture(t, baselineItem())
				provider := roleBackend(baselineImplements, repairVerdict, approveVerdict)
				fixture.invoke(t, "run", fixture.automaticTrial(t, provider, []string{"test -f feature.txt"}))
				return fixture
			},
		},
		{
			trace: "review-findings-spend-the-repair-budget-and-block",
			drive: func(t *testing.T) *baselineFixture {
				fixture := newBaselineFixture(t, baselineItem())
				provider := roleBackend(baselineImplements, repairVerdict)
				fixture.invoke(t, "run", fixture.automaticTrial(t, provider, []string{"test -f feature.txt"}))
				return fixture
			},
		},
		{
			trace: "protected-path-refusal-is-repaired-before-any-check-runs",
			drive: func(t *testing.T) *baselineFixture {
				fixture := newBaselineFixture(t, baselineItem())
				attempts := 0
				provider := roleBackend(func(request backend.RunRequest) error {
					attempts++
					if err := baselineImplements(request); err != nil {
						return err
					}
					if attempts == 1 {
						return writeUpstream(t, request.WorkingDirectory, "docs/product/brief.md", "the product is whatever this run needed it to be\n")
					}
					return os.RemoveAll(filepath.Join(request.WorkingDirectory, "docs", "product"))
				}, approveVerdict)
				fixture.invoke(t, "run", fixture.automaticTrial(t, provider, []string{"test -f feature.txt"}))
				return fixture
			},
		},
		{
			trace: "usage-limit-pause-exits-resumable-and-a-later-invocation-finishes-it",
			drive: func(t *testing.T) *baselineFixture {
				fixture := newBaselineFixture(t, baselineItem())
				resetsAt := baseTime.Add(2 * time.Hour)
				limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}
				refused := usageLimitBackend(1, limit, approveVerdict)
				fixture.invoke(t, "paused invocation", waiting(fixture.automaticTrial(t, refused, []string{"test -f feature.txt"}),
					&pausingClock{now: baseTime}, 6*time.Hour, time.Minute))
				served := usageLimitBackend(0, limit, approveVerdict)
				fixture.invoke(t, "resumed invocation", waiting(fixture.automaticTrial(t, served, []string{"test -f feature.txt"}),
					&pausingClock{now: resetsAt.Add(time.Minute)}, 6*time.Hour, time.Minute))
				return fixture
			},
		},
	}
}

// TestADeclarativeRunRecordsTheSequenceTheDefinitionChose is the trial itself:
// every path driven for real with the opt-in on, and the instance's own record
// of where the definition sent it held against the transcript the parity harness
// measures that path by.
//
// The instance rather than anything this test watched is deliberate, and it is
// the same reason the parity walk reads the record: what a later process would
// see is the checkpoints, so comparing those is comparing what was durably said
// to have happened.
func TestADeclarativeRunRecordsTheSequenceTheDefinitionChose(t *testing.T) {
	t.Parallel()

	for _, scenario := range declarativeScenarios() {
		t.Run(scenario.trace, func(t *testing.T) {
			t.Parallel()
			fixture := scenario.drive(t)
			state, instance := observedRun(t, fixture.store)

			if state.WorkflowDivergence != "" {
				t.Errorf("the run recorded the divergence %q; a path the harness measures is one the definition expresses", state.WorkflowDivergence)
			}
			want, terminal := transcriptOf(t, scenario.trace)
			if got := deliveryTranscript(instance); !slices.Equal(got, want) {
				t.Errorf("the instance recorded\n\t%v\nand the transcript this path is measured by is\n\t%v", got, want)
			}
			if !instance.Terminal || instance.State != terminal {
				t.Errorf("the instance ended in %q (terminal=%t) and the transcript ends in %q", instance.State, instance.Terminal, terminal)
			}
			// The instance is pinned to the definition it started on, and the run
			// names the instance rather than the other way round: both are what a
			// second process reads to pick the observation back up.
			if instance.InstanceID != deliveryInstanceID(state.RunID) {
				t.Errorf("the instance is %q and the run is %q", instance.InstanceID, state.RunID)
			}
			if instance.Digest == "" {
				t.Errorf("the instance is pinned to no definition")
			}
		})
	}
}

// TestTheDeclarativeTrialIsOffUntilTheProjectAsksForIt holds the opt-in to being
// one: the same run under the same pipeline with the flag left alone records no
// instance and is indistinguishable from every run made before the trial
// existed.
func TestTheDeclarativeTrialIsOffUntilTheProjectAsksForIt(t *testing.T) {
	t.Parallel()

	fixture := newBaselineFixture(t, baselineItem())
	provider := roleBackend(baselineImplements, approveVerdict)
	pipeline := fixture.pipeline(t, provider, []string{"test -f feature.txt"})
	// Somewhere to record instances, and nothing asking for any: the wiring is
	// what production does, so what this measures is the flag rather than the
	// absence of a store.
	pipeline.Instances = fixture.store
	if pipeline.Config.Execution.DeclarativeDelivery {
		t.Fatalf("a configuration nobody edited already opts into the trial")
	}
	fixture.invoke(t, "run", pipeline)

	state, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.WorkflowInstanceID != "" {
		t.Errorf("the run records the instance %q and nothing opted into the trial", state.WorkflowInstanceID)
	}
	if _, err := fixture.store.LoadWorkflowInstance(deliveryInstanceID(pipelineRunID)); err == nil {
		t.Errorf("an instance was recorded for a run nothing observed")
	}
}

// TestALegacyRunInFlightIsNeverMigratedIntoTheTrial is the resumability half of
// the opt-in, driven the only way it can be shown: a run started while the trial
// was off, left in flight, and picked up by a process whose configuration has
// the trial on.
//
// The rule it holds is that the run's own record decides. A run reserved before
// the opt-in names no instance, and no later process invents one for it however
// its configuration reads — so a legacy run stays a legacy run for the whole of
// its life, and the flag reaches new runs only.
func TestALegacyRunInFlightIsNeverMigratedIntoTheTrial(t *testing.T) {
	t.Parallel()

	fixture := newBaselineFixture(t, baselineItem())
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}

	// The first invocation is refused for want of capacity and the wait is longer
	// than this process holds open, so it exits with the run in flight. The trial
	// is off, which is what makes this a legacy run.
	refused := usageLimitBackend(1, limit, approveVerdict)
	legacy := waiting(automatic(fixture.pipeline(t, refused, []string{"test -f feature.txt"}), refused),
		&pausingClock{now: baseTime}, 6*time.Hour, time.Minute)
	legacy.Instances = fixture.store
	fixture.invoke(t, "paused invocation", legacy)

	paused, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if paused.Status.Terminal() {
		t.Fatalf("the first invocation ended the run in %s; there is nothing left in flight to resume", paused.Status)
	}
	if paused.WorkflowInstanceID != "" {
		t.Fatalf("the run was started with the trial off and records the instance %q", paused.WorkflowInstanceID)
	}

	// The flag is flipped between the two invocations, which is the whole of what
	// this test is about.
	served := usageLimitBackend(0, limit, approveVerdict)
	resuming := waiting(fixture.automaticTrial(t, served, []string{"test -f feature.txt"}),
		&pausingClock{now: resetsAt.Add(time.Minute)}, 6*time.Hour, time.Minute)
	outcome := fixture.invoke(t, "resumed invocation", resuming)
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("the resumed run ended in %s; the legacy path did not finish the work", outcome.Status)
	}

	finished, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if finished.WorkflowInstanceID != "" {
		t.Errorf("the resumed run records the instance %q; a run in flight is never migrated into the trial", finished.WorkflowInstanceID)
	}
	if finished.WorkflowDivergence != "" {
		t.Errorf("the resumed run records the divergence %q and was never observed", finished.WorkflowDivergence)
	}
	if _, err := fixture.store.LoadWorkflowInstance(deliveryInstanceID(pipelineRunID)); err == nil {
		t.Errorf("an instance was recorded for a run that started on the legacy path")
	}
}

// TestARunInTheTrialIsObservedAfterTheTrialIsTurnedOff is the same rule read the
// other way, and it is worth its own run because the other direction is the one
// that leaves a half-observed instance: a run that started in the trial keeps
// being stepped by a process whose configuration no longer asks for it, so the
// instance a soak is counting reaches a terminal rather than stopping wherever
// the operator happened to edit the file.
func TestARunInTheTrialIsObservedAfterTheTrialIsTurnedOff(t *testing.T) {
	t.Parallel()

	fixture := newBaselineFixture(t, baselineItem())
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}

	refused := usageLimitBackend(1, limit, approveVerdict)
	fixture.invoke(t, "paused invocation", waiting(fixture.automaticTrial(t, refused, []string{"test -f feature.txt"}),
		&pausingClock{now: baseTime}, 6*time.Hour, time.Minute))

	paused, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if paused.WorkflowInstanceID == "" {
		t.Fatalf("the run was started in the trial and records no instance")
	}

	served := usageLimitBackend(0, limit, approveVerdict)
	off := waiting(automatic(fixture.pipeline(t, served, []string{"test -f feature.txt"}), served),
		&pausingClock{now: resetsAt.Add(time.Minute)}, 6*time.Hour, time.Minute)
	off.Instances = fixture.store
	if off.Config.Execution.DeclarativeDelivery {
		t.Fatalf("the resuming pipeline still opts into the trial; this test turns it off")
	}
	fixture.invoke(t, "resumed invocation", off)

	state, instance := observedRun(t, fixture.store)
	if state.WorkflowDivergence != "" {
		t.Errorf("the run recorded the divergence %q", state.WorkflowDivergence)
	}
	want, terminal := transcriptOf(t, "usage-limit-pause-exits-resumable-and-a-later-invocation-finishes-it")
	if got := deliveryTranscript(instance); !slices.Equal(got, want) {
		t.Errorf("the instance recorded\n\t%v\nand the transcript this path is measured by is\n\t%v", got, want)
	}
	if !instance.Terminal || instance.State != terminal {
		t.Errorf("the instance ended in %q (terminal=%t) and the transcript ends in %q", instance.State, instance.Terminal, terminal)
	}
}

// observedFixture is a run standing where a fresh one stands, with the trial
// begun and nothing performed. It is built directly rather than driven because
// what the tests below are about is what the observation does when the run and
// the definition stop agreeing, and none of the paths driven above reaches that.
// The real path that does — a process killed while its reviewer was being asked,
// resumed at the checks because a resumed run re-earns the whole gate — needs an
// interrupted process to reach, and what it produces is the first of the two
// disagreements stated here.
func observedFixture(t *testing.T) (*activeRun, *runstate.Store) {
	t.Helper()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	state := completingRun("yoyodyne-task", runstate.PhaseDeveloping)
	pipeline := Pipeline{Store: store, Instances: store}
	pipeline.Config.Execution.DeclarativeDelivery = true
	pipeline.Config.Approvals.Integration = domain.ApprovalAutomatic
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	run := &activeRun{pipeline: pipeline, state: state}
	run.beginDeliveryTrial()
	if run.trial == nil {
		t.Fatalf("the trial did not begin over a store that can record it")
	}
	return run, store
}

// TestAnObservationTheDefinitionCannotFollowIsRecordedOnTheRun is what a soak
// counts: a boundary the definition and the run do not agree on is written down
// on the run, and the instance is left standing where the two last agreed rather
// than dragged into a position neither of them was in.
func TestAnObservationTheDefinitionCannotFollowIsRecordedOnTheRun(t *testing.T) {
	t.Parallel()

	for _, disagreement := range []struct {
		what    string
		state   string
		outcome string
	}{
		{
			what:    "a state the instance is not standing in",
			state:   deliveryCheck,
			outcome: "passed",
		},
		{
			what:    "an outcome the state does not route",
			state:   deliveryClaim,
			outcome: "produced",
		},
	} {
		t.Run(disagreement.what, func(t *testing.T) {
			t.Parallel()
			run, store := observedFixture(t)
			run.observe(context.Background(), disagreement.state, disagreement.outcome)

			if run.state.WorkflowDivergence == "" {
				t.Fatalf("%s was observed and the run records no divergence", disagreement.what)
			}
			recorded, err := store.Load(run.state.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if recorded.WorkflowDivergence != run.state.WorkflowDivergence {
				t.Errorf("the divergence on disk is %q and the run holds %q; a run can diverge after its own last save",
					recorded.WorkflowDivergence, run.state.WorkflowDivergence)
			}
			instance, err := store.LoadWorkflowInstance(run.state.WorkflowInstanceID)
			if err != nil {
				t.Fatalf("LoadWorkflowInstance() error = %v", err)
			}
			if instance.State != deliveryClaim || len(instance.Checkpoints) != 1 {
				t.Errorf("the instance stands in %q with %d checkpoint(s); it was moved by a boundary the definition refused",
					instance.State, len(instance.Checkpoints))
			}

			// Nothing further is observed, so a run that carries on delivering after
			// a divergence does not write a second answer over the first.
			first := run.state.WorkflowDivergence
			run.observe(context.Background(), deliveryClaim, "claimed")
			if run.state.WorkflowDivergence != first {
				t.Errorf("the run kept observing after it diverged: %q became %q", first, run.state.WorkflowDivergence)
			}
			after, err := store.LoadWorkflowInstance(run.state.WorkflowInstanceID)
			if err != nil {
				t.Fatalf("LoadWorkflowInstance() error = %v", err)
			}
			if len(after.Checkpoints) != 1 {
				t.Errorf("the instance recorded %d checkpoint(s) after the run stopped being observed", len(after.Checkpoints))
			}
		})
	}
}

// TestTheTrialReportsOnlyOutcomesTheRegisteredStepsProduce holds the observation
// to the same vocabulary the definitions are compiled against.
//
// An outcome the trial can report and the registry does not declare is a
// transition no definition routes, which would reach a run as a divergence
// against nothing — the definition would be right and the observation wrong. The
// other direction is checked too: an outcome a step produces and nothing here
// can report is a branch the trial would never exercise, which is a soak that
// looks broader than it is.
func TestTheTrialReportsOnlyOutcomesTheRegisteredStepsProduce(t *testing.T) {
	t.Parallel()

	for _, builtin := range builtinDeliveryWorkflows() {
		graph, err := compileDelivery(builtin)
		if err != nil {
			t.Fatalf("compileDelivery(%s) error = %v", builtin.Source, err)
		}
		for _, state := range graph.States() {
			node, _ := graph.Node(state)
			reported, named := deliveryTrialSteps()[state]
			if !named {
				t.Errorf("%s declares the state %q and the trial says nothing about it", builtin.Source, state)
				continue
			}
			declared := deliveryAnswers[node.Action().Name]
			for _, outcome := range reported {
				if !slices.Contains(declared, outcome) {
					t.Errorf("the trial reports %q for %q, which %q never produces; it answers with %v",
						outcome, state, node.Action().Name, declared)
				}
			}
			for _, outcome := range declared {
				if !slices.Contains(reported, outcome) {
					t.Errorf("%q produces %q in the state %q and the trial can never report it",
						node.Action().Name, outcome, state)
				}
			}
		}
	}
}
