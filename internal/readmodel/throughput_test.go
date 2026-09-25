package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// noon is a local noon, so a run an hour ago and a run thirty hours ago fall on
// today and yesterday whatever timezone the test runs in.
var noon = time.Date(2026, 9, 19, 12, 0, 0, 0, time.Local)

func at(offset time.Duration) *time.Time {
	moment := noon.Add(offset)
	return &moment
}

func terminal(id string, status runstate.Status, started, completed time.Duration, integrated bool, blocker string) runstate.State {
	state := runstate.State{
		RunID:       id,
		WorkItemID:  "yoyodyne-ifd." + id,
		Status:      status,
		StartedAt:   noon.Add(started),
		CompletedAt: at(completed),
		Blocker:     blocker,
	}
	if integrated {
		state.Integration = &runstate.Integration{TargetBranch: "main", SourceCommit: "abc", TargetCommit: "def", PreviousTargetCommit: "000"}
	}
	return state
}

func window(t *testing.T, reading Throughput, label string) Window {
	t.Helper()
	for _, window := range reading.Windows {
		if window.Label == label {
			return window
		}
	}
	t.Fatalf("no %q window in %+v", label, reading.Windows)
	return Window{}
}

// The two windows count the run history's own outcomes by when each run ended,
// and a run counts as landed exactly where the terminal prints it as succeeded
// with a promotion recorded.
func TestThroughputCountsEachEndingIntoTheWindowItEndedIn(t *testing.T) {
	t.Parallel()
	recorded := []runstate.State{
		// Today: landed, succeeded without promoting, stopped, and one still running.
		terminal("a", runstate.StatusSucceeded, -3*time.Hour, -time.Hour, true, ""),
		terminal("b", runstate.StatusSucceeded, -3*time.Hour, -2*time.Hour, false, ""),
		terminal("c", runstate.StatusFailed, -4*time.Hour, -30*time.Minute, false, "the reviewer asked for repair"),
		{RunID: "d", WorkItemID: "yoyodyne-ifd.d", Status: runstate.StatusRunning, StartedAt: noon.Add(-10 * time.Minute)},
		// Yesterday: landed, cancelled, timed out, failed.
		terminal("e", runstate.StatusSucceeded, -32*time.Hour, -30*time.Hour, true, ""),
		terminal("f", runstate.StatusCancelled, -32*time.Hour, -31*time.Hour, false, ""),
		terminal("g", runstate.StatusTimedOut, -33*time.Hour, -31*time.Hour, false, ""),
		terminal("h", runstate.StatusFailed, -33*time.Hour, -31*time.Hour, false, ""),
		// Eight days ago: outside both windows however it ended.
		terminal("i", runstate.StatusSucceeded, -9*24*time.Hour, -8*24*time.Hour, true, ""),
		// Started yesterday, landed today: an ending counts where it ended, a start
		// where it started.
		terminal("j", runstate.StatusSucceeded, -26*time.Hour, -20*time.Minute, true, ""),
		// Died two days in without writing a completion: it ended when its record
		// last moved, which was today.
		{RunID: "k", WorkItemID: "yoyodyne-ifd.k", Status: runstate.StatusFailed, StartedAt: noon.Add(-40 * time.Hour), UpdatedAt: noon.Add(-time.Hour)},
	}
	reading := ReadThroughput(context.Background(), ThroughputSources{
		Runs: fakeRuns{recorded: recorded},
		Now:  func() time.Time { return noon },
	})
	if reading.RunsProblem != "" {
		t.Fatalf("a problem on a readable reading: %q", reading.RunsProblem)
	}
	today := window(t, reading, "today")
	if today.Days != 1 || today.Since != runstate.LocalDay(noon) {
		t.Fatalf("today's window is %+v", today)
	}
	if today.Started != 4 || today.Landed != 2 || today.Succeeded != 1 || today.Stopped != 1 || today.Failed != 1 || today.Cancelled+today.TimedOut != 0 {
		t.Fatalf("today counted %+v", today)
	}
	week := window(t, reading, "last 7 days")
	if week.Days != 7 || week.Since != runstate.LocalDay(noon.AddDate(0, 0, -6)) {
		t.Fatalf("the week's window is %+v", week)
	}
	if week.Started != 10 || week.Landed != 3 || week.Succeeded != 1 || week.Stopped != 1 || week.Cancelled != 1 || week.TimedOut != 1 || week.Failed != 2 {
		t.Fatalf("the week counted %+v", week)
	}
	// The landed runs are named as well as counted, newest first, and the list
	// is the count: the surface that opens the landed grouping lists what the
	// figure counted.
	names := func(landed []LandedRun) []string {
		ids := make([]string, 0, len(landed))
		for _, run := range landed {
			ids = append(ids, run.RunID)
		}
		return ids
	}
	if got := names(today.LandedItems); len(got) != today.Landed || got[0] != "j" || got[1] != "a" {
		t.Fatalf("today's landed items = %v, want j then a", got)
	}
	if got := names(week.LandedItems); len(got) != week.Landed || got[2] != "e" || week.LandedItems[2].WorkItemID != "yoyodyne-ifd.e" || !week.LandedItems[2].LandedAt.Equal(noon.Add(-30*time.Hour)) {
		t.Fatalf("the week's landed items = %+v, want j, a, e with e's item and ending", week.LandedItems)
	}
}

// The endings are counted from a real run store as well as from the fake: the
// same derivation over records the store wrote, validated and read back, so a
// field the store keeps differently from the fake cannot move a count unseen.
// No run records a promotion, because the store validates one against a whole
// review and integration record; which succeeded runs count as landed is pinned
// above against the fake.
func TestThroughputCountsTheEndingsARealRunStoreHolds(t *testing.T) {
	t.Parallel()
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	record := func(started, completed time.Time, status runstate.Status, blocker string) {
		t.Helper()
		id, err := runstate.NewRunID()
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Create(runstate.State{
			SchemaVersion: runstate.StateSchemaVersion,
			RunID:         id,
			ProductID:     "yoyodyne",
			RepositoryID:  "yoyodyne",
			WorkItemID:    "yoyodyne-ifd.1",
			Backend:       "claude-code",
			Status:        status,
			StartedAt:     started,
			UpdatedAt:     completed,
			CompletedAt:   &completed,
			Blocker:       blocker,
		}); err != nil {
			t.Fatal(err)
		}
	}
	record(noon.Add(-3*time.Hour), noon.Add(-time.Hour), runstate.StatusSucceeded, "")
	record(noon.Add(-32*time.Hour), noon.Add(-30*time.Hour), runstate.StatusFailed, "the reviewer asked for repair")
	record(noon.Add(-33*time.Hour), noon.Add(-31*time.Hour), runstate.StatusCancelled, "")
	record(noon.Add(-9*24*time.Hour), noon.Add(-8*24*time.Hour), runstate.StatusSucceeded, "")

	reading := ReadThroughput(context.Background(), ThroughputSources{Runs: store, Now: func() time.Time { return noon }})
	if reading.RunsProblem != "" {
		t.Fatalf("a problem over a readable state directory: %q", reading.RunsProblem)
	}
	today := window(t, reading, "today")
	week := window(t, reading, "last 7 days")
	if today.Started != 1 || today.Succeeded != 1 || today.Stopped != 0 || today.Cancelled != 0 || today.Landed != 0 {
		t.Fatalf("today counted %+v: one run succeeded today without promoting", today)
	}
	if week.Started != 3 || week.Succeeded != 1 || week.Stopped != 1 || week.Cancelled != 1 || week.Landed != 0 {
		t.Fatalf("the week counted %+v: one succeeded today, one stopped and one cancelled yesterday, and the eight-day-old run is outside it", week)
	}
}

// Runs that cannot be read cost the reading its endings and say so; it never
// reports a zero in their place, and the landed list follows them rather than
// being an empty list a page would show as nothing having landed.
func TestThroughputSaysTheRunsCouldNotBeRead(t *testing.T) {
	t.Parallel()
	landed := []runstate.State{terminal("a", runstate.StatusSucceeded, -3*time.Hour, -time.Hour, true, "")}

	counted := ReadThroughput(context.Background(), ThroughputSources{
		Runs: fakeRuns{recorded: landed},
		Now:  func() time.Time { return noon },
	})
	if counted.RunsProblem != "" || window(t, counted, "today").Landed != 1 || window(t, counted, "today").LandedItems == nil {
		t.Fatalf("a readable reading reads as %+v", counted)
	}

	uncounted := ReadThroughput(context.Background(), ThroughputSources{
		Runs: fakeRuns{failRecorded: errors.New("scan recorded: no such directory")},
		Now:  func() time.Time { return noon },
	})
	if uncounted.RunsProblem == "" || !strings.Contains(uncounted.RunsProblem, "no such directory") {
		t.Fatalf("unreadable runs read as %+v", uncounted)
	}
	if window(t, uncounted, "today").LandedItems != nil {
		t.Fatalf("the landed list does not follow the runs: %+v", window(t, uncounted, "today").LandedItems)
	}
	// The problem reaches the JSON a page reads under its own name, and the
	// windows are still there to be labeled.
	encoded, err := json.Marshal(uncounted)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"runs_problem":"the recorded runs could not be read: scan recorded: no such directory"`) {
		t.Fatalf("unreadable runs encode as %s", encoded)
	}

	unwired := ReadThroughput(context.Background(), ThroughputSources{Now: func() time.Time { return noon }})
	if !strings.Contains(unwired.RunsProblem, "nothing was wired") || len(unwired.Windows) != 2 {
		t.Fatalf("an unwired reading reads as %+v", unwired)
	}

	// A source the caller could not open is named by the reason it gave, which
	// is what the page's error state has to say: what failed, not that a wire
	// was missing.
	unopened := ReadThroughput(context.Background(), ThroughputSources{
		RunsProblem: "state root must be an absolute path",
		Now:         func() time.Time { return noon },
	})
	if unopened.RunsProblem != "the recorded runs could not be opened: state root must be an absolute path" {
		t.Fatalf("an unopened reading reads as %+v", unopened)
	}
}
