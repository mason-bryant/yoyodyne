package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWhatASessionSaidSurvivesTheProcessThatSaidIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	session := testWatchSessionID
	transitions := []WatchTransition{
		testWatchTransition(session, WatchWatching, "watching the backlog until stopped"),
		testWatchTransition(session, WatchIdle, "the backlog is empty"),
		testWatchTransition(session, WatchStopped, "the scheduler was cancelled"),
	}
	for _, transition := range transitions {
		if err := store.Record(transition); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}

	// The reader is a different process: what makes a watch session legible at
	// all is that somebody who is not at its terminal can read what it is doing.
	reloaded := newTestWatchStore(t, root)
	recorded, err := reloaded.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != len(transitions) {
		t.Fatalf("List() = %#v, want every transition in the order it happened", recorded)
	}
	for index, transition := range transitions {
		if recorded[index].State != transition.State || recorded[index].Reason != transition.Reason {
			t.Fatalf("transition %d = %#v, want %#v", index, recorded[index], transition)
		}
	}
	latest, watched, err := reloaded.Latest()
	if err != nil || !watched {
		t.Fatalf("Latest() = watched %v, error %v", watched, err)
	}
	if latest.State != WatchStopped {
		t.Fatalf("latest state = %q, want where the session actually got to", latest.State)
	}
}

// A product nobody has watched has no session rather than an idle one: never
// having watched and having stopped watching are different answers, and only one
// of them is a reason to go looking for a dead process.
func TestAProductNobodyHasWatchedHasNoSession(t *testing.T) {
	t.Parallel()

	store := newTestWatchStore(t, t.TempDir())
	transitions, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(transitions) != 0 {
		t.Fatalf("List() = %#v, want nothing", transitions)
	}
	if _, watched, err := store.Latest(); err != nil || watched {
		t.Fatalf("Latest() = watched %v, error %v, want no session at all", watched, err)
	}
}

func TestATransitionThatCannotBeReadBackOrDoesNotBelongHereIsRefused(t *testing.T) {
	t.Parallel()

	store := newTestWatchStore(t, t.TempDir())
	elsewhere := testWatchTransition(testWatchSessionID, WatchIdle, "")
	elsewhere.ProductID = "another-product"
	if err := store.Record(elsewhere); err == nil {
		t.Fatal("Record() error = nil, want another product's session refused")
	}
	unnamed := testWatchTransition("session-one", WatchIdle, "")
	if err := store.Record(unnamed); err == nil {
		t.Fatal("Record() error = nil, want a session identifier nothing generated refused")
	}
	// A state nothing says is refused at the write rather than reaching a reader
	// that has no words for it.
	invented := testWatchTransition(testWatchSessionID, WatchState("pondering"), "")
	if err := store.Record(invented); err == nil {
		t.Fatal("Record() error = nil, want a state no session takes refused")
	}
	verbose := testWatchTransition(testWatchSessionID, WatchIdle, strings.Repeat("x", MaxWatchReasonBytes+1))
	if err := store.Record(verbose); err == nil {
		t.Fatal("Record() error = nil, want a reason past its bound refused")
	}
	// The build is handed to Git by whatever measures the session's age, so a
	// field that could carry anything is refused at the write rather than there.
	misbuilt := testWatchTransition(testWatchSessionID, WatchIdle, "")
	misbuilt.Build = "--upload-pack=touch /tmp/x"
	if err := store.Record(misbuilt); err == nil {
		t.Fatal("Record() error = nil, want a build that is not a revision refused")
	}
}

// A session that stays open runs whatever binary it was started with, and
// nothing else in the record says which. It is carried on every transition
// rather than only the opening one, because a reader arriving in the middle of a
// night reads the entry the session happened to write last.
//
// A session started by a binary that stamped no revision records none, and that
// is an ordinary entry rather than a malformed one: what it costs is the one
// comparison.
func TestASessionRecordsTheBuildItIsRunning(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	build := "4c1f2b3a9d8e7f6a5b4c3d2e1f0099887766554433221100aabbccddeeff0011"
	running := testWatchTransition(testWatchSessionID, WatchWatching, "watching the backlog until stopped")
	running.Build = build
	if err := store.Record(running); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	unstamped := testWatchTransition(testWatchSessionID, WatchIdle, "the backlog is empty")
	if err := store.Record(unstamped); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	recorded, err := newTestWatchStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("List() = %#v, want both transitions", recorded)
	}
	if recorded[0].Build != build {
		t.Fatalf("build = %q, want %q", recorded[0].Build, build)
	}
	if recorded[1].Build != "" {
		t.Fatalf("build = %q, want a binary that stamped nothing to record nothing", recorded[1].Build)
	}
}

// A log that cannot be read is an error rather than an absence, for the reason
// every other record here is: a session nobody can read must not be reported as
// a session that never ran.
func TestAnUnreadableWatchLogIsAnError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	if err := store.Record(testWatchTransition(testWatchSessionID, WatchWatching, "")); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.List(); err == nil {
		t.Fatal("List() error = nil, want an unreadable log refused rather than read as empty")
	}
}

func TestWatchSessionIdentifiersAreDistinct(t *testing.T) {
	t.Parallel()

	first, err := NewWatchSessionID()
	if err != nil {
		t.Fatalf("NewWatchSessionID() error = %v", err)
	}
	second, err := NewWatchSessionID()
	if err != nil {
		t.Fatalf("NewWatchSessionID() error = %v", err)
	}
	if first == second {
		t.Fatalf("two sessions were named %q, want each session identifiable on its own", first)
	}
	if !watchSessionIDPattern.MatchString(first) {
		t.Fatalf("session id = %q, want the shape the record is validated against", first)
	}
}

const testWatchSessionID = "watch-0123456789abcdef0123456789abcdef"

func newTestWatchStore(t *testing.T, root string) *WatchStore {
	t.Helper()
	store, err := NewWatchStore(filepath.Clean(root), "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	return store
}

func testWatchTransition(sessionID string, state WatchState, reason string) WatchTransition {
	return WatchTransition{
		SchemaVersion: WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     sessionID,
		State:         state,
		At:            time.Date(2026, 8, 19, 21, 0, 0, 0, time.UTC),
		Reason:        reason,
	}
}
