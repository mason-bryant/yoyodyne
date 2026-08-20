package notify

// What is worth saying, read from the record rather than from whatever is
// executing the work.
//
// Selection is a pure comparison of two readings of a durable record, which is
// what makes reporting an observation instead of a gate: nothing here is called
// from a run, so nothing here can slow one, fail one, or park one. A sink that
// was away comes back, re-reads, and finds exactly the crossings it missed.
//
// It reports transitions rather than states, so each one is said once however
// often the record is read. That is the same discipline the conversation's
// activity lines follow, and it is what keeps a thread a narrative rather than
// an event log scrolling sideways.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// FromRun reports what a run crossed between two readings of its durable state.
// A zero-valued before is a run this sink has not reported on yet, so everything
// its record already holds is a crossing: a sink that starts late says what
// happened rather than pretending the run began where it was first read.
func FromRun(before, after runstate.State) ([]Notification, error) {
	if strings.TrimSpace(after.RunID) == "" {
		return nil, nil
	}
	topic, err := WorkItem(after.WorkItemID)
	if err != nil {
		return nil, fmt.Errorf("address run %s: %w", after.RunID, err)
	}
	refs := Refs{RunID: after.RunID, WorkItemID: after.WorkItemID}
	if after.PullRequest != nil {
		refs.PullRequest = after.PullRequest.URL
	}
	at := after.UpdatedAt
	if at.IsZero() {
		at = after.StartedAt
	}
	// A different run is a different narrative, so the previous one's record is
	// no baseline for it: compared against a run that had already been promoted,
	// a fresh attempt at the same item would read as one that lost its promotion.
	started := before.RunID != after.RunID
	if started {
		before = runstate.State{}
	}

	var crossed []Notification
	sayWith := func(kind Kind, severity report.Severity, speaker Speaker, detail Detail, text string) {
		eventRefs := refs
		if kind == KindRunParked && after.DirectivePause != nil {
			eventRefs.DirectiveID = after.DirectivePause.DirectiveID
		}
		crossed = append(crossed, Notification{
			Topic:   topic,
			Speaker: speaker,
			Event: Event{
				Kind:     kind,
				At:       at,
				Severity: severity,
				Refs:     eventRefs,
				Detail:   detail,
				Text:     text,
			},
		})
	}
	say := func(kind Kind, severity report.Severity, speaker Speaker, detail Detail) {
		sayWith(kind, severity, speaker, detail, "")
	}

	// A run starting is the one crossing whose speaker varies, and it follows the
	// selector the record names: a persona must not narrate a selection it never
	// made.
	if started {
		say(KindRunStarted, report.SeverityNote, selectionSpeaker(after.Selection), selectionDetail(after.Selection))
	}
	if parked(before) && !parked(after) && !after.Status.Terminal() {
		say(KindRunContinued, report.SeverityNote, Harness(), Detail{})
	}
	if !checksBehind(before) && checksBehind(after) {
		say(KindChecksPassed, report.SeverityNote, Harness(), Detail{})
	}
	// A failing check is said whenever the recorded failure changes, so a second
	// repair attempt that fails differently is news rather than silence. A repeat
	// of exactly the same failure is the same fact and is said once.
	if after.CheckFailure != nil && (before.CheckFailure == nil || *before.CheckFailure != *after.CheckFailure) {
		say(KindChecksFailed, report.SeverityWarning, Harness(), Detail{
			Command:  after.CheckFailure.Command,
			ExitCode: after.CheckFailure.ExitCode,
		})
	}
	// A verdict is keyed on the invocation that gave it rather than on the words
	// it used, because a repair loop produces several and most of them say the
	// same word. The decision alone would report the first request for repairs
	// and silently swallow every one after it, leaving a thread that reads as one
	// repair followed by an approval with the rounds between them missing. The
	// reviewer's session is cleared and re-recorded around every review, so two
	// verdicts are always two sessions; the decision is compared beside it so a
	// verdict recorded without a session is still not lost. Neither field moves
	// while a run is repairing, so the verdict standing over a repair round is
	// not said a second time.
	if verdictGiven(after) && (after.ReviewSessionID != before.ReviewSessionID || after.ReviewDecision != before.ReviewDecision) {
		verdict := KindReviewRepairs
		if after.ReviewDecision == runstate.ReviewApprove {
			verdict = KindReviewApproved
		}
		say(verdict, report.SeverityNote, Persona(domain.RoleReviewer, ""), Detail{Findings: after.ReviewFindings})
	}
	// A promotion is the harness's own act — no agent performs one — so the
	// harness is the speaker rather than any persona.
	if before.Integration == nil && after.Integration != nil {
		say(KindPromoted, report.SeverityNote, Harness(), Detail{
			TargetBranch: after.Integration.TargetBranch,
			Commit:       after.Integration.TargetCommit,
		})
	}
	if before.PullRequest == nil && after.PullRequest != nil {
		say(KindPublished, report.SeverityNote, Harness(), Detail{PullRequest: describePullRequest(after.PullRequest)})
	}
	if mergeQueued(after) && !mergeQueued(before) {
		say(KindMergeQueued, report.SeverityNote, Harness(), Detail{PullRequest: describePullRequest(after.PullRequest)})
	}
	if merged(after) && !merged(before) {
		say(KindMergeCompleted, report.SeverityNote, Harness(), Detail{PullRequest: describePullRequest(after.PullRequest)})
	}
	if !parked(before) && parked(after) {
		say(KindRunParked, report.SeverityNote, Harness(), Detail{Cause: causeOf(after)})
	}
	// A run that stopped and stayed stopped is the one thing nobody finds out
	// about on their own, so it is the one crossing said as critical.
	if !blocked(before) && blocked(after) {
		sayWith(KindBlockerRecorded, report.SeverityCritical, Harness(), Detail{}, blockerText(after))
	}
	return crossed, nil
}

// FromReport says what an agent noticed while its own work carried on. The
// speaker is the reporting role itself, because a report is the one outbound
// message that is entirely the agent's own words, and the severity is the one
// the agent chose rather than one derived from anything here.
func FromReport(reported report.Report) (Notification, error) {
	topic, err := topicForItem(reported.WorkItemID)
	if err != nil {
		return Notification{}, fmt.Errorf("address report %s: %w", reported.ID, err)
	}
	return Notification{
		Topic:   topic,
		Speaker: Persona(reported.Role, reported.Agent),
		Event: Event{
			Kind:     KindReportFiled,
			At:       reported.RecordedAt,
			Severity: reported.Severity,
			Refs:     Refs{RunID: reported.RunID, WorkItemID: reported.WorkItemID},
			Text:     reported.Message,
		},
	}, nil
}

// FromProposal says that a role asked the owner of a document for a change to
// it. Both halves the agent wrote are carried: what should become true, and the
// case for it, which is the whole of what the owner decides on.
func FromProposal(proposal amendment.Proposal) (Notification, error) {
	topic, err := topicForItem(proposal.WorkItemID)
	if err != nil {
		return Notification{}, fmt.Errorf("address proposal %s: %w", proposal.ID, err)
	}
	text := strings.TrimSpace(proposal.Change)
	if why := strings.TrimSpace(proposal.Why); why != "" {
		text += " — " + why
	}
	return Notification{
		Topic:   topic,
		Speaker: Persona(proposal.Role, proposal.Agent),
		Event: Event{
			Kind:     KindProposalRaised,
			At:       proposal.RaisedAt,
			Severity: report.SeverityNote,
			Refs:     Refs{RunID: proposal.RunID, WorkItemID: proposal.WorkItemID},
			Detail:   Detail{Artifact: proposal.Artifact},
			Text:     text,
		},
	}, nil
}

// The operator's two switches are about the whole line rather than any one item,
// so they are addressed to the product and spoken by the harness: what an
// operator did is not any persona's account to give.
//
// Only the holds themselves are records; what lifts either is their absence, so
// releasing takes the moment it was observed rather than a record of it.

func FromIntakeHold(hold runstate.IntakeHold) Notification {
	return productNotification(KindIntakeHeld, hold.HeldAt, Detail{Reason: hold.Reason})
}

func IntakeReleased(at time.Time) Notification {
	return productNotification(KindIntakeReleased, at, Detail{})
}

func FromOperatorHold(hold runstate.OperatorHold) Notification {
	return productNotification(KindHoldPlaced, hold.HeldAt, Detail{})
}

func HoldLifted(at time.Time) Notification {
	return productNotification(KindHoldLifted, at, Detail{})
}

// Nothing here selects KindExchangeTurn or KindExchangeClosed, and so nothing
// ever addresses a topic to an exchange. That is deliberate and it is not
// finished work: ask exchanges are yoyodyne-ifd.99's, and they are the recorded
// second consumer of this interface rather than a producer that exists now. The
// envelope, the addressing, and every persona's line for both kinds are here so
// that arriving later costs that work a selection function and nothing else —
// but until it lands, an exchange thread is a path this package can describe and
// cannot yet reach, and it should not be read as behaviour already delivered.

func productNotification(kind Kind, at time.Time, detail Detail) Notification {
	return Notification{
		Topic:   Product(),
		Speaker: Harness(),
		Event: Event{
			Kind:     kind,
			At:       at,
			Severity: report.SeverityNote,
			Detail:   detail,
		},
	}
}

// topicForItem addresses something to the item it concerns, or to the product
// where it concerns none: a conversation has no assigned work rather than
// unknown work, and burying what it said in some item's thread would misfile it.
func topicForItem(workItemID string) (Topic, error) {
	if strings.TrimSpace(workItemID) == "" {
		return Product(), nil
	}
	return WorkItem(workItemID)
}

// selectionDetail carries the recorded account of why the harness is running
// this item. A run recorded before selections existed carries none, and the
// absence is stated rather than rendered as a blank — an unaccounted run is
// exactly what recording the reason exists to make visible.
// selectionSpeaker is whose account the start of a run is. The development
// manager speaks where its triage chose the item, because that choice is its own
// judgment; everything else is the harness, which is the same rule the whole
// table follows. The operator is not a persona and the scheduler is not a role,
// so neither has a voice to be spoken in, and a run whose selection nothing
// recorded has nobody to attribute at all — the harness's flat sentence is the
// honest account of it rather than a persona claiming a choice the record cannot
// show it made.
func selectionSpeaker(selection *runstate.Selection) Speaker {
	if selection == nil || strings.TrimSpace(selection.By) != runstate.SelectedByDevelopmentManager {
		return Harness()
	}
	return Persona(domain.RoleDevelopmentManager, "")
}

func selectionDetail(selection *runstate.Selection) Detail {
	if selection == nil {
		return Detail{}
	}
	return Detail{SelectedBy: selection.By, SelectionReason: selection.Reason}
}

// checksBehind reports a run with the deterministic checks behind it: the record
// carries no failing check and the run has moved past running them. Both halves
// are needed. A run in a later phase with a failing check recorded is one the
// checks handed back to the developer, and a run that has not reached reviewing
// has not been past the gate yet whatever else its record says. It is the rule
// the conversation's own milestones read the record by, stated here rather than
// imported so this stays independent of how a run is executed.
func checksBehind(state runstate.State) bool {
	if state.CheckFailure != nil {
		return false
	}
	switch state.Phase {
	case runstate.PhaseReviewing, runstate.PhaseIntegrating, runstate.PhaseCompleting, runstate.PhaseCleaningUp, runstate.PhaseComplete:
		return true
	default:
		return false
	}
}

// verdictGiven reports a record that holds a reviewer's verdict at all. The
// record passes through no verdict twice per round — it is cleared before each
// review runs and written again when that review answers — so the absence is a
// real state of a run rather than only the state of one that has never been
// reviewed.
func verdictGiven(state runstate.State) bool {
	return state.ReviewDecision != ""
}

// parked reports a run stopped short of finishing with an instruction to resume:
// a provider that refused it, the operator holding everything, or a directive
// nobody has resolved. All three keep the run's claim and its worktree, which is
// what makes a park different from a failure.
func parked(state runstate.State) bool {
	return state.UsageLimitResetsAt != nil || state.OperatorHeldSince != nil || state.DirectivePause != nil
}

// causeOf names what a parked run is waiting on, as the object of "waiting on".
// A directive is named first because it is the only one nothing but a person can
// clear, and the deadline is said with a provider refusal because "until when"
// is the part an operator plans around.
func causeOf(state runstate.State) string {
	if pause := state.DirectivePause; pause != nil {
		waiting := "an unresolved directive (" + pause.Kind + ")"
		if unresolved := strings.TrimSpace(pause.Unresolved); unresolved != "" {
			waiting += ": " + unresolved
		}
		return waiting
	}
	if state.OperatorHeldSince != nil {
		return runstate.DescribePause(runstate.PauseOperatorHold, "")
	}
	waiting := runstate.DescribePause(state.PauseCause, state.UsageLimitKind)
	if state.UsageLimitResetsAt != nil {
		waiting += ", until " + state.UsageLimitResetsAt.UTC().Format(time.RFC3339)
	}
	return waiting
}

// blocked reports a run that stopped and stayed stopped. It is read from a
// terminal record carrying a failure rather than from the tracker, for the same
// reason everything else here is: what is said is what the record says.
func blocked(state runstate.State) bool {
	return state.Status.Terminal() && state.Status != runstate.StatusSucceeded && blockerText(state) != ""
}

// blockerText is what the record gives as the reason. A publication that could
// not be pushed is an outstanding publication rather than a failed run, so it is
// only read where nothing else said why the run stopped.
func blockerText(state runstate.State) string {
	if failure := strings.TrimSpace(state.Failure); failure != "" {
		return failure
	}
	return strings.TrimSpace(state.PublishFailure)
}

func mergeQueued(state runstate.State) bool {
	return state.PullRequest != nil && state.PullRequest.MergeQueued
}

func merged(state runstate.State) bool {
	return state.PullRequest != nil && state.PullRequest.Merged
}

// describePullRequest names a published request the way somebody would quote
// one: its number where the record has one, and its URL, which is what a reader
// actually follows.
func describePullRequest(published *runstate.PullRequest) string {
	if published == nil {
		return ""
	}
	if published.Number > 0 && strings.TrimSpace(published.URL) != "" {
		return fmt.Sprintf("#%d (%s)", published.Number, published.URL)
	}
	if published.Number > 0 {
		return fmt.Sprintf("#%d", published.Number)
	}
	return strings.TrimSpace(published.URL)
}
