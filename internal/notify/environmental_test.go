package notify

// What the thread says about a round the environment refused.
//
// The blocker a thread carries is the only account of a stoppage most people
// read, and the words around it decide what they take from the counters. A round
// the environment refused cost the item nothing, so a thread saying only what
// went wrong reads as an item one step nearer its cap when it is not. And the one
// state where that reading is correct — a return the settle decided on and could
// not write — is the one the thread has to say loudest, because the counters
// really are higher than the round cost.

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// refusedRun is a run that stopped on an environmental refusal, with whatever
// the settle made of it applied by the caller.
func refusedRun(t *testing.T, apply func(*runstate.EnvironmentalRefusal)) runstate.State {
	t.Helper()
	completed := moment.Add(time.Minute)
	state := running()
	state.Status = runstate.StatusFailed
	state.CompletedAt = &completed
	state.Failure = "the preserved worktree holds none of the change it was picked up to continue"
	// A refusal like this is handed to a person, so the run carries the durable
	// blocker that says so. It is what makes the run a stoppage rather than one
	// the harness merely failed to carry, and the thread says a different thing
	// about each. That the production path actually records it is not this
	// fixture's word for it: blockOnMissingPreservedChange calls block(), and
	// TestAnEmptyDiffRoundTheEnvironmentRefusedSpendsNothing in
	// internal/orchestrator asserts the blocker and the stopped outcome on the run
	// the pipeline itself wrote.
	state.Blocker = runstate.RecordBlocker("the handback carried none of the change it was continuing")
	refusal := &runstate.EnvironmentalRefusal{
		Cause:      runstate.CauseHandbackMissingChange,
		Detail:     "the worktree holds no change at all against the base commit the run recorded",
		RecordedAt: moment,
		Settled:    true,
		Refused:    true,
	}
	apply(refusal)
	state.Environmental = refusal
	return state
}

// blockerBody is the words the thread actually carries for a stopped run.
func blockerBody(t *testing.T, after runstate.State) string {
	t.Helper()
	_, notifications := crossed(t, running(), after)
	blocker := only(t, notifications, KindBlockerRecorded)
	message, err := Render(blocker.Topic, blocker.Speaker, blocker.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return message.Body
}

// What the class is for, said in the thread: the round is named as refused and
// what it gave back is stated, rather than the failure standing alone.
func TestTheThreadSaysARefusedRoundGaveItsBudgetBack(t *testing.T) {
	after := refusedRun(t, func(refusal *runstate.EnvironmentalRefusal) {
		refusal.RoundReturned = true
		refusal.GrantReturned = true
	})
	body := blockerBody(t, after)
	for _, want := range []string{
		"environmentally refused",
		string(runstate.CauseHandbackMissingChange),
		"review round it was charged and the granted repair round it consumed were both returned",
		after.Failure,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the thread does not say %q:\n%s", want, body)
		}
	}
}

// A refusal that reached nothing that spends says so rather than claiming a
// return it never made. A reader who saw "refused" and an accounting that did not
// happen would take the accounting on trust.
func TestTheThreadDoesNotClaimAReturnARefusedRoundNeverMade(t *testing.T) {
	body := blockerBody(t, refusedRun(t, func(*runstate.EnvironmentalRefusal) {}))
	if !strings.Contains(body, "reached nothing that spends") {
		t.Fatalf("the thread does not say what a refusal with nothing to return came to:\n%s", body)
	}
	if strings.Contains(body, "was returned") {
		t.Fatalf("the thread claims a return nothing made:\n%s", body)
	}
}

// The one state where the item really is a round nearer its cap than it should
// be: the settle classified the round and could not write the return. The thread
// has to say that rather than the opposite.
func TestTheThreadSaysWhenARefusedRoundCouldNotBePaidBack(t *testing.T) {
	after := refusedRun(t, func(refusal *runstate.EnvironmentalRefusal) {
		refusal.Problem = "the review round attempt run-a#3 was charged could not be returned: the triage record could not be written"
	})
	body := blockerBody(t, after)
	for _, want := range []string{
		"environmentally refused",
		"could not be written",
		"counters are higher than the round cost it",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the thread does not say %q:\n%s", want, body)
		}
	}
	// And it must not say the opposite in the same breath.
	if strings.Contains(body, "spent no budget") || strings.Contains(body, "was returned") {
		t.Fatalf("the thread tells the operator the item stands where it did while its counters say otherwise:\n%s", body)
	}
}

// A cause recorded on a round that delivered a change anyway is not a refusal,
// and the thread says nothing about it: that round spent exactly as any round
// does, and a line implying otherwise would be the same misreading in reverse.
func TestTheThreadIsSilentOnACauseThatDidNotRefuseTheRound(t *testing.T) {
	after := refusedRun(t, func(refusal *runstate.EnvironmentalRefusal) {
		refusal.Refused = false
	})
	body := blockerBody(t, after)
	if strings.Contains(body, "environmentally refused") {
		t.Fatalf("the thread calls an unrefused round refused:\n%s", body)
	}
	if !strings.Contains(body, after.Failure) {
		t.Fatalf("the thread lost the reason the run stopped:\n%s", body)
	}
}
