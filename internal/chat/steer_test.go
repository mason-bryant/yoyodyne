package chat

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	backendapi "yoyodyne/internal/backend"
	"yoyodyne/internal/execution"
)

// TestAnOperatorTakesIntentThroughToIntegratedWorkInOneConversation is the
// threshold this work item exists for: stating intent, approving what it
// becomes, running it, and seeing it integrated, without leaving the
// conversation for a second tool.
func TestAnOperatorTakesIntentThroughToIntegratedWorkInOneConversation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	tracker := &fakeTracker{}
	work := &fakeWork{report: RunReport{
		RunID:          "run-0123456789abcdef0123456789abcdef",
		Status:         "succeeded",
		Branch:         "yoyodyne/yoyodyne-1/abcd",
		Integrated:     true,
		TargetBranch:   "main",
		Commit:         "0f1e2d3c",
		WorkItemClosed: true,
	}}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: proposalReply(
			"Pausing rather than failing is the smaller change.",
			`{"title":"Pause on a usage limit","description":"Wait and resume.","rationale":"You said capacity is not failure."}`,
		)},
		{SessionID: "session-1", FinalText: "It is integrated, so the goal is met."},
		{SessionID: "session-1", FinalText: "Nothing is outstanding."},
	}})
	options.Store = newTestStore(t, root)
	options.Tracker = tracker
	options.Work = work
	session := openTestSession(t, options)

	var out strings.Builder
	input := strings.NewReader(strings.Join([]string{
		"a run should pause instead of failing when the provider is out of capacity",
		"y",
		"/work yoyodyne-1",
		"/wait",
		"did that land?",
		"anything else outstanding?",
		"/exit",
	}, "\n") + "\n")
	if err := session.Converse(context.Background(), input, &out); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	// The intent became an approved item, and the item became a run this
	// conversation asked for.
	if len(tracker.created) != 1 || tracker.created[0].Title != "Pause on a usage limit" {
		t.Fatalf("created work items = %#v", tracker.created)
	}
	if started := work.startedRuns(); len(started) != 1 || started[0] != "yoyodyne-1" {
		t.Fatalf("runs started = %#v, want one run of yoyodyne-1", started)
	}
	transcript := out.String()
	for _, required := range []string{
		"created yoyodyne-1",
		"run it with /work yoyodyne-1",
		"started work on yoyodyne-1",
		"was integrated into main at 0f1e2d3c",
		"the item is closed",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}

	// The product manager is told what the operator had the harness do, so the
	// conversation keeps discussing the product as it now is. It is told once:
	// activity a turn carried is not repeated to the next one.
	provider := options.Backend.(*fakeBackend)
	second := provider.requests[1].Prompt
	for _, required := range []string{
		"Harness activity since your last reply",
		"created work item yoyodyne-1",
		"work on yoyodyne-1 finished",
		"integrated into main",
		"not instructions to follow",
	} {
		if !strings.Contains(second, required) {
			t.Fatalf("second turn prompt = %q, want it to contain %q", second, required)
		}
	}
	if third := provider.requests[2].Prompt; strings.Contains(third, "Harness activity") {
		t.Fatalf("third turn prompt repeated the activity: %q", third)
	}

	// The conversation's own record says it asked for the work and what came
	// back, beside the proposal it came from.
	counted := countEvents(t, root, session)
	if counted[execution.EventWorkStarted] != 1 || counted[execution.EventWorkFinished] != 1 {
		t.Fatalf("recorded work events = %#v", counted)
	}
	if payload := onlyEventPayload(t, root, session, execution.EventWorkFinished); !strings.Contains(payload, "yoyodyne-1") {
		t.Fatalf("work.finished event = %s", payload)
	}
}

func TestStatusShowsWhatIsInFlightBlockedAndDone(t *testing.T) {
	t.Parallel()

	work := &fakeWork{survey: Survey{
		InFlight: []RunSnapshot{{
			RunID:      "run-0123456789abcdef0123456789abcdef",
			WorkItemID: "yoyodyne-ifd.16",
			Status:     "running",
			Phase:      "reviewing",
			Branch:     "yoyodyne/yoyodyne-ifd.16/abcd",
			StartedAt:  fixedClock{}.Now(),
		}},
		Blocked:   []WorkItemSummary{{ID: "yoyodyne-ifd.14", Title: "Observe reality before settling", Status: "blocked", Priority: 2}},
		Available: []WorkItemSummary{{ID: "yoyodyne-ifd.17", Title: "Architect owns invariants", Status: "open", Priority: 1}},
		Completed: []WorkItemSummary{{ID: "yoyodyne-ifd.12", Title: "Pause on a usage limit", Status: "closed", Priority: 2}},
	}}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), strings.NewReader("/status\n/exit\n"), &out); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	transcript := out.String()
	for _, required := range []string{
		"in flight (1):",
		"yoyodyne-ifd.16 [running, reviewing]",
		"blocked (1):",
		"[yoyodyne-ifd.14] p2 Observe reality before settling",
		"available (1):",
		"completed (1):",
		"claimed: none",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
}

func TestStoppingWorkCancelsTheRunAndSettlesWhatItLeft(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// The run never finishes on its own, so only stopping it can end it.
	work := &fakeWork{
		gate:        make(chan struct{}),
		settlements: []Settlement{{RunID: "run-1", WorkItemID: "yoyodyne-1", Action: "blocked", Detail: "the worktree was preserved"}},
	}
	options := testOptions(t, &fakeBackend{})
	options.Store = newTestStore(t, root)
	options.Work = work
	session := openTestSession(t, options)

	var out strings.Builder
	input := strings.NewReader("/work yoyodyne-1\n/stop we are doing something else first\n/exit\n")
	if err := session.Converse(context.Background(), input, &out); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	if !work.wasCancelled() {
		t.Fatal("stopping did not cancel the run")
	}
	if settles := work.settleCount(); settles != 1 {
		t.Fatalf("settled %d time(s), want 1", settles)
	}
	notes := work.takenNotes()
	if len(notes) != 1 || notes[0][0] != "yoyodyne-1" {
		t.Fatalf("recorded notes = %#v", notes)
	}
	// Why the work stopped is recorded where the work is tracked, and traced
	// back to the conversation that stopped it.
	for _, required := range []string{"we are doing something else first", session.Evidence().ConversationID} {
		if !strings.Contains(notes[0][1], required) {
			t.Fatalf("stop note = %q, want it to contain %q", notes[0][1], required)
		}
	}
	transcript := out.String()
	for _, required := range []string{"stopped work on yoyodyne-1", "the worktree was preserved"} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
	// The record says what the operator was shown: the stop was asked for, and
	// the run it ended left a settled blocker behind. A run that ends leaves
	// exactly one terminal event, whether it was collected or stopped.
	counted := countEvents(t, root, session)
	if counted[execution.EventWorkStopped] != 1 || counted[execution.EventWorkFinished] != 1 {
		t.Fatalf("recorded work events = %#v", counted)
	}
	if requested := onlyEventPayload(t, root, session, execution.EventWorkStopped); !strings.Contains(requested, `"requested":true`) {
		t.Fatalf("work.stopped event = %s, want it recorded as the request it is", requested)
	}
	outcome := onlyEventPayload(t, root, session, execution.EventWorkFinished)
	for _, required := range []string{`"stopped":true`, `"already_finished":false`, "the worktree was preserved"} {
		if !strings.Contains(outcome, required) {
			t.Fatalf("work.finished event = %s, want it to contain %s", outcome, required)
		}
	}
	// The conversation is free again once the run it stopped is over.
	if running, _, ok := session.RunningWork(); ok {
		t.Fatalf("conversation still reports running %q after stopping it", running)
	}
}

func TestRedirectingStopsTheRunItRedirectsAndRecordsTheDirection(t *testing.T) {
	t.Parallel()

	t.Run("redirecting the running item stops it first", func(t *testing.T) {
		t.Parallel()

		work := &fakeWork{gate: make(chan struct{})}
		options := testOptions(t, &fakeBackend{})
		options.Work = work
		session := openTestSession(t, options)

		var out strings.Builder
		input := strings.NewReader("/work yoyodyne-1\n/redirect yoyodyne-1 keep the CLI surface unchanged\n/exit\n")
		if err := session.Converse(context.Background(), input, &out); err != nil {
			t.Fatalf("Converse() error = %v", err)
		}

		if !work.wasCancelled() {
			t.Fatal("redirecting left the attempt it redirects running")
		}
		notes := work.takenNotes()
		if len(notes) != 2 {
			t.Fatalf("recorded notes = %#v, want the stop and the direction", notes)
		}
		if !strings.Contains(notes[0][1], "in order to redirect it") {
			t.Fatalf("stop note = %q", notes[0][1])
		}
		if !strings.Contains(notes[1][1], "keep the CLI surface unchanged") {
			t.Fatalf("direction note = %q", notes[1][1])
		}
		transcript := out.String()
		for _, required := range []string{"recorded your direction on yoyodyne-1", "start it again with /work yoyodyne-1"} {
			if !strings.Contains(transcript, required) {
				t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
			}
		}
	})

	t.Run("redirecting a run that already finished does not offer to retry it", func(t *testing.T) {
		t.Parallel()

		// The run finished under a human integration policy before the
		// redirection reached it, so there is nothing to retry.
		work := &fakeWork{report: RunReport{Status: "succeeded", Branch: "yoyodyne/yoyodyne-1/abcd", WorktreePath: "/wt"}}
		options := testOptions(t, &fakeBackend{})
		options.Work = work
		session := openTestSession(t, options)

		if err := session.StartWork(context.Background(), "yoyodyne-1"); err != nil {
			t.Fatalf("StartWork() error = %v", err)
		}
		directed, err := session.DirectWork(context.Background(), "yoyodyne-1", "keep the CLI surface unchanged")
		if err != nil {
			t.Fatalf("DirectWork() error = %v", err)
		}
		if directed.Stopped == nil || !directed.Stopped.AlreadyFinished {
			t.Fatalf("directed = %#v, want the finished run reported as unstopped", directed)
		}
		rendered := directed.Render()
		if !strings.Contains(rendered, "recorded your direction on yoyodyne-1") {
			t.Fatalf("rendered redirection = %q", rendered)
		}
		if strings.Contains(rendered, "start it again") {
			t.Fatalf("rendered redirection offers to retry work that already finished: %q", rendered)
		}
		// Only the direction is recorded: nothing says the operator stopped a
		// run that finished on its own.
		notes := work.takenNotes()
		if len(notes) != 1 || !strings.Contains(notes[0][1], "keep the CLI surface unchanged") {
			t.Fatalf("recorded notes = %#v, want only the direction", notes)
		}
	})

	t.Run("redirecting an item nothing is running only records it", func(t *testing.T) {
		t.Parallel()

		work := &fakeWork{}
		options := testOptions(t, &fakeBackend{})
		options.Work = work
		session := openTestSession(t, options)

		var out strings.Builder
		if err := session.Converse(context.Background(), strings.NewReader("/redirect yoyodyne-2 prefer the smaller change\n/exit\n"), &out); err != nil {
			t.Fatalf("Converse() error = %v", err)
		}

		notes := work.takenNotes()
		if len(notes) != 1 || notes[0][0] != "yoyodyne-2" || !strings.Contains(notes[0][1], "prefer the smaller change") {
			t.Fatalf("recorded notes = %#v", notes)
		}
		if work.settleCount() != 0 {
			t.Fatal("redirecting an item nothing is running settled runs")
		}
		if len(work.startedRuns()) != 0 {
			t.Fatalf("redirecting started %#v", work.startedRuns())
		}
	})

	t.Run("a redirection with no direction is refused", func(t *testing.T) {
		t.Parallel()

		work := &fakeWork{}
		options := testOptions(t, &fakeBackend{})
		options.Work = work
		session := openTestSession(t, options)

		var out strings.Builder
		if err := session.Converse(context.Background(), strings.NewReader("/redirect yoyodyne-2\n/exit\n"), &out); err != nil {
			t.Fatalf("Converse() error = %v", err)
		}
		if len(work.takenNotes()) != 0 {
			t.Fatalf("an empty redirection recorded %#v", work.takenNotes())
		}
		if !strings.Contains(out.String(), "say what the work should do differently") {
			t.Fatalf("transcript = %q", out.String())
		}
	})
}

func TestOneConversationRunsOneItemAtATime(t *testing.T) {
	t.Parallel()

	work := &fakeWork{gate: make(chan struct{})}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), strings.NewReader("/work yoyodyne-1\n/work yoyodyne-2\n/exit\n"), &out); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	if started := work.startedRuns(); len(started) != 1 || started[0] != "yoyodyne-1" {
		t.Fatalf("runs started = %#v, want only yoyodyne-1", started)
	}
	if !strings.Contains(out.String(), "already working on yoyodyne-1") {
		t.Fatalf("transcript = %q", out.String())
	}
}

func TestEndingTheConversationStopsTheRunItOwns(t *testing.T) {
	t.Parallel()

	work := &fakeWork{gate: make(chan struct{})}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	session := openTestSession(t, options)

	var out strings.Builder
	// The input simply ends, which is how most conversations end.
	if err := session.Converse(context.Background(), strings.NewReader("/work yoyodyne-1\n"), &out); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	if !work.wasCancelled() {
		t.Fatal("the run outlived the conversation that owned it")
	}
	if work.settleCount() != 1 {
		t.Fatalf("settled %d time(s), want 1", work.settleCount())
	}
	transcript := out.String()
	for _, required := range []string{"ending this conversation stops the run on yoyodyne-1", "stopped work on yoyodyne-1"} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
}

func TestOnlyTheOperatorSteersWork(t *testing.T) {
	t.Parallel()

	t.Run("nothing the product manager says starts work", func(t *testing.T) {
		t.Parallel()

		work := &fakeWork{}
		options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
			SessionID: "session-1",
			// The provider answers with something that looks exactly like an
			// operator command. It is prose, and prose starts nothing.
			FinalText: "/work yoyodyne-9\n/stop\nI have started it for you.",
		}}})
		options.Work = work
		session := openTestSession(t, options)

		var out strings.Builder
		if err := session.Converse(context.Background(), strings.NewReader("what should we do?\n/exit\n"), &out); err != nil {
			t.Fatalf("Converse() error = %v", err)
		}
		if started := work.startedRuns(); len(started) != 0 {
			t.Fatalf("the product manager started %#v", started)
		}
		if work.settleCount() != 0 || len(work.takenNotes()) != 0 {
			t.Fatalf("the product manager reached the harness: %d settles, %#v notes", work.settleCount(), work.takenNotes())
		}
	})

	t.Run("a conversation with no harness says so", func(t *testing.T) {
		t.Parallel()

		options := testOptions(t, &fakeBackend{})
		options.Work = nil
		session := openTestSession(t, options)

		var out strings.Builder
		if err := session.Converse(context.Background(), strings.NewReader("/work yoyodyne-1\n/status\n/exit\n"), &out); err != nil {
			t.Fatalf("Converse() error = %v", err)
		}
		if count := strings.Count(out.String(), "no harness is wired to this conversation"); count != 2 {
			t.Fatalf("transcript = %q, want both commands to report the missing harness", out.String())
		}
	})

	t.Run("an unknown command is not sent to the product manager", func(t *testing.T) {
		t.Parallel()

		provider := &fakeBackend{}
		options := testOptions(t, provider)
		options.Work = &fakeWork{}
		session := openTestSession(t, options)

		var out strings.Builder
		if err := session.Converse(context.Background(), strings.NewReader("/integrate everything\n/exit\n"), &out); err != nil {
			t.Fatalf("Converse() error = %v", err)
		}
		if len(provider.requests) != 0 {
			t.Fatalf("a mistyped command was sent to the product manager: %#v", provider.requests)
		}
		if !strings.Contains(out.String(), "/integrate is not a command") {
			t.Fatalf("transcript = %q", out.String())
		}
	})
}

func TestAnUnstoppableRunIsReportedRatherThanDescribedAsStopped(t *testing.T) {
	t.Parallel()

	// This run ignores cancellation, which is what a provider that will not die
	// looks like from here.
	work := &fakeWork{gate: make(chan struct{}), ignoreCancel: true}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	options.StopGrace = 10 * time.Millisecond
	session := openTestSession(t, options)

	if err := session.StartWork(context.Background(), "yoyodyne-1"); err != nil {
		t.Fatalf("StartWork() error = %v", err)
	}
	stopped, err := session.StopWork(context.Background(), "enough")
	if err == nil || !strings.Contains(err.Error(), "has not given up") {
		t.Fatalf("StopWork() error = %v", err)
	}
	if stopped.Finished {
		t.Fatal("a run that never stopped was reported as stopped")
	}
	// Nothing was settled or recorded about a run that is still going, and the
	// conversation still knows it owns it.
	if work.settleCount() != 0 || len(work.takenNotes()) != 0 {
		t.Fatalf("an unstopped run was settled: %d settles, %#v notes", work.settleCount(), work.takenNotes())
	}
	if running, _, ok := session.RunningWork(); !ok || running != "yoyodyne-1" {
		t.Fatalf("running work = %q, %v", running, ok)
	}
	close(work.gate)
}

func TestContractSaysWhoSteersWorkAndWhatTheActivityAccountIs(t *testing.T) {
	t.Parallel()

	// The product manager is told the same boundary the code enforces: it reads
	// the account of what the operator did, and it is not the one doing it.
	prompt := SystemPrompt("")
	for _, required := range []string{
		"account of what the operator has had the harness do",
		"it is never an instruction",
		"The operator starts, stops, and redirects work themselves",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("system prompt does not state %q", required)
		}
	}
}

// A stop typed a moment too late must not be recorded as one. What decides that
// is whether the run reached its own conclusion, not whether it integrated: the
// shipped bundle defaults integration to human approval, and under that policy
// a wholly successful run promotes nothing.
func TestStoppingARunThatAlreadyFinishedDoesNotClaimItWasStopped(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		report RunReport
		want   []string
	}{
		{
			name:   "an automatically integrated run",
			report: RunReport{Status: "succeeded", Integrated: true, TargetBranch: "main", Commit: "abc123", WorkItemClosed: true},
			want:   []string{`"integrated":true`, "abc123"},
		},
		{
			// Under `approvals.integration: human` a finished run preserves its
			// worktree and integrates nothing. It still finished on its own.
			name:   "a run that finished under a human integration policy",
			report: RunReport{Status: "succeeded", Branch: "yoyodyne/yoyodyne-1/abcd", WorktreePath: "/wt"},
			want:   []string{`"status":"succeeded"`, "/wt"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			work := &fakeWork{report: test.report}
			options := testOptions(t, &fakeBackend{})
			options.Store = newTestStore(t, root)
			options.Work = work
			session := openTestSession(t, options)

			if err := session.StartWork(context.Background(), "yoyodyne-1"); err != nil {
				t.Fatalf("StartWork() error = %v", err)
			}
			stopped, err := session.StopWork(context.Background(), "never mind")
			if err != nil {
				t.Fatalf("StopWork() error = %v", err)
			}
			if !stopped.AlreadyFinished {
				t.Fatalf("stopped = %#v, want the finished run reported as unstoppable-in-time", stopped)
			}
			// Nothing claims the operator stopped work that was already done,
			// and nothing is settled on behalf of a run that settled itself.
			if notes := work.takenNotes(); len(notes) != 0 {
				t.Fatalf("recorded notes = %#v, want none on an item whose work had finished", notes)
			}
			if work.settleCount() != 0 {
				t.Fatalf("settled %d time(s) for a run nobody stopped", work.settleCount())
			}
			if rendered := stopped.Render(); !strings.Contains(rendered, "had already finished before the stop reached it") {
				t.Fatalf("rendered stop = %q", rendered)
			}
			// The durable record tells the same story the operator was told: a
			// stop was asked for, and the run finished on its own. A record that
			// asserted a stop and omitted the outcome would be false about both.
			if counted := countEvents(t, root, session); counted[execution.EventWorkFinished] != 1 {
				t.Fatalf("recorded work.finished events = %#v, want the run's outcome recorded once", counted)
			}
			outcome := onlyEventPayload(t, root, session, execution.EventWorkFinished)
			for _, required := range append([]string{`"stopped":false`, `"already_finished":true`}, test.want...) {
				if !strings.Contains(outcome, required) {
					t.Fatalf("work.finished event = %s, want it to contain %s", outcome, required)
				}
			}
		})
	}
}

// A paused run reported no failure either, but it is owed a continuation rather
// than finished, so the operator's stop is recorded against it.
func TestStoppingAPausedRunRecordsTheStopAndSaysItIsPreserved(t *testing.T) {
	t.Parallel()

	resetsAt := fixedClock{}.Now().Add(time.Hour)
	work := &fakeWork{report: RunReport{
		Status:             "running",
		Branch:             "yoyodyne/yoyodyne-1/abcd",
		Paused:             true,
		UsageLimitKind:     "five-hour",
		UsageLimitResetsAt: &resetsAt,
	}}
	options := testOptions(t, &fakeBackend{})
	options.Work = work
	session := openTestSession(t, options)

	if err := session.StartWork(context.Background(), "yoyodyne-1"); err != nil {
		t.Fatalf("StartWork() error = %v", err)
	}
	stopped, err := session.StopWork(context.Background(), "we are not waiting for that")
	if err != nil {
		t.Fatalf("StopWork() error = %v", err)
	}
	if stopped.AlreadyFinished {
		t.Fatalf("stopped = %#v, want a paused run treated as unfinished", stopped)
	}
	notes := work.takenNotes()
	if len(notes) != 1 || !strings.Contains(notes[0][1], "we are not waiting for that") {
		t.Fatalf("recorded notes = %#v, want the stop recorded on the item", notes)
	}
	// "Stopped" must not read as "over" for a run that is waiting to continue.
	rendered := stopped.Render()
	for _, required := range []string{"stopped work on yoyodyne-1", "it had paused itself before the stop reached it", "continues only if you start it again"} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered stop = %q, want it to contain %q", rendered, required)
		}
	}
}

func TestSurveyRenderingStatesEmptyGroupsAndCutsLongOnes(t *testing.T) {
	t.Parallel()

	survey := Survey{}
	for i := 0; i < maxSurveyItems+3; i++ {
		survey.Available = append(survey.Available, WorkItemSummary{
			ID:     fmt.Sprintf("yoyodyne-%d", i),
			Title:  "something to do",
			Status: "open",
		})
	}
	rendered := survey.Render()
	if !strings.Contains(rendered, fmt.Sprintf("available (%d):", maxSurveyItems+3)) {
		t.Fatalf("rendered survey = %q, want the exact count", rendered)
	}
	if !strings.Contains(rendered, "3 further available item(s) are not listed here.") {
		t.Fatalf("rendered survey = %q, want the cut named", rendered)
	}
	for _, group := range []string{"in flight: none", "claimed: none", "blocked: none", "completed: none"} {
		if !strings.Contains(rendered, group) {
			t.Fatalf("rendered survey = %q, want it to state %q", rendered, group)
		}
	}
}

func TestNoticesToTheProductManagerAreBounded(t *testing.T) {
	t.Parallel()

	session := openTestSession(t, testOptions(t, &fakeBackend{}))
	for i := 0; i < maxPendingNotices+5; i++ {
		session.notice("activity %d", i)
	}
	rendered := session.renderNotices()
	if strings.Count(rendered, "\n- activity ") != maxPendingNotices {
		t.Fatalf("rendered notices = %q, want %d of them", rendered, maxPendingNotices)
	}
	// The oldest activity is gone and the account says so rather than reading
	// as complete.
	if strings.Contains(rendered, "activity 0 ") || !strings.Contains(rendered, "earlier activity is not listed here") {
		t.Fatalf("rendered notices = %q", rendered)
	}
	if !strings.Contains(rendered, "activity 24") {
		t.Fatalf("rendered notices lost the most recent activity: %q", rendered)
	}
}

// fakeWork stands in for the harness behind an operator's commands. It records
// exactly what it was asked to do, which is what makes "only the operator
// steers work" an assertion rather than a claim.
type fakeWork struct {
	mu          sync.Mutex
	survey      Survey
	surveyErr   error
	started     []string
	report      RunReport
	runErr      error
	notes       [][2]string
	directErr   error
	settlements []Settlement
	settleErr   error
	settles     int
	cancelled   bool
	// gate holds every run until a test releases it, so a run can be observed
	// in flight. A nil gate finishes at once.
	gate chan struct{}
	// ignoreCancel makes a run refuse to give up, which is what stopping has to
	// cope with rather than assume away.
	ignoreCancel bool
}

func (f *fakeWork) Survey(context.Context) (Survey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.survey, f.surveyErr
}

func (f *fakeWork) Run(ctx context.Context, workItemID string) (RunReport, error) {
	f.mu.Lock()
	f.started = append(f.started, workItemID)
	gate, ignoreCancel := f.gate, f.ignoreCancel
	f.mu.Unlock()
	if gate != nil {
		if ignoreCancel {
			<-gate
		} else {
			select {
			case <-gate:
			case <-ctx.Done():
				f.mu.Lock()
				f.cancelled = true
				f.mu.Unlock()
				return RunReport{WorkItemID: workItemID, Status: "cancelled"}, ctx.Err()
			}
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	report := f.report
	report.WorkItemID = workItemID
	return report, f.runErr
}

func (f *fakeWork) Direct(_ context.Context, workItemID, note string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.directErr != nil {
		return f.directErr
	}
	f.notes = append(f.notes, [2]string{workItemID, note})
	return nil
}

func (f *fakeWork) Settle(context.Context) ([]Settlement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settles++
	return f.settlements, f.settleErr
}

func (f *fakeWork) startedRuns() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.started...)
}

func (f *fakeWork) takenNotes() [][2]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]string(nil), f.notes...)
}

func (f *fakeWork) settleCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settles
}

func (f *fakeWork) wasCancelled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelled
}

var _ Work = (*fakeWork)(nil)
