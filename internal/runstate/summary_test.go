package runstate

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The question this answers is "what has been failing?", so the runs that ended
// without succeeding are selectable on their own, newest first, priced from the
// same evidence the ledger uses.
func TestHistoryReportsFailedRunsNewestFirstWithTheirReasons(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	succeeded := testState(t, StatusSucceeded)
	succeeded.WorkItemID = "yoyodyne-ifd.41"
	succeeded.Phase = PhaseComplete
	succeeded.ProviderSessionID = "session-developer"
	if err := store.Create(succeeded); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendLegacyCostEvents(t, store, succeeded.RunID, 1, execution.EventRunCompleted, 19.0)

	failed := testState(t, StatusFailed)
	failed.WorkItemID = "yoyodyne-ifd.2.7"
	failed.Phase = PhaseChecking
	failed.StartedAt = succeeded.StartedAt.Add(time.Hour)
	failed.UpdatedAt = failed.StartedAt
	completedAt := failed.StartedAt
	failed.CompletedAt = &completedAt
	failed.Failure = "verification failed: make test exited with 2"
	failed.CheckFailure = &CheckFailure{Command: "make test", ExitCode: 2, Output: "FAIL"}
	failed.ProviderSessionID = "session-developer-2"
	if err := store.Create(failed); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendLegacyCostEvents(t, store, failed.RunID, 1, execution.EventRunFailed, 8.91)

	cancelled := testState(t, StatusCancelled)
	cancelled.WorkItemID = "yoyodyne-ifd.2.7"
	cancelled.Phase = PhaseDeveloping
	cancelled.StartedAt = succeeded.StartedAt.Add(-time.Hour)
	cancelled.UpdatedAt = cancelled.StartedAt
	cancelledAt := cancelled.StartedAt
	cancelled.CompletedAt = &cancelledAt
	cancelled.Failure = "context canceled"
	if err := store.Create(cancelled); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	history, err := store.History(RunQuery{FailedOnly: true})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if history.Recorded != 3 || history.Matched != 2 {
		t.Fatalf("history = %#v, want 2 of 3 recorded runs selected", history)
	}
	// A cancelled run did not land its work either, and it records why, so it
	// belongs beside the failed one rather than among the successes.
	if history.Runs[0].RunID != failed.RunID || history.Runs[1].RunID != cancelled.RunID {
		t.Fatalf("history order = %q, %q; want newest first", history.Runs[0].RunID, history.Runs[1].RunID)
	}
	reported := history.Runs[0]
	if !reported.Failed() || reported.Failure != failed.Failure || reported.Phase != PhaseChecking {
		t.Fatalf("failed run = %#v", reported)
	}
	if reported.FailingCheck == nil || reported.FailingCheck.Command != "make test" || reported.FailingCheck.ExitCode != 2 {
		t.Fatalf("failing check = %#v", reported.FailingCheck)
	}
	// The failed attempt spent real money and is priced from its own log, exactly
	// as the ledger prices it.
	if !reported.CostKnown() || reported.CostUSD != 8.91 || reported.Invocations != 1 {
		t.Fatalf("failed run price = %#v", reported)
	}
}

// The bookkeeping failures are recorded apart from the run's own so that neither
// can be read as the other, and a listing that ran them together would undo
// that. An integrated run carrying an outstanding cleanup is the case: the work
// landed, and only the janitorial step is unfinished.
func TestHistoryKeepsBookkeepingFailuresApartFromTheRunsOwn(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := integratedState(t, PhaseCleaningUp)
	state.CleanupFailure = "remove worktree: directory is busy"
	state.PublishFailure = "push branch: remote rejected"
	state.CompletionRecordingFailure = "save completed run state after cleanup: disk full"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	history, err := store.History(RunQuery{})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 1 {
		t.Fatalf("history = %#v", history)
	}
	reported := history.Runs[0]
	if reported.Failed() || reported.Failure != "" {
		t.Fatalf("an integrated run with unfinished cleanup reads as failed: %#v", reported)
	}
	if reported.CleanupFailure != state.CleanupFailure || reported.PublishFailure != state.PublishFailure ||
		reported.CompletionRecordingFailure != state.CompletionRecordingFailure {
		t.Fatalf("bookkeeping failures = %#v", reported)
	}
	// Cleanup that did not finish is exactly what still owes somebody a step.
	if !reported.Integrated || !reported.Outstanding {
		t.Fatalf("reported = %#v, want an integrated run that is still outstanding", reported)
	}
	// A merge the forge has not performed is the other thing a finished run can
	// owe, and it is carried so a reader can tell the two apart rather than only
	// being told that something is owed.
	queued := integratedState(t, PhaseComplete)
	queued.RunID = mustRunID(t)
	queued.WorktreeRemoved = true
	queued.BranchRemoved = true
	queued.PullRequest = &PullRequest{
		Remote:      "origin",
		Branch:      queued.Branch,
		Number:      73,
		URL:         "https://forge.example/pull/73",
		HeadCommit:  queued.Integration.SourceCommit,
		MergeQueued: true,
	}
	if err := store.Create(queued); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	waiting, err := store.History(RunQuery{WorkItemID: queued.WorkItemID})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(waiting.Runs) != 1 {
		t.Fatalf("history = %#v", waiting)
	}
	if !waiting.Runs[0].MergeQueued || !waiting.Runs[0].Outstanding {
		t.Fatalf("queued run = %#v, want an outstanding run waiting on its queued merge", waiting.Runs[0])
	}

	// Selecting the runs that went wrong must not sweep either of them up:
	// nothing about their work failed.
	failed, err := store.History(RunQuery{FailedOnly: true})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if failed.Matched != 0 || len(failed.Runs) != 0 {
		t.Fatalf("failed history = %#v", failed)
	}
}

// A limited listing has to say what it was limited from, or it reads as the
// whole record. One item's runs are selectable the same way.
func TestHistoryLimitsWhatItReportsWithoutHidingWhatItSelected(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	started := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	for index := 0; index < 4; index++ {
		state := testState(t, StatusFailed)
		state.WorkItemID = "yoyodyne-ifd.2.7"
		if index == 3 {
			state.WorkItemID = "yoyodyne-ifd.41"
		}
		state.StartedAt = started.Add(time.Duration(index) * time.Hour)
		state.UpdatedAt = state.StartedAt
		completedAt := state.StartedAt
		state.CompletedAt = &completedAt
		state.Failure = "developer reported failure: api_error"
		if err := store.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	history, err := store.History(RunQuery{Limit: 2})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 2 || history.Matched != 4 || history.Recorded != 4 {
		t.Fatalf("history = %#v, want 2 of 4 shown", history)
	}
	if !history.Runs[0].StartedAt.After(history.Runs[1].StartedAt) {
		t.Fatalf("history order = %v, %v; want newest first", history.Runs[0].StartedAt, history.Runs[1].StartedAt)
	}

	item, err := store.History(RunQuery{WorkItemID: "yoyodyne-ifd.41"})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if item.Matched != 1 || len(item.Runs) != 1 || item.Runs[0].WorkItemID != "yoyodyne-ifd.41" {
		t.Fatalf("item history = %#v", item)
	}
	// Reading one item's runs must still say how much record it was read out of.
	if item.Recorded != 4 {
		t.Fatalf("item history recorded = %d, want 4", item.Recorded)
	}
}

// "failed" was one word for four different endings, three of which leave the
// change intact and the item back in somebody's hands. The vocabulary has to
// tell them apart from what the records already hold, and it has to carry the
// artifacts that say the work is still there: an operator who reads a run's line
// and asks whether the run was discarded has been told the wrong thing by a
// listing that answered a question nobody asked.
func TestHistoryTellsAStoppedRunFromACancelledOneAndNamesWhatSurvives(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	// A review nobody repaired: the item carries the blocker, and the branch,
	// the worktree, the developer session, and the findings are all still there.
	blocked := stoppedState(t, PhaseReviewing)
	blocked.Blocker = "Yoyodyne blocked this item: independent review requires repair"
	blocked.ReviewDecision = ReviewRepair
	blocked.ReviewFindings = 2
	blocked.ProviderSessionID = "session-developer"
	if err := store.Create(blocked); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// A replay the target branch outran, and a provider that would not carry the
	// run. Both stopped on a blocker, and each says where it stopped.
	replay := stoppedState(t, PhaseIntegrating)
	replay.Blocker = "Yoyodyne blocked this item: the change cannot be replayed onto main"
	if err := store.Create(replay); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	provider := stoppedState(t, PhaseDeveloping)
	provider.Blocker = "Yoyodyne blocked this item: the provider ended this run without judging the work"
	if err := store.Create(provider); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// An operator stopping a run is not a verdict on the change, and it leaves
	// nobody a blocker to act on.
	cancelled := stoppedState(t, PhaseDeveloping)
	cancelled.Status = StatusCancelled
	cancelled.Failure = "the operator stopped this run"
	if err := store.Create(cancelled); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// And a run that broke before it made anything: nothing was preserved because
	// nothing was created, which is a different fact from work thrown away.
	broke := testState(t, StatusFailed)
	broke.WorkItemID = stoppedItem
	broke.Failure = "create isolated worktree: primary checkout is not ready"
	if err := store.Create(broke); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	history, err := store.History(RunQuery{WorkItemID: stoppedItem})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	byRun := make(map[string]RunSummary, len(history.Runs))
	for _, run := range history.Runs {
		byRun[run.RunID] = run
	}
	if len(byRun) != 5 {
		t.Fatalf("history = %#v, want all five runs of the item", history.Runs)
	}
	for _, want := range []struct {
		runID     string
		outcome   RunOutcome
		preserved bool
	}{
		{runID: blocked.RunID, outcome: OutcomeStopped, preserved: true},
		{runID: replay.RunID, outcome: OutcomeStopped, preserved: true},
		{runID: provider.RunID, outcome: OutcomeStopped, preserved: true},
		{runID: cancelled.RunID, outcome: OutcomeCancelled, preserved: true},
		{runID: broke.RunID, outcome: OutcomeFailed, preserved: false},
	} {
		run := byRun[want.runID]
		if run.Outcome != want.outcome {
			t.Errorf("run %s outcome = %q, want %q", want.runID, run.Outcome, want.outcome)
		}
		if run.Preserved() != want.preserved {
			t.Errorf("run %s preserved = %v, want %v", want.runID, run.Preserved(), want.preserved)
		}
		// Every one of them ended without succeeding, which is what --failed
		// selects; the vocabulary above is what separates them within that.
		if !run.Failed() {
			t.Errorf("run %s did not read as a run that ended without succeeding", want.runID)
		}
	}
	// The preserved change has to be reachable from the listing, or the operator
	// is sent to the run's JSON for the path — which is the errand this verb
	// exists to remove.
	stopped := byRun[blocked.RunID]
	if stopped.Branch != blocked.Branch || stopped.WorktreePath != blocked.WorktreePath {
		t.Errorf("stopped run = %#v, want the branch and worktree it preserved", stopped)
	}
	if stopped.ProviderSessionID != "session-developer" || stopped.ReviewFindings != 2 {
		t.Errorf("stopped run = %#v, want the preserved session and the findings against it", stopped)
	}
	// Where it stopped is the other half of telling three stoppages apart, and it
	// is the record's own phase rather than anything read out of the reason.
	if byRun[replay.RunID].Phase != PhaseIntegrating || byRun[provider.RunID].Phase != PhaseDeveloping {
		t.Errorf("phases = %q, %q; want each stoppage to say where it stopped",
			byRun[replay.RunID].Phase, byRun[provider.RunID].Phase)
	}
	// A run that landed its work is not described in the stopped-work vocabulary
	// at all, and its cleaned-up artifacts are not a loss to report.
	succeeded := integratedState(t, PhaseComplete)
	succeeded.WorktreeRemoved = true
	succeeded.BranchRemoved = true
	if err := store.Create(succeeded); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	landed, err := store.History(RunQuery{WorkItemID: succeeded.WorkItemID})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(landed.Runs) != 1 || landed.Runs[0].Outcome != OutcomeSucceeded {
		t.Fatalf("landed run = %#v, want the succeeded outcome", landed.Runs)
	}
}

// The vocabulary is closed, so what it maps every recorded status to is stated
// once here rather than left to whichever cases a rendering test happened to
// build. The two that are easy to get wrong are the ones spelled out: a blocker
// outranks the status, because a run stopped at a deadline having handed
// somebody a decision is a stoppage rather than a clock; and a run with no
// blocker keeps the word for how it ended, which is what stops "timed out" and
// "cancelled" collapsing into the failure they are not.
func TestOutcomeMapsEveryRecordedStatusOntoTheFixedVocabulary(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		status  Status
		blocker string
		want    RunOutcome
	}{
		{name: "pending", status: StatusPending, want: RunOutcome(StatusPending)},
		{name: "running", status: StatusRunning, want: RunOutcome(StatusRunning)},
		{name: "succeeded", status: StatusSucceeded, want: OutcomeSucceeded},
		{name: "failed with nothing handed to anybody", status: StatusFailed, want: OutcomeFailed},
		{name: "cancelled", status: StatusCancelled, want: OutcomeCancelled},
		{name: "timed out", status: StatusTimedOut, want: OutcomeTimedOut},
		{name: "failed on a blocker", status: StatusFailed, blocker: "the item carries this", want: OutcomeStopped},
		// The harness stops a provider on time and then blocks the item when its
		// relaunch budget is spent, so this pairing is one the records really
		// hold: what became of the work is the stoppage, not the clock.
		{name: "timed out on a blocker", status: StatusTimedOut, blocker: "the item carries this", want: OutcomeStopped},
		// Whitespace is not a blocker anybody was handed.
		{name: "failed on a blank blocker", status: StatusFailed, blocker: "  \n ", want: OutcomeFailed},
		// A run still in flight is never described as stopped, whatever a blocker
		// left over from an attempt triage re-entered says.
		{name: "running with a superseded blocker", status: StatusRunning, blocker: "cleared as it went again", want: RunOutcome(StatusRunning)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			state := State{Status: test.status, Blocker: test.blocker}
			if outcome := state.Outcome(); outcome != test.want {
				t.Fatalf("Outcome() = %q, want %q", outcome, test.want)
			}
		})
	}
}

// The item every run in the test above was made for, named once so the listing
// can be read back for it.
const stoppedItem = "yoyodyne-ifd.173"

// stoppedState is a terminal run with a worktree and a branch it did not remove,
// which is what every stoppage the harness hands to a person leaves behind.
func stoppedState(t *testing.T, phase Phase) State {
	t.Helper()
	state := testState(t, StatusFailed)
	state.WorkItemID = stoppedItem
	state.Phase = phase
	state.WorktreePath = "/state/worktrees/" + string(phase)
	state.Branch = "yoyodyne/yoyodyne-ifd.173/" + string(phase)
	state.BaseCommit = strings.Repeat("a", 40)
	state.TargetBranch = "main"
	state.Failure = "the run stopped in the " + string(phase) + " phase"
	return state
}

// A run whose event log is gone is reported as unpriceable rather than as free,
// for the reason the ledger does it: a zero meaning "no record" corrupts every
// figure it is read into.
func TestHistoryReportsARunWithNoSurvivingLogAsUnpriced(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	lost := testState(t, StatusFailed)
	lost.Failure = "developer backend failed"
	lost.ProviderSessionID = "session-developer"
	if err := store.Create(lost); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	history, err := store.History(RunQuery{})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 1 {
		t.Fatalf("history = %#v", history)
	}
	if history.Runs[0].CostKnown() || history.Runs[0].CostUSD != 0 {
		t.Fatalf("run = %#v, want an unpriced run", history.Runs[0])
	}
}

// What remains of a run's change is said in three phrases and never in two, and
// the third is the one the vocabulary exists for: a record naming no artifact is
// an absence stated as an absence rather than a claim the run made nothing.
// Every surface reads this one derivation, so `yoyo status` and the channel
// cannot come to say different words about one run.
func TestWhatRemainsOfARunIsStatedRatherThanInferredFromTwoEmptyFields(t *testing.T) {
	for _, want := range []struct {
		artifacts Artifacts
		preserved bool
		describes string
	}{
		{Artifacts{Branch: "yoyodyne/ifd-226", WorktreePath: "/tmp/run"}, true, "work preserved"},
		// Either one standing on its own still holds the work: a removed worktree
		// whose branch stands is a change somebody can check out.
		{Artifacts{Branch: "yoyodyne/ifd-226", WorktreePath: "/tmp/run", WorktreeRemoved: true}, true, "work preserved"},
		{Artifacts{Branch: "yoyodyne/ifd-226", BranchRemoved: true, WorktreePath: "/tmp/run"}, true, "work preserved"},
		{Artifacts{Branch: "yoyodyne/ifd-226", BranchRemoved: true, WorktreePath: "/tmp/run", WorktreeRemoved: true}, false, "work removed"},
		{Artifacts{}, false, "no artifacts recorded"},
	} {
		if want.artifacts.Preserved() != want.preserved {
			t.Errorf("%#v preserved = %v, want %v", want.artifacts, want.artifacts.Preserved(), want.preserved)
		}
		if described := want.artifacts.Describe(); described != want.describes {
			t.Errorf("%#v describes itself as %q, want %q", want.artifacts, described, want.describes)
		}
	}
	// A run and the summary of it answer identically, because a surface holding
	// one and a surface holding the other must not be able to disagree.
	state := testState(t, StatusFailed)
	state.Branch, state.WorktreePath = "yoyodyne/ifd-226", "/tmp/run"
	summary := RunSummary{Branch: state.Branch, WorktreePath: state.WorktreePath}
	if state.Artifacts() != summary.Artifacts() || !summary.Preserved() {
		t.Errorf("state says %#v and its summary says %#v", state.Artifacts(), summary.Artifacts())
	}
}
