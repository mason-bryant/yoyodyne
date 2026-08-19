package slack

// What the sink reads, and where the reading stops.
//
// The sink posts what a feed gives it and knows nothing else about where a
// message came from. That seam is the one the notifier fills: the reportable
// event selection and the per-role voice belong to the notifier rather than to
// the transport, and a sink written against the durable records directly would
// have to be rewritten the day a second producer — a conversation, a branch
// review, an ask exchange — had something to say.
//
// The feed below is the harness's own, and it is deliberately plain: it reads
// the milestones a run's durable state already records and the reports agents
// already filed, and renders them as facts rather than in any persona's voice.
// It is what makes one-way reporting work end to end today. The voice templates,
// the per-persona display identities, and the wider event selection are the
// notifier's, and this feed is replaced by it rather than extended here.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Delivery is one envelope together with what advances when it is posted. A
// stream that is a log advances to Position; one that reports a fixed set of
// milestones records Mark. Exactly one of them is set, and the sink writes
// whichever it was given before it posts anything else.
type Delivery struct {
	Stream   string
	Mark     string
	Position uint64
	Envelope notify.Envelope
}

// Batch is one pass over the durable records: what is ready to post, and which
// streams still exist so cursors for the rest can be dropped.
type Batch struct {
	Deliveries []Delivery
	Streams    map[string]struct{}
}

// Feed is where the sink's messages come from. It is polled rather than
// subscribed to because the records it reads are files written by other
// processes, and because a sink that has been away has to catch up from its
// cursors either way.
type Feed interface {
	Poll(ctx context.Context, cursors Cursors) (Batch, error)
}

// reportStream is the name the collected report pile advances under. Runs take
// a stream each, named for the run, because they are separate subjects rather
// than one log.
const reportStream = "reports"

func runStream(runID string) string { return "run:" + runID }

// HarnessFeed reads the product's own durable records: what became of its runs,
// and what its agents reported while their work carried on.
type HarnessFeed struct {
	Runs    *runstate.Store
	Reports *runstate.ReportStore
	// Since is when this sink started. What was already over before then is
	// history rather than news, and a channel opened today does not want a
	// month of it: a product with two hundred recorded runs would get two
	// hundred threads before it said anything about today's work. Only what
	// nothing has been said about yet is filtered this way, so a run in flight
	// is still caught up on in full, and a message an outage delayed is still
	// posted when the workspace returns. The zero value reports everything,
	// which is what a caller that wants the whole record asks for.
	Since time.Time
}

// Poll reads both streams and reports what the cursors say has not been posted.
func (f *HarnessFeed) Poll(_ context.Context, cursors Cursors) (Batch, error) {
	batch := Batch{Streams: map[string]struct{}{reportStream: {}}}

	states, err := f.Runs.Recorded()
	if err != nil {
		return Batch{}, fmt.Errorf("read the recorded runs: %w", err)
	}
	for _, state := range states {
		stream := runStream(state.RunID)
		batch.Streams[stream] = struct{}{}
		cursor := cursors.Streams[stream]
		if len(cursor.Delivered) == 0 && state.CompletedAt != nil && f.predates(*state.CompletedAt) {
			continue
		}
		batch.Deliveries = append(batch.Deliveries, runMilestones(state, cursor)...)
	}

	reports, err := f.Reports.List()
	if err != nil {
		return Batch{}, fmt.Errorf("read the collected reports: %w", err)
	}
	cursor := cursors.Streams[reportStream]
	for index := int(cursor.Position); index < len(reports); index++ {
		if f.predates(reports[index].RecordedAt) {
			continue
		}
		batch.Deliveries = append(batch.Deliveries, reportDelivery(reports[index], uint64(index+1)))
	}
	return batch, nil
}

// predates reports what was already recorded before this sink started.
func (f *HarnessFeed) predates(at time.Time) bool {
	return !f.Since.IsZero() && at.Before(f.Since)
}

// reportDelivery posts an agent's report as the agent wrote it. Nothing is
// rendered, summarized, or re-voiced: it is text an agent already said, at the
// severity the agent gave it, and the harness's part is to carry it.
func reportDelivery(filed report.Report, position uint64) Delivery {
	topic := notify.ProductTopic
	if item := strings.TrimSpace(filed.WorkItemID); item != "" {
		topic = notify.WorkItemTopic(item)
	}
	return Delivery{
		Stream:   reportStream,
		Position: position,
		Envelope: notify.New(
			notify.KindReport,
			topic,
			notify.Speaker{Role: filed.Role, Agent: filed.Agent},
			filed.Severity,
			filed.Message,
			notify.Refs{Run: filed.RunID, WorkItem: filed.WorkItemID},
		),
	}
}

// runMilestones reports what one run's state says that has not been said yet.
//
// Each milestone is named once and posted once, which is what keeps a thread a
// narrative rather than an event log scrolling sideways. The ones that can
// legitimately recur — a check that fails again after a repair attempt, a run
// that parks a second time — carry the attempt or the cause in their name, so
// saying a thing twice means it happened twice.
func runMilestones(state runstate.State, cursor Cursor) []Delivery {
	topic := notify.WorkItemTopic(state.WorkItemID)
	refs := notify.Refs{Run: state.RunID, WorkItem: state.WorkItemID}
	if state.PullRequest != nil {
		refs.PullRequest = state.PullRequest.URL
	}

	var deliveries []Delivery
	post := func(mark string, kind notify.Kind, severity report.Severity, body string) {
		if cursor.Has(mark) {
			return
		}
		deliveries = append(deliveries, Delivery{
			Stream:   runStream(state.RunID),
			Mark:     mark,
			Envelope: notify.New(kind, topic, notify.Harness, severity, body, refs),
		})
	}

	post("started", notify.KindRunStarted, report.SeverityNote, describeStart(state))

	attempt := strconv.Itoa(state.RepairAttempts)
	if state.CheckFailure != nil {
		post("checks.failed#"+attempt, notify.KindChecksFailed, report.SeverityWarning,
			fmt.Sprintf("The checks failed on %s: `%s` exited %d.", state.WorkItemID, singleLine(state.CheckFailure.Command), state.CheckFailure.ExitCode))
	} else if checksPassed(state) {
		post("checks.passed#"+attempt, notify.KindChecksPassed, report.SeverityNote,
			fmt.Sprintf("The checks passed on %s.", state.WorkItemID))
	}

	if decision := strings.TrimSpace(state.ReviewDecision); decision != "" {
		severity := report.SeverityNote
		if decision != runstate.ReviewApprove {
			severity = report.SeverityWarning
		}
		post("review:"+decision+"#"+attempt, notify.KindReviewVerdict, severity, describeVerdict(state))
	}

	if state.Integration != nil {
		post("promotion", notify.KindPromotion, report.SeverityNote,
			fmt.Sprintf("%s was promoted onto %s.", state.WorkItemID, integrationTarget(state)))
	}
	if state.PullRequest != nil {
		post("publication", notify.KindPublication, report.SeverityNote,
			fmt.Sprintf("%s was published as %s.", state.WorkItemID, state.PullRequest.URL))
		if state.PullRequest.MergeQueued {
			post("merge.queued", notify.KindMergeQueued, report.SeverityNote,
				fmt.Sprintf("The forge queued the merge of %s; it lands once the base branch's requirements are met.", state.PullRequest.URL))
		}
		if state.PullRequest.Merged {
			post("merge", notify.KindMerged, report.SeverityNote,
				fmt.Sprintf("%s was merged.", state.PullRequest.URL))
		}
	}

	postParks(state, cursor, post)

	if failure := strings.TrimSpace(state.Failure); failure != "" {
		post("blocker", notify.KindBlocker, report.SeverityCritical,
			fmt.Sprintf("%s stopped in the %s phase: %s", state.WorkItemID, phaseOrUnknown(state), singleLine(failure)))
	} else if state.Status == runstate.StatusSucceeded {
		post("finished", notify.KindRunFinished, report.SeverityNote,
			fmt.Sprintf("%s finished.", state.WorkItemID))
	}
	return deliveries
}

// postParks says that a run stopped waiting and that it started again. Both
// halves are said, because a park nobody saw lift reads as a run that died
// quietly — and the lift is only knowable by having said the park, which is why
// it is read out of the cursor rather than out of the state.
func postParks(state runstate.State, cursor Cursor, post func(string, notify.Kind, report.Severity, string)) {
	cause, waiting := parkCause(state)
	if waiting {
		post("parked:"+cause, notify.KindRunParked, report.SeverityWarning,
			fmt.Sprintf("%s is waiting on %s.", state.WorkItemID, describeParkCause(state, cause)))
		return
	}
	for _, delivered := range cursor.Delivered {
		parked, found := strings.CutPrefix(delivered, "parked:")
		if !found || cursor.Has("continued:"+parked) {
			continue
		}
		post("continued:"+parked, notify.KindRunContinued, report.SeverityNote,
			fmt.Sprintf("%s is running again.", state.WorkItemID))
	}
}

// parkCause names what a run is waiting on, if it is waiting. The order is the
// order a reader would want them in: the operator's own decision first, because
// a run held by the operator is not a run the provider refused.
func parkCause(state runstate.State) (string, bool) {
	switch {
	case state.OperatorHeldSince != nil:
		return runstate.PauseOperatorHold, true
	case state.DirectivePause != nil:
		return "directive", true
	case state.UsageLimitResetsAt != nil:
		cause := strings.TrimSpace(state.PauseCause)
		if cause == "" {
			cause = runstate.PauseUsageLimit
		}
		return cause, true
	default:
		return "", false
	}
}

func describeParkCause(state runstate.State, cause string) string {
	if cause == "directive" {
		return "an unresolved user directive"
	}
	return runstate.DescribePause(cause, state.UsageLimitKind)
}

// checksPassed reports a run that got past the deterministic gate. It is
// derived rather than recorded: the phases after checking are only reached by
// passing it, and a failing check is recorded on the state while it holds.
func checksPassed(state runstate.State) bool {
	switch state.Phase {
	case runstate.PhaseReviewing, runstate.PhaseIntegrating, runstate.PhaseCompleting,
		runstate.PhaseCleaningUp, runstate.PhaseComplete:
		return true
	default:
		return false
	}
}

// describeStart carries the recorded selection reason onto the message. The
// invariant that makes the reason durable exists so an operator can see why the
// harness chose what it chose; a run announced without it would be the half of
// that guarantee nobody can act on. A run with no recorded selection says so,
// because an unaccounted run is exactly what this is here to make visible.
func describeStart(state runstate.State) string {
	line := fmt.Sprintf("%s started, as run %s.", state.WorkItemID, state.RunID)
	if state.Selection == nil || !state.Selection.Stated() {
		return line + "\nNo reason for the selection was recorded."
	}
	return line + fmt.Sprintf("\nSelected by the %s: %s", state.Selection.By, singleLine(state.Selection.Reason))
}

func describeVerdict(state runstate.State) string {
	line := fmt.Sprintf("The reviewer decided %s on %s", state.ReviewDecision, state.WorkItemID)
	if state.ReviewFindings > 0 {
		line += fmt.Sprintf(", with %d finding(s)", state.ReviewFindings)
	}
	line += "."
	if summary := strings.TrimSpace(state.ReviewSummary); summary != "" {
		line += "\n" + summary
	}
	return line
}

func integrationTarget(state runstate.State) string {
	if branch := strings.TrimSpace(state.TargetBranch); branch != "" {
		return branch
	}
	return "its target branch"
}

func phaseOrUnknown(state runstate.State) string {
	if phase := strings.TrimSpace(string(state.Phase)); phase != "" {
		return phase
	}
	return "unrecorded"
}

// singleLine folds a recorded reason onto one line, so a verdict or a failure
// stays a sentence in a thread rather than a page of it. What it cuts is
// available in full in the record the message names.
func singleLine(text string) string {
	folded := strings.Join(strings.Fields(text), " ")
	const limit = 400
	if len(folded) <= limit {
		return folded
	}
	return folded[:limit] + "…"
}
