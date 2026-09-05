package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

var recurringNow = time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)

// wokenRole is a role's conversation as a test reaches it: what it was asked, and
// what it was told to answer.
type wokenRole struct {
	messages []string
	answers  []orchestrator2Answer
	failure  error
}

// orchestrator2Answer is one scripted turn.
type orchestrator2Answer struct {
	result *sweep.Result
	err    error
	cost   float64
}

func (r *wokenRole) Wake(_ context.Context, _ domain.AgentRole, message string) (Turn, error) {
	r.messages = append(r.messages, message)
	if r.failure != nil {
		return Turn{}, r.failure
	}
	if len(r.answers) == 0 {
		return Turn{ConversationID: "chat-1"}, nil
	}
	answer := r.answers[0]
	r.answers = r.answers[1:]
	return Turn{ConversationID: "chat-1", CostUSD: answer.cost, Result: answer.result}, answer.err
}

func sweepStore(t *testing.T) *runstate.SweepStore {
	t.Helper()
	store, err := runstate.NewSweepStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewSweepStore() error = %v", err)
	}
	return store
}

func hourlyTask(prompt string) map[string]config.RecurringTask {
	return map[string]config.RecurringTask{
		"a-sweep": {
			Role:     domain.RoleDevelopmentManager,
			Every:    config.Duration(time.Hour),
			Enabled:  true,
			Prompt:   prompt,
			MaxTurns: 3,
		},
	}
}

func complete(summary string, findings ...sweep.Finding) *sweep.Result {
	return &sweep.Result{Status: sweep.StatusComplete, Summary: summary, Findings: findings}
}

func TestFiringWakesTheRoleAndRecordsWhatItFound(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []orchestrator2Answer{{
		result: complete("one dead claim, released", sweep.Finding{
			Issue:       "a claim on a run nothing is running",
			Disposition: sweep.DispositionFixed,
			Filed:       []string{"yoyodyne-ifd.300"},
		}),
		cost: 0.25,
	}}}
	trigger := Trigger{Tasks: hourlyTask("look for unresolved issues"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want the one due task", fired.Fired)
	}
	if fired.Fired[0].Findings != 1 || fired.Fired[0].Turns != 1 {
		t.Errorf("fired = %+v, want one finding in one turn", fired.Fired[0])
	}
	if fired.Fired[0].CostUSD != 0.25 {
		t.Errorf("cost = %v, want what the turn cost", fired.Fired[0].CostUSD)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Result == nil {
		t.Fatalf("recorded = %+v, want one durable report with an account on it", recorded)
	}
	if recorded[0].ConversationID != "chat-1" {
		t.Errorf("conversation = %q, want the role's own conversation", recorded[0].ConversationID)
	}
}

// The message the harness sends carries three things the configuration cannot
// change: who woke the role, that being woken grants it nothing, and that a
// finding is checked against admitted work before it is filed.
func TestTheWakeMessageCarriesTheStandingConstraints(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []orchestrator2Answer{{result: complete("nothing")}}}
	trigger := Trigger{Tasks: hourlyTask("the project's own instruction"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}
	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(role.messages) != 1 {
		t.Fatalf("messages = %v, want one", role.messages)
	}
	for _, want := range []string{
		"the project's own instruction",
		"authority your role already holds",
		"check it against the work already admitted",
		sweep.Fence,
	} {
		if !strings.Contains(role.messages[0], want) {
			t.Errorf("the wake message does not carry %q:\n%s", want, role.messages[0])
		}
	}
}

// A heavy pass iterates rather than truncating: the role says it has more to do
// and is given another turn, and the findings of every turn are kept.
func TestAHeavyPassIteratesItsTurns(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []orchestrator2Answer{
		{result: &sweep.Result{Status: sweep.StatusMore, Summary: "started", Findings: []sweep.Finding{{Issue: "one", Disposition: sweep.DispositionFiled}}}},
		{result: complete("finished", sweep.Finding{Issue: "two", Disposition: sweep.DispositionFiled})},
	}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if fired.Fired[0].Turns != 2 || fired.Fired[0].Findings != 2 {
		t.Errorf("fired = %+v, want two turns and both turns' findings", fired.Fired[0])
	}
	if fired.Fired[0].Truncated {
		t.Error("a pass that finished inside its bound is reported as truncated")
	}
}

// A pass that still has more to do when its turn bound runs out says so. A
// truncated pass and a finished one produce the same short report, and nothing
// else distinguishes them.
func TestAPassTruncatedByItsBoundSaysSo(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	more := func() *sweep.Result {
		return &sweep.Result{Status: sweep.StatusMore, Summary: "still going"}
	}
	role := &wokenRole{answers: []orchestrator2Answer{{result: more()}, {result: more()}, {result: more()}, {result: more()}}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if fired.Fired[0].Turns != 3 {
		t.Errorf("turns = %d, want the configured bound of 3", fired.Fired[0].Turns)
	}
	if !fired.Fired[0].Truncated {
		t.Error("a pass stopped by its turn bound is not reported as truncated")
	}
	if !strings.Contains(fired.Fired[0].Problem, "still had more to do") {
		t.Errorf("problem = %q, want it to say the bound ended the pass", fired.Fired[0].Problem)
	}
}

// A pass that fixed something and filed nothing for its root cause is a silent
// repair, and the whole reason for these reports is that a week of them shows
// whether repairs are silent.
func TestASilentRepairIsCountedOnTheFiring(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []orchestrator2Answer{{result: complete("fixed it",
		sweep.Finding{Issue: "a stuck delivery", Disposition: sweep.DispositionFixed})}}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if fired.Fired[0].SilentRepairs != 1 {
		t.Errorf("silent repairs = %d, want 1", fired.Fired[0].SilentRepairs)
	}
	if !strings.Contains(RecurringSweep{Fired: fired.Fired}.Render(), "filed nothing for their root cause") {
		t.Errorf("the rendered pass does not say a fix filed nothing:\n%s", RecurringSweep{Fired: fired.Fired}.Render())
	}
}

// A task fires once per cadence however many passes walk past it, which is the
// whole of what the claim is for.
func TestATaskFiresOncePerCadence(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []orchestrator2Answer{{result: complete("nothing")}, {result: complete("nothing")}}}
	clock := &movingRecurringClock{now: recurringNow}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: clock}

	if _, err := trigger.Fire(context.Background()); err != nil {
		t.Fatalf("first Fire() error = %v", err)
	}
	clock.now = recurringNow.Add(10 * time.Minute)
	second, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("second Fire() error = %v", err)
	}
	if len(second.Fired) != 0 {
		t.Errorf("fired = %+v, want nothing from a pass whose task is not due", second.Fired)
	}
	clock.now = recurringNow.Add(time.Hour)
	third, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("third Fire() error = %v", err)
	}
	if len(third.Fired) != 1 {
		t.Errorf("fired = %+v, want the task fired once its cadence passed", third.Fired)
	}
}

func TestADisabledTaskNeverFires(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	tasks := hourlyTask("sweep")
	task := tasks["a-sweep"]
	task.Enabled = false
	tasks["a-sweep"] = task
	role := &wokenRole{}
	trigger := Trigger{Tasks: tasks, Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 0 || len(role.messages) != 0 {
		t.Errorf("fired = %+v, messages = %v, want a disabled task left alone", fired.Fired, role.messages)
	}
}

// A firing is a provider invocation, so the operator's pause covers it exactly as
// it covers a run, a turn, and a delivery. Nothing is claimed, so every task
// keeps its cadence.
func TestAPausedHarnessFiresNothing(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{}
	trigger := Trigger{
		Tasks:   hourlyTask("sweep"),
		Claims:  store,
		Reports: store,
		Roles:   role,
		Holds:   pausedHolds{hold: runstate.OperatorHold{HeldAt: recurringNow}, held: true},
		Clock:   recurringClock{},
	}
	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if fired.Paused == nil {
		t.Fatal("a paused harness did not report the pause")
	}
	if len(role.messages) != 0 {
		t.Errorf("messages = %v, want nothing said to the role", role.messages)
	}
	if _, found, err := store.Find("a-sweep"); err != nil || found {
		t.Errorf("Find() = %v, %v, want no claim taken under a pause", found, err)
	}
}

// A conversation that could never be opened asked the role nothing. The record
// says so, in words that distinguish it from a pass that ran and found nothing.
func TestAnUnreachableRoleIsRecordedRatherThanLost(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{failure: ErrRoleUnreachable}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 1 || fired.Fired[0].Turns != 0 {
		t.Fatalf("fired = %+v, want a firing that took no turn", fired.Fired)
	}
	if !strings.Contains(fired.Fired[0].Problem, "could not be put to the") {
		t.Errorf("problem = %q, want it to say the role was never asked", fired.Fired[0].Problem)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Result != nil || recorded[0].Problem == "" {
		t.Errorf("recorded = %+v, want a durable record of the firing that produced nothing", recorded)
	}
}

// A turn that answered in prose without a block is not a failed turn: the role
// answered, and what is lost is the structure. It is said out loud rather than
// recorded as a pass that found nothing.
func TestAnAnswerWithoutAnAccountIsSaidOutLoud(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	role := &wokenRole{answers: []orchestrator2Answer{{}}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if fired.Fired[0].Turns != 1 {
		t.Errorf("turns = %d, want the turn that answered", fired.Fired[0].Turns)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Result != nil || recorded[0].Problem == "" {
		t.Errorf("recorded = %+v, want a record saying no account came back", recorded)
	}
}

// One firing per pass, whatever else is due. A pass that fired three tasks would
// hold the queue closed for as long as all three took.
func TestOnlyOneTaskFiresPerPass(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	tasks := hourlyTask("sweep")
	tasks["b-sweep"] = tasks["a-sweep"]
	role := &wokenRole{answers: []orchestrator2Answer{{result: complete("nothing")}, {result: complete("nothing")}}}
	trigger := Trigger{Tasks: tasks, Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if len(fired.Fired) != 1 {
		t.Fatalf("fired = %+v, want one task per pass", fired.Fired)
	}
	if fired.Fired[0].Task != "a-sweep" {
		t.Errorf("task = %q, want the first in name order", fired.Fired[0].Task)
	}
	// The one it passed over is the next pass's, and it is still due.
	second, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("second Fire() error = %v", err)
	}
	if len(second.Fired) != 1 || second.Fired[0].Task != "b-sweep" {
		t.Errorf("fired = %+v, want the other task on the next pass", second.Fired)
	}
}

func TestATriggerWithNothingToRecordWithIsRefused(t *testing.T) {
	t.Parallel()

	if _, err := (Trigger{Tasks: hourlyTask("sweep")}).Fire(context.Background()); err == nil {
		t.Fatal("a trigger with no claims, reports, or roles fired")
	}
}

type recurringClock struct{}

func (recurringClock) Now() time.Time { return recurringNow }

type movingRecurringClock struct{ now time.Time }

func (c *movingRecurringClock) Now() time.Time { return c.now }

// The case iteration exists for, end to end: a firing that takes every one of its
// turns and reports the per-turn maximum on each of them still lands a durable
// report. It is here rather than only in the sweep package because the defect it
// guards was in the seam — per-turn bounds applied to a merged account — and what
// it cost was the whole report of the busiest passes.
func TestAFiringAtEveryTurnsMaximumStillLandsItsReport(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	tasks := hourlyTask("sweep")
	task := tasks["a-sweep"]
	task.MaxTurns = config.MaxRecurringTurns
	tasks["a-sweep"] = task

	crowded := func(status sweep.Status) *sweep.Result {
		result := &sweep.Result{Status: status, Summary: "a heavy pass"}
		for i := 0; i < sweep.MaxFindings; i++ {
			result.Findings = append(result.Findings, sweep.Finding{
				Issue:       "a thing that was found",
				Disposition: sweep.DispositionFiled,
			})
		}
		for i := 0; i < sweep.MaxQuestions; i++ {
			result.Questions = append(result.Questions, "something only a person can settle")
		}
		return result
	}
	var answers []orchestrator2Answer
	for turn := 0; turn < task.MaxTurns-1; turn++ {
		answers = append(answers, orchestrator2Answer{result: crowded(sweep.StatusMore)})
	}
	answers = append(answers, orchestrator2Answer{result: crowded(sweep.StatusComplete)})
	role := &wokenRole{answers: answers}
	trigger := Trigger{Tasks: tasks, Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if fired.Fired[0].Turns != task.MaxTurns {
		t.Errorf("turns = %d, want every one of the %d the task allows", fired.Fired[0].Turns, task.MaxTurns)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded = %+v, want the firing's durable report", recorded)
	}
	if recorded[0].Result == nil {
		t.Fatalf("the heaviest pass recorded no account: %s", recorded[0].Problem)
	}
	if got := len(recorded[0].Result.Findings); got != task.MaxTurns*sweep.MaxFindings {
		t.Errorf("findings = %d, want every turn's %d", got, task.MaxTurns*sweep.MaxFindings)
	}
	if fired.Fired[0].Problem != "" {
		t.Errorf("problem = %q, want a heavy pass recorded without complaint", fired.Fired[0].Problem)
	}
}

// The turn bound a task may configure has to fit inside the number of turns one
// account can fold together, or the configuration permits a firing whose own
// findings the merge would start dropping.
func TestTheConfigurableTurnBoundFitsTheMergeBound(t *testing.T) {
	t.Parallel()

	if config.MaxRecurringTurns > sweep.MaxMergedTurns {
		t.Fatalf("a task may configure %d turns and an account folds %d together, so the extra turns' findings would be dropped",
			config.MaxRecurringTurns, sweep.MaxMergedTurns)
	}
}

// Every problem sentence ends with a provider's error message, whose length
// nothing here controls. A record refused because those ran long would lose the
// pass over the description of a smaller failure.
func TestLongFailureMessagesDoNotCostTheRecord(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	tasks := hourlyTask("sweep")
	task := tasks["a-sweep"]
	task.MaxTurns = config.MaxRecurringTurns
	tasks["a-sweep"] = task
	role := &wokenRole{answers: []orchestrator2Answer{{
		result: &sweep.Result{Status: sweep.StatusMore, Summary: "started"},
	}, {
		err: errors.New(strings.Repeat("the provider said something very long. ", 400)),
	}}}
	trigger := Trigger{Tasks: tasks, Claims: store, Reports: store, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if strings.Contains(fired.Fired[0].Problem, "reaches nobody") {
		t.Errorf("problem = %q, want the record kept despite the long failure", fired.Fired[0].Problem)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded = %+v, want the firing's durable report", recorded)
	}
	if len(recorded[0].Problem) > runstate.MaxSweepTextBytes {
		t.Errorf("problem is %d bytes, and the record's bound is %d", len(recorded[0].Problem), runstate.MaxSweepTextBytes)
	}
	if recorded[0].Result == nil {
		t.Error("the turn that did answer left no account on the record")
	}
}

// The bounds above are meant to make the first write always succeed, and this is
// what happens when something makes it fail anyway: the firing is recorded
// without its account rather than not at all, because a pass that spent turns and
// left nothing behind is indistinguishable from one that never happened.
func TestARefusedRecordStillLeavesTheFiringBehind(t *testing.T) {
	t.Parallel()

	store := sweepStore(t)
	reports := &refusingReports{store: store}
	role := &wokenRole{answers: []orchestrator2Answer{{result: complete("found one thing",
		sweep.Finding{Issue: "a dead claim", Disposition: sweep.DispositionFixed})}}}
	trigger := Trigger{Tasks: hourlyTask("sweep"), Claims: store, Reports: reports, Roles: role, Clock: recurringClock{}}

	fired, err := trigger.Fire(context.Background())
	if err != nil {
		t.Fatalf("Fire() error = %v", err)
	}
	if !strings.Contains(fired.Fired[0].Problem, "would not store") {
		t.Errorf("problem = %q, want it to say the account would not store", fired.Fired[0].Problem)
	}
	recorded, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded = %+v, want the firing recorded without its account", recorded)
	}
	if recorded[0].Result != nil {
		t.Errorf("recorded = %+v, want the account left off the second attempt", recorded[0])
	}
	if recorded[0].Turns != 1 || recorded[0].Task != "a-sweep" {
		t.Errorf("recorded = %+v, want what the harness itself knows about the firing", recorded[0])
	}
}

// refusingReports refuses any record carrying an account, and takes one without.
// It stands for whatever would make a full record unwritable.
type refusingReports struct{ store *runstate.SweepStore }

func (r *refusingReports) Append(recorded runstate.Sweep) error {
	if recorded.Result != nil {
		return errors.New("this record carries an account and will not store")
	}
	return r.store.Append(recorded)
}
