package orchestrator

import (
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// refusingSaveStore stands in for a store whose terminal writes are failing —
// the exact situation reportCompletionRecordingFailure exists for. Only Save
// is real; the run at hand never touches the rest of the interface.
type refusingSaveStore struct {
	StateStore
	refusals int
	saved    []runstate.State
}

func (s *refusingSaveStore) Save(state runstate.State) error {
	if s.refusals > 0 {
		s.refusals--
		return errors.New("disk full")
	}
	s.saved = append(s.saved, state)
	return nil
}

func TestTheCompletionRecordingFailureIsPersistedWhenTheLateWriteLands(t *testing.T) {
	t.Parallel()

	store := &refusingSaveStore{}
	pipeline := Pipeline{Store: store, Tracker: &fakeTracker{}}
	state := runstate.State{RunID: "run-0123456789abcdef0123456789abcdef", WorkItemID: "yoyodyne-ifd.90"}
	cause := errors.New("save completed run state after cleanup: disk full")

	outcome, err := pipeline.reportCompletionRecordingFailure(state, Outcome{WorkItemID: state.WorkItemID}, cause)
	if err != nil {
		t.Fatalf("reportCompletionRecordingFailure() error = %v", err)
	}
	if outcome.CompletionRecordingFailure != cause.Error() {
		t.Fatalf("outcome failure = %q, want the cause alone when the late write landed", outcome.CompletionRecordingFailure)
	}
	if len(store.saved) != 1 || store.saved[0].CompletionRecordingFailure != cause.Error() {
		t.Fatalf("saved = %#v, want one record carrying the failure text", store.saved)
	}
}

func TestARefusedLateWriteJoinsTheErrorsRatherThanLosingOne(t *testing.T) {
	t.Parallel()

	store := &refusingSaveStore{refusals: 10}
	pipeline := Pipeline{Store: store, Tracker: &fakeTracker{}}
	state := runstate.State{RunID: "run-0123456789abcdef0123456789abcdef", WorkItemID: "yoyodyne-ifd.90"}
	cause := errors.New("save completed run state after cleanup: disk full")

	outcome, err := pipeline.reportCompletionRecordingFailure(state, Outcome{WorkItemID: state.WorkItemID}, cause)
	if err != nil {
		t.Fatalf("reportCompletionRecordingFailure() error = %v", err)
	}
	for _, want := range []string{cause.Error(), "record the completion problem in the run record"} {
		if !strings.Contains(outcome.CompletionRecordingFailure, want) {
			t.Fatalf("outcome failure = %q, want it to keep %q", outcome.CompletionRecordingFailure, want)
		}
	}
	if len(store.saved) != 0 {
		t.Fatalf("saved = %#v, want nothing persisted by a store that kept refusing", store.saved)
	}
}
