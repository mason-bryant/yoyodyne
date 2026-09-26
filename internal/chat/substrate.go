package chat

// The substrate a decomposition's children stand on.
//
// Work carved out of a run that failed is written against the change that run
// made, and that change is on the branch the run preserved rather than on the
// branch a fresh worktree is cut from. On 2026-08-23 a child was carved from a
// failed run's deferral assuming two files that existed only on that run's
// branch, with the pull request for it still open and the item's repair budget
// spent. Nothing on the child recorded the prerequisite, so the tracker
// reported it ready, and the next run to pull it would have started in a
// worktree that had neither file.
//
// So the harness records, on the child itself, where the parent's change is:
// the run that made it, its branch and commit, and the pull request that
// published it. That is guidance, and it holds nothing. Which children wait for
// the change is what the child says about itself. One that says in its own
// text that it builds on its parent's change — "builds on the parent's change",
// or the parent named — is held by the scheduler until that change lands
// (backlog.Holds.OnUnlandedParents), however it lands: a later run of the
// parent promoting it, or the parent closing on the preserved branch
// cherry-picked, the pull request revived, or the substrate rebuilt. One that
// says nothing, which is what a child superseding the parent's change is, is
// not held at all.
//
// It is not a dependency link and never a blocked status. The tracker refuses a
// link from a child onto its own parent, because the child already hangs on it,
// and what the harness did about that refusal was to set the child blocked on
// the item itself — a blocker nothing ever cleared. On 2026-09-25 that blocked
// all six children of yoyodyne-ifd.429.13, which superseded the parent's pull
// request 757 rather than building on its files and should not have waited at
// all, and it would have done the same to every re-scope of work whose change
// never landed.
//
// It gates rather than refuses, deliberately. A parent whose change has not
// landed is exactly the work most in need of being broken down, and a
// decomposition refused until the change landed would refuse the decision the
// development manager was reading the docket to make.
//
// It is on creation and not on reparenting, which names a parent too. What the
// guidance rests on is that the child's text was written just now, against the
// change the role decomposing is looking at. An item moved under a new parent
// was written earlier, under circumstances the move says nothing about, so
// recording guidance there would assert a substrate there is no evidence for.
// The hold itself reads the item's text wherever its parent is, so an item
// moved under unlanded work that says it builds on that work is held all the
// same.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// UnlandedChange is a work item's own change that the harness recorded and that
// never reached the integration target. Branch and Commit are where the change
// actually is, and PullRequest names its publication where there was one: which
// vehicle lands it is a decision somebody still has to make, and those are what
// they make it from.
type UnlandedChange struct {
	RunID        string
	Branch       string
	Commit       string
	TargetBranch string
	PullRequest  int
}

// describe says where a parent's change is, in the clause the operator reads and
// the item itself records. It is the account the scheduler gives for a child it
// holds on the change (readmodel.UnlandedAccount), so the guidance and the hold
// name the same run, branch, commit, and pull request in the same words. It
// names the run as well as the item, because the run is what a docket entry
// about the same stoppage names.
func (u UnlandedChange) describe(parent string) string {
	run := runstate.State{
		RunID:         u.RunID,
		WorkItemID:    parent,
		Branch:        u.Branch,
		HarnessCommit: u.Commit,
		TargetBranch:  u.TargetBranch,
	}
	if u.PullRequest > 0 {
		run.PullRequest = &runstate.PullRequest{Number: u.PullRequest}
	}
	return readmodel.UnlandedAccount(run)
}

// gateOnParentSubstrate records on a child of work whose change has not landed
// where that change is, and says in one clause whether the child is held for it.
// It is silent — the empty clause — for the ordinary decomposition of work that
// has no unlanded change behind it, which is nearly all of it, and for a
// conversation with no run records wired, which can establish nothing about
// where anything is.
//
// It never links and never blocks. The child is left open; whether the
// scheduler holds it is what the child's own text says (backlog.BuildsOnParent),
// and the clause tells the role decomposing which it is and how to say the
// other, so a child it meant to wait is not left pullable by an omission.
func (s *Session) gateOnParentSubstrate(ctx context.Context, parent string, child beads.WorkItem) string {
	parent = strings.TrimSpace(parent)
	if parent == "" || s.options.Stoppages == nil {
		return ""
	}
	unlanded, unlandedFound, err := s.options.Stoppages.UnlandedChange(ctx, parent)
	switch {
	case err != nil:
		return fmt.Sprintf("; nothing could be read about where %s's own change is, so %s was not told where it is and is not held for it: %s",
			parent, child.ID, singleLine(err.Error(), maxTrackerFailureBytes))
	case !unlandedFound:
		return ""
	}
	where := unlanded.describe(parent)
	held := backlog.BuildsOnParent(child, parent)
	holding := fmt.Sprintf("it is open and not held, because it does not say it builds on that change; if it does, put \"builds on %s's change\" in its description and the scheduler holds it until that change lands", parent)
	if held {
		holding = "it is open, and the scheduler holds it until that change lands, because it says it builds on it"
	}
	if _, err := s.options.Tracker.Update(ctx, child.ID, beads.WorkItemChange{AppendNotes: s.substrateNote(parent, child.ID, where)}); err != nil {
		return fmt.Sprintf("; %s; %s; the item itself was not told where that change is: %s",
			where, holding, singleLine(err.Error(), maxTrackerFailureBytes))
	}
	return fmt.Sprintf("; %s; %s; its notes say where that change is", where, holding)
}

// substrateNote is the guidance the child records about its parent's change. It
// is written as the harness's own rather than through the provenance every
// other note carries, because no role asked for it: the development manager
// decomposed, and this is the harness adding what its own records say.
func (s *Session) substrateNote(parent, child, where string) string {
	return fmt.Sprintf(
		"Guidance on %s's change, recorded by the harness as %s was created under it in conversation %s, after turn %d.\n\n%s.\n\nNothing here holds this item. If it builds on that change's files, its own text says so (\"builds on %s's change\") and the scheduler holds it until the change lands, however it lands: the preserved branch cherry-picked, the pull request revived, or the substrate rebuilt and %s closed. If it supersedes that change, it is not held, and the branch is only where to look.",
		parent, child, s.state.ConversationID, s.state.Turns, capitalized(where), parent, parent)
}

// capitalized opens a clause as a sentence.
func capitalized(clause string) string {
	if clause == "" {
		return clause
	}
	return strings.ToUpper(clause[:1]) + clause[1:]
}
