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
// So the dependency is recorded by the harness rather than remembered by
// whoever is decomposing. Which items a child waits for is the development
// manager's, and this adds the one it cannot see: what the harness itself
// recorded about where the parent's change actually is. However that change
// lands — the preserved branch cherry-picked, the pull request revived, the
// substrate rebuilt from nothing — the parent is the item that says so, and the
// child comes free with it.
//
// It gates rather than refuses, deliberately. A parent whose change has not
// landed is exactly the work most in need of being broken down, and a
// decomposition refused until the change landed would refuse the decision the
// development manager was reading the docket to make.
//
// It is on creation and not on reparenting, which names a parent too. What the
// gate rests on is that the child's text was written just now, against the
// change the role decomposing is looking at. An item moved under a new parent
// was written earlier, under circumstances the move says nothing about, so
// linking it would assert a substrate dependency there is no evidence for.
// Reparenting under work whose change has not landed therefore adds no link and
// no clause, and the development manager records that dependency itself where it
// is real, exactly as it records every other one.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// UnlandedChange is a work item's own change that the harness recorded and that
// never reached the integration target. Branch is where the change actually is,
// and PullRequest names its publication where there was one: which vehicle
// lands it is a decision somebody still has to make, and those two are what they
// make it from.
type UnlandedChange struct {
	RunID        string
	Branch       string
	TargetBranch string
	PullRequest  int
}

// describe says where a parent's change is, in the clause the operator reads and
// the item itself records. It names the run as well as the item, because the run
// is what a docket entry about the same stoppage names, and somebody reading one
// beside the other must not have to work out that they are about the same thing.
func (u UnlandedChange) describe(parent string) string {
	target := strings.TrimSpace(u.TargetBranch)
	if target == "" {
		target = "the integration target"
	}
	described := fmt.Sprintf("the change run %s made for %s never reached %s", u.RunID, parent, target)
	if branch := strings.TrimSpace(u.Branch); branch != "" {
		described += ", and is on " + branch
	}
	if u.PullRequest > 0 {
		described += fmt.Sprintf(", published as pull request #%d", u.PullRequest)
	}
	return described
}

// gateOnParentSubstrate makes a child of work whose change has not landed wait
// for that change, and says in one clause what it did. It is silent — the empty
// clause — for the ordinary decomposition of work that has no unlanded change
// behind it, which is nearly all of it, and for a conversation with no run
// records wired, which can establish nothing about where anything is.
//
// The link is recorded first because it is the half that decides readiness. The
// note after it is what the item says about itself, and it is the half the
// original failure was actually about: an item that waits for its parent and
// does not say why reads as sequencing, and what a reader has to know is that
// the files it names are not there yet.
func (s *Session) gateOnParentSubstrate(ctx context.Context, parent, child string) string {
	parent = strings.TrimSpace(parent)
	if parent == "" || s.options.Stoppages == nil {
		return ""
	}
	unlanded, unlandedFound, err := s.options.Stoppages.UnlandedChange(ctx, parent)
	switch {
	case err != nil:
		return fmt.Sprintf("; nothing could be read about where %s's own change is, so %s was not held for it and may be pulled without its substrate: %s",
			parent, child, singleLine(err.Error(), maxTrackerFailureBytes))
	case !unlandedFound:
		return ""
	}
	where := unlanded.describe(parent)
	if err := s.options.Tracker.AddBlocker(ctx, child, parent); err != nil {
		return fmt.Sprintf("; %s, and %s could not be linked to wait for %s, so %s: %s",
			where, child, parent, s.holdUngatedChild(ctx, child, parent, where), singleLine(err.Error(), maxTrackerFailureBytes))
	}
	if _, err := s.options.Tracker.Update(ctx, child, beads.WorkItemChange{AppendNotes: s.substrateNote(parent, child, where)}); err != nil {
		return fmt.Sprintf("; it waits for %s, because %s; the item itself was not told why: %s",
			parent, where, singleLine(err.Error(), maxTrackerFailureBytes))
	}
	return fmt.Sprintf("; it waits for %s, because %s", parent, where)
}

// holdUngatedChild is the second attempt at the one thing this must not leave
// undone. A child written against substrate that is not there, with nothing
// holding it back, is exactly the item the tracker offers as the next thing to
// pull — so a durable blocker on the item says it on the item itself, where no
// dependency graph is needed to read it. A tracker that will not take that
// either is said out loud rather than passed over: the item is in the queue, and
// somebody has to know it is in there unheld.
func (s *Session) holdUngatedChild(ctx context.Context, child, parent, where string) string {
	if _, err := s.options.Tracker.Block(ctx, child, s.substrateNote(parent, child, where)); err != nil {
		return "nothing holds it back and it may be pulled without its substrate"
	}
	return "it was blocked on the item itself instead"
}

// substrateNote is what the child records about the substrate it is waiting for.
// It is written as the harness's own rather than through the provenance every
// other note carries, because no role asked for it: the development manager
// decomposed, and this is the harness adding what its own records say.
func (s *Session) substrateNote(parent, child, where string) string {
	return fmt.Sprintf(
		"Held for %s's change to land, recorded by the harness as %s was created under it in conversation %s, after turn %d.\n\nReason: %s.\n\nIt comes free however that change lands: the preserved branch cherry-picked, the pull request revived, or the substrate rebuilt.",
		parent, child, s.state.ConversationID, s.state.Turns, where)
}
