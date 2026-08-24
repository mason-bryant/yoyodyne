package cli

// Where a work item's change actually is, read from the harness's own run
// records. It is the half of the substrate gate that establishes the fact; what
// a conversation does with the fact is the chat package's.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

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

	store := substrateRunRecord(t, t.TempDir(), func(state *runstate.State) {
		state.HarnessCommit = substrateCommit
		state.PullRequest = &runstate.PullRequest{
			Remote:     "origin",
			Branch:     state.Branch,
			Number:     174,
			URL:        "https://example.invalid/pull/174",
			HeadCommit: substrateCommit,
		}
	})
	unlanded, found, err := conversationStoppedRuns{store: store}.UnlandedChange(context.Background(), substrateItem)
	if err != nil {
		t.Fatalf("UnlandedChange() error = %v", err)
	}
	if !found {
		t.Fatal("a stopped run's unmerged change was not reported as unlanded")
	}
	if unlanded.Branch != "yoyodyne/yoyodyne-ifd.100" || unlanded.TargetBranch != "main" || unlanded.PullRequest != 174 {
		t.Fatalf("unlanded = %#v", unlanded)
	}
}

// Everything else the same records can say reports nothing, because each of
// them is a child standing on something. They are one table because the point is
// the boundary rather than any one of them: a gate that fired on ordinary work
// would hold decompositions with no substrate problem at all.
func TestWorkWithNothingUnlandedIsReportedAsSuch(t *testing.T) {
	t.Parallel()

	for name, prepare := range map[string]func(*runstate.State){
		"a promotion leaves the change on the target branch": func(state *runstate.State) {
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
		},
		"a run that produced nothing has no substrate to be missing": func(state *runstate.State) {
			state.Changes = nil
		},
		"a run still going has not failed to land anything yet": func(state *runstate.State) {
			state.HarnessCommit = substrateCommit
			state.Status = runstate.StatusRunning
			state.CompletedAt = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := substrateRunRecord(t, t.TempDir(), prepare)
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

// substrateItem is the parent of the founding case.
const substrateItem = "yoyodyne-ifd.100"

// substrateRunRecord writes one run for that item and lets the caller say what
// became of its change. The default is the shape the founding case had: ended on
// a blocker, with a change recorded and nothing promoted.
func substrateRunRecord(t *testing.T, stateRoot string, prepare func(*runstate.State)) *runstate.Store {
	t.Helper()

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	completed := now
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
		Branch:        "yoyodyne/" + substrateItem,
		BaseCommit:    substrateBase,
		TargetBranch:  "main",
		Blocker:       "the repair budget was spent",
		Changes:       runstate.RecordChanges("A\tinternal/artifact/write.go", " internal/artifact/write.go | 40 ++++"),
		StartedAt:     now,
		UpdatedAt:     now,
		CompletedAt:   &completed,
	}
	prepare(&state)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return store
}
