package chat

// Correcting backlog state the records have made stale, and reporting the state
// that is held for a person instead.
//
// The product manager owns the queue, and three things in it stop describing the
// world without anybody having changed them: a status of blocked left over from
// a stoppage that ended, a dependency on work that closed, an attribution the
// goals no longer state. None of them is a decision anybody owes and every one
// of them used to wait for a person to notice, which is what this ends.
//
// It is deliberately not an update with a well-chosen argument. What the harness
// carries out here is not what the role says the item should say: it is a
// correction the records themselves have to support, judged against them at the
// moment of the act and refused where they still say the old state is right. So
// a repair asked for over a picture that has gone stale changes nothing, and an
// item somebody has to release is reported with its reason restated rather than
// touched.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/backlogrepair"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
)

// HeldWork is the harness's own account of the admitted work somebody still has
// to release: a stoppage nobody has decided about, and a change that exists only
// on a preserved branch. It is satisfied by the derivation every operator surface
// and the scheduler already read, so what a repair is refused for is the same
// hold that keeps the item out of the queue rather than a second opinion about
// it.
//
// A conversation wired without one can correct nothing: an unread account holds
// every item, which is the safe reading and the one the queue already takes.
type HeldWork interface {
	HeldForAPerson(ctx context.Context) (backlog.Holds, error)
}

// maxStaleStateListed bounds how many stale entries a survey lists per section.
// What the section is for is a pass over the classes, not an export: a queue
// with more than this to correct has something systemic behind it, and the count
// says so while the list stays readable.
const maxStaleStateListed = 25

// repairProblems checks a repair as far as one action can be checked: it names a
// kind of stale state, and it carries what that kind is corrected with. Whether
// anything about the item is actually stale needs the records rather than the
// action, so it is judged where the action is carried out.
//
// An argument the named state has no use for is refused rather than ignored, for
// the reason the action's own arguments are: a repair carrying the goal for a
// dependency it is unlinking was misunderstood, and carrying out the part that
// parsed would correct something nobody named.
func (a TrackerAction) repairProblems() []error {
	var problems []error
	state := backlogrepair.Class(strings.TrimSpace(string(a.State)))
	switch {
	case state == "":
		problems = append(problems, fmt.Errorf("repair requires \"state\", the kind of stale state to correct: %s", backlogrepair.NamedClasses()))
		return problems
	case !state.Valid():
		problems = append(problems, fmt.Errorf("repair state %q is not a kind of stale state; they are %s", a.State, backlogrepair.NamedClasses()))
		return problems
	}
	dependsOn := strings.TrimSpace(a.DependsOn)
	goal := strings.TrimSpace(a.Goal)
	switch state {
	case backlogrepair.ClassStatus:
		if dependsOn != "" {
			problems = append(problems, errors.New("repairing a status takes no \"depends_on\"; a dead dependency is repaired as \"dependency\""))
		}
		if goal != "" {
			problems = append(problems, errors.New("repairing a status takes no \"goal\"; an orphaned attribution is repaired as \"attribution\""))
		}
	case backlogrepair.ClassDependency:
		switch {
		case dependsOn == "":
			problems = append(problems, errors.New("repairing a dependency requires \"depends_on\", the closed item the link names"))
		case dependsOn == strings.TrimSpace(a.ID):
			problems = append(problems, errors.New("an item cannot depend on itself"))
		}
		if goal != "" {
			problems = append(problems, errors.New("repairing a dependency takes no \"goal\"; an orphaned attribution is repaired as \"attribution\""))
		}
	case backlogrepair.ClassAttribution:
		if dependsOn != "" {
			problems = append(problems, errors.New("repairing an attribution takes no \"depends_on\"; a dead dependency is repaired as \"dependency\""))
		}
		problems = append(problems, a.goalProblems()...)
	}
	return problems
}

// carryOutRepair corrects one piece of stale backlog state, having established
// from the records that it is stale.
//
// The order is the whole of the safety here. The item is read as the act runs
// rather than taken from the picture the reply was written against; the records
// it is judged against are read then too; and what is written onto the item is
// what changed, what made the old state stale, and why — so a correction made
// wrongly is legible afterwards rather than being an item that quietly changed
// state.
func (s *Session) carryOutRepair(ctx context.Context, outcome *TrackerOutcome) {
	action := outcome.Action
	id := strings.TrimSpace(action.ID)
	// The item in full, which is a second read and not the one every action makes
	// before it runs: that one records the state the tracker holds the item in,
	// and what decides a repair is the item's dependencies and its notes, which it
	// does not carry.
	item, err := s.options.Tracker.Show(ctx, id)
	if err != nil {
		outcome.fail(fmt.Errorf("read %s to judge whether the state named is stale: %w", id, err))
		return
	}
	records, err := s.repairRecords(ctx, action, item)
	if err != nil {
		outcome.fail(err)
		return
	}
	repair, err := backlogrepair.Judge(item, action.State, action.DependsOn, records)
	if err != nil {
		outcome.fail(err)
		return
	}
	note := s.trackerProvenance(repairedWhat(repair), action.Reason) +
		"\n\nWhat made the old state stale: " + repair.Stale
	switch repair.Class {
	case backlogrepair.ClassStatus:
		if _, err := s.options.Tracker.Unblock(ctx, id, note); err != nil {
			outcome.fail(err)
			s.settleTrackerNote(ctx, outcome, id, note, "the account of why the blocked status was stale")
			return
		}
		outcome.applied("cleared %s's blocked status, which nothing unfinished stood behind; it is open and in the order its priority puts it", id)
	case backlogrepair.ClassDependency:
		if err := s.options.Tracker.RemoveBlocker(ctx, id, repair.DependsOn); err != nil {
			outcome.fail(err)
			return
		}
		// The link is gone from here on, so what it was and why it was removed are
		// written onto the outcome before the note that says so can fail: an act
		// reported as having changed nothing, with the dependency already retired,
		// is an act asked for again over a link that is no longer there.
		outcome.noteLanded("the dependency of %s on %s was removed", id, repair.DependsOn)
		if _, err := s.options.Tracker.Update(ctx, id, beads.WorkItemChange{AppendNotes: note}); err != nil {
			outcome.fail(err)
			s.settleTrackerNote(ctx, outcome, id, note, "the account of why the dependency was dead")
			return
		}
		outcome.applied("retired %s's dependency on %s, which the tracker holds as closed", id, repair.DependsOn)
	case backlogrepair.ClassAttribution:
		attributed := strings.TrimSpace(action.Goal)
		change := beads.WorkItemChange{AppendNotes: note + "\n\n" + s.options.Goals.NoteFor(attributed)}
		if _, err := s.options.Tracker.Update(ctx, id, change); err != nil {
			outcome.fail(err)
			s.settleTrackerNote(ctx, outcome, id, note, "the re-attribution and why the old one was orphaned")
			return
		}
		outcome.applied("re-attributed %s, whose recorded goal the goals no longer state, to: %s",
			id, singleLine(attributed, maxTrackerFailureBytes))
	}
}

// repairedWhat is what the item's own record says was done to it. It names the
// act rather than the class, because the item is read by somebody asking what
// happened to it rather than by somebody who knows this vocabulary.
func repairedWhat(repair backlogrepair.Repair) string {
	switch repair.Class {
	case backlogrepair.ClassStatus:
		return "Blocked status cleared as stale"
	case backlogrepair.ClassDependency:
		return "Dead dependency on " + repair.DependsOn + " retired"
	case backlogrepair.ClassAttribution:
		return "Re-attributed after the goal it named stopped resolving"
	default:
		return "Stale backlog state corrected"
	}
}

// repairRecords reads what a repair is judged against: the admitted work, what
// the harness is holding for a person, the directives in force, and the goals.
//
// A reading that fails is an error rather than a thinner judgement. Every one of
// these records can only refuse a repair or leave it standing, so a failure that
// carried on would be the one thing that must not happen here — an act allowed
// because the record that would have refused it could not be read.
func (s *Session) repairRecords(ctx context.Context, action TrackerAction, item beads.WorkItem) (backlogrepair.Records, error) {
	records := backlogrepair.Records{Goals: s.options.Goals}
	admitted, err := s.admittedWork(ctx)
	if err != nil {
		return backlogrepair.Records{}, err
	}
	records.Admitted = admitted
	if s.options.Held == nil {
		return backlogrepair.Records{}, errors.New("this conversation cannot read what the harness is holding for a person, so it cannot tell a stale status from a stoppage somebody has to decide about; nothing was corrected")
	}
	held, err := s.options.Held.HeldForAPerson(ctx)
	if err != nil {
		return backlogrepair.Records{}, fmt.Errorf("read what the harness is holding for a person: %w", err)
	}
	records.Held = held
	directives, err := s.recordedDirectives(ctx)
	if err != nil {
		return backlogrepair.Records{}, err
	}
	records.Directives = directives
	// A dependency is retired on the tracker's word that the work it names is
	// finished, and the item is read for it rather than inferred from being
	// absent: an identifier the queue does not carry may be closed, or claimed, or
	// a name nothing answers to, and only one of those is a dead link.
	switch action.State {
	case backlogrepair.ClassDependency:
		dependsOn := strings.TrimSpace(action.DependsOn)
		blocker, err := s.options.Tracker.Show(ctx, dependsOn)
		if err != nil {
			return backlogrepair.Records{}, fmt.Errorf("read %s to judge whether the dependency on it is dead: %w", dependsOn, err)
		}
		if strings.TrimSpace(blocker.Status) == closedWorkItemStatus {
			records.Finished = map[string]struct{}{blocker.ID: {}}
		}
	case backlogrepair.ClassStatus:
		// And a status is cleared on the same evidence about every link the item
		// records, for the same reason: the listings above are the open work and
		// the blocked work, so a blocker somebody is running right now is in
		// neither, and reading its absence as "finished" would clear a status that
		// is telling the truth.
		finished, err := s.finishedBlockers(ctx, item)
		if err != nil {
			return backlogrepair.Records{}, err
		}
		records.Finished = finished
	}
	return records, nil
}

// finishedBlockers reads the work behind each link the item's own listing does
// not settle, and reports those the tracker holds as closed.
//
// Only the unsettled links are read. A link the listing already calls closed
// needs no read, and one whose item is still in the admitted queue is a wait the
// judgement refuses on without this — so what this costs is one tracker call per
// link whose fate nothing else says, which is the only case where reading it
// decides anything.
func (s *Session) finishedBlockers(ctx context.Context, item beads.WorkItem) (map[string]struct{}, error) {
	finished := map[string]struct{}{}
	for _, dependency := range item.Dependencies {
		if dependency.Type != beads.BlocksDependency {
			continue
		}
		id := strings.TrimSpace(dependency.ID)
		if id == "" || strings.TrimSpace(dependency.Status) != "" {
			continue
		}
		blocker, err := s.options.Tracker.Show(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("read %s to judge whether it still blocks %s: %w", id, item.ID, err)
		}
		if strings.TrimSpace(blocker.Status) == closedWorkItemStatus {
			finished[id] = struct{}{}
		}
	}
	return finished, nil
}

// admittedWork is the queue as the tracker holds it now: the open work and the
// blocked work, which is what says whether anything unfinished stands behind a
// status or a link. The blocked half is the half a survey of open items never
// sees, and it is exactly where a stale status is.
func (s *Session) admittedWork(ctx context.Context) ([]beads.WorkItem, error) {
	var admitted []beads.WorkItem
	for _, status := range []string{openWorkItemStatus, blockedWorkItemStatus} {
		items, err := s.options.Tracker.List(ctx, status)
		if err != nil {
			return nil, fmt.Errorf("list the %s work items to judge what is still unfinished: %w", status, err)
		}
		admitted = append(admitted, items...)
	}
	return admitted, nil
}

// recordedDirectives is the directives as the record holds them.
//
// A conversation with no record to read them from corrects nothing, exactly as
// one that cannot read the holds does. A directive in force is one of the three
// governance holds, and the two halves of a hold must fail the same way: an
// unwired directive record that answered "none in force" would be the hold
// reading deciding from what it could not see, which is the one thing this
// boundary must never do. A conversation nothing could have recorded a directive
// through is still one where a directive recorded elsewhere reaches the same
// product, so an empty answer is not derivable from the wiring either.
func (s *Session) recordedDirectives(ctx context.Context) ([]directive.Directive, error) {
	if s.options.Directives == nil {
		return nil, errors.New("this conversation cannot read the recorded directives, so it cannot tell an item nothing is holding from one a directive pauses; nothing was corrected")
	}
	recorded, err := s.options.Directives.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the recorded directives to judge whether one pauses the item: %w", err)
	}
	return recorded, nil
}

// renderStaleBacklogState is what a survey says about the state the records have
// made stale, and about the state somebody has to release before anything
// touches it.
//
// It is part of the survey rather than a listing of its own because the pass
// that acts on this is the pass that takes a survey: state nobody is shown is
// state that waits for a person to go looking, which is what this whole
// capability exists to stop. A reading that failed says so and lists nothing,
// for the reason the repairs themselves refuse: a section that fell back to
// silence would read as a queue with nothing stale in it.
func (s *Session) renderStaleBacklogState(ctx context.Context, open []beads.WorkItem) string {
	// Only the role that may correct it is shown it. A survey is read by every
	// role that may read the tracker, and stale state in front of a role that
	// cannot act on it is work to route to somebody else — which is the operator
	// standing in the middle again, in the one place this was meant to be got out
	// of.
	if !slices.Contains(s.authority().TrackerActions, actionRepair) {
		return ""
	}
	records, err := s.surveyRecords(ctx, open)
	if err != nil {
		return fmt.Sprintf("\nWhat the records have made stale was not judged: %s\n", singleLine(err.Error(), maxTrackerFailureBytes))
	}
	report := backlogrepair.Survey(records)
	if !report.Anything() {
		return "\nNothing in the admitted work is state the records have made stale, and nothing is being held back from a repair.\n"
	}
	var rendered strings.Builder
	if len(report.Repairs) > 0 {
		fmt.Fprintf(&rendered, "\nState the records have made stale, which \"repair\" corrects (%d):\n", len(report.Repairs))
		for _, repair := range listedRepairs(report.Repairs) {
			fmt.Fprintf(&rendered, "- %s [%s] %s\n", repair.WorkItemID, repair.Class, repair.Stale)
		}
		if cut := len(report.Repairs) - maxStaleStateListed; cut > 0 {
			fmt.Fprintf(&rendered, "%d further stale entrie(s) are not listed here.\n", cut)
		}
	}
	if len(report.Holds) > 0 {
		// The held work is listed whether or not anything is repairable, and it is
		// listed as what it is: work whose state looks exactly as stale as the
		// entries above and which somebody still has to release. Correcting one
		// would release a stoppage nobody has decided about, so it is reported and
		// left alone every pass, with the reason restated each time.
		fmt.Fprintf(&rendered, "\nHeld for a person, so its state is reported rather than corrected (%d):\n", len(report.Holds))
		for _, held := range listedHolds(report.Holds) {
			fmt.Fprintf(&rendered, "- %s [%s] %s; held because %s\n", held.WorkItemID, held.Class, held.Stale, held.Reason)
		}
		if cut := len(report.Holds) - maxStaleStateListed; cut > 0 {
			fmt.Fprintf(&rendered, "%d further held entrie(s) are not listed here.\n", cut)
		}
	}
	return boundText(rendered.String(), maxTrackerSurveyBytes)
}

// surveyRecords is what a survey judges staleness against. It reuses the open
// items the survey already read rather than listing them twice, and reads the
// blocked half beside them: a survey of open work cannot see a stale status,
// because a stale status is what keeps an item out of that listing.
func (s *Session) surveyRecords(ctx context.Context, open []beads.WorkItem) (backlogrepair.Records, error) {
	records := backlogrepair.Records{Goals: s.options.Goals, Admitted: open}
	blocked, err := s.options.Tracker.List(ctx, blockedWorkItemStatus)
	if err != nil {
		return backlogrepair.Records{}, fmt.Errorf("list the blocked work items: %w", err)
	}
	records.Admitted = append(append([]beads.WorkItem(nil), open...), blocked...)
	if s.options.Held == nil {
		return backlogrepair.Records{}, errors.New("this conversation cannot read what the harness is holding for a person")
	}
	held, err := s.options.Held.HeldForAPerson(ctx)
	if err != nil {
		return backlogrepair.Records{}, fmt.Errorf("read what the harness is holding for a person: %w", err)
	}
	records.Held = held
	directives, err := s.recordedDirectives(ctx)
	if err != nil {
		return backlogrepair.Records{}, err
	}
	records.Directives = directives
	return records, nil
}

func listedRepairs(repairs []backlogrepair.Repair) []backlogrepair.Repair {
	if len(repairs) > maxStaleStateListed {
		return repairs[:maxStaleStateListed]
	}
	return repairs
}

func listedHolds(holds []backlogrepair.Held) []backlogrepair.Held {
	if len(holds) > maxStaleStateListed {
		return holds[:maxStaleStateListed]
	}
	return holds
}
