package orchestrator

// What the scheduler chooses, how many at a time, and what it records about
// having chosen. The end-to-end test at the top is the acceptance criterion
// itself — several real runs at once, each in a worktree of its own, none of
// them force-integrated — and the rest are about the arithmetic and the
// refusals, which are cheaper to drive against a fake harness than against
// three real Git worktrees.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/staleness"
)

// scheduleRendezvous is how long a test will wait for the developers it expects
// to overlap to actually overlap. It is generous because it bounds a failure
// rather than a success: runs that do overlap release each other immediately.
const scheduleRendezvous = 30 * time.Second

// The acceptance criterion, against the real pipeline: three ready items, a
// configured capacity of two, and every run in a worktree of its own. Two
// developers are held in a rendezvous until both are inside, which is what makes
// "concurrently" an observation rather than an inference, and all three changes
// reach the target branch by fast-forward — the second and third having been
// replayed onto where the first left it rather than forced over it.
func TestSchedulerRunsSeveralEligibleItemsAtOnceInWorktreesOfTheirOwn(t *testing.T) {
	t.Parallel()

	harness := newRealScheduleHarness(t, 2, "yoyodyne-alpha", "yoyodyne-beta", "yoyodyne-gamma")
	// Two developers must be inside at once before either is let out. With a
	// capacity of one this deadlocks until the rendezvous times out, which is
	// exactly the failure the criterion is about.
	harness.developersMeet(2)

	scheduler := Scheduler{Open: harness.open}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if harness.rendezvousFailure != nil {
		t.Fatalf("the developers never overlapped: %v", harness.rendezvousFailure)
	}
	if len(schedule.Started) != 3 {
		t.Fatalf("started = %d run(s) (%s), want all three items pulled", len(schedule.Started), schedule.Render())
	}
	for _, started := range schedule.Started {
		if started.Failure != "" || started.Declined != "" {
			t.Fatalf("%s did not run: failure=%q declined=%q", started.WorkItemID, started.Failure, started.Declined)
		}
		if started.Outcome.Integration == nil {
			t.Fatalf("%s was not integrated: %#v", started.WorkItemID, started.Outcome)
		}
	}

	// Every run had its own worktree and its own branch. Sharing either is the
	// thing the design forbids outright, so it is checked rather than assumed
	// from the runs having succeeded.
	worktrees := map[string]string{}
	branches := map[string]string{}
	for _, started := range schedule.Started {
		if previous, seen := worktrees[started.Outcome.WorktreePath]; seen {
			t.Fatalf("%s and %s shared worktree %s", previous, started.WorkItemID, started.Outcome.WorktreePath)
		}
		worktrees[started.Outcome.WorktreePath] = started.WorkItemID
		if previous, seen := branches[started.Outcome.Branch]; seen {
			t.Fatalf("%s and %s shared branch %s", previous, started.WorkItemID, started.Outcome.Branch)
		}
		branches[started.Outcome.Branch] = started.WorkItemID
	}

	// The target branch carries all three changes, each promoted onto the one
	// before it. A promotion that had been forced would have left one of these
	// files behind.
	for _, id := range []string{"yoyodyne-alpha", "yoyodyne-beta", "yoyodyne-gamma"} {
		if _, err := os.Stat(filepath.Join(harness.repository, id+".txt")); err != nil {
			t.Fatalf("%s is not on the target branch after its run integrated: %v", id, err)
		}
	}

	// Every run accounts for itself in durable state: work the harness chose
	// without a recorded reason is what the reason exists to make impossible.
	recorded, err := harness.store.Recorded()
	if err != nil {
		t.Fatalf("Recorded() error = %v", err)
	}
	if len(recorded) != 3 {
		t.Fatalf("recorded runs = %d, want one per started item", len(recorded))
	}
	for _, state := range recorded {
		if state.Selection == nil {
			t.Fatalf("run %s (%s) recorded no reason it was selected", state.RunID, state.WorkItemID)
		}
		if state.Selection.By != runstate.SelectedByScheduler {
			t.Fatalf("run %s was selected by %q, want the scheduler", state.RunID, state.Selection.By)
		}
		if !strings.Contains(state.Selection.Reason, state.WorkItemID) {
			t.Fatalf("run %s reason = %q, want it to account for the item it chose", state.RunID, state.Selection.Reason)
		}
	}
}

// A capacity of one -- the default -- serializes the work: three items still all
// run, and never two at a time.
func TestSchedulerHonorsACapacityOfOne(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.capacity = 1

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 3 {
		t.Fatalf("started = %d, want every item run: %s", len(schedule.Started), schedule.Render())
	}
	if harness.peak != 1 {
		t.Fatalf("peak concurrent runs = %d, want a configured capacity of one to be honored", harness.peak)
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want the queue drained", schedule.Stopped)
	}
}

// A raised capacity runs more at once, and the arithmetic accounts for the runs
// this pass has started but that have not reserved yet: a scheduler that counted
// only the recorded runs would start the same slot repeatedly.
func TestSchedulerRunsUpToTheConfiguredCapacity(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four")...)
	harness.capacity = 3
	harness.developersMeet(3)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if harness.rendezvousFailure != nil {
		t.Fatalf("three runs never overlapped: %v", harness.rendezvousFailure)
	}
	if len(schedule.Started) != 4 {
		t.Fatalf("started = %d, want every item run: %s", len(schedule.Started), schedule.Render())
	}
	if harness.peak > 3 {
		t.Fatalf("peak concurrent runs = %d, want no more than the configured capacity of 3", harness.peak)
	}
}

// The intake hold is the operator's narrow control over work the harness chooses
// for itself, and the scheduler is the thing that chooses. Nothing is started
// and nothing is claimed.
func TestSchedulerStartsNothingWhileIntakeIsHeld(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.held = &runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC),
		Reason:        "the decomposition is heading somewhere odd",
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing chosen while intake is held", schedule.Started)
	}
	if schedule.Stopped != ScheduleIntakeHeld || schedule.IntakeHeld == nil {
		t.Fatalf("schedule = %#v, want the hold reported as what stopped the choosing", schedule)
	}
	if !strings.Contains(schedule.Render(), "the decomposition is heading somewhere odd") {
		t.Fatalf("rendered = %q, want the operator's own reason for the hold", schedule.Render())
	}
}

// A hold placed while the scheduler is running stops it choosing anything more,
// and leaves what is already running alone. That is the whole difference between
// holding intake and pausing everything.
func TestSchedulerStopsChoosingWhenIntakeIsHeldMidPass(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.capacity = 1
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.mu.Lock()
		if len(h.order) == 1 {
			h.held = &runstate.IntakeHold{
				SchemaVersion: runstate.IntakeHoldSchemaVersion,
				ProductID:     "yoyodyne",
				HeldAt:        time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC),
			}
		}
		h.mu.Unlock()
		return h.complete(id), nil
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %d, want the run already going to finish and nothing more chosen: %s",
			len(schedule.Started), schedule.Render())
	}
	if schedule.Started[0].Outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("outcome = %#v, want the run in flight to have finished", schedule.Started[0].Outcome)
	}
	if schedule.Stopped != ScheduleIntakeHeld {
		t.Fatalf("stopped = %q, want the hold to be what stopped the choosing", schedule.Stopped)
	}
}

// Work an unresolved directive pauses is named and skipped rather than started
// into a pause. The pipeline stops it either way; what this saves is the slot,
// and what it adds is the directive's own words in the schedule.
func TestSchedulerSkipsWorkAnUnresolvedDirectivePauses(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.pausing["yoyodyne-one"] = []directive.Directive{{
		ID:         "directive-1",
		Kind:       directive.KindArtifact,
		Text:       "the goal is being rewritten",
		Unresolved: "which goal this item now serves",
	}}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-two" {
		t.Fatalf("started = %#v, want only the item nothing pauses", schedule.Started)
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != "yoyodyne-one" {
		t.Fatalf("deferred = %#v, want the paused item named rather than dropped", schedule.Deferred)
	}
	if !strings.Contains(schedule.Deferred[0].Reason, "directive-1") {
		t.Fatalf("deferred reason = %q, want the directive that paused it named", schedule.Deferred[0].Reason)
	}
}

// Dependencies are the tracker's answer rather than the scheduler's guess: an
// admitted item the tracker does not report as ready is left in the queue,
// however high its priority.
func TestSchedulerLeavesWorkTheTrackerDoesNotReportAsReady(t *testing.T) {
	t.Parallel()

	blocked := beads.WorkItem{ID: "yoyodyne-blocked", Title: "Blocked", Status: "open", Priority: 0}
	ready := beads.WorkItem{ID: "yoyodyne-ready", Title: "Ready", Status: "open", Priority: 2}
	harness := newScheduleHarness(blocked, ready)
	harness.ready = map[string]bool{ready.ID: true}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != ready.ID {
		t.Fatalf("started = %#v, want only the item the tracker reports as pullable", schedule.Started)
	}
}

// An item another process already has in flight is not started a second time.
// The reservation refuses it anyway; what this checks is that the scheduler does
// not spend a slot finding that out.
func TestSchedulerLeavesWorkAlreadyInFlightAlone(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.capacity = 2
	harness.inFlight["yoyodyne-one"] = runstate.State{
		RunID: "run-elsewhere", WorkItemID: "yoyodyne-one", Status: runstate.StatusRunning,
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-two" {
		t.Fatalf("started = %#v, want the item nobody else is running", schedule.Started)
	}
}

// Every developer slot held by somebody else leaves the scheduler nothing to
// start and nothing to wait for, so it says so rather than spinning.
func TestSchedulerStopsWhenEveryDeveloperSlotIsHeldElsewhere(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.capacity = 1
	harness.inFlight["yoyodyne-elsewhere"] = runstate.State{
		RunID: "run-elsewhere", WorkItemID: "yoyodyne-elsewhere", Status: runstate.StatusRunning,
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 || schedule.Stopped != ScheduleCapacityFull {
		t.Fatalf("schedule = %#v, want the full capacity reported rather than an empty queue", schedule)
	}
}

// The configuration is re-read at every pull, so a capacity raised while the
// scheduler is running takes effect at the next selection rather than at the
// next restart.
func TestSchedulerReadsTheConfigurationAtEveryPull(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.capacity = 1
	// The operator raises the limit after the first pull, which is the case the
	// design question was actually about.
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 1 {
			h.capacity = 3
		}
	}
	harness.developersMeet(2)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if harness.rendezvousFailure != nil {
		t.Fatalf("the raised capacity was never used: %v", harness.rendezvousFailure)
	}
	if harness.pulls < 2 {
		t.Fatalf("pulls = %d, want the configuration read more than once", harness.pulls)
	}
	if len(schedule.Started) != 3 {
		t.Fatalf("started = %d, want every item run: %s", len(schedule.Started), schedule.Render())
	}
}

// Staleness reports and decides nothing. An item whose goal was amended after it
// was admitted is pulled exactly as it would have been, and the change is
// written into the reason the run records, where somebody reading what the
// harness chose can see it.
func TestSchedulerPullsStaleWorkAndRecordsWhatChangedUnderIt(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.stale = []staleness.WorkItem{{
		ID:         "yoyodyne-one",
		ArtifactID: "v1-goals",
		Changes: []staleness.Change{{
			ArtifactID: "v1-goals",
			Action:     "amended",
			By:         domain.RoleProductManager,
			Reason:     "the second goal was narrowed",
		}},
	}}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want stale work pulled rather than withheld", schedule.Started)
	}
	reason := harness.selectionFor("yoyodyne-one").Reason
	if !strings.Contains(reason, "v1-goals") || !strings.Contains(reason, "the second goal was narrowed") {
		t.Fatalf("reason = %q, want what changed upstream named in it", reason)
	}
	if !strings.Contains(reason, "held nothing back") {
		t.Fatalf("reason = %q, want it stated that staleness decided nothing", reason)
	}
}

// A staleness reading that fails costs the recorded reasons a sentence and costs
// the pass nothing else.
func TestSchedulerSchedulesWhenStalenessCannotBeRead(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.staleErr = errors.New("the artifact home is unreadable")

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want the pass to carry on without a staleness reading", schedule.Started)
	}
	if !strings.Contains(schedule.StalenessProblem, "the artifact home is unreadable") {
		t.Fatalf("staleness problem = %q, want the failed reading named", schedule.StalenessProblem)
	}
}

// Two schedulers racing for the last slot is the design working, so a
// reservation that lost is reported as declined rather than as a failed run, and
// the pass exits zero.
func TestSchedulerReportsALostRaceAsDeclinedRatherThanFailed(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.close(id)
		return Outcome{WorkItemID: id}, fmt.Errorf("reserve developer run: %w", runstate.CapacityError{Limit: 1, Active: 1})
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].Declined == "" {
		t.Fatalf("started = %#v, want the lost race recorded as declined", schedule.Started)
	}
	if schedule.Started[0].Failure != "" || schedule.Failed() {
		t.Fatalf("schedule = %#v, want a lost race not to be a failure", schedule)
	}
}

// A run that failed is a failed run, and it does not stop the pass choosing
// others: what stops the choosing is the operator holding intake, not one item
// going wrong.
func TestSchedulerCarriesOnAfterOneRunFails(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.capacity = 1
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.close(id)
		if id == "yoyodyne-one" {
			return Outcome{WorkItemID: id, Status: runstate.StatusFailed}, errors.New("the checks did not pass")
		}
		return h.complete(id), nil
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %d, want a failed run not to stop the pass: %s", len(schedule.Started), schedule.Render())
	}
	if schedule.Started[0].Failure == "" || !schedule.Failed() {
		t.Fatalf("schedule = %#v, want the failed run reported as a failure", schedule)
	}
}

// A pull that cannot be made stops the choosing and does not abandon the runs
// already going: a scheduler that returned with work in flight would leave runs
// nothing in its own report accounts for.
func TestSchedulerWaitsOutStartedRunsWhenAPullFails(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.capacity = 1
	released := make(chan struct{})
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		<-released
		return h.complete(id), nil
	}
	// The pull after the one that started the run is the one that fails, so what
	// the scheduler is holding when it stops is a run of its own still going.
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 1 {
			h.openErr = errors.New("the configuration is unreadable")
			close(released)
		}
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err == nil || !strings.Contains(err.Error(), "the configuration is unreadable") {
		t.Fatalf("Schedule() error = %v, want the failed pull reported", err)
	}
	if schedule.Stopped != ScheduleUnreadable {
		t.Fatalf("stopped = %q, want the unreadable pull named", schedule.Stopped)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].Outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("started = %#v, want the run already going to have been waited out", schedule.Started)
	}
}

// An operator watching a pass wants a number; an unattended one wants the queue
// drained. The bound is respected exactly.
func TestSchedulerStopsAtTheRequestedLimit(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.capacity = 3

	schedule, err := Scheduler{Open: harness.open, Limit: 2}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 2 || schedule.Stopped != ScheduleLimitReached {
		t.Fatalf("schedule = %#v, want exactly the requested number of runs", schedule)
	}
}

// An empty backlog is an answer rather than a failure.
func TestSchedulerReportsAnEmptyQueue(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 || schedule.Stopped != ScheduleDrained {
		t.Fatalf("schedule = %#v, want an empty queue reported as drained", schedule)
	}
	if !strings.Contains(schedule.Render(), ScheduleDrained) {
		t.Fatalf("rendered = %q, want it to say why nothing was started", schedule.Render())
	}
}

// The order is the product manager's: highest priority first, and a lower
// priority never promoted past one because it happened to be listed earlier.
func TestSchedulerPullsInTheProductManagersOrder(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(
		beads.WorkItem{ID: "yoyodyne-low", Title: "Low", Status: "open", Priority: 3},
		beads.WorkItem{ID: "yoyodyne-high", Title: "High", Status: "open", Priority: 0},
		beads.WorkItem{ID: "yoyodyne-middle", Title: "Middle", Status: "open", Priority: 1},
	)
	harness.capacity = 1

	if _, err := (Scheduler{Open: harness.open}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	want := []string{"yoyodyne-high", "yoyodyne-middle", "yoyodyne-low"}
	if strings.Join(harness.pullOrder(), ",") != strings.Join(want, ",") {
		t.Fatalf("pull order = %v, want %v", harness.pullOrder(), want)
	}
}

// A scheduler with nothing to open is refused rather than reporting a queue it
// never read, and so is one asked to start a negative number of runs.
func TestSchedulerRefusesAPassItCannotMake(t *testing.T) {
	t.Parallel()

	if _, err := (Scheduler{}).Schedule(context.Background()); err == nil {
		t.Fatal("Schedule() error = nil, want a scheduler with no way to pull refused")
	}
	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	if _, err := (Scheduler{Open: harness.open, Limit: -1}).Schedule(context.Background()); err == nil {
		t.Fatal("Schedule() error = nil, want a negative limit refused")
	}
}

// A pull missing a collaborator is refused rather than half-used, because a
// scheduler that could not read the intake hold would be choosing work under a
// hold it never saw.
func TestSchedulerRefusesAnIncompletePull(t *testing.T) {
	t.Parallel()

	scheduler := Scheduler{Open: func(context.Context) (Pull, error) { return Pull{Capacity: 1}, nil }}
	schedule, err := scheduler.Schedule(context.Background())
	if err == nil {
		t.Fatal("Schedule() error = nil, want an incomplete pull refused")
	}
	if schedule.Stopped != ScheduleUnreadable {
		t.Fatalf("stopped = %q, want the unusable pull named", schedule.Stopped)
	}
}

// --- the fake harness the tests above pull from -------------------------------

// scheduleHarness is a whole harness for one scheduler to pull from: the
// tracker, the runs in flight, the operator's hold on intake, the recorded
// directives, and a way to run a chosen item. Everything it holds is behind one
// mutex, because a scheduler starts runs in parallel and they all report back
// into it.
type scheduleHarness struct {
	mu       sync.Mutex
	items    []beads.WorkItem
	ready    map[string]bool
	inFlight map[string]runstate.State
	pausing  map[string][]directive.Directive
	stale    []staleness.WorkItem
	staleErr error
	held     *runstate.IntakeHold
	capacity int
	openErr  error
	// onPull runs at the start of every pull with the number of pulls already
	// made, which is how a test changes the configuration under a running
	// scheduler.
	onPull func(*scheduleHarness, int)
	// run stands in for the pipeline. The default completes the item; a test
	// that cares about a refusal or a failure replaces it.
	run func(*scheduleHarness, string) (Outcome, error)

	pulls      int
	order      []string
	selections map[string]runstate.Selection
	running    int
	peak       int

	// The rendezvous a test uses to require that runs actually overlap.
	meet              int
	arrived           int
	gate              chan struct{}
	rendezvousFailure error
}

func newScheduleHarness(items ...beads.WorkItem) *scheduleHarness {
	harness := &scheduleHarness{
		items:      items,
		ready:      map[string]bool{},
		inFlight:   map[string]runstate.State{},
		pausing:    map[string][]directive.Directive{},
		selections: map[string]runstate.Selection{},
		capacity:   1,
		gate:       make(chan struct{}),
	}
	for _, item := range items {
		harness.ready[item.ID] = true
	}
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) { return h.complete(id), nil }
	return harness
}

// readyItems builds the ordinary case: open work at one priority, in the order
// given.
func readyItems(ids ...string) []beads.WorkItem {
	items := make([]beads.WorkItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, beads.WorkItem{ID: id, Title: id, Status: "open", Priority: 2})
	}
	return items
}

// developersMeet requires that many runs to be inside at once before any of them
// is let out. A test that asks for more overlap than the capacity allows
// deadlocks until the bound expires, which is reported as the failure it is.
func (h *scheduleHarness) developersMeet(count int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.meet = count
}

func (h *scheduleHarness) rendezvous() {
	h.mu.Lock()
	if h.meet == 0 {
		h.mu.Unlock()
		return
	}
	h.arrived++
	if h.arrived == h.meet {
		close(h.gate)
	}
	gate := h.gate
	h.mu.Unlock()
	select {
	case <-gate:
	case <-time.After(scheduleRendezvous):
		h.mu.Lock()
		if h.rendezvousFailure == nil {
			h.rendezvousFailure = fmt.Errorf("only %d of %d runs were ever inside at once", h.arrived, h.meet)
		}
		h.mu.Unlock()
	}
}

func (h *scheduleHarness) open(context.Context) (Pull, error) {
	h.mu.Lock()
	pulls := h.pulls
	h.pulls++
	onPull := h.onPull
	h.mu.Unlock()
	if onPull != nil {
		onPull(h, pulls)
	}
	h.mu.Lock()
	openErr, capacity := h.openErr, h.capacity
	h.mu.Unlock()
	if openErr != nil {
		return Pull{}, openErr
	}
	return Pull{
		Tracker: h, Runs: h, Intake: h, Directives: h, Staleness: h,
		Capacity: capacity, Start: h.start,
	}, nil
}

func (h *scheduleHarness) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var matching []beads.WorkItem
	for _, item := range h.items {
		if item.Status == status {
			matching = append(matching, item)
		}
	}
	return matching, nil
}

func (h *scheduleHarness) Ready(context.Context) ([]beads.WorkItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var pullable []beads.WorkItem
	for _, item := range h.items {
		if h.ready[item.ID] && item.Status == "open" {
			pullable = append(pullable, item)
		}
	}
	return pullable, nil
}

func (h *scheduleHarness) Incomplete() ([]runstate.State, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	states := make([]runstate.State, 0, len(h.inFlight))
	for _, state := range h.inFlight {
		states = append(states, state)
	}
	return states, nil
}

func (h *scheduleHarness) Held() (runstate.IntakeHold, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.held == nil {
		return runstate.IntakeHold{}, false, nil
	}
	return *h.held, true, nil
}

func (h *scheduleHarness) Pausing(workItemID string) ([]directive.Directive, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pausing[workItemID], nil
}

func (h *scheduleHarness) Stale(context.Context) ([]staleness.WorkItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.staleErr != nil {
		return nil, h.staleErr
	}
	return h.stale, nil
}

// start stands in for a reservation and a run: the item takes a slot, the
// selection is kept for the test to read, and the replaceable run decides what
// becomes of it.
func (h *scheduleHarness) start(_ context.Context, workItemID string, selection runstate.Selection) (Outcome, error) {
	h.mu.Lock()
	h.order = append(h.order, workItemID)
	h.selections[workItemID] = selection
	h.inFlight[workItemID] = runstate.State{RunID: "run-" + workItemID, WorkItemID: workItemID, Status: runstate.StatusRunning}
	h.running++
	if h.running > h.peak {
		h.peak = h.running
	}
	run := h.run
	h.mu.Unlock()

	h.rendezvous()
	outcome, err := run(h, workItemID)

	h.mu.Lock()
	delete(h.inFlight, workItemID)
	h.running--
	h.mu.Unlock()
	return outcome, err
}

// complete is what an ordinary run does to the tracker: the item closes and
// leaves the queue.
func (h *scheduleHarness) complete(workItemID string) Outcome {
	h.close(workItemID)
	return Outcome{WorkItemID: workItemID, Status: runstate.StatusSucceeded, WorkItemClosed: true}
}

func (h *scheduleHarness) close(workItemID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.ready, workItemID)
	for index := range h.items {
		if h.items[index].ID == workItemID {
			h.items[index].Status = "closed"
		}
	}
}

func (h *scheduleHarness) pullOrder() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.order...)
}

func (h *scheduleHarness) selectionFor(workItemID string) runstate.Selection {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.selections[workItemID]
}

// --- the real harness the end-to-end test pulls from --------------------------

// realScheduleHarness runs the actual pipeline: one Git repository, one worktree
// root, one run state store, and a fresh provider per run. The provider is per
// run rather than shared because that is what a run genuinely is — its own
// process — and because two runs sharing one fake would be a data race the
// harness itself does not have.
type realScheduleHarness struct {
	*scheduleHarness
	t            *testing.T
	repository   string
	worktreeRoot string
	store        *runstate.Store
	directives   *runstate.DirectiveStore
	holds        *runstate.OperatorHoldStore
	intake       *runstate.IntakeHoldStore
}

func newRealScheduleHarness(t *testing.T, capacity int, ids ...string) *realScheduleHarness {
	t.Helper()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	harness := &realScheduleHarness{
		scheduleHarness: newScheduleHarness(readyItems(ids...)...),
		t:               t,
		repository:      pipelineRepository(t),
		worktreeRoot:    filepath.Join(t.TempDir(), "worktrees"),
		store:           store,
		directives:      newDirectiveStore(t),
		holds:           newOperatorHoldStore(t),
		intake:          newIntakeHoldStore(t),
	}
	harness.capacity = capacity
	return harness
}

func (h *realScheduleHarness) open(context.Context) (Pull, error) {
	h.mu.Lock()
	h.pulls++
	capacity := h.capacity
	h.mu.Unlock()
	return Pull{
		Tracker: h, Runs: h.store, Intake: h.intake, Directives: h.directives,
		Capacity: capacity, Start: h.start,
	}, nil
}

// start builds one real pipeline over the shared repository and run state and
// runs the item through it, exactly as the command does.
func (h *realScheduleHarness) start(ctx context.Context, workItemID string, selection runstate.Selection) (Outcome, error) {
	h.mu.Lock()
	h.selections[workItemID] = selection
	h.mu.Unlock()

	provider := roleBackend(func(request backend.RunRequest) error {
		h.rendezvous()
		return os.WriteFile(filepath.Join(request.WorkingDirectory, workItemID+".txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := newSharedPipeline(h.t, h.repository, h.worktreeRoot, h.store, h, provider, []string{"exit 0"})
	pipeline.Config.Execution.MaxConcurrentDevelopers = h.scheduleHarness.capacity
	pipeline.Config.Approvals.Integration = domain.ApprovalAutomatic
	pipeline.Config.Agents["developer"] = config.AgentConfig{
		Role: domain.RoleDeveloper, Backend: domain.BackendClaudeCode, Model: testDeveloperModel,
		Instances: h.scheduleHarness.capacity,
	}
	pipeline.Config.Agents["reviewer"] = config.AgentConfig{
		Role: domain.RoleReviewer, Backend: domain.BackendClaudeCode, Model: testReviewerModel,
		Instances: h.scheduleHarness.capacity,
	}
	pipeline.Reviewer = review.Reviewer{Backend: provider, Model: testReviewerModel}
	pipeline.Directives = h.directives
	pipeline.Holds = h.holds
	pipeline.Intake = h.intake
	// Every run needs an identifier of its own; the shared fixture's constant one
	// would have two concurrent runs claiming the same lease.
	pipeline.NewRunID = runstate.NewRunID
	pipeline.Selection = selection

	outcome, err := pipeline.Run(ctx, workItemID)
	h.close(workItemID)
	return outcome, err
}

// Show, Claim, RecordOutcome, Block, and Complete are the tracker the real
// pipeline drives. They are the fake harness's items behind its own mutex,
// because three runs are calling them at once.
func (h *realScheduleHarness) Show(_ context.Context, id string) (beads.WorkItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, item := range h.items {
		if item.ID == id {
			return item, nil
		}
	}
	return beads.WorkItem{}, fmt.Errorf("no work item %s", id)
}

func (h *realScheduleHarness) Claim(_ context.Context, id string) (beads.WorkItem, error) {
	return h.setStatus(id, "in_progress")
}

func (h *realScheduleHarness) RecordOutcome(_ context.Context, id, _ string) (beads.WorkItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.itemLocked(id)
}

func (h *realScheduleHarness) Block(_ context.Context, id, _ string) (beads.WorkItem, error) {
	return h.setStatus(id, "blocked")
}

func (h *realScheduleHarness) Complete(_ context.Context, id, _ string) (beads.WorkItem, error) {
	return h.setStatus(id, "closed")
}

func (h *realScheduleHarness) setStatus(id, status string) (beads.WorkItem, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for index := range h.items {
		if h.items[index].ID == id {
			h.items[index].Status = status
			return h.items[index], nil
		}
	}
	return beads.WorkItem{}, fmt.Errorf("no work item %s", id)
}

func (h *realScheduleHarness) itemLocked(id string) (beads.WorkItem, error) {
	for _, item := range h.items {
		if item.ID == id {
			return item, nil
		}
	}
	return beads.WorkItem{}, fmt.Errorf("no work item %s", id)
}
