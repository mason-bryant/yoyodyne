package notify

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func running() runstate.State {
	return runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         "run-4d1f",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.68.2",
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusRunning,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     moment,
		UpdatedAt:     moment,
		Selection: &runstate.Selection{
			By:     runstate.SelectedByDevelopmentManager,
			Reason: "highest-priority item nothing is holding back",
			At:     moment,
		},
	}
}

// crossed reports the kinds one reading to the next produced, in order, and
// fails the test if anything it produced could not be said.
func crossed(t *testing.T, before, after runstate.State) ([]Kind, []Notification) {
	t.Helper()
	notifications, err := FromRun(before, after)
	if err != nil {
		t.Fatalf("select from run state: %v", err)
	}
	kinds := make([]Kind, 0, len(notifications))
	for _, notification := range notifications {
		if _, err := Render(notification.Topic, notification.Speaker, notification.Event); err != nil {
			t.Fatalf("a selected %s could not be said: %v", notification.Event.Kind, err)
		}
		kinds = append(kinds, notification.Event.Kind)
	}
	return kinds, notifications
}

func only(t *testing.T, notifications []Notification, kind Kind) Notification {
	t.Helper()
	for _, notification := range notifications {
		if notification.Event.Kind == kind {
			return notification
		}
	}
	t.Fatalf("nothing said %s", kind)
	return Notification{}
}

func TestARunStartingCarriesWhyItWasSelected(t *testing.T) {
	// The reason the invariant makes durable is the reason an operator reads.
	kinds, notifications := crossed(t, runstate.State{}, running())
	if len(kinds) != 1 || kinds[0] != KindRunStarted {
		t.Fatalf("a fresh run crossed %v", kinds)
	}
	started := notifications[0]
	if started.Topic.Key() != "work-item:yoyodyne-ifd.68.2" {
		t.Fatalf("addressed to %q", started.Topic.Key())
	}
	if started.Speaker.Role != domain.RoleDevelopmentManager {
		t.Fatalf("spoken by %q", started.Speaker.Key())
	}
	message, err := Render(started.Topic, started.Speaker, started.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "highest-priority item nothing is holding back") {
		t.Fatalf("body %q does not carry the recorded reason", message.Body)
	}
}

// The record says what the item is called, so every message a run produces is
// addressed to a topic that can be named in words. The sink reads durable
// records and never the tracker, so a title the run did not write down is a
// title nothing downstream can recover.
func TestARunAddressesItsTopicByWhatTheRecordCallsTheItem(t *testing.T) {
	state := running()
	state.WorkItemTitle = "Slack thread headers carry the item's title, not just its slug"
	_, notifications := crossed(t, runstate.State{}, state)
	started := notifications[0]
	if started.Topic.Key() != "work-item:yoyodyne-ifd.68.2" {
		t.Fatalf("addressed to %q, want the identifier to stay the key", started.Topic.Key())
	}
	if started.Topic.Title != state.WorkItemTitle {
		t.Fatalf("topic title = %q, want %q", started.Topic.Title, state.WorkItemTitle)
	}
	// A run recorded before titles were written is addressed exactly as it was.
	_, older := crossed(t, runstate.State{}, running())
	if older[0].Topic.Title != "" {
		t.Fatalf("topic title = %q, want nothing where the record carried nothing", older[0].Topic.Title)
	}
}

func TestARunStartingIsSpokenByTheSelectorTheRecordNames(t *testing.T) {
	// The development manager speaks only for the work its own triage chose. The
	// operator is not a persona and the scheduler is not a role, so a run either
	// of them selected is the harness's account rather than anybody's judgment.
	for _, selected := range []struct {
		by      string
		speaker string
	}{
		{by: runstate.SelectedByDevelopmentManager, speaker: string(domain.RoleDevelopmentManager)},
		{by: runstate.SelectedByOperator, speaker: HarnessSpeaker},
		{by: runstate.SelectedByScheduler, speaker: HarnessSpeaker},
	} {
		state := running()
		state.Selection = &runstate.Selection{By: selected.by, Reason: "named on the command line", At: moment}
		_, notifications := crossed(t, runstate.State{}, state)
		started := only(t, notifications, KindRunStarted)
		if started.Speaker.Key() != selected.speaker {
			t.Fatalf("a run selected by the %s is spoken by the %s, want the %s", selected.by, started.Speaker.Key(), selected.speaker)
		}
	}
}

func TestAnOperatorNamedRunSaysTheOperatorChoseIt(t *testing.T) {
	// The harness speaks for it, and what it says still names the selector: the
	// operator is who a reader has to be able to see behind the choice.
	state := running()
	state.Selection = &runstate.Selection{By: runstate.SelectedByOperator, Reason: "from a conversation, after turn 103", At: moment}
	_, notifications := crossed(t, runstate.State{}, state)
	started := only(t, notifications, KindRunStarted)
	message, err := Render(started.Topic, started.Speaker, started.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, runstate.SelectedByOperator) {
		t.Fatalf("body %q does not say the operator chose it", message.Body)
	}
	if !strings.Contains(message.Body, "from a conversation, after turn 103") {
		t.Fatalf("body %q does not carry the recorded reason", message.Body)
	}
}

func TestARunWithNoRecordedSelectionSaysSoRatherThanNothing(t *testing.T) {
	state := running()
	state.Selection = nil
	_, notifications := crossed(t, runstate.State{}, state)
	started := only(t, notifications, KindRunStarted)
	// Nobody is named as having chosen it, so nobody speaks for it either.
	if !started.Speaker.IsHarness() {
		t.Fatalf("an unaccounted run is spoken by the %s", started.Speaker.Key())
	}
	message, err := Render(started.Topic, started.Speaker, started.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "no reason recorded") {
		t.Fatalf("an unaccounted run reads as %q", message.Body)
	}
}

func TestAFreshAttemptIsNotComparedToThePreviousOne(t *testing.T) {
	// A second run at the same item must not read as the first one losing its
	// promotion, its verdict, and its published request.
	first := running()
	first.Phase = runstate.PhaseCompleting
	first.ReviewDecision = runstate.ReviewApprove
	first.Integration = &runstate.Integration{TargetBranch: "main", SourceCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("a", 40)}
	second := running()
	second.RunID = "run-91b2"
	kinds, _ := crossed(t, first, second)
	if len(kinds) != 1 || kinds[0] != KindRunStarted {
		t.Fatalf("a fresh attempt crossed %v", kinds)
	}
}

func TestEachCrossingIsSaidOnce(t *testing.T) {
	state := running()
	state.Phase = runstate.PhaseReviewing
	state.ReviewDecision = runstate.ReviewApprove
	kinds, _ := crossed(t, runstate.State{}, state)
	if len(kinds) == 0 {
		t.Fatal("a fresh run crossed nothing")
	}
	if again, _ := crossed(t, state, state); len(again) != 0 {
		t.Fatalf("re-reading the same record said %v again", again)
	}
}

func TestASinkThatStartsLateSaysWhatItMissed(t *testing.T) {
	// At-least-once catch-up: a sink that was away must not pretend the run began
	// where it first read it.
	state := running()
	state.Phase = runstate.PhaseCompleting
	state.ReviewDecision = runstate.ReviewApprove
	state.Integration = &runstate.Integration{TargetBranch: "main", TargetCommit: strings.Repeat("a", 40), SourceCommit: strings.Repeat("a", 40)}
	kinds, _ := crossed(t, runstate.State{}, state)
	for _, want := range []Kind{KindRunStarted, KindChecksPassed, KindReviewApproved, KindPromoted} {
		found := false
		for _, kind := range kinds {
			if kind == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("a late sink did not say %s; it said %v", want, kinds)
		}
	}
}

func TestAFailingCheckIsSaidAsAWarningInTheChecksOwnWords(t *testing.T) {
	before := running()
	after := before
	after.CheckFailure = &runstate.CheckFailure{Command: "go test ./...", ExitCode: 1}
	kinds, notifications := crossed(t, before, after)
	if len(kinds) != 1 || kinds[0] != KindChecksFailed {
		t.Fatalf("a failing check crossed %v", kinds)
	}
	failed := only(t, notifications, KindChecksFailed)
	if failed.Event.Severity != report.SeverityWarning {
		t.Fatalf("a failing check is a %s", failed.Event.Severity)
	}
	message, err := Render(failed.Topic, failed.Speaker, failed.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "go test ./...") || !strings.Contains(message.Body, "1") {
		t.Fatalf("body %q does not carry the check itself", message.Body)
	}
	// The same failure again is the same fact; a different one is news.
	if again, _ := crossed(t, after, after); len(again) != 0 {
		t.Fatalf("the same failing check was said twice: %v", again)
	}
	differently := after
	differently.CheckFailure = &runstate.CheckFailure{Command: "go vet ./...", ExitCode: 2}
	if kinds, _ := crossed(t, after, differently); len(kinds) != 1 || kinds[0] != KindChecksFailed {
		t.Fatalf("a different failing check crossed %v", kinds)
	}
}

func TestTheVerdictIsTheReviewersOwnAccount(t *testing.T) {
	before := running()
	before.Phase = runstate.PhaseReviewing
	for decision, want := range map[string]Kind{
		runstate.ReviewApprove: KindReviewApproved,
		runstate.ReviewRepair:  KindReviewRepairs,
	} {
		after := before
		after.ReviewDecision = decision
		after.ReviewFindings = 3
		kinds, notifications := crossed(t, before, after)
		if len(kinds) != 1 || kinds[0] != want {
			t.Fatalf("%s crossed %v", decision, kinds)
		}
		verdict := notifications[0]
		if verdict.Speaker.Role != domain.RoleReviewer {
			t.Fatalf("%s spoken by %q", decision, verdict.Speaker.Key())
		}
	}
	after := before
	after.ReviewDecision = runstate.ReviewRepair
	after.ReviewFindings = 3
	_, notifications := crossed(t, before, after)
	message, err := Render(notifications[0].Topic, notifications[0].Speaker, notifications[0].Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "3 findings") {
		t.Fatalf("body %q does not count the findings", message.Body)
	}
}

// awaitingVerdict is the record as it stands with the checks behind it and the
// review about to run: the evidence is cleared and no verdict is recorded.
func awaitingVerdict() runstate.State {
	state := running()
	state.Phase = runstate.PhaseReviewing
	return state
}

// reviewed is the record as it stands once a review has answered: the phase it
// answered in, the session that answered, and the verdict itself.
func reviewed(session, decision string, findings, attempts int) runstate.State {
	state := running()
	state.Phase = runstate.PhaseReviewing
	state.ReviewSessionID = session
	state.ReviewDecision = decision
	state.ReviewFindings = findings
	state.RepairAttempts = attempts
	return state
}

// repairing is the record as it stands while the developer works on what the
// reviewer asked for: the verdict is still standing, the count has moved, and no
// new review has happened.
func repairing(from runstate.State) runstate.State {
	state := from
	state.Phase = runstate.PhaseDeveloping
	state.RepairAttempts = from.RepairAttempts + 1
	return state
}

// verdicts reports only the verdicts one reading to the next produced. A repair
// round re-runs the deterministic checks and crosses them again on its way back
// to a review, which is a real crossing and not what these tests are about.
func verdicts(t *testing.T, before, after runstate.State) []Kind {
	t.Helper()
	kinds, _ := crossed(t, before, after)
	said := make([]Kind, 0, len(kinds))
	for _, kind := range kinds {
		if kind == KindReviewApproved || kind == KindReviewRepairs {
			said = append(said, kind)
		}
	}
	return said
}

func TestEveryRoundOfARepairLoopIsSaid(t *testing.T) {
	// A repair loop produces several verdicts and most of them say the same word.
	// Keyed on the word alone, a thread would read as one request for repairs
	// followed by an approval, with every round between them missing.
	first := reviewed("review-session-1", runstate.ReviewRepair, 3, 0)
	if said := verdicts(t, awaitingVerdict(), first); len(said) != 1 || said[0] != KindReviewRepairs {
		t.Fatalf("the first verdict crossed %v", said)
	}
	// The verdict stands over the whole repair round and is not said again.
	working := repairing(first)
	if said := verdicts(t, first, working); len(said) != 0 {
		t.Fatalf("a verdict standing over a repair round was said again as %v", said)
	}
	// The next review clears the evidence before it runs, and a sink reading in
	// that window sees no verdict rather than the previous one.
	clearing := working
	clearing.Phase = runstate.PhaseReviewing
	clearing.ReviewSessionID = ""
	clearing.ReviewDecision = ""
	clearing.ReviewFindings = 0
	if said := verdicts(t, working, clearing); len(said) != 0 {
		t.Fatalf("clearing the evidence before a review crossed %v", said)
	}
	second := reviewed("review-session-2", runstate.ReviewRepair, 3, 1)
	if said := verdicts(t, clearing, second); len(said) != 1 || said[0] != KindReviewRepairs {
		t.Fatalf("the second verdict crossed %v", said)
	}
	// And a sink that missed the window between them still says both, because
	// what identifies a verdict is the review that gave it rather than its word.
	// This is the case the decision alone gets wrong: the word did not change and
	// neither did the repair count, and it is still a second verdict.
	if said := verdicts(t, working, second); len(said) != 1 || said[0] != KindReviewRepairs {
		t.Fatalf("a second verdict read across the clearing window crossed %v", said)
	}
	// An identical verdict from an identical review is the same fact, said once.
	if said := verdicts(t, second, second); len(said) != 0 {
		t.Fatalf("re-reading one verdict said %v again", said)
	}
	approved := reviewed("review-session-3", runstate.ReviewApprove, 0, 2)
	if said := verdicts(t, second, approved); len(said) != 1 || said[0] != KindReviewApproved {
		t.Fatalf("the approval crossed %v", said)
	}
}

func TestAVerdictIsSaidEvenWhenNoSessionWasRecorded(t *testing.T) {
	// The session is what tells two verdicts apart; the decision is compared
	// beside it so a record missing the session loses the rounds rather than the
	// verdict.
	before := reviewed("", runstate.ReviewRepair, 2, 0)
	if said := verdicts(t, awaitingVerdict(), before); len(said) != 1 || said[0] != KindReviewRepairs {
		t.Fatalf("a verdict with no recorded session crossed %v", said)
	}
	after := reviewed("", runstate.ReviewApprove, 0, 1)
	if said := verdicts(t, before, after); len(said) != 1 || said[0] != KindReviewApproved {
		t.Fatalf("an approval with no recorded session crossed %v", said)
	}
}

func TestAReplayedChangeGetsItsOwnVerdict(t *testing.T) {
	// An integration retry discards the verdict and obtains a fresh independent
	// one, because the reviewed change is not the change that would now be
	// promoted. That is a new verdict and is said as one.
	approved := reviewed("review-session-1", runstate.ReviewApprove, 0, 0)
	replayed := approved
	replayed.ReviewSessionID = ""
	replayed.ReviewDecision = ""
	replayed.IntegrationRetries = 1
	if said := verdicts(t, approved, replayed); len(said) != 0 {
		t.Fatalf("discarding a verdict crossed %v", said)
	}
	again := reviewed("review-session-2", runstate.ReviewApprove, 0, 0)
	again.IntegrationRetries = 1
	if kinds, _ := crossed(t, replayed, again); len(kinds) != 1 || kinds[0] != KindReviewApproved {
		t.Fatalf("the verdict on the replayed change crossed %v", kinds)
	}
}

func TestAPromotionIsSaidByTheHarnessThatMadeIt(t *testing.T) {
	// No agent performs a promotion, so no persona gets to give an account of one.
	before := running()
	before.Phase = runstate.PhaseIntegrating
	after := before
	after.Integration = &runstate.Integration{
		TargetBranch: "main",
		SourceCommit: strings.Repeat("b", 40),
		TargetCommit: strings.Repeat("b", 40),
	}
	kinds, notifications := crossed(t, before, after)
	if len(kinds) != 1 || kinds[0] != KindPromoted {
		t.Fatalf("a promotion crossed %v", kinds)
	}
	promoted := notifications[0]
	if !promoted.Speaker.IsHarness() {
		t.Fatalf("a promotion is spoken by %q", promoted.Speaker.Key())
	}
	message, err := Render(promoted.Topic, promoted.Speaker, promoted.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "main") || !strings.Contains(message.Body, "bbbbbbbbbbbb") {
		t.Fatalf("body %q does not say where the change went", message.Body)
	}
}

func TestPublicationQueuedMergeAndMergeAreThreeSeparateFacts(t *testing.T) {
	before := running()
	published := before
	published.PullRequest = &runstate.PullRequest{
		Remote: "origin", Branch: "yoyodyne/ifd-68-2", Number: 84,
		URL: "https://example.test/pull/84", HeadCommit: strings.Repeat("c", 40),
	}
	if kinds, _ := crossed(t, before, published); len(kinds) != 1 || kinds[0] != KindPublished {
		t.Fatalf("a publication crossed %v", kinds)
	}
	queued := published
	request := *published.PullRequest
	request.MergeQueued = true
	queued.PullRequest = &request
	if kinds, _ := crossed(t, published, queued); len(kinds) != 1 || kinds[0] != KindMergeQueued {
		t.Fatalf("a queued merge crossed %v", kinds)
	}
	merged := queued
	settled := request
	settled.MergeQueued = false
	settled.Merged = true
	merged.PullRequest = &settled
	kinds, notifications := crossed(t, queued, merged)
	if len(kinds) != 1 || kinds[0] != KindMergeCompleted {
		t.Fatalf("a merge crossed %v", kinds)
	}
	message, err := Render(notifications[0].Topic, notifications[0].Speaker, notifications[0].Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, "#84") {
		t.Fatalf("body %q does not name the request", message.Body)
	}
}

func TestAParkedRunSaysWhatItIsWaitingOn(t *testing.T) {
	reset := moment.Add(2 * time.Hour)
	held := moment.Add(-time.Hour)
	// An exhausted limit is the one park an operator did not cause and cannot
	// shorten, so it is the one said as a warning. The rest are notes: a hold and
	// a directive are waiting on the person reading the channel, and an overload
	// lifts in seconds.
	for name, park := range map[string]struct {
		apply    func(*runstate.State)
		severity report.Severity
	}{
		"an exhausted provider usage limit": {
			apply: func(s *runstate.State) {
				s.UsageLimitResetsAt = &reset
				s.PauseCause = runstate.PauseUsageLimit
				s.UsageLimitKind = "provider"
			},
			severity: report.SeverityWarning,
		},
		"a transient provider server overload": {
			apply: func(s *runstate.State) {
				s.UsageLimitResetsAt = &reset
				s.PauseCause = runstate.PauseServerOverload
			},
			severity: report.SeverityNote,
		},
		"an operator hold on all harness activity": {
			apply: func(s *runstate.State) {
				s.OperatorHeldSince = &held
				s.PauseCause = runstate.PauseOperatorHold
			},
			severity: report.SeverityNote,
		},
		"an unresolved directive (ambiguous)": {
			apply: func(s *runstate.State) {
				s.DirectivePause = &runstate.DirectivePause{
					DirectiveID: "directive-7f3a",
					Kind:        "ambiguous",
					Unresolved:  "which branch the change belongs on",
				}
			},
			severity: report.SeverityNote,
		},
	} {
		before := running()
		after := before
		park.apply(&after)
		kinds, notifications := crossed(t, before, after)
		if len(kinds) != 1 || kinds[0] != KindRunParked {
			t.Fatalf("%s crossed %v", name, kinds)
		}
		parkedRun := notifications[0]
		message, err := Render(parkedRun.Topic, parkedRun.Speaker, parkedRun.Event)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if !strings.Contains(message.Body, name) {
			t.Fatalf("a run waiting on %s reads as %q", name, message.Body)
		}
		if parkedRun.Event.Severity != park.severity {
			t.Fatalf("a run waiting on %s is said at %q, want %q", name, parkedRun.Event.Severity, park.severity)
		}
		// The weight is said in words before it is said in decoration, so the
		// distinction survives a reader whose client renders neither.
		if park.severity == report.SeverityWarning && !strings.Contains(message.Body, "Warning") {
			t.Fatalf("a run waiting on %s reads as %q, which does not say its weight in words", name, message.Body)
		}
		// A run held on a directive leads back to the directive itself, because a
		// pause nobody can name is a pause nobody can lift.
		if after.DirectivePause != nil && parkedRun.Event.Refs.DirectiveID != "directive-7f3a" {
			t.Fatalf("a directive pause refers to %q", parkedRun.Event.Refs.DirectiveID)
		}
		// And it says so when it carries on.
		if kinds, _ := crossed(t, after, before); len(kinds) != 1 || kinds[0] != KindRunContinued {
			t.Fatalf("resuming from %s crossed %v", name, kinds)
		}
	}
}

func TestARefusalOutsideARunIsSaidAtWarningAndNamesWhatIsWaiting(t *testing.T) {
	reset := moment.Add(3 * time.Hour)
	refused := runstate.UsageLimitExhaustion{
		SchemaVersion:  runstate.UsageLimitSchemaVersion,
		ProductID:      "yoyodyne",
		At:             moment,
		Waiting:        "the product manager conversation chat-91253e0e",
		Kind:           "five-hour",
		ResetsAt:       &reset,
		ConversationID: "chat-91253e0e",
	}
	notification, err := FromUsageLimit(refused)
	if err != nil {
		t.Fatalf("select from a refusal: %v", err)
	}
	if notification.Event.Severity != report.SeverityWarning {
		t.Fatalf("a refusal is said at %q, want %q", notification.Event.Severity, report.SeverityWarning)
	}
	// A refusal that stopped no work item belongs to the whole line rather than
	// to somebody's thread, and the harness says it because no persona did it.
	if notification.Topic != Product() {
		t.Fatalf("a refusal with no work item is addressed to %v", notification.Topic)
	}
	if !notification.Speaker.IsHarness() {
		t.Fatalf("a refusal is spoken by %v", notification.Speaker)
	}
	message, err := Render(notification.Topic, notification.Speaker, notification.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"Warning",
		refused.Waiting,
		"an exhausted five-hour usage limit",
		reset.UTC().Format(time.RFC3339),
	} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("a refusal reads as %q, which does not say %q", message.Body, want)
		}
	}
	// A provider that named no reset time says so rather than reading as one that
	// lifts at some moment nobody recorded.
	refused.ResetsAt = nil
	unknown, err := FromUsageLimit(refused)
	if err != nil {
		t.Fatalf("select from a refusal with no reset: %v", err)
	}
	silent, err := Render(unknown.Topic, unknown.Speaker, unknown.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(silent.Body, "until") {
		t.Fatalf("a refusal that named no reset reads as %q", silent.Body)
	}
	// A refusal that did stop one item's work is addressed to that item's thread,
	// which is where somebody following the item is already reading.
	refused.WorkItemID = "yoyodyne-ifd.68.10"
	addressed, err := FromUsageLimit(refused)
	if err != nil {
		t.Fatalf("select from a refusal on an item: %v", err)
	}
	if addressed.Topic.Kind != TopicWorkItem || addressed.Topic.ID != refused.WorkItemID {
		t.Fatalf("a refusal on an item is addressed to %v", addressed.Topic)
	}
	// Every persona has a line for it, so a producer that is not the harness
	// still says it in a voice somebody wrote.
	for _, speaker := range []Speaker{
		Harness(),
		Persona(domain.RoleDeveloper, ""),
		Persona(domain.RoleReviewer, ""),
		Persona(domain.RoleDevelopmentManager, ""),
		Persona(domain.RoleProductManager, ""),
		Persona(domain.RoleArchitect, ""),
	} {
		if _, err := Render(addressed.Topic, speaker, addressed.Event); err != nil {
			t.Fatalf("the %s cannot say a refusal: %v", speaker.Key(), err)
		}
	}
}

func TestARunThatStoppedAndStayedStoppedIsSaidAsCritical(t *testing.T) {
	before := running()
	completed := moment.Add(time.Minute)
	after := before
	after.Status = runstate.StatusFailed
	after.CompletedAt = &completed
	after.Failure = "the repair budget was spent with the checks still failing"
	kinds, notifications := crossed(t, before, after)
	blockerRecorded := only(t, notifications, KindBlockerRecorded)
	if blockerRecorded.Event.Severity != report.SeverityCritical {
		t.Fatalf("a blocker is a %s among %v", blockerRecorded.Event.Severity, kinds)
	}
	message, err := Render(blockerRecorded.Topic, blockerRecorded.Speaker, blockerRecorded.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(message.Body, after.Failure) {
		t.Fatalf("body %q does not carry the recorded reason", message.Body)
	}
	if !strings.Contains(message.Body, "Critical") {
		t.Fatalf("body %q does not say its severity in words", message.Body)
	}
	// A run that succeeded is not a blocker however it is read.
	succeeded := after
	succeeded.Status = runstate.StatusSucceeded
	succeeded.Failure = ""
	if kinds, _ := crossed(t, before, succeeded); len(kinds) != 0 {
		t.Fatalf("a successful run crossed %v", kinds)
	}
}

func TestARunWithNoUsableWorkItemIsRefusedRatherThanMisaddressed(t *testing.T) {
	state := running()
	state.WorkItemID = ""
	if _, err := FromRun(runstate.State{}, state); err == nil {
		t.Fatal("selected from a run naming no work item")
	}
	// A reading with no run at all is nothing to say rather than a failure: it is
	// what the first moments of every run look like.
	notifications, err := FromRun(runstate.State{}, runstate.State{})
	if err != nil || len(notifications) != 0 {
		t.Fatalf("an empty reading gave %v, %v", notifications, err)
	}
}

func TestAReportIsSaidByTheRoleThatFiledItAtItsOwnSeverity(t *testing.T) {
	for _, role := range domain.Roles() {
		filed := report.Report{
			SchemaVersion: report.SchemaVersion,
			ID:            "report-" + strings.Repeat("a", 32),
			Role:          role,
			Agent:         "opus",
			RunID:         "run-4d1f",
			WorkItemID:    "yoyodyne-ifd.68.2",
			ProductID:     "yoyodyne",
			RepositoryID:  "yoyodyne",
			Severity:      report.SeverityWarning,
			Message:       "the staleness check reads the tracker twice",
			RecordedAt:    moment,
		}
		notification, err := FromReport(filed)
		if err != nil {
			t.Fatalf("select a %s report: %v", role, err)
		}
		if notification.Speaker.Role != role || notification.Speaker.Agent != "opus" {
			t.Fatalf("a %s report is spoken by %+v", role, notification.Speaker)
		}
		if notification.Event.Severity != report.SeverityWarning {
			t.Fatalf("a %s report is a %s", role, notification.Event.Severity)
		}
		message, err := Render(notification.Topic, notification.Speaker, notification.Event)
		if err != nil {
			t.Fatalf("render a %s report: %v", role, err)
		}
		if !strings.Contains(message.Body, filed.Message) {
			t.Fatalf("a %s report reads as %q", role, message.Body)
		}
		if message.Identity.Name == "" || !strings.Contains(message.Identity.Name, "opus") {
			t.Fatalf("a %s report appears as %q", role, message.Identity.Name)
		}
	}
}

func TestSomethingConcerningNoItemIsAddressedToTheProduct(t *testing.T) {
	filed := report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            "report-" + strings.Repeat("a", 32),
		Role:          domain.RoleProductManager,
		RunID:         "chat-91253e0e",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      report.SeverityNote,
		Message:       "the brief has not been revised since the goals were rewritten",
		RecordedAt:    moment,
	}
	notification, err := FromReport(filed)
	if err != nil {
		t.Fatalf("select a report from a conversation: %v", err)
	}
	if notification.Topic.Kind != TopicProduct {
		t.Fatalf("addressed to %q", notification.Topic.Key())
	}
}

func TestAProposalCarriesBothHalvesOfWhatWasWritten(t *testing.T) {
	proposal := amendment.Proposal{
		SchemaVersion: amendment.SchemaVersion,
		ID:            "amendment-" + strings.Repeat("a", 32),
		Role:          domain.RoleDeveloper,
		RunID:         "run-4d1f",
		WorkItemID:    "yoyodyne-ifd.68.2",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Artifact:      "slack-reporting-design",
		Kind:          artifact.KindDesign,
		Owner:         domain.RoleArchitect,
		Change:        "say which role speaks each reportable kind",
		Why:           "the notifier has to pick a speaker and the design does not name one",
		RaisedAt:      moment,
	}
	notification, err := FromProposal(proposal)
	if err != nil {
		t.Fatalf("select a proposal: %v", err)
	}
	message, err := Render(notification.Topic, notification.Speaker, notification.Event)
	if err != nil {
		t.Fatalf("render a proposal: %v", err)
	}
	for _, half := range []string{proposal.Artifact, proposal.Change, proposal.Why} {
		if !strings.Contains(message.Body, half) {
			t.Fatalf("body %q does not carry %q", message.Body, half)
		}
	}
}

func TestTheOperatorsSwitchesAreAddressedToTheWholeLine(t *testing.T) {
	for _, notification := range []Notification{
		FromIntakeHold(runstate.IntakeHold{
			SchemaVersion: runstate.IntakeHoldSchemaVersion,
			ProductID:     "yoyodyne",
			HeldAt:        moment,
			Reason:        "reordering the backlog first",
		}),
		IntakeReleased(moment),
		FromOperatorHold(runstate.OperatorHold{SchemaVersion: runstate.OperatorHoldSchemaVersion, HeldAt: moment}),
		HoldLifted(moment),
	} {
		if notification.Topic.Kind != TopicProduct {
			t.Fatalf("%s addressed to %q", notification.Event.Kind, notification.Topic.Key())
		}
		if !notification.Speaker.IsHarness() {
			t.Fatalf("%s spoken by %q", notification.Event.Kind, notification.Speaker.Key())
		}
		if _, err := Render(notification.Topic, notification.Speaker, notification.Event); err != nil {
			t.Fatalf("render %s: %v", notification.Event.Kind, err)
		}
	}
	held := FromIntakeHold(runstate.IntakeHold{HeldAt: moment, Reason: "reordering the backlog first"})
	message, err := Render(held.Topic, held.Speaker, held.Event)
	if err != nil {
		t.Fatalf("render a held intake: %v", err)
	}
	if !strings.Contains(message.Body, "reordering the backlog first") {
		t.Fatalf("body %q does not carry the operator's reason", message.Body)
	}
}

// A watch session is about the whole line rather than any one item, so it is
// addressed and spoken exactly as the operator's switches are — and the one
// state somebody has to act on is the one said as a warning.
func TestAWatchSessionIsAddressedToTheWholeLine(t *testing.T) {
	states := map[runstate.WatchState]Kind{
		runstate.WatchWatching: KindWatchStarted,
		runstate.WatchIdle:     KindWatchIdle,
		runstate.WatchBraked:   KindWatchBraked,
		runstate.WatchResumed:  KindWatchResumed,
		runstate.WatchStopped:  KindWatchStopped,
	}
	for state, kind := range states {
		notification, err := FromWatch(watchTransition(state, "the backlog is empty"))
		if err != nil {
			t.Fatalf("address a %s session: %v", state, err)
		}
		if notification.Event.Kind != kind {
			t.Fatalf("%s was said as %q, want %q", state, notification.Event.Kind, kind)
		}
		if notification.Topic.Kind != TopicProduct || !notification.Speaker.IsHarness() {
			t.Fatalf("%s addressed to %q and spoken by %q", state, notification.Topic.Key(), notification.Speaker.Key())
		}
		message, err := Render(notification.Topic, notification.Speaker, notification.Event)
		if err != nil {
			t.Fatalf("render %s: %v", state, err)
		}
		if !strings.Contains(message.Body, "the backlog is empty") {
			t.Fatalf("body %q does not carry what the session said about itself", message.Body)
		}
		wantSeverity := report.SeverityNote
		if state == runstate.WatchBraked {
			wantSeverity = report.SeverityWarning
		}
		if notification.Event.Severity != wantSeverity {
			t.Fatalf("%s said at %q, want %q", state, notification.Event.Severity, wantSeverity)
		}
	}
	// A state nothing has words for is refused rather than posted as something
	// nobody wrote a line for.
	if _, err := FromWatch(watchTransition(runstate.WatchState("pondering"), "")); err == nil {
		t.Fatal("FromWatch() error = nil, want a state nothing says refused")
	}
}

func watchTransition(state runstate.WatchState, reason string) runstate.WatchTransition {
	return runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     "watch-0123456789abcdef0123456789abcdef",
		State:         state,
		At:            moment,
		Reason:        reason,
	}
}

// recorder is a poster that keeps what it was handed, so a test can check that
// the notifier renders before it posts rather than after.
type recorder struct {
	posted []Message
}

func (r *recorder) Post(_ context.Context, message Message) error {
	r.posted = append(r.posted, message)
	return nil
}

func TestTheNotifierRendersEachEventBeforeItIsPosted(t *testing.T) {
	posted := &recorder{}
	notifier := New(posted, nil)
	_, notifications := crossed(t, runstate.State{}, running())
	for _, notification := range notifications {
		if err := notification.Notify(context.Background(), notifier); err != nil {
			t.Fatalf("notify: %v", err)
		}
	}
	if len(posted.posted) != len(notifications) {
		t.Fatalf("posted %d messages for %d events", len(posted.posted), len(notifications))
	}
	message := posted.posted[0]
	if message.SchemaVersion != SchemaVersion || message.Topic != "work-item:yoyodyne-ifd.68.2" {
		t.Fatalf("posted %+v", message)
	}
	if err := message.Validate(); err != nil {
		t.Fatalf("posted a message that does not validate: %v", err)
	}
}

func TestAnUnsayableEventNeverReachesThePoster(t *testing.T) {
	posted := &recorder{}
	err := New(posted, nil).Notify(context.Background(), Product(), Harness(), Event{Kind: "run.exploded", At: moment, Severity: report.SeverityNote})
	if err == nil {
		t.Fatal("notified an event nobody wrote words for")
	}
	if len(posted.posted) != 0 {
		t.Fatalf("posted %d messages anyway", len(posted.posted))
	}
}

func TestReportingWithNowhereToReportIsNotAFailure(t *testing.T) {
	// Reporting is observation, never a gate: a product nobody pointed at a
	// workspace behaves exactly as it did before there was one.
	if err := (Discard{}).Notify(context.Background(), Product(), Harness(), Event{
		Kind: KindHoldPlaced, At: moment, Severity: report.SeverityNote,
	}); err != nil {
		t.Fatalf("discarding a notification failed: %v", err)
	}
}
