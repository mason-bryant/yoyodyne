package cli

import (
	"errors"
	"fmt"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
)

// A wakeup the provider never took is marked as one, whether the provider met it
// mid-turn or the login was found lapsed before the turn began, so the corrector
// gives the turn back and makes it again rather than spending the refusal's one
// wakeup on an outage. Every other failure keeps the marking it had.
func TestAWakeupTheProviderNeverTookIsMarkedForRetry(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"a capacity window mid-turn", notCorrected(fmt.Errorf("%w: usage window", chat.ErrProviderCapacity)), orchestrator.ErrProviderWindow},
		{"an outage mid-turn", notCorrected(fmt.Errorf("%w: unreachable", chat.ErrProviderAway)), orchestrator.ErrProviderAway},
		{"a lapsed login found on opening", notOpened(fmt.Errorf("%w: not authenticated", chat.ErrProviderAway)), orchestrator.ErrProviderAway},
		{"a role nothing fills", notOpened(errors.New("no agent fills the role")), orchestrator.ErrRoleUnreachable},
	} {
		if !errors.Is(tc.err, tc.want) {
			t.Errorf("%s: %v, want it marked %v", tc.name, tc.err, tc.want)
		}
	}
	if errors.Is(notOpened(errors.New("no agent fills the role")), orchestrator.ErrProviderAway) {
		t.Errorf("a configuration failure was marked as the provider answering nobody, which would retry what only a person can fix")
	}
}
