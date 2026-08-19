package orchestrator

// The ready-work scheduler: what the harness starts when nobody names an item.
//
// Every run before this one was an operator typing an identifier, so choosing
// was not a thing the harness did and concurrency was not a thing it had. This
// is both at once, and the two are the same mechanism: the scheduler reads the
// backlog in the product manager's order, takes the items the tracker itself
// reports as pullable, and starts as many of them at once as the configured
// developer capacity leaves free.
//
// Almost nothing here enforces anything. Every constraint a scheduled run is
// held to is already enforced where it belongs — capacity and duplicate work at
// the reservation, the intake hold and the unresolved directives in the
// pipeline, integration order in the promotion lease, and a moved target branch
// in the promotion itself — and re-implementing any of them here would create a
// second account of the same rule that could disagree with the first. What this
// adds is the choosing: which items, in what order, how many at a time, and, for
// every one of them, the recorded reason it was chosen.
//
// # A pull re-reads the configuration
//
// The scheduler is the first thing in the harness that holds a configuration
// across time. Every command before it loaded the file, did one thing, and
// exited, so "when is a capacity change picked up?" had the answer "at the next
// command" without anybody deciding it. Here it is decided: the configuration is
// re-read at every pull, so a capacity raised or a priority reordered takes
// effect at the next selection. That keeps the answer the one the rest of the
// harness already gives, and it matches how the backlog is steered — a product
// manager reorders the queue and expects the next thing pulled to reflect it,
// not the next restart. Runs already in flight keep the configuration they
// started under, because a run's own parameters are fixed when it is reserved.
//
// # What it will not do
//
// It never withholds work because something upstream of it changed. Staleness is
// derived rather than stored and stops, closes, blocks, and reorders nothing, so
// an item whose goal was amended after it was admitted is pulled exactly as it
// would have been — and the fact is written into the run's recorded selection
// reason, where whoever reads what the harness chose can see it.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/staleness"
)

// scheduledStatuses are the tracker slices the backlog is assembled from: work
// that has been admitted and is not finished. Claimed work has been pulled
// already and closed work has left, so neither is still queued.
var scheduledStatuses = []string{"open", "blocked"}

// maxScheduleReasonBytes bounds one part of a recorded selection reason that
// came from a document rather than from this package. The reason as a whole has
// its own bound in the run state; this keeps a single amendment's prose from
// filling it.
const maxScheduleReasonBytes = 240

// Why the scheduler stopped pulling. Each is a different thing for an operator
// to do about it, which is why they are stated apart rather than folded into one
// "nothing more was started".
const (
	// ScheduleDrained reports a scheduler that ran out of work to pull: nothing
	// the tracker reports as ready is left that this pass has not already tried.
	ScheduleDrained = "nothing more is ready to pull"
	// ScheduleIntakeHeld reports the operator holding intake. Nothing further was
	// chosen; whatever was already running carried on to its own end.
	ScheduleIntakeHeld = "the operator is holding intake, so nothing more was chosen"
	// ScheduleLimitReached reports the requested number of runs having been
	// started, which is the operator bounding one pass rather than the harness
	// running out of anything.
	ScheduleLimitReached = "the requested number of runs was started"
	// ScheduleCapacityFull reports every developer slot occupied by runs this
	// scheduler does not own, so there was nothing to wait for and no room to
	// start anything.
	ScheduleCapacityFull = "every developer slot is held by a run this pass did not start"
	// ScheduleUnreadable reports a pull that could not be made at all. What
	// failed is on the schedule beside it; runs already started were waited out
	// rather than abandoned.
	ScheduleUnreadable = "the harness could not be read for another pull"
	// ScheduleCancelled reports a scheduler whose context ended. Runs already
	// started see the same cancellation and are waited out.
	ScheduleCancelled = "the scheduler was cancelled"
)

// ScheduleTracker is the tracker access one pull needs: the admitted work, so it
// can be put in the product manager's order, and the tracker's own account of
// what can be pulled now. Readiness is asked for rather than inferred, for the
// reason the backlog states — a listing carries dependencies without carrying
// whether they are finished, so only the tracker's dependency graph can answer
// it.
type ScheduleTracker interface {
	List(ctx context.Context, status string) ([]beads.WorkItem, error)
	Ready(ctx context.Context) ([]beads.WorkItem, error)
}

// ScheduleRuns is the durable run state one pull reads. It reads and never
// adopts: what is in flight is both what fills the configured capacity and what
// must not be started a second time, and answering either question is not acting
// on a run another process owns.
type ScheduleRuns interface {
	Incomplete() ([]runstate.State, error)
}

// ScheduleStaleness reports the admitted work something upstream of changed
// after it was admitted. It is optional and it decides nothing: a pull wired
// without one schedules exactly the same items in exactly the same order, and
// what is lost is a sentence in each run's recorded reason.
type ScheduleStaleness interface {
	Stale(ctx context.Context) ([]staleness.WorkItem, error)
}

// Starter runs one chosen item to its end. It is a function rather than the
// pipeline itself because a pull hands each run the configuration that pull
// read, and because what the scheduler needs from a run is only its outcome.
type Starter func(ctx context.Context, workItemID string, selection runstate.Selection) (Outcome, error)

// Pull is one reading of the harness: the configuration in force, the durable
// state built from it, and the way a chosen item is run. It is assembled fresh
// for every pull rather than once for the command, which is what makes a
// configuration edit take effect at the next selection.
type Pull struct {
	Tracker    ScheduleTracker
	Runs       ScheduleRuns
	Intake     IntakeHolds
	Directives Directives
	// Staleness is optional; see ScheduleStaleness for what a pull without one
	// loses, which is a sentence rather than a constraint.
	Staleness ScheduleStaleness
	// Capacity is execution.max_concurrent_developers as this pull read it. It
	// bounds how many runs the scheduler starts; the reservation enforces the
	// same number across every process, and this only keeps the scheduler from
	// walking into a refusal it can see coming.
	Capacity int
	Start    Starter
}

func (p Pull) validate() error {
	var problems []error
	if p.Tracker == nil {
		problems = append(problems, errors.New("a pull requires a work tracker"))
	}
	if p.Runs == nil {
		problems = append(problems, errors.New("a pull requires the durable run state"))
	}
	if p.Intake == nil {
		problems = append(problems, errors.New("a pull requires the intake hold"))
	}
	if p.Directives == nil {
		problems = append(problems, errors.New("a pull requires the recorded directives"))
	}
	if p.Start == nil {
		problems = append(problems, errors.New("a pull requires a way to start a run"))
	}
	if p.Capacity < 1 {
		problems = append(problems, fmt.Errorf("developer capacity is %d, which schedules nothing", p.Capacity))
	}
	return errors.Join(problems...)
}

// Scheduler starts the work the harness chooses for itself, up to the
// configured developer capacity, and waits for every run it started.
type Scheduler struct {
	// Open assembles one pull. It is called once per pull rather than once per
	// scheduler, which is the whole of how a configuration change is picked up.
	Open func(ctx context.Context) (Pull, error)
	// Limit bounds how many runs one pass starts. Zero means the pass drains
	// what is ready, which is what an unattended scheduler wants; an operator
	// watching one wants a number.
	Limit int
}

// Started is one item this pass chose, and what became of the run for it.
type Started struct {
	WorkItemID string `json:"work_item_id"`
	// Reason is what was recorded on the run as why this item was chosen. It is
	// repeated here so a schedule read on its own accounts for every run in it,
	// which is the same reason the run records it at all.
	Reason  string  `json:"reason"`
	Outcome Outcome `json:"outcome"`
	// Declined reports a start that never became a run because the slot or the
	// item went to another process between this pull and the reservation. It is
	// not a failure: the scheduler asked for something that had just stopped
	// being available, which is the ordinary outcome of two schedulers running.
	Declined string `json:"declined,omitempty"`
	Failure  string `json:"failure,omitempty"`
}

// Deferred is one ready item this pass did not choose, and why. It is reported
// rather than left out because an item missing from a schedule reads exactly
// like an item that was never in the queue.
type Deferred struct {
	WorkItemID string `json:"work_item_id"`
	Reason     string `json:"reason"`
}

// Schedule is what one pass did.
type Schedule struct {
	Started  []Started  `json:"started,omitempty"`
	Deferred []Deferred `json:"deferred,omitempty"`
	// IntakeHeld is the operator's hold, when one is what stopped the choosing.
	IntakeHeld *runstate.IntakeHold `json:"intake_held,omitempty"`
	// Capacity and Occupied are what the last pull read: the configured limit,
	// and how many developer slots were already taken when it read it.
	Capacity int `json:"capacity"`
	Occupied int `json:"occupied"`
	// Stopped says why the scheduler stopped pulling, in the words of one of the
	// Schedule* reasons above.
	Stopped string `json:"stopped"`
	// StalenessProblem names a staleness reading that failed. It costs the
	// recorded reasons a sentence and costs the schedule nothing else, so it is
	// reported beside the pass rather than failing it.
	StalenessProblem string `json:"staleness_problem,omitempty"`
}

// Schedule pulls ready work and runs it, up to the capacity the configuration
// allows, and returns once every run it started has ended.
//
// It returns an error only for something that stopped it pulling. A run that
// failed is on the schedule as a failed run, because one item failing is not a
// reason for the pass to stop choosing others — that decision belongs to the
// operator holding intake, or to the development manager replanning the item.
func (s Scheduler) Schedule(ctx context.Context) (Schedule, error) {
	if s.Open == nil {
		return Schedule{}, errors.New("a scheduler requires a way to open a pull")
	}
	if s.Limit < 0 {
		return Schedule{}, fmt.Errorf("a scheduler limit of %d starts nothing", s.Limit)
	}

	schedule := Schedule{}
	// completions carries each started run back to this goroutine, which is the
	// only one that touches the schedule. The runs themselves never share
	// anything: each has its own worktree, its own reservation, and its own
	// pipeline built from the configuration its pull read.
	completions := make(chan completed)
	// mine is the items this pass has started and not yet collected, by work
	// item. It exists because a run does not appear in the durable state until it
	// reserves, which is several steps after it is started, and a pull that
	// counted only the recorded runs would start the same slot twice.
	mine := make(map[string]int)
	// tried is every item this pass has already chosen or deferred. Nothing is
	// ever removed from it: a run that ends without moving the item out of the
	// ready queue would otherwise be chosen again on the next pull, for as long
	// as the pass ran.
	tried := make(map[string]bool)
	running := 0

	// collect takes one finished run into the schedule. It reports false when the
	// context ended first, which stops the pulling; the runs still in flight are
	// waited out below either way.
	collect := func() bool {
		select {
		case done := <-completions:
			running--
			delete(mine, schedule.Started[done.index].WorkItemID)
			schedule.Started[done.index].record(done)
			return true
		case <-ctx.Done():
			return false
		}
	}

	var failure error
pulling:
	for {
		if s.Limit > 0 && len(schedule.Started) >= s.Limit {
			schedule.Stopped = ScheduleLimitReached
			break
		}
		if ctx.Err() != nil {
			schedule.Stopped = ScheduleCancelled
			break
		}
		pull, err := s.Open(ctx)
		if err == nil {
			err = pull.validate()
		}
		if err != nil {
			failure = fmt.Errorf("open a pull: %w", err)
			schedule.Stopped = ScheduleUnreadable
			break
		}
		// The intake hold is read before anything is chosen, because choosing is
		// the whole of what it holds. It is asked again on every pull rather than
		// once for the pass: the hold that matters is the one the operator places
		// while the scheduler is running, and a pass that answered from its first
		// reading would keep choosing work for as long as it lasted.
		hold, held, err := pull.Intake.Held()
		if err != nil {
			failure = fmt.Errorf("read whether the operator has held intake: %w", err)
			schedule.Stopped = ScheduleUnreadable
			break
		}
		if held {
			schedule.IntakeHeld = &hold
			schedule.Stopped = ScheduleIntakeHeld
			break
		}

		occupied, err := occupiedItems(pull.Runs)
		if err != nil {
			failure = err
			schedule.Stopped = ScheduleUnreadable
			break
		}
		for id := range mine {
			occupied[id] = struct{}{}
		}
		schedule.Capacity = pull.Capacity
		schedule.Occupied = len(occupied)
		free := pull.Capacity - len(occupied)
		if free < 1 {
			if running == 0 {
				schedule.Stopped = ScheduleCapacityFull
				break
			}
			if !collect() {
				schedule.Stopped = ScheduleCancelled
				break
			}
			continue
		}

		queue, err := pull.queue(ctx)
		if err != nil {
			failure = err
			schedule.Stopped = ScheduleUnreadable
			break
		}
		stale, stalenessProblem := pull.stale(ctx)
		if stalenessProblem != "" {
			schedule.StalenessProblem = stalenessProblem
		}

		started := 0
		for _, entry := range queue.Entries {
			if started == free {
				break
			}
			if s.Limit > 0 && len(schedule.Started) >= s.Limit {
				break
			}
			if !entry.Ready || tried[entry.ID] {
				continue
			}
			if _, busy := occupied[entry.ID]; busy {
				continue
			}
			// An unresolved directive stops the work whether it is read here or in
			// the pipeline, so this is not the enforcement — it is the scheduler
			// declining to spend a slot on a start it can see will not proceed, and
			// saying which directive it was.
			pausing, err := pull.Directives.Pausing(entry.ID)
			if err != nil {
				failure = fmt.Errorf("read the directives that pause %s: %w", entry.ID, err)
				schedule.Stopped = ScheduleUnreadable
				break pulling
			}
			tried[entry.ID] = true
			if len(pausing) > 0 {
				schedule.Deferred = append(schedule.Deferred, Deferred{
					WorkItemID: entry.ID,
					Reason:     "an unresolved directive pauses it: " + pausing[0].Summary(),
				})
				continue
			}

			index := len(schedule.Started)
			selection := runstate.Selection{
				By:     runstate.SelectedByScheduler,
				Reason: scheduleReason(entry, queue, free, pull.Capacity, stale[entry.ID]),
			}
			schedule.Started = append(schedule.Started, Started{WorkItemID: entry.ID, Reason: selection.Reason})
			mine[entry.ID] = index
			running++
			started++
			go func(workItemID string) {
				outcome, err := pull.Start(ctx, workItemID, selection)
				completions <- completed{index: index, outcome: outcome, err: err}
			}(entry.ID)
		}
		if started > 0 {
			continue
		}
		// Nothing was startable this pull. With runs of ours still going, one of
		// them finishing changes the answer — it frees a slot, and it may close the
		// item something else was waiting on — so the pass waits rather than
		// concluding the queue is empty.
		if running == 0 {
			schedule.Stopped = ScheduleDrained
			break
		}
		if !collect() {
			schedule.Stopped = ScheduleCancelled
			break
		}
	}

	// Every run this pass started is waited out, including when the pass stopped
	// pulling because something failed or the context ended. A scheduler that
	// returned with runs still going would leave work in flight that nothing in
	// the schedule accounts for, which is the state this whole package exists to
	// keep the harness out of.
	for running > 0 {
		done := <-completions
		running--
		schedule.Started[done.index].record(done)
	}
	return schedule, failure
}

// completed is one finished run on its way back to the scheduling goroutine.
type completed struct {
	index   int
	outcome Outcome
	err     error
}

// record takes a finished run into its schedule entry. A refusal that means the
// work went to somebody else is recorded as declined rather than failed: two
// schedulers racing for the last slot is the design working, and reporting it as
// a failure would make ordinary concurrency look like breakage.
func (s *Started) record(done completed) {
	s.Outcome = done.outcome
	if done.err == nil {
		return
	}
	var capacity runstate.CapacityError
	var existing ExistingRunError
	switch {
	case errors.As(done.err, &capacity):
		s.Declined = fmt.Sprintf("the last free developer slot went to another run before this one was reserved (%d active, limit %d)",
			capacity.Active, capacity.Limit)
	case errors.As(done.err, &existing):
		s.Declined = fmt.Sprintf("another process is already running %s as %s", existing.State.WorkItemID, existing.State.RunID)
	default:
		s.Failure = done.err.Error()
	}
}

// occupiedItems names the work items with a run in flight anywhere. It is both
// halves of what a pull needs from the durable state: how many developer slots
// are taken, and which items must not be started again.
func occupiedItems(runs ScheduleRuns) (map[string]struct{}, error) {
	incomplete, err := runs.Incomplete()
	if err != nil {
		return nil, fmt.Errorf("read what is already in flight: %w", err)
	}
	occupied := make(map[string]struct{}, len(incomplete))
	for _, state := range incomplete {
		occupied[state.WorkItemID] = struct{}{}
	}
	return occupied, nil
}

// queue assembles the admitted work into the product manager's order. It is the
// same assembly a conversation's backlog uses and the same one a development
// manager reads, built from the tracker every pull rather than stored, so what
// the scheduler pulls can never drift from the priorities actually set.
func (p Pull) queue(ctx context.Context) (backlog.Queue, error) {
	var admitted []beads.WorkItem
	for _, status := range scheduledStatuses {
		items, err := p.Tracker.List(ctx, status)
		if err != nil {
			return backlog.Queue{}, fmt.Errorf("list %s work items: %w", status, err)
		}
		admitted = append(admitted, items...)
	}
	ready, err := p.Tracker.Ready(ctx)
	if err != nil {
		return backlog.Queue{}, fmt.Errorf("list the work items the tracker reports as ready: %w", err)
	}
	pullable := make([]string, 0, len(ready))
	for _, item := range ready {
		pullable = append(pullable, item.ID)
	}
	return backlog.Order(admitted, pullable), nil
}

// stale reads what changed upstream of the admitted work after it was admitted,
// keyed by work item. It withholds nothing and reorders nothing; what it
// produces goes into the recorded reason a run was chosen. A reading that failed
// is described rather than raised, because losing it costs a sentence.
func (p Pull) stale(ctx context.Context) (map[string][]staleness.Change, string) {
	if p.Staleness == nil {
		return nil, ""
	}
	items, err := p.Staleness.Stale(ctx)
	if err != nil {
		return nil, fmt.Sprintf("what changed upstream of the admitted work could not be read, so no recorded reason names it: %v", err)
	}
	changes := make(map[string][]staleness.Change, len(items))
	for _, item := range items {
		changes[item.ID] = item.Changes
	}
	return changes, ""
}

// scheduleReason is what the run records as why the harness chose this item. It
// is prose rather than a code because what makes a choice defensible is the
// argument for it: an operator reading a run months later needs where the item
// sat in the order, what else was pullable, and how much of the machine was
// already busy.
//
// Staleness goes in the same sentence and is stated as not having decided
// anything, because that is exactly what would otherwise be misread: an item
// pulled with a change named beside it looks like an item pulled in spite of a
// warning, and it is neither.
func scheduleReason(entry backlog.Entry, queue backlog.Queue, free, capacity int, stale []staleness.Change) string {
	reason := fmt.Sprintf(
		"the scheduler pulled %s from the backlog: position %d of %d admitted item(s) at priority %d, one of the %d the tracker reports as ready, with %d of %d developer slot(s) free",
		entry.ID, entry.Position, len(queue.Entries), entry.Priority, queue.Ready(), free, capacity)
	if len(stale) == 0 {
		return reason + "."
	}
	change := stale[0]
	return reason + fmt.Sprintf(
		". %s was %s by the %s after this item was admitted (%s), and %d further change(s) upstream of it; staleness is reported rather than acted on, so it held nothing back",
		change.ArtifactID, change.Action, change.By,
		singleLine(change.Reason, maxScheduleReasonBytes), len(stale)-1)
}

// Render describes a pass for an operator: what it started, what became of each
// one, and why it stopped choosing. What it will not do is print a line per
// quiet outcome — a pass that started nothing says so in one line, because that
// is the whole of what happened.
func (s Schedule) Render() string {
	var rendered strings.Builder
	if len(s.Started) == 0 {
		fmt.Fprintf(&rendered, "nothing was started: %s\n", s.Stopped)
	} else {
		fmt.Fprintf(&rendered, "%d run(s) started, %d of %d developer slot(s) taken at the last pull\n",
			len(s.Started), s.Occupied, s.Capacity)
	}
	for _, started := range s.Started {
		fmt.Fprintf(&rendered, "%s: %s\n", started.WorkItemID, started.state())
		fmt.Fprintf(&rendered, "  chosen because %s\n", started.Reason)
		if started.Outcome.Integration != nil {
			fmt.Fprintf(&rendered, "  integrated into %s: %s\n",
				started.Outcome.Integration.TargetBranch, started.Outcome.Integration.TargetCommit)
		}
		if started.Declined != "" {
			fmt.Fprintf(&rendered, "  not started: %s\n", started.Declined)
		}
		if started.Failure != "" {
			fmt.Fprintf(&rendered, "  failed: %s\n", started.Failure)
		}
	}
	for _, deferred := range s.Deferred {
		fmt.Fprintf(&rendered, "%s was not pulled: %s\n", deferred.WorkItemID, deferred.Reason)
	}
	if s.IntakeHeld != nil {
		fmt.Fprintf(&rendered, "intake has been held since %s", s.IntakeHeld.HeldAt.UTC().Format("2006-01-02 15:04:05Z"))
		if reason := strings.TrimSpace(s.IntakeHeld.Reason); reason != "" {
			fmt.Fprintf(&rendered, ": %s", reason)
		}
		rendered.WriteString("\n")
	}
	if s.StalenessProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.StalenessProblem)
	}
	if len(s.Started) > 0 {
		fmt.Fprintf(&rendered, "stopped pulling: %s\n", s.Stopped)
	}
	return rendered.String()
}

// state is the one-line account of what became of a started run. A declined
// start is neither a success nor a failure and is named as itself.
func (s Started) state() string {
	switch {
	case s.Declined != "":
		return "declined"
	case s.Failure != "":
		return "failed"
	case s.Outcome.Paused:
		return "paused"
	case s.Outcome.Integration != nil:
		return "integrated"
	default:
		return string(s.Outcome.Status)
	}
}

// Failed reports a pass with something in it an operator has to act on. A
// declined start is deliberately not one: the work went to another process,
// which is two schedulers doing exactly what they should.
func (s Schedule) Failed() bool {
	for _, started := range s.Started {
		if started.Failure != "" {
			return true
		}
	}
	return false
}
