package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var moment = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

type fakeRuns struct {
	incomplete  []runstate.State
	outstanding []runstate.State
	prices      map[string]runstate.ItemPrice
	failIncomplete,
	failOutstanding,
	failPrice error
}

func (f fakeRuns) Incomplete() ([]runstate.State, error) {
	return f.incomplete, f.failIncomplete
}

func (f fakeRuns) Outstanding() ([]runstate.State, error) {
	return f.outstanding, f.failOutstanding
}

func (f fakeRuns) Price(workItemID string) (runstate.ItemPrice, error) {
	if f.failPrice != nil {
		return runstate.ItemPrice{}, f.failPrice
	}
	return f.prices[workItemID], nil
}

type fakeConversations struct {
	recorded    []runstate.Conversation
	held        map[string]bool
	fail        error
	failObserve map[string]error
}

func (f fakeConversations) Recorded() ([]runstate.Conversation, error) {
	return f.recorded, f.fail
}

func (f fakeConversations) InFlight(identity runstate.ConversationIdentity) (bool, error) {
	if err, named := f.failObserve[identity.Agent]; named {
		return false, err
	}
	return f.held[identity.Agent], nil
}

type fakeTracker struct {
	byStatus map[string][]beads.WorkItem
	ready    []beads.WorkItem
	fail     error
}

func (f fakeTracker) List(context.Context, string) ([]beads.WorkItem, error) {
	return nil, f.fail
}

func (f fakeTracker) Ready(context.Context) ([]beads.WorkItem, error) {
	return f.ready, f.fail
}

type statusTracker struct{ fakeTracker }

func (s statusTracker) List(_ context.Context, status string) ([]beads.WorkItem, error) {
	if s.fail != nil {
		return nil, s.fail
	}
	return s.byStatus[status], nil
}

type fakeDirectives struct {
	recorded []directive.Directive
	fail     error
}

func (f fakeDirectives) List() ([]directive.Directive, error) { return f.recorded, f.fail }

type fakeAmendments struct {
	records []amendment.Record
	fail    error
}

func (f fakeAmendments) List() ([]amendment.Record, error) { return f.records, f.fail }

type fakeOperatorHolds struct {
	hold runstate.OperatorHold
	held bool
	fail error
}

func (f fakeOperatorHolds) Held() (runstate.OperatorHold, bool, error) {
	return f.hold, f.held, f.fail
}

type fakeIntakeHolds struct {
	hold runstate.IntakeHold
	held bool
	fail error
}

func (f fakeIntakeHolds) Held() (runstate.IntakeHold, bool, error) {
	return f.hold, f.held, f.fail
}

type fakeSessions struct {
	transitions []runstate.WatchTransition
	fail        error
}

func (f fakeSessions) List() ([]runstate.WatchTransition, error) { return f.transitions, f.fail }

// quietSources is a harness with nothing wrong with it and nothing happening:
// one session choosing work, no holds, an empty queue. Each test moves one
// thing, so what a line says is attributable to the one record that changed.
func quietSources() Sources {
	return Sources{
		Runs:          fakeRuns{prices: map[string]runstate.ItemPrice{}},
		Conversations: fakeConversations{},
		Tracker:       statusTracker{},
		Directives:    fakeDirectives{},
		Amendments:    fakeAmendments{},
		OperatorHolds: fakeOperatorHolds{},
		IntakeHolds:   fakeIntakeHolds{},
		Sessions: fakeSessions{transitions: []runstate.WatchTransition{
			{SessionID: "watch-1", State: runstate.WatchWatching, At: moment.Add(-time.Hour)},
		}},
		Capacity: 2,
		Now:      func() time.Time { return moment },
	}
}

// A quiet harness still prints all four lines, and every one of them says
// "nothing" in words. This is the whole point of the format: silence is what an
// operator cannot read, so there is no state in which a line is simply absent.
func TestQuietHarnessPrintsAllFourLines(t *testing.T) {
	t.Parallel()
	standing := ReadStanding(context.Background(), quietSources())
	rendered := standing.Render()
	want := "Running: nothing\n" +
		"Working: nothing\n" +
		"Not startable: nothing, of no admitted items\n" +
		"Needs a human: nothing\n"
	if rendered != want {
		t.Fatalf("rendered:\n%s\nwant:\n%s", rendered, want)
	}
}

// The operator's own example, rendered from live state: a run with its item,
// phase, elapsed and spend; a conversation turn in flight; a refusal taken from
// the queue; and something waiting on a named person.
func TestTheOperatorsExampleRendersFromState(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID:      "run-a",
			WorkItemID: "yoyodyne-ifd.194",
			Status:     runstate.StatusRunning,
			Phase:      runstate.PhaseDeveloping,
			StartedAt:  moment.Add(-12 * time.Minute),
		}},
		prices: map[string]runstate.ItemPrice{
			"yoyodyne-ifd.194": {Runs: []runstate.RunPrice{{RunID: "run-a", CostUSD: 3.41}}},
		},
	}
	sources.Conversations = fakeConversations{
		recorded: []runstate.Conversation{{
			ConversationID: "chat-1",
			Agent:          "product-manager",
			Role:           domain.RoleProductManager,
			Turns:          270,
			UpdatedAt:      moment.Add(-40 * time.Second),
		}},
		held: map[string]bool{"product-manager": true},
	}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{
			"open": {
				{ID: "yoyodyne-ifd.194", Title: "the four-line status", Status: "open"},
				{ID: "yoyodyne-ifd.200", Title: "later work", Status: "open"},
			},
			"blocked": {{ID: "yoyodyne-ifd.201", Title: "waiting work", Status: "blocked"}},
		},
		ready: []beads.WorkItem{{ID: "yoyodyne-ifd.194"}, {ID: "yoyodyne-ifd.200"}},
	}}
	sources.IntakeHolds = fakeIntakeHolds{
		hold: runstate.IntakeHold{HeldAt: moment.Add(-2 * time.Hour), Reason: "the overnight looked wrong"},
		held: true,
	}

	rendered := ReadStanding(context.Background(), sources).Render()
	for _, want := range []string{
		"Running (1 developer run):\n",
		"  yoyodyne-ifd.194 — developing, 12m elapsed, $3.41 so far\n",
		"Working (1 conversation):\n",
		"  product-manager — product-manager, a turn in flight for 40s after 270 recorded turns\n",
		"Not startable (2 of 3 admitted items):\n",
		"  yoyodyne-ifd.200 — intake is held — the overnight looked wrong; `yoyo release` lifts it\n",
		"  yoyodyne-ifd.201 — blocked; the item says what by\n",
		"Needs a human (1):\n",
		"intake is held, since 2026-08-30T10:00:00Z: the overnight looked wrong — the operator's",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered:\n%s\nmissing: %q", rendered, want)
		}
	}
	// The item a run is already carrying is on the running line and nowhere else.
	if strings.Count(rendered, "yoyodyne-ifd.194") != 1 {
		t.Fatalf("the running item is named more than once:\n%s", rendered)
	}
}

// A conversation turn in flight is what no surface counted before this. It is
// the lease that decides, so a recorded conversation nobody is holding is not
// working.
func TestWorkingCountsOnlyHeldConversations(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Conversations = fakeConversations{
		recorded: []runstate.Conversation{
			{ConversationID: "chat-1", Agent: "architect", Role: domain.RoleArchitect, Turns: 4, UpdatedAt: moment},
			{ConversationID: "chat-2", Agent: "reviewer", Role: domain.RoleReviewer, Turns: 9, UpdatedAt: moment},
		},
		held: map[string]bool{"architect": true},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Working) != 1 || standing.Working[0].Agent != "architect" {
		t.Fatalf("working = %+v, want only the held conversation", standing.Working)
	}
}

// A conversation record written before the agent was part of the identity is
// probed under the agent named for its role, which is where it actually lives.
func TestWorkingProbesRoleNamedAgentForOlderRecords(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Conversations = fakeConversations{
		recorded: []runstate.Conversation{
			{ConversationID: "chat-1", Role: domain.RoleArchitect, Turns: 2, UpdatedAt: moment},
		},
		held: map[string]bool{"architect": true},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Working) != 1 {
		t.Fatalf("working = %+v, want the record probed under its role's agent name", standing.Working)
	}
}

// A source that cannot be read never says "nothing". It says the harness does
// not know, which is a different answer and the one a reader acts on.
func TestAnUnreadableSourceIsNeverReportedAsNothing(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = fakeRuns{failIncomplete: errors.New("the state directory would not open")}
	sources.Tracker = statusTracker{fakeTracker{fail: errors.New("the tracker did not answer")}}

	standing := ReadStanding(context.Background(), sources)
	rendered := standing.Render()
	if !strings.Contains(rendered, "Running: could not be read — the runs in flight could not be read: the state directory would not open\n") {
		t.Fatalf("rendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Not startable: could not be read — ") {
		t.Fatalf("rendered:\n%s", rendered)
	}
	if strings.Contains(rendered, "Running: nothing") || strings.Contains(rendered, "Not startable: nothing") {
		t.Fatalf("an unreadable source was reported as nothing:\n%s", rendered)
	}
}

// A switch nobody could read is never reported as clear either: the line that
// would otherwise say what holds work back says it could not tell.
func TestAnUnreadableSwitchIsSaidRatherThanAssumedClear(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.IntakeHolds = fakeIntakeHolds{fail: errors.New("the hold file would not open")}
	standing := ReadStanding(context.Background(), sources)
	if !strings.Contains(standing.NotStartableProblem, "the intake hold could not be read") {
		t.Fatalf("not-startable problem = %q", standing.NotStartableProblem)
	}
	if !strings.Contains(standing.NeedsHumanProblem, "the intake hold could not be read") {
		t.Fatalf("needs-a-human problem = %q", standing.NeedsHumanProblem)
	}
}

// A run whose evidence cannot be priced is stated as unpriceable rather than as
// free. A figure of zero against an hour of provider work is the one number this
// may not print.
func TestAnUnpriceableRunIsNotFree(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID: "run-a", WorkItemID: "item-1", Status: runstate.StatusRunning,
			Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-time.Hour),
		}},
		prices: map[string]runstate.ItemPrice{
			"item-1": {Runs: []runstate.RunPrice{{RunID: "run-a", Unknown: "its event log is gone"}}},
		},
	}
	rendered := ReadStanding(context.Background(), sources).Render()
	if !strings.Contains(rendered, "cost unknown (its event log is gone)") {
		t.Fatalf("rendered:\n%s", rendered)
	}
	if strings.Contains(rendered, "$0.00") {
		t.Fatalf("an unpriceable run was reported as free:\n%s", rendered)
	}
}

// Nothing choosing work is a refusal in its own right. It is the state the
// overnight was in from midnight, and the one that reads exactly like a healthy
// quiet machine unless it is said.
func TestNoSessionChoosingIsARefusal(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Sessions = fakeSessions{transitions: []runstate.WatchTransition{
		{SessionID: "watch-1", State: runstate.WatchWatching, At: moment.Add(-3 * time.Hour)},
		{SessionID: "watch-1", State: runstate.WatchStopped, At: moment.Add(-2 * time.Hour)},
	}}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{ID: "item-1", Status: "open"}}},
		ready:    []beads.WorkItem{{ID: "item-1"}},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 1 || !strings.Contains(standing.NotStartable[0].Reason, "no watch session is running") {
		t.Fatalf("not startable = %+v", standing.NotStartable)
	}
	// It is also waiting on somebody. A queue nobody is pulling from will wait
	// forever without a person, and the attention line is where a person looks.
	if len(standing.NeedsHuman) != 1 || !strings.Contains(standing.NeedsHuman[0].Whose, "`yoyo work --watch`") {
		t.Fatalf("needs a human = %+v", standing.NeedsHuman)
	}
}

// A session that stopped while another carries on watching is not the product
// having stopped. The fold is per session, so the last line of the log never
// decides on its own.
func TestChoosingReadsTheLogPerSession(t *testing.T) {
	t.Parallel()
	sessions := []runstate.WatchTransition{
		{SessionID: "watch-1", State: runstate.WatchWatching, At: moment.Add(-3 * time.Hour)},
		{SessionID: "watch-2", State: runstate.WatchWatching, At: moment.Add(-2 * time.Hour)},
		{SessionID: "watch-2", State: runstate.WatchStopped, At: moment.Add(-time.Hour)},
	}
	choosing := Choosing(sessions)
	if len(choosing) != 1 || choosing[0].SessionID != "watch-1" {
		t.Fatalf("choosing = %+v, want only the session still watching", choosing)
	}
	// An idle session is alive and choosing nothing, which is opposite answers to
	// the two questions the same fold serves.
	idle := []runstate.WatchTransition{{SessionID: "watch-3", State: runstate.WatchIdle, At: moment}}
	if len(Choosing(idle)) != 0 {
		t.Fatalf("an idle session was reported as choosing work")
	}
	if len(Live(idle)) != 1 {
		t.Fatalf("an idle session was reported as gone")
	}
}

// The state that read as a stopped machine, replayed against the four lines: a
// watch session idle on one developer slot while a run works on the other, over
// a queue whose only unstarted work is the architect's to carry.
//
// The run is on the Running line, which is what keeps idle-on-one-slot from
// reading as system-idle. And nothing here is waiting on the operator: the items
// are refused for what they are rather than for a session that has stopped, so
// the only attention is the architect's conversation. An operator sent to look
// at the session, or at an admission, was sent somewhere nothing would change.
func TestARunInFlightBesideWorkAConversationCarriesIsNotAStalledLine(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 2
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID: "run-236", WorkItemID: "yoyodyne-ifd.236", Status: runstate.StatusRunning,
			Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-20 * time.Minute),
		}},
		prices: map[string]runstate.ItemPrice{},
	}
	architects := []beads.WorkItem{
		{ID: "yoyodyne-ifd.212", Status: "open", Executor: domain.ConversationWith(domain.RoleArchitect)},
		{ID: "yoyodyne-ifd.203", Status: "open", Executor: domain.ConversationWith(domain.RoleArchitect)},
	}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": architects},
		ready:    architects,
	}}
	// The session is alive and choosing nothing, which is exactly the log the
	// misleading line was written from.
	sources.Sessions = fakeSessions{transitions: []runstate.WatchTransition{
		{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-time.Hour)},
	}}

	standing := ReadStanding(context.Background(), sources)
	if len(standing.Running) != 1 || standing.Running[0].WorkItemID != "yoyodyne-ifd.236" {
		t.Fatalf("running = %+v, want the run in flight on the running line", standing.Running)
	}
	for _, refused := range standing.NotStartable {
		if strings.Contains(refused.Reason, "found nothing it can start") {
			t.Fatalf("not startable = %+v, want each item refused for what it is rather than for the session", standing.NotStartable)
		}
	}
	if len(standing.NeedsHuman) != len(architects) {
		t.Fatalf("needs a human = %+v, want only the conversations that carry the work", standing.NeedsHuman)
	}
	for _, attention := range standing.NeedsHuman {
		if !strings.Contains(attention.Whose, "the architect's") {
			t.Fatalf("needs a human = %+v, want the architect named rather than the operator", standing.NeedsHuman)
		}
	}
}

// Every developer slot taken is a different refusal from a held switch, and an
// operator does an entirely different thing about it.
func TestAFullMachineIsItsOwnRefusal(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 1
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{{
			RunID: "run-a", WorkItemID: "item-0", Status: runstate.StatusRunning,
			Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-time.Minute),
		}},
		prices: map[string]runstate.ItemPrice{},
	}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{ID: "item-1", Status: "open"}}},
		ready:    []beads.WorkItem{{ID: "item-1"}},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 1 || !strings.Contains(standing.NotStartable[0].Reason, "every developer slot is taken: 1 of 1") {
		t.Fatalf("not startable = %+v", standing.NotStartable)
	}
}

// An item the harness would start next is not on the not-startable line at all.
// The line means what it says, and a startable item listed with a reason nobody
// could act on is what makes a listing stop being read.
func TestStartableWorkIsNotListedAsRefused(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{ID: "item-1", Status: "open"}}},
		ready:    []beads.WorkItem{{ID: "item-1"}},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 0 {
		t.Fatalf("not startable = %+v, want nothing", standing.NotStartable)
	}
	if standing.Admitted != 1 {
		t.Fatalf("admitted = %d, want the startable item still counted", standing.Admitted)
	}
	if !strings.Contains(standing.Render(), "Not startable: nothing, of 1 admitted item\n") {
		t.Fatalf("rendered:\n%s", standing.Render())
	}
}

// An unresolved directive that pauses an item is the real refusal for it, and it
// is what the pipeline would refuse the work with.
func TestADirectivePauseIsTheItemsOwnRefusal(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Directives = fakeDirectives{recorded: []directive.Directive{{
		ID:         "dir-1",
		Kind:       directive.KindAmbiguous,
		Text:       "which branch does this land on?",
		Unresolved: "which branch does this land on?",
		Scope:      []string{"item-1"},
	}}}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {
			{ID: "item-1", Status: "open"},
			{ID: "item-2", Status: "open"},
		}},
		ready: []beads.WorkItem{{ID: "item-1"}, {ID: "item-2"}},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 1 {
		t.Fatalf("not startable = %+v, want only the paused item", standing.NotStartable)
	}
	if !strings.Contains(standing.NotStartable[0].Reason, "paused for unresolved directive dir-1") {
		t.Fatalf("reason = %q", standing.NotStartable[0].Reason)
	}
	// The same directive is a thing waiting on a person, with whose move it is.
	if len(standing.NeedsHuman) != 1 || !strings.Contains(standing.NeedsHuman[0].Whose, "the operator's") {
		t.Fatalf("needs a human = %+v", standing.NeedsHuman)
	}
}

// A run that ended still owing a step waits forever without somebody running the
// sweep, so it is named with the command that settles it.
func TestAnOutstandingRunNeedsAHuman(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Runs = fakeRuns{
		prices:      map[string]runstate.ItemPrice{},
		outstanding: []runstate.State{{RunID: "run-a", WorkItemID: "item-1"}},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NeedsHuman) != 1 {
		t.Fatalf("needs a human = %+v", standing.NeedsHuman)
	}
	if !strings.Contains(standing.NeedsHuman[0].Whose, "yoyo reconcile") {
		t.Fatalf("whose = %q, want the command that settles it", standing.NeedsHuman[0].Whose)
	}
}

// Work marked for a conversation says a different thing on each line: why
// nothing pulls it, and who has to open the conversation.
func TestHandedOffWorkIsNamedOnBothLines(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{
			ID: "item-1", Status: "open", Executor: domain.ConversationWith(domain.RoleArchitect),
		}}},
		ready: []beads.WorkItem{{ID: "item-1"}},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 1 || !strings.Contains(standing.NotStartable[0].Reason, "rather than a developer run") {
		t.Fatalf("not startable = %+v", standing.NotStartable)
	}
	if len(standing.NeedsHuman) != 1 || !strings.Contains(standing.NeedsHuman[0].Whose, "the architect's") {
		t.Fatalf("needs a human = %+v", standing.NeedsHuman)
	}
}

// A parked item is a decision somebody already took. It is a refusal and it is
// not waiting on anybody, so listing it as needing a human would send an
// operator to act on something that is settled.
func TestParkedWorkIsRefusedAndNeedsNobody(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{
			ID: "item-1", Status: "open", Parking: domain.WorkItemParking("the design is being reworked"),
		}}},
		ready: []beads.WorkItem{{ID: "item-1"}},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 1 || !strings.Contains(standing.NotStartable[0].Reason, "parked") {
		t.Fatalf("not startable = %+v", standing.NotStartable)
	}
	if len(standing.NeedsHuman) != 0 {
		t.Fatalf("needs a human = %+v, want nothing", standing.NeedsHuman)
	}
}

// A wiring gap is reported as a wiring gap. A surface assembled without a source
// must not report the state that source describes as empty.
func TestAnUnwiredSourceIsSaidRatherThanAssumedEmpty(t *testing.T) {
	t.Parallel()
	standing := ReadStanding(context.Background(), Sources{Now: func() time.Time { return moment }})
	for _, problem := range []string{
		standing.RunningProblem,
		standing.WorkingProblem,
		standing.NotStartableProblem,
		standing.NeedsHumanProblem,
	} {
		if !strings.Contains(problem, "nothing was wired") {
			t.Fatalf("problem = %q, want a stated wiring gap", problem)
		}
	}
}

// A conversation whose hold could not be observed is not counted either way,
// and the count says it is partial rather than passing as complete.
func TestAConversationThatCannotBeAskedIsSaidBesideTheCount(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Conversations = fakeConversations{
		recorded: []runstate.Conversation{
			{ConversationID: "chat-1", Agent: "architect", Role: domain.RoleArchitect, UpdatedAt: moment},
			{ConversationID: "chat-2", Agent: "reviewer", Role: domain.RoleReviewer, UpdatedAt: moment},
		},
		held:        map[string]bool{"reviewer": true},
		failObserve: map[string]error{"architect": errors.New("the holder stamp would not open")},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.Working) != 1 {
		t.Fatalf("working = %+v, want the one that answered", standing.Working)
	}
	if !strings.Contains(standing.WorkingProblem, "architect") {
		t.Fatalf("working problem = %q", standing.WorkingProblem)
	}
	if !strings.Contains(standing.Render(), "  not fully read: ") {
		t.Fatalf("rendered:\n%s", standing.Render())
	}
}

// fakeGates stands in for the harness's record of what a person has done.
type fakeGates struct {
	discharged []string
	fail       error
}

func (f fakeGates) DischargedGates() ([]string, error) { return f.discharged, f.fail }

// An item held by a step only a person can take is on two lines and says a
// different thing on each: the queue's line says why nothing pulls it, and the
// attention line says whose move it is and what records the act. A reader given
// only the first has been told the item is waiting, which is what a wait on
// other work also looks like.
func TestWorkHeldByAPersonsStepIsNamedAsWaitingOnThem(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{
			ID: "yoyodyne-ifd.209.7", Title: "Declarative becomes the default", Status: "open",
			Description: "human-gate: soak-reviewed — the operator has judged the parity soak\n",
		}}},
		ready: []beads.WorkItem{{ID: "yoyodyne-ifd.209.7"}},
	}}
	standing := ReadStanding(context.Background(), sources)

	if len(standing.NotStartable) != 1 || !strings.Contains(standing.NotStartable[0].Reason, "waiting on a person") {
		t.Fatalf("not startable = %+v", standing.NotStartable)
	}
	if len(standing.NeedsHuman) != 1 {
		t.Fatalf("needs human = %+v", standing.NeedsHuman)
	}
	attention := standing.NeedsHuman[0]
	for _, want := range []string{"yoyodyne-ifd.209.7", "soak-reviewed", "judged the parity soak"} {
		if !strings.Contains(attention.What, want) {
			t.Fatalf("what = %q, want it to mention %q", attention.What, want)
		}
	}
	for _, want := range []string{"closing an item", "yoyo gate record soak-reviewed"} {
		if !strings.Contains(attention.Whose, want) {
			t.Fatalf("whose = %q, want it to mention %q", attention.Whose, want)
		}
	}

	// Once the act is on the record the item is startable and nobody is waiting.
	sources.Gates = fakeGates{discharged: []string{"soak-reviewed"}}
	passed := ReadStanding(context.Background(), sources)
	if len(passed.NotStartable) != 0 || len(passed.NeedsHuman) != 0 {
		t.Fatalf("not startable = %+v, needs human = %+v", passed.NotStartable, passed.NeedsHuman)
	}
}

// What could not be read about the gates is said where it changed the answer,
// and not where it changed nothing. A caveat printed on every quiet reading is a
// caveat nobody reads by the time it matters.
func TestAnUnreadableGateRecordIsSaidOnlyWhereItOverstatesTheWait(t *testing.T) {
	t.Parallel()

	quiet := quietSources()
	quiet.Gates = fakeGates{fail: errors.New("the state store would not answer")}
	if problem := ReadStanding(context.Background(), quiet).NotStartableProblem; problem != "" {
		t.Fatalf("not startable problem = %q, want nothing said where no gate was declared", problem)
	}

	gated := quiet
	gated.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{
			ID: "yoyodyne-ifd.209.7", Status: "open",
			Description: "human-gate: soak-reviewed — the operator has judged the parity soak\n",
		}}},
		ready: []beads.WorkItem{{ID: "yoyodyne-ifd.209.7"}},
	}}
	problem := ReadStanding(context.Background(), gated).NotStartableProblem
	if !strings.Contains(problem, "would not answer") || !strings.Contains(problem, "1 gated item") {
		t.Fatalf("not startable problem = %q", problem)
	}
}

// A declaration nothing could read holds the item and is on the attention line
// too, saying the one thing that differs: no act records this one, and its
// author has to correct the line. An operator told only "waiting on a person"
// would go looking for a gate name to record and find none.
func TestADeclarationNothingCouldReadIsNamedAsTheAuthorsMove(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{
			ID: "yoyodyne-ifd.209.7", Title: "Declarative becomes the default", Status: "open",
			Description: "human-gate: Soak Reviewed — the operator has judged the parity soak\n",
		}}},
		ready: []beads.WorkItem{{ID: "yoyodyne-ifd.209.7"}},
	}}
	// Every act anybody could have recorded is on the record, and it still holds.
	sources.Gates = fakeGates{discharged: []string{"soak-reviewed", "soak"}}
	standing := ReadStanding(context.Background(), sources)

	if len(standing.NotStartable) != 1 {
		t.Fatalf("not startable = %+v", standing.NotStartable)
	}
	if len(standing.NeedsHuman) != 1 {
		t.Fatalf("needs human = %+v", standing.NeedsHuman)
	}
	attention := standing.NeedsHuman[0]
	if !strings.Contains(attention.What, "nothing could read") {
		t.Fatalf("what = %q", attention.What)
	}
	if !strings.Contains(attention.Whose, "author") {
		t.Fatalf("whose = %q, want the author's move rather than an act to record", attention.Whose)
	}
}
