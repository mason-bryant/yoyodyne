package runstate

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

func restartRequest(t *testing.T, agent string, part config.ServiceName, at time.Time) RestartRequest {
	t.Helper()
	id, err := NewRestartRequestID()
	if err != nil {
		t.Fatalf("NewRestartRequestID() error = %v", err)
	}
	return RestartRequest{
		SchemaVersion: RestartRequestSchemaVersion,
		ProductID:     "yoyodyne",
		ID:            id,
		Agent:         agent,
		Part:          part,
		Reason:        "it keeps dying",
		RequestedAt:   at,
	}
}

// At most one request per part per instance is open: a second for the same
// part and instance is refused naming the first, while another part, or the
// same part from another instance, is its own request.
func TestOneRestartRequestIsOpenPerPartPerInstance(t *testing.T) {
	t.Parallel()

	store, err := NewRestartRequestStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewRestartRequestStore() error = %v", err)
	}
	at := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	first, err := store.Request(restartRequest(t, "factory-pgm", config.ServiceScheduler, at))
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	_, err = store.Request(restartRequest(t, "factory-pgm", config.ServiceScheduler, at.Add(time.Minute)))
	var open *OpenRestartRequestError
	if !errors.As(err, &open) || open.Open.ID != first.ID || !strings.Contains(err.Error(), first.ID) {
		t.Fatalf("second Request() error = %v, want a refusal naming %s", err, first.ID)
	}
	if _, err := store.Request(restartRequest(t, "factory-pgm", config.ServiceSlack, at.Add(2*time.Minute))); err != nil {
		t.Errorf("a request for another part was refused: %v", err)
	}
	if _, err := store.Request(restartRequest(t, "writing-pgm", config.ServiceScheduler, at.Add(3*time.Minute))); err != nil {
		t.Errorf("another instance's request for the same part was refused: %v", err)
	}
	requests, err := store.Open()
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if len(requests) != 3 || requests[0].ID != first.ID {
		t.Fatalf("Open() = %+v, want three, the first oldest", requests)
	}
}

// A request somebody answered is no longer open, and the part may be asked
// for again. Nothing in this package answers one; the answer is written here by
// hand as the supervisor's pass will write it.
func TestAnAnsweredRestartRequestIsNoLongerOpen(t *testing.T) {
	t.Parallel()

	store, err := NewRestartRequestStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewRestartRequestStore() error = %v", err)
	}
	at := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	first, err := store.Request(restartRequest(t, "factory-pgm", config.ServiceScheduler, at))
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	answered := first
	answeredAt := at.Add(time.Minute)
	answered.AnsweredAt = &answeredAt
	answered.Answer = "restarted"
	if err := store.append(answered); err != nil {
		t.Fatalf("append() error = %v", err)
	}
	if open, err := store.Open(); err != nil || len(open) != 0 {
		t.Fatalf("Open() = %+v, %v; want none", open, err)
	}
	if _, err := store.Request(restartRequest(t, "factory-pgm", config.ServiceScheduler, at.Add(2*time.Minute))); err != nil {
		t.Errorf("a request after the first was answered was refused: %v", err)
	}
}

// A part the services section does not declare is refused before anything is
// written, naming the four it does.
func TestARestartRequestForAnUndeclaredPartIsRefused(t *testing.T) {
	t.Parallel()

	store, err := NewRestartRequestStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewRestartRequestStore() error = %v", err)
	}
	_, err = store.Request(restartRequest(t, "factory-pgm", "database", time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)))
	if err == nil {
		t.Fatal("Request() accepted a part nothing declares")
	}
	for _, part := range []string{"slack", "dashboard", "scheduler", "maintenance"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("the refusal %q does not name %q", err, part)
		}
	}
	if _, statErr := os.Stat(store.Path()); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a refused request wrote the log: %v", statErr)
	}
}
