package conformance

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The claim this package exists to make: whichever adapter met a condition, the
// harness is left holding the same answer. A failure here names the adapter, the
// condition, the shape it was reported in, and what that adapter left the
// harness to do instead.
func TestEveryAdapterClassifiesTheSameConditionsTheSameWay(t *testing.T) {
	t.Parallel()

	for _, problem := range Verify(BuiltInAdapters()) {
		t.Errorf("%s", problem)
	}
}

// The gate on a new adapter. An adapter this build ships and this suite says
// nothing about is one nobody has asked how it reads a refusal, and the first
// evidence of a divergent answer would be a run that waited where it should have
// failed — or relaunched into an account no relaunch can fix.
func TestEveryAdapterThisBuildShipsIsInTheSuite(t *testing.T) {
	t.Parallel()

	if missing := Uncovered(BuiltInAdapters()); len(missing) > 0 {
		t.Fatalf("these adapters ship with no conformance cases: %v\n"+
			"Add the provider's own words for each condition to BuiltInAdapters.", missing)
	}
	// The gate going stale the other way is worth catching too: a set that names
	// nothing would report no problems at all and read as a suite that passed.
	if missing := Uncovered(nil); len(missing) != len(backend.RunnableAdapters()) {
		t.Fatalf("Uncovered(nil) named %v, and every adapter this build ships is uncovered by nothing", missing)
	}
}

// A suite that cannot fail is evidence of nothing, so here is each way it fails:
// a condition an adapter says nothing about, and a condition an adapter answers
// differently from every other. The second is built by handing one adapter
// another condition's stream, which is exactly the divergence the suite is for —
// one provider's words read as the wrong condition.
func TestTheSuiteFailsAnAdapterThatDivergesOrSaysNothing(t *testing.T) {
	t.Parallel()

	t.Run("a condition nothing says how this provider reports", func(t *testing.T) {
		t.Parallel()

		silent := codexAdapter()
		delete(silent.Samples, AuthenticationRejected)
		problems := Verify([]Adapter{silent})
		if len(problems) != 1 || problems[0].Condition != AuthenticationRejected {
			t.Fatalf("Verify() = %v, want one problem naming the condition with no sample", problems)
		}
		if !strings.Contains(problems[0].Detail, string(ResponseRefusalStands)) {
			t.Fatalf("the problem does not say what the adapter owes: %s", problems[0])
		}
	})

	t.Run("a condition this provider answers differently", func(t *testing.T) {
		t.Parallel()

		// The provider's words for an exhausted window, filed as the network
		// failure it is not. The adapter reads them correctly, which is the point:
		// what the suite catches is the two being told apart differently, not a
		// dialect that cannot read its own provider.
		diverging := codexAdapter()
		diverging.Samples[NetworkFailure] = codexAdapter().Samples[CapacityExhausted]
		problems := Verify([]Adapter{diverging})
		if len(problems) != 2 {
			t.Fatalf("Verify() = %v, want a problem for each shape of the misfiled condition", problems)
		}
		for _, problem := range problems {
			if problem.Condition != NetworkFailure {
				t.Fatalf("Verify() reported %s, want the misfiled condition", problem)
			}
			if !strings.Contains(problem.Detail, string(ResponseWaitForTheWindow)) ||
				!strings.Contains(problem.Detail, string(ResponseMakeAnotherAttempt)) {
				t.Fatalf("the problem says neither what the adapter did nor what it owed: %s", problem)
			}
		}
	})

	t.Run("a provider nothing in this build can launch", func(t *testing.T) {
		t.Parallel()

		problems := Verify([]Adapter{{Descriptor: backend.Descriptor{ID: domain.Backend("invented")}}})
		if len(problems) != 1 || problems[0].Provider != domain.Backend("invented") {
			t.Fatalf("Verify() = %v, want one problem naming the provider with no adapter", problems)
		}
	})
}

// Every condition names a response the harness can actually take. A condition
// added with none would pass every adapter silently, because nothing would be
// asserted about it.
func TestEveryConditionRequiresAResponse(t *testing.T) {
	t.Parallel()

	for _, condition := range Conditions {
		switch condition.Requires() {
		case ResponseWaitForTheWindow, ResponseAskForAnotherModel, ResponseMakeAnotherAttempt, ResponseRefusalStands:
		default:
			t.Errorf("condition %q requires %q, which is not a response the harness takes", condition, condition.Requires())
		}
	}
}
