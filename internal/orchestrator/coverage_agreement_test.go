package orchestrator

// Whether an epic its children already cover can be started is one question, and
// two things answer it: the scheduling pass that decides what to pull, and the
// read model every operator surface projects. They disagreed. The pass passed a
// covered epic over and would never pull it, while the standing status went on
// reporting it as pullable and merely stalled — which sends whoever reads it to
// investigate a stall that is not one.
//
// The derivation is shared now, so the two cannot drift by accident. This is
// what holds them together anyway: one tracker, both readers, and the same words
// about the same item. The test lives beside the pass rather than beside the read
// model because whoever changes what the pass will not pull is who has to know
// the surfaces move with it.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The standing status over one scheduler harness's own records. The harness is
// the tracker, the runs in flight, and the intake hold already; the rest is a
// machine with nothing else wrong with it, so what either reader says about the
// epic is attributable to the coverage and to nothing else.
type coverageRuns struct{ *scheduleHarness }

func (coverageRuns) Outstanding() ([]runstate.State, error) { return nil, nil }

type coverageQuiet struct{}

func (coverageQuiet) Recorded() ([]runstate.Conversation, error) { return nil, nil }

func (coverageQuiet) InFlight(runstate.ConversationIdentity) (bool, error) { return false, nil }

func (coverageQuiet) Held() (runstate.OperatorHold, bool, error) {
	return runstate.OperatorHold{}, false, nil
}

type coverageDirectives struct{}

func (coverageDirectives) List() ([]directive.Directive, error) { return nil, nil }

type coverageAmendments struct{}

func (coverageAmendments) List() ([]amendment.Record, error) { return nil, nil }

// One session watching, which is the state the misleading line was read in: a
// live session over a queue it will never pull the epic from.
type coverageSessions struct{ at time.Time }

func (s coverageSessions) List() ([]runstate.WatchTransition, error) {
	return []runstate.WatchTransition{{SessionID: "watch-1", State: runstate.WatchWatching, At: s.at}}, nil
}

func TestTheStandingStatusRefusesTheEpicTheSchedulerWillNotPull(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		childStatus string
	}{
		{name: "a child still queued", childStatus: "open"},
		{name: "a child somebody is already running", childStatus: "in_progress"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			epic := beads.WorkItem{ID: "yoyodyne-ifd.121", Title: "A readable README", Status: "open", Priority: 1}
			// Parentage as an edge rather than as a field, which is the only shape
			// this project's own tracker ever states it in.
			child := beads.WorkItem{
				ID: "yoyodyne-ifd.121.2", Title: "Split it", Status: test.childStatus, Priority: 1,
				Dependencies: []beads.Dependency{
					{IssueID: "yoyodyne-ifd.121.2", ID: epic.ID, Type: "parent-child"},
				},
			}
			other := beads.WorkItem{ID: "yoyodyne-other", Title: "Something else", Status: "open", Priority: 2}
			harness := newScheduleHarness(epic, child, other)
			harness.capacity = 2

			// The standing status is read first, over the tracker the pass is about
			// to read: a pass that ran first would have closed the child and left
			// nothing covering the epic.
			standing := readmodel.ReadStanding(context.Background(), readmodel.Sources{
				Runs:          coverageRuns{harness},
				Conversations: coverageQuiet{},
				Tracker:       harness,
				Directives:    coverageDirectives{},
				Amendments:    coverageAmendments{},
				OperatorHolds: coverageQuiet{},
				IntakeHolds:   harness,
				Sessions:      coverageSessions{at: harness.clock().Add(-time.Hour)},
				Capacity:      harness.capacity,
			})
			if standing.NotStartableProblem != "" {
				t.Fatalf("not startable problem = %q, want the line fully read", standing.NotStartableProblem)
			}

			schedule, err := Scheduler{Open: harness.open, Limit: 1}.Schedule(context.Background())
			if err != nil {
				t.Fatalf("Schedule() error = %v", err)
			}

			deferred := ""
			for _, passedOver := range schedule.Deferred {
				if passedOver.WorkItemID == epic.ID {
					deferred = passedOver.Reason
				}
			}
			if deferred == "" {
				t.Fatalf("deferred = %#v, want the pass to have passed the covered epic over", schedule.Deferred)
			}
			refused := ""
			for _, notStartable := range standing.NotStartable {
				if notStartable.WorkItemID == epic.ID {
					refused = notStartable.Reason
				}
			}
			if refused == "" {
				t.Fatalf("not startable = %+v, want the surfaces to refuse what the pass will not pull:\n%s",
					standing.NotStartable, standing.Render())
			}
			if refused != deferred {
				t.Fatalf("the pass defers %s for %q and the surfaces refuse it for %q; "+
					"one question with two answers is what sends an operator after a stall that is not one",
					epic.ID, deferred, refused)
			}
			if !strings.Contains(refused, child.ID) {
				t.Fatalf("reason = %q, want the child that covers it named", refused)
			}
		})
	}
}
