package cli

// Where a work item's change actually is, read from the harness's own run
// records. It is the half of the substrate gate that establishes the fact; what
// a conversation does with the fact is the chat package's.

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// substrateItem is the parent of the founding case.
const substrateItem = "yoyodyne-ifd.100"

// substrateCommit and substrateBase are the two commits one recorded run needs.
// They differ because a harness commit equal to the base proves no commit was
// made, which the record refuses.
const (
	substrateCommit = "abcdef0123456789abcdef0123456789abcdef01"
	substrateBase   = "0123456789abcdef0123456789abcdef01234567"
)

// The founding case: a run that ended on a blocker with its change committed,
// published, and never promoted. A child carved out of it is written against
// files that are on that branch and nowhere else.
func TestAStoppedRunsUnmergedChangeIsReportedAsUnlanded(t *testing.T) {
	t.Parallel()

	store := substrateRuns(t, publishedAndUnmerged)
	unlanded, found, err := conversationStoppedRuns{store: store}.UnlandedChange(context.Background(), substrateItem)
	if err != nil {
		t.Fatalf("UnlandedChange() error = %v", err)
	}
	if !found {
		t.Fatal("a stopped run's unmerged change was not reported as unlanded")
	}
	if unlanded.Branch != substrateBranch(1) || unlanded.TargetBranch != "main" || unlanded.PullRequest != 174 {
		t.Fatalf("unlanded = %#v", unlanded)
	}
}

// The run that answers where the change is need not be the item's newest one. A
// re-run that wrote nothing, and a re-run still going, each say nothing at all
// about where the work is, and what stands is what the run before them left.
// Reading either of them as "nothing is missing" is what carves a child against
// a branch nobody named — the same failure this whole gate is for, one run
// further back.
func TestAnEarlierRunsUnlandedChangeSurvivesTheRunsAfterIt(t *testing.T) {
	t.Parallel()

	for name, after := range map[string]func(*runstate.State){
		"a re-run that produced nothing": producedNothing,
		"a re-run still in flight":       stillInFlight,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := substrateRuns(t, publishedAndUnmerged, after)
			unlanded, found, err := conversationStoppedRuns{store: store}.UnlandedChange(context.Background(), substrateItem)
			if err != nil {
				t.Fatalf("UnlandedChange() error = %v", err)
			}
			if !found {
				t.Fatal("an earlier run's unlanded change was lost behind a later run that said nothing")
			}
			// The answer names the run that made the change rather than the one in
			// front of it, because that is the branch somebody has to land.
			if unlanded.Branch != substrateBranch(1) || unlanded.PullRequest != 174 {
				t.Fatalf("unlanded = %#v, want the change run 1 left on %s", unlanded, substrateBranch(1))
			}
		})
	}
}

// The other direction of the same walk: a promotion ends it. Once any run of the
// item has put the work on the target branch, nothing an earlier run left on a
// branch is missing any more.
func TestAPromotionEndsTheWalkPastEarlierRuns(t *testing.T) {
	t.Parallel()

	store := substrateRuns(t, publishedAndUnmerged, promoted)
	_, found, err := conversationStoppedRuns{store: store}.UnlandedChange(context.Background(), substrateItem)
	if err != nil {
		t.Fatalf("UnlandedChange() error = %v", err)
	}
	if found {
		t.Fatal("an item whose work was promoted was reported as having a change off the target branch")
	}
}

// Everything else one run can say reports nothing, because each of them is a
// child standing on something. They are one table because the point is the
// boundary rather than any one of them: a gate that fired on ordinary work would
// hold decompositions with no substrate problem at all.
func TestWorkWithNothingUnlandedIsReportedAsSuch(t *testing.T) {
	t.Parallel()

	for name, prepare := range map[string]func(*runstate.State){
		"a promotion leaves the change on the target branch":         promoted,
		"a run that produced nothing has no substrate to be missing": producedNothing,
		"a run still going has not failed to land anything yet":      stillInFlight,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := substrateRuns(t, prepare)
			_, found, err := conversationStoppedRuns{store: store}.UnlandedChange(context.Background(), substrateItem)
			if err != nil {
				t.Fatalf("UnlandedChange() error = %v", err)
			}
			if found {
				t.Fatal("an item with nothing unlanded was reported as having a change off the target branch")
			}
		})
	}
}

// Work the harness has never run is a plain answer about the item rather than a
// failure to look, which is what nearly every decomposition asks about.
func TestWorkTheHarnessNeverRanHasNothingUnlanded(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	_, found, err := conversationStoppedRuns{store: store}.UnlandedChange(context.Background(), substrateItem)
	if err != nil {
		t.Fatalf("UnlandedChange() error = %v", err)
	}
	if found {
		t.Fatal("work with no recorded run was reported as having an unlanded change")
	}
}

// The four shapes a run of one item leaves behind. The default the helper builds
// is the founding case before publishing — ended on a blocker with a change
// recorded and nothing promoted — and each of these is what one run did to that.

// publishedAndUnmerged is a stopped run whose change the harness committed and
// pushed as a pull request the forge never merged.
func publishedAndUnmerged(state *runstate.State) {
	state.HarnessCommit = substrateCommit
	state.PullRequest = &runstate.PullRequest{
		Remote:     "origin",
		Branch:     state.Branch,
		Number:     174,
		URL:        "https://example.invalid/pull/174",
		HeadCommit: substrateCommit,
	}
}

// producedNothing is a run that reached a terminal state having written no code,
// which records no change and makes no commit.
func producedNothing(state *runstate.State) {
	state.Changes = nil
}

// stillInFlight is a run a live process owns, which has not failed to land
// anything yet.
func stillInFlight(state *runstate.State) {
	state.HarnessCommit = substrateCommit
	state.Status = runstate.StatusRunning
	state.CompletedAt = nil
}

// promoted is a run that put the work on the target branch, with the approval
// and the two independent invocations the record holds a promotion to.
func promoted(state *runstate.State) {
	state.Status = runstate.StatusSucceeded
	state.Phase = runstate.PhaseIntegrating
	state.HarnessCommit = substrateCommit
	state.Blocker = ""
	state.ProviderSessionID = "session-developer"
	state.ProviderModel = "opus"
	state.ReviewSessionID = "session-reviewer"
	state.ReviewModel = "opus"
	state.ReviewDecision = runstate.ReviewApprove
	state.Integration = &runstate.Integration{
		TargetBranch:         "main",
		SourceCommit:         substrateCommit,
		TargetCommit:         substrateCommit,
		PreviousTargetCommit: substrateBase,
	}
}

// substrateBranch names the branch of the nth run the helper writes, counting
// from one. They differ per run so that an assertion about the answer says which
// run the answer came from.
func substrateBranch(position int) string {
	return fmt.Sprintf("yoyodyne/%s-%d", substrateItem, position)
}

// substrateRuns writes the given runs for the item, oldest first, and returns
// the store holding them. Each is started a minute after the one before it, so
// the order the walk reads them in is a fact about the records rather than about
// the order this wrote them.
func substrateRuns(t *testing.T, prepares ...func(*runstate.State)) *runstate.Store {
	t.Helper()

	stateRoot := t.TempDir()
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	oldest := time.Now().UTC().Truncate(time.Second).Add(-time.Duration(len(prepares)) * time.Minute)
	for index, prepare := range prepares {
		runID, err := runstate.NewRunID()
		if err != nil {
			t.Fatalf("NewRunID() error = %v", err)
		}
		at := oldest.Add(time.Duration(index) * time.Minute)
		completed := at
		state := runstate.State{
			SchemaVersion: runstate.StateSchemaVersion,
			RunID:         runID,
			ProductID:     domain.ProductID("yoyodyne"),
			RepositoryID:  "yoyodyne",
			WorkItemID:    substrateItem,
			Backend:       domain.BackendClaudeCode,
			Status:        runstate.StatusFailed,
			Phase:         runstate.PhaseReviewing,
			WorktreePath:  filepath.Join(stateRoot, "worktrees", runID),
			Branch:        substrateBranch(index + 1),
			BaseCommit:    substrateBase,
			TargetBranch:  "main",
			Blocker:       "the repair budget was spent",
			Changes:       runstate.RecordChanges("A\tinternal/artifact/write.go", " internal/artifact/write.go | 40 ++++"),
			StartedAt:     at,
			UpdatedAt:     at,
			CompletedAt:   &completed,
		}
		prepare(&state)
		if err := store.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
	return store
}
