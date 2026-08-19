package runstate

import (
	"path/filepath"
	"testing"
)

// Something singular must have one owner rather than two owners taking turns,
// so a lease that is already held is reported as held rather than queued for.
func TestALeasedPathHasExactlyOneHolder(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sink", ".sink.lock")
	lease, held, err := TryLeasePath(path, "slack sink")
	if err != nil || !held {
		t.Fatalf("TryLeasePath() = %t, %v, want the first holder admitted", held, err)
	}
	if _, second, err := TryLeasePath(path, "slack sink"); err != nil || second {
		t.Fatalf("second TryLeasePath() = %t, %v, want a second holder refused", second, err)
	}

	// The operating system drops the lock when its holder goes away, so a
	// process that was killed leaves nothing for anybody to clear.
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	again, held, err := TryLeasePath(path, "slack sink")
	if err != nil || !held {
		t.Fatalf("TryLeasePath() after release = %t, %v, want the next holder admitted", held, err)
	}
	if err := again.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	// Releasing twice is a no-op, so a caller can defer it beside the error
	// path that already released it.
	if err := again.Release(); err != nil {
		t.Fatalf("second Release() error = %v", err)
	}
}

// A lease with no absolute path or no label is a lease whose failures cannot say
// what they were about.
func TestALeaseNamesWhatItOwns(t *testing.T) {
	t.Parallel()

	if _, _, err := TryLeasePath("relative/path.lock", "slack sink"); err == nil {
		t.Fatal("TryLeasePath() with a relative path = nil, want a refusal")
	}
	if _, _, err := TryLeasePath(filepath.Join(t.TempDir(), "x.lock"), "  "); err == nil {
		t.Fatal("TryLeasePath() with no label = nil, want a refusal")
	}
}
