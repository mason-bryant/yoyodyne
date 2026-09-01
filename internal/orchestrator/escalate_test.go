package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// loadableRuns is the durable run state a delivery reads: which stoppage each
// docketed run actually was.
type loadableRuns struct {
	states map[string]runstate.State
}

func (r loadableRuns) Load(runID string) (runstate.State, error) {
	state, found := r.states[runID]
	if !found {
		return runstate.State{}, fmt.Errorf("no run %s is recorded", runID)
	}
	return state, nil
}

// standingJudge is the development manager's conversation without the provider:
// it records what it was shown, and answers with whatever the test says she
// decided.
type standingJudge struct {
	shown    []triage.Entry
	judgment Judgment
	err      error
}

func (j *standingJudge) Judge(_ context.Context, entry triage.Entry) (Judgment, error) {
	j.shown = append(j.shown, entry)
	if j.err != nil {
		return Judgment{}, j.err
	}
	return j.judgment, nil
}

// pausedHolds is the operator's pause over everything the harness spends.
type pausedHolds struct {
	hold runstate.OperatorHold
	held bool
	err  error
}

func (h pausedHolds) Held() (runstate.OperatorHold, bool, error) { return h.hold, h.held, h.err }

var escalationNow = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

type escalationClock struct{}

func (escalationClock) Now() time.Time { return escalationNow }

// reviewStoppedState is the 2-of-2 stoppage: a run that ended on a durable
// blocker with its reviewer still requiring repair, and its change preserved.
func reviewStoppedState(runID, workItemID string) runstate.State {
	completed := escalationNow.Add(-time.Hour)
	return runstate.State{
		SchemaVersion:        runstate.StateSchemaVersion,
		RunID:                runID,
		ProductID:            "yoyodyne",
		RepositoryID:         "yoyodyne",
		WorkItemID:           workItemID,
		WorkItemTitle:        docketedTitle,
		Backend:              "claude-code",
		Status:               runstate.StatusFailed,
		Phase:                runstate.PhaseReviewing,
		StartedAt:            completed.Add(-time.Hour),
		UpdatedAt:            completed,
		CompletedAt:          &completed,
		WorktreePath:         "/state/worktrees/task",
		Branch:               "yoyodyne/task/abc",
		BaseCommit:           strings.Repeat("a", 40),
		TargetBranch:         "main",
		RepairAttempts:       2,
		ReviewRounds:         2,
		ReviewDecision:       runstate.ReviewRepair,
		ReviewSummary:        "the change misses the acceptance criteria",
		ReviewFindings:       1,
		ReviewFindingDetails: []runstate.Finding{{Severity: "blocker", Message: "add the missing file", File: "feature.txt", Line: 1}},
		Blocker:              "Yoyodyne stopped this item: its independent reviewer still required repair after every permitted attempt.",
	}
}

func stoppedEntry(state runstate.State) triage.Entry {
	return triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.Key(triage.ClassStoppedRun, state.RunID),
		Class:         triage.ClassStoppedRun,
		ProductID:     domain.ProductID("yoyodyne"),
		RunID:         state.RunID,
		WorkItemID:    state.WorkItemID,
		WorkItemTitle: state.WorkItemTitle,
		RecordedAt:    state.UpdatedAt,
		Blocker:       state.Blocker,
		Summary:       state.ReviewSummary,
	}
}

func escalationRecords(t *testing.T) *runstate.EscalationStore {
	t.Helper()
	store, err := runstate.NewEscalationStore(t.TempDir(), domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewEscalationStore() error = %v", err)
	}
	return store
}

func escalatorOver(t *testing.T, states []runstate.State, judge *standingJudge, holds OperatorHolds) Escalator {
	t.Helper()
	docket := &memoryDocket{}
	loadable := loadableRuns{states: map[string]runstate.State{}}
	for _, state := range states {
		loadable.states[state.RunID] = state
		if _, err := docket.RecordOnce(stoppedEntry(state)); err != nil {
			t.Fatalf("RecordOnce() error = %v", err)
		}
	}
	return Escalator{
		Docket:  docket,
		Runs:    loadable,
		Records: escalationRecords(t),
		Manager: judge,
		Holds:   holds,
		Clock:   escalationClock{},
	}
}

// The whole of what this exists for: a run that stopped on its reviewer reaches
// the development manager without anybody carrying it there, and what she
// decided is written down.
func TestATwoOfTwoStoppageReachesTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{judgment: Judgment{
		ConversationID: "chat-abc",
		Decision:       "repair",
		Reason:         "the findings are narrow and the change is preserved",
	}}
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)

	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 1 {
		t.Fatalf("escalated = %#v, want the one stoppage", sweep.Escalated)
	}
	escalated := sweep.Escalated[0]
	if !escalated.Delivered || escalated.Decision != "repair" || escalated.Problem != "" {
		t.Fatalf("escalated = %#v, want it delivered with her decision on it", escalated)
	}
	if len(judge.shown) != 1 || judge.shown[0].RunID != docketedRunID {
		t.Fatalf("shown = %#v, want the docket entry itself put in front of her", judge.shown)
	}
	if !strings.Contains(sweep.Render(), "who triaged yoyodyne-task") {
		t.Fatalf("Render() = %q, want it to say what she decided", sweep.Render())
	}

	// And she is not shown it again: the same evidence in front of her twice is
	// how one authorized recovery becomes two decisions.
	second, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("second Escalate() error = %v", err)
	}
	if len(second.Escalated) != 0 || len(judge.shown) != 1 {
		t.Fatalf("second pass delivered %#v, want a delivered stoppage left alone", second.Escalated)
	}
}

// Only the stoppage this was asked for. A failing check and a refused path are
// stoppages too, and each is a different question: they stay on the docket for
// her to read rather than being delivered by a rule nobody has argued for.
func TestOnlyTheReviewStoppageIsDelivered(t *testing.T) {
	t.Parallel()

	checkFailed := reviewStoppedState("run-1111111111111111111111111111aaaa", "yoyodyne-checks")
	checkFailed.CheckFailure = &runstate.CheckFailure{Command: "make test", ExitCode: 1, Output: "FAIL"}
	pathRefused := reviewStoppedState("run-2222222222222222222222222222bbbb", "yoyodyne-paths")
	pathRefused.PathRefusal = &runstate.PathRefusal{Paths: []string{"docs/designs/v1.md"}}
	stillRunning := reviewStoppedState("run-3333333333333333333333333333cccc", "yoyodyne-running")
	stillRunning.Status = runstate.StatusRunning
	stillRunning.CompletedAt = nil

	judge := &standingJudge{}
	escalator := escalatorOver(t, []runstate.State{checkFailed, pathRefused, stillRunning}, judge, nil)

	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 0 || len(judge.shown) != 0 {
		t.Fatalf("delivered %#v, want nothing but the review stoppage put to her", sweep.Escalated)
	}
}

// A pass delivers one stoppage. A pass that put a morning's backlog in front of
// her at once would hold the queue closed for as long as she took to read all of
// it.
func TestOneStoppageIsDeliveredPerPass(t *testing.T) {
	t.Parallel()

	first := reviewStoppedState(docketedRunID, docketedItem)
	second := reviewStoppedState("run-4444444444444444444444444444dddd", "yoyodyne-second")
	judge := &standingJudge{judgment: Judgment{ConversationID: "chat-abc"}}
	escalator := escalatorOver(t, []runstate.State{first, second}, judge, nil)

	for pass := 1; pass <= 2; pass++ {
		sweep, err := escalator.Escalate(context.Background())
		if err != nil {
			t.Fatalf("pass %d: Escalate() error = %v", pass, err)
		}
		if len(sweep.Escalated) != 1 {
			t.Fatalf("pass %d delivered %#v, want exactly one stoppage", pass, sweep.Escalated)
		}
	}
	if len(judge.shown) != 2 || judge.shown[0].RunID != first.RunID || judge.shown[1].RunID != second.RunID {
		t.Fatalf("shown = %#v, want the oldest stoppage first and the next one after it", judge.shown)
	}
}

// A delivery is a provider invocation, so the operator's pause covers it. What
// matters is that nothing is claimed: the stoppage keeps its delivery for after
// the pause is lifted.
func TestAPausedHarnessDeliversNothing(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{judgment: Judgment{ConversationID: "chat-abc", Decision: "repair"}}
	holds := &pausedHolds{hold: runstate.OperatorHold{HeldAt: escalationNow}, held: true}
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, holds)

	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if sweep.Paused == nil || len(judge.shown) != 0 {
		t.Fatalf("sweep = %#v, want the pause reported and nothing said to her", sweep)
	}

	holds.held = false
	resumed, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() after the pause error = %v", err)
	}
	if len(resumed.Escalated) != 1 || !resumed.Escalated[0].Delivered {
		t.Fatalf("sweep after the pause = %#v, want the stoppage delivered", resumed.Escalated)
	}
}

// A pause that cannot be read refuses the pass rather than being spent through,
// exactly as it does everywhere else it is read.
func TestAnUnreadablePauseDeliversNothing(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{}
	holds := &pausedHolds{err: errors.New("the hold is unreadable")}
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, holds)

	if _, err := escalator.Escalate(context.Background()); err == nil {
		t.Fatalf("Escalate() error = nil, want the unreadable pause refused")
	}
	if len(judge.shown) != 0 {
		t.Fatalf("shown = %#v, want nothing said to her", judge.shown)
	}
}

// A conversation that could not be opened asks her nothing, so the attempt is
// given back and the next pass makes it.
func TestAnUnreachableConversationKeepsTheDelivery(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{err: fmt.Errorf("%w: the conversation is already held", ErrConversationUnreachable)}
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)

	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 1 || sweep.Escalated[0].Delivered {
		t.Fatalf("escalated = %#v, want a delivery that did not happen", sweep.Escalated)
	}
	if !strings.Contains(sweep.Escalated[0].Problem, "will be at the next pass") {
		t.Fatalf("problem = %q, want it to say the stoppage keeps its delivery", sweep.Escalated[0].Problem)
	}

	judge.err = nil
	judge.judgment = Judgment{ConversationID: "chat-abc", Decision: "rerun"}
	next, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("next Escalate() error = %v", err)
	}
	if len(next.Escalated) != 1 || !next.Escalated[0].Delivered {
		t.Fatalf("next pass = %#v, want the stoppage delivered once the conversation opened", next.Escalated)
	}
}

// A turn that may have been taken is one nothing can claim was not, so the
// attempt stands and the delivery does not. It is tried again, and the trying is
// bounded: past the bound the stoppage needs a person, and the pass says so
// rather than spending every poll on it.
func TestADeliveryThatKeepsFailingStopsBeingTried(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{err: errors.New("the provider refused the turn")}
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)

	for attempt := 1; attempt <= runstate.MaxEscalationAttempts; attempt++ {
		sweep, err := escalator.Escalate(context.Background())
		if err != nil {
			t.Fatalf("attempt %d: Escalate() error = %v", attempt, err)
		}
		if len(sweep.Escalated) != 1 || sweep.Escalated[0].Delivered {
			t.Fatalf("attempt %d = %#v, want an attempt that did not deliver", attempt, sweep.Escalated)
		}
		if !strings.Contains(sweep.Escalated[0].Problem, fmt.Sprintf("attempt %d of %d", attempt, runstate.MaxEscalationAttempts)) {
			t.Fatalf("attempt %d problem = %q, want it to say which attempt this was", attempt, sweep.Escalated[0].Problem)
		}
	}
	spent, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() past the bound error = %v", err)
	}
	if len(spent.Escalated) != 0 {
		t.Fatalf("past the bound = %#v, want the harness to have stopped trying", spent.Escalated)
	}
	if len(judge.shown) != runstate.MaxEscalationAttempts {
		t.Fatalf("turns taken = %d, want the %d the bound permits", len(judge.shown), runstate.MaxEscalationAttempts)
	}
}

// A stoppage she read and decided nothing about has still reached her, and is
// reported as the answer it is rather than as a delivery that failed.
func TestADeliverySheDecidedNothingAboutIsStillADelivery(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{judgment: Judgment{ConversationID: "chat-abc"}}
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)

	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 1 || !sweep.Escalated[0].Delivered || sweep.Escalated[0].Decision != "" {
		t.Fatalf("escalated = %#v, want a delivery she decided nothing about", sweep.Escalated)
	}
	if !strings.Contains(sweep.Render(), "recorded no decision") {
		t.Fatalf("Render() = %q, want it to say she decided nothing", sweep.Render())
	}
}

// An escalator missing any of what it needs refuses rather than half-delivering.
func TestEscalatingRequiresWhatItDeliversWith(t *testing.T) {
	t.Parallel()

	_, err := Escalator{}.Escalate(context.Background())
	if err == nil {
		t.Fatalf("Escalate() error = nil, want an escalator with nothing wired refused")
	}
	for _, wanted := range []string{"triage docket", "durable run state", "one delivery per docketed stoppage", "development manager's conversation"} {
		if !strings.Contains(err.Error(), wanted) {
			t.Fatalf("refusal = %q, want it to name %q", err, wanted)
		}
	}
}

// claimedReruns is what the harness has already carried out against a stoppage.
type claimedReruns struct {
	claimed map[string]runstate.Rerun
	err     error
}

func (r claimedReruns) Find(docketKey string) (runstate.Rerun, bool, error) {
	if r.err != nil {
		return runstate.Rerun{}, false, r.err
	}
	claimed, found := r.claimed[docketKey]
	return claimed, found, nil
}

// A stoppage somebody already had run again is not one nobody has looked at: the
// decision was made and carried out, and the one re-run it gets is spent.
func TestAStoppageAlreadyRunAgainIsNotDelivered(t *testing.T) {
	t.Parallel()

	stopped := reviewStoppedState(docketedRunID, docketedItem)
	judge := &standingJudge{judgment: Judgment{ConversationID: "chat-abc"}}
	escalator := escalatorOver(t, []runstate.State{stopped}, judge, nil)
	escalator.Reruns = claimedReruns{claimed: map[string]runstate.Rerun{
		triage.Key(triage.ClassStoppedRun, stopped.RunID): {DocketKey: triage.Key(triage.ClassStoppedRun, stopped.RunID)},
	}}

	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 0 || len(judge.shown) != 0 {
		t.Fatalf("delivered %#v, want a stoppage already acted on left alone", sweep.Escalated)
	}
}

// A run whose blocker the repair carry-out cleared is no longer a stoppage, so
// nothing delivers it — the same rule that decides every other run, rather than
// a second one written here.
func TestARepairedStoppageIsNotDelivered(t *testing.T) {
	t.Parallel()

	repaired := reviewStoppedState(docketedRunID, docketedItem)
	entry := stoppedEntry(repaired)
	repaired.Blocker = ""
	docket := &memoryDocket{}
	if _, err := docket.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	judge := &standingJudge{}
	escalator := Escalator{
		Docket:  docket,
		Runs:    loadableRuns{states: map[string]runstate.State{repaired.RunID: repaired}},
		Records: escalationRecords(t),
		Manager: judge,
		Clock:   escalationClock{},
	}

	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 0 || len(judge.shown) != 0 {
		t.Fatalf("delivered %#v, want the repaired run left alone", sweep.Escalated)
	}
}
