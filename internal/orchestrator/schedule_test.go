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
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
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

// The other half of the criterion, and the case concurrency is what makes
// reachable at all: two runs started from the same base, both approved, both
// changing the same line. One of them promotes; the other finds its target moved,
// cannot replay onto it, and is blocked with everything preserved. Nothing is
// forced, and the target branch carries exactly the winner's change.
//
// Which of the two wins is decided by the promotion lease and is deliberately
// not asserted: what matters is that exactly one did and the loser was stopped
// rather than resolved.
func TestSchedulerBlocksTheLoserOfAConflictRatherThanForcingIt(t *testing.T) {
	t.Parallel()

	harness := newRealScheduleHarness(t, 2, "yoyodyne-alpha", "yoyodyne-beta")
	// Both developers are inside before either returns, so both runs were created
	// from the same base commit: that is what makes the second promotion a
	// contended one rather than a fast-forward onto work it already had.
	harness.developersMeet(2)
	harness.develop = func(workItemID, worktree string) error {
		return os.WriteFile(filepath.Join(worktree, "shared.txt"), []byte(workItemID+" wrote this\n"), 0o600)
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if harness.rendezvousFailure != nil {
		t.Fatalf("the developers never overlapped, so nothing contended: %v", harness.rendezvousFailure)
	}
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %d run(s), want both items pulled: %s", len(schedule.Started), schedule.Render())
	}

	var promoted, blocked *Started
	for index := range schedule.Started {
		started := &schedule.Started[index]
		switch {
		case started.Outcome.Integration != nil:
			promoted = started
		case started.Outcome.Blocked:
			blocked = started
		}
	}
	if promoted == nil || blocked == nil {
		t.Fatalf("outcomes = %s, want exactly one promotion and one blocked run", schedule.Render())
	}
	if blocked.Failure == "" {
		t.Fatalf("%s was blocked without a reason recorded", blocked.WorkItemID)
	}

	// The target branch carries the winner's change and nothing of the loser's:
	// a promotion that had been forced through would show the other content, and
	// one that had been resolved would show both.
	content, err := os.ReadFile(filepath.Join(harness.repository, "shared.txt"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != promoted.WorkItemID+" wrote this\n" {
		t.Fatalf("shared.txt = %q, want exactly what %s promoted", content, promoted.WorkItemID)
	}

	// The blocked run keeps everything it had, which is what makes the conflict
	// somebody's to decide rather than something the harness threw away.
	if blocked.Outcome.WorktreePath == "" || blocked.Outcome.WorktreeRemoved {
		t.Fatalf("blocked run = %#v, want its worktree preserved for whoever picks the conflict up", blocked.Outcome)
	}
	if blocked.Outcome.Branch == "" || blocked.Outcome.BranchRemoved {
		t.Fatalf("blocked run = %#v, want its branch preserved", blocked.Outcome)
	}
	// And the blocker reached the tracker, which is where the decision is owed.
	item, err := harness.Show(context.Background(), blocked.WorkItemID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if item.Status != "blocked" {
		t.Fatalf("%s status = %q, want the conflict recorded on the work item", item.ID, item.Status)
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
	// A held pass never reads the queue, so it says nothing about it rather than
	// reporting the zeroes it did not read. "0 admitted items" over a backlog
	// nobody looked at would be worse than saying nothing.
	if schedule.BacklogRead {
		t.Fatalf("schedule = %#v, want no claim about a backlog a held pass never read", schedule)
	}
	if strings.Contains(schedule.Render(), "backlog at the last pull") {
		t.Fatalf("rendered = %q, want no backlog counts from a pass that stopped before reading it", schedule.Render())
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

// The mis-selection this guard exists for, replayed: the architect's
// brief-promotion item, admitted and pullable and reported by the tracker as
// ready, with a free developer slot and an unattended scheduler. What it cost
// the first time was a whole run and two review rounds producing a correctly
// refused empty diff — and those rounds count against the item's cap, so a
// second mis-selection escalates work nobody ever started.
//
// So: nothing is started, and the pass says which item it passed over and why.
// Saying why is half the criterion. The item is not waiting for anything and
// never becomes pullable, so a pass that silently counted it among the unready
// would report a queue that is about to move when it is not.
func TestSchedulerNeverSelectsWorkAConversationCarries(t *testing.T) {
	t.Parallel()

	promotion := beads.WorkItem{
		ID: "yoyodyne-ifd.138", Title: "Promote the brief", Status: "open", Priority: 0,
		Executor: domain.WorkItemExecutorConversation,
	}
	harness := newScheduleHarness(promotion)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing selected for a run that cannot execute it: %s", schedule.Started, schedule.Render())
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != promotion.ID {
		t.Fatalf("deferred = %#v, want the item named rather than counted among the unready", schedule.Deferred)
	}
	if !strings.Contains(schedule.Deferred[0].Reason, "conversation") {
		t.Fatalf("deferred reason = %q, want what carries the work named", schedule.Deferred[0].Reason)
	}
	// The item is still admitted work in the product manager's order; what it is
	// not is pullable.
	if schedule.Admitted != 1 || schedule.Pullable != 0 {
		t.Fatalf("backlog = %d admitted, %d pullable, want it queued and unpullable", schedule.Admitted, schedule.Pullable)
	}
	if !strings.Contains(schedule.Render(), promotion.ID+" was not pulled") {
		t.Fatalf("rendered = %q, want the pass readable by an operator", schedule.Render())
	}
}

// The selection this guard exists for, replayed exactly: a queue drained to the
// bottom, and the Codex backend sitting there — open, at the lowest priority,
// reported by the tracker as ready, with a free developer slot and an unattended
// watch session. That is what happened on 2026-08-27, and it cost $34.38 for a
// run that failed at work a scope decision had put off the critical path months
// earlier. The deferral was expressed as priority 4, which reads as "last" to
// everything that pulls, so nothing about the pull was wrong.
//
// Parked, the same pass selects nothing. Draining to the bottom now finds
// nothing at the bottom, and the pass says which item it passed over and that
// releasing it is a decision rather than a wait.
func TestSchedulerNeverSelectsParkedWorkHoweverFarTheQueueDrains(t *testing.T) {
	t.Parallel()

	codex := beads.WorkItem{
		ID: "yoyodyne-ifd.6", Title: "Add the thin Codex developer and reviewer backend",
		Status: "open", Priority: 4,
		Parking: "off the critical path by the scope decision; released when a second backend is wanted",
	}
	harness := newScheduleHarness(codex)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want a drained queue to select nothing parked: %s", schedule.Started, schedule.Render())
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want the pass to have drained rather than stopped for anything else", schedule.Stopped)
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != codex.ID {
		t.Fatalf("deferred = %#v, want the parked item named rather than counted among the unready", schedule.Deferred)
	}
	// Half the criterion is saying why. Parking is not a wait: an operator told
	// only that it was not pulled would go looking for a blocker to clear.
	reason := schedule.Deferred[0].Reason
	for _, required := range []string{"parked", "however far the queue drains", "off the critical path by the scope decision"} {
		if !strings.Contains(reason, required) {
			t.Fatalf("deferred reason = %q, want it to contain %q", reason, required)
		}
	}
	// It is still admitted work in the product manager's order; what it is not is
	// pullable.
	if schedule.Admitted != 1 || schedule.Pullable != 0 {
		t.Fatalf("backlog = %d admitted, %d pullable, want it queued and unpullable", schedule.Admitted, schedule.Pullable)
	}
	if !strings.Contains(schedule.Render(), codex.ID+" was not pulled") {
		t.Fatalf("rendered = %q, want the pass readable by an operator", schedule.Render())
	}
}

// Parking one item does not stop the pass: the slot goes to the next thing in
// the order rather than being spent on it or idled beside it. This is the other
// half of the parked set staying out of reach — a scheduler that stalled on a
// parked item would be a worse failure than the one it replaced.
func TestSchedulerCarriesOnPastParkedWork(t *testing.T) {
	t.Parallel()

	parked := beads.WorkItem{
		ID: "yoyodyne-ifd.77", Title: "First-class external configuration",
		Status: "open", Priority: 0,
		Parking: "deferred until team mode is scoped",
	}
	ordinary := beads.WorkItem{ID: "yoyodyne-ifd.188", Title: "Parked work is unschedulable", Status: "open", Priority: 1}
	harness := newScheduleHarness(parked, ordinary)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != ordinary.ID {
		t.Fatalf("started = %#v, want the pass to carry on past the parked item: %s", schedule.Started, schedule.Render())
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want the pass to have drained", schedule.Stopped)
	}
}

// A marked item does not stop the pass: the slot it would have taken goes to the
// next thing in the order rather than being spent on it or idled beside it.
func TestSchedulerCarriesOnPastWorkAConversationCarries(t *testing.T) {
	t.Parallel()

	promotion := beads.WorkItem{
		ID: "yoyodyne-ifd.138", Title: "Promote the brief", Status: "open", Priority: 0,
		Executor: domain.WorkItemExecutorConversation,
	}
	ordinary := beads.WorkItem{ID: "yoyodyne-ifd.144", Title: "Mark them", Status: "open", Priority: 1}
	harness := newScheduleHarness(promotion, ordinary)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != ordinary.ID {
		t.Fatalf("started = %#v, want the pass to carry on to the next item: %s", schedule.Started, schedule.Render())
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want the pass to have drained", schedule.Stopped)
	}
}

// The failure this guard exists for, replayed: an epic and the child that
// carries its execution both sitting ready, and a pass with room for both. The
// tracker reports both as pullable and the reservation sees two different items,
// so nothing downstream would have stopped two developers making the same change
// -- and the second of them would have met the first at integration.
func TestSchedulerLeavesAnEpicItsOpenChildrenAlreadyCover(t *testing.T) {
	t.Parallel()

	epic := beads.WorkItem{ID: "yoyodyne-epic", Title: "Rewrite the README", Status: "open", Priority: 1}
	child := beads.WorkItem{ID: "yoyodyne-epic.2", Title: "Rewrite the README", Status: "open", Priority: 1, Parent: epic.ID}
	harness := newScheduleHarness(epic, child)
	harness.capacity = 2

	// One pass with room for both is the whole of the failure: the epic is first
	// in the order, so a scheduler that did not know it was a container would have
	// started it — and started the child beside it.
	schedule, err := Scheduler{Open: harness.open, Limit: 1}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != child.ID {
		t.Fatalf("started = %#v, want only the child that carries the work: %s", schedule.Started, schedule.Render())
	}
	// The skip is named against the item like any other selection decision, and
	// it names where the execution went: a container left in the queue is only
	// legible if the report says what is covering it.
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != epic.ID {
		t.Fatalf("deferred = %#v, want the covered epic named rather than silently dropped", schedule.Deferred)
	}
	if !strings.Contains(schedule.Deferred[0].Reason, child.ID) {
		t.Fatalf("deferred reason = %q, want the child that covers it named", schedule.Deferred[0].Reason)
	}
	if !strings.Contains(schedule.Render(), epic.ID+" was not pulled") {
		t.Fatalf("rendered = %q, want the skip readable by an operator", schedule.Render())
	}
}

// The same guard against a shape a tracker can hand it. The pair above states
// its parentage as a field; this one states it only as a parent-child edge,
// which is what the tracker's own export does — carrying no parent field on any
// item in it — and a guard that read only the field would see such a store as a
// backlog with nothing decomposed in it.
//
// The identifiers are yoyodyne-ifd.121's because that is the decomposition the
// guard was written for, not because this reading is what failed on it: nothing
// keyed on parentage was in the tree when those two runs were started. See
// docs/diagnoses/yoyodyne-ifd-273-121-double-run-mechanism.md.
func TestSchedulerLeavesAnEpicCoveredByAChildTheTrackerStatesAsAnEdge(t *testing.T) {
	t.Parallel()

	epic := beads.WorkItem{
		ID: "yoyodyne-ifd.121", Title: "Docs architecture: a readable README", Status: "open", Priority: 1,
	}
	child := beads.WorkItem{
		ID: "yoyodyne-ifd.121.2", Title: "Execute the README split", Status: "open", Priority: 1,
		Dependencies: []beads.Dependency{
			{IssueID: "yoyodyne-ifd.121.2", ID: epic.ID, Type: "parent-child"},
		},
	}
	harness := newScheduleHarness(epic, child)
	harness.capacity = 2

	schedule, err := Scheduler{Open: harness.open, Limit: 1}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if child.Parent != "" {
		t.Fatalf("child parent field = %q, want the shape the tracker states, which sets none", child.Parent)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != child.ID {
		t.Fatalf("started = %#v, want only the child that carries the work: %s", schedule.Started, schedule.Render())
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != epic.ID {
		t.Fatalf("deferred = %#v, want the covered epic named rather than silently dropped", schedule.Deferred)
	}
	if !strings.Contains(schedule.Deferred[0].Reason, child.ID) {
		t.Fatalf("deferred reason = %q, want the child that covers it named", schedule.Deferred[0].Reason)
	}
}

// The shape of the double-run as it actually happened, rather than of the cause
// that was recorded for it. From the two runs' own durable state: the scheduler
// pulled yoyodyne-ifd.121.2 at 2026-08-20T06:05:34Z as position 1 of 48, and
// twenty minutes later pulled the epic yoyodyne-ifd.121 at 06:25:43Z as position
// 2, while that first run was still going — it ended at 06:47:15Z. Both were
// started from the same base commit, which carried nothing keyed on parentage in
// either direction; the coverage guard landed 12h43m after that second pull. So
// the failure was the absence of a guard, and this is what the one that exists
// now does when handed that pull.
//
// Three things about that pull have to be together for it to be the observed
// one, and each is a separate way through the guard:
//
//   - the child is claimed rather than queued, so a reading of the queue alone
//     cannot see it;
//   - its parentage is stated only as an edge, so a reading of the parent field
//     alone cannot see it; and
//   - the epic carries a parent-child edge of its own, pointing up at the root
//     epic it belongs to, so a reading that took an edge's ends the other way
//     round would defer the child and run the epic — the double-run over again
//     with the two runs swapped.
func TestSchedulerLeavesTheEpicWhoseClaimedChildIsTheOneThe121DoubleRunStarted(t *testing.T) {
	t.Parallel()

	const root = "yoyodyne-ifd"
	epic := beads.WorkItem{
		ID: "yoyodyne-ifd.121", Title: "Docs architecture: a readable README", Status: "open", Priority: 2,
		Dependencies: []beads.Dependency{
			{IssueID: "yoyodyne-ifd.121", ID: root, Type: "parent-child"},
		},
	}
	child := beads.WorkItem{
		ID: "yoyodyne-ifd.121.2", Title: "Execute the README split", Status: "in_progress", Priority: 2,
		Dependencies: []beads.Dependency{
			{IssueID: "yoyodyne-ifd.121.2", ID: epic.ID, Type: "parent-child"},
		},
	}
	harness := newScheduleHarness(epic, child)
	harness.capacity = 2
	harness.inFlight[child.ID] = runstate.State{
		RunID: "run-44940ec3d71cdbc6e47e39745fedf15f", WorkItemID: child.ID, Status: runstate.StatusRunning,
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	// The whole of the failure: a second developer run of the same scope, beside
	// the one already going.
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing started beside the run already carrying this scope: %s",
			schedule.Started, schedule.Render())
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != epic.ID {
		t.Fatalf("deferred = %#v, want the epic named as covered by the run in flight under it", schedule.Deferred)
	}
	if !strings.Contains(schedule.Deferred[0].Reason, child.ID) {
		t.Fatalf("deferred reason = %q, want the child that covers it named", schedule.Deferred[0].Reason)
	}
	// And not the other way round, which is what a mirrored reading of the edges
	// would produce from this same pull.
	for _, deferred := range schedule.Deferred {
		if deferred.WorkItemID == child.ID {
			t.Fatalf("deferred = %#v, want the child left alone: it is the work, and its own edge points up at %s",
				schedule.Deferred, epic.ID)
		}
	}
}

// What covers an item is an unfinished child, whatever slice of the tracker it
// is in. A blocked child is work somebody will release; a claimed one is a run
// in flight over the very same change, and it is the case a reading of the queue
// alone would miss, because a claimed item has left the queue.
func TestSchedulerLeavesAnEpicItsUnfinishedChildrenCoverWhereverTheySit(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		childStatus string
	}{
		{name: "a child waiting on a blocker", childStatus: "blocked"},
		{name: "a child somebody is already running", childStatus: "in_progress"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			epic := beads.WorkItem{ID: "yoyodyne-epic", Title: "Rewrite the README", Status: "open", Priority: 1}
			child := beads.WorkItem{ID: "yoyodyne-epic.2", Title: "Rewrite the README", Status: test.childStatus, Priority: 1, Parent: epic.ID}
			other := beads.WorkItem{ID: "yoyodyne-other", Title: "Something else", Status: "open", Priority: 2}
			harness := newScheduleHarness(epic, child, other)
			harness.capacity = 2

			schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != other.ID {
				t.Fatalf("started = %#v, want the epic left to its child: %s", schedule.Started, schedule.Render())
			}
			if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != epic.ID {
				t.Fatalf("deferred = %#v, want the epic named as covered", schedule.Deferred)
			}
			if !strings.Contains(schedule.Deferred[0].Reason, child.ID) {
				t.Fatalf("deferred reason = %q, want the child that covers it named", schedule.Deferred[0].Reason)
			}
		})
	}
}

// Coverage is read from the tracker at every pull rather than remembered, so it
// is a state an item is in rather than a mark it carries: an item stops being
// covered when its last unfinished child leaves, and is ordinary work again.
// What the guard does not do is retire an item permanently -- a parent whose one
// child has closed while it stayed open is common, and holding it back forever
// would strand real work behind a decomposition that finished.
func TestSchedulerPullsAContainerOnceItsChildrenHaveLeftTheBacklog(t *testing.T) {
	t.Parallel()

	epic := beads.WorkItem{ID: "yoyodyne-epic", Title: "Rewrite the README", Status: "open", Priority: 1}
	child := beads.WorkItem{ID: "yoyodyne-epic.2", Title: "Rewrite the README", Status: "open", Priority: 1, Parent: epic.ID}
	harness := newScheduleHarness(epic, child)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if pulled := harness.pullOrder(); len(pulled) != 2 || pulled[0] != child.ID || pulled[1] != epic.ID {
		t.Fatalf("pulled = %v, want the child first and the parent only once its run had closed it: %s",
			pulled, schedule.Render())
	}
	// The deferral is what the pass said while the epic was covered, and it is
	// said once: the item is skipped for as long as the cover lasts, not reported
	// once per pull that skips it.
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != epic.ID {
		t.Fatalf("deferred = %#v, want the coverage said once rather than once per pull", schedule.Deferred)
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
	// The unready item is accounted for by the counts rather than by a line of its
	// own. Both halves matter: naming every unready item would print a line per
	// backlog entry on every pass, and reporting nothing at all would leave a
	// backlog full of unpullable work indistinguishable from an empty one.
	if len(schedule.Deferred) != 0 {
		t.Fatalf("deferred = %#v, want an unready item left to the counts", schedule.Deferred)
	}
	// The counts are the last pull's, which is deliberately the reading an
	// operator wants from a finished pass: the ready item ran and left the queue,
	// so what is left is the one item nothing can pull -- which is the answer to
	// "why did it stop with work still admitted?".
	if schedule.Admitted != 1 || schedule.Pullable != 0 {
		t.Fatalf("admitted = %d, pullable = %d, want the item nothing can pull still counted",
			schedule.Admitted, schedule.Pullable)
	}
	if !strings.Contains(schedule.Render(), "1 admitted item(s), 0 of them ready to pull") {
		t.Fatalf("rendered = %q, want the backlog counted in what an operator reads", schedule.Render())
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
	// A run in flight is not a deferral: the item was chosen already, by whoever
	// is running it, and `yoyo status` is where that run is read. What this pass
	// owes is the slot arithmetic that explains its own choices.
	if len(schedule.Deferred) != 0 {
		t.Fatalf("deferred = %#v, want a run in flight left to the slot counts", schedule.Deferred)
	}
	if schedule.Occupied < 1 {
		t.Fatalf("occupied = %d, want the run another process holds counted against capacity", schedule.Occupied)
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
	// The stop reason is the whole account here: no item is named, because what
	// kept this one out was the machine being full rather than anything about it.
	if len(schedule.Started) != 0 || len(schedule.Deferred) != 0 || schedule.Stopped != ScheduleCapacityFull {
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
	// An empty backlog that was actually read is a different fact from one that
	// was not, and the emptiness is only reported because this pass looked.
	if !schedule.BacklogRead || schedule.Admitted != 0 {
		t.Fatalf("schedule = %#v, want an emptiness this pass actually read", schedule)
	}
	if !strings.Contains(schedule.Render(), "0 admitted item(s), 0 of them ready to pull") {
		t.Fatalf("rendered = %q, want the empty backlog counted", schedule.Render())
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

// --- watching ------------------------------------------------------------------

// The whole of what watching is: an empty queue does not end the pass. The
// session waits out its interval, reads the queue again, and starts work that
// was admitted while it was waiting — with nothing between the readings but the
// wait, because nothing about the queue is cached.
func TestWatchingPullsWorkAdmittedWhileItWasWaiting(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	sessions := &recordedSessions{}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 1 {
			h.admit(readyItems("yoyodyne-late")...)
		}
		// The second wait is the queue empty again with the late item run, which
		// is where the operator stops the session.
		return sleeps < 2
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-late" {
		t.Fatalf("started = %#v, want the item admitted between polls pulled", schedule.Started)
	}
	if !schedule.Watched || schedule.Polls != 2 {
		t.Fatalf("schedule = watched %v after %d poll(s), want a session that waited twice", schedule.Watched, schedule.Polls)
	}
	// A drain would have stopped at the first empty queue and said so. The stop
	// reason a watch ends on is the operator, never an empty queue.
	if schedule.Stopped != ScheduleCancelled {
		t.Fatalf("stopped = %q, want the session ended by its operator rather than by an empty queue", schedule.Stopped)
	}
	// Idle is said once and stopping is said at the end, so a session that waited
	// twice over one quiet queue does not write two lines about it.
	want := []runstate.WatchState{runstate.WatchWatching, runstate.WatchIdle, runstate.WatchResumed, runstate.WatchIdle, runstate.WatchStopped}
	if got := sessions.states(); !sameStates(got, want) {
		t.Fatalf("recorded states = %v, want %v", got, want)
	}
	if reason := sessions.said(runstate.WatchIdle); !strings.Contains(reason, "backlog is empty") {
		t.Fatalf("idle reason = %q, want it to say what the session found", reason)
	}
}

// The state that misled an operator three times on 2026-09-01, replayed: a
// session idle on one developer slot while a run works on the other, over a
// queue whose only unstarted work is the architect's to carry in conversation.
//
// What it said then was that nothing among the ready items was startable that
// the session had not already tried, and that the next move was the product
// manager's, nothing being chosen until ready work was admitted. Both halves
// were false. The line had not stopped — a run was in flight — and no admission
// would have moved any of the three items, because no run will ever start one.
//
// So the idle line says what the poll actually found: the runs going, the items
// it passed over, and the conversation each of them is carried in.
func TestAnIdleWatchSaysWhatItPassedOverAndWhichConversationCarriesIt(t *testing.T) {
	t.Parallel()

	architects := []beads.WorkItem{
		{ID: "yoyodyne-ifd.212", Title: "Amend the invariants", Status: "open", Priority: 1,
			Executor: domain.ConversationWith(domain.RoleArchitect)},
		{ID: "yoyodyne-ifd.203", Title: "Settle the design", Status: "open", Priority: 2,
			Executor: domain.ConversationWith(domain.RoleArchitect)},
		{ID: "yoyodyne-ifd.162", Title: "Record the decision", Status: "open", Priority: 3,
			Executor: domain.ConversationWith(domain.RoleArchitect)},
	}
	harness := newScheduleHarness(architects...)
	harness.capacity = 2
	// The other slot: a run somebody else's process is carrying, which is what
	// makes the line moving rather than stopped.
	harness.inFlight["yoyodyne-ifd.236"] = runstate.State{
		RunID: "run-236", WorkItemID: "yoyodyne-ifd.236", Status: runstate.StatusRunning,
	}
	sessions := &recordedSessions{}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}.
		Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing started for work no run can carry", schedule.Started)
	}
	idle, recorded := sessions.entered(runstate.WatchIdle)
	if !recorded {
		t.Fatal("no idle transition was recorded, so nothing said what the session found")
	}
	for _, want := range []string{
		"1 run in flight",
		"3 items passed over, of 3 admitted",
		"carried in conversation (architect: yoyodyne-ifd.212, yoyodyne-ifd.203, yoyodyne-ifd.162)",
	} {
		if !strings.Contains(idle.reason, want) {
			t.Fatalf("idle reason = %q, want it to say %q", idle.reason, want)
		}
	}
	// The sentence that was read as a stopped line, gone rather than reworded.
	if strings.Contains(idle.reason, "this session has not already tried") {
		t.Fatalf("idle reason = %q, want what was found rather than a count of what was not", idle.reason)
	}
	// The two facts whose move follows is derived from. Without them a reader is
	// back to the clause that named the one person who could do nothing about it.
	if idle.running != 1 {
		t.Fatalf("idle running = %d, want the run in flight recorded so the line does not read as stopped", idle.running)
	}
	if want := domain.ConversationWith(domain.RoleArchitect); idle.executor != want {
		t.Fatalf("idle executor = %q, want %q, the conversation that carries the work passed over", idle.executor, want)
	}
}

// The idle line names items and counts runs, and it is said again when either
// changes — so what it must not do is say itself again when neither has. A
// session polling an unchanged queue writes one line however many polls it
// makes, which is what the reporting promises a reader for a quiet night.
func TestAnUnchangedIdlePollSaysItselfOnce(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(beads.WorkItem{
		ID: "yoyodyne-ifd.212", Title: "Amend the invariants", Status: "open", Priority: 1,
		Executor: domain.ConversationWith(domain.RoleArchitect),
	})
	sessions := &recordedSessions{}
	// Four polls over a queue nothing touches between them.
	harness.onSleep = func(*scheduleHarness, int) bool { return harness.sleeps < 4 }

	if _, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}).
		Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	idles := 0
	for _, state := range sessions.states() {
		if state == runstate.WatchIdle {
			idles++
		}
	}
	if idles != 1 {
		t.Fatalf("recorded %d idle transitions across %d polls, want the unchanged account said once", idles, harness.sleeps)
	}
}

// The other side of the same line: a poll with nothing going and nothing
// anybody carries says so, and names nobody it should not. The conversation and
// the runs are what displace the admission clause, so a session that has neither
// leaves it where it was.
func TestAnIdleWatchOverAnEmptyBacklogNamesNoConversationAndNoRun(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	sessions := &recordedSessions{}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	if _, err := (Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}).
		Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	idle, recorded := sessions.entered(runstate.WatchIdle)
	if !recorded {
		t.Fatal("no idle transition was recorded over an empty backlog")
	}
	if idle.reason != "the backlog is empty" {
		t.Fatalf("idle reason = %q, want the empty backlog said as itself", idle.reason)
	}
	if idle.running != 0 || idle.executor != "" {
		t.Fatalf("idle = %d run(s), executor %q, want neither over a queue with nothing in it", idle.running, idle.executor)
	}
}

// The intake hold is a brake rather than a stop for a session that is watching:
// it polls, chooses nothing, and resumes in place when the operator releases it.
// A drain still returns, because a drain is a command somebody is waiting on.
func TestWatchingBrakesOnHeldIntakeAndResumesWhenItIsReleased(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.held = &runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        time.Now().UTC(),
		Reason:        "the queue is being reordered",
	}
	sessions := &recordedSessions{}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 1 {
			h.release()
		}
		return sleeps < 2
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-one" {
		t.Fatalf("started = %#v, want the released session to pull the item that was waiting", schedule.Started)
	}
	want := []runstate.WatchState{runstate.WatchWatching, runstate.WatchBraked, runstate.WatchResumed, runstate.WatchIdle, runstate.WatchStopped}
	if got := sessions.states(); !sameStates(got, want) {
		t.Fatalf("recorded states = %v, want %v", got, want)
	}
	if reason := sessions.said(runstate.WatchBraked); !strings.Contains(reason, "the queue is being reordered") {
		t.Fatalf("braked reason = %q, want the operator's own reason carried into it", reason)
	}
}

// The guard the day's own history asked for. A run that fails before it starts
// leaves the item exactly as ready as it was, so a loop with no memory would
// pull it again every interval forever. It is left alone until something about
// the item changes, and tried again as soon as something does.
func TestWatchingLeavesAFailedStartAloneUntilTheItemChanges(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-preflight")...)
	// The brake is out of the way here: what this is about is one item failing
	// repeatedly, which is exactly the case the brake is not for.
	harness.blockedRuns = 0
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		// Nothing is claimed and nothing is recorded: the item is left where it
		// was, which is what makes it ready again at the next reading.
		return Outcome{WorkItemID: id}, errors.New("validate work item context: acceptance criteria are required")
	}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		switch sleeps {
		case 3:
			// Three quiet polls in, the development manager rewrites the item.
			h.amend("yoyodyne-preflight", "now with acceptance criteria")
		case 6:
			return false
		}
		return true
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if starts := len(harness.pullOrder()); starts != 2 {
		t.Fatalf("the item was started %d time(s) over six polls, want once before it was amended and once after: %v",
			starts, harness.pullOrder())
	}
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %#v, want the two attempts accounted for", schedule.Started)
	}
	for _, started := range schedule.Started {
		if started.Failure == "" {
			t.Fatalf("%s = %#v, want the failed start recorded as failed", started.WorkItemID, started)
		}
	}
}

// What pauses an item for a directive is a person, and the whole point of a
// session that outlives them answering is that it notices. The directive is
// re-read at every pull, so an item deferred all night is pulled at the poll
// after it is resolved — and named once in the report rather than once a minute.
func TestWatchingPullsAnItemOnceItsDirectiveIsResolved(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-directed")...)
	harness.pausing["yoyodyne-directed"] = []directive.Directive{{
		ID:         "directive-1",
		Kind:       directive.KindArtifact,
		Text:       "the goal is being rewritten",
		Unresolved: "which goal this item now serves",
	}}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 2 {
			h.mu.Lock()
			delete(h.pausing, "yoyodyne-directed")
			h.mu.Unlock()
		}
		return sleeps < 4
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-directed" {
		t.Fatalf("started = %#v, want the item pulled once its directive was resolved", schedule.Started)
	}
	// Deferred is a report rather than a decision, and an item paused across four
	// polls is one line in it.
	if len(schedule.Deferred) != 1 {
		t.Fatalf("deferred = %#v, want the pause said once rather than once per poll", schedule.Deferred)
	}
}

// A drain is unchanged by any of this: it tries an item once and stops when
// nothing is left, whatever the item does afterwards.
func TestDrainingStillStopsWhenNothingMoreIsReady(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		// The item stays ready, which is what would make a watch look at it
		// again. A drain never does.
		return Outcome{WorkItemID: id}, errors.New("the run could not be started")
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want a drain to end on an empty queue", schedule.Stopped)
	}
	if starts := len(harness.pullOrder()); starts != 1 {
		t.Fatalf("the item was started %d time(s), want a drain to try it once", starts)
	}
	if schedule.Watched || schedule.Polls != 0 {
		t.Fatalf("schedule = watched %v after %d poll(s), want a drain that waited for nothing", schedule.Watched, schedule.Polls)
	}
}

// The failure-storm brake: runs blocking one after another with nothing landing
// between them holds intake, and it stays held. What it places is the operator's
// own switch, so what an operator arriving at a stopped line finds is one thing
// to understand and one thing to lift.
func TestWatchingHoldsIntakeWhenRunsKeepBlocking(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three", "yoyodyne-four")...)
	harness.blockedRuns = 3
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		// A blocked run leaves the tracker with the item blocked, which is what
		// takes it out of the ready queue.
		h.retire(id)
		return Outcome{WorkItemID: id, Status: runstate.StatusFailed, Blocked: true}, nil
	}
	sessions := &recordedSessions{}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Braked == nil {
		t.Fatalf("schedule = %#v, want the brake to have held intake", schedule)
	}
	if schedule.BlockedInARow < 3 {
		t.Fatalf("blocked in a row = %d, want the storm that tripped the brake counted", schedule.BlockedInARow)
	}
	// The fourth item is what says the line actually stopped: three runs blocked,
	// and the brake held intake before the queue got to it.
	if len(schedule.Started) != 3 {
		t.Fatalf("started = %d run(s), want the brake to have stopped the line at three: %s", len(schedule.Started), schedule.Render())
	}
	if _, held, _ := harness.Held(); !held {
		t.Fatal("intake is not held, want the brake to have placed the operator's own switch")
	}
	if reason := sessions.said(runstate.WatchBraked); !strings.Contains(reason, "blocking") {
		t.Fatalf("braked reason = %q, want it to say the harness held intake after runs kept blocking", reason)
	}
}

// One item failing is not a storm. A run that blocks between runs that land
// leaves the count where it started, because what the brake is for is a broken
// machine rather than a broken item.
func TestWatchingDoesNotBrakeOnBlockedRunsThatAreNotConsecutive(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.blockedRuns = 2
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.retire(id)
		if id == "yoyodyne-two" {
			return Outcome{WorkItemID: id, Status: runstate.StatusSucceeded, WorkItemClosed: true}, nil
		}
		return Outcome{WorkItemID: id, Status: runstate.StatusFailed, Blocked: true}, nil
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Braked != nil {
		t.Fatalf("schedule braked on %#v, want a run that landed to have cleared the count", schedule.Braked)
	}
	if len(schedule.Started) != 3 {
		t.Fatalf("started = %d run(s), want every item pulled: %s", len(schedule.Started), schedule.Render())
	}
}

// A session given a budget stops when the runs it started have spent it. Nothing
// in flight is interrupted for money: the spend is already made, and what
// stopping a run would lose is the work it bought.
func TestASessionStopsWhenItHasSpentItsBudget(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.prices["yoyodyne-one"] = 1.25
	harness.prices["yoyodyne-two"] = 1.25
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.close(id)
		return Outcome{RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusSucceeded, WorkItemClosed: true}, nil
	}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Budget: 2}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleBudgetSpent {
		t.Fatalf("stopped = %q, want the session ended on its budget: %s", schedule.Stopped, schedule.Render())
	}
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %d run(s), want the third left unstarted once the budget was spent", len(schedule.Started))
	}
	if schedule.SpentUSD != 2.5 || schedule.Budget != 2 {
		t.Fatalf("spent $%.2f of $%.2f, want what the two runs actually cost against what the session was given", schedule.SpentUSD, schedule.Budget)
	}
}

// A bounded session that cannot tell what it spent stops, rather than carrying
// on inside a bound it has lost the ability to hold. It is the difference
// between a budget and the appearance of one: an unpriceable run left running is
// a session spending against a number nothing is comparing anything to, and the
// operator would find that out from a schedule this session does not return
// until it is over.
func TestABoundedSessionStopsWhenItCannotTellWhatItSpent(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.close(id)
		// A run whose price is not among the recorded runs of its item is
		// evidence that went missing rather than a run that was free.
		return Outcome{RunID: "run-elsewhere", WorkItemID: id, Status: runstate.StatusSucceeded, WorkItemClosed: true}, nil
	}
	harness.prices["yoyodyne-one"] = 1
	sessions := &recordedSessions{}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Budget: 100}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleSpendUnreadable {
		t.Fatalf("stopped = %q, want the session stopped rather than left unbounded: %s", schedule.Stopped, schedule.Render())
	}
	// The budget is nowhere near spent, so nothing but the unreadable evidence
	// could have stopped this, and it stopped before the queue was drained.
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %d run(s), want the session to stop at the first run it could not price", len(schedule.Started))
	}
	if !strings.Contains(schedule.SpendProblem, "run-elsewhere") {
		t.Fatalf("spend problem = %q, want the unpriceable run named", schedule.SpendProblem)
	}
	// And it says so where somebody who is not at this terminal reads it, which
	// is the whole reason a session that stops has to stop loudly.
	said := sessions.said(runstate.WatchStopped)
	if !strings.Contains(said, "budget") || !strings.Contains(said, "run-elsewhere") {
		t.Fatalf("recorded stop = %q, want the budget and the run it could not price both carried into it", said)
	}
}

// An unbounded pass is unaffected by the same evidence: nothing there was
// spending against a number, so the unpriceable run costs the report a sentence
// and the pass nothing at all.
func TestAnUnboundedPassReportsSpendItCouldNotReadAndCarriesOn(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.close(id)
		return Outcome{RunID: "run-elsewhere", WorkItemID: id, Status: runstate.StatusSucceeded, WorkItemClosed: true}, nil
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 2 || schedule.Stopped != ScheduleDrained {
		t.Fatalf("schedule = %s, want every item run and the pass drained", schedule.Render())
	}
	if !strings.Contains(schedule.SpendProblem, "run-elsewhere") {
		t.Fatalf("spend problem = %q, want the unpriceable run named", schedule.SpendProblem)
	}
}

// A budget with nothing to measure it against is refused before anything is
// started. The operator asked for a bound; a pass that ran anyway would be
// reporting one it never had.
func TestASessionGivenABudgetIsRefusedWithNoWayToPriceItself(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	unpriced := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Spend = nil
		return pull, err
	}

	schedule, err := (Scheduler{Open: unpriced, Budget: 20}).Schedule(context.Background())
	if err == nil || !strings.Contains(err.Error(), "price what it has spent") {
		t.Fatalf("Schedule() error = %v, want a budget nothing can measure refused", err)
	}
	if schedule.Stopped != ScheduleUnreadable {
		t.Fatalf("stopped = %q, want the unusable pull named", schedule.Stopped)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing run under a bound that was never held", schedule.Started)
	}
	// The same pull is fine unbounded: nothing there is spending against a
	// number, so nothing needs to price it.
	if _, err := (Scheduler{Open: unpriced}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v, want an unbounded pass unaffected", err)
	}
}

// The ordinary recovery a development manager makes: a run stops on a blocker,
// they release the item without editing anything else, and the session pulls it
// again. The blocker the harness wrote into the item's notes is what says the
// item is not the one this session already tried.
func TestWatchingPullsAnItemAgainAfterItsBlockerIsReleased(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-blocked")...)
	harness.blockedRuns = 0
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		// What a blocked run does to the tracker: the item leaves the ready
		// queue, and the blocker is appended to its notes.
		h.block(id, "the replay conflicted and was left for a person")
		return Outcome{RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusFailed, Blocked: true}, nil
	}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 2 {
			// The development manager unblocks it and changes nothing else.
			h.unblock("yoyodyne-blocked")
		}
		return sleeps < 4
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if starts := len(harness.pullOrder()); starts != 2 {
		t.Fatalf("the item was started %d time(s), want it pulled again once somebody released it: %v", starts, harness.pullOrder())
	}
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %#v, want both attempts accounted for", schedule.Started)
	}
}

// A session nobody can read the state of still does its work, and says that
// nobody can read it. The alternative is a session that stops working because
// it could not be observed, which is the wrong way round.
func TestASessionThatCannotRecordItselfStillWorks(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	sessions := &recordedSessions{failure: errors.New("the watch log is not writable")}
	harness.onSleep = func(*scheduleHarness, int) bool { return false }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want the work done anyway", schedule.Started)
	}
	if !strings.Contains(schedule.SessionProblem, "the watch log is not writable") {
		t.Fatalf("session problem = %q, want the unwritable log reported", schedule.SessionProblem)
	}
}

// A watching pull with no interval is refused rather than read again as fast as
// the machine allows.
func TestWatchingRefusesAPullWithNoInterval(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	open := func(ctx context.Context) (Pull, error) {
		pull, err := harness.open(ctx)
		pull.Poll = 0
		return pull, err
	}
	schedule, err := (Scheduler{Open: open, Watching: true, Sleep: harness.sleep}).Schedule(context.Background())
	if err == nil {
		t.Fatal("Schedule() error = nil, want a watch with no interval refused")
	}
	if schedule.Stopped != ScheduleUnreadable {
		t.Fatalf("stopped = %q, want the unusable pull named", schedule.Stopped)
	}
	// The same pull drains perfectly well: a pass that never waits never reads
	// the interval.
	if _, err := (Scheduler{Open: open}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v, want a drain unaffected by an interval it never uses", err)
	}
}

// --- readings of the harness that fail ------------------------------------------

// contendedStore is the reading that ended a session on 2026-09-01: the tracker
// refusing a listing while something else was writing to the same store, which
// succeeded again minutes later.
const contendedStore = "bd list failed with status cancelled and exit code -1"

// The observed sequence, replayed: one tracker reading fails and the next one
// succeeds. The session used to exit on the first of those and leave the queue
// to an external job; it now waits, reads again, pulls the work, and says that
// it did.
func TestWatchingReadsTheHarnessAgainAfterAReadingThatFails(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	sessions := &recordedSessions{}
	harness.failList = func(_ *scheduleHarness, lists int) error {
		if lists == 1 {
			return errors.New(contendedStore)
		}
		return nil
	}
	// The first wait is the retry and the session carries on; the second is the
	// drained queue after the item ran, which is where the operator stops it.
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 2 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v, want a contended reading ridden through", err)
	}
	if schedule.Stopped != ScheduleCancelled {
		t.Fatalf("stopped = %q, want the session ended by its operator rather than by one failed reading", schedule.Stopped)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-one" {
		t.Fatalf("started = %#v, want the work pulled at the reading that succeeded", schedule.Started)
	}
	if schedule.ReadsRetried != 1 || !strings.Contains(schedule.ReadProblem, contendedStore) {
		t.Fatalf("schedule = %d retried, problem %q, want the one failed reading counted and named", schedule.ReadsRetried, schedule.ReadProblem)
	}
	if rendered := schedule.Render(); !strings.Contains(rendered, "1 reading(s) of the harness failed and were made again") {
		t.Fatalf("rendered = %q, want the reading it rode through reported", rendered)
	}
	// The session says it while it is happening, not only in the report it
	// returns when it is over: a session nobody is sitting at is read from the
	// watch log or not at all.
	if said := sessions.said(runstate.WatchIdle); !strings.Contains(said, contendedStore) || !strings.Contains(said, "read again") {
		t.Fatalf("the session said %q while it retried, want the failed reading and that it is being made again", said)
	}
	// And it says so as a reading that failed rather than as a queue it read and
	// found nothing in. Nothing anybody admits reaches a store that will not
	// answer, so this is the mark that keeps the message off the admission clause.
	idle, recorded := sessions.entered(runstate.WatchIdle)
	if !recorded || !idle.unreadable {
		t.Fatalf("idle transition = %#v, want the poll marked as a reading that failed", idle)
	}
}

// A reading that fails while this session has a run of its own going. The line
// is moving and the store is not answering, and both have to be said: the runs
// are what stop it reading as a stopped machine, and the failed reading is what
// stops it reading as a queue somebody has to admit work to.
func TestAReadingThatFailsBesideARunSaysBothRatherThanNeither(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-held")...)
	harness.capacity = 2
	sessions := &recordedSessions{}
	// The run is held open across the pull whose reading fails, so the session
	// genuinely has one going when it records the outage.
	holding := make(chan struct{})
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		<-holding
		return h.complete(id), nil
	}
	// Which pull is being made, so the reading that fails is the one after the
	// run started rather than a listing counted by hand.
	pulls := 0
	harness.onPull = func(_ *scheduleHarness, made int) { pulls = made }
	harness.failList = func(*scheduleHarness, int) error {
		if pulls == 1 {
			return errors.New(contendedStore)
		}
		return nil
	}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool {
		if sleeps == 1 {
			close(holding)
		}
		return sleeps < 3
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Now: harness.clock}
	if _, err := scheduler.Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v, want a contended reading ridden through", err)
	}
	idle, recorded := sessions.entered(runstate.WatchIdle)
	if !recorded {
		t.Fatal("no idle transition was recorded while the reading failed")
	}
	if !strings.Contains(idle.reason, "1 run in flight") || !strings.Contains(idle.reason, contendedStore) {
		t.Fatalf("idle reason = %q, want the run it had going and the reading that failed", idle.reason)
	}
	if idle.running != 1 || !idle.unreadable {
		t.Fatalf("idle transition = %#v, want %d run(s) and the reading marked as failed", idle, 1)
	}
}

// A store that stays unreadable is not contention, and the session stops on it —
// saying how long it went on failing, which is the whole of what tells an
// operator which of the two they are looking at.
func TestWatchingStopsOnceTheHarnessGoesOnBeingUnreadable(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	sessions := &recordedSessions{}
	harness.failList = func(*scheduleHarness, int) error { return errors.New(contendedStore) }
	// The operator never stops this one: what ends it is the window, and a session
	// that did not have one would back off here forever.
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 100 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err == nil || !strings.Contains(err.Error(), contendedStore) {
		t.Fatalf("Schedule() error = %v, want the store that would not be read reported", err)
	}
	if schedule.Stopped != ScheduleUnreadable {
		t.Fatalf("stopped = %q, want the unreadable harness named", schedule.Stopped)
	}
	if !strings.Contains(err.Error(), "5m0s") {
		t.Fatalf("Schedule() error = %v, want it to say how long the session tried", err)
	}
	if schedule.ReadsRetried < 2 {
		t.Fatalf("retried = %d, want a session that tried again rather than stopping on the first reading", schedule.ReadsRetried)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing pulled from a queue that was never read", schedule.Started)
	}
	// The duration travels with the stop into the log, because the reader who has
	// to tell a broken store from one bad minute is not at this terminal.
	if said := sessions.said(runstate.WatchStopped); !strings.Contains(said, "5m0s") {
		t.Fatalf("the session stopped saying %q, want how long it tried", said)
	}
	if !strings.Contains(schedule.ReadFailure, "5m0s") {
		t.Fatalf("read failure = %q, want the reading the session stopped on carried apart from the ones it rode through", schedule.ReadFailure)
	}
}

// A session stops as unreadable for a second reason — a pull that assembles and
// is unusable — and what it says about that stop is about that stop. A contended
// reading it rode through earlier is a different fact about a different moment,
// and reporting it as why the session ended would put a store outage in front of
// an operator whose capacity is misconfigured.
func TestAReadingRiddenThroughIsNotWhyASessionStoppedForSomethingElse(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	sessions := &recordedSessions{}
	harness.failList = func(_ *scheduleHarness, lists int) error {
		if lists == 1 {
			return errors.New(contendedStore)
		}
		return nil
	}
	// The configuration is edited to something no pass can be made from between
	// the reading that failed and the one that would have succeeded.
	harness.onPull = func(h *scheduleHarness, pulls int) {
		if pulls == 1 {
			h.mu.Lock()
			h.capacity = 0
			h.mu.Unlock()
		}
	}
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 100 }

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Now: harness.clock}
	schedule, err := scheduler.Schedule(context.Background())
	if err == nil || !strings.Contains(err.Error(), "developer capacity is 0") {
		t.Fatalf("Schedule() error = %v, want the unusable pull reported", err)
	}
	if schedule.Stopped != ScheduleUnreadable || schedule.ReadFailure != "" {
		t.Fatalf("schedule = stopped %q on read failure %q, want a stop that was not a reading that failed", schedule.Stopped, schedule.ReadFailure)
	}
	if schedule.ReadsRetried != 1 || !strings.Contains(schedule.ReadProblem, contendedStore) {
		t.Fatalf("schedule = %d retried, problem %q, want the earlier reading still accounted for", schedule.ReadsRetried, schedule.ReadProblem)
	}
	// The line the session ends on is the whole of what a reader away from this
	// terminal gets, so a contended reading from before must not be in it.
	if said := sessions.said(runstate.WatchStopped); said != ScheduleUnreadable {
		t.Fatalf("the session stopped saying %q, want %q and nothing about a reading it rode through", said, ScheduleUnreadable)
	}
	if rendered := schedule.Render(); !strings.Contains(rendered, "the last of them: ") || !strings.Contains(rendered, contendedStore) {
		t.Fatalf("rendered = %q, want the reading it rode through still named", rendered)
	}
}

// A drain does not wait any of that out. It is a command somebody is waiting on
// the return of, and one that slept through an outage would be one that hung.
func TestADrainStopsOnTheFirstReadingThatFails(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.failList = func(*scheduleHarness, int) error { return errors.New(contendedStore) }

	schedule, err := (Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}).Schedule(context.Background())
	if err == nil || !strings.Contains(err.Error(), contendedStore) {
		t.Fatalf("Schedule() error = %v, want the failed reading reported at once", err)
	}
	if schedule.Stopped != ScheduleUnreadable || schedule.ReadsRetried != 0 {
		t.Fatalf("schedule = stopped %q after %d retried reading(s), want a drain that did not wait", schedule.Stopped, schedule.ReadsRetried)
	}
	if harness.sleeps != 0 {
		t.Fatalf("the drain waited %d time(s), want none", harness.sleeps)
	}
}

// --- redeploying ---------------------------------------------------------------

// The whole of what a session redeploying itself is: a build lands over the one
// it is executing, and it stops choosing and stops, so the caller can restart it
// into what was deployed. The item still queued proves it stopped choosing
// rather than merely finishing.
func TestAWatchingSessionStopsToTakeUpTheBuildDeployedOverIt(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	sessions := &recordedSessions{}
	deployment := &deployedOver{}
	// The build lands while the first item is running, which is the ordinary case:
	// nobody deploys into an idle machine on purpose.
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		return h.complete(id), nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Sessions: sessions, Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if !schedule.Redeploying() || schedule.Stopped != ScheduleRedeployed {
		t.Fatalf("stopped = %q, want the session stopped to be restarted into what was deployed", schedule.Stopped)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-one" {
		t.Fatalf("started = %#v, want the session to have claimed nothing after the deploy landed", schedule.Started)
	}
	// Nothing waited: a session with a build to take up does not spend a poll
	// interval first, because every interval it waits is an interval the machine
	// dispatches from the build somebody replaced.
	if schedule.Polls != 0 {
		t.Fatalf("polls = %d, want a session that stopped rather than waited", schedule.Polls)
	}
	if reason := sessions.said(runstate.WatchStopped); !strings.Contains(reason, "restarting into it") {
		t.Fatalf("stopped reason = %q, want the restart said where somebody who is not at the terminal reads it", reason)
	}
	// And the stop is marked as a restart rather than left to read as an ending.
	// Every surface takes whose move follows from that mark: a session that ended
	// is waiting on somebody to start another, and this one is waiting on nothing,
	// which is the whole difference between ending the operator's chore and
	// reproducing it once per deploy.
	if !sessions.restarted() {
		t.Fatal("the session recorded its stop as an ending, which tells every reader to start a session that is already coming back")
	}
}

// The guarantee the whole shape rests on: a run already going is never
// interrupted for a redeploy. The session stops claiming immediately and waits
// out what it started, which is what makes the window an external restart can
// never find.
func TestARedeployWaitsOutTheRunsAlreadyGoing(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two", "yoyodyne-three")...)
	harness.capacity = 2
	// Both runs are required to be inside at once, so the deploy below lands with
	// two live runs rather than with one that has already finished.
	harness.developersMeet(2)
	deployment := &deployedOver{}
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		return h.complete(id), nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if harness.rendezvousFailure != nil {
		t.Fatalf("the deploy never landed on two live runs: %v", harness.rendezvousFailure)
	}
	if schedule.Stopped != ScheduleRedeployed {
		t.Fatalf("stopped = %q, want the session stopped to be restarted", schedule.Stopped)
	}
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %#v, want the two runs already going and nothing claimed after them", schedule.Started)
	}
	for _, started := range schedule.Started {
		if started.Failure != "" || started.Outcome.Status != runstate.StatusSucceeded {
			t.Fatalf("%s = %#v, want a live run carried to its own end rather than cut off", started.WorkItemID, started)
		}
	}
}

// A bound the session has reached wins over a deploy waiting to be taken up. The
// operator gave this session a number to stay inside, and a restart is a session
// starting that number again — so a budget that is gone stops the session as
// spent, and what takes the build up is the next session somebody starts.
func TestASpentBudgetStopsTheSessionRatherThanRedeployingIt(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.prices["yoyodyne-one"] = 2.50
	deployment := &deployedOver{}
	// The deploy lands while the run that spends the budget is still going, so
	// both are true at the pull that follows it.
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		h.close(id)
		return Outcome{RunID: "run-" + id, WorkItemID: id, Status: runstate.StatusSucceeded, WorkItemClosed: true}, nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Budget: 2, Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleBudgetSpent {
		t.Fatalf("stopped = %q, want the bound the operator set to end the session: %s", schedule.Stopped, schedule.Render())
	}
	if schedule.Redeploying() {
		t.Fatal("the session asked to be restarted with its budget spent, which is the bound starting over")
	}
}

// The count of runs is the other bound, and it holds the same way. A session
// that has started the last run it was allowed stops on the number rather than
// restarting into the build: the restart would be that number starting again,
// which is not what the operator asked for by writing it.
func TestALastPermittedRunStopsTheSessionRatherThanRedeployingIt(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	deployment := &deployedOver{}
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		deployment.deploy()
		return h.complete(id), nil
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Limit: 1, Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleLimitReached {
		t.Fatalf("stopped = %q, want the number of runs the operator asked for to end the session: %s", schedule.Stopped, schedule.Render())
	}
	if schedule.Redeploying() {
		t.Fatal("the session asked to be restarted having started every run it was allowed, which is the bound starting over")
	}
}

// A session that cannot tell whether it has been deployed over goes on working.
// Stopping the line because a file could not be read would be a worse failure
// than the staleness this guards against, and the reading is tried again at
// every pull.
func TestASessionThatCannotReadItsOwnBinaryKeepsChoosingWork(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.onSleep = func(*scheduleHarness, int) bool { return false }
	deployment := &deployedOver{failure: errors.New("no such file or directory")}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Deployment: deployment}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Redeploying() {
		t.Fatalf("schedule = %#v, want the work done and no restart claimed", schedule.Started)
	}
	if !strings.Contains(schedule.RedeployProblem, "no such file or directory") {
		t.Fatalf("redeploy problem = %q, want the reading that failed reported", schedule.RedeployProblem)
	}
}

// A drain is a command somebody is waiting on the return of, so it returns. A
// pass that restarted itself would run the whole pass again from the top, which
// is the opposite of what was asked for.
func TestADrainNeverRedeploysItself(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	deployment := &deployedOver{}
	deployment.deploy()

	schedule, err := (Scheduler{Open: harness.open, Deployment: deployment}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want a drain that ran its pass and returned", schedule.Stopped)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want the queue drained", schedule.Started)
	}
}

// deployedOver is the session's own binary: unchanged until a test deploys over
// it, and unreadable where a test says the reading itself fails.
type deployedOver struct {
	mu       sync.Mutex
	replaced bool
	failure  error
}

func (d *deployedOver) deploy() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.replaced = true
}

func (d *deployedOver) Replaced() (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failure != nil {
		return false, d.failure
	}
	return d.replaced, nil
}

func sameStates(got, want []runstate.WatchState) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
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
	// blockedRuns is the brake bound each pull reports, and prices is what each
	// run this harness ran cost. Both are per pull for the reason capacity is:
	// the scheduler re-reads them, and a test changes them under it.
	blockedRuns int
	prices      map[string]float64
	// sleeps counts the intervals a watching scheduler waited out, and onSleep is
	// how a test changes the world between polls. It reports whether the session
	// carries on, so returning false is the operator stopping it.
	sleeps  int
	onSleep func(*scheduleHarness, int) bool
	// onPull runs at the start of every pull with the number of pulls already
	// made, which is how a test changes the configuration under a running
	// scheduler.
	onPull func(*scheduleHarness, int)
	// failList decides whether a tracker listing fails, with the number of
	// listings already made. It stands in for the store contention a reconcile or
	// a settling run makes, which is a reading that fails and then does not.
	lists    int
	failList func(*scheduleHarness, int) error
	// now is the clock a watching session stamps its retries against. The fake
	// sleep below advances it by exactly the interval it was asked to wait, so a
	// test that drives a session through a store outage spends no real time
	// reaching the window the session gives up at.
	now time.Time
	// run stands in for the pipeline. The default completes the item; a test
	// that cares about a refusal or a failure replaces it.
	run func(*scheduleHarness, string) (Outcome, error)
	// stoppages is the harness's own record of work it stopped and nobody has
	// decided about, which is what separates a blocked status somebody has to
	// release from one whose blockers have all closed. A pull is wired with one
	// only where a test asks for it; without it a pull holds every blocked item,
	// which is what every other test here means.
	stoppages readmodel.Stoppages
	// escalate stands in for putting stopped work to the development manager,
	// with the number of passes already made. A pull is wired with one only where
	// a test asks for it, so every other test's pass is what it always was.
	escalations int
	escalate    func(*scheduleHarness, int) (EscalationSweep, error)

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
		prices:     map[string]float64{},
		capacity:   1,
		gate:       make(chan struct{}),
		// The morning the session that provoked the retry died, so a test reading
		// its own timings reads the ones in the report.
		now: time.Date(2026, 9, 1, 5, 40, 0, 0, time.UTC),
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
	h.mu.Lock()
	blockedRuns := h.blockedRuns
	stoppages := h.stoppages
	var escalations ScheduleEscalations
	if h.escalate != nil {
		escalations = h
	}
	h.mu.Unlock()
	return Pull{
		Tracker: h, Runs: h, Intake: h, Directives: h, Staleness: h, Stoppages: stoppages,
		Capacity: capacity, Start: h.start, Escalations: escalations,
		// A minute is the shipped interval, and no test spends one: the sleep is
		// the harness's own, so this is only what a watching pull is validated
		// against.
		Poll:                        time.Minute,
		BlockedRunsBeforeIntakeHold: blockedRuns,
		Brake:                       h,
		Spend:                       h,
	}, nil
}

// Escalate stands in for delivering one stopped run to the development
// manager's conversation, which is a provider turn the scheduler never makes
// itself.
func (h *scheduleHarness) Escalate(context.Context) (EscalationSweep, error) {
	h.mu.Lock()
	h.escalations++
	passes, escalate := h.escalations, h.escalate
	h.mu.Unlock()
	return escalate(h, passes)
}

// sleep stands in for waiting out a poll interval. It spends no time at all: a
// test that wants a session to end says so by returning false, which is the
// operator stopping it. The clock moves by what the wait asked for, so a session
// backing off through an outage reaches the window it gives up at.
func (h *scheduleHarness) sleep(_ context.Context, interval time.Duration) bool {
	h.mu.Lock()
	h.sleeps++
	h.now = h.now.Add(interval)
	sleeps, onSleep := h.sleeps, h.onSleep
	h.mu.Unlock()
	if onSleep == nil {
		return false
	}
	return onSleep(h, sleeps)
}

// Hold is the brake placing the operator's own switch. Nothing in the scheduler
// releases one, so this harness only ever has to place it.
func (h *scheduleHarness) Hold(reason string, at time.Time) (runstate.IntakeHold, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.held != nil {
		return *h.held, nil
	}
	held := runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        at,
		Reason:        reason,
	}
	h.held = &held
	return held, nil
}

// Price is what the runs of one item cost, as the recorded evidence would say.
func (h *scheduleHarness) Price(workItemID string) (runstate.ItemPrice, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cost, priced := h.prices[workItemID]
	if !priced {
		return runstate.ItemPrice{WorkItemID: workItemID}, nil
	}
	return runstate.ItemPrice{
		WorkItemID: workItemID,
		Runs:       []runstate.RunPrice{{RunID: "run-" + workItemID, WorkItemID: workItemID, CostUSD: cost}},
		TotalUSD:   cost,
	}, nil
}

// admit puts work in the backlog, ready to pull. It is what a product manager
// admitting something while a session watches looks like from the queue's side.
func (h *scheduleHarness) admit(items ...beads.WorkItem) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.items = append(h.items, items...)
	for _, item := range items {
		h.ready[item.ID] = true
	}
}

// block is what a run that stopped on a blocker does to the tracker: the item's
// status moves out of the ready queue and the reason is appended to its notes,
// which is exactly what beads.Client.Block does.
func (h *scheduleHarness) block(workItemID, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.ready, workItemID)
	for index := range h.items {
		if h.items[index].ID == workItemID {
			h.items[index].Status = "blocked"
			h.items[index].Notes = strings.TrimSpace(h.items[index].Notes + "\n" + reason)
		}
	}
}

// unblock is the development manager releasing that item and changing nothing
// else about it, which is the recovery a session has to notice.
func (h *scheduleHarness) unblock(workItemID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready[workItemID] = true
	for index := range h.items {
		if h.items[index].ID == workItemID {
			h.items[index].Status = "open"
		}
	}
}

// amend changes what an item says, which is what a development manager
// replanning stopped work does to it.
func (h *scheduleHarness) amend(workItemID, description string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for index := range h.items {
		if h.items[index].ID == workItemID {
			h.items[index].Description = description
		}
	}
}

// release lifts the intake hold, which is the operator letting a braked session
// carry on.
func (h *scheduleHarness) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.held = nil
}

// recordedSessions is the durable account a watch session writes about itself,
// as a test reads it back.
type recordedSessions struct {
	mu          sync.Mutex
	transitions []recordedTransition
	failure     error
}

type recordedTransition struct {
	state  runstate.WatchState
	reason string
	// running, executor, and unreadable are what a reader takes whose move follows
	// an idle poll from: the runs the session could see going, the conversation
	// carrying the work it passed over, and a reading of the harness that failed.
	running    int
	executor   domain.WorkItemExecutor
	unreadable bool
	// restarting is the session marking its last line as a restart rather than an
	// ending, which is what every surface reads to tell the operator whether
	// anything is waiting on them.
	restarting bool
}

func (r *recordedSessions) Record(transition SessionState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failure != nil {
		return r.failure
	}
	r.transitions = append(r.transitions, recordedTransition{
		state:      transition.State,
		reason:     transition.Reason,
		running:    transition.Running,
		executor:   transition.Executor,
		unreadable: transition.Unreadable,
		restarting: transition.Restarting,
	})
	return nil
}

// entered is the first transition recorded into a state, whole rather than only
// its reason: a test about what a reader is told has to read the fields the
// reader's whose-move clause is derived from too.
func (r *recordedSessions) entered(state runstate.WatchState) (recordedTransition, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, transition := range r.transitions {
		if transition.state == state {
			return transition, true
		}
	}
	return recordedTransition{}, false
}

func (r *recordedSessions) states() []runstate.WatchState {
	r.mu.Lock()
	defer r.mu.Unlock()
	states := make([]runstate.WatchState, 0, len(r.transitions))
	for _, transition := range r.transitions {
		states = append(states, transition.state)
	}
	return states
}

// restarted reports the session having marked a stop as a restart rather than as
// an ending, which is what a reader takes whose move follows from.
func (r *recordedSessions) restarted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, transition := range r.transitions {
		if transition.restarting {
			return true
		}
	}
	return false
}

func (r *recordedSessions) said(state runstate.WatchState) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, transition := range r.transitions {
		if transition.state == state {
			return transition.reason
		}
	}
	return ""
}

// clock is what a scheduler stamps its own timings with, moved only by the fake
// sleep above.
func (h *scheduleHarness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *scheduleHarness) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	h.mu.Lock()
	h.lists++
	lists, failList := h.lists, h.failList
	h.mu.Unlock()
	if failList != nil {
		if err := failList(h, lists); err != nil {
			return nil, err
		}
	}
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

// retire drops an item's readiness and leaves its status alone. The real harness
// uses it rather than close because there the pipeline is what sets the status —
// closed when the work landed, blocked when a conflict stopped it — and a
// fixture that overwrote it would have the test asserting its own bookkeeping
// instead of what the run actually recorded.
func (h *scheduleHarness) retire(workItemID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.ready, workItemID)
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
	// develop is what each run's developer writes into its worktree. The default
	// gives every item a file of its own, which is the ordinary case; a test
	// about what happens when two changes collide points them at one path.
	develop func(workItemID, worktree string) error
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
	harness.develop = func(workItemID, worktree string) error {
		return os.WriteFile(filepath.Join(worktree, workItemID+".txt"), []byte("implemented\n"), 0o600)
	}
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
		return h.develop(workItemID, request.WorkingDirectory)
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
	h.retire(workItemID)
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

func (h *realScheduleHarness) Reopen(_ context.Context, id, _ string) (beads.WorkItem, error) {
	return h.setStatus(id, "open")
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

// Every stoppage comes back to the scheduler as an error, so a pass that read
// the error first said "failed" over a run handed to a person with its branch
// and worktree intact — the same word it said over one that broke with nothing
// to show. The pass reports the ending the run reached, in the read model's
// vocabulary, and works none of it out for itself.
func TestAPassNamesEachEndingRatherThanCallingEveryStoppageAFailure(t *testing.T) {
	t.Parallel()

	for _, want := range []struct {
		named   string
		started Started
	}{
		// The reading this exists to stop: the item is back with a person and the
		// change is still there, which is not what "failed" says.
		{"stopped", Started{
			WorkItemID: "yoyodyne-stopped",
			Outcome:    Outcome{Status: runstate.StatusFailed, Blocked: true, Branch: "yoyodyne/stopped"},
			Failure:    "the repair budget was spent with the checks still failing",
		}},
		{"failed", Started{
			WorkItemID: "yoyodyne-broke",
			Outcome:    Outcome{Status: runstate.StatusFailed},
			Failure:    "the worktree could not be cut",
		}},
		{"cancelled", Started{
			WorkItemID: "yoyodyne-cancelled",
			Outcome:    Outcome{Status: runstate.StatusCancelled},
			Failure:    "context canceled",
		}},
		{"timed out", Started{
			WorkItemID: "yoyodyne-late",
			Outcome:    Outcome{Status: runstate.StatusTimedOut},
			Failure:    "context deadline exceeded",
		}},
		// The three that are not endings at all keep the words they had: a start
		// the scheduler lost to another process, a run still in flight, and a run
		// whose work landed.
		{"declined", Started{WorkItemID: "yoyodyne-lost", Declined: "the slot went to another process"}},
		{"paused", Started{WorkItemID: "yoyodyne-parked", Outcome: Outcome{Status: runstate.StatusRunning, Paused: true}}},
		{"integrated", Started{
			WorkItemID: "yoyodyne-landed",
			Outcome:    Outcome{Status: runstate.StatusSucceeded, Integration: integratedOnto("main")},
		}},
		// A start that never became a run has no ending to name, so the failure is
		// still what is said.
		{"failed", Started{WorkItemID: "yoyodyne-unstarted", Failure: "the store refused the reservation"}},
	} {
		if named := want.started.state(); named != want.named {
			t.Errorf("%s is reported as %q, want %q", want.started.WorkItemID, named, want.named)
		}
	}

	// And the word reaches the rendered pass rather than only the helper.
	rendered := Schedule{Started: []Started{{
		WorkItemID: "yoyodyne-stopped",
		Outcome:    Outcome{Status: runstate.StatusFailed, Blocked: true},
		Failure:    "the repair budget was spent",
	}}}.Render()
	if !strings.Contains(rendered, "yoyodyne-stopped: stopped") {
		t.Errorf("the pass reads:\n%s\nwant the stoppage named as one", rendered)
	}
}

// integratedOnto is a promotion onto one branch, which is all the pass's own
// account of a run reads from it.
func integratedOnto(branch string) *gitworktree.Integration {
	return &gitworktree.Integration{TargetBranch: branch}
}

// Stopped work reaches the development manager from the pass rather than from
// somebody carrying it to her, and what the pass did about it is on the schedule
// beside what it pulled.
func TestAPassPutsStoppedWorkToTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.escalate = func(_ *scheduleHarness, passes int) (EscalationSweep, error) {
		if passes > 1 {
			return EscalationSweep{}, nil
		}
		return EscalationSweep{Escalated: []Escalated{{
			WorkItemID: "yoyodyne-stopped",
			RunID:      "run-0123456789abcdef0123456789abcdef",
			DocketKey:  "stopped_run:run-0123456789abcdef0123456789abcdef",
			Delivered:  true,
			Decision:   "repair",
		}}}, nil
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Escalated) != 1 || !schedule.Escalated[0].Delivered {
		t.Fatalf("escalated = %#v, want the stoppage this pass delivered", schedule.Escalated)
	}
	if len(schedule.Started) != 1 {
		t.Fatalf("started = %#v, want the pass to have gone on choosing work", schedule.Started)
	}
	if !strings.Contains(schedule.Render(), "escalated the stoppage of run run-0123456789abcdef0123456789abcdef") {
		t.Fatalf("rendered = %q, want the delivery said beside what the pass pulled", schedule.Render())
	}
}

// A delivery that failed costs the pass nothing it was doing, so it is reported
// beside the pull rather than stopping it — and never left unsaid.
func TestADeliveryThatFailedDoesNotStopThePass(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.escalate = func(*scheduleHarness, int) (EscalationSweep, error) {
		return EscalationSweep{}, errors.New("the conversation could not be opened")
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Stopped != ScheduleDrained {
		t.Fatalf("schedule = %#v, want a pass that carried on choosing work", schedule)
	}
	if !strings.Contains(schedule.EscalationProblem, "waiting on somebody carrying it to her") {
		t.Fatalf("escalation problem = %q, want the failed delivery said out loud", schedule.EscalationProblem)
	}
	if !strings.Contains(schedule.Render(), "could not be opened") {
		t.Fatalf("rendered = %q, want the failure in the pass's own account", schedule.Render())
	}
}

// Held intake stops the harness choosing work. It does not stop stopped work
// reaching the development manager, because the judgment a held queue is waiting
// on is exactly what that delivery produces.
func TestAHeldPassStillPutsStoppedWorkToTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one")...)
	harness.held = &runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC),
		Reason:        "three runs blocked in a row",
	}
	harness.escalate = func(*scheduleHarness, int) (EscalationSweep, error) {
		return EscalationSweep{Escalated: []Escalated{{
			WorkItemID: "yoyodyne-stopped",
			RunID:      "run-0123456789abcdef0123456789abcdef",
			Delivered:  true,
		}}}, nil
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleIntakeHeld || len(schedule.Started) != 0 {
		t.Fatalf("schedule = %#v, want a held pass that chose nothing", schedule)
	}
	if len(schedule.Escalated) != 1 {
		t.Fatalf("escalated = %#v, want the stoppage delivered while intake was held", schedule.Escalated)
	}
}

// A delivery that keeps failing is one problem rather than a thousand. A
// watching session polls all night, so a pass that accumulated every failed
// attempt would report the same sentence once a minute and bury what it did.
func TestRepeatedDeliveryFailuresAreOneProblemOnThePass(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.escalate = func(_ *scheduleHarness, passes int) (EscalationSweep, error) {
		return EscalationSweep{Escalated: []Escalated{{
			WorkItemID: "yoyodyne-stopped",
			RunID:      "run-0123456789abcdef0123456789abcdef",
			Problem:    fmt.Sprintf("the conversation could not be opened, attempt %d", passes),
		}}}, nil
	}
	scheduler := Scheduler{}
	pull := Pull{Escalations: harness}
	schedule := Schedule{}

	scheduler.escalate(context.Background(), &schedule, pull)
	scheduler.escalate(context.Background(), &schedule, pull)

	if len(schedule.Escalated) != 0 {
		t.Fatalf("escalated = %#v, want a delivery that did not happen kept as a problem rather than an event", schedule.Escalated)
	}
	if schedule.EscalationProblem != "the conversation could not be opened, attempt 2" {
		t.Fatalf("escalation problem = %q, want the latest attempt and only it", schedule.EscalationProblem)
	}

	// And a pass that gets through says so, rather than going on reporting a
	// failure that is over.
	harness.escalate = func(*scheduleHarness, int) (EscalationSweep, error) {
		return EscalationSweep{Escalated: []Escalated{{
			WorkItemID: "yoyodyne-stopped",
			RunID:      "run-0123456789abcdef0123456789abcdef",
			Delivered:  true,
			Decision:   "repair",
		}}}, nil
	}
	scheduler.escalate(context.Background(), &schedule, pull)
	if len(schedule.Escalated) != 1 || schedule.EscalationProblem != "" {
		t.Fatalf("schedule = %#v, want the delivery kept and the failure behind it cleared", schedule)
	}
}

// A pass does not erase its own account of stopped work. A pull that finds
// nothing to say about a stoppage is not evidence that the last one's failure
// was resolved — the stoppage may be waiting out its retry delay — so what the
// pass said stands until a delivery actually happens.
func TestAPassDoesNotEraseWhatItSaidAboutStoppedWork(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.escalate = func(_ *scheduleHarness, passes int) (EscalationSweep, error) {
		switch passes {
		case 1:
			return EscalationSweep{Escalated: []Escalated{{
				WorkItemID: "yoyodyne-stopped",
				RunID:      "run-0123456789abcdef0123456789abcdef",
				Problem:    "the provider refused the turn on attempt 1 of 3",
			}}}, nil
		case 2, 3:
			// The pulls a drain makes while the stoppage waits out its delay.
			return EscalationSweep{}, nil
		default:
			return EscalationSweep{Escalated: []Escalated{{
				WorkItemID: "yoyodyne-stopped",
				RunID:      "run-0123456789abcdef0123456789abcdef",
				Delivered:  true,
				Decision:   "repair",
			}}}, nil
		}
	}
	scheduler := Scheduler{}
	pull := Pull{Escalations: harness}
	schedule := Schedule{}

	scheduler.escalate(context.Background(), &schedule, pull)
	if schedule.EscalationProblem == "" {
		t.Fatal("the pass said nothing about a delivery that failed")
	}
	for pass := 2; pass <= 3; pass++ {
		scheduler.escalate(context.Background(), &schedule, pull)
		if schedule.EscalationProblem == "" {
			t.Fatalf("pass %d erased what the pass had already said about stopped work", pass)
		}
	}

	// And a delivery that actually happened is what clears it.
	scheduler.escalate(context.Background(), &schedule, pull)
	if schedule.EscalationProblem != "" || len(schedule.Escalated) != 1 {
		t.Fatalf("schedule = %#v, want the delivery kept and the failure behind it cleared", schedule)
	}
}

// A stoppage the harness has given up delivering is said on every pass for as
// long as it is true, so a pass that ends hours later still names what needs a
// person.
func TestAnAbandonedStoppageIsSaidByEveryPass(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.escalate = func(*scheduleHarness, int) (EscalationSweep, error) {
		return EscalationSweep{Escalated: []Escalated{{
			WorkItemID: "yoyodyne-stopped",
			RunID:      "run-0123456789abcdef0123456789abcdef",
			Problem:    "the stoppage of run run-0123456789abcdef0123456789abcdef could not be put to the development manager in 3 attempt(s), so the harness stopped trying and it needs a person",
		}}}, nil
	}
	scheduler := Scheduler{}
	pull := Pull{Escalations: harness}
	schedule := Schedule{}

	for pass := 1; pass <= 3; pass++ {
		scheduler.escalate(context.Background(), &schedule, pull)
		if !strings.Contains(schedule.EscalationProblem, "needs a person") {
			t.Fatalf("pass %d says %q, want the abandoned stoppage still named", pass, schedule.EscalationProblem)
		}
	}
	// Said once, however many passes have found it: what a reader needs is the
	// standing fact rather than one line per pull.
	if strings.Count(schedule.EscalationProblem, "needs a person") != 1 {
		t.Fatalf("the pass says %q, want the standing fact once rather than once per pull", schedule.EscalationProblem)
	}
	if len(schedule.Escalated) != 0 {
		t.Fatalf("escalated = %#v, want nothing reported as delivered", schedule.Escalated)
	}
}

// A delivery is a spend the pass makes itself, so it counts against the bound
// the session was given. A turn is not a run and nothing else in the pass would
// see it, and a session that spent past its cap on turns nobody counted is the
// operator's cap disappearing quietly.
func TestWhatADeliveryCostCountsAgainstTheSessionsBudget(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.escalate = func(_ *scheduleHarness, passes int) (EscalationSweep, error) {
		return EscalationSweep{Escalated: []Escalated{{
			WorkItemID: "yoyodyne-stopped",
			RunID:      "run-0123456789abcdef0123456789abcdef",
			Delivered:  passes == 1,
			CostUSD:    0.40,
			// The second pass is a turn that failed, which the provider charged
			// for exactly as it charged for the first.
			Problem: map[bool]string{true: "", false: "the reply could not be read"}[passes == 1],
		}}}, nil
	}
	scheduler := Scheduler{Budget: 1}
	pull := Pull{Escalations: harness}
	schedule := Schedule{}

	scheduler.escalate(context.Background(), &schedule, pull)
	scheduler.escalate(context.Background(), &schedule, pull)

	if schedule.SpentUSD != 0.80 {
		t.Fatalf("spent = %.2f, want both turns counted against the session", schedule.SpentUSD)
	}
}

// And a session stops on its budget over deliveries alone. A watching session
// with an empty queue starts nothing and used to spend nothing; it can now spend
// a turn per poll, so the bound has to be able to end it on that spend by itself.
func TestASessionStopsOnItsBudgetOverDeliveriesAlone(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.escalate = func(*scheduleHarness, int) (EscalationSweep, error) {
		return EscalationSweep{Escalated: []Escalated{{
			WorkItemID: "yoyodyne-stopped",
			RunID:      "run-0123456789abcdef0123456789abcdef",
			Delivered:  true,
			CostUSD:    0.40,
		}}}, nil
	}
	// The session keeps polling; what stops it is the bound rather than the
	// operator.
	harness.onSleep = func(*scheduleHarness, int) bool { return true }

	schedule, err := Scheduler{
		Open: harness.open, Watching: true, Budget: 1, Sleep: harness.sleep, Now: harness.clock,
	}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if schedule.Stopped != ScheduleBudgetSpent {
		t.Fatalf("stopped = %q, want the session stopped on its budget", schedule.Stopped)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want a session that spent its budget on deliveries alone", schedule.Started)
	}
	if schedule.SpentUSD < 1 {
		t.Fatalf("spent = %.2f, want the deliveries counted up to the bound", schedule.SpentUSD)
	}
}

// haltedWork is the harness's durable account of the work it stopped: the runs
// it recorded and the stoppages it put in front of the development manager. It
// is what tells a blocked status somebody still has to release from one whose
// blockers have all closed.
type haltedWork struct {
	runs        []runstate.State
	escalations []runstate.Escalation
}

func (h haltedWork) Recorded() ([]runstate.State, error) { return h.runs, nil }

func (h haltedWork) Escalated() ([]runstate.Escalation, error) { return h.escalations, nil }

// The idle morning of 2026-09-04, replayed at the grain the scheduler reads at.
// Two items sit at status blocked with no unfinished dependency between them.
// One of them stopped last night and its change is still on a branch; the other
// was blocked months ago on work that has since landed, and nothing rewrote the
// field when it did. The status says the same word about both, which is why the
// scheduler asks the records instead: it starts the released one and passes over
// the held one, naming what holds it.
func TestSchedulerStartsBlockedWorkNothingIsHoldingAndPassesOverAStoppage(t *testing.T) {
	t.Parallel()

	stopped := beads.WorkItem{
		ID: "yoyodyne-ifd.153", Title: "Guard the notes writer", Status: "blocked", Priority: 0,
	}
	released := beads.WorkItem{
		ID: "yoyodyne-ifd.117.1", Title: "Split the configuration reference", Status: "blocked", Priority: 1,
		// The blocker closed as the week's work landed, so it has left the backlog.
		// The dependency is still listed, exactly as Beads lists it.
		Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.117", Type: "blocks"}},
	}
	harness := newScheduleHarness(stopped, released)
	// Neither is on the tracker's ready list, because that list is computed from
	// the same status field. That is the whole of what hid them.
	harness.ready = map[string]bool{}
	harness.stoppages = haltedWork{runs: []runstate.State{{
		RunID:        "run-5035c832",
		WorkItemID:   stopped.ID,
		Status:       runstate.StatusFailed,
		UpdatedAt:    harness.now.Add(-8 * time.Hour),
		Branch:       "yoyodyne/yoyodyne-ifd-153/5035c832",
		WorktreePath: "/state/worktrees/yoyodyne-ifd-153-5035c832",
		Blocker:      "Yoyodyne stopped this item: its independent reviewer still required repair after every permitted attempt.",
	}}}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != released.ID {
		t.Fatalf("started = %#v, want the released item pulled: %s", schedule.Started, schedule.Render())
	}
	// The stoppage keeps its place in the order and is passed over with what holds
	// it named, rather than being started on top of the change it left behind.
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != stopped.ID {
		t.Fatalf("deferred = %#v, want the stoppage named rather than counted among the unready", schedule.Deferred)
	}
	if !strings.Contains(schedule.Deferred[0].Reason, "run-5035c832") {
		t.Fatalf("deferred reason = %q, want the preserved change named", schedule.Deferred[0].Reason)
	}
	// The released item has left the backlog by being pulled, and the stoppage is
	// still admitted work in the product manager's order. What it is not is
	// pullable, which is a different thing from being hidden.
	if schedule.Admitted != 1 || schedule.Pullable != 0 {
		t.Fatalf("backlog = %d admitted, %d pullable, want the stoppage queued and unpullable", schedule.Admitted, schedule.Pullable)
	}
}

// And the safe direction when the records cannot be read at all: a pull wired
// without them holds every blocked item rather than releasing work whose hold it
// could not see. Holding a releasable item costs one pull; releasing a held one
// starts a run over a change that is still there.
func TestSchedulerHoldsBlockedWorkWhenNothingCanSayWhatIsHoldingIt(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(beads.WorkItem{
		ID: "yoyodyne-ifd.117.1", Title: "Split the configuration reference", Status: "blocked", Priority: 1,
	})
	harness.ready = map[string]bool{}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 || schedule.Pullable != 0 {
		t.Fatalf("started = %#v, pullable = %d, want nothing released on an unread hold: %s",
			schedule.Started, schedule.Pullable, schedule.Render())
	}
}
