package readmodel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type fakeRestartRequests struct {
	open []runstate.RestartRequest
	err  error
}

func (f fakeRestartRequests) Open() ([]runstate.RestartRequest, error) { return f.open, f.err }

// An instance's open restart requests are carried by its query and by the
// standing under program_managers, beside every configured instance with none,
// and are not a line waiting on a person.
func TestAProgramManagersOpenRestartRequestsAreInItsQueryAndTheStanding(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	request := runstate.RestartRequest{
		SchemaVersion: runstate.RestartRequestSchemaVersion,
		ProductID:     "yoyodyne",
		ID:            "restart-0123456789abcdef",
		Agent:         "factory-pgm",
		Part:          config.ServiceScheduler,
		Reason:        "died four times in an hour",
		RequestedAt:   at,
	}
	sources := quietSources()
	sources.ProgramManagers = []string{"writing-pgm", "factory-pgm"}
	sources.RestartRequests = fakeRestartRequests{open: []runstate.RestartRequest{request}}

	instance, known, problem := ProgramManagerOf(sources, "factory-pgm")
	if !known || problem != "" || len(instance.RestartRequests) != 1 || instance.RestartRequests[0].ID != request.ID {
		t.Fatalf("ProgramManagerOf(factory-pgm) = %+v, %t, %q; want its one open request", instance, known, problem)
	}

	standing := ReadStanding(context.Background(), sources)
	if len(standing.ProgramManagers) != 2 || standing.ProgramManagers[0].Agent != "factory-pgm" || standing.ProgramManagers[1].Agent != "writing-pgm" {
		t.Fatalf("ProgramManagers = %+v, want both instances by name", standing.ProgramManagers)
	}
	if len(standing.ProgramManagers[1].RestartRequests) != 0 {
		t.Errorf("writing-pgm carries %+v, want none", standing.ProgramManagers[1].RestartRequests)
	}
	if len(standing.NeedsHuman) != 0 {
		t.Errorf("NeedsHuman = %+v; a request waits on the supervisor's pass, not on a person", standing.NeedsHuman)
	}
	encoded, err := json.Marshal(standing)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"program_managers":[{"agent":"factory-pgm","restart_requests":[{`) ||
		!strings.Contains(string(encoded), `"restart_requests":[]`) {
		t.Errorf("standing JSON does not carry the instances under program_managers:\n%s", encoded)
	}
}

// A request log that cannot be read is said, and the configured instances are
// still listed rather than the reading reporting that nobody asked anything.
func TestAnUnreadableRestartRequestLogIsSaidRatherThanReadAsNone(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.ProgramManagers = []string{"factory-pgm"}
	sources.RestartRequests = fakeRestartRequests{err: errors.New("torn line")}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.ProgramManagers) != 1 || !strings.Contains(standing.ProgramManagersProblem, "torn line") {
		t.Errorf("ProgramManagers = %+v, problem %q; want the instance and the reason", standing.ProgramManagers, standing.ProgramManagersProblem)
	}
}
