package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// These drive yoyodyne-ifd.429.16: a merge the forge still holds is read with
// its checks, and a red one is never simply left queued. Each case is a whole
// run that ends with its merge queued on a protected target, and a forge that
// then reports the head's checks.

// checkedForge is the fabricated forge with check state: what it reports about
// the head's checks, and every queued merge it was asked to withdraw. Withdrawing
// one is the forge no longer holding it.
type checkedForge struct {
	queuedForge
	reading   publish.CheckReading
	withdrawn []int
}

func (f *checkedForge) Checks(_ context.Context, number int, _ string) (publish.CheckReading, error) {
	reading := f.reading
	if reading.HeadCommit == "" {
		merges := f.MergeRequests()
		reading.HeadCommit = merges[len(merges)-1].HeadCommit
	}
	return reading, nil
}

func (f *checkedForge) DisableAutoMerge(_ context.Context, number int) error {
	f.withdrawn = append(f.withdrawn, number)
	f.DropQueuedMerge()
	return nil
}

// queuedOnProtectedTarget is a run that landed through its pull request and
// finished with the forge holding the merge.
func queuedOnProtectedTarget(t *testing.T) (queuedFixture, *checkedForge, Outcome) {
	t.Helper()
	fixture := newQueuedFixture(t)
	fixture.forge.SetTargetProtection(publish.BranchProtection{Protected: true, By: "ruleset"})
	outcome := fixture.run(t)
	if outcome.Integration == nil || !outcome.Integration.ThroughPullRequest {
		t.Fatalf("integration = %#v, want a landing through the pull request", outcome.Integration)
	}
	return fixture, &checkedForge{queuedForge: fixture.forge}, outcome
}

// sweep is the reconciler the reconcile verb builds: the forge's checks read,
// a free slot, and — where hosts is set — the run it makes
// live continued through the same pipeline a run is.
func (f queuedFixture) sweep(t *testing.T, forge *checkedForge, hosts bool) Reconciler {
	t.Helper()
	reconciler := f.reconciler(t)
	reconciler.Publisher = forge
	reconciler.Checks = forge
	reconciler.Capacity = 1
	reconciler.HostsRuns = hosts
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := publishing(automatic(newSharedPipeline(t, f.repository, f.worktreeRoot, f.store, f.tracker, provider, []string{"exit 0"}), provider), f.forge)
	pipeline.Publisher = forge
	reconciler.Continue = pipeline.Continue
	return reconciler
}

// A head that fell behind its target and fails a check on a file its change
// does not touch is brought up to date by the harness: the queued merge is
// withdrawn, the change is replayed onto the target, checked and reviewed
// again, and its merge queued again — spending one integration retry.
func TestAQueuedHeadBehindItsTargetFailingUnrelatedChecksIsUpdatedAndRequeued(t *testing.T) {
	t.Parallel()

	fixture, forge, outcome := queuedOnProtectedTarget(t)
	driftRemoteTarget(t, fixture.remote, "main")
	forge.reading = publish.CheckReading{
		Files:    []string{"feature.txt"},
		Failing:  []publish.FailedCheck{{Name: "go test", Paths: []string{"internal/elsewhere/elsewhere_test.go"}}},
		Passing:  3,
		BehindBy: 1,
	}
	reconciler := fixture.sweep(t, forge, true)

	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionUpdating || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the queued head put back at its promotion", results)
	}
	if len(forge.withdrawn) != 1 || forge.HoldsQueuedMerge() {
		t.Fatalf("withdrawn = %v, queued = %t; want the queued merge withdrawn before the head is rewritten", forge.withdrawn, forge.HoldsQueuedMerge())
	}
	resumed, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !updatingQueuedHead(resumed) || resumed.Integration != nil {
		t.Fatalf("record = status %q, phase %q, integration %#v, resumptions %#v; want a live run at its promotion",
			resumed.Status, resumed.Phase, resumed.Integration, resumed.IntegrationResumptions)
	}
	if fixture.tracker.Record().Blocked || fixture.tracker.Record().Closed {
		t.Fatalf("blocked = %t, closed = %t; an update hands nothing back and closes nothing", fixture.tracker.Record().Blocked, fixture.tracker.Record().Closed)
	}
	if !strings.Contains(fixture.tracker.Record().Notes, "go test (on internal/elsewhere/elsewhere_test.go, which this change does not touch)") {
		t.Errorf("the item was not told which check failed and on what:\n%s", fixture.tracker.Record().Notes)
	}

	updates, err := reconciler.ContinueUpdates(context.Background())
	if err != nil {
		t.Fatalf("ContinueUpdates() error = %v", err)
	}
	if len(updates) != 1 || !updates[0].Continued || updates[0].Failure != "" || updates[0].Outcome == nil {
		t.Fatalf("updates = %#v, want the run hosted through its replay", updates)
	}
	replayed := *updates[0].Outcome
	if replayed.IntegrationRetries != 1 {
		t.Errorf("integration retries = %d, want the update charged as the one replay it is", replayed.IntegrationRetries)
	}
	if replayed.PullRequest == nil || !replayed.PullRequest.MergeQueued || len(forge.MergeRequests()) != 2 {
		t.Fatalf("outcome pull request = %#v, merges = %d; want the replayed change's merge queued again", replayed.PullRequest, len(forge.MergeRequests()))
	}
	if again := forge.MergeRequests()[1].HeadCommit; again == outcome.PullRequest.HeadCommit {
		t.Errorf("the merge was queued again on the old head %s, want the replayed one", again)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !settled.Outstanding() || settled.PullRequest == nil || !settled.PullRequest.MergeQueued || settled.Integration == nil {
		t.Fatalf("record = %#v, want a finished run waiting on its merge queued again", settled)
	}
	if settled.BaseCommit == outcome.BaseCommit {
		t.Errorf("base commit = %s, want the replay's new base rather than the one the head fell behind", settled.BaseCommit)
	}
	// The replayed change was reviewed again rather than carrying the old verdict.
	if settled.ReviewHeadCommit != settled.Integration.SourceCommit {
		t.Errorf("review head = %s, promoted = %s; want the verdict to be about the replayed change", settled.ReviewHeadCommit, settled.Integration.SourceCommit)
	}
}

// A head whose own change fails a check is handed back with the check named, as
// a dropped merge is, rather than left queued.
func TestAQueuedHeadFailingACheckOnItsOwnChangeIsHandedBack(t *testing.T) {
	t.Parallel()

	fixture, forge, _ := queuedOnProtectedTarget(t)
	forge.reading = publish.CheckReading{
		Files:    []string{"feature.txt"},
		Failing:  []publish.FailedCheck{{Name: "lint", Paths: []string{"feature.txt"}}},
		BehindBy: 4,
	}
	fixture.docket = &memoryDocket{}
	reconciler := fixture.sweep(t, forge, true)

	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the red merge handed back", results)
	}
	if len(forge.withdrawn) != 1 || forge.HoldsQueuedMerge() {
		t.Fatalf("withdrawn = %v, queued = %t; want the queued merge withdrawn so nothing lands it", forge.withdrawn, forge.HoldsQueuedMerge())
	}
	if !fixture.tracker.Record().Blocked {
		t.Fatal("the red change was left queued rather than handed back")
	}
	for _, want := range []string{"lint (on feature.txt, which this change touches)", "fail on this change", "withdrew the queued merge"} {
		if !strings.Contains(fixture.tracker.Record().BlockReason, want) {
			t.Errorf("blocker does not say %q:\n%s", want, fixture.tracker.Record().BlockReason)
		}
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.MergeDrop == nil || settled.PullRequest.MergeQueued || settled.Outstanding() {
		t.Fatalf("record = drop %#v, queued %t, outstanding %t; want the dropped merge a re-arm can act on", settled.MergeDrop, settled.PullRequest.MergeQueued, settled.Outstanding())
	}
	if settled.PullRequest.Checks == nil || !settled.PullRequest.Checks.ChangeFails() {
		t.Errorf("checks = %#v, want the reading that decided it kept on the publication", settled.PullRequest.Checks)
	}
	if updates, err := reconciler.ContinueUpdates(context.Background()); err != nil || len(updates) != 0 {
		t.Errorf("ContinueUpdates() = %#v, %v; a handed-back run is not updated", updates, err)
	}
}

// A request queued past triage.stuck_merge_age with red checks — here one a
// pass that hosts no runs could not bring up to date — is docketed with its
// checks beside it. The attention line's half is TestAQueuedPublicationLineCarriesItsChecks.
func TestAStuckQueuedMergeIsDocketedWithItsChecks(t *testing.T) {
	t.Parallel()

	fixture, forge, _ := queuedOnProtectedTarget(t)
	forge.reading = publish.CheckReading{
		Files:    []string{"feature.txt"},
		Failing:  []publish.FailedCheck{{Name: "go test", Paths: []string{"internal/elsewhere/elsewhere_test.go"}}},
		BehindBy: 31,
	}
	results, err := fixture.sweep(t, forge, false).Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionQueued || !strings.Contains(results[0].Detail, "left queued for the next sweep") {
		t.Fatalf("reconciliation = %#v, want the merge left queued for a sweep that hosts runs", results)
	}
	if len(forge.withdrawn) != 0 || !forge.HoldsQueuedMerge() {
		t.Fatalf("withdrawn = %v; a merge nothing will update is not withdrawn", forge.withdrawn)
	}

	docket := &memoryDocket{}
	docketer := docketerOverStore(docket, fixture.store, docketConfig())
	docketer.Clock = fixedClock{at: time.Now().Add(3 * time.Hour)}
	if _, err := docketer.Build(); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	var entry triage.Entry
	for _, docketed := range docket.entries {
		if docketed.Class == triage.ClassPublication {
			entry = docketed
		}
	}
	if entry.Publication == nil || !entry.Publication.MergeQueued {
		t.Fatalf("docket = %#v, want the stuck queued merge docketed", docket.entries)
	}
	rendered := entry.Render()
	for _, want := range []string{"the forge has its merge queued", "Checks: checks failing: go test (on internal/elsewhere/elsewhere_test.go, which this change does not touch)", "31 commit(s) behind main"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("docket entry does not say %q:\n%s", want, rendered)
		}
	}

	// A queued merge no sweep has read the checks of says so, rather than being
	// shown as approved and queued with nothing beside it.
	unread := entry
	published := *entry.Publication
	published.Checks = ""
	unread.Publication = &published
	if !strings.Contains(unread.Render(), "Checks: not yet read") {
		t.Errorf("a queued merge with unread checks is shown without saying so:\n%s", unread.Render())
	}
}
