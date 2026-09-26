package runstate

// A check stage the bound stopped, and the harness continuing it at its checks.
//
// execution.check_stage_timeout ends a stage that runs past it, and a stage
// that ends there judged nothing: no check failed, nothing was handed back to
// the developer, and the change on the branch is exactly what the attempt left.
// What stops a stage there is nearly always the machine rather than the change —
// three runs' race suites beside each other on one laptop — so until
// yoyodyne-ifd.429.25 the ending cost the most of anything on the line: repair
// was refused for want of a failure to hand back, resumption covers only
// approved changes, and the only thing that fired was a re-run from the target
// branch that redid the development and spent the item's re-run budget, while
// the finished change sat on its branch.
//
// So the harness continues such a run itself: at its checks, on the same branch
// and worktree and the change it already has, with no developer invoked and no
// review round, repair grant, or re-run spent. This is what the run's record
// says about that — the continuations it has had, the bound on them, and the
// one sentence every surface says the continuation in.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxCheckStageContinuations bounds how many times the harness continues one
// run at its checks after the stage bound stopped it. It is small on purpose:
// a stage the bound stops again and again at a load the harness judged low
// enough to try is a suite that does not fit its bound, which is a decision
// about the gate or the bound, not something another try settles. Past it the
// stoppage is the development manager's, as every such stoppage was before.
const MaxCheckStageContinuations = 2

// CheckStageContinuation is one continuation of this run at its checks by the
// harness, after the stage bound stopped it.
type CheckStageContinuation struct {
	// Command is the check the bound stopped the stage during.
	Command     string    `json:"command,omitempty"`
	ContinuedAt time.Time `json:"continued_at"`
	// Reason is the harness's own account of why the run is going again.
	Reason string `json:"reason"`
	// SupersededFailure is what the stopped run ended on, in the words it was
	// recorded in.
	SupersededFailure string `json:"superseded_failure,omitempty"`
}

// Validate reports every contract violation in the record at once.
func (c CheckStageContinuation) Validate() error {
	var problems []error
	if c.ContinuedAt.IsZero() {
		problems = append(problems, errors.New("continued_at is required"))
	}
	if strings.TrimSpace(c.Reason) == "" {
		problems = append(problems, errors.New("the reason the harness continued this run is required"))
	}
	if len(c.Reason) > MaxSelectionReasonBytes {
		problems = append(problems, fmt.Errorf("reason is %d bytes, which exceeds the %d byte bound", len(c.Reason), MaxSelectionReasonBytes))
	}
	if len(c.SupersededFailure) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("superseded_failure is %d bytes, which exceeds the %d byte bound", len(c.SupersededFailure), MaxBlockerBytes))
	}
	return errors.Join(problems...)
}

// validateCheckStageContinuations reports every contract violation in the
// record's account of the harness having continued it at its checks.
func (s State) validateCheckStageContinuations() []error {
	var problems []error
	if len(s.CheckStageContinuations) > MaxCheckStageContinuations {
		problems = append(problems, fmt.Errorf("%d check stage continuations are recorded, which exceeds the bound of %d", len(s.CheckStageContinuations), MaxCheckStageContinuations))
	}
	for index, continuation := range s.CheckStageContinuations {
		if err := continuation.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("check_stage_continuations[%d]: %w", index, err))
		}
	}
	if len(s.CheckStageContinuationRefused) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("check_stage_continuation_refused is %d bytes, which exceeds the %d byte bound", len(s.CheckStageContinuationRefused), MaxBlockerBytes))
	}
	return problems
}

// StoppedAtStageBound reports a run that ended because its check stage reached
// execution.check_stage_timeout: timed out, at its checks, with the stage on the
// record saying the bound is what ended it, and nothing promoted. Whatever else
// the record carries, nothing judged the change this round.
func (s State) StoppedAtStageBound() bool {
	return s.Status == StatusTimedOut &&
		s.Phase == PhaseChecking &&
		s.CheckStage != nil && s.CheckStage.StoppedAtBound &&
		s.Integration == nil
}

// HarnessContinuesCheckStage reports a run the stage bound stopped that the
// harness will continue at its checks by itself: its branch and worktree are
// still there, the developer attempt that made the change is on the record,
// the harness has not already continued it MaxCheckStageContinuations times,
// and no earlier continuation was refused for something only a person can
// settle. Whether it may go now — a free slot, a load below the threshold, the
// operator's switches — is the moment's to answer rather than the record's.
func (s State) HarnessContinuesCheckStage() bool {
	if !s.StoppedAtStageBound() {
		return false
	}
	if s.WorktreePath == "" || s.Branch == "" || s.BaseCommit == "" || s.TargetBranch == "" {
		return false
	}
	if s.WorktreeRemoved || s.BranchRemoved || strings.TrimSpace(s.ProviderSessionID) == "" {
		return false
	}
	if strings.TrimSpace(s.CheckStageContinuationRefused) != "" {
		return false
	}
	return len(s.CheckStageContinuations) < MaxCheckStageContinuations
}

// CheckStageLoadThreshold is the condition the harness waits for before it
// continues a stage the bound stopped, in words. It is stated once here so the
// docket, the channel, the item, and the configuration guide say one thing.
const CheckStageLoadThreshold = "the machine's one-minute load average below its number of cores"

// CheckStageStopSays is what every surface says about a run the stage bound
// stopped: that load stopped it rather than the change, and what happens next.
// It is empty for every other run.
func (s State) CheckStageStopSays() string {
	if !s.StoppedAtStageBound() {
		return ""
	}
	stopped := "the check stage was stopped by load at its execution.check_stage_timeout bound, not by the change: nothing was judged and nothing was handed back to the developer"
	if s.HarnessContinuesCheckStage() {
		return fmt.Sprintf(
			"%s; the harness continues it itself, re-running the checks on the change the run already has, on the same branch and worktree, at the next pull with a developer slot free and %s — no developer is invoked and no review round, repair grant, or re-run is spent (continuation %d of %d)",
			stopped, CheckStageLoadThreshold, len(s.CheckStageContinuations)+1, MaxCheckStageContinuations)
	}
	if refused := strings.TrimSpace(s.CheckStageContinuationRefused); refused != "" {
		return fmt.Sprintf("%s; the harness's continuation of it was refused — %s — so what happens to it next is the development manager's decision", stopped, refused)
	}
	if len(s.CheckStageContinuations) >= MaxCheckStageContinuations {
		return fmt.Sprintf("%s; the harness has already continued it at its checks %d times, which is its bound, so what happens to it next is the development manager's decision", stopped, len(s.CheckStageContinuations))
	}
	return stopped + "; its branch or worktree is gone, so the harness cannot continue it, and what happens to it next is the development manager's decision"
}
