package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// survival is a repository that answers one way for every run it is asked about.
type survival gitworktree.Survival

func (s survival) Survives(context.Context, gitworktree.Worktree) (gitworktree.Survival, error) {
	return gitworktree.Survival(s), nil
}

// The line an approved change the environment stopped ends on is decided by the
// repository the standing reading holds, asked the way the docket and the pull's
// hold ask it. A sink that catches the stop up after its branch was deleted names
// no resume — the verb would refuse — and says a re-run is the way on; while the
// branch is there it names the resume.
func TestTheLineForAnIntegrationStopAsksTheRepositoryWhetherTheBranchSurvives(t *testing.T) {
	t.Parallel()

	for _, branchThere := range []bool{false, true} {
		harness := newTestHarness(t, time.Time{})
		harness.feed.Standing = &readmodel.Sources{Remains: survival{BranchExists: branchThere, WorktreePresent: true}}
		state := harness.run(t, runstate.StatusFailed)
		state.Phase = runstate.PhaseIntegrating
		state.WorktreePath = "/state/worktrees/task"
		state.Branch = "yoyodyne/task/abc"
		state.BaseCommit = strings.Repeat("a", 40)
		state.TargetBranch = "main"
		state.ReviewSessionID = "reviewer-session"
		state.ReviewDecision = runstate.ReviewApprove
		state.Failure = "integrate approved change: primary checkout is not ready for integration: primary repository has uncommitted changes: AGENTS.md"
		state.IntegrationStop = &runstate.IntegrationStop{
			Cause:      runstate.CauseDirtyPrimary,
			Detail:     "integrate approved change: primary checkout is not ready for integration",
			Phase:      runstate.PhaseIntegrating,
			RecordedAt: moment,
		}
		harness.record(t, state)

		batch, err := harness.feed.Poll(context.Background(), harness.start())
		if err != nil {
			t.Fatalf("Poll() error = %v", err)
		}
		var body string
		for _, delivery := range batch.Deliveries {
			if delivery.Notification.Event.Kind != notify.KindRunEnded {
				continue
			}
			message, err := notify.Render(delivery.Notification.Topic, delivery.Notification.Speaker, delivery.Notification.Event)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			body = message.Body
		}
		if body == "" {
			t.Fatalf("branchThere=%t: no line said the run ended: %#v", branchThere, batch.Deliveries)
		}
		if branchThere {
			if !strings.Contains(body, "yoyo triage resume "+state.RunID) {
				t.Fatalf("the line does not name the resume while the branch is there:\n%s", body)
			}
			continue
		}
		if strings.Contains(body, "triage resume") {
			t.Fatalf("the line names the resume for a stop whose branch is gone:\n%s", body)
		}
		for _, want := range []string{"'s branch is gone", "checked and NOT there", "a re-run is the way on"} {
			if !strings.Contains(body, want) {
				t.Fatalf("the line does not say %q:\n%s", want, body)
			}
		}
	}
}
