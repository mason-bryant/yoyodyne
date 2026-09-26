package slack

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

const pgmConversation = "chat-00000000000000000000000000000001"

// pgmConversations is one program manager instance's conversation, which is
// what its passes are attributed to it through.
type pgmConversations struct{ started time.Time }

func (c pgmConversations) Recorded() ([]runstate.Conversation, error) {
	return []runstate.Conversation{{ConversationID: pgmConversation, Agent: "factory-pgm", Role: domain.RoleProgramManager, StartedAt: c.started}}, nil
}

func (pgmConversations) InFlight(runstate.ConversationIdentity) (bool, error) { return false, nil }

// pgmPasses is the pass log holding one completed pass in that conversation.
type pgmPasses struct{ ended time.Time }

func (p pgmPasses) List() ([]runstate.Sweep, []runstate.UnreadableSweep, error) {
	return []runstate.Sweep{{
		Task: "pgm-pass", Role: domain.RoleProgramManager, ConversationID: pgmConversation,
		StartedAt: p.ended.Add(-time.Minute), EndedAt: p.ended, Turns: 1, Result: &sweep.Result{Summary: "moving"},
	}}, nil, nil
}

// staleInstanceSources is a standing with one hourly program manager whose last
// completed pass ended at the moment given.
func staleInstanceSources(harness *testHarness, lastPass time.Time) *readmodel.Sources {
	return &readmodel.Sources{
		Runs:            harness.runs,
		Conversations:   pgmConversations{started: lastPass.Add(-time.Hour)},
		Tracker:         standingTracker{},
		Directives:      harness.directives,
		Amendments:      harness.amend,
		OperatorHolds:   harness.holds,
		IntakeHolds:     harness.intake,
		Sessions:        harness.watch,
		ProgramManagers: []readmodel.ProgramManagerInstance{{Agent: "factory-pgm", Lane: "reliability", Every: time.Hour}},
		Passes:          pgmPasses{ended: lastPass},
		Capacity:        1,
		Now:             func() time.Time { return harness.now },
	}
}

// The hourly line counts a stale program manager where it already posts, from
// the read model's own derivation.
func TestTheHourlyLineCountsAStaleProgramManagerWhereItAlreadyPosts(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	held := harness.now
	harness.hold(t, "reordering the backlog first", held)
	harness.feed.Standing = staleInstanceSources(harness, held.Add(-3*time.Hour))

	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)
	harness.now = held.Add(2 * time.Hour)
	said := harness.say(t, cursors, notify.KindLineWaiting)
	if !strings.Contains(said.Body, "Program managers stale: 1 of 1 (factory-pgm)") {
		t.Fatalf("body %q does not count the stale program manager", said.Body)
	}
}

// A stale program manager alone posts nothing: the operator reviews an
// instance when he chooses, and the channel pushes nothing for one.
func TestAStaleProgramManagerAlonePostsNothing(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchWatching, "", moment)
	harness.feed.Standing = staleInstanceSources(harness, harness.now.Add(-3*time.Hour))

	cursors := harness.poll(t, harness.start())
	harness.now = harness.now.Add(3 * time.Hour)
	harness.poll(t, cursors)
}
