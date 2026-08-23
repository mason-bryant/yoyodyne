// Package config loads Yoyodyne's effective configuration from a project's
// .yoyodyne directory. A project owns its configuration outright: `yoyo init`
// generates a complete file from the versioned bundle shipped inside the
// executable and copies that bundle's personas into the project, so what runs
// is what the project can read. A project may still inherit the bundle by name
// with "extends" and overlay only what it changes, which trades the legibility
// of an explicit file for defaults that improve when the executable does.
// Either shape is usable from a repository with no access to the Yoyodyne
// source checkout.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/research"
)

const CurrentVersion = 1

// DefaultSpecifications is where the product manager reads product intent from
// when nothing names another directory. It is the layout the design recommends
// for human-readable product artifacts, so a project that followed that layout
// needs no setting at all.
const DefaultSpecifications = "docs/product"

// DefaultInvariants is where the architect's durable invariants live when
// nothing names another directory. It is the recommended layout for the same
// reason the specifications default is: a project that followed it writes
// nothing down, and a project with no such directory simply has no invariants
// yet rather than a broken configuration.
const DefaultInvariants = "docs/decisions/invariants"

// DefaultDesigns and DefaultDecisions are the other two homes canonical
// artifacts live in when nothing names another directory. Together with the
// specifications directory they are what the harness reads artifact identity
// and metadata from, and they follow the recommended layout for the same reason
// the invariants default does: a project that followed it writes nothing down,
// and a project with no such directory simply records no artifacts of that kind
// yet. The decisions home contains the invariants one by default, and the
// invariants keep their own identity scheme rather than being read twice.
const (
	DefaultDesigns   = "docs/designs"
	DefaultDecisions = "docs/decisions"
)

type Config struct {
	Version int `yaml:"version" json:"version"`
	// Extends names the built-in bundle this configuration inherits from, and
	// is empty for a complete standalone configuration.
	Extends   string                 `yaml:"extends,omitempty" json:"extends,omitempty"`
	Product   Product                `yaml:"product" json:"product"`
	Execution Execution              `yaml:"execution" json:"execution"`
	Triage    Triage                 `yaml:"triage" json:"triage"`
	Exchange  Exchange               `yaml:"exchange" json:"exchange"`
	Research  Research               `yaml:"research,omitempty" json:"research,omitempty"`
	Approvals Approvals              `yaml:"approvals" json:"approvals"`
	Checks    []string               `yaml:"checks" json:"checks"`
	Agents    map[string]AgentConfig `yaml:"agents" json:"agents"`
	// Accounts are the provider accounts this project runs agents under, keyed by
	// the alias each one is named by. It is top level rather than under `agents`
	// because an account is a thing several agents share: which roles run on which
	// account is stated on the agents, and what accounts exist is stated once
	// here. A project that names none runs under the default alias, which is what
	// makes a single-account project write nothing and still record what it ran
	// under.
	Accounts map[string]Account `yaml:"accounts,omitempty" json:"accounts,omitempty"`
	// Operators are the humans the project recognizes, keyed by a short name for
	// each one. It is top level rather than under any one surface because a human
	// is known by several, and the authority is the human's: an act is authorized
	// by resolving whichever namespace it arrived through to a person. It is
	// absent from a project that has named nobody, which recognizes nobody rather
	// than everybody.
	Operators map[string]Operator `yaml:"operators,omitempty" json:"operators,omitempty"`
	// Slack configures the reporting sink. It is absent from a project that does
	// not report to a workspace, which is every project until one opts in.
	Slack Slack `yaml:"slack,omitempty" json:"slack,omitempty"`
}

// Slack is what a project says about reporting into a chat workspace. What it
// deliberately does not hold is a credential: the sink's two tokens live in the
// sink process's environment and nowhere else, so nothing that is read into a
// prompt, a context bundle, or a run's environment can carry one. What is here
// is identity and addressing, which is configuration in the ordinary sense —
// checked in, reviewed with the code, and readable by anybody who can read the
// repository.
//
// What it deliberately no longer holds either is the allow-list of who may steer
// the harness from the workspace. Who counts as an operator is a fact about
// humans rather than about Slack, so it is stated once in the top-level
// operators mapping and read back through Config.SlackOperators: the humans
// granted direct-work who have bound a member id. An allow-list authored beside
// those grants is one that disagrees with them — silently, and about authority.
type Slack struct {
	// Enabled is the switch. A project that has not set it reports nothing, and
	// the sink refuses to start rather than posting into a workspace nobody
	// configured.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// Channel is where the threads are opened, as a channel id (the stable
	// thing, which a rename does not break) or a #name. A project that enables
	// Slack without one is refused at load, before any work is claimed, because
	// the alternative is a sink that starts, reads a stream, and then discovers
	// it has nowhere to post.
	Channel string `yaml:"channel,omitempty" json:"channel,omitempty"`
	// Avatars overrides the picture beside a speaker's name, keyed by role —
	// `developer`, `reviewer`, and the rest — or by `harness` for what no persona
	// did. A value is an emoji shortcode, including one this workspace added
	// itself, or the https URL of an image. A speaker with no entry keeps the avatar
	// the harness ships, so a project that names one names one.
	//
	// Only the picture is here. The name a message appears under, and whose
	// account it is, are deliberately not configurable: who speaks is a claim
	// about who did the work, and a project that could rewrite it could attribute
	// a promotion to a developer. The avatar carries none of that — everything it
	// distinguishes is already distinguished by the name beside it — which is what
	// makes it the part that can be a preference.
	Avatars map[string]string `yaml:"avatars,omitempty" json:"avatars,omitempty"`
}

type Product struct {
	ID           domain.ProductID    `yaml:"id" json:"id"`
	RepositoryID domain.RepositoryID `yaml:"repository_id,omitempty" json:"repository_id,omitempty"`
	Repository   string              `yaml:"repository" json:"repository"`
	// Specifications is the directory the product manager reads product intent
	// from, relative to the repository root. It is the whole of what that role
	// is shown about the product, so it is confined to the repository: a path
	// that escapes it would put arbitrary text in front of the role that decides
	// what the product is for.
	Specifications string `yaml:"specifications" json:"specifications"`
	// Invariants is the directory of durable architectural invariants the
	// architect owns, relative to the repository root. The harness reads it to
	// deliver the ones relevant to a work item into the developer's context and
	// the reviewer's evidence, so it is confined to the repository for the same
	// reason the specifications are: a path that escaped it would put arbitrary
	// text in front of every developer as a constraint on their change.
	Invariants string `yaml:"invariants" json:"invariants"`
	// Designs and Decisions are the architect's two artifact homes, relative to
	// the repository root: the designs and specifications that serve the goals,
	// and the decision records the invariants are extracted from. With
	// Specifications they are the directories the harness reads canonical
	// artifact identity and metadata from, so they are confined to the
	// repository for the same reason the others are.
	Designs   string `yaml:"designs" json:"designs"`
	Decisions string `yaml:"decisions" json:"decisions"`
}

type Execution struct {
	MaxConcurrentDevelopers    int `yaml:"max_concurrent_developers" json:"max_concurrent_developers"`
	RepairAttemptsBeforeReplan int `yaml:"repair_attempts_before_replan" json:"repair_attempts_before_replan"`
	// IntegrationRetriesBeforeReconciliation bounds how many times a run whose
	// promotion lost a race — to another run, or to whoever moved the target
	// branch mid-run — replays its change onto where the target went and tries
	// again. Each retry re-runs the deterministic checks and obtains a fresh
	// independent review, because the reviewed change is not the change that
	// would now be promoted. Zero never retries, which is the behavior a run had
	// before this bound existed: the first refusal ends it.
	IntegrationRetriesBeforeReconciliation int `yaml:"integration_retries_before_reconciliation" json:"integration_retries_before_reconciliation"`
	// TransientRelaunchesBeforeBlocking bounds how many times a run reissues a
	// provider invocation that died without judging the work — an API error the
	// provider's own retries did not outlast, or a response cut off mid-flight.
	// The relaunch continues the same worktree and the same session, so nothing
	// the dead attempt built is redone. One budget covers the developer and the
	// reviewer, because what it bounds is how much of the provider's weather one
	// run absorbs. Zero never relaunches, which is the behavior a run had before
	// this bound existed: the first transient death ends it.
	TransientRelaunchesBeforeBlocking int    `yaml:"transient_relaunches_before_blocking" json:"transient_relaunches_before_blocking"`
	WorktreeRoot                      string `yaml:"worktree_root" json:"worktree_root"`
	// Remote names the Git remote publishing pushes to and opens pull requests
	// against. It is only consulted when `approvals.publishing` is automatic, and
	// a repository that has no remote by this name publishes nothing rather than
	// failing.
	Remote string `yaml:"remote" json:"remote"`
	// UsageLimitMaxPause bounds how long a run may wait for an exhausted
	// provider usage limit to reset. A reset further away than this is treated
	// as no usable reset time at all: the run stops and records a blocker
	// instead of sleeping on it. Zero disables waiting entirely, so every
	// exhausted limit blocks immediately.
	UsageLimitMaxPause Duration `yaml:"usage_limit_max_pause" json:"usage_limit_max_pause"`
	// UsageLimitInProcessPause is how much of that bound a run will spend
	// sleeping inside this process. The process sleeps probes until it has spent
	// this much on one run and then exits with the run still in flight and its
	// deadline recorded, so a later invocation resumes it rather than holding a
	// process open for hours. It is measured against every probe this process has
	// already slept, across phases, rather than against each probe separately: a
	// bound applied per probe would not bound how long the process stays open,
	// because a probe interval that fits under it fits however many times it is
	// taken. It is never larger than UsageLimitMaxPause in effect, because a
	// pause beyond that bound is refused before either path is chosen.
	UsageLimitInProcessPause Duration `yaml:"usage_limit_in_process_pause" json:"usage_limit_in_process_pause"`
	// UsageLimitUnknownResetPause is the interval between probes: how long a run
	// sleeps before reissuing the attempt to find out whether the provider will
	// serve it now. It applies whether or not a reset time was named, which is
	// the whole of the polling discipline. A limit reported without one is not
	// the same as having no capacity — the monthly overage allowance reports this
	// way while the ordinary rolling window keeps resetting on its usual schedule
	// — so it is waitable and simply carries no deadline. A limit reported with
	// one carries a deadline that bounds the wait rather than gating it, because
	// a reset time is a claim about the provider and claims go stale in both
	// directions. Either way a run sleeps this interval or the time left to the
	// deadline, whichever is shorter, and asks again. Every probe spends
	// UsageLimitMaxPause, so a provider that keeps refusing walks into that bound
	// rather than polling forever.
	UsageLimitUnknownResetPause Duration `yaml:"usage_limit_unknown_reset_pause" json:"usage_limit_unknown_reset_pause"`
	// CheckTimeout is the total budget one configured check gets: the whole time
	// it may run, not the time it may stay quiet. It scales with the work rather
	// than with the machine, so it is configured rather than fixed — a suite
	// grows, and N runs at once multiply its wall clock without multiplying the
	// cores it runs on, so the budget has to be raised or the runs serialized.
	// A check killed at this bound is not a check that failed: it is work that
	// may have been passing the whole time, which is why every check reports
	// what it spent against this budget rather than only the one that ran out.
	CheckTimeout Duration `yaml:"check_timeout" json:"check_timeout"`
	// ServerOverloadPause is how long a run waits before reissuing an attempt the
	// provider refused because its own servers were transiently overloaded. It is
	// the same polling discipline as an exhausted limit with a different clock:
	// an overload quotes no reset time and lifts in seconds rather than hours, so
	// waiting the usage-limit probe interval would park a run for half an hour on
	// a condition that had already passed. It spends UsageLimitMaxPause like every
	// other wait, so a provider that stays overloaded walks into that bound rather
	// than reissuing forever.
	ServerOverloadPause Duration `yaml:"server_overload_pause" json:"server_overload_pause"`
	// WorkPoll is how long a watch session waits before reading the queue again
	// when it found nothing to start. It is the whole cost of an idle watch: one
	// local tracker read per interval, no provider anywhere near it. Nothing is
	// cached between polls, so it is also the whole of the latency on a change —
	// work admitted, reprioritized, or unblocked is picked up at the next poll
	// rather than by anything detecting it.
	WorkPoll Duration `yaml:"work_poll" json:"work_poll"`
	// BlockedRunsBeforeIntakeHold is the failure-storm brake: this many runs
	// blocking in a row, with nothing landing between them, holds intake and
	// leaves it held for the operator to lift. It is a bound on systemic
	// breakage rather than on any one item — an item that keeps failing is the
	// per-item cooldown's business — and it exists because a watch session left
	// overnight against a broken machine would otherwise put the whole queue
	// through a run each and dock every one of them. Zero never brakes, which is
	// the behaviour a pass had before this bound existed.
	BlockedRunsBeforeIntakeHold int `yaml:"blocked_runs_before_intake_hold" json:"blocked_runs_before_intake_hold"`
}

const (
	// defaultRemote is the remote publishing uses when nothing names another. A
	// repository with no remote by this name is not an error: it simply publishes
	// nothing and behaves exactly as a local-only project does.
	defaultRemote = "origin"
	// defaultUsageLimitMaxPause covers the provider's five-hour limit with slack
	// and stops short of its seven-day one. A run that would have to sleep for
	// days is not a run that should be sleeping: it stops and records a blocker,
	// so the capacity problem reaches a person instead of a timer.
	defaultUsageLimitMaxPause = Duration(6 * time.Hour)
	// defaultUsageLimitInProcessPause equals the maximum pause, so by default
	// every wait the harness will take at all is taken in this process and the
	// run continues on its own — which is what waiting out a usage limit is for.
	// Lowering it trades that for a run that exits with its deadline recorded
	// and is resumed by a later invocation.
	defaultUsageLimitInProcessPause = defaultUsageLimitMaxPause
	// defaultUsageLimitUnknownResetPause is how long to wait between probes,
	// whether or not a reset time was named. Short enough to resume soon after a
	// window rolls, long enough not to hammer a provider that is refusing — and
	// deliberately checked at least this often even under a multi-hour quoted
	// reset, because a quoted reset can be overtaken by a window that rolls or by
	// capacity somebody bought.
	defaultUsageLimitUnknownResetPause = Duration(30 * time.Minute)
	// defaultCheckTimeout has room for a suite several times the size of the one
	// that provoked it. The flat ten minutes it replaces killed a run whose
	// tests were passing package by package under the contention of two
	// concurrent runs, so the default is set against the contended case rather
	// than the single-run one: this repository's own suite takes well under two
	// minutes with two of them running at once, and a project that outgrows
	// thirty minutes raises this rather than meeting it as a failed run.
	defaultCheckTimeout = Duration(30 * time.Minute)
	// defaultServerOverloadPause is long enough to be worth waiting — the
	// provider CLI has already spent its own ten retries on the condition before
	// the harness ever sees it — and short enough that a run resumes within a
	// minute or two of the overload lifting, which is the timescale the provider's
	// own message describes.
	defaultServerOverloadPause = Duration(90 * time.Second)
	// defaultWorkPoll is a minute, which is the granularity a person steering a
	// backlog actually works at: work admitted or reordered is picked up within
	// a minute, and an idle session costs one tracker read a minute to be that
	// responsive. Shorter buys latency nobody is waiting on; much longer makes
	// reordering the queue feel like it did nothing.
	defaultWorkPoll = Duration(60 * time.Second)
	// defaultBlockedRunsBeforeIntakeHold is three, which is the same shape of
	// bound as the repair and relaunch budgets: enough that one bad item and the
	// unlucky item after it do not stop the line, and short of a session that
	// spends the whole backlog finding out the machine is broken.
	defaultBlockedRunsBeforeIntakeHold = 3
)

// Triage is what the triage workflow measures against: when work that has
// stopped moving is docketed to be looked at, and what looking at it may spend.
// The numbers are configuration rather than constants in the workflow because
// each one is a judgement about a project's pace — how long a merge may take
// before nobody merging it is news, how many times one change may go round with
// a reviewer before going round again is the problem — and a project sets that
// differently from the next one.
type Triage struct {
	// StuckMergeAge is how long an approved publication may sit unmerged before
	// it is docketed. It is an age rather than a deadline because what makes a
	// publication stuck is that nothing has happened to it, and nothing
	// happening offers no event to hang a deadline on. Zero is refused rather
	// than read as a choice: a threshold of no time at all dockets every
	// publication the instant it is made, which is a docket of everything and a
	// triage of nothing.
	StuckMergeAge Duration `yaml:"stuck_merge_age" json:"stuck_merge_age"`
	// ReviewRoundsCap bounds the review rounds one work item may accumulate in
	// total — across repairs, across runs — past which triage may no longer hand
	// it back for another repair. Past the cap triage still has both of its other
	// actions: escalate the item, or re-scope it. What it may not do is buy the
	// same argument another round. Zero is a deliberate choice rather than an
	// error, which is why it is accepted: it says an item that reaches triage is
	// never repaired again, only escalated or re-scoped.
	ReviewRoundsCap int `yaml:"review_rounds_cap" json:"review_rounds_cap"`
	// RepairGrantAttempts is how many repair attempts triage hands an item when
	// it decides the work is worth another go. A project that states nothing
	// gets its configured execution.repair_attempts_before_replan, because a
	// grant is the same kind of budget a run starts with and a project that
	// tuned one has said what it thinks that budget is worth. It is never zero:
	// a grant of nothing changes nothing about the item it was granted to, so it
	// is refused where it is stated and floored at one where it is derived from
	// a repair budget of zero.
	RepairGrantAttempts int `yaml:"repair_grant_attempts" json:"repair_grant_attempts"`
}

const (
	// defaultStuckMergeAge is long enough that an ordinary merge lands well
	// inside it — including one waiting on a person who stepped away from the
	// keyboard — and short enough that a publication nobody is going to merge is
	// looked at within the working session that produced it.
	defaultStuckMergeAge = Duration(2 * time.Hour)
	// defaultReviewRoundsCap is two rounds past the repair budget a run starts
	// with, so an item reaches the cap only after triage has already bought it
	// more rounds than the run itself would have taken. A change still arguing
	// with its reviewer that far in is not a change another round fixes.
	defaultReviewRoundsCap = 4
	// minimumRepairGrant is the floor under a derived grant. A project that
	// configured no routine repair attempts at all still gets a grant of one,
	// because the grant is triage's deliberate exception to that budget rather
	// than another helping of it, and a grant of nothing is not an exception.
	minimumRepairGrant = 1
)

// Exchange is what the inter-role ask channel is bounded by. Two roles talking
// to each other is the one thing here with no natural end: each of them is a
// judgement model, each can always find something further worth saying, and
// neither is the operator. So an exchange is opened with a hard limit on rounds,
// and the limit is a project's judgement about how long a question between two
// of its roles is worth going on for.
type Exchange struct {
	// MaxRounds is the most rounds one exchange thread may take. Reaching it
	// closes the exchange as unresolved and escalates it to the operator, which
	// is what turns the pathological case — two roles deferring to each other for
	// ever — into a rare, legible question somebody can answer. It is copied onto
	// each exchange as it opens, so changing it never lengthens a thread already
	// running long.
	//
	// Zero is not a choice here, unlike the triage caps: an exchange allowed no
	// round at all is a channel that is off, and turning the channel off is
	// leaving the block out of a persona rather than configuring a limit nothing
	// can be spent against. It is refused, and one is the floor.
	MaxRounds int `yaml:"max_rounds" json:"max_rounds"`
}

// defaultExchangeMaxRounds is far more rounds than a question between two roles
// has ever needed and few enough that the loop this bounds costs a knowable
// amount before it reaches the operator.
const defaultExchangeMaxRounds = exchange.DefaultMaxRounds

// Research is what this project permits the product manager to find out from
// outside the repository, and what it is bounded by. Every part of it is a
// judgement an operator makes about their own money, their own privacy, and
// their own patience, which is why it is configuration and why its default is
// nothing at all.
//
// A project that states no source has the capability off. That is the important
// default and it is deliberate: a conversational role reaching the network is
// something an operator turns on, naming what it may reach, rather than
// something they acquire by extending a bundle or upgrading the executable. It
// is the same rule work_items, integration, and publishing are held to.
type Research struct {
	// Sources are the evidence sources the operator permits, each a command the
	// harness runs with the question on standard input and whose standard output
	// is the evidence. It is a command rather than a built-in integration for the
	// reason the checks are commands: the operator decides what runs, in the file
	// they decide everything else in, and the harness reaches nothing they did not
	// name.
	//
	// It replaces an inherited list wholesale rather than adding to it, like the
	// check list: what the harness may reach is one statement, and half of one
	// operator's answer joined to half of another's is a policy nobody decided.
	Sources []research.Source `yaml:"sources,omitempty" json:"sources,omitempty"`
	// MaxQueriesPerTurn is the cost bound: how many questions one reply may set
	// off. Zero takes the harness default, and it is capped at what the protocol
	// itself permits, so a project cannot configure its way past a limit the block
	// enforces.
	MaxQueriesPerTurn int `yaml:"max_queries_per_turn,omitempty" json:"max_queries_per_turn,omitempty"`
	// Timeout is the time bound, per question. Zero takes the harness default. It
	// is never zero in effect: a source with no budget holds a conversation open
	// for as long as it keeps not answering, with the operator sitting in front of
	// it.
	Timeout Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// Policy is this configuration as the research capability reads it. It is
// derived rather than stored so the two can never disagree about what a project
// permitted.
func (r Research) Policy() research.Policy {
	return research.Policy{
		Sources:           r.Sources,
		MaxQueriesPerTurn: r.MaxQueriesPerTurn,
		Timeout:           time.Duration(r.Timeout),
	}
}

type Approvals struct {
	// Brief, Goals, and Designs decide which canonical documents the operator's
	// approval is asked for. `human` means the operator approves the document and
	// the approval is recorded against it, in its frontmatter and against the
	// revision it was given for, so an approved document and a draft one are
	// distinguishable and a document amended after approval is distinguishable
	// from both. Neither setting gates anything: an unapproved document still
	// loads and still governs what is downstream of it, and what is reported is
	// what is recorded. Goals governs the non-goals with the goals, because a
	// bound on intent nobody approved is as much unapproved intent as a goal is.
	//
	// The shipped bundle says `human` for the brief and the goals deliberately
	// rather than by inheritance: they are what the operator states and the whole
	// of what the design asks a person to approve routinely, and everything
	// downstream traces to them. Designs are `automatic` for the same reason read
	// the other way — a design that serves an approved goal is the architect's
	// judgement about how, and asking a person to approve each one is the
	// per-change gate autonomy is the absence of.
	Brief   domain.ApprovalMode `yaml:"brief" json:"brief"`
	Goals   domain.ApprovalMode `yaml:"goals" json:"goals"`
	Designs domain.ApprovalMode `yaml:"designs" json:"designs"`
	// WorkItems decides whether the operator is asked about every work item
	// before it reaches the queue. It is the one approval that gates rather than
	// records: `human` is the per-item gate the harness started with, where the
	// product manager proposes and nothing exists until the operator says so,
	// and `automatic` moves that decision up to the goals — work that traces to
	// a goal the operator approved is admitted without a further prompt, and
	// everything else is still put to them.
	//
	// "Everything else" is not a residue. Work that traces to no goal, work that
	// would cut against one, and work the product manager judges to be against
	// what the product is for are exactly the cases it escalates rather than
	// proposes, and a change to the goals themselves is the operator's and
	// reaches the queue through nothing. Approval moved up a level; it did not
	// disappear.
	//
	// `automatic` requires the operator to be approving goals at all, which is
	// checked below: the whole weight of admitting work without asking rests on
	// the goal it serves having been approved, and a project recording no goal
	// approvals has nothing for it to rest on. That refusal only ever names a key
	// its author wrote, because `automatic` is never inherited — see below.
	//
	// Both the harness default and the shipped bundle say `human`, deliberately
	// and at the same value: this is the setting that lets work reach the queue
	// with no person in the loop, so a project turns it on rather than acquiring
	// it by extending a bundle or by upgrading the executable. That is the same
	// rule integration and publishing are held to, and for the same reason.
	WorkItems domain.ApprovalMode `yaml:"work_items" json:"work_items"`
	// WorkItemExemptions are the classes of work this project admits without
	// asking, whatever WorkItems says. It exists because the per-item gate turned
	// out to be coarser than the operators who keep it actually mean: an operator
	// who wants to be asked about every change to the product does not
	// necessarily want to be asked before something reads the repository and
	// writes down what it found, and with no way to say so the policy they
	// recorded stays a sentence nothing enforces.
	//
	// It is empty by default, which is the gate exactly as it stands: an
	// exemption is an operator handing over a decision, and one that arrived by
	// inheritance or by upgrading the executable would not be that. Each class is
	// stated rather than derived, and the harness recognizes only the classes it
	// names — a project naming anything else is refused rather than quietly
	// exempting nothing.
	//
	// What it does not move is the goal. Work admitted under an exemption still
	// names a goal the repository records, because an exemption is about who is
	// asked and never about whether the work is for anything.
	WorkItemExemptions []domain.WorkItemClass `yaml:"work_item_exemptions" json:"work_item_exemptions,omitempty"`
	Integration        domain.ApprovalMode    `yaml:"integration" json:"integration"`
	// Publishing decides whether the harness pushes a run's branch and opens the
	// pull request its reviewer's verdict merges. It sits beside integration
	// because publishing has the wider blast radius of the two: integration moves
	// a local branch, publishing puts the work somewhere other people see it.
	// `human` leaves pushing and pull requests to the operator, which is what a
	// project gets until it opts in.
	Publishing domain.ApprovalMode `yaml:"publishing" json:"publishing"`
}

type AgentConfig struct {
	Role    domain.AgentRole `yaml:"role" json:"role"`
	Backend domain.Backend   `yaml:"backend" json:"backend"`
	// Model is the required provider model selector for every instance of this
	// agent. There is no implicit harness default: a family alias such as
	// "opus" intentionally floats to the backend's current default for that
	// family, while an exact provider identifier pins a version.
	Model string `yaml:"model" json:"model"`
	// Account is the alias of the provider account this agent runs under, from
	// the top-level accounts mapping. The assignment is the operator's and it is
	// fixed: an agent runs where the configuration says it runs, and nothing
	// chooses at run time. A project that states nothing has every agent assigned
	// to its single account, which is the only arrangement v1 executes.
	Account   string `yaml:"account,omitempty" json:"account,omitempty"`
	Instances int    `yaml:"instances,omitempty" json:"instances"`
	// Persona is the resolved role guidance handed to this agent's prompt. It
	// may specialize how an agent works; it can never remove a harness
	// invariant, which is why the immutable contracts stay in Go.
	Persona Persona `yaml:"persona,omitempty" json:"persona,omitempty"`
}

// MaxPersonaBytes bounds a persona so role guidance stays guidance rather than
// an unbounded document smuggled into every prompt.
const MaxPersonaBytes = 32 << 10

// Persona is one resolved persona: the declared revision, the path it was
// declared with, where the text was actually read from, and the text itself.
// Text is excluded from serialized diagnostics, which report its size instead,
// so `config show` stays readable.
type Persona struct {
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
	Path    string `yaml:"path,omitempty" json:"path,omitempty"`
	Source  string `yaml:"source,omitempty" json:"source,omitempty"`
	Bytes   int    `yaml:"bytes,omitempty" json:"bytes,omitempty"`
	Text    string `yaml:"-" json:"-"`
}

// Defined reports whether an agent declares a persona at all. Agents in legacy
// complete configurations may declare none, and prompt construction then falls
// back to the immutable harness contract alone.
func (p Persona) Defined() bool {
	return strings.TrimSpace(p.Version) != "" || strings.TrimSpace(p.Path) != "" || strings.TrimSpace(p.Text) != ""
}

func (c Config) Validate() error {
	var problems []string

	if c.Version != CurrentVersion {
		problems = append(problems, fmt.Sprintf("version must be %d", CurrentVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(c.Product.ID)); err != nil {
		problems = append(problems, err.Error())
	}
	if err := domain.ValidateIdentifier("repository id", string(c.Product.RepositoryID)); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(c.Product.Repository) == "" {
		problems = append(problems, "product repository is required")
	}
	if err := validateSpecificationsDirectory(c.Product.Specifications); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateRepositoryDirectory("product invariants", c.Product.Invariants); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateRepositoryDirectory("product designs", c.Product.Designs); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateRepositoryDirectory("product decisions", c.Product.Decisions); err != nil {
		problems = append(problems, err.Error())
	}
	if c.Execution.MaxConcurrentDevelopers < 1 {
		problems = append(problems, "max_concurrent_developers must be at least 1")
	}
	if c.Execution.RepairAttemptsBeforeReplan < 0 {
		problems = append(problems, "repair_attempts_before_replan cannot be negative")
	}
	// Zero is a deliberate choice here too — never retry a refused promotion —
	// so only a negative bound, which describes no retry anybody could take, is
	// refused.
	if c.Execution.IntegrationRetriesBeforeReconciliation < 0 {
		problems = append(problems, "integration_retries_before_reconciliation cannot be negative")
	}
	// And here, for the same reason: zero says fail on the first transient death,
	// which is a choice, and a negative bound is not one.
	if c.Execution.TransientRelaunchesBeforeBlocking < 0 {
		problems = append(problems, "transient_relaunches_before_blocking cannot be negative")
	}
	if strings.TrimSpace(c.Execution.WorktreeRoot) == "" {
		problems = append(problems, "worktree_root is required")
	}
	if err := validateRemoteName(c.Execution.Remote); err != nil {
		problems = append(problems, err.Error())
	}
	// Zero is a deliberate choice — never wait, block on every exhausted limit —
	// so only a negative bound, which describes no wait anybody could take, is
	// refused.
	if c.Execution.UsageLimitMaxPause < 0 {
		problems = append(problems, "usage_limit_max_pause cannot be negative")
	}
	if c.Execution.UsageLimitUnknownResetPause <= 0 {
		problems = append(problems, "execution.usage_limit_unknown_reset_pause must be positive")
	}
	if c.Execution.UsageLimitInProcessPause < 0 {
		problems = append(problems, "usage_limit_in_process_pause cannot be negative")
	}
	// Zero is not a deliberate choice here, unlike the pauses above: a check with
	// no budget at all holds a worktree, a claim, and a run open for as long as
	// it keeps running, and nothing else bounds it.
	if c.Execution.CheckTimeout <= 0 {
		problems = append(problems, "execution.check_timeout must be positive")
	}
	// An overload names no reset time, so this interval is the whole of the wait
	// rather than a bound on it. Zero would mean reissuing straight back into the
	// same overloaded server with nothing between the attempts.
	if c.Execution.ServerOverloadPause <= 0 {
		problems = append(problems, "execution.server_overload_pause must be positive")
	}
	// Zero is not a choice here either: a watch session that polled with nothing
	// between the readings would read the tracker as fast as the machine allows
	// for as long as the queue stayed empty, which is a spin rather than a watch.
	if c.Execution.WorkPoll <= 0 {
		problems = append(problems, "execution.work_poll must be positive")
	}
	// Zero is a choice here — never brake, let the operator be the only thing
	// that holds intake — so only a negative bound, which describes no run
	// anybody could count, is refused.
	if c.Execution.BlockedRunsBeforeIntakeHold < 0 {
		problems = append(problems, "blocked_runs_before_intake_hold cannot be negative")
	}
	// An age of zero is not the choice the pauses above make with theirs: it
	// dockets every approved publication the instant it is made, which is not a
	// stricter triage but the end of triage meaning anything.
	if c.Triage.StuckMergeAge <= 0 {
		problems = append(problems, "triage.stuck_merge_age must be positive")
	}
	// Zero is a choice here — an item that reaches triage is escalated or
	// re-scoped rather than repaired again — so only a negative cap, which
	// describes no round anybody could take, is refused.
	if c.Triage.ReviewRoundsCap < 0 {
		problems = append(problems, "triage.review_rounds_cap cannot be negative")
	}
	// And zero is not a choice here: triage granting no attempts leaves the item
	// exactly where granting nothing would have, so it is refused rather than
	// silently spent.
	if c.Triage.RepairGrantAttempts < minimumRepairGrant {
		problems = append(problems, "triage.repair_grant_attempts must be at least 1")
	}
	// And nor is zero a choice here: an exchange that may take no round is a
	// question nobody can put, which is what leaving the channel unused already
	// is.
	if c.Exchange.MaxRounds < 1 {
		problems = append(problems, "exchange.max_rounds must be at least 1")
	}
	problems = append(problems, c.Research.problems()...)

	approvalValues := []struct {
		name string
		mode domain.ApprovalMode
	}{
		{name: "brief", mode: c.Approvals.Brief},
		{name: "goals", mode: c.Approvals.Goals},
		{name: "designs", mode: c.Approvals.Designs},
		{name: "work_items", mode: c.Approvals.WorkItems},
		{name: "integration", mode: c.Approvals.Integration},
		{name: "publishing", mode: c.Approvals.Publishing},
	}
	for _, approval := range approvalValues {
		if !approval.mode.Valid() {
			problems = append(problems, fmt.Sprintf("approval %s must be %q or %q", approval.name, domain.ApprovalHuman, domain.ApprovalAutomatic))
		}
	}
	// A class the harness does not recognize exempts nothing, so a file naming one
	// is refused rather than loaded with a policy its author believes is in force.
	// A duplicate is refused for the same reason: it is a file saying something
	// twice, which is the shape a merge that went wrong leaves behind.
	exempted := make(map[domain.WorkItemClass]struct{}, len(c.Approvals.WorkItemExemptions))
	for _, class := range c.Approvals.WorkItemExemptions {
		if !class.Valid() {
			problems = append(problems, fmt.Sprintf("approvals.work_item_exemptions names %q, which is not a class the harness recognizes; the classes there are: %s", class, namedWorkItemClasses()))
			continue
		}
		if _, duplicate := exempted[class]; duplicate {
			problems = append(problems, fmt.Sprintf("approvals.work_item_exemptions names %q twice", class))
			continue
		}
		exempted[class] = struct{}{}
	}

	if len(c.Agents) == 0 {
		problems = append(problems, "at least one agent is required")
	}

	agentNames := make([]string, 0, len(c.Agents))
	for name := range c.Agents {
		agentNames = append(agentNames, name)
	}
	sort.Strings(agentNames)

	developers := 0
	reviewers := 0
	for _, name := range agentNames {
		agent := c.Agents[name]
		if err := domain.ValidateIdentifier("agent name", name); err != nil {
			problems = append(problems, err.Error())
		}
		// The set of roles is fixed in the harness, so a name outside it is
		// refused here rather than reaching the backend that assembles the
		// invocation: everything downstream — the role's authority, its tool
		// posture, whether it counts as a developer — is derived from the name,
		// and a typo in an agents block would otherwise load and fail only once
		// work had been claimed. The known roles are named because the whole
		// point of the refusal is that the operator can see which one was meant.
		roleKnown := agent.Role.Valid()
		if strings.TrimSpace(string(agent.Role)) == "" {
			problems = append(problems, fmt.Sprintf("agent %q role is required", name))
		} else if !roleKnown {
			problems = append(problems, fmt.Sprintf("agent %q has unknown role %q; roles are %s", name, agent.Role, describeRoles()))
		}
		if !agent.Backend.Valid() {
			problems = append(problems, fmt.Sprintf("agent %q has unsupported backend %q", name, agent.Backend))
		} else if roleKnown && !agent.Backend.SupportsRole(agent.Role) {
			problems = append(problems, fmt.Sprintf("backend %q does not support role %q for agent %q", agent.Backend, agent.Role, name))
		}
		// Every executable agent declares its own selector; the harness never
		// falls back to a provider default nobody chose or recorded.
		if err := validateModelSelector(agent.Model); err != nil {
			problems = append(problems, fmt.Sprintf("agent %q %s", name, err))
		}
		if agent.Instances < 1 {
			problems = append(problems, fmt.Sprintf("agent %q instances must be at least 1", name))
		}
		problems = append(problems, agent.Persona.problems(name)...)
		if agent.Role == domain.RoleDeveloper {
			developers += agent.Instances
		}
		if agent.Role == domain.RoleReviewer {
			reviewers += agent.Instances
		}
	}
	if developers == 0 {
		problems = append(problems, "at least one developer agent is required")
	}
	if developers > 0 && c.Execution.MaxConcurrentDevelopers > developers {
		problems = append(problems, "max_concurrent_developers cannot exceed configured developer instances")
	}

	for index, check := range c.Checks {
		if strings.TrimSpace(check) == "" {
			problems = append(problems, fmt.Sprintf("check %d cannot be empty", index))
		}
	}
	// Admitting work without asking rests entirely on the operator's approval of
	// the goal that work serves, so a project that records no goal approvals has
	// nothing for it to rest on: the setting would read as autonomy and mean
	// nothing, because no goal would ever be approved for work to trace to. It is
	// refused here rather than left to be discovered as a queue that never fills,
	// which is the same choice automatic integration makes about its own gates.
	if c.Approvals.WorkItems == domain.ApprovalAutomatic && c.Approvals.Goals != domain.ApprovalHuman {
		problems = append(problems, fmt.Sprintf("automatic work_items requires approvals.goals to be %q; work is admitted without asking on the strength of the goal it serves having been approved", domain.ApprovalHuman))
	}
	if c.Approvals.Integration == domain.ApprovalAutomatic {
		if len(c.Checks) == 0 {
			problems = append(problems, "automatic integration requires at least one check")
		}
		if reviewers == 0 {
			problems = append(problems, "automatic integration requires at least one reviewer agent")
		}
	}

	problems = append(problems, c.accountProblems()...)
	problems = append(problems, c.operatorProblems()...)
	problems = append(problems, c.Slack.problems()...)

	if len(problems) > 0 {
		return ValidationError{Problems: problems}
	}
	return nil
}

// namedWorkItemClasses lists the classes a project may exempt, so a refusal
// names what was actually available rather than only what was wrong.
func namedWorkItemClasses() string {
	named := make([]string, 0, len(domain.WorkItemClasses))
	for _, class := range domain.WorkItemClasses {
		named = append(named, fmt.Sprintf("%q", class))
	}
	return strings.Join(named, ", ")
}

// slackChannelPattern keeps a configured channel a channel: an id, or a name
// with or without its leading hash. Anything else names nothing the workspace
// has, and is refused here rather than becoming a posting failure every few
// seconds for as long as the sink runs.
var slackChannelPattern = regexp.MustCompile(`^#?[A-Za-z0-9][A-Za-z0-9._-]*$`)

// slackUserPattern is the shape of a Slack member id, which an operator binds in
// the top-level mapping. Names are deliberately not accepted: a display name is
// not identity — two people can carry one, and one person can change theirs —
// and a binding keyed on something that moves is one that quietly stops matching
// the person it was written for.
var slackUserPattern = regexp.MustCompile(`^[UW][A-Z0-9]{6,}$`)

// slackEmojiPattern is the shape of an emoji shortcode: a name between colons,
// optionally with a skin-tone variant after it. It checks the shape and
// deliberately not the name, because a workspace's own custom emoji are the
// workspace's — a list of accepted names checked in here would refuse
// `:ship-it:` and go stale besides.
var slackEmojiPattern = regexp.MustCompile(`^:[a-z0-9][a-z0-9_+-]*:(:skin-tone-[2-6]:)?$`)

// MaxSlackChannelBytes bounds a configured channel so it stays a channel rather
// than a request smuggled onto the Slack API.
const MaxSlackChannelBytes = 80

// MaxSlackAvatarBytes bounds one configured avatar. It is generous enough for
// an image URL with a path on it and far short of anything that is no longer an
// avatar.
const MaxSlackAvatarBytes = 500

// SlackHarnessAvatar is the key an avatar override uses for what no persona did.
// It is the notifier's own speaker key, spelled here so checking a key does not
// make the configuration package depend on the notifier.
const SlackHarnessAvatar = "harness"

// problems reports what makes a Slack section unusable. Everything is checked
// whether or not reporting is enabled, so a project that has configured the
// section and not switched it on yet learns about a typo now rather than on the
// day it turns reporting on.
// problems reports everything wrong with the research settings at once. A
// project that permits no source has nothing to be wrong: the capability is off,
// which is what most projects are, and the bounds beneath it bound nothing.
func (r Research) problems() []string {
	if len(r.Sources) == 0 {
		return nil
	}
	var problems []string
	named := make(map[string]struct{}, len(r.Sources))
	for _, source := range r.Sources {
		if err := source.Validate(); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		// Two sources of one name is a question whose destination depends on which
		// entry the harness happened to find first, which is not a destination the
		// operator chose.
		if _, duplicate := named[source.Name]; duplicate {
			problems = append(problems, fmt.Sprintf("research source %q is named twice", source.Name))
			continue
		}
		named[source.Name] = struct{}{}
	}
	// Zero is a choice here — take the harness default — so only a negative
	// number, which describes no question anybody could ask, is refused.
	if r.MaxQueriesPerTurn < 0 {
		problems = append(problems, "research.max_queries_per_turn cannot be negative")
	}
	if r.Timeout < 0 {
		problems = append(problems, "research.timeout cannot be negative")
	}
	return problems
}

func (s Slack) problems() []string {
	var problems []string
	channel := strings.TrimSpace(s.Channel)
	switch {
	case channel == "":
		if s.Enabled {
			problems = append(problems, "slack.channel is required when slack is enabled")
		}
	case len(channel) > MaxSlackChannelBytes:
		problems = append(problems, fmt.Sprintf("slack.channel is %d bytes, limit is %d", len(channel), MaxSlackChannelBytes))
	case !slackChannelPattern.MatchString(channel):
		problems = append(problems, fmt.Sprintf("slack.channel %q must be a channel id or name", s.Channel))
	}
	return append(problems, s.avatarProblems()...)
}

// avatarProblems reports what makes a configured avatar unusable. A typo here
// is worth refusing at load rather than posting: Slack takes an unknown
// shortcode or an unreachable image without complaint and simply shows the
// app's own icon, so the failure would be a picture nobody notices is the wrong
// one rather than anything that says so.
//
// The keys are reported in a stable order, because a validation error that
// names three problems in a different order on every load is one nobody can
// diff.
func (s Slack) avatarProblems() []string {
	speakers := make([]string, 0, len(s.Avatars))
	for speaker := range s.Avatars {
		speakers = append(speakers, speaker)
	}
	sort.Strings(speakers)

	var problems []string
	for _, speaker := range speakers {
		if speaker != SlackHarnessAvatar && !domain.AgentRole(speaker).Valid() {
			problems = append(problems, fmt.Sprintf("slack.avatars %q is not a role or %q", speaker, SlackHarnessAvatar))
			continue
		}
		avatar := strings.TrimSpace(s.Avatars[speaker])
		switch {
		case avatar == "":
			problems = append(problems, fmt.Sprintf("slack.avatars.%s is empty; leave it out to keep the one the harness ships", speaker))
		case len(avatar) > MaxSlackAvatarBytes:
			problems = append(problems, fmt.Sprintf("slack.avatars.%s is %d bytes, limit is %d", speaker, len(avatar), MaxSlackAvatarBytes))
		case !slackEmojiPattern.MatchString(avatar) && !slackImageURL(avatar):
			problems = append(problems, fmt.Sprintf("slack.avatars.%s %q must be an emoji shortcode like %q or an https image URL", speaker, avatar, ":robot_face:"))
		}
	}
	return problems
}

// slackImageURL reports the other shape an avatar may take. It is https only:
// Slack fetches the image itself and an avatar is not worth a plaintext hop,
// and a project that meant a shortcode and wrote something else gets a refusal
// naming both shapes rather than a URL nothing will load.
func slackImageURL(avatar string) bool {
	parsed, err := url.Parse(avatar)
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

// problems reports what makes a declared persona unusable. A persona that
// resolved to nothing is a configuration error rather than a silent fallback:
// an agent whose configured guidance vanished is not the agent that was
// configured.
func (p Persona) problems(agentName string) []string {
	if !p.Defined() {
		return nil
	}
	var problems []string
	if strings.TrimSpace(p.Version) == "" {
		problems = append(problems, fmt.Sprintf("agent %q persona version is required", agentName))
	}
	if strings.TrimSpace(p.Path) == "" {
		problems = append(problems, fmt.Sprintf("agent %q persona path is required", agentName))
	}
	if strings.TrimSpace(p.Text) == "" {
		problems = append(problems, fmt.Sprintf("agent %q persona %q is empty", agentName, p.Path))
	}
	if len(p.Text) > MaxPersonaBytes {
		problems = append(problems, fmt.Sprintf("agent %q persona %q is %d bytes, limit is %d", agentName, p.Path, len(p.Text), MaxPersonaBytes))
	}
	return problems
}

// describeRoles lists the harness's roles the way an operator would read them
// back into the file they mistyped.
func describeRoles() string {
	names := make([]string, 0, len(domain.Roles()))
	for _, role := range domain.Roles() {
		names = append(names, strconv.Quote(string(role)))
	}
	return strings.Join(names, ", ")
}

// validateSpecificationsDirectory keeps the product manager's inputs inside the
// repository. The path is resolved against the repository root, so an absolute
// path or one that climbs out of it names something the repository does not
// contain and is refused rather than read.
func validateSpecificationsDirectory(directory string) error {
	return validateRepositoryDirectory("product specifications", directory)
}

// validateRepositoryDirectory holds one configured directory inside the
// repository. Every such setting names artifacts an agent is shown, so the rule
// is stated once here rather than once per setting: a path that climbs out of
// the repository names text nobody reviewed with the code.
func validateRepositoryDirectory(setting, directory string) error {
	trimmed := strings.TrimSpace(directory)
	if trimmed == "" {
		return fmt.Errorf("%s directory is required", setting)
	}
	clean := filepath.Clean(trimmed)
	if filepath.IsAbs(trimmed) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s %q must be a directory inside the repository", setting, directory)
	}
	return nil
}

// remoteNamePattern keeps a configured remote a plain remote name. The harness
// puts it on a `git push` command line, so anything that could read as an
// option, a path, or a refspec is refused rather than passed along.
var remoteNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateRemoteName(remote string) error {
	trimmed := strings.TrimSpace(remote)
	if trimmed == "" {
		return errors.New("remote is required")
	}
	if !remoteNamePattern.MatchString(trimmed) {
		return fmt.Errorf("remote %q must be a plain Git remote name", remote)
	}
	return nil
}

// MaxModelSelectorBytes bounds a configured selector so it stays a model name
// rather than an argument smuggled onto a provider command line.
const MaxModelSelectorBytes = 128

// ValidateModelSelector reports whether a configured model selector is usable.
// It deliberately accepts both floating family aliases and pinned identifiers,
// and rejects only what cannot name a model.
func ValidateModelSelector(model string) error {
	return validateModelSelector(model)
}

func validateModelSelector(model string) error {
	trimmed := strings.TrimSpace(model)
	switch {
	case trimmed == "":
		return errors.New("model selector is required; there is no implicit harness default")
	case len(trimmed) > MaxModelSelectorBytes:
		return fmt.Errorf("model selector is %d bytes, limit is %d", len(trimmed), MaxModelSelectorBytes)
	case strings.IndexFunc(trimmed, unicode.IsSpace) >= 0 || strings.HasPrefix(trimmed, "-"):
		return fmt.Errorf("model selector %q must be a single model name", model)
	}
	return nil
}

type ValidationError struct {
	Problems []string
}

func (e ValidationError) Error() string {
	return "invalid configuration: " + strings.Join(e.Problems, "; ")
}
