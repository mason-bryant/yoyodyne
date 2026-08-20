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
	for _, deferred := range schedule.Deferred {
		if !strings.Contains(deferred.Reason, "yoyodyne-epic") {
			t.Fatalf("deferred reason = %q, want the epic the two share named", deferred.Reason)
		}
		if !strings.Contains(deferred.Reason, "yoyodyne-epic.1") {
			t.Fatalf("deferred reason = %q, want the run it would have raced named", deferred.Reason)
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
	if harness.rendezvousFailure != nil {
		t.Fatalf("unrelated items did not overlap: %v", harness.rendezvousFailure)
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
	if harness.rendezvousFailure != nil {
		t.Fatalf("the free slot was not spent on unrelated work: %v", harness.rendezvousFailure)
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
		t.Fatalf("deferred reason = %q, want the run in flight named", schedule.Deferred[0].Reason)
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want a drain that found nothing startable to end", schedule.Stopped)
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

// siblings builds the ordinary decomposition: several open items at one
// priority, all broken out of one epic.
func siblings(parent string, ids ...string) []beads.WorkItem {
	items := make([]beads.WorkItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, beads.WorkItem{ID: id, Title: id, Status: "open", Priority: 2, Parent: parent})
	}
	return items
}
