package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend/claudecode"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/cost"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type runOutput struct {
	Outcome *orchestrator.Outcome `json:"outcome,omitempty"`
	Error   string                `json:"error,omitempty"`
}

func runWorkItem(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "run requires exactly one Beads work item id")
		printRunUsage(stderr)
		return 2
	}

	pipeline, err := buildPipeline(*configPath)
	if err != nil {
		return reportRunResult(stdout, stderr, *jsonOutput, orchestrator.Outcome{}, err)
	}
	// The operator typed the identifier, so the run records that as why it exists.
	// It is worth recording even though it is obvious to whoever typed it: what an
	// operator reads later is a list of runs, and one that says nothing about why
	// it was chosen is indistinguishable from one the harness chose unaccountably.
	pipeline.Selection = runstate.OperatorSelection(
		"the operator ran this item by name from the command line", time.Now())
	outcome, err := pipeline.Run(ctx, positional[0])
	return reportRunResult(stdout, stderr, *jsonOutput, outcome, err)
}

// components are the durable and repository-facing parts every command that
// acts on runs shares. They are built once here so a pipeline and a reconciler
// always address the same state root, worktree root, and repository.
type components struct {
	config     config.Config
	repository string
	// stateRoot is where everything durable that is not the repository lives.
	// It is kept here so a command that needs another store built on it — the
	// conversation record and the collected reports beside the run state —
	// addresses the same root.
	stateRoot string
	runner    execution.OSProcessRunner
	store     *runstate.Store
	// reports is the collected pile of what agents noticed while their work
	// carried on. It is built beside the run store because it is durable in the
	// same way and outlives the runs that fill it.
	reports *runstate.ReportStore
	// amendments is where changes proposed to documents their proposer does not
	// own are kept, with what the owner or the operator decided about each. It is
	// built beside the reports because it is durable in the same way and for the
	// same reason: the argument outlives the run that made it.
	amendments *runstate.AmendmentStore
	// evaluations is where the product manager's recommendations about the
	// operator's ideas are kept. It is built beside the amendments for the same
	// reason: the reasoning outlives the conversation that reached it, and a
	// decision taken weeks later is the one that most needs it.
	evaluations *runstate.EvaluationStore
	// docket is the work that has stopped moving, waiting for the development
	// manager to decide what becomes of it. It is built beside the reports for
	// the same reason: an entry outlives the run that produced it, and a run
	// whose artifacts have been cleaned up leaves a stoppage that is still
	// somebody's to decide.
	docket *runstate.DocketStore
	// branchReviews is where verdicts on accumulated changes are recorded. It is
	// its own store for the same reason: a branch review outlives every run whose
	// work it judged, and it belongs to no one of them.
	branchReviews *runstate.BranchReviewStore
	// directives is what the operator has told the harness. It is built beside
	// the run store rather than inside it because that is what makes a directive
	// reach work regardless of which agent received it: every command here
	// addresses the same product-scoped records.
	directives *runstate.DirectiveStore
	// holds is the operator's switch over everything the harness would spend on a
	// provider. It is built on the state root itself rather than under the
	// product, because one switch pauses the machine: what makes an operator pause
	// is an account or an afternoon, neither of which belongs to a product.
	holds *runstate.OperatorHoldStore
	// intake is the operator's switch over the work the harness chooses for
	// itself. It is built under the product rather than on the state root,
	// because unlike the hold above it is about one backlog: holding what a
	// development manager may pull from this product must leave another alone.
	intake *runstate.IntakeHoldStore
	// watch is what a session that stays open says it is doing. It is built
	// under the product beside the intake hold, because what it watches is one
	// backlog and because the two are read together: a session that is choosing
	// nothing and a hold that says why are one story to whoever reads them.
	watch *runstate.WatchStore
	// usageLimits is where a provider refusing the harness outside a run is
	// recorded. It is built under the product beside the watch log, because a
	// limit is exhausted for an account and read as a fact about one product's
	// line stopping: the processes that meet one — a conversation, a review —
	// have no run between them to write it on.
	usageLimits  *runstate.UsageLimitStore
	worktrees    *gitworktree.Manager
	redactValues []string
}

func buildComponents(configPath string) (components, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return components{}, err
	}
	cfg := resolved.Config
	// Relative paths resolve against the project, not against the .yoyodyne
	// directory the configuration happens to live in.
	projectDirectory := config.ProjectDirectory(resolved.Path)
	repository, err := resolvePath(projectDirectory, cfg.Product.Repository)
	if err != nil {
		return components{}, fmt.Errorf("resolve product repository: %w", err)
	}
	cfg.Product.Repository = repository

	stateRoot, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		return components{}, err
	}
	worktreeRoot := cfg.Execution.WorktreeRoot
	if worktreeRoot == "auto" {
		worktreeRoot = filepath.Join(stateRoot, "worktrees", string(cfg.Product.ID), string(cfg.Product.RepositoryID))
	} else {
		worktreeRoot, err = resolvePath(projectDirectory, worktreeRoot)
		if err != nil {
			return components{}, fmt.Errorf("resolve worktree root: %w", err)
		}
	}

	processRunner := execution.OSProcessRunner{}
	store, err := runstate.NewStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	reports, err := runstate.NewReportStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	amendments, err := runstate.NewAmendmentStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	evaluations, err := runstate.NewEvaluationStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	docket, err := runstate.NewDocketStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	branchReviews, err := runstate.NewBranchReviewStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	directives, err := runstate.NewDirectiveStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	holds, err := runstate.NewOperatorHoldStore(stateRoot)
	if err != nil {
		return components{}, err
	}
	intake, err := runstate.NewIntakeHoldStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	watch, err := runstate.NewWatchStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	usageLimits, err := runstate.NewUsageLimitStore(stateRoot, cfg.Product.ID)
	if err != nil {
		return components{}, err
	}
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:                processRunner,
		RepositoryRoot:        repository,
		WorktreeRoot:          worktreeRoot,
		Remote:                cfg.Execution.Remote,
		AllowedPrimaryChanges: []string{".beads/interactions.jsonl", ".beads/issues.jsonl"},
	})
	if err != nil {
		return components{}, err
	}
	return components{
		config:        cfg,
		repository:    repository,
		stateRoot:     stateRoot,
		runner:        processRunner,
		store:         store,
		reports:       reports,
		amendments:    amendments,
		evaluations:   evaluations,
		docket:        docket,
		branchReviews: branchReviews,
		directives:    directives,
		holds:         holds,
		intake:        intake,
		watch:         watch,
		usageLimits:   usageLimits,
		worktrees:     worktrees,
		redactValues:  execution.SensitiveEnvironmentValues(os.Environ()),
	}, nil
}

func (c components) tracker() beads.Client {
	return beads.Client{Runner: c.runner, Dir: c.repository}
}

func buildPipeline(configPath string) (orchestrator.Pipeline, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return orchestrator.Pipeline{}, err
	}
	return pipelineFrom(parts), nil
}

// pipelineFrom wires the run pipeline over parts that are already built, so a
// command that needs more than one of them — a conversation that also steers
// work — builds the repository and state boundaries exactly once.
func pipelineFrom(parts components) orchestrator.Pipeline {
	cfg := parts.config
	processRunner := parts.runner
	redactValues := parts.redactValues

	return orchestrator.Pipeline{
		Tracker:   parts.tracker(),
		Worktrees: parts.worktrees,
		Store:     parts.store,
		// Which account serves a run is chosen once, as the run starts, and every
		// invocation the run goes on to make reads the alias back off the record.
		// The backend below carries no account of its own for that reason: it is
		// the same value for every run this process starts, and the account is not.
		Accounts: accountPool{
			config:    cfg,
			stateRoot: parts.stateRoot,
			runs:      parts.store,
			now:       func() time.Time { return time.Now().UTC() },
		},
		StateRoot: parts.stateRoot,
		Backend: claudecode.Backend{
			Runner: processRunner,
		},
		Checks: checks.Runner{
			Process: processRunner,
			// The budget every check gets is configured rather than fixed,
			// because it is a property of the project's suite and of how many
			// runs share the machine with it, neither of which the harness knows.
			Timeout:      cfg.Execution.CheckTimeout.Duration(),
			RedactValues: redactValues,
		},
		// The reviewer runs its own provider invocation, so it is built from a
		// separate backend value rather than sharing the developer's, and with
		// the reviewer agent's own required model selector and effective
		// persona.
		Reviewer: review.Reviewer{
			Backend: claudecode.Backend{Runner: processRunner},
			Model:   agentModel(cfg, domain.RoleReviewer),
			Persona: agentForRole(cfg, domain.RoleReviewer).Persona.Text,
		},
		// The publisher is the harness's own forge access, wired here so that no
		// agent is ever asked to invoke it and nothing about it reaches a prompt
		// or a context bundle. It is only consulted when the project opted in to
		// publishing.
		Publisher: publish.GitHub{
			Runner: processRunner,
			Dir:    parts.repository,
			// The forge client speaks about the same remote the push targets,
			// so a checkout with several remotes publishes to the configured
			// one rather than to whichever the CLI would infer.
			Remote:       cfg.Execution.Remote,
			RedactValues: redactValues,
		},
		// What the developer and the reviewer report while their work carries on
		// is collected here. It is wired as its own store rather than through the
		// run state, because a report outlives the run that made it: the run is
		// settled and its artifacts are removed, and what it reported is still
		// waiting for somebody to read.
		Reports: parts.reports,
		// What the operator has directed is read from the product's own records
		// every time a run is about to commit to work. It is wired here rather
		// than delivered into a prompt because a directive that pauses work is not
		// something an agent weighs: it is a reason the work must not proceed, and
		// the harness is what enforces it.
		Directives: parts.directives,
		// The operator's hold over provider spending, read at every boundary where
		// this pipeline would spend. It is wired here rather than delivered into a
		// prompt for the same reason a directive is: a paused harness is not
		// something an agent weighs.
		Holds: parts.holds,
		// The operator's hold on the work the harness chooses for itself, read
		// where a run would be started for a reason other than the operator naming
		// the item. It stops nothing already under way, which is the whole reason
		// it is a second switch rather than part of the first.
		Intake: parts.intake,
		// A change an agent proposes to a document it may not edit is recorded
		// here, for the same reason and in the same way: the run that argued the
		// design was wrong is over long before anybody decides what to do about it,
		// and the proposal has to still be there when they do.
		Amendments: parts.amendments,
		// What a run costs is already recorded in its event log, and the item it
		// served is already recorded beside it. This is the join: as a run ends,
		// the item it was for is priced across every run ever made for it, and the
		// price is put where the tracker carries it.
		Prices: ledgerFrom(parts),
		// Where a run that ends on a durable blocker is put in front of the
		// development manager. It is wired here rather than delivered into a
		// prompt for the same reason the directives are: what has stopped moving
		// is a fact the harness records, and the role that decides about it is not
		// the one that stopped.
		Docket:       docketerFrom(parts),
		NewRunID:     runstate.NewRunID,
		Repository:   parts.repository,
		Config:       cfg,
		RedactValues: redactValues,
	}
}

// ledgerFrom wires the ledger that prices work items over parts that are
// already built, so the price a run records and the price `yoyo cost` reports
// are read from one set of records and written to one tracker.
//
// Recording a price is one bd command per item, and the tracker carries the
// bound on it, so a backfill of a hundred items is a hundred separately bounded
// writes rather than one long one nothing can interrupt.
func ledgerFrom(parts components) cost.Ledger {
	tracker := parts.tracker()
	tracker.Timeout = costTrackerTimeout
	return cost.Ledger{Prices: parts.store, Tracker: tracker}
}

// docketerFrom wires the triage docket over parts that are already built, so
// the entries a run makes, the ones a sweep finds, and the docket a development
// manager reads are one log rather than three.
func docketerFrom(parts components) *orchestrator.Docketer {
	return &orchestrator.Docketer{
		Docket: parts.docket,
		Runs:   parts.store,
		// The two records the triage guards spend and refuse against: what has
		// been decided about the item, and what the harness has carried out of it.
		// The docket reads those rather than a count of its own, so a decision a
		// guard would enforce is never one the docket shows as absent.
		Decisions: parts.store.Triage(),
		Reruns:    parts.store.Reruns(),
		// The same caps the conversation's budgets spend against, assembled once,
		// so what an entry says an item has left and what refuses the next
		// decision about it are one set of numbers.
		Caps:   orchestrator.TriageCaps(parts.config.Execution, parts.config.Triage),
		Triage: parts.config.Triage,
	}
}

func buildReconciler(configPath string) (orchestrator.Reconciler, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return orchestrator.Reconciler{}, err
	}
	return reconcilerFrom(parts), nil
}

// reconcilerFrom wires reconciliation over parts that are already built. It is
// deliberately given no backend: settling an interrupted run is never a reason
// to invoke a provider. The forge client it does get can only ask what became
// of a merge the forge queued, which is the one thing a finished run can still
// be waiting on.
func reconcilerFrom(parts components) orchestrator.Reconciler {
	return orchestrator.Reconciler{
		Tracker:   parts.tracker(),
		Worktrees: parts.worktrees,
		Store:     parts.store,
		Publisher: publish.GitHub{
			Runner:       parts.runner,
			Dir:          parts.repository,
			Remote:       parts.config.Execution.Remote,
			RedactValues: parts.redactValues,
		},
		// A run this sweep stops is docketed as it is settled, so a stoppage the
		// process that made it never got to record still reaches the development
		// manager.
		Docket: docketerFrom(parts),
	}
}

// agentModel returns the configured selector for a role. Configuration
// validation already requires one for every agent, so an empty result means the
// role is not configured at all and the pipeline refuses the run.
func agentModel(cfg config.Config, role domain.AgentRole) string {
	return agentForRole(cfg, role).Model
}

// agentForRole returns the effective agent that fills a role, chosen by name so
// the same configuration always wires the same agent.
func agentForRole(cfg config.Config, role domain.AgentRole) config.AgentConfig {
	if name := agentNameForRole(cfg, role); name != "" {
		return cfg.Agents[name]
	}
	return config.AgentConfig{}
}

// agentNameForRole names that agent, which is what a record attributed to it
// says: a project may configure more than one agent for a role, and the role
// alone would not say which of them acted.
func agentNameForRole(cfg config.Config, role domain.AgentRole) string {
	names := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if cfg.Agents[name].Role == role {
			return name
		}
	}
	return ""
}

func resolvePath(base, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is empty")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return filepath.Abs(path)
}

func reportRunResult(stdout, stderr io.Writer, jsonOutput bool, outcome orchestrator.Outcome, err error) int {
	if jsonOutput {
		result := runOutput{}
		if outcome.RunID != "" || outcome.WorkItemID != "" || outcome.Status != "" {
			result.Outcome = &outcome
		}
		if err != nil {
			result.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, result); code != 0 {
			return code
		}
	} else if outcome.Paused {
		// A paused run is neither a success nor a failure: it is in flight and
		// owed a continuation. Reporting it as either would tell an operator to do
		// something about a run that only needs to be started again.
		//
		// A directive pause is the one that can have no run behind it at all,
		// because a directive stops work before it is claimed as readily as during
		// it. So it is reported on its own terms: naming a run that was never
		// started, or a preserved worktree that never existed, would send an
		// operator looking for artifacts nothing made.
		// The operator's hold is the other pause that can have no run behind it,
		// and it is reported on its own terms for the same reason: what lifts it is
		// one command, and nothing about this work item has anything to do with it.
		// A held intake is the third pause with no run behind it, and the one that
		// never applies to work an operator named: it is reported on its own terms
		// so nothing describes it as a run waiting on a provider.
		if outcome.PausedByIntake != nil {
			reportIntakeHold(stdout, outcome)
		} else if outcome.PausedByOperator != nil {
			reportOperatorHold(stdout, stderr, outcome, err)
		} else if outcome.PausedByDirective != nil {
			reportDirectivePause(stdout, outcome)
			if err != nil {
				fmt.Fprintf(stderr, "the pause is recorded, but reporting it failed: %v\n", err)
			}
		} else if outcome.PausedByDependency != nil {
			// A dependency pause is the fourth that can have no run behind it, and it
			// is reported on its own terms for the same reason: what lifts it is other
			// work finishing, and nothing about it is a wait on a provider.
			reportDependencyPause(stdout, outcome)
			if err != nil {
				fmt.Fprintf(stderr, "the pause is recorded, but reporting it failed: %v\n", err)
			}
		} else {
			reportRunPause(stdout, stderr, outcome, err)
		}
	} else if err != nil {
		fmt.Fprintf(stderr, "run failed: %v\n", err)
		if outcome.RunID != "" {
			fmt.Fprintf(stderr, "run: %s\n", outcome.RunID)
		}
		if outcome.RepairAttempts > 0 {
			fmt.Fprintf(stderr, "repair attempts: %d\n", outcome.RepairAttempts)
		}
		if outcome.TransientRelaunches > 0 {
			fmt.Fprintf(stderr, "relaunches after a provider death: %d\n", outcome.TransientRelaunches)
		}
		// A blocked item is not waiting on this process: what stopped it is
		// recorded where the work is tracked and needs a person. Several different
		// things block a run — unresolved findings, a failing check, a target
		// branch that kept moving, a provider that kept killing it — so this says
		// where to look rather than naming one of them and being wrong.
		if outcome.Blocked {
			fmt.Fprintf(stderr, "blocker recorded on %s; the failure above says what stopped it\n", outcome.WorkItemID)
		}
		// An artifact recorded as removed is never described as preserved.
		if outcome.Branch != "" && !outcome.BranchRemoved {
			fmt.Fprintf(stderr, "preserved branch: %s\n", outcome.Branch)
		}
		if outcome.WorktreePath != "" && !outcome.WorktreeRemoved {
			fmt.Fprintf(stderr, "preserved worktree: %s\n", outcome.WorktreePath)
		}
		if outcome.BranchRemoved {
			fmt.Fprintf(stderr, "branch was already removed: %s\n", outcome.Branch)
		}
		if outcome.WorktreeRemoved {
			fmt.Fprintf(stderr, "worktree was already removed: %s\n", outcome.WorktreePath)
		}
		if outcome.Integration != nil {
			fmt.Fprintf(stderr, "already integrated into %s: %s\n", outcome.Integration.TargetBranch, outcome.Integration.TargetCommit)
		}
		reportPullRequest(stderr, outcome)
	} else {
		fmt.Fprintf(stdout, "run succeeded: %s\n", outcome.RunID)
		fmt.Fprintf(stdout, "branch: %s\n", outcome.Branch)
		reportPullRequest(stdout, outcome)
		if outcome.RepairAttempts > 0 {
			fmt.Fprintf(stdout, "repair attempts: %d\n", outcome.RepairAttempts)
		}
		if outcome.TransientRelaunches > 0 {
			fmt.Fprintf(stdout, "relaunches after a provider death: %d\n", outcome.TransientRelaunches)
		}
		if outcome.Integration == nil {
			fmt.Fprintf(stdout, "worktree: %s\n", outcome.WorktreePath)
		} else {
			fmt.Fprintf(stdout, "review: %s (session %s, model %s)\n", outcome.ReviewDecision, outcome.ReviewSessionID, outcome.ReviewModel)
			fmt.Fprintf(stdout, "integrated into %s: %s\n", outcome.Integration.TargetBranch, outcome.Integration.TargetCommit)
			if outcome.WorktreeRemoved {
				fmt.Fprintf(stdout, "worktree removed: %s\n", outcome.WorktreePath)
			} else {
				fmt.Fprintf(stdout, "worktree NOT removed: %s\n", outcome.WorktreePath)
			}
			if outcome.BranchRemoved {
				fmt.Fprintf(stdout, "branch removed: %s\n", outcome.Branch)
			} else {
				fmt.Fprintf(stdout, "branch NOT removed: %s\n", outcome.Branch)
			}
		}
		if outcome.CleanupFailure != "" {
			// The run succeeded; only the artifacts that actually survive still
			// need an operator, and cleanup can simply be retried. A failure
			// with nothing left is a failed confirmation, not leftover work.
			if outcome.WorktreeRemoved && outcome.BranchRemoved {
				fmt.Fprintf(stderr, "cleanup could not be confirmed after a successful run: %s\n", outcome.CleanupFailure)
				fmt.Fprintln(stderr, "both artifacts were removed; nothing is known to remain")
			} else {
				fmt.Fprintf(stderr, "cleanup incomplete after a successful run: %s\n", outcome.CleanupFailure)
				if !outcome.BranchRemoved {
					fmt.Fprintf(stderr, "remaining branch: %s\n", outcome.Branch)
				}
				if !outcome.WorktreeRemoved {
					fmt.Fprintf(stderr, "remaining worktree: %s\n", outcome.WorktreePath)
				}
			}
		}
		if outcome.PublishFailure != "" {
			// The promotion itself succeeded: the local target branch is the
			// authoritative one and it already moved. Only the publication of it
			// is unfinished, so this is reported as outstanding rather than as a
			// change that did not land.
			fmt.Fprintf(stderr, "publication incomplete after a successful run: %s\n", outcome.PublishFailure)
			fmt.Fprintln(stderr, "the change is integrated locally; the pull request still needs to be reconciled by hand")
		}
		if outcome.CompletionRecordingFailure != "" {
			// Cleanup finished here; only writing it down did not. Saying
			// anything about remaining artifacts would send an operator after
			// files that are gone.
			fmt.Fprintf(stderr, "completion recording failed after a successful run: %s\n", outcome.CompletionRecordingFailure)
			fmt.Fprintln(stderr, "cleanup completed: the worktree and branch were both removed and nothing remains to clean up")
		}
		fmt.Fprintf(stdout, "base commit: %s\n", outcome.BaseCommit)
		if outcome.Changes.Status != "" {
			fmt.Fprintf(stdout, "changes:\n%s\n", outcome.Changes.Status)
		}
		if outcome.Changes.DiffStat != "" {
			fmt.Fprintf(stdout, "diff stat:\n%s\n", outcome.Changes.DiffStat)
		}
	}
	if !jsonOutput {
		// What the run's agents reported is collected whichever way the run went,
		// so it is named whichever way this reports.
		reportCollectedReports(stdout, console.ThemeFor(stdout, os.Getenv), outcome)
		// And so is what they proposed changing in a document they do not own: it
		// is waiting on a person either way, and a proposal nobody is told about is
		// one nobody decides.
		reportProposedAmendments(stdout, outcome)
		// So is what the work item has cost: a failed attempt spent money too, and
		// the price is of the item rather than of this run.
		reportItemCost(stdout, stderr, outcome)
	}
	if err != nil {
		return 1
	}
	return 0
}

// reportRunPause describes a run that is waiting on the provider: an exhausted
// usage limit, or an invocation the harness stopped on time. Both leave a run in
// flight with its artifacts preserved, and both are continued by starting the
// item again.
func reportRunPause(stdout, stderr io.Writer, outcome orchestrator.Outcome, err error) {
	fmt.Fprintf(stdout, "run paused: %s\n", outcome.RunID)
	if outcome.ProviderStop != "" {
		// A stall and an exhausted budget are different facts about the run, and
		// only one of them is worth investigating.
		if outcome.ProviderStop == runstate.ProviderStopStalled {
			fmt.Fprintln(stdout, "the provider stopped emitting events and was stopped; it reported no failure")
		} else {
			fmt.Fprintln(stdout, "the provider was still working when its total budget ran out; it reported no failure")
		}
	} else {
		fmt.Fprintf(stdout, "waiting out %s\n", runstate.DescribePause(outcome.PauseCause, outcome.UsageLimitKind))
		if outcome.UsageLimitResetsAt != nil {
			fmt.Fprintf(stdout, "asks again by: %s\n", outcome.UsageLimitResetsAt.Format(time.RFC3339))
		}
	}
	fmt.Fprintf(stdout, "branch: %s\n", outcome.Branch)
	fmt.Fprintf(stdout, "worktree: %s\n", outcome.WorktreePath)
	if outcome.ProviderStop != "" {
		fmt.Fprintf(stdout, "run yoyodyne on %s again to continue this run\n", outcome.WorkItemID)
	} else {
		// The recorded time bounds the wait rather than gating it, so continuing is
		// not something to hold off until it passes. What does need saying is the
		// one verb that overrides it, for when the refusal has stopped being true.
		fmt.Fprintf(stdout, "run yoyodyne on %s again to continue this run; it asks the provider again at its probe interval rather than only at that time\n", outcome.WorkItemID)
		fmt.Fprintf(stdout, "`yoyo resume %s` releases the wait now if the provider would serve it already\n", outcome.WorkItemID)
	}
	if err != nil {
		fmt.Fprintf(stderr, "the pause is recorded, but reporting it failed: %v\n", err)
	}
}

// reportDirectivePause describes work held up by an unresolved user directive.
// It names the directive in full, because what lifts the pause is somebody
// reading it and deciding, and it says which of the two cases this is: a run
// that was under way and is preserved, or work that was never started.
func reportDirectivePause(stdout io.Writer, outcome orchestrator.Outcome) {
	held := outcome.PausedByDirective
	fmt.Fprintf(stdout, "%s is paused for an unresolved directive\n", outcome.WorkItemID)
	fmt.Fprint(stdout, held.Render())
	if outcome.RunID != "" {
		fmt.Fprintf(stdout, "run: %s\n", outcome.RunID)
		fmt.Fprintf(stdout, "branch: %s\n", outcome.Branch)
		fmt.Fprintf(stdout, "worktree: %s\n", outcome.WorktreePath)
		fmt.Fprintln(stdout, "the item stays claimed and its artifacts are preserved; nothing was cancelled")
	} else {
		fmt.Fprintln(stdout, "nothing was started for it, so there is nothing to clean up")
	}
	fmt.Fprintf(stdout, "`yoyo directive resolve %s` settles it, and running yoyodyne on %s after that carries on\n",
		held.ID, outcome.WorkItemID)
}

// reportDependencyPause describes work held up by unfinished work its item was
// made to wait on. It names that work, because closing it or unlinking it is what
// lifts the pause, and it says which of the two cases this is: a run that was
// under way and is preserved, or work that was never started.
func reportDependencyPause(stdout io.Writer, outcome orchestrator.Outcome) {
	fmt.Fprintf(stdout, "%s waits on unfinished work and is paused\n", outcome.WorkItemID)
	fmt.Fprintf(stdout, "waiting on: %s\n", outcome.PausedByDependency.Summary())
	if outcome.RunID != "" {
		fmt.Fprintf(stdout, "run: %s\n", outcome.RunID)
		fmt.Fprintf(stdout, "branch: %s\n", outcome.Branch)
		fmt.Fprintf(stdout, "worktree: %s\n", outcome.WorktreePath)
		fmt.Fprintln(stdout, "the item stays claimed and its artifacts are preserved; nothing was cancelled")
	} else {
		fmt.Fprintln(stdout, "nothing was started for it, so there is nothing to clean up")
	}
	fmt.Fprintf(stdout, "closing that work, or removing the dependency link, lifts the pause; running yoyodyne on %s after that carries on\n",
		outcome.WorkItemID)
}

// reportOperatorHold describes work the operator's own pause is holding. It
// leads with the pause rather than with the item, because the operator paused
// everything rather than this: whatever they were asking for here, what they
// need to be told is that the harness is where they left it. It says when the
// hold was placed for the same reason, since a system somebody paused and forgot
// looks exactly like a system that died.
//
// What it will not do is say nothing was started when something was. A run this
// item already has is named, whether it parked at a boundary or is still working
// its way to one — both keep everything they hold, and both carry on when the
// pause lifts. A lookup that failed says so instead of guessing either way: an
// operator told nothing was started for a run that is sitting in a worktree with
// hours of work in it would be misled about the one thing this verb exists to
// make legible.
func reportOperatorHold(stdout, stderr io.Writer, outcome orchestrator.Outcome, err error) {
	fmt.Fprintf(stdout, "PAUSED: all harness activity is paused, since %s\n",
		outcome.PausedByOperator.HeldAt.Format(time.RFC3339))
	switch {
	case outcome.RunID != "":
		fmt.Fprintf(stdout, "%s has a run in flight; it parks at its next provider call\n", outcome.WorkItemID)
		fmt.Fprintf(stdout, "run: %s\n", outcome.RunID)
		fmt.Fprintf(stdout, "branch: %s\n", outcome.Branch)
		fmt.Fprintf(stdout, "worktree: %s\n", outcome.WorktreePath)
		fmt.Fprintln(stdout, "the item stays claimed and its artifacts are preserved; nothing was cancelled")
	case err != nil:
		fmt.Fprintf(stdout, "what is in flight for %s could not be read, so nothing here says whether anything was started for it\n", outcome.WorkItemID)
	default:
		fmt.Fprintf(stdout, "nothing is in flight for %s, so nothing was started and there is nothing to clean up\n", outcome.WorkItemID)
	}
	fmt.Fprintf(stdout, "`yoyo resume` lifts the pause, and running yoyodyne on %s after that carries on\n", outcome.WorkItemID)
	if err != nil {
		// The pause itself is not in doubt — it is a flag this command just read —
		// so what failed is named beside it rather than in place of it.
		fmt.Fprintf(stderr, "the pause is in force; this could not be fully reported: %v\n", err)
	}
}

// reportIntakeHold names work the harness declined to choose because the
// operator is holding intake. Nothing was claimed and nothing developed, so it
// names no run, no branch, and no worktree: sending an operator to look for
// artifacts nothing made is the failure every pause report here avoids.
//
// It always says the two ways out, because they are genuinely different
// decisions: lift the hold and let the harness choose again, or leave it in force
// and name this item yourself, which the hold was never over.
func reportIntakeHold(stdout io.Writer, outcome orchestrator.Outcome) {
	fmt.Fprintf(stdout, "INTAKE HELD: the harness starts nothing on its own, since %s\n",
		outcome.PausedByIntake.HeldAt.Format(time.RFC3339))
	if reason := strings.TrimSpace(outcome.PausedByIntake.Reason); reason != "" {
		fmt.Fprintln(stdout, reason)
	}
	fmt.Fprintf(stdout, "nothing was started for %s and nothing was claimed; work already running carries on\n", outcome.WorkItemID)
	fmt.Fprintf(stdout, "`yoyo release` lets the harness choose work again, as does /release in a conversation, and `yoyo run %s` runs this item now regardless\n", outcome.WorkItemID)
}

// reportCollectedReports names what this run's agents reported without it
// stopping their work. The reports themselves are read from the pile, either
// from a command line or from the conversation the operator may already be in;
// what this owes them is to say there is something new to read, and where.
//
// It says how many of what rather than only how many, and is marked and dressed
// by the worst of them. This is the last line of a run that has just printed a
// screen of branches, worktrees and prices, and an operator who is only going to
// read one of those lines has to be told from it whether anything in that pile
// is already costing them.
func reportCollectedReports(writer io.Writer, theme console.Theme, outcome orchestrator.Outcome) {
	if len(outcome.Reports) > 0 {
		worst := report.Worst(outcome.Reports)
		fmt.Fprint(writer, theme.Severity(console.Severity(worst), fmt.Sprintf(
			"%sreported %d thing(s) without stopping the run (%s); `yoyo reports` shows them, as does /reports in `yoyo chat`\n",
			worst.Prefix(), len(outcome.Reports), report.Tally(outcome.Reports))))
	}
	if outcome.ReportProblem != "" {
		fmt.Fprintln(writer, outcome.ReportProblem)
	}
}

// reportProposedAmendments names what this run's agents proposed changing in a
// document they do not own. Nothing was written to any document, and nothing
// will be until somebody decides, so what this owes the operator is to say a
// decision is waiting and where to make it.
func reportProposedAmendments(writer io.Writer, outcome orchestrator.Outcome) {
	for _, proposal := range outcome.Amendments {
		fmt.Fprintf(writer, "proposed a change to %s for the %s to decide; nothing was written to it (%s)\n",
			proposal.Artifact, proposal.Owner, proposal.ID)
	}
	if len(outcome.Amendments) > 0 {
		fmt.Fprintln(writer, "`yoyo amendment list` shows what is waiting, and approve or decline decides one")
	}
	if outcome.AmendmentProblem != "" {
		fmt.Fprintln(writer, outcome.AmendmentProblem)
	}
}

// reportItemCost names what the work item has cost across every run made for
// it, which is what the run just added to. A price that could not be recorded is
// named too: the spending happened either way, and an operator reading a ledger
// has to know where it stopped being written down.
func reportItemCost(stdout, stderr io.Writer, outcome orchestrator.Outcome) {
	if outcome.Cost != nil {
		total := fmt.Sprintf("$%.2f", outcome.Cost.TotalUSD)
		if !outcome.Cost.Complete() {
			// A run nothing survives to price is left out of the total rather than
			// added as a zero, so the number is the least the item can have cost.
			total = fmt.Sprintf("at least $%.2f (%d run(s) left no record to price)", outcome.Cost.TotalUSD, outcome.Cost.UnknownRuns)
		}
		fmt.Fprintf(stdout, "%s has cost %s across %d run(s)\n", outcome.WorkItemID, total, outcome.Cost.Runs)
	}
	if outcome.CostProblem != "" {
		fmt.Fprintf(stderr, "the price of %s was not recorded: %s\n", outcome.WorkItemID, outcome.CostProblem)
	}
}

// reportPullRequest names the published work. A run that asked to publish and
// did not is reported too, because "no pull request appeared" is otherwise
// indistinguishable from a harness that quietly stopped publishing.
func reportPullRequest(writer io.Writer, outcome orchestrator.Outcome) {
	if outcome.PullRequest != nil {
		state := "open"
		switch {
		case outcome.PullRequest.Merged:
			state = "merged"
		// A queued merge is not the same fact as an open request nobody has acted
		// on: the forge has accepted the merge and performs it once the base
		// branch's requirements are met, after this run is over.
		case outcome.PullRequest.MergeQueued:
			state = "merge queued"
		}
		fmt.Fprintf(writer, "pull request #%d (%s): %s\n", outcome.PullRequest.Number, state, outcome.PullRequest.URL)
		if outcome.PullRequest.MergeQueued {
			fmt.Fprintln(writer, "the forge merges it once the required checks pass; `yoyo reconcile` settles the run when it does")
			fmt.Fprintln(writer, "the work item stays open until that merge is confirmed, and is handed back with a blocker if the forge drops it")
		}
	}
	if outcome.PublishSkipped != "" {
		fmt.Fprintf(writer, "publishing skipped: %s\n", outcome.PublishSkipped)
	}
}

// nonEmptyValue falls back to a stated placeholder rather than printing a blank
// where a name belongs: the provider does not always name the limit it refused
// on, and "the  usage limit" reads as a bug.
func nonEmptyValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func printRunUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo run [options] <beads-id>

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
