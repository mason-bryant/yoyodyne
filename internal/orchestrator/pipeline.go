package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/spend"
)

// maxCommitSubjectBytes bounds the work item title carried into the
// harness-owned commit subject.
const maxCommitSubjectBytes = 72

type WorkTracker interface {
	Show(ctx context.Context, id string) (beads.WorkItem, error)
	Claim(ctx context.Context, id string) (beads.WorkItem, error)
	RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error)
	// Block records a durable blocker. The harness uses it when a run stops on
	// something no further attempt of its own can resolve.
	Block(ctx context.Context, id, reason string) (beads.WorkItem, error)
	Complete(ctx context.Context, id, reason string) (beads.WorkItem, error)
}

// Pricer records what a work item has cost across every run made for it. The
// pipeline never prices anything itself: it says when a run for an item has
// ended, and what that is worth is read from the recorded evidence elsewhere. It
// is satisfied by cost.Ledger.
type Pricer interface {
	Record(ctx context.Context, workItemID string) (*beads.Cost, error)
}

type WorktreeManager interface {
	ValidateReady(ctx context.Context) error
	CurrentBranch(ctx context.Context) (string, error)
	Create(ctx context.Context, request gitworktree.CreateRequest) (gitworktree.Worktree, error)
	SummarizeChanges(ctx context.Context, worktree gitworktree.Worktree) (gitworktree.ChangeSummary, error)
	UnifiedChanges(ctx context.Context, worktree gitworktree.Worktree, limits gitworktree.DiffLimits) (gitworktree.ChangeDiff, error)
	// ChangedPaths names every path the change touches. It is what the gate in
	// front of the checks decides on, so it is separate from the summary and the
	// patch above: those are bounded for a reader, and a gate that saw a bounded
	// listing would pass whatever the bound had cut.
	ChangedPaths(ctx context.Context, worktree gitworktree.Worktree) ([]string, error)
	Integrate(ctx context.Context, worktree gitworktree.Worktree, message string) (gitworktree.Integration, error)
	// RebaseOntoTarget re-prepares a change whose promotion lost a race, by
	// replaying it onto wherever the target branch went. It is the only thing
	// that ever rewrites a run's branch, and it never resolves a conflict.
	RebaseOntoTarget(ctx context.Context, worktree gitworktree.Worktree, message string) (gitworktree.Rebase, error)
	CleanupIntegrated(ctx context.Context, request gitworktree.CleanupRequest) (gitworktree.Cleanup, error)
	// The publishing half. RemoteConfigured is what lets a repository with no
	// remote degrade to purely local behavior instead of failing; PublishBranch
	// and DeleteRemoteBranch are the Git writes publishing needs, which the
	// harness performs whichever phase asked for them. The merge itself is the
	// forge's, so the two remaining calls only observe it: one says whether the
	// remote target may still be merged into, the other whether the merge put
	// the promotion there and at which commit it left the branch.
	RemoteConfigured(ctx context.Context) (bool, error)
	// PushRemoteConfigured asks the same question about the remote run branches
	// are pushed to, which is a different repository from the one above whenever
	// a project publishes from a fork. It is separate so a contributor whose
	// fork remote is missing is told about the fork rather than about the
	// project's own remote, which is there.
	PushRemoteConfigured(ctx context.Context) (bool, error)
	PublishBranch(ctx context.Context, worktree gitworktree.Worktree, message string) (gitworktree.Publication, error)
	// RepublishBranch puts a replayed run branch back on the remote, replacing
	// exactly the commit the harness published there and nothing else.
	RepublishBranch(ctx context.Context, worktree gitworktree.Worktree, previousCommit string) (gitworktree.Publication, error)
	VerifyRemoteTarget(ctx context.Context, integration gitworktree.Integration) error
	ConfirmRemoteTarget(ctx context.Context, integration gitworktree.Integration) (string, error)
	DeleteRemoteBranch(ctx context.Context, worktree gitworktree.Worktree, commit string) error
	// CatchUpTarget brings the local target branch onto what the forge has,
	// which is the local half of the merge the forge just performed. It moves
	// the same branch a promotion does, so it is only ever called while this run
	// holds that branch's promotion lease.
	CatchUpTarget(ctx context.Context, targetBranch string) (gitworktree.Catchup, error)
}

// PullRequests is the forge access publishing needs. The pipeline decides when
// a pull request must exist and when its work has been authorized for merging;
// it never decides forge semantics itself.
type PullRequests interface {
	Availability(ctx context.Context) (publish.Availability, error)
	Ensure(ctx context.Context, request publish.Request) (publish.PullRequest, error)
	Merge(ctx context.Context, request publish.MergeRequest) (publish.MergeResult, error)
	State(ctx context.Context, head string) (publish.PullRequest, error)
}

// ChangeReviewer runs one independent review of a developer's change. The
// pipeline never decides review semantics itself; it only acts on the verdict.
type ChangeReviewer interface {
	Review(ctx context.Context, request review.Request) (review.Result, error)
}

type StateStore interface {
	// Reserve creates a fresh run and returns the lease that makes this process
	// its only owner; Adopt takes the same lease over the run already in flight
	// for an item, reporting runstate.ErrNoRunInFlight when there is none. Every
	// entry point into an in-flight run goes through one of them, so resuming a
	// run is exactly as exclusive as starting one.
	Reserve(ctx context.Context, state runstate.State, maxConcurrent int) (*runstate.Lease, error)
	Adopt(ctx context.Context, workItemID string) (runstate.State, *runstate.Lease, error)
	// LeasePromotion admits this run to promote into one target branch, waiting
	// its turn behind whatever is promoting into it now. Development is parallel
	// and integration is serial, and this is what makes the second half true
	// across processes rather than only within one.
	LeasePromotion(ctx context.Context, targetBranch string) (*runstate.Lease, error)
	Save(state runstate.State) error
	AppendEvent(event execution.Event) error
	// ReleasedWait reports whether the operator has said that a run's recorded
	// usage-limit deadline no longer describes the provider, and ClearRelease
	// consumes that statement as the run acts on it. They are read and written
	// from beside the run rather than in it, because the process serving the wait
	// holds the run's lease and the operator releasing it does not.
	ReleasedWait(runID string) (runstate.Release, bool, error)
	ClearRelease(runID string) error
	// StopRequested reports whether the operator has asked one run to stop. It is
	// read from beside the run for the same reason a release is: the operator does
	// not hold the run's lease, so they state the fact in a file of their own and
	// the run reads it at the boundaries where it would otherwise spend.
	StopRequested(runID string) (runstate.StopRequest, bool, error)
	// Triage is the product's durable per-work-item counters, where the review
	// rounds an item has spent are recorded. They are reached through the run
	// store because they are the same product's record, and they are not part of
	// any run: what an item has cost spans every run of it, and a run that ends
	// takes none of it with it.
	Triage() *runstate.TriageStore
	// Incomplete lists the runs still in flight. It is how a step that declines to
	// act on a run can still say what it is leaving alone, which is the whole of
	// what it is for here: reading a record is not acting on it, so this takes no
	// lease and the run it describes may well belong to another process.
	Incomplete() ([]runstate.State, error)
	// Latest is the most recently started run recorded for this item, whatever
	// became of it, and runstate.ErrNoRecordedRun where the harness has never run
	// it. Adopt answers for the runs in flight and says nothing about the ones
	// that ended, which is exactly the blind spot a fresh run started in place of
	// a repair falls into: the stopped run is not in flight, so nothing above
	// notices that starting over is the wrong thing to do to it.
	Latest(workItemID string) (runstate.State, error)
	// Reruns is what triage has claimed of the fresh runs it decided. A fresh run
	// of an item whose last run stopped owing a repair is right in exactly one
	// case — the development manager decided the ground moved and the work is to
	// be done again — and a claim against that stoppage is what says so.
	Reruns() *runstate.RerunStore
}

type CheckRunner interface {
	Run(ctx context.Context, runID, directory string, commands []string, lastSequence uint64, sink func(execution.Event) error) ([]checks.Result, uint64, error)
}

// Directives is what the operator has told the harness, as a run reads it.
//
// It is consulted rather than delivered. A directive that changes a governed
// artifact this work derives from, or that nobody can act on until the operator
// says what they meant, is not context for the developer to weigh — it is a
// reason the work must not proceed at all, because the intent it would be
// written against is being rewritten or was never settled. So the pipeline asks
// this question at every point where it is about to commit to work: before it
// claims an item, before it resumes a run, and before it puts a change through
// the gate that would integrate it.
//
// It is read from durable records every time, never cached. The directive that
// matters most is the one recorded by another process while this run was
// developing, and a run that answered from what it read at the start would be
// exactly the run this exists to stop.
type Directives interface {
	// Pausing lists the unresolved directives that pause one work item. An empty
	// result is the ordinary answer and means the work may proceed.
	Pausing(workItemID string) ([]directive.Directive, error)
}

// OperatorHolds is the operator's switch over everything the harness would spend
// on a provider, as a run reads it.
//
// It is consulted for the same reason and in the same way the directives are,
// and it is read from durable records every time rather than cached: the hold
// that matters is the one an operator placed while this run was developing. What
// differs is its scope. A directive is about work; this is about spending, so it
// is asked at every provider-call boundary rather than at the points where a run
// commits to work, and it says nothing about the work being right or wrong.
//
// It is satisfied by runstate.OperatorHoldStore.
type OperatorHolds interface {
	// Held reports whether the operator is holding harness activity. Not held is
	// the ordinary answer and means the run may spend.
	Held() (runstate.OperatorHold, bool, error)
}

// IntakeHolds is the operator's switch over the work the harness chooses for
// itself, as a run reads it.
//
// It is the narrower of the two switches and it is asked in exactly one place:
// where a run would be started for a reason other than the operator naming the
// item. A hold on intake stops the harness pulling anything more and leaves
// everything already running alone, which is the point of having it apart from
// the hold over spending — an operator who suspects the queue is heading
// somewhere wrong wants the queue stopped, not the half-finished change thrown
// away.
//
// It is satisfied by runstate.IntakeHoldStore.
type IntakeHolds interface {
	// Held reports whether the operator is holding intake for this product. Not
	// held is the ordinary answer and means the harness may choose work.
	Held() (runstate.IntakeHold, bool, error)
}

type Pipeline struct {
	Tracker   WorkTracker
	Worktrees WorktreeManager
	Store     StateStore
	Backend   backend.Backend
	Checks    CheckRunner
	// Reviewer is required only when integration is automatic, because nothing
	// is ever integrated without an independent verdict.
	Reviewer ChangeReviewer
	// Publisher is required only when publishing is automatic, because a project
	// that has not opted in never opens a pull request.
	Publisher PullRequests
	// Directives is what the operator has told the harness. It is required rather
	// than optional, unlike the two collectors below: a run that cannot find out
	// what has been directed would proceed against intent that may already have
	// been withdrawn, and a directive nothing enforces is the state this was built
	// to end.
	Directives Directives
	// Holds is the operator's switch over provider spending. It is required for
	// the same reason the directives are: a run that cannot find out whether the
	// operator has paused everything would spend against a hold that is in force,
	// and a pause the harness can miss is not a pause.
	Holds OperatorHolds
	// Intake is the operator's switch over work the harness chooses for itself.
	// It is required for the same reason, and it stops nothing an operator asked
	// for by name: what it holds is the choosing, so a run this pipeline was told
	// to make proceeds under it exactly as it would have.
	Intake IntakeHolds
	// Selection is why this pipeline is running what it runs: who chose the work
	// and on what grounds. It is recorded with the run so that an operator reading
	// what is in flight can see why each item was picked, which is the question
	// with no answer at all once something other than them does the picking. It is
	// also what tells this pipeline whether an intake hold applies to it.
	//
	// The zero value is a pipeline that cannot account for its choice. It is
	// permitted — a caller that says nothing records nothing rather than being
	// refused — and it is treated as the harness choosing rather than the
	// operator, because unaccounted work is the case the hold exists to catch.
	Selection runstate.Selection
	// Reports is where what this run's agents noticed is collected. It is
	// optional: a pipeline wired without one still runs exactly as it would
	// have, and a report it cannot keep is named on the outcome rather than
	// disappearing quietly.
	Reports ReportCollector
	// Amendments is where changes this run's agents propose to documents they do
	// not own are kept, so the argument outlives the run that made it. It is
	// optional in the same way as the reports and for the same reason: a proposal
	// decides nothing about the run, so a pipeline wired without one still runs
	// exactly as it would have and names the proposal it could not keep.
	Amendments AmendmentRecorder
	// Prices puts the price of this work item on the item itself as a run for it
	// ends. It is optional in the same way and for the same reason: a run is not
	// worth failing over a number nobody could write down, so a pipeline wired
	// without one runs exactly as it would have and one that could not record a
	// price names that on the outcome.
	Prices Pricer
	// Spend is the cost log every provider invocation this run makes lands in,
	// one line each at the moment its cost is known. It is optional in the same
	// way as the three above and for a narrower reason: a pipeline wired without
	// one runs exactly as it would have, and what it loses is the only record of
	// what the run spent that does not have to be read back out of an event log.
	// The harness always wires it; a test that does not care about money does not
	// have to.
	Spend SpendLog
	// Docket is where a run that ends on a durable blocker is put in front of the
	// development manager. It is optional in the same way as the three above: a
	// stoppage is already recorded on the work item and in this run's own record
	// by the time it is docketed, so a pipeline wired without one stops exactly
	// as it would have and loses only the delivery.
	Docket *Docketer
	Clock  execution.Clock
	// Sleep waits out a usage-limit pause. It is a field so a test can drive a
	// pause without spending the real time, and so the wait is always cut short
	// by a cancelled context rather than holding the process past a shutdown.
	Sleep    func(ctx context.Context, duration time.Duration) error
	NewRunID func() (string, error)
	// Accounts chooses which of the configured provider accounts a fresh run is
	// served by, and is optional. A pipeline wired without one runs every run
	// under the configuration's single account, which is what a project with one
	// has and is exactly the behaviour there was before pooling existed.
	//
	// It is consulted once, when a run is started. Everything after that reads the
	// alias the run recorded: a resumed run, a repair attempt, and the review of
	// the change are all the same run, and a run that changed account mid-flight
	// would leave half its spend on one subscription and half on another with
	// nothing saying so.
	Accounts AccountChooser
	// StateRoot is where everything durable that is not the repository lives. The
	// pipeline needs it because a pooled alias authenticates in a provider home
	// under it, and that path is this machine's business rather than the
	// configuration's — which is why it is wired here rather than written down in
	// a file that is versioned with the repository.
	StateRoot    string
	Repository   string
	Config       config.Config
	RedactValues []string
}

// AccountChooser picks the provider account the next run is served by. It is an
// interface rather than the configuration's own method because choosing needs
// evidence the configuration does not hold — what each account has already spent
// this week, and which one the last run used — and that evidence is in the run
// records.
type AccountChooser interface {
	ChooseAccount() (config.AccountEndpoint, error)
}

// chooseAccount is the account a fresh run is served by. A pipeline with no
// chooser wired runs under the configuration's single account, which is what a
// project with one has; a pooled configuration with no chooser names no account
// and is refused here, before anything is claimed, rather than starting a run
// nothing could attribute.
func (p Pipeline) chooseAccount() (config.AccountEndpoint, error) {
	if p.Accounts != nil {
		return p.Accounts.ChooseAccount()
	}
	return p.Config.Endpoint(p.StateRoot, p.Config.AccountAlias())
}

// accountFor is where an alias a run already recorded authenticates. An alias
// the configuration no longer declares is still the alias that run spent, and
// the run is not worth failing over a mapping edited underneath it: the
// invocation is made where that alias authenticates, and the record goes on
// saying which account it was.
func (p Pipeline) accountFor(alias string) config.AccountEndpoint {
	if endpoint, err := p.Config.Endpoint(p.StateRoot, alias); err == nil {
		return endpoint
	}
	return config.AccountEndpoint{Alias: alias, Directory: config.AccountConfigDirectory(p.StateRoot, alias)}
}

type Outcome struct {
	RunID        string          `json:"run_id"`
	WorkItemID   string          `json:"work_item_id"`
	Status       runstate.Status `json:"status"`
	Phase        runstate.Phase  `json:"phase,omitempty"`
	Branch       string          `json:"branch,omitempty"`
	WorktreePath string          `json:"worktree_path,omitempty"`
	BaseCommit   string          `json:"base_commit,omitempty"`
	// ProviderSessionID identifies the developer session; ReviewSessionID
	// identifies the separate reviewer session that judged its work. The model
	// pairs are the requested selector and what the provider reported serving.
	ProviderSessionID     string                    `json:"provider_session_id,omitempty"`
	ProviderModel         string                    `json:"provider_model,omitempty"`
	ProviderResolvedModel string                    `json:"provider_resolved_model,omitempty"`
	Checks                []checks.Result           `json:"checks,omitempty"`
	Changes               gitworktree.ChangeSummary `json:"changes"`
	Summary               string                    `json:"summary,omitempty"`
	// Reports are what this run's agents noticed and reported while their work
	// carried on: risks worked around, assumptions that may not hold, things
	// outside the assigned work. They are collected beside the run rather than
	// on it, so nothing here decided anything about what the run did.
	// ReportProblem names a report that could not be read or could not be kept,
	// because a report nobody collected would otherwise leave no trace at all.
	Reports       []report.Report `json:"reports,omitempty"`
	ReportProblem string          `json:"report_problem,omitempty"`
	// Amendments are the changes this run's agents proposed to documents they do
	// not own. Like the reports they are recorded beside the run and decided
	// nothing about it: each one is waiting on the role that owns the document or
	// on the operator, and nothing was written to any document.
	// AmendmentProblem names a proposal that could not be read or could not be
	// kept, because a proposal nobody recorded would otherwise leave no trace.
	Amendments       []amendment.Proposal `json:"amendments,omitempty"`
	AmendmentProblem string               `json:"amendment_problem,omitempty"`
	// Cost is what every run made for this work item has cost, as the provider
	// reported it: this run and every earlier one, the attempts that failed as
	// well as the one that finished. It is absent when nothing priced the item,
	// and CostProblem names why when something tried and could not.
	Cost        *beads.Cost `json:"cost,omitempty"`
	CostProblem string      `json:"cost_problem,omitempty"`
	// Invariants names the architectural invariants this run delivered to its
	// developer and to its reviewer. It is the audit record of which durable
	// constraints the change was actually held to, which is the thing a
	// transcribed constraint in a bead could never say afterwards.
	// InvariantProblems names what the delivered set was missing: a file in the
	// invariants directory that could not be read as one, or an invariant that
	// matched and did not fit the prompt's bound. Both mean the set the agents saw
	// was incomplete, which is a fact for the operator rather than a run failure.
	Invariants          []string         `json:"invariants,omitempty"`
	InvariantProblems   []string         `json:"invariant_problems,omitempty"`
	ReviewSessionID     string           `json:"review_session_id,omitempty"`
	ReviewModel         string           `json:"review_model,omitempty"`
	ReviewResolvedModel string           `json:"review_resolved_model,omitempty"`
	ReviewDecision      review.Decision  `json:"review_decision,omitempty"`
	ReviewSummary       string           `json:"review_summary,omitempty"`
	ReviewFindings      []review.Finding `json:"review_findings,omitempty"`
	// RepairAttempts counts the times this run returned a failure to the
	// developer, whether it was a failing check or the reviewer's findings; the
	// two share one budget. Blocked reports that the budget was spent and what
	// remained unresolved was recorded on the work item.
	RepairAttempts int `json:"repair_attempts,omitempty"`
	// IntegrationRetries counts the promotions this run re-prepared after losing
	// a race for its target branch. Each one replayed the change onto where the
	// target went and put it back through the checks and a fresh independent
	// review, so it is evidence about the target moving rather than about the
	// change being wrong.
	IntegrationRetries int `json:"integration_retries,omitempty"`
	// TransientRelaunches counts the provider invocations this run reissued after
	// one died without judging the work. It is evidence about the provider rather
	// than about the change or the target branch, and a run reporting some and
	// finishing anyway is the whole point of the budget: the deaths cost nobody
	// anything. Blocked reports the budget spent, with what killed the last
	// attempt recorded on the work item.
	TransientRelaunches int  `json:"transient_relaunches,omitempty"`
	Blocked             bool `json:"blocked,omitempty"`
	// Environmental is the environment refusing this round rather than the work
	// failing: which environmental cause the run recorded, and what the settle
	// gave back once it found the round had delivered nothing. It is the one
	// stoppage that leaves the item's budgets exactly where they were, so a caller
	// that reported the blocker without it would say an item had spent another
	// round toward its cap when it had spent none.
	Environmental *runstate.EnvironmentalRefusal `json:"environmental,omitempty"`
	// Paused reports a run that stopped short of finishing and is owed a
	// continuation rather than having failed. The run is still in flight when it
	// is set: its worktree, branch, claimed item, and developer session are all
	// preserved. Four things pause a run, and they are told apart by which of the
	// fields below is set: an exhausted provider usage limit, whose deadline says
	// when the run becomes runnable again; a provider invocation the harness
	// stopped on time, which is runnable immediately; an unresolved user
	// directive, which is runnable once somebody settles the directive; and the
	// operator's hold on all harness activity, which is runnable once they lift it.
	//
	// The directive and the hold are the two that can pause work before there is a
	// run at all. Nothing is claimed and no worktree exists in that case, so the
	// paused outcome names the work item and what stopped it and nothing else.
	Paused bool `json:"paused,omitempty"`
	// PausedByDirective is the unresolved directive this run or this work item
	// stopped short for. It carries the directive itself rather than only its
	// identifier, because what an operator has to do about it — answer the
	// question, or decide the artifact change — is in the directive's own words.
	PausedByDirective *directive.Directive `json:"paused_by_directive,omitempty"`
	// PausedByDependency is the unfinished work this run or this work item stopped
	// short for. It carries the blocking items themselves rather than only saying
	// there are some, because what somebody has to do about it — close that work,
	// or unlink it — is a decision about those items by name.
	//
	// Like the directive it can appear with no run behind it, on work a dependency
	// stopped before anything was claimed, and unlike the directive what lifts it
	// is other work finishing rather than a person deciding.
	PausedByDependency *runstate.DependencyPause `json:"paused_by_dependency,omitempty"`
	// PausedByOperator is the operator's hold on all harness activity, present on
	// a run parked at a provider-call boundary for it and on work this process
	// declined to start while it was in force. It carries the hold itself rather
	// than only saying there is one, because when it was placed is what tells an
	// operator whether they are looking at a system they paused or one that died.
	PausedByOperator *runstate.OperatorHold `json:"paused_by_operator,omitempty"`
	// PausedByIntake is the operator's hold on the work the harness chooses for
	// itself, present on work this pipeline declined to start because of it.
	// Unlike the four pauses above it never appears on a run: what it holds is the
	// choosing, so there is nothing claimed, nothing developed, and nothing to
	// resume — only an item that was not started and says why.
	PausedByIntake *runstate.IntakeHold `json:"paused_by_intake,omitempty"`
	// UsageLimitResetsAt is when the provider said the exhausted limit resets,
	// and UsageLimitKind is the provider's own name for it. They are reported on
	// a paused run and on a run that stopped because the reset was unusable.
	UsageLimitResetsAt *time.Time `json:"usage_limit_resets_at,omitempty"`
	UsageLimitKind     string     `json:"usage_limit_kind,omitempty"`
	// PauseCause is which refusal that deadline is being waited out for, one of
	// the runstate.Pause constants. A transiently overloaded server and an
	// exhausted account both park a run on a deadline, and only this tells a
	// reader which of them they are looking at.
	PauseCause string `json:"pause_cause,omitempty"`
	// ProviderStop names why the harness stopped a provider invocation on time
	// rather than the provider ending it: runstate.ProviderStopStalled when it
	// stopped emitting events, runstate.ProviderStopBudgetExhausted when it was
	// still live and out of budget. It is never a report of failure by the agent.
	ProviderStop string                   `json:"provider_stop,omitempty"`
	Integration  *gitworktree.Integration `json:"integration,omitempty"`
	// PullRequest is the published pull request, present only on a run that
	// published one. PublishSkipped says why a run that asked to publish did not,
	// which is a repository with no configured remote and nothing else.
	PullRequest    *runstate.PullRequest `json:"pull_request,omitempty"`
	PublishSkipped string                `json:"publish_skipped,omitempty"`
	// PublishFailure reports a promotion that could not be published. The local
	// target branch is the authoritative one and it already moved, so this is an
	// outstanding publication rather than a failed run.
	PublishFailure string `json:"publish_failure,omitempty"`
	// Catchup is the local aftermath of a merge the forge performed: the target
	// branch brought onto the merge commit the forge made above the promotion,
	// so the checkout a person reads carries what the forge has. It is recorded
	// rather than made durable because it is idempotent and unowned — `yoyo
	// reconcile` sweeps every target branch and would do it again — so a
	// catch-up that was held is a fact to report, not outstanding work anybody
	// has to track.
	Catchup        *gitworktree.Catchup `json:"catchup,omitempty"`
	WorkItemClosed bool                 `json:"work_item_closed"`
	// WorktreeRemoved and BranchRemoved report each artifact separately, because
	// cleanup removes them in two steps and a partial result must not describe
	// a deleted artifact as remaining or a surviving one as gone.
	WorktreeRemoved bool   `json:"worktree_removed"`
	BranchRemoved   bool   `json:"branch_removed"`
	Failure         string `json:"failure,omitempty"`
	// CleanupFailure is set when the run completed but its post-completion
	// cleanup did not finish cleanly. The work is integrated and the item is
	// closed either way, so this is evidence for reconciliation rather than a
	// run failure. It covers two different situations, which WorktreeRemoved and
	// BranchRemoved tell apart: an artifact that survives, when either flag is
	// false, and a removal that succeeded but could not be confirmed afterwards,
	// when both are true. Only the first leaves something to remove, so a report
	// must read these flags rather than infer leftovers from this field alone.
	CleanupFailure string `json:"cleanup_failure,omitempty"`
	// CompletionRecordingFailure is set when the run completed, both artifacts
	// were removed and confirmed gone, and only the final completion record
	// could not be written. It is deliberately distinct from CleanupFailure:
	// cleanup itself reported nothing wrong, so nothing must be reported as
	// remaining or as unconfirmed.
	CompletionRecordingFailure string `json:"completion_recording_failure,omitempty"`
}

type ExistingRunError struct {
	State runstate.State
}

func (e ExistingRunError) Error() string {
	return fmt.Sprintf("work item %s already has incomplete run %s in status %s", e.State.WorkItemID, e.State.RunID, e.State.Status)
}

func (p Pipeline) Run(ctx context.Context, workItemID string) (Outcome, error) {
	if err := p.validate(); err != nil {
		return Outcome{}, err
	}
	if err := p.Config.Validate(); err != nil {
		return Outcome{}, err
	}
	developer := p.developer()
	if !p.runsOnCompiledAdapter(developer.Backend) {
		return Outcome{}, fmt.Errorf("Milestone 0 run pipeline requires a claude-code developer, configured backend is %q", developer.Backend)
	}
	// Every invocation names its own model; the harness never lets a provider
	// pick one for it, so the run evidence always says what actually ran.
	if err := config.ValidateModelSelector(developer.Model); err != nil {
		return Outcome{}, fmt.Errorf("developer agent %s", err)
	}
	if len(p.Config.Checks) == 0 {
		return Outcome{}, errors.New("run pipeline requires at least one configured check")
	}
	if p.automatic() {
		if err := p.validateReviewPolicy(); err != nil {
			return Outcome{}, err
		}
	}
	// The operator's hold is read before anything else this command would do,
	// because it is the cheapest question here and the broadest answer: a held
	// harness starts nothing, claims nothing, and asks the provider nothing, not
	// even whether it is installed. A run already in flight is left in flight, and
	// it is left held rather than resumed into a boundary that would park it again.
	if hold, held, err := p.operatorHold(); err != nil || held {
		if err != nil {
			return Outcome{}, err
		}
		return p.holdWorkItem(workItemID, hold)
	}
	// Whether this run publishes is settled before anything is claimed, so a
	// project that asked for pull requests and cannot open one fails here rather
	// than after a developer has already produced work.
	publishing, skipped, err := p.resolvePublishing(ctx)
	if err != nil {
		return Outcome{}, err
	}
	availability, err := p.Backend.CheckAvailability(ctx)
	if err != nil {
		return Outcome{}, err
	}
	if !availability.Installed {
		return Outcome{}, errors.New("Claude Code is not installed")
	}
	if !availability.Authenticated {
		return Outcome{}, fmt.Errorf("Claude Code is not authenticated; run `claude auth login` before handing work to Yoyodyne (auth method: %s)", availability.AuthMethod)
	}

	item, err := p.Tracker.Show(ctx, workItemID)
	if err != nil {
		return Outcome{}, fmt.Errorf("load work item: %w", err)
	}
	// What the operator has directed is read before anything is claimed, adopted,
	// or resumed. A directive that changes the artifact this work derives from, or
	// that nobody can act on until the operator says what they meant, stops the
	// work here rather than after a developer has already written a change against
	// intent that is being rewritten. Nothing has been claimed at this point, so
	// the item is simply left where it is, and a run already in flight for it is
	// left in flight rather than resumed.
	pausing, err := p.pausingDirectives(workItemID)
	if err != nil {
		return Outcome{}, err
	}
	if len(pausing) > 0 {
		return pauseWorkItem(workItemID, pausing[0]), nil
	}
	// What the item waits on is read at the same boundary, from the same freshly
	// loaded item, and for the same reason. A dependency link applied after this
	// item was selected is a gate somebody added to work that was already moving,
	// and a run that trusted the readiness selection saw would develop straight
	// through it — which is exactly what a link applied to an in-flight item is
	// filed to stop. Nothing has been claimed at this point, so the item is left
	// where it is, and a run already in flight for it is left in flight rather
	// than resumed.
	if blockers := blockingDependencies(item); len(blockers) > 0 {
		return pauseWorkItemForDependencies(workItemID, blockers), nil
	}
	// An incomplete run for this item is either a run an interrupted process left
	// behind, a run waiting out a provider usage limit, or a duplicate that must
	// be refused. Adopting it takes the same exclusive lease a fresh reservation
	// takes, so entering a run this process did not start can never put two
	// developers on one item; a run another process is still holding is reported
	// as existing rather than picked up. Only runs whose remaining work is fully
	// described by durable state are continued: the repair loop, a paused run that
	// recorded the deadline it is waiting on, and a run that recorded the directive
	// it stopped short for — which is reached only once that directive is settled,
	// because the directives were read a moment ago.
	inFlight, lease, err := p.Store.Adopt(ctx, workItemID)
	switch {
	case err == nil:
		defer lease.Release()
		// A usage-limit pause, a provider the harness stopped on time, a run held
		// up by a directive, one waiting on work its item depends on, and one the
		// operator parked are all resumable whatever the approval policy, because
		// none of them depends on the repair loop: the first has not had its
		// attempt served yet, the second is owed the rest of an attempt it was
		// making, and the rest are owed the rest of the step they stopped short of.
		// Nothing reaches here while the hold or the dependency is still in force,
		// so a run carrying one is a run whose reason to wait has gone.
		if !pausedForUsageLimit(inFlight) && !pausedForDirective(inFlight) && !pausedForDependency(inFlight) && !pausedForOperatorHold(inFlight) && !stoppedProviderIsResumable(inFlight) && !(p.automatic() && resumableRepair(inFlight)) {
			return Outcome{}, ExistingRunError{State: inFlight}
		}
		return p.resumeRun(ctx, inFlight, item, publishing, skipped)
	case errors.Is(err, runstate.ErrNoRunInFlight):
	default:
		var existing runstate.ExistingWorkItemError
		if errors.As(err, &existing) {
			return Outcome{}, ExistingRunError{State: existing.State}
		}
		return Outcome{}, fmt.Errorf("adopt run in flight: %w", err)
	}
	// Nothing is in flight for this item, so what follows would start something
	// new — which is the one thing an intake hold stops. It is asked here rather
	// than at the top because that is exactly the distinction the hold is for:
	// everything above this point either resumed a run or found none, and a run
	// already under way carries on while intake is held.
	if outcome, held, err := p.holdIntake(workItemID); err != nil || held {
		return outcome, err
	}
	// And what follows would start it clean, which for an item whose last run
	// stopped owing a repair is starting over on work that is waiting to be
	// continued. It is asked before the item's own readiness, because the item
	// having been put back is what lets this substitution past every other gate
	// and says nothing about whether starting over is the right thing to do.
	if err := p.refuseSubstitutedHandback(workItemID); err != nil {
		return Outcome{}, err
	}
	if err := validateReadyItem(item, workItemID); err != nil {
		return Outcome{}, err
	}
	if _, err := contextbundle.Assemble(contextbundle.Request{RepositoryRoot: p.Repository, WorkItem: item}); err != nil {
		return Outcome{}, fmt.Errorf("validate work item context: %w", err)
	}
	// The invariants are read before anything is claimed. A directory that cannot
	// be read at all is refused here rather than delivering nothing, because a
	// repository whose constraints silently failed to load looks exactly like one
	// that has none.
	invariants, err := p.loadInvariants()
	if err != nil {
		return Outcome{}, err
	}
	if err := p.Worktrees.ValidateReady(ctx); err != nil {
		return Outcome{}, fmt.Errorf("repository is not ready for an isolated run: %w", err)
	}
	// An automatic run is written against exactly the branch it will be promoted
	// into, so the integration target is fixed before any work starts and never
	// inferred afterwards. A published run fixes the same branch for the same
	// reason: it is the base its pull request is opened against, and a pull
	// request whose base could still change is not describing one change.
	baseRef := "HEAD"
	targetBranch := ""
	if p.automatic() || publishing {
		targetBranch, err = p.Worktrees.CurrentBranch(ctx)
		if err != nil {
			return Outcome{}, fmt.Errorf("resolve integration target: %w", err)
		}
		baseRef = targetBranch
	}
	runID, err := p.NewRunID()
	if err != nil {
		return Outcome{}, err
	}
	// Which account will serve this run is settled before anything is claimed, so
	// a pool with nothing left to spend refuses here rather than after a work item
	// has been taken and a worktree cut for it.
	account, err := p.chooseAccount()
	if err != nil {
		return Outcome{}, fmt.Errorf("choose the provider account for this run: %w", err)
	}
	now := p.clock().Now()
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     p.Config.Product.ID,
		RepositoryID:  string(p.Config.Product.RepositoryID),
		WorkItemID:    workItemID,
		// What the item is called is written with the run because this is where the
		// tracker's answer is in hand: everything that reads the record afterwards
		// reads only the record, so a title not copied here is a title nothing can
		// say the work by.
		WorkItemTitle: item.Title,
		Backend:       domain.BackendClaudeCode,
		// Which account this run spends and which configuration set it up are
		// written with the run for the reason the title is: this is where the
		// answer is in hand. Everything that reads the record afterwards reads only
		// the record, and neither can be recovered from it later — a configuration
		// is edited, and an account nobody recorded is an account nobody can bill
		// the run to. Under a pool it is also what the run is affined to: every
		// invocation this run goes on to make reads the alias back off the record
		// rather than asking the pool a second time.
		AccountAlias:   account.Alias,
		ConfigRevision: p.Config.Revision(),
		Status:         runstate.StatusPending,
		StartedAt:      now,
		UpdatedAt:      now,
	}
	// Why this item was chosen is written with the run and never rewritten. A
	// caller that said nothing records nothing, which is reported afterwards as a
	// run nothing accounted for rather than as a run whose reason was empty.
	if selection, stated := p.Selection.Stamped(now); stated {
		state.Selection = &selection
	}
	reservation, err := p.Store.Reserve(ctx, state, p.Config.Execution.MaxConcurrentDevelopers)
	if err != nil {
		var existing runstate.ExistingWorkItemError
		if errors.As(err, &existing) {
			return Outcome{}, ExistingRunError{State: existing.State}
		}
		return Outcome{}, fmt.Errorf("reserve developer run: %w", err)
	}
	defer reservation.Release()
	run := &activeRun{
		pipeline:   p,
		state:      state,
		outcome:    Outcome{RunID: runID, WorkItemID: workItemID, Status: runstate.StatusPending, PublishSkipped: skipped},
		item:       item,
		publishing: publishing,
		invariants: invariants,
	}

	item, err = p.Tracker.Claim(ctx, workItemID)
	if err != nil {
		return run.fail(fmt.Errorf("claim work item: %w", err), runstate.StatusFailed)
	}
	run.claimed = true
	run.item = item
	if err := validateClaimedItem(item, workItemID); err != nil {
		return run.fail(fmt.Errorf("validate claimed work item: %w", err), runstate.StatusFailed)
	}
	bundle, err := contextbundle.Assemble(contextbundle.Request{RepositoryRoot: p.Repository, WorkItem: item})
	if err != nil {
		return run.fail(fmt.Errorf("assemble claimed work item context: %w", err), runstate.StatusFailed)
	}
	run.context = bundle.Text
	worktree, err := p.Worktrees.Create(ctx, gitworktree.CreateRequest{
		RunID:        runID,
		WorkItemID:   workItemID,
		BaseRef:      baseRef,
		TargetBranch: targetBranch,
	})
	if err != nil {
		if worktree.Path != "" {
			run.recordWorktree(worktree)
		}
		return run.fail(fmt.Errorf("create isolated worktree: %w", err), runstate.StatusFailed)
	}
	run.recordWorktree(worktree)
	run.state.Status = runstate.StatusRunning
	run.state.Phase = runstate.PhaseDeveloping
	run.state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(run.state); err != nil {
		return run.fail(fmt.Errorf("save running state: %w", err), runstate.StatusFailed)
	}
	run.outcome.Status = runstate.StatusRunning
	run.outcome.Phase = run.state.Phase

	if err := run.develop(ctx, developerPrompt(developer.Persona.Text, run.deliveredInvariants().Text(), bundle.Text), ""); err != nil {
		return run.stop(ctx, err)
	}
	return run.verifyReviewAndFinish(ctx)
}

// resumeRun picks up a run this process did not finish: one an interrupted
// process left inside its repair loop, one that paused because a provider usage
// limit was exhausted, one whose provider the harness stopped on time, or one
// that stopped short for a directive somebody has since settled. The
// worktree, branch, developer session, attempt
// count, and the failure the interrupted attempt was handed all come from
// durable state, so the resumed run continues the same change at the attempt it
// had reached instead of starting a second one against a fresh budget. Its
// caller holds the run's lease for the whole of it.
func (p Pipeline) resumeRun(ctx context.Context, state runstate.State, item beads.WorkItem, publishing bool, skipped string) (Outcome, error) {
	if err := validateClaimedItem(item, state.WorkItemID); err != nil {
		return Outcome{}, fmt.Errorf("validate resumed work item: %w", err)
	}
	bundle, err := contextbundle.Assemble(contextbundle.Request{RepositoryRoot: p.Repository, WorkItem: item})
	if err != nil {
		return Outcome{}, fmt.Errorf("assemble resumed work item context: %w", err)
	}
	// Refusing here costs the run nothing: it stays exactly as the interrupted
	// process left it, still resumable, rather than spending an attempt on work
	// that could not be integrated afterwards anyway.
	if err := p.Worktrees.ValidateReady(ctx); err != nil {
		refused := fmt.Errorf("repository is not ready to resume run %s: %w", state.RunID, err)
		// The round this would have been is turned away by the environment, so the
		// run says so rather than leaving the reason in an error a caller prints
		// once. Nothing is charged here — the run is untouched and resumable — and
		// the settle records exactly that.
		if named, environmental := environmentalCauseOf(err); environmental {
			if recordErr := p.refuseDispatchEnvironmentally(state, named, err.Error()); recordErr != nil {
				refused = errors.Join(refused, recordErr)
			}
		}
		return Outcome{}, refused
	}
	// An environmental refusal on the record belongs to a round that is over: a
	// dispatch something turned away before it reached this run, or a round an
	// earlier process settled. This one is a fresh round and is classified on its
	// own evidence, so the old record is cleared here — where every resume passes,
	// rather than only on the repair-grant continuation that clears it for the same
	// reason. Left standing it would reach the terminal record, the docket, and the
	// thread of whatever this round turns out to be, telling an operator the item
	// stands where it did while its counters say otherwise, which is the misreading
	// this class exists to prevent, inverted.
	state.Environmental = nil
	// The invariants are re-read rather than carried in run state: they are the
	// repository's current constraints, and a resumed attempt must be held to what
	// holds now rather than to what held when the interrupted process started.
	invariants, err := p.loadInvariants()
	if err != nil {
		return Outcome{}, err
	}
	// A run reserved before either was recorded acquires them as it is picked up,
	// which is the best either can be: the account is the one this process is
	// about to spend, and the configuration is the one the rest of the run is
	// carried out under. A record that already names them keeps what it names —
	// re-stamping would quietly replace evidence about the run with a reading of
	// the file as it stands now.
	if state.AccountAlias == "" {
		// The pool is asked here rather than only at the start, because a record
		// written before the alias was carried has no account to be affined to and
		// this process is about to spend one. A pool that cannot choose leaves the
		// record as it found it: a resume is not worth refusing over an attribution
		// that was already missing.
		if account, err := p.chooseAccount(); err == nil {
			state.AccountAlias = account.Alias
		}
	}
	if state.ConfigRevision == "" {
		state.ConfigRevision = p.Config.Revision()
	}
	run := &activeRun{
		pipeline:   p,
		state:      state,
		item:       item,
		context:    bundle.Text,
		claimed:    true,
		publishing: publishing,
		invariants: invariants,
		// The worktree is reconstructed from what was recorded when it was
		// created, never from what the repository looks like now. The manager
		// revalidates ownership of every field before it acts on them.
		worktree: gitworktree.Worktree{
			RunID:         state.RunID,
			WorkItemID:    state.WorkItemID,
			Path:          state.WorktreePath,
			Branch:        state.Branch,
			BaseCommit:    state.BaseCommit,
			TargetBranch:  state.TargetBranch,
			HarnessCommit: state.HarnessCommit,
		},
		outcome: Outcome{
			RunID:                 state.RunID,
			WorkItemID:            state.WorkItemID,
			Status:                runstate.StatusRunning,
			Phase:                 state.Phase,
			Branch:                state.Branch,
			WorktreePath:          state.WorktreePath,
			BaseCommit:            state.BaseCommit,
			ProviderSessionID:     state.ProviderSessionID,
			ProviderModel:         state.ProviderModel,
			ProviderResolvedModel: state.ProviderResolvedModel,
			RepairAttempts:        state.RepairAttempts,
			TransientRelaunches:   state.TransientRelaunches,
			UsageLimitKind:        state.UsageLimitKind,
			PauseCause:            state.PauseCause,
			// A resumed run keeps the pull request the interrupted process
			// published, so the attempt it is owed updates that request rather than
			// opening a second one for the same branch. It reports a skipped
			// publication for the same reason a fresh run does: a repository with no
			// remote must say so on every pass, not only the first.
			PullRequest:    state.PullRequest,
			PublishSkipped: skipped,
		},
	}
	// A stop the operator asked for is honored before anything is resumed. The
	// process that was serving this run may have exited before it reached a
	// boundary, so without this a later invocation would pick the run up and carry
	// on with work somebody has already stopped.
	if err := run.stopRequested(); err != nil {
		return run.stop(ctx, err)
	}
	// A recorded directive pause is lifted rather than honored. Nothing reaches
	// this point while a directive still pauses the item — they were read before
	// the run was adopted — so the pause is over, and clearing it is what keeps a
	// running attempt from looking like a waiting one to the next process.
	if state.DirectivePause != nil {
		if err := run.clearDirectivePause(); err != nil {
			return run.fail(err, runstate.StatusFailed)
		}
	}
	// A recorded dependency pause is lifted rather than honored, on the same
	// evidence and for the same reason: nothing reaches this point while the item
	// still waits on unfinished work, because what it waits on was read from a
	// freshly loaded item before the run was adopted.
	if state.DependencyPause != nil {
		if err := run.clearDependencyPause(); err != nil {
			return run.fail(err, runstate.StatusFailed)
		}
	}
	// A recorded operator hold is lifted rather than served, for the same reason
	// and on the same evidence: nothing reaches this point while the operator is
	// still holding activity, because the hold was read before the run was
	// adopted. What the run was held for is added to its account as it is cleared,
	// so the time nobody spent is attributed to whoever decided it.
	if state.OperatorHeldSince != nil {
		if err := run.clearOperatorHold(); err != nil {
			return run.fail(err, runstate.StatusFailed)
		}
	}
	// A recorded deadline is honored before anything else happens, so a restart
	// during a pause waits out the rest of it rather than asking the provider
	// again and being refused by the same limit.
	if state.UsageLimitResetsAt != nil {
		if err := run.awaitRecordedUsageLimit(ctx); err != nil {
			return run.stop(ctx, err)
		}
	}
	// A re-entry carries the preserved change, not a clean worktree. It is asked
	// here rather than inside the branch below because both routes out of this
	// point continue a change: the branch below hands a developer a failure about
	// a change this run already made, and the step past it puts that change
	// through the checks and the reviewer. Neither has anything to work on if the
	// worktree lost it.
	if err := run.verifyHandback(ctx); err != nil {
		return run.stop(ctx, err)
	}
	// A repair attempt that was in flight when the process stopped was already
	// counted against the budget, so it is re-run rather than re-counted, with
	// the same session and the same repair input it was given.
	if state.Phase == runstate.PhaseDeveloping {
		prompt, err := resumedDeveloperPrompt(state, p.developer().Persona.Text, run.deliveredInvariants().Text(), bundle.Text,
			protectedpath.Protect(p.Config), run.repairBudget())
		if err != nil {
			return run.fail(err, runstate.StatusFailed)
		}
		if err := run.develop(ctx, prompt, state.ProviderSessionID); err != nil {
			return run.stop(ctx, err)
		}
	}
	return run.verifyReviewAndFinish(ctx)
}

// resumedDeveloperPrompt rebuilds the prompt the interrupted attempt was given
// from what survived on disk. Only one kind of repair input is ever recorded at
// a time, and where more than one is somehow present the most recent trigger
// wins. That is the earliest gate a run meets rather than the latest, because a
// gate that refuses is a gate the ones behind it never ran: refused paths are
// decided in front of the checks, so a check failure beside them was recorded
// against a change this run has already moved past, and the same holds for
// findings beside a failing check. A run that recorded none of the three never
// had a failure returned to it — it paused before or during its first attempt —
// so what it is owed is that attempt.
func resumedDeveloperPrompt(state runstate.State, persona, invariants, bundle string, protected protectedpath.Set, limit int) (string, error) {
	switch {
	case state.PathRefusal != nil:
		return pathRefusalRepairPrompt(invariants, *state.PathRefusal, protected, state.RepairAttempts, limit), nil
	case state.CheckFailure != nil:
		return checkRepairPrompt(invariants, *state.CheckFailure, state.RepairAttempts, limit), nil
	case len(state.ReviewFindingDetails) > 0:
		return repairPrompt(invariants, state.ReviewSummary, state.ReviewFindingDetails, state.RepairAttempts, limit)
	default:
		return developerPrompt(persona, invariants, bundle), nil
	}
}

// handedBackRepair reports a run carrying a failure that was actually returned
// to its developer: refused paths, a failing check, or the reviewer's findings.
// Each of the three is a failure about a change that exists, so the presence of
// any of them is what says a worktree is supposed to hold one, and a run that
// recorded none of them never had a failure returned at all.
func handedBackRepair(state runstate.State) bool {
	return state.PathRefusal != nil || state.CheckFailure != nil || len(state.ReviewFindingDetails) > 0
}

// resumesAnExistingChange reports a resumed run whose worktree is supposed to
// hold a change already. Two different facts put a run in that position, and
// both of them have to be here or the gate below covers one route and reads as
// though it covers every one.
//
// A run resumed inside its repair loop carries a failure returned about a change
// it made, and the prompt it is about to be handed describes that change. A run
// resumed at the checks or at the review has completed a developer attempt
// whatever else it recorded — what those two steps judge is the change that
// attempt made, and there is nothing else there for them to judge. That second
// one is not hypothetical: a repair round that reached a review and burned it on
// an empty diff is one of the field instances this item was filed for.
//
// The one resume this is false for is the run owed its first attempt — paused
// before or during it, with no failure ever returned — and an empty worktree is
// exactly what that attempt starts from.
func resumesAnExistingChange(state runstate.State) bool {
	switch state.Phase {
	case runstate.PhaseChecking, runstate.PhaseReviewing:
		return true
	case runstate.PhaseDeveloping:
		return handedBackRepair(state)
	default:
		return false
	}
}

// owedARepair reports a stopped run a repair would continue rather than replace:
// it ended on a blocker nobody has settled, a failure was returned to its
// developer, and the branch it left still carries the change. All three are read
// from the run's own record, which is the only account of it that survives the
// process that made it.
func owedARepair(state runstate.State) bool {
	if !state.Status.Terminal() || strings.TrimSpace(state.Blocker) == "" {
		return false
	}
	if !handedBackRepair(state) {
		return false
	}
	return state.Branch != "" && !state.BranchRemoved
}

// ErrHandbackSubstituted is what a fresh run refused for standing in place of a
// repair unwraps to, so a caller can tell it from a handback that arrived
// without its change — the opposite failure, and the one that at least got as
// far as re-entering the run.
var ErrHandbackSubstituted = errors.New("a fresh run would start over on work a repair is owed")

// SubstitutedHandbackError refuses a fresh run of an item whose last run stopped
// owing a repair of the change it preserved. Nothing was reserved, claimed, or
// created: the stopped run is exactly as it was, and so is its branch.
type SubstitutedHandbackError struct {
	WorkItemID   string
	RunID        string
	Branch       string
	WorktreePath string
}

func (e SubstitutedHandbackError) Error() string {
	return fmt.Sprintf(
		"run %s of %s stopped on a blocker with a failure returned to its developer and its change preserved on %s, so what that stoppage is owed is a repair of the change it already has rather than a fresh run started from nothing; no run was reserved and no worktree was created. A fresh run here hands a developer the work item and an empty worktree off the target branch, which is delivered as an empty change or as the preserved change re-derived by hand — `yoyo triage repair %s` continues the stopped run in the worktree it preserved at %s, and `yoyo triage rerun %s` is what starts over deliberately, recording that the development manager decided the ground moved",
		e.RunID, e.WorkItemID, e.Branch, e.RunID, e.WorktreePath, e.RunID)
}

func (e SubstitutedHandbackError) Unwrap() error { return ErrHandbackSubstituted }

// refuseSubstitutedHandback refuses to start a fresh run of an item whose last
// run stopped owing a repair of the change it preserved.
//
// This is the other half of the failure this item was filed for, and it is not
// the same failure as a handback arriving on an empty worktree: nothing is
// handed back at all. A fresh run reserves a new run, creates a new worktree off
// the target branch, and hands a developer the work item — so the developer sees
// work to do from nothing, and what it delivers is either an empty change or the
// preserved change re-derived by hand against a base that has moved. It is
// silent by construction, because the fresh worktree is perfectly valid and the
// only record that says otherwise belongs to a run nothing in this path reads.
//
// A re-run is the one fresh run of such an item that is right, and it says so in
// the record before it starts: triage claims it against the stoppage, and a claim
// naming this run is the development manager deciding that the ground moved and
// the work is to be done again. So the claim is what this looks for, and its
// absence is what refuses. An item nobody has run yet, one whose last run
// finished, and one whose preserved branch has since been retired all pass
// without a question being asked of them.
func (p Pipeline) refuseSubstitutedHandback(workItemID string) error {
	latest, err := p.Store.Latest(workItemID)
	if errors.Is(err, runstate.ErrNoRecordedRun) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the last run of %s, to tell a fresh run from work a repair is owed: %w", workItemID, err)
	}
	if !owedARepair(latest) {
		return nil
	}
	claimed, err := p.Store.Reruns().Claimed(workItemID)
	if err != nil {
		return fmt.Errorf("read the re-runs triage has claimed of %s: %w", workItemID, err)
	}
	for _, rerun := range claimed {
		if rerun.PriorRunID == latest.RunID {
			return nil
		}
	}
	return SubstitutedHandbackError{
		WorkItemID:   workItemID,
		RunID:        latest.RunID,
		Branch:       latest.Branch,
		WorktreePath: latest.WorktreePath,
	}
}

// activeRun is one work item's run in progress: the durable state, the reported
// outcome, and the worktree and context every attempt shares. A fresh run and a
// resumed one both build one and then take the same steps, so a repair attempt
// behaves identically whichever process started it.
type activeRun struct {
	pipeline Pipeline
	state    runstate.State
	outcome  Outcome
	item     beads.WorkItem
	worktree gitworktree.Worktree
	context  string
	// claimed records that the tracker holds this item, which is what makes a
	// failure worth reporting back to it.
	claimed bool
	// publishing records that this run publishes: the configuration asked for it
	// and the repository has a remote to publish to. It is decided once, before
	// the item is claimed, so no step has to re-derive it.
	publishing bool
	// invariants is every architectural invariant the repository records. The set
	// is kept rather than one selection of it because the two roles are selected
	// for differently: the developer's set is what the work item names, and the
	// reviewer's adds what the change turned out to touch.
	invariants invariant.Set
	// artifactSet is the recorded canonical documents, read only if something in
	// this run proposes a change to one and kept so a second proposal in the same
	// run does not read the repository again. Nil means nothing has needed it.
	artifactSet *artifact.Set
	// proposedAmendments is the changes this run has already recorded, by document
	// and change, so a developer that makes the same argument again on a repair
	// attempt raises one proposal rather than one per attempt.
	proposedAmendments map[string]bool
	// inProcessWait is how long this process has already slept waiting out usage
	// limits for this run, across every probe and every phase. It is what the
	// in-process bound is measured against, because that bound is on how long a
	// process stays open rather than on any one probe. It is deliberately not
	// durable: a later invocation is a new process and gets the whole bound.
	inProcessWait time.Duration
	// pausedAt is when this process recorded the deadline it is now serving. It
	// dates the pause so an operator's release can be told apart from one aimed
	// at a pause this run has already served. The zero time means the deadline
	// was written by an earlier process, which no release can predate.
	pausedAt time.Time
}

// loadInvariants reads the architect's durable constraints. It is a hard failure
// rather than an empty set, because delivering nothing is what an unconstrained
// repository looks like and the whole point of an invariant is that a developer
// whose own work looks correct is stopped by it.
func (p Pipeline) loadInvariants() (invariant.Set, error) {
	store := invariant.Store{RepositoryRoot: p.Repository, Directory: p.Config.Product.Invariants}
	set, err := store.Load()
	if err != nil {
		return invariant.Set{}, fmt.Errorf("load architectural invariants: %w", err)
	}
	return set, nil
}

// deliveredInvariants selects the invariants relevant to this run's work item and
// records what was delivered. It is what reaches the developer, and it depends on
// nothing anybody wrote into the bead by hand: the work item's own prose is the
// evidence, and every repository-wide invariant reaches every item regardless.
func (a *activeRun) deliveredInvariants() invariant.Delivery {
	delivery := a.invariants.Select(workItemEvidence(a.item)...)
	a.recordDeliveredInvariants(delivery)
	return delivery
}

// reviewedInvariants selects the invariants for the reviewer. The change itself
// is added to the evidence, so an invariant scoped to code the work item never
// mentioned still reaches the gate that judges the change that touched it —
// which is exactly the case a developer's own reading of its work cannot catch.
func (a *activeRun) reviewedInvariants(changes gitworktree.ChangeDiff) invariant.Delivery {
	evidence := append(workItemEvidence(a.item), changes.Status, changes.DiffStat)
	delivery := a.invariants.Select(evidence...)
	a.recordDeliveredInvariants(delivery)
	return delivery
}

// workItemEvidence is what the harness knows about the code a work item concerns
// before any of it exists: the item's own prose. Scope selection is textual over
// this, so an item that names the package it is about pulls in the invariants
// that constrain it.
func workItemEvidence(item beads.WorkItem) []string {
	return []string{item.Title, item.Description, item.Design, item.AcceptanceCriteria, item.Notes}
}

// grantEvidence is the part of a work item that can admit a protected path, and
// it is deliberately narrower than the evidence above: the fields somebody
// authored, and not the notes.
//
// The notes are where the harness appends what a run produced, and some of that
// is written by an agent — the reviewer's verdict summary and every finding
// message go into the item's notes through RecordOutcome and Block. Reading
// grants from there would mean an agent's own prose could admit a protected path
// for the next run of the same item, which is exactly the thing this gate exists
// to stop, and it would make "nothing an agent writes grants a path" false in the
// contract that says it. Nothing the harness writes touches the four fields below.
//
// The consequence is worth stating because it is the failure an operator would
// meet: a grant written into the notes does not count. The refusal names the
// fields a grant is read from, so an item that visibly says the words and is
// still refused says why.
func grantEvidence(item beads.WorkItem) []string {
	return []string{item.Title, item.Description, item.Design, item.AcceptanceCriteria}
}

// refuseProviderGrant refuses to start on an item that grants a path no provider
// honours. Such a grant admits work no attempt can finish, so no attempt is
// made: the run stops here, before the item is claimed and before a single
// repair round is spent, which is the entire failure this gate was built for.
//
// It exists as well as the check admission makes, rather than instead of it,
// because the two doors admission holds — a proposal and a tracker action —
// carry an item's title and description and nothing else. A grant is honoured
// from the design guidance and the acceptance criteria too, and nothing in the
// harness writes either of those: they are set with the tracker's own command,
// by an operator or by an agent's shell, so there is no admission door for them
// to be refused at. This is where a grant written into one is caught, and it is
// also what catches an item admitted before that gate existed.
//
// It reads exactly the fields the gate that obeys a grant reads, through the
// same predicate admission asks, so what a run refuses and what a run would have
// obeyed can never come apart.
func refuseProviderGrant(item beads.WorkItem) error {
	problems := protectedpath.GrantProblems(grantEvidence(item)...)
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("work item %s grants a path no run can write to: %w", item.ID, errors.Join(problems...))
}

// recordDeliveredInvariants keeps the run's account of which constraints its
// change was held to. It merges rather than replaces, because a run delivers
// twice — once to the developer and once to the reviewer — and the second
// selection can legitimately be wider than the first.
func (a *activeRun) recordDeliveredInvariants(delivery invariant.Delivery) {
	for _, id := range delivery.IDs() {
		a.outcome.Invariants = appendUnique(a.outcome.Invariants, id)
	}
	for _, problem := range delivery.Problems {
		a.outcome.InvariantProblems = appendUnique(a.outcome.InvariantProblems, problem.String())
	}
	for _, id := range delivery.Omitted {
		a.outcome.InvariantProblems = appendUnique(a.outcome.InvariantProblems,
			id+": it matched this work item and did not fit the delivered context")
	}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// verifyReviewAndFinish is the gated half of a run: the deterministic checks,
// the independent review behind them, the bounded repair loop that returns
// either kind of failure to the developer, and then integration and cleanup of
// a change an attempt actually got approved.
func (a *activeRun) verifyReviewAndFinish(ctx context.Context) (Outcome, error) {
	// A run whose integration a human still approves has no repair loop to
	// return anything to: nothing is promoted without that person, and the
	// worktree is preserved for them either way, so a failing check ends the
	// run exactly as it always has.
	if !a.pipeline.automatic() {
		if err := a.holdForDirective(); err != nil {
			return a.stop(ctx, err)
		}
		if err := a.holdForDependency(ctx); err != nil {
			return a.stop(ctx, err)
		}
		if err := a.verify(ctx); err != nil {
			return a.stop(ctx, err)
		}
		return a.finish(ctx)
	}
	// The whole gate repeats when a promotion loses its race for the target
	// branch, because everything it established belongs to a change that would
	// now be promoted onto a different base: the checks ran against the old one
	// and the approval was given for the old diff. Nothing about the loop
	// weakens the gate — it re-earns it.
	for {
		if err := a.repairLoop(ctx); err != nil {
			return a.stop(ctx, err)
		}
		// An approval only authorizes integration when it demonstrably came from a
		// second invocation. Missing or reused provider identity means the
		// independence the policy relies on was never established.
		if err := validateIndependentInvocations(a.outcome); err != nil {
			return a.fail(err, runstate.StatusFailed)
		}
		// The promotion is the last moment a directive can still stop this work,
		// and the loop above can have spent hours in the provider since it last
		// asked. Asking again here is what keeps a directive recorded mid-repair
		// from reaching the run only after its change was already on the target
		// branch, which is indistinguishable from it reaching nothing. A dependency
		// link applied in that same stretch is the same fact and is asked the same
		// way: promoting work somebody has just made wait on other work is the one
		// outcome a link applied late must not still produce.
		if err := a.holdForDirective(); err != nil {
			return a.stop(ctx, err)
		}
		if err := a.holdForDependency(ctx); err != nil {
			return a.stop(ctx, err)
		}
		err := a.integrate(ctx)
		if err == nil {
			return a.finish(ctx)
		}
		retry, retryErr := a.prepareIntegrationRetry(ctx, err)
		if retryErr != nil {
			return a.fail(retryErr, failureStatus(ctx, retryErr))
		}
		if !retry {
			return a.fail(err, failureStatus(ctx, err))
		}
	}
}

// contendedIntegration reports a promotion refused because the target branch is
// not where this run left it. Both refusals mean it: the recorded base no
// longer matches the branch, or the fast-forward itself lost the race. Neither
// says anything is wrong with the change, which is why they are the only
// failures worth re-preparing rather than ending the run on.
func contendedIntegration(err error) bool {
	return errors.Is(err, gitworktree.ErrTargetDrift) || errors.Is(err, gitworktree.ErrNotFastForward)
}

// prepareIntegrationRetry re-prepares a change whose promotion lost its race,
// and reports whether the run may try again. The retry is recorded before any
// of it happens, so a process that dies part-way through cannot come back to a
// fresh budget, and the commit the refused promotion had already made is
// recorded for the same reason publishing records its own: a worktree at a HEAD
// nothing named is a worktree nothing may promote afterwards.
//
// A replay that conflicts is the end of the line rather than another retry. The
// harness does not decide which side of a conflict is right — that belongs to
// the development manager, which is the operator until the role exists — so it
// is recorded as a blocker on the work item with the artifacts preserved.
func (a *activeRun) prepareIntegrationRetry(ctx context.Context, cause error) (bool, error) {
	if !contendedIntegration(cause) {
		return false, nil
	}
	limit := a.pipeline.Config.Execution.IntegrationRetriesBeforeReconciliation
	if a.state.IntegrationRetries >= limit {
		return false, a.blockOnContendedIntegration(cause, limit)
	}
	a.state.IntegrationRetries++
	a.outcome.IntegrationRetries = a.state.IntegrationRetries
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return false, fmt.Errorf("save integration retry %d: %w", a.state.IntegrationRetries, err)
	}
	rebase, err := a.pipeline.Worktrees.RebaseOntoTarget(ctx, a.worktree, integrationMessage(a.item, a.outcome))
	// The replay reports the commit it owns whichever way it went, so it is
	// recorded before the failure is: an aborted replay leaves the branch on that
	// commit, and the worktree is preserved for whoever picks the conflict up.
	if recordErr := a.recordRebase(rebase); recordErr != nil {
		return false, errors.Join(err, recordErr)
	}
	if errors.Is(err, gitworktree.ErrRebaseConflict) {
		return false, a.blockOnRebaseConflict(err)
	}
	if err != nil {
		return false, fmt.Errorf("replay the change onto the moved integration target: %w", err)
	}
	// The published branch has to become the replayed one, or the pull request
	// would carry work the authoritative local branch no longer has.
	if err := a.republishRebase(ctx, rebase); err != nil {
		return false, err
	}
	// The approval that was granted described the change on its old base. It is
	// discarded rather than carried over, so the next pass through the gate gets
	// its own independent verdict on the replayed diff.
	//
	// The deterministic checks need no equivalent, and the asymmetry is not an
	// omission: a verdict is a recorded fact that would otherwise still be
	// standing, while the checks are simply run again. verify() executes every
	// configured command on every pass and records what they did, so the replayed
	// change is judged by checks that ran against it rather than by a result from
	// before it was replayed.
	a.clearReviewEvidence()
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return false, fmt.Errorf("save replayed run state: %w", err)
	}
	return true, nil
}

// recordRebase makes the replayed base and the harness commit that carries the
// work durable together. They move as one — the recorded base is what the
// promotion is checked against and what every diff is taken from, and the
// harness commit is the only HEAD the worktree may be at — so a record with one
// of them updated and not the other describes a worktree nothing would accept.
func (a *activeRun) recordRebase(rebase gitworktree.Rebase) error {
	if rebase.HeadCommit == "" {
		return nil
	}
	a.worktree.BaseCommit = rebase.BaseCommit
	a.state.BaseCommit = rebase.BaseCommit
	a.outcome.BaseCommit = rebase.BaseCommit
	// A replay that leaves nothing above the base has no harness commit to name:
	// the work it replayed is already in the target. Recording one anyway would
	// claim a commit past a base that is also that commit.
	harnessCommit := rebase.HeadCommit
	if harnessCommit == rebase.BaseCommit {
		harnessCommit = ""
	}
	a.recordHarnessCommit(harnessCommit)
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("save the replayed change: %w", err)
	}
	return nil
}

// block records one durable blocker on the work item and keeps its text on the
// run. Every stoppage a person has to decide about goes through here, which is
// what makes the blocker a triage docket entry carries the same words the item
// carries rather than a second account assembled from the same evidence — and
// what lets a reader of the run record afterwards say what stopped it without
// working out which of the item's notes was this run's.
//
// It is recorded on its own deadline rather than on a context this run may have
// exhausted: a run that stopped on something nobody was told about is the one
// outcome this must never produce. The text is durable on the run only when the
// tracker took it, so a blocker the item never carried is never claimed here.
func (a *activeRun) block(notes string) error {
	blockCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.pipeline.Tracker.Block(blockCtx, a.state.WorkItemID, notes); err != nil {
		return err
	}
	a.outcome.Blocked = true
	// The terminal write that ends the run is a moment away and carries this
	// with it, so nothing is saved here: a second write would be one more chance
	// for the record and the item to disagree about what stopped the run.
	a.state.Blocker = runstate.RecordBlocker(notes)
	return nil
}

// blockOnContendedIntegration ends a run that kept losing its target branch. It
// is the integration-side twin of the repair blockers: the change is sound as
// far as every gate could tell, and what it needs is a person to say what the
// target is supposed to look like.
func (a *activeRun) blockOnContendedIntegration(cause error, limit int) error {
	blocked := fmt.Errorf("integration lost its target branch after %d of %d permitted retry(s): %w",
		a.state.IntegrationRetries, limit, cause)
	if err := a.block(renderIntegrationBlockerNotes(a.outcome, blocked.Error(), limit)); err != nil {
		return errors.Join(blocked, fmt.Errorf("record the contended integration as a blocker: %w", err))
	}
	return blocked
}

// blockOnRebaseConflict ends a run whose change cannot be replayed onto what its
// target became. Nothing is forced and nothing is resolved: the worktree and the
// branch stay exactly as they were, and the conflict is recorded for whoever
// owns the decision.
func (a *activeRun) blockOnRebaseConflict(cause error) error {
	if err := a.block(renderRebaseConflictNotes(a.outcome, cause.Error())); err != nil {
		return errors.Join(cause, fmt.Errorf("record the replay conflict as a blocker: %w", err))
	}
	return cause
}

// blockOnDivergedTarget ends a run whose target branch and the remote's have
// gone different ways, before anything is promoted. It is the remote-side twin
// of the replay conflict: the local branch cannot be brought onto what the
// remote holds, so there is nowhere to replay onto, and promoting anyway would
// close this item as integrated against a divergence no later sweep can
// reconcile. Nothing is forced and nothing is reset — both branches are left
// exactly where they are and named for whoever settles them.
func (a *activeRun) blockOnDivergedTarget(catchup gitworktree.Catchup) error {
	remote := a.pipeline.Config.Execution.Remote
	diverged := fmt.Errorf("%s cannot be brought onto %s before promoting: %s",
		catchup.TargetBranch, remote, catchup.Held)
	if err := a.block(renderDivergedTargetNotes(a.outcome, catchup, remote, diverged.Error())); err != nil {
		return errors.Join(diverged, fmt.Errorf("record the diverged target branch as a blocker: %w", err))
	}
	return diverged
}

// blockOnPromotedDivergence ends a run whose remote target diverged in the
// window after the local promotion, which is the same divergence as
// blockOnDivergedTarget's with one thing already done that cannot be undone: the
// change is on the local target branch. So the blocker says so rather than
// pretending otherwise — what a person settles here is the two branches and the
// publication, not the work — and the run ends without closing the item, because
// an item closed as integrated is exactly the receipt that made this outcome
// invisible.
func (a *activeRun) blockOnPromotedDivergence(integration gitworktree.Integration, catchup gitworktree.Catchup, cause error) error {
	remote := a.pipeline.Config.Execution.Remote
	diverged := fmt.Errorf("%w; %s cannot be brought onto %s afterwards: %s",
		cause, integration.TargetBranch, remote, catchup.Held)
	if err := a.block(renderPromotedDivergenceNotes(a.outcome, integration, catchup, remote, diverged.Error())); err != nil {
		return errors.Join(diverged, fmt.Errorf("record the diverged target branch as a blocker: %w", err))
	}
	return diverged
}

// repairLoop returns each failure to the same developer until an attempt both
// passes the deterministic gates and is approved, or the configured repair
// budget is spent. A refused path, a failing check, and a reviewer's findings
// are the same kind of event here: each is repair input for the developer that
// produced the change, and all three draw on one shared budget. Sharing it is
// what bounds the total number of developer invocations a run can make, which is
// what the budget exists to do; separate budgets would let a run alternating
// between them spend far more than the operator configured. Every attempt puts
// the change through both gates again and obtains its own independent review, so
// an approval always belongs to a change that passed them, and nothing an
// earlier attempt was granted carries forward.
func (a *activeRun) repairLoop(ctx context.Context) error {
	limit := a.repairBudget()
	for {
		// Every round of the gate asks what the operator has directed, because a
		// round is another developer invocation and a directive recorded while one
		// was running must not buy the run another one. The first round is where a
		// directive that arrived during the attempt that got the run here reaches
		// it, which is the case this exists for.
		if err := a.holdForDirective(); err != nil {
			return err
		}
		// It asks what the item waits on for the same reason, from a freshly read
		// item rather than from the one selection saw. The first round is where a
		// dependency link applied while the developer was working reaches the run,
		// and it reaches it before the reviewer is asked to judge work that should
		// never have been developed — which is the round that was burned when this
		// gate was missing.
		if err := a.holdForDependency(ctx); err != nil {
			return err
		}
		if err := a.verify(ctx); err != nil {
			// A change refused for what it touched is answered before anything else,
			// because it is what the gate decided first: the checks never ran on this
			// attempt, so there is no check failure competing with it.
			var refused pathRefusal
			if errors.As(err, &refused) {
				a.recordPathRefusal(refused.refusal)
				if a.state.RepairAttempts >= limit {
					return a.blockOnRefusedPaths(refused, limit)
				}
				if err := a.repair(ctx, pathRefusalRepairPrompt(a.deliveredInvariants().Text(), refused.refusal, refused.set, a.state.RepairAttempts+1, limit)); err != nil {
					return err
				}
				continue
			}
			// Verification that could not run at all is not something a developer
			// can repair, so it ends the run rather than spending an attempt.
			var failing checkFailure
			if !errors.As(err, &failing) {
				return err
			}
			a.recordCheckFailure(failing.result)
			if a.state.RepairAttempts >= limit {
				return a.blockOnFailingCheck(limit)
			}
			if err := a.repair(ctx, checkRepairPrompt(a.deliveredInvariants().Text(), *a.state.CheckFailure, a.state.RepairAttempts+1, limit)); err != nil {
				return err
			}
			continue
		}
		decision, err := a.reviewChange(ctx)
		if err != nil {
			return err
		}
		if decision == review.DecisionApprove {
			return nil
		}
		if a.state.RepairAttempts >= limit {
			return a.blockOnUnresolvedFindings(limit)
		}
		prompt, err := repairPrompt(a.deliveredInvariants().Text(), a.state.ReviewSummary, a.state.ReviewFindingDetails, a.state.RepairAttempts+1, limit)
		if err != nil {
			return err
		}
		if err := a.repair(ctx, prompt); err != nil {
			return err
		}
	}
}

// repairBudget is how many repair attempts this run may make: what the project
// configured, plus whatever triage has granted it to continue on. A run nothing
// continued is the configured budget unchanged, which is every run until triage
// re-enters one.
func (a *activeRun) repairBudget() int {
	return a.state.RepairBudget(a.pipeline.Config.Execution.RepairAttemptsBeforeReplan)
}

// repair records one attempt against the budget and then hands the failure back
// to the developer. The attempt is recorded before the developer is invoked, so
// an interrupted attempt still counts and a restart resumes at the attempt the
// run reached rather than buying it another one. The same developer continues in
// the same worktree, resuming its session so the attempt keeps the context it
// already built.
func (a *activeRun) repair(ctx context.Context, prompt string) error {
	a.state.RepairAttempts++
	a.state.Phase = runstate.PhaseDeveloping
	a.state.UpdatedAt = a.pipeline.clock().Now()
	a.outcome.RepairAttempts = a.state.RepairAttempts
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("save repair attempt %d: %w", a.state.RepairAttempts, err)
	}
	return a.develop(ctx, prompt, a.state.ProviderSessionID)
}

// recordCheckFailure makes the failing check the run's outstanding repair input.
// Any findings recorded beside it described the change an earlier attempt was
// judged on, and that change no longer passes its checks, so they are cleared
// rather than left to compete with the check for the next attempt.
func (a *activeRun) recordCheckFailure(result checks.Result) {
	a.clearReviewEvidence()
	a.state.CheckFailure = &runstate.CheckFailure{
		Command:  result.Command,
		ExitCode: result.Process.ExitCode,
		Output:   boundedCheckOutput(result.Process),
	}
}

// recordPathRefusal makes the refused paths the run's outstanding repair input.
// It clears both of the others for the reason recordCheckFailure clears the
// findings, and it clears one more of them than that does: this gate is decided
// in front of the checks, so a check failure recorded beside it describes a
// suite that did not run on the change now in the worktree.
func (a *activeRun) recordPathRefusal(refusal runstate.PathRefusal) {
	a.clearReviewEvidence()
	a.state.CheckFailure = nil
	recorded := refusal
	a.state.PathRefusal = &recorded
}

// blockOnUnresolvedFindings ends a run whose repair budget is spent. The design
// hands control back to a development manager at this point; that role does not
// exist yet, so the unresolved findings are recorded as a durable blocker on the
// work item rather than disappearing along with the failed run.
func (a *activeRun) blockOnUnresolvedFindings(limit int) error {
	cause := fmt.Errorf("independent review requires repair after %d of %d permitted attempt(s): %s",
		a.state.RepairAttempts, limit, a.outcome.ReviewSummary)
	if err := a.block(renderBlockerNotes(a.outcome, limit)); err != nil {
		return errors.Join(cause, fmt.Errorf("record unresolved review findings as a blocker: %w", err))
	}
	return cause
}

// blockOnFailingCheck ends a run whose repair budget was spent on a check that
// still fails. It is the check-side twin of blockOnUnresolvedFindings: the run
// cannot reach review or integration, so what stopped it is recorded on the work
// item rather than disappearing along with the failed run.
func (a *activeRun) blockOnFailingCheck(limit int) error {
	failure := *a.state.CheckFailure
	cause := fmt.Errorf("verification failed after %d of %d permitted attempt(s): %s exited with %d",
		a.state.RepairAttempts, limit, failure.Command, failure.ExitCode)
	if err := a.block(renderCheckBlockerNotes(a.outcome, failure, limit)); err != nil {
		return errors.Join(cause, fmt.Errorf("record the failing check as a blocker: %w", err))
	}
	return cause
}

// verifyHandback proves the worktree a run is re-entered in still holds the
// change the next step is about, before that step spends anything on it.
//
// This is the enforcement rather than the courtesy. The triage action that
// carries out a handback asks the same question before it spends the item's
// grant, and asking it here is what makes the answer bind every route into a
// resumed run — that action, an interrupted process a later invocation picks up,
// and whatever re-entry is built next, none of which will mention this. The
// failure it catches is silent by construction: a worktree that lost its change
// looks exactly like a valid one, so what is spent on it comes back as an empty
// repair, a review round burned on an empty diff, or the change reinvented, and
// the run's own record afterwards says none of those.
//
// It refuses to a person rather than starting over. Whether the change was never
// seeded or somebody removed it is not something this can tell, and both are
// decisions about work that may still exist on the preserved branch.
func (a *activeRun) verifyHandback(ctx context.Context) error {
	if !resumesAnExistingChange(a.state) {
		return nil
	}
	if err := preservedChangeHeld(ctx, a.pipeline.Worktrees, a.state); err != nil {
		return a.blockOnMissingPreservedChange(err)
	}
	return nil
}

// blockOnMissingPreservedChange ends a run re-entered to continue a change its
// worktree does not hold. It is the handback-side twin of the repair blockers:
// nothing here says the change was wrong, and the branch the run recorded may
// still carry every line of it, so what this hands a person is where to go
// looking rather than a verdict.
func (a *activeRun) blockOnMissingPreservedChange(cause error) error {
	blocked := fmt.Errorf("%w: run %s was picked up again at the %s phase and %v", ErrPreservedChangeMissing, a.state.RunID, a.state.Phase, cause)
	// The environment is what refused this round, and saying so here is what lets
	// the settle give back what the round would otherwise have spent. It is
	// recorded before the block, so the terminal write that carries the blocker
	// carries the cause with it.
	a.recordEnvironmentalRefusal(runstate.CauseHandbackMissingChange, cause.Error(), nothingRan)
	if err := a.block(renderMissingPreservedChangeNotes(a.outcome, blocked.Error())); err != nil {
		return errors.Join(blocked, fmt.Errorf("record the missing preserved change as a blocker: %w", err))
	}
	return blocked
}

// nothingRan says a refusal happened before anything this round would have
// delivered could exist, and ranAnyway that it did not. They are named rather
// than passed as bare booleans, because which one a site says is the whole of
// what makes the emptiness question answerable on a run whose worktree already
// holds an earlier round's work.
const (
	nothingRan = true
	ranAnyway  = false
)

// recordEnvironmentalRefusal notes on the run that the environment, rather than
// the work, is why this round has nothing to show. It is written where the
// refusal is decided, because that is the only place that knows which cause it
// was and whether anything of the round ran; what the record is worth is decided
// at settle, which is the first point the other half of the definition can be
// asked.
//
// Nothing here is saved on its own. Every caller is a step away from the
// terminal write that ends the run, and a second write would be one more chance
// for the record and the item to disagree about what stopped it — which is the
// reason the blocker is not saved here either.
func (a *activeRun) recordEnvironmentalRefusal(cause runstate.EnvironmentalCause, detail string, nothingOfItRan bool) {
	refusal := &runstate.EnvironmentalRefusal{
		Cause:      cause,
		Detail:     singleLine(detail, runstate.MaxEnvironmentalDetailBytes),
		NothingRan: nothingOfItRan,
		RecordedAt: a.pipeline.clock().Now().UTC(),
	}
	a.state.Environmental = refusal
	a.outcome.Environmental = refusal
}

// settleEnvironmentalRound classifies the round this run is ending, and gives
// back what an environmental one must never have spent.
//
// The class is the conjunction and nothing less. The run recorded a cause the
// harness is answerable for, and the change that round was to deliver is not
// there. A cause on its own excuses nothing — a round that recorded one and
// delivered a change anyway spends exactly as any round does — and an empty
// delivery on its own excuses nothing either, which is what keeps a developer
// that did nothing out of a class built for a harness that handed it nothing.
//
// It runs here because here is the first point both halves are known, and before
// the stoppage is docketed, so the entry a development manager reads carries the
// budgets as this settle leaves them rather than as the round spent them.
//
// It never fails the run. The run is already ending, and a return that could not
// be written is a fact for whoever reads the record rather than a second reason
// to end it — so it is recorded on the refusal, where the docket and the thread
// both carry it, and the round stays spent until somebody looks.
func (a *activeRun) settleEnvironmentalRound() {
	refusal := a.state.Environmental
	if refusal == nil || refusal.Settled {
		return
	}
	// Whatever this concludes, it concludes once. A round is settled where it
	// ends, and a later round of the same run is judged on its own evidence rather
	// than inheriting a cause recorded before it.
	refusal.Settled = true
	settleCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	delivered, err := a.roundDelivered(settleCtx, refusal)
	if err != nil {
		// A round whose delivery cannot be read is left spent. That is the
		// direction this must fail in: an item charged a round it should have kept
		// is visible in its counters, and one credited a round it did spend is a
		// budget nothing bounds.
		refusal.Problem = singleLine(fmt.Sprintf(
			"whether run %s delivered anything could not be read, so the round was left spent: %v", a.state.RunID, err),
			runstate.MaxEnvironmentalProblemBytes)
		return
	}
	if delivered {
		return
	}
	refusal.Refused = true
	// The review round the item was charged against its cap, given back under the
	// attempt that produced it — which is what keeps the return to this round and
	// not to whatever the item spent before it.
	attempt := runstate.RoundKey(a.state.RunID, a.state.RepairAttempts)
	_, returned, err := a.pipeline.Store.Triage().ReturnReviewRound(settleCtx, a.state.WorkItemID, attempt, a.pipeline.clock().Now())
	if err != nil {
		refusal.Problem = singleLine(fmt.Sprintf(
			"the review round attempt %s was charged could not be returned: %v", attempt, err),
			runstate.MaxEnvironmentalProblemBytes)
	}
	refusal.RoundReturned = returned
	// And the granted repair round the continuation consumed, which is the budget
	// the field cases actually burned: the grant itself still stands, and this is
	// what stops the run's record saying it was carried out.
	refusal.GrantReturned = a.state.ReturnGrantedRound()
	a.outcome.Environmental = refusal
}

// roundDelivered reports this round having left a change behind.
//
// A refusal that says nothing of this round ran answers it without reading
// anything, and that is the case the worktree cannot answer. A round of a repair
// grant runs in the worktree earlier rounds already filled, so what is in it
// answers "did this run ever deliver anything" rather than "did this round" —
// and a granted round whose developer the machine never started would read as a
// delivery, spend the grant on work no agent did, and be described on every
// surface as a round that delivered a change. Where the refusal knows nothing
// ran, this round added nothing whatever the worktree holds.
//
// Everything else is measured against the worktree rather than trusted: a cause
// recorded from the error that ended a run can belong to a round that ran and
// delivered, and the conjunction the class is defined by is what stops one
// excusing the other. It asks the worktree rather than the run's recorded
// summary, because a summary is written by steps a refused round never reached,
// and it asks it through the run's own durable record, which is the only account
// of that worktree surviving the process that made it.
func (a *activeRun) roundDelivered(ctx context.Context, refusal *runstate.EnvironmentalRefusal) (bool, error) {
	if refusal.NothingRan {
		return false, nil
	}
	// A run with no worktree recorded delivered nothing, and that is proved rather
	// than assumed: the harness never gave it anywhere to deliver to. It is the
	// state a run refused before its worktree could be cut is in, which is exactly
	// a round the environment turned away.
	if a.state.WorktreePath == "" || a.state.BaseCommit == "" {
		return false, nil
	}
	changed, err := a.pipeline.Worktrees.ChangedPaths(ctx, worktreeOf(a.state))
	if err != nil {
		return false, err
	}
	return len(changed) > 0, nil
}

// environmentalCauseOf reports the environmental cause a failure names, where it
// names one. It is how the causes the harness recognizes from the failure alone
// reach the record: a run turned away because the primary checkout was not the
// harness's to cut from, and one whose provider invocation the machine never
// started at all. Both are the environment rather than the work, and neither has
// a refusal site of its own to record at — they surface as the error that ends
// the run.
//
// Nothing here guesses. A cause is named only by a sentinel the refusing package
// declares, so a message somebody rewords cannot silently move a round into or
// out of a class that returns budget.
func environmentalCauseOf(failure error) (runstate.EnvironmentalCause, bool) {
	switch {
	case errors.Is(failure, gitworktree.ErrPrimaryNotReady):
		return runstate.CauseDirtyPrimary, true
	case errors.Is(failure, execution.ErrProcessNotStarted):
		return runstate.CauseSandboxSpawnFailure, true
	default:
		return "", false
	}
}

// refuseDispatchEnvironmentally records, on a run the harness turned away before
// it entered it at all, that the environment is what turned it away.
//
// It exists for the one refusal outside the run's own machinery: a resumed run
// turned back because the repository is not in a state anything may be resumed
// against. The dispatch never became a round, so what it delivered is nothing —
// not because a worktree is empty, which is how a round that ran is measured, but
// because nothing ran. That is why the class is settled here without asking the
// worktree: the worktree holds whatever earlier rounds left, and none of it is
// this dispatch's.
//
// It gives nothing back, and says so. Nothing was charged: the run stays exactly
// as the interrupted process left it, with its attempt and its grant still live
// and still to be spent when it is resumed. What this buys is not an accounting
// correction but the record and the surfaces saying what happened, which is
// otherwise lost in an error a caller prints once.
//
// A record that could not be written is reported beside the refusal rather than
// in place of it: the refusal stands either way, and the run is untouched.
func (p Pipeline) refuseDispatchEnvironmentally(state runstate.State, cause runstate.EnvironmentalCause, detail string) error {
	turned := &activeRun{pipeline: p, state: state}
	turned.recordEnvironmentalRefusal(cause, detail, nothingRan)
	turned.state.Environmental.Settled = true
	turned.state.Environmental.Refused = true
	turned.state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(turned.state); err != nil {
		return fmt.Errorf("record the environmental refusal of run %s: %w", state.RunID, err)
	}
	return nil
}

// blockOnRefusedPaths ends a run whose repair budget was spent still touching
// paths its work item does not grant. It is the scope-side twin of
// blockOnFailingCheck, and what it hands to a person is a decision rather than a
// defect: either the change keeps reaching for something outside its item, or the
// item is missing a grant it should have had. The note names both possibilities,
// because only a person can say which one it is.
func (a *activeRun) blockOnRefusedPaths(refused pathRefusal, limit int) error {
	cause := fmt.Errorf("protected paths refused after %d of %d permitted attempt(s): %s",
		a.state.RepairAttempts, limit, strings.Join(refused.refusal.Paths, ", "))
	if err := a.block(renderPathRefusalBlockerNotes(a.outcome, refused, limit)); err != nil {
		return errors.Join(cause, fmt.Errorf("record the refused protected paths as a blocker: %w", err))
	}
	return cause
}

// develop runs one developer attempt in the run's worktree and records what the
// provider reported. A repair attempt passes the recorded session so the
// developer continues the change it already made instead of re-deriving it.
//
// A provider that refuses the attempt does not end the run, whether it refused
// for want of capacity on the account or because its own servers could not serve
// it. The work was never judged in either case, so the attempt is waited out and
// reissued in the same worktree and the same session. Only a wait the harness
// cannot take — an unusable reset time, or one beyond the configured maximum —
// stops the run.
//
// An attempt the provider killed rather than refused is reissued too, against a
// budget rather than a clock. There is no condition to wait out: a connection
// that dropped is already gone, and the provider's own retry ladder is spent
// before the harness ever sees the terminal, so what a relaunch waits for has
// already been waited. The relaunch keeps the worktree and the session for the
// same reason a refusal's reissue does, and here it matters more: the attempt
// that died mid-response had already made part of the change, and continuing the
// session is what carries that work into the next attempt instead of asking a
// developer to derive it a second time.
func (a *activeRun) develop(ctx context.Context, prompt, sessionID string) error {
	for {
		// A stop is asked for before the hold, so a run the operator both stopped
		// and paused stops rather than parking on a hold nothing will lift for it.
		if err := a.stopRequested(); err != nil {
			return err
		}
		// The operator's hold is asked before every attempt, including the reissue
		// after a refusal: a developer invocation is the largest thing this harness
		// spends, and a pause that only covered the first one would let a run keep
		// spending for as long as the provider kept refusing it.
		if err := a.holdForOperator(ctx); err != nil {
			return err
		}
		providerResult, err := a.attemptDevelopment(ctx, prompt, sessionID)
		limit, refusedForLimit := refusedForUsageLimit(providerResult, err)
		overload, refusedForOverload := refusedForServerOverload(providerResult, err)
		transient, died := diedTransiently(providerResult.TransientFailure, providerResult.Process.Status, providerResult.IsError, err)
		if !refusedForLimit && !refusedForOverload && !a.mayRelaunch(died) {
			recorded := a.recordDevelopment(ctx, providerResult, err)
			// A transient death with the budget already spent is recorded exactly as
			// any other developer failure is — the attempt's changes are part of what
			// the run has to say about itself — and then handed to a person, because
			// nothing else is going to relaunch it.
			if died {
				return a.blockOnSpentRelaunchBudget(ctx, transient, recorded)
			}
			if recorded != nil {
				// The developer invocation is what this round delivers with, so an
				// invocation the machine never started is a round that added nothing —
				// and it is said here because here is the only place that knows which
				// invocation it was. The same failure from a check would be a round
				// whose developer had already written a change, and this is what keeps
				// the two apart. The settle needs it: on a granted repair the worktree
				// holds what earlier rounds left, so nothing else could tell that this
				// round delivered nothing.
				if errors.Is(recorded, execution.ErrProcessNotStarted) {
					a.recordEnvironmentalRefusal(runstate.CauseSandboxSpawnFailure, recorded.Error(), nothingRan)
				}
				return recorded
			}
			// The attempt that just finished is what publishes. Doing it here
			// covers every developer invocation a run makes — the first and each
			// repair — so the pull request always shows the change the checks and
			// the reviewer are about to judge.
			return a.publishAttempt(ctx)
		}
		// The refused attempt may still have established a session. Continuing in
		// it is what lets the reissued attempt resume in context rather than
		// starting the work over.
		if providerResult.SessionID != "" {
			sessionID = providerResult.SessionID
			a.state.ProviderSessionID = providerResult.SessionID
			a.outcome.ProviderSessionID = providerResult.SessionID
		}
		// An exhausted limit is answered first when a refused attempt somehow
		// reports both: it is the longer wait of the two, and waiting an overload's
		// interval into a limit that has hours left would spend the budget on
		// attempts the account cannot serve.
		if refusedForLimit {
			if err := a.pauseForUsageLimit(ctx, limit); err != nil {
				return err
			}
			continue
		}
		// An overload is answered before a transient death for the same reason: a
		// terminal the backend somehow reported as both is the one condition here
		// that names a wait, and taking the wait costs a relaunch nothing while
		// skipping it spends the budget on a server that has not recovered yet.
		if refusedForOverload {
			if err := a.pauseForServerOverload(ctx, overload); err != nil {
				return err
			}
			continue
		}
		if err := a.recordRelaunch(); err != nil {
			return err
		}
	}
}

// mayRelaunch reports a provider death this run still has budget to absorb. It
// reads the durable count rather than anything this process is holding, so a run
// resumed after a crash mid-relaunch is bounded by what it already spent.
func (a *activeRun) mayRelaunch(died bool) bool {
	return died && a.state.TransientRelaunches < a.pipeline.Config.Execution.TransientRelaunchesBeforeBlocking
}

// diedTransiently reports an invocation the provider ended without judging the
// work, on something that may not happen again. It takes the same shape as the
// two refusals beside it: a transient failure reported alongside an invocation
// that still produced its answer is evidence rather than a death, so only one
// accompanying a failed attempt relaunches the run.
//
// An invocation the harness stopped on time is never one of these, however the
// provider's last words read. That stop is the harness's own decision, the run it
// leaves behind is owed a continuation rather than a relaunch, and charging it to
// a budget for the provider's weather would spend the run's tolerance on the
// harness's own clock.
func diedTransiently(failure *backend.TransientFailure, status execution.ProcessStatus, isError bool, err error) (backend.TransientFailure, bool) {
	if failure == nil || (err == nil && !isError) {
		return backend.TransientFailure{}, false
	}
	if _, stopped := providerStopReason(status); stopped {
		return backend.TransientFailure{}, false
	}
	return *failure, true
}

// recordRelaunch counts one provider death against the budget and records what
// killed the attempt. Like a repair attempt it is recorded before the relaunch it
// authorizes happens, so a process that dies here comes back to the budget it had
// spent rather than to a fresh one — which is the difference between a bounded
// self-repair and an unbounded loop that a crash resets.
//
// Nothing about the attempt's own outcome is recorded, because there is no
// outcome: the provider judged nothing, and writing a failure the next attempt
// will overwrite would make a run that recovered look like one that failed first.
// What killed the attempt is already durable without this — the terminal the
// provider sent is in the run's event stream — so the count is all this has to
// add.
func (a *activeRun) recordRelaunch() error {
	a.state.TransientRelaunches++
	a.state.UpdatedAt = a.pipeline.clock().Now()
	a.outcome.TransientRelaunches = a.state.TransientRelaunches
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("save transient relaunch %d: %w", a.state.TransientRelaunches, err)
	}
	return nil
}

// blockOnSpentRelaunchBudget ends a run the provider kept killing. It is the
// provider-side twin of the repair blockers, and it says the opposite thing about
// the change: every gate that ran was satisfied or never got to run, and nothing
// here found anything wrong with the work. What the reader has to look at is why
// the provider will not carry this run, which is the one question the harness
// cannot answer by asking again.
//
// recorded is what recording the dead attempt reported, and it is read rather
// than discarded. Its status is the run's, so a provider killed by a cancelled
// context is not filed as one that failed; and anything it says beyond the
// provider's own failure — a store that would not take the record, a change
// summary that could not be taken — travels with the blocker, because that is a
// second thing wrong rather than another way of saying this one.
func (a *activeRun) blockOnSpentRelaunchBudget(ctx context.Context, failure backend.TransientFailure, recorded error) error {
	limit := a.pipeline.Config.Execution.TransientRelaunchesBeforeBlocking
	blocked := fmt.Errorf("the provider ended this run without judging the work after %d of %d permitted relaunch(es): %s",
		a.state.TransientRelaunches, limit, failure.Detail)
	var reported phaseError
	if recorded != nil && !errors.As(recorded, &reported) {
		blocked = errors.Join(blocked, recorded)
	}
	cause := error(phaseError{status: failureStatus(ctx, recorded), cause: blocked})
	if err := a.block(renderRelaunchBlockerNotes(a.outcome, failure, a.state.CheckFailure, a.state.PathRefusal, limit)); err != nil {
		return errors.Join(cause, fmt.Errorf("record the spent relaunch budget as a blocker: %w", err))
	}
	return cause
}

// account is where this run's invocations are made. It is read off the run's own
// record rather than chosen again, which is what affinity means here: a repair
// attempt, a resumed attempt, and the review of the change are all the same run,
// and a run that moved between accounts mid-flight would leave its spend split
// across subscriptions with nothing saying so.
func (a *activeRun) account() config.AccountEndpoint {
	return a.pipeline.accountFor(a.state.AccountAlias)
}

// attemptDevelopment makes one developer invocation.
//
// It goes through the meter rather than straight at the backend, so that what
// this attempt spends is one line in the cost log however it ends. An attempt
// the provider refused, killed, or answered badly was charged for exactly as one
// that succeeded was, and the reissued invocation after it is charged again.
func (a *activeRun) attemptDevelopment(ctx context.Context, prompt, sessionID string) (backend.RunResult, error) {
	p := a.pipeline
	developer := p.developer()
	account := a.account()
	a.state.ProviderModel = developer.Model
	a.outcome.ProviderModel = developer.Model
	provider := spend.Metered{
		Provider:    p.Backend,
		Log:         p.Spend,
		Attribution: a.spendAttribution(domain.RoleDeveloper, a.developmentPhase()),
		Clock:       p.Clock,
	}
	return provider.Run(ctx, backend.RunRequest{
		RunID:            a.state.RunID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: a.worktree.Path,
		Prompt:           prompt,
		SessionID:        sessionID,
		Model:            developer.Model,
		LastSequence:     a.state.LastSequence,
		RedactValues:     p.RedactValues,
		EventSink:        a.sink,
		AccountAlias:     account.Alias,
		AccountConfigDir: account.Directory,
	})
}

// recordDevelopment records what a served developer attempt reported.
func (a *activeRun) recordDevelopment(ctx context.Context, providerResult backend.RunResult, err error) error {
	p := a.pipeline
	if err != nil {
		cause := fmt.Errorf("developer backend failed: %w", err)
		summaryCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		changeSummary, summaryErr := p.Worktrees.SummarizeChanges(summaryCtx, a.worktree)
		cancel()
		if summaryErr != nil {
			cause = errors.Join(cause, fmt.Errorf("summarize changes after developer backend failure: %w", summaryErr))
		} else {
			// The record is left to the terminal save this failure is on its way
			// to: the run is ending, and what it changed before it did is part of
			// what the record has to say about it.
			a.recordChanges(changeSummary)
		}
		return cause
	}
	a.state.ProviderSessionID = providerResult.SessionID
	a.state.ProviderResolvedModel = providerResult.ResolvedModel
	a.state.LastSequence = providerResult.LastEvent
	// Whatever stopped the previous attempt is spent: this one ran, and how it
	// ended is recorded below.
	a.state.ProviderStop = ""
	a.state.UpdatedAt = p.clock().Now()
	a.outcome.ProviderSessionID = providerResult.SessionID
	a.outcome.ProviderResolvedModel = providerResult.ResolvedModel
	// Anything the developer reported is collected out of what it said, so the
	// summary stays the account of the work and the report reaches the operator
	// instead of sitting in prose nothing surfaces.
	a.outcome.Summary = a.collectFromReply(domain.RoleDeveloper, providerResult.FinalText)
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("save developer outcome state: %w", err)
	}
	changeSummary, err := p.Worktrees.SummarizeChanges(ctx, a.worktree)
	if err != nil {
		return fmt.Errorf("summarize developer changes: %w", err)
	}
	a.recordChanges(changeSummary)
	// The account of the change is saved as soon as it is taken rather than with
	// whatever the run does next, because a process that dies here still leaves
	// somebody able to say what the run had changed.
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("save the account of what the developer changed: %w", err)
	}
	if !providerResult.IsError {
		return nil
	}
	// A stopped invocation is the harness's own doing, so it is never reported as
	// the developer having said anything. What it produced is already in the
	// worktree and its session is already recorded, so the run is left owed a
	// continuation instead of being failed.
	if reason, stopped := providerStopReason(providerResult.Process.Status); stopped {
		resumable, err := a.recordProviderStop(reason)
		if err != nil {
			return err
		}
		if resumable {
			return providerStop{reason: reason}
		}
		return phaseError{
			status: statusForProcess(providerResult.Process.Status),
			cause: fmt.Errorf("the harness stopped the developer: %s, and this run has nothing to continue from",
				describeProviderStop(reason)),
		}
	}
	return phaseError{
		status: statusForProcess(providerResult.Process.Status),
		cause:  fmt.Errorf("developer reported failure: %s", providerResult.DescribeFailure()),
	}
}

// refusedForUsageLimit reports an attempt the provider declined for want of
// capacity. A limit reported alongside work that still finished is evidence
// rather than a refusal — the provider re-reports its limits whenever they
// change, and almost every such report arrives on a run with capacity to spare —
// so only a report accompanying an attempt that produced no usable result
// pauses the run.
func refusedForUsageLimit(result backend.RunResult, err error) (backend.UsageLimit, bool) {
	if result.UsageLimit == nil || (err == nil && !result.IsError) {
		return backend.UsageLimit{}, false
	}
	return *result.UsageLimit, true
}

// refusedForServerOverload reports an attempt the provider could not serve
// because its own servers were transiently overloaded. An overload is reported
// as the terminal error that ended the invocation rather than beside a result,
// so in practice an attempt carrying one produced nothing to keep; the guard on
// a finished attempt is kept regardless, so no backend can park a run that
// already has its answer.
func refusedForServerOverload(result backend.RunResult, err error) (backend.ServerOverload, bool) {
	if result.ServerOverload == nil || (err == nil && !result.IsError) {
		return backend.ServerOverload{}, false
	}
	return *result.ServerOverload, true
}

// providerStopReason names the harness's reason for stopping a provider
// invocation, and reports whether it stopped one at all. Only these two process
// statuses are the harness acting on time; every other way a process ends is the
// provider's own, including a cancelled one, which is an operator's.
func providerStopReason(status execution.ProcessStatus) (string, bool) {
	switch status {
	case execution.ProcessStalled:
		return runstate.ProviderStopStalled, true
	case execution.ProcessTimedOut:
		return runstate.ProviderStopBudgetExhausted, true
	default:
		return "", false
	}
}

func describeProviderStop(reason string) string {
	switch reason {
	case runstate.ProviderStopStalled:
		return "it stopped emitting events"
	case runstate.ProviderStopBudgetExhausted:
		return "it was still working when its total budget ran out"
	default:
		return "it was stopped on time"
	}
}

// providerStop reports an invocation the harness stopped on time rather than one
// the provider ended. Like a usage-limit pause it is an error only so that it
// travels the path a stopped step already travels; it is deliberately not a
// failure, and the run it leaves behind is still in flight and still resumable.
type providerStop struct {
	reason string
}

func (e providerStop) Error() string {
	return "the harness stopped the provider: " + describeProviderStop(e.reason)
}

// recordProviderStop makes a stop durable and reports whether the run can be
// picked up again from it. A stop the run could not be resumed from is
// deliberately not recorded: a marker nothing can act on would leave the run in
// flight with no way back into it, which is worse than ending it honestly.
func (a *activeRun) recordProviderStop(reason string) (bool, error) {
	a.state.ProviderStop = reason
	if !stoppedProviderIsResumable(a.state) {
		a.state.ProviderStop = ""
		return false, nil
	}
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return false, fmt.Errorf("record the stopped provider invocation: %w", err)
	}
	return true, nil
}

// stoppedProviderIsResumable reports a run whose provider the harness stopped on
// time and which can be continued from durable state. It needs the worktree the
// stopped invocation was working in and the developer session a continuation
// resumes: a developer attempt continues in that session, and a review re-reads
// the change that session produced, which is also the session integration later
// demands as evidence of two independent invocations.
func stoppedProviderIsResumable(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.ProviderStop == "" {
		return false
	}
	switch state.Phase {
	case runstate.PhaseDeveloping, runstate.PhaseReviewing:
	default:
		return false
	}
	if state.ProviderSessionID == "" {
		return false
	}
	return state.WorktreePath != "" && state.Branch != "" && state.BaseCommit != ""
}

// releaseCheckInterval is how often a waiting run looks for the operator's
// release of its wait. It bounds how long "release this now" takes to take
// effect in a process that is already asleep, so it is short enough to read as
// immediate to the person who typed it; the cost of it is reading one small file
// that usually does not exist.
const releaseCheckInterval = 5 * time.Second

// pauseForUsageLimit records an exhausted limit and waits it out. The reset time
// and the run's remaining pause budget are both checked before anything is
// written, because a wait the harness will not take must stop the run rather
// than become a pause nobody can honor.
func (a *activeRun) pauseForUsageLimit(ctx context.Context, limit backend.UsageLimit) error {
	p := a.pipeline
	a.state.UsageLimitKind = limit.Kind
	a.outcome.UsageLimitKind = limit.Kind
	a.state.PauseCause = runstate.PauseUsageLimit
	a.outcome.PauseCause = runstate.PauseUsageLimit
	maximum := p.Config.Execution.UsageLimitMaxPause
	spent := a.state.UsageLimitPaused()
	now := p.clock().Now()
	wait := limit.ResetsAt.Sub(now)
	// What a reset time is worth is the provider contract's answer rather than
	// this run's: the two cases that are not simply a time in the future were
	// each learned at the cost of a run, and every provider inherits both rather
	// than rediscovering them. What the harness decides is what each one earns,
	// which is here, because a wait spends an account.
	reset := backend.ReadReset(limit.ResetsAt, now)
	// A limit with no reset time is unknown rather than unwaitable. The overage
	// allowance reports this way while the ordinary rolling window keeps
	// resetting on its usual schedule, so the work resumes -- the harness simply
	// has to ask again rather than be told when. It waits a configured interval
	// and reattempts, and because that wait spends the same budget as any other,
	// a provider that keeps refusing walks into the maximum instead of polling
	// forever.
	unknownReset := reset == backend.ResetUnknown
	if unknownReset {
		wait = p.Config.Execution.UsageLimitUnknownResetPause.Duration()
		limit.ResetsAt = p.clock().Now().Add(wait)
	}
	switch {
	case reset == backend.ResetMalformed:
		// A limit still refusing work while naming a reset that has already
		// passed is not describing a wait. Honoring it would mean reissuing
		// immediately into the same refusal, over and over, with nothing bounding
		// the attempts; a clock skew or a window the provider has not rolled yet
		// is a fact for a person, not something to spin on.
		return a.blockOnUsageLimit(fmt.Sprintf("it reports resetting at %s, which is not in the future",
			limit.ResetsAt.UTC().Format(time.RFC3339)))
	case wait > maximum.Duration()-spent:
		// The budget covers the run, not one pause. Checking each wait on its own
		// would let a provider that keeps refusing walk a run far past the
		// maximum an operator configured, one acceptable-looking wait at a time.
		reason := fmt.Sprintf("waiting until %s would take this run past the %s maximum pause",
			limit.ResetsAt.UTC().Format(time.RFC3339), maximum)
		if unknownReset {
			reason = fmt.Sprintf("it named no reset time, and waiting %s to ask again would take this run past the %s maximum pause",
				p.Config.Execution.UsageLimitUnknownResetPause, maximum)
		}
		if spent > 0 {
			reason += fmt.Sprintf(", and it has already committed %s to waiting", spent)
		}
		return a.blockOnUsageLimit(reason)
	}
	// The deadline becomes durable before the wait starts, so a process that dies
	// during the wait honors the same deadline on restart rather than retrying
	// straight back into the same limit. What the wait spends is recorded as it is
	// spent, one probe at a time, by the wait itself.
	resetsAt := limit.ResetsAt.UTC()
	a.state.UsageLimitResetsAt = &resetsAt
	a.state.UpdatedAt = p.clock().Now()
	// When this pause was recorded is what tells a release meant for it apart
	// from one meant for a pause this run has already served and reissued past.
	a.pausedAt = p.clock().Now()
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("record usage limit pause: %w", err)
	}
	return a.awaitRecordedUsageLimit(ctx)
}

// pauseForServerOverload records a transiently overloaded provider and waits it
// out. It is the usage-limit pause with a different clock and nothing else: an
// overload quotes no reset time and lifts in seconds rather than hours, so the
// run sets its own short deadline instead of honoring one, and everything after
// that is shared. The deadline is durable before the wait starts, the wait
// spends the same aggregate budget, and a provider that stays overloaded walks
// into the same configured maximum rather than reissuing forever.
//
// The provider CLI has already spent its own retries on this condition before it
// reports it, so the overload the harness sees is one that outlasted them.
func (a *activeRun) pauseForServerOverload(ctx context.Context, overload backend.ServerOverload) error {
	p := a.pipeline
	// An overload is the provider's own state rather than the account's, so
	// nothing names a limit here. The kind is cleared for that reason: a limit
	// left over from an earlier pause would describe this one as an exhaustion it
	// is not.
	a.state.UsageLimitKind = ""
	a.outcome.UsageLimitKind = ""
	a.state.PauseCause = runstate.PauseServerOverload
	a.outcome.PauseCause = runstate.PauseServerOverload
	maximum := p.Config.Execution.UsageLimitMaxPause
	spent := a.state.UsageLimitPaused()
	wait := p.Config.Execution.ServerOverloadPause.Duration()
	if wait > maximum.Duration()-spent {
		// The budget covers the run rather than one pause, exactly as it does for a
		// limit. A provider that keeps refusing therefore reaches the maximum an
		// operator configured instead of reissuing a short attempt forever.
		reason := fmt.Sprintf("waiting %s to ask again would take this run past the %s maximum pause",
			p.Config.Execution.ServerOverloadPause, maximum)
		if spent > 0 {
			reason += fmt.Sprintf(", and it has already committed %s to waiting", spent)
		}
		return a.blockOnUsageLimit(reason)
	}
	resetsAt := p.clock().Now().Add(wait).UTC()
	a.state.UsageLimitResetsAt = &resetsAt
	a.state.UpdatedAt = p.clock().Now()
	a.pausedAt = p.clock().Now()
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("record server overload pause: %w", err)
	}
	return a.awaitRecordedUsageLimit(ctx)
}

// awaitRecordedUsageLimit serves the deadline already in durable state. It is
// the whole of a resumed pause and the tail of a fresh one, so a restart during
// a wait takes exactly the path the interrupted process was on.
//
// The deadline is an upper bound on the wait rather than a gate on it. A run
// sleeps the shorter of the configured probe interval and the time left, and
// then reissues the attempt — the reissue *is* the probe. A reset time is a
// claim about the provider, and claims go stale in both directions: capacity
// gets bought mid-wait, and a rolling window can free room before the quoted
// edge. A probe into a window that is still closed costs one refused request and
// re-parks on whatever the provider now reports, which is the same price a wrong
// release costs. This is also what unifies the two cases: a limit that named no
// reset time already polled at this interval, and one that named a distant reset
// now polls at it too, under one discipline rather than two. A server overload
// falls out of the same rule without a case of its own: the deadline it sets is
// already shorter than the probe interval, so the shorter of the two is the
// whole of its wait.
func (a *activeRun) awaitRecordedUsageLimit(ctx context.Context) error {
	p := a.pipeline
	deadline := a.state.UsageLimitResetsAt.UTC()
	a.outcome.UsageLimitKind = a.state.UsageLimitKind
	a.outcome.PauseCause = a.state.PauseCause
	a.outcome.UsageLimitResetsAt = &deadline
	// A committed wait that no longer fits the bound — because the bound was
	// lowered, or because a differently configured process wrote it — is refused
	// here for the same reason it would have been refused on arrival. It is asked
	// before the release is, because the release says the deadline went stale and
	// says nothing about a run that has already spent everything it was allowed to
	// spend waiting.
	if a.state.UsageLimitPaused() > p.Config.Execution.UsageLimitMaxPause.Duration() {
		return a.blockOnUsageLimit(fmt.Sprintf("this run has committed %s to waiting, which is past the %s maximum pause",
			a.state.UsageLimitPaused(), p.Config.Execution.UsageLimitMaxPause))
	}
	// The operator may have released this wait while no process was serving it,
	// so it is asked before anything is decided about sleeping or exiting.
	released, err := a.releasedByOperator()
	if err != nil {
		return err
	}
	remaining := deadline.Sub(p.clock().Now())
	probe := min(remaining, p.Config.Execution.UsageLimitUnknownResetPause.Duration())
	switch {
	case released:
		// The next probe is now. Nothing else in the run reaches this: the
		// deadline binds every other path exactly as strictly as it did before.
	case remaining <= 0:
		// The recorded deadline has passed, so the wait this run committed to is
		// served and the refused attempt is owed its reissue. A deadline already
		// behind us is the normal way a paused run is resumed, and is a different
		// thing from a fresh report naming a reset in the past.
	case a.inProcessWait+probe > p.Config.Execution.UsageLimitInProcessPause.Duration():
		// This process has stayed open for this run as long as it is allowed to.
		// The bound counts every probe this process has already slept rather than
		// this one on its own, because a bound applied per probe would not bound
		// anything: an hour's worth of it would hold a process open for a whole
		// six-hour deadline, half an hour at a time. The deadline is durable, so
		// the run is left in flight for a later invocation to continue.
		return usageLimitPause{cause: a.state.PauseCause, kind: a.state.UsageLimitKind, resetsAt: deadline}
	default:
		// What this probe will spend is committed before it is spent, so a process
		// that dies mid-sleep cannot buy the run a fresh budget by forgetting it.
		// Only this probe is committed, not the whole span to the deadline: the
		// budget has to describe what was actually waited.
		wakesAt := p.clock().Now().Add(probe)
		a.state.UsageLimitPausedSeconds += int64(probe / time.Second)
		a.state.UpdatedAt = p.clock().Now()
		if err := p.Store.Save(a.state); err != nil {
			return fmt.Errorf("record usage limit pause: %w", err)
		}
		if err := a.waitForProbe(ctx, wakesAt); err != nil {
			return err
		}
		// A release that landed mid-probe leaves time on the clock that was never
		// waited. The budget must not be charged for it, and it is not time this
		// process stayed open for either.
		waited := probe
		if unspent := wakesAt.Sub(p.clock().Now()); unspent > 0 {
			waited -= unspent
			a.state.UsageLimitPausedSeconds -= int64(unspent / time.Second)
		}
		a.inProcessWait += waited
	}
	return a.clearUsageLimitPause()
}

// waitForProbe sleeps until the next probe is due, in slices short enough that
// an operator releasing the wait is acted on while this process is still asleep.
// Waking on a release is the whole reason the sleep is sliced rather than taken
// in one piece: a run that only looked at the release record between probes
// would make "release this now" mean "release this within half an hour".
func (a *activeRun) waitForProbe(ctx context.Context, wakesAt time.Time) error {
	p := a.pipeline
	for {
		remaining := wakesAt.Sub(p.clock().Now())
		if remaining <= 0 {
			return nil
		}
		if err := p.sleep(ctx, min(remaining, releaseCheckInterval)); err != nil {
			return err
		}
		released, err := a.releasedByOperator()
		if err != nil {
			return err
		}
		if released {
			return nil
		}
	}
}

// releasedByOperator reports whether the operator has said this run's recorded
// deadline no longer describes the provider. A record that cannot be read fails
// the wait rather than being treated as an absence: an operator who released a
// run and was silently ignored would be back where this verb exists to get them
// out of, and the run is preserved and resumable either way.
//
// A release older than the pause being served belongs to a pause this process
// has already served and reissued past. The operator reads the waiting run
// without holding its lease, so a release they typed against the pause they saw
// can land just after this run cleared it — and honoring it would release a
// pause the provider reported afterwards, which nobody has said anything about.
// A release recorded while no process was serving the run has no such pause to
// be older than, and is honored.
func (a *activeRun) releasedByOperator() (bool, error) {
	released, found, err := a.pipeline.Store.ReleasedWait(a.state.RunID)
	if err != nil {
		return false, fmt.Errorf("read whether the operator released this usage limit wait: %w", err)
	}
	if !found {
		return false, nil
	}
	return !released.ReleasedAt.Before(a.pausedAt), nil
}

// clearUsageLimitPause records that the run is no longer waiting, before the
// attempt it was waiting for is reissued. A deadline left behind would make a
// running attempt look like a pause to the next process that adopts the run.
//
// The operator's release is consumed here for the same reason and at the same
// moment: it has been acted on, and a record left behind would release whatever
// pause the reissued attempt earns next — a pause nobody has said anything about
// yet.
func (a *activeRun) clearUsageLimitPause() error {
	if a.state.UsageLimitResetsAt == nil {
		return nil
	}
	if err := a.pipeline.Store.ClearRelease(a.state.RunID); err != nil {
		return err
	}
	a.state.UsageLimitResetsAt = nil
	// The cause goes with the deadline it described. What refused the run is kept
	// on the outcome for the record, but a cause left in durable state beside no
	// deadline would describe a wait this run is no longer taking.
	a.state.PauseCause = ""
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("clear usage limit pause: %w", err)
	}
	a.outcome.UsageLimitResetsAt = nil
	return nil
}

// blockOnUsageLimit ends a run the provider refused and whose wait the harness
// will not take: an unusable reset time, or a wait beyond the configured
// maximum. Guessing a wait is the one thing that must not happen here, so what
// stopped the run is recorded on the work item and a person decides what to do
// about it. It serves both refusals, because what is undecidable about them is
// the same thing — how long to wait — whichever one asked.
func (a *activeRun) blockOnUsageLimit(reason string) error {
	cause := fmt.Errorf("this run was refused by %s and cannot wait for it: %s",
		runstate.DescribePause(a.state.PauseCause, a.state.UsageLimitKind), reason)
	if err := a.block(renderUsageLimitBlockerNotes(a.outcome, reason)); err != nil {
		return errors.Join(cause, fmt.Errorf("record the provider's refusal as a blocker: %w", err))
	}
	return cause
}

// usageLimitPause reports a run that stopped short of finishing because it is
// waiting out a provider that refused it for longer than this process will stay
// open. It is an error only so that it travels the path a stopped step already
// travels; it is deliberately not a failure, and the run it leaves behind is
// still in flight and still resumable.
type usageLimitPause struct {
	cause    string
	kind     string
	resetsAt time.Time
}

func (e usageLimitPause) Error() string {
	return fmt.Sprintf("paused until %s for %s",
		e.resetsAt.Format(time.RFC3339), runstate.DescribePause(e.cause, e.kind))
}

// pausedForUsageLimit reports a run waiting out an exhausted provider usage
// limit. The recorded deadline is what makes it a pause rather than an
// interruption, and the worktree is what makes it resumable: without the change
// every attempt shares there is nothing to continue. A developer session is
// deliberately not required, because a limit can refuse the very first attempt
// before the provider ever established one, and neither is a spent repair
// attempt, because a run can pause before any failure was returned to it.
//
// Both provider-invoking phases can pause. A developer attempt resumes by being
// reissued; a review resumes by re-verifying and reviewing again, which is what
// an interrupted review already does.
func pausedForUsageLimit(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.UsageLimitResetsAt == nil {
		return false
	}
	switch state.Phase {
	case runstate.PhaseDeveloping, runstate.PhaseReviewing:
	default:
		return false
	}
	return state.WorktreePath != "" && state.Branch != "" && state.BaseCommit != ""
}

// pausingDirectives asks what the operator has directed that stops this work. A
// failure to read is a failure to run: a harness that cannot find out what has
// been directed is indistinguishable from one that has been directed nothing,
// and proceeding on that reading is the whole failure directives exist to
// prevent.
func (p Pipeline) pausingDirectives(workItemID string) ([]directive.Directive, error) {
	pausing, err := p.Directives.Pausing(workItemID)
	if err != nil {
		return nil, fmt.Errorf("read what the operator has directed about %s: %w", workItemID, err)
	}
	return pausing, nil
}

// pauseWorkItem reports work the harness declined to start or resume because a
// directive is unresolved. There is no run behind it: nothing was claimed, no
// worktree exists, and a run already in flight for the item was left exactly as
// it was. It is a pause rather than a failure because settling the directive is
// all that stands between here and the work proceeding.
func pauseWorkItem(workItemID string, paused directive.Directive) Outcome {
	return Outcome{
		WorkItemID:        workItemID,
		Paused:            true,
		PausedByDirective: &paused,
	}
}

// holdForDirective stops a run in flight for an unresolved directive, and does
// nothing at all when there is none, which is the ordinary case. What it returns
// on the pausing path is a directivePause, which travels the path a stopped step
// already travels.
//
// This is what makes a directive reach work that is already under way. The check
// before a run starts covers work that has not begun; without this one, a
// directive recorded while a developer was working would be enforced against
// every item except the one it was about.
func (a *activeRun) holdForDirective() error {
	pausing, err := a.pipeline.pausingDirectives(a.state.WorkItemID)
	if err != nil {
		return err
	}
	if len(pausing) == 0 {
		return nil
	}
	return a.recordDirectivePause(pausing[0])
}

// recordDirectivePause makes the pause durable and then reports it. The record
// comes first for the same reason a usage-limit deadline is written before the
// wait begins: a process that dies here must leave a run that can be told from
// an interrupted one and picked up again, rather than one nothing will resume.
func (a *activeRun) recordDirectivePause(paused directive.Directive) error {
	// The developer's attempt is behind this run and the gate is what it stopped
	// short of, so the recorded phase says so. Left at developing, a resumed run
	// would be handed a second developer attempt it does not need.
	if a.state.Phase == runstate.PhaseDeveloping {
		a.state.Phase = runstate.PhaseChecking
	}
	a.state.DirectivePause = &runstate.DirectivePause{
		DirectiveID: paused.ID,
		Kind:        string(paused.Kind),
		Unresolved:  paused.Unresolved,
	}
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the directive that paused this run: %w", err)
	}
	return directivePause{directive: paused}
}

// clearDirectivePause records that the run is no longer held up, before it
// carries on. A pause left behind would make a running attempt look like a
// waiting one to the next process that adopts the run.
func (a *activeRun) clearDirectivePause() error {
	if a.state.DirectivePause == nil {
		return nil
	}
	a.state.DirectivePause = nil
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("clear the directive pause: %w", err)
	}
	return nil
}

// directivePause reports a run that stopped short of finishing because an
// unresolved user directive affects its work item. Like a usage-limit pause it
// is an error only so that it travels the path a stopped step already travels;
// it is deliberately not a failure, and the run it leaves behind is still in
// flight, still claimed, and still resumable.
type directivePause struct {
	directive directive.Directive
}

func (e directivePause) Error() string {
	return "paused for an unresolved directive: " + e.directive.Summary()
}

// pausedForDirective reports a run held up by an unresolved user directive. The
// recorded pause is what makes it a pause rather than an interruption, and the
// worktree is what makes it resumable: the change every attempt shares is what
// the run comes back to.
func pausedForDirective(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.DirectivePause == nil {
		return false
	}
	return state.WorktreePath != "" && state.Branch != "" && state.BaseCommit != ""
}

// pauseWorkItemForDependencies reports work the harness declined to start or
// resume because the item waits on work that is not finished. There is no run
// behind it: nothing was claimed, no worktree exists, and a run already in flight
// for the item was left exactly as it was. It is a pause rather than a failure
// because closing what it waits on is all that stands between here and the work
// proceeding.
func pauseWorkItemForDependencies(workItemID string, blockers []string) Outcome {
	paused := runstate.DependencyPause{Blockers: blockers}
	return Outcome{
		WorkItemID:         workItemID,
		Paused:             true,
		PausedByDependency: &paused,
	}
}

// holdForDependency stops a run in flight for work its item has since been made
// to wait on, and does nothing at all when there is none, which is the ordinary
// case. What it returns on the pausing path is a dependencyPause, which travels
// the path a stopped step already travels.
//
// The item is re-read from the tracker rather than taken from the run, and that
// is the whole of what this adds. The item a run carries is what selection read
// when the run started; a dependency link applied since then is precisely the
// gate that would otherwise stop nothing, and a run answering from its own copy
// would be the run this exists to end.
//
// A failure to read is a failure to proceed, exactly as an unreadable directive
// is: a harness that cannot find out what an item waits on is indistinguishable
// from one whose item waits on nothing, and spending another attempt on that
// reading is the whole failure this exists to prevent.
func (a *activeRun) holdForDependency(ctx context.Context) error {
	item, err := a.pipeline.Tracker.Show(ctx, a.state.WorkItemID)
	if err != nil {
		return fmt.Errorf("read what %s waits on: %w", a.state.WorkItemID, err)
	}
	blockers := blockingDependencies(item)
	if len(blockers) == 0 {
		return nil
	}
	return a.recordDependencyPause(blockers)
}

// recordDependencyPause makes the pause durable and then reports it, for the
// reason recordDirectivePause does: a process that dies here must leave a run
// that can be told from an interrupted one and picked up again.
func (a *activeRun) recordDependencyPause(blockers []string) error {
	// The developer's attempt is behind this run and the gate is what it stopped
	// short of, so the recorded phase says so. Left at developing, a resumed run
	// would be handed a second developer attempt it does not need.
	if a.state.Phase == runstate.PhaseDeveloping {
		a.state.Phase = runstate.PhaseChecking
	}
	paused := runstate.DependencyPause{Blockers: blockers}
	a.state.DependencyPause = &paused
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the unfinished work that paused this run: %w", err)
	}
	return dependencyPause{blockers: blockers}
}

// clearDependencyPause records that the run is no longer waiting, before it
// carries on. A pause left behind would make a running attempt look like a
// waiting one to the next process that adopts the run.
func (a *activeRun) clearDependencyPause() error {
	if a.state.DependencyPause == nil {
		return nil
	}
	a.state.DependencyPause = nil
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("clear the dependency pause: %w", err)
	}
	return nil
}

// dependencyPause reports a run that stopped short of finishing because its work
// item waits on work that is not finished. Like the directive pause it is an
// error only so that it travels the path a stopped step already travels; it is
// deliberately not a failure, and the run it leaves behind is still in flight,
// still claimed, and still resumable.
type dependencyPause struct {
	blockers []string
}

func (e dependencyPause) Error() string {
	return "paused for unfinished work this item waits on: " + strings.Join(e.blockers, ", ")
}

// pausedForDependency reports a run held up by work its item waits on. The
// recorded pause is what makes it a pause rather than an interruption, and the
// worktree is what makes it resumable: the change every attempt shares is what
// the run comes back to.
func pausedForDependency(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.DependencyPause == nil {
		return false
	}
	return state.WorktreePath != "" && state.Branch != "" && state.BaseCommit != ""
}

// operatorHoldProbe is how often a held run looks for the operator lifting the
// hold. It bounds how long `yoyo resume` takes to be acted on by a process that
// is already parked, so it is short enough to read as immediate to the person
// who typed it; what it costs is reading one small file that usually is not
// there, and never a request to the provider. It is the interval a released wait
// is noticed on, for the same reason and at the same price.
const operatorHoldProbe = releaseCheckInterval

// operatorHold asks whether the operator has paused all harness activity. A
// failure to read is a failure to proceed, for the reason a directive that
// cannot be read is: a harness that cannot find out whether it has been paused
// is indistinguishable from one that has not been, and spending on that reading
// is the whole failure this exists to prevent.
func (p Pipeline) operatorHold() (runstate.OperatorHold, bool, error) {
	hold, held, err := p.Holds.Held()
	if err != nil {
		return runstate.OperatorHold{}, false, fmt.Errorf("read whether the operator has paused harness activity: %w", err)
	}
	return hold, held, nil
}

// holdWorkItem reports work the harness declined to start or resume because the
// operator is holding all activity. It is a pause rather than a failure because
// lifting the hold is all that stands between here and the work proceeding.
//
// It names the run this item already has, when it has one. That run was left
// exactly as it was — nothing here claims, adopts, or touches it — but "left
// alone" and "never started" are opposite facts about an operator's worktree and
// their claimed item, and a report that could not tell them apart would say
// nothing was started for a run that is parked with hours of work in it.
//
// The run is found by reading rather than by adopting, exactly as releasing a
// wait finds one: a run another process is serving must keep serving it, and
// taking its lease to describe it would be the harness stopping the very run the
// pause exists to preserve. A lookup that fails travels back with the pause
// rather than replacing it — the hold is in force either way and nothing will be
// spent — so what is lost is the description and not the answer.
func (p Pipeline) holdWorkItem(workItemID string, hold runstate.OperatorHold) (Outcome, error) {
	outcome := Outcome{
		WorkItemID:       workItemID,
		Paused:           true,
		PauseCause:       runstate.PauseOperatorHold,
		PausedByOperator: &hold,
	}
	inFlight, err := p.Store.Incomplete()
	if err != nil {
		return outcome, fmt.Errorf("find what is in flight for %s while activity is paused: %w", workItemID, err)
	}
	for _, existing := range inFlight {
		if existing.WorkItemID != workItemID {
			continue
		}
		outcome.RunID = existing.RunID
		outcome.Status = existing.Status
		outcome.Phase = existing.Phase
		outcome.Branch = existing.Branch
		outcome.WorktreePath = existing.WorktreePath
		outcome.BaseCommit = existing.BaseCommit
		outcome.ProviderSessionID = existing.ProviderSessionID
		break
	}
	return outcome, nil
}

// holdIntake reports work this pipeline declined to start because the operator
// is holding intake, and reports nothing at all in the ordinary case. It is the
// whole of what holding intake means: nothing is claimed, nothing is developed,
// and the item stays exactly where it was, so lifting the hold is all that
// stands between here and the work starting.
//
// A hold never stops the operator. They are the one who placed it, and an item
// they then name is them deciding that this piece of work is the exception —
// which is the distinction between holding what the harness chooses and pausing
// everything, and the reason both switches exist.
func (p Pipeline) holdIntake(workItemID string) (Outcome, bool, error) {
	if !p.Selection.SelectedByHarness() {
		return Outcome{}, false, nil
	}
	hold, held, err := p.Intake.Held()
	if err != nil {
		return Outcome{}, false, fmt.Errorf("read whether the operator has held intake: %w", err)
	}
	if !held {
		return Outcome{}, false, nil
	}
	return Outcome{
		WorkItemID:     workItemID,
		Paused:         true,
		PausedByIntake: &hold,
	}, true, nil
}

// stopRequested reports the operator asking this run to stop, and nothing at all
// in the ordinary case. It is asked at the boundaries where the operator's hold
// is asked, and for the same reason: those are the points at which a run is about
// to spend, so they are the points at which stopping it costs the least.
//
// A record that cannot be read stops nothing and fails nothing. It is deliberately
// the opposite of how an unreadable hold is treated, because the two protect
// against opposite mistakes: an unreadable hold might be a pause being spent
// through, while an unreadable stop request would be a run killed on the strength
// of a file nobody could parse. The failure travels back with the run instead.
func (a *activeRun) stopRequested() error {
	request, requested, err := a.pipeline.Store.StopRequested(a.state.RunID)
	if err != nil {
		return fmt.Errorf("read whether the operator asked this run to stop: %w", err)
	}
	if !requested {
		return nil
	}
	return operatorStop{request: request}
}

// operatorStop reports a run the operator asked to stop. Unlike the pauses it is
// a real ending: the run is made terminal and cancelled, its artifacts are left
// exactly where they are, and settling what they amount to is reconciliation's
// job rather than this one's — which is what stopping has always done here, and
// is why a stop aimed at another process's run leaves the same thing behind as a
// stop aimed at this one's.
type operatorStop struct {
	request runstate.StopRequest
}

func (e operatorStop) Error() string {
	stopped := "the operator stopped this run at " + e.request.RequestedAt.Format(time.RFC3339)
	if strings.TrimSpace(e.request.Reason) != "" {
		stopped += ": " + strings.TrimSpace(e.request.Reason)
	}
	return stopped
}

// holdForOperator parks a run at a provider-call boundary for as long as the
// operator holds harness activity, and does nothing at all when they do not,
// which is the ordinary case. It is the whole of what "pause" means to a run:
// every provider invocation a run makes passes through here first, so a hold
// placed while a developer was working reaches that run at its next attempt
// rather than only reaching the runs that had not started.
//
// What it deliberately does not do is interrupt an invocation already under way.
// A generation that is streaming has already been paid for, and stopping it
// mid-flight would throw that away and leave the run needing the same work
// again — which is the cost that makes killing processes the wrong verb in the
// first place.
func (a *activeRun) holdForOperator(ctx context.Context) error {
	p := a.pipeline
	for {
		hold, held, err := p.operatorHold()
		if err != nil {
			return err
		}
		if !held {
			// Clearing is what keeps a run that is working again from looking parked
			// to the next process that reads it, and it is where the hold's share of
			// this run's elapsed time is accounted.
			return a.clearOperatorHold()
		}
		if err := a.recordOperatorHold(hold); err != nil {
			return err
		}
		// This process stays open for a held run exactly as long as it stays open
		// for a refused one, and the bound counts every probe it has already slept
		// rather than this one alone. The hold is durable, so what the bound costs
		// is a later invocation picking the run up rather than anything being lost.
		if a.inProcessWait+operatorHoldProbe > p.Config.Execution.UsageLimitInProcessPause.Duration() {
			return operatorHoldPause{hold: hold}
		}
		if err := p.sleep(ctx, operatorHoldProbe); err != nil {
			return err
		}
		a.inProcessWait += operatorHoldProbe
	}
}

// recordOperatorHold makes the park durable, once, before any waiting happens.
// The record comes first for the reason a usage-limit deadline is written before
// its wait: a process that dies while the harness is held must leave a run that
// says so and can be picked up, rather than one that looks interrupted.
//
// Only the first park of a stretch is written. When it began is what says how
// long the harness has been quiet, and restamping it every probe would make a
// hold that has been in force since yesterday describe itself as five seconds
// old.
func (a *activeRun) recordOperatorHold(hold runstate.OperatorHold) error {
	a.outcome.PauseCause = runstate.PauseOperatorHold
	a.outcome.PausedByOperator = &hold
	if a.state.OperatorHeldSince != nil {
		return nil
	}
	since := a.pipeline.clock().Now().UTC()
	a.state.OperatorHeldSince = &since
	a.state.PauseCause = runstate.PauseOperatorHold
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the operator hold this run parked on: %w", err)
	}
	return nil
}

// clearOperatorHold records that the run is spending again, and adds what the
// hold cost it to the run's account of held time. The span is measured from when
// the park was recorded rather than summed probe by probe, because a hold that
// outlived the process serving it held the run for the whole of it: the time a
// run spent doing nothing is the ledger's question, and "no process was awake
// for part of it" is not an answer to that question.
func (a *activeRun) clearOperatorHold() error {
	if a.state.OperatorHeldSince == nil {
		return nil
	}
	if held := a.pipeline.clock().Now().Sub(*a.state.OperatorHeldSince); held > 0 {
		a.state.OperatorHeldSeconds += int64(held / time.Second)
	}
	a.state.OperatorHeldSince = nil
	// The cause goes with the park it described. Left behind, it would say a run
	// that is working is waiting on somebody.
	a.state.PauseCause = ""
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("clear the operator hold: %w", err)
	}
	// Only a cause this park set is cleared from the outcome. A provider refusal
	// this run waited out earlier is kept there for the record, exactly as the
	// limit it named is, and a hold arriving afterwards must not erase it.
	if a.outcome.PauseCause == runstate.PauseOperatorHold {
		a.outcome.PauseCause = ""
	}
	a.outcome.PausedByOperator = nil
	return nil
}

// operatorHoldPause reports a run parked at a provider-call boundary because the
// operator holds harness activity, for longer than this process will stay open.
// Like the other pauses it is an error only so that it travels the path a
// stopped step already travels; it is deliberately not a failure, and the run it
// leaves behind is still in flight, still claimed, and still resumable.
type operatorHoldPause struct {
	hold runstate.OperatorHold
}

func (e operatorHoldPause) Error() string {
	return "parked for an operator hold on all harness activity, placed at " + e.hold.HeldAt.Format(time.RFC3339)
}

// pausedForOperatorHold reports a run parked on the operator's hold. The
// recorded park is what makes it a pause rather than an interruption, and the
// worktree is what makes it resumable: the change every attempt shares is what
// the run comes back to.
func pausedForOperatorHold(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.OperatorHeldSince == nil {
		return false
	}
	return state.WorktreePath != "" && state.Branch != "" && state.BaseCommit != ""
}

// sleep waits out a pause, cut short by a cancelled context so a shutdown is
// never held up by a deadline hours away.
func (p Pipeline) sleep(ctx context.Context, duration time.Duration) error {
	if p.Sleep != nil {
		return p.Sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// verify puts the change through the two deterministic gates in front of the
// reviewer: what it was allowed to touch, and then the configured checks over
// what it did. Every attempt runs both again: no attempt inherits an earlier
// attempt's verification, and review and integration are only reachable through
// a change that passed both on the change being judged.
func (a *activeRun) verify(ctx context.Context) error {
	p := a.pipeline
	a.state.Phase = runstate.PhaseChecking
	// Scope is settled before the suite runs, because it costs a listing of names
	// against a handful of prefixes and the suite costs whatever the project's
	// suite costs. A change that is not allowed to stand does not get a check
	// suite spent on it first.
	if err := a.gateProtectedPaths(ctx); err != nil {
		return err
	}
	checkResults, lastSequence, err := p.Checks.Run(ctx, a.state.RunID, a.worktree.Path, p.Config.Checks, a.state.LastSequence, a.sink)
	a.outcome.Checks = checkResults
	a.state.LastSequence = lastSequence
	if err != nil {
		return fmt.Errorf("verification infrastructure failed: %w", err)
	}
	for _, check := range checkResults {
		if check.Passed {
			continue
		}
		// Only a check that actually ran and failed describes something the
		// developer can repair. A cancelled or timed-out one says the run itself
		// was stopped, so it ends the run rather than spending an attempt on a
		// developer that would be stopped the same way.
		var cause error = fmt.Errorf("verification failed: %s exited with %d", check.Command, check.Process.ExitCode)
		switch check.Process.Status {
		case execution.ProcessFailed:
			cause = checkFailure{result: check}
		case execution.ProcessTimedOut:
			// A check stopped on time says nothing about the change: the work
			// may have been passing the whole way, as it was when this bound
			// was flat and a contended suite grew past it. So the failure names
			// both numbers and the setting that moves the ceiling, rather than
			// reporting the kill as an exit code nobody chose.
			cause = fmt.Errorf(
				"verification timed out: %s ran for %s and was stopped at its %s execution.check_timeout budget; raise that budget or lower execution.max_concurrent_developers, because concurrent runs multiply the wall clock of every suite",
				check.Command, check.Elapsed().Round(time.Second), check.Timeout)
		}
		return phaseError{status: statusForProcess(check.Process.Status), cause: cause}
	}
	// The change in the worktree now passes, so any failure an earlier attempt
	// was handed is no longer this run's outstanding repair input.
	a.state.CheckFailure = nil
	return nil
}

// gateProtectedPaths refuses a change that touched an upstream artifact this
// work item never admitted into its scope. The paths are the harness's own
// configuration and the artifact homes the roles above the developer own, and
// the grants are read from the work item's own text — which is the whole point
// of doing it this way. An exception written into an item was decided before the
// run started and reviewed with the rest of it; an exception the run discovers
// is the developer deciding what its work was allowed to redefine.
//
// It answers with a refusal rather than a verdict. Nothing here says the change
// is wrong, only that part of it is not this run's to make, so it goes back to
// the same developer inside the same repair loop as any other failure that
// stands between a change and its reviewer.
func (a *activeRun) gateProtectedPaths(ctx context.Context) error {
	changed, err := a.pipeline.Worktrees.ChangedPaths(ctx, a.worktree)
	if err != nil {
		return fmt.Errorf("list the paths this change touches: %w", err)
	}
	protected := protectedpath.Protect(a.pipeline.Config)
	granted := protectedpath.Grants(grantEvidence(a.item)...)
	refused := protected.Refused(changed, granted)
	if len(refused) == 0 {
		// The change in the worktree is within its scope now, so a refusal an
		// earlier attempt was handed no longer describes it.
		a.state.PathRefusal = nil
		return nil
	}
	return phaseError{status: runstate.StatusFailed, cause: pathRefusal{
		refusal: boundedPathRefusal(refused, granted),
		set:     protected,
	}}
}

// boundedPathRefusal is what a refusal is allowed to carry into durable state
// and into the developer's next attempt. A change that rewrote a whole artifact
// home is refused on all of it and told about the first of it, with the rest
// counted rather than dropped silently: the developer has to take every one of
// them back out, and a listing that stopped without saying so would read as the
// whole of what the gate caught.
func boundedPathRefusal(refused, granted []string) runstate.PathRefusal {
	recorded := runstate.PathRefusal{Paths: refused, Grants: granted}
	if len(refused) > runstate.MaxRefusedPaths {
		recorded.Paths = refused[:runstate.MaxRefusedPaths]
		recorded.Omitted = len(refused) - runstate.MaxRefusedPaths
	}
	return recorded
}

// pathRefusal is a change refused for the paths it touched. Like a failing check
// it is its own error type because it is repair input for the developer that
// produced the change, which has to be told apart from the gate failing to run
// at all — an unreadable worktree is not something a developer can fix by
// editing its change.
type pathRefusal struct {
	refusal runstate.PathRefusal
	set     protectedpath.Set
}

func (e pathRefusal) Error() string {
	return fmt.Sprintf("change touches protected paths this work item does not grant: %s",
		strings.Join(e.refusal.Paths, ", "))
}

// checkFailure is a deterministic check that ran and failed. It is its own error
// type because a failure the developer can be asked to repair has to be told
// apart from verification infrastructure that could not run at all.
type checkFailure struct {
	result checks.Result
}

func (e checkFailure) Error() string {
	return fmt.Sprintf("verification failed: %s exited with %d", e.result.Command, e.result.Process.ExitCode)
}

// integrate promotes an approved change and records the promotion.
//
// Reaching this phase is what puts a run in the promotion queue for its target
// branch. Everything under the lease reads where that branch is and then moves
// it — the drift check, the fast-forward, and the merge the forge is asked for
// against the same commit — so a second promotion interleaved with it is a race
// rather than a second promotion. The lease is the harness's own, taken in this
// process: no agent asks for it, and none can perform what it admits.
//
// It is released as soon as the promotion settles, which is well before the run
// ends. Cleanup is this run's own artifacts rather than the target branch, and
// holding the queue through it would make every other run wait on work that
// cannot affect them.
func (a *activeRun) integrate(ctx context.Context) error {
	p := a.pipeline
	a.state.Phase = runstate.PhaseIntegrating
	a.state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("save integrating run state: %w", err)
	}
	lease, err := p.Store.LeasePromotion(ctx, a.worktree.TargetBranch)
	if err != nil {
		return fmt.Errorf("wait for this run's turn to promote: %w", err)
	}
	// Releasing is this process letting the next promotion in, and the operating
	// system does it anyway when the process exits. A close that failed therefore
	// says nothing about the promotion below, which either happened or did not.
	defer func() { _ = lease.Release() }()
	// Where the remote target stands is settled before the promotion rather than
	// only after it. A promotion made onto a target the remote has moved away from
	// can be neither published nor taken back, so finding out afterwards closes the
	// item as integrated against a divergence nothing owns; finding out here leaves
	// a change that can still be replayed onto wherever the target went.
	if err := a.settleRemoteTarget(ctx); err != nil {
		return err
	}
	integration, err := p.Worktrees.Integrate(ctx, a.worktree, integrationMessage(a.item, a.outcome))
	if err != nil {
		// A refused promotion may already have committed what the developer left,
		// and that commit is what this worktree's HEAD is now. It is recorded
		// before the failure is reported, for the reason publishing records its
		// own: a retry, a resumed run, and a reconciler all have to be able to tell
		// it from a commit an agent made for itself.
		if integration.SourceCommit != "" && integration.SourceCommit != a.state.HarnessCommit {
			a.recordHarnessCommit(integration.SourceCommit)
			a.state.UpdatedAt = p.clock().Now()
			if saveErr := p.Store.Save(a.state); saveErr != nil {
				return errors.Join(fmt.Errorf("integrate approved change: %w", err),
					fmt.Errorf("record the commit the refused promotion made: %w", saveErr))
			}
		}
		return fmt.Errorf("integrate approved change: %w", err)
	}
	a.outcome.Integration = &integration
	a.state.Integration = &runstate.Integration{
		TargetBranch:         integration.TargetBranch,
		SourceCommit:         integration.SourceCommit,
		TargetCommit:         integration.TargetCommit,
		PreviousTargetCommit: integration.PreviousTargetCommit,
	}
	// The approving verdict authorized this promotion, so it also authorized the
	// merge of the pull request that carried it. Publishing does not fail the run
	// over an unfinished publication — the local target branch has already moved
	// and it is the authoritative one — but it does fail it over a remote target
	// that diverged in the window this check-then-act leaves open, because the
	// alternative is closing the item as integrated against a divergence no
	// fast-forward reconciles. The promotion stands either way; the blocker says so.
	if err := a.publishIntegration(ctx); err != nil {
		return err
	}
	a.state.Phase = runstate.PhaseCompleting
	a.state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(a.state); err != nil {
		return fmt.Errorf("save integrated run state: %w", err)
	}
	return nil
}

// finish records the outcome, closes an integrated item whose publication is
// settled, makes the run durably terminal, and only then removes what the run
// created.
func (a *activeRun) finish(ctx context.Context) (Outcome, error) {
	p := a.pipeline
	// The tracker is updated only once the work is durably where it belongs:
	// after integration when it is automatic, and after passing checks when a
	// human still owns the promotion.
	if _, err := p.Tracker.RecordOutcome(ctx, a.state.WorkItemID, renderOutcomeNotes(a.outcome)); err != nil {
		return a.fail(fmt.Errorf("record successful run outcome: %w", err), runstate.StatusFailed)
	}
	// An item closes as integrated once the promotion is where it is going to
	// stay. A merge the forge only queued is not that yet: it lands minutes
	// later, or the forge drops it because something the base branch requires
	// went unmet, and closing here would record integrated for a publication
	// that may never happen. So the closure waits for the forge's answer, which
	// is the same step that already settles the queue — and until it arrives the
	// item stays claimed with the queued merge named on it, rather than closed
	// against a merge nobody has confirmed.
	if a.outcome.Integration != nil && !a.mergeQueued() {
		if _, err := p.Tracker.Complete(ctx, a.state.WorkItemID, completionReason(a.outcome)); err != nil {
			return a.fail(fmt.Errorf("close integrated work item: %w", err), runstate.StatusFailed)
		}
		a.outcome.WorkItemClosed = true
	}
	// The item is priced when the run that spent it ends, whether or not the
	// closure waits: a queued merge defers the closure to a later sweep, and that
	// sweep does not re-read what this run cost.
	a.recordPrice()

	// The run becomes durably terminal before anything is destroyed. Cleanup is
	// the only remaining step, it removes evidence, and it must never be able to
	// leave a closed item behind a non-terminal run: an interrupted process at
	// this boundary leaves a succeeded run in the cleaning_up phase with
	// worktree_removed still false, which is a resumable instruction rather than
	// a lost run. A reconciler re-runs cleanup, which refuses anything that is
	// not the recorded, registered, already-integrated worktree.
	completedAt := p.clock().Now()
	// A recorded stop or park is an instruction to continue later, and this run
	// has finished. Leaving either would promise a continuation of a completed run.
	a.state.ProviderStop = ""
	a.state.OperatorHeldSince = nil
	a.state.Status = runstate.StatusSucceeded
	a.state.Phase = runstate.PhaseCleaningUp
	a.state.UpdatedAt = completedAt
	a.state.CompletedAt = &completedAt
	if a.outcome.Integration == nil {
		a.state.Phase = runstate.PhaseComplete
	}
	if err := p.Store.Save(a.state); err != nil {
		return a.fail(fmt.Errorf("save successful run state: %w", err), runstate.StatusFailed)
	}
	a.outcome.Status = runstate.StatusSucceeded
	a.outcome.Phase = a.state.Phase
	if a.outcome.Integration == nil {
		return a.outcome, nil
	}

	// Only artifacts proven to be integrated are removed, and only after the
	// tracker agrees the item is done and that fact is durable. Cleanup reports
	// each artifact separately, so a partial removal is recorded as what it is
	// rather than collapsed into a single failed flag.
	cleanup, cleanupErr := p.Worktrees.CleanupIntegrated(ctx, gitworktree.CleanupRequest{
		Worktree:     a.worktree,
		TargetBranch: a.outcome.Integration.TargetBranch,
		SourceCommit: a.outcome.Integration.SourceCommit,
	})
	a.outcome.WorktreeRemoved = cleanup.WorktreeRemoved
	a.outcome.BranchRemoved = cleanup.BranchRemoved
	a.state.WorktreeRemoved = cleanup.WorktreeRemoved
	a.state.BranchRemoved = cleanup.BranchRemoved
	if cleanupErr != nil {
		// A failure here leaves the run succeeded and reports the outstanding
		// cleanup: the change is integrated and the item is closed. Whatever was
		// removed before the failure is still recorded as removed.
		return p.reportOutstandingCleanup(a.state, a.outcome, fmt.Errorf("clean up integrated run artifacts: %w", cleanupErr))
	}
	// Cleanup finished, so the run is complete whatever happens to the record of
	// it. The reported phase follows that fact rather than the write below.
	a.state.Phase = runstate.PhaseComplete
	a.state.UpdatedAt = p.clock().Now()
	a.outcome.Phase = a.state.Phase
	if err := p.Store.Save(a.state); err != nil {
		// An interrupted write that recovers must leave a clean terminal record,
		// not a cleanup warning about artifacts that are already gone.
		a.state.UpdatedAt = p.clock().Now()
		if retryErr := p.Store.Save(a.state); retryErr != nil {
			return p.reportCompletionRecordingFailure(a.state, a.outcome,
				fmt.Errorf("save completed run state after cleanup: %w", errors.Join(err, retryErr)))
		}
	}
	return a.outcome, nil
}

// stop turns a stopped step into the outcome the run reports. Two things it can
// be handed are deliberately not failures, because both leave the run in flight
// with its worktree, branch, claimed work item, and developer session preserved:
// a usage-limit pause, which a later invocation resumes once the recorded
// deadline passes, and a provider the harness stopped on time, which a later
// invocation resumes straight away.
func (a *activeRun) stop(ctx context.Context, cause error) (Outcome, error) {
	var paused usageLimitPause
	if errors.As(cause, &paused) {
		return a.pause(paused)
	}
	var stopped providerStop
	if errors.As(cause, &stopped) {
		return a.pauseForProviderStop(stopped)
	}
	var directed directivePause
	if errors.As(cause, &directed) {
		return a.pauseForDirective(directed)
	}
	var waiting dependencyPause
	if errors.As(cause, &waiting) {
		return a.pauseForDependency(waiting)
	}
	var operatorHeld operatorHoldPause
	if errors.As(cause, &operatorHeld) {
		return a.pauseForOperatorHold(operatorHeld)
	}
	// A stop the operator asked for is the one ending here that is not a pause and
	// not a failure of the work. It is recorded as cancelled, which is exactly
	// what a run this process cancelled itself is recorded as, so what it leaves
	// behind reads the same to reconciliation whichever way the stop arrived.
	var stoppedByOperator operatorStop
	if errors.As(cause, &stoppedByOperator) {
		return a.fail(cause, runstate.StatusCancelled)
	}
	return a.fail(cause, failureStatus(ctx, cause))
}

// pause reports a run left waiting. Nothing is cleaned up and nothing is made
// terminal; the deadline written before the wait began is what a later
// invocation resumes from, so the run survives this process exiting.
func (a *activeRun) pause(paused usageLimitPause) (Outcome, error) {
	a.outcome.Status = runstate.StatusRunning
	a.outcome.Phase = a.state.Phase
	a.outcome.Paused = true
	a.outcome.UsageLimitKind = paused.kind
	a.outcome.PauseCause = paused.cause
	resetsAt := paused.resetsAt
	a.outcome.UsageLimitResetsAt = &resetsAt
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	if !a.claimed {
		return a.outcome, nil
	}
	recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.pipeline.Tracker.RecordOutcome(recordCtx, a.state.WorkItemID, renderUsageLimitPauseNotes(a.outcome)); err != nil {
		// The pause itself is already durable, so a note that could not be
		// written costs the run nothing: it stays claimed and resumable either
		// way. It is still reported, because an operator watching the tracker
		// would otherwise see the item simply stop moving.
		return a.outcome, fmt.Errorf("record the pause on the work item: %w", err)
	}
	return a.outcome, nil
}

// pauseForProviderStop reports a run whose provider the harness stopped on time.
// It is the twin of pause: the stop was made durable before this point, nothing
// is cleaned up, and nothing is made terminal, so the change the stopped
// invocation had already made stays where the next one continues it.
func (a *activeRun) pauseForProviderStop(stopped providerStop) (Outcome, error) {
	a.outcome.Status = runstate.StatusRunning
	a.outcome.Phase = a.state.Phase
	a.outcome.Paused = true
	a.outcome.ProviderStop = stopped.reason
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	if !a.claimed {
		return a.outcome, nil
	}
	recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.pipeline.Tracker.RecordOutcome(recordCtx, a.state.WorkItemID, renderProviderStopNotes(a.outcome)); err != nil {
		// The stop is already durable, so a note that could not be written costs
		// the run nothing. It is still reported, for the same reason a pause is.
		return a.outcome, fmt.Errorf("record the stopped provider invocation on the work item: %w", err)
	}
	return a.outcome, nil
}

// pauseForDirective reports a run held up by an unresolved user directive. It is
// the third of the pauses and behaves exactly as the other two: the pause was
// made durable before this point, nothing is cleaned up, and nothing is made
// terminal, so the change the run has already produced stays where the next
// attempt continues it. What differs is only what lifts it — somebody answering
// the question or deciding the artifact change, rather than a clock.
func (a *activeRun) pauseForDirective(paused directivePause) (Outcome, error) {
	a.outcome.Status = runstate.StatusRunning
	a.outcome.Phase = a.state.Phase
	a.outcome.Paused = true
	held := paused.directive
	a.outcome.PausedByDirective = &held
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	if !a.claimed {
		return a.outcome, nil
	}
	recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.pipeline.Tracker.RecordOutcome(recordCtx, a.state.WorkItemID, renderDirectivePauseNotes(a.outcome, held)); err != nil {
		// The pause is already durable, so a note that could not be written costs
		// the run nothing. It is still reported, for the same reason the other two
		// pauses report it: an operator watching the tracker would otherwise see
		// the item simply stop moving.
		return a.outcome, fmt.Errorf("record the directive pause on the work item: %w", err)
	}
	return a.outcome, nil
}

// pauseForDependency reports a run held up by work its item waits on. It behaves
// exactly as the directive pause it sits beside: the pause was made durable
// before this point, nothing is cleaned up, and nothing is made terminal, so the
// change the run has already produced stays where the next attempt continues it.
// What differs is only what lifts it — the work it waits on being closed or
// unlinked, rather than somebody settling a question.
func (a *activeRun) pauseForDependency(paused dependencyPause) (Outcome, error) {
	a.outcome.Status = runstate.StatusRunning
	a.outcome.Phase = a.state.Phase
	a.outcome.Paused = true
	waiting := runstate.DependencyPause{Blockers: paused.blockers}
	a.outcome.PausedByDependency = &waiting
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	if !a.claimed {
		return a.outcome, nil
	}
	recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.pipeline.Tracker.RecordOutcome(recordCtx, a.state.WorkItemID, renderDependencyPauseNotes(a.outcome, waiting)); err != nil {
		// The pause is already durable, so a note that could not be written costs
		// the run nothing. It is still reported, for the same reason the other
		// pauses report it: an operator watching the tracker would otherwise see
		// the item simply stop moving.
		return a.outcome, fmt.Errorf("record the dependency pause on the work item: %w", err)
	}
	return a.outcome, nil
}

// pauseForOperatorHold reports a run parked because the operator holds harness
// activity. It is the fourth of the pauses and behaves exactly as the other
// three: the park was made durable before this point, nothing is cleaned up, and
// nothing is made terminal, so the change the run has already produced stays
// where the next attempt continues it. What differs is only what lifts it, which
// is the operator rather than a clock or a resolved directive.
func (a *activeRun) pauseForOperatorHold(paused operatorHoldPause) (Outcome, error) {
	a.outcome.Status = runstate.StatusRunning
	a.outcome.Phase = a.state.Phase
	a.outcome.Paused = true
	held := paused.hold
	a.outcome.PausedByOperator = &held
	a.outcome.PauseCause = runstate.PauseOperatorHold
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	if !a.claimed {
		return a.outcome, nil
	}
	recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.pipeline.Tracker.RecordOutcome(recordCtx, a.state.WorkItemID, renderOperatorHoldNotes(a.outcome, held)); err != nil {
		// The park is already durable, so a note that could not be written costs
		// the run nothing. It is still reported, for the same reason the other
		// pauses report it.
		return a.outcome, fmt.Errorf("record the operator hold on the work item: %w", err)
	}
	return a.outcome, nil
}

// fail records a terminal run failure everywhere it has to be visible: the
// durable state, the reported outcome, and the work item when the run holds it.
func (a *activeRun) fail(cause error, status runstate.Status) (Outcome, error) {
	p := a.pipeline
	// A failure that names an environmental cause is recorded as one here, which
	// is where a cause with no refusal site of its own reaches the record: a
	// worktree that could not be cut from the primary checkout, a check the
	// machine would not start. A refusal site that already recorded one knew more
	// than the error does and is left alone; a settled record belongs to a round
	// that has already ended, so it does not stand in the way of this one.
	//
	// What is recorded here is never "nothing ran", because the error alone cannot
	// say so. The same sentinels arrive both before a round could deliver anything
	// and long after one did — a checkout the harness does not own refuses a
	// promotion as well as a worktree — so a round classified from here is measured
	// against the worktree like any other. The sites that do know say so
	// themselves.
	if a.state.Environmental == nil || a.state.Environmental.Settled {
		if named, environmental := environmentalCauseOf(cause); environmental {
			a.recordEnvironmentalRefusal(named, cause.Error(), ranAnyway)
		}
	}
	// This is where a round settles, so it is where the environment refusing one
	// is decided and paid back. It happens before the terminal write below, so the
	// record that ends the run carries the classification, and before the docket
	// write after it, so the entry a development manager reads reports the budgets
	// as this leaves them rather than as the round spent them.
	a.settleEnvironmentalRound()
	message := cause.Error()
	completedAt := p.clock().Now()
	// A recorded pause or stop is an instruction to resume later, and this run is
	// ending now. Clearing all five keeps the terminal record coherent; what
	// stopped the run is still named by the recorded limit kind and by the
	// failure, and what it spent waiting stays on the record either way.
	a.state.UsageLimitResetsAt = nil
	a.state.PauseCause = ""
	a.state.ProviderStop = ""
	a.state.DirectivePause = nil
	a.state.DependencyPause = nil
	a.state.OperatorHeldSince = nil
	a.state.Status = status
	a.state.UpdatedAt = completedAt
	a.state.CompletedAt = &completedAt
	a.state.Failure = message
	if saveErr := p.Store.Save(a.state); saveErr != nil {
		cause = errors.Join(cause, fmt.Errorf("save failed run state: %w", saveErr))
	}
	a.outcome.Status = status
	a.outcome.Phase = a.state.Phase
	a.outcome.Failure = message
	a.outcome.Branch = a.state.Branch
	a.outcome.WorktreePath = a.state.WorktreePath
	a.outcome.BaseCommit = a.state.BaseCommit
	a.outcome.ProviderSessionID = a.state.ProviderSessionID
	if a.claimed {
		recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, recordErr := p.Tracker.RecordOutcome(recordCtx, a.state.WorkItemID, renderFailureNotes(a.outcome))
		cancel()
		if recordErr != nil {
			cause = errors.Join(cause, fmt.Errorf("record failed run outcome: %w", recordErr))
		}
	}
	// A run that stopped on something a person has to decide reaches the
	// development manager by being docketed as it ends, rather than by an
	// operator noticing the item went quiet. The write is keyed to this
	// stoppage, so a later sweep that walks past the same run adds nothing.
	if a.pipeline.Docket != nil {
		if _, err := a.pipeline.Docket.RecordStoppedRun(a.state); err != nil {
			cause = errors.Join(cause, fmt.Errorf("docket the stopped run: %w", err))
		}
	}
	// A failed attempt spent real money, so it is priced exactly as a successful
	// one is. An item priced only by the run that finished it would be recorded
	// at less than it cost, which is the whole reason the price is per item.
	a.recordPrice()
	return a.outcome, cause
}

// maxCostProblemBytes keeps a price that could not be recorded to a readable
// line of the outcome.
const maxCostProblemBytes = 512

// recordPrice puts what this work item has cost onto the item, aggregated
// across every run made for it rather than only this one.
//
// Nothing here may fail the run. The spending already happened and the run is
// already over; a price nobody could write down is a fact for the operator
// rather than a reason to recast a finished run as a failed one. It is given its
// own bounded context for the same reason recording a failure is: the run's
// context is often already cancelled by the time it ends, and what it cost is
// worth recording anyway.
func (a *activeRun) recordPrice() {
	if a.pipeline.Prices == nil || !a.claimed {
		return
	}
	priceCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cost, err := a.pipeline.Prices.Record(priceCtx, a.state.WorkItemID)
	if cost != nil {
		a.outcome.Cost = cost
	}
	if err != nil {
		a.outcome.CostProblem = singleLine(err.Error(), maxCostProblemBytes)
	}
}

// sink persists one normalized event and the progress it represents.
func (a *activeRun) sink(event execution.Event) error {
	if err := a.pipeline.Store.AppendEvent(event); err != nil {
		return err
	}
	a.state.LastSequence = event.Sequence
	a.state.UpdatedAt = event.Timestamp
	return a.pipeline.Store.Save(a.state)
}

// recordWorktree records the created worktree, including the integration target
// fixed with it, so a later process promotes the work into the branch it was
// written against rather than one it has to infer.
func (a *activeRun) recordWorktree(worktree gitworktree.Worktree) {
	a.worktree = worktree
	a.state.WorktreePath = worktree.Path
	a.state.Branch = worktree.Branch
	a.state.BaseCommit = worktree.BaseCommit
	a.state.TargetBranch = worktree.TargetBranch
	a.outcome.WorktreePath = worktree.Path
	a.outcome.Branch = worktree.Branch
	a.outcome.BaseCommit = worktree.BaseCommit
}

// recordChanges keeps the account of what the run has changed, in the outcome
// its caller reads and in the durable record that outlives the worktree it
// describes. The two are set together so they can never disagree, and the
// durable one is what an operator is shown afterwards: cleanup removes the
// worktree and the branch, and nothing can be diffed out of a tree that is gone.
func (a *activeRun) recordChanges(summary gitworktree.ChangeSummary) {
	a.outcome.Changes = summary
	a.state.Changes = runstate.RecordChanges(summary.Status, summary.DiffStat)
}

// recordHarnessCommit names the commit the harness just made in the worktree.
// Every later inspection of that worktree accepts this exact commit and no
// other, so it is held in the durable state a resumed run rebuilds from as well
// as in the worktree this process is holding.
func (a *activeRun) recordHarnessCommit(commit string) {
	a.worktree.HarnessCommit = commit
	a.state.HarnessCommit = commit
}

// reportCompletionRecordingFailure covers a run whose artifacts were all
// removed but whose final completion record could not be written. Nothing is
// outstanding to clean up, so this must never be described as an incomplete
// cleanup. The durable state keeps the pre-cleanup marker, and resolving it
// costs nothing: a resumed cleanup over absent artifacts is a safe no-op.
func (p Pipeline) reportCompletionRecordingFailure(state runstate.State, outcome Outcome, cause error) (Outcome, error) {
	outcome.CompletionRecordingFailure = cause.Error()
	// A further write is attempted with the failure on it. The store just
	// refused this record twice, so this is best effort — but when it lands,
	// the terminal record is whole and carries why it was late, which is the
	// only durable home this failure class has: the work-item note below may
	// itself fail, and that is part of what makes this class distinct.
	state.CompletionRecordingFailure = outcome.CompletionRecordingFailure
	state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(state); err != nil {
		outcome.CompletionRecordingFailure = errors.Join(cause, fmt.Errorf("record the completion problem in the run record: %w", err)).Error()
	}
	notesCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := p.Tracker.RecordOutcome(notesCtx, outcome.WorkItemID, renderCompletionRecordingNotes(outcome)); err != nil {
		outcome.CompletionRecordingFailure = errors.Join(cause, fmt.Errorf("record the completion problem on the work item: %w", err)).Error()
	}
	return outcome, nil
}

// reportOutstandingCleanup records a post-completion problem without recasting
// a finished run as a failed one. The work is integrated, the item is closed,
// and that is already durable; what is left is a janitorial fact an operator
// and a later reconciler both need to see.
func (p Pipeline) reportOutstandingCleanup(state runstate.State, outcome Outcome, cause error) (Outcome, error) {
	outcome.CleanupFailure = cause.Error()
	state.CleanupFailure = outcome.CleanupFailure
	state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(state); err != nil {
		outcome.CleanupFailure = errors.Join(cause, fmt.Errorf("record outstanding cleanup: %w", err)).Error()
	}
	notesCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := p.Tracker.RecordOutcome(notesCtx, outcome.WorkItemID, renderCleanupNotes(outcome)); err != nil {
		outcome.CleanupFailure = errors.Join(errors.New(outcome.CleanupFailure), fmt.Errorf("record outstanding cleanup on the work item: %w", err)).Error()
	}
	return outcome, nil
}

// reviewChange obtains one independent verdict on the change. A review the
// provider declined for want of capacity was never made, so it is waited out and
// asked for again rather than ending the run: the reviewer is a provider
// invocation like the developer's, and a run stopped there loses just as much
// work. A reply nothing could read as a verdict was not a review either, and it
// is asked for once more for the same reason. Nothing an unanswered review
// leaves behind is carried forward — the retry starts from the same cleared
// evidence any review starts from.
func (a *activeRun) reviewChange(ctx context.Context) (review.Decision, error) {
	reasked := false
	for {
		// A review is a provider invocation like the developer's, so it passes the
		// same two boundaries in the same order: a run the operator stopped stops
		// here rather than buying a verdict on a change nobody is going to take.
		if err := a.stopRequested(); err != nil {
			return "", err
		}
		// A run that reached the gate while the operator was holding activity waits
		// there rather than buying one more invocation.
		if err := a.holdForOperator(ctx); err != nil {
			return "", err
		}
		decision, reported, err := a.attemptReview(ctx)
		if limit, refused := refusedReviewForUsageLimit(reported.usageLimit, err); refused {
			if pauseErr := a.pauseForUsageLimit(ctx, limit); pauseErr != nil {
				return "", pauseErr
			}
			continue
		}
		// A review the provider's own servers could not serve was never made
		// either, and is waited out for the same reason and on the same budget.
		if overload, refused := refusedReviewForServerOverload(reported.serverOverload, err); refused {
			if pauseErr := a.pauseForServerOverload(ctx, overload); pauseErr != nil {
				return "", pauseErr
			}
			continue
		}
		// A review the provider killed rather than refused was not made either, and
		// it costs more to lose than a developer attempt does: the change is already
		// built, checked, and waiting on the one thing that has to happen before it
		// can be promoted. So it is asked for again against the same budget a
		// developer death spends, and a run that spends the budget here stops with
		// the provider named rather than the change.
		if transient, died := diedTransiently(reported.transientFailure, reported.processStatus, false, err); died {
			if !a.mayRelaunch(true) {
				return "", a.blockOnSpentRelaunchBudget(ctx, transient, err)
			}
			if relaunchErr := a.recordRelaunch(); relaunchErr != nil {
				return "", relaunchErr
			}
			continue
		}
		// A review the harness stopped on time was never made either, and the
		// change waiting to be judged is untouched by it. Continuing that run
		// costs one more review; failing it would cost the whole change.
		if reason, stopped := providerStopReason(reported.processStatus); stopped && err != nil {
			resumable, recordErr := a.recordProviderStop(reason)
			if recordErr != nil {
				return "", recordErr
			}
			if resumable {
				return "", providerStop{reason: reason}
			}
		}
		// A reply the verdict contract could not read at all is a failed review
		// invocation rather than a failed change: the reviewer said nothing about
		// the work, and the change waiting to be judged is untouched by it. So it
		// is asked once more, exactly as a declined review is. Two unreadable
		// replies in a row is a reviewer that cannot answer the contract and the
		// run ends on it; one is weather.
		var undecodable review.UndecodableVerdictError
		if !reasked && errors.As(err, &undecodable) {
			reasked = true
			continue
		}
		// A verdict that was actually reached is one round of this work item's
		// life, and it is counted before it is acted on. Everything above this
		// point produced no verdict at all — declined, unserved, killed, or
		// unreadable — and none of those is a round the item spent.
		if err == nil {
			if countErr := a.countReviewRound(ctx); countErr != nil {
				return "", countErr
			}
		}
		return decision, err
	}
}

// countReviewRound records against the work item's durable triage counters that
// one more reviewer verdict has been produced for it. The count spans every run
// of the item, which is what makes it the figure a repair grant is truncated
// against: a run's own repair budget starts again at zero each time, so nothing
// inside a run says what the item has already cost.
//
// The round is recorded under the developer attempt that produced the change, so
// the same attempt judged twice is counted once. That is what keeps two cases
// off the item's bill. A review re-asked for after an interrupted process
// re-judges the attempt that was already counted. And a promotion that loses its
// race replays the same work onto where the target went and obtains a fresh
// verdict on it, which is the reviewer judging the same developer attempt on
// moved ground — charging the item for that would charge it for losing a race it
// did not cause.
func (a *activeRun) countReviewRound(ctx context.Context) error {
	attempt := runstate.RoundKey(a.state.RunID, a.state.RepairAttempts)
	if _, err := a.pipeline.Store.Triage().RecordReviewRound(ctx, a.state.WorkItemID, attempt, a.pipeline.clock().Now()); err != nil {
		return fmt.Errorf("count the review round attempt %s produced: %w", attempt, err)
	}
	return nil
}

// providerEvidence is what an attempted invocation says about why it produced no
// answer, separately from the error it returned: a usage limit the provider
// refused it for, an overload of the provider's own servers, or the way its own
// process ended. Each decides whether the run continues, and none is legible
// from the error alone.
type providerEvidence struct {
	usageLimit       *backend.UsageLimit
	serverOverload   *backend.ServerOverload
	transientFailure *backend.TransientFailure
	processStatus    execution.ProcessStatus
}

// refusedReviewForUsageLimit reports a review the provider declined for want of
// capacity. A limit reported by a review that still produced a verdict is
// evidence rather than a refusal, exactly as it is for a developer attempt.
func refusedReviewForUsageLimit(limit *backend.UsageLimit, err error) (backend.UsageLimit, bool) {
	if limit == nil || err == nil {
		return backend.UsageLimit{}, false
	}
	return *limit, true
}

// refusedReviewForServerOverload reports a review the provider's own servers
// could not serve, on the same rule: a review that still produced a verdict is
// evidence rather than a refusal.
func refusedReviewForServerOverload(overload *backend.ServerOverload, err error) (backend.ServerOverload, bool) {
	if overload == nil || err == nil {
		return backend.ServerOverload{}, false
	}
	return *overload, true
}

// attemptReview runs the configured independent reviewer once and records
// exactly what it decided, including when it fails or answers with something the
// verdict contract rejects. Every recorded outcome is written into the run state
// before the caller acts on it, so a stopped run still explains why it stopped.
func (a *activeRun) attemptReview(ctx context.Context) (review.Decision, providerEvidence, error) {
	p := a.pipeline
	a.state.Phase = runstate.PhaseReviewing
	// Nothing an earlier attempt was told carries into this one. Clearing the
	// evidence before the reviewer runs is what makes a recorded verdict always
	// belong to the change that is about to be judged, so an earlier approval
	// can never authorize a later attempt.
	a.clearReviewEvidence()
	// Whatever stopped a previous invocation is spent once this one starts: a
	// stop left behind would make a running review look continuable to the next
	// process that adopts the run.
	a.state.ProviderStop = ""
	a.state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(a.state); err != nil {
		return "", providerEvidence{}, fmt.Errorf("save reviewing run state: %w", err)
	}
	changes, err := p.Worktrees.UnifiedChanges(ctx, a.worktree, gitworktree.DiffLimits{})
	if err != nil {
		return "", providerEvidence{}, fmt.Errorf("assemble reviewed change: %w", err)
	}
	account := a.account()
	result, reviewErr := p.Reviewer.Review(ctx, review.Request{
		RunID:      a.state.RunID,
		WorkItemID: a.state.WorkItemID,
		Context:    a.context,
		// The invariants reach the reviewer's evidence by the same delivery that
		// reached the developer's context, so a change that violates one is judged
		// against it whether or not the work item ever mentioned it.
		Invariants:   a.reviewedInvariants(changes).Text(),
		WorktreePath: a.worktree.Path,
		Changes:      changes,
		Checks:       a.outcome.Checks,
		RedactValues: p.RedactValues,
		LastSequence: a.state.LastSequence,
		EventSink:    a.sink,
		// What the reviewer's invocation spends is this run's spend, charged to
		// the review rather than to the change: an item that was reviewed four
		// times is where that distinction is the whole answer.
		Spend: a.spendAttribution(domain.RoleReviewer, runstate.SpendPhaseReview),
		// The review is made under the account the run is affined to, so what one
		// piece of work cost is what one subscription was spent.
		AccountAlias:     account.Alias,
		AccountConfigDir: account.Directory,
	})
	if result.LastSequence > a.state.LastSequence {
		a.state.LastSequence = result.LastSequence
	}
	// What the reviewer reported is collected before its verdict is read,
	// because a report survives a review the harness went on to reject: the
	// reviewer noticed the thing whatever became of the verdict beside it.
	a.collectReports(domain.RoleReviewer, result.Reports)
	if result.ReportProblem != "" {
		a.noteReportProblem(domain.RoleReviewer, errors.New(result.ReportProblem))
	}
	a.state.ReviewSessionID = result.SessionID
	a.state.ReviewModel = result.RequestedModel
	a.state.ReviewResolvedModel = result.ResolvedModel
	a.outcome.ReviewSessionID = result.SessionID
	a.outcome.ReviewModel = result.RequestedModel
	a.outcome.ReviewResolvedModel = result.ResolvedModel
	if result.Verdict.Summary != "" {
		a.state.ReviewSummary = result.Verdict.Summary
		a.state.ReviewFindings = len(result.Verdict.Findings)
		a.state.ReviewFindingDetails = durableFindings(result.Verdict.Findings)
		a.outcome.ReviewSummary = result.Verdict.Summary
		a.outcome.ReviewFindings = result.Verdict.Findings
	}
	if result.Decision == review.DecisionApprove || result.Decision == review.DecisionRepair {
		a.state.ReviewDecision = string(result.Decision)
		a.outcome.ReviewDecision = result.Decision
		// One round with a reviewer is a verdict, whichever way it went, and the
		// count of them is what triage measures a work item against. It is counted
		// here rather than derived from the repair attempts because the two are not
		// the same number: a refused path and a failing check are handed back
		// without anybody reviewing anything, and an approved change was reviewed
		// without a repair at all. Unlike the verdict beside it, it is never
		// cleared: what the next attempt discards is the judgement, not the fact
		// that this work has been round once more.
		a.state.ReviewRounds++
	}
	if reviewErr != nil {
		return "", providerEvidence{
			usageLimit:       result.UsageLimit,
			serverOverload:   result.ServerOverload,
			transientFailure: result.TransientFailure,
			processStatus:    result.ProcessStatus,
		}, fmt.Errorf("independent review failed: %w", reviewErr)
	}
	// The reviewer reports the selector it actually ran with. Auditing that
	// against configuration here keeps the recorded evidence a fact rather than
	// an assumption about how the reviewer was wired.
	configured := p.reviewer().Model
	if result.RequestedModel != configured {
		return "", providerEvidence{}, fmt.Errorf("reviewer ran with model %q, configured reviewer model is %q", result.RequestedModel, configured)
	}
	return result.Decision, providerEvidence{}, nil
}

func (a *activeRun) clearReviewEvidence() {
	a.state.ReviewSessionID = ""
	a.state.ReviewModel = ""
	a.state.ReviewResolvedModel = ""
	a.state.ReviewDecision = ""
	a.state.ReviewSummary = ""
	a.state.ReviewFindings = 0
	a.state.ReviewFindingDetails = nil
	a.outcome.ReviewSessionID = ""
	a.outcome.ReviewModel = ""
	a.outcome.ReviewResolvedModel = ""
	a.outcome.ReviewDecision = ""
	a.outcome.ReviewSummary = ""
	a.outcome.ReviewFindings = nil
}

// durableFindings converts a reviewer's findings into the durable schema. They
// are what the next repair attempt is built from and what a recorded blocker
// names, so they have to survive the process that received them.
func durableFindings(findings []review.Finding) []runstate.Finding {
	if len(findings) == 0 {
		return nil
	}
	durable := make([]runstate.Finding, 0, len(findings))
	for _, finding := range findings {
		recorded := runstate.Finding{Severity: string(finding.Severity), Message: finding.Message}
		if finding.Location != nil {
			recorded.File = finding.Location.File
			recorded.Line = finding.Location.Line
		}
		durable = append(durable, recorded)
	}
	return durable
}

// resumableRepair reports whether an incomplete run stopped inside its repair
// loop, which is the only interrupted run this pipeline picks up. It needs the
// worktree every attempt shares, the developer session the next attempt
// continues, a recorded attempt count, and, when an attempt was in flight, the
// repair input that attempt was given: the refused paths, the failing check, or
// the reviewer's findings. Anything else is left to reconciliation rather than
// reconstructed from guesswork.
func resumableRepair(state runstate.State) bool {
	if state.Status != runstate.StatusRunning || state.RepairAttempts == 0 {
		return false
	}
	if state.WorktreePath == "" || state.Branch == "" || state.BaseCommit == "" || state.TargetBranch == "" {
		return false
	}
	if state.ProviderSessionID == "" {
		return false
	}
	switch state.Phase {
	case runstate.PhaseDeveloping:
		return handedBackRepair(state)
	case runstate.PhaseChecking, runstate.PhaseReviewing:
		return true
	default:
		return false
	}
}

// phaseError carries the run status a failed step must be recorded with, so a
// cancelled or timed-out step reports what actually happened to it rather than
// whatever the surrounding context looks like afterwards.
type phaseError struct {
	status runstate.Status
	cause  error
}

func (e phaseError) Error() string { return e.cause.Error() }

func (e phaseError) Unwrap() error { return e.cause }

func failureStatus(ctx context.Context, err error) runstate.Status {
	var phase phaseError
	if errors.As(err, &phase) {
		return phase.status
	}
	return statusForContext(ctx)
}

func (p Pipeline) automatic() bool {
	return p.Config.Approvals.Integration == domain.ApprovalAutomatic
}

// validateIndependentInvocations refuses to integrate work whose developer and
// reviewer cannot be told apart. An empty or shared session identifier means
// the second opinion the policy depends on was never demonstrated.
func validateIndependentInvocations(outcome Outcome) error {
	developer := strings.TrimSpace(outcome.ProviderSessionID)
	reviewer := strings.TrimSpace(outcome.ReviewSessionID)
	switch {
	case developer == "" || reviewer == "":
		return fmt.Errorf("integration requires recorded developer and reviewer sessions, got developer %q and reviewer %q", outcome.ProviderSessionID, outcome.ReviewSessionID)
	case developer == reviewer:
		return fmt.Errorf("integration requires an independent reviewer, but both invocations reported session %q", outcome.ProviderSessionID)
	}
	if strings.TrimSpace(outcome.ProviderModel) == "" || strings.TrimSpace(outcome.ReviewModel) == "" {
		return fmt.Errorf("integration requires recorded developer and reviewer model selectors, got developer %q and reviewer %q", outcome.ProviderModel, outcome.ReviewModel)
	}
	return nil
}

func (p Pipeline) validate() error {
	var problems []error
	if p.Tracker == nil {
		problems = append(problems, errors.New("work tracker is required"))
	}
	if p.Worktrees == nil {
		problems = append(problems, errors.New("worktree manager is required"))
	}
	if p.Store == nil {
		problems = append(problems, errors.New("state store is required"))
	}
	if p.Backend == nil {
		problems = append(problems, errors.New("agent backend is required"))
	}
	if p.Checks == nil {
		problems = append(problems, errors.New("check runner is required"))
	}
	if p.Directives == nil {
		problems = append(problems, errors.New("durable user directives are required"))
	}
	if p.Holds == nil {
		problems = append(problems, errors.New("the operator's hold on harness activity is required"))
	}
	if p.NewRunID == nil {
		problems = append(problems, errors.New("run id generator is required"))
	}
	if strings.TrimSpace(p.Repository) == "" {
		problems = append(problems, errors.New("repository is required"))
	}
	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	return nil
}

func (p Pipeline) developer() config.AgentConfig {
	return p.agentForRole(domain.RoleDeveloper)
}

func (p Pipeline) reviewer() config.AgentConfig {
	return p.agentForRole(domain.RoleReviewer)
}

func (p Pipeline) agentForRole(role domain.AgentRole) config.AgentConfig {
	for _, name := range p.agentNames() {
		agent := p.Config.Agents[name]
		if agent.Role == role {
			return agent
		}
	}
	return config.AgentConfig{}
}

// agentNames lists the configured agents in a fixed order, so the same
// configuration always resolves a role to the same agent.
func (p Pipeline) agentNames() []string {
	names := make([]string, 0, len(p.Config.Agents))
	for name := range p.Config.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// runsOnCompiledAdapter reports a configured backend this build can actually
// launch. That is the Claude Code adapter, and — since a project may declare a
// provider of its own — anything the project declared that runs on it: a
// declared provider is this adapter driving its executable and reading its
// stream with the dialect the declaration supplied, which is exactly what the
// pipeline wires. A backend the vocabulary has and this build ships no adapter
// for, Codex today, is refused here rather than started.
func (p Pipeline) runsOnCompiledAdapter(named domain.Backend) bool {
	registry, err := p.Config.ProviderRegistry()
	if err != nil {
		// A configuration whose providers will not resolve has already been
		// refused by Config.Validate above, so the only honest answer left is the
		// one backend this build ships unconditionally.
		return named == domain.BackendClaudeCode
	}
	descriptor, known := registry.Lookup(named)
	return known && descriptor.Adapter == domain.BackendClaudeCode
}

// validateReviewPolicy refuses automatic integration that is not actually
// gated. An unenforceable policy must stop the run before anything is claimed,
// rather than integrate work no independent reviewer ever saw.
func (p Pipeline) validateReviewPolicy() error {
	if p.Reviewer == nil {
		return errors.New("automatic integration requires an independent reviewer")
	}
	reviewer := p.reviewer()
	if reviewer.Role != domain.RoleReviewer {
		return errors.New("automatic integration requires a configured reviewer agent")
	}
	if !p.runsOnCompiledAdapter(reviewer.Backend) {
		return fmt.Errorf("run pipeline requires a claude-code reviewer, configured backend is %q", reviewer.Backend)
	}
	if err := config.ValidateModelSelector(reviewer.Model); err != nil {
		return fmt.Errorf("reviewer agent %s", err)
	}
	return nil
}

func validateReadyItem(item beads.WorkItem, requestedID string) error {
	return validateWorkItem(item, requestedID, "open")
}

func validateClaimedItem(item beads.WorkItem, requestedID string) error {
	return validateWorkItem(item, requestedID, "in_progress")
}

func validateWorkItem(item beads.WorkItem, requestedID, expectedStatus string) error {
	if item.ID != requestedID {
		return fmt.Errorf("Beads returned work item %q for requested id %q", item.ID, requestedID)
	}
	if item.Status != expectedStatus {
		return fmt.Errorf("work item %s status is %q, want %s", item.ID, item.Status, expectedStatus)
	}
	if blockers := blockingDependencies(item); len(blockers) > 0 {
		return fmt.Errorf("work item %s is blocked by: %s", item.ID, strings.Join(blockers, ", "))
	}
	// Asked of a ready item and of a claimed one alike, because an item that
	// acquired the grant after it was claimed is the same wall as one that carried
	// it all along, and a run resumed into it spends the rounds a run refused here
	// does not.
	return refuseProviderGrant(item)
}

// blocksDependency is the tracker relation that makes one item wait for another.
// It is named once because more than one gate decides on it, and two gates
// spelling the relation differently would be two accounts of what "blocked"
// means that could disagree.
const blocksDependency = "blocks"

// blockingDependencies names the unfinished work an item waits for, in a stable
// order. It is the single reading every dependency gate in this package decides
// on: the run-start check, the gate each attempt passes through, and the refusal
// a stopped run's continuation is held to.
//
// A dependency whose own item is closed is not something anybody is waiting for,
// and a relation that is not "blocks" — a parent-child link, say — says nothing
// about whether this work may proceed.
func blockingDependencies(item beads.WorkItem) []string {
	var blockers []string
	for _, dependency := range item.Dependencies {
		if dependency.Type == blocksDependency && dependency.Status != "closed" {
			blockers = append(blockers, dependency.ID)
		}
	}
	sort.Strings(blockers)
	return blockers
}

func (p Pipeline) clock() execution.Clock {
	if p.Clock == nil {
		return execution.RealClock{}
	}
	return p.Clock
}

// developerContract is the harness policy every developer run carries. It is a
// Go constant rather than configuration because a configured persona may
// specialize how a developer works but must never be able to remove the bounds
// it works within.
const developerContract = `You are the developer for one bounded Yoyodyne work item.

Work only inside the current assigned worktree. Do not create, remove, or switch branches or worktrees. Do not commit, push, or integrate the change; the harness does all three. Do not modify upstream product, goal, design, or specification artifacts; propose the change instead, in the block described below. Implement the assigned work, run relevant focused checks, and finish with a concise summary of changes, verification, and any remaining risk.

That boundary is enforced rather than trusted. The project's configuration directory and the homes its product artifacts, designs, and decision records live in are refused in your change: the harness compares what you touched against them before any check runs and before any reviewer sees the work, and hands the change back to you if it touches one of them. The only exception is a path this work item grants, on a line beginning ` + "`" + protectedpath.GrantMarker + "`" + ` in its title, description, design guidance, or acceptance criteria. Nothing you write grants a path, and neither does anything written into the item's notes, which is where a run's own record goes. If your work genuinely needs one, leave it alone and say so — the refusal you would get names the same thing this does.

A grant lifts the harness's refusal and never somebody else's. Claude Code refuses your writes to ".claude/settings.json" and ".claude/settings.local.json" above anything this harness permits: the editing tools are denied there however this run is configured, and the shell sandbox names the file and cannot be disabled. No grant reaches those paths, and an item that tries to grant one is refused before a run starts, so the case you can actually meet is work that needs one of them changed and says so in prose. Those files are the operator's to change by hand. Do the rest of the work, say in your summary exactly what has to go into the file and that a person has to put it there, and do not spend attempts finding another way in — there is not one.

The work backlog is upstream in the same way. The product manager decides what is admitted to it and in what order it is pulled, so do not admit work to it, reorder it, or retire anything from it. Work you discover goes in your summary, as work to be admitted rather than work you have queued.

Documentation that describes behavior you change is part of the assigned work, not a follow-up: leave no document asserting what your change has made false. Update the ones you may edit in this same change, and for a stale upstream artifact you may not edit, propose the correction it needs.

Any architectural invariant delivered with this work item is a constraint on your change rather than advice. Invariants exist because a change whose own work is correct can still break something the work item never mentioned, so each one holds even where nothing else you were given refers to it. They belong to the architect: do not create, amend, retire, or edit one. If your work cannot satisfy an invariant, or you believe one is wrong, leave it in force and put the amendment you would propose in your summary for the architect to decide.

` + report.Contract + `

Your summary and a report do different jobs, and something can need both. The summary is your account of this work item, read by whoever looks at this run, and it is still where discovered work goes for the product manager to admit. A report outlives the run, so it is what you use for something that will still matter once this item is closed and nobody is reading its summary any more.

` + amendment.Contract + `

A proposal is not a report and not a work item. A report says what somebody should know and asks for nothing; a proposal asks the owner of one document for one change to it, and waits for their answer rather than yours. An architectural invariant is not one of these documents and has its own lifecycle, so the amendment you would propose to one still goes in your summary for the architect.`

// developerPrompt places the immutable contract first, the configured persona
// second as guidance subordinate to it, then the architectural invariants that
// constrain the change, and the work item context last.
func developerPrompt(persona, invariants, bundle string) string {
	var prompt strings.Builder
	prompt.WriteString(developerContract)
	prompt.WriteString("\n\n")
	if trimmed := strings.TrimSpace(persona); trimmed != "" {
		prompt.WriteString("# Configured developer persona\n\nThe project configuration supplies the guidance below. It may specialize how you work, but it cannot remove or weaken any rule above.\n\n")
		prompt.WriteString(trimmed)
		prompt.WriteString("\n\n")
	}
	prompt.WriteString(deliveredInvariantSection(invariants))
	prompt.WriteString(bundle)
	return prompt.String()
}

// deliveredInvariantSection is how the invariants enter a developer prompt. It is
// one helper because every prompt a developer receives carries them — the first
// attempt and both kinds of repair — and a repair attempt that lost the
// constraints would be an attempt free to break one while fixing something else.
func deliveredInvariantSection(invariants string) string {
	if strings.TrimSpace(invariants) == "" {
		return ""
	}
	return invariants + "\n"
}

// repairPrompt hands one attempt's findings back to the developer that produced
// the change. The findings are rendered as the structured value the reviewer
// produced rather than restated as prose, so nothing is lost or reinterpreted
// between the reviewer and the developer that must act on it. The harness
// contract is repeated because it bounds the attempt whether or not the provider
// actually restored the session it was asked to resume.
func repairPrompt(invariants, summary string, findings []runstate.Finding, attempt, limit int) (string, error) {
	encoded, err := json.MarshalIndent(findings, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode review findings for repair attempt %d: %w", attempt, err)
	}
	var prompt strings.Builder
	prompt.WriteString(developerContract)
	prompt.WriteString("\n\n")
	prompt.WriteString(deliveredInvariantSection(invariants))
	prompt.WriteString("# Independent review: repair required\n\n")
	fmt.Fprintf(&prompt, "An independent reviewer examined your change and did not approve it. This is repair attempt %d of %d. Continue the change already in your worktree instead of starting over, and resolve every finding below.\n\n", attempt, limit)
	if trimmed := strings.TrimSpace(summary); trimmed != "" {
		prompt.WriteString("Reviewer summary: " + trimmed + "\n\n")
	}
	prompt.WriteString("Findings:\n\n")
	prompt.Write(encoded)
	prompt.WriteString("\n\nFix each finding, re-run the relevant checks, and finish with a concise summary of what you changed. If a finding is wrong, say why in your summary rather than leaving it unaddressed.")
	return prompt.String(), nil
}

// pathRefusalRepairPrompt hands a refused change back to the developer that
// produced it. It says what was refused, what the item did grant, and how a
// grant is made, in that order: the first is what has to come back out of the
// change, and the last is the whole reason this is a refusal rather than a
// finding — a developer that genuinely needs one of these paths has to be able
// to ask instead of quietly reaching for it again. The harness contract is
// repeated for the reason both other repair prompts repeat it: it bounds the
// attempt whether or not the provider actually restored the session.
func pathRefusalRepairPrompt(invariants string, refusal runstate.PathRefusal, protected protectedpath.Set, attempt, limit int) string {
	var prompt strings.Builder
	prompt.WriteString(developerContract)
	prompt.WriteString("\n\n")
	prompt.WriteString(deliveredInvariantSection(invariants))
	prompt.WriteString("# Protected paths: repair required\n\n")
	fmt.Fprintf(&prompt, "Your change touches paths this work item does not grant, so it was refused before any check ran and before it reached a reviewer. This is repair attempt %d of %d. Continue the change already in your worktree instead of starting over, and take these paths back out of it.\n\n", attempt, limit)
	prompt.WriteString("Refused paths:\n\n")
	for _, refused := range refusal.Paths {
		prompt.WriteString("- " + refused + "\n")
	}
	if refusal.Omitted > 0 {
		fmt.Fprintf(&prompt, "- and %d further refused path(s) not listed here\n", refusal.Omitted)
	}
	fmt.Fprintf(&prompt, "\nProtected by this project: %s\n", strings.Join(protected.Directories(), ", "))
	if len(refusal.Grants) > 0 {
		fmt.Fprintf(&prompt, "Granted by this work item: %s\n", strings.Join(refusal.Grants, ", "))
	} else {
		prompt.WriteString("Granted by this work item: nothing\n")
	}
	prompt.WriteString("\n" + protectedpath.GrantInstruction + "\n")
	prompt.WriteString("\nRestore each refused path to what it held before your change, finish the work you were assigned within the paths that are yours, and finish with a concise summary of what you changed. The harness applies this gate again afterwards; the checks, review, and integration stay out of reach until the change touches nothing it was not granted.")
	return prompt.String()
}

// checkRepairPrompt hands one failing deterministic check back to the developer
// that produced the change. The command, its exit code, and its captured output
// go back verbatim, because what makes a check better repair input than a
// reviewer's finding is that it is reproducible and names the exact failure.
// The harness contract is repeated for the same reason the review repair repeats
// it: it bounds the attempt whether or not the provider actually restored the
// session it was asked to resume.
func checkRepairPrompt(invariants string, failure runstate.CheckFailure, attempt, limit int) string {
	var prompt strings.Builder
	prompt.WriteString(developerContract)
	prompt.WriteString("\n\n")
	prompt.WriteString(deliveredInvariantSection(invariants))
	prompt.WriteString("# Failing check: repair required\n\n")
	fmt.Fprintf(&prompt, "A configured check failed on your change. This is repair attempt %d of %d. Continue the change already in your worktree instead of starting over, and make this check pass.\n\n", attempt, limit)
	fmt.Fprintf(&prompt, "Command: %s\nExit code: %d\n\n", failure.Command, failure.ExitCode)
	if failure.Output != "" {
		prompt.WriteString("Captured output:\n\n```\n")
		prompt.WriteString(failure.Output)
		prompt.WriteString("\n```\n\n")
	}
	prompt.WriteString("Fix the cause, run the command yourself to confirm it passes, and finish with a concise summary of what you changed. The harness re-runs every configured check afterwards; review and integration stay out of reach until they all pass.")
	return prompt.String()
}

// boundedCheckOutput is what a failing check gets to say, in the order a
// terminal would have shown it. The tail is what is kept when a check says more
// than the bound allows, because a suite prints its failures and its summary at
// the end, and the truncation is stated so the developer reading it knows the
// check did not simply stop there.
func boundedCheckOutput(result execution.ProcessResult) string {
	var combined strings.Builder
	for _, stream := range []string{result.Stdout, result.Stderr} {
		trimmed := strings.Trim(stream, "\n")
		if trimmed == "" {
			continue
		}
		if combined.Len() > 0 {
			combined.WriteString("\n")
		}
		combined.WriteString(trimmed)
	}
	return boundedTail(combined.String(), runstate.MaxCheckOutputBytes)
}

// truncationNotice replaces the output boundedTail dropped. It counts against
// the bound, so what is kept plus the notice never exceeds it.
const truncationNotice = "[earlier check output truncated]\n"

// boundedTail keeps the last bytes of a value within a limit, cut on a rune
// boundary: output truncated mid-rune is not text.
func boundedTail(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	keep := limit - len(truncationNotice)
	if keep <= 0 {
		keep = limit
	}
	cut := len(value) - keep
	for cut < len(value) && !utf8.RuneStart(value[cut]) {
		cut++
	}
	if keep == limit {
		return value[cut:]
	}
	return truncationNotice + value[cut:]
}

// renderBlockerNotes describes a run that spent its repair budget. It names the
// findings that are still unresolved and the artifacts that were kept, because
// this note is what a human or a later development manager replans from.
func renderBlockerNotes(outcome Outcome, limit int) string {
	lines := []string{
		"Yoyodyne stopped this item: its independent reviewer still required repair after every permitted attempt.",
		fmt.Sprintf("Repair attempts: %d of %d permitted", outcome.RepairAttempts, limit),
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"The branch and worktree are preserved; the unresolved findings below need a replan or a reassignment.",
	}
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// renderCheckBlockerNotes describes a run that spent its repair budget on a
// check that still fails. It names the exact command and what it printed,
// because that is what a human or a later development manager replans from.
func renderCheckBlockerNotes(outcome Outcome, failure runstate.CheckFailure, limit int) string {
	lines := []string{
		"Yoyodyne stopped this item: a configured check still failed after every permitted attempt.",
		fmt.Sprintf("Repair attempts: %d of %d permitted", outcome.RepairAttempts, limit),
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"The branch and worktree are preserved; the failing check below needs a replan or a reassignment.",
		fmt.Sprintf("Failing check: %s (exit %d)", failure.Command, failure.ExitCode),
	}
	if failure.Output != "" {
		lines = append(lines, "Captured output:\n"+failure.Output)
	}
	return strings.Join(lines, "\n")
}

// renderPathRefusalBlockerNotes describes a run that kept reaching for paths its
// work item does not grant. Unlike the other blockers this one names a decision
// somebody has to take rather than a defect somebody has to fix: the grant is
// the product manager's to add to the item, so the note says what was refused
// and what the item grants today, and leaves which of the two is wrong to the
// reader.
func renderPathRefusalBlockerNotes(outcome Outcome, refused pathRefusal, limit int) string {
	lines := []string{
		"Yoyodyne stopped this item: its change kept touching paths the work item does not grant, after every permitted attempt.",
		fmt.Sprintf("Repair attempts: %d of %d permitted", outcome.RepairAttempts, limit),
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"The branch and worktree are preserved. Either the change reaches outside this item, or this item is missing a grant it should have had; the grant belongs in the item's own text.",
		"Refused paths: " + strings.Join(refused.refusal.Paths, ", "),
	}
	if refused.refusal.Omitted > 0 {
		lines = append(lines, fmt.Sprintf("Further refused paths not listed: %d", refused.refusal.Omitted))
	}
	lines = append(lines, "Protected by this project: "+strings.Join(refused.set.Directories(), ", "))
	if len(refused.refusal.Grants) > 0 {
		lines = append(lines, "Granted by this work item: "+strings.Join(refused.refusal.Grants, ", "))
	} else {
		lines = append(lines, "Granted by this work item: nothing")
	}
	return strings.Join(lines, "\n")
}

// renderIntegrationBlockerNotes describes a run that kept losing its target
// branch. It says plainly that nothing was found wrong with the change, because
// the artifacts it preserves are worth picking up rather than replanning: what
// the reader has to settle is what the target branch is supposed to hold.
func renderIntegrationBlockerNotes(outcome Outcome, failure string, limit int) string {
	lines := []string{
		"Yoyodyne stopped this item: its target branch kept moving under the promotion, so the change was never integrated.",
		fmt.Sprintf("Integration retries: %d of %d permitted", outcome.IntegrationRetries, limit),
		"Failure: " + failure,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"Base commit: " + outcome.BaseCommit,
		"The checks passed and the reviewer approved; nothing here says the change is wrong. The branch and worktree are preserved, and the target branch is what needs looking at.",
	}
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// renderRelaunchBlockerNotes describes a run the provider kept killing. What
// stopped it is never a verdict on the change, so like the integration blocker it
// preserves work that is worth picking up rather than replanning: the developer's
// session is still resumable and the worktree holds whatever the last attempt
// reached.
//
// What the run was already carrying when the provider killed it is a separate
// question, and the note answers it rather than assuming. A provider can die
// during a repair attempt as easily as during the first one, so the run may hold
// a spent repair attempt, refused paths, a failing check, and a reviewer's
// findings — all of which are named here, because a reader told only about the
// provider would go looking for a clean change and find a dirty one.
func renderRelaunchBlockerNotes(outcome Outcome, failure backend.TransientFailure, checkFailure *runstate.CheckFailure, refusal *runstate.PathRefusal, limit int) string {
	lines := []string{
		"Yoyodyne stopped this item: the provider kept ending its invocations without judging the work, and the relaunch budget is spent.",
		fmt.Sprintf("Relaunches: %d of %d permitted", outcome.TransientRelaunches, limit),
		"Last provider failure: " + failure.Detail,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
	}
	if outcome.RepairAttempts > 0 {
		lines = append(lines, "Repair attempts already spent: "+strconv.Itoa(outcome.RepairAttempts))
	}
	if refusal != nil {
		lines = append(lines, "Refused protected paths: "+strings.Join(refusal.Paths, ", "))
	}
	if checkFailure != nil {
		lines = append(lines, fmt.Sprintf("Last failing check: %s (exit %d)", checkFailure.Command, checkFailure.ExitCode))
	}
	lines = append(lines, relaunchBlockerVerdict(outcome, checkFailure))
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// relaunchBlockerVerdict says what the run's own evidence supports about the
// change, which is not the same sentence on every run the provider killed. A run
// killed before anything judged it carries no verdict at all, and saying so is
// what tells the reader to pick the work up rather than replan it. A run killed
// inside its repair loop carries a failing check or a reviewer's findings, and
// that same sentence would deny evidence recorded in the note around it.
func relaunchBlockerVerdict(outcome Outcome, checkFailure *runstate.CheckFailure) string {
	if outcome.RepairAttempts > 0 || checkFailure != nil || len(outcome.ReviewFindings) > 0 {
		return "What stopped this run is the provider rather than a verdict on the change. The repair evidence recorded with this note is what the run was already carrying, and it is unresolved rather than dismissed. The branch, worktree, and developer session are preserved."
	}
	return "No check failed and no reviewer asked for repair; nothing here says the change is wrong. The branch, worktree, and developer session are preserved, and what needs looking at is the provider."
}

// renderMissingPreservedChangeNotes describes a run picked up again to continue
// a change its worktree does not hold. It names the branch first and says
// plainly that nothing was developed, because the two things a reader has to be
// stopped from concluding are that the work is gone and that the empty diff
// behind this is somebody's verdict on it: the change may be on the recorded
// branch in full, and no developer and no reviewer were ever invoked.
func renderMissingPreservedChangeNotes(outcome Outcome, failure string) string {
	lines := []string{
		"Yoyodyne stopped this item: its run was picked up again to continue a change, and the worktree it was re-entered in holds none of that change.",
		"Nothing was developed, checked, or reviewed from an empty worktree; continuing a run means continuing a change that already exists, and doing it from nothing is how an empty repair, a review round burned on an empty diff, or a reinvented change gets delivered.",
		"This round is an environmental refusal: the environment handed it nothing, so whatever it would have spent — a review round against the item's cap, a granted repair round out of the item's grant — is given back as the run settles. This item is no closer to its cap for the round, and the note recorded as the run ends says exactly what was returned.",
		"Failure: " + failure,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"Base commit: " + outcome.BaseCommit,
		"Nothing here says the change was wrong, and nothing was deleted: the branch above is where the preserved work is if it survived. What this needs is somebody to say whether the worktree was seeded from that branch, and to re-enter the run once it carries the change again.",
	}
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// renderRebaseConflictNotes describes a change that cannot be replayed onto what
// its target became. This is the one integration outcome that is genuinely a
// decision rather than a retry, so it names both sides and says explicitly that
// nothing was resolved automatically.
func renderRebaseConflictNotes(outcome Outcome, failure string) string {
	lines := []string{
		"Yoyodyne stopped this item: its target branch moved, and this change conflicts with what the target now holds.",
		"Nothing was force-merged, reset, or auto-resolved; which side of the conflict is right is a decision for a person.",
		"Failure: " + failure,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"Base commit: " + outcome.BaseCommit,
		"The branch and worktree are preserved exactly as they were; reconciling them against the target branch is what this needs.",
	}
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// renderDivergedTargetNotes describes a target branch that has gone a different
// way from the one on the remote, with nothing promoted. Like the replay
// conflict it says explicitly that nothing was resolved, and it names where each
// branch stands: what a person settles here is which of the two histories the
// project's target branch is, and neither commit can be found from the other.
func renderDivergedTargetNotes(outcome Outcome, catchup gitworktree.Catchup, remote, failure string) string {
	lines := []string{
		"Yoyodyne stopped this item: its target branch and the one on the remote have diverged, so the change was never promoted.",
		"Nothing was force-merged, reset, or auto-resolved; which history is right is a decision for a person.",
		"Failure: " + failure,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"Base commit: " + outcome.BaseCommit,
		fmt.Sprintf("Local %s: %s", catchup.TargetBranch, nonEmpty(catchup.LocalCommit, "an unresolved commit")),
		fmt.Sprintf("%s %s: %s", remote, catchup.TargetBranch, nonEmpty(catchup.RemoteCommit, "no such branch")),
		"The checks passed and the reviewer approved; nothing here says the change is wrong. The branch and worktree are preserved, and reconciling the two target branches is what this needs before the change can be promoted.",
		divergedTargetRecovery,
	}
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// divergedTargetRecovery is the route out of a target branch that has gone a
// different way from the remote's, named wherever that divergence is reported. It
// is the one repository state the harness will not decide, so a report of it that
// says only that a person is needed leaves them to invent the steps: which side
// is the shared truth, where the unpublished promotions go so nothing is lost,
// and how the branch is moved without racing a promotion. Those steps are written
// down, and every report of the divergence says where.
const divergedTargetRecovery = "Recovery: follow \"Unwedging a target branch that diverged from the forge\" in docs/operations.md. It preserves the local-only promotions on their own branch, puts the target back onto the remote's history, and leaves that branch for you to republish; the harness will not do it unasked, because which history is right is yours to say."

// renderPromotedDivergenceNotes describes the same divergence found one step too
// late: the local target branch already carries the promotion. It says that
// plainly and first, because a reader who assumed the change was lost would go
// looking for work that is already on their branch — and it says the item is
// deliberately not closed, because the promotion alone would ordinarily have
// closed it and what stops that here is the divergence rather than any doubt
// about the change.
func renderPromotedDivergenceNotes(outcome Outcome, integration gitworktree.Integration, catchup gitworktree.Catchup, remote, failure string) string {
	lines := []string{
		"Yoyodyne stopped this item: the change was promoted onto the local target branch, and the one on the remote then turned out to have diverged from it.",
		"The change itself is not at risk — it is on the local target branch, which is the authoritative one — but the publication did not happen and the two branches cannot be brought together by a fast-forward.",
		"The item is deliberately left open rather than closed as integrated: closing it would record this work as landed against a state nothing reconciles.",
		"Nothing was force-merged, reset, or auto-resolved; which history is right is a decision for a person.",
		"Failure: " + failure,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		fmt.Sprintf("Promoted commit: %s, onto local %s at %s", integration.SourceCommit, integration.TargetBranch, integration.TargetCommit),
		fmt.Sprintf("%s %s: %s", remote, integration.TargetBranch, nonEmpty(catchup.RemoteCommit, "could not be resolved")),
		divergedTargetRecovery,
	}
	if outcome.PullRequest != nil {
		lines = append(lines, fmt.Sprintf("Pull request left unmerged: #%d %s", outcome.PullRequest.Number, outcome.PullRequest.URL))
	}
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// renderUsageLimitPauseNotes describes a run that is waiting rather than one
// that stopped. It says plainly that nothing was abandoned, because an operator
// reading a claimed item that has gone quiet needs to know the difference
// between work in progress and work that needs them.
func renderUsageLimitPauseNotes(outcome Outcome) string {
	lines := []string{
		"Yoyodyne paused this run: the provider refused the attempt without judging the work, so the run is waiting rather than failing.",
		"Run: " + outcome.RunID,
		"Waiting out: " + runstate.DescribePause(outcome.PauseCause, outcome.UsageLimitKind),
	}
	if outcome.UsageLimitResetsAt != nil {
		lines = append(lines, "Asks again by: "+outcome.UsageLimitResetsAt.Format(time.RFC3339))
	}
	lines = append(lines,
		"Branch: "+outcome.Branch,
		"Worktree: "+outcome.WorktreePath,
	)
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	lines = append(lines,
		"This item stays claimed and its branch, worktree, and developer session are all preserved.",
		"Running Yoyodyne on this item again continues the same run; nothing needs to be restarted. The time above bounds the wait rather than gating it: the run asks the provider again at its configured probe interval, and `yoyo resume` moves the next probe to now if what refused it has stopped being true.",
	)
	return strings.Join(lines, "\n")
}

// renderProviderStopNotes describes a run whose provider the harness stopped on
// time. It says which of the two reasons it was, because they call for different
// things from whoever reads it: a stall is worth investigating, an exhausted
// budget is work that needs another pass. Neither is a report from the agent.
func renderProviderStopNotes(outcome Outcome) string {
	headline := "Yoyodyne stopped this run's provider: it stopped emitting events, so nothing was happening. The developer reported no failure."
	if outcome.ProviderStop == runstate.ProviderStopBudgetExhausted {
		headline = "Yoyodyne stopped this run's provider: it was still working when its total budget ran out. The developer reported no failure."
	}
	lines := []string{
		headline,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	lines = append(lines,
		"This item stays claimed and its branch, worktree, and developer session are all preserved.",
		"Running Yoyodyne on this item again continues the same run from where it stopped; nothing needs to be restarted.",
	)
	return strings.Join(lines, "\n")
}

// renderDirectivePauseNotes describes a run held up by an unresolved directive.
// It names the directive and what is unresolved about it, because those are the
// two things somebody needs in order to lift the pause, and it says plainly that
// the work was not abandoned: an operator reading a claimed item that has gone
// quiet has to be able to tell waiting from stopped.
func renderDirectivePauseNotes(outcome Outcome, held directive.Directive) string {
	lines := []string{
		"Yoyodyne paused this run: a user directive affects this work and is unresolved, so the run is waiting rather than failing.",
		"Directive: " + held.ID + " (" + string(held.Kind) + "), received by the " + string(held.ReceivedBy),
		"The operator said: " + held.Text,
	}
	if held.Artifact != "" {
		lines = append(lines, "Governed artifact it changes: "+held.Artifact)
	}
	lines = append(lines,
		"Unresolved: "+held.Unresolved,
		"Run: "+outcome.RunID,
	)
	if outcome.Branch != "" {
		lines = append(lines, "Branch: "+outcome.Branch)
	}
	if outcome.WorktreePath != "" {
		lines = append(lines, "Worktree: "+outcome.WorktreePath)
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	lines = append(lines,
		"This item stays claimed and its branch, worktree, and developer session are all preserved.",
		"Resolving the directive is what lifts the pause; running Yoyodyne on this item after that continues the same run.",
	)
	return strings.Join(lines, "\n")
}

// renderDependencyPauseNotes describes a run held up by work its item waits on.
// It names the blocking items, because closing or unlinking one of them is what
// lifts the pause, and it says plainly that the work was not abandoned: an
// operator reading a claimed item that has gone quiet has to be able to tell
// waiting from stopped.
func renderDependencyPauseNotes(outcome Outcome, waiting runstate.DependencyPause) string {
	lines := []string{
		"Yoyodyne paused this run: this item waits on work that is not finished, so the run is waiting rather than failing. Nothing about the change was judged.",
		"Waiting on: " + waiting.Summary(),
		"Run: " + outcome.RunID,
	}
	if outcome.Branch != "" {
		lines = append(lines, "Branch: "+outcome.Branch)
	}
	if outcome.WorktreePath != "" {
		lines = append(lines, "Worktree: "+outcome.WorktreePath)
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	lines = append(lines,
		"This item stays claimed and its branch, worktree, and developer session are all preserved.",
		"Closing the work above, or removing the dependency link, is what lifts the pause; running Yoyodyne on this item after that continues the same run.",
	)
	return strings.Join(lines, "\n")
}

// renderOperatorHoldNotes describes a run parked because the operator paused all
// harness activity. It says who paused it and when, because the one thing an
// operator reading a quiet item has to be able to tell is a system they paused
// from a system that died.
func renderOperatorHoldNotes(outcome Outcome, held runstate.OperatorHold) string {
	lines := []string{
		"Yoyodyne parked this run: the operator paused all harness activity, so the run is waiting rather than failing. Nothing about the work was judged.",
		"Paused at: " + held.HeldAt.Format(time.RFC3339),
		"Run: " + outcome.RunID,
	}
	if outcome.Branch != "" {
		lines = append(lines, "Branch: "+outcome.Branch)
	}
	if outcome.WorktreePath != "" {
		lines = append(lines, "Worktree: "+outcome.WorktreePath)
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	lines = append(lines,
		"This item stays claimed and its branch, worktree, and developer session are all preserved.",
		"`yoyo resume` lifts the hold; a process still parked on it carries on within seconds, and running Yoyodyne on this item after that continues the same run.",
	)
	return strings.Join(lines, "\n")
}

// renderUsageLimitBlockerNotes describes a run stopped by a refusal it could not
// wait out. It names the reason the wait was refused, because the alternative to
// a stated deadline is a guessed one, and that is the decision being handed to a
// person.
func renderUsageLimitBlockerNotes(outcome Outcome, reason string) string {
	lines := []string{
		"Yoyodyne stopped this item: the provider refused it in a way this run could not wait out.",
		"Reason: " + reason,
		"Run: " + outcome.RunID,
		"Refused by: " + runstate.DescribePause(outcome.PauseCause, outcome.UsageLimitKind),
	}
	if outcome.UsageLimitResetsAt != nil {
		lines = append(lines, "Reported reset: "+outcome.UsageLimitResetsAt.Format(time.RFC3339))
	}
	if outcome.Branch != "" {
		lines = append(lines, "Branch: "+outcome.Branch)
	}
	if outcome.WorktreePath != "" {
		lines = append(lines, "Worktree: "+outcome.WorktreePath)
	}
	lines = append(lines, "The branch and worktree are preserved; this needs more provider capacity, a longer configured maximum pause, or a replan.")
	return strings.Join(lines, "\n")
}

func renderOutcomeNotes(outcome Outcome) string {
	headline := "Yoyodyne bootstrap run succeeded."
	if outcome.Integration != nil {
		headline = "Yoyodyne run passed checks, was approved by an independent reviewer, and was integrated automatically."
	}
	// These notes are recorded before cleanup, because the promotion is settled
	// first and only a promoted change's artifacts are removed. Cleanup can still
	// fail, so say it is scheduled rather than predicting that it happened.
	worktree := "Worktree: " + outcome.WorktreePath
	if outcome.Integration != nil {
		worktree = "Worktree (cleanup pending): " + outcome.WorktreePath
	}
	lines := []string{
		headline,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		worktree,
		"Base commit: " + outcome.BaseCommit,
	}
	if outcome.RepairAttempts > 0 {
		lines = append(lines, "Repair attempts: "+strconv.Itoa(outcome.RepairAttempts))
	}
	// A promotion that had to be re-prepared says something about the branch it
	// was promoted into rather than about the change, and it is the only record
	// that the approved diff was replayed and judged again.
	if outcome.IntegrationRetries > 0 {
		lines = append(lines, "Integration retries: "+strconv.Itoa(outcome.IntegrationRetries))
	}
	// A run that absorbed the provider dying under it and finished anyway says
	// nothing about the change, but it is the only place the deaths are counted
	// where somebody watching the item will see them: a provider degrading run
	// after run is visible here before it is visible as a blocked item.
	if outcome.TransientRelaunches > 0 {
		lines = append(lines, "Relaunches after a provider death: "+strconv.Itoa(outcome.TransientRelaunches))
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	if outcome.ProviderModel != "" {
		lines = append(lines, "Developer model: "+renderModel(outcome.ProviderModel, outcome.ProviderResolvedModel))
	}
	if outcome.Changes.Status != "" {
		lines = append(lines, "Changes:\n"+outcome.Changes.Status)
	}
	if outcome.Changes.DiffStat != "" {
		lines = append(lines, "Diff stat:\n"+outcome.Changes.DiffStat)
	}
	for _, check := range outcome.Checks {
		// What a check spent against what it was allowed is recorded on the item
		// itself, so a suite growing toward its budget is visible run after run
		// rather than only in the run the budget finally stops.
		lines = append(lines, fmt.Sprintf("Check: %s (passed=%t, exit=%d, %s of %s)",
			check.Command, check.Passed, check.Process.ExitCode, check.Elapsed().Round(time.Second), check.Timeout))
	}
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// renderInvariantNotes records which durable constraints this change was held to,
// and what the delivered set was missing. The tracker is where that outlives the
// run: an operator asking later what constrained a change gets an answer, and one
// asking why an invariant did not stop something sees whether it was delivered at
// all.
func renderInvariantNotes(outcome Outcome) []string {
	var lines []string
	if len(outcome.Invariants) > 0 {
		lines = append(lines, "Invariants delivered: "+strings.Join(outcome.Invariants, ", "))
	}
	for _, problem := range outcome.InvariantProblems {
		lines = append(lines, "Invariant not delivered: "+problem)
	}
	return lines
}

func renderFailureNotes(outcome Outcome) string {
	headline := "Yoyodyne bootstrap run failed; branch and worktree are preserved when present."
	switch {
	case outcome.WorktreeRemoved || outcome.BranchRemoved:
		headline = "Yoyodyne run failed after the change was integrated and its artifacts were cleaned up; the integrated commit is the surviving evidence."
	case outcome.Integration != nil:
		headline = "Yoyodyne run failed after the change was already integrated; the integrated commit, branch, and worktree are preserved for reconciliation."
	}
	if outcome.Blocked {
		// A run is blocked by a spent repair budget, by a target branch it could
		// not promote into, or by a provider that kept killing it, and the recorded
		// blocker says which. This headline deliberately does not: naming one of
		// them would be wrong more often than not.
		headline = "Yoyodyne blocked this item; the branch and worktree are preserved, and the blocker recorded on the item says what stopped it."
	}
	lines := []string{
		headline,
		"Run: " + outcome.RunID,
		"Failure: " + outcome.Failure,
	}
	if outcome.Phase != "" {
		lines = append(lines, "Phase: "+string(outcome.Phase))
	}
	// The accounting comes before the counts below it, because it changes what
	// they mean: a reader shown "repair attempts: 3" with nothing saying the last
	// of them was refused by the environment reads an item three rounds closer to
	// its cap than it actually is.
	if outcome.Environmental != nil {
		lines = append(lines, "Round: "+outcome.Environmental.Describe())
	}
	if outcome.RepairAttempts > 0 {
		lines = append(lines, "Repair attempts: "+strconv.Itoa(outcome.RepairAttempts))
	}
	if outcome.IntegrationRetries > 0 {
		lines = append(lines, "Integration retries: "+strconv.Itoa(outcome.IntegrationRetries))
	}
	if outcome.TransientRelaunches > 0 {
		lines = append(lines, "Relaunches after a provider death: "+strconv.Itoa(outcome.TransientRelaunches))
	}
	if outcome.Branch != "" {
		lines = append(lines, "Branch: "+outcome.Branch)
	}
	if outcome.WorktreePath != "" {
		lines = append(lines, "Worktree: "+outcome.WorktreePath)
	}
	if outcome.BaseCommit != "" {
		lines = append(lines, "Base commit: "+outcome.BaseCommit)
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	if outcome.ProviderModel != "" {
		lines = append(lines, "Developer model: "+renderModel(outcome.ProviderModel, outcome.ProviderResolvedModel))
	}
	if outcome.Changes.Status != "" {
		lines = append(lines, "Preserved changes:\n"+outcome.Changes.Status)
	}
	if outcome.Changes.DiffStat != "" {
		lines = append(lines, "Preserved diff stat:\n"+outcome.Changes.DiffStat)
	}
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// renderReviewNotes carries the invariant, review, and integration evidence into
// the tracker, so an operator reconciling an item never has to reconstruct which
// constraints applied, which reviewer decided what, or which commit carried the
// work. Every kind of recorded note ends with this, so a run that succeeded, one
// that failed, and one that was blocked all say the same things about themselves.
func renderReviewNotes(outcome Outcome) []string {
	lines := renderInvariantNotes(outcome)
	if outcome.ReviewSessionID != "" {
		lines = append(lines, "Reviewer session: "+outcome.ReviewSessionID)
	}
	if outcome.ReviewModel != "" {
		lines = append(lines, "Reviewer model: "+renderModel(outcome.ReviewModel, outcome.ReviewResolvedModel))
	}
	if outcome.ReviewDecision != "" {
		lines = append(lines, "Review decision: "+string(outcome.ReviewDecision))
	}
	if outcome.ReviewSummary != "" {
		lines = append(lines, "Review summary: "+outcome.ReviewSummary)
	}
	for _, finding := range outcome.ReviewFindings {
		location := ""
		if finding.Location != nil {
			location = fmt.Sprintf(" (%s:%d)", finding.Location.File, finding.Location.Line)
		}
		lines = append(lines, fmt.Sprintf("Finding [%s]%s: %s", finding.Severity, location, finding.Message))
	}
	if outcome.Integration != nil {
		lines = append(lines,
			"Integrated into: "+outcome.Integration.TargetBranch,
			"Integrated commit: "+outcome.Integration.SourceCommit,
			"Previous target commit: "+outcome.Integration.PreviousTargetCommit,
		)
	}
	return append(lines, renderPublishNotes(outcome)...)
}

// renderModel reports a requested selector alongside what the provider
// resolved it to, because a floating alias only becomes audit evidence once the
// served model is named.
func renderModel(requested, resolved string) string {
	if resolved == "" || resolved == requested {
		return requested
	}
	return requested + " (resolved: " + resolved + ")"
}

// renderCleanupNotes records that a completed run left its worktree behind. The
// integrated commit and the closed item are already true; this is what an
// operator needs in order to finish the job by hand.
func renderCleanupNotes(outcome Outcome) string {
	// Cleanup can fail after both removals already succeeded, when only the
	// confirmation of them failed. Nothing remains in that case, so it must not
	// be described as unfinished work.
	headline := "Yoyodyne run completed but its post-completion cleanup did not finish. The change is integrated and this item is closed."
	if outcome.WorktreeRemoved && outcome.BranchRemoved {
		headline = "Yoyodyne run completed and both run artifacts were removed, but confirming their removal failed. Nothing is known to remain; a repeated cleanup re-checks and removes nothing."
	}
	lines := []string{
		headline,
		"Run: " + outcome.RunID,
		"Cleanup failure: " + outcome.CleanupFailure,
		"Worktree removed: " + strconv.FormatBool(outcome.WorktreeRemoved),
		"Branch removed: " + strconv.FormatBool(outcome.BranchRemoved),
	}
	// Only artifacts that actually survive are reported as remaining; cleanup
	// is retryable, so naming a deleted one would send an operator after
	// something that is not there.
	if outcome.Branch != "" && !outcome.BranchRemoved {
		lines = append(lines, "Remaining branch: "+outcome.Branch)
	}
	if outcome.WorktreePath != "" && !outcome.WorktreeRemoved {
		lines = append(lines, "Remaining worktree: "+outcome.WorktreePath)
	}
	if outcome.Integration != nil {
		lines = append(lines, "Integrated commit: "+outcome.Integration.SourceCommit)
	}
	return strings.Join(lines, "\n")
}

// renderCompletionRecordingNotes describes a finished run whose completion
// could not be written down. It states that removal is done, because an
// operator reading "cleanup" here must not go looking for artifacts that no
// longer exist.
func renderCompletionRecordingNotes(outcome Outcome) string {
	lines := []string{
		"Yoyodyne run completed and its worktree and branch were both removed, but recording final completion failed. Cleanup is finished; nothing remains to remove.",
		"Run: " + outcome.RunID,
		"Completion recording failure: " + outcome.CompletionRecordingFailure,
		"Worktree removed: " + strconv.FormatBool(outcome.WorktreeRemoved),
		"Branch removed: " + strconv.FormatBool(outcome.BranchRemoved),
	}
	if outcome.Integration != nil {
		lines = append(lines, "Integrated commit: "+outcome.Integration.SourceCommit)
	}
	lines = append(lines, "Durable run state may still show the pre-cleanup marker; reconciling it requires no further removal.")
	return strings.Join(lines, "\n")
}

// integrationMessage describes the promoted work in the harness-owned commit,
// including the review that authorized it.
func integrationMessage(item beads.WorkItem, outcome Outcome) string {
	subject := strings.TrimSpace(fmt.Sprintf("yoyodyne: %s %s", outcome.WorkItemID, singleLine(item.Title, maxCommitSubjectBytes)))
	body := []string{
		"",
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Base: " + outcome.BaseCommit,
		"Developer session: " + outcome.ProviderSessionID,
		"Reviewer session: " + outcome.ReviewSessionID,
		"Review decision: " + string(outcome.ReviewDecision),
	}
	return subject + "\n" + strings.Join(body, "\n") + "\n"
}

// singleLine folds a tracker-supplied title into one bounded subject line, so
// the commit subject stays a subject whatever the work item contains. It is cut
// on a rune boundary: a subject truncated mid-rune is not text.
func singleLine(value string, limit int) string {
	folded := strings.Join(strings.Fields(value), " ")
	if len(folded) <= limit {
		return folded
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimSpace(folded[:cut])
}

func completionReason(outcome Outcome) string {
	return fmt.Sprintf("Reviewed and integrated by Yoyodyne run %s: %s is at %s",
		outcome.RunID, outcome.Integration.TargetBranch, outcome.Integration.TargetCommit)
}

func statusForContext(ctx context.Context) runstate.Status {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return runstate.StatusTimedOut
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return runstate.StatusCancelled
	}
	return runstate.StatusFailed
}

func statusForProcess(status execution.ProcessStatus) runstate.Status {
	switch status {
	case execution.ProcessCancelled:
		return runstate.StatusCancelled
	case execution.ProcessTimedOut, execution.ProcessStalled:
		// Both are the harness stopping a process on time. There is no separate
		// durable status for a stall, and recording it as a plain failure would
		// describe the agent as having failed at something.
		return runstate.StatusTimedOut
	default:
		return runstate.StatusFailed
	}
}

func nonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "unknown provider failure"
}
