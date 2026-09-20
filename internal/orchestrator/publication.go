package orchestrator

// Re-asking the forge about a publication whose run is over.
//
// A run that ended without integrating keeps whatever the forge last said about
// its pull request at the moment it ended, and until now nothing ever asked
// again. settleQueuedMerge covers the one publication a finished run is owed an
// answer about — a merge the forge accepted and had not yet performed — and it
// is deliberately narrow: it settles the whole run on that answer, which is only
// a thing to do for a run that promoted something. Every other published run
// simply keeps its death-moment record while the forge moves on without it, so a
// request that was merged by hand days later stays recorded open and unmerged
// for good.
//
// That record is evidence other things act on. The triage docket asks whether a
// publication merged before it dockets it as stuck, the status surfaces print
// what it says, and a sweep over the requests the harness left open reads it to
// decide which of them is still an orphan. A record frozen at a run's death makes
// every one of those wrong in the same direction, and nothing corrects it.
//
// So this sweep asks. It only ever reads the forge and writes the run's own
// publication record: it merges nothing, closes nothing, moves no branch, and
// touches neither the work item nor anything the run promoted. What this is for
// is that the record and the forge agree, so that every decision downstream is
// made on a record that is true.
//
// # Finishing what the record says is unfinished
//
// A publication recorded as merged and still outstanding is the other half, and
// FinishPublications is what finishes it. Three records have that shape. A merge
// the harness could not confirm when it landed — which until yoyodyne-ifd.357 was
// every merge that landed among others, because confirmation demanded that the
// remote tip carry exactly the promotion's content, and only the last merge of a
// batch does. A merge the forge dropped that somebody then made by hand, which the
// refresh above records as merged and nothing else ever touched. And a confirmed
// merge whose consumed branch could not be deleted, which is a leftover on the
// forge that a person may since have removed.
//
// Each of those held its item out of the pull, counted as a promotion awaiting
// the forge, kept a `Publication outstanding` line on the item, and sat on the
// triage docket — for good, because nothing re-asked. docs/work.md said a hold
// lifts "by the publication being settled", and this is the lever behind that
// sentence: the remote is asked again whether it carries the promotion, and where
// it does the record is finished exactly as the settle path would have finished
// it — merge commit recorded, local target caught up, item settled by its own
// landing, consumed branch deleted, docket entry closed — and every surface that
// read the outstanding publication stops reading one.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// PublicationRefresh is what one recorded publication's state turned out to be.
// It reports both halves of the comparison rather than the answer alone, because
// what a reader acts on is the disagreement: a record that already agreed with
// the forge is the ordinary case and says nothing.
type PublicationRefresh struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Number     int    `json:"number"`
	URL        string `json:"url,omitempty"`
	// Recorded is what the run's record said before the forge was asked. State
	// and Merged are what the forge answered, and are empty on a publication that
	// could not be asked about at all.
	Recorded string `json:"recorded_state,omitempty"`
	State    string `json:"state,omitempty"`
	Merged   bool   `json:"merged"`
	// Updated reports the record having actually been rewritten, which separates
	// a stale record this corrected from one that was already true.
	Updated bool `json:"updated"`
	// Kept is why a record the forge answered about was deliberately left where
	// it stands. It is not a failure: a run a live process holds and a branch the
	// forge answers about with some other request are both correct outcomes, and
	// reporting them as failures would make every sweep of them look broken.
	Kept    string `json:"kept,omitempty"`
	Failure string `json:"failure,omitempty"`
}

// RefreshPublications asks the forge what became of every publication the
// harness recorded and nothing has settled, and writes the answer onto the run
// that made it.
//
// One publication that cannot be refreshed never stops the sweep, for the reason
// one unreconcilable run does not: a record nobody could correct is reported
// beside every record that was, so an operator reading the sweep sees the whole
// of it.
func (r Reconciler) RefreshPublications(ctx context.Context) ([]PublicationRefresh, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	recorded, err := r.Store.Recorded()
	if err != nil {
		return nil, fmt.Errorf("discover recorded runs: %w", err)
	}
	unsettled := make([]runstate.State, 0, len(recorded))
	for _, state := range recorded {
		if unsettledPublication(state) {
			unsettled = append(unsettled, state)
		}
	}
	refreshed := make([]PublicationRefresh, 0, len(unsettled))
	if len(unsettled) == 0 {
		return refreshed, nil
	}
	// A project that publishes always has forge access wired, so this is a
	// harness that was assembled wrong rather than a project that never published:
	// there are records here whose truth is on a forge nothing can reach. It is
	// reported once rather than as a failure per record, because it is one fact
	// about the wiring rather than one about each publication.
	if r.Publisher == nil {
		return refreshed, fmt.Errorf(
			"%d recorded publication(s) are unsettled, and reconciliation has no forge access to ask what became of them", len(unsettled))
	}
	for _, state := range unsettled {
		refreshed = append(refreshed, r.refreshPublication(ctx, state))
	}
	return refreshed, nil
}

// unsettledPublication reports a recorded publication whose state can still be
// wrong and that nothing else in the sweep will ask about.
//
// Three things put a record out of reach, each for its own reason. A run that
// published nothing has nothing to ask about. A run that still owes a step is
// reconciliation's own, and the one thing it can owe about a publication — a
// merge the forge queued — is settled there as part of settling the whole run,
// so asking here as well would be two paths deciding one merge. And a request
// the record already has as merged is finished: merged is the one answer a forge
// does not take back, which makes it the one that ends the asking rather than a
// question every later sweep repeats.
func unsettledPublication(state runstate.State) bool {
	published := state.PullRequest
	if published == nil || state.Outstanding() {
		return false
	}
	return !published.Merged
}

// refreshPublication asks about one run's pull request and records the answer
// under that run's own lease, which is what keeps the reading and the write one
// act: the record that is rewritten is the record that was read, so a sweep
// settling the same run beside this cannot lose either half.
func (r Reconciler) refreshPublication(ctx context.Context, recorded runstate.State) PublicationRefresh {
	published := *recorded.PullRequest
	refresh := PublicationRefresh{
		RunID:      recorded.RunID,
		WorkItemID: recorded.WorkItemID,
		Number:     published.Number,
		URL:        published.URL,
		Recorded:   nonEmpty(published.State, "unrecorded"),
	}
	observed, err := r.Publisher.State(ctx, published.Branch)
	if err != nil {
		refresh.Failure = fmt.Errorf("ask the forge about pull request %d of run %s: %w",
			published.Number, recorded.RunID, err).Error()
		return refresh
	}
	// The forge is asked about the branch, because that is the durable handle a
	// published run keeps on its request. A different request answering for that
	// branch is a publication this record was never about, and rewriting the
	// record from it would put one request's state where another's belongs, which
	// is a worse record than the stale one this exists to fix.
	if observed.Number != published.Number {
		refresh.Kept = fmt.Sprintf("the forge reports pull request %d for branch %s, and run %s published request %d there",
			observed.Number, published.Branch, recorded.RunID, published.Number)
		return refresh
	}
	answered := refreshedPublication(published, observed)
	refresh.State = nonEmpty(answered.State, "unreported")
	refresh.Merged = answered.Merged
	// A record the forge agrees with is the ordinary outcome and is left
	// untouched, so a sweep over a long history writes nothing at all.
	if answered.Equal(published) {
		return refresh
	}

	state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		refresh.Kept = fmt.Sprintf("a live process holds run %s, so what the forge says about its publication is that process's to record", recorded.RunID)
		return refresh
	case err != nil:
		refresh.Failure = fmt.Errorf("adopt run %s to record what the forge says about pull request %d: %w",
			recorded.RunID, published.Number, err).Error()
		return refresh
	}
	defer lease.Release()

	// The record is re-read under the lease, so what is rewritten is what is on
	// disk now rather than what the listing showed. A run something settled in the
	// meantime has had its publication written by whatever settled it, and there
	// is nothing here left to correct.
	if !unsettledPublication(state) || state.PullRequest.Number != published.Number {
		refresh.Kept = fmt.Sprintf("run %s was settled while the forge was being asked, so its publication record is what settled it wrote", recorded.RunID)
		return refresh
	}
	current := refreshedPublication(*state.PullRequest, observed)
	if current.Equal(*state.PullRequest) {
		return refresh
	}
	state.PullRequest = &current
	state.UpdatedAt = r.clock().Now()
	if err := r.Store.Save(state); err != nil {
		refresh.Failure = fmt.Errorf("record what the forge says about pull request %d of run %s: %w",
			published.Number, recorded.RunID, err).Error()
		return refresh
	}
	refresh.Updated = true
	return refresh
}

// refreshedPublication is the record as the forge's answer leaves it. Only what
// the forge actually reported is taken: a field it said nothing about keeps what
// the run recorded, because an answer that omits something is not an answer that
// it is empty.
//
// The merged flag it writes can land on a run that promoted nothing, which is
// the whole case this sweep exists for. Durable state allows that and refuses
// the other one — a merge the run itself asked the forge for, which is what a
// recorded merge method says, still requires the promotion that authorized it.
// Nothing here ever writes a merge method, so what this records is only ever an
// observation of what the forge did.
func refreshedPublication(recorded runstate.PullRequest, observed publish.PullRequest) runstate.PullRequest {
	refreshed := recorded
	if strings.TrimSpace(observed.State) != "" {
		refreshed.State = observed.State
	}
	refreshed.Merged = observed.Merged
	// A queued merge is only ever cleared here and never set. Deciding one is
	// settleQueuedMerge's work, and it does it as part of settling the whole run —
	// finishing the publication, closing the item, catching the target branch up —
	// so a finished run put back into that state from here would be handed to a
	// path that expects to own everything about it.
	if observed.Merged || !observed.AutoMerge {
		refreshed.MergeQueued = false
		refreshed.FailingChecks = nil
	}
	return refreshed
}

// PublicationSettlement is what one sweep did about a publication the harness
// had recorded as merged and unfinished. It reports what was outstanding before
// and what is left after, because a reader acts on the difference: a publication
// this sweep finished lifts a hold, and one it could not is still a person's.
type PublicationSettlement struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Number     int    `json:"number"`
	URL        string `json:"url,omitempty"`
	// Outstanding is what the run's record said was unfinished about the
	// publication before this sweep asked again.
	Outstanding string `json:"outstanding"`
	// Settled reports the publication finished: the merge confirmed on the
	// remote, the item settled by its landing, and nothing left outstanding.
	Settled bool `json:"settled"`
	// MergeCommit is the forge's merge commit the confirmation named, where it
	// named one. A fast-forward names none.
	MergeCommit string `json:"merge_commit,omitempty"`
	// Catchup is where the confirmation left the local target branch, present only
	// on a publication this sweep confirmed.
	Catchup *gitworktree.Catchup `json:"catchup,omitempty"`
	// Remaining is what is still outstanding after this sweep, in the remote's
	// words now: a confirmation it still refuses, or a consumed branch that still
	// could not be deleted. The record keeps the account the run wrote, which is
	// the line the work item carries, so the next sweep asks the same question
	// and a reader can match the record to the item.
	Remaining string `json:"remaining,omitempty"`
	// Kept is why a record was deliberately left where it stands, which is not a
	// failure: a run a live process holds, or a branch the forge answers about
	// with some other request.
	Kept string `json:"kept,omitempty"`
	// DocketProblem names a settled publication whose docket entry could not be
	// closed. It is not a settlement failure — the record and the item are
	// settled — and the next build of the docket cannot re-derive the entry from
	// a record that says nothing is outstanding.
	DocketProblem string `json:"docket_problem,omitempty"`
	Failure       string `json:"failure,omitempty"`
}

// FinishPublications asks the remote again about every publication the harness
// recorded as merged and could not finish, and finishes the ones the remote now
// confirms. It runs after RefreshPublications, which is what records a
// hand-made merge as merged in the first place.
//
// One publication that cannot be finished never stops the sweep, for the reason
// one unreconcilable run does not. A publication the remote still refuses keeps
// its record exactly as the run wrote it and is asked about again next time;
// what the remote says now is reported by the sweep rather than written.
func (r Reconciler) FinishPublications(ctx context.Context) ([]PublicationSettlement, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	recorded, err := r.Store.Recorded()
	if err != nil {
		return nil, fmt.Errorf("discover recorded runs: %w", err)
	}
	unfinished := make([]runstate.State, 0, len(recorded))
	for _, state := range recorded {
		if unfinishedPublication(state) {
			unfinished = append(unfinished, state)
		}
	}
	settled := make([]PublicationSettlement, 0, len(unfinished))
	if len(unfinished) == 0 {
		return settled, nil
	}
	if r.Publisher == nil {
		return settled, fmt.Errorf(
			"%d recorded publication(s) are merged and unfinished, and reconciliation has no forge access to ask what merged them", len(unfinished))
	}
	for _, state := range unfinished {
		if err := ctx.Err(); err != nil {
			return settled, err
		}
		settled = append(settled, r.finishPublication(ctx, state))
	}
	return settled, nil
}

// unfinishedPublication reports a recorded publication the forge has merged and
// the harness has not finished: the run is over, the promotion is on the local
// target, the forge says the request merged, and the record still carries an
// outstanding publication.
//
// A run that still owes a step is reconciliation's own — its queued merge is
// settled there, as part of settling the whole run — and a publication with
// nothing outstanding is finished. An unmerged one is not this either: a merge
// the forge dropped and nobody has made is a person's or triage's, and the
// refresh above is what turns it into this the day somebody makes it.
func unfinishedPublication(state runstate.State) bool {
	published := state.PullRequest
	if published == nil || state.Integration == nil || state.Outstanding() {
		return false
	}
	return published.Merged && strings.TrimSpace(state.PublishFailure) != ""
}

// finishPublication finishes one merged publication, under the run's own lease
// so the record that is rewritten is the record that was read.
//
// The steps are the settle path's, in the settle path's order and for its
// reasons. A publication nothing confirmed is confirmed first, and where the
// remote refuses that is the whole of what happens: nothing is written, and the
// publication stays outstanding for a person.
// A confirmed publication settles its item before its record stops saying it is
// outstanding, so a process that dies between the two leaves an item held for
// one more sweep rather than one nothing holds out of the pull. And the branch
// the merge consumed is deleted last, after the item is settled, because it is
// hygiene rather than part of the publication and must not hold the settlement
// up.
//
// A publication that was confirmed by an earlier settlement and left only its
// consumed branch behind is the one case taken out of that order: the branch is
// tried first, and nothing is written anywhere unless it goes. The alternative
// was a settlement note and a leftover note on the item at every sweep the
// branch went on refusing to be deleted.
func (r Reconciler) finishPublication(ctx context.Context, recorded runstate.State) PublicationSettlement {
	published := *recorded.PullRequest
	settlement := PublicationSettlement{
		RunID:       recorded.RunID,
		WorkItemID:  recorded.WorkItemID,
		Number:      published.Number,
		URL:         published.URL,
		Outstanding: recorded.PublishFailure,
	}
	// The forge is asked for the commit it recorded as the merge, which is what
	// confirms a merge other merges have since landed on top of. It is asked about
	// the branch, and a different request answering for that branch is a
	// publication this record was never about.
	observed, err := r.Publisher.State(ctx, published.Branch)
	if err != nil {
		settlement.Failure = fmt.Errorf("ask the forge about pull request %d of run %s: %w",
			published.Number, recorded.RunID, err).Error()
		return settlement
	}
	if observed.Number != published.Number {
		settlement.Kept = fmt.Sprintf("the forge reports pull request %d for branch %s, and run %s published request %d there",
			observed.Number, published.Branch, recorded.RunID, published.Number)
		return settlement
	}

	state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		settlement.Kept = fmt.Sprintf("a live process holds run %s, so its publication is that process's to finish", recorded.RunID)
		return settlement
	case err != nil:
		settlement.Failure = fmt.Errorf("adopt run %s to finish the publication of pull request %d: %w",
			recorded.RunID, published.Number, err).Error()
		return settlement
	}
	defer lease.Release()

	// Re-read under the lease: a run something finished in the meantime has had
	// its publication written by whatever finished it.
	if !unfinishedPublication(state) || state.PullRequest.Number != published.Number {
		settlement.Kept = fmt.Sprintf("run %s was settled while the forge was being asked, so its publication record is what settled it wrote", recorded.RunID)
		return settlement
	}
	published = *state.PullRequest
	settlement.Outstanding = state.PublishFailure
	settlement.MergeCommit = published.MergeCommit

	// A publication with a recorded merge commit was confirmed by whatever
	// recorded it, so what it still owes is the consumed branch, and the branch
	// alone decides it.
	if published.MergeCommit != "" {
		err := r.recovering(ctx, &state, runstate.RetryDeleteRemoteBranch, func(ctx context.Context) error {
			return r.Worktrees.DeleteRemoteBranch(ctx, worktreeOf(state), published.HeadCommit)
		})
		if err != nil {
			settlement.Remaining = fmt.Errorf("delete the merged remote branch: %w", err).Error()
			return settlement
		}
		settlement = r.recordSettledPublication(ctx, &state, published, settlement)
		return r.settleDocket(settlement, state)
	}

	confirmed, err := r.Worktrees.ConfirmRemoteTarget(ctx, integrationOf(state), observed.MergeCommit)
	if err != nil {
		// The remote still refuses, and the sweep says so in the remote's words now.
		// The record is deliberately left as the run wrote it: that account is the
		// `Publication outstanding` line on the work item, and a record reworded on
		// every sweep would stop matching the line a reader finds there. The
		// publication stays outstanding for the next sweep and for a person.
		settlement.Remaining = fmt.Errorf("confirm the merge reached %s: %w", state.Integration.TargetBranch, err).Error()
		return settlement
	}
	published.MergeCommit = confirmed
	state.PullRequest = &published
	settlement.MergeCommit = confirmed
	// The merge is confirmed on the remote, so the local branch may be behind it
	// by the forge's merge commit. Catching it up is idempotent and takes the
	// branch's promotion lease, and a held catch-up is a fact to report rather
	// than a reason to leave the publication outstanding.
	catchup := r.catchUp(ctx, state.Integration.TargetBranch)
	settlement.Catchup = &catchup

	settlement = r.recordSettledPublication(ctx, &state, published, settlement)
	if settlement.Failure != "" {
		return settlement
	}
	// The branch the merge consumed is removed last and cannot hold the
	// settlement up. A deletion that fails writes the leftover onto the record and
	// the item exactly as the settle path does, so what is outstanding afterwards
	// is a dead branch on the forge rather than a publication nobody confirmed —
	// and the docket entry stays open over it, as it does for the settle path's
	// leftover.
	if failure := r.deleteMergedBranch(ctx, &state, published); failure != "" {
		settlement.Settled = false
		settlement.Remaining = failure
		return settlement
	}
	return r.settleDocket(settlement, state)
}

// settleDocket closes the docket entries of a publication this sweep finished,
// and reports the entry it could not close beside a settlement that stands: the
// record and the item are settled, and a docket rebuilt from them cannot
// re-derive an entry for a publication that says nothing is outstanding.
func (r Reconciler) settleDocket(settlement PublicationSettlement, state runstate.State) PublicationSettlement {
	if !settlement.Settled || r.Docket == nil {
		return settlement
	}
	if _, err := r.Docket.SettlePublication(state, settledPublicationReason(state)); err != nil {
		settlement.DocketProblem = err.Error()
	}
	return settlement
}

// recordSettledPublication writes a confirmed publication onto the item and
// then the record, and reports it settled. The docket is the caller's, because
// an entry is closed only once nothing of the publication is left.
//
// The publication is confirmed, so what the record said was outstanding about it
// is not. A blocker about this publication goes with it, as it does when triage
// re-arms a dropped merge: left standing it would describe a publication that
// needs a person over one the forge has made. The item is settled only where
// that blocker handed it back — a drop puts the item in a person's hands, and
// this is what takes it out of them. Every other run with a confirmed merge
// settled its item as it ended, or had it settled by the sweep that found the
// merge landed, and an item somebody has since reopened on purpose is theirs.
//
// The record is the caller's, and it is left as it was saved: the caller goes on
// to the consumed branch with it, and a deletion that fails writes onto the
// record this settled.
func (r Reconciler) recordSettledPublication(ctx context.Context, state *runstate.State, published runstate.PullRequest, settlement PublicationSettlement) PublicationSettlement {
	previously := state.PublishFailure
	handedBack := state.MergeDrop != nil && strings.TrimSpace(state.Blocker) != ""
	state.PullRequest = &published
	state.PublishFailure = ""
	if handedBack {
		state.Blocker = ""
	}
	// The item first, then the record, for the reason settleQueuedMerge takes
	// them in that order. The note says what this sweep found and what it
	// replaces, because the line it replaces is still on the item above it.
	if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, renderSettledPublicationNotes(*state, previously, settlement.Catchup)); err != nil {
		settlement.Failure = fmt.Errorf("record the settled publication for run %s: %w", state.RunID, err).Error()
		return settlement
	}
	if handedBack {
		settled, err := r.closeSettledMerge(ctx, *state)
		if err != nil {
			settlement.Failure = err.Error()
			return settlement
		}
		*state = settled
	}
	state.UpdatedAt = r.clock().Now()
	if err := r.Store.Save(*state); err != nil {
		settlement.Failure = fmt.Errorf("record the settled publication of run %s: %w", state.RunID, err).Error()
		return settlement
	}
	settlement.Settled = true
	return settlement
}

// settledPublicationReason is what a closed docket entry says about why nobody
// has to decide about this publication any more.
func settledPublicationReason(state runstate.State) string {
	return fmt.Sprintf("the forge's merge of pull request %d is confirmed on %s, so nothing about the publication is outstanding",
		state.PullRequest.Number, state.Integration.TargetBranch)
}

// renderSettledPublicationNotes tells the work item that a publication it
// carried as outstanding is finished. It quotes the line that said so, exactly
// as every writer of that line renders it, because the line is still on the
// item above this note and a reader has to be able to match the two.
func renderSettledPublicationNotes(state runstate.State, previously string, catchup *gitworktree.Catchup) string {
	lines := []string{
		"Yoyodyne settled this item's publication: the forge's merge is confirmed on the remote target, and nothing about it is outstanding any more.",
		"Run: " + state.RunID,
		fmt.Sprintf("Pull request: #%d %s", state.PullRequest.Number, state.PullRequest.URL),
		"Integrated into: " + state.Integration.TargetBranch,
		"Integrated commit: " + state.Integration.TargetCommit,
	}
	if state.PullRequest.MergeCommit != "" {
		lines = append(lines, fmt.Sprintf("Remote target commit: %s (the forge's merge commit above the promoted commit)", state.PullRequest.MergeCommit))
	}
	lines = append(lines, "Previously outstanding, as the line above it reads: \"Publication outstanding: "+previously+"\"")
	return strings.Join(append(lines, renderCatchupNotes(catchup)...), "\n")
}
