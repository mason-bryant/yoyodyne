package orchestrator

// What the scheduler will not start beside what. The first test is the day this
// was admitted for: three items broken out of one epic, a machine with room for
// two, and every pair of them racing the other into the same target branch. The
// rest are the two halves that make it a scheduling rule rather than a brake —
// work that shares nothing still runs concurrently, and an item held back is
// held back for exactly as long as the run it would have raced lasts.

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The motivating evidence, replayed: three same-epic siblings ready at once with
// capacity for two. Started together, two of them promote into a branch the
// third has to replay onto, re-check, and have reviewed again from nothing — the
// day's three replay conflicts. Sequenced, all three land and the machine spends
// one wait.
func TestSchedulerSequencesSiblingsOfOneEpicRatherThanRacingThem(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(siblings("yoyodyne-epic", "yoyodyne-epic.1", "yoyodyne-epic.2", "yoyodyne-epic.3")...)
	harness.capacity = 2

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	// Nothing is withheld: every item still runs, and the pass still drains.
	pulled := harness.pullOrder()
	if len(pulled) != 3 {
		t.Fatalf("pulled = %v, want all three siblings run: %s", pulled, schedule.Render())
	}
	// The whole of the criterion. Two at once is the racing integration this
	// exists to stop, and it is asserted against what actually overlapped rather
	// than inferred from the order they were started in.
	if harness.peak != 1 {
		t.Fatalf("peak concurrent runs = %d, want siblings of one epic sequenced rather than raced: %s",
			harness.peak, schedule.Render())
	}
	// The pass says which items it passed over and what they would have raced,
	// because an operator watching a machine with a free slot sit idle needs the
	// trade rather than the silence.
	if len(schedule.Deferred) != 2 {
		t.Fatalf("deferred = %#v, want the two siblings held back named", schedule.Deferred)
	}
	// Each line names the run its item was last held behind, which is one of this
	// session's own — named as the session's where the pull that held it had not
	// yet seen the run reserve, and by its identifier afterwards. The second
	// sibling only ever waited on the first; the third waited on the first and
	// then, at whichever pulls found the second's run still over an item the
	// tracker listed, on the second.
	for _, deferred := range schedule.Deferred {
		if !strings.Contains(deferred.Reason, "yoyodyne-epic") {
			t.Fatalf("deferred reason = %q, want the epic the two share named", deferred.Reason)
		}
		switch deferred.WorkItemID {
		case "yoyodyne-epic.2":
			if !namesRunOf(deferred.Reason, "yoyodyne-epic.1") {
				t.Fatalf("deferred reason for %s = %q, want the first sibling's run named", deferred.WorkItemID, deferred.Reason)
			}
		case "yoyodyne-epic.3":
			if !namesRunOf(deferred.Reason, "yoyodyne-epic.1") && !namesRunOf(deferred.Reason, "yoyodyne-epic.2") {
				t.Fatalf("deferred reason for %s = %q, want the sibling's run it was held behind named", deferred.WorkItemID, deferred.Reason)
			}
		default:
			t.Fatalf("deferred = %#v, want only the two held-back siblings named", deferred)
		}
	}
	// And the ordering rationale reaches durable state, which is the only place
	// it survives the pass: the first sibling raced nothing and says nothing, and
	// the ones that waited say so.
	if reason := harness.selectionFor("yoyodyne-epic.1").Reason; strings.Contains(reason, "held back") {
		t.Fatalf("reason = %q, want the first sibling to have waited for nothing", reason)
	}
	for _, id := range []string{"yoyodyne-epic.2", "yoyodyne-epic.3"} {
		reason := harness.selectionFor(id).Reason
		if !strings.Contains(reason, "held back earlier in this session") {
			t.Fatalf("reason for %s = %q, want the wait it came out of recorded", id, reason)
		}
		if !strings.Contains(reason, "yoyodyne-epic") {
			t.Fatalf("reason for %s = %q, want what it would have raced recorded", id, reason)
		}
	}
}

// The other half of the rule, and the one it must not cost: items that share
// nothing still run at once. The developers are held in a rendezvous until both
// are inside, so "concurrently" is an observation rather than an inference.
func TestSchedulerStillRunsUnrelatedItemsConcurrently(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-one", "yoyodyne-two")...)
	harness.capacity = 2
	harness.developersMeet(2)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Deferred) != 0 {
		t.Fatalf("deferred = %#v, want nothing held back where nothing is shared", schedule.Deferred)
	}
	for _, started := range schedule.Started {
		if strings.Contains(started.Reason, "held back") {
			t.Fatalf("reason for %s = %q, want nothing said about sequencing where none happened",
				started.WorkItemID, started.Reason)
		}
	}
}

// Two items of no shared parentage that will plainly change the same file, and a
// third that will not. The two are sequenced and the third runs beside the first,
// so the slot the sequencing freed is spent rather than idled — and the run that
// took it records having been pulled ahead of the one that was held.
func TestSchedulerSequencesItemsOverASharedSurfaceAndPullsPastThem(t *testing.T) {
	t.Parallel()

	first := beads.WorkItem{
		ID: "yoyodyne-first", Title: "Widen the schedule reason", Status: "open", Priority: 1,
		Description: "conflict-surface: internal/orchestrator/schedule.go",
	}
	second := beads.WorkItem{
		ID: "yoyodyne-second", Title: "Bound the schedule reason", Status: "open", Priority: 1,
		Description: "conflict-surface: internal/orchestrator/schedule.go",
	}
	unrelated := beads.WorkItem{
		ID: "yoyodyne-unrelated", Title: "Something else entirely", Status: "open", Priority: 2,
		Description: "conflict-surface: internal/notify/voice.go",
	}
	harness := newScheduleHarness(first, second, unrelated)
	harness.capacity = 2
	// The first item and the unrelated one must be inside at once: that is the
	// slot the held-back item would otherwise have taken being spent on work that
	// races nothing.
	harness.developersMeet(2)

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != second.ID {
		t.Fatalf("deferred = %#v, want the item over the same file held back: %s", schedule.Deferred, schedule.Render())
	}
	if !strings.Contains(schedule.Deferred[0].Reason, "internal/orchestrator/schedule.go") {
		t.Fatalf("deferred reason = %q, want the surface the two share named", schedule.Deferred[0].Reason)
	}
	// The order was departed from, so the run that benefited says so.
	reason := harness.selectionFor(unrelated.ID).Reason
	if !strings.Contains(reason, second.ID) || !strings.Contains(reason, "held back at this pull") {
		t.Fatalf("reason for %s = %q, want the item it was pulled ahead of recorded", unrelated.ID, reason)
	}
	if pulled := harness.pullOrder(); len(pulled) != 3 {
		t.Fatalf("pulled = %v, want every item run once the conflict cleared: %s", pulled, schedule.Render())
	}
}

// An item that declares nothing is read for the files it plainly names, and only
// for those: a shared file sequences, and different files in one package do not.
// The narrowness is the point — a surface invented out of prose holds unrelated
// work back, which is the concurrency this is supposed to be protecting.
func TestSchedulerSequencesOnSurfacesInferredFromWhatAnItemSays(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		other    string
		deferred int
	}{
		{
			name:     "the same file named by both",
			other:    "Bound what internal/orchestrator/schedule.go records against the run state's own limit.",
			deferred: 1,
		},
		{
			name:     "different files in one package",
			other:    "Teach internal/orchestrator/publish.go to say which remote refused it.",
			deferred: 0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			first := beads.WorkItem{
				ID: "yoyodyne-first", Title: "Widen the recorded reason", Status: "open", Priority: 1,
				Description: "Put the ordering rationale into internal/orchestrator/schedule.go.",
			}
			second := beads.WorkItem{
				ID: "yoyodyne-second", Title: "Bound the recorded reason", Status: "open", Priority: 1,
				Description: test.other,
			}
			harness := newScheduleHarness(first, second)
			harness.capacity = 2

			schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if len(schedule.Deferred) != test.deferred {
				t.Fatalf("deferred = %#v, want %d held back: %s", schedule.Deferred, test.deferred, schedule.Render())
			}
			if pulled := harness.pullOrder(); len(pulled) != 2 {
				t.Fatalf("pulled = %v, want both items run either way: %s", pulled, schedule.Render())
			}
		})
	}
}

// The case a reading of the queue alone would miss: the run it would race
// belongs to another process, over an item that has already left the backlog by
// being claimed. Nothing in the queue names it, and the item it would race is
// exactly the one whose integration is nearest.
func TestSchedulerSequencesBehindWorkAnotherProcessAlreadyHasInFlight(t *testing.T) {
	t.Parallel()

	claimed := beads.WorkItem{ID: "yoyodyne-epic.1", Title: "First half", Status: "in_progress", Priority: 1, Parent: "yoyodyne-epic"}
	sibling := beads.WorkItem{ID: "yoyodyne-epic.2", Title: "Second half", Status: "open", Priority: 1, Parent: "yoyodyne-epic"}
	harness := newScheduleHarness(claimed, sibling)
	harness.capacity = 2
	harness.inFlight[claimed.ID] = runstate.State{
		RunID: "run-" + claimed.ID, WorkItemID: claimed.ID, Status: runstate.StatusRunning,
	}

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want nothing started beside another process's run over the same epic", schedule.Started)
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != sibling.ID {
		t.Fatalf("deferred = %#v, want the sibling named as sequenced behind it: %s", schedule.Deferred, schedule.Render())
	}
	if !strings.Contains(schedule.Deferred[0].Reason, claimed.ID) {
		t.Fatalf("deferred reason = %q, want the item in flight named", schedule.Deferred[0].Reason)
	}
	// The run itself, and not only its item: an item cannot be checked against
	// `yoyo status`, which lists runs, and a reason that named one alone was read
	// on 2026-09-18 as the guard holding a slot on a run that had already failed.
	if !strings.Contains(schedule.Deferred[0].Reason, "run-"+claimed.ID) {
		t.Fatalf("deferred reason = %q, want the run in flight named", schedule.Deferred[0].Reason)
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want a drain that found nothing startable to end", schedule.Stopped)
	}
}

// What the guard counts as in flight is runstate.Status.InFlight — pending or
// running — and nothing the listing it is handed says otherwise. The store's own
// listing already answers in those terms, so with the real store this rule is
// never the one that decides; what this proves is that the guard's reading is
// the status's own rather than the listing's, by handing it the runs the store
// never would: a sibling whose last run ended in each terminal status, beside a
// ready sibling of the same epic. None of them holds a developer slot or the
// epic, and the ready sibling is pulled with nothing said about a wait.
//
// The failed case is shaped like yoyodyne-ifd.272's run of 2026-09-16 — stopped
// at integration on a replay conflict, its pull request still open, a decision
// about it owed — because that is the run a stale schedule report named two days
// later as still in flight. That report, not the guard, was what misread the run
// (see passOver); this is the rule the report was mistaken for having broken.
func TestSchedulerDoesNotHoldAnEpicOnASiblingWhoseRunHasEnded(t *testing.T) {
	t.Parallel()

	for _, status := range []runstate.Status{
		runstate.StatusFailed, runstate.StatusSucceeded, runstate.StatusCancelled, runstate.StatusTimedOut,
	} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			ended := beads.WorkItem{
				ID: "yoyodyne-epic.272", Title: "A claimed-but-dead item is audited", Status: "blocked", Priority: 1,
				Parent: "yoyodyne-epic",
			}
			ready := beads.WorkItem{
				ID: "yoyodyne-epic.379", Title: "The guard reads run state", Status: "open", Priority: 0,
				Parent: "yoyodyne-epic",
			}
			harness := newScheduleHarness(ended, ready)
			harness.capacity = 2
			run := runstate.State{RunID: "run-2f6e6e0a", WorkItemID: ended.ID, Status: status, Phase: runstate.PhaseComplete}
			if status == runstate.StatusFailed {
				run.Phase = runstate.PhaseIntegrating
				run.Failure = "change cannot be replayed onto the moved integration target"
				run.PullRequest = &runstate.PullRequest{
					Remote: "origin", Branch: "yoyodyne/yoyodyne-epic-272/2f6e6e0a", Number: 511, State: "OPEN",
				}
			}
			harness.inFlight[ended.ID] = run

			schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != ready.ID {
				t.Fatalf("started = %#v, want the ready sibling pulled beside a %s run over the other: %s",
					schedule.Started, status, schedule.Render())
			}
			if len(schedule.Deferred) != 0 {
				t.Fatalf("deferred = %#v, want nothing held behind a run that has ended", schedule.Deferred)
			}
			if schedule.Occupied != 0 {
				t.Fatalf("occupied = %d, want a %s run to hold no developer slot", schedule.Occupied, status)
			}
			if reason := harness.selectionFor(ready.ID).Reason; strings.Contains(reason, "held back") {
				t.Fatalf("reason = %q, want the sibling to have waited for nothing", reason)
			}
		})
	}
}

// The other half of the same predicate: a run that is pending or running holds
// its epic whatever phase it is in. A run integrating is the case worth
// stating, because "failed/integrating" is how the 2026-09-16 run was described
// and the phase is not what decided anything — a running run at that phase is a
// promotion in progress, listed by `yoyo status`, and exactly the run a sibling
// would race.
func TestSchedulerHoldsAnEpicOnASiblingWhoseRunIsInFlightWhateverItsPhase(t *testing.T) {
	t.Parallel()

	for _, run := range []runstate.State{
		{RunID: "run-pending", Status: runstate.StatusPending},
		{RunID: "run-integrating", Status: runstate.StatusRunning, Phase: runstate.PhaseIntegrating},
	} {
		t.Run(run.RunID, func(t *testing.T) {
			t.Parallel()

			claimed := beads.WorkItem{ID: "yoyodyne-epic.1", Title: "First half", Status: "in_progress", Priority: 1, Parent: "yoyodyne-epic"}
			sibling := beads.WorkItem{ID: "yoyodyne-epic.2", Title: "Second half", Status: "open", Priority: 1, Parent: "yoyodyne-epic"}
			harness := newScheduleHarness(claimed, sibling)
			harness.capacity = 2
			run.WorkItemID = claimed.ID
			harness.inFlight[claimed.ID] = run

			schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}
			if len(schedule.Started) != 0 {
				t.Fatalf("started = %#v, want nothing started beside a %s run over the same epic", schedule.Started, run.Status)
			}
			if len(schedule.Deferred) != 1 || !strings.Contains(schedule.Deferred[0].Reason, run.RunID) {
				t.Fatalf("deferred = %#v, want the sibling held behind %s: %s", schedule.Deferred, run.RunID, schedule.Render())
			}
			if schedule.Occupied != 1 {
				t.Fatalf("occupied = %d, want the run in flight to hold a developer slot", schedule.Occupied)
			}
		})
	}
}

// What the schedule says about a held item is what the last pull found, not the
// first. A session that holds a sibling behind one run, and then behind the run
// that follows it, reports the second: the schedule is rendered when the session
// ends, and on 2026-09-18 one rendered with each sibling's first reason named a
// run two days dead as what every one of them was still waiting on.
func TestWatchingReportsTheLastRunAHeldItemWaitedBehind(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(siblings("yoyodyne-epic", "yoyodyne-epic.1")...)
	harness.capacity = 2
	harness.admit(
		beads.WorkItem{ID: "yoyodyne-epic.8", Title: "First elsewhere", Status: "in_progress", Priority: 1, Parent: "yoyodyne-epic"},
		beads.WorkItem{ID: "yoyodyne-epic.9", Title: "Second elsewhere", Status: "in_progress", Priority: 1, Parent: "yoyodyne-epic"},
	)
	harness.inFlight["yoyodyne-epic.8"] = runstate.State{
		RunID: "run-first", WorkItemID: "yoyodyne-epic.8", Status: runstate.StatusRunning,
	}
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 2 {
			// The first run ends and another process starts the next sibling
			// before this session's next pull.
			h.mu.Lock()
			delete(h.inFlight, "yoyodyne-epic.8")
			h.inFlight["yoyodyne-epic.9"] = runstate.State{
				RunID: "run-second", WorkItemID: "yoyodyne-epic.9", Status: runstate.StatusRunning,
			}
			h.mu.Unlock()
		}
		return sleeps < 4
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 0 {
		t.Fatalf("started = %#v, want the sibling held for the whole session", schedule.Started)
	}
	if len(schedule.Deferred) != 1 || schedule.Deferred[0].WorkItemID != "yoyodyne-epic.1" {
		t.Fatalf("deferred = %#v, want the held sibling named once", schedule.Deferred)
	}
	reason := schedule.Deferred[0].Reason
	if !strings.Contains(reason, "run-second") || strings.Contains(reason, "run-first") {
		t.Fatalf("deferred reason = %q, want the run the last pull held it behind rather than the first", reason)
	}
}

// A held-back item is held back for as long as the run it would race lasts and
// no longer. What is remembered across a watching session is the fact of the
// wait, so the run that finally takes the item can account for it; nothing about
// the hold survives the run that caused it.
func TestWatchingPullsASequencedItemOnceTheRunItWouldRaceEnds(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(siblings("yoyodyne-epic", "yoyodyne-epic.1")...)
	harness.capacity = 2
	harness.inFlight["yoyodyne-epic.9"] = runstate.State{
		RunID: "run-outside", WorkItemID: "yoyodyne-epic.9", Status: runstate.StatusRunning,
	}
	// The other process's item is in the tracker as claimed work, which is what
	// says what it is going to change.
	harness.admit(beads.WorkItem{ID: "yoyodyne-epic.9", Title: "Elsewhere", Status: "in_progress", Priority: 1, Parent: "yoyodyne-epic"})
	harness.onSleep = func(h *scheduleHarness, sleeps int) bool {
		if sleeps == 2 {
			h.mu.Lock()
			delete(h.inFlight, "yoyodyne-epic.9")
			h.items[1].Status = "closed"
			h.mu.Unlock()
		}
		return sleeps < 4
	}

	scheduler := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep}
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-epic.1" {
		t.Fatalf("started = %#v, want the sibling pulled once the run it would race ended: %s",
			schedule.Started, schedule.Render())
	}
	// The deferral is a report rather than a decision, and an item held across
	// two polls is one line in it.
	if len(schedule.Deferred) != 1 {
		t.Fatalf("deferred = %#v, want the hold said once rather than once per poll", schedule.Deferred)
	}
	reason := harness.selectionFor("yoyodyne-epic.1").Reason
	if !strings.Contains(reason, "held back earlier in this session") || !strings.Contains(reason, "yoyodyne-epic.9") {
		t.Fatalf("reason = %q, want the wait it came out of recorded with what caused it", reason)
	}
}

// namesRunOf reports a hold reason naming the run over one item, in either of
// the ways a pull can know it: as this session's own, where the pull that held
// the item had not yet seen the run reserve, and by the identifier the fake
// harness mints for it afterwards.
func namesRunOf(reason, item string) bool {
	return strings.Contains(reason, "the run this session started for "+item+",") ||
		strings.Contains(reason, "run-"+item+" ("+item+")")
}

// siblings builds the ordinary decomposition: several open items at one
// priority, all broken out of one epic.
func siblings(parent string, ids ...string) []beads.WorkItem {
	items := make([]beads.WorkItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, beads.WorkItem{ID: id, Title: id, Status: "open", Priority: 2, Parent: parent})
	}
	return items
}
