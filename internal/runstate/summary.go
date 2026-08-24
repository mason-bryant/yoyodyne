package runstate

// What became of a run is written down carefully and read back by almost
// nobody. A terminal record keeps the status, the phase the run died in, and
// the reason it gave, with the bookkeeping failures in fields of their own so a
// cleanup that could not finish never reads as a run that failed. Until this
// existed, reaching any of it meant the work item's notes one item at a time,
// or the run's own JSON.
//
// So this is the read side of exactly those records, and it decides nothing
// about them: reading a run is not acting on it, so a run a live process owns is
// summarized here as readily as one that ended months ago. What it costs comes
// from the same event logs the ledger prices from, and for the same reason it is
// unknown rather than zero when the evidence is gone.

import (
	"sort"
	"strings"
	"time"
)

// RunOutcome is what became of a run, in the fixed vocabulary every listing
// says it in. It exists because the durable status alone answers a different
// question from the one a listing is read for: "failed" is accurate about the
// attempt and says nothing about the work, so a run whose change is preserved
// and whose item is back with a person printed the same word as one that was
// cancelled and one that broke with nothing to show for it.
//
// The vocabulary is small and closed on purpose. A word per failure message
// would be a vocabulary nobody can learn, and the whole point of these is that a
// reader who has seen them once knows what the next line means without reading
// the reason under it.
type RunOutcome string

const (
	// OutcomeSucceeded is the run whose work landed.
	OutcomeSucceeded RunOutcome = "succeeded"
	// OutcomeStopped is the run that ended on a durable blocker: the item carries
	// it, a person decides what happens next, and nothing about the change was
	// discarded. Every stoppage the harness hands to somebody lands here — an
	// unrepaired review, a check that kept failing, refused paths, a replay the
	// target branch outran, a provider that would not carry the run — because what
	// they have in common is the fact a reader is asking about: the work is still
	// there and it is waiting on a decision. Which of them it was is the reason
	// printed under the run and the phase printed beside this word.
	OutcomeStopped RunOutcome = "stopped"
	// OutcomeCancelled is the run something stopped rather than judged: the
	// operator, or a killed process. Nothing here is a verdict on the change.
	OutcomeCancelled RunOutcome = "cancelled"
	// OutcomeTimedOut is the run the harness stopped on time, with nothing
	// recorded for anybody to decide.
	OutcomeTimedOut RunOutcome = "timed out"
	// OutcomeFailed is what is left: a run that ended without succeeding and
	// without leaving anybody a blocker to act on. It is the harness itself
	// having failed to carry the run, which is why it keeps the bare word.
	OutcomeFailed RunOutcome = "failed"
)

// Outcome reports what became of this run in that vocabulary. It is derived
// here, in the read model, rather than in each surface, so `yoyo status` and
// `yoyo cost` cannot come to say different words about one run.
//
// It draws only on distinctions the record already holds, so a run recorded long
// before this existed classifies exactly as a run recorded today: a recorded
// blocker is what says the item was handed back, and it outranks the status
// because a run blocked at a deadline is still a stoppage somebody owns. A run
// that has not reached a terminal status keeps its own status word, since
// "pending" and "running" are already the two facts a reader wants there.
func (s State) Outcome() RunOutcome {
	if !s.Status.Terminal() {
		return RunOutcome(s.Status)
	}
	switch {
	case s.Status == StatusSucceeded:
		return OutcomeSucceeded
	case strings.TrimSpace(s.Blocker) != "":
		return OutcomeStopped
	case s.Status == StatusCancelled:
		return OutcomeCancelled
	case s.Status == StatusTimedOut:
		return OutcomeTimedOut
	default:
		return OutcomeFailed
	}
}

// RunSummary is what one recorded run says about itself: how it ended, where it
// was when it did, what it gave as the reason, and what it spent getting there.
type RunSummary struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Status     Status `json:"status"`
	// Outcome is what became of the run in the fixed vocabulary above. It is
	// carried beside the status rather than instead of it: the status is the
	// durable value and stays what it always was, and this is the reading of it a
	// listing shows.
	Outcome     RunOutcome `json:"outcome"`
	Phase       Phase      `json:"phase,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// The artifacts the run leaves behind, and the harness's own record of which
	// of them it removed. They are here because "is my work gone" is the question
	// a listing of runs that did not succeed is actually read for, and a listing
	// that named neither the branch nor the worktree left it to be answered by
	// going and looking.
	Branch          string `json:"branch,omitempty"`
	WorktreePath    string `json:"worktree_path,omitempty"`
	BranchRemoved   bool   `json:"branch_removed,omitempty"`
	WorktreeRemoved bool   `json:"worktree_removed,omitempty"`
	// ProviderSessionID is the developer session preserved with the change, which
	// is what a continuation resumes in rather than deriving the work a second
	// time. It is named for the same reason the artifacts are.
	ProviderSessionID string `json:"provider_session_id,omitempty"`
	// ReviewFindings is how many findings the last review of this run recorded.
	// It says there is something written down to read rather than carrying the
	// findings themselves, which are the docket's to render in full.
	ReviewFindings int `json:"review_findings,omitempty"`
	// Integrated reports a run that promoted its work, which is what separates
	// the attempt that finished a piece of work from the ones that did not.
	Integrated bool `json:"integrated,omitempty"`
	// Outstanding reports a run that still owes somebody a step: one a process
	// was killed part-way through, or a finished one whose cleanup or queued
	// merge is unsettled. It is what `yoyo reconcile` acts on, and it is here
	// because a run that owes something and a run that is over look identical
	// from the status alone.
	Outstanding bool `json:"outstanding,omitempty"`
	// MergeQueued reports a merge the forge accepted but has not performed. It
	// is one of exactly two things a finished run can owe -- the other is the
	// cleanup that an integrated run short of the complete phase still owes --
	// so carrying it is what lets a reader say which of the two a run marked
	// outstanding is waiting on, rather than only that it is waiting.
	MergeQueued bool `json:"merge_queued,omitempty"`
	// Failure is the run's own reason for ending, and it is the only one of the
	// four recorded reasons that says the run failed. The three below happened
	// around the work rather than to it, and are kept apart here for the reason
	// they are kept apart in the record: a publication or a cleanup that could
	// not finish must never be read as a failed piece of work.
	Failure string `json:"failure,omitempty"`
	// FailingCheck is the deterministic check that was still failing when the
	// record was last written. It is what a repair attempt was handed, so on a
	// failed run it is usually the thing behind the reason rather than a second
	// reason.
	FailingCheck *CheckFailure `json:"failing_check,omitempty"`
	// RefusedPaths is the protected-path refusal that was still standing when the
	// record was last written. It sits beside the failing check because it is the
	// same kind of fact — what a repair attempt was handed, and usually the thing
	// behind the reason rather than a second reason — and because it is the one
	// the reader cannot go and reproduce: the worktree it describes is gone.
	RefusedPaths   *PathRefusal `json:"refused_paths,omitempty"`
	PublishFailure string       `json:"publish_failure,omitempty"`
	CleanupFailure string       `json:"cleanup_failure,omitempty"`
	// CompletionRecordingFailure is on the summary for the reason it is on the
	// state: the run record is the one durable home this failure class has.
	CompletionRecordingFailure string `json:"completion_recording_failure,omitempty"`
	// Selection is why the harness was running this item: who chose it and on
	// what grounds. It matters most for the runs nobody typed an identifier for,
	// which is why its absence is worth reporting rather than omitting — a run
	// nothing accounts for is indistinguishable from work happening behind the
	// operator's back, and a missing line reads as neither.
	Selection *Selection `json:"selection,omitempty"`
	// AccountAlias and ConfigRevision are which provider account the run spent
	// and which configuration set it up. They are here for the reason the
	// selection is: a run nobody can attribute to an account or to a
	// configuration is one whose cost and whose behaviour cannot be explained
	// afterwards, and there is one of each today only until there is not.
	AccountAlias   string `json:"account_alias,omitempty"`
	ConfigRevision string `json:"config_revision,omitempty"`
	// CostUSD is what the provider reported for every invocation in this run's
	// log, and UnknownCost says why there is no figure rather than reporting one
	// of zero: a run whose evidence is gone did not cost nothing.
	CostUSD     float64 `json:"cost_usd"`
	Invocations int     `json:"invocations,omitempty"`
	UnknownCost string  `json:"unknown_cost,omitempty"`
}

// Failed reports a run that reached a terminal status without succeeding.
// Cancelled and timed out belong here beside failed: what they have in common is
// that the work did not land, which is what somebody looking for the runs that
// went wrong is asking about, and each of them records why it ended.
func (r RunSummary) Failed() bool { return endedBadly(r.Status) }

// Preserved reports the run's change surviving the run: a branch or a worktree
// the harness has not recorded as removed. It asks about both because either one
// on its own still holds the work — a removed worktree whose branch stands is
// work somebody can still check out — and it is false for a run that never got
// as far as creating either, which is a run with nothing to preserve rather than
// one whose work was thrown away.
func (r RunSummary) Preserved() bool {
	return (r.Branch != "" && !r.BranchRemoved) || (r.WorktreePath != "" && !r.WorktreeRemoved)
}

// CostKnown reports a run the recorded evidence could actually price.
func (r RunSummary) CostKnown() bool { return r.UnknownCost == "" }

func endedBadly(status Status) bool {
	return status.Terminal() && status != StatusSucceeded
}

// RunQuery narrows which recorded runs a history covers.
type RunQuery struct {
	// WorkItemID keeps only the runs made for one work item, when it is set.
	WorkItemID string
	// FailedOnly keeps only the runs that ended without succeeding.
	FailedOnly bool
	// Limit is how many of the selected runs to report, newest first. Zero
	// reports all of them.
	Limit int
}

// RunHistory is the runs a query selected, and enough about what it selected
// them from that a limited listing never reads as the whole record.
type RunHistory struct {
	Runs []RunSummary `json:"runs"`
	// Matched is how many runs the query selected before the limit cut it down
	// to the runs above.
	Matched int `json:"matched"`
	// Recorded is how many runs the harness holds for this product at all, which
	// is what tells "nothing failed" from "nothing ran".
	Recorded int `json:"recorded"`
}

// History reports what became of the runs a query selects, newest first.
// Reading the record decides nothing about the runs, so a run another process is
// executing is summarized here exactly as a finished one is, with whatever its
// record and log hold so far.
func (s *Store) History(query RunQuery) (RunHistory, error) {
	states, err := s.scan("recorded", func(State) bool { return true })
	if err != nil {
		return RunHistory{}, err
	}
	history := RunHistory{Recorded: len(states), Runs: []RunSummary{}}
	workItemID := strings.TrimSpace(query.WorkItemID)
	matched := make([]State, 0, len(states))
	for _, state := range states {
		if workItemID != "" && state.WorkItemID != workItemID {
			continue
		}
		if query.FailedOnly && !endedBadly(state.Status) {
			continue
		}
		matched = append(matched, state)
	}
	// Newest first, unlike the price breakdown, which reads oldest first because
	// it is a history of one item. This is asked when something has gone wrong,
	// and what went wrong most recently is what is being asked about.
	sort.SliceStable(matched, func(first, second int) bool {
		if !matched[first].StartedAt.Equal(matched[second].StartedAt) {
			return matched[first].StartedAt.After(matched[second].StartedAt)
		}
		return matched[first].RunID < matched[second].RunID
	})
	history.Matched = len(matched)
	if query.Limit > 0 && len(matched) > query.Limit {
		matched = matched[:query.Limit]
	}
	// Only the runs actually reported are priced, because pricing one reads its
	// whole event log: a listing of the twenty most recent runs must not cost a
	// scan of every log the product has ever written.
	for _, state := range matched {
		history.Runs = append(history.Runs, s.summarize(state))
	}
	return history, nil
}

func (s *Store) summarize(state State) RunSummary {
	summary := RunSummary{
		RunID:             state.RunID,
		WorkItemID:        state.WorkItemID,
		Status:            state.Status,
		Outcome:           state.Outcome(),
		Phase:             state.Phase,
		StartedAt:         state.StartedAt,
		CompletedAt:       state.CompletedAt,
		Branch:            state.Branch,
		WorktreePath:      state.WorktreePath,
		BranchRemoved:     state.BranchRemoved,
		WorktreeRemoved:   state.WorktreeRemoved,
		ProviderSessionID: state.ProviderSessionID,
		ReviewFindings:    state.ReviewFindings,
		Integrated:        state.Integration != nil,
		Outstanding:       state.Outstanding(),
		MergeQueued:       state.PullRequest != nil && state.PullRequest.MergeQueued,
		AccountAlias:      state.AccountAlias,
		ConfigRevision:    state.ConfigRevision,
		Failure:           state.Failure,
		PublishFailure:    state.PublishFailure,
		CleanupFailure:    state.CleanupFailure,

		CompletionRecordingFailure: state.CompletionRecordingFailure,
	}
	if state.CheckFailure != nil {
		failing := *state.CheckFailure
		summary.FailingCheck = &failing
	}
	if state.PathRefusal != nil {
		refused := *state.PathRefusal
		summary.RefusedPaths = &refused
	}
	if state.Selection != nil {
		selection := *state.Selection
		summary.Selection = &selection
	}
	price := s.priceRun(state)
	summary.CostUSD = price.CostUSD
	summary.Invocations = price.Invocations
	summary.UnknownCost = price.Unknown
	return summary
}
