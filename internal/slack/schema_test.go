package slack

// A sink is older than the records it reads whenever a deploy lands while it is
// running, which under watch mode is most nights. These are the tests for what
// that must cost: nothing at all for an added key, and one skipped record with
// the remedy said for anything worse.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The overnight outage, reproduced: a run record carrying a key this build has
// never heard of. Reporting carries on as though the key were not there, which
// is what the schema version exists to make possible.
func TestASinkOlderThanTheRecordsSurvivesAnAddedKeySilently(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	said := harness.logs()
	writeRunFile(t, harness.runs.Root(), "run-22ce30530a5cd7ac66414f2095dcede1", `"work_item_summary": "added by a build newer than this sink",`)

	harness.poll(t, harness.start(), notify.KindRunStarted)
	if len(*said) != 0 {
		t.Fatalf("log = %v, want an added key read past without a word about it", *said)
	}
}

// A record this build genuinely cannot decode must not stop the pass. It is
// skipped, the operator is told which record and what to do about it, and every
// other stream reports exactly as it would have.
func TestARecordThatWillNotDecodeIsSkippedAndTheRestOfThePassStillReports(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	said := harness.logs()
	harness.record(t, harness.run(t, runstate.StatusRunning))
	// A schema version a later build broke compatibility at, which is the one
	// thing tolerance must not paper over.
	writeRunFile(t, harness.runs.Root(), "run-4c1d9f2a5b6e70318294adcb5e7f0d11", `"schema_version": 99,`)

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v, want the undecodable record skipped rather than the pass failed", err)
	}
	// A run record names its own stream even when it will not decode, so its
	// cursor is accounted for and the pass can still prune. Freezing pruning over
	// a skip that costs nothing would keep every dead run's cursor for the life of
	// the process.
	if _, kept := batch.Streams[runStream("run-4c1d9f2a5b6e70318294adcb5e7f0d11")]; !kept {
		t.Fatalf("streams = %v, want the skipped run's own stream still accounted for", batch.Streams)
	}
	if batch.Partial {
		t.Fatal("Partial = true, want a skip that could name its stream not to freeze pruning")
	}
	var kinds []notify.Kind
	for _, delivery := range batch.Deliveries {
		if !delivery.Silent() {
			kinds = append(kinds, delivery.Notification.Event.Kind)
		}
	}
	if len(kinds) != 1 || kinds[0] != notify.KindRunStarted {
		t.Fatalf("said %v, want the readable run reported as though nothing were wrong", kinds)
	}

	if len(*said) != 1 {
		t.Fatalf("log = %v, want the skip said exactly once", *said)
	}
	notice := (*said)[0]
	for _, want := range []string{"run-4c1d9f2a5b6e70318294adcb5e7f0d11", "skipped", "restarting it on the current build"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice = %q, want it to contain %q", notice, want)
		}
	}

	// The record stays undecodable until somebody acts on it, so the pass after
	// says nothing further: a line every fifteen seconds is how a log stops being
	// read.
	if _, err := harness.feed.Poll(context.Background(), harness.start()); err != nil {
		t.Fatalf("second Poll() error = %v", err)
	}
	if len(*said) != 1 {
		t.Fatalf("log = %v, want the same skip said once rather than once per pass", *said)
	}
}

// The cursor is what makes an outage a delay. A record that would not decode
// does not say which stream it is, so a pass that read past one cannot be the
// pass that decides which cursors are for streams that have gone away —
// otherwise a sink restarted on the newer build reports the run from its
// beginning again.
func TestAPassThatReadPastARecordForgetsNoCursor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	sink := newTestSink(t, root, &partialFeed{}, posts)
	carried := Cursor{Position: 7}
	if err := sink.store.SaveCursors(Cursors{
		SchemaVersion: CursorsSchemaVersion,
		Since:         moment,
		Streams:       map[string]Cursor{"run:run-a": carried},
	}); err != nil {
		t.Fatalf("SaveCursors() error = %v", err)
	}

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	cursors, err := sink.store.LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	if cursors.Streams["run:run-a"].Position != carried.Position {
		t.Fatalf("cursors = %#v, want how far the unread record had been read kept", cursors.Streams)
	}
}

// A hold the sink cannot read is not a hold that was lifted. The lift is derived
// from the record's absence, so a reading failure that looked like an absence
// would post a release the operator never made — and the pass must not fail over
// it either, which is the starvation this whole reading exists to end.
func TestAHoldThatCannotBeReadIsNeitherPostedNorReportedLifted(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	said := harness.logs()
	if _, err := harness.intake.Hold("reordering the queue", moment); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)

	// The record is replaced by one this build cannot make sense of at all.
	path := filepath.Join(harness.intake.Root(), "intake-hold.json")
	if err := os.WriteFile(path, []byte(`{"schema_version": 99}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	batch, err := harness.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v, want an unreadable switch skipped rather than the pass failed", err)
	}
	for _, delivery := range batch.Deliveries {
		if !delivery.Silent() {
			t.Fatalf("said %v about a switch it could not read, want nothing claimed either way", delivery.Notification.Event.Kind)
		}
	}
	if len(*said) != 1 || !strings.Contains((*said)[0], "the intake hold") {
		t.Fatalf("log = %v, want the unreadable switch named once", *said)
	}

	// And the mark stays where it was, so the release is still said when the hold
	// is genuinely gone rather than merely unreadable.
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	harness.poll(t, cursors, notify.KindIntakeReleased)
}

// A conversation's event log is a record of another process's output like any
// other, and the turn a newer build wrote is the same collision on a different
// file. An added key is read past; a line this build cannot decode is skipped so
// the milestones behind it still reach the channel, rather than one turn taking
// the whole pass down with it.
func TestAConversationEventANewerBuildWroteDoesNotStopTheLogBeingRead(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	said := harness.logs()
	conversation := harness.converse(t, domain.RoleProductManager)
	appendEventLine(t, harness.chats, conversation.ConversationID, `{
  "schema_version": 1,
  "run_id": "`+conversation.ConversationID+`",
  "sequence": 1,
  "timestamp": "2026-08-19T10:00:00Z",
  "type": "agent_message",
  "source": "harness.chat",
  "payload": {"text": "a turn a newer build wrote"},
  "a_key_this_build_has_never_heard_of": true
}`)
	// And one this build cannot make sense of at all.
	appendEventLine(t, harness.chats, conversation.ConversationID, `{"schema_version": 99, "run_id": "`+conversation.ConversationID+`", "sequence": 2}`)
	harness.chatted(t, conversation, 3, execution.EventTrackerActionApplied, map[string]any{
		"action_id": "t1.1",
		"turn":      1,
		"action": map[string]any{
			"action":   "reprioritize",
			"id":       "yoyodyne-ifd.99",
			"priority": 1,
			"reason":   "it waits on the epic above it",
		},
		"work_item_id": "yoyodyne-ifd.99",
		"summary":      "set yoyodyne-ifd.99 to priority 1",
	})

	// The milestone behind the unreadable turn is still said.
	harness.poll(t, harness.start(), notify.KindItemReprioritized)
	if len(*said) != 1 || !strings.Contains((*said)[0], "the event log of conversation "+conversation.ConversationID) {
		t.Fatalf("log = %v, want the one unreadable line named and the added key read past in silence", *said)
	}
	if !strings.Contains((*said)[0], "restarting it on the current build") {
		t.Fatalf("notice = %q, want the remedy named", (*said)[0])
	}
}

// The reports pile is the same shape and gets the same reading: one report a
// newer build filed must not take every report behind it off the channel.
func TestAReportLineThatWillNotDecodeIsSkippedAndTheRestStillReach(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	said := harness.logs()
	appendLine(t, harness.reports.Path(), `{"schema_version": 99, "id": "rep-unreadable"}`)
	harness.file(t, "report-0123456789abcdef0123456789abcde0", report.SeverityWarning, moment)

	harness.poll(t, harness.start(), notify.KindReportFiled)
	if len(*said) != 1 || !strings.Contains((*said)[0], "the report log line 1") {
		t.Fatalf("log = %v, want the unreadable line named by log and position", *said)
	}
}

// A conversation record that will not decode is the one skip that costs the pass
// something: its stream is the conversation id inside the record, so a pass that
// read past it can no longer enumerate what this product has and must not be the
// pass that forgets a cursor.
func TestAConversationRecordThatWillNotDecodeLeavesThePassUnableToPrune(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.logs()
	harness.converse(t, domain.RoleProductManager)
	if err := os.WriteFile(filepath.Join(harness.chats.Root(), "architect.json"), []byte(`{"schema_version": 99}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v, want the record skipped rather than the pass failed", err)
	}
	if !batch.Partial {
		t.Fatal("Partial = false, want a skip that could not name its stream to stop the pruning")
	}
}

// The heartbeat is derived from the switches rather than read from a record, so
// a switch that would not read leaves it with nothing to derive from. It says
// nothing and forgets nothing: the state it was already standing on keeps its
// clock and is picked up again on the pass the record reads.
func TestTheHeartbeatSaysNothingWhileASwitchCannotBeRead(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(3)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	harness.hold(t, "reordering the backlog first", moment)
	cursors := harness.poll(t, harness.start(), notify.KindWatchStopped, notify.KindIntakeHeld)
	standing := cursors.Streams[heartbeatStream]

	if err := os.WriteFile(filepath.Join(harness.intake.Root(), "intake-hold.json"), []byte(`{"schema_version": 99}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	harness.now = harness.now.Add(2 * time.Hour)
	after := harness.poll(t, cursors)
	if after.Streams[heartbeatStream].Standing != standing.Standing {
		t.Fatalf("standing = %q, want the state it was already on kept rather than re-armed", after.Streams[heartbeatStream].Standing)
	}
}

// partialFeed is a pass that read past a record: it names no streams it did not
// account for and says so.
type partialFeed struct{}

func (f *partialFeed) Poll(context.Context, Cursors) (Batch, error) {
	return Batch{Streams: map[string]struct{}{"reports": {}}, Partial: true}, nil
}

// logs collects what the feed says about itself, which is where a skip is
// stated and where a test reads it back.
func (h *testHarness) logs() *[]string {
	said := []string{}
	h.feed.Log = func(format string, args ...any) {
		said = append(said, fmt.Sprintf(format, args...))
	}
	return &said
}

// appendEventLine puts one raw line on a conversation's event log, which is how
// a turn recorded by another build is reproduced without a second build to
// record it. The document is folded onto one line because the log is one record
// per line and a reader counts them.
func appendEventLine(t *testing.T, store *runstate.ConversationStore, conversationID, document string) {
	t.Helper()
	folded := strings.Join(strings.Fields(document), " ")
	appendLine(t, filepath.Join(store.Root(), conversationID+".events.jsonl"), folded)
}

// appendLine appends one raw line to an append-only log, creating it if this is
// the first thing on it.
func appendLine(t *testing.T, path, line string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	defer file.Close()
	if _, err := file.WriteString(line + "\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
}

// writeRunFile puts one run state file on disk with an extra line spliced into
// it, which is how a record written by another build is reproduced without a
// second build to write it.
func writeRunFile(t *testing.T, root, runID, extra string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	document := `{
  "schema_version": 1,
  ` + extra + `
  "run_id": "` + runID + `",
  "product_id": "yoyodyne",
  "repository_id": "yoyodyne",
  "work_item_id": "yoyodyne-ifd.68.7",
  "backend": "claude-code",
  "status": "running",
  "phase": "developing",
  "started_at": "2026-08-19T10:00:00Z",
  "updated_at": "2026-08-19T10:00:00Z"
}
`
	if err := os.WriteFile(filepath.Join(root, runID+".json"), []byte(document), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
