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
// Three things it does decide about an item itself, and all three are choosing
// rather than enforcing, which is why they are here. The first is whether the
// item is work at all: a container whose unfinished children carry its execution
// is a heading over the queue rather than an entry in it, and nothing downstream
// can tell — the tracker reports it as pullable, the reservation sees a different
// item from its child, and both runs then make the same change twice. The second
// is whether it is work to start now: two items over the same epic or the same
// files still integrate correctly when they are raced, and what that costs is a
// replay, a fresh set of checks, and a fresh review on whichever loses. So they
// are sequenced instead. See conflict.go.
//
// The third is whether a developer run is what carries the work at all. An item
// admitted as conversation-executed — a promotion the architect makes to a
// document it owns, a decomposition settled in conversation — is passed over
// rather than started, and the backlog is where that is decided rather than
// here: an item nothing can run has to look the same to everything that reads
// the order. What is here is saying so against the item, because the alternative
// is silence. The cost of not doing it was measured: an architect's item was
// selected as ordinary developer work and spent a whole run and two review
// rounds producing a correctly refused empty diff — and those rounds count
// against the item's cap, so a second mis-selection would have escalated work
// nobody had ever started.
//
// Parked work is the same shape and was measured the same way. An item the
// product manager has deliberately taken out of reach is passed over rather than
// started, decided in the backlog for the same reason, and said out loud here
// for the same one. The cost that bought it: the parking used to be expressed as
// the bottom of the priority order, which is a reading nothing that pulls
// shares, and on 2026-08-27 a drained queue reached it and spent $34.38 on a run
// of deferred work that failed. Nothing about that selection was wrong. What was
// wrong was that the decision lived somewhere selection could not look, and a
// watch session drains a queue every day it is quiet.
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
//
// # Draining and watching
//
// A pass either drains what is ready and returns, or stays open until somebody
// stops it. They are one loop with one difference: where a drain concludes the
// queue is empty and stops choosing, a watch waits out a configured interval and
// reads it again. Nothing else changes, and nothing else needed to — every pull
// already re-reads the configuration, re-reads the intake hold, takes the queue
// in the product manager's order, and records why it chose what it chose, so a
// reprioritization is honored at the next pull and an admission at the next poll
// without anything here detecting either.
//
// What watching adds is three guards, and each one is against a failure that
// only exists because the loop no longer ends. An item whose run failed before
// it ever started is left alone until something about the item changes, because
// a queue the harness cannot get past is one it would otherwise re-pull every
// interval for as long as it ran. Runs blocking one after another with nothing
// landing between them hold intake, because systemic breakage left overnight
// would otherwise put the whole backlog through a failed run each. And a session
// says what it is doing where somebody who is not at its terminal can read it,
// because an idle session and a dead one are the same silence.
//
// # A session that redeploys itself
//
// The fourth thing watching adds is the one failure the loop is itself. A
// session goes on choosing and dispatching from the binary it was started with
// while fixes land behind it, so the work it dispatches is spent against defects
// that were fixed hours before — which reads as agents failing rather than as a
// process nobody restarted. It cost three review rounds on 2026-08-30 against a
// bug that had been dead for a day, and on 2026-08-31 the session choosing work
// was found forty-three changes old.
//
// Nothing outside the process can close that. Killing a session cancels the run
// it is carrying, so an external job may only bounce it while nothing is
// running; with more than one developer slot and a deep queue the next run
// starts the moment one settles, and a poll at any interval never lands in that
// window. The session is the only thing standing in it.
//
// So the session takes the deploy up itself. When it finds that the binary it
// was started from has been replaced, it stops choosing, waits out the runs it
// already started, and stops with ScheduleRedeployed — which is the caller's
// signal to re-execute. A live run is never interrupted for it, and that is why
// this wins the race an external restart loses rather than being the same thing
// moved inside: the session declining to claim anything more is what makes the
// window exist at all, and nothing but the session can decline.
//
// A drain never does any of this. It is a command somebody is waiting on the
// return of, and restarting it would run the pass again from the beginning.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/staleness"
)

// scheduledStatuses are the tracker slices the backlog is assembled from: work
// that has been admitted and is not finished. Claimed work has been pulled
// already and closed work has left, so neither is still queued.
var scheduledStatuses = []string{"open", "blocked"}

// claimedStatus is the tracker slice that has left the backlog by being pulled.
// The scheduler does not choose from it, and reads it for one thing: a child
// somebody is running right now is the strongest possible cover over its
// parent's execution, and it is precisely the child a queue reading alone cannot
// see.
const claimedStatus = "in_progress"

// maxScheduleReasonBytes bounds one part of a recorded selection reason that
// came from a document rather than from this package. The reason as a whole has
// its own bound in the run state; this keeps a single amendment's prose from
// filling it.
const maxScheduleReasonBytes = 240

// maxCoveringChildrenNamed bounds how many children a deferral names before it
// falls back to counting them. The count stays exact either way.
const maxCoveringChildrenNamed = 3

// How a watch session rides through a reading of the harness that failed.
//
// The tracker is a database a reconcile and every settling run write to, so a
// reading that fails is contention far more often than it is a store that is
// broken. The one that ended a session on 2026-09-01 succeeded again in 0.4s a
// few minutes later, and what it cost was the session: it stopped on that single
// reading, and the queue sat until an external job noticed the process was gone
// and started another. A session that dies on one contended read defeats
// everything built on the session outliving the work it starts, the
// self-redeploy above first among it.
//
// So a watch waits and reads again, doubling the wait from the first delay to
// the longest, and stops only once the readings have gone on failing for the
// window. The window is the whole of what separates contention from breakage,
// which is why a session that stops on one says how long it tried rather than
// only what failed.
const (
	firstReadRetryDelay   = 2 * time.Second
	longestReadRetryDelay = 30 * time.Second
	readRetryWindow       = 5 * time.Minute
)

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
	// ScheduleBudgetSpent reports a session that reached the spend the operator
	// bounded it to. It is the operator bounding one session rather than the
	// harness running out of anything, which is why it reads like the limit
	// above rather than like a failure.
	ScheduleBudgetSpent = "the session spent the budget it was given"
	// ScheduleRedeployed reports a session that stopped to take up the binary
	// deployed over the one it was started from. It is not the end of the line:
	// the caller re-executes, and the session that comes back is watching the
	// same queue from the build that was deployed.
	ScheduleRedeployed = "a build was deployed over the one this session was started from, and the session is restarting into it"
	// ScheduleSpendUnreadable reports a bounded session that stopped because it
	// could not tell what it had spent. A budget measured against evidence
	// nobody can read is not a smaller budget, it is no budget at all, so the
	// session stops rather than carrying on inside a bound it has lost the
	// ability to hold. An unbounded pass is unaffected: nothing there was
	// spending against a number.
	ScheduleSpendUnreadable = "what this session had spent could not be read, and it was given a budget to stay inside"
)

// ScheduleTracker is the tracker access one pull needs: the work, by status, so
// the admitted part can be put in the product manager's order and the claimed
// part can say what is already being worked on, and the tracker's own account of
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

// ScheduleGates is the human gates a person has recorded passing. It is required
// rather than optional, and it is required of the pull rather than read from the
// tracker, because it is the one refusal the tracker cannot express: a step
// somebody reserved for themselves has no encoding there but an item to close,
// and machinery closing that item is how a reserved step was jumped once
// already. A pull that cannot read it schedules nothing rather than scheduling
// past a gate.
//
// It is satisfied by *runstate.Store.
type ScheduleGates interface {
	DischargedGates() ([]string, error)
}

// ScheduleStaleness reports the admitted work something upstream of changed
// after it was admitted. It is optional and it decides nothing: a pull wired
// without one schedules exactly the same items in exactly the same order, and
// what is lost is a sentence in each run's recorded reason.
type ScheduleStaleness interface {
	Stale(ctx context.Context) ([]staleness.WorkItem, error)
}

// ScheduleBrake is how a failure storm stops the line: the same intake hold an
// operator places, placed by the harness when runs keep blocking with nothing
// landing between them. It is satisfied by runstate.IntakeHoldStore.
//
// Only the placing is here. Nothing in this package releases a hold, whoever
// placed it: a brake the harness could lift by itself would stop the line
// exactly as long as it took the next run to fail, and what a held queue needs
// is a person, which is the whole reason for tripping it.
type ScheduleBrake interface {
	Hold(reason string, at time.Time) (runstate.IntakeHold, error)
}

// ScheduleSpend prices what a session has spent, from the same recorded run
// evidence `yoyo cost` reads.
//
// It is optional exactly as far as the budget is. A pass with no budget runs the
// same items without one and simply prices nothing; a pass given a budget is
// refused without one, because a bound nothing can measure is not a bound and
// must not be reported as one. The same rule holds once a session is running: a
// run whose evidence will not price stops a bounded session rather than being
// counted as free.
//
// It is satisfied by runstate.Store.
type ScheduleSpend interface {
	Price(workItemID string) (runstate.ItemPrice, error)
}

// ScheduleDeployment reports whether the binary this session is executing has
// been deployed over since it started. It is satisfied by redeploy.Binary.
//
// It is optional, and a session wired without one behaves exactly as every
// session did before: it goes on running what it was started with until somebody
// restarts it, which is the state the package comment above describes the cost
// of. It is asked once per pull and answers from one file reading, so an idle
// session pays for it what it pays for reading the queue.
type ScheduleDeployment interface {
	Replaced() (bool, error)
}

// SessionState is one transition a watch session records about itself: what it
// entered, when, why, and whether the stop it is recording is a session coming
// straight back rather than a line going down.
type SessionState struct {
	State  runstate.WatchState
	At     time.Time
	Reason string
	// Running is how many developer runs the pass could see in flight when it
	// recorded this, and Executor is the conversation that carries the work it
	// passed over where one does. They travel with the reason because they are the
	// two facts a reader of an idle line was missing: whether the harness is
	// nonetheless working, and who has to act before the answer changes.
	Running  int
	Executor domain.WorkItemExecutor
	// Unreadable marks the poll that chose nothing because the harness could not
	// be read at all. It travels for the same reason the two above do: nothing a
	// person admits, releases, or opens changes the answer while the store will
	// not answer, so a reader told to admit work would be told to do the one thing
	// that cannot help.
	Unreadable bool
	// Restarting marks the one stop that is not an ending — the session waiting
	// out its runs to be re-executed into a build deployed over it. It is what
	// keeps every surface from telling the operator to start a session that is
	// already on its way back, which is the standing chore this whole mechanism
	// exists to end rather than reproduce once per deploy.
	Restarting bool
}

// WatchSessions is where a watch session says what it is doing, for the reader
// who is not at its terminal. It is optional: a session wired without one
// behaves identically and is simply invisible between the runs it starts, which
// is the state this exists to end.
type WatchSessions interface {
	Record(SessionState) error
}

// ScheduleEscalations puts stopped work in front of the development manager,
// once per docketed stoppage. It is satisfied by Escalator.
//
// It is optional, and a pull wired without one pulls exactly the same work: what
// is lost is the delivery, so a run that stopped waits on somebody carrying it
// to her, which is what every pass did before this existed.
type ScheduleEscalations interface {
	Escalate(ctx context.Context) (EscalationSweep, error)
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
	// Gates is the human gates a person has passed; see ScheduleGates for why a
	// pull without one is refused rather than run.
	Gates ScheduleGates
	// Staleness is optional; see ScheduleStaleness for what a pull without one
	// loses, which is a sentence rather than a constraint.
	Staleness ScheduleStaleness
	// Capacity is execution.max_concurrent_developers as this pull read it. It
	// bounds how many runs the scheduler starts; the reservation enforces the
	// same number across every process, and this only keeps the scheduler from
	// walking into a refusal it can see coming.
	Capacity int
	// Poll is execution.work_poll as this pull read it: how long a watch session
	// waits before reading the queue again. It is read per pull like everything
	// else here, so an interval changed under a running session takes effect at
	// the next wait rather than at the next restart. A drain never waits and
	// never reads it.
	Poll time.Duration
	// BlockedRunsBeforeIntakeHold is execution.blocked_runs_before_intake_hold as
	// this pull read it: how many runs may block in a row, with nothing landing
	// between them, before the brake holds intake. Zero never brakes.
	BlockedRunsBeforeIntakeHold int
	// Brake places that hold. It is optional, and a session wired without one
	// counts the storm and reports it without stopping the line, because a brake
	// nothing can apply must not be reported as applied.
	Brake ScheduleBrake
	// Spend prices what the session has spent. It is required of a pass that was
	// given a budget and of no other; see ScheduleSpend.
	Spend ScheduleSpend
	// Escalations delivers stopped work to the development manager. Optional; see
	// ScheduleEscalations.
	Escalations ScheduleEscalations
	Start       Starter
}

// pullNeeds is what this pass will actually ask of a pull, which is not the same
// for every pass. A watch waits, so it needs an interval; a session spending
// against a number needs a way to read what it has spent. A drain asks for
// neither and is not refused for lacking either.
type pullNeeds struct {
	waits   bool
	bounded bool
}

func (p Pull) validate(needs pullNeeds) error {
	var problems []error
	if needs.waits && p.Poll <= 0 {
		problems = append(problems, fmt.Errorf("a watch interval of %s reads the queue with nothing between the readings", p.Poll))
	}
	// A budget with nothing to measure it against is the one refusal here that
	// is about the operator rather than about the harness: they asked for a
	// bound, and a pass that ran anyway would be reporting a bound it never had.
	if needs.bounded && p.Spend == nil {
		problems = append(problems, errors.New("a pull given a budget requires a way to price what it has spent"))
	}
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
	// A pull with no way to read the recorded human acts cannot tell a gate
	// somebody passed from one nobody has, so it would either hold every gated
	// item forever or start past all of them. Refusing here is the only reading
	// of that which is neither.
	if p.Gates == nil {
		problems = append(problems, errors.New("a pull requires the human gates a person has passed"))
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
	// Limit bounds how many runs one pass starts. Zero means no bound on the
	// count, which is what an unattended scheduler wants; an operator sitting in
	// front of one wants a number. What ends an unbounded pass is what it was
	// asked for: an empty queue for a drain, and the operator for a watch.
	Limit int
	// Watching keeps the pass open when it runs out of work instead of
	// returning: the queue is read again after the configured interval, and the
	// pass ends only when the operator stops it, when a bound it was given is
	// reached, or when the harness can no longer be read.
	Watching bool
	// Budget caps what one session may spend, in the provider's own reported
	// dollars, over everything the session spends on: the runs it starts, and the
	// turns it takes itself putting stopped work in front of the development
	// manager. Zero is unbounded, which is what a drain has always been. The bound
	// is checked between pulls rather than during a run or a turn: what is already
	// spent is spent, and what stopping part way would lose is the work it bought.
	Budget float64
	// Sessions is where the session's state transitions are recorded. Optional;
	// see WatchSessions.
	Sessions WatchSessions
	// Deployment is how a watching session finds out that the binary it is
	// executing has been deployed over. Optional; see ScheduleDeployment. It is
	// consulted only while watching, because a drain is a command somebody is
	// waiting on the return of rather than a process that outlives a deploy.
	Deployment ScheduleDeployment
	// Sleep waits out one poll interval and reports false when the context ended
	// first. It is injected so a test does not have to spend real seconds, and
	// defaults to a timer.
	Sleep func(ctx context.Context, interval time.Duration) bool
	// Now stamps the recorded transitions. It defaults to the wall clock.
	Now func() time.Time
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

// Deferred is one pullable item this pass declined to start, and why.
//
// Five things land here: an unresolved directive, an item whose unfinished
// children already carry its execution, an item that would have raced work
// already in flight over the same epic or the same files, an item whose
// executor is a persona conversation rather than a developer run, and an item
// somebody parked. Each is named
// against the item rather than counted, because each is a fact about that item
// that nothing else in the harness would report — the first needs a person, and
// the rest are the scheduler passing over something the tracker called ready.
// The third is a wait rather than a refusal: the item is pulled at the
// first pull where what it would have raced has ended, and the run that pulls it
// records having waited. The last two are the opposite of a wait, and say so: no
// pull will ever take either, and what moves them is somebody opening the
// conversation the item names, or the product manager releasing the parking.
// The other reasons an
// item is not started — the tracker not calling it ready, a run for it already
// being in flight, no free developer slot — are facts about the pass rather than
// about any one item, and the counts on the schedule report them at that grain.
// A line per unready item would be a line per backlog entry on every pass, which
// is how a listing stops being read at all.
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
	// Admitted and Pullable are the size of the backlog and how much of it the
	// tracker called ready, as of the last pull that got far enough to read the
	// queue — a pull that found every slot taken stops before that, and leaves
	// these where the pull before it put them.
	//
	// They are the pass-level answer to why an item was not started, and they are
	// counts rather than a list on purpose: "nothing more is ready to pull" is a
	// different fact when the backlog is empty and when forty admitted items are
	// all waiting on something, and naming those forty every pass would bury the
	// deferrals that actually need reading.
	Admitted int `json:"admitted"`
	Pullable int `json:"pullable"`
	// BacklogRead reports that some pull got as far as reading the queue. Without
	// it, counts of zero would be indistinguishable from a pass that stopped
	// before it ever looked — a held intake, or a machine already full — and
	// "0 admitted items" over a backlog nobody read is the confident emptiness
	// every report in this harness is written to avoid.
	BacklogRead bool `json:"backlog_read"`
	// Stopped says why the scheduler stopped pulling, in the words of one of the
	// Schedule* reasons above.
	Stopped string `json:"stopped"`
	// StalenessProblem names a staleness reading that failed. It costs the
	// recorded reasons a sentence and costs the schedule nothing else, so it is
	// reported beside the pass rather than failing it.
	StalenessProblem string `json:"staleness_problem,omitempty"`
	// Watched reports a pass that stayed open rather than draining, and Polls
	// counts the intervals it waited out. A session that started nothing and
	// polled four hundred times is a session that was alive, which is the fact a
	// schedule read afterwards would otherwise be missing.
	Watched bool `json:"watched,omitempty"`
	Polls   int  `json:"polls,omitempty"`
	// Escalated is the stopped work this pass put in front of the development
	// manager, and what she recorded about it. It is on the schedule for the
	// reason the started runs are: a pass that woke a role and spent a turn doing
	// it is a pass that did something, and an operator reading what the session
	// did must not have to infer it from her conversation.
	Escalated []Escalated `json:"escalated,omitempty"`
	// EscalationProblem names a delivery that did not reach her. It costs the
	// pass nothing it was doing, so it is reported beside the pull rather than
	// stopping it — but never left unsaid, because stopped work nobody was told
	// about is exactly what the delivery exists to prevent.
	EscalationProblem string `json:"escalation_problem,omitempty"`
	// Braked is the intake hold this session's own failure-storm brake placed,
	// and BlockedInARow is what tripped it. Nothing here lifts it: a held queue
	// needs a person, which is the whole reason for holding it.
	Braked        *runstate.IntakeHold `json:"braked,omitempty"`
	BlockedInARow int                  `json:"blocked_in_a_row,omitempty"`
	// BrakeProblem names a brake that could not be applied — no way to place the
	// hold, or a hold that would not be written. The storm is still counted and
	// still reported, because a brake that failed is exactly the thing an
	// operator must not find out about by inferring it from the silence.
	BrakeProblem string `json:"brake_problem,omitempty"`
	// SpentUSD is what this pass spent, as the provider reported it: the runs it
	// started, and the turns it took itself putting stopped work in front of the
	// development manager. Budget is what it was allowed. Both are absent from a
	// pass nobody bounded and nothing priced.
	SpentUSD float64 `json:"spent_usd,omitempty"`
	Budget   float64 `json:"budget,omitempty"`
	// SpendProblem names run evidence that could not be priced. On an unbounded
	// pass it is a note beside a total that is a floor rather than an exact
	// number; on a bounded one it is why the session stopped, because a budget
	// measured against evidence nobody can read is no budget at all.
	SpendProblem string `json:"spend_problem,omitempty"`
	// RedeployProblem names a reading of the session's own binary that failed, so
	// whether a build has been deployed over it is a question nobody answered. It
	// costs the session its self-redeployment and nothing else, so it is reported
	// beside the pass rather than stopping it: a session that stopped choosing
	// work because it could not stat a file would be a worse failure than the
	// staleness it is guarding against.
	RedeployProblem string `json:"redeploy_problem,omitempty"`
	// SessionProblem names a transition that could not be recorded. It costs the
	// session its visibility rather than its work, so it is reported beside the
	// pass rather than failing it — the alternative is a session that stops
	// working because nobody could be told it was working.
	SessionProblem string `json:"session_problem,omitempty"`
	// ReadsRetried counts the readings of the harness that failed and were made
	// again rather than stopping the session, and ReadProblem names the last of
	// them. Both are for the reader afterwards: a reading that succeeded on the
	// second attempt leaves nothing at all behind, so a session that rode out a
	// store outage overnight would otherwise be a session nobody could tell had
	// met one.
	ReadsRetried int    `json:"reads_retried,omitempty"`
	ReadProblem  string `json:"read_problem,omitempty"`
	// ReadFailure is the reading the session finally stopped on, and how long the
	// harness had gone on being unreadable when it did.
	//
	// It is a field of its own rather than the last value of ReadProblem because
	// the two are facts about different moments, and only this one is why the pass
	// ended. A pass stops as unreadable for a second reason — a pull that
	// assembles and is unusable — and a session that had ridden through a
	// contended reading an hour earlier would otherwise report that reading as
	// what stopped it, in the log this exists to make worth trusting.
	ReadFailure string `json:"read_failure,omitempty"`
}

// Schedule pulls ready work and runs it, up to the capacity the configuration
// allows. A drain returns once every run it started has ended; a watch stays
// open, waiting out the configured interval whenever it finds nothing to start,
// and returns when the operator stops it or a bound it was given is reached.
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
	if s.Budget < 0 {
		return Schedule{}, fmt.Errorf("a session budget of %.2f spends nothing", s.Budget)
	}

	schedule := Schedule{Watched: s.Watching, Budget: s.Budget}
	session := s.session(&schedule)
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
	// tried is every item this pass has already started, against the item as it
	// read at the time. A drain never looks at that reading: nothing is ever
	// removed, because a run that ends without moving the item out of the ready
	// queue would otherwise be chosen again on the next pull.
	//
	// A watch cannot afford that rule in either direction. Keeping an item out
	// for the life of a session that never ends is a queue the session can never
	// get back to; letting it back in unconditionally is the failure that
	// provoked this guard — an item whose run fails before it starts leaves the
	// item exactly as ready as it was, and a loop with no memory would re-pull it
	// every interval until somebody noticed. So a watch remembers what the item
	// looked like and tries it again when that changes, which is the same thing a
	// person means by "nothing has changed, don't try again".
	tried := make(map[string]string)
	// deferred is the items already named on the schedule as passed over — paused
	// by a directive, or covered by children carrying their execution. It bounds
	// the report rather than the choosing: both are re-read at every pull, so an
	// item stops being deferred the moment somebody resolves what paused it or
	// closes what covered it.
	deferred := make(map[string]bool)
	// sequencedEarlier is the items this pass passed over because starting them
	// would have raced work already in flight, against the conflict that held
	// each one. It
	// decides nothing — every pull re-reads what is actually in flight — and is
	// remembered so that the selection which eventually pulls one can say it
	// waited, which is the only place that fact survives the pass.
	sequencedEarlier := make(map[string]conflict)
	// blockedInARow counts the runs that ended blocked with nothing landing
	// between them. It is the storm the brake watches for, and it is reset by any
	// run that finishes: one item failing is not a systemic failure, and the
	// per-item guard above is what that case is for.
	blockedInARow := 0
	// spend is how the runs this session started are priced, taken from the last
	// pull that could be opened. It is held outside the loop because a run
	// collected after the final pull still cost what it cost.
	var spend ScheduleSpend
	// redeploying is the session having found a build deployed over the one it is
	// executing. From that point it claims nothing more and waits out what it
	// already started, which is the whole of how a restart reaches the machine
	// without cancelling a run.
	redeploying := false
	// retries is the run of harness readings that have failed with none
	// succeeding between them. See readRetries: it is what lets a watch ride
	// through the store contention a reconcile or a settling run makes, and what
	// stops the session once the failures have gone on too long to be contention.
	var retries readRetries
	running := 0

	// settle takes one finished run into the schedule: what became of it, what it
	// cost, and what it does to the storm the brake is counting.
	settle := func(done completed) {
		started := &schedule.Started[done.index]
		started.record(done)
		switch {
		case started.Declined != "":
			// The work went to another process, which is two schedulers doing
			// exactly what they should. It says nothing about the machine and
			// nothing about the item, so it neither counts nor clears.
		case started.blockedRun():
			blockedInARow++
			if blockedInARow > schedule.BlockedInARow {
				schedule.BlockedInARow = blockedInARow
			}
		case started.Outcome.Paused:
			// A parked run is owed a continuation rather than having failed.
		default:
			blockedInARow = 0
		}
		if cost, problem := priceRun(spend, started.Outcome); problem != "" {
			schedule.SpendProblem = problem
		} else {
			schedule.SpentUSD += cost
		}
	}

	// collect takes one finished run. It reports false when the context ended
	// first, which stops the pulling; the runs still in flight are waited out
	// below either way.
	collect := func() bool {
		select {
		case done := <-completions:
			running--
			delete(mine, schedule.Started[done.index].WorkItemID)
			settle(done)
			return true
		case <-ctx.Done():
			return false
		}
	}

	// wait is what a watch does instead of concluding the queue is empty: it
	// collects a run of its own if one is still going, because a run finishing
	// changes the answer sooner than any interval would, and otherwise sleeps out
	// the interval this pull read. It reports false when the context ended, which
	// is the operator stopping the session.
	wait := func(pull Pull, state runstate.WatchState, said account) bool {
		session.enter(state, said)
		if running > 0 {
			return collect()
		}
		schedule.Polls++
		return s.sleep(ctx, pull.Poll)
	}

	var failure error
	// unreadable takes a reading of the harness that failed and says whether the
	// pass carries on. It sets what stopped the pass, and the failure it stopped
	// with, wherever it does not — so a caller handed false has only to break.
	//
	// A drain stops on the first one, exactly as it always has: it is a command
	// somebody is waiting on the return of, and one that slept through a store
	// outage would be one that hung. A watch waits and reads again, and stops only
	// once the readings have gone on failing past the window. See the retry
	// constants for what that is worth and what it cost to find out.
	unreadable := func(err error) bool {
		if !s.Watching {
			schedule.Stopped = ScheduleUnreadable
			failure = err
			return false
		}
		retries.failed = true
		now := s.now()
		if retries.since.IsZero() {
			retries.since = now
			retries.delay = firstReadRetryDelay
		}
		retries.attempts++
		if tried := now.Sub(retries.since); tried >= readRetryWindow {
			schedule.Stopped = ScheduleUnreadable
			schedule.ReadFailure = fmt.Sprintf(
				"the harness went on being unreadable for %s across %d reading(s), which is longer than a store somebody else is writing to takes to come back, so the session stopped rather than polling something it cannot read: %v",
				tried.Round(time.Second), retries.attempts, err)
			failure = errors.New(schedule.ReadFailure)
			return false
		}
		schedule.ReadsRetried++
		schedule.ReadProblem = fmt.Sprintf(
			"reading %d, failing for %s, read again in %s: %v",
			retries.attempts, now.Sub(retries.since).Round(time.Second), retries.delay, err)
		// What the session says about itself leaves the changing numbers of the
		// outage out — which attempt this is, how long it has been failing — so an
		// outage is one line in the log rather than one per attempt. What they are
		// is on the schedule, which is where a reader afterwards looks.
		//
		// The runs it already has going are not one of those numbers. They are the
		// difference between a line that has stopped and a line that is working
		// while one reading fails, and they are named for the same reason the idle
		// account below names them.
		outage := fmt.Sprintf(
			"the harness could not be read and is being read again for up to %s before the session gives up on it: %v",
			readRetryWindow, err)
		if running > 0 {
			outage = fmt.Sprintf("%s in flight; %s", plural(running, "run", "runs"), outage)
		}
		session.enter(runstate.WatchIdle, account{reason: outage, running: running, unreadable: true})
		if !s.sleep(ctx, retries.delay) {
			schedule.Stopped = ScheduleCancelled
			return false
		}
		retries.delay = min(retries.delay*2, longestReadRetryDelay)
		return true
	}

	session.enter(runstate.WatchWatching, account{reason: s.opening()})
pulling:
	for {
		// A pull that read everything it needed clears the failures behind it. What
		// the window measures is a store that has gone on being unreadable, rather
		// than a count of the contended readings one session met over a long life.
		if !retries.failed {
			retries = readRetries{}
		}
		retries.failed = false
		if s.Limit > 0 && len(schedule.Started) >= s.Limit {
			schedule.Stopped = ScheduleLimitReached
			break
		}
		if s.Budget > 0 && schedule.SpentUSD >= s.Budget {
			schedule.Stopped = ScheduleBudgetSpent
			break
		}
		// A bounded session that has lost the ability to measure itself stops
		// here rather than at the end. Carrying on would be spending against a
		// number nothing is comparing anything to, and the operator would find
		// out in the morning — from a field on a schedule this session does not
		// return until it is over.
		if s.Budget > 0 && schedule.SpendProblem != "" {
			schedule.Stopped = ScheduleSpendUnreadable
			break
		}
		if ctx.Err() != nil {
			schedule.Stopped = ScheduleCancelled
			break
		}
		// Whether a build has been deployed over this one, asked before anything
		// is chosen and before the configuration is even read. It is asked here
		// rather than after a run settles because those are the same moment seen
		// from either side — this is the top of the loop a settled run returns to,
		// and it is the last point before the next item is claimed.
		if !redeploying && s.Watching && s.Deployment != nil {
			replaced, err := s.Deployment.Replaced()
			switch {
			case err != nil:
				if schedule.RedeployProblem == "" {
					schedule.RedeployProblem = fmt.Sprintf("whether a build has been deployed over this session could not be read, so it goes on running what it was started with: %v", err)
				}
			case replaced:
				redeploying = true
			}
		}
		if redeploying {
			// A live run is never interrupted for a redeploy. Waiting one out is
			// bounded by the run rather than by the queue, because the session has
			// already stopped claiming: the window an external restart could never
			// find is one this makes rather than waits for.
			if running > 0 {
				if !collect() {
					schedule.Stopped = ScheduleCancelled
					break
				}
				continue
			}
			schedule.Stopped = ScheduleRedeployed
			break
		}
		pull, err := s.Open(ctx)
		if err != nil {
			if !unreadable(fmt.Errorf("open a pull: %w", err)) {
				break
			}
			continue
		}
		// A pull that was assembled and is not usable is a decision somebody made
		// about the configuration rather than a reading that failed, so it stops the
		// pass at once. Waiting out a window for a capacity of zero to become
		// something else is a session that hangs, not one that rides out contention.
		if err := pull.validate(pullNeeds{waits: s.Watching, bounded: s.Budget > 0}); err != nil {
			failure = fmt.Errorf("open a pull: %w", err)
			schedule.Stopped = ScheduleUnreadable
			break
		}
		spend = pull.Spend
		// Stopped work reaches the development manager here, rather than by
		// somebody carrying it to her. It is done before the brake and before the
		// hold, because it chooses nothing and starts nothing: what it produces is
		// her judgment about work that has already stopped, which is exactly what a
		// held or braked queue is usually waiting on. What it delivers is bounded to
		// one stoppage per pass; see Escalator.
		s.escalate(ctx, &schedule, pull)
		// The brake is applied before the hold is read, so the reading that
		// follows is what stops the choosing whether the operator held intake or
		// this session did. Nothing else in the loop knows the difference, which
		// is the point: a brake that stopped the line by its own separate path
		// would be a second account of a rule that already has one.
		if s.Watching && blockedInARow > 0 && pull.BlockedRunsBeforeIntakeHold > 0 && blockedInARow >= pull.BlockedRunsBeforeIntakeHold {
			s.brake(&schedule, pull, blockedInARow)
			blockedInARow = 0
		}
		// The intake hold is read before anything is chosen, because choosing is
		// the whole of what it holds. It is asked again on every pull rather than
		// once for the pass: the hold that matters is the one the operator places
		// while the scheduler is running, and a pass that answered from its first
		// reading would keep choosing work for as long as it lasted.
		hold, held, err := pull.Intake.Held()
		if err != nil {
			if !unreadable(fmt.Errorf("read whether the operator has held intake: %w", err)) {
				break
			}
			continue
		}
		if held {
			schedule.IntakeHeld = &hold
			if !s.Watching {
				schedule.Stopped = ScheduleIntakeHeld
				break
			}
			// A held intake is a brake rather than a stop: the session keeps
			// polling and chooses nothing, and resumes in place when it is
			// released. That is what makes holding intake something an operator
			// can do to a session they are not sitting at.
			if !wait(pull, runstate.WatchBraked, account{
				reason: brakedReason(schedule.Braked != nil, hold),
				// This session's own runs: a held intake stops the choosing and
				// interrupts nothing, and the reader has to be able to tell those apart.
				// What another process has going is not read until after the hold is.
				running: running,
			}) {
				schedule.Stopped = ScheduleCancelled
				break
			}
			continue
		}
		schedule.IntakeHeld = nil

		occupied, err := occupiedItems(pull.Runs)
		if err != nil {
			if !unreadable(err) {
				break
			}
			continue
		}
		for id := range mine {
			occupied[id] = struct{}{}
		}
		schedule.Capacity = pull.Capacity
		schedule.Occupied = len(occupied)
		free := pull.Capacity - len(occupied)
		if free < 1 {
			if running > 0 {
				if !collect() {
					schedule.Stopped = ScheduleCancelled
					break
				}
				continue
			}
			if !s.Watching {
				schedule.Stopped = ScheduleCapacityFull
				break
			}
			if !wait(pull, runstate.WatchIdle, account{
				reason:  "every developer slot is held by a run this session did not start",
				running: len(occupied),
			}) {
				schedule.Stopped = ScheduleCancelled
				break
			}
			continue
		}

		read, err := pull.queue(ctx)
		if err != nil {
			if !unreadable(err) {
				break
			}
			continue
		}
		queue := read.queue
		schedule.Admitted = len(queue.Entries)
		schedule.Pullable = queue.Ready()
		schedule.BacklogRead = true
		stale, stalenessProblem := pull.stale(ctx)
		if stalenessProblem != "" {
			schedule.StalenessProblem = stalenessProblem
		}

		// What the runs already going have taken, so nothing that would race one
		// is started beside it. It is seeded from every item in flight anywhere —
		// this pass's own runs and another process's alike — and grows as this
		// pull starts things.
		flight := newInFlight()
		for id := range occupied {
			flight.take(read.items[id])
		}
		// sequenced names the items this pull has held back for a conflict so
		// far, in the order the product manager set. An item started after one of
		// them was pulled ahead of it, and its recorded reason says so.
		var sequenced []string
		// What this pull passed over and why, assembled as it goes so that a poll
		// which starts nothing can say what it actually found rather than only that
		// it found nothing. It is this pull's own reading and is discarded with it.
		poll := idlePoll{}

		started := 0
		for _, entry := range queue.Entries {
			if started == free {
				break
			}
			if s.Limit > 0 && len(schedule.Started) >= s.Limit {
				break
			}
			if !entry.Ready {
				// Unready entries are counted rather than listed, with two exceptions:
				// an item no developer run can carry, and an item somebody parked.
				// Neither is waiting for anything, so the count they would otherwise
				// disappear into — work that will become pullable — is a count neither
				// of them will ever join. Each is named once, like every other
				// deferral, and what it names is what somebody does about it.
				if reason, named := passedOverReason(entry); named && !deferred[entry.ID] {
					deferred[entry.ID] = true
					schedule.Deferred = append(schedule.Deferred, Deferred{WorkItemID: entry.ID, Reason: reason})
				}
				poll.pass(entry.ID, unreadyClass(entry), entry.Executor.Role())
				continue
			}
			if s.cooling(tried, read.items[entry.ID]) {
				poll.pass(entry.ID, idleAlreadyTried, "")
				continue
			}
			if _, busy := occupied[entry.ID]; busy {
				poll.pass(entry.ID, idleAlreadyInFlight, "")
				continue
			}
			// A decomposed item is not itself a run. Its children are where the work
			// went, and starting the parent beside them buys the same change a second
			// time — two developers rewriting one file, the second of them guaranteed
			// a conflict at integration. Nothing downstream would catch it: the
			// reservation sees two different items, and the tracker reports the parent
			// as ready because nothing blocks it.
			//
			// A child covers whether it is queued or already claimed, because both are
			// work that has not been done yet. This is re-read at every pull like
			// everything else, so an item stops being covered when its last child
			// closes, and one that is decomposed while the session watches stops being
			// pullable at the next selection.
			if covering := read.children[entry.ID]; len(covering) > 0 {
				if !deferred[entry.ID] {
					deferred[entry.ID] = true
					schedule.Deferred = append(schedule.Deferred, Deferred{
						WorkItemID: entry.ID,
						Reason:     coveredReason(covering),
					})
				}
				poll.pass(entry.ID, idleCoveredByChildren, "")
				continue
			}
			// An unresolved directive stops the work whether it is read here or in
			// the pipeline, so this is not the enforcement — it is the scheduler
			// declining to spend a slot on a start it can see will not proceed, and
			// saying which directive it was.
			pausing, err := pull.Directives.Pausing(entry.ID)
			if err != nil {
				if !unreadable(fmt.Errorf("read the directives that pause %s: %w", entry.ID, err)) {
					break pulling
				}
				// The items this pull already started keep running and are collected
				// like any other; the pull that follows re-reads the queue and passes
				// over them because they are in flight.
				continue pulling
			}
			if len(pausing) > 0 {
				// The directive is re-read at every pull rather than remembered,
				// because what clears it is a person and the whole point of a pass
				// that outlives them answering is that it notices. What is
				// remembered is only that this was said: an item paused all night
				// is one line in the report rather than one per poll.
				if !deferred[entry.ID] {
					deferred[entry.ID] = true
					schedule.Deferred = append(schedule.Deferred, Deferred{
						WorkItemID: entry.ID,
						Reason:     "an unresolved directive pauses it: " + pausing[0].Summary(),
					})
				}
				poll.pass(entry.ID, idlePausedByDirective, "")
				continue
			}
			// Work that would race something already going is sequenced behind it
			// rather than started beside it. This enforces nothing — the promotion
			// lease and the replay still do all of that — and buys the difference
			// between a wait and a replayed, re-checked, re-reviewed run. It is
			// re-read at every pull like everything else here, so an item held back
			// now is pulled at the first pull where the run it would have raced has
			// ended.
			if racing, races := flight.against(read.items[entry.ID]); races {
				if !deferred[entry.ID] {
					deferred[entry.ID] = true
					schedule.Deferred = append(schedule.Deferred, Deferred{
						WorkItemID: entry.ID,
						Reason:     racing.reason(),
					})
				}
				sequencedEarlier[entry.ID] = racing
				sequenced = append(sequenced, entry.ID)
				poll.pass(entry.ID, idleSequencedBehindWork, "")
				continue
			}
			delete(deferred, entry.ID)
			tried[entry.ID] = fingerprint(read.items[entry.ID])

			// Only an item that was actually held back carries the first half of
			// this; the second is whatever this pull passed over ahead of it.
			ordering := sequencing{after: sequencedEarlier[entry.ID], ahead: append([]string(nil), sequenced...)}
			delete(sequencedEarlier, entry.ID)

			index := len(schedule.Started)
			selection := runstate.Selection{
				By:     runstate.SelectedByScheduler,
				Reason: scheduleReason(entry, queue, free, pull.Capacity, stale[entry.ID], ordering),
			}
			schedule.Started = append(schedule.Started, Started{WorkItemID: entry.ID, Reason: selection.Reason})
			flight.take(read.items[entry.ID])
			mine[entry.ID] = index
			running++
			started++
			go func(workItemID string) {
				outcome, err := pull.Start(ctx, workItemID, selection)
				completions <- completed{index: index, outcome: outcome, err: err}
			}(entry.ID)
		}
		if started > 0 {
			session.resume(fmt.Sprintf("%d item(s) pulled from a backlog of %d admitted, %d of them ready", started, len(queue.Entries), queue.Ready()))
			continue
		}
		// Nothing was startable this pull. With runs of ours still going, one of
		// them finishing changes the answer — it frees a slot, and it may close the
		// item something else was waiting on — so the pass waits rather than
		// concluding the queue is empty.
		if running == 0 && !s.Watching {
			schedule.Stopped = ScheduleDrained
			break
		}
		// What this poll actually found, rather than the bare fact that it started
		// nothing: the runs already going, the items passed over and why, and the
		// conversation that has to act where one does.
		if !wait(pull, runstate.WatchIdle, account{
			reason:   poll.reason(queue, len(occupied)),
			running:  len(occupied),
			executor: poll.carrier(),
		}) {
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
		settle(done)
	}
	// The last line, and whether it is an ending. A session stopping to be
	// restarted into the build deployed over it is waiting on nothing and nobody,
	// and a reader told otherwise is being handed the chore this exists to end.
	session.stop(stopping(schedule), schedule.Redeploying())
	return schedule, failure
}

// stopping is what the session's last recorded line says: why it stopped, and
// the detail behind it where the reason alone would not be actionable. A session
// that stopped because it could not price itself is read in a channel rather
// than in this process's terminal, so the run nothing could price has to travel
// with the reason rather than staying on a schedule nobody there sees.
//
// A session that stopped because the harness would not be read travels the same
// way and for a sharper reason: what tells an operator whether they are looking
// at a broken store or at one bad minute is how long it went on failing, and the
// reason alone says neither.
func stopping(schedule Schedule) string {
	switch {
	case schedule.Stopped == ScheduleSpendUnreadable && schedule.SpendProblem != "":
		return schedule.Stopped + ": " + schedule.SpendProblem
	case schedule.Stopped == ScheduleUnreadable && schedule.ReadFailure != "":
		return schedule.Stopped + ": " + schedule.ReadFailure
	}
	return schedule.Stopped
}

// readRetries is the run of harness readings that have failed with none
// succeeding between them: when the first of them was, how many there have been,
// and how long the next wait is.
//
// It is cleared by a pull that read everything it needed rather than by a single
// successful reading, because what the window measures is a store that has gone
// on being unreadable — a pull whose queue read succeeds and whose directive read
// fails is not a store that came back.
type readRetries struct {
	since    time.Time
	attempts int
	delay    time.Duration
	// failed marks the pull being made now as having had a reading fail, which is
	// what keeps a pull that failed from clearing itself at the top of the loop.
	failed bool
}

// cooling reports an item this pass has already started and should not start
// again. A drain never starts one twice at all. A watch starts one again when
// the item itself has changed, and not otherwise: what a failed start leaves
// behind is an item exactly as it was, and re-reading it will produce exactly
// the same failure until somebody edits the work, reprioritizes it, or unblocks
// what it depends on.
//
// It covers every item the session has started rather than only the starts that
// failed, and that is deliberate. The case it must not miss is the one that
// leaves an item pullable and unclaimed with nothing recorded anywhere — a run
// the operator's hold stopped before it claimed anything — where starting again
// produces the same non-result immediately and with no wait in between. Narrowed
// to failed starts, that case spins.
func (s Scheduler) cooling(tried map[string]string, item beads.WorkItem) bool {
	recorded, attempted := tried[item.ID]
	if !attempted {
		return false
	}
	return !s.Watching || recorded == fingerprint(item)
}

// fingerprint is what "something about the item changed" means: every part of
// the item a person steers with — what the work says, what it is for, where it
// sits in the queue, what it waits on — and the notes, which is where the
// harness records what became of a run.
//
// The notes are the half that is not obvious, and they are what makes the
// ordinary recovery work. A run that stops on a blocker takes the item out of
// the ready queue and writes the blocker into its notes; a development manager
// who then unblocks it without editing anything leaves every other field exactly
// as this session first read it, and a fingerprint that ignored the notes would
// have the session refusing to pull work somebody had just deliberately
// released. Reading them costs the guard nothing, because the one case it exists
// for — a start that fails before a run claims anything — writes to the tracker
// not at all: the harness only ever appends to the notes of an item it has
// claimed, blocked, or closed, and none of those is an item still sitting
// pullable in the queue. A change that made the harness write to a pullable
// item's notes would put that spin back.
func fingerprint(item beads.WorkItem) string {
	parts := []string{
		item.Status,
		fmt.Sprint(item.Priority),
		item.Title,
		item.Description,
		item.Design,
		item.AcceptanceCriteria,
		item.Notes,
		item.Assignee,
		item.Parent,
	}
	// A dependency is named rather than counted, because one swapped for another
	// is a different piece of work with the same arithmetic.
	for _, dependency := range item.Dependencies {
		parts = append(parts, dependency.ID, dependency.Type)
	}
	return strings.Join(parts, "\x00")
}

// brake holds intake because runs kept blocking with nothing landing between
// them. What it places is the operator's own switch, which is deliberate: an
// operator arriving at a stopped line finds one thing to understand and one
// thing to lift, rather than a second mechanism that stops work in a way only
// this package knows how to undo.
func (s Scheduler) brake(schedule *Schedule, pull Pull, blocked int) {
	reason := fmt.Sprintf(
		"the harness held intake: %d run(s) blocked in a row with nothing landing between them, which is the configured brake at %d. Nothing further is chosen until this is released; runs already going carry on.",
		blocked, pull.BlockedRunsBeforeIntakeHold)
	if pull.Brake == nil {
		schedule.BrakeProblem = "runs kept blocking and nothing was wired to hold intake, so the line was left choosing work: " + reason
		return
	}
	held, err := pull.Brake.Hold(reason, s.now())
	if err != nil {
		schedule.BrakeProblem = fmt.Sprintf("intake could not be held after %d run(s) blocked in a row, so the line is still choosing work: %v", blocked, err)
		return
	}
	schedule.Braked = &held
}

// escalate puts the oldest stopped run the development manager has not been
// shown in front of her, and records what came back on the schedule.
//
// Nothing here stops the pass. A delivery that failed costs the pass nothing it
// was doing — no work was chosen by it and none is withheld for it — so it is
// reported beside the pull rather than in place of it, exactly as a staleness
// reading or a brake that could not be placed is. What must not happen is
// silence: a stoppage that reached nobody is the state this exists to end.
//
// What it does cost is the pull's own thread while the turn is taken, which is
// the one thing worth knowing before reading further: runs already going are
// untouched, but one that finishes mid-delivery is collected when the delivery
// returns rather than at once, so a free slot is refilled a turn later than it
// would have been. That is bounded by the turn and by one delivery per pass, and
// it is the trade the placement was chosen for — a run that delivered its own
// stoppage would hold a developer slot instead, which is the same wait taken out
// of the scarcer thing.
func (s Scheduler) escalate(ctx context.Context, schedule *Schedule, pull Pull) {
	if pull.Escalations == nil {
		return
	}
	sweep, err := pull.Escalations.Escalate(ctx)
	var problems []string
	if err != nil {
		problems = append(problems, fmt.Sprintf("stopped work could not be put to the development manager, so it is waiting on somebody carrying it to her: %v", err))
	}
	delivered := false
	for _, escalated := range sweep.Escalated {
		// What the delivery cost is the session's spend, exactly as a run's is. It
		// is counted whichever way the turn went and before anything else is
		// decided about it, because the provider charged for it either way — and a
		// session bounded by a budget that spent past it on turns nothing counted
		// would be the operator's cap disappearing quietly, which is the one thing
		// a bound must not do. The bound itself is read at the top of the next
		// pull, like every other spend this session makes.
		schedule.SpentUSD += escalated.CostUSD
		// A delivery that happened is kept, because it is one of the things this
		// pass did and there are as many of them as there were stoppages. One that
		// did not happen is a problem rather than an event, and the problems are
		// this sweep's rather than every sweep's: a session polling all night
		// against a conversation nothing can open would otherwise report the same
		// failure a thousand times.
		if escalated.Delivered {
			schedule.Escalated = append(schedule.Escalated, escalated)
			delivered = true
		}
		if escalated.Problem != "" {
			problems = append(problems, escalated.Problem)
		}
	}
	// What the pass says is replaced by what this sweep found, and cleared only by
	// a delivery that actually happened. A sweep that found nothing to say is not
	// evidence that the failure before it was resolved — the stoppage may simply
	// be waiting out its retry delay — and a pass that erased its own account of
	// having failed to reach her would end reporting nothing at all about stopped
	// work, which is the silence this exists to end. A stoppage the harness has
	// given up on is restated by every sweep, so what stands at the end of a pass
	// is what is still true.
	switch {
	case len(problems) > 0:
		schedule.EscalationProblem = strings.Join(problems, "; ")
	case delivered:
		schedule.EscalationProblem = ""
	}
}

// priceRun is what one finished run cost, from the recorded evidence. A run that
// never got as far as a record is priced at nothing because there is nothing to
// price, which is the truth about a start that failed before it began; evidence
// that exists and cannot be read is reported rather than assumed to be free, and
// under a budget that report is what stops the session.
//
// A pass with nothing to price with reaches here only when nothing is bounded,
// because a budget without a way to measure it is refused at the pull. So the
// nil is silence about a pass nobody asked to bound rather than a bound quietly
// dropped.
func priceRun(spend ScheduleSpend, outcome Outcome) (float64, string) {
	if spend == nil || strings.TrimSpace(outcome.RunID) == "" {
		return 0, ""
	}
	price, err := spend.Price(outcome.WorkItemID)
	if err != nil {
		return 0, fmt.Sprintf("what run %s cost could not be read, so the session's spend is a floor rather than a total: %v", outcome.RunID, err)
	}
	for _, run := range price.Runs {
		if run.RunID != outcome.RunID {
			continue
		}
		if !run.Known() {
			return 0, fmt.Sprintf("run %s left no evidence to price, so the session's spend is a floor rather than a total: %s", run.RunID, run.Unknown)
		}
		return run.CostUSD, ""
	}
	return 0, fmt.Sprintf("run %s is not among the recorded runs of %s, so the session's spend is a floor rather than a total", outcome.RunID, outcome.WorkItemID)
}

// blockedRun reports a run that ended without getting its work anywhere: it
// failed outright, or it stopped on a durable blocker. Both are the storm the
// brake counts, and neither is a run somebody is owed a continuation of.
func (s Started) blockedRun() bool {
	return s.Failure != "" || s.Outcome.Blocked
}

// maxIdleItemsNamed bounds how many items one class of an idle account names
// before it falls back to counting the rest. The count stays exact either way,
// for the reason every listing here is bounded: a backlog of forty deferred
// items would otherwise put forty identifiers into a line whose whole job is to
// be read at a glance.
const maxIdleItemsNamed = 5

// idleClass is one reason a poll left an item where it was, in the words the
// idle line names it by. The set is closed, and every place the pull loop passes
// an item over names one of them: a class nobody named is an item that
// disappears into a count saying something else about it, which is the
// misreading this whole account exists to end.
type idleClass string

const (
	idleCarriedInConversation idleClass = "carried in conversation"
	idleParked                idleClass = "parked"
	idleWaitingOnAPerson      idleClass = "waiting on a person"
	idleWaitingOnOtherWork    idleClass = "waiting on other work"
	idleAlreadyTried          idleClass = "already tried this session"
	idleAlreadyInFlight       idleClass = "already in flight"
	idleCoveredByChildren     idleClass = "covered by its children"
	idlePausedByDirective     idleClass = "paused by a directive"
	idleSequencedBehindWork   idleClass = "sequenced behind work in flight"
)

// idlePassedOver is one item a poll did not start: which item, why, and the
// conversation that carries it where the class names one.
type idlePassedOver struct {
	id    string
	class idleClass
	role  domain.AgentRole
}

// idlePoll is what one pull found while it started nothing, in the product
// manager's own order: every item it met, and the class it met it in.
//
// It is this pull's reading rather than the session's memory, and that is the
// whole of what makes it worth printing. The line it renders is read at the
// moment somebody is deciding whether the harness is working at all, so a count
// carried over from an earlier poll would be an account of a queue that has
// since changed.
type idlePoll struct {
	passed []idlePassedOver
}

// pass records one item this poll left where it was. The role is empty for every
// class but the conversation-carried one, which is the only class whose answer
// is a person rather than a wait.
func (p *idlePoll) pass(id string, class idleClass, role domain.AgentRole) {
	p.passed = append(p.passed, idlePassedOver{id: id, class: class, role: role})
}

// reason is the idle line: what is already running, and what this poll passed
// over and why.
//
// Both halves are said because either alone misleads, and both misled. The line
// said only that nothing was startable, which reads as a stopped machine while a
// run works on the other slot; and it named a count without naming the items or
// the reason, which reads as a queue that will move on its own while the only
// unstarted work is somebody's to carry in conversation. An operator acted on
// that reading three times.
func (p idlePoll) reason(queue backlog.Queue, inFlight int) string {
	if len(queue.Entries) == 0 {
		return "the backlog is empty"
	}
	var said []string
	if inFlight > 0 {
		said = append(said, fmt.Sprintf("%s in flight", plural(inFlight, "run", "runs")))
	}
	if groups := p.groups(); len(groups) > 0 {
		said = append(said, fmt.Sprintf("%s passed over, of %d admitted: %s",
			plural(len(p.passed), "item", "items"), len(queue.Entries), strings.Join(groups, "; ")))
	}
	if len(said) == 0 {
		// The pull stopped before it read the queue through, which is what a session
		// at its own item limit does. Saying what it did not reach is the honest
		// answer; saying nothing was startable would be a claim about items nothing
		// looked at.
		return fmt.Sprintf("none of the %s admitted was reached at this poll", plural(len(queue.Entries), "item", "items"))
	}
	return strings.Join(said, "; ")
}

// groups gathers what was passed over into one entry per class, in the order the
// pull met them, naming the conversation where the class names one. Grouping is
// what gives the line something to act on: an operator reads a class and knows
// what to do about all of it, where a list of items one per line is a list they
// have to classify themselves.
func (p idlePoll) groups() []string {
	type class struct {
		class idleClass
		role  domain.AgentRole
	}
	var order []class
	items := make(map[class][]string, len(p.passed))
	for _, passed := range p.passed {
		at := class{class: passed.class, role: passed.role}
		if _, met := items[at]; !met {
			order = append(order, at)
		}
		items[at] = append(items[at], passed.id)
	}
	groups := make([]string, 0, len(order))
	for _, at := range order {
		named := items[at]
		further := 0
		if len(named) > maxIdleItemsNamed {
			further = len(named) - maxIdleItemsNamed
			named = named[:maxIdleItemsNamed]
		}
		listed := strings.Join(named, ", ")
		if further > 0 {
			listed += fmt.Sprintf(", and %d further", further)
		}
		if at.role != "" {
			groups = append(groups, fmt.Sprintf("%s (%s: %s)", at.class, at.role.Title(), listed))
			continue
		}
		groups = append(groups, fmt.Sprintf("%s (%s)", at.class, listed))
	}
	return groups
}

// carrier is the conversation an idle poll is waiting on, which is the marker on
// the highest-priority item it passed over for one. It is empty where no such
// item was passed over, which is every idle poll whose answer is a wait rather
// than a person.
//
// The highest-priority one is named because that is the item an operator would
// open first, and because the reason above names every one of them by role
// anyway: this decides whose move the message closes on, and the prose is where
// the rest of them are.
func (p idlePoll) carrier() domain.WorkItemExecutor {
	for _, passed := range p.passed {
		if passed.class == idleCarriedInConversation && passed.role != "" {
			return domain.ConversationWith(passed.role)
		}
	}
	return ""
}

// plural counts something in words, so a line an operator reads says "1 run"
// rather than "1 run(s)". The parenthesised plural is what a status line looks
// like when nobody read it out loud.
func plural(count int, one, many string) string {
	if count == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", count, many)
}

// coveredReason says that the work has already been broken out, and names what
// broke it out. The children are what makes it actionable: an operator reading
// that an item was passed over needs to see where its execution went, and a
// container left behind after its last child closes is read from the same line.
//
// The naming is bounded rather than exhaustive, for the reason every listing
// here is: an epic decomposed into twenty children would otherwise put twenty
// identifiers into a report whose whole job is to be read.
func coveredReason(children []string) string {
	named := children
	if len(named) > maxCoveringChildrenNamed {
		named = named[:maxCoveringChildrenNamed]
	}
	reason := fmt.Sprintf("its execution is covered by %d unfinished child item(s): %s",
		len(children), strings.Join(named, ", "))
	if further := len(children) - len(named); further > 0 {
		reason += fmt.Sprintf(", and %d further", further)
	}
	return reason + ". The children are the work; pulling this beside them would make the same change twice"
}

// conversationExecutedReason says that the work was admitted for something other
// than a developer run, and that this is not a wait. Every other deferral here
// ends when something clears — a directive resolved, a child closed, a run
// finished — and this one ends when somebody opens the conversation the item
// names, which is why it says so rather than reading like a queue that will move
// on its own.
func conversationExecutedReason(executor domain.WorkItemExecutor) string {
	return fmt.Sprintf("its executor is %q rather than a developer run, so nothing a run can do would carry it out; the item is done in the conversation it names, and it is passed over here rather than waiting for anything", executor)
}

// passedOverReason says why an unready entry is being named rather than counted,
// and says nothing for the entries that are counted. Three kinds are named: work
// no run can carry, work somebody parked, and work held by a step only a person
// can take. None of the three is a wait on anything the harness will finish, and
// the order between them is the order the queue's own account gives — an item in
// more than one is told the thing that would still hold once the others lifted.
func passedOverReason(entry backlog.Entry) (string, bool) {
	switch {
	case !entry.Executor.DeveloperRun():
		return conversationExecutedReason(entry.Executor), true
	case entry.Parking.Parked():
		return parkedReason(entry.Parking), true
	case entry.HumanGates.Holds():
		return entry.Hold(), true
	default:
		return "", false
	}
}

// unreadyClass is which class an unready entry is passed over in, and it makes
// the same distinction passedOverReason does and in the same order: work no run
// can carry, then work somebody parked, then work held by a step only a person
// can take, then everything that is genuinely waiting for something. An item in
// more than one is told the thing that would still hold once the others lifted.
func unreadyClass(entry backlog.Entry) idleClass {
	switch {
	case !entry.Executor.DeveloperRun():
		return idleCarriedInConversation
	case entry.Parking.Parked():
		return idleParked
	case entry.HumanGates.Holds():
		return idleWaitingOnAPerson
	default:
		return idleWaitingOnOtherWork
	}
}

// parkedReason says that the work was deliberately taken out of reach, and says
// what decided it. Like the executor above it is not a wait, and unlike every
// other deferral here nothing clears it but a person releasing the item.
//
// It says the queue depth outright, because that is the misreading it exists to
// end. Parking used to be expressed as the bottom of the order, which reads as
// "last" to everything that pulls — so a queue with nothing else left took it,
// and the run cost $34.38 to fail at work somebody had already decided to defer.
// A pull that reaches a parked item now says it will never take it, however
// little else there is.
func parkedReason(parking domain.WorkItemParking) string {
	return fmt.Sprintf("it is parked, so no pull selects it however far the queue drains and this is not a wait for anything: %s. Releasing it is the product manager's, and until they do it is passed over at every pull",
		singleLine(parking.Reason(), maxScheduleReasonBytes))
}

// brakedReason says which brake stopped the line, because what an operator does
// about it depends entirely on which one it was.
func brakedReason(ownBrake bool, hold runstate.IntakeHold) string {
	if ownBrake {
		return "the harness held intake after runs kept blocking; the session polls and chooses nothing until it is released"
	}
	reason := strings.TrimSpace(hold.Reason)
	if reason == "" {
		return "the operator is holding intake; the session polls and chooses nothing until it is released"
	}
	return "the operator is holding intake: " + reason
}

// opening says what the session was started to do, which is the first thing its
// log holds and the only line a session that dies immediately will ever write.
func (s Scheduler) opening() string {
	if !s.Watching {
		return "draining what is ready"
	}
	opening := "watching the backlog until stopped"
	if s.Limit > 0 {
		opening += fmt.Sprintf(", stopping after %d run(s)", s.Limit)
	}
	if s.Budget > 0 {
		opening += fmt.Sprintf(", within a budget of $%.2f", s.Budget)
	}
	return opening
}

// sleep waits out one interval between readings of the queue.
func (s Scheduler) sleep(ctx context.Context, interval time.Duration) bool {
	if s.Sleep != nil {
		return s.Sleep(ctx, interval)
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// session records what the pass is doing, once per change rather than once per
// poll. A session idling all night writes one line, which is what makes the log
// worth reading and what makes a line appearing in it mean something.
//
// A drain records nothing at all: it is a command somebody is waiting on the
// return of, and the schedule it returns is the account of it.
func (s Scheduler) session(schedule *Schedule) *watchSession {
	if !s.Watching || s.Sessions == nil {
		return &watchSession{}
	}
	return &watchSession{to: s.Sessions, now: s.now, schedule: schedule}
}

// account is what a session says about the state it is entering: why it is in
// it, how many developer runs it can see in flight, and the conversation that
// has to act before its answer changes where there is one.
//
// The last two are on the account rather than folded into the prose because
// something other than a person reads them. The reason is a sentence, and whose
// move follows an idle poll is a fact a channel closes its message on — derived
// from these fields rather than parsed back out of the words.
type account struct {
	reason  string
	running int
	// executor is the conversation the session is waiting on, and unreadable marks
	// the poll that chose nothing because the harness itself could not be read.
	// They are the two states whose next move is not an admission, and they are
	// carried as facts rather than left in the prose because the clause a channel
	// closes on is derived from them.
	executor   domain.WorkItemExecutor
	unreadable bool
}

type watchSession struct {
	to       WatchSessions
	now      func() time.Time
	schedule *Schedule
	state    runstate.WatchState
	said     account
}

// enter records the session arriving in a state. The same state said the same
// way is the session still being in it, which is not news.
//
// "The same way" is the whole account rather than the reason alone, so a poll
// that finds what the poll before it found writes nothing however many times it
// is made — a session idling all night over an unchanging queue is still one
// line. What does write a second line is the account changing: an item passed
// over for a different reason, or a run starting or finishing. Both are news to
// somebody reading an idle line, and both are bounded by the poll interval,
// because a pass records at most one transition per poll.
func (w *watchSession) enter(state runstate.WatchState, said account) {
	if w.to == nil || (w.state == state && w.said == said) {
		return
	}
	w.state, w.said = state, said
	w.record(SessionState{
		State:      state,
		Reason:     said.reason,
		Running:    said.running,
		Executor:   said.executor,
		Unreadable: said.unreadable,
	})
}

// stop records the session's last line, and whether the stop is the session
// being restarted into a build deployed over it rather than the line going down.
// The two read identically in the log and mean opposite things to whoever is
// waiting: one is a session somebody has to start again, and the other is a
// session that is already on its way back.
func (w *watchSession) stop(reason string, restarting bool) {
	if w.to == nil {
		return
	}
	w.state, w.said = runstate.WatchStopped, account{reason: reason}
	w.record(SessionState{State: runstate.WatchStopped, Reason: reason, Restarting: restarting})
}

// resume records the session choosing work again after a wait. It leaves the
// session in the watching state rather than a resumed one, so a queue that goes
// quiet and busy all day reads as alternating rather than as a session
// restarting every hour.
func (w *watchSession) resume(reason string) {
	if w.to == nil || w.state == runstate.WatchWatching {
		return
	}
	w.state, w.said = runstate.WatchWatching, account{reason: reason}
	w.record(SessionState{State: runstate.WatchResumed, Reason: reason})
}

// record writes one transition. A transition that cannot be written costs the
// session its visibility and not its work: what is reported is a session nobody
// can see, which is worth saying out loud and is not worth stopping the work
// for.
func (w *watchSession) record(transition SessionState) {
	transition.At = w.now()
	if err := w.to.Record(transition); err != nil && w.schedule.SessionProblem == "" {
		w.schedule.SessionProblem = fmt.Sprintf("the session could not record that it was %s, so what it is doing is not readable from anywhere but here: %v", transition.State, err)
	}
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

// pulled is one reading of the queue: the admitted work in the product
// manager's order, and the items it was assembled from. The items are kept
// because the order alone does not carry what the work says, and a watch has to
// be able to tell an item that has changed since it was tried from one that has
// not.
type pulled struct {
	queue backlog.Queue
	// items is every work item this pull read, by identifier: the admitted ones
	// the queue was assembled from, and the claimed ones that have left it. The
	// claimed half is there because what a run in flight is going to change is
	// what says whether something else may be started beside it, and an item
	// somebody has already pulled is not in the queue to be read from.
	items map[string]beads.WorkItem
	// children names the unfinished children of each item in this reading — the
	// ones still queued, and the ones somebody has already pulled — in the
	// product manager's order. A child that has closed is not here, which is
	// exactly when its parent stops being covered by it.
	children map[string][]string
}

// queue assembles the admitted work into the product manager's order. It is the
// same assembly a conversation's backlog uses and the same one a development
// manager reads, built from the tracker every pull rather than stored, so what
// the scheduler pulls can never drift from the priorities actually set.
func (p Pull) queue(ctx context.Context) (pulled, error) {
	var admitted []beads.WorkItem
	for _, status := range scheduledStatuses {
		items, err := p.Tracker.List(ctx, status)
		if err != nil {
			return pulled{}, fmt.Errorf("list %s work items: %w", status, err)
		}
		admitted = append(admitted, items...)
	}
	ready, err := p.Tracker.Ready(ctx)
	if err != nil {
		return pulled{}, fmt.Errorf("list the work items the tracker reports as ready: %w", err)
	}
	pullable := make([]string, 0, len(ready))
	for _, item := range ready {
		pullable = append(pullable, item.ID)
	}
	items := make(map[string]beads.WorkItem, len(admitted))
	for _, item := range admitted {
		items[item.ID] = item
	}
	// The gates a person has recorded passing come from the harness's own store,
	// because the tracker cannot answer the question: the only completion it
	// records is an item being closed, and an item's closure passing a step the
	// operator reserved for themselves is what these exist to stop. A pull that
	// could not read them treats every declared gate as still holding, which
	// stops work rather than starting it past somebody's step.
	discharged, err := p.Gates.DischargedGates()
	if err != nil {
		return pulled{}, fmt.Errorf("read the human gates a person has passed: %w", err)
	}
	queue := backlog.Order(admitted, pullable, discharged)
	// Coverage is read one status wider than the backlog. A claimed child has left
	// the queue and is never chosen from here, but it is a run in flight over the
	// same work, so its parent is the last thing that should be started beside it.
	claimed, err := p.Tracker.List(ctx, claimedStatus)
	if err != nil {
		return pulled{}, fmt.Errorf("list %s work items: %w", claimedStatus, err)
	}
	for _, item := range claimed {
		items[item.ID] = item
	}
	children := make(map[string][]string)
	// Parentage is taken from whichever way the tracker states it rather than
	// from the parent field alone. A store that records decomposition only as an
	// edge — the tracker's own export is one — reads as a backlog nothing was ever
	// broken out of, and a coverage map built from that is empty however many
	// epics are sitting in the queue beside their children.
	cover := func(item beads.WorkItem) {
		if parent := item.DecomposedFrom(); parent != "" {
			children[parent] = append(children[parent], item.ID)
		}
	}
	// The queue first, so what a deferral names reads in the product manager's
	// order rather than in whatever order the tracker listed.
	for _, entry := range queue.Entries {
		cover(items[entry.ID])
	}
	for _, item := range claimed {
		cover(item)
	}
	return pulled{queue: queue, items: items, children: children}, nil
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
//
// Sequencing goes in the same reason for the opposite purpose: it did decide
// something. Where conflict-avoidance moved what was started, the run that was
// started says so, because an order departed from silently is one nobody can
// account for afterwards.
func scheduleReason(entry backlog.Entry, queue backlog.Queue, free, capacity int, stale []staleness.Change, ordering sequencing) string {
	reason := fmt.Sprintf(
		"the scheduler pulled %s from the backlog: position %d of %d admitted item(s) at priority %d, one of the %d the tracker reports as ready, with %d of %d developer slot(s) free",
		entry.ID, entry.Position, len(queue.Entries), entry.Priority, queue.Ready(), free, capacity)
	if len(stale) > 0 {
		change := stale[0]
		reason += fmt.Sprintf(
			". %s was %s by the %s after this item was admitted (%s), and %d further change(s) upstream of it; staleness is reported rather than acted on, so it held nothing back",
			change.ArtifactID, change.Action, change.By,
			singleLine(change.Reason, maxScheduleReasonBytes), len(stale)-1)
	}
	return reason + "." + ordering.reason()
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
	// What the queue looked like is the pass-level answer to why an item was not
	// started, so it is said whichever way the pass went — and said as counts,
	// because the alternative is a line for every admitted item on every pass. A
	// pass that never got as far as the queue says nothing here rather than
	// printing zeroes it did not read.
	if s.BacklogRead {
		fmt.Fprintf(&rendered, "backlog at the last pull: %d admitted item(s), %d of them ready to pull\n",
			s.Admitted, s.Pullable)
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
	// What the pass put in front of the development manager, said beside what it
	// pulled: a run stopped hours ago reaching her now is the other half of what
	// this session did with its time.
	rendered.WriteString(EscalationSweep{Escalated: s.Escalated}.Render())
	if s.EscalationProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.EscalationProblem)
	}
	if s.IntakeHeld != nil {
		fmt.Fprintf(&rendered, "intake has been held since %s", s.IntakeHeld.HeldAt.UTC().Format("2006-01-02 15:04:05Z"))
		if reason := strings.TrimSpace(s.IntakeHeld.Reason); reason != "" {
			fmt.Fprintf(&rendered, ": %s", reason)
		}
		rendered.WriteString("\n")
	}
	// A session that waited says how long it was alive for, because the whole
	// point of watching is that nothing happening is not the same as nothing
	// running.
	if s.Watched && s.Polls > 0 {
		fmt.Fprintf(&rendered, "waited out %d poll interval(s) with nothing to start\n", s.Polls)
	}
	// A session that rode through readings that failed says so. The reading that
	// succeeded afterwards leaves nothing behind, so an outage nobody was told
	// about is one that gets diagnosed from scratch the next time it happens. The
	// last of them is named except where the session went on to stop on a reading
	// too, which is said once in the failure it stopped with rather than twice.
	if s.ReadsRetried > 0 {
		fmt.Fprintf(&rendered, "%d reading(s) of the harness failed and were made again rather than stopping the session\n", s.ReadsRetried)
	}
	if s.ReadProblem != "" && s.ReadFailure == "" {
		fmt.Fprintf(&rendered, "the last of them: %s\n", s.ReadProblem)
	}
	if s.Braked != nil {
		// The brake places a hold nobody chose, so the line that reports it carries
		// the command that lifts it: whoever meets this is reading a terminal, and
		// a remedy they cannot run from there is no remedy.
		fmt.Fprintf(&rendered, "the harness held intake after %d run(s) blocked in a row; `yoyo release` lifts it and the line carries on\n", s.BlockedInARow)
	}
	if s.BrakeProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.BrakeProblem)
	}
	if s.SpentUSD > 0 || s.Budget > 0 {
		fmt.Fprintf(&rendered, "spent $%.2f", s.SpentUSD)
		if s.Budget > 0 {
			fmt.Fprintf(&rendered, " of the $%.2f this session was given", s.Budget)
		}
		rendered.WriteString("\n")
	}
	if s.SpendProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.SpendProblem)
	}
	if s.SessionProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.SessionProblem)
	}
	if s.RedeployProblem != "" {
		fmt.Fprintf(&rendered, "%s\n", s.RedeployProblem)
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
//
// A run that reached an ending is named by that ending, in the read model's own
// vocabulary, rather than by the error the start came back with. Every stoppage
// returns one: a run handed to a person with its branch and worktree intact
// comes back to the scheduler as an error exactly as a run that broke does, so
// reading the error first put "failed" over both. Which of them it was is the
// pass's to report and not to work out — Ending is the same derivation `yoyo
// status` and the channel read.
func (s Started) state() string {
	switch {
	case s.Declined != "":
		return "declined"
	case endedWithoutSucceeding(s.Outcome.Status):
		return string(s.Outcome.Ending())
	case s.Failure != "":
		// No run reached a status, so there is no ending to name: the start itself
		// is what failed.
		return "failed"
	case s.Outcome.Paused:
		return "paused"
	case s.Outcome.Integration != nil:
		return "integrated"
	default:
		return string(s.Outcome.Status)
	}
}

// endedWithoutSucceeding reports a run that reached a terminal status other than
// success, which is the whole of where the four-way vocabulary applies.
func endedWithoutSucceeding(status runstate.Status) bool {
	return status.Terminal() && status != runstate.StatusSucceeded
}

// Redeploying reports a session that stopped in order to be restarted into the
// build deployed over it, with every run it started waited out first.
//
// It is read from why the session stopped rather than from a flag of its own,
// because the two can disagree and only one of them is the truth: a session that
// had decided to redeploy and was then stopped by its operator stopped for the
// operator, and restarting it would be this loop overriding the person who
// closed it.
func (s Schedule) Redeploying() bool {
	return s.Stopped == ScheduleRedeployed
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
