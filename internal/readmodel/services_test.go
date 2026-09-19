package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeSupervision struct {
	running     bool
	recorded    runstate.Supervision
	found       bool
	failRunning error
	failLoad    error
}

func (f fakeSupervision) Running() (bool, error) { return f.running, f.failRunning }

func (f fakeSupervision) Load() (runstate.Supervision, bool, error) {
	return f.recorded, f.found, f.failLoad
}

// A part the supervisor has left down is down and not coming back on its own,
// so it is on the attention line with the supervisor's own reason, and the
// record is carried whole beside the lines for the surfaces that read it.
func TestADegradedServiceWaitsOnTheOperatorWithTheSupervisorsReason(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Supervision = fakeSupervision{
		running: true,
		found:   true,
		recorded: runstate.Supervision{
			PID: 4242,
			Children: []runstate.SupervisedChild{
				{Service: config.ServiceSlack, State: runstate.ChildRunning, PID: 77},
				{Service: config.ServiceScheduler, State: runstate.ChildDegraded, Reason: "died 6 times within 2m0s of being started, most recently at 2026-09-19T12:03:00Z, so it is left down"},
			},
		},
	}
	standing := ReadStanding(context.Background(), sources)
	if standing.Services == nil || !standing.Services.SupervisorRunning || !standing.Services.Recorded || standing.Services.Record.PID != 4242 {
		t.Fatalf("Services = %+v, want the supervisor running and its record carried", standing.Services)
	}
	if len(standing.NeedsHuman) != 1 {
		t.Fatalf("NeedsHuman = %+v, want the one degraded child", standing.NeedsHuman)
	}
	if got := standing.NeedsHuman[0]; !strings.Contains(got.What, "scheduler service is degraded") || !strings.Contains(got.What, "died 6 times") || !strings.HasPrefix(got.Whose, "the operator's") {
		t.Errorf("attention = %+v, want the child, the reason, and whose move it is", got)
	}
	rendered := standing.Render()
	if !strings.Contains(rendered, "Needs a human (1):\n  the scheduler service is degraded: died 6 times") {
		t.Errorf("rendered:\n%s\nwant the degraded child on the attention line", rendered)
	}
}

// A running part is not attention, a product nothing has supervised says so
// rather than reporting parts down, and a reading with nothing wired says
// nothing at all.
func TestARunningServiceAndAnUnsupervisedProductWaitOnNobody(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Supervision = fakeSupervision{running: true, found: true, recorded: runstate.Supervision{
		PID:      1,
		Children: []runstate.SupervisedChild{{Service: config.ServiceSlack, State: runstate.ChildRunning}},
	}}
	if standing := ReadStanding(context.Background(), sources); len(standing.NeedsHuman) != 0 {
		t.Errorf("a running service was reported as waiting on somebody: %+v", standing.NeedsHuman)
	}

	sources.Supervision = fakeSupervision{}
	standing := ReadStanding(context.Background(), sources)
	if standing.Services == nil || standing.Services.SupervisorRunning || standing.Services.Recorded || len(standing.NeedsHuman) != 0 {
		t.Errorf("an unsupervised product = %+v, %+v, want no supervisor, no record, and nothing waiting", standing.Services, standing.NeedsHuman)
	}

	sources.Supervision = nil
	if standing := ReadStanding(context.Background(), sources); standing.Services != nil {
		t.Errorf("Services = %+v with nothing wired, want nil", standing.Services)
	}
}

// A record that cannot be read is said on the line it would have been said on
// rather than read as every part running.
func TestAnUnreadableSupervisionRecordIsSaidRatherThanAssumedHealthy(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Supervision = fakeSupervision{failLoad: errors.New("decode supervision record: unexpected EOF")}
	standing := ReadStanding(context.Background(), sources)
	if !strings.Contains(standing.ServicesProblem, "unexpected EOF") || !strings.Contains(standing.NeedsHumanProblem, "unexpected EOF") {
		t.Errorf("problems = %q / %q, want the unreadable record named on both", standing.ServicesProblem, standing.NeedsHumanProblem)
	}
}
