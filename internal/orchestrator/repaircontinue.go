package orchestrator

// Re-entering a stopped run's repair loop, on the development manager's
// decision.
//
// This is the second triage decision the harness carries out, and it is the
// opposite of the first. A re-run is for a change whose ground moved: nothing
// about the work was wrong, so it starts over against the repository as it now
// stands. This is for a change that is nearly right and ran out of attempts —
// the reviewer's findings are the ones worth acting on, and the developer that
// wrote the change is the one to act on them. So nothing starts over: the same
// run continues, on the branch and in the worktree it stopped in, in the
// developer session that already holds the context, with the findings handed
// back exactly as the reviewer wrote them.
//
// The decision is not this package's, and neither is the size of what it grants.
// The development manager records a repair, which spends the item's repair-grant
// budget as it is recorded and is truncated there to the review rounds the cap
// still had room for. What reaches here is that grant, and everything here is
// the harness acting on it: proving the stoppage is over, proving the worktree
// is still the one the harness left, reading whether it may spend on a provider
// at all, superseding the blocker, and continuing the run.
//
// # Why nothing is written until everything has been asked
//
// The grant is already spent when this runs, so what this action can waste is
// the other thing: a re-entry half made. A condition asked after the item has
// been put back, or after the run has been made live, leaves one of the two
// saying something the other contradicts — and a run recorded as running that
// nothing is running is a state no other reader here would notice. That is not
// hypothetical in shape: the first exercised triage re-run asked the item's
// status past the claim that bounded it, and the next attempt was then refused
// by the once-only guard for a run that had never happened. So everything that
// can refuse is asked first, and the two writes that carry the grant out are the
// last things to happen.
//
// # Why the blocker is superseded rather than left standing
//
// A run that is going again has not stopped, and three readers take the blocker
// on its record as the fact that it has: `yoyo status`, reconciliation, and the
// docket, which re-dockets any terminal run whose blocker still stands. Leaving
// it would make the record lie to all three, and would make the work item —
// blocked by the run that stopped — one the pipeline refuses to resume. So both
// are superseded at the moment of re-entry: the item is put back and told why,
// and the run's blocker is cleared onto the continuation that supersedes it,
// which keeps the words it was recorded in.
//
// # Why the worktree has to be as the harness left it
//
// What a continued developer is handed back is whatever is in that worktree
// now. A worktree somebody has been operating on by hand — or one an agent
// committed in — is one where continuing would hand a developer work nothing
// recorded and then put it through a gate that assumes the harness owns every
// commit. That is a person's decision rather than this action's, so re-entry
// passes the same HEAD-state ownership check every read of a worktree's change
// already passes, and refuses to a person where it does not.
//
// # What the developer is given
//
// Nothing here assembles it. The repair input the run recorded — the reviewer's
// findings, the failing check, or the refused paths — is what the resumed run
// hands back, rebuilt from durable state by the pipeline exactly as it is for a
// run an interrupted process left mid-repair. Guidance a development manager
// wants to add travels in the work item's notes, which every run's context
// bundle already carries, and which cannot grant a protected path.

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

// RepairRuns is the durable run state this action reads and writes. It is the
// re-run's set with one more read: what the harness has already carried out of
// an item's repair grant lives on the runs it was carried out on, so counting it
// takes every run the item has had rather than the one being continued.
//
// It is satisfied by runstate.Store.
type RepairRuns interface {
	RerunRuns
	Recorded() ([]runstate.State, error)
}

// RepairItems is the work item the stopped run is about. Unlike a re-run's, this
// is read and written: the item was blocked by the run stopping, and a re-entry
// that left it blocked would be a continuation the pipeline itself refuses to
// resume. What is written is the triage decision and the claim that supersedes
// the blocker, and nothing else — what becomes of the item is the continued
// run's to record, as it always was.
//
// It is satisfied by beads.Client.
type RepairItems interface {
	Show(ctx context.Context, id string) (beads.WorkItem, error)
	RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error)
	Claim(ctx context.Context, id string) (beads.WorkItem, error)
}

// RepairWorktrees proves the stopped run's worktree is still the one the harness
// left. It is the architect's condition on re-entry and it is deliberately the
// existing gate rather than a second one: anything touched since the blocker
// refuses, and a refusal is a person's to decide about.
//
// It is satisfied by gitworktree.Manager.
type RepairWorktrees interface {
	VerifyOwnedHead(ctx context.Context, worktree gitworktree.Worktree) error
}

// RepairContinueStarter re-enters the harness on one work item. For a run
// recorded as running and resumable — which is what this action leaves behind —
// that is the run being adopted and continued rather than a fresh one started,
// so no selection is passed: why this run exists was recorded when it was
// reserved, and why it is going again is recorded on the run and the item by the
// action itself.
type RepairContinueStarter func(ctx context.Context, workItemID string) (Outcome, error)

// RepairContinuer re-enters one stopped run's repair loop under a grant of
// further repair attempts. It reads the work item and writes the triage decision
// to it, it has no forge access, and it decides nothing about the work: what it
// does is check that a decision somebody else made may be carried out, and then
// carry it out.
type RepairContinuer struct {
	Docket RerunDocket
	Runs   RepairRuns
	Intake IntakeHolds
	// Decisions is the item's durable triage record, which is what says the
	// development manager granted this repair and how much it is worth. Required:
	// a continuation carried out on nobody's grant would be a repair budget of
	// this action's own invention.
	Decisions RerunDecisions
	// Items is the work item the stopped run blocked. Required: the run cannot be
	// resumed while its item says it is waiting on a person.
	Items RepairItems
	// Worktrees proves the preserved worktree is still as the harness left it.
	// Required: the change handed back is whatever is in it.
	Worktrees RepairWorktrees
	// ConfiguredAttempts is `execution.repair_attempts_before_replan`, which is
	// the budget the continued run's loop adds its grants to. It is read here
	// only to report what the run may now spend in total: a carry-out that
	// reported the grant alone would say a run with four attempts had two.
	ConfiguredAttempts int
	// Capacity is execution.max_concurrent_developers as this carry-out read it.
	// Required: re-entry makes a terminal run live again, so a continuation the
	// harness has no room for would put one more developer to work than the
	// operator configured.
	Capacity int
	Start    RepairContinueStarter
	Clock    execution.Clock
}

// RepairContinueRequest is one decision to carry out: the run the docket entry
// names, and the reasoning the development manager recorded for deciding a
// repair of it.
type RepairContinueRequest struct {
	Run    string
	Reason string
}

// RepairContinueResult is what the action did. It reports the continuation it
// made and what became of the run, and it reports just as carefully when it
// continued nothing: an intake hold, a full harness, and a refusal are three
// different things for an operator to do something about.
type RepairContinueResult struct {
	WorkItemID string `json:"work_item_id"`
	RunID      string `json:"run_id"`
	DocketKey  string `json:"docket_key"`
	// Reason is what the run and the item record as why this continuation exists:
	// the grant the harness verified and the reasoning it was given.
	Reason string `json:"reason"`
	// Granted is what this re-entry added to the run's repair budget, out of the
	// Decided rounds the development manager's grant is worth. Truncated says the
	// item's record has a grant the round cap cut, which is what says the item is
	// at the end of what it will be given.
	Granted   int  `json:"granted,omitempty"`
	Decided   int  `json:"decided,omitempty"`
	Truncated bool `json:"truncated,omitempty"`
	// RepairBudget is what the continued run may now spend in total, and
	// RepairAttempts what it had already spent when it stopped.
	RepairBudget   int  `json:"repair_budget,omitempty"`
	RepairAttempts int  `json:"repair_attempts,omitempty"`
	Continued      bool `json:"continued"`
	// SupersededBlocker is the durable blocker the re-entry cleared, in the words
	// it was recorded in.
	SupersededBlocker string `json:"superseded_blocker,omitempty"`
	// IntakeHeld is the operator's hold, when one is what stopped this. Nothing
	// was carried out and nothing was superseded.
	IntakeHeld *runstate.IntakeHold `json:"intake_held,omitempty"`
	// CapacityFull is every developer slot being occupied, when that is what this
	// is waiting on. It is not a refusal and not a failure: nothing was spent, the
	// decision stands, and asking again once a slot frees carries out the same one.
	CapacityFull *runstate.CapacityError `json:"capacity_full,omitempty"`
	Outcome      Outcome                 `json:"outcome"`
	// RecordProblem names a durable record this action could not write once it had
	// begun carrying the grant out. It is reported beside the result rather than
	// in place of it, and never left unsaid: half a re-entry is exactly the state
	// nothing else here will notice.
	RecordProblem string `json:"record_problem,omitempty"`
}

// ErrWorktreeNotAsLeft is what a re-entry refused for the state of the preserved
// worktree unwraps to, so a caller can tell "somebody has been in here" from a
// repository that could not be read without matching on the words of either.
var ErrWorktreeNotAsLeft = errors.New("the preserved worktree is not as the harness left it")

// WorktreeSurgeryError refuses a continuation of a worktree something has
// touched since the run stopped, and says what it is being escalated to a person
// for. Nothing was spent and nothing was superseded: the item is still blocked,
// which is the durable state an escalation would have made anyway.
type WorktreeSurgeryError struct {
	RunID        string
	WorktreePath string
	Cause        error
}

func (e WorktreeSurgeryError) Error() string {
	return fmt.Sprintf(
		"the worktree run %s preserved at %s is not as the harness left it, so its repair loop was not re-entered and nothing was spent: %v; what is in that worktree is what a continued developer would be handed back, so this is a person's to look at — the item stays blocked, and re-entry is refused until somebody says what became of the change",
		e.RunID, e.WorktreePath, e.Cause)
}

func (e WorktreeSurgeryError) Unwrap() error { return ErrWorktreeNotAsLeft }

// Continue carries out one repair-continue decision.
//
// The order is the order the guarantees need. Everything that can refuse is
// asked before anything is written, so a refused continuation leaves the item's
// grant exactly where it was and asking again once the refusal no longer applies
// carries out the same decision; and the item is put back before the run is made
// live again, because a run recorded as running that nothing is running is the
// one half-finished state no other reader here would notice.
func (c RepairContinuer) Continue(ctx context.Context, request RepairContinueRequest) (RepairContinueResult, error) {
	if err := c.validate(); err != nil {
		return RepairContinueResult{}, err
	}
	runID := strings.TrimSpace(request.Run)
	reasoning := strings.TrimSpace(request.Reason)
	if !runstate.ValidRunID(runID) {
		return RepairContinueResult{}, fmt.Errorf("%q is not a run identifier; a triage decision names the run the docket entry is about", request.Run)
	}
	if reasoning == "" {
		return RepairContinueResult{}, errors.New("a repair continues on the development manager's reasoning, and none was given")
	}

	entry, err := docketedStoppage(c.Docket, runID, "repair")
	if err != nil {
		return RepairContinueResult{}, err
	}
	result := RepairContinueResult{
		WorkItemID: entry.WorkItemID,
		RunID:      entry.RunID,
		DocketKey:  entry.Key,
	}
	// The run is adopted before anything is asked of it, and held until the
	// re-entry is written. A stopped run that still owes something is not in
	// flight and would be found by nothing else, so this is the same lease
	// reconciliation takes to act on one: what is read here is what is written,
	// and a sweep settling the same run beside this cannot lose either half.
	prior, lease, err := c.Runs.AdoptRun(ctx, entry.RunID)
	if err != nil {
		return result, fmt.Errorf("take the stopped run to re-enter its repair loop: %w", err)
	}
	defer lease.Release()

	if err := stoppageIsOver(prior); err != nil {
		return result, err
	}
	if err := continuableRepair(prior); err != nil {
		return result, err
	}
	result.RepairAttempts = prior.RepairAttempts
	result.SupersededBlocker = prior.Blocker
	// The architect's condition, asked before anything is written: the change a
	// continued developer is handed back is whatever is in that worktree.
	if err := c.Worktrees.VerifyOwnedHead(ctx, worktreeOf(prior)); err != nil {
		return result, WorktreeSurgeryError{RunID: prior.RunID, WorktreePath: prior.WorktreePath, Cause: err}
	}
	if err := noRunInFlight(c.Runs, entry.WorkItemID); err != nil {
		return result, err
	}
	// That the development manager granted this repair is read rather than taken
	// on trust, and so is how much of that grant is left to carry out.
	granted, err := c.granted(entry.WorkItemID)
	if err != nil {
		return result, err
	}
	result.Granted = granted.attempts
	result.Decided = granted.decided
	result.Truncated = granted.truncated
	item, err := c.Items.Show(ctx, entry.WorkItemID)
	if err != nil {
		return result, fmt.Errorf("read the work item the stoppage is about: %w", err)
	}
	if err := continuableItem(item, entry.WorkItemID); err != nil {
		return result, err
	}
	// The hold is read before anything is written, so a held harness leaves the
	// grant uncarried rather than half re-entering a run it would not continue.
	hold, held, err := c.Intake.Held()
	if err != nil {
		return result, fmt.Errorf("read whether the operator has held intake: %w", err)
	}
	if held {
		result.IntakeHeld = &hold
		return result, nil
	}
	// Capacity is read last of everything asked, because it is the condition most
	// likely to have changed while the rest were being asked and the one a
	// moment's wait settles. A full harness is reported rather than refused: the
	// grant is untouched and the decision stands.
	full, free, err := slotIsFree(c.Runs, c.Capacity)
	if err != nil {
		return result, err
	}
	if !free {
		result.CapacityFull = &full
		return result, nil
	}

	result.Reason = continueReason(entry, granted, reasoning)

	// The item is put back first, because a run made live behind an item that
	// still says it is blocked is a run nothing can resume and nothing will
	// notice. Recording why comes before the claim, so the item never reads as
	// work somebody quietly restarted.
	if err := c.supersedeOnItem(ctx, entry.WorkItemID, result.Reason); err != nil {
		return result, err
	}
	continued, err := c.supersedeOnRun(prior, granted, result.Reason)
	if err != nil {
		return result, fmt.Errorf("record the re-entry on run %s, whose item has already been put back and told why: %w", prior.RunID, err)
	}
	result.RepairBudget = continued.RepairBudget(c.ConfiguredAttempts)
	result.RepairAttempts = continued.RepairAttempts
	result.Continued = true
	// The lease is given up before the run is continued, because continuing it is
	// the pipeline adopting the same run: holding it here would refuse the very
	// process this action exists to start.
	lease.Release()

	outcome, runErr := c.Start(ctx, entry.WorkItemID)
	result.Outcome = outcome
	return result, runErr
}

// repairGrant is the development manager's grant as this carry-out found it:
// how many of its rounds are left to carry out, how many it was worth in total,
// and whether the round cap cut it when it was recorded.
type repairGrant struct {
	attempts  int
	decided   int
	truncated bool
}

// granted reports a repair grant of the development manager's that this
// re-entry may carry out, and refuses where there is none. It is two questions
// and the second is what makes the first mean anything.
//
// The item's repair-grant counter is the decision's own footprint: the
// development manager spends it as the decision is recorded and before anything
// acts on it, so an item carrying none is an item nobody decided this about, and
// an item whose grant the round cap refused carries none either. Its size is
// what the project configured a grant to be worth, truncated to the rounds the
// cap had room for — which is why the size is read from the record rather than
// from the configuration a second time: a carry-out working from its own reading
// would hand a run more attempts than the cap ever let the item have.
//
// But the counter is a total and is never cleared, so on its own it cannot tell
// a grant waiting to be carried out from one carried out last month. So what has
// already been carried out is read back against it, from the continuations the
// item's runs record, and a grant with nothing left is refused: a further
// stoppage needs a further decision, which past the cap is an escalation rather
// than a larger budget.
func (c RepairContinuer) granted(workItemID string) (repairGrant, error) {
	counters, err := c.Decisions.Counters(workItemID)
	if err != nil {
		return repairGrant{}, fmt.Errorf("read what triage has recorded about %s: %w", workItemID, err)
	}
	if counters.RepairGrants < 1 {
		return repairGrant{}, fmt.Errorf(
			"triage has granted %s no repair, so there is no decision here to carry out: the development manager records the decision, which spends the item's repair budget, before the harness continues anything on it",
			workItemID)
	}
	carried, err := c.carriedOut(workItemID)
	if err != nil {
		return repairGrant{}, err
	}
	remaining := counters.GrantedRounds - carried
	if remaining < 1 {
		return repairGrant{}, fmt.Errorf(
			"triage has granted %s %d repair attempt(s) and the harness has carried out %d, so there is nothing of that grant left to re-enter on: a further stoppage of an item that has already been handed back needs a further decision, which past the cap is an escalation rather than a larger budget",
			workItemID, counters.GrantedRounds, carried)
	}
	return repairGrant{attempts: remaining, decided: counters.GrantedRounds, truncated: counters.TruncatedGrants > 0}, nil
}

// carriedOut is how much of one item's repair grant the harness has already
// handed to a run. It is counted from the continuations recorded on the item's
// runs rather than from a counter of its own, because those are what the grant
// actually bought: a continuation is written before the developer is invoked, so
// an attempt nobody made still counts, which is the direction that keeps one
// decision from starting two continuations.
func (c RepairContinuer) carriedOut(workItemID string) (int, error) {
	recorded, err := c.Runs.Recorded()
	if err != nil {
		return 0, fmt.Errorf("read the runs %s has had, to count what its repair grant has already bought: %w", workItemID, err)
	}
	carried := 0
	for _, state := range recorded {
		if state.WorkItemID == workItemID {
			carried += state.GrantedRepairAttempts()
		}
	}
	return carried, nil
}

// continuableRepair reports a stopped run whose repair loop there is something to
// re-enter. Every condition is one the pipeline would otherwise meet past the
// grant: it resumes from durable state alone, so a run missing any of what that
// state has to supply would be refused after the item's budget had been spent on
// it.
//
// The recorded repair input is the last of them and the one that makes this the
// action it is. A run that stopped with no failure returned to it — a provider
// that kept refusing, a replay that conflicted — has no repair loop to continue:
// what it needs is a re-run or a person, and handing it another repair budget
// would buy attempts at a failure nobody ever showed the developer.
func continuableRepair(prior runstate.State) error {
	if prior.WorktreePath == "" || prior.Branch == "" || prior.BaseCommit == "" || prior.TargetBranch == "" {
		return fmt.Errorf("run %s recorded no preserved worktree to continue in, so there is no change to repair; a fresh run of the item is what it needs", prior.RunID)
	}
	if prior.WorktreeRemoved || prior.BranchRemoved {
		return fmt.Errorf("what run %s preserved has already been retired, so the change it stopped with is gone and there is nothing to continue", prior.RunID)
	}
	if prior.ProviderSessionID == "" {
		return fmt.Errorf("run %s recorded no developer session, so a continuation could not be the same developer carrying on with the change it made", prior.RunID)
	}
	if prior.PathRefusal == nil && prior.CheckFailure == nil && len(prior.ReviewFindingDetails) == 0 {
		return fmt.Errorf("run %s recorded no reviewer findings, failing check, or refused paths, so no failure was ever returned to its developer and there is no repair loop to re-enter: %s stopped for something a repair budget does not answer",
			prior.RunID, prior.RunID)
	}
	return nil
}

// continuableItem reports a work item a stopped run may be continued on. It is
// deliberately narrower than the condition a fresh run is held to: an item
// blocked by its own run stopping is exactly what this action expects to find,
// and requiring it to have been put back first is the remembered reopen this
// action exists to remove. What it refuses is an item nothing may be run on at
// all — one somebody closed, or one waiting on other work.
func continuableItem(item beads.WorkItem, workItemID string) error {
	if item.ID != workItemID {
		return fmt.Errorf("Beads returned work item %q for requested id %q", item.ID, workItemID)
	}
	switch item.Status {
	case "open", "in_progress", "blocked":
	default:
		return fmt.Errorf("work item %s status is %q, so it is not one a stopped run may be continued on; nothing was spent, so the same decision is carried out by asking again once it is", item.ID, item.Status)
	}
	var blockers []string
	for _, dependency := range item.Dependencies {
		if dependency.Type == "blocks" && dependency.Status != "closed" {
			blockers = append(blockers, dependency.ID)
		}
	}
	if len(blockers) > 0 {
		return fmt.Errorf("work item %s is blocked by: %s; nothing was spent, so the same decision is carried out by asking again once they are closed",
			item.ID, strings.Join(blockers, ", "))
	}
	return nil
}

// supersedeOnItem records the decision on the work item and puts it back to work
// the harness may continue. Both halves are the supersession: the note is what
// the next reader of the item finds instead of deciding the stoppage a second
// time, and the claim is what stops the item saying it is waiting on a person
// while a developer is working on it.
func (c RepairContinuer) supersedeOnItem(ctx context.Context, workItemID, reason string) error {
	if _, err := c.Items.RecordOutcome(ctx, workItemID, reason); err != nil {
		return fmt.Errorf("record the repair decision on %s: %w", workItemID, err)
	}
	item, err := c.Items.Claim(ctx, workItemID)
	if err != nil {
		return fmt.Errorf("put %s back to work for the repair it was granted: %w", workItemID, err)
	}
	// What bd reports back is checked rather than assumed, for the reason every
	// other write here is: an item that still says it is blocked is one the
	// pipeline refuses to resume, and finding that out from the refusal would
	// cost the grant that has already been spent.
	if err := validateClaimedItem(item, workItemID); err != nil {
		return fmt.Errorf("validate the work item put back for its repair: %w", err)
	}
	return nil
}

// supersedeOnRun makes the stopped run live again under its grant, and reports
// the record it wrote.
//
// The attempt this re-entry is about is counted here rather than left to the
// resumed run, and that is what makes the grant mean what it says. The pipeline
// re-runs an attempt that was in flight when a process stopped rather than
// counting it a second time, which is right for an interruption and wrong here:
// this attempt was never made, so a continuation that did not count it would
// hand the item one more developer invocation than the operator configured a
// grant to be worth. It is recorded before the developer is invoked, exactly as
// the repair loop's own attempts are.
func (c RepairContinuer) supersedeOnRun(prior runstate.State, granted repairGrant, reason string) (runstate.State, error) {
	continued := prior
	continued.RepairContinuations = append(append([]runstate.RepairContinuation{}, prior.RepairContinuations...),
		runstate.RepairContinuation{
			GrantedAttempts:   granted.attempts,
			Reason:            reason,
			ContinuedAt:       c.now(),
			SupersededBlocker: prior.Blocker,
		})
	continued.RepairAttempts = prior.RepairAttempts + 1
	// The blocker is cleared onto the continuation that supersedes it. A terminal
	// run whose blocker still stands is what the docket re-dockets and what
	// `yoyo status` reports as stopped work, and this run has not stopped.
	continued.Blocker = ""
	// The failure the run ended on describes the stoppage this supersedes, and a
	// run that is going again has not failed.
	continued.Failure = ""
	continued.Status = runstate.StatusRunning
	continued.Phase = runstate.PhaseDeveloping
	continued.CompletedAt = nil
	continued.UpdatedAt = c.now()
	if err := c.Runs.Save(continued); err != nil {
		return runstate.State{}, err
	}
	return continued, nil
}

// continueReason is what the run and the item record as why this run is going
// again: the grant the harness verified, and the reasoning it was given.
//
// The two are worded apart on purpose, exactly as a re-run's reason is. That the
// item was granted another repair is a fact read from its durable triage record
// and is stated as one; the prose after it arrived with the instruction to carry
// the decision out, and is attributed to that rather than quoted as the
// development manager's own words.
func continueReason(entry triage.Entry, granted repairGrant, reasoning string) string {
	grant := fmt.Sprintf("%d further repair attempt(s)", granted.attempts)
	if granted.truncated {
		grant = fmt.Sprintf("%d further repair attempt(s), from a grant the review-round cap had already cut to %d",
			granted.attempts, granted.decided)
	}
	reason := fmt.Sprintf(
		"Triaged: the repair loop of run %s was re-entered on the change it already has, under a grant of %s recorded against %s's durable triage budget. The durable blocker that run stopped on is superseded by this re-entry. The reasoning given to the harness when it was asked to: ",
		entry.RunID, grant, entry.WorkItemID)
	// The reasoning is folded to what the run's record will hold rather than
	// refused: losing the end of a long argument is better than refusing to carry
	// out a decision because of its length.
	return reason + singleLine(reasoning, runstate.MaxSelectionReasonBytes-len(reason))
}

func (c RepairContinuer) validate() error {
	var problems []error
	if c.Docket == nil {
		problems = append(problems, errors.New("a repair requires the triage docket the decision was made against"))
	}
	if c.Runs == nil {
		problems = append(problems, errors.New("a repair requires the durable run state"))
	}
	if c.Intake == nil {
		problems = append(problems, errors.New("a repair requires the intake hold, because continuing a run spends on a provider"))
	}
	if c.Decisions == nil {
		problems = append(problems, errors.New("a repair requires the item's triage budget, which is what says the development manager granted one and what bounds it"))
	}
	if c.Items == nil {
		problems = append(problems, errors.New("a repair requires the work item, because the run that stopped blocked it and a blocked item is not one a run may be resumed on"))
	}
	if c.Worktrees == nil {
		problems = append(problems, errors.New("a repair requires the worktree, because what a continued developer is handed back is whatever is in it"))
	}
	if c.Start == nil {
		problems = append(problems, errors.New("a repair requires a way to continue the run"))
	}
	if c.ConfiguredAttempts < 0 {
		problems = append(problems, fmt.Errorf("the configured repair budget is %d, which is not a budget; execution.repair_attempts_before_replan is what states it", c.ConfiguredAttempts))
	}
	if c.Capacity < 1 {
		problems = append(problems, fmt.Errorf("developer capacity is %d, which runs nothing; a repair reads the same limit the reservation enforces, so that a full harness costs the item nothing", c.Capacity))
	}
	return errors.Join(problems...)
}

func (c RepairContinuer) now() time.Time {
	if c.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return c.Clock.Now().UTC()
}

// Render describes what the action did, for whoever asked for it.
func (result RepairContinueResult) Render() string {
	var rendered strings.Builder
	if result.IntakeHeld != nil {
		fmt.Fprintf(&rendered, "INTAKE HELD: nothing was continued for %s, since %s\n",
			result.WorkItemID, result.IntakeHeld.HeldAt.UTC().Format(time.RFC3339))
		if reason := strings.TrimSpace(result.IntakeHeld.Reason); reason != "" {
			fmt.Fprintln(&rendered, reason)
		}
		fmt.Fprintf(&rendered, "%s keeps its repair grant; `yoyo release` lifts the hold, and asking again carries out the same decision\n", result.WorkItemID)
		return rendered.String()
	}
	if result.CapacityFull != nil {
		fmt.Fprintf(&rendered, "WAITING FOR A DEVELOPER: nothing was continued for %s, %d active run(s), limit %d\n",
			result.WorkItemID, result.CapacityFull.Active, result.CapacityFull.Limit)
		fmt.Fprintf(&rendered, "%s keeps its repair grant and the decision still stands, so asking again once a slot frees carries out the same one\n", result.WorkItemID)
		return rendered.String()
	}
	fmt.Fprintf(&rendered, "re-entered the repair loop of run %s on the change it already has\n", result.RunID)
	fmt.Fprintf(&rendered, "carried out %d further repair attempt(s)", result.Granted)
	if result.Truncated {
		fmt.Fprintf(&rendered, ", from a grant the review-round cap had already cut to %d", result.Decided)
	}
	fmt.Fprintf(&rendered, "; %d of %d attempt(s) now spent\n", result.RepairAttempts, result.RepairBudget)
	fmt.Fprintf(&rendered, "continued because %s\n", result.Reason)
	if result.SupersededBlocker != "" {
		fmt.Fprintf(&rendered, "superseded blocker: %s\n", singleLine(result.SupersededBlocker, 240))
	}
	if result.RecordProblem != "" {
		fmt.Fprintln(&rendered, result.RecordProblem)
	}
	return rendered.String()
}
