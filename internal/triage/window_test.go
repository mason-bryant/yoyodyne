package triage

import (
	"fmt"
	"testing"
	"time"
)

// windowEntry is one stopped run on the docket, recorded a number of days
// before the moment the tests read it at.
func windowEntry(class Class, run, item string, daysAgo int) Entry {
	return Entry{
		SchemaVersion: SchemaVersion,
		Key:           Key(class, run),
		Class:         class,
		ProductID:     "yoyodyne",
		RunID:         run,
		WorkItemID:    item,
		RecordedAt:    windowNow.AddDate(0, 0, -daysAgo),
	}
}

var windowNow = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func closedAmong(items ...string) func(string) bool {
	closed := map[string]bool{}
	for _, item := range items {
		closed[item] = true
	}
	return func(item string) bool { return closed[item] }
}

func stoppageKeys(stoppages []Stoppage) []string {
	keys := make([]string, 0, len(stoppages))
	for _, stoppage := range stoppages {
		keys = append(keys, stoppage.Entry.Key)
	}
	return keys
}

// The docket of 2026-09-25 in miniature: entries on closed work, a run that
// left two entries, a settled entry, and live stoppages recorded out of order.
// What the window reads is the live ones, once each, oldest stoppage first.
func TestLiveKeepsOneEntryPerLiveStoppedRunOldestFirst(t *testing.T) {
	t.Parallel()

	settled := windowEntry(ClassStoppedRun, "run-settled", "item-settled", 40)
	settled.Closed = &Closure{Decision: "rescope", ClosedAt: windowNow.AddDate(0, 0, -1)}
	repeatFirst := windowEntry(ClassStoppedRun, "run-repeated", "item-repeated", 20)
	repeatLater := windowEntry(ClassPublication, "run-repeated", "item-repeated", 3)
	entries := []Entry{
		windowEntry(ClassStoppedRun, "run-dead-1", "item-closed", 50),
		windowEntry(ClassStoppedRun, "run-new", "item-new", 7),
		repeatLater,
		settled,
		windowEntry(ClassStoppedRun, "run-oldest", "item-oldest", 36),
		repeatFirst,
		windowEntry(ClassStoppedRun, "run-dead-2", "item-closed", 2),
	}

	live := Live(entries, closedAmong("item-closed"), windowNow)

	if live.Dead != 2 || live.Settled != 1 || live.Folded != 1 {
		t.Fatalf("dead, settled, folded = %d, %d, %d, want 2, 1, 1", live.Dead, live.Settled, live.Folded)
	}
	got := stoppageKeys(live.Stoppages)
	want := []string{Key(ClassStoppedRun, "run-oldest"), repeatLater.Key, Key(ClassStoppedRun, "run-new")}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("live stoppages = %v, want %v", got, want)
	}
	// The repeated run is listed once, by the entry it stands at now, and waits
	// from when it first stopped rather than from its later entry.
	repeated := live.Stoppages[1]
	if repeated.Folded != 1 || !repeated.Since.Equal(repeatFirst.RecordedAt) {
		t.Fatalf("repeated run = folded %d since %s, want folded 1 since %s", repeated.Folded, repeated.Since, repeatFirst.RecordedAt)
	}
}

// A tracker nobody could read closes nothing: every entry stays live rather than
// being hidden on the strength of a listing that was never read.
func TestLiveTakesNothingForDeadWithoutATrackerAnswer(t *testing.T) {
	t.Parallel()

	entries := []Entry{windowEntry(ClassStoppedRun, "run-a", "item-a", 3)}
	if live := Live(entries, nil, windowNow); live.Dead != 0 || len(live.Stoppages) != 1 {
		t.Fatalf("Live(nil) = %+v, want the entry live", live)
	}
}

// The walk resumes past the position, carries on from the oldest end once it
// reaches the newest, and lets a critical jump it wherever it sits.
func TestWalkResumesPastThePositionWithCriticalsAhead(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		windowEntry(ClassStoppedRun, "run-1", "item-1", 30),
		windowEntry(ClassStoppedRun, "run-2", "item-2", 20),
		windowEntry(ClassStoppedRun, "run-3", "item-3", 10),
		windowEntry(ClassEscalation, "run-4", "item-4", 1),
		windowEntry(ClassStoppedRun, "run-5", "item-5", 5),
	}
	live := Live(entries, closedAmong(), windowNow)

	first := Walk(live.Stoppages, WindowPosition{})
	if got := stoppageKeys(first.Urgent); fmt.Sprint(got) != fmt.Sprint([]string{Key(ClassEscalation, "run-4")}) {
		t.Fatalf("urgent = %v, want the escalation", got)
	}
	if got, want := stoppageKeys(first.Next), []string{Key(ClassStoppedRun, "run-1"), Key(ClassStoppedRun, "run-2"), Key(ClassStoppedRun, "run-3"), Key(ClassStoppedRun, "run-5")}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("first walk = %v, want %v", got, want)
	}

	// A pass that showed the first two leaves the position at the second, and the
	// next pass starts with what that one did not reach.
	second := Walk(live.Stoppages, first.Next[1].At())
	if got, want := stoppageKeys(second.Next), []string{Key(ClassStoppedRun, "run-3"), Key(ClassStoppedRun, "run-5"), Key(ClassStoppedRun, "run-1"), Key(ClassStoppedRun, "run-2")}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("resumed walk = %v, want %v", got, want)
	}
}

// A decision the harness was stopped carrying out by a gate that will not clear
// on its own is critical; one waiting on a gate that clears by itself is not.
func TestCriticalIsAnEscalationOrACarryOutStoppedForGood(t *testing.T) {
	t.Parallel()

	decided := windowEntry(ClassStoppedRun, "run-a", "item-a", 3)
	decided.Closed = &Closure{Decision: "repair", ClosedAt: windowNow.AddDate(0, 0, -2)}
	decided.CarryOut = &CarryOut{Decision: "repair", Gate: "worktree", RefusedAt: windowNow.AddDate(0, 0, -1)}
	if !decided.Critical() || !decided.Undecided(windowNow) {
		t.Fatalf("a refused carry-out: critical %v undecided %v, want both", decided.Critical(), decided.Undecided(windowNow))
	}
	decided.CarryOut.Waiting = true
	if decided.Critical() {
		t.Fatal("a carry-out waiting on a gate that clears by itself was read as critical")
	}
	if plain := windowEntry(ClassStoppedRun, "run-b", "item-b", 3); plain.Critical() {
		t.Fatal("an ordinary stopped run was read as critical")
	}
}
