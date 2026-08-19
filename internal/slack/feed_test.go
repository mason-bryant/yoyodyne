package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A thread is a narrative rather than an event log scrolling sideways, so each
// transition is said once. What proves it is the second pass: the same records,
// the cursor the first pass left, and nothing further to say.
func TestEachMilestoneIsSaidOnceAndNotAgain(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs := newRunStore(t, root)
	state := newRunState("run-000000000000000000000000000000a1", runstate.StatusRunning)
	state.Selection = &runstate.Selection{
		By:     runstate.SelectedByDevelopmentManager,
		Reason: "next ready item on the critical path",
		At:     state.StartedAt,
	}
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	feed := &HarnessFeed{Runs: runs, Reports: newReportStore(t, root)}

	cursors := Cursors{Streams: map[string]Cursor{}}
	batch, err := feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(batch.Deliveries) != 1 {
		t.Fatalf("Poll() = %d deliveries, want only the run starting", len(batch.Deliveries))
	}
	started := batch.Deliveries[0]
	if started.Envelope.Kind != notify.KindRunStarted {
		t.Fatalf("kind = %q, want the run starting", started.Envelope.Kind)
	}
	// The invariant that makes the selection reason durable exists so an
	// operator can see why the harness chose what it chose. A message that
	// dropped it would be the half of that guarantee nobody can act on.
	if !strings.Contains(started.Envelope.Body, "next ready item on the critical path") {
		t.Fatalf("body = %q, want the recorded selection reason carried onto the message", started.Envelope.Body)
	}
	if started.Envelope.Topic != notify.WorkItemTopic(state.WorkItemID) {
		t.Fatalf("topic = %q, want the work item it is about", started.Envelope.Topic)
	}

	cursors.Streams[started.Stream] = Cursor{}.With(started.Mark)
	repeated, err := feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("second Poll() error = %v", err)
	}
	if len(repeated.Deliveries) != 0 {
		t.Fatalf("second Poll() = %#v, want nothing further to say", repeated.Deliveries)
	}
}

// A run with no recorded selection is exactly what the operator most needs to
// see, so it has to say so rather than post a line with a blank where the reason
// would be.
func TestARunNobodyAccountedForSaysSo(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs := newRunStore(t, root)
	if err := runs.Create(newRunState("run-000000000000000000000000000000a2", runstate.StatusRunning)); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	batch, err := (&HarnessFeed{Runs: runs, Reports: newReportStore(t, root)}).Poll(context.Background(), Cursors{Streams: map[string]Cursor{}})
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if !strings.Contains(batch.Deliveries[0].Envelope.Body, "No reason for the selection was recorded") {
		t.Fatalf("body = %q, want an unaccounted run to say it is unaccounted", batch.Deliveries[0].Envelope.Body)
	}
}

// A park nobody saw lift reads as a run that died quietly, so both halves are
// said — and the lift is only knowable by having said the park.
func TestAParkIsSaidAndSoIsTheRunCarryingOn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs := newRunStore(t, root)
	state := newRunState("run-000000000000000000000000000000a3", runstate.StatusRunning)
	resets := state.StartedAt.Add(time.Hour)
	state.UsageLimitResetsAt = &resets
	state.PauseCause = runstate.PauseUsageLimit
	state.UsageLimitKind = "provider"
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	feed := &HarnessFeed{Runs: runs, Reports: newReportStore(t, root)}
	stream := runStream(state.RunID)

	cursors := Cursors{Streams: map[string]Cursor{stream: Cursor{}.With("started")}}
	batch, err := feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(batch.Deliveries) != 1 || batch.Deliveries[0].Envelope.Kind != notify.KindRunParked {
		t.Fatalf("Poll() = %#v, want the run reported as waiting", batch.Deliveries)
	}
	if batch.Deliveries[0].Envelope.Severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want a wait reported as a warning", batch.Deliveries[0].Envelope.Severity)
	}

	// The wait is over, which is only sayable because the park was said.
	cursors.Streams[stream] = cursors.Streams[stream].With(batch.Deliveries[0].Mark)
	state.UsageLimitResetsAt = nil
	state.PauseCause = ""
	state.Phase = runstate.PhaseDeveloping
	if err := runs.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	resumed, err := feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() after the wait error = %v", err)
	}
	if len(resumed.Deliveries) != 1 || resumed.Deliveries[0].Envelope.Kind != notify.KindRunContinued {
		t.Fatalf("Poll() = %#v, want the run reported as running again", resumed.Deliveries)
	}
}

// The milestones a run reaches are what the thread is made of, and the ones that
// can legitimately recur carry the attempt they belong to, so saying a thing
// twice means it happened twice.
func TestWhatARunReachedIsWhatIsReported(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs := newRunStore(t, root)
	commit := strings.Repeat("a", 40)
	state := newRunState("run-000000000000000000000000000000a4", runstate.StatusSucceeded)
	state.Phase = runstate.PhaseComplete
	state.WorktreePath = "/tmp/worktrees/yoyodyne-work"
	state.WorktreeRemoved = true
	state.BranchRemoved = true
	state.Branch = "yoyodyne/work"
	state.BaseCommit = strings.Repeat("c", 40)
	state.TargetBranch = "main"
	state.ProviderSessionID = "session-developer"
	state.ProviderModel = "opus"
	state.ReviewSessionID = "session-reviewer"
	state.ReviewModel = "opus"
	state.ReviewDecision = runstate.ReviewApprove
	state.ReviewSummary = "the change does what the item asked"
	state.Integration = &runstate.Integration{
		TargetBranch:         "main",
		SourceCommit:         commit,
		TargetCommit:         commit,
		PreviousTargetCommit: strings.Repeat("c", 40),
	}
	state.PullRequest = &runstate.PullRequest{
		Remote: "origin", Branch: "yoyodyne/work", Number: 84,
		URL: "https://example.test/pull/84", HeadCommit: commit, Merged: true,
	}
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	batch, err := (&HarnessFeed{Runs: runs, Reports: newReportStore(t, root)}).Poll(context.Background(), Cursors{Streams: map[string]Cursor{}})
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	kinds := map[notify.Kind]bool{}
	for _, delivery := range batch.Deliveries {
		kinds[delivery.Envelope.Kind] = true
	}
	for _, want := range []notify.Kind{
		notify.KindRunStarted, notify.KindChecksPassed, notify.KindReviewVerdict,
		notify.KindPromotion, notify.KindPublication, notify.KindMerged, notify.KindRunFinished,
	} {
		if !kinds[want] {
			t.Errorf("Poll() said nothing about %q", want)
		}
	}
	if kinds[notify.KindBlocker] {
		t.Error("a run that succeeded must not be reported as blocked")
	}
}

// A failed run is the message an operator most needs at night, and it must not
// read like a note.
func TestAFailedRunIsReportedAsCritical(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs := newRunStore(t, root)
	state := newRunState("run-000000000000000000000000000000a5", runstate.StatusFailed)
	state.Phase = runstate.PhaseChecking
	state.Failure = "the repair budget was spent with the checks still failing"
	state.CheckFailure = &runstate.CheckFailure{Command: "go test ./...", ExitCode: 1}
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	batch, err := (&HarnessFeed{Runs: runs, Reports: newReportStore(t, root)}).Poll(context.Background(), Cursors{Streams: map[string]Cursor{}})
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	severities := map[notify.Kind]report.Severity{}
	for _, delivery := range batch.Deliveries {
		severities[delivery.Envelope.Kind] = delivery.Envelope.Severity
	}
	if severities[notify.KindBlocker] != report.SeverityCritical {
		t.Fatalf("blocker severity = %q, want critical", severities[notify.KindBlocker])
	}
	if severities[notify.KindChecksFailed] != report.SeverityWarning {
		t.Fatalf("failing checks severity = %q, want a warning", severities[notify.KindChecksFailed])
	}
	if _, said := severities[notify.KindRunFinished]; said {
		t.Error("a run that failed must not also be reported as finished")
	}
}

// A report is text an agent already wrote, at the severity the agent gave it.
// The harness's part is to carry it: rewording it here would be a paraphrase of
// something a person is meant to read verbatim.
func TestAReportIsCarriedVerbatimUnderItsAuthor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	reports := newReportStore(t, root)
	filed := report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            "report-0123456789abcdef0123456789abcdef",
		Role:          domain.RoleDeveloper,
		Agent:         "developer",
		RunID:         "run-000000000000000000000000000000a6",
		WorkItemID:    "yoyodyne-ifd.68.3",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      report.SeverityWarning,
		Message:       "the manifest's scopes were not verified against a live workspace",
		RecordedAt:    time.Now().UTC(),
	}
	if err := reports.Append(filed); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	feed := &HarnessFeed{Runs: newRunStore(t, root), Reports: reports}

	batch, err := feed.Poll(context.Background(), Cursors{Streams: map[string]Cursor{}})
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(batch.Deliveries) != 1 {
		t.Fatalf("Poll() = %#v, want the one filed report", batch.Deliveries)
	}
	delivered := batch.Deliveries[0]
	if delivered.Envelope.Body != filed.Message {
		t.Fatalf("body = %q, want the report as the agent wrote it", delivered.Envelope.Body)
	}
	if delivered.Envelope.Severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want the severity the agent filed it at", delivered.Envelope.Severity)
	}
	if delivered.Envelope.Speaker.Name() != "developer" {
		t.Fatalf("speaker = %q, want it attributed to its author", delivered.Envelope.Speaker.Name())
	}
	if delivered.Envelope.Topic != notify.WorkItemTopic("yoyodyne-ifd.68.3") {
		t.Fatalf("topic = %q, want the item the report is about", delivered.Envelope.Topic)
	}
	if delivered.Position != 1 {
		t.Fatalf("position = %d, want the log position the pile reached", delivered.Position)
	}

	// A report filed by a conversation has no work item, so it belongs to the
	// whole line rather than to an item nobody named.
	loose := filed
	loose.ID = "report-abcdef0123456789abcdef0123456789"
	loose.WorkItemID = ""
	if err := reports.Append(loose); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	next, err := feed.Poll(context.Background(), Cursors{Streams: map[string]Cursor{reportStream: {Position: 1}}})
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(next.Deliveries) != 1 || next.Deliveries[0].Envelope.Topic != notify.ProductTopic {
		t.Fatalf("Poll() = %#v, want the unattached report on the product topic", next.Deliveries)
	}
}

// A channel opened today does not want a month of history arriving at once, so
// what was already over before the sink started is left in the records. What is
// still in flight is caught up on in full, because that is news.
func TestWhatWasAlreadyOverBeforeTheSinkStartedIsNotReplayed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs := newRunStore(t, root)
	old := newRunState("run-000000000000000000000000000000a7", runstate.StatusSucceeded)
	live := newRunState("run-000000000000000000000000000000a8", runstate.StatusRunning)
	for _, state := range []runstate.State{old, live} {
		if err := runs.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
	reports := newReportStore(t, root)
	if err := reports.Append(report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            "report-0123456789abcdef0123456789abcdef",
		Role:          domain.RoleDeveloper,
		RunID:         old.RunID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      report.SeverityNote,
		Message:       "filed long before anybody was reading this channel",
		RecordedAt:    old.StartedAt,
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	feed := &HarnessFeed{Runs: runs, Reports: reports, Since: old.StartedAt.Add(24 * time.Hour)}
	batch, err := feed.Poll(context.Background(), Cursors{Streams: map[string]Cursor{}})
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(batch.Deliveries) != 1 {
		t.Fatalf("Poll() = %#v, want only the run that is still going", batch.Deliveries)
	}
	if batch.Deliveries[0].Stream != runStream(live.RunID) {
		t.Fatalf("delivery = %#v, want the live run rather than the finished one", batch.Deliveries[0])
	}

	// A run the sink has already said something about is followed to its end
	// whenever it ends, so an outage delays messages rather than losing them.
	finished := live
	finished.Status = runstate.StatusSucceeded
	completed := feed.Since.Add(time.Hour)
	finished.CompletedAt = &completed
	if err := runs.Save(finished); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	followed, err := feed.Poll(context.Background(), Cursors{Streams: map[string]Cursor{
		runStream(live.RunID): Cursor{}.With("started"),
	}})
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(followed.Deliveries) != 1 || followed.Deliveries[0].Envelope.Kind != notify.KindRunFinished {
		t.Fatalf("Poll() = %#v, want the run it had been reporting followed to its end", followed.Deliveries)
	}
}

func newRunStore(t *testing.T, root string) *runstate.Store {
	t.Helper()
	store, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

func newReportStore(t *testing.T, root string) *runstate.ReportStore {
	t.Helper()
	store, err := runstate.NewReportStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewReportStore() error = %v", err)
	}
	return store
}

func newRunState(runID string, status runstate.Status) runstate.State {
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.68.3",
		Backend:       domain.BackendClaudeCode,
		Status:        status,
		StartedAt:     now,
		UpdatedAt:     now,
	}
	if status.Terminal() {
		completed := now.Add(time.Hour)
		state.CompletedAt = &completed
	}
	return state
}
