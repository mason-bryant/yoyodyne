package orchestrator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A developer slot that prefers a label: it pulls the label's ready work first
// and the rest only when none is ready, a slot with no preference leaves labelled
// work to it while it is free, and the report says which slot took what and what
// was left for another slot.

// labelled builds one open item at one priority carrying labels.
func labelled(id string, priority int, labels ...string) beads.WorkItem {
	return beads.WorkItem{ID: id, Title: id, Status: "open", Priority: priority, Labels: labels}
}

// slotReplay holds every run this harness starts until the test releases it, so
// a pull's choice is observed against exactly the runs in flight the test put
// there rather than against whatever happened to finish first.
type slotReplay struct {
	harness  *scheduleHarness
	releases map[string]chan struct{}
}

func newSlotReplay(items ...beads.WorkItem) *slotReplay {
	replay := &slotReplay{harness: newScheduleHarness(items...), releases: map[string]chan struct{}{}}
	for _, item := range items {
		replay.releases[item.ID] = make(chan struct{})
	}
	replay.harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		<-replay.releases[id]
		return h.complete(id), nil
	}
	return replay
}

// release lets one run finish, once the pull that started it has been observed
// to have started as many runs as the test expects so far.
//
// What the runs were started in is read from the schedule afterwards rather
// than from the harness's order of starts: two runs one pull starts are two
// goroutines, and which of them reaches the harness first is nothing the
// scheduler decides.
func (r *slotReplay) release(t *testing.T, id string, startedSoFar int) {
	t.Helper()
	deadline := time.Now().Add(scheduleRendezvous)
	for len(r.harness.pullOrder()) < startedSoFar {
		if time.Now().After(deadline) {
			t.Fatalf("only %d run(s) started, want %d before releasing %s: %v", len(r.harness.pullOrder()), startedSoFar, id, r.harness.pullOrder())
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(r.releases[id])
}

// startedOrder is the order the scheduler chose items in, as its own record
// says it.
func startedOrder(schedule Schedule) []string {
	order := make([]string, 0, len(schedule.Started))
	for _, started := range schedule.Started {
		order = append(order, started.WorkItemID)
	}
	return order
}

// The acceptance criterion as a replay: two slots, one preferring the dashboard
// label, over a backlog of labelled and unlabelled ready items in the product
// manager's order. The preferring slot pulls dashboard work ahead of higher
// priority unlabelled items, the other slot takes the unlabelled work in order,
// and once the label's work is exhausted the preferring slot falls back to the
// rest rather than idling.
func TestAPreferringSlotPullsItsLabelFirstAndFallsBackOnceItIsExhausted(t *testing.T) {
	t.Parallel()

	replay := newSlotReplay(
		labelled("yoyodyne-plain-1", 1),
		labelled("yoyodyne-dash-1", 2, "dashboard"),
		labelled("yoyodyne-plain-2", 3),
		labelled("yoyodyne-dash-2", 4, "dashboard"),
		labelled("yoyodyne-plain-3", 5),
	)
	harness := replay.harness
	harness.capacity = 2
	harness.slots = []domain.DeveloperSlot{{Prefer: []string{"dashboard"}}, {}}

	go func() {
		// Pull 1 fills both slots: dashboard work into slot 1, the top unlabelled
		// item into slot 2. Freeing slot 1 pulls the next dashboard item; freeing
		// slot 2 pulls the next unlabelled one; freeing slot 1 again finds no
		// dashboard work and falls back to what is left.
		replay.release(t, "yoyodyne-dash-1", 2)
		replay.release(t, "yoyodyne-plain-1", 3)
		replay.release(t, "yoyodyne-dash-2", 4)
		replay.release(t, "yoyodyne-plain-2", 5)
		replay.release(t, "yoyodyne-plain-3", 5)
	}()

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	want := []string{"yoyodyne-dash-1", "yoyodyne-plain-1", "yoyodyne-dash-2", "yoyodyne-plain-2", "yoyodyne-plain-3"}
	if got := startedOrder(schedule); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("pull order = %v, want %v\n%s", got, want, schedule.Render())
	}
	slots := map[string]int{}
	for _, started := range schedule.Started {
		slots[started.WorkItemID] = started.Slot
	}
	// One item was left for another slot along the way: at the pull that freed
	// slot 1 while plain-1 still held slot 2, slot 1 pulled dash-2 ahead of
	// plain-2, and plain-2 says so — and nothing else does. An item in flight
	// that a preferring slot walked past is not reported as left for anyone.
	for _, deferred := range schedule.Deferred {
		if deferred.WorkItemID != "yoyodyne-plain-2" || !strings.Contains(deferred.Reason, "pulled yoyodyne-dash-2 ahead of it") {
			t.Errorf("%s was reported as not pulled: %s", deferred.WorkItemID, deferred.Reason)
		}
	}
	if len(schedule.Deferred) != 1 {
		t.Errorf("deferred = %+v, want plain-2 alone reported as left for another slot", schedule.Deferred)
	}
	for id, want := range map[string]int{
		"yoyodyne-dash-1": 1, "yoyodyne-plain-1": 2, "yoyodyne-dash-2": 1, "yoyodyne-plain-2": 2, "yoyodyne-plain-3": 1,
	} {
		if slots[id] != want {
			t.Errorf("%s was pulled into slot %d, want slot %d", id, slots[id], want)
		}
	}
	// Each run's recorded reason says which slot took it and on what footing.
	for id, want := range map[string]string{
		"yoyodyne-dash-1":  "pulled into developer slot 1, which prefers the dashboard label this item carries",
		"yoyodyne-plain-1": "pulled into developer slot 2, which prefers no label",
		"yoyodyne-plain-3": "pulled into developer slot 1, which prefers the dashboard label and found none of that work ready, so it fell back to the rest of the backlog",
	} {
		if reason := harness.selectionFor(id).Reason; !strings.Contains(reason, want) {
			t.Errorf("%s reason = %q, want it to say %q", id, reason, want)
		}
	}
	if schedule.Stopped != ScheduleDrained {
		t.Fatalf("stopped = %q, want the queue drained", schedule.Stopped)
	}
}

// A slot with no preference leaves labelled work to a preferring slot that is
// free to take it: with both slots free and only one dashboard item ready, the
// dashboard item goes to slot 1 however the queue ranks it, and slot 2 takes the
// unlabelled work. Where no preferring slot is free, a label is only a
// preference: the unpreferring slot takes labelled work in the order.
func TestASlotWithNoPreferenceLeavesLabelledWorkToAFreePreferringSlot(t *testing.T) {
	t.Parallel()

	replay := newSlotReplay(
		labelled("yoyodyne-dash-1", 1, "dashboard"),
		labelled("yoyodyne-dash-2", 2, "dashboard"),
		labelled("yoyodyne-plain-1", 3),
	)
	harness := replay.harness
	harness.capacity = 2
	harness.slots = []domain.DeveloperSlot{{Prefer: []string{"dashboard"}}, {}}

	go func() {
		// Pull 1: dash-1 into slot 1; slot 2, with no preferring slot free, takes
		// dash-2 rather than idling over labelled work. Then plain-1 fills
		// whichever frees first.
		replay.release(t, "yoyodyne-dash-1", 2)
		replay.release(t, "yoyodyne-dash-2", 3)
		replay.release(t, "yoyodyne-plain-1", 3)
	}()

	schedule, err := Scheduler{Open: harness.open}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	want := []string{"yoyodyne-dash-1", "yoyodyne-dash-2", "yoyodyne-plain-1"}
	if got := startedOrder(schedule); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("pull order = %v, want %v\n%s", got, want, schedule.Render())
	}
	if schedule.Started[0].Slot != 1 || schedule.Started[1].Slot != 2 {
		t.Fatalf("slots = %d, %d, want dash-1 in slot 1 and dash-2 in slot 2: %s", schedule.Started[0].Slot, schedule.Started[1].Slot, schedule.Render())
	}
}

// An item the only free slot walked past for its label is reported as left for
// another slot rather than as deferred: it waits on nothing about itself, and
// the line says which slot pulled what ahead of it. It is also what the idle
// account carries, in its own class, so the alarm can say whose move it is.
func TestTheReportNamesWorkLeftForAnotherSlotRatherThanDeferred(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(
		labelled("yoyodyne-plain-1", 1),
		labelled("yoyodyne-dash-1", 2, "dashboard"),
	)
	harness.capacity = 2
	harness.slots = []domain.DeveloperSlot{{Prefer: []string{"dashboard"}}, {}}
	// Slot 2 is taken by a run another process started, so the only free slot is
	// the one preferring dashboard.
	harness.inFlight["yoyodyne-elsewhere"] = runstate.State{RunID: "run-elsewhere", WorkItemID: "yoyodyne-elsewhere", Status: runstate.StatusRunning}
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		// The dashboard run ends with the other slot still taken, so the pull
		// that follows finds slot 1 free again and falls back to plain-1.
		return h.complete(id), nil
	}

	schedule, err := Scheduler{Open: harness.open, Limit: 1}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if got := harness.pullOrder(); strings.Join(got, ",") != "yoyodyne-dash-1" {
		t.Fatalf("pull order = %v, want the dashboard item alone: %s", got, schedule.Render())
	}
	var left *Deferred
	for index := range schedule.Deferred {
		if schedule.Deferred[index].WorkItemID == "yoyodyne-plain-1" {
			left = &schedule.Deferred[index]
		}
	}
	if left == nil {
		t.Fatalf("plain-1 is not on the report as passed over: %s", schedule.Render())
	}
	for _, want := range []string{
		string(runstate.PassedOverLeftForAnotherSlot),
		"developer slot 1 prefers the dashboard label and pulled yoyodyne-dash-1 ahead of it",
		"a slot with no preference takes this item in the product manager's order",
	} {
		if !strings.Contains(left.Reason, want) {
			t.Errorf("reason = %q, want it to say %q", left.Reason, want)
		}
	}
}

// A project whose slots prefer nothing pulls exactly as it did before slots
// could prefer anything, and its runs record exactly the reason they always did.
func TestSlotsPreferringNothingChangeNothing(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(
		labelled("yoyodyne-plain-1", 1),
		labelled("yoyodyne-dash-1", 2, "dashboard"),
	)
	harness.capacity = 1
	harness.slots = []domain.DeveloperSlot{{}}

	if _, err := (Scheduler{Open: harness.open}).Schedule(context.Background()); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if got := harness.pullOrder(); strings.Join(got, ",") != "yoyodyne-plain-1,yoyodyne-dash-1" {
		t.Fatalf("pull order = %v, want the product manager's order", got)
	}
	if reason := harness.selectionFor("yoyodyne-plain-1").Reason; strings.Contains(reason, "developer slot 1") {
		t.Fatalf("reason = %q, want no slot named where no slot prefers anything", reason)
	}
}
