package chat

// A run started with /work is silent between prompts. Everything it does is in
// the record as it happens, and until the operator asks — with /status, with
// /wait, or by waiting for the run to end — none of it is said out loud, so a
// run that has been reviewed and merged looks exactly like one that is still
// developing.
//
// What is said here is deliberately little: the few crossings that change what
// an operator would do next. The account of work in progress exists so that an
// event log does not scroll past the conversation, and this must not quietly
// become one. Each crossing is said once, above the line being composed, and
// carried into the product manager's next turn as the harness activity it is.
//
// The crossings are read from the run's durable record rather than from the
// process executing it. That is the same discipline the display of a turn
// follows — what the operator is told is never more than what the record
// holds — and it is what keeps this independent of how a run is executed.

import (
	"context"
	"fmt"
	"io"
	"time"
)

// progressInterval is how often a watched run's record is re-read. It is short
// enough that a crossing reaches the operator while it still tells them
// something and long enough that watching a run costs a file read every couple
// of seconds rather than a spin.
const progressInterval = 2 * time.Second

// reviewApprove is the reviewer verdict that authorizes a promotion, named here
// rather than imported so a conversation stays independent of how a review is
// executed — the same reason the provider stop reasons are named here.
const reviewApprove = "approve"

// RunProgress is where a run has got to, as durable run state holds it. Every
// field is read from the record rather than from whatever is executing the run,
// so what a conversation says happened is what somebody reading the record
// afterwards would say happened.
type RunProgress struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
	Phase  string `json:"phase,omitempty"`
	// ChecksPassed reports the deterministic checks behind the run: the record
	// carries no failing check and the run has moved past running them.
	ChecksPassed bool `json:"checks_passed,omitempty"`
	// ReviewDecision is the reviewer's verdict once the record holds one.
	ReviewDecision string `json:"review_decision,omitempty"`
	// Integrated reports a recorded promotion into the target branch, and
	// TargetBranch is what it was promoted into.
	Integrated   bool   `json:"integrated,omitempty"`
	TargetBranch string `json:"target_branch,omitempty"`
	// MergeQueued reports a merge the forge accepted and has not performed, and
	// Merged reports one it has. They are separate facts about the same request:
	// a queued merge is a run that finished with its publication still owed.
	MergeQueued bool `json:"merge_queued,omitempty"`
	Merged      bool `json:"merged,omitempty"`
}

// crossings names what a run crossed between one reading of its record and the
// next. It reports transitions rather than states, so a crossing is said once
// however often the record is read, and the set is short on purpose: these are
// the points at which what the operator would do next changes.
func crossings(before, after RunProgress) []string {
	var crossed []string
	if !before.ChecksPassed && after.ChecksPassed {
		crossed = append(crossed, "its checks passed")
	}
	if before.ReviewDecision != after.ReviewDecision && after.ReviewDecision != "" {
		if after.ReviewDecision == reviewApprove {
			crossed = append(crossed, "the reviewer approved it")
		} else {
			crossed = append(crossed, "the reviewer asked for repairs")
		}
	}
	if !before.Integrated && after.Integrated {
		crossed = append(crossed, integrated(after))
	}
	if !before.MergeQueued && after.MergeQueued {
		crossed = append(crossed, "its pull request is queued to merge")
	}
	if !before.Merged && after.Merged {
		crossed = append(crossed, "its pull request was merged")
	}
	return crossed
}

func integrated(progress RunProgress) string {
	if progress.TargetBranch == "" {
		return "it was integrated"
	}
	return "it was integrated into " + progress.TargetBranch
}

// watchProgress says what a run crosses while it is going. It stops as soon as
// the run is over: what became of the run is reported in full by whoever
// collects it, and a crossing said after that would be describing something the
// operator has already read the outcome of.
func (s *Session) watchProgress(ctx context.Context, work Work, run *activeRun) {
	ticker := time.NewTicker(s.progressEvery())
	defer ticker.Stop()
	var before RunProgress
	var reading bool
	for {
		select {
		case <-run.done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		after, err := work.Progress(ctx, run.workItemID)
		if err != nil {
			// A record that cannot be read is what the first seconds of every run
			// look like, because there is nothing written down yet. Nothing is said
			// about it: a crossing is news the operator can act on, and failing to
			// read a record is not.
			continue
		}
		// The first reading, and the first reading of a different run, are the
		// baseline rather than a set of crossings. Without that, starting work on
		// an item that has been run before would announce everything the previous
		// attempt reached as though this one had just done it.
		if !reading || before.RunID != after.RunID {
			before, reading = after, true
			continue
		}
		crossed := crossings(before, after)
		before = after
		if len(crossed) > 0 {
			run.crossed(crossed)
		}
	}
}

// progressEvery is how often this conversation re-reads a watched run's record.
func (s *Session) progressEvery() time.Duration {
	if s.progressInterval > 0 {
		return s.progressInterval
	}
	return progressInterval
}

// reportMilestones says what the run this conversation started has crossed
// since the operator was last told. Each one is a line above whatever they are
// composing and a notice carried into the product manager's next turn: the
// operator is watching the work, and the product manager answers questions
// about it.
func (s *Session) reportMilestones(out io.Writer) {
	run := s.active
	if run == nil {
		return
	}
	for _, crossed := range run.takeCrossings() {
		fmt.Fprintf(out, "%s: %s\n", run.workItemID, crossed)
		s.notice("work on %s reached a milestone: %s", run.workItemID, crossed)
	}
}
