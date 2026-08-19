package slack

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
)

// A restart must reply into the threads it already opened. That is the whole
// reason the map is durable: a sink that forgot would open a second thread per
// work item every time the operator restarted it.
func TestTheThreadMapSurvivesTheProcessThatWroteIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStore(t, root)
	threads, err := store.LoadThreads()
	if err != nil {
		t.Fatalf("LoadThreads() error = %v", err)
	}
	if len(threads.Threads) != 0 {
		t.Fatalf("LoadThreads() = %#v, want nothing recorded for a product that has never reported", threads)
	}

	topic := notify.WorkItemTopic("yoyodyne-ifd.68.3")
	threads.Record(topic, Thread{Channel: "C123", ThreadTS: "1755.0001", OpenedAt: time.Now().UTC()})
	if err := store.SaveThreads(threads); err != nil {
		t.Fatalf("SaveThreads() error = %v", err)
	}

	// A second store over the same root is what the next process is.
	reloaded, err := newStore(t, root).LoadThreads()
	if err != nil {
		t.Fatalf("LoadThreads() error = %v", err)
	}
	thread, found := reloaded.Lookup("C123", topic)
	if !found || thread.ThreadTS != "1755.0001" {
		t.Fatalf("Lookup() = %#v, %t, want the thread as it was opened", thread, found)
	}
	// A thread timestamp means nothing outside the channel it was posted in, so
	// a project that changed channels must open new threads rather than reply
	// into a timestamp the new channel has never heard of.
	if _, found := reloaded.Lookup("C999", topic); found {
		t.Fatal("a thread from another channel must not be replied into")
	}
}

// The cursor is what stops a message being posted twice, so it has to be
// readable by the next process exactly as it was left.
func TestCursorsRecordBothWhatWasReadAndWhatWasSaid(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStore(t, root)
	cursors, err := store.LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	cursors.Streams["reports"] = Cursor{Position: 3}
	cursors.Streams["run:run-a"] = Cursor{}.With("started").With("checks.passed#0")
	if err := store.SaveCursors(cursors); err != nil {
		t.Fatalf("SaveCursors() error = %v", err)
	}

	reloaded, err := newStore(t, root).LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	if reloaded.Streams["reports"].Position != 3 {
		t.Fatalf("reports cursor = %#v, want the log position it reached", reloaded.Streams["reports"])
	}
	if !reloaded.Streams["run:run-a"].Has("started") || !reloaded.Streams["run:run-a"].Has("checks.passed#0") {
		t.Fatalf("run cursor = %#v, want every milestone already said", reloaded.Streams["run:run-a"])
	}
	if reloaded.Streams["run:run-a"].Has("promotion") {
		t.Fatal("a milestone that was never posted must not read as posted")
	}

	// A machine that has run for months must not carry a cursor for every run it
	// ever made.
	if !reloaded.Keep(map[string]struct{}{"reports": {}}) {
		t.Fatal("Keep() = false, want it to report the cursor it dropped")
	}
	if _, kept := reloaded.Streams["run:run-a"]; kept {
		t.Fatal("Keep() must drop the cursors of streams that no longer exist")
	}
	// A pass with nothing to forget writes nothing, so an idle sink does not
	// rewrite its state every few seconds for years.
	if reloaded.Keep(map[string]struct{}{"reports": {}}) {
		t.Fatal("Keep() = true, want nothing reported when nothing was dropped")
	}
}

// Two sinks hold separate thread maps, so the second opens its own threads and
// posts everything twice. The lease is what makes that impossible rather than
// merely discouraged.
func TestOnlyOneSinkHoldsTheProductAtATime(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStore(t, root)
	lease, held, err := store.Lease()
	if err != nil || !held {
		t.Fatalf("Lease() = %t, %v, want the first sink to take it", held, err)
	}
	if _, second, err := newStore(t, root).Lease(); err != nil || second {
		t.Fatalf("second Lease() = %t, %v, want a second sink refused", second, err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	// A sink that has stopped leaves nothing for the next one to clear.
	if _, again, err := newStore(t, root).Lease(); err != nil || !again {
		t.Fatalf("Lease() after release = %t, %v, want the next sink admitted", again, err)
	}
}

// A record the sink cannot read must never be reported as a product that has
// never said anything: that would reopen every thread and repost every
// milestone.
func TestUnreadableStateIsAFailureRatherThanAnEmptyMap(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newStore(t, root)
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), "threads.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.LoadThreads(); err == nil {
		t.Fatal("LoadThreads() = nil, want a thread map that is not one to be refused")
	}

	if err := os.WriteFile(filepath.Join(store.Root(), "cursors.json"), []byte(`{"schema_version":99}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.LoadCursors(); err == nil {
		t.Fatal("LoadCursors() = nil, want a schema this sink does not know to be refused")
	}
}

func newStore(t *testing.T, root string) *Store {
	t.Helper()
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}
