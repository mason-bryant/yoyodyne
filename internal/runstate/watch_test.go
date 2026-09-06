package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
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

// One product has one watching session. Two of them read one queue and choose
// from it independently, and an item chosen is not in the run store until the
// run reserves, so the second session picks work the first has already taken in
// exactly that window -- which is what happened while the 2026-09-05 wedge was
// being cleared.
func TestOneProductHasOneWatchingSession(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	lease, held, err := store.Lease(testWatchSessionID)
	if err != nil || !held {
		t.Fatalf("Lease() = %t, %v, want the first session admitted", held, err)
	}
	// A second session against the same product, from a store built exactly as
	// another process would build it.
	second := newTestWatchStore(t, root)
	if _, held, err := second.Lease("watch-fedcba9876543210fedcba9876543210"); err != nil || held {
		t.Fatalf("second Lease() = %t, %v, want the second session refused while the first watches", held, err)
	}
	// And the refusal can say which session it was refused for, rather than only
	// that somebody is there.
	holder, found, err := second.Holder()
	if err != nil || !found {
		t.Fatalf("Holder() = %#v, found %v, error %v, want the session holding it named", holder, found, err)
	}
	if holder.SessionID != testWatchSessionID || holder.PID != os.Getpid() || holder.HeldAt.IsZero() {
		t.Fatalf("holder = %#v, want the session, the process and when it took the watch", holder)
	}

	// The session ends, and the next one is admitted. What the first left behind
	// is nothing: the stamp goes with the lock, so a reader is never told a
	// session holds a watch it has let go of.
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, found, err := store.Holder(); err != nil || found {
		t.Fatalf("Holder() after release = found %v, error %v, want nobody named", found, err)
	}
	next, held, err := second.Lease("watch-fedcba9876543210fedcba9876543210")
	if err != nil || !held {
		t.Fatalf("Lease() after release = %t, %v, want the next session admitted", held, err)
	}
	if err := next.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// A watch is held for a session, so a lease asked for under a name nothing
// generated is refused rather than taken under a name no surface can match.
func TestAWatchIsHeldForASessionThatCanBeNamed(t *testing.T) {
	t.Parallel()

	store := newTestWatchStore(t, t.TempDir())
	if _, held, err := store.Lease("session-one"); err == nil || held {
		t.Fatalf("Lease() with an invented session = %t, %v, want a refusal", held, err)
	}
	// A stamp that will not decode is a holder nobody can name, which is reported
	// rather than answered with a session invented from a broken file.
	lease, held, err := store.Lease(testWatchSessionID)
	if err != nil || !held {
		t.Fatalf("Lease() = %t, %v, want the session admitted", held, err)
	}
	defer lease.Release()
	if err := os.WriteFile(filepath.Join(store.Root(), watchHolderFile), []byte("{not json}"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, _, err := store.Holder(); err == nil {
		t.Fatal("Holder() error = nil, want an unreadable stamp reported rather than read as nobody")
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
	// The two fields a reader takes whose move follows an idle session from. A
	// count nothing observed and a marker naming no role would both leave a surface
	// saying something about the machine that nobody recorded.
	miscounted := testWatchTransition(testWatchSessionID, WatchIdle, "")
	miscounted.Running = -1
	if err := store.Record(miscounted); err == nil {
		t.Fatal("Record() error = nil, want a negative count of runs in flight refused")
	}
	misaddressed := testWatchTransition(testWatchSessionID, WatchIdle, "")
	misaddressed.Executor = "conversation:nobody"
	if err := store.Record(misaddressed); err == nil {
		t.Fatal("Record() error = nil, want a marker naming no role refused")
	}
}

// A session idle on one slot while a run works on the other is the state that
// was read three times as a line that had stopped. What it recorded said only
// that it had started nothing, so every surface downstream had to guess: these
// two fields are what it says instead, and they survive the process that said
// them like everything else here.
func TestASessionRecordsWhatItSawGoingAndWhoItIsWaitingOn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	idle := testWatchTransition(testWatchSessionID, WatchIdle,
		"1 run in flight; 3 items passed over, of 3 admitted: carried in conversation (architect: yoyodyne-ifd.212)")
	idle.Running = 1
	idle.Executor = domain.ConversationWith(domain.RoleArchitect)
	if err := store.Record(idle); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	recorded, err := newTestWatchStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the one transition back", recorded)
	}
	if recorded[0].Running != 1 || recorded[0].Executor != domain.ConversationWith(domain.RoleArchitect) {
		t.Fatalf("transition = %#v, want the runs it saw and the conversation it waits on carried", recorded[0])
	}
	if recorded[0].Unreadable {
		t.Fatalf("transition = %#v, want a queue it read left unmarked", recorded[0])
	}

	// The other poll that chose nothing, and the one no admission would change:
	// the store would not answer, so what is in the queue is not what stopped it.
	outage := testWatchTransition(testWatchSessionID, WatchIdle, "the harness could not be read and is being read again")
	outage.Unreadable = true
	if err := store.Record(outage); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	reread, err := newTestWatchStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(reread) != 2 || !reread[1].Unreadable {
		t.Fatalf("transitions = %#v, want the reading that failed marked as one", reread)
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

// A session that stopped to be restarted into a build deployed over it says so
// on the stop, because the state alone cannot tell that apart from a line
// somebody closed — and the two ask opposite things of whoever reads the log.
//
// The mark is a field rather than a state of its own on purpose: a reader from
// before it existed ignores an unknown field and refuses an unknown state, and
// the reader running while a redeploy happens is exactly the older one.
func TestASessionSaysWhenItsStopIsARestart(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	restarting := testWatchTransition(testWatchSessionID, WatchStopped, "a build was deployed over the one this session was started from")
	restarting.Restarting = true
	if err := store.Record(restarting); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	ended := testWatchTransition(testWatchSessionID, WatchStopped, "the operator stopped it")
	if err := store.Record(ended); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	recorded, err := newTestWatchStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("List() = %#v, want both stops", recorded)
	}
	if !recorded[0].Restarting {
		t.Fatal("the stop that was a restart reads back as an ending")
	}
	if recorded[1].Restarting {
		t.Fatal("the stop the operator asked for reads back as a restart")
	}
	// Only a stop can be one. A session marked as coming back while it is still
	// watching would have every surface announcing a restart nothing is going to
	// make.
	watching := testWatchTransition(testWatchSessionID, WatchWatching, "watching the backlog until stopped")
	watching.Restarting = true
	if err := store.Record(watching); err == nil {
		t.Fatal("Record() error = nil, want a restart marked on something that is not a stop refused")
	}
}

// A session waiting out the provider's usage window says so on the poll it made
// inside one, and says when the provider named the window lifting. Nothing else
// in the record distinguishes that poll from a poll over an empty queue, and on
// 2026-09-05 ninety minutes of it was read as a line that had quietly stopped.
//
// The mark is a field rather than a state of its own for the reason the restart
// above is: a reader from before it existed ignores an unknown field and refuses
// an unknown state.
func TestASessionSaysWhenAPollWasMadeInsideTheProvidersWindow(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestWatchStore(t, root)
	lifts := time.Date(2026, 9, 5, 13, 43, 0, 0, time.UTC)
	waiting := testWatchTransition(testWatchSessionID, WatchIdle, "waiting on the provider's usage window until 13:43Z")
	waiting.ProviderWindow = true
	waiting.ProviderWindowResetsAt = &lifts
	if err := store.Record(waiting); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	// A provider that named no reset time records none rather than a moment
	// nobody said: the harness asks again rather than being told when.
	untimed := testWatchTransition(testWatchSessionID, WatchIdle, "waiting on the provider's usage window")
	untimed.ProviderWindow = true
	if err := store.Record(untimed); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	recorded, err := newTestWatchStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("List() = %#v, want both polls", recorded)
	}
	if !recorded[0].ProviderWindow || recorded[0].ProviderWindowResetsAt == nil ||
		!recorded[0].ProviderWindowResetsAt.Equal(lifts) {
		t.Fatalf("the timed window reads back as %+v", recorded[0])
	}
	if !recorded[1].ProviderWindow || recorded[1].ProviderWindowResetsAt != nil {
		t.Fatalf("the untimed window reads back as %+v", recorded[1])
	}

	// Only an idle poll can be one. A session marked as held by the provider while
	// it is watching would have every surface accounting for a silence something
	// else is causing, which is the alarm this is supposed to keep honest.
	watching := testWatchTransition(testWatchSessionID, WatchWatching, "watching the backlog until stopped")
	watching.ProviderWindow = true
	if err := store.Record(watching); err == nil {
		t.Fatal("Record() error = nil, want a window marked on something that is not an idle poll refused")
	}
	// A deadline with nothing waiting on it is a moment nobody is held to, and a
	// surface reading it would say the harness is held until a time it is not.
	orphaned := testWatchTransition(testWatchSessionID, WatchIdle, "the backlog is empty")
	orphaned.ProviderWindowResetsAt = &lifts
	if err := store.Record(orphaned); err == nil {
		t.Fatal("Record() error = nil, want a reset time with no window to belong to refused")
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
