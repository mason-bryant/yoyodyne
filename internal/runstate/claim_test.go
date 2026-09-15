package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func releasedClaim(id string, at time.Time) ReleasedClaim {
	return ReleasedClaim{
		SchemaVersion: ReleasedClaimSchemaVersion,
		ProductID:     "yoyodyne",
		WorkItemID:    id,
		WorkItemTitle: "A claim that outlived its run",
		RunID:         "run-" + id,
		Since:         at.Add(-9 * time.Hour),
		Because:       "its run ended failed and the claim outlived it",
		ReleasedAt:    at,
	}
}

// The log is what makes a release survive the pass that made it, and what makes
// saying so once a property of a file rather than of a process's memory.
func TestAReleasedClaimIsReadBackAsItWasWritten(t *testing.T) {
	t.Parallel()

	store, err := NewClaimStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewClaimStore() error = %v", err)
	}
	// A product nothing has ever released is not a failure to read.
	if released, err := store.List(); err != nil || len(released) != 0 {
		t.Fatalf("List() = %v, %v, want an empty log read as empty", released, err)
	}

	at := time.Date(2026, 9, 4, 7, 30, 0, 0, time.UTC)
	first := releasedClaim("yoyodyne-ifd.264", at)
	second := releasedClaim("yoyodyne-ifd.211", at.Add(time.Minute))
	for _, record := range []ReleasedClaim{first, second} {
		if err := store.Append(record); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	released, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(released) != 2 {
		t.Fatalf("List() = %d record(s), want both", len(released))
	}
	// The order is the order they were recorded, because that is what a surface
	// reading by position depends on.
	if released[0].WorkItemID != first.WorkItemID || released[1].WorkItemID != second.WorkItemID {
		t.Fatalf("List() = %+v, want the records in the order they were written", released)
	}
	if !released[0].Since.Equal(first.Since) || released[0].RunID != first.RunID {
		t.Fatalf("first record = %+v, want the run and the moment it last spoke", released[0])
	}
}

// A record that names another product is refused, so one product's log can never
// carry another's releases.
func TestAReleasedClaimFromAnotherProductIsRefused(t *testing.T) {
	t.Parallel()

	store, err := NewClaimStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewClaimStore() error = %v", err)
	}
	elsewhere := releasedClaim("yoyodyne-ifd.9", time.Now().UTC())
	elsewhere.ProductID = "somebody-else"
	if err := store.Append(elsewhere); err == nil || !strings.Contains(err.Error(), "does not match store product") {
		t.Fatalf("Append() error = %v, want a refusal naming the mismatch", err)
	}
}

// The bound on what a release says about the run that left the claim. A sentence
// that was assembled has to be one that can be written, so it is folded and cut
// rather than refused.
func TestALongAccountOfADeadRunIsCutRatherThanRefused(t *testing.T) {
	t.Parallel()

	store, err := NewClaimStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewClaimStore() error = %v", err)
	}
	record := releasedClaim("yoyodyne-ifd.9", time.Now().UTC())
	record.Because = strings.Repeat("é", MaxReleasedClaimDetailBytes)
	if err := store.Append(record); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	released, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(released) != 1 {
		t.Fatalf("List() = %d record(s), want the cut record written", len(released))
	}
	if len(released[0].Because) > MaxReleasedClaimDetailBytes {
		t.Fatalf("because is %d bytes, want it cut to the bound", len(released[0].Because))
	}
}

// A line nothing can decode is a failure to read rather than a log read as
// empty: a surface that silently skipped it would stop saying what the harness
// gave back.
func TestAnUnreadableReleaseLogIsAFailureRatherThanSilence(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewClaimStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewClaimStore() error = %v", err)
	}
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.List(); err == nil {
		t.Fatal("List() error = nil, want an undecodable log refused")
	}
	if filepath.Base(store.Path()) != "released-claims.jsonl" {
		t.Fatalf("Path() = %s, want the log named for what it holds", store.Path())
	}
}
