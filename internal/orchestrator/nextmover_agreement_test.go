package orchestrator

// Whether a carry-out is outstanding about a stoppage is one question, and two
// things answer it: the development manager's docket, which names a next mover
// on every entry, and the read model every operator surface projects, which puts
// the same item in one of two waits on the status head, the attention line and
// the alarm. They were two rules. The docket asked the item's totals — any
// re-run not yet claimed, or any grant not yet spent — and the read model asked
// the decision standing about this run and consulted the grant only where that
// decision was a repair. An item with an unspent repair grant whose latest
// stoppage was then decided wait, re-scope or escalate read as the harness's on
// one and as the development manager's on the other: one piece of work with two
// next movers, which is a disagreement only the operator can adjudicate.
//
// The rule is triage.AwaitingCarryOut now, and both read it. This is what holds
// them together anyway: one durable ledger, both readers, and the same answer
// about the same stoppage. It lives beside the docket rather than beside the read
// model because whoever changes what an entry says about its next mover is who
// has to know the surfaces move with it.

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// nextMoverStoppages is the one stopped run both readers are asked about. The
// read model wants the escalations beside the runs; there are none, so what
// either says about the item is attributable to the triage record and to nothing
// else.
type nextMoverStoppages struct{ states []runstate.State }

func (s nextMoverStoppages) Recorded() ([]runstate.State, error) { return s.states, nil }

func (nextMoverStoppages) Escalated() ([]runstate.Escalation, error) { return nil, nil }

func TestAnItemIsNeverGivenTwoNextMoversAcrossSurfaces(t *testing.T) {
	t.Parallel()

	// A grant recorded and unspent on every case, which is the state that made the
	// two rules disagree: the read model consults it only under a repair, and the
	// docket consulted it whatever had been decided since.
	const grantedRounds, roundsSpent = 3, 2

	for _, test := range []struct {
		name string
		// decided is the decision standing about this stoppage, or empty for a
		// stoppage nobody has decided about.
		decided string
		// carryOut is what both readers must say: the harness has something left to
		// do about this stoppage.
		carryOut bool
	}{
		{name: "nobody has decided anything", decided: "", carryOut: false},
		{name: "a repair granted and not handed back", decided: runstate.TriageDecisionRepair, carryOut: true},
		{name: "a re-run nothing has claimed", decided: runstate.TriageDecisionRerun, carryOut: true},
		// The three that buy no attempt. Each leaves the harness nothing to do, and
		// each is what stood recorded over an unspent grant in the case this test
		// was written for.
		{name: "a wait decided over an unspent grant", decided: runstate.TriageDecisionWait, carryOut: false},
		{name: "a re-scope decided over an unspent grant", decided: runstate.TriageDecisionRescope, carryOut: false},
		{name: "an escalation decided over an unspent grant", decided: runstate.TriageDecisionEscalate, carryOut: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			stopped := stoppedState()
			ledger := runstate.TriageCounters{
				RepairGrants:    1,
				GrantedRounds:   grantedRounds,
				CommittedRounds: grantedRounds,
				ReviewRounds:    roundsSpent,
			}
			if test.decided != "" {
				ledger.Decisions = []runstate.TriageDecision{triageDecided(test.decided, stopped.RunID)}
			}
			recorded := &recordedDecisions{
				counters: map[string]runstate.TriageCounters{docketedItem: ledger},
			}

			built, err := docketerDeciding([]runstate.State{stopped}, &memoryDocket{}, recorded, recorded).Build()
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if len(built.Entries) != 1 {
				t.Fatalf("built = %#v, want the one stoppage docketed", built)
			}
			entry := built.Entries[0]

			// The read model is asked about the same run and the same record. Nothing
			// is wired to look for the change, so the run is held on what its own
			// record says survived — which is beside the point here: what is being
			// compared is which of the two waits the hold is in.
			held, err := readmodel.HeldForAPerson(context.Background(),
				nextMoverStoppages{states: []runstate.State{stopped}}, recorded, nil)
			if err != nil {
				t.Fatalf("HeldForAPerson() error = %v", err)
			}
			reason, holding := held.Reason(docketedItem)
			if !holding {
				t.Fatalf("held = %+v, want the stopped run to hold its item", held)
			}

			docket, surfaces := entry.Counters.AwaitingCarryOut(), held.Decided(docketedItem)
			if docket != surfaces {
				t.Fatalf("the docket says a carry-out is outstanding = %t and the surfaces say %t about %s; "+
					"one stoppage with two answers is one item with two next movers\ndocket:\n%s\nsurfaces: %s",
					docket, surfaces, docketedItem, entry.Render(), reason)
			}
			if docket != test.carryOut {
				t.Fatalf("a carry-out is outstanding = %t, want %t with %q decided about the stoppage",
					docket, test.carryOut, test.decided)
			}

			// And the words each surface says follow the one answer, so the agreement
			// is readable rather than only arithmetic.
			mover, clause := "Next mover: you", "the development manager decides what happens to it"
			if test.carryOut {
				mover, clause = "Next mover: the harness", "the development manager has already decided what happens to it"
			}
			if rendered := entry.Render(); !strings.Contains(rendered, mover) {
				t.Fatalf("the entry does not name %q as the next mover:\n%s", mover, rendered)
			}
			if !strings.Contains(reason, clause) {
				t.Fatalf("hold = %q, want it to close on %q", reason, clause)
			}
		})
	}
}
