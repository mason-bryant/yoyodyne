package readmodel

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The five stoppages the 2026-09-04 reading named as the ones an audit must pass
// over. Four are runs whose branch and worktree the harness still holds; the
// fifth reached the development manager and has been sitting on her answer. None
// of them has an unfinished dependency, so nothing but this holds them back, and
// releasing any of them starts a fresh run on top of a change that is still
// there.
func TestPreservedWorkAndUndecidedStoppagesAreHeldForAPerson(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	delivered := time.Date(2026, 9, 3, 1, 31, 0, 0, time.UTC)
	held := heldForAPerson(
		[]runstate.State{
			preservedRun("run-f4fbf60a", "yoyodyne-ifd.148", stopped),
			preservedRun("run-5035c832", "yoyodyne-ifd.153", stopped),
			preservedRun("run-f28ebe44", "yoyodyne-ifd.174", stopped),
			preservedRun("run-031981f8", "yoyodyne-ifd.68.20", stopped),
		},
		[]runstate.Escalation{{
			WorkItemID: "yoyodyne-ifd.241", RunID: "run-ffbfc9d1",
			Attempts: 1, DeliveredAt: &delivered,
		}},
	)

	for _, id := range []string{
		"yoyodyne-ifd.148", "yoyodyne-ifd.153", "yoyodyne-ifd.174", "yoyodyne-ifd.68.20",
	} {
		reason := heldReason(t, held, id)
		if !strings.Contains(reason, "its change is preserved") {
			t.Fatalf("%s is held for %q, want the preserved change named", id, reason)
		}
	}
	pending := heldReason(t, held, "yoyodyne-ifd.241")
	if !strings.Contains(pending, "in front of the development manager") {
		t.Fatalf("yoyodyne-ifd.241 is held for %q, want the undelivered decision named", pending)
	}
}

// The other side of the same rule, and the one that ends the idle morning: a run
// that stopped and was cleaned up afterwards holds nothing. Its change is gone,
// so there is nothing for a fresh run to strand, and the item goes back to being
// decided by its dependencies.
func TestAStoppageWhoseWorkWasCleanedUpHoldsNothing(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	decided := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	swept := preservedRun("run-0aa11bb2", "yoyodyne-ifd.78", stopped)
	swept.BranchRemoved = true
	swept.WorktreeRemoved = true

	held := heldForAPerson(
		[]runstate.State{
			swept,
			// A run that ended without a durable blocker was never handed to anybody,
			// so it is nobody's to decide about however much of it survives.
			func() runstate.State {
				finished := preservedRun("run-3c4d5e6f", "yoyodyne-ifd.243", stopped)
				finished.Blocker = ""
				finished.Status = runstate.StatusSucceeded
				return finished
			}(),
		},
		[]runstate.Escalation{{
			// Answered: triage said what happens to it, so this is not what holds it.
			WorkItemID: "yoyodyne-ifd.78", RunID: "run-0aa11bb2",
			Attempts: 1, DeliveredAt: &decided, Decision: "rerun", Reason: "the ground moved",
		}},
	)

	for _, id := range []string{"yoyodyne-ifd.78", "yoyodyne-ifd.243"} {
		if reason, ok := held.Reason(id); ok {
			t.Fatalf("%s was held for %q, want nothing holding it", id, reason)
		}
	}
}

// What an undecided stoppage says is which person it is waiting on, and the
// three answers are three different people to go to: the development manager who
// has it and has not answered, the harness that has not finished putting it to
// her, and — once the tries are gone — whoever reads the queue next. Each is
// pinned rather than left to whichever branch a fixture happens to take, because
// the one that reads as quiet and is not, a stoppage nobody was ever asked
// about, is the one that costs a morning.
func TestAnUndecidedStoppageSaysWhichPersonItIsWaitingOn(t *testing.T) {
	t.Parallel()

	const (
		stoppedItem = "yoyodyne-ifd.153"
		stoppedRun  = "run-5035c832"
	)
	delivered := time.Date(2026, 9, 3, 1, 31, 0, 0, time.UTC)

	for _, stoppage := range []struct {
		name       string
		escalation runstate.Escalation
		want       string
	}{
		{
			name: "delivered and unanswered",
			escalation: runstate.Escalation{
				WorkItemID: stoppedItem, RunID: stoppedRun,
				Attempts: 1, DeliveredAt: &delivered,
			},
			// "is in front of" rather than "in front of": the exhausted case below
			// says "could not be put in front of", so the shorter phrase would pass
			// on either branch and pin neither.
			want: "is in front of the development manager",
		},
		{
			// An attempt failed and there are tries left, so the harness is still
			// going to ask. It is held meanwhile: nobody has decided anything, and a
			// fresh run started under a stoppage still on its way to her is the same
			// mistake as one started under a stoppage she is holding.
			name: "not delivered with attempts left",
			escalation: runstate.Escalation{
				WorkItemID: stoppedItem, RunID: stoppedRun,
				Attempts: runstate.MaxEscalationAttempts - 1,
				Problem:  "her conversation was busy",
			},
			want: "has not reached the development manager yet",
		},
		{
			name: "delivery attempts exhausted",
			escalation: runstate.Escalation{
				WorkItemID: stoppedItem, RunID: stoppedRun,
				Attempts: runstate.MaxEscalationAttempts,
				Problem:  "development manager reported failure: cancelled",
			},
			want: "needs a person",
		},
	} {
		t.Run(stoppage.name, func(t *testing.T) {
			t.Parallel()

			held := heldForAPerson(nil, []runstate.Escalation{stoppage.escalation})
			reason := heldReason(t, held, stoppedItem)
			if !strings.Contains(reason, stoppage.want) {
				t.Fatalf("the hold says %q, want it to name %q", reason, stoppage.want)
			}
		})
	}
}

// An item that stopped more than once is described by its latest stoppage. An
// older run's account would send somebody after a branch that a later run has
// already superseded.
func TestTheLatestStoppageDescribesAnItemThatStoppedTwice(t *testing.T) {
	t.Parallel()

	first := preservedRun("run-aaaaaaaa", "yoyodyne-ifd.100", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC))
	second := preservedRun("run-bbbbbbbb", "yoyodyne-ifd.100", time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))

	for _, order := range [][]runstate.State{{first, second}, {second, first}} {
		reason := heldReason(t, heldForAPerson(order, nil), "yoyodyne-ifd.100")
		if !strings.Contains(reason, "run-bbbbbbbb") {
			t.Fatalf("the hold names %q, want the later run", reason)
		}
	}
}

// A reading that failed is an error rather than an empty answer, because an
// empty answer is indistinguishable from nothing being held and would release
// exactly the work this holds.
func TestAFailedReadingIsAnErrorRatherThanNoHolds(t *testing.T) {
	t.Parallel()

	unreadable := errors.New("state root is not readable")
	if _, err := HeldForAPerson(failingStoppages{runs: unreadable}); !errors.Is(err, unreadable) {
		t.Fatalf("HeldForAPerson() error = %v, want the run reading's failure", err)
	}
	if _, err := HeldForAPerson(failingStoppages{escalations: unreadable}); !errors.Is(err, unreadable) {
		t.Fatalf("HeldForAPerson() error = %v, want the escalation reading's failure", err)
	}
}

func preservedRun(runID, workItemID string, stopped time.Time) runstate.State {
	return runstate.State{
		RunID:        runID,
		WorkItemID:   workItemID,
		Status:       runstate.StatusFailed,
		UpdatedAt:    stopped,
		Branch:       "yoyodyne/" + workItemID + "/" + runID,
		WorktreePath: "/state/worktrees/" + runID,
		Blocker:      "Yoyodyne stopped this item: its independent reviewer still required repair after every permitted attempt.",
	}
}

func heldReason(t *testing.T, held backlog.Holds, id string) string {
	t.Helper()
	reason, ok := held.Reason(id)
	if !ok {
		t.Fatalf("%s is not held, want it held for a person", id)
	}
	return reason
}

type failingStoppages struct {
	runs        error
	escalations error
}

func (f failingStoppages) Recorded() ([]runstate.State, error) { return nil, f.runs }

func (f failingStoppages) Escalated() ([]runstate.Escalation, error) { return nil, f.escalations }
