package exchange

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The three properties this slice exists to prove are properties of the record,
// so they are proved against records rather than against a running loop. What
// acts on the plan is tested where it acts.

// A round is on disk before the provider is invoked, so a process that dies
// between the two leaves a round asked and never answered. This is the whole of
// how a restart tells that from a round that came back — and the lease is what
// tells it from a round being taken right now, which is why the same record
// yields a reclaim when it is free and nothing at all when somebody holds it.
func TestARoundNobodyAnsweredIsReclaimedOnlyWhenNobodyIsCarryingIt(t *testing.T) {
	t.Parallel()

	interrupted := open("exchange-"+strings.Repeat("a", 32), 3)
	interrupted.Rounds = []Round{asked(1, "yoyo pid 4242")}

	free := Survey(State{Exchanges: []Exchange{interrupted}})
	if len(free.Reclaim) != 1 {
		t.Fatalf("Reclaim = %#v, want the interrupted round reclaimed", free.Reclaim)
	}
	if free.Reclaim[0].Round != 1 || free.Reclaim[0].Holder != "yoyo pid 4242" {
		t.Errorf("Reclaim[0] = %#v, want round 1 and the process that was carrying it", free.Reclaim[0])
	}
	if !strings.Contains(free.Reclaim[0].Because, "yoyo pid 4242") {
		t.Errorf("Because = %q, want the holder named in it", free.Reclaim[0].Because)
	}
	// The round was spent, so the delivery this pass may make is the next one.
	if len(free.Deliver) != 1 || free.Deliver[0].Round != 2 || !free.Deliver[0].Reclaimed {
		t.Errorf("Deliver = %#v, want round 2 marked as following a reclaim", free.Deliver)
	}

	carried := Survey(State{
		Exchanges: []Exchange{interrupted},
		Carried:   map[string]bool{interrupted.ID: true},
	})
	if len(carried.Reclaim) != 0 || len(carried.Deliver) != 0 {
		t.Fatalf("plan = %#v, want an exchange a live process holds left entirely alone", carried)
	}
	if len(carried.Carried) != 1 || carried.Carried[0] != interrupted.ID {
		t.Errorf("Carried = %#v, want the held exchange named", carried.Carried)
	}
}

// A round the provider failed is not an interrupted one. It records why it
// produced nothing, which is exactly what a reclaim would have written, and
// reclaiming it again would overwrite the real reason with a guess.
func TestARoundThatFailedIsNotMistakenForOneNobodyFinished(t *testing.T) {
	t.Parallel()

	failed := open("exchange-"+strings.Repeat("b", 32), 3)
	failed.Rounds = []Round{answered(1, "", "the answering role said nothing")}

	plan := Survey(State{Exchanges: []Exchange{failed}})
	if len(plan.Reclaim) != 0 {
		t.Fatalf("Reclaim = %#v, want a round that already says why it produced nothing left alone", plan.Reclaim)
	}
	if len(plan.Deliver) != 1 || plan.Deliver[0].Reclaimed {
		t.Errorf("Deliver = %#v, want an ordinary next round", plan.Deliver)
	}
}

// A thread that has spent every round it was opened with is closed by the
// conductor the moment somebody asks past the cap. Nothing closes the one nobody
// asked again — which is the thread whose last round died — so it would sit open
// for ever with the operator never told.
func TestAThreadThatRanOutOfRoundsIsEndedEvenThoughNobodyAskedAgain(t *testing.T) {
	t.Parallel()

	spent := open("exchange-"+strings.Repeat("c", 32), 2)
	spent.Rounds = []Round{answered(1, "one", ""), answered(2, "two", "")}

	plan := Survey(State{Exchanges: []Exchange{spent}})
	if len(plan.Exhaust) != 1 || plan.Exhaust[0].Rounds != 2 || plan.Exhaust[0].Cap != 2 {
		t.Fatalf("Exhaust = %#v, want the spent thread ended", plan.Exhaust)
	}
	if len(plan.Deliver) != 0 {
		t.Errorf("Deliver = %#v, want nothing delivered on a thread with no rounds left", plan.Deliver)
	}
}

// A thread whose last round died on its cap is both things at once. Reclaiming
// without ending it would leave it open for ever; ending it without reclaiming
// would close a record whose last round still reads as a question somebody is
// working on. One pass does both.
func TestAThreadWhoseLastRoundDiedOnItsCapIsReclaimedAndThenEnded(t *testing.T) {
	t.Parallel()

	dying := open("exchange-"+strings.Repeat("d", 32), 2)
	dying.Rounds = []Round{answered(1, "one", ""), asked(2, "yoyo pid 99")}

	plan := Survey(State{Exchanges: []Exchange{dying}})
	if len(plan.Reclaim) != 1 || plan.Reclaim[0].Round != 2 {
		t.Fatalf("Reclaim = %#v, want the dead round reclaimed", plan.Reclaim)
	}
	if len(plan.Exhaust) != 1 || plan.Exhaust[0].Rounds != 2 {
		t.Fatalf("Exhaust = %#v, want the same thread ended in the same pass", plan.Exhaust)
	}
	if len(plan.Deliver) != 0 {
		t.Errorf("Deliver = %#v, want nothing delivered on a thread being ended", plan.Deliver)
	}
}

// The bound is on how many exchanges have a round open at once across the whole
// product, and an exchange a live process is carrying is already one of them. So
// the room a pass has is what the bound allows less what is already in flight,
// and everything past it is queued rather than refused.
func TestTheInFlightBoundCountsWhatIsAlreadyCarriedAndQueuesTheRest(t *testing.T) {
	t.Parallel()

	var exchanges []Exchange
	for i := 0; i < 5; i++ {
		exchanges = append(exchanges, open(fmt.Sprintf("exchange-%032x", i), 4))
	}
	plan := Survey(State{
		Exchanges: exchanges,
		Carried:   map[string]bool{exchanges[0].ID: true},
		Bounds:    Bounds{InFlight: 3},
	})
	if len(plan.Deliver) != 2 {
		t.Fatalf("Deliver = %#v, want the two the bound has room for beside the one already carried", plan.Deliver)
	}
	if len(plan.Queued) != 2 {
		t.Fatalf("Queued = %#v, want the rest queued rather than refused", plan.Queued)
	}
	for _, queued := range plan.Queued {
		if !strings.Contains(queued.Because, "already have a round open") {
			t.Errorf("Because = %q, want the bound named as the reason", queued.Because)
		}
	}
	// Queued is a state of this pass and not of the exchange: nothing about it is
	// written down, and the next pass with room takes it.
	roomier := Survey(State{Exchanges: exchanges, Bounds: Bounds{InFlight: 9}})
	if len(roomier.Deliver) != 5 || len(roomier.Queued) != 0 {
		t.Errorf("plan = %#v, want every exchange deliverable once there is room", roomier)
	}
}

// A bound nobody set is the default rather than no bound at all. A zero here
// would be a product on which nothing may ever be asked, which is not what
// leaving the field out means.
func TestABoundNobodySetIsTheDefaultRatherThanNone(t *testing.T) {
	t.Parallel()

	var exchanges []Exchange
	for i := 0; i < DefaultInFlight+2; i++ {
		exchanges = append(exchanges, open(fmt.Sprintf("exchange-%032x", i), 4))
	}
	plan := Survey(State{Exchanges: exchanges})
	if plan.InFlight != DefaultInFlight {
		t.Fatalf("InFlight = %d, want the default %d", plan.InFlight, DefaultInFlight)
	}
	if len(plan.Deliver) != DefaultInFlight || len(plan.Queued) != 2 {
		t.Errorf("plan = %#v, want the default bound applied", plan)
	}
}

// A question asked against a goal that has since been amended may no longer be
// the question. What that produces is a reason to tell the role that asked it,
// and nothing else: the exchange is not stopped, not closed, and not held back
// from its next round.
func TestAQuestionAskedAgainstSomethingThatMovedTellsTheAskerAndStopsNothing(t *testing.T) {
	t.Parallel()

	asking := open("exchange-"+strings.Repeat("e", 32), 4)
	asking.Asker = Party{Role: domain.RoleProductManager}
	asking.Answerer = Party{Role: domain.RoleArchitect}
	asking.Refers = []Reference{
		{What: "goal", ID: "autonomy", Revision: "rev-1"},
		{What: "artifact", ID: "brief", Revision: "rev-7"},
	}

	plan := Survey(State{
		Exchanges: []Exchange{asking},
		Revisions: map[string]string{"goal/autonomy": "rev-2", "artifact/brief": "rev-7"},
	})
	if len(plan.Stale) != 1 {
		t.Fatalf("Stale = %#v, want the moved reference reported", plan.Stale)
	}
	stale := plan.Stale[0]
	if stale.Tell != domain.RoleProductManager {
		t.Errorf("Tell = %q, want the role that asked the question", stale.Tell)
	}
	if len(stale.Moved) != 1 || stale.Moved[0].Reference.ID != "autonomy" || stale.Moved[0].Now != "rev-2" {
		t.Errorf("Moved = %#v, want only the goal, at what it is now", stale.Moved)
	}
	if len(stale.Unjudged) != 0 {
		t.Errorf("Unjudged = %#v, want nothing unjudged when every revision is known", stale.Unjudged)
	}
	// Staleness is advisory in the strongest sense: it is said, and the exchange
	// carries on exactly as it would have.
	if len(plan.Deliver) != 1 || plan.Deliver[0].ExchangeID != asking.ID {
		t.Errorf("Deliver = %#v, want a stale exchange still deliverable", plan.Deliver)
	}
}

// Silence is not evidence that something held still. A reference the survey
// knows nothing current about is named as unjudged rather than counted as
// unmoved, because a plan that reported the first as the second would be a
// reassurance nobody could check.
func TestAReferenceNothingCurrentIsKnownAboutIsNamedRatherThanCalledUnmoved(t *testing.T) {
	t.Parallel()

	asking := open("exchange-"+strings.Repeat("f", 32), 4)
	asking.Refers = []Reference{{What: "goal", ID: "autonomy", Revision: "rev-1"}}

	unasked := Survey(State{Exchanges: []Exchange{asking}})
	if len(unasked.Stale) != 1 || len(unasked.Stale[0].Unjudged) != 1 || len(unasked.Stale[0].Moved) != 0 {
		t.Fatalf("Stale = %#v, want the reference reported as unjudged", unasked.Stale)
	}

	unmoved := Survey(State{
		Exchanges: []Exchange{asking},
		Revisions: map[string]string{"goal/autonomy": "rev-1"},
	})
	if len(unmoved.Stale) != 0 {
		t.Errorf("Stale = %#v, want nothing said about a reference that has not moved", unmoved.Stale)
	}
}

// A closed exchange is read and skipped rather than left out, so a caller need
// not filter before it asks and a thread that ended is never asked, reclaimed,
// or ended a second time.
func TestAClosedExchangeIsNothingThisPassDoes(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	closed := open("exchange-"+strings.Repeat("1", 32), 2)
	closed.Rounds = []Round{answered(1, "one", ""), answered(2, "two", "")}
	closed.Outcome = OutcomeResolved
	closed.Settled = "what it came to"
	closed.ClosedAt = &at
	closed.Refers = []Reference{{What: "goal", ID: "autonomy", Revision: "rev-1"}}

	plan := Survey(State{
		Exchanges: []Exchange{closed},
		Revisions: map[string]string{"goal/autonomy": "rev-9"},
	})
	if len(plan.Reclaim)+len(plan.Exhaust)+len(plan.Deliver)+len(plan.Queued)+len(plan.Stale)+len(plan.Carried) != 0 {
		t.Fatalf("plan = %#v, want nothing at all for a thread that ended", plan)
	}
}

// A record with an unanswered round behind a later one is two processes having
// written over each other, and reclaiming against it would be exactly the double
// delivery the lease exists to prevent. It is refused at the store rather than
// surveyed.
func TestARecordWithAnUnansweredRoundBehindALaterOneIsRefused(t *testing.T) {
	t.Parallel()

	overwritten := open("exchange-"+strings.Repeat("2", 32), 3)
	overwritten.Rounds = []Round{asked(1, "yoyo pid 1"), asked(2, "yoyo pid 2")}

	err := overwritten.Validate()
	if err == nil || !strings.Contains(err.Error(), "a later round was taken behind it") {
		t.Fatalf("Validate() = %v, want the overwritten round refused", err)
	}
}

func open(id string, cap int) Exchange {
	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	return Exchange{
		SchemaVersion: SchemaVersion,
		ID:            id,
		ProductID:     "yoyodyne",
		Asker:         Party{Role: domain.RoleProductManager},
		Answerer:      Party{Role: domain.RoleArchitect},
		Question:      "what am I missing?",
		MaxRounds:     cap,
		OpenedAt:      at,
		UpdatedAt:     at,
	}
}

func asked(number int, holder string) Round {
	return Round{
		Number:   number,
		Question: "what am I missing?",
		Holder:   holder,
		AskedAt:  time.Date(2026, 9, 5, 9, number, 0, 0, time.UTC),
	}
}

func answered(number int, answer, problem string) Round {
	at := time.Date(2026, 9, 5, 9, number, 30, 0, time.UTC)
	round := asked(number, "yoyo pid 1")
	round.Answer = answer
	round.Problem = problem
	round.AnsweredAt = &at
	return round
}
