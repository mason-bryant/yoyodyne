package orchestrator

// The shapes yoyodyne-ifd.402 was admitted on, replayed, and the rule it
// established: a promotion whose record holds no pull request is an outstanding
// publication every surface reports, never a run that finished quietly.
//
// docs/diagnoses/yoyodyne-ifd-402-publication-record-not-lost.md is the account
// of the two runs. Neither record lost its request; what each shows is here so
// the reading stays checkable — a queued merge is on the record the run
// completes with and settles on the next sweep, and an escalated run's request
// is on its record and, now, on the docket entry the development manager reads.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// run-7ae62396 on yoyodyne-ifd.391: the run asked the forge to merge, the forge
// queued it, and the run completed with the merge queued on its record. The
// number the summary prints is the number the durable record holds, in the same
// arming state, and the forge's merge is settled by the sweep after the run.
func TestAQueuedMergeIsOnTheRecordTheRunCompletesWith(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)

	recorded, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.Status != runstate.StatusSucceeded || recorded.Integration == nil {
		t.Fatalf("recorded = status %q, integration %#v; want a succeeded, integrated run", recorded.Status, recorded.Integration)
	}
	if recorded.PullRequest == nil {
		t.Fatalf("the record holds no pull request while the outcome reports #%d", outcome.PullRequest.Number)
	}
	if recorded.PullRequest.Number != outcome.PullRequest.Number || !recorded.PullRequest.MergeQueued {
		t.Fatalf("recorded pull request = %#v, want #%d with its merge queued, as the outcome reports", recorded.PullRequest, outcome.PullRequest.Number)
	}
	if len(fixture.forge.merges) != 1 {
		t.Fatalf("forge merges = %d, want the one request the run made", len(fixture.forge.merges))
	}
	if recorded.PublishFailure != "" {
		t.Fatalf("publish failure = %q on a merge the forge accepted", recorded.PublishFailure)
	}

	// The forge merges seconds later, with no run watching, and the next sweep
	// finishes the publication rather than leaving the record at the run's death.
	fixture.forge.performQueuedMerge(t)
	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the queued merge settled", results)
	}
	settled, err := fixture.store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !settled.PullRequest.Merged || settled.PullRequest.MergeQueued || settled.PullRequest.MergeCommit == "" {
		t.Fatalf("settled pull request = %#v, want the forge's merge recorded", settled.PullRequest)
	}
}

// run-c07d6849 on yoyodyne-ifd.141.3: the reviewer escalated on the granted
// repair round. An escalation integrates nothing, so no merge is asked for —
// that is the verb, not a lost record — and the request the run opened stays on
// its record, open and unmerged, and is named on the escalation entry the
// development manager decides from.
func TestAnEscalatedRunRecordsThePullRequestItLeftOnTheForge(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := newOutcomeTracker()
	forge := &fakeForge{remote: remote}
	provider := roleBackend(writeFeature, escalateVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration != nil {
		t.Fatalf("status = %q, integration = %#v, want an escalation that promoted nothing", outcome.Status, outcome.Integration)
	}
	if outcome.PullRequest == nil || outcome.PullRequest.Merged || outcome.PullRequest.MergeQueued {
		t.Fatalf("outcome pull request = %#v, want the request the developer phase opened, open and unmerged", outcome.PullRequest)
	}
	// Nothing authorized a merge, so nothing asked for one: an escalated change
	// on the forge is one only a person merges.
	if len(forge.merges) != 0 {
		t.Fatalf("forge merges = %#v, want none for an escalated change", forge.merges)
	}
	recorded, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.PullRequest == nil || recorded.PullRequest.Number != outcome.PullRequest.Number {
		t.Fatalf("recorded pull request = %#v, want #%d as the outcome reports", recorded.PullRequest, outcome.PullRequest.Number)
	}
	if !strings.Contains(tracker.notes, "Pull request: #1") {
		t.Errorf("the work item does not name the request:\n%s", tracker.notes)
	}
	// The entry is what the development manager reads before deciding, and the
	// request is an artifact of the run exactly as its branch is.
	entry := onlyDocketed(t, docket)
	if entry.Class != triage.ClassEscalation {
		t.Fatalf("entry class = %q, want the reviewer's escalation", entry.Class)
	}
	if entry.Artifacts.PullRequest != outcome.PullRequest.Number || entry.Artifacts.PullRequestURL != outcome.PullRequest.URL {
		t.Fatalf("entry artifacts = %#v, want pull request #%d %s named", entry.Artifacts, outcome.PullRequest.Number, outcome.PullRequest.URL)
	}
	if entry.Artifacts.PullRequestMerged {
		t.Error("the entry reports the request merged, and nothing merged it")
	}
	if rendered := entry.Render(); !strings.Contains(rendered, "Pull request (open on the forge, unmerged): #1") {
		t.Errorf("the rendered entry does not name the open request:\n%s", rendered)
	}
}

// The refusal the item asks for: a run whose outcome names a request its
// durable record does not hold is refused completion rather than recorded
// succeeded over it. The shape cannot be produced by the pipeline — the record
// and the outcome are written by one statement — so it is produced here by
// hand, through the registered completing step the pipeline itself goes
// through.
func TestCompletionRefusesARunWhoseRecordLostItsPullRequest(t *testing.T) {
	t.Parallel()

	const itemID = "yoyodyne-ifd.402"
	registry, err := deliveryRegistry()
	if err != nil {
		t.Fatalf("deliveryRegistry() error = %v", err)
	}
	complete, found := registry.Lookup("run.complete")
	if !found {
		t.Fatal(`Lookup("run.complete") found nothing`)
	}
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	state := completingRun(itemID, runstate.PhaseCompleting)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	tracker := &fakeTracker{item: beads.WorkItem{ID: itemID, Status: "in_progress"}}
	run := &activeRun{
		pipeline: Pipeline{Tracker: tracker, Store: store},
		claimed:  true,
		state:    state,
		outcome: Outcome{
			Integration: &gitworktree.Integration{TargetBranch: "main", SourceCommit: "b0bb1e5"},
			// What the summary would print: the request the run obtained and, on
			// this record, never wrote down.
			PullRequest: &runstate.PullRequest{Number: 544, URL: "https://forge.invalid/pull/544"},
		},
	}
	err = complete.Perform(context.Background(), run)
	if err == nil {
		t.Fatal("Perform() completed a run whose record holds no pull request")
	}
	for _, want := range []string{"pull request 544", "durable record holds none", "refused completion"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Perform() error = %v, want it to say %q", err, want)
		}
	}
	// Nothing about the run was recorded as finished: the item was not closed,
	// and what the tracker was told is the refusal rather than the outcome.
	if tracker.closed || countCalls(tracker.calls, "complete") != 0 {
		t.Errorf("the tracker was asked for %v on a refused completion, want no closure", tracker.calls)
	}
	refused, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if refused.Status != runstate.StatusFailed {
		t.Errorf("recorded status = %q, want the refusal recorded as a failed run", refused.Status)
	}
}

// The silence the item names, ended at the run: a publishing run that promoted
// a change and holds no request records the publication as outstanding rather
// than returning over it, and asks the forge for nothing. The account names the
// branch, because the branch is what the sweep then asks the forge by.
func TestAPromotionWithoutARequestIsRecordedAsAnOutstandingPublication(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	state := lostPublicationState()
	state.Status = runstate.StatusRunning
	state.Phase = runstate.PhaseIntegrating
	state.CompletedAt = nil
	state.WorktreeRemoved, state.BranchRemoved = false, false
	state.PublishFailure = ""
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	forge := &fakeForge{}
	run := &activeRun{
		pipeline:   Pipeline{Store: store, Publisher: forge},
		publishing: true,
		state:      state,
		worktree:   gitworktree.Worktree{Branch: state.Branch, TargetBranch: "main", BaseCommit: state.BaseCommit},
		outcome: Outcome{Integration: &gitworktree.Integration{
			TargetBranch:         "main",
			SourceCommit:         rearmedCommit,
			TargetCommit:         rearmedCommit,
			PreviousTargetCommit: rearmedBase,
		}},
	}
	if err := run.publishIntegration(context.Background()); err != nil {
		t.Fatalf("publishIntegration() error = %v", err)
	}
	if len(forge.merges) != 0 {
		t.Fatalf("forge merges = %#v, want nothing asked of the forge without a request to merge", forge.merges)
	}
	for _, want := range []string{"holds no pull request for branch " + state.Branch, "nothing was asked of the forge", "yoyo reconcile"} {
		if !strings.Contains(run.outcome.PublishFailure, want) {
			t.Errorf("publish failure %q does not say %q", run.outcome.PublishFailure, want)
		}
	}
	if run.state.MergeDrop != nil {
		t.Errorf("merge drop = %#v recorded over a merge that was never asked for", run.state.MergeDrop)
	}
	recorded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.PublishFailure != run.outcome.PublishFailure {
		t.Fatalf("durable publish failure = %q, want the outcome's %q", recorded.PublishFailure, run.outcome.PublishFailure)
	}
	// The account reaches the work item in the same words, which is where a
	// reader without the record finds it.
	if notes := strings.Join(renderPublishNotes(run.outcome), "\n"); !strings.Contains(notes, "Publication outstanding: "+run.outcome.PublishFailure) {
		t.Errorf("the item notes do not carry the outstanding publication:\n%s", notes)
	}
}

// The silence ended at the sweep: the next reconcile asks the forge for the
// request by the run's branch, records it, and from then on the publication is
// what the status line counts as awaiting the forge and what the docket lists
// for the development manager — rather than a promotion nothing reports.
func TestReconcileRecoversThePullRequestOfAPromotionThatRecordedNone(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	state := lostPublicationState()
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// Before the sweep, nothing that reads publications can see this one.
	if len(readmodel.AwaitingForge([]runstate.State{state})) != 0 {
		t.Fatal("a promotion with no recorded request counted as awaiting the forge before anything recorded the request")
	}
	forge := &fakeForge{number: 544}
	reconciler := Reconciler{
		Tracker:   newOutcomeTracker(),
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Publisher: forge,
	}

	recoveries, err := reconciler.RecoverPublications(context.Background())
	if err != nil {
		t.Fatalf("RecoverPublications() error = %v", err)
	}
	if len(recoveries) != 1 || !recoveries[0].Recovered || recoveries[0].Failure != "" {
		t.Fatalf("recoveries = %#v, want the one request recovered", recoveries)
	}
	if recoveries[0].Number != 544 || recoveries[0].Branch != state.Branch {
		t.Errorf("recovery = %#v, want pull request 544 found by branch %s", recoveries[0], state.Branch)
	}
	recovered, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recovered.PullRequest == nil {
		t.Fatal("the sweep reported the request recovered and the record still holds none")
	}
	published := *recovered.PullRequest
	if published.Number != 544 || published.Branch != state.Branch || published.State != "OPEN" || published.Merged {
		t.Fatalf("recovered pull request = %#v, want #544 on the run's branch, open and unmerged as the forge reports", published)
	}
	// The head is the commit the harness pushed and promoted, because the forge
	// named none; the remote is the one run branches are published to.
	if published.HeadCommit != rearmedCommit || published.Remote != "origin" {
		t.Errorf("recovered pull request = %#v, want head %s on origin", published, rearmedCommit)
	}
	// No merge was ever asked for, so no method is recorded: a method says the
	// run asked, and a re-arm reads it as the request to repeat.
	if published.MergeMethod != "" || published.MergeQueued {
		t.Errorf("recovered pull request = %#v, want no merge recorded as asked for", published)
	}
	// The run's own account of the loss stands until the publication finishes,
	// and it is what keeps the item out of the pull meanwhile.
	if recovered.PublishFailure != state.PublishFailure {
		t.Errorf("publish failure = %q, want the run's account kept: %q", recovered.PublishFailure, state.PublishFailure)
	}

	// From here every surface reads it. The status line counts it as awaiting
	// the forge, with the merge as the operator's move.
	awaiting := readmodel.AwaitingForge([]runstate.State{recovered})
	if len(awaiting) != 1 {
		t.Fatalf("awaiting the forge = %d run(s), want the recovered publication counted", len(awaiting))
	}
	// And the docket lists the publication for the development manager, keyed
	// to the run and the request, carrying the run's own account.
	docket := &memoryDocket{}
	build, err := docketerOverStore(docket, store, docketConfig()).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if build.Added != 1 {
		t.Fatalf("docket build = %#v, want the one publication docketed", build)
	}
	entries, _ := docket.List()
	if len(entries) != 1 || entries[0].Class != triage.ClassPublication || entries[0].Key != triage.PublicationKey(state.RunID, 544) {
		t.Fatalf("docket = %#v, want the publication of run %s and pull request 544", entries, state.RunID)
	}
	if entries[0].Publication == nil || !strings.Contains(entries[0].Publication.Message, "nothing was asked of the forge") {
		t.Errorf("docket entry publication = %#v, want the run's account of the lost request", entries[0].Publication)
	}
	// A second sweep finds nothing to recover: the record now holds the request.
	if again, err := reconciler.RecoverPublications(context.Background()); err != nil || len(again) != 0 {
		t.Fatalf("second RecoverPublications() = %#v, %v; want nothing left to recover", again, err)
	}
}

// A forge that holds no request for the branch leaves the record as the run
// wrote it, and says so on every sweep: the promotion is still one nothing can
// see waiting, and going quiet about it would read as having recovered it.
func TestReconcileReportsAPromotionWhoseRequestTheForgeDoesNotHold(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	state := lostPublicationState()
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	reconciler := Reconciler{
		Tracker:   newOutcomeTracker(),
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Publisher: &fakeForge{stateErr: errors.New("no pull request exists for branch " + state.Branch)},
	}
	recoveries, err := reconciler.RecoverPublications(context.Background())
	if err != nil {
		t.Fatalf("RecoverPublications() error = %v", err)
	}
	if len(recoveries) != 1 || recoveries[0].Recovered || !strings.Contains(recoveries[0].Failure, "no pull request exists") {
		t.Fatalf("recoveries = %#v, want the forge's refusal reported and nothing recovered", recoveries)
	}
	unchanged, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if unchanged.PullRequest != nil || unchanged.PublishFailure != state.PublishFailure {
		t.Fatalf("record = pull request %#v, publish failure %q; want the record left as the run wrote it", unchanged.PullRequest, unchanged.PublishFailure)
	}
}

// A publishing run that never promoted, and a local run that promoted and
// published nothing, are not lost publications: neither said it published a
// request it does not hold.
func TestOnlyAPromotionThatSaidItPublishedIsRecovered(t *testing.T) {
	t.Parallel()

	local := lostPublicationState()
	local.PublishFailure = ""
	if lostPublicationRecord(local) {
		t.Error("a local promotion with no publication failure was selected for recovery")
	}
	unpromoted := lostPublicationState()
	unpromoted.Integration = nil
	if lostPublicationRecord(unpromoted) {
		t.Error("a run that promoted nothing was selected for recovery")
	}
	live := lostPublicationState()
	live.Status = runstate.StatusRunning
	live.Phase = runstate.PhaseIntegrating
	live.CompletedAt = nil
	live.WorktreeRemoved, live.BranchRemoved = false, false
	if lostPublicationRecord(live) {
		t.Error("a run still in flight was selected for recovery; its publication is its own to record")
	}
	if !lostPublicationRecord(lostPublicationState()) {
		t.Error("the promoted, published, requestless run was not selected for recovery")
	}
}

// lostPublicationState is a succeeded, integrated run whose record says it
// published and holds no request: the state publishIntegration now writes
// instead of returning quietly, as it stands once the run is over.
func lostPublicationState() runstate.State {
	state := droppedPublication()
	state.Status = runstate.StatusSucceeded
	state.Phase = runstate.PhaseComplete
	state.WorktreeRemoved = true
	state.BranchRemoved = true
	state.Blocker = ""
	state.PullRequest = nil
	state.PublishFailure = lostPublication(state.RunID, state.WorkItemID, "main", state.Branch).Error()
	return state
}
