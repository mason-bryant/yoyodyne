package orchestrator

// Closure follows the kind of landing a run claimed, not the fact that it
// landed. The case these tests exist for is a real one: a run answered a work
// item it could not do yet with a diagnosis that said so in bold, the diagnosis
// integrated because it was good evidence, and the item closed against it —
// because closure mechanically followed integration and read nothing of what
// had landed. Both kinds are driven through the whole pipeline here rather than
// asserted over a hand-built state, because a test that set the claim itself
// would pass just as happily if nothing ever read one off a developer's reply.

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/landing"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// landingBlock is the claim as a developer's reply carries it.
func landingBlock(payload string) string {
	return landing.Fence + "\n" + payload + "\n```\n"
}

// The diagnosis this whole item comes from, replayed: a developer that finds the
// work is not doable yet lands the evidence for that and says so. The change is
// reviewed and integrated exactly as any other change is, and the item it was
// claimed for is left open with the reason on it.
func TestAnHonestNotDoableYetLandingIntegratesAndLeavesItsItemOpen(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalText = "The conversion is not doable yet; this change lands the diagnosis.\n\n" +
		landingBlock(`{"outcome":"evidence","why":"the management-conversion design has not landed, so the anchor stays open"}`)
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The evidence landed. That half is unchanged: an honest landing is worth
	// keeping and is promoted like anything else that passes the gate.
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil {
		t.Fatalf("an evidence landing did not integrate: %#v", outcome)
	}
	if outcome.WorkItemClosed {
		t.Fatal("the run closed an item its own landing said it does not discharge")
	}
	if tracker.closed {
		t.Fatalf("the tracker closed the item; calls = %v", tracker.calls)
	}
	if !tracker.reopened {
		t.Fatalf("the item was left claimed by a run that has ended; calls = %v", tracker.calls)
	}
	if tracker.item.Status != "open" {
		t.Errorf("item status = %q, want open", tracker.item.Status)
	}
	// The reason has to be on the item, or the item afterwards reads as work
	// somebody walked away from.
	if !strings.Contains(tracker.reopenReason, "the anchor stays open") {
		t.Errorf("the item does not carry the developer's account: %q", tracker.reopenReason)
	}
	if !strings.Contains(tracker.reopenReason, outcome.RunID) {
		t.Errorf("the item does not name the run whose evidence landed: %q", tracker.reopenReason)
	}
	// The notes an operator reads must not open by describing a completed item.
	if !strings.Contains(tracker.notes, "the item stays open") {
		t.Errorf("the recorded outcome reads as a discharged item: %q", tracker.notes)
	}
	if outcome.Landing != landing.OutcomeEvidence {
		t.Errorf("outcome landing = %q, want %q", outcome.Landing, landing.OutcomeEvidence)
	}
	// And the claim is durable, because the closure is not always made by the
	// process that read it.
	recorded := onlyRecordedRun(t, store)
	if recorded.Outcome != runstate.OutcomeSucceeded {
		t.Errorf("run outcome = %q, want %q: landing evidence is not a failed run", recorded.Outcome, runstate.OutcomeSucceeded)
	}
}

// The other kind, and the one nearly every run is. A developer that says nothing
// about its landing has discharged the item, and so has one that says so.
func TestADischargingLandingClosesItsItem(t *testing.T) {
	t.Parallel()

	for _, claimed := range []struct {
		name  string
		reply string
	}{
		{name: "a reply that claims nothing", reply: "implemented the work item"},
		{
			name: "a reply that claims the discharge",
			reply: "implemented the work item\n\n" +
				landingBlock(`{"outcome":"discharged","why":"the acceptance criteria are met and the suite covers them"}`),
		},
	} {
		t.Run(claimed.name, func(t *testing.T) {
			t.Parallel()

			tracker := newOutcomeTracker()
			provider := roleBackend(writeFeature, approveVerdict)
			provider.developerFinalText = claimed.reply
			pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

			outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if !outcome.WorkItemClosed || !tracker.closed || tracker.item.Status != "closed" {
				t.Fatalf("a discharging landing did not close its item: outcome = %#v, calls = %v", outcome, tracker.calls)
			}
			if tracker.reopened {
				t.Errorf("a discharging landing put its item back in the backlog; calls = %v", tracker.calls)
			}
		})
	}
}

// A claim that arrived and could not be read is not a claim that was never made.
// The developer wrote a block, which means it was trying to say something about
// whether the item closes, and the safe reading of an unreadable one is the
// recoverable direction: an item left open is something a person can settle, and
// a false closure is the thing nobody sees.
func TestAnUnreadableLandingClaimWithholdsTheClosure(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalText = "worked on it\n\n" + landingBlock(`{"outcome":"partly","why":"some of it"}`)
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil {
		t.Fatalf("an unreadable claim cost the run its integration: %#v", outcome)
	}
	if outcome.WorkItemClosed || tracker.closed {
		t.Fatalf("the item closed on a claim nobody could read; calls = %v", tracker.calls)
	}
	if outcome.LandingProblem == "" {
		t.Fatal("an unreadable claim left no trace on the outcome")
	}
	if !tracker.reopened || !strings.Contains(tracker.reopenReason, "could not be read") {
		t.Errorf("the item does not say why it was not closed: %q", tracker.reopenReason)
	}
	// The prose the developer wrote is the run's evidence, and a refused claim
	// must not take it.
	if !strings.Contains(outcome.Summary, "worked on it") {
		t.Errorf("a refused claim cost the run its summary: %q", outcome.Summary)
	}
}

// A repair round that finishes the work must not be closed against the previous
// attempt's evidence claim. The claim describes the change as it now stands.
func TestALaterAttemptReplacesTheLandingTheEarlierOneClaimed(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, repairVerdict, approveVerdict)
	provider.developerFinalTextByAttempt = []string{
		"not doable yet\n\n" + landingBlock(`{"outcome":"evidence","why":"the dependency has not landed"}`),
		"the reviewer was right, and it was doable after all",
	}
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 2

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Landing != "" || outcome.LandingReason != "" {
		t.Errorf("the second attempt inherited the first one's claim: %q / %q", outcome.Landing, outcome.LandingReason)
	}
	if !outcome.WorkItemClosed || tracker.reopened {
		t.Fatalf("the finished work was not closed: outcome = %#v, calls = %v", outcome, tracker.calls)
	}
}

// The reviewer is the only reader that sees the claim beside the change, which
// is what lets a diagnosis be judged as a diagnosis rather than as a missing
// implementation. It reaches the reviewer as untrusted evidence, because the
// developer wrote it.
func TestTheReviewerIsShownWhichLandingWasClaimed(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalText = "the diagnosis\n\n" +
		landingBlock(`{"outcome":"evidence","why":"the design this needs has not landed"}`)
	var reviewerPrompt string
	underlying := provider.run
	provider.run = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			reviewerPrompt = request.Prompt
		}
		return underlying(request)
	}
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(reviewerPrompt, "Claimed landing outcome") {
		t.Fatal("the reviewer was not shown the claim at all")
	}
	if !strings.Contains(reviewerPrompt, "does not discharge the work item") {
		t.Errorf("the reviewer was not told which landing was claimed: %q", reviewerPrompt)
	}
	if !strings.Contains(reviewerPrompt, "the design this needs has not landed") {
		t.Errorf("the reviewer was not told why: %q", reviewerPrompt)
	}
	// The claim is the developer's, so it belongs under the untrusted heading
	// rather than beside the invariants the harness supplied.
	claimAt := strings.Index(reviewerPrompt, "Claimed landing outcome")
	untrustedAt := strings.Index(reviewerPrompt, "# Untrusted review evidence")
	if untrustedAt < 0 || claimAt < untrustedAt {
		t.Error("the claim was presented outside the untrusted evidence the developer produced")
	}
}

// A sweep settling a run somebody killed decides the same way the run would
// have. It has to: a run whose merge the forge only queued is settled by a later
// process entirely, and a sweep reading the promotion alone is exactly the
// closure this record exists to stop.
func TestReconciliationSettlesAnInterruptedRunByTheLandingItClaimed(t *testing.T) {
	t.Parallel()

	for _, settled := range []struct {
		name       string
		reply      string
		wantClosed bool
	}{
		{
			name:       "a landing that discharges the item",
			reply:      "implemented the work item",
			wantClosed: true,
		},
		{
			name: "a landing that does not",
			reply: "the diagnosis\n\n" +
				landingBlock(`{"outcome":"evidence","why":"the design this needs has not landed"}`),
		},
	} {
		t.Run(settled.name, func(t *testing.T) {
			t.Parallel()

			repository, worktreeRoot, store := restartableFixture(t)
			tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := roleBackend(writeFeature, approveVerdict)
			provider.developerFinalText = settled.reply
			// Killed after the promotion and before the item was settled, which is
			// the boundary reconciliation exists for.
			halting := &haltingStore{StateStore: store, at: runstate.PhaseCompleting}
			pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
			if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil {
				t.Fatal("interrupted Run() error = nil")
			}
			if tracker.closed || tracker.reopened {
				t.Fatalf("the interrupted run settled the item itself; calls = %v", tracker.calls)
			}

			results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
			if len(results) != 1 {
				t.Fatalf("reconciled %d runs, want the one this test made", len(results))
			}
			if tracker.closed != settled.wantClosed {
				t.Errorf("closed = %t, want %t; calls = %v", tracker.closed, settled.wantClosed, tracker.calls)
			}
			if tracker.reopened == settled.wantClosed {
				t.Errorf("reopened = %t, want %t; calls = %v", tracker.reopened, !settled.wantClosed, tracker.calls)
			}
			// Whichever way it went, settling it twice settles it once: the second
			// sweep finds the item already in the state this run's landing calls for.
			calls := len(tracker.calls)
			reconcileSweep(t, repository, worktreeRoot, store, tracker)
			if extra := tracker.calls[calls:]; countCalls(extra, "complete")+countCalls(extra, "reopen") > 0 {
				t.Errorf("a second sweep settled the item again: %v", extra)
			}
		})
	}
}

// The derivation the closure reads is one derivation, so a run cannot be closed
// by the process that made it and left open by the sweep that settles it.
func TestTheClosureDerivationReadsTheSameRecordEverywhere(t *testing.T) {
	t.Parallel()

	for _, recorded := range []struct {
		name       string
		state      runstate.State
		discharges bool
	}{
		{name: "a run that claimed nothing", state: runstate.State{}, discharges: true},
		{
			name:       "a run recorded before the channel existed",
			state:      runstate.State{Status: runstate.StatusSucceeded},
			discharges: true,
		},
		{
			name:       "a claimed discharge",
			state:      runstate.State{LandingOutcome: runstate.LandingDischarged, LandingReason: "done"},
			discharges: true,
		},
		{
			name:       "a claimed evidence landing",
			state:      runstate.State{LandingOutcome: runstate.LandingEvidence, LandingReason: "not yet"},
			discharges: false,
		},
		{
			name:       "a claim that could not be read",
			state:      runstate.State{LandingProblem: "decode landing claim: unexpected trailing content"},
			discharges: false,
		},
	} {
		t.Run(recorded.name, func(t *testing.T) {
			t.Parallel()

			if got := recorded.state.LandingDischarges(); got != recorded.discharges {
				t.Fatalf("LandingDischarges() = %t, want %t", got, recorded.discharges)
			}
			// An item already in the state the landing calls for is settled, which
			// is what makes settling one twice settle it once.
			for status, want := range map[string]bool{"closed": true, "open": !recorded.discharges, "in_progress": false} {
				if got := landingSettled(recorded.state, status); got != want {
					t.Errorf("landingSettled(%q) = %t, want %t", status, got, want)
				}
			}
		})
	}
}

// A claim with no account of itself is refused by the durable schema, because
// the account is the half of the record anybody reads afterwards: an item left
// open for a reason nobody wrote down is the false closure's quieter cousin.
func TestTheDurableSchemaRefusesALandingClaimWithNoReason(t *testing.T) {
	t.Parallel()

	now := execution.RealClock{}.Now()
	state := runstate.State{
		SchemaVersion:  runstate.StateSchemaVersion,
		RunID:          "run-abcdef0123456789abcdef0123456789",
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
		WorkItemID:     "yoyodyne-task",
		Backend:        "claude-code",
		Status:         runstate.StatusRunning,
		StartedAt:      now,
		UpdatedAt:      now,
		LandingOutcome: runstate.LandingEvidence,
	}
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "landing_reason") {
		t.Fatalf("Validate() error = %v, want a refusal naming the missing reason", err)
	}
	state.LandingReason = "the design this needs has not landed"
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() refused a complete claim: %v", err)
	}
}
