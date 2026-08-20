package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend/claudecode"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/review"
)

type branchReviewOutput struct {
	Review *orchestrator.BranchReviewOutcome `json:"review,omitempty"`
	Error  string                            `json:"error,omitempty"`
}

// reviewBranch runs one independent review of what a branch accumulated over a
// base commit. It is the review scope no per-work-item run can reach: each run
// judges one worktree, and a defect that only exists across the commits of
// several of them is invisible to every one of those reviews.
//
// It never touches an in-flight or settled run. The work it covers is already
// integrated, so this reports on the branch rather than gating anything that has
// already happened — and it fails unless an independent reviewer approved the
// whole accumulated change, so nothing downstream can read anything else as an
// approval of it.
func reviewBranch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("review", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	base := flags.String("base", "", "the base ref the branch accumulated over")
	branch := flags.String("branch", "", "the branch to review (default: the repository's current branch)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "review does not accept positional arguments")
		printReviewUsage(stderr)
		return 2
	}
	if *base == "" {
		fmt.Fprintln(stderr, "review requires --base: an accumulated change is measured against the commit it grew from")
		printReviewUsage(stderr)
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportBranchReview(stdout, stderr, *jsonOutput, orchestrator.BranchReviewOutcome{}, err)
	}
	// A branch review is one provider invocation and nothing else — there is no
	// run to park, no claim to keep, and nothing durable it could resume from — so
	// a held harness refuses it here rather than parking it. Asking again after
	// `yoyo resume` costs nothing that was already spent.
	if hold, held, err := parts.holds.Held(); err != nil || held {
		if err == nil {
			err = fmt.Errorf("all harness activity is paused, since %s; `yoyo resume` lifts it and this review can be asked for again",
				hold.HeldAt.Format(time.RFC3339))
		}
		return reportBranchReview(stdout, stderr, *jsonOutput, orchestrator.BranchReviewOutcome{}, err)
	}
	reviewed := *branch
	if reviewed == "" {
		// A branch nobody named is the one the repository is on, which is the
		// branch an operator asking about "this work" means.
		reviewed, err = parts.worktrees.CurrentBranch(ctx)
		if err != nil {
			return reportBranchReview(stdout, stderr, *jsonOutput, orchestrator.BranchReviewOutcome{}, err)
		}
	}
	outcome, err := branchReviewerFrom(parts).Review(ctx, orchestrator.BranchReviewRequest{
		Branch:  reviewed,
		BaseRef: *base,
	})
	return reportBranchReview(stdout, stderr, *jsonOutput, outcome, err)
}

// branchReviewerFrom wires a branch review over parts that are already built. It
// is given the reviewer and nothing else that acts: no run store, no tracker,
// and no integration, because a verdict on already-integrated work must not be
// able to reach the runs that produced it.
func branchReviewerFrom(parts components) orchestrator.BranchReviewer {
	cfg := parts.config
	return orchestrator.BranchReviewer{
		Worktrees: parts.worktrees,
		Reviewer: review.Reviewer{
			Backend: claudecode.Backend{Runner: parts.runner},
			Model:   agentModel(cfg, domain.RoleReviewer),
			Persona: agentForRole(cfg, domain.RoleReviewer).Persona.Text,
		},
		Reviews: parts.branchReviews,
		Reports: parts.reports,
		// Where a provider refusing this review for want of capacity is written
		// down. It records nothing about the branch and reaches no run: it is the
		// account of why nothing happened, for somebody who is not at this
		// terminal.
		UsageLimits:  parts.usageLimits,
		Repository:   parts.repository,
		Config:       cfg,
		RedactValues: parts.redactValues,
	}
}

// reportBranchReview says what the review decided and exits on whether the
// branch was approved. A repair verdict is not a failed review — the review
// worked, and it asked for repairs — but it is not an approval either, and the
// exit code answers that question rather than the other one.
func reportBranchReview(stdout, stderr io.Writer, jsonOutput bool, outcome orchestrator.BranchReviewOutcome, err error) int {
	if jsonOutput {
		result := branchReviewOutput{}
		if outcome.ReviewID != "" {
			result.Review = &outcome
		}
		if err != nil {
			result.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, result); code != 0 {
			return code
		}
		if err != nil || !outcome.Approved() {
			return 1
		}
		return 0
	}

	if err != nil {
		fmt.Fprintf(stderr, "branch review failed: %v\n", err)
	}
	if outcome.ReviewID != "" {
		fmt.Fprintf(stdout, "reviewed %s against %s: %d commit(s) from %s to %s\n",
			outcome.Branch, outcome.BaseRef, outcome.Commits, outcome.BaseCommit, outcome.HeadCommit)
		if outcome.Decision != "" {
			fmt.Fprintf(stdout, "review: %s (session %s, model %s)\n", outcome.Decision, outcome.SessionID, outcome.Model)
		}
		if outcome.Summary != "" {
			fmt.Fprintf(stdout, "summary: %s\n", outcome.Summary)
		}
		for _, finding := range outcome.Findings {
			location := ""
			if finding.Location != nil {
				location = fmt.Sprintf(" [%s:%d]", finding.Location.File, finding.Location.Line)
			}
			fmt.Fprintf(stdout, "- %s%s: %s\n", finding.Severity, location, finding.Message)
		}
		// A change the bounds cut is one the reviewer could not see the whole of,
		// which is why it could not approve it either.
		if outcome.Truncated {
			fmt.Fprintln(stderr, "the accumulated change was too large to describe in full; what was not shown is unreviewed")
			if outcome.CommitsOmitted > 0 {
				fmt.Fprintf(stderr, "%d older commit(s) were not described\n", outcome.CommitsOmitted)
			}
		}
		if outcome.RecordFailure != "" {
			fmt.Fprintf(stderr, "the verdict above was not recorded: %s\n", outcome.RecordFailure)
		}
		if len(outcome.Reports) > 0 {
			fmt.Fprintf(stdout, "reported %d thing(s) beside the verdict; `yoyo reports` shows them, as does /reports in `yoyo chat`\n", len(outcome.Reports))
		}
		if outcome.ReportProblem != "" {
			fmt.Fprintln(stdout, outcome.ReportProblem)
		}
		// The per-item work is integrated already, so this says what the branch
		// now needs rather than holding anything back from it.
		if !outcome.Approved() && err == nil {
			fmt.Fprintf(stderr, "%s is not approved as an accumulated change; the work on it stays integrated and the findings above are work to be admitted\n", outcome.Branch)
		}
	}
	if err != nil || !outcome.Approved() {
		return 1
	}
	return 0
}

func printReviewUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo review --base <ref> [options]

Reviews what a branch accumulated over a base commit, as one change, with the
same independent reviewer that judges a single work item. The work it covers is
already integrated: this reports on the branch and never revises a run.

Options:
  --base <ref>      the base the branch accumulated over (required)
  --branch <name>   the branch to review (default: the current branch)
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
