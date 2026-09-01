package runstate

import (
	"strings"
	"testing"
	"time"
)

var stallMoment = time.Date(2026, 9, 1, 6, 5, 0, 0, time.UTC)

func newStallStore(t *testing.T) *StallStore {
	t.Helper()
	store, err := NewStallStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStallStore() error = %v", err)
	}
	return store
}

// The whole of the dedup, and the reason it lives in the record rather than in
// whatever is doing the checking: a checker runs on a poll loop, and "notice
// once" has to survive both the next poll and the next process.
func TestAStallIsOpenedOnceHoweverOftenItIsChecked(t *testing.T) {
	t.Parallel()

	store := newStallStore(t)
	observation := StallObservation{
		Stalled: true,
		Since:   stallMoment,
		Ready:   4,
		Chooser: "the session choosing work last recorded watching at 2026-09-01T06:05:00Z",
		At:      stallMoment.Add(45 * time.Minute),
	}
	first, err := store.Reconcile(observation)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if first.Opened == nil {
		t.Fatal("Reconcile() opened nothing, want the stall recorded")
	}
	for check := 0; check < 240; check++ {
		observation.At = observation.At.Add(15 * time.Second)
		again, err := store.Reconcile(observation)
		if err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if again.Opened != nil || again.Closed != nil {
			t.Fatalf("check %d recorded %+v, want a standing stall to change nothing", check, again)
		}
		if again.Standing == nil || again.Standing.EventID != first.Opened.EventID {
			t.Fatalf("check %d stands on %+v, want the stall that was already open", check, again.Standing)
		}
	}
	events, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("List() = %d events, want one stall however many checks agreed with it", len(events))
	}
}

// A stall that cleared closes rather than simply stopping being reported, and
// says what cleared it: a reader shown a stall that just stops has to decide for
// themselves whether it was fixed or merely stopped being looked for.
func TestAClearingStallClosesItsEventAndSaysWhatClearedIt(t *testing.T) {
	t.Parallel()

	store := newStallStore(t)
	opened, err := store.Reconcile(StallObservation{
		Stalled: true, Since: stallMoment, Ready: 2, At: stallMoment.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	cleared, err := store.Reconcile(StallObservation{
		Explains: "1 developer run(s) are in flight",
		At:       stallMoment.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if cleared.Closed == nil {
		t.Fatal("Reconcile() closed nothing, want the standing stall closed")
	}
	if cleared.Standing != nil {
		t.Fatalf("Standing = %+v, want nothing standing once the stall cleared", cleared.Standing)
	}
	if cleared.Closed.EventID != opened.Opened.EventID {
		t.Fatalf("closed %s, want the stall that was open", cleared.Closed.EventID)
	}
	if !strings.Contains(cleared.Closed.Cleared, "in flight") {
		t.Fatalf("Cleared = %q, want what accounted for it", cleared.Closed.Cleared)
	}
	if cleared.Closed.For() != 2*time.Hour {
		t.Fatalf("For() = %s, want the whole stretch it ran", cleared.Closed.For())
	}
	// And a check over a machine that is behaving records nothing at all, so the
	// log does not grow a line for every poll of a healthy product.
	quiet, err := store.Reconcile(StallObservation{Explains: "nothing ready", At: stallMoment.Add(3 * time.Hour)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if quiet.Opened != nil || quiet.Closed != nil || quiet.Standing != nil {
		t.Fatalf("Reconcile() = %+v over a healthy product, want nothing recorded", quiet)
	}
}

// The record is the history as well as the dedup: a stall that closed is still
// readable afterwards, which is the whole of what the seven and a half hours of
// 2026-09-01 left nobody.
func TestAClosedStallStaysReadableWithBothItsMoments(t *testing.T) {
	t.Parallel()

	store := newStallStore(t)
	noticed := stallMoment.Add(35 * time.Minute)
	if _, err := store.Reconcile(StallObservation{
		Stalled: true, Since: stallMoment, Ready: 3,
		Chooser: "the session choosing work last recorded watching at 2026-09-01T06:05:00Z",
		At:      noticed,
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if _, err := store.Reconcile(StallObservation{
		Explains: "1 developer run(s) are in flight", At: stallMoment.Add(8 * time.Hour),
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	// A second stall a week later is a second thing to say rather than the same
	// one said again.
	later := stallMoment.Add(7 * 24 * time.Hour)
	if _, err := store.Reconcile(StallObservation{
		Stalled: true, Since: later, Ready: 1, At: later.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	events, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("List() = %d events, want the closed one and the standing one", len(events))
	}
	closed := events[0]
	if closed.Open() {
		t.Fatalf("the first stall reads as open: %+v", closed)
	}
	if !closed.Since.Equal(stallMoment) || !closed.OpenedAt.Equal(noticed) {
		t.Fatalf("the closed stall reports since %s noticed %s, want %s and %s",
			closed.Since, closed.OpenedAt, stallMoment, noticed)
	}
	if closed.Ready != 3 || !strings.Contains(closed.Chooser, "watching") {
		t.Fatalf("the closed stall lost what it was recorded with: %+v", closed)
	}
	if !events[1].Open() {
		t.Fatalf("the second stall reads as closed: %+v", events[1])
	}
	standing, open, err := store.Standing()
	if err != nil {
		t.Fatalf("Standing() error = %v", err)
	}
	if !open || standing.EventID != events[1].EventID {
		t.Fatalf("Standing() = %+v %v, want the second stall", standing, open)
	}
}

// A record with no moment to measure from is a stall nobody can say the length
// of, and the length is the whole of what makes it worth reporting.
func TestARecordThatCouldNotBeReadBackIsRefused(t *testing.T) {
	t.Parallel()

	store := newStallStore(t)
	eventID, err := NewStallEventID()
	if err != nil {
		t.Fatalf("NewStallEventID() error = %v", err)
	}
	sound := StallEvent{
		SchemaVersion: StallSchemaVersion,
		ProductID:     "yoyodyne",
		EventID:       eventID,
		OpenedAt:      stallMoment.Add(time.Hour),
		Since:         stallMoment,
		Ready:         2,
	}
	if err := store.Record(sound); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	for name, broken := range map[string]func(*StallEvent){
		"no moment to measure from": func(e *StallEvent) { e.Since = time.Time{} },
		"no moment it was noticed":  func(e *StallEvent) { e.OpenedAt = time.Time{} },
		"a name nothing generated":  func(e *StallEvent) { e.EventID = "stall-nope" },
		"another product's stall":   func(e *StallEvent) { e.ProductID = "somebody-else" },
		"an ending with no close":   func(e *StallEvent) { e.Cleared = "a run started" },
	} {
		event := sound
		broken(&event)
		if err := store.Record(event); err == nil {
			t.Fatalf("%s: Record() error = nil, want the record refused", name)
		}
	}
}

// A product that has never gone quiet has no log rather than a broken one.
func TestAProductThatHasNeverGoneQuietReadsAsNothing(t *testing.T) {
	t.Parallel()

	store := newStallStore(t)
	events, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("List() = %v, want nothing", events)
	}
	if _, open, err := store.Standing(); err != nil || open {
		t.Fatalf("Standing() = %v %v, want nothing standing and no failure", open, err)
	}
}
