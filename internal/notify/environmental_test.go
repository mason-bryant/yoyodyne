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
	"github.com/mason-bryant/yoyodyne/internal/triage"
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

// approvedStoppedRun is the 309 shape: a change the reviewer approved, stopped
// short of its promotion by the environment, with the stop on the record. The
// pipeline's terminal write leaves no blocker on such a run, so the thread says
// it as a run that ended rather than as a stoppage — and both kinds have to end
// on the same sentence, which is what the blocker variant below is for.
func approvedStoppedRun(t *testing.T, blocked bool) runstate.State {
	t.Helper()
	completed := moment.Add(time.Minute)
	state := running()
	state.Status = runstate.StatusFailed
	state.Phase = runstate.PhaseIntegrating
	state.CompletedAt = &completed
	state.WorktreePath = "/state/worktrees/task"
	state.Branch = "yoyodyne/task/abc"
	state.BaseCommit = strings.Repeat("a", 40)
	state.TargetBranch = "main"
	state.ProviderSessionID = "developer-session"
	state.ReviewSessionID = "reviewer-session"
	state.ReviewDecision = runstate.ReviewApprove
	state.Failure = "integrate approved change: primary checkout is not ready for integration: primary repository has uncommitted changes: AGENTS.md"
	state.IntegrationStop = &runstate.IntegrationStop{
		Cause:      runstate.CauseDirtyPrimary,
		Detail:     "integrate approved change: primary checkout is not ready for integration",
		Phase:      runstate.PhaseIntegrating,
		RecordedAt: completed,
	}
	if blocked {
		state.Blocker = runstate.RecordBlocker("the promotion was refused for a dirty primary checkout")
	}
	return state
}

// The channel line for an approved change the environment stopped ends on the
// same sentence the docket entry carries and the repair verb refuses in: the
// harness's move, by `yoyo triage resume`, with the cause — rather than the
// table's clause sending the reader to triage for a decision, or telling them
// nothing was recorded for anybody.
func TestTheThreadNamesTheResumeVerbForAnApprovedChangeTheEnvironmentStopped(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		after := approvedStoppedRun(t, blocked)
		_, notifications := crossed(t, running(), after)
		kind := KindRunEnded
		if blocked {
			kind = KindBlockerRecorded
		}
		said := only(t, notifications, kind)
		message, err := Render(said.Topic, said.Speaker, said.Event)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		want := after.IntegrationStop.ResumeSays(after.RunID)
		if !strings.Contains(message.Body, nextMoveLead+"the harness's — "+want) {
			t.Fatalf("blocked=%t: the thread does not end on the resume sentence %q:\n%s", blocked, want, message.Body)
		}
		for _, wrong := range []string{"nothing moves this item until it is decided", "nothing was recorded for anybody to decide"} {
			if strings.Contains(message.Body, wrong) {
				t.Fatalf("blocked=%t: the thread sends the reader the wrong way with %q:\n%s", blocked, wrong, message.Body)
			}
		}
		if !strings.Contains(message.Body, after.Failure) {
			t.Fatalf("blocked=%t: the thread lost the reason the run stopped:\n%s", blocked, message.Body)
		}
	}
}

// The same stop once its branch is gone. The resume would refuse, so the line
// names no resume: it says the branch is gone and that a re-run is the way on,
// which is what the docket entry, the pull's hold, `yoyo status`, and the repair
// verb's refusal say of the same stoppage, by the same look and the same rule.
// Where the look finds the branch there, the line still names the resume.
func TestTheThreadNamesNoResumeForAnApprovedChangeWhoseBranchIsGone(t *testing.T) {
	lookedAt := moment.Add(time.Hour)
	for _, blocked := range []bool{false, true} {
		after := approvedStoppedRun(t, blocked)
		kind := KindRunEnded
		if blocked {
			kind = KindBlockerRecorded
		}
		body := func(look func(runstate.State) triage.Found) string {
			t.Helper()
			notifications, err := FromRun(running(), after, look)
			if err != nil {
				t.Fatalf("select from run state: %v", err)
			}
			said := only(t, notifications, kind)
			message, err := Render(said.Topic, said.Speaker, said.Event)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			return message.Body
		}
		gone := body(func(run runstate.State) triage.Found {
			return triage.Found{At: lookedAt, Branch: run.Branch, WorktreePath: run.WorktreePath, WorktreeThere: true}
		})
		if strings.Contains(gone, "triage resume") {
			t.Fatalf("blocked=%t: the thread names the resume for a stop whose branch is gone:\n%s", blocked, gone)
		}
		for _, want := range []string{"run " + after.RunID + "'s branch is gone", "checked and NOT there", "a re-run is the way on"} {
			if !strings.Contains(gone, want) {
				t.Fatalf("blocked=%t: the thread does not say %q:\n%s", blocked, want, gone)
			}
		}
		there := body(func(run runstate.State) triage.Found {
			return triage.Found{At: lookedAt, Branch: run.Branch, WorktreePath: run.WorktreePath, BranchThere: true, WorktreeThere: true}
		})
		if want := after.IntegrationStop.ResumeSays(after.RunID); !strings.Contains(there, nextMoveLead+"the harness's — "+want) {
			t.Fatalf("blocked=%t: the thread does not end on the resume sentence while the branch is there:\n%s", blocked, there)
		}
		// A sink wired without a repository answers from the run's record, and a
		// record that says the branch was removed names no resume either.
		removed := after
		removed.BranchRemoved = true
		notifications, err := FromRun(running(), removed, nil)
		if err != nil {
			t.Fatalf("select from run state: %v", err)
		}
		said := only(t, notifications, kind)
		message, err := Render(said.Topic, said.Speaker, said.Event)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if strings.Contains(message.Body, "triage resume") || !strings.Contains(message.Body, "a re-run is the way on") {
			t.Fatalf("blocked=%t: a record saying the branch was removed still sends the reader to the resume:\n%s", blocked, message.Body)
		}
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
