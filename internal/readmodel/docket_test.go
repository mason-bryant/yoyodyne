package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

var docketReadAt = time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)

type fakeDocket struct {
	entries []triage.Entry
	fail    error
}

func (f fakeDocket) Docket() ([]triage.Entry, error) { return f.entries, f.fail }

type admitted map[string][]beads.WorkItem

func (a admitted) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	return a[status], nil
}

type failingTracker struct{ err error }

func (f failingTracker) List(context.Context, string) ([]beads.WorkItem, error) {
	return nil, f.err
}

func stoppedEntry(runID, workItemID string, recorded time.Time) triage.Entry {
	return triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.Key(triage.ClassStoppedRun, runID),
		Class:         triage.ClassStoppedRun,
		ProductID:     "example",
		RunID:         runID,
		WorkItemID:    workItemID,
		WorkItemTitle: "Work on " + workItemID,
		RecordedAt:    recorded,
		Blocker:       "Yoyodyne stopped this item: its independent reviewer still required repair after every permitted attempt.",
	}
}

func decidedAbout(runID, decision string, at time.Time) runstate.TriageDecision {
	return runstate.TriageDecision{
		Decision: decision, RunID: runID, Reason: "because", DecidedBy: "development manager",
		Conversation: "chat-dm", Turn: 3, DecidedAt: at,
	}
}

// The shape the operator found on 2026-09-14, replayed: stoppages nobody has
// decided about, a decision recorded that nothing carried out, and beside them
// every kind of entry that is not waiting on anybody — decided and acted on,
// decided with a wait, on closed work, repaired and landed, re-run. The reading
// has to name the first two and count the rest, because a sweep handed this is
// what makes "the queue is quiet" impossible over thirty waiting entries.
func TestTheDocketStandingNamesWhatWaitsOnHerAndWhatWaitsOnTheHarness(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	decided := time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC)
	delivered := time.Date(2026, 9, 7, 1, 21, 0, 0, time.UTC)

	undecided := preservedRun("run-ce3a1135", "yoyodyne-ifd.192", stopped)
	undecidedToo := preservedRun("run-af66cb33", "yoyodyne-ifd.187", stopped)
	uncarried := preservedRun("run-f71718d7", "yoyodyne-ifd.117.1", stopped)
	waited := preservedRun("run-0aa11bb2", "yoyodyne-ifd.78", stopped)
	closed := preservedRun("run-44e3ba15", "yoyodyne-ifd.102", stopped)
	rerunFrom := preservedRun("run-bd6c12d0", "yoyodyne-ifd.349", stopped)
	// Repaired under a grant and landed: the blocker is cleared, the run
	// succeeded, and there is nothing here for anybody.
	landed := preservedRun("run-5cf767ad", "yoyodyne-ifd.102.7", stopped)
	landed.Blocker = ""
	landed.Status = runstate.StatusSucceeded
	// Repaired under a grant and stopped again after the decision: the decision
	// stands on the record and says nothing about this stoppage.
	overtakenRun := preservedRun("run-d02b509c", "yoyodyne-ifd.346", stopped)
	stoppedAgain := decided.Add(2 * time.Hour)
	overtakenRun.CompletedAt = &stoppedAgain

	entries := []triage.Entry{
		stoppedEntry(undecided.RunID, undecided.WorkItemID, stopped),
		stoppedEntry(undecidedToo.RunID, undecidedToo.WorkItemID, stopped.Add(time.Hour)),
		stoppedEntry(uncarried.RunID, uncarried.WorkItemID, stopped),
		stoppedEntry(waited.RunID, waited.WorkItemID, stopped),
		stoppedEntry(closed.RunID, closed.WorkItemID, stopped),
		stoppedEntry(landed.RunID, landed.WorkItemID, stopped),
		stoppedEntry(overtakenRun.RunID, overtakenRun.WorkItemID, stopped),
		func() triage.Entry {
			entry := stoppedEntry(rerunFrom.RunID, rerunFrom.WorkItemID, stopped)
			entry.Rerun = &triage.Rerun{ClaimedAt: decided, RunID: "run-fresh"}
			return entry
		}(),
		{
			SchemaVersion: triage.SchemaVersion,
			Key:           triage.UnreadyKey("yoyodyne-ifd.400", []string{"citation"}),
			Class:         triage.ClassUnreadyItem,
			ProductID:     "example",
			WorkItemID:    "yoyodyne-ifd.400",
			RecordedAt:    stopped,
			Unready:       &triage.Unready{ReadAt: stopped, Prerequisites: []triage.Prerequisite{{Kind: "citation", Missing: "a file"}}},
		},
	}
	open := []beads.WorkItem{}
	for _, run := range []runstate.State{undecided, undecidedToo, uncarried, waited, landed, overtakenRun, rerunFrom} {
		open = append(open, beads.WorkItem{ID: run.WorkItemID, Title: "Work on " + run.WorkItemID, Status: "blocked"})
	}
	standing := ReadDocket(context.Background(), DocketSources{
		Docket: fakeDocket{entries: entries},
		Stoppages: fakeStoppages{
			runs: []runstate.State{undecided, undecidedToo, uncarried, waited, closed, landed, overtakenRun, rerunFrom},
			escalations: []runstate.Escalation{{
				DocketKey: triage.Key(triage.ClassStoppedRun, undecided.RunID), RunID: undecided.RunID,
				WorkItemID: undecided.WorkItemID, Attempts: 1, DeliveredAt: &delivered,
			}},
		},
		Decisions: recordedDecisions{
			uncarried.WorkItemID: {WorkItemID: uncarried.WorkItemID, ReviewRounds: 2, CommittedRounds: 3,
				Decisions: []runstate.TriageDecision{decidedAbout(uncarried.RunID, runstate.TriageDecisionRepair, decided)}},
			waited.WorkItemID: {WorkItemID: waited.WorkItemID,
				Decisions: []runstate.TriageDecision{decidedAbout(waited.RunID, runstate.TriageDecisionWait, decided)}},
			landed.WorkItemID: {WorkItemID: landed.WorkItemID, ReviewRounds: 3, CommittedRounds: 3,
				Decisions: []runstate.TriageDecision{decidedAbout(landed.RunID, runstate.TriageDecisionRepair, decided)}},
			overtakenRun.WorkItemID: {WorkItemID: overtakenRun.WorkItemID, ReviewRounds: 3, CommittedRounds: 3,
				Decisions: []runstate.TriageDecision{decidedAbout(overtakenRun.RunID, runstate.TriageDecisionRepair, decided)}},
			rerunFrom.WorkItemID: {WorkItemID: rerunFrom.WorkItemID, Reruns: 1,
				Decisions: []runstate.TriageDecision{decidedAbout(rerunFrom.RunID, runstate.TriageDecisionRerun, decided)}},
		},
		Tracker: admitted{"blocked": open},
		Now:     func() time.Time { return docketReadAt },
	})

	if len(standing.Problems) != 0 {
		t.Fatalf("problems = %v, want none", standing.Problems)
	}
	gotUndecided := runsOf(standing.Undecided)
	if want := []string{undecided.RunID, overtakenRun.RunID, undecidedToo.RunID}; strings.Join(gotUndecided, ",") != strings.Join(want, ",") {
		t.Errorf("undecided = %v, want %v (oldest first)", gotUndecided, want)
	}
	if got := runsOf(standing.Uncarried); len(got) != 1 || got[0] != uncarried.RunID {
		t.Errorf("uncarried = %v, want the repair grant nothing has taken", got)
	}
	if standing.Uncarried[0].Decision != runstate.TriageDecisionRepair || standing.Uncarried[0].DecidedBy != "development manager" {
		t.Errorf("uncarried[0] = %+v, want the decision and who recorded it", standing.Uncarried[0])
	}
	// Waited, closed, landed, re-run: four entries that need nothing from anybody.
	if standing.Settled != 4 {
		t.Errorf("settled = %d, want 4", standing.Settled)
	}
	if standing.Runless != 1 || standing.Unchecked != 0 {
		t.Errorf("runless = %d, unchecked = %d, want 1 and 0", standing.Runless, standing.Unchecked)
	}
	if !strings.Contains(standing.Undecided[0].Delivery, "put to you at 2026-09-07T01:21:00Z") {
		t.Errorf("delivery = %q, want the one-shot delivery named so the re-offer reads as one", standing.Undecided[0].Delivery)
	}
	if standing.Undecided[1].Delivery != "" {
		t.Errorf("delivery = %q on an entry the harness never delivered, want nothing", standing.Undecided[1].Delivery)
	}
}

func runsOf(waits []DocketWait) []string {
	runs := make([]string, 0, len(waits))
	for _, wait := range waits {
		runs = append(runs, wait.RunID)
	}
	return runs
}

// The classes the event-driven delivery never covers are on the docket too, and
// each stands or is settled on its own fact: a publication the forge merged with
// nothing outstanding is over, one the harness could not confirm is not; an
// escalation stands on the run's own word; a death before the claim is settled by
// the item being dispatched again.
func TestEveryDocketClassStandsOnItsOwnRecord(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	merged := runstate.State{RunID: "run-merged", WorkItemID: "yoyodyne-ifd.308", Status: runstate.StatusSucceeded,
		ReviewDecision: runstate.ReviewApprove, StartedAt: stopped, UpdatedAt: stopped,
		PullRequest: &runstate.PullRequest{Number: 1, Merged: true}}
	unconfirmed := runstate.State{RunID: "run-unconfirmed", WorkItemID: "yoyodyne-ifd.283", Status: runstate.StatusSucceeded,
		ReviewDecision: runstate.ReviewApprove, StartedAt: stopped, UpdatedAt: stopped, PublishFailure: "confirm the queued merge reached main: it did not",
		PullRequest: &runstate.PullRequest{Number: 2, Merged: true}}
	escalated := runstate.State{RunID: "run-escalated", WorkItemID: "yoyodyne-ifd.330", Status: runstate.StatusFailed,
		ReviewDecision: runstate.ReviewEscalate, StartedAt: stopped, UpdatedAt: stopped}
	diedUnstarted := runstate.State{RunID: "run-unstarted", WorkItemID: "yoyodyne-ifd.285", Status: runstate.StatusFailed,
		Failure: "claim failed", StartedAt: stopped, UpdatedAt: stopped}
	dispatchedAgain := runstate.State{RunID: "run-again", WorkItemID: "yoyodyne-ifd.285", Status: runstate.StatusRunning,
		StartedAt: stopped.Add(time.Hour), UpdatedAt: stopped.Add(time.Hour)}

	publication := func(run runstate.State, message string) triage.Entry {
		return triage.Entry{
			SchemaVersion: triage.SchemaVersion, Key: triage.PublicationKey(run.RunID, run.PullRequest.Number),
			Class: triage.ClassPublication, ProductID: "example", RunID: run.RunID, WorkItemID: run.WorkItemID, RecordedAt: stopped,
			Publication: &triage.Publication{Number: run.PullRequest.Number, Merged: run.PullRequest.Merged, Message: message, ApprovedAt: stopped},
		}
	}
	entries := []triage.Entry{
		publication(merged, ""),
		publication(unconfirmed, unconfirmed.PublishFailure),
		{SchemaVersion: triage.SchemaVersion, Key: triage.Key(triage.ClassEscalation, escalated.RunID), Class: triage.ClassEscalation,
			ProductID: "example", RunID: escalated.RunID, WorkItemID: escalated.WorkItemID, RecordedAt: stopped,
			Escalation: &triage.Escalation{RaisedBy: "reviewer", Reason: "the criteria contradict the design"}},
		{SchemaVersion: triage.SchemaVersion, Key: triage.Key(triage.ClassUnstartedRun, diedUnstarted.RunID), Class: triage.ClassUnstartedRun,
			ProductID: "example", RunID: diedUnstarted.RunID, WorkItemID: diedUnstarted.WorkItemID, RecordedAt: stopped, Failure: diedUnstarted.Failure},
	}
	standing := ReadDocket(context.Background(), DocketSources{
		Docket:    fakeDocket{entries: entries},
		Stoppages: fakeStoppages{runs: []runstate.State{merged, unconfirmed, escalated, diedUnstarted, dispatchedAgain}},
		Decisions: recordedDecisions{},
		Tracker: admitted{"open": []beads.WorkItem{
			{ID: "yoyodyne-ifd.308"}, {ID: "yoyodyne-ifd.283"}, {ID: "yoyodyne-ifd.330"}, {ID: "yoyodyne-ifd.285"},
		}},
		Now: func() time.Time { return docketReadAt },
	})

	if got, want := runsOf(standing.Undecided), []string{unconfirmed.RunID, escalated.RunID}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("undecided = %v, want %v", got, want)
	}
	if standing.Settled != 2 {
		t.Errorf("settled = %d, want the merged publication and the re-dispatched item", standing.Settled)
	}
	if !strings.Contains(standing.Undecided[1].Stopped, "reviewer judged the item cannot be met") {
		t.Errorf("stopped = %q, want the escalation in the role's words", standing.Undecided[1].Stopped)
	}
	if !strings.Contains(standing.Undecided[0].Stopped, "pull request #2: confirm the queued merge") {
		t.Errorf("stopped = %q, want the forge's account of the publication", standing.Undecided[0].Stopped)
	}
}

// A reading that cannot see the admitted work must not decide anything about
// which entries are waiting, and must not report calm either: every entry it
// could not place is counted as unread and the rendering says so first.
func TestADocketThatCannotBeCheckedAgainstTheAdmittedWorkIsUnreadRatherThanQuiet(t *testing.T) {
	t.Parallel()

	stopped := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	run := preservedRun("run-ce3a1135", "yoyodyne-ifd.192", stopped)
	standing := ReadDocket(context.Background(), DocketSources{
		Docket:    fakeDocket{entries: []triage.Entry{stoppedEntry(run.RunID, run.WorkItemID, stopped)}},
		Stoppages: fakeStoppages{runs: []runstate.State{run}},
		Decisions: recordedDecisions{},
		Tracker:   failingTracker{errors.New("bd is not answering")},
		Now:       func() time.Time { return docketReadAt },
	})
	if len(standing.Undecided) != 0 || standing.Unchecked != 1 {
		t.Fatalf("standing = %+v, want the one entry unchecked and nothing listed", standing)
	}
	if !standing.Waiting() {
		t.Fatalf("Waiting() = false over an unchecked entry, want true")
	}
	rendered := standing.Render()
	for _, want := range []string{"1 entry could not be placed", "Could not read: the admitted work could not be read", "bd is not answering"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendering does not carry %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Nothing on the docket is waiting") {
		t.Errorf("rendering reports calm over an unread docket:\n%s", rendered)
	}
}

// The rendering is what the development manager is woken with, so it has to
// say the counts first, put the undecided entries under what to do about them,
// and put the decided ones under what not to.
func TestTheDocketRenderingLeadsWithTheCountsAndSeparatesTheTwoWaits(t *testing.T) {
	t.Parallel()

	decided := time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC)
	standing := DocketStanding{
		ReadAt: docketReadAt,
		Undecided: []DocketWait{{
			Class: triage.ClassStoppedRun, RunID: "run-ce3a1135", WorkItemID: "yoyodyne-ifd.192", WorkItemTitle: "Provider failover",
			RecordedAt: decided, Stopped: "Yoyodyne stopped this item: its independent reviewer still required repair.",
			Delivery: "put to you at 2026-09-07T01:21:00Z and no decision was recorded",
		}},
		Uncarried: []DocketWait{{
			Class: triage.ClassStoppedRun, RunID: "run-f71718d7", WorkItemID: "yoyodyne-ifd.117.1",
			RecordedAt: decided, Decision: "repair", DecidedBy: "development manager", DecidedAt: decided,
		}},
		Settled: 12,
		Runless: 2,
	}
	rendered := standing.Render()
	for _, want := range []string{
		"1 stoppage has no decision standing; 1 decision is recorded and not yet carried out",
		"## Waiting on your decision",
		"- run run-ce3a1135 on yoyodyne-ifd.192 — Provider failover [stopped run, docketed 2026-09-07]: put to you at 2026-09-07T01:21:00Z and no decision was recorded. Stopped by: Yoyodyne stopped this item",
		"## Decided, and not yet carried out",
		`- run run-f71718d7 on yoyodyne-ifd.117.1 [stopped run, docketed 2026-09-07]: "repair" recorded by the development manager at 2026-09-07T09:00:00Z`,
		"2 further entries name no run",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendering does not carry %q:\n%s", want, rendered)
		}
	}
	if !strings.Contains(DocketStanding{ReadAt: docketReadAt}.Render(), "Nothing on the docket is waiting on your decision") {
		t.Errorf("an empty standing does not say so:\n%s", DocketStanding{ReadAt: docketReadAt}.Render())
	}
}
