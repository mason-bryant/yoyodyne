package orchestrator

// Retiring a publication whose work landed by another vehicle.
//
// A run branch carries the run that published it, so an item attempted again
// publishes a new branch and opens a new pull request rather than reusing the
// first run's. Nothing revisited the first one: it sat open with a green build
// and no queued merge, indistinguishable from pending work until somebody asked
// why. That happened to the publications of runs killed and relaunched, and to
// the loser of a duplicate selection, and it took a person going looking each
// time.
//
// Closing one is not a decision. The work the request was opened for is on the
// target branch by another vehicle — the harness's own promotion, recorded — so
// a request that will never merge is left open by omission rather than on
// purpose, and what makes the forge's open list honest is closing it with the
// vehicle named. Nothing here judges the change, reopens anything, or touches a
// request that merged.
//
// Two places know it and both retire the publication through here. The
// convergence sweep finds every such publication whatever left it behind, which
// is what covers the ones nobody was present for; the re-run knows at the moment
// it retires what the stopped run held, which is what keeps the list honest
// between sweeps.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// SupersededPublications is the forge access retiring a publication needs:
// closing a pull request that will never merge, with a comment saying where its
// work went. It can close and comment and nothing else — in particular it cannot
// merge, which keeps this the same distance from a promotion that every other
// harness-owned forge call is.
//
// It is satisfied by publish.GitHub.
type SupersededPublications interface {
	Close(ctx context.Context, request publish.CloseRequest) (publish.Closure, error)
}

// SupersededBranches is the repository access it needs: removing the branch the
// closed request carried. The deletion is a compare-and-swap on the exact commit
// the harness published, so a remote branch that moved is left for a person.
//
// It is satisfied by gitworktree.Manager.
type SupersededBranches interface {
	DeleteRemoteBranch(ctx context.Context, worktree gitworktree.Worktree, commit string) error
}

// Supersession is the vehicle a publication's work actually landed by: the run
// that landed it, the commit that reached the target branch, and the pull
// request the forge merged where there was one.
type Supersession struct {
	RunID        string `json:"run_id"`
	Commit       string `json:"commit"`
	TargetBranch string `json:"target_branch"`
	Number       int    `json:"number,omitempty"`
	URL          string `json:"url,omitempty"`
}

// PublicationRetirement is what became of one superseded publication. Closed
// and BranchDeleted are reported apart because they cannot be done atomically
// and because a reader acts on them differently: a request still open is a false
// signal on the forge, and a branch still there is debris.
type PublicationRetirement struct {
	Number        int    `json:"number"`
	URL           string `json:"url,omitempty"`
	Branch        string `json:"branch"`
	Closed        bool   `json:"closed"`
	BranchDeleted bool   `json:"branch_deleted"`
	Failure       string `json:"failure,omitempty"`
}

// supersessionOf reads the vehicle from a recorded run whose work landed, and
// supersessionOfOutcome from a run that has just finished. Both go through
// supersession, so a run read from the store and a run still in hand describe
// the same landing the same way.
//
// A run that integrated nothing supersedes nothing, and the empty vehicle says
// so: Landed reports false, and nothing is retired on it.
func supersessionOf(landed runstate.State) Supersession {
	if landed.Integration == nil {
		return Supersession{}
	}
	return supersession(landed.RunID, landed.Integration.SourceCommit, landed.Integration.TargetBranch, landed.PullRequest)
}

func supersessionOfOutcome(outcome Outcome) Supersession {
	if outcome.Integration == nil {
		return Supersession{}
	}
	return supersession(outcome.RunID, outcome.Integration.SourceCommit, outcome.Integration.TargetBranch, outcome.PullRequest)
}

// supersession builds the vehicle from the four things that describe it.
//
// The pull request is named only where it is the vehicle. A landing run whose
// own request the forge has not merged has published nothing to send anybody
// to — its merge may still be queued, or may have been dropped — while the
// promoted commit is on the target branch either way, and is what landed the
// work.
func supersession(runID, commit, targetBranch string, published *runstate.PullRequest) Supersession {
	by := Supersession{RunID: runID, Commit: commit, TargetBranch: targetBranch}
	if published != nil && published.Merged {
		by.Number = published.Number
		by.URL = published.URL
	}
	return by
}

// Landed reports the vehicle being one this can name. A run that integrated
// nothing supersedes nothing, and closing somebody's pull request on that would
// be an assertion rather than the recorded promotion this is allowed to act on.
func (s Supersession) Landed() bool {
	return s.RunID != "" && s.Commit != "" && s.TargetBranch != ""
}

// Vehicle names in one line what the work landed by, which is what the closing
// comment leads with and what the run's record keeps.
func (s Supersession) Vehicle() string {
	if s.Number > 0 {
		return fmt.Sprintf("pull request #%d, which the forge merged for run %s", s.Number, s.RunID)
	}
	return fmt.Sprintf("commit %s on %s, integrated by run %s", s.Commit, s.TargetBranch, s.RunID)
}

// retirablePublication reports a run holding a pull request that will never
// merge and that nothing has retired yet.
//
// Every clause is doing work. The run ended, so nothing is still going to
// promote from it. It integrated nothing, so the request carries work no
// promotion of its own ever took — a run that did integrate and whose merge the
// forge then dropped is the opposite case and belongs to a person. The request
// is not merged, so there is something still open. And nothing has retired it
// already, which is what keeps a sweep from asking the forge about the same
// request forever.
func retirablePublication(state runstate.State) bool {
	published := state.PullRequest
	return state.Status.Terminal() &&
		state.Integration == nil &&
		published != nil &&
		!published.Merged &&
		strings.TrimSpace(published.Superseded) == ""
}

// landedAfter reports the landing run having got to its integration after some
// moment. A run still finishing its own publication has no completion time yet,
// so what it last recorded stands in for one: it has integrated either way,
// which is the fact this is about.
func landedAfter(landed runstate.State, moment time.Time) bool {
	at := landed.UpdatedAt
	if landed.CompletedAt != nil {
		at = *landed.CompletedAt
	}
	return at.After(moment)
}

// retirePublication closes one superseded run's pull request and deletes the
// branch it published. It reports what it managed rather than failing: the work
// itself is on the target branch, so a request that could not be closed is a
// false signal for somebody to read rather than anything at risk.
//
// The close comes first because it is the half a person sees. A branch left
// behind a closed request is debris on the remote; a branch deleted under a
// request still open is a pull request nobody can even read the diff of.
func retirePublication(ctx context.Context, forge SupersededPublications, branches SupersededBranches, superseded runstate.State, by Supersession) PublicationRetirement {
	published := *superseded.PullRequest
	retirement := PublicationRetirement{Number: published.Number, URL: published.URL, Branch: published.Branch}
	closure, err := forge.Close(ctx, publish.CloseRequest{
		Head:    published.Branch,
		Number:  published.Number,
		Comment: renderSupersededComment(superseded, by),
	})
	if err != nil {
		retirement.Failure = fmt.Errorf("close the superseded pull request %d of run %s: %w", published.Number, superseded.RunID, err).Error()
		return retirement
	}
	retirement.Closed = closure.Closed
	// The record said unmerged and the forge says merged, so this publication's
	// own work is on the remote after all. Nothing was closed — the forge is asked
	// before anything is — and the branch stays, because deleting it would retire
	// a merge two records now disagree about.
	if closure.Merged {
		retirement.Failure = fmt.Sprintf(
			"pull request %d of run %s is merged at the forge, so its own work reached the remote and it is not superseded; run %s is recorded as having integrated %s into %s",
			published.Number, superseded.RunID, by.RunID, by.Commit, by.TargetBranch)
		return retirement
	}
	if err := branches.DeleteRemoteBranch(ctx, worktreeOf(superseded), published.HeadCommit); err != nil {
		retirement.Failure = fmt.Errorf("delete the branch pull request %d published: %w", published.Number, err).Error()
		return retirement
	}
	retirement.BranchDeleted = true
	return retirement
}

// renderSupersededComment is what the closed pull request is left carrying. It
// says which vehicle the work landed by and how to check that for yourself,
// because a request closed by a machine with no account of why is the same
// unexplained state as one left open.
func renderSupersededComment(superseded runstate.State, by Supersession) string {
	lines := []string{
		fmt.Sprintf("Yoyodyne closed this pull request: the work it was opened for landed by %s, so nothing here is pending review or waiting to merge.", by.Vehicle()),
		"",
		"- Work item: `" + superseded.WorkItemID + "`",
		"- This publication: run `" + superseded.RunID + "` on branch `" + superseded.PullRequest.Branch + "`",
		fmt.Sprintf("- Landed instead: run `%s`, commit `%s` on `%s`", by.RunID, by.Commit, by.TargetBranch),
	}
	if by.URL != "" {
		lines = append(lines, "- Superseding pull request: "+by.URL)
	}
	return strings.Join(append(lines, "",
		"The branch this request carries is being deleted with it. Nothing on that branch was promoted — run `"+superseded.RunID+"` ended without integrating anything — and the item's work reached the target branch by the vehicle above.",
	), "\n")
}
