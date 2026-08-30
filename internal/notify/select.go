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
	// The record carries what the item is called, so the thread this run opens can
	// be named in words rather than in the identifier alone. A run recorded before
	// titles were written carries none, and its topic is addressed exactly as it
	// was before there was one.
	topic = topic.WithTitle(after.WorkItemTitle)
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
		say(KindRunStarted, report.SeverityNote, selectionSpeaker(after.Selection), startedDetail(after))
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
		say(KindRunParked, parkSeverity(after), Harness(), Detail{Cause: causeOf(after)})
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

// FromUsageLimit says that a provider refused the harness for want of capacity
// somewhere that is not a run. A run says the same thing by parking, and this is
// how every other process says it: the conversation turn or the review that was
// stopped, what the limit was, and when the provider said it lifts.
//
// It is a warning for the reason a park by the same cause is. Nobody chose it,
// nothing else in the record says it happened, and what follows it is hours of
// silence that look exactly like a healthy quiet queue. The speaker is the
// harness, because a provider running out of capacity is not any persona's act
// and no role should be made to narrate one.
func FromUsageLimit(exhaustion runstate.UsageLimitExhaustion) (Notification, error) {
	topic, err := topicForItem(exhaustion.WorkItemID)
	if err != nil {
		return Notification{}, fmt.Errorf("address usage limit refusal at %s: %w", exhaustion.At.UTC().Format(time.RFC3339), err)
	}
	return Notification{
		Topic:   topic,
		Speaker: Harness(),
		Event: Event{
			Kind:     KindUsageLimitExhausted,
			At:       exhaustion.At,
			Severity: report.SeverityWarning,
			Refs: Refs{
				WorkItemID:     exhaustion.WorkItemID,
				ConversationID: exhaustion.ConversationID,
			},
			Detail: Detail{
				Waiting: exhaustion.Waiting,
				Cause:   exhaustion.Describe(),
			},
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

// FromWatch says what a watch session changed to. It is addressed to the
// product for the same reason the holds are — a session is about the whole line
// rather than any one item — and spoken by the harness, because choosing work is
// not a role's judgement and no persona should be made to narrate it.
//
// A state nothing has a line for is refused rather than posted as something
// nobody wrote words for, which is the same refusal an unrecognized kind gets:
// a log written by a newer harness than the sink is one the sink reads past
// rather than mistranslates.
func FromWatch(transition runstate.WatchTransition) (Notification, error) {
	kind, ok := watchKinds[transition.State]
	if !ok {
		return Notification{}, fmt.Errorf("address watch session %s: %q is not a state anything says", transition.SessionID, transition.State)
	}
	// A braked session is the one an operator has to do something about: the
	// line has stopped and it stays stopped until intake is released.
	severity := report.SeverityNote
	if kind == KindWatchBraked {
		severity = report.SeverityWarning
	}
	notification := productNotification(kind, transition.At, Detail{Reason: transition.Reason})
	notification.Event.Severity = severity
	return notification, nil
}

// watchKinds is what each recorded state is said as. It is a table rather than a
// switch because the states and the kinds are two vocabularies that have to
// agree, and a table is where a disagreement is visible.
var watchKinds = map[runstate.WatchState]Kind{
	runstate.WatchWatching: KindWatchStarted,
	runstate.WatchIdle:     KindWatchIdle,
	runstate.WatchBraked:   KindWatchBraked,
	runstate.WatchResumed:  KindWatchResumed,
	runstate.WatchStopped:  KindWatchStopped,
}

// Line is a product's line with nothing being chosen from it: what stopped the
// choosing, when it became that way, and how much admitted work the tracker
// reports as ready behind it.
//
// It is the one thing selection says that is not a crossing. Everything else
// here compares two readings of a record and reports the difference, which says
// a state once and is right to: a thread is a narrative. A line that is held or
// idle over ready work is not news that happened, it is a condition that
// persists, and the reader who needs it is the one who was told once at midnight
// and has heard nothing since. So the state is said again while it stands, and
// how often is the sink's to decide — this only says it.
type Line struct {
	// Stopped is what has stopped the choosing, in the words whoever read the
	// record would use: the operator's hold, a held intake and why it was held,
	// a session that found nothing it could start, no session at all.
	Stopped string
	// Since is when the line became that way, which is what makes the message
	// worth repeating: the state does not change and its age does.
	Since time.Time
	// Ready is how much admitted work the tracker itself calls ready. It is what
	// separates a line waiting on somebody from an honestly quiet one, so a line
	// with nothing ready is never said at all.
	Ready int
}

// FromLine says that nothing is being chosen while work is ready to be chosen.
// It is addressed to the product and spoken by the harness for the same reason
// the holds and the watch sessions are: a line is about every item rather than
// any one of them, and what stopped it is nobody's judgement to narrate.
//
// The moment is the reading rather than the state's own start, because that is
// what it is: an account of what was true when somebody looked, whose whole
// point is that it is being looked at again.
func FromLine(line Line, at time.Time) Notification {
	return productNotification(KindLineWaiting, at, Detail{
		Stopped: strings.TrimSpace(line.Stopped),
		Since:   line.Since,
		Ready:   line.Ready,
	})
}

// Resident is the binary a live watch session is running: the revision it was
// built from, and how many harness changes the repository has taken on since.
//
// It is the second thing here that is a state rather than a crossing, and it is
// the only one no durable record says on its own. A held line is at least
// derivable from the hold that stopped it; a session running a binary the
// harness has moved past leaves no trace anywhere — it goes on choosing work,
// and every run it starts looks exactly like a run started by a current one.
// What it actually produces is rounds spent against defects that were fixed on
// the main line hours before, which reads as agents failing rather than as a
// process nobody restarted, and on 2026-08-30 that reading cost three review
// rounds against a bug that had already been dead for a day.
type Resident struct {
	// Build is the revision the session's binary was built from, carried so a
	// reader can check the count rather than take it.
	Build string
	// Behind is how many harness changes have landed since. It is never said at
	// zero: a session running what is deployed is the ordinary state, and the
	// whole discipline here is that silence keeps meaning nothing to do.
	Behind int
}

// FromResident says that the session choosing work is running a build the
// harness has moved past. It is addressed to the product and spoken by the
// harness for the reason the line and the holds are: which binary a session is
// executing is about every item rather than any one of them, and it is nobody's
// judgement to narrate.
//
// The severity is the caller's rather than derived here, because how loud this
// is is a question about a threshold and a threshold is the reporting surface's
// to hold: the same two facts are a note worth reading beside the heartbeat and,
// far enough past that threshold, a degraded system somebody has to be told
// about directly.
func FromResident(resident Resident, severity report.Severity, at time.Time) Notification {
	notification := productNotification(KindResidentStale, at, Detail{
		Commit: strings.TrimSpace(resident.Build),
		Behind: resident.Behind,
	})
	notification.Event.Severity = severity
	return notification
}

// Accumulation is what one topic gathered while nothing was posting its events:
// how many there were, the first and last of them, and the most attention any
// one of them asked for.
//
// It is the one thing here that is not read from a record at all. Everything
// else compares two readings and reports the difference; this reports what a
// surface did with the difference when it was too large to say one message at a
// time, which is a fact about the reporting rather than about the work. It is
// still said in this package, because what a message says is decided here
// whatever ends up carrying it.
type Accumulation struct {
	// Topic is the thread the accumulation belongs to, which is what makes a
	// digest one message per thread rather than one message for a whole backlog.
	Topic Topic
	// Events is how many were collapsed into this one message.
	Events int
	// Since and At are the first and last of them. The span between them is what
	// tells a reader whether they are looking at an afternoon or a fortnight.
	Since time.Time
	At    time.Time
	// Severity is the most attention any of the collapsed events asked for, so a
	// digest standing for a warning is marked as one. A digest is not allowed to
	// be quieter than the loudest thing inside it.
	Severity report.Severity
}

// FromAccumulation says that a topic gathered more than a channel can carry, and
// where the whole of it is.
//
// It is spoken by the harness rather than by any of the personas whose events
// were collapsed, for the reason a thread's opening message is: deciding not to
// repeat four hundred messages is not anybody's account of the work, and a
// persona made to say it would be claiming a judgement it never made.
func FromAccumulation(gathered Accumulation) Notification {
	severity := gathered.Severity
	if !severity.Valid() {
		severity = report.SeverityNote
	}
	refs := Refs{}
	if gathered.Topic.Kind == TopicWorkItem {
		refs.WorkItemID = gathered.Topic.ID
	}
	return Notification{
		Topic:   gathered.Topic,
		Speaker: Harness(),
		Event: Event{
			Kind:     KindCatchUpDigest,
			At:       gathered.At,
			Severity: severity,
			Refs:     refs,
			Detail:   Detail{Accumulated: gathered.Events, Since: gathered.Since},
		},
	}
}

func FromOperatorHold(hold runstate.OperatorHold) Notification {
	return productNotification(KindHoldPlaced, hold.HeldAt, Detail{})
}

func HoldLifted(at time.Time) Notification {
	return productNotification(KindHoldLifted, at, Detail{})
}

// KindExchangeTurn and KindExchangeClosed are selected in conversation.go rather
// than here, because an ask exchange is something a conversation did and this
// file reads runs. That is where the prediction this file used to carry came
// out: the envelope, the addressing, and every persona's line for both kinds
// were written before the channel existed, and the channel arriving cost one
// selection function and nothing in the sink, the threading, or the envelope.

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

// selectionDetail carries the recorded account of why the harness is running
// this item. A run recorded before selections existed carries none, and the
// absence is stated rather than rendered as a blank — an unaccounted run is
// exactly what recording the reason exists to make visible.
func selectionDetail(selection *runstate.Selection) Detail {
	if selection == nil {
		return Detail{}
	}
	return Detail{SelectedBy: selection.By, SelectionReason: selection.Reason}
}

// startedDetail is what a run's opening message says about it: why it is
// running, and what it is running as. The account and the configuration are said
// once, where the thread opens, rather than on every crossing after it — they
// are fixed for the life of a run, and a fact repeated on every message is one
// nobody reads on the message where it changed.
func startedDetail(after runstate.State) Detail {
	detail := selectionDetail(after.Selection)
	detail.Account = after.AccountAlias
	detail.Configuration = after.ConfigRevision
	return detail
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
// a provider that refused it, the operator holding everything, a directive
// nobody has resolved, or work the item was made to wait on. All four keep the
// run's claim and its worktree, which is what makes a park different from a
// failure.
func parked(state runstate.State) bool {
	return state.UsageLimitResetsAt != nil || state.OperatorHeldSince != nil ||
		state.DirectivePause != nil || state.DependencyPause != nil
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
	if pause := state.DependencyPause; pause != nil {
		return "unfinished work this item depends on: " + pause.Summary()
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

// parkSeverity is how loudly a park is said, and it follows the same precedence
// causeOf names the cause by, so the weight of a message and the words in it can
// never describe two different pauses.
//
// An exhausted usage limit is a warning and the other causes are notes. That is
// not a judgement about which is worse: it is what an unattended reader can do
// about each. A directive, a dependency link, and an operator hold are waiting on
// a decision somebody already made — the person reading the channel placed the
// first and the last, and the middle one is a development manager's own link; an
// exhausted limit is hours in which nothing will happen for a reason nobody
// chose, and it must not weigh the same as checks passing. A transient overload
// lifts in seconds and stays a note for exactly that reason.
func parkSeverity(state runstate.State) report.Severity {
	if state.DirectivePause != nil || state.DependencyPause != nil || state.OperatorHeldSince != nil {
		return report.SeverityNote
	}
	if state.PauseCause == runstate.PauseServerOverload {
		return report.SeverityNote
	}
	return report.SeverityWarning
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
		return environmentallyRefused(state) + failure
	}
	return strings.TrimSpace(state.PublishFailure)
}

// environmentallyRefused says a round the environment refused before the words
// the run stopped on, where that is what happened. It goes first because it
// changes how the rest reads: a thread carrying only the failure reads as an
// item that has just spent another round toward its cap, and where the round was
// refused the opposite is true.
//
// What it says is the record's own sentence rather than a second reading of the
// same flags. The accounting has five states and one of them — a return the
// settle decided on and could not write — is the one a thread must not get
// wrong, so it is derived once, in the record, and phrased around here.
//
// It is silent on every ordinary stoppage, and on a run that recorded a cause
// and delivered a change anyway, because that round spent as any round does.
func environmentallyRefused(state runstate.State) string {
	refused := state.Environmental
	if refused == nil || !refused.Refused {
		return ""
	}
	return refused.Describe() + " — "
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
