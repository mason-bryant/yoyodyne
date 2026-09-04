package orchestrator

// Repeating a merge request the forge dropped, on the development manager's
// decision.
//
// A queued merge the forge gives up on leaves an integrated change published and
// unmerged. Most of those are somebody's work — a conflict with the base branch,
// a protection rule the request does not satisfy — and the harness never merges
// past one, not with administrator privileges and not by asking again. Some are
// not: a required check that never finished, an auto-merge race lost to another
// request that landed first. Nothing about the change is wrong in those, and what
// the publication needs is the same merge request made again.
//
// So this repeats it, and repeats exactly it. The pull request is the one the
// reviewer's verdict authorized, the method is the one that verdict's own merge
// was made by, read off the run's record rather than chosen here, and the head
// commit is pinned to the commit that was integrated. Nothing is overridden to
// get there: the request goes back through the forge's whole requirement
// machinery, which is why repeating it is not merging past a requirement.
//
// # What refuses, and why each is asked before anything is spent
//
// The order is the order the guarantees need, and it is the re-run's order for
// the re-run's reason: the publication gets one re-arm, spent by recording it, so
// a condition asked after the record spends the very budget it refuses.
//
//   - The run that made the publication has to be terminally recorded. That is
//     the same precondition the re-run's carry-out asks, and it is here because of
//     what happened without it: an agent re-armed PR #92 while the run that owned
//     it was still alive, the forge merged an earlier approved promotion into the
//     branch mid-run, the run's own republish then failed against a request it
//     could no longer publish into, and a 117-line amendment was stranded on the
//     preserved branch for somebody to recover by hand. A re-arm against a live
//     run's publication is indistinguishable from that mistake.
//   - The forge's own merge state has to name nothing only a person can supply.
//     publish.AwaitsOnlyAPerson draws that line, and a state that could not be
//     read refuses: this is a gate, and the safe answer for a gate is no.
//   - The request has to be unchanged. Its head is still the commit the run
//     integrated, and the remote target still passes the same pre-merge content
//     check the original gate ran — the identical check publishIntegration makes,
//     rather than a second rendering of it.
//   - The publication has to have a re-arm decision of the development manager's
//     that nothing has carried out. The decision is recorded on the work item and
//     keyed to this publication; what says it has been acted on is the count on
//     the publication's own record.
//
// # Why the promotion lease
//
// A re-arm is an integration retry against the target branch, and
// `one-promotion-per-target-branch` binds it exactly as it binds the promotion
// the run made: this asks a forge to move the remote target, so it queues behind
// whatever is promoting into that branch now. The lease is the harness's own and
// this is the harness's own process — no agent acquires it and no agent performs
// the merge, which is the same boundary that keeps the roles that authorize a
// promotion from being able to make one. It is taken after the run's own lease,
// which is the order everything else that holds both takes them in; repeat says
// why that order and not the other.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// RearmRuns is the durable run state this action reads, writes, and serializes
// against. The publication it repeats lives on the run's own record, so the
// count of what has been repeated is written there too, under that run's lease
// and under the target branch's promotion lease.
type RearmRuns interface {
	Load(runID string) (runstate.State, error)
	Incomplete() ([]runstate.State, error)
	AdoptRun(ctx context.Context, runID string) (runstate.State, *runstate.Lease, error)
	Save(state runstate.State) error
	LeasePromotion(ctx context.Context, targetBranch string) (*runstate.Lease, error)
}

// RearmForge is the forge access a re-arm needs: what the request looks like
// now, what the forge says is unmet on it, and the merge request itself. It is
// satisfied by publish.GitHub.
type RearmForge interface {
	State(ctx context.Context, head string) (publish.PullRequest, error)
	MergeState(ctx context.Context, number int) (string, error)
	Merge(ctx context.Context, request publish.MergeRequest) (publish.MergeResult, error)
}

// RearmWorktrees is the repository access a re-arm needs, and it is one read:
// the pre-merge check on the remote target that the original gate ran. Nothing
// here moves a ref.
type RearmWorktrees interface {
	VerifyRemoteTarget(ctx context.Context, integration gitworktree.Integration) error
}

// RearmDecisions is the harness's own durable record of what triage has decided
// about one work item, which is what proves a re-arm was decided at all: the
// development manager's decision spends the publication's re-arm budget as it is
// recorded, so a publication carrying none is a publication nobody decided this
// about.
//
// It is satisfied by runstate.TriageStore.
type RearmDecisions interface {
	Counters(workItemID string) (runstate.TriageCounters, error)
}

// Rearmer repeats one merge request the forge dropped. It decides nothing about
// the work: what it does is check that a decision somebody else made may be
// carried out, and then make the identical request the reviewer's verdict
// already authorized.
type Rearmer struct {
	Docket    RerunDocket
	Runs      RearmRuns
	Forge     RearmForge
	Worktrees RearmWorktrees
	// Decisions is the item's durable triage record, which is what says the
	// development manager decided this at all. Required: a re-arm made on nobody's
	// decision would ask a forge for a merge that no recorded decision stands
	// behind.
	Decisions RearmDecisions
	// Caps are the ceilings the guards refuse against, as every other reader of
	// the same record assembles them. The re-arm's own is read per publication.
	Caps  runstate.TriageCaps
	Clock execution.Clock
}

// RearmRequest is one decision to carry out: the run whose publication the
// docket entry names, and the reasoning the development manager recorded for
// deciding a re-arm of it.
type RearmRequest struct {
	Run    string
	Reason string
}

// RearmResult is what the action did. It reports the request it repeated and
// what the forge answered, and it says which of the two answers it was: a merge
// the forge queued leaves the run outstanding for reconciliation to settle, and
// one it performed on the spot is a publication reconciliation finishes on its
// next sweep.
type RearmResult struct {
	WorkItemID string `json:"work_item_id"`
	RunID      string `json:"run_id"`
	DocketKey  string `json:"docket_key"`
	Number     int    `json:"number"`
	URL        string `json:"url,omitempty"`
	// Method is what the repeated request was made by, which is the method the
	// run's own merge recorded rather than anything chosen here.
	Method string `json:"method,omitempty"`
	// HeadCommit is the commit the repeated request was pinned to, which is the
	// commit the run integrated.
	HeadCommit string `json:"head_commit,omitempty"`
	// Reason is what the harness recorded as why the request was repeated: the
	// decision it verified, and the reasoning it was given.
	Reason string `json:"reason,omitempty"`
	// Rearmed reports the request having actually been repeated, and Queued the
	// forge having accepted it to perform later rather than performing it now.
	Rearmed bool `json:"rearmed"`
	Queued  bool `json:"queued"`
	// Rearms is what this publication's durable counter stands at afterwards.
	Rearms int `json:"rearms,omitempty"`
	// RecordProblem names a durable record this action could not update after the
	// request was made. The request happened either way, so it is reported beside
	// the result rather than in place of it.
	RecordProblem string `json:"record_problem,omitempty"`
}

// Rearm carries out one re-arm decision.
//
// Everything that can refuse is asked before the counter is spent, so a refused
// re-arm costs the publication nothing; the counter is written before the
// request is made, so a process that dies between the two has recorded a re-arm
// it did not make rather than made one it did not record; and what the forge
// answered is recorded after it answers.
func (r Rearmer) Rearm(ctx context.Context, request RearmRequest) (RearmResult, error) {
	if err := r.validate(); err != nil {
		return RearmResult{}, err
	}
	runID := strings.TrimSpace(request.Run)
	reasoning := strings.TrimSpace(request.Reason)
	if !runstate.ValidRunID(runID) {
		return RearmResult{}, fmt.Errorf("re-arm %q is not a run identifier; a triage decision names the run the docket entry is about", request.Run)
	}
	if reasoning == "" {
		return RearmResult{}, errors.New("a re-arm records the development manager's reasoning as why the request was repeated, and none was given")
	}

	prior, err := r.Runs.Load(runID)
	if err != nil {
		return RearmResult{}, fmt.Errorf("read the run the docket entry is about: %w", err)
	}
	published, integration, err := rearmablePublication(prior)
	if err != nil {
		return RearmResult{}, err
	}
	result := RearmResult{
		WorkItemID: prior.WorkItemID,
		RunID:      prior.RunID,
		DocketKey:  triage.PublicationKey(prior.RunID, published.Number),
		Number:     published.Number,
		URL:        published.URL,
		Method:     published.MergeMethod,
		HeadCommit: published.HeadCommit,
	}
	// The publication has to be on the docket, for the reason a stoppage does: the
	// entry is what the development manager decided against, and a re-arm of
	// something nothing docketed is a re-arm nothing bounds.
	if err := r.docketed(result.DocketKey, prior.RunID); err != nil {
		return result, err
	}
	// The run's own record, not the entry that describes it, is what says the
	// publication is free to be acted on. Both halves are the same condition
	// stated at the two scales a mistake has actually happened at.
	if err := publicationIsSettled(prior, published); err != nil {
		return result, err
	}
	if err := noRearmRunInFlight(r.Runs, prior.WorkItemID); err != nil {
		return result, err
	}
	// That the development manager decided this, and that nothing has carried the
	// decision out, are read before anything is asked of the forge.
	decided, err := r.decided(prior, published, result.DocketKey)
	if err != nil {
		return result, err
	}
	result.Reason = rearmReason(prior, published, decided, result.DocketKey, reasoning)
	// What the forge says now decides whether this is a drop worth repeating at
	// all, and it is asked before the local check because it is the cheaper of the
	// two and the one that refuses most re-arms.
	if err := r.forgeWouldTakeItBack(ctx, prior, published, integration); err != nil {
		return result, err
	}
	if err := r.Worktrees.VerifyRemoteTarget(ctx, gitworktree.Integration{
		Branch:               prior.Branch,
		TargetBranch:         integration.TargetBranch,
		SourceCommit:         integration.SourceCommit,
		TargetCommit:         integration.TargetCommit,
		PreviousTargetCommit: integration.PreviousTargetCommit,
	}); err != nil {
		return result, fmt.Errorf("check the remote target branch before repeating the merge request: %w; nothing was spent, so the publication keeps its re-arm", err)
	}

	return r.repeat(ctx, prior.RunID, integration.TargetBranch, published.MergeRearms, result)
}

// repeat spends the publication's re-arm and makes the request, under the run's
// own lease so that the counter and the answer are written onto the record they
// were read from, and under the target branch's promotion lease so that the
// merge queues where every promotion into that branch queues.
//
// The two are taken in that order, which is the order every other holder of both
// takes them: the pipeline holds its run's lease across the promotion, and
// reconciliation adopts a run before it catches its target branch up. Taking the
// promotion lease first would have this waiting on a branch while a sweep waits
// on the run this holds. Only the second wait blocks — a run somebody else holds
// is refused here rather than queued for.
//
// The record is re-read under the lease rather than reused, for the reason every
// other adoption here re-reads: what is rewritten has to be what is on disk now,
// and a run something settled while the forge was being asked has had its
// publication written by whatever settled it.
func (r Rearmer) repeat(ctx context.Context, runID, targetBranch string, checked int, result RearmResult) (RearmResult, error) {
	state, lease, err := r.Runs.AdoptRun(ctx, runID)
	if err != nil {
		return result, fmt.Errorf("take run %s to record the re-arm of its publication: %w; nothing was spent, so the publication keeps its re-arm", runID, err)
	}
	defer func() { _ = lease.Release() }()

	promotion, err := r.Runs.LeasePromotion(ctx, targetBranch)
	if err != nil {
		return result, fmt.Errorf("wait for a turn to move %s: %w; nothing was spent, so the publication keeps its re-arm", targetBranch, err)
	}
	defer func() { _ = promotion.Release() }()

	published, _, err := rearmablePublication(state)
	if err != nil {
		return result, fmt.Errorf("run %s was settled while its publication was being checked: %w", runID, err)
	}
	if published.Number != result.Number || published.MergeRearms != checked {
		return result, fmt.Errorf("run %s was settled while its publication was being checked, so what would be repeated is no longer what was checked", runID)
	}
	// The counter is written before the request is made, which is the direction
	// every triage counter fails in. A process that dies here has recorded a
	// re-arm it did not make, and the publication has spent the one it had.
	published.MergeRearms++
	state.PullRequest = &published
	state.UpdatedAt = r.now()
	if err := r.Runs.Save(state); err != nil {
		return result, fmt.Errorf("record the re-arm of pull request %d before repeating it: %w; nothing was asked of the forge", published.Number, err)
	}
	result.Rearms = published.MergeRearms

	merge, mergeErr := r.Forge.Merge(ctx, publish.MergeRequest{
		Number:     published.Number,
		HeadCommit: published.HeadCommit,
		Method:     publish.MergeMethod(published.MergeMethod),
	})
	if mergeErr != nil {
		return result, fmt.Errorf("repeat the merge request for pull request %d: %w; the re-arm is spent and recorded, so a further drop is an escalation rather than another re-arm",
			published.Number, mergeErr)
	}
	result.Rearmed = true
	result.Queued = merge.Queued
	// The run goes back where reconciliation settles a merge, which is exactly
	// where its original merge left it, and it goes there on either answer. A
	// queued merge is the state's own case. A merge the forge performed on the
	// spot is the same thing one step further on: this action has no worktree to
	// finish a publication with — confirming the remote target, recording the
	// merge commit, deleting the consumed branch, catching the local target up —
	// and reconciliation does all four for a merge it finds landed. Recording it
	// as settled here would leave a publication half-finished that nothing would
	// come back to.
	//
	// The publication's earlier failure is cleared with it, because what it said
	// was that the forge had dropped the merge and the forge has taken one again.
	published.MergeQueued = true
	state.PullRequest = &published
	state.PublishFailure = ""
	state.UpdatedAt = r.now()
	if err := r.Runs.Save(state); err != nil {
		result.RecordProblem = fmt.Sprintf(
			"the merge request for pull request %d was repeated and the run's record still says the forge dropped it, so reconciliation will not settle what the forge does next: %v",
			published.Number, err)
	}
	return result, nil
}

// rearmablePublication is the publication a re-arm would repeat, and the
// promotion it carries. Every condition here is about the record being able to
// describe a repeated request at all: what is repeated is a merge of a promoted
// commit, by a recorded method, of a request the forge has not merged.
func rearmablePublication(state runstate.State) (runstate.PullRequest, runstate.Integration, error) {
	if state.PullRequest == nil {
		return runstate.PullRequest{}, runstate.Integration{}, fmt.Errorf("run %s published nothing, so it has no merge request to repeat", state.RunID)
	}
	published := *state.PullRequest
	if state.Integration == nil {
		return runstate.PullRequest{}, runstate.Integration{}, fmt.Errorf(
			"run %s recorded no promotion, so pull request %d carries nothing this harness integrated and its merge is not one to repeat", state.RunID, published.Number)
	}
	if published.Merged {
		return runstate.PullRequest{}, runstate.Integration{}, fmt.Errorf("pull request %d is merged, so there is no dropped merge to repeat", published.Number)
	}
	if strings.TrimSpace(published.MergeMethod) == "" {
		return runstate.PullRequest{}, runstate.Integration{}, fmt.Errorf(
			"run %s records no merge method for pull request %d, so nothing says which request the reviewer's verdict authorized; a re-arm repeats that request rather than making one of its own",
			state.RunID, published.Number)
	}
	if published.HeadCommit != state.Integration.SourceCommit {
		return runstate.PullRequest{}, runstate.Integration{}, fmt.Errorf(
			"pull request %d carries %s and run %s promoted %s, so repeating its merge would put on the remote a change the authoritative branch does not have",
			published.Number, published.HeadCommit, state.RunID, state.Integration.SourceCommit)
	}
	return published, *state.Integration, nil
}

// publicationIsSettled reports the run that made the publication being over.
//
// It is the re-run's stoppage precondition applied to the other class of stopped
// work, and it is one condition rather than two: a run still in flight owns its
// own publication, and asking a forge to merge a request that run is still
// working on is what stranded a hand-written amendment on a preserved branch on
// 2026-08-19. A merge the forge is still holding is not a dropped one either, so
// there is nothing to repeat while one is queued.
func publicationIsSettled(state runstate.State, published runstate.PullRequest) error {
	if !state.Status.Terminal() {
		return fmt.Errorf("run %s is recorded as %s rather than ended, so its publication is that run's to finish; a re-arm is refused while anything of it is live",
			state.RunID, state.Status)
	}
	if published.MergeQueued {
		return fmt.Errorf("the forge still has the merge of pull request %d queued, so nothing was dropped and there is nothing to repeat", published.Number)
	}
	return nil
}

// noRearmRunInFlight refuses a re-arm of an item something is already running,
// which is the same rule every triage action that acts on an item's work asks: a
// live run of the item may promote and publish while this is asking a forge
// about the last publication, and the two would be merging into one branch at
// once.
func noRearmRunInFlight(runs RearmRuns, workItemID string) error {
	incomplete, err := runs.Incomplete()
	if err != nil {
		return fmt.Errorf("read what is already in flight: %w", err)
	}
	for _, state := range incomplete {
		if state.WorkItemID == workItemID {
			return fmt.Errorf("%s already has run %s in flight in status %s, so its publication is not settled work to repeat a merge of",
				workItemID, state.RunID, state.Status)
		}
	}
	return nil
}

// docketed reports the publication being on the triage docket, which is what the
// development manager decided against.
//
// The key an entry was written under is accepted in either of its two shapes.
// The docket is an append-only log nothing rewrites, so an entry recorded before
// the pull request joined the key names the run alone, and refusing a re-arm for
// the age of the entry would refuse exactly the oldest publications nobody has
// got to yet. What the re-arm is counted under is the current shape either way:
// that key is derived from the run's own record rather than from the entry.
func (r Rearmer) docketed(key, runID string) error {
	entries, err := r.Docket.List()
	if err != nil {
		return fmt.Errorf("read the triage docket: %w", err)
	}
	legacy := triage.Key(triage.ClassPublication, runID)
	for _, candidate := range entries {
		if candidate.Class != triage.ClassPublication {
			continue
		}
		if candidate.Key == key || candidate.Key == legacy {
			return nil
		}
	}
	return fmt.Errorf("no unfinished publication of run %s is on the triage docket under %s, so there is no publication to re-arm", runID, key)
}

// decided reports a decision of the development manager's that this re-arm may
// carry out, and refuses where there is none. It is the re-run's two questions
// asked of a publication.
//
// The publication's re-arm counter on the item is the decision's own footprint:
// the development manager spends it as the decision is recorded and before
// anything acts on it, so a publication carrying none is one nobody decided this
// about. The counter alone cannot say whether the decision has been acted on —
// it is a total nothing clears — so what has been carried out is read off the
// publication's own record, and a decision already carried out is refused: a
// second drop of the same publication needs a further decision, which past the
// cap is an escalation rather than a larger budget.
func (r Rearmer) decided(state runstate.State, published runstate.PullRequest, key string) (runstate.TriageCounters, error) {
	counters, err := r.Decisions.Counters(state.WorkItemID)
	if err != nil {
		return runstate.TriageCounters{}, fmt.Errorf("read what triage has recorded about %s: %w", state.WorkItemID, err)
	}
	decided := counters.RearmsOf(key)
	if decided < 1 {
		return runstate.TriageCounters{}, fmt.Errorf(
			"triage has recorded no re-arm of publication %s, so there is no decision here to carry out: the development manager records the decision, which spends that publication's re-arm budget, before the harness asks the forge for anything",
			key)
	}
	if published.MergeRearms >= decided {
		return runstate.TriageCounters{}, fmt.Errorf(
			"triage has decided %d re-arm(s) of publication %s and the harness has made %d, so this drop has no decision of its own to act on: a merge the forge dropped a second time is an escalation rather than another re-arm",
			decided, key, published.MergeRearms)
	}
	return counters, nil
}

// forgeWouldTakeItBack reports the forge having nothing outstanding on the
// request that a person has to supply, and the request still being the one that
// was authorized.
//
// The whole of it is asked of the forge as it stands now rather than of the
// record, because the record says what was true when the merge was dropped and
// what decides this is what is true when the request is repeated. A requirement
// that is still unmet is refused rather than merged past — with administrator
// privileges or by asking again — and what is left is a request whose drop has
// stopped applying.
func (r Rearmer) forgeWouldTakeItBack(ctx context.Context, state runstate.State, published runstate.PullRequest, integration runstate.Integration) error {
	observed, err := r.Forge.State(ctx, published.Branch)
	if err != nil {
		return fmt.Errorf("ask the forge about pull request %d of run %s: %w", published.Number, state.RunID, err)
	}
	if observed.Number != published.Number {
		return fmt.Errorf("the forge reports pull request %d for branch %s, and run %s published request %d there, so what would be repeated is not the request the verdict authorized",
			observed.Number, published.Branch, state.RunID, published.Number)
	}
	if observed.Merged {
		return fmt.Errorf("pull request %d is merged on the forge, so there is no dropped merge to repeat; `yoyo reconcile` settles the run on that answer", published.Number)
	}
	if observed.AutoMerge {
		return fmt.Errorf("the forge has a merge queued for pull request %d again, so nothing is dropped and there is nothing to repeat", published.Number)
	}
	// The head is checked against the commit the promotion made, which is what the
	// repeated request pins. A request that moved carries work no reviewer of this
	// run saw, and the merge would put it on the remote target.
	if head := strings.TrimSpace(observed.HeadCommit); head != "" && head != integration.SourceCommit {
		return fmt.Errorf("pull request %d is at %s on the forge and run %s promoted %s, so the request is not the one the verdict authorized and its merge is not one to repeat",
			published.Number, head, state.RunID, integration.SourceCommit)
	}
	status, err := r.Forge.MergeState(ctx, published.Number)
	if err != nil {
		return fmt.Errorf("ask the forge what is unmet on pull request %d: %w; a re-arm is refused on a request nothing can say the state of", published.Number, err)
	}
	if publish.AwaitsOnlyAPerson(status) {
		return fmt.Errorf("the merge of pull request %d is held by something only a person can satisfy: %s; the harness does not merge past a requirement, so this is an escalation rather than a re-arm",
			published.Number, rearmRequirement(status))
	}
	return nil
}

// rearmRequirement says what a merge state holds a request on, for a refusal
// that has to name it. A state the forge's vocabulary has grown since is quoted
// rather than described, so the refusal still says something a reader can act on.
func rearmRequirement(status string) string {
	if requirement := publish.MergeRequirement(status); requirement != "" {
		return requirement
	}
	if trimmed := strings.TrimSpace(status); trimmed != "" {
		return "the forge reports its merge state as " + trimmed
	}
	return "the forge reported no merge state for it"
}

// rearmReason is what the harness records as why the request was repeated: the
// decision it verified, the publication it settles, and the reasoning it was
// given.
//
// The two are worded apart for the reason the re-run's are. That the development
// manager decided a re-arm of this publication is a fact read from the item's
// durable triage record and is stated as one; the prose after it arrived with the
// instruction to carry the decision out, and is attributed to that rather than
// quoted as the development manager's own words.
func rearmReason(state runstate.State, published runstate.PullRequest, decided runstate.TriageCounters, key, reasoning string) string {
	reason := fmt.Sprintf(
		"the development manager's triage decided a re-arm of publication %s — %d recorded against that publication's durable triage budget, %d already made — and the harness repeated the merge request of pull request %d by the %s method, on the promotion run %s made.%s The reasoning given to the harness when it was asked to: ",
		key, decided.RearmsOf(key), published.MergeRearms, published.Number, published.MergeMethod, state.RunID, crossedRearmCap(decided))
	room := runstate.MaxSelectionReasonBytes - len(reason)
	if room < 0 {
		room = 0
	}
	return singleLine(reason+singleLine(reasoning, room), runstate.MaxSelectionReasonBytes)
}

// crossedRearmCap names the operator override this decision stands on, and says
// nothing on the ordinary publication. A re-arm recorded past the cap exists
// because a person crossed it, and the reason this records is where that survives
// the conversation it was argued in.
func crossedRearmCap(counters runstate.TriageCounters) string {
	override, found := counters.OverrideOf(runstate.TriageMergeRearmBudget)
	if !found {
		return ""
	}
	return fmt.Sprintf(" It stands on a recorded operator override of the %s cap, by %s at %s.",
		runstate.TriageMergeRearmBudget,
		singleLine(override.DecidedBy, maxCrossedCapAttributionBytes), override.DecidedAt.UTC().Format(time.RFC3339))
}

func (r Rearmer) validate() error {
	var problems []error
	if r.Docket == nil {
		problems = append(problems, errors.New("a re-arm requires the triage docket the decision was made against"))
	}
	if r.Runs == nil {
		problems = append(problems, errors.New("a re-arm requires the durable run state, which is where the publication it repeats is recorded"))
	}
	if r.Forge == nil {
		problems = append(problems, errors.New("a re-arm requires forge access, because what it repeats is a merge request"))
	}
	if r.Worktrees == nil {
		problems = append(problems, errors.New("a re-arm requires the pre-merge check on the remote target, which is what says the request is still the one the gate passed"))
	}
	if r.Decisions == nil {
		problems = append(problems, errors.New("a re-arm requires the item's triage record, which is what says the development manager decided one"))
	}
	return errors.Join(problems...)
}

func (r Rearmer) now() time.Time {
	if r.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return r.Clock.Now().UTC()
}

// Render describes what the action did, for whoever asked for it.
func (result RearmResult) Render() string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "repeated the merge request for pull request %d of %s by the %s method\n",
		result.Number, result.WorkItemID, result.Method)
	if result.URL != "" {
		fmt.Fprintf(&rendered, "pull request: %s\n", result.URL)
	}
	fmt.Fprintf(&rendered, "pinned to %s, the commit run %s integrated\n", result.HeadCommit, result.RunID)
	if result.Queued {
		fmt.Fprintln(&rendered, "the forge has the merge queued again and performs it once the base branch's requirements are met; `yoyo reconcile` settles the run when it does")
	} else {
		fmt.Fprintln(&rendered, "the forge merged it on the spot rather than queuing it; `yoyo reconcile` finishes the publication — the merge commit, the consumed branch, and the local target branch")
	}
	fmt.Fprintf(&rendered, "%d re-arm(s) of this publication are now recorded; a further drop is an escalation rather than another re-arm\n", result.Rearms)
	fmt.Fprintf(&rendered, "repeated because %s\n", result.Reason)
	if result.RecordProblem != "" {
		fmt.Fprintln(&rendered, result.RecordProblem)
	}
	return rendered.String()
}
