package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"yoyodyne/internal/chat"
	"yoyodyne/internal/contextbundle"
	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
	"yoyodyne/internal/gitworktree"
	"yoyodyne/internal/orchestrator"
	"yoyodyne/internal/runstate"
)

// The conversation steers work through this, which is a compile-time fact
// worth stating where the construction lives.
var _ chat.Work = conversationWork{}

func TestSurveyReadsRunStateAndEveryTrackerGroup(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	resetsAt := time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)
	started := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	if err := store.Create(runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         "run-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.16",
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusRunning,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     started,
		UpdatedAt:     started,
		// A run waiting out a usage limit is in flight and owed a continuation,
		// so the survey has to say that rather than only that it is running.
		UsageLimitResetsAt: &resetsAt,
		UsageLimitKind:     "five-hour",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	runner := &recordingRunner{stdout: `[{"id":"yoyodyne-9","title":"Pause on a usage limit","status":"open","priority":2,"issue_type":"task"}]`}
	work := conversationWork{
		tracker: chatTracker(runner, "/repo"),
		store:   store,
		timeout: chatTrackerTimeout,
	}
	survey, err := work.Survey(context.Background())
	if err != nil {
		t.Fatalf("Survey() error = %v", err)
	}

	if len(survey.InFlight) != 1 {
		t.Fatalf("in flight = %#v", survey.InFlight)
	}
	inFlight := survey.InFlight[0]
	if inFlight.WorkItemID != "yoyodyne-ifd.16" || inFlight.Status != "running" || inFlight.Phase != "developing" {
		t.Fatalf("in-flight run = %#v", inFlight)
	}
	if !strings.Contains(inFlight.Detail, "five-hour usage limit until 2026-08-16T09:00:00Z") {
		t.Fatalf("in-flight detail = %q", inFlight.Detail)
	}

	// Every group is asked for by name, in the order the survey reports them,
	// so a group is never missing merely because the tracker's default listing
	// left it out.
	wantStatuses := []string{"--status=in_progress", "--status=blocked", "--status=open", "--status=closed"}
	if len(runner.commands) != len(wantStatuses) {
		t.Fatalf("tracker commands = %#v", runner.commands)
	}
	for i, command := range runner.commands {
		if command.Name != "bd" || command.Dir != "/repo" || command.Timeout != chatTrackerTimeout {
			t.Fatalf("command %d = %#v", i, command)
		}
		if !contains(command.Args, wantStatuses[i]) {
			t.Fatalf("command %d args = %#v, want %s", i, command.Args, wantStatuses[i])
		}
	}
	for name, group := range map[string][]chat.WorkItemSummary{
		"claimed":   survey.Claimed,
		"blocked":   survey.Blocked,
		"available": survey.Available,
		"completed": survey.Completed,
	} {
		if len(group) != 1 || group[0].ID != "yoyodyne-9" || group[0].Priority != 2 {
			t.Fatalf("%s = %#v", name, group)
		}
	}
}

func TestSurveyNamesThePartsItCouldNotReadInsteadOfFailing(t *testing.T) {
	t.Parallel()

	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// The tracker answers for the first group and then stops answering, which
	// is what a tracker that becomes unavailable mid-survey looks like.
	runner := &flakyRunner{stdout: `[{"id":"yoyodyne-9","title":"Pause on a usage limit","status":"in_progress","issue_type":"task"}]`, failAfter: 1}
	work := conversationWork{tracker: chatTracker(runner, "/repo"), store: store, timeout: chatTrackerTimeout}

	survey, err := work.Survey(context.Background())
	if err != nil {
		t.Fatalf("Survey() error = %v, want the failures reported inside the survey", err)
	}
	if len(survey.Claimed) != 1 {
		t.Fatalf("claimed = %#v, want the group that was readable", survey.Claimed)
	}
	if len(survey.Unavailable) != 3 {
		t.Fatalf("unavailable = %#v, want the three groups that could not be read", survey.Unavailable)
	}
	rendered := survey.Render()
	// An unreadable group must never read as an empty one: it says so where it
	// would have been listed.
	if !strings.Contains(rendered, "blocked: could not be read, so treat it as unknown rather than empty: list blocked work items") {
		t.Fatalf("rendered survey = %q", rendered)
	}
	if strings.Contains(rendered, "blocked: none") {
		t.Fatalf("rendered survey described an unreadable group as empty: %q", rendered)
	}
	if !strings.Contains(rendered, "claimed (1):") {
		t.Fatalf("rendered survey lost the group it could read: %q", rendered)
	}
}

func TestDirectRecordsOperatorDirectionWithoutChangingStatus(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{stdout: `[{"id":"yoyodyne-9","title":"Pause on a usage limit","status":"open","issue_type":"task"}]`}
	work := conversationWork{tracker: chatTracker(runner, "/repo"), timeout: chatTrackerTimeout}
	if err := work.Direct(context.Background(), "yoyodyne-9", "prefer the smaller change"); err != nil {
		t.Fatalf("Direct() error = %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("tracker commands = %#v", runner.commands)
	}
	args := runner.commands[0].Args
	if !contains(args, "--append-notes=prefer the smaller change") {
		t.Fatalf("direction was not appended to the item's notes: %#v", args)
	}
	// Direction says what to do differently. Deciding the item is done, blocked,
	// or claimed is not part of saying it.
	for _, argument := range args {
		if strings.HasPrefix(argument, "--status") || argument == "close" || argument == "--claim" {
			t.Fatalf("recording direction changed the item's state: %#v", args)
		}
	}
}

// The claim /redirect rests on is that the direction reaches whoever picks the
// work up next. This walks that path with the real code on both sides of the
// tracker: the direction is appended to the item's notes, the item the next
// attempt claims carries them, and the context the pipeline assembles from that
// item (pipeline.go assembles exactly this bundle from the claimed item) puts
// them in front of the developer.
func TestDirectionRecordedInAConversationIsReadByTheNextAttempt(t *testing.T) {
	t.Parallel()

	direction := "Operator direction from product-manager conversation chat-1, after turn 2. The next attempt at this item is expected to follow it.\n\nKeep the CLI surface unchanged."
	runner := &appendingRunner{id: "yoyodyne-9", title: "Pause on a usage limit"}
	work := conversationWork{tracker: chatTracker(runner, "/repo"), timeout: chatTrackerTimeout}
	if err := work.Direct(context.Background(), "yoyodyne-9", direction); err != nil {
		t.Fatalf("Direct() error = %v", err)
	}

	// What the next attempt does: claim the item and assemble its context.
	item, err := chatTracker(runner, "/repo").Claim(context.Background(), "yoyodyne-9")
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	bundle, err := contextbundle.Assemble(contextbundle.Request{RepositoryRoot: t.TempDir(), WorkItem: item})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "Keep the CLI surface unchanged.") {
		t.Fatalf("developer context = %q, want it to carry the operator's direction", bundle.Text)
	}
	// It arrives as the item's notes, which is where the developer reads what it
	// has been told rather than somewhere it might skip.
	notesAt := strings.Index(bundle.Text, "## Notes")
	if notesAt < 0 || strings.Index(bundle.Text, "Keep the CLI surface unchanged.") < notesAt {
		t.Fatalf("developer context = %q, want the direction under the item's notes", bundle.Text)
	}
}

func TestRunReportClaimsIntegrationOnlyFromARecordedPromotion(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		outcome orchestrator.Outcome
		want    chat.RunReport
	}{
		{
			name: "an integrated run reports where the work landed",
			outcome: orchestrator.Outcome{
				RunID: "run-1", WorkItemID: "yoyodyne-9", Status: runstate.StatusSucceeded, Branch: "b",
				Integration:    &gitworktree.Integration{TargetBranch: "main", SourceCommit: "abc123", TargetCommit: "abc123"},
				WorkItemClosed: true,
			},
			want: chat.RunReport{
				RunID: "run-1", WorkItemID: "yoyodyne-9", Status: "succeeded", Branch: "b",
				Integrated: true, TargetBranch: "main", Commit: "abc123", WorkItemClosed: true,
			},
		},
		{
			name: "a blocked run reports the blocker and the artifacts it kept",
			outcome: orchestrator.Outcome{
				RunID: "run-2", WorkItemID: "yoyodyne-9", Status: runstate.StatusFailed, Branch: "b",
				WorktreePath: "/wt", Blocked: true, RepairAttempts: 2, Failure: "the reviewer's findings are unresolved",
			},
			want: chat.RunReport{
				RunID: "run-2", WorkItemID: "yoyodyne-9", Status: "failed", Branch: "b",
				WorktreePath: "/wt", Blocked: true, RepairAttempts: 2, Failure: "the reviewer's findings are unresolved",
			},
		},
		{
			name: "a paused run is reported as still in flight",
			outcome: orchestrator.Outcome{
				RunID: "run-3", WorkItemID: "yoyodyne-9", Status: runstate.StatusRunning,
				Paused: true, UsageLimitKind: "weekly",
			},
			want: chat.RunReport{
				RunID: "run-3", WorkItemID: "yoyodyne-9", Status: "running", Paused: true, UsageLimitKind: "weekly",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := runReportOf(test.outcome); got != test.want {
				t.Fatalf("runReportOf() = %#v, want %#v", got, test.want)
			}
		})
	}
}

// appendingRunner stands in for the one thing this depends on bd to do: an
// appended note becomes part of the item the tracker reports afterwards.
// Everything downstream of that — decoding the item and assembling the
// developer's context from it — is the real code under test.
type appendingRunner struct {
	id    string
	title string
	notes string
}

func (r *appendingRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	for _, argument := range command.Args {
		note, appended := strings.CutPrefix(argument, "--append-notes=")
		if !appended {
			continue
		}
		if r.notes != "" {
			r.notes += "\n\n"
		}
		r.notes += note
	}
	encoded, err := json.Marshal([]map[string]any{{
		"id":         r.id,
		"title":      r.title,
		"status":     "in_progress",
		"issue_type": "task",
		"notes":      r.notes,
	}})
	if err != nil {
		return execution.ProcessResult{}, err
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: string(encoded)}, nil
}

// flakyRunner answers a fixed number of commands and then fails, so a survey
// can be checked against a tracker that stops answering partway through.
type flakyRunner struct {
	stdout    string
	failAfter int
	commands  int
}

func (r *flakyRunner) Run(_ context.Context, _ execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.commands++
	if r.commands > r.failAfter {
		return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "bd is unavailable"}, nil
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: r.stdout}, nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
