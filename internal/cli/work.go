package cli

import (
	"context"
	"fmt"
	"time"

	"yoyodyne/internal/backlog"
	"yoyodyne/internal/beads"
	"yoyodyne/internal/chat"
	"yoyodyne/internal/orchestrator"
	"yoyodyne/internal/runstate"
)

// surveyedStatuses are the tracker slices a conversation reports, each asked
// for by name. A survey states what it found in every one of them, so an empty
// group is an answer rather than a gap.
var surveyedStatuses = []struct {
	status string
	// group is what the survey calls this slice, so a status that cannot be
	// read is reported as unknown exactly where it would have been listed.
	group string
	into  func(*chat.Survey, []chat.WorkItemSummary)
}{
	{"in_progress", "claimed", func(s *chat.Survey, items []chat.WorkItemSummary) { s.Claimed = items }},
	{"blocked", "blocked", func(s *chat.Survey, items []chat.WorkItemSummary) { s.Blocked = items }},
	{"open", "available", func(s *chat.Survey, items []chat.WorkItemSummary) { s.Available = items }},
	{"closed", "completed", func(s *chat.Survey, items []chat.WorkItemSummary) { s.Completed = items }},
}

// backlogStatuses are the tracker slices the backlog is assembled from: work
// that has been admitted and is not finished. Claimed work has been pulled
// already and closed work has left, so neither is still queued.
var backlogStatuses = []string{"open", "blocked"}

// conversationWork is the harness a product-manager conversation steers work
// with: the same pipeline `yoyodyne run` executes, the same reconciler
// `yoyodyne reconcile` settles with, and the same tracker and run state both
// read. Nothing here is a second path to development — it is the existing one,
// reachable from the conversation the operator is already in.
type conversationWork struct {
	tracker    beads.Client
	store      *runstate.Store
	pipeline   orchestrator.Pipeline
	reconciler orchestrator.Reconciler
	// timeout bounds one tracker command taken on the conversation's behalf, so
	// an unresponsive tracker delays an answer rather than hanging the prompt.
	timeout time.Duration
}

func newConversationWork(parts components) conversationWork {
	return conversationWork{
		tracker:    parts.tracker(),
		store:      parts.store,
		pipeline:   pipelineFrom(parts),
		reconciler: reconcilerFrom(parts),
		timeout:    chatTrackerTimeout,
	}
}

// Survey reads what the harness has in flight from durable run state and what
// the work looks like from the tracker. The two are separate questions: a run
// another process is executing is in the state whether or not the tracker has
// caught up with it.
// A part that cannot be read is named in the survey rather than failing it: an
// operator asking what is in flight is worse off with nothing than with the
// half the harness could answer, as long as the missing half is stated.
func (w conversationWork) Survey(ctx context.Context) (chat.Survey, error) {
	survey := chat.Survey{}
	incomplete, err := w.store.Incomplete()
	if err != nil {
		survey.Unavailable = append(survey.Unavailable, chat.Unread{Group: chat.InFlightGroup(), Reason: err.Error()})
	}
	for _, state := range incomplete {
		survey.InFlight = append(survey.InFlight, snapshotOf(state))
	}
	for _, group := range surveyedStatuses {
		items, err := w.list(ctx, group.status)
		if err != nil {
			survey.Unavailable = append(survey.Unavailable, chat.Unread{Group: group.group, Reason: err.Error()})
			continue
		}
		group.into(&survey, items)
	}
	return survey, nil
}

// Backlog reads the admitted work and puts it in the product manager's order.
// It is the same tracker the survey reads and the same ordering a development
// manager pulls in, assembled here rather than stored, so the queue can never
// drift from the priorities the product manager actually set.
//
// A slice that cannot be read fails the whole answer, where a survey would name
// it as unknown and report the rest. The difference is what the two are for: a
// survey describes what is happening, and a partial one is still worth reading,
// while a backlog says what comes next, and half a queue would answer that
// question wrongly rather than incompletely.
func (w conversationWork) Backlog(ctx context.Context) (backlog.Queue, error) {
	var admitted []beads.WorkItem
	for _, status := range backlogStatuses {
		items, err := w.listItems(ctx, status)
		if err != nil {
			return backlog.Queue{}, err
		}
		admitted = append(admitted, items...)
	}
	// The order comes from the listings; which of those items can actually be
	// pulled comes from the tracker, because a blocker lives in its dependency
	// graph rather than reliably in a status listing.
	trackerCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	ready, err := w.tracker.Ready(trackerCtx)
	if err != nil {
		return backlog.Queue{}, fmt.Errorf("list the work items the tracker reports as ready: %w", err)
	}
	pullable := make([]string, 0, len(ready))
	for _, item := range ready {
		pullable = append(pullable, item.ID)
	}
	return backlog.Order(admitted, pullable), nil
}

// Run executes one work item through the pipeline. The outcome is reported even
// when the run failed, because a failed run's branch, worktree, and blocker are
// what the operator decides about next.
func (w conversationWork) Run(ctx context.Context, workItemID string) (chat.RunReport, error) {
	outcome, err := w.pipeline.Run(ctx, workItemID)
	return runReportOf(outcome), err
}

// Changes reports what the most recent recorded run of a work item changed. It
// reads the durable run state and nothing else: no worktree is inspected and no
// git command is run, so the answer is the same whether the run finished a
// moment ago or was cleaned up weeks back and had its worktree and branch
// removed. Reading a record decides nothing about the run, so a run another
// process is executing is as readable here as a finished one.
func (w conversationWork) Changes(_ context.Context, workItemID string) (chat.RunChanges, error) {
	state, err := w.store.Latest(workItemID)
	if err != nil {
		return chat.RunChanges{}, err
	}
	return changesOf(state), nil
}

// Direct appends the operator's direction to the item's notes, which is where
// the next attempt at it reads it: the developer's context carries the item's
// notes, so direction recorded here reaches whoever picks the work up. It
// deliberately does not touch the item's status — saying what to do differently
// is not deciding the work is done or blocked.
func (w conversationWork) Direct(ctx context.Context, workItemID, note string) error {
	trackerCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	if _, err := w.tracker.RecordOutcome(trackerCtx, workItemID, note); err != nil {
		return err
	}
	return nil
}

// Settle runs the same reconciliation `yoyodyne reconcile` runs. It is safe to
// repeat and leaves alone every run a live process still holds, so settling
// after stopping one run never disturbs another.
func (w conversationWork) Settle(ctx context.Context) ([]chat.Settlement, error) {
	results, err := w.reconciler.Reconcile(ctx)
	settlements := make([]chat.Settlement, 0, len(results))
	for _, result := range results {
		settlements = append(settlements, chat.Settlement{
			RunID:      result.RunID,
			WorkItemID: result.WorkItemID,
			Action:     string(result.Action),
			Detail:     result.Detail,
			Failure:    result.Failure,
		})
	}
	return settlements, err
}

func (w conversationWork) list(ctx context.Context, status string) ([]chat.WorkItemSummary, error) {
	items, err := w.listItems(ctx, status)
	if err != nil {
		return nil, err
	}
	summaries := make([]chat.WorkItemSummary, 0, len(items))
	for _, item := range items {
		summaries = append(summaries, chat.WorkItemSummary{
			ID:       item.ID,
			Title:    item.Title,
			Status:   item.Status,
			Priority: item.Priority,
		})
	}
	return summaries, nil
}

// listItems asks the tracker for one status, bounded so an unresponsive tracker
// delays an answer rather than hanging the prompt.
func (w conversationWork) listItems(ctx context.Context, status string) ([]beads.WorkItem, error) {
	trackerCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	items, err := w.tracker.List(trackerCtx, status)
	if err != nil {
		return nil, fmt.Errorf("list %s work items: %w", status, err)
	}
	return items, nil
}

// snapshotOf describes one recorded run for a conversation. A run waiting out a
// provider usage limit is named as waiting rather than reported as work in
// progress, because those call for opposite responses from an operator.
func snapshotOf(state runstate.State) chat.RunSnapshot {
	snapshot := chat.RunSnapshot{
		RunID:      state.RunID,
		WorkItemID: state.WorkItemID,
		Status:     string(state.Status),
		Phase:      string(state.Phase),
		Branch:     state.Branch,
		StartedAt:  state.StartedAt,
	}
	switch {
	case state.UsageLimitResetsAt != nil:
		snapshot.Detail = fmt.Sprintf("paused for the %s usage limit until %s",
			nonEmptyValue(state.UsageLimitKind, "provider"), state.UsageLimitResetsAt.UTC().Format(time.RFC3339))
	case state.ProviderStop == runstate.ProviderStopStalled:
		snapshot.Detail = "its provider stopped emitting events and was stopped; the run can be continued"
	case state.ProviderStop == runstate.ProviderStopBudgetExhausted:
		snapshot.Detail = "its provider ran out of total budget while still working; the run can be continued"
	}
	return snapshot
}

// changesOf projects one recorded run into the account of what it changed. It
// claims nothing the record does not hold: the promotion comes from the
// recorded integration, the preserved worktree from the cleanup markers rather
// than from the path still being written down, and the change itself from the
// summary the run recorded while its worktree existed.
func changesOf(state runstate.State) chat.RunChanges {
	changes := chat.RunChanges{
		RunID:        state.RunID,
		WorkItemID:   state.WorkItemID,
		Status:       string(state.Status),
		Phase:        string(state.Phase),
		StartedAt:    state.StartedAt,
		CompletedAt:  state.CompletedAt,
		Branch:       state.Branch,
		WorktreePath: state.WorktreePath,
		Preserved:    state.WorktreePath != "" && !state.WorktreeRemoved,
		Failure:      state.Failure,
	}
	if state.Changes != nil {
		changes.Files = state.Changes.Files
		changes.DiffStat = state.Changes.DiffStat
	}
	if state.Integration != nil {
		changes.Integrated = true
		changes.TargetBranch = state.Integration.TargetBranch
		changes.Commit = state.Integration.TargetCommit
	}
	if state.PullRequest != nil {
		changes.PullRequest = &chat.PublishedChange{
			Number: state.PullRequest.Number,
			URL:    state.PullRequest.URL,
			State:  state.PullRequest.State,
			Merged: state.PullRequest.Merged,
		}
	}
	return changes
}

// runReportOf projects a pipeline outcome into what a conversation reports. It
// claims integration only from the recorded promotion itself, so a run that
// ended any other way is never described as integrated.
func runReportOf(outcome orchestrator.Outcome) chat.RunReport {
	report := chat.RunReport{
		RunID:              outcome.RunID,
		WorkItemID:         outcome.WorkItemID,
		Status:             string(outcome.Status),
		Branch:             outcome.Branch,
		WorktreePath:       outcome.WorktreePath,
		WorkItemClosed:     outcome.WorkItemClosed,
		RepairAttempts:     outcome.RepairAttempts,
		Blocked:            outcome.Blocked,
		Paused:             outcome.Paused,
		UsageLimitKind:     outcome.UsageLimitKind,
		UsageLimitResetsAt: outcome.UsageLimitResetsAt,
		ProviderStop:       outcome.ProviderStop,
		Failure:            outcome.Failure,
		Reported:           len(outcome.Reports),
		ReportProblem:      outcome.ReportProblem,
	}
	if outcome.Integration != nil {
		report.Integrated = true
		report.TargetBranch = outcome.Integration.TargetBranch
		report.Commit = outcome.Integration.TargetCommit
	}
	return report
}
