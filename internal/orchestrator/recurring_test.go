package orchestrator

import (
	"context"
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
	recorded, err := store.List()
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
	recorded, err := store.List()
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
	recorded, err := store.List()
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
