package readmodel

// Where the triage docket stands for the development manager: what is waiting
// on her decision, and what is waiting on the harness carrying a decision out.
//
// The docket itself cannot say either. An entry is written once, as the work
// stops, and stands forever; every decision about it is made afterwards and
// recorded somewhere else — on the item's durable triage record, on the re-run
// claimed against the entry, on the run whose blocker a repair cleared. A reader
// handed the docket alone sees three hundred entries and no way to tell the
// thirty that still need her from the ones settled weeks ago. That is what the
// development manager's hourly sweep was handed until yoyodyne-ifd.353, and
// nothing at all on the turns after the first: her conversation carries the
// docket only in the picture it opened with, so a sweep on a resumed conversation
// read a docket gathered days before and reported calm over thirty undecided
// stoppages.
//
// This is the one derivation both her wakeup and any surface read, for the
// reason every derivation here is shared: a sweep that counted the undecided
// stoppages one way and a status line that counted them another would be a
// disagreement only the operator could adjudicate.
//
// # What "undecided" means
//
// An entry is undecided when it names a run whose stoppage still stands, on work
// still admitted, and no decision is standing about that run. A decision is
// standing when the item's triage record carries one naming the run and the run
// has not stopped again since — a run repaired under a grant and stopped a second
// time is a new stoppage the old decision said nothing about. Any decision
// counts, a wait or an escalation included: those spend nothing and leave no
// counter, and this reads the decision itself rather than the counters, which is
// what lets an entry she told to wait stop being re-offered to her.
//
// An entry is awaiting carry-out when a decision stands about it that buys
// another attempt and the harness has not yet taken it — the same rule the
// backlog's holds read, asked of the same record. Everything else is settled:
// decided and acted on, over because the run went on and landed, or on work
// nobody has admitted any more.
//
// An entry that names no run cannot be decided through the triage record at all,
// because a decision names a run. Those are counted rather than listed, so what
// she is told is that they exist and where to read them.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// DocketEntries is the triage docket as it now stands, built and joined to the
// triage record the way the development manager's conversation reads it. A build
// that could only be completed in part returns the entries it has beside the
// error, and this reading carries both.
//
// It is satisfied by a caller wrapping orchestrator.Docketer.Build.
type DocketEntries interface {
	Docket() ([]triage.Entry, error)
}

// AdmittedWork is the tracker slice the admitted work is read from. It is what
// separates a stoppage on open work from one on an item closed weeks ago, which
// the docket alone cannot do: the docket is append-only and the item's closure
// is recorded nowhere near it.
//
// It is satisfied by the same tracker Standing reads.
type AdmittedWork interface {
	List(ctx context.Context, status string) ([]beads.WorkItem, error)
}

// DocketSources are the records one reading of the docket is assembled from.
type DocketSources struct {
	// Docket is the docket as it now stands. Required.
	Docket DocketEntries
	// Stoppages is every run the harness has recorded, which is what says whether
	// an entry's stoppage still stands, and every delivery it has made of one to
	// the development manager. Required.
	Stoppages Stoppages
	// Decisions is what triage has recorded about each item. Required: a reading
	// without it could not tell a decided stoppage from an undecided one, which is
	// the whole of the question.
	Decisions Decisions
	// Tracker is the admitted work. Optional; a reading without one cannot tell a
	// stoppage on closed work from one on open work, and says so rather than
	// listing every stoppage the docket ever recorded as though it were waiting.
	Tracker        AdmittedWork
	TrackerTimeout time.Duration
	Now            func() time.Time
}

// DocketWait is one entry that is still waiting on somebody, in the words a line
// of a wakeup carries: which run, which item, what stopped it, and what has
// already been done about putting it to her.
type DocketWait struct {
	Key           string       `json:"key"`
	Class         triage.Class `json:"class"`
	RunID         string       `json:"run_id"`
	WorkItemID    string       `json:"work_item_id"`
	WorkItemTitle string       `json:"work_item_title,omitempty"`
	RecordedAt    time.Time    `json:"recorded_at"`
	// Stopped is what stopped the work, folded to one line: the blocker, the
	// failure of a death that recorded none, the escalating role's reason, or the
	// forge's account of the publication.
	Stopped string `json:"stopped,omitempty"`
	// Delivery is what the harness's own delivery of this entry came to — put to
	// her and unanswered, never reached her, or given up on — so a re-offer says it
	// is one. Empty where the harness never tried, which is every class the
	// event-driven delivery does not cover.
	Delivery string `json:"delivery,omitempty"`
	// Decision is the decision standing about the run, on an entry awaiting
	// carry-out, with who recorded it and when.
	Decision  string    `json:"decision,omitempty"`
	DecidedBy string    `json:"decided_by,omitempty"`
	DecidedAt time.Time `json:"decided_at,omitempty"`
}

// DocketStanding is where the docket stands: the entries waiting on the
// development manager, the entries waiting on the harness, and what this
// reading could not read.
type DocketStanding struct {
	ReadAt time.Time `json:"read_at"`
	// Undecided are the stoppages nobody has decided about, oldest first, because
	// the oldest is the one that has waited longest and is the one a bounded
	// listing must not cut.
	Undecided []DocketWait `json:"undecided"`
	// Uncarried are the stoppages a decision stands about that the harness has not
	// acted on. They are hers to know about rather than to decide: a decision
	// recorded twice is two decisions.
	Uncarried []DocketWait `json:"uncarried"`
	// Settled counts the entries that need nothing from anybody: decided and acted
	// on, over, or on work no longer admitted.
	Settled int `json:"settled"`
	// Runless counts the entries that name no run and so cannot be decided through
	// the triage record: an item the tree is not ready for, an attempt that never
	// became a run.
	Runless int `json:"runless"`
	// Unchecked counts the entries this reading could not place, because the
	// admitted work or a run's record could not be read. They are neither listed
	// nor counted as settled: what a reader is told is that they exist and were
	// not read, so calm is never reported over them.
	Unchecked int `json:"unchecked,omitempty"`
	// Problems is what could not be read. A reading with problems is still a
	// reading — what it found is real — and what it says about the rest is that it
	// could not see it.
	Problems []string `json:"problems,omitempty"`
}

// The bounds a rendering holds itself to. A wakeup is a prompt, and a docket
// grows with everything that ever stopped; what the bound cuts is the newest
// entry rather than the oldest, and how many it cut is stated.
const (
	maxDocketWaitsRendered = 100
	maxDocketWaitBytes     = 240
)

// ReadDocket assembles where the docket stands. It never fails as a whole: a
// source that cannot be read costs the entries that depended on it, which are
// counted as unchecked and said so, and the rest of the reading stands.
func ReadDocket(ctx context.Context, sources DocketSources) DocketStanding {
	standing := DocketStanding{ReadAt: sources.now(), Undecided: []DocketWait{}, Uncarried: []DocketWait{}}
	if sources.Docket == nil || sources.Stoppages == nil || sources.Decisions == nil {
		standing.Problems = append(standing.Problems, "nothing was wired to read the docket, the runs, or what triage has decided, so where the docket stands cannot be said")
		return standing
	}
	entries, err := sources.Docket.Docket()
	if err != nil {
		standing.Problems = append(standing.Problems, fmt.Sprintf("the triage docket could not be read in full: %v", err))
	}
	runs, err := sources.Stoppages.Recorded()
	if err != nil {
		standing.Problems = append(standing.Problems, fmt.Sprintf("the recorded runs could not be read, so no entry could be placed: %v", err))
		standing.Unchecked = len(entries)
		return standing
	}
	byRun := make(map[string]runstate.State, len(runs))
	for _, run := range runs {
		byRun[run.RunID] = run
	}
	deliveries := make(map[string]runstate.Escalation)
	if escalated, err := sources.Stoppages.Escalated(); err != nil {
		standing.Problems = append(standing.Problems, fmt.Sprintf("what the harness has already put to the development manager could not be read: %v", err))
	} else {
		for _, escalation := range escalated {
			deliveries[escalation.DocketKey] = escalation
		}
	}
	admitted, admittedProblem := sources.admitted(ctx)
	if admittedProblem != "" {
		standing.Problems = append(standing.Problems, admittedProblem)
	}
	decided := standingDecisions(sources.Decisions)
	counters := make(map[string]runstate.TriageCounters)
	for _, entry := range entries {
		if strings.TrimSpace(entry.RunID) == "" {
			standing.Runless++
			continue
		}
		if admitted == nil {
			// Without the admitted work, a stoppage on closed work and one on open
			// work read alike, and listing every stoppage the docket ever recorded
			// would bury the ones that are waiting under the ones that are not.
			standing.Unchecked++
			continue
		}
		item, open := admitted[entry.WorkItemID]
		if !open {
			standing.Settled++
			continue
		}
		run, recorded := byRun[entry.RunID]
		if !recorded {
			standing.Problems = append(standing.Problems, fmt.Sprintf("the record of run %s, which the docket entry for %s names, could not be found", entry.RunID, entry.WorkItemID))
			standing.Unchecked++
			continue
		}
		if !stoppageStands(entry, run, runs) {
			standing.Settled++
			continue
		}
		if entry.Rerun != nil {
			// A re-run claimed against this entry is her decision acted on, and the
			// stopped run stays exactly as it was: nothing on it will ever say so.
			standing.Settled++
			continue
		}
		record, seen := counters[entry.WorkItemID]
		if !seen {
			opened, err := sources.Decisions.Counters(entry.WorkItemID)
			if err != nil {
				standing.Problems = append(standing.Problems, fmt.Sprintf("what triage has decided about %s could not be read: %v", entry.WorkItemID, err))
				standing.Unchecked++
				continue
			}
			record, counters[entry.WorkItemID] = opened, opened
		}
		wait := docketWait(entry, item, run, deliveries[entry.Key])
		decision, standingDecision := record.DecisionOf(entry.RunID)
		switch {
		case !standingDecision || overtaken(decision, run):
			standing.Undecided = append(standing.Undecided, wait)
		default:
			carryOut, _ := decided(entry.WorkItemID, entry.RunID)
			if carryOut {
				wait.Decision = decision.Decision
				wait.DecidedBy = decision.DecidedBy
				wait.DecidedAt = decision.DecidedAt
				standing.Uncarried = append(standing.Uncarried, wait)
			} else {
				standing.Settled++
			}
		}
	}
	sort.SliceStable(standing.Undecided, func(first, second int) bool {
		return standing.Undecided[first].RecordedAt.Before(standing.Undecided[second].RecordedAt)
	})
	sort.SliceStable(standing.Uncarried, func(first, second int) bool {
		return standing.Uncarried[first].DecidedAt.Before(standing.Uncarried[second].DecidedAt)
	})
	return standing
}

// admitted is the admitted work keyed by identifier, read from the same tracker
// slices the scheduler pulls from. A tracker nothing was wired for and a tracker
// that would not answer are both a reading that cannot place any entry, and both
// say so.
func (s DocketSources) admitted(ctx context.Context) (map[string]beads.WorkItem, string) {
	if s.Tracker == nil {
		return nil, "nothing was wired to read the admitted work, so no docket entry could be checked against it"
	}
	admitted := make(map[string]beads.WorkItem)
	for _, status := range backlogStatuses {
		trackerCtx, cancel := s.bounded(ctx)
		items, err := s.Tracker.List(trackerCtx, status)
		cancel()
		if err != nil {
			return nil, fmt.Sprintf("the admitted work could not be read, so no docket entry could be checked against it: list %s work items: %v", status, err)
		}
		for _, item := range items {
			admitted[item.ID] = item
		}
	}
	return admitted, ""
}

func (s DocketSources) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.TrackerTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, s.TrackerTimeout)
}

func (s DocketSources) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// stoppageStands reports an entry's stoppage still being one, asked of the run's
// own record rather than of the entry: the entry says what was true when it was
// written, and a repair, a re-dispatch, or a merge since then is written on the
// run.
//
// Each class stands on its own fact. A stopped run stands while it is terminal,
// carries the blocker or the failure it stopped on, and its change is still
// there — the same three the backlog's hold asks, so the queue and the docket
// agree about which stoppages are still work. A death before the claim stands
// until the item has a later run, because the later run is what a re-dispatch
// is. An escalation stands while the run does. A publication stands while the
// harness recorded it as unfinished or the forge has not merged it.
func stoppageStands(entry triage.Entry, run runstate.State, runs []runstate.State) bool {
	if !run.Status.Terminal() {
		return false
	}
	switch entry.Class {
	case triage.ClassStoppedRun:
		return (strings.TrimSpace(run.Blocker) != "" || strings.TrimSpace(run.Failure) != "") && run.Artifacts().Preserved()
	case triage.ClassUnstartedRun:
		for _, later := range runs {
			if later.WorkItemID == run.WorkItemID && later.RunID != run.RunID && later.StartedAt.After(run.StartedAt) {
				return false
			}
		}
		return true
	case triage.ClassEscalation:
		return run.Escalated()
	case triage.ClassPublication:
		if run.PullRequest == nil {
			return strings.TrimSpace(run.PublishFailure) != ""
		}
		return strings.TrimSpace(run.PublishFailure) != "" || !run.PullRequest.Merged
	default:
		return true
	}
}

// overtaken reports a decision the run has stopped again since. A decision names
// a run, and a run continued under a grant and stopped a second time is the same
// run and a new stoppage: the old decision was carried out, and what the run is
// waiting on now is another.
func overtaken(decision runstate.TriageDecision, run runstate.State) bool {
	return run.CompletedAt != nil && run.CompletedAt.After(decision.DecidedAt)
}

// docketWait is one entry as a wakeup line carries it.
func docketWait(entry triage.Entry, item beads.WorkItem, run runstate.State, delivery runstate.Escalation) DocketWait {
	title := strings.TrimSpace(entry.WorkItemTitle)
	if title == "" {
		title = strings.TrimSpace(item.Title)
	}
	return DocketWait{
		Key:           entry.Key,
		Class:         entry.Class,
		RunID:         entry.RunID,
		WorkItemID:    entry.WorkItemID,
		WorkItemTitle: title,
		RecordedAt:    entry.RecordedAt,
		Stopped:       stoppedBy(entry, run),
		Delivery:      describeDelivery(delivery),
	}
}

// stoppedBy is what stopped the work, in the record's own words. The run is
// asked before the entry for the blocker, because a run that stopped again
// carries the blocker it stopped on now rather than the one the entry recorded.
func stoppedBy(entry triage.Entry, run runstate.State) string {
	switch {
	case entry.Escalation != nil:
		return fmt.Sprintf("the %s judged the item cannot be met as it stands: %s", entry.Escalation.RaisedBy.Title(), entry.Escalation.Reason)
	case entry.Class == triage.ClassPublication && entry.Publication != nil:
		if message := strings.TrimSpace(entry.Publication.Message); message != "" {
			return fmt.Sprintf("pull request #%d: %s", entry.Publication.Number, message)
		}
		return fmt.Sprintf("pull request #%d is approved and the forge has not merged it", entry.Publication.Number)
	case strings.TrimSpace(run.Blocker) != "":
		return run.Blocker
	case strings.TrimSpace(entry.Blocker) != "":
		return entry.Blocker
	case strings.TrimSpace(run.Failure) != "":
		return "died holding its change: " + run.Failure
	case strings.TrimSpace(entry.Failure) != "":
		return "died holding its change: " + entry.Failure
	default:
		return ""
	}
}

// describeDelivery says what the harness's own delivery of an entry came to, so
// a line re-offering it says it is a re-offer. It is silent on an entry the
// harness never tried to deliver, which is every class the event-driven delivery
// leaves on the docket.
func describeDelivery(delivery runstate.Escalation) string {
	switch {
	case delivery.DocketKey == "":
		return ""
	case delivery.Delivered():
		return fmt.Sprintf("put to you at %s and no decision was recorded", delivery.DeliveredAt.UTC().Format(time.RFC3339))
	case delivery.Attempts >= runstate.MaxEscalationAttempts:
		return fmt.Sprintf("could not be put to you in %d attempt(s), so the harness stopped trying", delivery.Attempts)
	default:
		return "has not reached you through the event-driven delivery yet"
	}
}

// Waiting reports anything on the docket still waiting on somebody, which is
// what a sweep that reports calm has to be able to deny.
func (s DocketStanding) Waiting() bool {
	return len(s.Undecided) > 0 || len(s.Uncarried) > 0 || s.Unchecked > 0
}

// Render is the docket as the development manager's wakeup carries it: the
// counts first, because they are the whole of what "the queue is quiet" has to
// be checked against, then one line per entry, the undecided ones under what to
// do about them and the decided ones under what not to.
func (s DocketStanding) Render() string {
	var rendered strings.Builder
	rendered.WriteString("# Triage docket: where it stands\n\n")
	rendered.WriteString("Read at " + s.ReadAt.UTC().Format(time.RFC3339) + " from the docket, the run records, and the triage record, so it is current where the docket in your opening context is not. ")
	fmt.Fprintf(&rendered, "%s no decision standing; %s recorded and not yet carried out by the harness",
		agreed(len(s.Undecided), "stoppage has", "stoppages have"), agreed(len(s.Uncarried), "decision is", "decisions are"))
	if s.Unchecked > 0 {
		fmt.Fprintf(&rendered, "; %s could not be placed and must be treated as unread rather than as settled", agreed(s.Unchecked, "entry", "entries"))
	}
	rendered.WriteString(".\n")
	for _, problem := range s.Problems {
		rendered.WriteString("Could not read: " + singleLine(problem, 512) + "\n")
	}
	if s.Runless > 0 {
		fmt.Fprintf(&rendered, "%s no run — an item the tree is not ready for, an attempt that never became a run — and cannot be decided through triage; read them on the docket in your opening context.\n",
			agreed(s.Runless, "further entry names", "further entries name"))
	}
	if len(s.Undecided) == 0 && len(s.Uncarried) == 0 {
		if s.Unchecked == 0 && len(s.Problems) == 0 {
			rendered.WriteString("Nothing on the docket is waiting on your decision, and no recorded decision is waiting to be carried out.\n")
		}
		return rendered.String()
	}
	if len(s.Undecided) > 0 {
		rendered.WriteString("\n## Waiting on your decision\n\n")
		rendered.WriteString("Each of these is re-offered on every pass until a decision naming its run is recorded. Decide each with a triage action naming the run — repair, rerun, rescope, rearm, wait, or escalate — or say what you are waiting on; a wait is a decision and stops the re-offer. Oldest first.\n\n")
		rendered.WriteString(renderDocketWaits(s.Undecided, "undecided"))
	}
	if len(s.Uncarried) > 0 {
		rendered.WriteString("\n## Decided, and not yet carried out\n\n")
		rendered.WriteString("Nothing here is yours to decide: a decision is recorded and the harness has still to act on it. Deciding one again spends a second decision rather than repeating the first. Oldest decision first.\n\n")
		rendered.WriteString(renderDocketWaits(s.Uncarried, "decided"))
	}
	return rendered.String()
}

func renderDocketWaits(waits []DocketWait, noun string) string {
	var rendered strings.Builder
	listed := 0
	for _, wait := range waits {
		if listed >= maxDocketWaitsRendered {
			break
		}
		rendered.WriteString(wait.line())
		listed++
	}
	if listed < len(waits) {
		fmt.Fprintf(&rendered, "%d further %s entry(s) are not listed here; treat them as waiting rather than as absent.\n", len(waits)-listed, noun)
	}
	return rendered.String()
}

// line is one entry as a wakeup line.
func (w DocketWait) line() string {
	var line strings.Builder
	fmt.Fprintf(&line, "- run %s on %s", w.RunID, w.WorkItemID)
	if w.WorkItemTitle != "" {
		line.WriteString(" — " + singleLine(w.WorkItemTitle, 120))
	}
	fmt.Fprintf(&line, " [%s, docketed %s]", w.Class.Title(), w.RecordedAt.UTC().Format("2006-01-02"))
	if w.Decision != "" {
		fmt.Fprintf(&line, ": %q recorded by the %s at %s", w.Decision, w.DecidedBy, w.DecidedAt.UTC().Format(time.RFC3339))
	} else if w.Delivery != "" {
		line.WriteString(": " + w.Delivery)
	}
	if w.Stopped != "" {
		line.WriteString(". Stopped by: " + singleLine(w.Stopped, maxDocketWaitBytes))
	}
	line.WriteString("\n")
	return line.String()
}

// agreed says a number with the form of the phrase that agrees with it.
func agreed(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
