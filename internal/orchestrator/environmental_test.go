package orchestrator

// A round the environment refused, against a real repository.
//
// The field cases were items that had been granted a repair, dispatched into a
// clean worktree by a build that did not carry the gate the grant relied on, and
// charged for the round anyway. The grant was gone, the item was one step nearer
// its cap, and the escalation that followed reads afterwards as work nobody could
// finish.
//
// Four tests, and each asserts a different half of the class. The first is that
// sequence with the accounting asserted: the run refuses, the round is classified
// environmental, and the item stands exactly where it stood before it. The second
// is the same failure repeated past what the round budget has room for, which is
// the bound the item asks for rather than one case of it. The third is the
// conjunction from the other side — an empty delivery with no environmental
// cause still spends. The fourth is a cause the harness recognizes from the
// failure alone rather than from a refusal site of its own.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// environmentalCaps leaves the round budget with room the grant is not truncated
// against, so what these tests measure is a refused round spending nothing rather
// than a grant the cap had already cut. The stoppage they start from has cost
// three rounds and the grant asks for two, which fits.
var environmentalCaps = runstate.TriageCaps{ReviewRounds: 8, RepairGrants: 1, Reruns: 1, MergeRearms: 2}

// environmentalRefusals is how many times the sequence test lets the environment
// refuse the same item. It is more than the round budget has room for after the
// grant, which is the point: one round leaked per refusal would have the item at
// its cap well before the loop ends.
const environmentalRefusals = 5

// The founding case of this item. A granted repair is carried out into a
// worktree holding none of the change, the run refuses, and what the round would
// have cost the item is given back rather than spent on the harness's own
// failure.
func TestAnEmptyDiffRoundTheEnvironmentRefusedSpendsNothing(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})

	// The development manager's decision, which spends the item's repair-grant
	// budget as it is recorded.
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.item.ID, 2, docketedNow, environmentalCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	before, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}

	// What the stale-binary dispatch handed the granted round: the run re-entered
	// under its grant, in a worktree seeded from the target branch rather than
	// from the preserved one.
	emptyPreservedWorktree(t, stopped.WorktreePath)
	continueOnGrant(t, store, tracker, stopped.RunID)

	docket := &memoryDocket{}
	provider := roleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked in %s, which holds none of the change the grant was to repair", request.WorkingDirectory)
		return nil
	}, approveVerdict)
	continuing := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	continuing.Docket = docketerOverStore(docket, store, continuing.Config)

	if _, err := continuing.Run(context.Background(), tracker.item.ID); !errors.Is(err, ErrPreservedChangeMissing) {
		t.Fatalf("Run() error = %v, want the round refused for holding none of its change", err)
	}
	if invocations := len(provider.requests); invocations != 0 {
		t.Fatalf("provider invocations = %d, want the refusal to have spent nothing on a provider", invocations)
	}

	refused, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The class is on the run, named, and settled.
	environmental := refused.Environmental
	if environmental == nil {
		t.Fatal("the run records no environmental cause, so nothing downstream can tell this round from a delivery of nothing")
	}
	if environmental.Cause != runstate.CauseHandbackMissingChange || !environmental.Refused {
		t.Fatalf("environmental = %#v, want the handback refusal classified", environmental)
	}
	if environmental.Problem != "" {
		t.Fatalf("the settle could not pay the refusal back: %s", environmental.Problem)
	}
	// The grant is back: the run's record says the continuation bought nothing,
	// while the run's own budget still counts the attempt slot it spent.
	if !environmental.GrantReturned {
		t.Fatal("the granted repair round was not returned, so the grant was spent on a worktree that held nothing")
	}
	if carried := refused.CarriedOutRepairAttempts(); carried != 0 {
		t.Fatalf("carried-out repair attempts = %d, want the grant untouched", carried)
	}
	if granted := refused.GrantedRepairAttempts(); granted != 1 {
		t.Fatalf("granted repair attempts = %d, want the run's own budget to still count the attempt it spent", granted)
	}
	// And the cap is untouched: every figure the guards refuse against is exactly
	// what it was before the round.
	after, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if after.ReviewRounds != before.ReviewRounds || after.CommittedRounds != before.CommittedRounds {
		t.Fatalf("counters after the refusal = %d round(s), %d committed; want %d and %d",
			after.ReviewRounds, after.CommittedRounds, before.ReviewRounds, before.CommittedRounds)
	}
	// The development manager reads it on the docket rather than deriving it from
	// a blocker: the counters on the entry mean something different because of it.
	if len(docket.entries) != 1 {
		t.Fatalf("docket entries = %d, want the refused round docketed once", len(docket.entries))
	}
	entry := docket.entries[0]
	if entry.Environmental == nil || !entry.Environmental.Refused {
		t.Fatalf("docket entry environmental = %#v, want the refusal carried onto it", entry.Environmental)
	}
	rendered := entry.Render()
	for _, want := range []string{"Environmentally refused", string(runstate.CauseHandbackMissingChange), "stands where it did before the round"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the docket entry does not say %q:\n%s", want, rendered)
		}
	}
}

// The guarantee stated as a bound rather than as one case: however many times
// the environment refuses an item, it is no nearer its cap at the end than at
// the start. A class that leaked one round per refusal would be a slower walk to
// the same escalation.
func TestNoSequenceOfEnvironmentalRefusalsWalksAnItemToItsCap(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.item.ID, 2, docketedNow, environmentalCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	before, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if before.RoundsUncommitted(environmentalCaps.ReviewRounds) < 1 {
		t.Fatalf("the fixture starts with no round left to lose, so this proves nothing: %#v", before)
	}
	emptyPreservedWorktree(t, stopped.WorktreePath)

	provider := roleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked in %s across a run of environmental refusals", request.WorkingDirectory)
		return nil
	}, approveVerdict)
	continuing := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)

	// More refusals than the round budget has room for. If any one of them leaked
	// a round, the item would be at its cap by the end of this loop.
	for refusal := 0; refusal < environmentalRefusals; refusal++ {
		continueOnGrant(t, store, tracker, stopped.RunID)
		if _, err := continuing.Run(context.Background(), tracker.item.ID); !errors.Is(err, ErrPreservedChangeMissing) {
			t.Fatalf("refusal %d: Run() error = %v, want the round refused", refusal, err)
		}
		after, err := store.Triage().Counters(tracker.item.ID)
		if err != nil {
			t.Fatalf("Counters() error = %v", err)
		}
		if after.ReviewRounds != before.ReviewRounds {
			t.Fatalf("refusal %d left the item at %d round(s), want %d", refusal, after.ReviewRounds, before.ReviewRounds)
		}
		if after.RoundsUncommitted(environmentalCaps.ReviewRounds) != before.RoundsUncommitted(environmentalCaps.ReviewRounds) {
			t.Fatalf("refusal %d moved the item toward its cap: %d round(s) uncommitted, want %d",
				refusal, after.RoundsUncommitted(environmentalCaps.ReviewRounds), before.RoundsUncommitted(environmentalCaps.ReviewRounds))
		}
	}
	refused, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if carried := refused.CarriedOutRepairAttempts(); carried != 0 {
		t.Fatalf("carried-out repair attempts = %d after a run of refusals, want the grant untouched", carried)
	}
}

// The other half of the definition, which is what keeps the class honest. A run
// that delivers nothing and records no environmental cause spends exactly as any
// round does: laziness cannot hide in a class built for a harness that handed a
// round nothing.
func TestAnEmptyDeliveryWithNoEnvironmentalCauseStillSpends(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// A developer that writes nothing at all, judged until the run's own repair
	// budget is spent. Nothing about the environment refused it.
	provider := roleBackend(func(backend.RunRequest) error { return nil }, repairVerdict)
	running := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)

	outcome, err := running.Run(context.Background(), tracker.item.ID)
	if err == nil {
		t.Fatal("Run() ended without stopping, so nothing here spent a budget")
	}
	if !outcome.Blocked {
		t.Fatalf("outcome = %#v, want the run blocked on its unresolved findings", outcome)
	}
	if outcome.Environmental != nil {
		t.Fatalf("outcome environmental = %#v, want an empty delivery nobody can account for left in no class at all", outcome.Environmental)
	}
	spent, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if spent.Environmental != nil {
		t.Fatalf("the run records an environmental cause it never had: %#v", spent.Environmental)
	}
	counters, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds < 1 {
		t.Fatalf("review rounds = %d, want the empty delivery charged for every verdict it bought", counters.ReviewRounds)
	}
	if invocations := len(provider.requestsForRole(domain.RoleDeveloper)); invocations == 0 {
		t.Fatal("no developer was invoked, so this is not the empty delivery the test is about")
	}
}

// A cause the harness recognizes from the failure that ended the run rather than
// from a refusal site of its own. A worktree that could not be cut from the
// primary checkout is the environment refusing the round as squarely as an empty
// handback is, and it reaches the class through the sentinel the refusing package
// declares rather than through a message somebody could reword.
func TestARoundTurnedAwayByThePrimaryCheckoutIsRefusedEnvironmentally(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked in %s, and no worktree was ever cut for this run", request.WorkingDirectory)
		return nil
	}, approveVerdict)
	starting := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	starting.NewRunID = runstate.NewRunID
	starting.Worktrees = dirtyPrimaryWorktrees{starting.Worktrees}

	outcome, err := starting.Run(context.Background(), tracker.item.ID)
	if err == nil {
		t.Fatal("Run() started work in a checkout no worktree could be cut from")
	}
	if outcome.Environmental == nil {
		t.Fatalf("outcome = %#v, want the round classified by the cause its failure names", outcome)
	}
	if outcome.Environmental.Cause != runstate.CauseDirtyPrimary {
		t.Fatalf("environmental cause = %q, want %q", outcome.Environmental.Cause, runstate.CauseDirtyPrimary)
	}
	if !outcome.Environmental.Settled || !outcome.Environmental.Refused {
		t.Fatalf("environmental = %#v, want the round settled and refused", outcome.Environmental)
	}
	// It reached nothing that spends — no worktree, no reviewer, no grant — so
	// there was nothing to give back, and the record says that rather than
	// claiming a return.
	if outcome.Environmental.RoundReturned || outcome.Environmental.GrantReturned {
		t.Fatalf("environmental = %#v, want a round that reached nothing that spends to have returned nothing", outcome.Environmental)
	}
	if outcome.Environmental.Problem != "" {
		t.Fatalf("the settle reported a problem it did not have: %s", outcome.Environmental.Problem)
	}
	counters, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 0 || counters.RepairGrants != 0 {
		t.Fatalf("counters = %#v, want an item charged nothing for a round the environment turned away", counters)
	}
}

// dirtyPrimaryWorktrees passes the readiness gate and then refuses to cut a
// worktree, which is the window that gate cannot close: the checkout is the
// harness's to read at the moment it is asked and somebody's to write into a
// moment later. It is also the only route to the refusal that ends a run — the
// gate before the claim turns a run away before there is any run to record
// anything on.
type dirtyPrimaryWorktrees struct {
	WorktreeManager
}

func (dirtyPrimaryWorktrees) Create(context.Context, gitworktree.CreateRequest) (gitworktree.Worktree, error) {
	return gitworktree.Worktree{}, fmt.Errorf("%w: primary repository has uncommitted changes: notes.txt", gitworktree.ErrPrimaryNotReady)
}

// continueOnGrant records what carrying out a repair grant records on the run
// before the pipeline is asked to continue it: the continuation, the attempt it
// buys, and the stoppage superseded. It is the harness's own re-entry written by
// hand, so what these tests drive is the state a granted round actually starts
// from rather than an approximation of it.
func continueOnGrant(t *testing.T, store *runstate.Store, tracker *fakeTracker, runID string) {
	t.Helper()
	state, err := store.Load(runID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	state.RepairContinuations = append(state.RepairContinuations, runstate.RepairContinuation{
		GrantedAttempts:   1,
		Reason:            "Triaged: the repair loop was re-entered on the change it already has, under a grant recorded against the item's durable triage budget.",
		ContinuedAt:       docketedNow,
		SupersededBlocker: state.Blocker,
	})
	state.RepairAttempts++
	state.Blocker = ""
	state.Failure = ""
	// The refusal on the record describes the round this continuation supersedes,
	// and that round has settled. A fresh round is classified on its own evidence.
	state.Environmental = nil
	state.Status = runstate.StatusRunning
	state.Phase = runstate.PhaseDeveloping
	state.CompletedAt = nil
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := tracker.Claim(context.Background(), tracker.item.ID); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
}
