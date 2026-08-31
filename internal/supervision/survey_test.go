package supervision

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Supervised restart, the first half: a process died carrying a request, so the
// attempt it opened is held by nobody. The next process delivers it again, says
// so, and spends the next attempt rather than the same one.
func TestARestartDeliversARequestNobodyIsCarrying(t *testing.T) {
	t.Parallel()

	interrupted := testRequest(1)
	interrupted.Attempts = []Attempt{testOpenAttempt(1, "harness-a", interrupted.OpenedAt)}
	mustValidate(t, interrupted)

	// A restart holds nothing, which is the whole of what makes it a restart.
	plan := Survey(State{Requests: []Request{interrupted}})

	if len(plan.Deliver) != 1 {
		t.Fatalf("Deliver = %#v, want the interrupted request delivered again", plan.Deliver)
	}
	delivery := plan.Deliver[0]
	if delivery.RequestID != interrupted.ID || !delivery.Reclaimed || delivery.Attempt != 2 {
		t.Fatalf("delivery = %+v, want attempt 2 of the interrupted request, marked reclaimed", delivery)
	}
	if !strings.Contains(delivery.Because, "harness-a") {
		t.Fatalf("because = %q, want the process that was carrying it named", delivery.Because)
	}
	if len(plan.Settle) != 0 {
		t.Fatalf("Settle = %#v, want nothing settled", plan.Settle)
	}
}

// Supervised restart, the second half and the one that matters: the process died
// between recording the answer and writing the ending, so the request looks
// unfinished and has already been paid for. It settles rather than being asked
// again.
func TestARestartNeverAsksAgainForAnAnswerAlreadyRecorded(t *testing.T) {
	t.Parallel()

	paidFor := testRequest(1)
	paidFor.Attempts = []Attempt{testOpenAttempt(1, "harness-a", paidFor.OpenedAt)}
	paidFor.Response = testAnswer(1, paidFor.OpenedAt.Add(time.Minute))
	mustValidate(t, paidFor)

	plan := Survey(State{Requests: []Request{paidFor}})

	if len(plan.Deliver) != 0 {
		t.Fatalf("Deliver = %#v, want an answered request never delivered again", plan.Deliver)
	}
	if len(plan.Settle) != 1 || plan.Settle[0].Outcome != OutcomeAnswered {
		t.Fatalf("Settle = %#v, want the recorded answer settled", plan.Settle)
	}
	if plan.Settle[0].Escalate {
		t.Fatalf("settlement = %+v, want an ordinary ending rather than one the operator hears about", plan.Settle[0])
	}
}

// A request some live process is carrying is that process's business. It is
// neither delivered nor reported as waiting, because it is not waiting.
func TestARequestSomebodyIsCarryingIsLeftAlone(t *testing.T) {
	t.Parallel()

	carried := testRequest(1)
	carried.Attempts = []Attempt{testOpenAttempt(1, "harness-a", carried.OpenedAt)}
	mustValidate(t, carried)

	plan := Survey(State{
		Requests: []Request{carried},
		Held:     map[string]bool{carried.ID: true},
	})
	if plan.Anything() {
		t.Fatalf("plan = %#v, want nothing to do about a request already being carried", plan)
	}
}

// The lease decides who is carrying a request, not the attempt record. A
// harness takes the lease and then writes the attempt, so a request held with
// nothing recorded against it yet is one somebody is about to deliver —
// delivering it here on the evidence that no attempt is written is the double
// delivery the lease exists to prevent.
func TestALeaseHeldBeforeTheAttemptIsWrittenStillHoldsTheRequest(t *testing.T) {
	t.Parallel()

	taken, waiting := testRequest(1), testRequest(2)
	waiting.Topic = "chat-two"
	mustValidate(t, taken)

	plan := Survey(State{
		Requests: []Request{taken, waiting},
		Held:     map[string]bool{taken.ID: true},
		Bounds:   Bounds{InFlight: 1},
	})
	if got := deliveredIDs(plan); len(got) != 0 {
		t.Fatalf("Deliver = %v, want nothing delivered against a lease somebody holds", got)
	}
	if len(plan.Queued) != 1 || plan.Queued[0].RequestID != waiting.ID {
		t.Fatalf("Queued = %#v, want the held request counted against the bound", plan.Queued)
	}
}

// Bounded concurrency, the part that is not configurable: one durable
// conversation takes its requests one at a time, because two deliveries
// interleaved against one transcript is a corrupted transcript.
func TestOneTopicTakesItsRequestsOneAtATime(t *testing.T) {
	t.Parallel()

	first, second := testRequest(1), testRequest(2)
	second.To = domain.RoleDevelopmentManager

	plan := Survey(State{Requests: []Request{second, first}})

	if got := deliveredIDs(plan); len(got) != 1 || got[0] != first.ID {
		t.Fatalf("Deliver = %v, want only the older request on the topic", got)
	}
	if len(plan.Queued) != 1 || plan.Queued[0].RequestID != second.ID || plan.Queued[0].Behind != first.ID {
		t.Fatalf("Queued = %#v, want the newer request queued behind the older", plan.Queued)
	}
}

// Different durable conversations progress independently: serializing one topic
// is not serializing the loop.
func TestDifferentTopicsProceedIndependently(t *testing.T) {
	t.Parallel()

	first, second := testRequest(1), testRequest(2)
	second.Topic = "chat-two"

	plan := Survey(State{Requests: []Request{first, second}})
	if got := deliveredIDs(plan); len(got) != 2 {
		t.Fatalf("Deliver = %v, want both topics delivered", got)
	}
	if len(plan.Queued) != 0 {
		t.Fatalf("Queued = %#v, want nothing waiting", plan.Queued)
	}
}

// Bounded concurrency, the part that is: however many topics are open, the
// product delivers only so many at once, and what waits says what it waits for.
func TestTheProductDeliversOnlySoManyAtOnce(t *testing.T) {
	t.Parallel()

	first, second, third := testRequest(1), testRequest(2), testRequest(3)
	second.Topic, third.Topic = "chat-two", "chat-three"

	plan := Survey(State{
		Requests: []Request{first, second, third},
		Bounds:   Bounds{InFlight: 2},
	})

	if got := deliveredIDs(plan); len(got) != 2 || got[0] != first.ID || got[1] != second.ID {
		t.Fatalf("Deliver = %v, want the two oldest", got)
	}
	if len(plan.Queued) != 1 || plan.Queued[0].RequestID != third.ID || plan.Queued[0].Behind != "" {
		t.Fatalf("Queued = %#v, want the third waiting on the bound rather than on a topic", plan.Queued)
	}
	if !strings.Contains(plan.Queued[0].Because, "bound") {
		t.Fatalf("because = %q, want the bound named", plan.Queued[0].Because)
	}
}

// What another process is already carrying counts against the bound, so two
// harnesses reading the same records do not between them open twice as many
// deliveries as either is allowed.
func TestDeliveriesAlreadyOpenCountAgainstTheBound(t *testing.T) {
	t.Parallel()

	carried, waiting := testRequest(1), testRequest(2)
	carried.Attempts = []Attempt{testOpenAttempt(1, "harness-a", carried.OpenedAt)}
	waiting.Topic = "chat-two"

	plan := Survey(State{
		Requests: []Request{carried, waiting},
		Held:     map[string]bool{carried.ID: true},
		Bounds:   Bounds{InFlight: 1},
	})
	if len(plan.Deliver) != 0 {
		t.Fatalf("Deliver = %#v, want the bound already taken", plan.Deliver)
	}
	if len(plan.Queued) != 1 || plan.Queued[0].RequestID != waiting.ID {
		t.Fatalf("Queued = %#v, want the second request waiting", plan.Queued)
	}
}

// Bounded coordination cycles: a request nothing answers ends rather than being
// retried for ever, and the ending is one the operator hears about. That is what
// stops two roles deferring to each other politely at the operator's expense.
func TestARequestThatRanOutOfAttemptsEndsAndIsEscalated(t *testing.T) {
	t.Parallel()

	spent := testRequest(1)
	spent.CycleLimit = 2
	spent.Attempts = []Attempt{
		testFinishedAttempt(1, "harness-a", spent.OpenedAt, "the provider refused"),
		testFinishedAttempt(2, "harness-a", spent.OpenedAt.Add(time.Minute), "the provider refused"),
	}
	mustValidate(t, spent)

	plan := Survey(State{Requests: []Request{spent}})
	if len(plan.Deliver) != 0 {
		t.Fatalf("Deliver = %#v, want nothing delivered past the limit", plan.Deliver)
	}
	if len(plan.Settle) != 1 || plan.Settle[0].Outcome != OutcomeUnresolved || !plan.Settle[0].Escalate {
		t.Fatalf("Settle = %#v, want it ended unresolved and escalated", plan.Settle)
	}
	if !strings.Contains(plan.Settle[0].Because, "2 of 2") {
		t.Fatalf("because = %q, want what was spent against what was allowed", plan.Settle[0].Because)
	}
}

// Degraded state is explicit rather than silent: a request whose carrier died
// with its last attempt spent will never be delivered again and was never
// answered, and that is said out loud beside the ending it gets.
func TestARequestNothingWillFinishIsNamed(t *testing.T) {
	t.Parallel()

	abandoned := testRequest(1)
	abandoned.CycleLimit = 1
	abandoned.Attempts = []Attempt{testOpenAttempt(1, "harness-a", abandoned.OpenedAt)}
	mustValidate(t, abandoned)

	plan := Survey(State{Requests: []Request{abandoned}})
	if len(plan.Settle) != 1 || plan.Settle[0].Outcome != OutcomeUnresolved {
		t.Fatalf("Settle = %#v, want it ended unresolved", plan.Settle)
	}
	if len(plan.Degraded) != 1 || plan.Degraded[0].RequestID != abandoned.ID {
		t.Fatalf("Degraded = %#v, want the abandoned request named", plan.Degraded)
	}
	if !strings.Contains(plan.Degraded[0].Because, "harness-a") {
		t.Fatalf("because = %q, want the process that is gone named", plan.Degraded[0].Because)
	}
}

// Staleness handling, and the whole of what advisory means: a judgment made
// against something that has moved wakes the role that owns it, and holds up
// nothing at all. The work it judged is delivered in the same reading.
func TestAStaleJudgmentWakesItsOwnerAndHoldsUpNothing(t *testing.T) {
	t.Parallel()

	judgment := testReadiness(1, JudgmentArchitecture)
	judgment.Disposition = DispositionCrossCutting
	judgment.Against = []Reference{{What: "artifact", ID: "v1-goals", Revision: "r7"}}

	pending := testRequest(1)
	pending.Refers = []Reference{{What: "work-item", ID: judgment.Item, Revision: "w3"}}

	plan := Survey(State{
		Requests:  []Request{pending},
		Readiness: []Readiness{judgment},
		Revisions: map[string]string{
			"artifact/v1-goals":          "r8",
			"work-item/" + judgment.Item: "w3",
		},
	})

	if len(plan.Wake) != 1 {
		t.Fatalf("Wake = %#v, want the architect woken", plan.Wake)
	}
	woken := plan.Wake[0]
	if woken.Role != domain.RoleArchitect || woken.Judgment != JudgmentArchitecture || woken.Item != judgment.Item {
		t.Fatalf("wakeup = %+v, want the architect woken for their own judgment", woken)
	}
	if len(woken.Moved) != 1 || woken.Moved[0].Now != "r8" {
		t.Fatalf("moved = %#v, want the goals named at what they are now", woken.Moved)
	}
	if !strings.Contains(woken.Because, "not held") {
		t.Fatalf("because = %q, want it to say the item is not held", woken.Because)
	}
	// The advisory property, stated as the test that would fail if readiness ever
	// quietly became a gate: a cross-cutting judgment, stale, and the work it
	// judged still goes out in this same reading.
	if got := deliveredIDs(plan); len(got) != 1 || got[0] != pending.ID {
		t.Fatalf("Deliver = %v, want the work delivered despite the stale judgment", got)
	}
}

// A judgment nothing current is known about is reported as one nothing could be
// said about, rather than guessed either way.
func TestAJudgmentNothingCanBeComparedAgainstIsNamedRatherThanGuessed(t *testing.T) {
	t.Parallel()

	judgment := testReadiness(1, JudgmentDelivery)
	judgment.Against = []Reference{{What: "artifact", ID: "v1-goals", Revision: "r7"}}

	plan := Survey(State{
		Readiness: []Readiness{judgment},
		Revisions: map[string]string{"artifact/something-else": "r1"},
	})
	if len(plan.Wake) != 0 {
		t.Fatalf("Wake = %#v, want nobody woken on a comparison nothing could make", plan.Wake)
	}
	if len(plan.Degraded) != 1 || plan.Degraded[0].Item != judgment.Item ||
		plan.Degraded[0].Judgment != JudgmentDelivery {
		t.Fatalf("Degraded = %#v, want the judgment named as one that could not be compared", plan.Degraded)
	}
}

// A reading given no revisions was not asked about staleness, so it reports
// none rather than reporting everything as unreadable.
func TestAReadingWithNoRevisionsJudgesNoStaleness(t *testing.T) {
	t.Parallel()

	judgment := testReadiness(1, JudgmentProduct)
	plan := Survey(State{Requests: []Request{testRequest(1)}, Readiness: []Readiness{judgment}})
	if len(plan.Wake) != 0 || len(plan.Degraded) != 0 {
		t.Fatalf("plan = %#v, want no staleness judged where nothing said what is current", plan)
	}
}

// A request written against something that has moved is still delivered — this
// is advisory — but the answer will be read against a stale premise, and that is
// said rather than left for somebody to notice.
func TestARequestWrittenAgainstSomethingThatMovedIsNamed(t *testing.T) {
	t.Parallel()

	pending := testRequest(1)
	pending.Refers = []Reference{{What: "artifact", ID: "v1-goals", Revision: "r7"}}

	plan := Survey(State{
		Requests:  []Request{pending},
		Revisions: map[string]string{"artifact/v1-goals": "r8"},
	})
	if got := deliveredIDs(plan); len(got) != 1 {
		t.Fatalf("Deliver = %v, want the request still delivered", got)
	}
	if len(plan.Degraded) != 1 || plan.Degraded[0].RequestID != pending.ID {
		t.Fatalf("Degraded = %#v, want the moved premise named", plan.Degraded)
	}
}

// A request that already ended is over. Reading one again is how a settled
// question gets asked a second time.
func TestASettledRequestIsNotReadAgain(t *testing.T) {
	t.Parallel()

	settled := testRequest(1)
	settledAt := settled.OpenedAt.Add(time.Hour)
	settled.Attempts = []Attempt{testFinishedAttempt(1, "harness-a", settled.OpenedAt, "")}
	settled.Response = testAnswer(1, settledAt)
	settled.Outcome = OutcomeAnswered
	settled.SettledAt = &settledAt
	mustValidate(t, settled)

	if plan := Survey(State{Requests: []Request{settled}}); plan.Anything() {
		t.Fatalf("plan = %#v, want nothing to do about a request that ended", plan)
	}
}

// Two readings of one store deliver in the same order, whatever order the
// records came off the disk in.
func TestTwoReadingsOfOneStoreAgree(t *testing.T) {
	t.Parallel()

	first, second, third := testRequest(1), testRequest(2), testRequest(3)
	second.Topic, third.Topic = "chat-two", "chat-three"

	forwards := Survey(State{Requests: []Request{first, second, third}, Bounds: Bounds{InFlight: 2}})
	backwards := Survey(State{Requests: []Request{third, second, first}, Bounds: Bounds{InFlight: 2}})
	if got, want := deliveredIDs(forwards), deliveredIDs(backwards); len(got) != len(want) {
		t.Fatalf("Deliver = %v and %v from the same records", got, want)
	} else {
		for index := range got {
			if got[index] != want[index] {
				t.Fatalf("Deliver = %v and %v from the same records", got, want)
			}
		}
	}
}

func deliveredIDs(plan Plan) []string {
	ids := make([]string, 0, len(plan.Deliver))
	for _, delivery := range plan.Deliver {
		ids = append(ids, delivery.RequestID)
	}
	return ids
}

func mustValidate(t *testing.T, request Request) {
	t.Helper()
	if err := request.Validate(); err != nil {
		t.Fatalf("the test built a request the contract refuses: %v", err)
	}
}
