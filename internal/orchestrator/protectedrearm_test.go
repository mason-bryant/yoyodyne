package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// On a protected target a run never moves the primary checkout's main, so a
// change whose merge the forge dropped has landed nowhere, and its one route to
// landing is its pull request: the development manager decides a re-arm, `yoyo
// triage rearm` repeats the request, the forge merges it, and reconciliation
// settles the item. This drives that route end to end over a real repository, a
// real remote that refuses direct pushes to main, and the durable records every
// step reads — rather than over a record fabricated to look like the state a
// step leaves.

// rearmableForge is the fabricated forge with the one question a re-arm asks
// that a run's own merge does not: what is unmet on the request now. Nothing is,
// which is what a drop whose cause has since passed looks like.
type rearmableForge struct {
	queuedForge
}

func (rearmableForge) MergeState(context.Context, int) (string, error) {
	return "CLEAN", nil
}

// droppedProtectedRun is a run on a protected main whose queued merge the forge
// then dropped, settled by reconciliation onto a blocker and docketed, with the
// development manager's re-arm decision recorded against its publication.
type droppedProtectedRun struct {
	queuedFixture
	outcome  Outcome
	docketer *Docketer
	key      string
}

func newDroppedProtectedRun(t *testing.T) droppedProtectedRun {
	t.Helper()
	fixture := newQueuedFixture(t)
	protectBranch(t, fixture.remote, "main")
	fixture.forge.SetTargetProtection(publish.BranchProtection{Protected: true, By: "branch protection"})
	fixture.docket = &memoryDocket{}
	outcome := fixture.run(t)
	if outcome.Integration == nil || !outcome.Integration.ThroughPullRequest {
		t.Fatalf("integration = %#v, want a landing through the pull request", outcome.Integration)
	}
	if local := publishedCommit(t, fixture.repository, "main"); local != outcome.BaseCommit {
		t.Fatalf("local main = %q with the merge queued, want the base %q", local, outcome.BaseCommit)
	}

	// The forge drops the queued merge, and the sweep hands the unlanded change to
	// a person.
	fixture.forge.DropQueuedMerge()
	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionBlocked || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the dropped merge settled onto a blocker", results)
	}
	dropped := loadRun(t, fixture.store, pipelineRunID)
	if dropped.Blocker == "" || dropped.MergeDrop == nil || !fixture.tracker.Record().Blocked || fixture.tracker.Record().Closed {
		t.Fatalf("dropped = blocker %q, drop %#v, item blocked %t closed %t; want the unlanded change handed to a person",
			dropped.Blocker, dropped.MergeDrop, fixture.tracker.Record().Blocked, fixture.tracker.Record().Closed)
	}
	if local := publishedCommit(t, fixture.repository, "main"); local != outcome.BaseCommit {
		t.Fatalf("local main = %q after the drop, want the base %q the change never left", local, outcome.BaseCommit)
	}
	docketer := docketerOverStore(fixture.docket, fixture.store, docketConfig())
	if built, err := docketer.Build(); err != nil || len(built.Entries) != 1 || len(built.Entries[0].Earlier) != 1 {
		t.Fatalf("docket = %#v, %v; want the stoppage and the publication both docketed, as one live entry for the run", built, err)
	}

	// The development manager decides a re-arm, which spends the publication's
	// budget where the conversation records it.
	key := triage.PublicationKey(pipelineRunID, dropped.PullRequest.Number)
	if _, err := fixture.store.Triage().RecordMergeRearm(context.Background(), fixture.tracker.Record().Item.ID, key,
		triageDecided(runstate.TriageDecisionRearm, pipelineRunID), time.Now().UTC(), rearmCaps); err != nil {
		t.Fatalf("RecordMergeRearm() error = %v", err)
	}
	return droppedProtectedRun{queuedFixture: fixture, outcome: outcome, docketer: docketer, key: key}
}

// rearmer is the action `yoyo triage rearm` carries the decision out with, wired
// as buildRearmer wires it: the docket, the run store, the forge, the real
// worktree manager's pre-merge check, and the item's triage record.
func (r droppedProtectedRun) rearmer(t *testing.T) Rearmer {
	t.Helper()
	return Rearmer{
		Docket:    r.docket,
		Runs:      r.store,
		Forge:     rearmableForge{r.forge},
		Worktrees: newSweepManager(t, r.repository, r.worktreeRoot),
		Decisions: r.store.Triage(),
	}
}

func (r droppedProtectedRun) rearm(t *testing.T) RearmResult {
	t.Helper()
	merges := len(r.forge.MergeRequests())
	result, err := r.rearmer(t).Rearm(context.Background(), RearmRequest{Run: pipelineRunID, Reason: rearmReasoning})
	if err != nil {
		t.Fatalf("Rearm() error = %v", err)
	}
	if !result.Rearmed || !result.Queued || result.Rearms != 1 {
		t.Fatalf("result = %+v, want the request repeated and queued once", result)
	}
	if len(r.forge.MergeRequests()) != merges+1 {
		t.Fatalf("merge requests = %d, want the one repeat", len(r.forge.MergeRequests())-merges)
	}
	want := publish.MergeRequest{
		Number:     r.outcome.PullRequest.Number,
		HeadCommit: r.outcome.Integration.SourceCommit,
		Method:     publish.MergeMethod(r.outcome.PullRequest.MergeMethod),
	}
	if repeated := r.forge.MergeRequests()[len(r.forge.MergeRequests())-1]; repeated != want {
		t.Fatalf("repeated request = %#v, want the identical authorized one %#v", repeated, want)
	}
	return result
}

func TestARearmOnAProtectedTargetLandsThroughTheForgeAndReconcileClosesTheItem(t *testing.T) {
	t.Parallel()

	t.Run("the forge merges the re-armed request", func(t *testing.T) {
		t.Parallel()

		run := newDroppedProtectedRun(t)
		run.rearm(t)
		// Nothing about the re-arm moved main: it asks the forge and nothing else.
		if local := publishedCommit(t, run.repository, "main"); local != run.outcome.BaseCommit {
			t.Fatalf("local main = %q after the re-arm, want the base %q", local, run.outcome.BaseCommit)
		}
		rearmed := loadRun(t, run.store, pipelineRunID)
		if !rearmed.Outstanding() || rearmed.PublishFailure != "" || rearmed.Blocker != "" {
			t.Fatalf("re-armed run = outstanding %t, publish failure %q, blocker %q; want it back where reconciliation settles it",
				rearmed.Outstanding(), rearmed.PublishFailure, rearmed.Blocker)
		}

		run.forge.PerformQueuedMerge(t)
		merge := publishedCommit(t, run.remote, "main")
		if merge == run.outcome.Integration.SourceCommit {
			t.Fatalf("remote main = %q, want the forge's own merge commit above the promoted commit", merge)
		}
		results := run.reconcile(t)
		if len(results) != 1 || results[0].Action != ActionCompleted || results[0].Failure != "" {
			t.Fatalf("reconciliation = %#v, want the re-armed merge settled as completed", results)
		}

		// The item is closed on the forge's merge.
		if !run.tracker.Record().Closed || !strings.Contains(run.tracker.Record().CloseReason, "merged by the forge") {
			t.Errorf("item closed = %t with reason %q, want the re-armed merge to close it", run.tracker.Record().Closed, run.tracker.Record().CloseReason)
		}
		// The local target is caught up onto the remote merge commit, by the settle
		// itself rather than a later convergence sweep.
		if local := publishedCommit(t, run.repository, "main"); local != merge {
			t.Errorf("local main = %q, want it caught up onto the forge's merge %q", local, merge)
		}
		if catchup := results[0].Catchup; catchup == nil || !catchup.Advanced || catchup.RemoteCommit != merge {
			t.Errorf("catch-up = %#v, want main advanced onto %q", catchup, merge)
		}
		assertRemoteCarriesPromotion(t, run.repository, run.remote, "main", run.outcome.Integration.SourceCommit)
		(&protectedRun{repository: run.repository, remote: run.remote}).assertMainNotAhead(t)

		// The run's record shows the publication settled.
		settled := loadRun(t, run.store, pipelineRunID)
		if settled.PullRequest == nil || !settled.PullRequest.Merged || settled.PullRequest.MergeQueued || settled.PullRequest.MergeCommit != merge {
			t.Fatalf("recorded publication = %#v, want it merged at the forge's merge commit %q", settled.PullRequest, merge)
		}
		if settled.PullRequest.MergeRearms != 1 {
			t.Errorf("recorded re-arms = %d, want the one the decision bought", settled.PullRequest.MergeRearms)
		}
		if settled.PublishFailure != "" || settled.Blocker != "" {
			t.Errorf("record = publish failure %q, blocker %q; want nothing outstanding", settled.PublishFailure, settled.Blocker)
		}
		if settled.Outstanding() || settled.AwaitingForge() || settled.Outcome() != runstate.OutcomeSucceeded {
			t.Errorf("record = outstanding %t, awaiting the forge %t, outcome %s; want a settled success",
				settled.Outstanding(), settled.AwaitingForge(), settled.Outcome())
		}
		if !settled.WorktreeRemoved || !settled.BranchRemoved {
			t.Errorf("worktree removed = %t, branch removed = %t; want the landed run cleaned up", settled.WorktreeRemoved, settled.BranchRemoved)
		}
		if published := publishedCommit(t, run.remote, run.outcome.Branch); published != "" {
			t.Errorf("merged remote branch survived at %q", published)
		}
		if held := heldItemsOf(t, run.store, run.tracker.Record().Item.ID); held[run.tracker.Record().Item.ID] {
			t.Errorf("holds = %v, want the item released once its publication settled", held)
		}

		// The docket entries are closed as settled, and nothing is re-derived.
		rebuilt, err := run.docketer.Build()
		if err != nil {
			t.Fatalf("Build() after the settlement error = %v", err)
		}
		if len(rebuilt.Entries) != 0 || rebuilt.Closed != 2 || rebuilt.Added != 0 {
			t.Errorf("docket after the settlement = %#v, want the stoppage and the publication closed", rebuilt)
		}
		if closed := run.docket.closed[run.key]; closed.Decision != settledPublicationDecision {
			t.Errorf("publication closure = %#v, want the harness's settlement recorded on it", closed)
		}

		// Settled is settled: the next sweep has nothing to ask.
		if again := run.reconcile(t); len(again) != 0 {
			t.Errorf("second reconciliation = %#v, want nothing left to settle", again)
		}
	})

	t.Run("the forge drops the re-armed request again", func(t *testing.T) {
		t.Parallel()

		run := newDroppedProtectedRun(t)
		run.rearm(t)
		merges := len(run.forge.MergeRequests())

		run.forge.DropQueuedMerge()
		notes := len(run.tracker.Record().NoteRecords)
		results := run.reconcile(t)
		if len(results) != 1 || results[0].Action != ActionBlocked || results[0].Failure != "" {
			t.Fatalf("reconciliation = %#v, want the second drop settled onto a blocker", results)
		}
		// The re-arm left the item blocked from the first drop, so the second one's
		// blocker reaches it as a note rather than a fresh block, which in the
		// tracker is the same appended note without the status change.
		if !run.tracker.Record().Blocked || run.tracker.Record().Closed {
			t.Fatalf("item blocked = %t, closed = %t; want the second drop left with a person", run.tracker.Record().Blocked, run.tracker.Record().Closed)
		}
		if len(run.tracker.Record().NoteRecords) == notes {
			t.Fatal("the second drop wrote nothing on the item")
		}
		written := strings.Join(run.tracker.Record().NoteRecords[notes:], "\n")
		redropped := loadRun(t, run.store, pipelineRunID)
		// Recorded as an escalation: on the result, on the item, and on the run's
		// own blocker, which is what the docket and `yoyo status` read.
		for _, want := range []string{"second drop of this publication", "escalation rather than something to re-arm again"} {
			if !strings.Contains(results[0].Detail, want) {
				t.Errorf("detail %q does not say %q", results[0].Detail, want)
			}
			if !strings.Contains(written, want) {
				t.Errorf("the item's notes %q do not say %q", written, want)
			}
			if !strings.Contains(redropped.Blocker, want) {
				t.Errorf("the run's blocker %q does not say %q", redropped.Blocker, want)
			}
		}
		if redropped.PullRequest.MergeRearms != 1 || redropped.PullRequest.MergeQueued {
			t.Fatalf("recorded publication = %#v, want the second drop recorded against the one re-arm", redropped.PullRequest)
		}
		if local := publishedCommit(t, run.repository, "main"); local != run.outcome.BaseCommit {
			t.Errorf("local main = %q after a second drop, want the base %q", local, run.outcome.BaseCommit)
		}
		(&protectedRun{repository: run.repository, remote: run.remote}).assertMainNotAhead(t)

		// And not re-armed again: the decision already carried out buys no second
		// repeat, and a second decision is refused where it would be recorded.
		if _, err := run.rearmer(t).Rearm(context.Background(), RearmRequest{Run: pipelineRunID, Reason: rearmReasoning}); err == nil ||
			!strings.Contains(err.Error(), "escalation rather than another re-arm") {
			t.Fatalf("a second Rearm() error = %v, want it refused as an escalation", err)
		}
		if len(run.forge.MergeRequests()) != merges {
			t.Fatalf("merge requests after the second drop = %d, want none", len(run.forge.MergeRequests())-merges)
		}
		if _, err := run.store.Triage().RecordMergeRearm(context.Background(), run.tracker.Record().Item.ID, run.key,
			triageDecided(runstate.TriageDecisionRearm, pipelineRunID), time.Now().UTC(), rearmCaps); !errors.Is(err, runstate.ErrTriageCapReached) {
			t.Fatalf("a second decision error = %v, want the publication's cap refusing it", err)
		}
	})
}
