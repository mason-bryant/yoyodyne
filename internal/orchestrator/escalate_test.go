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
	// killTurn is the death that arrives while the turn is in flight: a shutdown
	// cancelling the context the delivery is running under, before the turn it is
	// running comes back. Nil where the death a test is about is a teardown of the
	// child alone, which leaves that context untouched.
	killTurn func()
}

func (j *standingJudge) Judge(_ context.Context, entry triage.Entry) (Judgment, error) {
	j.shown = append(j.shown, entry)
	if j.killTurn != nil {
		j.killTurn()
	}
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

// movingClock is for the tests about pacing, which are the ones that have to be
// able to say what happens a minute later and what happens a quarter of an hour
// later without spending either.
type movingClock struct{ now time.Time }

func (c *movingClock) Now() time.Time { return c.now }

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

// judgedItems is the item's durable triage record: what the development manager
// has already decided about it.
type judgedItems struct {
	counters map[string]runstate.TriageCounters
	err      error
}

func (d judgedItems) Counters(workItemID string) (runstate.TriageCounters, error) {
	if d.err != nil {
		return runstate.TriageCounters{}, d.err
	}
	return d.counters[workItemID], nil
}

// claimedReruns is what the harness has carried out of those decisions.
type claimedReruns struct {
	claimed map[string][]runstate.Rerun
	err     error
}

func (r claimedReruns) Claimed(workItemID string) ([]runstate.Rerun, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.claimed[workItemID], nil
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
		// Nothing decided and nothing carried out, which is what a stoppage nobody
		// has looked at reads as. The tests about what she has already decided
		// replace these.
		Decisions: judgedItems{},
		Reruns:    claimedReruns{},
		Manager:   judge,
		Holds:     holds,
		Clock:     escalationClock{},
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

	clock := &movingClock{now: escalationNow}
	escalator.Clock = clock

	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 1 || sweep.Escalated[0].Delivered {
		t.Fatalf("escalated = %#v, want a delivery that did not happen", sweep.Escalated)
	}
	if !strings.Contains(sweep.Escalated[0].Problem, "will be once") {
		t.Fatalf("problem = %q, want it to say the stoppage keeps its delivery", sweep.Escalated[0].Problem)
	}

	judge.err = nil
	judge.judgment = Judgment{ConversationID: "chat-abc", Decision: "rerun"}
	// The attempt was given back and the pacing was not: a conversation somebody
	// else is holding and a provider with no capacity both last longer than a
	// pull, so asking again at once would meet the same refusal several times a
	// minute.
	waiting, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() before the delay error = %v", err)
	}
	if len(waiting.Escalated) != 0 || len(judge.shown) != 1 {
		t.Fatalf("before the delay = %#v, want the stoppage waiting rather than asked again", waiting.Escalated)
	}

	clock.now = escalationNow.Add(runstate.EscalationRetryDelay)
	next, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("next Escalate() error = %v", err)
	}
	if len(next.Escalated) != 1 || !next.Escalated[0].Delivered {
		t.Fatalf("next pass = %#v, want the stoppage delivered once the conversation opened", next.Escalated)
	}
	// And the attempt it gave back is not one of the three: the bound counts what
	// was asked of her, and nothing was.
	recorded, found, err := escalator.Records.Find(next.Escalated[0].DocketKey)
	if err != nil || !found {
		t.Fatalf("Find() = found %v, error %v", found, err)
	}
	if recorded.Attempts != 1 {
		t.Fatalf("attempts = %d, want the turn nobody took left uncounted", recorded.Attempts)
	}
}

// A delivery a cancellation killed before her answer existed is the harness's
// own death rather than anything about her, so it gives its attempt back too.
// This is the whole of yoyodyne-ifd.250: every teardown of the process the pull
// ran in spent an attempt, and enough of them turned a stoppage nobody had ever
// been shown into a record saying it needs a person.
func TestADeliveryACancellationKilledKeepsTheDelivery(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{err: fmt.Errorf("%w: development manager reported failure: cancelled", ErrDeliveryCancelled)}
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)
	clock := &movingClock{now: escalationNow}
	escalator.Clock = clock

	// More teardowns than the bound permits attempts. Not one of them may spend
	// one, so no number of them can abandon a stoppage.
	for teardown := 0; teardown <= runstate.MaxEscalationAttempts; teardown++ {
		sweep, err := escalator.Escalate(context.Background())
		if err != nil {
			t.Fatalf("teardown %d: Escalate() error = %v", teardown, err)
		}
		if len(sweep.Escalated) != 1 || sweep.Escalated[0].Delivered {
			t.Fatalf("teardown %d = %#v, want a delivery that did not happen", teardown, sweep.Escalated)
		}
		if strings.Contains(sweep.Escalated[0].Problem, "needs a person") {
			t.Fatalf("teardown %d problem = %q, want a killed turn to leave the stoppage deliverable", teardown, sweep.Escalated[0].Problem)
		}
		// And what a person reads says what actually happened. "Not put to the
		// development manager" is the claim this failure explicitly refuses to
		// make: the provider had the prompt from the moment the invocation
		// started, and what ended was the turn rather than the asking.
		if !strings.Contains(sweep.Escalated[0].Problem, "ended before she answered") {
			t.Fatalf("teardown %d problem = %q, want it to say the turn ended before she answered", teardown, sweep.Escalated[0].Problem)
		}
		if strings.Contains(sweep.Escalated[0].Problem, "was not put to the development manager") {
			t.Fatalf("teardown %d problem = %q, want it not to claim she was never asked", teardown, sweep.Escalated[0].Problem)
		}
		recorded, found, err := escalator.Records.Find(sweep.Escalated[0].DocketKey)
		if err != nil || !found {
			t.Fatalf("teardown %d: Find() = found %v, error %v", teardown, found, err)
		}
		if recorded.Attempts != 0 {
			t.Fatalf("teardown %d attempts = %d, want the harness's own death left uncounted", teardown, recorded.Attempts)
		}
		clock.now = clock.now.Add(runstate.EscalationRetryDelay)
	}

	// And the stoppage is still hers to judge once something answers.
	judge.err = nil
	judge.judgment = Judgment{ConversationID: "chat-abc", Decision: "rerun"}
	next, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() after the teardowns error = %v", err)
	}
	if len(next.Escalated) != 1 || !next.Escalated[0].Delivered {
		t.Fatalf("after the teardowns = %#v, want the stoppage still delivered", next.Escalated)
	}
}

// cancelSensitiveRecords is the escalation record with the property that makes a
// shutdown different from a teardown: a write refused under a context that is
// already cancelled.
//
// The store really is this. Its read-modify-write is serialized by a lock whose
// wait returns the context's error, so either of the two writes a delivery makes
// after its turn — the give-back of an attempt, the settling of one that
// answered — is refused when handed a cancelled context and another process
// holds the record, and only then, because an uncontended lock is taken without
// the context being consulted at all. A test that waited for the real refusal
// would be waiting on that race; this makes the refusal certain instead, so what
// the tests prove is that neither write is ever handed a cancelled context
// rather than that they got lucky.
type cancelSensitiveRecords struct {
	*runstate.EscalationStore
}

func (r cancelSensitiveRecords) Withdraw(ctx context.Context, docketKey, problem string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("lock the triage escalation of %s: %w", docketKey, err)
	}
	return r.EscalationStore.Withdraw(ctx, docketKey, problem)
}

func (r cancelSensitiveRecords) Settle(ctx context.Context, docketKey string, delivery runstate.Delivery) (runstate.Escalation, error) {
	if err := ctx.Err(); err != nil {
		return runstate.Escalation{}, fmt.Errorf("lock the triage escalation of %s: %w", docketKey, err)
	}
	return r.EscalationStore.Settle(ctx, docketKey, delivery)
}

// The same death as the teardown above, arriving the way a shutdown arrives.
// yoyodyne-ifd.250's give-back was proven against a process-group teardown,
// where the child is killed and the pull lives on with its context intact; a
// shutdown cancels the pull's own context, so the give-back it asks for runs
// under a context that is already cancelled. It has to land anyway, or the
// largest class of harness death spends the attempt after all.
func TestADeliveryAShutdownCancelledKeepsTheDelivery(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{err: fmt.Errorf("%w: development manager reported failure: cancelled", ErrDeliveryCancelled)}
	records := escalationRecords(t)
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)
	escalator.Records = cancelSensitiveRecords{EscalationStore: records}
	clock := &movingClock{now: escalationNow}
	escalator.Clock = clock

	// More shutdowns than the bound permits attempts, for the reason the teardowns
	// above are counted: no number of the harness's own deaths may abandon a
	// stoppage nobody has been shown.
	for shutdown := 0; shutdown <= runstate.MaxEscalationAttempts; shutdown++ {
		ctx, cancel := context.WithCancel(context.Background())
		// The shutdown lands mid-turn: the delivery claimed its attempt under a live
		// context, and the context is gone by the time the turn comes back.
		judge.killTurn = cancel

		sweep, err := escalator.Escalate(ctx)
		cancel()
		if err != nil {
			t.Fatalf("shutdown %d: Escalate() error = %v", shutdown, err)
		}
		if len(sweep.Escalated) != 1 || sweep.Escalated[0].Delivered {
			t.Fatalf("shutdown %d = %#v, want a delivery that did not happen", shutdown, sweep.Escalated)
		}
		if strings.Contains(sweep.Escalated[0].Problem, "could not be given back") {
			t.Fatalf("shutdown %d problem = %q, want the give-back to outlive the cancellation that made it necessary", shutdown, sweep.Escalated[0].Problem)
		}
		if strings.Contains(sweep.Escalated[0].Problem, "needs a person") {
			t.Fatalf("shutdown %d problem = %q, want a killed turn to leave the stoppage deliverable", shutdown, sweep.Escalated[0].Problem)
		}
		recorded, found, err := records.Find(sweep.Escalated[0].DocketKey)
		if err != nil || !found {
			t.Fatalf("shutdown %d: Find() = found %v, error %v", shutdown, found, err)
		}
		if recorded.Attempts != 0 {
			t.Fatalf("shutdown %d attempts = %d, want the harness's own shutdown left uncounted", shutdown, recorded.Attempts)
		}
		clock.now = clock.now.Add(runstate.EscalationRetryDelay)
	}

	// And the stoppage is still hers to judge once the harness is up again.
	judge.err = nil
	judge.killTurn = nil
	judge.judgment = Judgment{ConversationID: "chat-abc", Decision: "rerun"}
	next, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() after the shutdowns error = %v", err)
	}
	if len(next.Escalated) != 1 || !next.Escalated[0].Delivered {
		t.Fatalf("after the shutdowns = %#v, want the stoppage still delivered", next.Escalated)
	}
}

// The other half of the same shutdown: it lands after her answer arrived rather
// than instead of it, so what the cancellation threatens is not the attempt but
// the record of the delivery. A settle refused there leaves the attempt standing
// with no delivery against it, which reads as a stoppage nobody has been shown —
// and a later pass puts one she has already answered to her a second time, the
// harm the at-most-once record exists to prevent. Nothing else catches it: a
// decision that spends no counter is one alreadyJudged cannot see.
func TestADeliveryAShutdownCancelledAfterHerAnswerIsNotDeliveredAgain(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{judgment: Judgment{
		ConversationID: "chat-abc",
		Decision:       "rerun",
		Reason:         "the change is preserved and the findings are stale",
	}}
	records := escalationRecords(t)
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)
	escalator.Records = cancelSensitiveRecords{EscalationStore: records}

	ctx, cancel := context.WithCancel(context.Background())
	// The shutdown lands between her answer and the write that records it: the turn
	// came back with a decision on it, and the context is gone by the time what she
	// decided is written down.
	judge.killTurn = cancel
	sweep, err := escalator.Escalate(ctx)
	cancel()
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 1 || !sweep.Escalated[0].Delivered {
		t.Fatalf("escalated = %#v, want the delivery she answered", sweep.Escalated)
	}
	if strings.Contains(sweep.Escalated[0].Problem, "could not be recorded") {
		t.Fatalf("problem = %q, want the settle to outlive the cancellation that landed on it", sweep.Escalated[0].Problem)
	}
	recorded, found, err := records.Find(sweep.Escalated[0].DocketKey)
	if err != nil || !found {
		t.Fatalf("Find() = found %v, error %v", found, err)
	}
	if !recorded.Delivered() || recorded.Decision != "rerun" {
		t.Fatalf("recorded = %#v, want her answer written down against the attempt", recorded)
	}

	// And the harness coming back up does not ask her again.
	judge.killTurn = nil
	next, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() after the shutdown error = %v", err)
	}
	if len(next.Escalated) != 0 || len(judge.shown) != 1 {
		t.Fatalf("after the shutdown = %#v, shown %d, want a stoppage she answered left alone", next.Escalated, len(judge.shown))
	}
}

// A turn that may have been taken is one nothing can claim was not, so the
// attempt stands and the delivery does not. It is tried again, and the trying is
// bounded: past the bound the harness stops trying, and stops saying so on every
// pass with it.
func TestADeliveryThatKeepsFailingStopsBeingTried(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{err: errors.New("the provider refused the turn")}
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)
	clock := &movingClock{now: escalationNow}
	escalator.Clock = clock

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
		clock.now = clock.now.Add(runstate.EscalationRetryDelay)
	}
	// Past the bound the harness has stopped trying and says nothing further about
	// it. The stoppage is not lost by that: its record is what holds the item for a
	// person on every operator surface, and it holds it whether or not a pull
	// mentions it. What restating it here cost was every session start opening with
	// a paragraph per abandoned stoppage -- twelve of them by 2026-09-05 -- which
	// buried what each pass had actually done under a standing fact nothing was
	// acting on.
	for _, pass := range []string{"the pass that finds it spent", "the pass after that"} {
		spent, err := escalator.Escalate(context.Background())
		if err != nil {
			t.Fatalf("%s: Escalate() error = %v", pass, err)
		}
		if len(spent.Escalated) != 0 {
			t.Fatalf("%s = %#v, want a pass with nothing to do about an abandoned stoppage to say nothing", pass, spent.Escalated)
		}
		clock.now = clock.now.Add(runstate.EscalationRetryDelay)
	}
	if len(judge.shown) != runstate.MaxEscalationAttempts {
		t.Fatalf("turns taken = %d, want the %d the bound permits", len(judge.shown), runstate.MaxEscalationAttempts)
	}
}

// A session opening onto a docket full of stoppages the harness gave up on says
// nothing about them, and gets on with the one it can still deliver.
//
// This is the shape the operator actually met: twelve spent escalations from a
// kill loop, none of them anything a pass could act on, restated in full at the
// top of every session start until the block was what a session start was. The
// stoppage still awaiting somebody is what the pass is for, and it is what the
// pass should be legible as having done.
func TestASessionStartSaysNothingAboutStoppagesItGaveUpOn(t *testing.T) {
	t.Parallel()

	spent := []runstate.State{
		reviewStoppedState("run-"+strings.Repeat("a", 32), "yoyodyne-spent-1"),
		reviewStoppedState("run-"+strings.Repeat("b", 32), "yoyodyne-spent-2"),
		reviewStoppedState("run-"+strings.Repeat("c", 32), "yoyodyne-spent-3"),
	}
	refusing := &standingJudge{err: errors.New("the provider refused the turn")}
	escalator := escalatorOver(t, spent, refusing, nil)
	clock := &movingClock{now: escalationNow}
	escalator.Clock = clock

	// Every one of them tried until the harness stops trying: one attempt per
	// pass, and the pacing waited out between them.
	for range len(spent) * runstate.MaxEscalationAttempts {
		if _, err := escalator.Escalate(context.Background()); err != nil {
			t.Fatalf("Escalate() error = %v", err)
		}
		clock.now = clock.now.Add(runstate.EscalationRetryDelay)
	}
	if len(refusing.shown) != len(spent)*runstate.MaxEscalationAttempts {
		t.Fatalf("turns taken = %d, want every stoppage tried the %d times the bound permits", len(refusing.shown), runstate.MaxEscalationAttempts)
	}

	// The session that starts next: the same docket and the same records, a
	// conversation that answers, and one stoppage nobody has been shown yet.
	awaiting := reviewStoppedState(docketedRunID, docketedItem)
	docket, ok := escalator.Docket.(*memoryDocket)
	if !ok {
		t.Fatalf("docket = %T, want the memory docket the helper wires", escalator.Docket)
	}
	if _, err := docket.RecordOnce(stoppedEntry(awaiting)); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	runs, ok := escalator.Runs.(loadableRuns)
	if !ok {
		t.Fatalf("runs = %T, want the loadable runs the helper wires", escalator.Runs)
	}
	runs.states[awaiting.RunID] = awaiting
	answering := &standingJudge{judgment: Judgment{ConversationID: "chat-abc", Decision: "rerun"}}
	escalator.Manager = answering

	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("the session start's Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 1 {
		t.Fatalf("escalated = %#v, want the one stoppage the pass could still do something about and none of the spent ones", sweep.Escalated)
	}
	if !sweep.Escalated[0].Delivered || sweep.Escalated[0].RunID != docketedRunID {
		t.Fatalf("escalated = %#v, want the awaiting stoppage delivered", sweep.Escalated[0])
	}
	for _, state := range spent {
		if strings.Contains(sweep.Render(), state.RunID) {
			t.Fatalf("Render() = %q, want the session start to say nothing about the stoppage of run %s", sweep.Render(), state.RunID)
		}
	}
}

// The bound is a span of time rather than a burst. Whatever drives the delivery
// decides how often it looks, and the loop that does today looks once per pull —
// so three attempts counted and not paced would be three attempts inside one
// command, and a provider that was restarting would cost the stoppage every
// delivery it had.
func TestAFailedDeliveryIsNotRepeatedUntilItsDelayHasPassed(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{err: errors.New("the provider refused the turn")}
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)
	clock := &movingClock{now: escalationNow}
	escalator.Clock = clock

	if _, err := escalator.Escalate(context.Background()); err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	// The pulls a drain makes back to back, which is what this is about.
	for _, elapsed := range []time.Duration{0, time.Second, runstate.EscalationRetryDelay - time.Second} {
		clock.now = escalationNow.Add(elapsed)
		sweep, err := escalator.Escalate(context.Background())
		if err != nil {
			t.Fatalf("Escalate() at %s error = %v", elapsed, err)
		}
		if len(sweep.Escalated) != 0 {
			t.Fatalf("at %s the pass reported %#v, want the attempt left for its delay", elapsed, sweep.Escalated)
		}
	}
	if len(judge.shown) != 1 {
		t.Fatalf("turns taken = %d, want the one attempt the delay permits", len(judge.shown))
	}

	clock.now = escalationNow.Add(runstate.EscalationRetryDelay)
	judge.err = nil
	judge.judgment = Judgment{ConversationID: "chat-abc", Decision: "repair"}
	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() after the delay error = %v", err)
	}
	if len(sweep.Escalated) != 1 || !sweep.Escalated[0].Delivered {
		t.Fatalf("after the delay = %#v, want the stoppage tried again and delivered", sweep.Escalated)
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

// A stoppage somebody already had run again is not one nobody has looked at: the
// decision was made and carried out, and the one re-run it gets is spent.
func TestAStoppageAlreadyRunAgainIsNotDelivered(t *testing.T) {
	t.Parallel()

	stopped := reviewStoppedState(docketedRunID, docketedItem)
	judge := &standingJudge{judgment: Judgment{ConversationID: "chat-abc"}}
	escalator := escalatorOver(t, []runstate.State{stopped}, judge, nil)
	key := triage.Key(triage.ClassStoppedRun, stopped.RunID)
	escalator.Decisions = judgedItems{counters: map[string]runstate.TriageCounters{docketedItem: {Reruns: 1}}}
	escalator.Reruns = claimedReruns{claimed: map[string][]runstate.Rerun{
		docketedItem: {{DocketKey: key}},
	}}

	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 0 || len(judge.shown) != 0 {
		t.Fatalf("delivered %#v, want a stoppage already acted on left alone", sweep.Escalated)
	}
}

// The window this closes. She decides, the decision is recorded against the
// item's budget, and the carry-out happens whenever the harness or an operator
// gets to it — and in between the stopped run's own record still says exactly
// what it said before she looked at it. Every stoppage carried to her by hand
// passes through that window, tonight's two among them.
func TestAStoppageSheHasDecidedIsNotDeliveredAgain(t *testing.T) {
	t.Parallel()

	for _, judged := range []struct {
		name     string
		counters runstate.TriageCounters
	}{
		{
			// A repair grant recorded and not yet spent: the rounds the item is
			// committed to exceed the rounds it has cost.
			name:     "a repair grant she recorded that nothing has carried out",
			counters: runstate.TriageCounters{RepairGrants: 1, ReviewRounds: 2, CommittedRounds: 3},
		},
		{
			// A re-run recorded and not yet claimed. The claim is what says the
			// harness acted on it, and there is none.
			name:     "a re-run she recorded that nothing has carried out",
			counters: runstate.TriageCounters{Reruns: 1, ReviewRounds: 2},
		},
	} {
		t.Run(judged.name, func(t *testing.T) {
			t.Parallel()

			judge := &standingJudge{judgment: Judgment{ConversationID: "chat-abc"}}
			escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)
			escalator.Decisions = judgedItems{counters: map[string]runstate.TriageCounters{docketedItem: judged.counters}}

			sweep, err := escalator.Escalate(context.Background())
			if err != nil {
				t.Fatalf("Escalate() error = %v", err)
			}
			if len(sweep.Escalated) != 0 || len(judge.shown) != 0 {
				t.Fatalf("delivered %#v, want a stoppage she has already judged left alone", sweep.Escalated)
			}
		})
	}
}

// A decision that cannot be read is not a decision that is absent. Delivering on
// a record nobody could read is exactly the second delivery this guards against,
// so the pass says what it could not read and puts nothing to her.
func TestAnUnreadableDecisionRecordDeliversNothing(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{judgment: Judgment{ConversationID: "chat-abc"}}
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)
	escalator.Decisions = judgedItems{err: errors.New("the triage record is unreadable")}

	sweep, err := escalator.Escalate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "already decided") {
		t.Fatalf("Escalate() error = %v, want it to say what it could not read", err)
	}
	if len(sweep.Escalated) != 0 || len(judge.shown) != 0 {
		t.Fatalf("delivered %#v, want nothing put to her on a record nobody could read", sweep.Escalated)
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
		Docket:    docket,
		Runs:      loadableRuns{states: map[string]runstate.State{repaired.RunID: repaired}},
		Records:   escalationRecords(t),
		Decisions: judgedItems{},
		Reruns:    claimedReruns{},
		Manager:   judge,
		Clock:     escalationClock{},
	}

	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 0 || len(judge.shown) != 0 {
		t.Fatalf("delivered %#v, want the repaired run left alone", sweep.Escalated)
	}
}

// The item's own replay: the two runs that stopped at 2-of-2 on the night this
// was asked for, reaching her judgment with nobody carrying them.
//
// It is the closest a test can stand to that replay, and it is written here
// because the real one cannot be run from a developer's worktree: the two runs
// are records in the operator's own state root, and replaying them means opening
// her conversation against a real provider account and spending real money on a
// turn. What this holds is the whole of the flow those relays performed — the
// docketed stoppage reaching her, her decision coming back recorded, and the
// stoppage then being left alone while the carry-out is still owed — with the
// couriers removed and nothing else changed.
func TestTheTwoHandRelayedStoppagesReachHerWithNobodyCarryingThem(t *testing.T) {
	t.Parallel()

	// Two runs, each stopped on its reviewer with its change preserved, exactly
	// as the pair on that night were.
	first := reviewStoppedState(docketedRunID, "yoyodyne-ifd.224")
	second := reviewStoppedState("run-4444444444444444444444444444dddd", "yoyodyne-ifd.226")
	judge := &standingJudge{judgment: Judgment{
		ConversationID: "chat-91253e0e070c17b0663651cc48602122",
		Decision:       "repair",
		Reason:         "the findings are narrow and the change is preserved, so one more bounded attempt is worth it",
	}}
	escalator := escalatorOver(t, []runstate.State{first, second}, judge, nil)

	// Two passes, two deliveries, and a recorded decision on each. The relays
	// that stood here were: somebody opening her conversation to tell her, and
	// somebody carrying her answer back to be written down.
	for pass, stopped := range []runstate.State{first, second} {
		sweep, err := escalator.Escalate(context.Background())
		if err != nil {
			t.Fatalf("pass %d: Escalate() error = %v", pass+1, err)
		}
		if len(sweep.Escalated) != 1 {
			t.Fatalf("pass %d delivered %#v, want the one stoppage", pass+1, sweep.Escalated)
		}
		escalated := sweep.Escalated[0]
		if !escalated.Delivered || escalated.RunID != stopped.RunID || escalated.WorkItemID != stopped.WorkItemID {
			t.Fatalf("pass %d = %#v, want %s of %s delivered", pass+1, escalated, stopped.RunID, stopped.WorkItemID)
		}
		if escalated.Decision != "repair" || escalated.Problem != "" {
			t.Fatalf("pass %d = %#v, want her decision recorded and nothing gone wrong", pass+1, escalated)
		}
	}
	if len(judge.shown) != 2 {
		t.Fatalf("shown = %#v, want both stoppages put to her", judge.shown)
	}

	// And then the state those two were actually left in: a repair grant recorded
	// against each item and the carry-out still owed. Neither is put to her a
	// second time, which is what the relayed version could not promise.
	escalator.Decisions = judgedItems{counters: map[string]runstate.TriageCounters{
		"yoyodyne-ifd.224": {RepairGrants: 1, ReviewRounds: 2, CommittedRounds: 3},
		"yoyodyne-ifd.226": {RepairGrants: 1, ReviewRounds: 2, CommittedRounds: 3},
	}}
	settled, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() after her decisions error = %v", err)
	}
	if len(settled.Escalated) != 0 || len(judge.shown) != 2 {
		t.Fatalf("after her decisions the pass reported %#v, want both stoppages left alone", settled.Escalated)
	}
}

// A conversation nothing can open asks her nothing, so its attempt comes back
// and the stoppage is never abandoned. The reasons are all reasons that clear —
// a signed-out provider, a conversation somebody has open, a role nobody has
// configured — and a stoppage given up on while the harness was misconfigured is
// one nothing re-delivers when somebody fixes it.
func TestAnUnreachableConversationIsNeverAbandoned(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{err: fmt.Errorf("%w: no agent fills the development-manager role", ErrConversationUnreachable)}
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)
	clock := &movingClock{now: escalationNow}
	escalator.Clock = clock

	// Well past the bound that abandons a turn which may have been taken.
	for attempt := 1; attempt <= runstate.MaxEscalationAttempts+3; attempt++ {
		sweep, err := escalator.Escalate(context.Background())
		if err != nil {
			t.Fatalf("attempt %d: Escalate() error = %v", attempt, err)
		}
		if len(sweep.Escalated) != 1 || sweep.Escalated[0].Delivered {
			t.Fatalf("attempt %d = %#v, want the stoppage still being tried", attempt, sweep.Escalated)
		}
		if strings.Contains(sweep.Escalated[0].Problem, "needs a person") {
			t.Fatalf("attempt %d was abandoned: %q", attempt, sweep.Escalated[0].Problem)
		}
		clock.now = clock.now.Add(runstate.EscalationRetryDelay)
	}
	if len(judge.shown) != runstate.MaxEscalationAttempts+3 {
		t.Fatalf("attempts made = %d, want one per delay for as long as the conversation stays shut", len(judge.shown))
	}

	// And it lands the moment the conversation opens.
	judge.err = nil
	judge.judgment = Judgment{ConversationID: "chat-abc", Decision: "repair"}
	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() once the conversation opened error = %v", err)
	}
	if len(sweep.Escalated) != 1 || !sweep.Escalated[0].Delivered {
		t.Fatalf("once the conversation opened = %#v, want the stoppage delivered", sweep.Escalated)
	}
}

// What a delivery cost comes back on the sweep, because the pass that made it is
// the only thing that can count it: a turn is not a run, and a session bounded by
// a budget has nothing else to read it from.
func TestWhatADeliveryCostComesBackOnTheSweep(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{judgment: Judgment{ConversationID: "chat-abc", Decision: "repair", CostUSD: 0.42}}
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)

	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 1 || sweep.Escalated[0].CostUSD != 0.42 {
		t.Fatalf("escalated = %#v, want what the turn cost carried back", sweep.Escalated)
	}

	// And a turn that failed cost what it cost: the provider charged for it
	// exactly as it charges for one that answered.
	spent := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, &standingJudge{}, nil)
	spent.Manager = costingJudge{cost: 0.17, err: errors.New("the reply could not be read")}
	sweep, err = spent.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 1 || sweep.Escalated[0].Delivered || sweep.Escalated[0].CostUSD != 0.17 {
		t.Fatalf("escalated = %#v, want a failed turn reporting what it cost", sweep.Escalated)
	}
}

// costingJudge is a turn that cost money and then failed, which is the one shape
// standingJudge cannot express: its failure returns no judgment at all.
type costingJudge struct {
	cost float64
	err  error
}

func (j costingJudge) Judge(context.Context, triage.Entry) (Judgment, error) {
	return Judgment{ConversationID: "chat-abc", CostUSD: j.cost}, j.err
}

// coolingRecords is the escalation record as the loser of a race meets it: the
// claim refused because another session took it a moment ago. It cannot be
// produced with the real store from one process, because the pass reads the
// pacing from the same clock it claims with.
type coolingRecords struct {
	EscalationRecords
}

func (coolingRecords) Attempt(context.Context, runstate.Escalation) (runstate.Escalation, error) {
	return runstate.Escalation{}, runstate.EscalationCoolingError{
		Existing: runstate.Escalation{RunID: docketedRunID, Attempts: 1, LastAttemptedAt: escalationNow},
		At:       escalationNow,
	}
}

// The loser of that race says nothing and takes no turn. Another session is
// delivering this stoppage, which is the record doing its job rather than
// anything this pass has to report.
func TestAStoppageAnotherSessionJustClaimedIsLeftToIt(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{judgment: Judgment{ConversationID: "chat-abc", Decision: "repair"}}
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)
	escalator.Records = coolingRecords{EscalationRecords: escalator.Records}

	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 0 || len(judge.shown) != 0 {
		t.Fatalf("sweep = %#v, want the stoppage left to the session that claimed it", sweep.Escalated)
	}
}

// The limit of what the harness can see, held here so it cannot drift from what
// the documents promise. Escalating to the operator, re-scoping, and waiting
// spend nothing, so they leave no counter anywhere this reads — and a stoppage
// she settled one of those ways is delivered to her once more. What that costs is
// a turn and a paragraph she has read before: the docket entry says what has been
// decided about the item, and the delivery spends no budget and carries nothing
// out.
func TestAStoppageSettledWithoutSpendingIsDeliveredAgain(t *testing.T) {
	t.Parallel()

	judge := &standingJudge{judgment: Judgment{ConversationID: "chat-abc"}}
	escalator := escalatorOver(t, []runstate.State{reviewStoppedState(docketedRunID, docketedItem)}, judge, nil)
	// The rounds the item has cost, and nothing committed or re-run: what the
	// record looks like after she escalated it, re-scoped it, or said to wait.
	escalator.Decisions = judgedItems{counters: map[string]runstate.TriageCounters{
		docketedItem: {ReviewRounds: 2},
	}}

	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 1 || !sweep.Escalated[0].Delivered {
		t.Fatalf("sweep = %#v, want the stoppage delivered, which is what the documents say happens", sweep.Escalated)
	}
}
