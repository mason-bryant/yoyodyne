package runstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func divergedObservation(branch, run string, at time.Time) DivergedTargetObservation {
	return DivergedTargetObservation{
		TargetBranch: branch,
		Remote:       "origin",
		LocalCommit:  "4d7e805",
		RemoteCommit: "9f1c2ab",
		Held:         branch + " on origin is at 9f1c2ab, which does not contain the local " + branch + " at 4d7e805; only a person can say which history is right",
		RunID:        run,
		WorkItemID:   "yoyodyne-" + run,
		At:           at,
	}
}

// A second refusal on the same branch is the same divergence: it keeps when it
// began and counts the refusal. A refusal on another branch is its own. Clearing
// one leaves the other standing, and clearing the last leaves no record at all.
func TestADivergenceIsOnePerBranchAndClearsPerBranch(t *testing.T) {
	t.Parallel()

	store, err := NewDivergedTargetStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDivergedTargetStore() error = %v", err)
	}
	if standing, err := store.Standing(); err != nil || len(standing) != 0 {
		t.Fatalf("Standing() = %#v, %v; want nothing standing on a fresh product", standing, err)
	}
	first := time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)
	if _, err := store.Notice(divergedObservation("main", "one", first)); err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	again, err := store.Notice(divergedObservation("main", "two", first.Add(time.Hour)))
	if err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	if !again.Since.Equal(first) || again.Refusals != 2 || again.RunID != "two" || !again.LastSeen.Equal(first.Add(time.Hour)) {
		t.Fatalf("divergence = %#v, want the first moment kept, the refusal counted, and the latest run named", again)
	}
	if _, err := store.Notice(divergedObservation("release", "three", first)); err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	standing, err := store.Standing()
	if err != nil || len(standing) != 2 || standing[0].TargetBranch != "main" || standing[1].TargetBranch != "release" {
		t.Fatalf("Standing() = %#v, %v; want one divergence per branch", standing, err)
	}

	cleared, found, err := store.Clear("main")
	if err != nil || !found || cleared.TargetBranch != "main" {
		t.Fatalf("Clear(main) = %#v, %t, %v; want the divergence on main lifted", cleared, found, err)
	}
	if standing, err := store.Standing(); err != nil || len(standing) != 1 || standing[0].TargetBranch != "release" {
		t.Fatalf("Standing() = %#v, %v; want release still standing", standing, err)
	}
	if _, found, err := store.Clear("main"); err != nil || found {
		t.Fatalf("Clear(main) again = %t, %v; want nothing to lift and no error", found, err)
	}
	if _, found, err := store.Clear("release"); err != nil || !found {
		t.Fatalf("Clear(release) = %t, %v; want it lifted", found, err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), "diverged-targets.json")); !os.IsNotExist(err) {
		t.Fatalf("record still on disk after the last divergence was lifted: %v", err)
	}
}

// A record nobody can read is an error rather than no divergence, because a
// line pulled through a wedge it could not read spends a run finding it again.
func TestAnUnreadableDivergenceRecordIsAnError(t *testing.T) {
	t.Parallel()

	store, err := NewDivergedTargetStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDivergedTargetStore() error = %v", err)
	}
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), "diverged-targets.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.Standing(); err == nil {
		t.Fatal("Standing() read a corrupt record as no divergence, want an error")
	}
	if _, err := store.Notice(divergedObservation("main", "one", time.Now())); err == nil {
		t.Fatal("Notice() wrote over a record it could not read, want an error")
	}
}
