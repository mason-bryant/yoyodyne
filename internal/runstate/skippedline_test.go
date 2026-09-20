package runstate

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// tear appends a torn line to a log — a write a crash interrupted, closed off
// by the newline the record after it happened to begin with — so what is behind
// it is exactly what a reader must not lose.
func tear(t *testing.T, path string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := file.WriteString(`{"schema_version":1,"product_id":"yoyo` + "\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// A torn line holds its place among the records rather than failing the read
// or vanishing from it. The position is what a cursor is written against, so a
// record after the tear keeps the position it would have had without it, and
// the line itself is named with where in the file it is.
func TestScanLogKeepsATornLinesPositionAndReadsPastIt(t *testing.T) {
	t.Parallel()

	log := "{\"n\":1}\n\n{\"n\":2}\n{torn\n{\"n\":3}\n{\"n\":4}"
	var decoded []string
	skipped, err := scanLog(strings.NewReader(log), 64, func(line []byte) error {
		if bytes.HasPrefix(line, []byte("{torn")) {
			return errors.New("unexpected end of JSON input")
		}
		decoded = append(decoded, string(line))
		return nil
	})
	if err != nil {
		t.Fatalf("scanLog() error = %v", err)
	}
	if strings.Join(decoded, " ") != `{"n":1} {"n":2} {"n":3} {"n":4}` {
		t.Fatalf("decoded = %v, want every record around the tear, the last one without its newline included", decoded)
	}
	// The blank line takes no position, exactly as it never has. The torn line is
	// the third record, on the fourth line, starting after the two records and
	// the blank line before it.
	want := SkippedLine{Position: 2, Line: 4, Offset: 17, Problem: "unexpected end of JSON input"}
	if len(skipped) != 1 || skipped[0] != want {
		t.Fatalf("skipped = %+v, want %+v", skipped, want)
	}
}

// A line past the bound is a torn write joined to the record appended after it,
// which is the one shape of corruption an append-only log produces. It is set
// aside like any other line that will not decode, and the read carries on from
// the line after it rather than stopping at the bound.
func TestScanLogSetsAsideALineLongerThanTheBoundAndCarriesOn(t *testing.T) {
	t.Parallel()

	log := "{\"n\":1}\n" + strings.Repeat("x", 200) + "\n{\"n\":2}\n"
	var decoded []string
	skipped, err := scanLog(strings.NewReader(log), 64, func(line []byte) error {
		decoded = append(decoded, string(line))
		return nil
	})
	if err != nil {
		t.Fatalf("scanLog() error = %v", err)
	}
	if strings.Join(decoded, " ") != `{"n":1} {"n":2}` {
		t.Fatalf("decoded = %v, want the records on either side of the overlong line", decoded)
	}
	if len(skipped) != 1 || skipped[0].Position != 1 || skipped[0].Line != 2 || skipped[0].Offset != 8 ||
		!strings.Contains(skipped[0].Problem, "longer than 64 bytes") {
		t.Fatalf("skipped = %+v, want the overlong line named at its position", skipped)
	}
}

// Every log the sink reads by position reads past a torn line the same way:
// the records after it come back with their positions kept, the line is named,
// and the strict listing every other surface uses still refuses the log rather
// than quietly dropping what it could not read.
func TestEveryPositionalLogReadsPastATornLineAndListsRefuseIt(t *testing.T) {
	t.Parallel()

	t.Run("reports", func(t *testing.T) {
		t.Parallel()
		store := newTestReportStore(t, t.TempDir())
		if err := store.Append(testReport("report-0123456789abcdef0123456789abcde0", report.SeverityNote, "before")); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
		tear(t, store.Path())
		if err := store.Append(testReport("report-0123456789abcdef0123456789abcde1", report.SeverityNote, "after")); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
		reports, skipped, err := store.Scan()
		if err != nil {
			t.Fatalf("Scan() error = %v", err)
		}
		if len(reports) != 2 || reports[1].Message != "after" {
			t.Fatalf("Scan() = %+v, want the report after the tear delivered", reports)
		}
		if len(skipped) != 1 || skipped[0].Position != 1 || skipped[0].Line != 2 {
			t.Fatalf("skipped = %+v, want the torn line at the second position", skipped)
		}
		if _, err := store.List(); err == nil || !strings.Contains(err.Error(), "decode report log: line 2") {
			t.Fatalf("List() error = %v, want the strict listing to name the torn line", err)
		}
	})

	t.Run("amendments", func(t *testing.T) {
		t.Parallel()
		store := newTestAmendmentStore(t, t.TempDir())
		if err := store.Append(testProposal("amendment-0123456789abcdef0123456789abcde0")); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
		tear(t, store.Path())
		if err := store.Append(testProposal("amendment-0123456789abcdef0123456789abcde1")); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
		records, skipped, err := store.Scan()
		if err != nil {
			t.Fatalf("Scan() error = %v", err)
		}
		if proposals := amendment.Proposals(records); len(proposals) != 2 || proposals[1].ID != "amendment-0123456789abcdef0123456789abcde1" {
			t.Fatalf("Scan() = %+v, want the proposal after the tear delivered", records)
		}
		if len(skipped) != 1 || skipped[0].Position != 1 {
			t.Fatalf("skipped = %+v, want the torn line at the second position", skipped)
		}
		if _, err := store.List(); err == nil {
			t.Fatal("List() error = nil, want the strict listing to refuse the torn log")
		}
	})

	t.Run("watch", func(t *testing.T) {
		t.Parallel()
		store := newTestWatchStore(t, t.TempDir())
		if err := store.Record(testWatchTransition(testWatchSessionID, WatchWatching, "before")); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
		tear(t, store.Path())
		if err := store.Record(testWatchTransition(testWatchSessionID, WatchStopped, "after")); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
		transitions, skipped, err := store.Scan()
		if err != nil {
			t.Fatalf("Scan() error = %v", err)
		}
		if len(transitions) != 2 || transitions[1].State != WatchStopped {
			t.Fatalf("Scan() = %+v, want the transition after the tear delivered", transitions)
		}
		if len(skipped) != 1 || skipped[0].Position != 1 {
			t.Fatalf("skipped = %+v, want the torn line at the second position", skipped)
		}
		if _, err := store.List(); err == nil {
			t.Fatal("List() error = nil, want the strict listing to refuse the torn log")
		}
	})

	t.Run("usage limits", func(t *testing.T) {
		t.Parallel()
		store := newTestUsageLimitStore(t, t.TempDir())
		if err := store.Record(testUsageLimitExhaustion("before", nil)); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
		tear(t, store.Path())
		if err := store.Record(testUsageLimitExhaustion("after", nil)); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
		exhaustions, skipped, err := store.Scan()
		if err != nil {
			t.Fatalf("Scan() error = %v", err)
		}
		if len(exhaustions) != 2 || exhaustions[1].Waiting != "after" {
			t.Fatalf("Scan() = %+v, want the refusal after the tear delivered", exhaustions)
		}
		if len(skipped) != 1 || skipped[0].Position != 1 {
			t.Fatalf("skipped = %+v, want the torn line at the second position", skipped)
		}
		if _, err := store.List(); err == nil {
			t.Fatal("List() error = nil, want the strict listing to refuse the torn log")
		}
	})

	t.Run("released claims", func(t *testing.T) {
		t.Parallel()
		store, err := NewClaimStore(t.TempDir(), "yoyodyne")
		if err != nil {
			t.Fatalf("NewClaimStore() error = %v", err)
		}
		at := time.Date(2026, 9, 4, 7, 30, 0, 0, time.UTC)
		if err := store.Append(releasedClaim("yoyodyne-ifd.264", at)); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
		tear(t, store.Path())
		if err := store.Append(releasedClaim("yoyodyne-ifd.211", at.Add(time.Minute))); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
		released, skipped, err := store.Scan()
		if err != nil {
			t.Fatalf("Scan() error = %v", err)
		}
		if len(released) != 2 || released[1].WorkItemID != "yoyodyne-ifd.211" {
			t.Fatalf("Scan() = %+v, want the release after the tear delivered", released)
		}
		if len(skipped) != 1 || skipped[0].Position != 1 {
			t.Fatalf("skipped = %+v, want the torn line at the second position", skipped)
		}
		if _, err := store.List(); err == nil {
			t.Fatal("List() error = nil, want the strict listing to refuse the torn log")
		}
	})

	t.Run("conversation events", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		store := newConversationStore(t, root)
		conversation := testConversation(t)
		if err := store.Save(conversation); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
		record := func(sequence uint64) {
			event, err := execution.NewEvent(conversation.ConversationID, sequence, time.Now().UTC(), execution.EventAgentMessage, "provider.claude-code", nil)
			if err != nil {
				t.Fatalf("NewEvent() error = %v", err)
			}
			if err := store.AppendEvent(event); err != nil {
				t.Fatalf("AppendEvent() error = %v", err)
			}
		}
		record(1)
		path, err := store.eventPathForConversation(conversation.ConversationID)
		if err != nil {
			t.Fatalf("eventPathForConversation() error = %v", err)
		}
		tear(t, path)
		record(2)
		events, skipped, err := store.ScanEvents(conversation.ConversationID)
		if err != nil {
			t.Fatalf("ScanEvents() error = %v", err)
		}
		if len(events) != 2 || events[1].Sequence != 2 {
			t.Fatalf("ScanEvents() = %+v, want the event after the tear delivered", events)
		}
		if len(skipped) != 1 || skipped[0].Position != 1 {
			t.Fatalf("skipped = %+v, want the torn line at the second position", skipped)
		}
		if _, err := store.LoadEvents(conversation.ConversationID); err == nil {
			t.Fatal("LoadEvents() error = nil, want the strict read to refuse the torn log")
		}
	})
}
