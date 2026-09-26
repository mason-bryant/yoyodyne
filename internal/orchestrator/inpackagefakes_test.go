package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/selfcheck"
)

// No test file uses the in-package fakes below any more: every one has moved
// onto orchestratortest's. They stay only until the change that removes them.

type fakeTracker struct {
	item beads.WorkItem
	// alsoHolds is the other work this tracker has, by identifier. It is what
	// makes an impediment a landing named one the harness can confirm; a tracker
	// that answered for every identifier could not tell the two cases apart.
	alsoHolds map[string]beads.WorkItem
	claimed   bool
	notes     string
	// noteRecords is each note as it was recorded, kept beside the accumulated
	// text above so a reader can tell one account from the next one's. A run that
	// pauses and is resumed writes two, and the concatenation alone cannot say
	// where the first ended.
	noteRecords []string
	closed      bool
	closeReason string
	blocked     bool
	blockReason string
	calls       []string
	onClaim     func() error
	// showFailures and transientShowErr are how many reads are refused before one
	// answers, and what they are refused with. They are the read's half of what
	// completeFailures is for the write: a store that was busy rather than one
	// that keeps answering the same way. showCalls counts every read, so a test
	// can say which of a run's reads the refusals landed on.
	showFailures     int
	transientShowErr error
	showCalls        int
	// staleBlockClear is what the claim reports about a stale blocked status it
	// cleared, returned beside the item and beside onClaim's error alike, as the
	// real client returns it. Nil is a claim that met none.
	staleBlockClear *beads.StaleBlockClear
	completeErr     error
	// completeFailures and transientCompleteErr are how many closures are refused
	// before one goes through, and what they are refused with. They are apart
	// from completeErr because that one is a tracker that keeps answering the
	// same way, and this is a store that was busy.
	completeFailures     int
	transientCompleteErr error
	blockErr             error
	// released and releaseReason are the claim given back to the queue by a run
	// that ended on something nobody has to decide about.
	released      bool
	releaseReason string
	// reopened and reopenReason are the item put back in the backlog by a run
	// that integrated its change and claimed the change does not discharge the
	// item. They are apart from the closure above because the whole point of the
	// landing claim is that the two are different acts.
	reopened     bool
	reopenReason string
	reopenErr    error
	// blockers is each dependency added to the item, in the order they were
	// added. It is what says the leave-open path actually made the item wait for
	// something rather than putting it back bare.
	blockers   []string
	blockerErr error
}

type partialWorktreeManager struct {
	worktree gitworktree.Worktree
	err      error
}

func (partialWorktreeManager) ValidateReady(context.Context) error { return nil }

func (partialWorktreeManager) CurrentBranch(context.Context) (string, error) { return "main", nil }

func (m partialWorktreeManager) Create(context.Context, gitworktree.CreateRequest) (gitworktree.Worktree, error) {
	return m.worktree, m.err
}

// Observe refuses, as the real manager does for a worktree it never finished
// creating: the recorded path is not one it owns, so there is nothing it can say
// about what is there. A preservation check that gets this must claim nothing.
func (partialWorktreeManager) Observe(context.Context, gitworktree.Worktree) (gitworktree.Observation, error) {
	return gitworktree.Observation{}, errors.New("partial worktree cannot be observed")
}

func (partialWorktreeManager) SummarizeChanges(context.Context, gitworktree.Worktree) (gitworktree.ChangeSummary, error) {
	return gitworktree.ChangeSummary{}, nil
}

func (partialWorktreeManager) UnifiedChanges(context.Context, gitworktree.Worktree, gitworktree.DiffLimits) (gitworktree.ChangeDiff, error) {
	return gitworktree.ChangeDiff{}, nil
}

func (partialWorktreeManager) FileAtCommit(context.Context, string, string, int64) (gitworktree.FileAt, error) {
	return gitworktree.FileAt{}, gitworktree.ErrNotAtCommit
}

func (partialWorktreeManager) ChangedPaths(context.Context, gitworktree.Worktree) ([]string, error) {
	return nil, nil
}

func (partialWorktreeManager) CurrentExports() []string { return nil }

func (partialWorktreeManager) CommitAttempt(context.Context, gitworktree.Worktree, string) (string, error) {
	return "", errors.New("partial worktree cannot be committed")
}

func (partialWorktreeManager) Integrate(context.Context, gitworktree.Worktree, string) (gitworktree.Integration, error) {
	return gitworktree.Integration{}, errors.New("partial worktree cannot be integrated")
}

func (partialWorktreeManager) PrepareLanding(context.Context, gitworktree.Worktree, string) (gitworktree.Integration, error) {
	return gitworktree.Integration{}, errors.New("partial worktree cannot be landed")
}

func (partialWorktreeManager) RebaseOntoTarget(context.Context, gitworktree.Worktree, string) (gitworktree.Rebase, error) {
	return gitworktree.Rebase{}, errors.New("partial worktree cannot be replayed")
}

func (partialWorktreeManager) CleanupIntegrated(context.Context, gitworktree.CleanupRequest) (gitworktree.Cleanup, error) {
	return gitworktree.Cleanup{}, errors.New("partial worktree cannot be cleaned up")
}

func (partialWorktreeManager) RemoteConfigured(context.Context) (bool, error) { return false, nil }

func (partialWorktreeManager) PushRemoteConfigured(context.Context) (bool, error) {
	return false, nil
}

func (partialWorktreeManager) PublishBranch(context.Context, gitworktree.Worktree, string) (gitworktree.Publication, error) {
	return gitworktree.Publication{}, errors.New("partial worktree cannot be published")
}

func (partialWorktreeManager) RepublishBranch(context.Context, gitworktree.Worktree, string) (gitworktree.Publication, error) {
	return gitworktree.Publication{}, errors.New("partial worktree cannot be republished")
}

func (partialWorktreeManager) VerifyRemoteTarget(context.Context, gitworktree.Integration) error {
	return errors.New("partial worktree has no remote target")
}

func (partialWorktreeManager) ConfirmRemoteTarget(context.Context, gitworktree.Integration, string) (string, error) {
	return "", errors.New("partial worktree has no remote")
}

func (partialWorktreeManager) DeleteRemoteBranch(context.Context, gitworktree.Worktree, string) error {
	return errors.New("partial worktree has no remote branch")
}

func (partialWorktreeManager) CatchUpTarget(context.Context, string) (gitworktree.Catchup, error) {
	return gitworktree.Catchup{}, errors.New("partial worktree has no remote to catch up to")
}

// Show answers for the item this tracker holds and for anything else explicitly
// put in it, and refuses everything else the way the real client does. Answering
// for every identifier would make "the tracker has no such work item" untestable,
// which is the case a landing's impediment marker has to be resolved against.
func (f *fakeTracker) Show(_ context.Context, id string) (beads.WorkItem, error) {
	f.showCalls++
	if f.showFailures > 0 {
		f.showFailures--
		return beads.WorkItem{}, f.transientShowErr
	}
	if id == f.item.ID {
		return f.item, nil
	}
	if item, held := f.alsoHolds[id]; held {
		return item, nil
	}
	return beads.WorkItem{}, fmt.Errorf("no work item %s", id)
}

// holds puts another open work item in this tracker, which is what makes an
// impediment a landing names one the harness can confirm.
func (f *fakeTracker) holds(id string) *fakeTracker {
	return f.holdsItem(beads.WorkItem{ID: id, Title: "The impediment", Status: "open"})
}

// holdsItem is the same for work that has to be more than open — finished, or
// already waiting on something — because what the impediment says about itself is
// what decides whether waiting on it would hold anything back.
func (f *fakeTracker) holdsItem(item beads.WorkItem) *fakeTracker {
	if f.alsoHolds == nil {
		f.alsoHolds = make(map[string]beads.WorkItem)
	}
	f.alsoHolds[item.ID] = item
	return f
}

func (f *fakeTracker) Claim(context.Context, string) (beads.WorkItem, *beads.StaleBlockClear, error) {
	if f.onClaim != nil {
		if err := f.onClaim(); err != nil {
			return beads.WorkItem{}, f.staleBlockClear, err
		}
	}
	f.claimed = true
	f.calls = append(f.calls, "claim")
	f.item.Status = "in_progress"
	return f.item, f.staleBlockClear, nil
}

func (f *fakeTracker) RecordOutcome(_ context.Context, _ string, notes string) (beads.WorkItem, error) {
	f.notes += notes
	f.noteRecords = append(f.noteRecords, notes)
	f.calls = append(f.calls, "record")
	return f.item, nil
}

func (f *fakeTracker) Block(_ context.Context, _ string, reason string) (beads.WorkItem, error) {
	f.calls = append(f.calls, "block")
	if f.blockErr != nil {
		return beads.WorkItem{}, f.blockErr
	}
	f.blocked = true
	f.blockReason = reason
	f.item.Status = "blocked"
	return f.item, nil
}

func (f *fakeTracker) Release(_ context.Context, _ string, reason string) (beads.WorkItem, error) {
	f.calls = append(f.calls, "release")
	f.released = true
	f.releaseReason = reason
	f.claimed = false
	f.item.Status = "open"
	return f.item, nil
}

func (f *fakeTracker) Complete(_ context.Context, _ string, reason string) (beads.WorkItem, error) {
	f.calls = append(f.calls, "complete")
	// completeFailures is how many times the closure is refused before it goes
	// through, which is what a `bd` the store was too busy to run looks like.
	if f.completeFailures > 0 {
		f.completeFailures--
		return beads.WorkItem{}, f.transientCompleteErr
	}
	if f.completeErr != nil {
		return beads.WorkItem{}, f.completeErr
	}
	f.closed = true
	f.closeReason = reason
	f.item.Status = "closed"
	return f.item, nil
}

func (f *fakeTracker) Reopen(_ context.Context, _ string, reason string, parking domain.WorkItemParking) (beads.WorkItem, error) {
	f.calls = append(f.calls, "reopen")
	if f.reopenErr != nil {
		return beads.WorkItem{}, f.reopenErr
	}
	f.reopened = true
	f.reopenReason = reason
	f.notes += reason
	f.noteRecords = append(f.noteRecords, reason)
	f.item.Status = "open"
	// The parking is applied exactly as the tracker applies it: an empty one is a
	// release rather than an omission, so an item put back unparked is one the
	// queue offers again.
	f.item.Parking = parking
	return f.item, nil
}

func (f *fakeTracker) AddBlocker(_ context.Context, _ string, blockerID string) error {
	f.calls = append(f.calls, "blocker")
	if f.blockerErr != nil {
		return f.blockerErr
	}
	f.blockers = append(f.blockers, blockerID)
	f.item.Dependencies = append(f.item.Dependencies, beads.Dependency{ID: blockerID, Type: "blocks"})
	return nil
}

// fakePricer stands in for the ledger that prices work items. It records the
// items it was asked about, which is what makes "a run prices the item it
// served" an assertion rather than a claim.
type fakePricer struct {
	cost   beads.Cost
	err    error
	priced []string
}

func (f *fakePricer) Record(_ context.Context, workItemID string) (*beads.Cost, error) {
	f.priced = append(f.priced, workItemID)
	if f.err != nil {
		return nil, f.err
	}
	cost := f.cost
	return &cost, nil
}

type fakeBackend struct {
	availability backend.Availability
	run          func(backend.RunRequest) (backend.RunResult, error)
	requests     []backend.RunRequest
	// Session identities are configurable so a test can prove that missing or
	// reused provider identity never reaches integration.
	developerSession string
	reviewerSession  string
	// developerFinalText replaces what a served developer attempt says about its
	// work, which is what a test that cares about the summary itself sets.
	developerFinalText string
	// developerRecordsNoExecution makes the served developer reply exactly what
	// the test wrote, with no verification record added to it. It is for the
	// tests about the execution-evidence gate itself, which need a reply that
	// records nothing.
	developerRecordsNoExecution bool
	// developerFinalTextByAttempt says it per attempt instead, for a test where
	// what the developer says has to change between the first attempt and the
	// repair. The last entry repeats once the list runs out, the way the review
	// verdicts do.
	developerFinalTextByAttempt []string
	developerAttempts           int
}

func (f *fakeBackend) CheckAvailability(context.Context) (backend.Availability, error) {
	if !f.availability.Installed && !f.availability.Authenticated && f.availability.AuthMethod == "" {
		return backend.Availability{Installed: true, Authenticated: true}, nil
	}
	return f.availability, nil
}

func (*fakeBackend) Capabilities() backend.Capabilities {
	return backend.Capabilities{StructuredEvents: true}
}

func (f *fakeBackend) Run(_ context.Context, request backend.RunRequest) (backend.RunResult, error) {
	f.requests = append(f.requests, request)
	result, err := f.run(request)
	// A developer that recorded nothing it executed is refused before its change
	// reaches a reviewer, so every fake developer here carries the record its
	// contract asks for unless its own test is about the absence. It is added
	// where every fake passes rather than in each of them, so a test written next
	// year inherits it instead of a copy of it.
	if request.Role == domain.RoleDeveloper && !f.developerRecordsNoExecution {
		result.FinalText = withVerification(result.FinalText)
	}
	return result, err
}

func (f *fakeBackend) requestsForRole(role domain.AgentRole) []backend.RunRequest {
	var matching []backend.RunRequest
	for _, request := range f.requests {
		if request.Role == role {
			matching = append(matching, request)
		}
	}
	return matching
}

// passingVerification is the record of its own executions a developer's contract
// asks every reply to carry: the probe it ran before it changed anything, and a
// check it ran against the change. Every fake developer below carries one unless
// its test is about the absence, because a run whose developer records nothing
// is refused before it reaches a reviewer — which is the gate rather than an
// accident of these doubles.
const passingVerification = "\n\n" + selfcheck.Fence + "\n" +
	`{"probe":{"command":"make build","outcome":"passed"},"checks":[{"command":"make test","outcome":"passed"}]}` + "\n```"

// withVerification adds that record to a reply that does not already write one
// of its own, so a test about anything else does not have to.
func withVerification(reply string) string {
	if strings.Contains(reply, selfcheck.Fence) {
		return reply
	}
	return reply + passingVerification
}

// roleBackend serves the developer and the reviewer from one fake provider, so
// a test can prove the two invocations are actually distinct rather than
// assuming it from separate doubles. The reviewer answers with each verdict in
// turn and repeats the last one, which is what lets a test drive a repair loop
// to a chosen outcome.
func roleBackend(develop func(backend.RunRequest) error, verdicts ...string) *fakeBackend {
	provider := &fakeBackend{developerSession: "developer-session", reviewerSession: "reviewer-session"}
	reviews := 0
	provider.run = func(request backend.RunRequest) (backend.RunResult, error) {
		switch request.Role {
		case domain.RoleDeveloper:
			if err := develop(request); err != nil {
				return backend.RunResult{}, err
			}
			finalText := "implemented the work item"
			if provider.developerFinalText != "" {
				finalText = provider.developerFinalText
			}
			if texts := provider.developerFinalTextByAttempt; len(texts) > 0 {
				finalText = texts[min(provider.developerAttempts, len(texts)-1)]
			}
			provider.developerAttempts++
			return backend.RunResult{
				Backend:       domain.BackendClaudeCode,
				SessionID:     provider.developerSession,
				ResolvedModel: developerResolved,
				FinalText:     finalText,
				Process:       execution.ProcessResult{Status: execution.ProcessSucceeded},
				LastEvent:     request.LastSequence,
			}, nil
		case domain.RoleReviewer:
			verdict := verdicts[len(verdicts)-1]
			if reviews < len(verdicts) {
				verdict = verdicts[reviews]
			}
			reviews++
			return backend.RunResult{
				Backend:       domain.BackendClaudeCode,
				SessionID:     provider.reviewerSession,
				ResolvedModel: reviewerResolved,
				FinalText:     verdict,
				Process:       execution.ProcessResult{Status: execution.ProcessSucceeded},
				LastEvent:     request.LastSequence,
			}, nil
		default:
			return backend.RunResult{}, fmt.Errorf("unexpected role %q", request.Role)
		}
	}
	return provider
}
