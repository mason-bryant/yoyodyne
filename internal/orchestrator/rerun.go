package orchestrator

// Running a docketed stoppage again, on the development manager's decision.
//
// This is the one triage decision the harness carries out. A re-run is what a
// correct change whose ground moved needs: nothing about the work was wrong, so
// there is nothing to repair and nothing to escalate — what it needs is the same
// item attempted again against the repository as it now stands.
//
// The decision is still not this package's. What reaches here is a decision a
// development manager already recorded on the work item, with the reasoning it
// gave, and everything here is the harness acting on it: reading whether it may
// start work at all, proving the stoppage is really over, reading that the item
// is one a run may start on, claiming the one re-run that stoppage gets, and
// starting the run.
//
// # Why everything that refuses is asked before the claim
//
// The budget a re-run spends is one per docketed stoppage, and it is spent by
// claiming it rather than by running anything. So a condition that would refuse
// the fresh run and is asked after the claim spends the very budget it refuses:
// that is what a re-run of a blocked item did, where the pipeline refused the
// item's status past the claim and the next attempt was then refused by the
// once-only guard, for a run that had never happened.
//
// The item's own state is the one such condition the harness does not hold in
// its own records, so it is read here from the tracker, and what refuses it is
// the pipeline's own condition rather than a second rendering of it. Everything
// past the claim keeps the opposite order deliberately: a claim taken before the
// run means a process that dies between the two has spent a re-run nobody took
// rather than taken one nobody recorded.
//
// # Why a full harness is a state rather than a refusal
//
// Developer capacity is the one condition here that says nothing at all about
// the decision: two developers happening to be busy at this second is not an
// argument against running the item again, and it stops being true on its own.
// So a carry-out that meets it neither fails nor spends anything — it reports
// what it is waiting on and leaves the authorization standing, to be carried out
// by asking again once a slot frees. The item is meanwhile in the open pool the
// scheduler pulls from, which is the other way the same work reaches a
// developer; the two agree because nothing was claimed here.
//
// That is also the one thing withdrawn past the claim. The slot can go to
// another run between the reading and the reservation, and a claim taken for a
// run the reservation then refused is a re-run spent on a run that provably
// never existed — the reservation refuses before the run's record, the item's
// claim and any agent. So it is given back, and the carry-out reports the same
// waiting state it would have reported a moment earlier.
//
// # Why the hold applies
//
// A re-run is the harness choosing work. The development manager naming the item
// is not the operator naming it, and `selected-work-passes-intake-and-records-why`
// draws exactly that line: the exemption belongs to the operator, because naming
// an item is them deciding it is the exception. So the intake hold is read before
// anything is claimed or spent, and a held intake starts nothing — the claim is
// not taken, so the stoppage keeps its one re-run for after the hold is lifted.
// The pipeline reads the hold again where it would start the run, which is the
// enforcement; this reading is what keeps a held harness from spending the
// stoppage's only claim on a run that would then decline to start.
//
// The reason the run records is the development manager's decision and its
// reasoning, which is the other half of the same invariant. A re-run nobody can
// account for looks exactly like work happening behind somebody's back.
//
// # What the developer is given
//
// Nothing here assembles the developer's context. The guidance a development
// manager records for a re-run — what the preserved branch holds, what is worth
// cherry-picking — is written into the work item's notes when the decision is
// recorded, and the item's notes are part of every run's context bundle already.
// That route is deliberate and worth stating so nobody improves it: notes are not
// evidence for a protected-path grant, so guidance travelling this way can never
// widen what the re-run may touch. Carrying it any other way would put the two
// back in the same channel.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// RerunDocket is the docket a re-run is decided against. It is read and never
// written: a re-run settles nothing about the entry, which stands as the record
// that the work stopped however many times it is run again.
type RerunDocket interface {
	List() ([]triage.Entry, error)
}

// RerunRuns is the durable run state this action reads, and — once a retirement
// has actually removed something — writes. The two reads are the architect's
// condition rather than convenience: the stopped run's own record is what proves
// the stoppage is terminal, and what is in flight is what proves the item has no
// live run to collide with.
//
// The write is the other side of the same fact. Everything in the harness that
// asks whether a stopped run's branch and worktree are still there reads the
// flags on its record rather than looking on disk, so a retirement that is not
// written back leaves every one of those readers naming artifacts that are gone.
// It is taken under the stopped run's own lease, which is what AdoptRun exists
// for: a run that is terminal and still owes cleanup is not in flight, and still
// must have exactly one owner while anybody acts on it.
type RerunRuns interface {
	Load(runID string) (runstate.State, error)
	Incomplete() ([]runstate.State, error)
	AdoptRun(ctx context.Context, runID string) (runstate.State, *runstate.Lease, error)
	Save(state runstate.State) error
}

// RerunDecisions is the harness's own durable record of what triage has decided
// about one work item. It is what proves a re-run was actually decided: the
// development manager's decision spends the item's re-run budget as it is
// recorded, so an item whose budget carries no re-run is an item nobody decided
// this about.
//
// It is satisfied by runstate.TriageStore.
type RerunDecisions interface {
	Counters(workItemID string) (runstate.TriageCounters, error)
}

// RerunRecords is where the one re-run a docketed stoppage gets is claimed and
// settled, and where what has already been carried out for a work item is
// read back. What has been claimed is the other half of the decision gate: a
// claim is what says a decision has been acted on, so counting them is what stops
// one decision authorizing a re-run of every stoppage an item ever has.
//
// It is satisfied by runstate.RerunStore.
type RerunRecords interface {
	Claim(ctx context.Context, rerun runstate.Rerun) (runstate.Rerun, error)
	Settle(ctx context.Context, docketKey, runID string, preserved runstate.PreservedArtifacts) (runstate.Rerun, error)
	Claimed(workItemID string) ([]runstate.Rerun, error)
	// Withdraw gives the claim back, for the one case where the run it was taken
	// for provably never existed: a reservation refused for developer capacity.
	// See runstate.RerunStore.Withdraw for why that case and no other.
	Withdraw(ctx context.Context, docketKey string) error
}

// RerunItems is the work item the stoppage is about. It is read and never
// written: what becomes of an item is the fresh run's to record, and a re-run
// that reopened what it wanted to run would be deciding the thing it is here to
// carry out somebody else's decision about.
//
// It is the one condition a re-run asks outside the harness's own records, and
// it is asked because a fresh run starts on the item itself. The pipeline reads
// the same tracker where it would claim the item, which is the enforcement; this
// reading is what keeps an item the pipeline would refuse from spending the
// stoppage's only claim on a run that would then decline to start.
//
// It is satisfied by beads.Client.
type RerunItems interface {
	Show(ctx context.Context, id string) (beads.WorkItem, error)
}

// PreservedRetirer retires what the stopped run left behind, once the fresh run
// has integrated and what it held has stopped being worth keeping. It is
// optional: an action wired without one starts exactly the same run and records
// the preserved artifacts as kept, which is what they are.
//
// It is satisfied by gitworktree.Manager.
type PreservedRetirer interface {
	RetirePreserved(ctx context.Context, worktree gitworktree.Worktree, targetBranch string) (gitworktree.Retirement, error)
}

// Rerunner starts a fresh run of an item triage decided to run again. It reads
// the work item and writes nothing to it, it has no forge access, and it decides
// nothing about the work: what it does is check that a decision somebody else
// made may be carried out, and then carry it out.
type Rerunner struct {
	Docket RerunDocket
	Runs   RerunRuns
	Intake IntakeHolds
	Reruns RerunRecords
	// Decisions is the item's durable triage record, which is what says the
	// development manager decided this at all. Required: a re-run carried out on
	// nobody's decision would record the development manager as having chosen
	// work it never looked at.
	Decisions RerunDecisions
	// Items is the work item the fresh run would start on. Required: the pipeline
	// asks the same question past the claim, so a re-run that could not ask it
	// here would go on spending the stoppage's one re-run to find out.
	Items RerunItems
	// Capacity is execution.max_concurrent_developers as this carry-out read it,
	// which is the same number the reservation enforces. Required: a carry-out
	// that could not tell a full harness from a free one would go on spending the
	// stoppage's re-run to find out there was no slot for it.
	Capacity int
	// Preserved retires the stopped run's branch and worktree once the fresh run
	// integrates. Optional; see PreservedRetirer.
	Preserved PreservedRetirer
	Start     Starter
	Clock     execution.Clock
}

// RerunRequest is one decision to carry out: the run the docket entry names, and
// the reasoning the development manager recorded for deciding a re-run of it.
type RerunRequest struct {
	Run    string
	Reason string
}

// RerunResult is what the action did. It reports the run it started and what
// became of it, and it reports just as carefully when it started nothing: an
// intake hold and a refusal are different things for an operator to do something
// about, and neither is a run that failed.
type RerunResult struct {
	WorkItemID string `json:"work_item_id"`
	PriorRunID string `json:"prior_run_id"`
	DocketKey  string `json:"docket_key"`
	// Reason is what the fresh run recorded as why it exists, which is the
	// development manager's decision and its reasoning rather than this package's
	// account of either.
	Reason  string `json:"reason"`
	Started bool   `json:"started"`
	// IntakeHeld is the operator's hold, when one is what stopped this. Nothing
	// was claimed and the stoppage keeps its re-run.
	IntakeHeld *runstate.IntakeHold `json:"intake_held,omitempty"`
	// CapacityFull is every developer slot being occupied, when that is what this
	// is waiting on. It is not a refusal and not a failure: nothing was claimed,
	// the decision stands until it is carried out or the development manager
	// withdraws it, and asking again once a slot frees carries out the same one.
	CapacityFull *runstate.CapacityError `json:"capacity_full,omitempty"`
	Outcome      Outcome                 `json:"outcome"`
	// Preserved is what the stopped run left behind and what became of it.
	Preserved runstate.PreservedArtifacts `json:"preserved"`
	// RecordProblem names a durable record this action could not update after the
	// run. The run happened either way, so it is reported beside the result rather
	// than in place of it — but a disposition nobody wrote down is exactly the
	// orphan this record exists to prevent, so it is never left unsaid.
	RecordProblem string `json:"record_problem,omitempty"`
}

// Rerun carries out one re-run decision.
//
// The order is the order the guarantees need. Everything that can refuse is
// asked before anything is claimed or started, so a refused re-run costs the
// stoppage nothing; the claim is taken before the run is started, so a process
// that dies between the two has spent a re-run nobody took rather than taken one
// nobody recorded; and what became of the preserved artifacts is recorded after
// the run, because until it ends there is nothing to decide about them.
func (r Rerunner) Rerun(ctx context.Context, request RerunRequest) (RerunResult, error) {
	if err := r.validate(); err != nil {
		return RerunResult{}, err
	}
	priorRunID := strings.TrimSpace(request.Run)
	reasoning := strings.TrimSpace(request.Reason)
	if !runstate.ValidRunID(priorRunID) {
		return RerunResult{}, fmt.Errorf("re-run %q is not a run identifier; a triage decision names the run the docket entry is about", request.Run)
	}
	if reasoning == "" {
		return RerunResult{}, errors.New("a re-run records the development manager's reasoning as why the fresh run exists, and none was given")
	}

	entry, err := r.entry(priorRunID)
	if err != nil {
		return RerunResult{}, err
	}
	result := RerunResult{
		WorkItemID: entry.WorkItemID,
		PriorRunID: entry.RunID,
		DocketKey:  entry.Key,
	}
	prior, err := r.Runs.Load(entry.RunID)
	if err != nil {
		return result, fmt.Errorf("read the run the docket entry is about: %w", err)
	}
	// What the stoppage left behind is read from the run's record rather than
	// from the entry, for the same reason the stoppage itself is: the entry says
	// what was there when it was written, and an artifact removed since must not
	// be recorded as one somebody could go and look at.
	result.Preserved = preservedOf(prior)
	// The docket says what was true when the entry was made. What decides whether
	// this stoppage may be run again is what is true now, so the run's own record
	// is asked rather than the entry that describes it.
	if err := stoppageIsOver(prior); err != nil {
		return result, err
	}
	if err := r.noRunInFlight(entry.WorkItemID); err != nil {
		return result, err
	}
	// That the development manager decided this is read rather than taken on
	// trust. The reason this run records names that role, so an action carried out
	// on nobody's decision would put an attribution nothing supports into the one
	// record the invariant exists to make trustworthy.
	decided, taken, err := r.decided(entry)
	if err != nil {
		return result, err
	}
	result.Reason = rerunReason(entry, decided, taken, reasoning)
	// The item is read before the claim, for the reason the hold below is: a fresh
	// run starts on the item itself, so an item the pipeline would refuse must not
	// spend the stoppage's one re-run on finding that out.
	if err := r.itemCanBeRun(ctx, entry.WorkItemID); err != nil {
		return result, err
	}
	// The hold is read before the claim, so a held harness leaves the stoppage its
	// one re-run rather than spending it on a run that would decline to start.
	hold, held, err := r.Intake.Held()
	if err != nil {
		return result, fmt.Errorf("read whether the operator has held intake: %w", err)
	}
	if held {
		result.IntakeHeld = &hold
		return result, nil
	}
	// Capacity is read last of everything asked before the claim, because it is
	// the condition most likely to have changed while the rest were being asked
	// and the one a moment's wait can settle. A full harness is reported rather
	// than refused: the decision stands, and nothing was claimed to carry it out
	// with.
	full, free, err := r.slotIsFree()
	if err != nil {
		return result, err
	}
	if !free {
		result.CapacityFull = &full
		return result, nil
	}

	claimed, err := r.Reruns.Claim(ctx, runstate.Rerun{
		DocketKey:  entry.Key,
		PriorRunID: entry.RunID,
		WorkItemID: entry.WorkItemID,
		Reason:     result.Reason,
		ClaimedAt:  r.now(),
		Preserved:  result.Preserved,
	})
	if err != nil {
		return result, err
	}
	result.Preserved = claimed.Preserved

	result.Started = true
	outcome, runErr := r.Start(ctx, entry.WorkItemID, runstate.Selection{
		By:     runstate.SelectedByDevelopmentManager,
		Reason: result.Reason,
		At:     r.now(),
	})
	// The last free slot going to another run between the reading above and the
	// reservation is the same waiting state read a moment too late, and it has to
	// cost the same: the claim taken for a run that never existed is given back,
	// and there is nothing to settle behind it.
	if capacity, refused := capacityRefused(outcome, runErr); refused {
		return r.withdraw(ctx, entry, capacity, result), nil
	}
	result.Outcome = outcome
	result.Preserved = r.settle(ctx, entry, prior, outcome, &result)
	return result, runErr
}

// entry finds the docketed stoppage a decision is about. A run that is not on
// the docket is refused rather than run: the docket entry is what a re-run is
// counted against, so a re-run of something nothing docketed would be a re-run
// nothing bounds.
func (r Rerunner) entry(priorRunID string) (triage.Entry, error) {
	return docketedStoppage(r.Docket, priorRunID, "run again")
}

// docketedStoppage finds the docketed stoppage of one run, for whichever action
// is about to act on it. act names what the caller would do, so a refusal reads
// as the thing that was refused rather than as a lookup that came back empty.
func docketedStoppage(docket RerunDocket, priorRunID, act string) (triage.Entry, error) {
	entries, err := docket.List()
	if err != nil {
		return triage.Entry{}, fmt.Errorf("read the triage docket: %w", err)
	}
	for _, candidate := range entries {
		if candidate.Class == triage.ClassStoppedRun && candidate.RunID == priorRunID {
			return candidate, nil
		}
	}
	return triage.Entry{}, fmt.Errorf("no stopped run of %s is on the triage docket, so there is no stoppage to %s", priorRunID, act)
}

// stoppageIsOver reports the run's own record proving the stoppage is terminal
// with its blocker standing. Both halves are the condition: a run still in
// flight is owed the rest of its own step, and one that ended without a blocker
// stopped for a reason nobody has to decide about.
func stoppageIsOver(prior runstate.State) error {
	if !prior.Status.Terminal() {
		return fmt.Errorf("run %s is recorded as %s rather than ended, so it is owed a continuation rather than a fresh run; a re-run is refused while anything of it is resumable",
			prior.RunID, prior.Status)
	}
	if strings.TrimSpace(prior.Blocker) == "" {
		return fmt.Errorf("run %s ended carrying no durable blocker, so nothing about it stopped for a person to decide", prior.RunID)
	}
	return nil
}

// decided reports a decision of the development manager's that this re-run may
// carry out, and refuses where there is none. It is two questions and the second
// is what makes the first mean anything.
//
// The item's re-run counter is the decision's own footprint: the development
// manager spends it as the decision is recorded and before anything acts on it,
// so an item carrying none is an item nobody decided this about. But the counter
// is a total and is never cleared, so on its own it cannot tell a decision that
// is waiting to be carried out from one that was carried out last month — and
// reading it that way would let a second stoppage of an already re-run item start
// a whole second run on the strength of the first decision, past the one-per-item
// bound and under an attribution that describes a decision about a different
// stoppage.
//
// So each decision authorizes exactly one re-run, and what has been claimed for
// the item is read back against what has been decided. An item whose decisions
// are all spent is refused: a further stoppage of it needs a further decision,
// which past the cap is an escalation rather than a larger budget — which is
// exactly what the development manager's own workflow says.
//
// The count is read before the claim is taken, so two processes carrying out one
// decision at the same instant could both pass it. What that costs is bounded by
// the reservation rather than by this: the second run of one item is refused
// where it is reserved, so the loser spends a claim rather than putting a second
// developer on the work.
//
// What it returns is the whole record rather than the one counter, because the
// reason this run records has to be able to say what the decision stands on: an
// item whose caps an operator crossed was re-run past a bound the harness would
// otherwise have refused, and the run's own selection reason is where that
// survives the conversation it was decided in.
func (r Rerunner) decided(entry triage.Entry) (decided runstate.TriageCounters, taken int, err error) {
	workItemID := entry.WorkItemID
	counters, err := r.Decisions.Counters(workItemID)
	if err != nil {
		return runstate.TriageCounters{}, 0, fmt.Errorf("read what triage has recorded about %s: %w", workItemID, err)
	}
	if counters.Reruns < 1 {
		return runstate.TriageCounters{}, 0, fmt.Errorf(
			"triage has recorded no re-run of %s, so there is no decision here to carry out: the development manager records the decision, which spends the item's re-run budget, before the harness starts anything on it",
			workItemID)
	}
	claimed, err := r.Reruns.Claimed(workItemID)
	if err != nil {
		return runstate.TriageCounters{}, 0, fmt.Errorf("read the re-runs already taken of %s: %w", workItemID, err)
	}
	// This stoppage having been re-run already is the more particular answer, and
	// the one whose refusal can say what became of it, so it is given rather than
	// the arithmetic below. The claim itself refuses this too; asking here is what
	// makes the refusal name the run the first re-run started.
	for _, existing := range claimed {
		if existing.DocketKey == entry.Key {
			return runstate.TriageCounters{}, 0, runstate.RerunTakenError{Existing: existing}
		}
	}
	if len(claimed) >= counters.Reruns {
		return runstate.TriageCounters{}, 0, fmt.Errorf(
			"triage has decided %d re-run(s) of %s and the harness has carried out %d, so this stoppage has no decision of its own to act on: a further stoppage of an item that has already been run again needs a further decision, which past the cap is an escalation rather than a larger budget",
			counters.Reruns, entry.WorkItemID, len(claimed))
	}
	return counters, len(claimed), nil
}

// itemCanBeRun reports the work item being in a state a fresh run may start on,
// which for a docketed stoppage ordinarily means somebody has put it back: a run
// that stopped on a durable blocker blocked its item, and a blocked item is not
// one the pipeline starts work on.
//
// What it asks is the pipeline's own condition rather than a second rendering of
// it, so this refusal and the one the fresh run would make can never drift apart.
// The refusal says what has to become true, because that is the whole of what
// this being free is worth: the stoppage keeps its re-run, so the same decision
// is carried out by asking again once the item has been put back.
func (r Rerunner) itemCanBeRun(ctx context.Context, workItemID string) error {
	item, err := r.Items.Show(ctx, workItemID)
	if err != nil {
		return fmt.Errorf("read the work item the stoppage is about: %w", err)
	}
	if err := validateReadyItem(item, workItemID); err != nil {
		return fmt.Errorf("%w, which is what a fresh run of it would start from; nothing was claimed, so the stoppage keeps its re-run — put the item back in a state a run may start on and ask again to carry out the same decision", err)
	}
	return nil
}

// noRunInFlight refuses a re-run of an item something is already running. The
// reservation refuses a second run of one item anyway; this is the same rule
// asked before anything is claimed, so a collision costs the stoppage's re-run
// nothing.
func (r Rerunner) noRunInFlight(workItemID string) error {
	return noRunInFlight(r.Runs, workItemID)
}

// noRunInFlight is the same rule for every triage action that would put a
// developer on an item: an item something is already running is not work that
// has stopped, whatever the docket entry said when it was written.
func noRunInFlight(runs RerunRuns, workItemID string) error {
	incomplete, err := runs.Incomplete()
	if err != nil {
		return fmt.Errorf("read what is already in flight: %w", err)
	}
	for _, state := range incomplete {
		if state.WorkItemID == workItemID {
			return fmt.Errorf("%s already has run %s in flight in status %s, so it is not work that has stopped",
				workItemID, state.RunID, state.Status)
		}
	}
	return nil
}

// slotIsFree reports a developer slot being free for the fresh run, and reports
// a full harness as a state rather than as a refusal.
//
// Capacity is the one condition here that is about the moment rather than about
// the work: two developers being busy at this second says nothing about whether
// the item should be run again, and it stops being true without anybody doing
// anything. A carry-out that failed on it would make a recorded decision of the
// development manager's weaker than an ordinary scheduler poll, which simply
// comes back — so this waits instead, and waiting costs the stoppage nothing
// because it is asked before the claim.
//
// The same number the reservation enforces is counted the same way, from the
// runs in flight, so what is read here and what would refuse the fresh run are
// one fact rather than two.
func (r Rerunner) slotIsFree() (runstate.CapacityError, bool, error) {
	return slotIsFree(r.Runs, r.Capacity)
}

// slotIsFree counts the runs in flight against the configured limit, the same
// way and from the same records the reservation does, so what a triage action
// reads before it spends anything and what would refuse the run it starts are
// one fact rather than two.
func slotIsFree(runs RerunRuns, capacity int) (runstate.CapacityError, bool, error) {
	incomplete, err := runs.Incomplete()
	if err != nil {
		return runstate.CapacityError{}, false, fmt.Errorf("read what is already in flight: %w", err)
	}
	if len(incomplete) >= capacity {
		return runstate.CapacityError{Limit: capacity, Active: len(incomplete)}, false, nil
	}
	return runstate.CapacityError{}, true, nil
}

// capacityRefused reports the fresh run having been refused a developer slot and
// nothing else having happened. Both halves are the condition: the reservation
// is what refuses for capacity and it is taken before the run's record exists,
// before the work item is claimed and before any agent runs, so a refusal that
// nonetheless names a run is not this and is left to be settled like any other
// run that ended badly.
func capacityRefused(outcome Outcome, err error) (runstate.CapacityError, bool) {
	var capacity runstate.CapacityError
	if !errors.As(err, &capacity) || outcome.RunID != "" {
		return runstate.CapacityError{}, false
	}
	return capacity, true
}

// withdraw gives back the claim taken for a fresh run a full harness refused to
// reserve, and reports the same waiting state a carry-out that met capacity
// before the claim reports.
//
// A claim that could not be given back is said out loud rather than swallowed:
// the stoppage has then paid for a refusal that was meant to be free, and the
// decision it authorized needs a person to look at it, because nothing else here
// will notice.
func (r Rerunner) withdraw(ctx context.Context, entry triage.Entry, capacity runstate.CapacityError, result RerunResult) RerunResult {
	result.Started = false
	result.CapacityFull = &capacity
	if err := r.Reruns.Withdraw(ctx, entry.Key); err != nil {
		result.note(fmt.Sprintf(
			"the last free developer slot went to another run before this re-run of %s was reserved, and the claim taken for it could not be given back, so the stoppage has spent its re-run on a run that never started: %v",
			entry.WorkItemID, err))
	}
	return result
}

// settle records what became of the stopped run's artifacts and reports the
// disposition it recorded. They are kept until the fresh run integrates, which
// is the moment what the stopped run holds stops being what somebody might
// cherry-pick from; then they are retired explicitly, and what could not be
// retired is kept with the reason, because an artifact nobody records is an
// orphan nobody discovers.
func (r Rerunner) settle(ctx context.Context, entry triage.Entry, prior runstate.State, outcome Outcome, result *RerunResult) runstate.PreservedArtifacts {
	preserved := preservedOf(prior)
	if preserved.Disposition == runstate.PreservedKept && outcome.Integration != nil {
		var problem string
		preserved, problem = r.retire(ctx, prior, outcome.RunID, preserved)
		result.note(problem)
	}
	settled, err := r.Reruns.Settle(ctx, entry.Key, outcome.RunID, preserved)
	if err != nil {
		result.note(fmt.Sprintf(
			"the re-run of %s ran, and what became of the branch and worktree run %s preserved could not be recorded: %v",
			entry.WorkItemID, entry.RunID, err))
		return preserved
	}
	return settled.Preserved
}

// note adds one record this action could not update. They accumulate rather than
// replacing each other: the stopped run's record and the re-run's own are two
// different readers' accounts of the same artifacts, and a caller told about one
// failure would go looking in the wrong place for the other.
func (result *RerunResult) note(problem string) {
	switch {
	case problem == "":
	case result.RecordProblem == "":
		result.RecordProblem = problem
	default:
		result.RecordProblem += "; " + problem
	}
}

// retire removes what the stopped run preserved, now that the fresh run has
// integrated the work, and reports the disposition beside any record it could
// not update. Nothing here fails the re-run: the work landed, and an artifact
// that has to be looked at by hand is a fact to record rather than a reason to
// report a successful run as a failure.
//
// It is done under the stopped run's own lease, taken and released here. That is
// what makes the removal and the record of it one act: the state the flags are
// written onto is the state read under the lease, so a sweep settling the same
// run beside this cannot lose either half.
func (r Rerunner) retire(ctx context.Context, prior runstate.State, freshRunID string, preserved runstate.PreservedArtifacts) (runstate.PreservedArtifacts, string) {
	if r.Preserved == nil {
		preserved.Problem = "nothing is wired to retire what the stopped run preserved, so it is still there"
		return preserved, ""
	}
	stopped, lease, err := r.Runs.AdoptRun(ctx, prior.RunID)
	if err != nil {
		// Somebody else owns the stopped run, or its record could not be read.
		// Either way nothing is removed: an artifact retired without its record
		// being writable is exactly the stale record this took the lease to avoid.
		preserved.Problem = fmt.Sprintf("what run %s preserved was left where it is, because its record could not be taken to write the removal onto: %v", prior.RunID, err)
		return preserved, ""
	}
	defer lease.Release()

	retirement, retireErr := r.Preserved.RetirePreserved(ctx, worktreeOf(stopped), stopped.TargetBranch)
	// What was removed is written onto the stopped run before anything else is
	// decided, because that record is what every other reader in the harness asks
	// whether these artifacts still exist.
	recorded := r.recordRemoval(stopped, freshRunID, retirement)
	if retireErr == nil && retirement.Retired() {
		retired := r.now()
		preserved.Disposition = runstate.PreservedRetired
		preserved.RetiredAt = &retired
		return preserved, recorded
	}
	// Something survived, so the record still says kept — and it names only what
	// actually survived. A retirement that took one of the two and then failed is
	// the case that makes this matter: a reader sent after an artifact that is not
	// there is exactly what these fields exist to prevent.
	if retirement.Worktree.Removed {
		preserved.WorktreePath = ""
	}
	if retirement.Branch.Removed {
		preserved.Branch = ""
	}
	if retireErr != nil {
		preserved.Problem = fmt.Sprintf("retiring what run %s preserved failed: %v", prior.RunID, retireErr)
		return preserved, recorded
	}
	preserved.Problem = retirement.Kept()
	return preserved, recorded
}

// recordRemoval marks the stopped run's own record with the artifacts this
// retirement removed, and reports what stopped it where it could not.
//
// It is the record the rest of the harness reads: `yoyo status`, a docket entry
// built later, and reconciliation all take these flags as the answer to whether
// the branch and the worktree are still there. A removal that is not written back
// leaves every one of them sending somebody after an artifact that is gone, which
// is the same failure the disposition on the re-run's own record exists to
// prevent, one reader further out.
//
// A retirement that removed nothing writes nothing: this is called on every
// retirement, including the ones that kept both artifacts.
func (r Rerunner) recordRemoval(stopped runstate.State, freshRunID string, retirement gitworktree.Retirement) string {
	worktreeGone := retirement.Worktree.Removed && !stopped.WorktreeRemoved
	branchGone := retirement.Branch.Removed && !stopped.BranchRemoved
	if !worktreeGone && !branchGone {
		return ""
	}
	stopped.WorktreeRemoved = stopped.WorktreeRemoved || worktreeGone
	stopped.BranchRemoved = stopped.BranchRemoved || branchGone
	// A stopped run promoted nothing, so what earns the removal is the run that
	// superseded it. Naming it is what keeps the record evidence rather than an
	// assertion, and it is what the run state requires of a removal with no
	// integration behind it.
	stopped.ArtifactsRetiredBy = freshRunID
	// When the run ended is what dates it; this dates the last thing the harness
	// did to what it left behind, which is what UpdatedAt has always meant.
	stopped.UpdatedAt = r.now()
	if err := r.Runs.Save(stopped); err != nil {
		return fmt.Sprintf(
			"what run %s preserved was retired, and its own record still says otherwise, so anything reading that run will name artifacts that are gone: %v",
			stopped.RunID, err)
	}
	return ""
}

// preservedOf is what the stopped run left behind, as its own record has it. A
// run whose artifacts the harness already removed has nothing to keep or retire,
// and says so rather than describing what is gone as kept.
func preservedOf(prior runstate.State) runstate.PreservedArtifacts {
	preserved := runstate.PreservedArtifacts{Disposition: runstate.PreservedKept}
	if !prior.BranchRemoved {
		preserved.Branch = prior.Branch
	}
	if !prior.WorktreeRemoved {
		preserved.WorktreePath = prior.WorktreePath
	}
	if preserved.Branch == "" && preserved.WorktreePath == "" {
		preserved.Disposition = runstate.PreservedGone
	}
	return preserved
}

// rerunReason is what the fresh run records as why it exists: the decision the
// harness verified, the stoppage it settles, and the reasoning it was given.
//
// The two are worded apart on purpose. That the development manager decided a
// re-run of this item is a fact read from the item's durable triage record, and
// is stated as one. The prose after it arrived with the instruction to carry the
// decision out, and is attributed to that rather than quoted as the development
// manager's own words — a reason nobody can check must not read as one somebody
// verified.
func rerunReason(entry triage.Entry, decided runstate.TriageCounters, taken int, reasoning string) string {
	reason := fmt.Sprintf(
		"the development manager's triage decided a re-run of %s — %d recorded against the item's durable triage budget, %d of them already carried out — and the harness started this run to carry out the one left, on the stopped work of run %s.%s The reasoning given to the harness when it was asked to: ",
		entry.WorkItemID, decided.Reruns, taken, entry.RunID, crossedCaps(decided))
	// The reasoning is folded to what the run's recorded selection will hold,
	// rather than refused: losing the end of a long argument is better than
	// refusing to carry out a decision because of its length. The room is never
	// negative, so a reason that filled the record on its own truncates the
	// argument to nothing rather than indexing past the end of it.
	room := runstate.MaxSelectionReasonBytes - len(reason)
	if room < 0 {
		room = 0
	}
	return reason + singleLine(reasoning, room)
}

// crossedCaps names the operator overrides this decision stands on, and says
// nothing on the ordinary item.
//
// A re-run recorded past a cap exists because a person crossed that cap, and the
// run's selection reason is where that has to survive: the record it was read
// from is a live one that the next override rewrites, and the conversation it was
// argued in is gone. It names the two budgets a re-run is refused against and
// only those, because the reason is a sentence somebody reads rather than a dump
// of the item's record — and it names who and when rather than why, because the
// argument for crossing the cap is on the override itself, at whatever length the
// operator wrote it.
func crossedCaps(counters runstate.TriageCounters) string {
	var crossed []string
	for _, budget := range []string{runstate.TriageRerunBudget, runstate.TriageReviewRoundBudget} {
		override, found := counters.OverrideOf(budget)
		if !found {
			continue
		}
		crossed = append(crossed, fmt.Sprintf("the %s cap, by %s at %s",
			budget, singleLine(override.DecidedBy, maxCrossedCapAttributionBytes), override.DecidedAt.UTC().Format(time.RFC3339)))
	}
	if len(crossed) == 0 {
		return ""
	}
	return " It stands on a recorded operator override of " + strings.Join(crossed, ", and of ") + "."
}

// maxCrossedCapAttributionBytes bounds how much of an operator's name the run's
// reason carries. The record keeps whatever they gave; this is a clause inside a
// sentence, and a bounded one is what keeps the argument the reason exists for
// from being crowded out by the attribution.
const maxCrossedCapAttributionBytes = 64

func (r Rerunner) validate() error {
	var problems []error
	if r.Docket == nil {
		problems = append(problems, errors.New("a re-run requires the triage docket the decision was made against"))
	}
	if r.Runs == nil {
		problems = append(problems, errors.New("a re-run requires the durable run state"))
	}
	if r.Intake == nil {
		problems = append(problems, errors.New("a re-run requires the intake hold, because a re-run is the harness choosing work"))
	}
	if r.Reruns == nil {
		problems = append(problems, errors.New("a re-run requires the record that bounds it to one per docketed stoppage"))
	}
	if r.Decisions == nil {
		problems = append(problems, errors.New("a re-run requires the item's triage record, which is what says the development manager decided one"))
	}
	if r.Items == nil {
		problems = append(problems, errors.New("a re-run requires the work item, because a fresh run starts on it and a re-run that cannot read it would spend the stoppage's claim to find that out"))
	}
	if r.Start == nil {
		problems = append(problems, errors.New("a re-run requires a way to start a run"))
	}
	if r.Capacity < 1 {
		problems = append(problems, fmt.Errorf("developer capacity is %d, which starts nothing; a re-run reads the same limit the reservation enforces, so that a full harness costs the stoppage nothing", r.Capacity))
	}
	return errors.Join(problems...)
}

func (r Rerunner) now() time.Time {
	if r.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return r.Clock.Now().UTC()
}

// Render describes what the action did, for whoever asked for it.
func (result RerunResult) Render() string {
	var rendered strings.Builder
	if result.IntakeHeld != nil {
		fmt.Fprintf(&rendered, "INTAKE HELD: nothing was started for %s, since %s\n",
			result.WorkItemID, result.IntakeHeld.HeldAt.UTC().Format(time.RFC3339))
		if reason := strings.TrimSpace(result.IntakeHeld.Reason); reason != "" {
			fmt.Fprintln(&rendered, reason)
		}
		fmt.Fprintf(&rendered, "the stoppage of run %s keeps its one re-run; `yoyo release` lifts the hold, and asking again carries out the same decision\n", result.PriorRunID)
		return rendered.String()
	}
	if result.CapacityFull != nil {
		fmt.Fprintf(&rendered, "WAITING FOR A DEVELOPER: nothing was started for %s, %d active run(s), limit %d\n",
			result.WorkItemID, result.CapacityFull.Active, result.CapacityFull.Limit)
		fmt.Fprintf(&rendered, "the stoppage of run %s keeps its one re-run and the decision still stands, so asking again once a slot frees carries out the same one\n", result.PriorRunID)
		fmt.Fprintf(&rendered, "%s is meanwhile open work the scheduler pulls from, so it can reach a developer without this being asked again\n", result.WorkItemID)
		if result.RecordProblem != "" {
			fmt.Fprintln(&rendered, result.RecordProblem)
		}
		return rendered.String()
	}
	fmt.Fprintf(&rendered, "re-ran %s on the stopped work of run %s\n", result.WorkItemID, result.PriorRunID)
	fmt.Fprintf(&rendered, "chosen because %s\n", result.Reason)
	if result.Outcome.RunID != "" {
		fmt.Fprintf(&rendered, "fresh run: %s\n", result.Outcome.RunID)
	}
	rendered.WriteString(result.Preserved.Render())
	if result.RecordProblem != "" {
		fmt.Fprintln(&rendered, result.RecordProblem)
	}
	return rendered.String()
}
