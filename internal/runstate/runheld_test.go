package runstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A run's lease is observable from outside without being taken: the standing
// status asks it of every run that is over with its landing checks unfinished,
// to tell a landing a process is running from one whose process died, and a
// reader that took the lease to ask would refuse the sweep for as long as it
// held it.
func TestARunsHolderIsObservedWithoutTakingItsLease(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusPending)
	if held, err := store.Held(state.RunID); held || err != nil {
		t.Fatalf("Held() before anybody holds the run = %v, %v, want not held", held, err)
	}
	lease, err := store.Reserve(context.Background(), state, 1)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if held, err := store.Held(state.RunID); !held || err != nil {
		t.Fatalf("Held() while this process holds the run = %v, %v, want held", held, err)
	}
	stamp := filepath.Join(store.Root(), state.RunID+".holder")
	written, err := os.ReadFile(stamp)
	if err != nil {
		t.Fatalf("the lease left no stamp to observe: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, err := os.Stat(stamp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a released lease left its stamp behind: %v", err)
	}
	if held, err := store.Held(state.RunID); held || err != nil {
		t.Fatalf("Held() after release = %v, %v, want not held", held, err)
	}
	// The stamp is not a record, so the listings still read only the run.
	if recorded, err := store.Recorded(); err != nil || len(recorded) != 1 {
		t.Fatalf("Recorded() = %d runs, %v, want the one run", len(recorded), err)
	}

	// A holder killed outright leaves its stamp behind and its lock dropped, and
	// the answer has to be the operating system's.
	gone := exitedProcess(t)
	stale := strings.Replace(string(written), fmt.Sprintf(`"pid": %d`, os.Getpid()), fmt.Sprintf(`"pid": %d`, gone), 1)
	if stale == string(written) {
		t.Fatalf("the stamp does not name this process, so this test proves nothing:\n%s", written)
	}
	if err := os.WriteFile(stamp, []byte(stale), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if held, err := store.Held(state.RunID); held || err != nil {
		t.Fatalf("Held() over a dead holder's stamp = %v, %v, want not held", held, err)
	}
	_, adopted, err := store.AdoptRun(context.Background(), state.RunID)
	if err != nil {
		t.Fatalf("AdoptRun() after a stale stamp error = %v", err)
	}
	if held, err := store.Held(state.RunID); !held || err != nil {
		t.Fatalf("Held() once the sweep adopted the run = %v, %v, want held", held, err)
	}
	if err := adopted.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	// A stamp that will not decode is a failure to answer, not an answer.
	if err := os.WriteFile(stamp, []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if held, err := store.Held(state.RunID); held || err == nil {
		t.Fatalf("Held() over an unreadable stamp = %v, %v, want a stated problem", held, err)
	}
	if held, err := store.Held("not a run"); held || err == nil {
		t.Fatalf("Held() of an invalid run id = %v, %v, want a stated problem", held, err)
	}
}

// Where a landing whose checks have not ended stands is said from the record:
// the check it is on, for how long against its bound, and how long the landing
// has run — or how long it has waited its turn.
func TestAnUnfinishedLandingSaysWhichCheckItIsOnAndForHowLong(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	now := started.Add(52 * time.Minute)
	checkBegan := now.Add(-14 * time.Minute)
	landing := LandingChecks{Commit: "0123456789abcdef", StartedAt: started, BoundSeconds: 7200, TargetBranch: "main"}

	if got, want := landing.Progress(now), "52m into the landing checks, each bounded at 120m"; got != want {
		t.Fatalf("Progress() before any check began = %q, want %q", got, want)
	}
	landing.Command, landing.CommandStartedAt = "make race", &checkBegan
	if got, want := landing.Progress(now), "on make race for 14m of its 120m bound, 52m into the landing checks"; got != want {
		t.Fatalf("Progress() on a check = %q, want %q", got, want)
	}

	waiting := LandingChecks{Commit: "0123456789abcdef", StartedAt: started, BoundSeconds: 7200, TargetBranch: "main", WaitingSince: &started}
	if got, want := waiting.Progress(started.Add(5*time.Minute)), "waiting 5m behind another landing on main"; got != want {
		t.Fatalf("Progress() while waiting = %q, want %q", got, want)
	}
}
