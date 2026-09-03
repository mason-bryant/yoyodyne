package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The window this exists for, as the records actually held it: a session whose
// last word was that it was watching, at 06:05, and then nothing — no stop, no
// idle poll, no run — while the tracker went on reporting work ready. Every
// existing derivation reads that as a healthy machine, because a live session is
// a live session.
func TestASessionThatDiedStillClaimingToWatchIsAStall(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 9, 1, 6, 5, 0, 0, time.UTC)
	silence := ReadSilence(Activity{
		Since:   started,
		Ready:   4,
		Watched: true,
		Now:     started.Add(7*time.Hour + 30*time.Minute),
	})
	if !silence.Stalled {
		t.Fatalf("ReadSilence() = %+v, want the dead window read as a stall", silence)
	}
	if !silence.Since.Equal(started) {
		t.Fatalf("Since = %s, want the moment the harness last started anything", silence.Since)
	}
	if silence.Explains != "" {
		t.Fatalf("Explains = %q, want nothing accounting for a stall", silence.Explains)
	}
}

// Every state that legitimately accounts for nothing having started, and the
// sentence a closed stall keeps for it. A reading that named no reason for one
// of these would wake somebody about a decision they made themselves.
func TestEveryStateThatAccountsForTheQuietSaysSo(t *testing.T) {
	t.Parallel()

	base := Activity{
		Since:   moment.Add(-4 * time.Hour),
		Ready:   3,
		Watched: true,
		Now:     moment,
	}
	for name, activity := range map[string]Activity{
		"the operator held everything": with(base, func(a *Activity) { a.OperatorHeld = true }),
		"intake is held":               with(base, func(a *Activity) { a.IntakeHeld = true }),
		"a run is in flight":           with(base, func(a *Activity) { a.Running = 1 }),
		"nobody has ever watched":      with(base, func(a *Activity) { a.Watched = false }),
		"nothing has ever been seen":   with(base, func(a *Activity) { a.Since = time.Time{} }),
		"something started just now":   with(base, func(a *Activity) { a.Since = moment.Add(-time.Minute) }),
		"the queue is drained":         with(base, func(a *Activity) { a.Ready = 0 }),
	} {
		silence := ReadSilence(activity)
		if silence.Stalled {
			t.Fatalf("%s: ReadSilence() = %+v, want no stall", name, silence)
		}
		if strings.TrimSpace(silence.Explains) == "" {
			t.Fatalf("%s: a reading that is not a stall says nothing about why", name)
		}
	}
}

// The tracker read is the one thing here that costs anything, so a caller has to
// be able to skip it. Everything except the queue's own depth answers without it,
// and a drained queue is the only state Unexplained cannot see.
func TestWhatCostsNothingIsAnsweredWithoutTheTracker(t *testing.T) {
	t.Parallel()

	quiet := Activity{Since: moment.Add(-4 * time.Hour), Watched: true, Now: moment}
	if !quiet.Unexplained() {
		t.Fatal("Unexplained() = false, want a quiet machine reported without the ready count")
	}
	held := quiet
	held.IntakeHeld = true
	if held.Unexplained() {
		t.Fatal("Unexplained() = true over a held intake, want the switch to account for it")
	}
	// And with nothing ready it is still unexplained until the count is read,
	// which is exactly why the count is read second rather than not at all.
	if silence := ReadSilence(quiet); silence.Stalled {
		t.Fatalf("ReadSilence() = %+v, want an unread queue to report no stall", silence)
	}
}

// The threshold is what separates a stall from the ordinary gap between one run
// finishing and the next being chosen.
func TestNothingIsAStallUntilTheThresholdHasPassed(t *testing.T) {
	t.Parallel()

	activity := Activity{
		Since:     moment.Add(-20 * time.Minute),
		Ready:     2,
		Watched:   true,
		Threshold: 30 * time.Minute,
		Now:       moment,
	}
	if silence := ReadSilence(activity); silence.Stalled {
		t.Fatalf("ReadSilence() = %+v, want a gap inside the threshold read as no stall", silence)
	}
	activity.Since = moment.Add(-31 * time.Minute)
	if silence := ReadSilence(activity); !silence.Stalled {
		t.Fatalf("ReadSilence() = %+v, want a gap past the threshold read as a stall", silence)
	}
	// A caller that configured nothing gets the shipped window rather than a zero
	// one, which would report every moment between two runs as a stall.
	activity.Threshold = 0
	activity.Since = moment.Add(-DefaultStallThreshold).Add(time.Minute)
	if silence := ReadSilence(activity); silence.Stalled {
		t.Fatalf("ReadSilence() = %+v, want the default threshold applied where none was given", silence)
	}
}

// A dead scheduler and a wedged one need opposite things done about them, and
// the stall itself cannot tell them apart — it is the absence of any record at
// all. What can is the last thing the log holds.
func TestTheChoosersLastWordTellsADeadSchedulerFromAWedgedOne(t *testing.T) {
	t.Parallel()

	if word := LastWord(nil); !strings.Contains(word, "has ever run") {
		t.Fatalf("LastWord() = %q, want a product nobody has watched said as one", word)
	}
	wedged := []runstate.WatchTransition{transition(runstate.WatchWatching, moment)}
	if word := LastWord(wedged); !strings.Contains(word, "watching") || !strings.Contains(word, "said nothing since") {
		t.Fatalf("LastWord() = %q, want a session still claiming to watch named as one", word)
	}
	dead := []runstate.WatchTransition{
		transition(runstate.WatchWatching, moment),
		transition(runstate.WatchStopped, moment.Add(time.Hour)),
	}
	if word := LastWord(dead); !strings.Contains(word, "no watch session is running") {
		t.Fatalf("LastWord() = %q, want a stopped session named as no session running", word)
	}
}

// The anchor is the runs' own start times, because that is the one fact a dead
// process cannot have failed to write: it was written when the run started. A
// product whose scheduler died before its first run has no run to anchor on, and
// falls back to when it was first seen watching rather than to never.
func TestTheAnchorIsTheLastRunStartAndFallsBackToBeingSeenAtAll(t *testing.T) {
	t.Parallel()

	first := moment
	latest := moment.Add(2 * time.Hour)
	runs := []runstate.State{{StartedAt: first}, {StartedAt: latest}}
	if got := LastStart(runs, nil); !got.Equal(latest) {
		t.Fatalf("LastStart() = %s, want the latest run start %s", got, latest)
	}
	sessions := []runstate.WatchTransition{
		transition(runstate.WatchWatching, moment.Add(-time.Hour)),
		transition(runstate.WatchIdle, moment),
	}
	if got := LastStart(nil, sessions); !got.Equal(moment.Add(-time.Hour)) {
		t.Fatalf("LastStart() = %s, want the earliest moment anything was seen watching", got)
	}
	if got := LastStart(nil, nil); !got.IsZero() {
		t.Fatalf("LastStart() = %s, want nothing observed reported as nothing", got)
	}
}

func with(base Activity, change func(*Activity)) Activity {
	changed := base
	change(&changed)
	return changed
}

func transition(state runstate.WatchState, at time.Time) runstate.WatchTransition {
	return runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     "watch-0123456789abcdef0123456789abcdef",
		State:         state,
		At:            at,
	}
}
