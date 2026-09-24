package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// One run record this build cannot read is a reason to say nothing about that
// run, and nothing else. The live sink once held every stream's cursor still
// over five run states a newer binary wrote — seven thousand aborted passes, and
// no reporting at all for as long as the records stood — so a pass reads the
// record past, says so once, and carries every other stream on. The unreadable
// run's own cursor stays where it was, so the run is reported from there when a
// build that can read it arrives.
func TestOneRunRecordNobodyCanReadStopsNoOtherStream(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	var logged []string
	harness.feed.Log = func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	readable := harness.run(t, runstate.StatusRunning)
	harness.record(t, readable)
	unreadable := harness.run(t, runstate.StatusRunning)
	unreadable.WorkItemID = "yoyodyne-ifd.68.4"
	harness.record(t, unreadable)

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), harness.feed, posts)
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("first pass() error = %v", err)
	}
	before, err := sink.store.LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	held, found := before.Streams[runStream(unreadable.RunID)]
	if !found {
		t.Fatalf("cursors = %#v, want the second run reported before it broke", before.Streams)
	}

	// A newer build writes a status this one has no word for, which no amount of
	// tolerating unknown fields reads.
	path := filepath.Join(harness.runs.Root(), unreadable.RunID+".json")
	rewriteStatus(t, path, "a_status_a_newer_build_added")
	readable.Phase = runstate.PhaseReviewing
	readable.UpdatedAt = moment.Add(time.Minute)
	harness.save(t, readable)

	posts.requests = nil
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v, want the unreadable run read past", err)
	}
	if len(posts.requests) == 0 {
		t.Fatal("the pass posted nothing, want the readable run's crossing said")
	}
	after, err := sink.store.LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	if after.Streams[runStream(readable.RunID)].Reported == nil ||
		after.Streams[runStream(readable.RunID)].Reported.Phase != runstate.PhaseReviewing {
		t.Fatalf("readable cursor = %#v, want it moved on to the reading just said", after.Streams[runStream(readable.RunID)])
	}
	kept, found := after.Streams[runStream(unreadable.RunID)]
	if !found {
		t.Fatal("the unreadable run's cursor was dropped, want it kept for when the record reads again")
	}
	if kept.Reported == nil || held.Reported == nil || kept.Reported.Phase != held.Reported.Phase || kept.Closed != held.Closed {
		t.Fatalf("unreadable cursor = %#v, want it where it was (%#v)", kept, held)
	}
	if len(logged) != 1 {
		t.Fatalf("logged %q, want the unreadable run said once", logged)
	}

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("third pass() error = %v", err)
	}
	if len(logged) != 1 {
		t.Fatalf("logged %q, want it said once rather than on every pass", logged)
	}
}

// Only the pass that read everything may forget a stream: a pass that read one
// record past cannot tell a run that went away from a run it could not read, so
// it forgets nothing.
func TestAPassThatReadARecordPastForgetsNoCursor(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.feed.Log = func(string, ...any) {}
	conversation := filepath.Join(harness.chats.Root(), "architect.json")
	if err := os.MkdirAll(filepath.Dir(conversation), 0o700); err != nil {
		t.Fatalf("create the conversation directory: %v", err)
	}
	if err := os.WriteFile(conversation, []byte(`{"conversation_id":`), 0o600); err != nil {
		t.Fatalf("write a torn conversation record: %v", err)
	}

	sink := newTestSink(t, t.TempDir(), harness.feed, &recordedPosts{})
	gone := "conversation:a-conversation-nothing-names-any-more"
	cursors := Cursors{SchemaVersion: CursorsSchemaVersion, Since: moment, Streams: map[string]Cursor{gone: {Position: 3}}}
	if err := sink.store.SaveCursors(cursors); err != nil {
		t.Fatalf("SaveCursors() error = %v", err)
	}
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v, want the torn conversation read past", err)
	}
	after, err := sink.store.LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	if after.Streams[gone].Position != 3 {
		t.Fatalf("cursors = %#v, want the stream the torn record may have been kept", after.Streams)
	}

	// Once everything reads, the pass forgets what is no longer there.
	if err := os.Remove(conversation); err != nil {
		t.Fatalf("remove the torn record: %v", err)
	}
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	after, err = sink.store.LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	if _, found := after.Streams[gone]; found {
		t.Fatalf("cursors = %#v, want the stream nothing names forgotten once a pass read everything", after.Streams)
	}
}

func rewriteStatus(t *testing.T, path, status string) {
	t.Helper()

	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var carried map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &carried); err != nil {
		t.Fatalf("read the record back as JSON: %v", err)
	}
	carried["status"] = json.RawMessage(fmt.Sprintf("%q", status))
	rewritten, err := json.Marshal(carried)
	if err != nil {
		t.Fatalf("rewrite the record: %v", err)
	}
	if err := os.WriteFile(path, rewritten, 0o600); err != nil {
		t.Fatalf("rewrite %s: %v", path, err)
	}
}
