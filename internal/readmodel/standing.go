// Package readmodel is the one server-side derivation the operator surfaces
// project. Nothing here renders for a particular surface and nothing here
// writes: it reads the durable records the harness already keeps and says what
// they mean, so that a number the CLI reports and the same number a channel
// reports cannot disagree.
//
// What it answers first is the standing question — where does the harness stand
// right now — in the four lines the operator ratified:
//
//	Running        the developer runs in flight, each with item, phase, elapsed, spend
//	Working        the persona conversations with a turn in flight
//	Not startable  each admitted item nothing will pull, with the refusal that stops it
//	Needs a human  what is waiting on a person, and whose move it is
//
// The lines are a contract rather than a layout. Every one of them is printed
// on every reading, because the failure they exist to end is silence: an
// operator who sees nothing cannot tell a quiet machine from a dead one, and
// four lines that are sometimes absent are four lines nobody can rely on. A
// line with nothing in it says "nothing", and a line whose source could not be
// read says that instead — never "nothing", which would be the confident
// emptiness every report in this harness is written to avoid.
//
// There is no fifth line and no residual category. Work that is admitted, has a
// free slot, and is held back by nothing is startable and is about to be
// started, so it is named in the not-startable line's own count of the backlog
// rather than in a bucket for whatever was left over: a residual category is
// where the states nobody thought about go to be invisible.
//
// # Where the answers come from
//
// Each line reads the truest record for it and nothing else. The runs come from
// durable run state, the conversations from the conversation records and the
// advisory hold that is the only thing that actually knows a turn is in flight
// — observed rather than taken, so that reading the status never costs the
// operator the conversation it describes — the refusals from the queue's own
// account of what holds each entry back, and the attention conditions from the
// switches, directives, proposals, and unsettled runs the harness records as it
// goes. Nothing is read from a session's memory of what it has already tried:
// that is a fact about one process rather than about the product, and an item
// passed over because a watch session remembers failing at it is an item this
// says nothing about.
package readmodel

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// backlogStatuses are the tracker slices the admitted work is assembled from.
// They are the scheduler's own, because the queue this describes has to be the
// queue that is actually pulled from.
var backlogStatuses = []string{"open", "blocked"}

// maxRefusalBytes bounds one refusal as this renders it. A parking reason and a
// directive are prose somebody wrote at whatever length they wanted, and this is
// a status line.
const maxRefusalBytes = 160

// Runs is the durable run state the standing status reads. It reads and never
// adopts: what is in flight and what is unsettled are both questions about runs
// other processes own, and answering them is not acting on one.
//
// It is satisfied by *runstate.Store.
type Runs interface {
	Incomplete() ([]runstate.State, error)
	Outstanding() ([]runstate.State, error)
	Price(workItemID string) (runstate.ItemPrice, error)
}

// Conversations is the durable conversation state, and the observation that
// says whether a turn is in flight. Both are needed and neither is enough: the
// record says what the conversation is and how many turns it has had, and only
// the hold says whether one is happening right now.
//
// The hold is observed rather than taken. A reading of the standing status must
// leave every conversation exactly as free as it found it, because this is
// asked of every conversation on every reading and hourly by the heartbeat, and
// a probe that acquired would be refusing the operator their own chat for the
// instant it held.
//
// It is satisfied by *runstate.ConversationStore.
type Conversations interface {
	Recorded() ([]runstate.Conversation, error)
	InFlight(runstate.ConversationIdentity) (bool, error)
}

// Tracker is the admitted work and the tracker's own account of what can be
// pulled. Readiness is asked for rather than inferred, for the reason the
// backlog states: a listing carries dependencies without carrying whether they
// are finished.
//
// It is satisfied by beads.Client.
type Tracker interface {
	List(ctx context.Context, status string) ([]beads.WorkItem, error)
	Ready(ctx context.Context) ([]beads.WorkItem, error)
}

// Directives is what the operator has told the harness, read for the ones
// nobody has settled. It is satisfied by *runstate.DirectiveStore.
type Directives interface {
	List() ([]directive.Directive, error)
}

// Amendments is the changes proposed to documents their proposer does not own,
// read for the ones nobody has decided. It is satisfied by
// *runstate.AmendmentStore.
type Amendments interface {
	List() ([]amendment.Record, error)
}

// OperatorHolds is the switch over everything the harness would spend on a
// provider. It is satisfied by *runstate.OperatorHoldStore.
type OperatorHolds interface {
	Held() (runstate.OperatorHold, bool, error)
}

// IntakeHolds is the switch over the work the harness chooses for itself. It is
// satisfied by *runstate.IntakeHoldStore.
type IntakeHolds interface {
	Held() (runstate.IntakeHold, bool, error)
}

// Sessions is what the sessions that choose work have said about themselves. It
// is satisfied by *runstate.WatchStore.
type Sessions interface {
	List() ([]runstate.WatchTransition, error)
}

// Sources are the durable records one standing reading is assembled from, and
// the two configured numbers it is read against. Every store is an interface so
// that this derivation can be exercised without a state directory, which is the
// only way a format nobody may break gets a fixture that holds it.
type Sources struct {
	Runs          Runs
	Conversations Conversations
	Tracker       Tracker
	Directives    Directives
	Amendments    Amendments
	OperatorHolds OperatorHolds
	IntakeHolds   IntakeHolds
	Sessions      Sessions
	// Capacity is execution.max_concurrent_developers as the caller read it. It is
	// what turns "nothing is starting" into "there is no slot", which are opposite
	// things for an operator to do about.
	Capacity int
	// TrackerTimeout bounds one tracker command, so an unresponsive tracker costs
	// this answer a line rather than hanging the surface that asked.
	TrackerTimeout time.Duration
	// Now stamps the reading. It defaults to the wall clock and is injected so a
	// test can pin an elapsed time.
	Now func() time.Time
}

// RunningRun is one developer run in flight, as the four-line status names it.
type RunningRun struct {
	RunID      string         `json:"run_id"`
	WorkItemID string         `json:"work_item_id"`
	Phase      runstate.Phase `json:"phase,omitempty"`
	StartedAt  time.Time      `json:"started_at"`
	Elapsed    time.Duration  `json:"elapsed"`
	CostUSD    float64        `json:"cost_usd"`
	// UnknownCost says why there is no figure rather than reporting one of zero: a
	// run whose evidence cannot be read has not cost nothing.
	UnknownCost string `json:"unknown_cost,omitempty"`
}

// WorkingTurn is one persona conversation with a turn in flight. It is the fact
// no surface counted before this: a conversation is not a run, so a machine
// spending an operator's money on six persona turns reported nothing running at
// all.
type WorkingTurn struct {
	Agent string           `json:"agent"`
	Role  domain.AgentRole `json:"role"`
	// Turns is how many turns the record holds, which is the turn before the one
	// in flight: a turn is recorded as it completes.
	Turns int `json:"turns"`
	// Since is when the record last moved, which is the closest thing the durable
	// state has to when the turn in flight began.
	Since   time.Time     `json:"since"`
	Elapsed time.Duration `json:"elapsed"`
}

// Refused is one admitted item nothing will pull, with the refusal that stops
// it. The reason is the queue's or the harness's own account rather than a
// paraphrase, because a reason nobody can act on is the whole of what this line
// exists to replace.
type Refused struct {
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title,omitempty"`
	Reason     string `json:"reason"`
}

// Attention is one thing waiting on a person: what it is, and whose move it is.
// The move is half the fact — a thread that says something is waiting without
// saying who on is the silence this whole surface exists to end.
type Attention struct {
	What  string `json:"what"`
	Whose string `json:"whose"`
}

// Standing is where the harness stands, in the four lines and nothing else.
// Every line carries its own problem rather than a shared one, because a
// tracker that will not answer says nothing whatever about the runs in flight,
// and one failure standing for all four would lose three answers the caller
// still has.
type Standing struct {
	ObservedAt time.Time `json:"observed_at"`

	Running        []RunningRun `json:"running"`
	RunningProblem string       `json:"running_problem,omitempty"`

	Working        []WorkingTurn `json:"working"`
	WorkingProblem string        `json:"working_problem,omitempty"`

	NotStartable []Refused `json:"not_startable"`
	// Admitted is the whole backlog this reading saw, so a short not-startable
	// list is legible: two refusals out of three admitted items and two out of
	// forty are different states of the same machine.
	Admitted            int    `json:"admitted"`
	NotStartableProblem string `json:"not_startable_problem,omitempty"`

	NeedsHuman        []Attention `json:"needs_human"`
	NeedsHumanProblem string      `json:"needs_human_problem,omitempty"`
}

// ReadStanding assembles the four lines from the durable records. It never
// fails as a whole: a source that cannot be read costs its own line and leaves
// the other three, because an operator asking where things stand is worse off
// with nothing than with the three quarters the harness could answer, as long as
// the missing quarter says so.
func ReadStanding(ctx context.Context, sources Sources) Standing {
	now := sources.now()
	standing := Standing{
		ObservedAt:   now,
		Running:      []RunningRun{},
		Working:      []WorkingTurn{},
		NotStartable: []Refused{},
		NeedsHuman:   []Attention{},
	}

	running, runningProblem := readRunning(sources, now)
	standing.Running, standing.RunningProblem = running, runningProblem

	standing.Working, standing.WorkingProblem = readWorking(sources, now)

	// The switches and the directives are read once and used twice: they are why
	// admitted work is not being pulled, and they are themselves things waiting on
	// a person. Reading them twice would be two chances for the two lines to
	// disagree about one file.
	switches := readSwitches(sources)
	refused, queue, stall, notStartableProblem := readNotStartable(ctx, sources, switches, running)
	standing.NotStartable = refused
	standing.Admitted = len(queue.Entries)
	standing.NotStartableProblem = notStartableProblem

	needs, needsProblem := readNeedsHuman(sources, switches)
	// A stall that is holding admitted work back and is nobody else's line to
	// carry is attention in its own right. Nothing else reports it: a live session
	// choosing nothing over a ready queue is a state no record announces, and the
	// only thing that ever said it was the silence afterwards.
	if waiting, attention := stall.Waiting(); attention {
		needs = append(needs, waiting)
	}
	// Work marked for a conversation is on both lines and says a different thing
	// on each: the queue's line says why nothing pulls it, and this says who has
	// to open the conversation. A reader looking for what waits on a person must
	// not have to read the queue to find the longest wait there is.
	standing.NeedsHuman = append(needs, HandedOff(queue)...)
	standing.NeedsHumanProblem = needsProblem
	return standing
}

// readRunning is the developer runs in flight, priced from the same recorded
// evidence `yoyo cost` reads. A run whose price cannot be read is reported as
// unpriceable rather than as free, which is the rule every cost surface here
// already holds.
func readRunning(sources Sources, now time.Time) ([]RunningRun, string) {
	if sources.Runs == nil {
		return nil, "nothing was wired to read the runs in flight"
	}
	states, err := sources.Runs.Incomplete()
	if err != nil {
		return nil, fmt.Sprintf("the runs in flight could not be read: %v", err)
	}
	running := make([]RunningRun, 0, len(states))
	for _, state := range states {
		run := RunningRun{
			RunID:      state.RunID,
			WorkItemID: state.WorkItemID,
			Phase:      state.Phase,
			StartedAt:  state.StartedAt,
			Elapsed:    now.Sub(state.StartedAt),
			// A run nothing has priced yet is stated as unpriced rather than as free.
			// It is overwritten below by whatever the ledger actually says.
			UnknownCost: "no priced invocation is recorded for it yet",
		}
		price, err := sources.Runs.Price(state.WorkItemID)
		if err != nil {
			run.UnknownCost = fmt.Sprintf("what it has spent could not be read: %v", err)
		} else {
			for _, priced := range price.Runs {
				if priced.RunID != state.RunID {
					continue
				}
				run.CostUSD, run.UnknownCost = priced.CostUSD, priced.Unknown
				break
			}
		}
		running = append(running, run)
	}
	sort.SliceStable(running, func(first, second int) bool {
		if !running[first].StartedAt.Equal(running[second].StartedAt) {
			return running[first].StartedAt.Before(running[second].StartedAt)
		}
		return running[first].RunID < running[second].RunID
	})
	return running, ""
}

// readWorking is the persona conversations with a turn in flight. A conversation
// nobody is holding is not listed at all: this line counts work happening now,
// and a record of every conversation the product has ever had would make the
// count mean nothing.
func readWorking(sources Sources, now time.Time) ([]WorkingTurn, string) {
	if sources.Conversations == nil {
		return nil, "nothing was wired to read the conversations"
	}
	recorded, err := sources.Conversations.Recorded()
	if err != nil {
		return nil, fmt.Sprintf("the conversations could not be read: %v", err)
	}
	working := make([]WorkingTurn, 0, len(recorded))
	var unanswered []string
	for _, conversation := range recorded {
		identity := runstate.ConversationIdentity{Agent: conversation.Agent, Role: conversation.Role}
		if identity.Agent == "" {
			identity.Agent = string(conversation.Role)
		}
		inFlight, problem := InFlight(sources.Conversations, identity)
		if problem != "" {
			unanswered = append(unanswered, fmt.Sprintf("%s (%s)", identity, problem))
			continue
		}
		if !inFlight {
			continue
		}
		working = append(working, WorkingTurn{
			Agent:   identity.Agent,
			Role:    conversation.Role,
			Turns:   conversation.Turns,
			Since:   conversation.UpdatedAt,
			Elapsed: now.Sub(conversation.UpdatedAt),
		})
	}
	sort.SliceStable(working, func(first, second int) bool {
		return working[first].Agent < working[second].Agent
	})
	if len(unanswered) > 0 {
		// The conversations that did answer are still reported. What is said beside
		// them is which ones this reading cannot speak for, because a count that
		// silently skipped them would be a count somebody trusts.
		return working, "whether a turn is in flight could not be asked of " + strings.Join(unanswered, "; ")
	}
	return working, ""
}

// InFlight reports whether a process is holding one agent's conversation right
// now, and why the question could not be answered when it could not. It
// observes the hold and acquires nothing, so asking costs the conversation
// nothing: every surface asks this of every conversation, and the answer must
// never be the reason the next question is answered differently.
//
// A failure to ask is not an answer. Reporting one as in flight would say every
// agent at once was mid-turn whenever the state directory could not be opened,
// which is both wrong and the opposite of what anybody would do about it.
func InFlight(conversations Conversations, identity runstate.ConversationIdentity) (bool, string) {
	inFlight, err := conversations.InFlight(identity)
	if err != nil {
		return false, err.Error()
	}
	return inFlight, ""
}

// switches is what has stopped the harness choosing work, read once for both the
// line that says which items are not startable and the line that says who has to
// act.
type switches struct {
	operator     runstate.OperatorHold
	operatorHeld bool
	intake       runstate.IntakeHold
	intakeHeld   bool
	// pausing are the unresolved directives that stop work, in the order they were
	// recorded.
	pausing []directive.Directive
	// problems are the switches that could not be read. A switch nobody can read is
	// never reported as clear: an operator told nothing is holding the line, by a
	// reading that could not open the hold file, has been told the one thing that
	// is worse than nothing.
	problems []string
}

func readSwitches(sources Sources) switches {
	var read switches
	if sources.OperatorHolds == nil {
		read.problems = append(read.problems, "nothing was wired to read the operator's hold")
	} else if hold, held, err := sources.OperatorHolds.Held(); err != nil {
		read.problems = append(read.problems, fmt.Sprintf("the operator's hold could not be read: %v", err))
	} else {
		read.operator, read.operatorHeld = hold, held
	}
	if sources.IntakeHolds == nil {
		read.problems = append(read.problems, "nothing was wired to read the intake hold")
	} else if hold, held, err := sources.IntakeHolds.Held(); err != nil {
		read.problems = append(read.problems, fmt.Sprintf("the intake hold could not be read: %v", err))
	} else {
		read.intake, read.intakeHeld = hold, held
	}
	if sources.Directives == nil {
		read.problems = append(read.problems, "nothing was wired to read the recorded directives")
	} else if recorded, err := sources.Directives.List(); err != nil {
		read.problems = append(read.problems, fmt.Sprintf("the recorded directives could not be read: %v", err))
	} else {
		for _, given := range recorded {
			if given.Pauses() {
				read.pausing = append(read.pausing, given)
			}
		}
	}
	return read
}

// readNotStartable is every admitted item nothing will pull, with the refusal
// that stops it, how much admitted work this reading saw in all, and the stall
// that is actually holding some of it back.
//
// The refusals come from three places and each is the real one. An entry the
// queue itself calls unready carries the queue's own account. An item an
// unresolved directive pauses carries that directive, because the pipeline
// refuses to commit to the work for exactly that reason. Everything else is
// pullable, and what is left is the pass-level refusal — a switch, a full
// machine, nothing choosing — which is a fact about the harness said once
// against each item it actually stops.
//
// The stall it returns is the one that stopped at least one item. A stall over
// an empty queue is a state of the machine and not something waiting on
// anybody, so it is returned as no stall at all rather than as attention nobody
// asked for.
func readNotStartable(ctx context.Context, sources Sources, held switches, running []RunningRun) ([]Refused, backlog.Queue, Stall, string) {
	if sources.Tracker == nil {
		return nil, backlog.Queue{}, Stall{}, "nothing was wired to read the admitted work"
	}
	queue, err := readQueue(ctx, sources)
	if err != nil {
		return nil, backlog.Queue{}, Stall{}, fmt.Sprintf("the admitted work could not be read: %v", err)
	}
	// An item a run is already carrying is on the running line. Naming it here as
	// well would report the machine working as work that will not start.
	inFlight := make(map[string]struct{}, len(running))
	for _, run := range running {
		inFlight[run.WorkItemID] = struct{}{}
	}
	// Why nothing more is being chosen, worked out once and by the derivation every
	// other surface reads. It names no reason when the harness would start the next
	// pullable item, which is what makes a pullable item's absence from this line
	// mean something.
	stopped := whyNothingStarts(sources, held, len(running))
	refusal := stopped.Refusal()
	// What the stall could not read is said whatever the queue holds. It is a
	// gap in this reading rather than a fact about the work, so a queue with
	// nothing in it must not swallow it below.
	problem := stopped.Problem

	// Whether the stall actually stopped anything. A stall over an empty queue is
	// a state of the machine rather than something waiting on a person, and the
	// attention line must not be given one.
	stalled := false
	refused := make([]Refused, 0, len(queue.Entries))
	for _, entry := range queue.Entries {
		if _, carried := inFlight[entry.ID]; carried {
			continue
		}
		paused := pausedBy(held.pausing, entry.ID)
		switch {
		case !entry.Ready:
			refused = append(refused, Refused{WorkItemID: entry.ID, Title: entry.Title, Reason: entry.Hold()})
		case paused != nil:
			refused = append(refused, Refused{WorkItemID: entry.ID, Title: entry.Title,
				Reason: fmt.Sprintf("paused for unresolved directive %s: %s",
					paused.ID, singleLine(paused.Unresolved, maxRefusalBytes))})
		case refusal != "":
			refused = append(refused, Refused{WorkItemID: entry.ID, Title: entry.Title, Reason: refusal})
			stalled = true
		}
	}
	if !stalled {
		stopped = Stall{}
	}
	if len(held.problems) > 0 {
		problem = joinProblems(problem, strings.Join(held.problems, "; "))
	}
	return refused, queue, stopped, problem
}

// whyNothingStarts is the pass-level stall: the reason a pullable item with
// nothing wrong with it is still not being started, from the records this
// reading holds. The derivation itself is shared, because a channel and a
// terminal that worked this out separately would be two answers to the one
// question an operator asks.
func whyNothingStarts(sources Sources, held switches, running int) Stall {
	conditions := Conditions{
		OperatorHold: held.operator,
		OperatorHeld: held.operatorHeld,
		IntakeHold:   held.intake,
		IntakeHeld:   held.intakeHeld,
		Running:      running,
		Capacity:     sources.Capacity,
	}
	// The watch log costs a read, so it is passed as the question rather than the
	// answer: the derivation asks for it only where the switches and the capacity
	// did not already settle what has stopped the choosing.
	if sources.Sessions != nil {
		conditions.Sessions = sources.Sessions.List
	}
	return WhyNothingStarts(conditions)
}

// Choosing is the watch sessions still alive, latest transition per session. It
// reads the log per session rather than taking its last line, because one log
// holds every session a product has had and nothing stops two running at once: a
// last entry can be one session stopping while another carries on watching, and
// a reading that took it at face value would report a line that is being pulled
// from as one nobody is pulling from.
//
// It is exported and lives here for the reason the whole package does: which
// sessions are alive is one derivation, and two surfaces folding the same log
// their own way is how they come to disagree.
func Choosing(sessions []runstate.WatchTransition) []runstate.WatchTransition {
	return alive(sessions, func(state runstate.WatchState) bool {
		switch state {
		case runstate.WatchStopped, runstate.WatchIdle:
			// A stopped session is gone, and an idle one is alive but choosing
			// nothing — which is the same answer to "is anything being pulled".
			return false
		default:
			return true
		}
	})
}

// Live is the watch sessions that have not stopped, latest transition per
// session, including the idle ones. It is the other reading of the same fold: a
// session polling an empty queue is not choosing anything and is very much a
// process that is running, and the two questions have opposite answers about it.
func Live(sessions []runstate.WatchTransition) []runstate.WatchTransition {
	return alive(sessions, func(state runstate.WatchState) bool {
		return state != runstate.WatchStopped
	})
}

// alive folds a watch log to the last transition of each session and keeps the
// ones a caller counts as still going, newest first.
func alive(sessions []runstate.WatchTransition, keep func(runstate.WatchState) bool) []runstate.WatchTransition {
	last := make(map[string]runstate.WatchTransition, len(sessions))
	for _, transition := range sessions {
		if recorded, seen := last[transition.SessionID]; seen && recorded.At.After(transition.At) {
			continue
		}
		last[transition.SessionID] = transition
	}
	kept := make([]runstate.WatchTransition, 0, len(last))
	for _, transition := range last {
		if keep(transition.State) {
			kept = append(kept, transition)
		}
	}
	sort.SliceStable(kept, func(first, second int) bool {
		if !kept[first].At.Equal(kept[second].At) {
			return kept[first].At.After(kept[second].At)
		}
		return kept[first].SessionID < kept[second].SessionID
	})
	return kept
}

// readQueue assembles the admitted work in the product manager's order, from the
// same two readings the scheduler makes: the listings that carry the order, and
// the tracker's own account of what can be pulled.
func readQueue(ctx context.Context, sources Sources) (backlog.Queue, error) {
	var admitted []beads.WorkItem
	for _, status := range backlogStatuses {
		items, err := sources.list(ctx, status)
		if err != nil {
			return backlog.Queue{}, err
		}
		admitted = append(admitted, items...)
	}
	trackerCtx, cancel := sources.bounded(ctx)
	defer cancel()
	ready, err := sources.Tracker.Ready(trackerCtx)
	if err != nil {
		return backlog.Queue{}, fmt.Errorf("list the work items the tracker reports as ready: %w", err)
	}
	pullable := make([]string, 0, len(ready))
	for _, item := range ready {
		pullable = append(pullable, item.ID)
	}
	return backlog.Order(admitted, pullable), nil
}

// readNeedsHuman is everything waiting on a person, with whose move it is.
//
// What is here and what is not is the whole of the line's value. A switch
// somebody placed, a directive nobody settled, a proposal nobody decided, a run
// that owes a step, and work marked for a conversation are all waiting on a
// named person and will wait forever without one. A parked item is not: parking
// is a decision already taken, and listing it would tell an operator to act on
// something somebody deliberately settled.
func readNeedsHuman(sources Sources, held switches) ([]Attention, string) {
	attention := make([]Attention, 0, 4)
	if held.operatorHeld {
		attention = append(attention, Attention{
			What: fmt.Sprintf("all harness activity is held, since %s",
				held.operator.HeldAt.UTC().Format(time.RFC3339)),
			Whose: "the operator's — nothing runs until `yoyo resume` lifts it",
		})
	}
	if held.intakeHeld {
		what := fmt.Sprintf("intake is held, since %s", held.intake.HeldAt.UTC().Format(time.RFC3339))
		if reason := singleLine(held.intake.Reason, maxRefusalBytes); reason != "" {
			what += ": " + reason
		}
		attention = append(attention, Attention{
			What:  what,
			Whose: "the operator's — nothing new is chosen until `yoyo release` lifts it",
		})
	}
	for _, paused := range held.pausing {
		attention = append(attention, Attention{
			What: fmt.Sprintf("directive %s is unresolved: %s",
				paused.ID, singleLine(paused.Unresolved, maxRefusalBytes)),
			Whose: "the operator's — the work it affects waits until `yoyo directive resolve` settles it",
		})
	}
	problem := strings.Join(held.problems, "; ")

	if sources.Amendments == nil {
		problem = joinProblems(problem, "nothing was wired to read the proposed changes")
	} else if records, err := sources.Amendments.List(); err != nil {
		problem = joinProblems(problem, fmt.Sprintf("the proposed changes could not be read: %v", err))
	} else {
		for _, proposal := range amendment.Pending(records) {
			attention = append(attention, Attention{
				What: fmt.Sprintf("a change to %s is proposed and undecided (%s)", proposal.Artifact, proposal.ID),
				Whose: fmt.Sprintf("the %s's — nothing reaches the document until they or the operator decide it",
					proposal.Owner),
			})
		}
	}

	if sources.Runs == nil {
		problem = joinProblems(problem, "nothing was wired to read the runs that owe a step")
	} else if outstanding, err := sources.Runs.Outstanding(); err != nil {
		problem = joinProblems(problem, fmt.Sprintf("the runs that owe a step could not be read: %v", err))
	} else {
		for _, state := range outstanding {
			attention = append(attention, Attention{
				What:  fmt.Sprintf("run %s of %s ended still owing a step", state.RunID, state.WorkItemID),
				Whose: "the operator's — `yoyo reconcile` reports which and settles it",
			})
		}
	}
	return attention, problem
}

// HandedOff is the admitted work no run will ever carry, as attention rather
// than as a queue entry: the item is done in the conversation it names, and the
// wait between the handoff and somebody opening that conversation is the longest
// silence any of this has.
//
// It is derived from the same queue the not-startable line reads, and it is a
// separate function because the two lines say different things about the same
// item: one says why nothing pulls it, and this says who has to move.
func HandedOff(queue backlog.Queue) []Attention {
	attention := make([]Attention, 0, len(queue.Entries))
	for _, entry := range queue.Entries {
		if entry.Executor.DeveloperRun() {
			continue
		}
		whose := "the role it names — in conversation; no run will ever be started for it"
		if role := entry.Executor.Role(); role != "" {
			whose = "the " + role.Title() + "'s — in conversation; no run will ever be started for it"
		}
		attention = append(attention, Attention{
			What:  fmt.Sprintf("%s is admitted for %q rather than a developer run", entry.ID, entry.Executor),
			Whose: whose,
		})
	}
	return attention
}

// pausedBy is the unresolved directive that stops one item, or nothing.
func pausedBy(pausing []directive.Directive, workItemID string) *directive.Directive {
	for index := range pausing {
		if pausing[index].Affects(workItemID) {
			return &pausing[index]
		}
	}
	return nil
}

func (s Sources) list(ctx context.Context, status string) ([]beads.WorkItem, error) {
	trackerCtx, cancel := s.bounded(ctx)
	defer cancel()
	items, err := s.Tracker.List(trackerCtx, status)
	if err != nil {
		return nil, fmt.Errorf("list %s work items: %w", status, err)
	}
	return items, nil
}

// bounded gives one tracker command its own deadline, so a tracker that will not
// answer costs this reading a line rather than hanging whatever asked for it. A
// caller that configured no bound gets the context it passed in.
func (s Sources) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.TrackerTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, s.TrackerTimeout)
}

func (s Sources) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// joinProblems puts two accounts of what could not be read on one line, and
// keeps whichever of them there is when there is only one.
func joinProblems(first, second string) string {
	switch {
	case strings.TrimSpace(first) == "":
		return second
	case strings.TrimSpace(second) == "":
		return first
	default:
		return first + "; " + second
	}
}

// singleLine folds prose somebody wrote into the one line a status can carry,
// and says where it was cut. It is cut on a rune boundary: a line truncated
// mid-rune is not text.
func singleLine(text string, limit int) string {
	folded := strings.Join(strings.Fields(text), " ")
	if len(folded) <= limit {
		return folded
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimRight(folded[:cut], " ") + "…"
}
