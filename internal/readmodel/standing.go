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
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/developerslot"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// backlogStatuses are the tracker slices the admitted work is assembled from.
// They are the scheduler's own, because the queue this describes has to be the
// queue that is actually pulled from.
var backlogStatuses = []string{"open", "blocked"}

// maxRefusalBytes bounds one refusal as this renders it. A parking reason and a
// directive are prose somebody wrote at whatever length they wanted, and this is
// a status line.
//
// It binds every surface, and the strictest requirement on it is not this one's.
// A terminal prints a refusal when somebody asks for it; the channel heartbeat
// says the same words again every hour while a state stands, which is a line
// that has to stay a line or become the thing people mute. Raising this would
// lengthen that message too, so it is raised for both surfaces or for neither.
const maxRefusalBytes = 160

// Runs is the durable run state the standing status reads. It reads and never
// adopts: what is in flight and what is unsettled are both questions about runs
// other processes own, and answering them is not acting on one.
//
// Recorded is every run, and it is read for the one question the other two
// listings cannot answer: which promotions the forge has not published. A run
// settled after a dropped merge owes no step and is not in flight, and its
// publication is still not on the remote.
//
// It is satisfied by *runstate.Store.
type Runs interface {
	Incomplete() ([]runstate.State, error)
	Outstanding() ([]runstate.State, error)
	Recorded() ([]runstate.State, error)
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

// Reports is the pile every role files what it noticed into, and what became of
// the ones somebody has decided. It is read for one derived fact — how deep the
// pile is and how old the oldest undecided report in it is — because that is the
// only way an operator can tell a channel that is being worked through from one
// that is quietly filling up. It is satisfied by *runstate.ReportStore.
type Reports interface {
	List() ([]report.Report, error)
	Handlings() ([]report.Handling, error)
}

// ProviderOutages is the provider answering nobody, as a reading asks it. It is
// satisfied by *runstate.ProviderOutageStore.
type ProviderOutages interface {
	Standing() (runstate.ProviderOutage, bool, error)
}

// Sources are the durable records one standing reading is assembled from, and
// the two configured numbers it is read against. Every store is an interface so
// that this derivation can be exercised without a state directory, which is the
// only way a format nobody may break gets a fixture that holds it.
type Sources struct {
	Runs          Runs
	Conversations Conversations
	Tracker       Tracker
	// Stoppages is what the harness has stopped and nobody has decided about, which
	// is what separates an item held for a person from one whose dependencies have
	// all landed. It is optional, and a reading without one holds every blocked
	// item rather than releasing work whose hold it could not read; see
	// backlog.Holds for why that is the safe direction.
	Stoppages Stoppages
	// Decisions is what triage has already decided about those stoppages, which is
	// what separates an item waiting on the development manager from one waiting
	// on the harness carrying her decision out. It is optional, and a reading
	// without one reports every held item as one nobody has decided about, saying
	// so in the refusal rather than guessing the other way.
	Decisions Decisions
	// Remains is the repository, asked whether each stopped run's change is still
	// there; a hold on a preserved change is decided from that rather than from
	// the run's removal flags. It is optional, and a reading without one decides
	// from the record and says so in the hold.
	Remains       Remains
	Directives    Directives
	Amendments    Amendments
	OperatorHolds OperatorHolds
	IntakeHolds   IntakeHolds
	Sessions      Sessions
	// Reports is the collected pile. It is optional, and a reading without one
	// says nothing about the pile rather than reporting it empty: "nobody has
	// reported anything" and "nothing was wired to read what anybody reported" are
	// opposite answers, and only one of them means there is nothing to do.
	Reports Reports
	// UsageLimits is the provider's refusals outside a run, read with the runs
	// above — for the ones parked on a limit — and the agents below for the one
	// thing the three say together: whether the provider is holding every role
	// at once. It is optional, and a reading without one reads the hold from the
	// runs alone and says nothing about the log rather than reporting it empty —
	// a project whose every refusal went unread for five days is the reason
	// this is here.
	UsageLimits UsageLimits
	// ProviderOutages is the product's record of the provider answering nobody.
	// It is optional, and a reading without one says nothing about an outage
	// rather than reporting none — three days of a login nobody was told had
	// expired is the reason this is here.
	ProviderOutages ProviderOutages
	// Supervision is the product's supervisor: whether one is running, and what
	// it last recorded about the parts of the product. It is optional, and a
	// reading without one says nothing about the parts rather than reporting
	// them all off — a part the supervisor has left down is the one state here
	// that nothing else reports.
	Supervision Supervision
	// Agents is every configured agent, as the configuration resolved it: what
	// each asks for and what each may be served by instead. It is the other half
	// of the hold above, because a refusal holds a role only against what that
	// role is configured to ask.
	Agents []AgentEndpoint
	// UnknownResetPause is execution.usage_limit_unknown_reset_pause as the caller
	// read it: how long a refusal that named no reset stands before the harness
	// asks again, which is the same interval failover reads the same log on.
	UnknownResetPause time.Duration
	// Capacity is execution.max_concurrent_developers as the caller read it. It is
	// what turns "nothing is starting" into "there is no slot", which are opposite
	// things for an operator to do about.
	Capacity int
	// Slots is execution.developer_slots as the caller read it: what each of the
	// Capacity developer slots prefers. It is what lets the running line say
	// which slot each run occupies and name the preferred label beside each slot,
	// read off the same derivation the scheduler fills the free slots from. Empty
	// is every slot preferring nothing, and the line then reads exactly as it did
	// before slots could prefer anything.
	Slots []domain.DeveloperSlot
	// TrackerTimeout bounds one tracker command, so an unresponsive tracker costs
	// this answer a line rather than hanging the surface that asked.
	TrackerTimeout time.Duration
	// Now stamps the reading. It defaults to the wall clock and is injected so a
	// test can pin an elapsed time.
	Now func() time.Time
}

// RunningRun is one developer run in flight, as the four-line status names it.
type RunningRun struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	// Title is the work item's title as the run recorded it at its claim, and
	// empty on a run recorded before titles were carried. It is here for the
	// surface that shows a run as a card rather than a line: an id alone is a
	// lookup, and a title beside it is a glance.
	Title string `json:"title,omitempty"`
	// Labels is the tracker's labels on the item as the run recorded them at its
	// claim, and empty on a run recorded before labels were carried. They are
	// what the slot below is read from.
	Labels []string `json:"labels,omitempty"`
	// Backend, Model, and Account are what the run is spending: the provider it
	// runs on, the model it asked for (the resolved identifier where the provider
	// reported one), and the account alias it runs under. The alias is exactly
	// what lets this be said on a page — it names nothing a credential is.
	Backend domain.Backend `json:"backend,omitempty"`
	Model   string         `json:"model,omitempty"`
	Account string         `json:"account,omitempty"`
	Phase   runstate.Phase `json:"phase,omitempty"`
	// Stage is the phase folded onto the three parts of a run a pipeline shows,
	// derived here so no surface keeps its own list of which phase is which.
	Stage Stage `json:"stage"`
	// ResumingIntegration reports a run at its promotion again after the
	// environment stopped it there, with its approval standing. The line says
	// runstate.ResumingIntegrationSays for it in place of the bare phase, because
	// a run integrating after such a stop and one integrating for the first time
	// are the same phase and different facts.
	ResumingIntegration bool          `json:"resuming_integration,omitempty"`
	StartedAt           time.Time     `json:"started_at"`
	Elapsed             time.Duration `json:"elapsed"`
	CostUSD             float64       `json:"cost_usd"`
	// UnknownCost says why there is no figure rather than reporting one of zero: a
	// run whose evidence cannot be read has not cost nothing.
	UnknownCost string `json:"unknown_cost,omitempty"`
	// Slot is the developer slot this run occupies, counted from 1 as the
	// configuration counts them, and SlotPrefers what that slot prefers. Both are
	// carried only where some configured slot prefers a label — which slot a run
	// is in is only worth saying where the slots differ — and both are read off
	// the derivation the scheduler reads, so the slot this says a run holds is
	// the slot the scheduler will not fill. Zero is a run in flight beyond the
	// configured capacity, which no slot holds.
	Slot        int      `json:"developer_slot,omitempty"`
	SlotPrefers []string `json:"slot_prefers,omitempty"`
}

// DeveloperSlotStanding is one configured developer slot as the standing status
// names it: its number, what it prefers, and the run in it where one is. It is
// carried only where some configured slot prefers a label, for the surfaces
// that read the model rather than its lines, and the running line renders the
// free ones under it.
type DeveloperSlotStanding = developerslot.Slot

// WorkingTurn is one persona conversation with a turn in flight. It is the fact
// no surface counted before this: a conversation is not a run, so a machine
// spending an operator's money on six persona turns reported nothing running at
// all.
type WorkingTurn struct {
	Agent string           `json:"agent"`
	Role  domain.AgentRole `json:"role"`
	// Backend and Model are what the turn is spending, as the conversation record
	// carries them; the model is the resolved identifier where the provider
	// reported one.
	Backend domain.Backend `json:"backend,omitempty"`
	Model   string         `json:"model,omitempty"`
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
	// Kind is which pile the refusal puts the item in, from the queue's own
	// closed vocabulary. It is carried for the surface that shows where admitted
	// work accumulates, so that surface counts the piles from the reading that
	// worded the refusals rather than from a second parse of them.
	Kind backlog.HoldKind `json:"kind"`
}

// WorkItemRef names one admitted work item, by id and title, for a surface that
// lists a grouping of the queue rather than counting it. It carries no more than
// that: what an item is in full is ReadWorkItem's answer, one item at a time.
type WorkItemRef struct {
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title,omitempty"`
}

// Standing is where the harness stands, in the four lines and nothing else.
// Every line carries its own problem rather than a shared one, because a
// tracker that will not answer says nothing whatever about the runs in flight,
// and one failure standing for all four would lose three answers the caller
// still has.
type Standing struct {
	ObservedAt time.Time `json:"observed_at"`

	// Paused is the harness waiting out the provider's usage window, said above
	// the four lines rather than inside them.
	//
	// It is the one state that goes there, and it is there because the operator
	// asked for it in those terms: when the system is paused on a provider usage
	// window, the cause is the first words of any message that reaches him. A
	// reading is a message — it is what he runs when he wants to know why nothing
	// is happening — and the window said only as one item's refusal is the cause
	// three lines down and possibly tenth in a list. It is not a fifth line: the
	// four still render exactly as they did, and this is a banner above them, in
	// the shape `yoyo status --list` already puts the operator's own pause in.
	Paused string `json:"paused,omitempty"`
	// CapacityHold is the provider holding every configured role at once, as the
	// refusal log and the agents' configuration say it, and nil where it is not.
	// It is carried whole for the surfaces that read the model rather than its
	// lines: the banner above says it in one sentence, and this is the reset, the
	// count, and the models behind that sentence.
	CapacityHold *CapacityHold `json:"capacity_hold,omitempty"`
	// CapacityBlocked is what is parked or held on provider capacity, one run
	// and one conversation at a time: the hold above is every role refused at
	// once, and this is each thing the provider has stopped on its own, with
	// its deadline and what a person can do about it. It is not a fifth line
	// and is not rendered as one; it is carried for the surfaces that read the
	// model rather than its lines — the JSON a script reads, and the capacity
	// panel the dashboard design asks for — and it is always present, with each
	// half saying so where its records could not be read.
	CapacityBlocked CapacityBlocked `json:"capacity_blocked"`
	// ProviderOutage is the provider answering nobody — a login nobody has
	// renewed, an API nothing reaches — as the product's record says it, and nil
	// where it is answering. It is carried whole for the surfaces that read the
	// model rather than its lines; the banner above says it in one sentence.
	ProviderOutage *runstate.ProviderOutage `json:"provider_outage,omitempty"`

	Running        []RunningRun `json:"running"`
	RunningProblem string       `json:"running_problem,omitempty"`
	// Dispatching is every dispatch a watch session started that is waiting out a
	// tracker failure before it has claimed anything, as WaitingOnTracker reads it
	// from the watch log. Such a dispatch holds a developer slot with no run
	// record, so it is on no list above, and it is said on the running line
	// because that is where a reader looks for what is holding the slots.
	// DispatchingProblem is a watch log that could not be read for them, said
	// under the line rather than in place of it: the runs above were read.
	Dispatching        []DispatchWait `json:"dispatching,omitempty"`
	DispatchingProblem string         `json:"dispatching_problem,omitempty"`
	// DeveloperSlots is every configured developer slot with the run in it, in
	// slot order, where some slot prefers a label; nil otherwise. The running
	// line names the free ones with their preference under itself, and a run's
	// own entry says which slot it is in, so this is for the surface that wants
	// the whole set rather than the lines.
	DeveloperSlots []DeveloperSlotStanding `json:"developer_slots,omitempty"`

	Working        []WorkingTurn `json:"working"`
	WorkingProblem string        `json:"working_problem,omitempty"`

	NotStartable []Refused `json:"not_startable"`
	// Admitted is the whole backlog this reading saw, so a short not-startable
	// list is legible: two refusals out of three admitted items and two out of
	// forty are different states of the same machine.
	Admitted int `json:"admitted"`
	// AwaitingDecision and AwaitingCarryOut are how much of the admitted work is
	// held, split by whose move it is: a stoppage the development manager has
	// still to decide about, and a decision she recorded that the harness has
	// still to act on. They are counted separately and said in the head of the
	// not-startable line, because the head is the whole of what an hourly message
	// carries and one figure covering both is what sent an operator's attention to
	// the wrong role for days.
	AwaitingDecision int `json:"awaiting_decision"`
	AwaitingCarryOut int `json:"awaiting_carry_out"`
	// Startable is how much of the admitted work nothing refuses: the items the
	// harness would start next, counted over the same entries the refusals are,
	// so the head of the line, the refusals under it, and this are one set of
	// items. It is zero whenever the pass-level stall stands, because a stall
	// is precisely every pullable item refused at once. It is not printed — the
	// four lines say it by the absence of a refusal — and is carried for the
	// surface that shows the pipeline, so that surface reads the count rather
	// than subtracting one list from another.
	Startable int `json:"startable"`
	// AdmittedItems and StartableItems name the items behind Admitted and
	// Startable, each in the product manager's order, so a surface that opens a
	// grouping of the queue lists the same items the count counts rather than
	// assembling a list of its own from the lines. The refusals above already
	// name the held-back items, so with these three every item Admitted counts is
	// named exactly once, and an item a run is carrying is in AdmittedItems and
	// on the running line and nowhere else. Both are nil where the queue could
	// not be read, as NotStartable is, and empty rather than absent otherwise.
	AdmittedItems       []WorkItemRef `json:"admitted_items"`
	StartableItems      []WorkItemRef `json:"startable_items"`
	NotStartableProblem string        `json:"not_startable_problem,omitempty"`

	NeedsHuman        []Attention `json:"needs_human"`
	NeedsHumanProblem string      `json:"needs_human_problem,omitempty"`

	// Reports is how the collected pile stands. It is not a fifth line and is not
	// rendered as one: the four are a contract the operator ratified, and this is
	// carried for the surfaces that read the model rather than its lines — the
	// JSON a script or a dashboard reads, and the arithmetic behind the attention
	// entry below. Whether the pile is draining is a question about a week rather
	// than a moment, and it is answerable only if each reading says how deep the
	// pile is and how old the oldest undecided report in it was.
	Reports report.Pile `json:"reports"`
	// ReportsProblem is a pile that could not be read. It is stated rather than
	// reported as an empty pile, for the reason every other line here states its
	// own failure: a reader told nothing concludes there is nothing.
	ReportsProblem string `json:"reports_problem,omitempty"`

	// Services is the product's parts as its supervisor last recorded them, and
	// whether a supervisor is running now. It is not a fifth line: it is carried
	// for the surfaces that read the model, and the one thing in it that waits
	// on a person — a part the supervisor has left down — is on the attention
	// line with its reason. It is nil where nothing was wired to read it.
	Services        *Services `json:"services,omitempty"`
	ServicesProblem string    `json:"services_problem,omitempty"`
}

// maxUndecidedReportAge is how long the oldest report nobody has decided about
// may go unanswered before the pile is something waiting on a person rather
// than something a schedule is working through.
//
// A pile is meant to drain on its own: every role files into it, the product
// manager decides about what it is shown, and a recurring task works it on a
// cadence so that neither depends on an operator being at a terminal. The
// failure this catches is that machinery not running or not keeping up, which is
// invisible in any one reading — the pile looks the same the day it stops
// draining as it did the day before — and shows only as the oldest report's age
// climbing past anything a working cadence would leave.
const maxUndecidedReportAge = 7 * 24 * time.Hour

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
	standing.Dispatching, standing.DispatchingProblem = readDispatching(sources, now)
	// Which slot each run occupies, and which slots are free, from the reading
	// the scheduler makes. It is read only where a slot prefers a label, and
	// only where the runs could be read: a slot said to be free over runs
	// nobody could list would be the confident emptiness this refuses.
	if runningProblem == "" {
		standing.DeveloperSlots = readSlots(sources, standing.Running)
	}

	standing.Working, standing.WorkingProblem = readWorking(sources, now)

	// The switches and the directives are read once and used twice: they are why
	// admitted work is not being pulled, and they are themselves things waiting on
	// a person. Reading them twice would be two chances for the two lines to
	// disagree about one file.
	switches := readSwitches(sources)
	refused, waits, queue, stall, notStartableProblem := readNotStartable(ctx, sources, switches, running, now)
	standing.NotStartable = refused
	standing.Admitted = len(queue.Entries)
	standing.AwaitingDecision = waits.awaitingDecision
	standing.AwaitingCarryOut = waits.awaitingCarryOut
	standing.Startable = len(waits.startable)
	standing.StartableItems = waits.startable
	standing.AdmittedItems = waits.admitted
	standing.NotStartableProblem = notStartableProblem
	// The provider's usage window is read out of the same stall the refusals are
	// worded from, rather than derived a second time here: one reading of one
	// silence, said in two places, is the rule this whole package holds.
	if stall.Reason == ReasonProviderWindow || stall.Reason == ReasonProviderAway {
		standing.Paused = stall.Says
	}
	// The provider answering nobody is carried whole as well as said, and it is
	// read again here rather than only through the stall: the stall is dropped
	// where it stopped nothing, and a login that expired over an empty queue is
	// still a login the operator has to renew before anything can happen.
	if switches.providerAway {
		standing.ProviderOutage = &switches.providerOutage
		if standing.Paused == "" {
			standing.Paused = switches.providerOutage.Says()
		}
	}
	// The provider holding every role is the other pause, read from the refusal
	// log and the parked runs rather than from the session choosing work: on
	// 2026-09-08 that session was idle over items waiting on a decision, and the
	// role that would have decided was the one being refused, so the watch log
	// never said a window at all. The session's own account wins where it has
	// one, because it is the same window said with less inference; this says it
	// where nothing else does.
	hold, holdProblem := CapacityHoldOf(sources, now)
	if hold.Holding {
		standing.CapacityHold = &hold
		if standing.Paused == "" {
			standing.Paused = hold.Says()
		}
	}
	// What is parked or held on capacity one at a time is read from the same
	// records the hold and the running line are read from, and carried whole
	// rather than said: a run asleep on a reset is still on the running line,
	// and this is where a surface finds out that it is asleep.
	standing.CapacityBlocked = CapacityBlockedOf(sources, now)

	standing.Reports, standing.ReportsProblem = readReports(sources, now)

	needs, needsProblem := readNeedsHuman(sources, switches)
	// The provider answering nobody is on the attention line whatever the queue
	// holds, because what ends it is a person: it is added here where the stall
	// did not already carry it, which is a stall over an empty queue.
	if switches.providerAway && stall.Reason != ReasonProviderAway {
		needs = append(needs, outageAttention(switches.providerOutage))
	}
	// The hold is waiting on a person in the one way a window is not: the window
	// lifts on the provider's clock, and the configuration that let it hold every
	// role is the operator's to change.
	if attention, held := hold.Attention(); held {
		needs = append(needs, attention)
	}
	needsProblem = joinProblems(needsProblem, holdProblem)
	// A pile whose oldest undecided report has been waiting longer than any
	// working cadence would leave it is waiting on a person, whatever else is
	// running. Nothing else says so: the pile is not work, so no queue holds it,
	// and the reports themselves are filed and forgotten by the roles that filed
	// them.
	if standing.Reports.OldestAge > maxUndecidedReportAge {
		needs = append(needs, reportsAttention(standing.Reports))
	}
	// A stall that is holding admitted work back and is nobody else's line to
	// carry is attention in its own right. Nothing else reports it: a live session
	// choosing nothing over a ready queue is a state no record announces, and the
	// only thing that ever said it was the silence afterwards.
	if waiting, attention := stall.Waiting(); attention {
		needs = append(needs, waiting)
	}
	// A part of the product the supervisor has stopped restarting is down and
	// not coming back on its own, which is the design's degraded state reaching
	// the standing surfaces: it is said here with the reason the supervisor
	// recorded, and the record is carried whole beside the lines.
	standing.Services, standing.ServicesProblem = readServices(sources)
	needs = append(needs, standing.Services.Attention()...)
	needsProblem = joinProblems(needsProblem, standing.ServicesProblem)
	// Held work is on both lines for the reason handed-off work below is, and says
	// a different thing on each: the queue's line says why nothing pulls each
	// item, and this says who has to move and how many items are waiting on them.
	needs = append(needs, Held(standing.AwaitingDecision, standing.AwaitingCarryOut)...)
	// Work marked for a conversation is on both lines and says a different thing
	// on each: the queue's line says why nothing pulls it, and this says who has
	// to open the conversation. A reader looking for what waits on a person must
	// not have to read the queue to find the longest wait there is.
	standing.NeedsHuman = append(needs, HandedOff(queue)...)
	// A pile that could not be read is said on the line the pile would have been
	// said on, as well as in its own field. The field is what a script reads and
	// the line is what a person reads, and a failure only the script can see is
	// one nobody sees.
	standing.NeedsHumanProblem = joinProblems(needsProblem, standing.ReportsProblem)
	return standing
}

// readRunning is the developer runs in flight, priced from the same recorded
// evidence `yoyo cost` reads. A run whose price cannot be read is reported as
// unpriceable rather than as free, which is the rule every cost surface here
// already holds.
//
// In flight is runstate.Status.InFlight and nothing wider: the store's listing
// already answers in those terms, and the predicate is applied here as well so
// that this count is the status's own whatever listing it was handed. The
// scheduler reads the same predicate for the slots and epics already taken,
// which is what lets its refusals be checked against this line.
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
		if !state.Status.InFlight() {
			continue
		}
		run := RunningRun{
			RunID:               state.RunID,
			WorkItemID:          state.WorkItemID,
			Title:               state.WorkItemTitle,
			Labels:              append([]string(nil), state.WorkItemLabels...),
			Backend:             state.Backend,
			Model:               modelOf(state.ProviderModel, state.ProviderResolvedModel),
			Account:             state.AccountAlias,
			Phase:               state.Phase,
			Stage:               StageOf(state.Phase),
			ResumingIntegration: state.ResumingIntegration(),
			StartedAt:           state.StartedAt,
			Elapsed:             now.Sub(state.StartedAt),
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

// readDispatching is the dispatches standing in a wait on the tracker. A reading
// wired with no watch log has none to report, which is every reading from before
// a session could record one.
func readDispatching(sources Sources, now time.Time) ([]DispatchWait, string) {
	if sources.Sessions == nil {
		return nil, ""
	}
	sessions, err := sources.Sessions.List()
	if err != nil {
		return nil, fmt.Sprintf("whether a dispatch is waiting out the tracker could not be read: %v", err)
	}
	waiting := WaitingOnTracker(sessions, now)
	if len(waiting) == 0 {
		return nil, ""
	}
	return waiting, ""
}

// readSlots is which developer slot each run in flight occupies and which are
// free, read from the same derivation the scheduler fills the free slots from,
// over the labels each run recorded at its claim. It answers nothing where no
// configured slot prefers a label: which slot a run is in is only worth saying
// where the slots differ, and a project that configured no preference reads
// exactly as it did before. The runs are given their slot in place.
func readSlots(sources Sources, running []RunningRun) []DeveloperSlotStanding {
	if !developerslot.Preferring(sources.Slots) {
		return nil
	}
	inFlight := make([]developerslot.Run, 0, len(running))
	for _, run := range running {
		inFlight = append(inFlight, developerslot.Run{
			RunID:      run.RunID,
			WorkItemID: run.WorkItemID,
			Labels:     run.Labels,
			StartedAt:  run.StartedAt,
		})
	}
	assignment := developerslot.Assign(sources.Capacity, sources.Slots, inFlight)
	for _, slot := range assignment.Slots {
		if slot.Free() {
			continue
		}
		for index := range running {
			if running[index].RunID == slot.RunID {
				running[index].Slot = slot.Number
				running[index].SlotPrefers = slot.Preferred
			}
		}
	}
	return assignment.Slots
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
			Backend: conversation.Backend,
			Model:   modelOf(conversation.ProviderModel, conversation.ProviderResolvedModel),
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

// Stage is the part of a run a phase belongs to, in the three words a pipeline
// shows: the developer's part, the reviewer's, and the harness's. It is owned
// here so that no surface keeps its own list of which phase is which.
type Stage string

const (
	// StageDeveloping is the developer at work, or about to be: developing,
	// checking, and a run that has not recorded a phase yet, which is one that
	// is still being set up for the developer.
	StageDeveloping Stage = "developing"
	// StageReviewing is the independent review.
	StageReviewing Stage = "reviewing"
	// StageIntegrating is everything after an approval: the promotion and what
	// settles it.
	StageIntegrating Stage = "integrating"
)

// StageOf folds a phase onto its stage. A phase this does not know is the
// harness's, because every phase before the review is named above.
func StageOf(phase runstate.Phase) Stage {
	switch phase {
	case runstate.PhaseDeveloping, runstate.PhaseChecking, "":
		return StageDeveloping
	case runstate.PhaseReviewing:
		return StageReviewing
	default:
		return StageIntegrating
	}
}

// modelOf is the model a record says an invocation ran on: the identifier the
// provider resolved the selector to where it reported one, and the selector
// itself otherwise. A selector such as "opus" floats; the resolved identifier is
// what was actually spent, so it is preferred where the record has it.
func modelOf(selector, resolved string) string {
	if strings.TrimSpace(resolved) != "" {
		return resolved
	}
	return selector
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
	// providerOutage is the provider answering nobody, and providerAway whether
	// that stands. It is read with the switches because it is read the way they
	// are — one file under the product, present or absent — and said the way
	// they are: as what stops the choosing, and as something waiting on a person.
	providerOutage runstate.ProviderOutage
	providerAway   bool
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
	if sources.ProviderOutages != nil {
		if outage, standing, err := sources.ProviderOutages.Standing(); err != nil {
			read.problems = append(read.problems, fmt.Sprintf("whether the provider is answering could not be read: %v", err))
		} else {
			read.providerOutage, read.providerAway = outage, standing
		}
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
// The refusals come from four places and each is the real one. An entry the
// queue itself calls unready carries the queue's own account. An item whose
// unfinished children already carry its execution carries those children, in the
// backlog's own words, because the scheduling pass will never pull it and an
// operator told otherwise is sent to investigate a stall that is not one. An
// item an unresolved directive pauses carries that directive, because the
// pipeline refuses to commit to the work for exactly that reason. Everything
// else is pullable, and what is left is the pass-level refusal — a switch, a
// full machine, nothing choosing — which is a fact about the harness said once
// against each item it actually stops.
//
// The order the four are asked in is the scheduling pass's own, so an item
// several of them stop is refused here for the same one the pass would name.
//
// The stall it returns is the one that stopped at least one item. A stall over
// an empty queue is a state of the machine and not something waiting on
// anybody, so it is returned as no stall at all rather than as attention nobody
// asked for.
//
// The held counts it returns are counted over the same entries, in-flight work
// left out with the rest of it: they are said in the head of the line the
// refusals are listed under, so a count covering an item the line does not name
// is a head that contradicts what is printed beneath it.
func readNotStartable(ctx context.Context, sources Sources, held switches, running []RunningRun, now time.Time) ([]Refused, heldWork, backlog.Queue, Stall, string) {
	if sources.Tracker == nil {
		return nil, heldWork{}, backlog.Queue{}, Stall{}, "nothing was wired to read the admitted work"
	}
	queue, coverage, err := readQueue(ctx, sources)
	if err != nil {
		return nil, heldWork{}, backlog.Queue{}, Stall{}, fmt.Sprintf("the admitted work could not be read: %v", err)
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
	stopped := whyNothingStarts(sources, held, len(running), now)
	refusal := stopped.Refusal()
	// What the stall could not read is said whatever the queue holds. It is a
	// gap in this reading rather than a fact about the work, so a queue with
	// nothing in it must not swallow it below.
	problem := stopped.Problem

	// Whether the stall actually stopped anything. A stall over an empty queue is
	// a state of the machine rather than something waiting on a person, and the
	// attention line must not be given one.
	stalled := false
	waits := heldWork{admitted: make([]WorkItemRef, 0, len(queue.Entries)), startable: []WorkItemRef{}}
	refused := make([]Refused, 0, len(queue.Entries))
	for _, entry := range queue.Entries {
		waits.admitted = append(waits.admitted, WorkItemRef{WorkItemID: entry.ID, Title: entry.Title})
		if _, carried := inFlight[entry.ID]; carried {
			continue
		}
		paused := pausedBy(held.pausing, entry.ID)
		covering := coverage.Covering(entry.ID)
		switch {
		case !entry.Ready:
			waits.count(entry)
			refused = append(refused, Refused{WorkItemID: entry.ID, Title: entry.Title, Reason: entry.Hold(), Kind: entry.HoldKind()})
		case len(covering) > 0:
			// A covered item is not stalled and never will be: nothing is holding it
			// back that clearing a switch or freeing a slot would release, so the
			// pass-level refusal below must not be the one it carries.
			refused = append(refused, Refused{WorkItemID: entry.ID, Title: entry.Title,
				Reason: backlog.CoveredReason(covering), Kind: backlog.HeldCovered})
		case paused != nil:
			refused = append(refused, Refused{WorkItemID: entry.ID, Title: entry.Title,
				Reason: fmt.Sprintf("paused for unresolved directive %s: %s",
					paused.ID, singleLine(paused.Unresolved, maxRefusalBytes)),
				Kind: backlog.HeldByDirective})
		case refusal != "":
			refused = append(refused, Refused{WorkItemID: entry.ID, Title: entry.Title, Reason: refusal, Kind: backlog.HeldByStall})
			stalled = true
		default:
			// Nothing refuses it: this is the work the harness starts next.
			waits.startable = append(waits.startable, WorkItemRef{WorkItemID: entry.ID, Title: entry.Title})
		}
	}
	if !stalled {
		stopped = Stall{}
	}
	if len(held.problems) > 0 {
		problem = joinProblems(problem, strings.Join(held.problems, "; "))
	}
	return refused, waits, queue, stopped, problem
}

// heldWork is how much of a reading's not-startable work is held, split by whose
// move it is. It is a pair rather than one figure because they are two different
// people to go to, and it is counted where the refusals are so that the head of
// that line and the entries under it describe one set of items.
type heldWork struct {
	awaitingDecision int
	awaitingCarryOut int
	// startable is the other side of the same count: the entries nothing
	// refuses, named rather than counted so the surface that lists them lists
	// the entries the count was taken over. admitted is every entry the reading
	// saw, in the same order, for the same reason.
	startable []WorkItemRef
	admitted  []WorkItemRef
}

func (h *heldWork) count(entry backlog.Entry) {
	switch held, carryOut := entry.Awaits(); {
	case !held:
	case carryOut:
		h.awaitingCarryOut++
	default:
		h.awaitingDecision++
	}
}

// whyNothingStarts is the pass-level stall: the reason a pullable item with
// nothing wrong with it is still not being started, from the records this
// reading holds. The derivation itself is shared, because a channel and a
// terminal that worked this out separately would be two answers to the one
// question an operator asks.
func whyNothingStarts(sources Sources, held switches, running int, now time.Time) Stall {
	conditions := Conditions{
		OperatorHold:   held.operator,
		OperatorHeld:   held.operatorHeld,
		IntakeHold:     held.intake,
		IntakeHeld:     held.intakeHeld,
		ProviderOutage: held.providerOutage,
		ProviderAway:   held.providerAway,
		Running:        running,
		Capacity:       sources.Capacity,
		// The reading's own moment, so a provider's usage window this line reports
		// as standing is one that had not lifted when the rest of these lines were
		// read.
		Now: now,
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
// ones a caller counts as still going, newest first. A note about one of a
// session's dispatches is not a transition of the session, so it is read past;
// see runstate.WatchTransition.Note.
func alive(sessions []runstate.WatchTransition, keep func(runstate.WatchState) bool) []runstate.WatchTransition {
	last := make(map[string]runstate.WatchTransition, len(sessions))
	for _, transition := range sessions {
		if transition.Note() {
			continue
		}
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

// readQueue assembles the admitted work in the product manager's order, and the
// coverage over it, from the same four readings the scheduler makes: the
// listings that carry the order, the tracker's own account of what can be
// pulled, what the harness is holding for a person, and the work that has
// already been pulled.
func readQueue(ctx context.Context, sources Sources) (backlog.Queue, backlog.Coverage, error) {
	var admitted []beads.WorkItem
	for _, status := range backlogStatuses {
		items, err := sources.list(ctx, status)
		if err != nil {
			return backlog.Queue{}, nil, err
		}
		admitted = append(admitted, items...)
	}
	trackerCtx, cancel := sources.bounded(ctx)
	defer cancel()
	ready, err := sources.Tracker.Ready(trackerCtx)
	if err != nil {
		return backlog.Queue{}, nil, fmt.Errorf("list the work items the tracker reports as ready: %w", err)
	}
	pullable := make([]string, 0, len(ready))
	for _, item := range ready {
		pullable = append(pullable, item.ID)
	}
	var held backlog.Holds
	if sources.Stoppages != nil {
		held, err = HeldForAPerson(ctx, sources.Stoppages, sources.Decisions, sources.Remains)
		if err != nil {
			return backlog.Queue{}, nil, fmt.Errorf("read what the harness is holding for a person: %w", err)
		}
	}
	queue := backlog.Order(admitted, pullable, held)
	// Coverage is read one status wider than the order, exactly as the scheduling
	// pass reads it. Leaving the claimed slice out would report an epic whose
	// child a run is carrying right now as pullable, which is the shape of the
	// disagreement this line exists to have none of.
	claimed, err := sources.list(ctx, backlog.StatusClaimed)
	if err != nil {
		return backlog.Queue{}, nil, err
	}
	return queue, backlog.Cover(queue, admitted, claimed), nil
}

// readReports is how the collected pile stands. A pile that could not be read
// says so and reports no counts at all: a zero here would read as a channel
// nobody has filed into, which is the one thing a broken read of it must never
// look like.
func readReports(sources Sources, now time.Time) (report.Pile, string) {
	if sources.Reports == nil {
		return report.Pile{}, "nothing was wired to read what the roles have reported"
	}
	reports, err := sources.Reports.List()
	if err != nil {
		return report.Pile{}, fmt.Sprintf("the collected reports could not be read: %v", err)
	}
	handlings, err := sources.Reports.Handlings()
	if err != nil {
		// The pile is readable and what became of it is not, so every report would
		// count as undecided. That overstates the backlog in the direction that
		// sends somebody to work on something already done, so no counts are given
		// at all and the gap is named.
		return report.Pile{}, fmt.Sprintf("what became of the collected reports could not be read, so how many are still waiting cannot be said: %v", err)
	}
	return report.Summarize(reports, handlings, now), ""
}

// readNeedsHuman is everything waiting on a person, with whose move it is.
//
// What is here and what is not is the whole of the line's value. A switch
// somebody placed, a directive nobody settled, a proposal nobody decided, a run
// that owes a step, held work, and work marked for a conversation are all
// waiting on somebody named and will wait forever without them. A parked item is
// not: parking is a decision already taken, and listing it would tell an
// operator to act on something somebody deliberately settled.
//
// Held work is the one entry here whose mover can be the harness rather than a
// person, and it is on this line for exactly that reason: an operator scanning
// for what is waiting on him has to be able to see which of it is not.
func readNeedsHuman(sources Sources, held switches) ([]Attention, string) {
	attention := make([]Attention, 0, 4)
	if held.operatorHeld {
		attention = append(attention, operatorHoldAttention(held.operator))
	}
	if held.intakeHeld {
		// Who placed it is on the record and is said with it: the same switch is
		// placed by the operator and by the harness's own failure-storm brake,
		// and an operator told this hold is theirs when the brake placed it goes
		// looking for a decision they never made. Whose move it is comes from
		// the same record: the operator's hold is theirs, and the brake's is the
		// development manager's while she decides, the harness's while a probe
		// runs, and the operator's only once she has escalated it — with the
		// probe named where one is in flight, so the line says what is being
		// tried rather than only that something is.
		attention = append(attention, intakeHoldAttention(held.intake))
	}
	for _, paused := range held.pausing {
		attention = append(attention, directiveAttention(paused))
	}
	problem := strings.Join(held.problems, "; ")

	if sources.Amendments == nil {
		problem = joinProblems(problem, "nothing was wired to read the proposed changes")
	} else if records, err := sources.Amendments.List(); err != nil {
		problem = joinProblems(problem, fmt.Sprintf("the proposed changes could not be read: %v", err))
	} else {
		for _, proposal := range amendment.Pending(records) {
			attention = append(attention, amendmentAttention(proposal))
		}
	}

	if sources.Runs == nil {
		problem = joinProblems(problem, "nothing was wired to read the runs that owe a step")
		return attention, problem
	}
	// A run that owes a step and a promotion the forge has not published are two
	// entries where one run is both — a merge the forge still has queued — because
	// they are two different waits: the step is the harness's to take, and the
	// publication is whoever's the entry names.
	if outstanding, err := sources.Runs.Outstanding(); err != nil {
		problem = joinProblems(problem, fmt.Sprintf("the runs that owe a step could not be read: %v", err))
	} else {
		for _, state := range outstanding {
			attention = append(attention, owedStepAttention(state))
		}
	}
	// What is awaiting the forge is read by the record's own predicate, over every
	// recorded run, because it is the same reading the channel's heartbeat counts:
	// a count said hourly in a channel and a line absent from the terminal is the
	// disagreement this whole package exists to prevent.
	if recorded, err := sources.Runs.Recorded(); err != nil {
		problem = joinProblems(problem, fmt.Sprintf("the promotions awaiting the forge could not be read: %v", err))
	} else {
		for _, state := range AwaitingForge(recorded) {
			attention = append(attention, awaitingForgeAttention(state))
		}
	}
	return attention, problem
}

// Held is the admitted work somebody has to release, as attention rather than as
// queue entries: how many items wait on the development manager's decision and
// how many wait on the harness carrying out decisions she has already recorded.
//
// The two are separate entries because they are separate people, and that is the
// whole of what this exists for. Held work was named on the attention line only
// through the queue's own refusals, one per item and all of them wording one
// state, so an operator counting them read every held item as a decision
// somebody owed. On 2026-09-07 that was thirty-three items and none of them: the
// development manager had decided each one, and the gap was the harness never
// carrying them out.
//
// Counts rather than identifiers, for the reason the attention line bounds
// everything else it carries: what this line answers is who has to move, and a
// reader who wants the items has the not-startable line above it, which names
// them with the reason against each.
func Held(awaitingDecision, awaitingCarryOut int) []Attention {
	attention := make([]Attention, 0, 2)
	if awaitingDecision > 0 {
		attention = append(attention, heldWorkAttention(HeldAwaitingDecision, awaitingDecision))
	}
	if awaitingCarryOut > 0 {
		attention = append(attention, heldWorkAttention(HeldAwaitingCarryOut, awaitingCarryOut))
	}
	return attention
}

// awaits agrees the verb with the count, because a line that says "1 admitted
// item await" is one a reader stops trusting the arithmetic of.
func awaits(count int) string {
	if count == 1 {
		return "awaits"
	}
	return "await"
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
		attention = append(attention, carriedItemAttention(entry.ID, entry.Executor))
	}
	return attention
}

// Pausing is every directive holding one work item up, for a surface that has to
// name them rather than count them: a thread settling what it is waiting on has
// to know whether that is one thing or several before it settles anything.
//
// It is here rather than in the surface that asks, and it reads through the same
// source the four lines do, because which directives hold an item is the reading
// the run pipeline enforces on: a channel that worked it out its own way could
// lift a pause the pipeline still holds. Nothing about it revises the record —
// this says what is in force, and settling one stays with the acts that settle
// directives. That division is the architect's standing ruling rather than this
// package's preference; docs/developing-yoyo.md records which ruling and what it
// said, under "Where a surface reads the work's own state from".
//
// A directive that named no scope holds every item, so it is here too. That is
// the pipeline's own reading and the honest one: it is what stops this item, and
// settling it from here settles it wherever else it was stopping work. A surface
// acting on this says so where it acts — a settlement that reached past the item
// somebody was looking at is the one thing they could not have known from the
// thread they were in.
func Pausing(sources Sources, workItemID string) ([]directive.Directive, error) {
	if sources.Directives == nil {
		return nil, errors.New("nothing was wired to read the recorded directives")
	}
	recorded, err := sources.Directives.List()
	if err != nil {
		return nil, fmt.Errorf("the recorded directives could not be read: %w", err)
	}
	pausing := directive.Pausing(recorded, workItemID)
	directive.Sort(pausing)
	return pausing, nil
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
// and says where it was cut. The fold and the cut are oneline's, on a rune
// boundary, because a line truncated mid-rune is not text; the mark is this
// package's own ellipsis, which every status line already ends a cut with.
func singleLine(text string, limit int) string {
	bounded := oneline.Bound(text, limit)
	if len(bounded) == len(strings.Join(strings.Fields(text), " ")) {
		return bounded
	}
	return bounded + "…"
}
