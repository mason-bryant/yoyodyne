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

	"yoyodyne/internal/backend/claudecode"
	"yoyodyne/internal/beads"
	"yoyodyne/internal/checks"
	"yoyodyne/internal/config"
	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
	"yoyodyne/internal/gitworktree"
	"yoyodyne/internal/orchestrator"
	"yoyodyne/internal/review"
	"yoyodyne/internal/runstate"
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
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "run requires exactly one Beads work item id")
		printRunUsage(stderr)
		return 2
	}

	pipeline, err := buildPipeline(*configPath)
	if err != nil {
		return reportRunResult(stdout, stderr, *jsonOutput, orchestrator.Outcome{}, err)
	}
	outcome, err := pipeline.Run(ctx, flags.Arg(0))
	return reportRunResult(stdout, stderr, *jsonOutput, outcome, err)
}

// components are the durable and repository-facing parts every command that
// acts on runs shares. They are built once here so a pipeline and a reconciler
// always address the same state root, worktree root, and repository.
type components struct {
	config       config.Config
	repository   string
	runner       execution.OSProcessRunner
	store        *runstate.Store
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
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:                processRunner,
		RepositoryRoot:        repository,
		WorktreeRoot:          worktreeRoot,
		AllowedPrimaryChanges: []string{".beads/interactions.jsonl", ".beads/issues.jsonl"},
	})
	if err != nil {
		return components{}, err
	}
	return components{
		config:       cfg,
		repository:   repository,
		runner:       processRunner,
		store:        store,
		worktrees:    worktrees,
		redactValues: execution.SensitiveEnvironmentValues(os.Environ()),
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
	cfg := parts.config
	processRunner := parts.runner
	redactValues := parts.redactValues

	return orchestrator.Pipeline{
		Tracker:   parts.tracker(),
		Worktrees: parts.worktrees,
		Store:     parts.store,
		Backend: claudecode.Backend{
			Runner: processRunner,
		},
		Checks: checks.Runner{
			Process:      processRunner,
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
		NewRunID:     runstate.NewRunID,
		Repository:   parts.repository,
		Config:       cfg,
		RedactValues: redactValues,
	}, nil
}

func buildReconciler(configPath string) (orchestrator.Reconciler, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return orchestrator.Reconciler{}, err
	}
	// The reconciler is wired without a backend on purpose: settling an
	// interrupted run is never a reason to invoke a provider.
	return orchestrator.Reconciler{
		Tracker:   parts.tracker(),
		Worktrees: parts.worktrees,
		Store:     parts.store,
	}, nil
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
	names := make([]string, 0, len(cfg.Agents))
	for name := range cfg.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if agent := cfg.Agents[name]; agent.Role == role {
			return agent
		}
	}
	return config.AgentConfig{}
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
		// waiting. Reporting it as either would tell an operator to do something
		// about a run that only needs to be left alone until its deadline.
		fmt.Fprintf(stdout, "run paused: %s\n", outcome.RunID)
		fmt.Fprintf(stdout, "waiting for the %s usage limit to reset\n", nonEmptyValue(outcome.UsageLimitKind, "provider"))
		if outcome.UsageLimitResetsAt != nil {
			fmt.Fprintf(stdout, "resets at: %s\n", outcome.UsageLimitResetsAt.Format(time.RFC3339))
		}
		fmt.Fprintf(stdout, "branch: %s\n", outcome.Branch)
		fmt.Fprintf(stdout, "worktree: %s\n", outcome.WorktreePath)
		fmt.Fprintf(stdout, "run yoyodyne on %s again after the reset time to continue this run\n", outcome.WorkItemID)
		if err != nil {
			fmt.Fprintf(stderr, "the pause is recorded, but reporting it failed: %v\n", err)
		}
	} else if err != nil {
		fmt.Fprintf(stderr, "run failed: %v\n", err)
		if outcome.RunID != "" {
			fmt.Fprintf(stderr, "run: %s\n", outcome.RunID)
		}
		if outcome.RepairAttempts > 0 {
			fmt.Fprintf(stderr, "repair attempts: %d\n", outcome.RepairAttempts)
		}
		// A blocked item is not waiting on this process: the unresolved findings
		// are recorded where the work is tracked and need a replan.
		if outcome.Blocked {
			fmt.Fprintf(stderr, "blocker recorded on %s: the reviewer's findings are unresolved\n", outcome.WorkItemID)
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
	} else {
		fmt.Fprintf(stdout, "run succeeded: %s\n", outcome.RunID)
		fmt.Fprintf(stdout, "branch: %s\n", outcome.Branch)
		if outcome.RepairAttempts > 0 {
			fmt.Fprintf(stdout, "repair attempts: %d\n", outcome.RepairAttempts)
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
	if err != nil {
		return 1
	}
	return 0
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
	fmt.Fprintln(writer, `Usage: yoyodyne run [options] <beads-id>

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
