package orchestrator

// Auditing the claims the tracker holds against the runs the harness actually
// has, and giving back the ones with nothing alive behind them.
//
// The harness claims a work item at the start of every run and nothing gives the
// claim back when the process holding it dies. What that leaves is an item the
// tracker calls in progress, a run record nobody is writing to, and a scheduler
// that will never choose the item again — because it chooses from what the
// tracker calls ready, and a claimed item is not ready. On four nights of the
// week of 2026-09-01 that was the whole of the line's throughput, and on the last
// of them both developer slots went that way inside an hour and the machine sat
// idle until morning. Nothing reported it, because there was nothing to report:
// every surface here reads a claimed item as work in progress.
//
// So the audit reads the claims against the runs and gives back the ones nothing
// is working on. It is here rather than in a surface because it writes: giving a
// claim back is directing work, which is the harness's, and a surface that did it
// would be a second control plane. It is called from the watch loop rather than
// from a person because the failure it catches is precisely the one nobody is
// there for.
//
// It decides two things and neither of them is what a message says. Which claims
// are dead is the read model's — the same derivation `yoyo status` would read —
// and saying so once is the durable record's, exactly as it is for a stall.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ClaimTracker is the tracker access one claim audit needs: giving one item
// back to the queue, with the reason on its notes. It is satisfied by
// beads.Client.
type ClaimTracker interface {
	Release(ctx context.Context, id, reason string) (beads.WorkItem, error)
}

// ClaimRuns is the durable run state one claim audit reads. It reads every run
// the harness holds rather than only the ones in flight, because the case this
// exists for is a claim whose run is over: a listing of what is still going
// cannot tell an item nothing ever ran from an item whose run ended without
// giving its claim back.
//
// It reads and never adopts. Deciding a claim is dead is a question about
// records, and taking a lease to answer it would be this pass acting on runs it
// is not making.
type ClaimRuns interface {
	Recorded() ([]runstate.State, error)
}

// ClaimReleases is where a release is written down and read back. The reading is
// what makes "once" a property of the record rather than of a process's memory:
// a tracker that has not caught up, or a release the store accepted and the
// tracker refused, must not produce a second release and a second message.
type ClaimReleases interface {
	Append(released runstate.ReleasedClaim) error
	List() ([]runstate.ReleasedClaim, error)
}

// ClaimAuditor gives back the claims with nothing alive behind them.
type ClaimAuditor struct {
	Tracker  ClaimTracker
	Runs     ClaimRuns
	Releases ClaimReleases
	// ProductID is what a record this audit writes has to name. The store knows
	// its own product, and a record it will not accept from the wrong one is
	// exactly why this is carried rather than assumed.
	ProductID domain.ProductID
	// Threshold is how long a claim may have nothing alive behind it before it is
	// dead rather than a tracker catching up. Zero takes
	// readmodel.DefaultDeadClaimThreshold.
	Threshold time.Duration
	// Window is how long a run's own record may go unmoved before it stops
	// counting as alive. Zero takes readmodel.DefaultRunActivityWindow.
	Window time.Duration
	Clock  execution.Clock
}

// ClaimSweep is what one audit did: the claims it gave back, and the ones it
// could not.
//
// Both are reported because they are different things for an operator. A release
// is the harness having found a stuck item and freed it, which is worth being
// told once; a release that failed is an item still stuck, which is worth being
// told about for as long as it stays that way.
type ClaimSweep struct {
	Released []runstate.ReleasedClaim `json:"released,omitempty"`
	// Problems name the claims this audit found dead and could not give back. They
	// are named rather than counted because what a person does about one is
	// specific to the item.
	Problems []string `json:"problems,omitempty"`
}

func (a ClaimAuditor) validate() error {
	var problems []error
	if a.Tracker == nil {
		problems = append(problems, errors.New("a claim audit requires a work tracker"))
	}
	if a.Runs == nil {
		problems = append(problems, errors.New("a claim audit requires the durable run state"))
	}
	if a.Releases == nil {
		problems = append(problems, errors.New("a claim audit requires somewhere to record what it released"))
	}
	if err := domain.ValidateIdentifier("product id", string(a.ProductID)); err != nil {
		problems = append(problems, err)
	}
	return errors.Join(problems...)
}

// Audit reads the claimed items against the recorded runs and gives back the
// ones with nothing alive behind them.
//
// One claim that cannot be given back is reported and never stops the sweep: an
// item the tracker refuses must not hide the others from the operator reading
// the report, and it is exactly the pass where several claims died at once that
// this matters in.
func (a ClaimAuditor) Audit(ctx context.Context, claimed []beads.WorkItem) (ClaimSweep, error) {
	if err := a.validate(); err != nil {
		return ClaimSweep{}, err
	}
	if len(claimed) == 0 {
		// Nothing is claimed, so nothing can be stuck. It is answered before the
		// records are read because this is the ordinary case on a quiet product and
		// the reading costs a directory walk.
		return ClaimSweep{}, nil
	}
	runs, err := a.Runs.Recorded()
	if err != nil {
		return ClaimSweep{}, fmt.Errorf("read the recorded runs: %w", err)
	}
	alreadyReleased, err := a.Releases.List()
	if err != nil {
		return ClaimSweep{}, fmt.Errorf("read the claims already given back: %w", err)
	}
	claims := make([]readmodel.Claim, 0, len(claimed))
	for _, item := range claimed {
		claims = append(claims, readmodel.Claim{WorkItemID: item.ID, Title: item.Title})
	}
	now := a.clock().Now()
	var sweep ClaimSweep
	for _, dead := range readmodel.DeadClaims(claims, runs, now, a.Threshold, a.Window) {
		if saidAlready(alreadyReleased, dead) {
			// This claim has been given back once already and the tracker still shows
			// it claimed. Releasing it again would be the same act said twice — and,
			// since a message is written from each record, a second message about one
			// stuck item. What a claim that came straight back needs is a person, and
			// the record they need is the one already written.
			continue
		}
		released := runstate.ReleasedClaim{
			SchemaVersion: runstate.ReleasedClaimSchemaVersion,
			ProductID:     a.ProductID,
			WorkItemID:    dead.WorkItemID,
			WorkItemTitle: dead.Title,
			RunID:         dead.RunID,
			Since:         dead.Since,
			Because:       dead.Because,
			ReleasedAt:    now,
		}
		if _, err := a.Tracker.Release(ctx, dead.WorkItemID, releaseNotes(released)); err != nil {
			sweep.Problems = append(sweep.Problems, fmt.Sprintf(
				"%s is claimed with nothing working on it and the claim could not be given back, so nothing will pull it: %v",
				dead.WorkItemID, err))
			continue
		}
		// The record is written after the tracker took the release rather than
		// before, because what it is a record of is the release having happened. A
		// process that dies between the two leaves the item back in the queue and no
		// record of why, which the next audit finds as a claim that is no longer
		// claimed and says nothing about — a message lost, rather than an item.
		if err := a.Releases.Append(released); err != nil {
			sweep.Problems = append(sweep.Problems, fmt.Sprintf(
				"%s was given back to the queue and the release could not be recorded, so nobody will be told about it: %v",
				dead.WorkItemID, err))
			continue
		}
		sweep.Released = append(sweep.Released, released)
	}
	return sweep, nil
}

// saidAlready reports a claim already given back over this same dead run.
//
// The key is the run rather than the item, and that is what keeps the two cases
// apart. A release the tracker did not take — an item that reads as claimed on
// the next pull anyway — is the same run's death said twice, and is skipped; a
// fresh run that was started for the item and died in its turn is a different
// death, and is said again. Keying on the item alone would swallow the second
// forever, and keying on a moment cannot separate them at all: a release is
// always recorded after the run it is about last spoke.
func saidAlready(released []runstate.ReleasedClaim, dead readmodel.DeadClaim) bool {
	for _, record := range released {
		if record.WorkItemID == dead.WorkItemID && record.RunID == dead.RunID {
			return true
		}
	}
	return false
}

// releaseNotes is what the item's own notes record about having been given back.
// It is written onto the item because that is where the next attempt at the work
// reads: a developer who finds a worktree from a run nobody finished should be
// told the harness gave the item back rather than left to infer it.
func releaseNotes(released runstate.ReleasedClaim) string {
	return fmt.Sprintf("The harness gave this item back to the queue at %s: %s. Nothing was working on it, so it was released to be pulled again.",
		released.ReleasedAt.UTC().Format(time.RFC3339), released.Because)
}

func (a ClaimAuditor) clock() execution.Clock {
	if a.Clock == nil {
		return execution.RealClock{}
	}
	return a.Clock
}
