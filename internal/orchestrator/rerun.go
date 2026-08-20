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
// start work at all, proving the stoppage is really over, claiming the one re-run
// that stoppage gets, and starting the run.
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

// RerunRuns is the durable run state this action reads. Both halves are the
// architect's condition rather than convenience: the stopped run's own record is
// what proves the stoppage is terminal, and what is in flight is what proves the
// item has no live run to collide with.
type RerunRuns interface {
	Load(runID string) (runstate.State, error)
	Incomplete() ([]runstate.State, error)
}

// RerunRecords is where the one re-run a docketed stoppage gets is claimed and
// settled. It is satisfied by runstate.RerunStore.
type RerunRecords interface {
	Claim(ctx context.Context, rerun runstate.Rerun) (runstate.Rerun, error)
	Settle(ctx context.Context, docketKey, runID string, preserved runstate.PreservedArtifacts) (runstate.Rerun, error)
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

// Rerunner starts a fresh run of an item triage decided to run again. It has no
// tracker and no forge access, and it decides nothing about the work: what it
// does is check that a decision somebody else made may be carried out, and then
// carry it out.
type Rerunner struct {
	Docket RerunDocket
	Runs   RerunRuns
	Intake IntakeHolds
	Reruns RerunRecords
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
	Outcome    Outcome              `json:"outcome"`
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
		Reason:     rerunReason(entry, reasoning),
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
	result.Outcome = outcome
	result.Preserved = r.settle(ctx, entry, prior, outcome, &result)
	return result, runErr
}

// entry finds the docketed stoppage a decision is about. A run that is not on
// the docket is refused rather than run: the docket entry is what a re-run is
// counted against, so a re-run of something nothing docketed would be a re-run
// nothing bounds.
func (r Rerunner) entry(priorRunID string) (triage.Entry, error) {
	entries, err := r.Docket.List()
	if err != nil {
		return triage.Entry{}, fmt.Errorf("read the triage docket: %w", err)
	}
	for _, candidate := range entries {
		if candidate.Class == triage.ClassStoppedRun && candidate.RunID == priorRunID {
			return candidate, nil
		}
	}
	return triage.Entry{}, fmt.Errorf("no stopped run of %s is on the triage docket, so there is no stoppage to run again", priorRunID)
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

// noRunInFlight refuses a re-run of an item something is already running. The
// reservation refuses a second run of one item anyway; this is the same rule
// asked before anything is claimed, so a collision costs the stoppage's re-run
// nothing.
func (r Rerunner) noRunInFlight(workItemID string) error {
	incomplete, err := r.Runs.Incomplete()
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

// settle records what became of the stopped run's artifacts and reports the
// disposition it recorded. They are kept until the fresh run integrates, which
// is the moment what the stopped run holds stops being what somebody might
// cherry-pick from; then they are retired explicitly, and what could not be
// retired is kept with the reason, because an artifact nobody records is an
// orphan nobody discovers.
func (r Rerunner) settle(ctx context.Context, entry triage.Entry, prior runstate.State, outcome Outcome, result *RerunResult) runstate.PreservedArtifacts {
	preserved := preservedOf(prior)
	if preserved.Disposition == runstate.PreservedKept && outcome.Integration != nil {
		preserved = r.retire(ctx, prior, preserved)
	}
	settled, err := r.Reruns.Settle(ctx, entry.Key, outcome.RunID, preserved)
	if err != nil {
		result.RecordProblem = fmt.Sprintf(
			"the re-run of %s ran, and what became of the branch and worktree run %s preserved could not be recorded: %v",
			entry.WorkItemID, entry.RunID, err)
		return preserved
	}
	return settled.Preserved
}

// retire removes what the stopped run preserved, now that the fresh run has
// integrated the work. Nothing here fails the re-run: the work landed, and an
// artifact that has to be looked at by hand is a fact to record rather than a
// reason to report a successful run as a failure.
func (r Rerunner) retire(ctx context.Context, prior runstate.State, preserved runstate.PreservedArtifacts) runstate.PreservedArtifacts {
	if r.Preserved == nil {
		preserved.Problem = "nothing is wired to retire what the stopped run preserved, so it is still there"
		return preserved
	}
	retirement, err := r.Preserved.RetirePreserved(ctx, worktreeOf(prior), prior.TargetBranch)
	if err == nil && retirement.Retired() {
		retired := r.now()
		preserved.Disposition = runstate.PreservedRetired
		preserved.RetiredAt = &retired
		return preserved
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
	if err != nil {
		preserved.Problem = fmt.Sprintf("retiring what run %s preserved failed: %v", prior.RunID, err)
		return preserved
	}
	preserved.Problem = retirement.Kept()
	return preserved
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

// rerunReason is what the fresh run records as why it exists. It names the
// decision, the stoppage it settles, and the development manager's own reasoning,
// because a reason that only said "triage decided" would account for a run
// without explaining it.
func rerunReason(entry triage.Entry, reasoning string) string {
	reason := fmt.Sprintf("the development manager triaged the stopped run %s of %s as a re-run, and the harness started this run on that decision: ",
		entry.RunID, entry.WorkItemID)
	// The reasoning is folded to what the run's recorded selection will hold. It
	// is the development manager's prose, so it is bounded rather than refused:
	// losing the end of a long argument is better than refusing to carry out a
	// decision because of its length.
	return reason + singleLine(reasoning, runstate.MaxSelectionReasonBytes-len(reason))
}

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
	if r.Start == nil {
		problems = append(problems, errors.New("a re-run requires a way to start a run"))
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
		fmt.Fprintf(&rendered, "the stoppage of run %s keeps its one re-run; release the hold and ask again\n", result.PriorRunID)
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
