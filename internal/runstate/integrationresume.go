package runstate

// An approved change the environment stopped on its way to the target branch,
// and what resuming it costs the item: nothing.
//
// A run's budgets bound the work's own failures, and a stop between the
// reviewer's approval and the promotion is not one. yoyodyne-ifd.309's rerun
// passed independent review and then stopped at integration twice — once for an
// uncommitted edit in the primary checkout, once for a tracker read that timed
// out under load — and every verb that could have picked it up spent something:
// a repair grant for a run that recorded no findings, or a fresh run and a fresh
// review for a change nobody disputed. Four operator overrides were signed to
// get one approved change onto the target, none of them for a verdict.
//
// So the stop is a durable fact on the run, written where the run fails from
// the error's own sentinel, and a resumption is a continuation rather than an
// attempt: the run is made live again at the promotion it stopped short of, with
// the approval it already had, and the item's counters stay where the review
// left them. What leaves that path is a replay that conflicts, which is a
// person's to settle exactly as it always was.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxIntegrationResumptions bounds how many resumptions one run's record may
// carry. Nothing in the harness spends toward it — a resumption charges no
// budget, which is the point of it — so it is the record's own bound: an
// environment that keeps refusing the same promotion is a machine somebody has
// to look at, not a state file that grows until it does.
const MaxIntegrationResumptions = 16

// ResumingIntegrationSays is what every surface says of a run resumed at its
// promotion while that promotion is going: the approval stands, and what is
// happening is the integration the environment stopped, not a new round. It is
// one phrase here rather than one per surface for the reason the outcome
// vocabulary is: `yoyo status` and the channel must not say different words
// about one run.
const ResumingIntegrationSays = "approved, resuming integration"

// IntegrationStop is the environment having stopped this run after its change
// was approved and before that change was promoted.
//
// The cause is one of the closed set in environmental.go, and it is what makes
// the stop resumable: a promotion the environment refused is one the environment
// can stop refusing, where a replay that conflicted or a target that diverged is
// a decision somebody has to make. The phase says which step the run was in
// when it stopped, which is the step a resumption re-enters.
type IntegrationStop struct {
	Cause EnvironmentalCause `json:"cause"`
	// Detail is the failure the run ended on, folded to a line. It is evidence for
	// whoever reads the record rather than a second classification of it.
	Detail string `json:"detail,omitempty"`
	// Phase is the phase the run stopped in: reviewing, for a stop between the
	// approving verdict and the promotion, or integrating.
	Phase      Phase     `json:"phase"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Validate reports every contract violation in the record at once.
func (s IntegrationStop) Validate() error {
	var problems []error
	if !s.Cause.Valid() {
		problems = append(problems, fmt.Errorf("environmental cause %q is not one this harness records", s.Cause))
	}
	if len(s.Detail) > MaxEnvironmentalDetailBytes {
		problems = append(problems, fmt.Errorf("detail is %d bytes, which exceeds the %d byte bound", len(s.Detail), MaxEnvironmentalDetailBytes))
	}
	if s.Phase != PhaseReviewing && s.Phase != PhaseIntegrating {
		problems = append(problems, fmt.Errorf("phase %q is not one an approved change is stopped in before its promotion", s.Phase))
	}
	if s.RecordedAt.IsZero() {
		problems = append(problems, errors.New("recorded_at is required"))
	}
	return errors.Join(problems...)
}

// Describe says what the stop was, the way a docket entry or a listing reads it.
func (s IntegrationStop) Describe() string {
	return fmt.Sprintf("approved, then stopped at the %s phase by the environment: %s (%s)", s.Phase, s.Cause, s.Cause.Title())
}

// IntegrationResumption is one continuation of this run's integration after an
// environmental stop. It is the run's own account of having been made live
// again: which stop it superseded, why, and when. It is deliberately not an
// attempt — the repair count and the review evidence are untouched by it — and
// deliberately not a triage decision, because there was nothing to decide: the
// reviewer decided, and the environment got in the way.
type IntegrationResumption struct {
	// Cause is the environmental cause of the stop this resumption supersedes.
	Cause EnvironmentalCause `json:"cause"`
	// Reason is what the run records as why it is going again: the harness's own
	// account of the stop, and any reasoning given to the command that resumed it.
	Reason    string    `json:"reason"`
	ResumedAt time.Time `json:"resumed_at"`
	// SupersededFailure and SupersededBlocker are what the run ended on, in the
	// words it was recorded in. Re-entry clears both, because a run that is going
	// again has neither failed nor stopped; keeping the words here is what stops
	// the clearing losing the evidence of what stopped it.
	SupersededFailure string `json:"superseded_failure,omitempty"`
	SupersededBlocker string `json:"superseded_blocker,omitempty"`
}

// Validate reports every contract violation in the record at once.
func (r IntegrationResumption) Validate() error {
	var problems []error
	if !r.Cause.Valid() {
		problems = append(problems, fmt.Errorf("environmental cause %q is not one this harness records", r.Cause))
	}
	if strings.TrimSpace(r.Reason) == "" {
		problems = append(problems, errors.New("the reason this run's integration was resumed is required"))
	}
	if len(r.Reason) > MaxSelectionReasonBytes {
		problems = append(problems, fmt.Errorf("reason is %d bytes, which exceeds the %d byte bound", len(r.Reason), MaxSelectionReasonBytes))
	}
	if r.ResumedAt.IsZero() {
		problems = append(problems, errors.New("resumed_at is required"))
	}
	if len(r.SupersededFailure) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("superseded_failure is %d bytes, which exceeds the %d byte bound", len(r.SupersededFailure), MaxBlockerBytes))
	}
	if len(r.SupersededBlocker) > MaxBlockerBytes {
		problems = append(problems, fmt.Errorf("superseded_blocker is %d bytes, which exceeds the %d byte bound", len(r.SupersededBlocker), MaxBlockerBytes))
	}
	return errors.Join(problems...)
}

// validateIntegrationResume reports every contract violation in the record's
// account of an integration stop and of the resumptions after it.
func (s State) validateIntegrationResume() []error {
	var problems []error
	if s.IntegrationStop != nil {
		if err := s.IntegrationStop.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("integration_stop: %w", err))
		}
		// A stop is recorded on an approved change that was not promoted: it is what
		// makes the run resumable at its promotion, and a record carrying one beside
		// a promotion, or beside a verdict that was not an approval, describes a
		// resumption of something that either happened already or was never
		// authorized.
		if s.ReviewDecision != ReviewApprove {
			problems = append(problems, errors.New("integration_stop requires an approving review decision, which is what the resumed promotion is authorized by"))
		}
		if s.Integration != nil {
			problems = append(problems, errors.New("integration_stop cannot be recorded beside a promotion, which is what it says did not happen"))
		}
	}
	if len(s.IntegrationResumptions) > MaxIntegrationResumptions {
		problems = append(problems, fmt.Errorf("%d integration resumptions are recorded, which exceeds the bound of %d", len(s.IntegrationResumptions), MaxIntegrationResumptions))
	}
	for index, resumption := range s.IntegrationResumptions {
		if err := resumption.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("integration_resumptions[%d]: %w", index, err))
		}
	}
	return problems
}

// ApprovedAwaitingIntegration reports a run whose change the independent
// reviewer approved and whose promotion has not happened: the approval is
// standing, with the reviewer's session recorded as the evidence of it, and
// nothing has been integrated. It is the condition on which a stop is
// environmental rather than a verdict, whatever else the record says.
func (s State) ApprovedAwaitingIntegration() bool {
	if s.ReviewDecision != ReviewApprove || strings.TrimSpace(s.ReviewSessionID) == "" {
		return false
	}
	if s.Integration != nil {
		return false
	}
	return s.Phase == PhaseReviewing || s.Phase == PhaseIntegrating
}

// ResumableIntegration reports a stopped run whose integration may be resumed
// where it stopped: it ended, its approval is standing, the environment is what
// stopped it, and the branch and worktree that hold the approved change are
// still there. Everything else about whether it may be resumed now — the
// checkout being clean again, the worktree being as the harness left it, the
// item having no run in flight — is the resuming action's to ask, because it is
// about the moment rather than about the record.
func (s State) ResumableIntegration() bool {
	if !s.Status.Terminal() || s.IntegrationStop == nil || !s.ApprovedAwaitingIntegration() {
		return false
	}
	if s.WorktreePath == "" || s.Branch == "" || s.BaseCommit == "" || s.TargetBranch == "" {
		return false
	}
	return !s.WorktreeRemoved && !s.BranchRemoved
}

// ResumingIntegration reports a run that is at its promotion again after an
// environmental stop, which is what `yoyo status` says in the words of
// ResumingIntegrationSays: in flight, at the integrating phase, with the
// approval standing and a resumption recorded. A resumed run whose replay put it
// back through the checks and the review is at a different phase and is
// described as that phase; one that has re-earned its approval and is promoting
// again is resuming the same integration, and is described as such.
func (s State) ResumingIntegration() bool {
	if !s.Status.InFlight() || s.Phase != PhaseIntegrating || len(s.IntegrationResumptions) == 0 {
		return false
	}
	return s.ReviewDecision == ReviewApprove && s.Integration == nil
}

// LastIntegrationResumption is the most recent resumption of this run's
// integration, and whether there is one.
func (s State) LastIntegrationResumption() (IntegrationResumption, bool) {
	if len(s.IntegrationResumptions) == 0 {
		return IntegrationResumption{}, false
	}
	return s.IntegrationResumptions[len(s.IntegrationResumptions)-1], true
}
