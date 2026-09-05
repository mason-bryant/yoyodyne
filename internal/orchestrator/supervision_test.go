package orchestrator

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The pass is exercised over the real store and real operating-system leases
// rather than over fakes of either. What is being claimed here is that a process
// that died leaves a record the next process can act on, and a fake lease would
// prove that about the fake.

// A round is on disk before the answering provider is invoked, so a process
// killed in between leaves a round asked and never answered. The next pass finds
// the lease free, which is what says the carrier is gone, and closes the round
// saying so — the round stays spent, and the thread is neither delivered twice
// nor left reading as a question somebody is still working on.
func TestAPassReclaimsTheRoundAProcessDiedCarrying(t *testing.T) {
	t.Parallel()

	store, reports := supervisionStores(t)
	crashed := openExchange("exchange-"+strings.Repeat("a", 32), 3)
	crashed.Rounds = []exchange.Round{askedRound(1, "yoyo pid 4242")}
	save(t, store, crashed)

	pass, err := supervisionLoop(store, reports).Run()
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if !recorded(pass, crashed.ID, SupervisionReclaimed) {
		t.Fatalf("results = %#v, want the interrupted round reclaimed", pass.Results)
	}

	after := load(t, store, crashed.ID)
	if !after.Open() {
		t.Errorf("Outcome = %q, want the thread still open with rounds left", after.Outcome)
	}
	if after.Spent() != 1 {
		t.Errorf("Spent() = %d, want the interrupted round still spent", after.Spent())
	}
	if !strings.Contains(after.Rounds[0].Problem, "yoyo pid 4242") {
		t.Errorf("Problem = %q, want the process that was carrying it named", after.Rounds[0].Problem)
	}
	if len(reports.reports) != 0 {
		t.Errorf("reports = %#v, want nobody told about an ordinary reclaim", reports.reports)
	}

	// A second pass over the reclaimed record does nothing further. That is the
	// property a restart actually needs: the recovery is not repeated, and the
	// round is not paid for twice.
	again, err := supervisionLoop(store, reports).Run()
	if err != nil {
		t.Fatalf("second Run() = %v", err)
	}
	if recorded(again, crashed.ID, SupervisionReclaimed) {
		t.Errorf("results = %#v, want nothing reclaimed a second time", again.Results)
	}
}

// A live process holding an exchange is mid-round, and a second pass acting on
// it is exactly the double delivery the lease exists to prevent. The lease is a
// real one held by this test for the duration, so what is proved is that the
// pass asks the operating system rather than a record.
func TestAPassLeavesAnExchangeALiveProcessIsCarrying(t *testing.T) {
	t.Parallel()

	store, reports := supervisionStores(t)
	carried := openExchange("exchange-"+strings.Repeat("b", 32), 3)
	carried.Rounds = []exchange.Round{askedRound(1, "some other yoyo")}
	save(t, store, carried)

	lease, taken, err := store.Hold(carried.ID)
	if err != nil || !taken {
		t.Fatalf("Hold() = %t, %v; want the lease held by this test", taken, err)
	}
	defer lease.Release()

	pass, err := supervisionLoop(store, reports).Run()
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if !recorded(pass, carried.ID, SupervisionCarried) {
		t.Fatalf("results = %#v, want the held exchange reported as carried", pass.Results)
	}
	if recorded(pass, carried.ID, SupervisionReclaimed) {
		t.Fatalf("results = %#v, want nothing done to an exchange somebody is carrying", pass.Results)
	}
	after := load(t, store, carried.ID)
	if after.Rounds[0].AnsweredAt != nil || after.Rounds[0].Problem != "" {
		t.Errorf("Rounds[0] = %#v, want the live round untouched", after.Rounds[0])
	}
}

// A thread that spent every round it was given and was never asked again would
// sit open for ever with the operator never told — the cap only fires when
// somebody asks past it. The sweep is what closes that one, and telling the
// operator is the whole point of the ending.
func TestAPassEndsAThreadThatRanOutOfRoundsAndTellsTheOperator(t *testing.T) {
	t.Parallel()

	store, reports := supervisionStores(t)
	spent := openExchange("exchange-"+strings.Repeat("c", 32), 2)
	spent.Rounds = []exchange.Round{answeredRound(1, "one"), answeredRound(2, "two")}
	save(t, store, spent)

	pass, err := supervisionLoop(store, reports).Run()
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if !recorded(pass, spent.ID, SupervisionSettled) {
		t.Fatalf("results = %#v, want the spent thread ended", pass.Results)
	}
	after := load(t, store, spent.ID)
	if after.Outcome != exchange.OutcomeUnresolved {
		t.Errorf("Outcome = %q, want unresolved", after.Outcome)
	}
	if len(reports.reports) != 1 || reports.reports[0].Severity != report.SeverityWarning {
		t.Fatalf("reports = %#v, want the operator told once, as a warning", reports.reports)
	}
	if !strings.Contains(reports.reports[0].Message, spent.ID) {
		t.Errorf("Message = %q, want the exchange named so it can be read", reports.reports[0].Message)
	}
}

// A thread whose last round died on its cap needs both things, in that order and
// in one pass. Ending it without reclaiming would close a record whose last
// round still reads as a question somebody is working on, and the store refuses
// exactly that.
func TestAThreadThatDiedOnItsCapIsReclaimedAndEndedInOnePass(t *testing.T) {
	t.Parallel()

	store, reports := supervisionStores(t)
	dying := openExchange("exchange-"+strings.Repeat("d", 32), 2)
	dying.Rounds = []exchange.Round{answeredRound(1, "one"), askedRound(2, "yoyo pid 5")}
	save(t, store, dying)

	pass, err := supervisionLoop(store, reports).Run()
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if !recorded(pass, dying.ID, SupervisionReclaimed) || !recorded(pass, dying.ID, SupervisionSettled) {
		t.Fatalf("results = %#v, want the round reclaimed and the thread ended", pass.Results)
	}
	after := load(t, store, dying.ID)
	if after.Outcome != exchange.OutcomeUnresolved {
		t.Errorf("Outcome = %q, want unresolved", after.Outcome)
	}
	if after.Rounds[1].Problem == "" {
		t.Errorf("Rounds[1] = %#v, want the dead round saying why it produced nothing", after.Rounds[1])
	}
	if len(reports.reports) != 1 {
		t.Errorf("reports = %#v, want the operator told once", reports.reports)
	}
}

// The pass carries no voice, so nothing is put in front of a role by it. That is
// deliberate rather than unfinished: recovering from a lost process is never a
// reason to ask a question nobody asked for, and the harness is the only thing
// that invokes a role.
func TestAPassPutsNothingInFrontOfARole(t *testing.T) {
	t.Parallel()

	store, reports := supervisionStores(t)
	waiting := openExchange("exchange-"+strings.Repeat("e", 32), 3)
	waiting.Rounds = []exchange.Round{answeredRound(1, "one")}
	save(t, store, waiting)

	pass, err := supervisionLoop(store, reports).Run()
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if !recorded(pass, waiting.ID, SupervisionUndelivered) {
		t.Fatalf("results = %#v, want the deliverable round reported as not taken", pass.Results)
	}
	after := load(t, store, waiting.ID)
	if after.Spent() != 1 {
		t.Errorf("Spent() = %d, want no round taken by a pass with no voice", after.Spent())
	}
}

// The in-flight bound is across the product, and an exchange a live process is
// carrying is already one of them. What the bound produces is a plan that says
// which threads are held back and why — it is advisory, and nothing about being
// queued is written onto the exchange.
func TestTheInFlightBoundHoldsBackWhatTheProductIsAlreadyCarrying(t *testing.T) {
	t.Parallel()

	store, reports := supervisionStores(t)
	var ids []string
	for i := 0; i < 4; i++ {
		one := openExchange(fmt.Sprintf("exchange-%032x", i), 3)
		save(t, store, one)
		ids = append(ids, one.ID)
	}
	lease, taken, err := store.Hold(ids[0])
	if err != nil || !taken {
		t.Fatalf("Hold() = %t, %v", taken, err)
	}
	defer lease.Release()

	loop := supervisionLoop(store, reports)
	loop.Bounds = exchange.Bounds{InFlight: 2}
	pass, err := loop.Run()
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	var undelivered, queued int
	for _, result := range pass.Results {
		switch result.Outcome {
		case SupervisionUndelivered:
			undelivered++
		case SupervisionQueued:
			queued++
		}
	}
	if undelivered != 1 || queued != 2 {
		t.Fatalf("results = %#v, want one within the bound beside the carried one, and two queued", pass.Results)
	}
	for _, id := range ids {
		if after := load(t, store, id); !after.Open() || after.Spent() != 0 {
			t.Errorf("%s = %#v, want the bound to have written nothing", id, after)
		}
	}
}

// A question asked against something that has since moved is reported to be
// carried to the role that asked it, and stops nothing at all. That is the whole
// of what staleness is here: a reason to tell somebody, never a gate.
func TestAStaleQuestionIsReportedAndHoldsNothingBack(t *testing.T) {
	t.Parallel()

	store, reports := supervisionStores(t)
	asking := openExchange("exchange-"+strings.Repeat("f", 32), 3)
	asking.Refers = []exchange.Reference{
		{What: "goal", ID: "autonomy", Revision: "rev-1"},
		{What: "artifact", ID: "brief", Revision: "rev-3"},
	}
	save(t, store, asking)

	loop := supervisionLoop(store, reports)
	loop.Revisions = map[string]string{"goal/autonomy": "rev-2"}
	pass, err := loop.Run()
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	var detail string
	for _, result := range pass.Results {
		if result.ExchangeID == asking.ID && result.Outcome == SupervisionStale {
			detail = result.Detail
		}
	}
	if detail == "" {
		t.Fatalf("results = %#v, want the stale question reported", pass.Results)
	}
	if !strings.Contains(detail, "product-manager") || !strings.Contains(detail, "goal/autonomy") {
		t.Errorf("detail = %q, want the role to tell and what moved", detail)
	}
	// Silence about the brief is not evidence it held still, and the detail says
	// so rather than leaving it out.
	if !strings.Contains(detail, "artifact/brief") || !strings.Contains(detail, "not judged") {
		t.Errorf("detail = %q, want the reference nothing is known about named as unjudged", detail)
	}
	if !recorded(pass, asking.ID, SupervisionUndelivered) {
		t.Errorf("results = %#v, want a stale exchange still deliverable", pass.Results)
	}
	if after := load(t, store, asking.ID); !after.Open() {
		t.Errorf("Outcome = %q, want staleness to have closed nothing", after.Outcome)
	}
}

// The sweep gives back the lease on every thread it turns out to have nothing to
// do with, as soon as it knows that, rather than holding the lot until the pass
// ends.
//
// It matters because a conductor meeting a held exchange refuses the ask instead
// of waiting for it. Holding every lease for the length of a pass would make an
// operator's question fail because a `yoyo reconcile` happened to be walking
// past, and the thread it was failing over is one the sweep was never going to
// touch. This asks the question from inside the pass — while the sweep is
// reclaiming a dead thread, the idle one beside it has to be free.
func TestASweepHoldsOnlyTheThreadsItIsActingOn(t *testing.T) {
	t.Parallel()

	store, reports := supervisionStores(t)
	crashed := openExchange("exchange-"+strings.Repeat("1", 32), 3)
	crashed.Rounds = []exchange.Round{askedRound(1, "yoyo pid 4242")}
	save(t, store, crashed)
	idle := openExchange("exchange-"+strings.Repeat("2", 32), 3)
	idle.Rounds = []exchange.Round{answeredRound(1, "one")}
	save(t, store, idle)

	loop := supervisionLoop(store, reports)
	// The real conductor does the reclaiming; this only listens in on the moment
	// it happens, which is the one moment the sweep is holding anything.
	watching := &watchWhileActing{
		inner: loop.Conductor,
		during: func() {
			lease, taken, err := store.Hold(idle.ID)
			if taken {
				lease.Release()
			}
			watchedIdleFree(t, taken, err)
		},
	}
	loop.Conductor = watching
	pass, err := loop.Run()
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if !recorded(pass, crashed.ID, SupervisionReclaimed) {
		t.Fatalf("results = %#v, want the dead thread reclaimed", pass.Results)
	}
	if !watching.acted {
		t.Fatal("the reclaim never ran, so nothing was asked while the sweep held anything")
	}
}

func watchedIdleFree(t *testing.T, taken bool, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("Hold() on the idle thread = %v, want it askable", err)
	}
	if !taken {
		t.Error("the sweep was still holding a thread it had nothing to do with; a conversation asking on it would have been refused")
	}
}

// watchWhileActing runs a callback at the moment the sweep is acting, and
// otherwise conducts exactly as the real one does.
type watchWhileActing struct {
	inner  SupervisionConductor
	during func()
	acted  bool
}

func (w *watchWhileActing) Reclaim(recorded exchange.Exchange, because string) (exchange.Exchange, error) {
	w.acted = true
	w.during()
	return w.inner.Reclaim(recorded, because)
}

func (w *watchWhileActing) Exhaust(recorded exchange.Exchange) (exchange.Exchange, error) {
	return w.inner.Exhaust(recorded)
}

// A product whose roles have never asked each other anything is nothing to
// sweep, rather than a sweep that failed.
func TestAProductWithNoExchangesIsNotAFailureToSweep(t *testing.T) {
	t.Parallel()

	store, reports := supervisionStores(t)
	pass, err := supervisionLoop(store, reports).Run()
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if len(pass.Results) != 0 {
		t.Errorf("results = %#v, want nothing", pass.Results)
	}
}

func supervisionStores(t *testing.T) (*runstate.ExchangeStore, *pileOfReports) {
	t.Helper()
	store, err := runstate.NewExchangeStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewExchangeStore() = %v", err)
	}
	return store, &pileOfReports{}
}

func supervisionLoop(store *runstate.ExchangeStore, reports *pileOfReports) SupervisionLoop {
	return SupervisionLoop{
		Store: store,
		Conductor: exchange.Conductor{
			Store:        store,
			Reports:      reports,
			ProductID:    "yoyodyne",
			RepositoryID: "yoyodyne",
			Holder:       "yoyo pid 1",
		},
	}
}

func openExchange(id string, cap int) exchange.Exchange {
	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	return exchange.Exchange{
		SchemaVersion: exchange.SchemaVersion,
		ID:            id,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Asker:         exchange.Party{Role: domain.RoleProductManager, Conversation: "chat-1"},
		Answerer:      exchange.Party{Role: domain.RoleArchitect},
		Question:      "what am I missing?",
		MaxRounds:     cap,
		OpenedAt:      at,
		UpdatedAt:     at,
	}
}

func askedRound(number int, holder string) exchange.Round {
	return exchange.Round{
		Number:   number,
		Question: "what am I missing?",
		Holder:   holder,
		AskedAt:  time.Date(2026, 9, 5, 9, number, 0, 0, time.UTC),
	}
}

func answeredRound(number int, answer string) exchange.Round {
	at := time.Date(2026, 9, 5, 9, number, 30, 0, time.UTC)
	round := askedRound(number, "yoyo pid 1")
	round.Answer = answer
	round.AnsweredAt = &at
	return round
}

func save(t *testing.T, store *runstate.ExchangeStore, recorded exchange.Exchange) {
	t.Helper()
	if err := store.Save(recorded); err != nil {
		t.Fatalf("Save(%s) = %v", recorded.ID, err)
	}
}

func load(t *testing.T, store *runstate.ExchangeStore, id string) exchange.Exchange {
	t.Helper()
	loaded, err := store.Load(id)
	if err != nil {
		t.Fatalf("Load(%s) = %v", id, err)
	}
	return loaded
}

func recorded(pass SupervisionPass, id string, outcome SupervisionOutcome) bool {
	for _, result := range pass.Results {
		if result.ExchangeID == id && result.Outcome == outcome {
			return true
		}
	}
	return false
}

type pileOfReports struct{ reports []report.Report }

func (p *pileOfReports) Append(reported report.Report) error {
	p.reports = append(p.reports, reported)
	return nil
}
