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
// touches neither the work item nor anything the run promoted. A publication
// that turns out to have merged is therefore recorded as merged and left
// unfinished — the merge commit, the consumed remote branch and the local
// catch-up are the settle path's work, and a publication the harness recorded as
// outstanding stays outstanding for triage. What this is for is that the record
// and the forge agree, so that every decision downstream is made on a record
// that is true.

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	if answered == published {
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
	if current == *state.PullRequest {
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
	}
	return refreshed
}
