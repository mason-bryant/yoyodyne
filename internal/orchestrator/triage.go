package orchestrator

// Docketing the work that has stopped moving.
//
// Two things stop work in a way no further attempt of the harness resolves. A
// run ends on a durable blocker — the repair budget spent, a replay that
// conflicts, a provider that kept refusing — and an approved change is
// published to a forge that then never merges it. The first is an event the
// harness is present for, so it is docketed where it happens. The second is the
// absence of an event, which nothing can be present for, so it is found by a
// scan: the reconciling sweep, and the build that runs when the development
// manager opens a conversation.
//
// The scan is deliberately not a scheduled process, because there is none to
// hang it on. What that costs is stated rather than hidden: the configured
// stuck-merge age is a floor and not a promise. A publication becomes
// docketable at that age and is docketed the next time something scans, which
// on a quiet system is the next sweep or the next conversation.
//
// Both paths converge on one idempotent write keyed to the event, so a run that
// dockets its own stoppage and a sweep that settles the same run afterwards
// produce one entry between them rather than two accounts of one stoppage.

import (
	"errors"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// Docket is the durable docket entries are recorded into and read back from.
// It is satisfied by runstate.DocketStore.
type Docket interface {
	RecordOnce(entry triage.Entry) (bool, error)
	List() ([]triage.Entry, error)
}

// DocketRuns is the run evidence a docket is built from: every record the
// harness holds, and the review rounds one work item has accumulated across
// them. Reading either decides nothing about any run, which is what lets a
// build run beside whatever else is happening.
type DocketRuns interface {
	Recorded() ([]runstate.State, error)
	ReviewRounds(workItemID string) (int, error)
}

// Docketer makes and reads the triage docket. It has no tracker, no worktree
// access, and no forge access, and that is the point: docketing is a statement
// that work stopped, assembled from evidence somebody already recorded. What to
// do about a docketed entry is the development manager's, and nothing here can
// claim, repair, escalate, or retire anything.
type Docketer struct {
	Docket Docket
	Runs   DocketRuns
	// Triage is what the docket measures against: the age past which an unmerged
	// publication is stuck, and the budgets every entry reports beside what the
	// item has already spent.
	Triage config.Triage
	Clock  execution.Clock
}

// DocketBuild is what one build found: the whole docket as it now stands, and
// how many entries this build is what created. The count is reported rather
// than the entries themselves because a build is not a notification — an entry
// created by this build and one created by last week's sweep are the same
// standing fact to whoever reads the docket.
type DocketBuild struct {
	Entries []triage.Entry `json:"entries"`
	Added   int            `json:"added"`
}

// Build scans every recorded run, dockets what has stopped and is not docketed
// yet, and returns the docket as it stands. It is safe to repeat and safe to
// run concurrently with anything: every write is keyed to the event it
// describes, so a build that races another build records the same entries and
// the docket collapses them.
//
// A run whose record cannot supply an entry is skipped rather than failing the
// build: a docket that refuses to be read because one run is odd is a docket
// nobody sees, and the runs beside it are exactly the ones somebody needs.
func (d Docketer) Build() (DocketBuild, error) {
	if err := d.validate(); err != nil {
		return DocketBuild{}, err
	}
	recorded, err := d.Runs.Recorded()
	if err != nil {
		return DocketBuild{}, fmt.Errorf("read the recorded runs to build the triage docket: %w", err)
	}
	// What is already docketed is read once rather than once per candidate. The
	// store refuses a repeated key anyway; this is what stops a build over a long
	// history from re-reading the whole docket for every run in it.
	docketed, err := d.Docket.List()
	if err != nil {
		return DocketBuild{}, fmt.Errorf("read the triage docket: %w", err)
	}
	already := make(map[string]bool, len(docketed))
	for _, entry := range docketed {
		already[entry.Key] = true
	}
	now := d.now()
	added := 0
	var problems []error
	for _, state := range recorded {
		entries, err := d.entriesFor(state, now, already)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		for _, entry := range entries {
			created, err := d.Docket.RecordOnce(entry)
			if err != nil {
				problems = append(problems, fmt.Errorf("docket %s of run %s: %w", entry.Class, state.RunID, err))
				continue
			}
			if created {
				added++
			}
		}
	}
	entries, err := d.Docket.List()
	if err != nil {
		problems = append(problems, fmt.Errorf("read the triage docket: %w", err))
		return DocketBuild{Added: added}, errors.Join(problems...)
	}
	return DocketBuild{Entries: entries, Added: added}, errors.Join(problems...)
}

// RecordStoppedRun dockets one run that ended on a durable blocker, at the
// moment it ended. It reports whether this call is what created the entry, so a
// caller can tell docketing a stoppage from finding it already docketed.
//
// A run with no recorded blocker dockets nothing and is not an error: that is
// every run that ended for a reason nobody has to decide about, which is most
// of them.
func (d Docketer) RecordStoppedRun(state runstate.State) (bool, error) {
	if err := d.validate(); err != nil {
		return false, err
	}
	if !stoppedRun(state) {
		return false, nil
	}
	entry, err := d.stoppedRunEntry(state, d.now())
	if err != nil {
		return false, err
	}
	return d.Docket.RecordOnce(entry)
}

// entriesFor is what one run record contributes to the docket, leaving out
// what is already on it. A run can contribute both: a stoppage is about the run
// and a stuck publication is about what the forge did with its work, and one
// run can have done both.
func (d Docketer) entriesFor(state runstate.State, now time.Time, already map[string]bool) ([]triage.Entry, error) {
	var entries []triage.Entry
	var problems []error
	if stoppedRun(state) && !already[triage.Key(triage.ClassStoppedRun, state.RunID)] {
		entry, err := d.stoppedRunEntry(state, now)
		if err != nil {
			problems = append(problems, err)
		} else {
			entries = append(entries, entry)
		}
	}
	if already[triage.Key(triage.ClassPublication, state.RunID)] {
		return entries, errors.Join(problems...)
	}
	if stuckPublication(state, now, d.Triage.StuckMergeAge.Duration()) {
		entry, err := d.publicationEntry(state, now)
		if err != nil {
			problems = append(problems, err)
		} else {
			entries = append(entries, entry)
		}
	}
	return entries, errors.Join(problems...)
}

// stoppedRun reports a run that ended on a durable blocker. Both halves matter.
// The blocker is what says a person has to decide something, and the terminal
// status is what says nobody is going to: a run that is still going, or parked
// waiting out a provider, an operator, or a directive, is owed a continuation
// and is not stopped work at all — every one of those pauses is recorded on a
// run that is still running, which is exactly what this excludes.
func stoppedRun(state runstate.State) bool {
	return state.Blocker != "" && state.Status.Terminal()
}

// stuckPublication reports an approved publication that did not finish and is
// not going to without somebody looking at it. There are two kinds, and they
// are one class because they need the same thing from the same reader.
//
// The first is a publication the harness already recorded as outstanding: a
// merge the forge dropped because a requirement of the base branch went unmet,
// which the harness never merges past, or one the forge performed that the
// harness could not then confirm. Nothing about either changes with time, so
// both are docketable the moment they are recorded.
//
// The second is a publication nothing has recorded anything about, which is
// what a merge that is simply not happening looks like. Nothing happened to it,
// so there is no event to hang a deadline on, and it is docketed on its age
// instead — measured from when the run that made it ended, which is when it
// became something waiting on the forge.
func stuckPublication(state runstate.State, now time.Time, stuckMergeAge time.Duration) bool {
	published := state.PullRequest
	if published == nil {
		return false
	}
	// A run still in flight owns its own publication, and a parked one is owed
	// the rest of its own step. Neither is work that has stopped.
	if !state.Status.Terminal() {
		return false
	}
	// Only an approved publication is stuck. A pull request from a run that was
	// never approved is a branch nobody authorized merging, and the run's own
	// blocker is what says so.
	if state.ReviewDecision != runstate.ReviewApprove {
		return false
	}
	// An outstanding publication the harness recorded is docketable whichever
	// way the merge went, and there are two of those: a merge the forge dropped,
	// and one it performed that the harness could not confirm. The second is the
	// reason this is asked before the merged publication below is dismissed —
	// the merge happened and the publication still did not finish.
	if state.PublishFailure != "" {
		return true
	}
	// Past that, a merged publication is finished work.
	if published.Merged {
		return false
	}
	// A threshold of no time at all would docket every publication the instant
	// it was made, which the configuration refuses; this refuses to act on one
	// anyway, because a docket built from a configuration nobody validated must
	// not be a docket of everything.
	if stuckMergeAge <= 0 {
		return false
	}
	return now.Sub(publicationApprovedAt(state)) >= stuckMergeAge
}

// publicationApprovedAt is when the publication became something waiting on the
// forge. It is the moment the run ended rather than the moment the record was
// last touched: a sweep that walks past a stuck publication and writes nothing
// must not be able to reset its age, and one that does write must not either.
func publicationApprovedAt(state runstate.State) time.Time {
	if state.CompletedAt != nil {
		return *state.CompletedAt
	}
	return state.UpdatedAt
}

func (d Docketer) stoppedRunEntry(state runstate.State, now time.Time) (triage.Entry, error) {
	counters, err := d.counters(state)
	if err != nil {
		return triage.Entry{}, err
	}
	entry := triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.Key(triage.ClassStoppedRun, state.RunID),
		Class:         triage.ClassStoppedRun,
		ProductID:     state.ProductID,
		RunID:         state.RunID,
		WorkItemID:    state.WorkItemID,
		RecordedAt:    now.UTC(),
		Blocker:       state.Blocker,
		Summary:       state.ReviewSummary,
		Findings:      docketFindings(state.ReviewFindingDetails),
		Check:         docketCheck(state.CheckFailure),
		Artifacts:     docketArtifacts(state),
		Counters:      counters,
	}
	if err := entry.Validate(); err != nil {
		return triage.Entry{}, fmt.Errorf("docket the stoppage of run %s: %w", state.RunID, err)
	}
	return entry, nil
}

func (d Docketer) publicationEntry(state runstate.State, now time.Time) (triage.Entry, error) {
	counters, err := d.counters(state)
	if err != nil {
		return triage.Entry{}, err
	}
	published := *state.PullRequest
	entry := triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.Key(triage.ClassPublication, state.RunID),
		Class:         triage.ClassPublication,
		ProductID:     state.ProductID,
		RunID:         state.RunID,
		WorkItemID:    state.WorkItemID,
		RecordedAt:    now.UTC(),
		Summary:       state.ReviewSummary,
		Findings:      docketFindings(state.ReviewFindingDetails),
		Artifacts:     docketArtifacts(state),
		Publication: &triage.Publication{
			Number:      published.Number,
			URL:         published.URL,
			Branch:      published.Branch,
			HeadCommit:  published.HeadCommit,
			State:       published.State,
			Merged:      published.Merged,
			MergeQueued: published.MergeQueued,
			MergeMethod: published.MergeMethod,
			MergeCommit: published.MergeCommit,
			Message:     state.PublishFailure,
			ApprovedAt:  publicationApprovedAt(state).UTC(),
		},
		Counters: counters,
	}
	if err := entry.Validate(); err != nil {
		return triage.Entry{}, fmt.Errorf("docket the unmerged publication of run %s: %w", state.RunID, err)
	}
	return entry, nil
}

// counters are what the item has already spent beside what it is allowed to
// spend. The rounds are read across every run made for the item, because that
// is what the configured cap bounds; everything else is this run's.
func (d Docketer) counters(state runstate.State) (triage.Counters, error) {
	rounds, err := d.Runs.ReviewRounds(state.WorkItemID)
	if err != nil {
		return triage.Counters{}, fmt.Errorf("count the review rounds of %s: %w", state.WorkItemID, err)
	}
	return triage.Counters{
		ReviewRounds:        rounds,
		ReviewRoundsCap:     d.Triage.ReviewRoundsCap,
		RepairAttempts:      state.RepairAttempts,
		RepairGrantAttempts: d.Triage.RepairGrantAttempts,
	}, nil
}

func docketFindings(findings []runstate.Finding) []triage.Finding {
	if len(findings) == 0 {
		return nil
	}
	// The reviewer's words are copied rather than summarized: what makes a
	// finding worth carrying to a development manager is the reviewer's own
	// account of it, and a paraphrase is a second opinion nobody asked for.
	docketed := make([]triage.Finding, 0, len(findings))
	for _, finding := range findings {
		docketed = append(docketed, triage.Finding{
			Severity: finding.Severity,
			Message:  finding.Message,
			File:     finding.File,
			Line:     finding.Line,
		})
	}
	return docketed
}

func docketCheck(failure *runstate.CheckFailure) *triage.Check {
	if failure == nil {
		return nil
	}
	return &triage.Check{Command: failure.Command, ExitCode: failure.ExitCode, Output: failure.Output}
}

func docketArtifacts(state runstate.State) triage.Artifacts {
	return triage.Artifacts{
		Branch:          state.Branch,
		WorktreePath:    state.WorktreePath,
		TargetBranch:    state.TargetBranch,
		BaseCommit:      state.BaseCommit,
		BranchRemoved:   state.BranchRemoved,
		WorktreeRemoved: state.WorktreeRemoved,
	}
}

func (d Docketer) validate() error {
	var problems []error
	if d.Docket == nil {
		problems = append(problems, errors.New("a triage docket is required"))
	}
	if d.Runs == nil {
		problems = append(problems, errors.New("the recorded runs are required to build a triage docket"))
	}
	return errors.Join(problems...)
}

func (d Docketer) now() time.Time {
	if d.Clock == nil {
		return execution.RealClock{}.Now()
	}
	return d.Clock.Now()
}
