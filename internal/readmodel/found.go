package readmodel

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// Look is the one way a surface finds out what a stopped run left: asked of the
// repository as the surface writes, never read off the run's removal flags.
// The hold the pull reads, the claim audit and the note it writes, the triage
// docket, and `yoyo status` all take one of these, so none of them can answer
// the question a way the others do not.
type Look func(run runstate.State) triage.Found

// Looking is the look one reading makes. A run that recorded neither a branch
// nor a worktree has nothing to look for and is answered without asking.
//
// remains may be nil, for a reading wired without a repository. It then answers
// from what the run's record says survived, and says in the answer that nothing
// looked, so no surface can pass a record off as a check. A look that fails
// answers nothing and says why, and triage.Found.Holds treats that as a change
// that may still be there.
func Looking(ctx context.Context, remains Remains, now func() time.Time) Look {
	if now == nil {
		now = time.Now
	}
	return func(run runstate.State) triage.Found {
		found := triage.Found{At: now(), Branch: run.Branch, WorktreePath: run.WorktreePath}
		if !found.Recorded() {
			return found
		}
		if remains == nil {
			recorded := run.Artifacts()
			found.BranchThere = recorded.Branch != "" && !recorded.BranchRemoved
			found.WorktreeThere = recorded.WorktreePath != "" && !recorded.WorktreeRemoved
			found.Unchecked = "nothing was wired to look in the repository"
			return found
		}
		survives, err := remains.Survives(ctx, gitworktree.Worktree{
			RunID:         run.RunID,
			WorkItemID:    run.WorkItemID,
			Path:          run.WorktreePath,
			Branch:        run.Branch,
			BaseCommit:    run.BaseCommit,
			TargetBranch:  run.TargetBranch,
			HarnessCommit: run.HarnessCommit,
		})
		if err != nil {
			found.Unknown = true
			found.Unchecked = boundUnchecked(fmt.Sprintf("the repository could not be asked (%v)", err))
			return found
		}
		found.BranchThere = run.Branch != "" && survives.BranchExists
		found.WorktreeThere = run.WorktreePath != "" && survives.WorktreePresent
		return found
	}
}

// LookFor is Looking for one run.
func LookFor(ctx context.Context, remains Remains, run runstate.State) triage.Found {
	return Looking(ctx, remains, nil)(run)
}

// Recorded is the durable run state a listing looks the runs up in. It is
// satisfied by *runstate.Store.
type Recorded interface {
	Recorded() ([]runstate.State, error)
}

// LookForSummaries puts what the repository holds onto every summarized run
// that is over and did not succeed, which is the set `yoyo status` says
// preservation about. The summary names the run but not its base commit, which
// the repository's ownership check asks for, so each is looked for through the
// run's own record; where those records cannot be read the summaries say that
// nothing could be looked for, rather than falling back on the flags unsaid.
func LookForSummaries(ctx context.Context, remains Remains, recorded Recorded, summaries []runstate.RunSummary) {
	byID := map[string]runstate.State{}
	runs, err := recorded.Recorded()
	for _, run := range runs {
		byID[run.RunID] = run
	}
	look := Looking(ctx, remains, nil)
	for index := range summaries {
		summary := &summaries[index]
		if !summary.Status.Terminal() || summary.Outcome == runstate.OutcomeSucceeded {
			continue
		}
		run, known := byID[summary.RunID]
		if !known {
			if err == nil {
				continue
			}
			summary.Found = &triage.Found{
				At:           time.Now(),
				Branch:       summary.Branch,
				WorktreePath: summary.WorktreePath,
				Unknown:      true,
				Unchecked:    boundUnchecked(fmt.Sprintf("the run records could not be read to look for it (%v)", err)),
			}
			continue
		}
		found := look(run)
		summary.Found = &found
	}
}

func boundUnchecked(reason string) string {
	if len(reason) <= triage.MaxFoundUncheckedBytes {
		return reason
	}
	cut := triage.MaxFoundUncheckedBytes - len("...")
	for cut > 0 && !utf8.RuneStart(reason[cut]) {
		cut--
	}
	return reason[:cut] + "..."
}
