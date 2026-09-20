package slack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// tear appends a torn line to one of the product's logs: a write a crash
// interrupted, closed off by the newline the record after it happened to begin
// with. What follows it is the record nobody has seen yet.
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

// One torn write must never starve reporting. Before this, a line the reports
// log's reader could not decode failed the whole pass, and every report behind
// it went unsaid for as long as the line stood — which was every pass, since
// nothing rewrites the log. Now the line holds its position, it is said once in
// the sink's own log and once in the channel, and the reports after it are
// delivered exactly as they would have been.
func TestATornLineIsSaidOnceAndTheRecordsAfterItAreStillDelivered(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	var logged []string
	harness.feed.Log = func(format string, args ...any) { logged = append(logged, format) }
	harness.file(t, "report-0123456789abcdef0123456789abcde0", report.SeverityNote, moment)
	cursors := harness.poll(t, harness.start(), notify.KindReportFiled)

	// A crash tears the next write, and the write after it lands cleanly.
	tear(t, harness.reports.Path())
	harness.file(t, "report-0123456789abcdef0123456789abcde1", report.SeverityNote, moment.Add(time.Minute))

	batch, err := harness.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v, want the pass to carry on past the torn line", err)
	}
	var said []Delivery
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == reportStream && delivery.Posts() {
			said = append(said, delivery)
		}
	}
	if len(said) != 2 || said[0].Notification.Event.Kind != notify.KindLogLineSkipped || said[1].Notification.Event.Kind != notify.KindReportFiled {
		t.Fatalf("said %+v, want the torn line said and then the report after it", said)
	}
	// The torn line is the second record, so it holds the second position and
	// the report after it holds the third, exactly as it would have without the
	// tear.
	if said[0].Cursor.Position != 2 || said[1].Cursor.Position != 3 {
		t.Fatalf("positions = %d, %d, want the torn line at 2 and the report after it at 3", said[0].Cursor.Position, said[1].Cursor.Position)
	}
	body := harness.rendered(t, said[0])
	if !strings.Contains(body, "reports log") || !strings.Contains(body, "line 2 (byte ") || !strings.Contains(body, "Warning") {
		t.Fatalf("rendered %q, want the log named, the line placed, and a warning", body)
	}
	if said[0].Notification.Topic.Kind != notify.TopicProduct || !said[0].Notification.Speaker.IsHarness() {
		t.Fatalf("notification = %+v, want the harness saying it about the product", said[0].Notification)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "could not be decoded and was read past, keeping its position") {
		t.Fatalf("logged %v, want the torn line said once in the sink's own log", logged)
	}

	// The next pass says nothing, in the log or the channel: the cursor stands
	// past the line and the reader does not meet it again.
	cursors = advanced(cursors, batch)
	harness.poll(t, cursors)
	if len(logged) != 1 {
		t.Fatalf("logged %v, want the torn line said once rather than on every pass", logged)
	}
	// And a report filed after all of that is delivered at the position it would
	// have had, with the torn line still counted.
	harness.file(t, "report-0123456789abcdef0123456789abcde2", report.SeverityNote, moment.Add(2*time.Minute))
	cursors = harness.poll(t, cursors, notify.KindReportFiled)
	if cursors.Streams[reportStream].Position != 4 {
		t.Fatalf("cursor = %#v, want the third report at the fourth position", cursors.Streams[reportStream])
	}
}

// The same rule holds on every log the sink reads by position, the conversation
// logs included — and a conversation's torn line is said without the
// conversation's identifier, which is not something anybody reading a channel
// does anything with.
func TestATornConversationLineIsSaidByRoleAndTheEventsAfterItAreRead(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	conversation := harness.converse(t, domain.RoleProductManager)
	harness.chatted(t, conversation, 1, execution.EventAgentMessage, map[string]any{"text": "what was said in the turn"})
	cursors := harness.poll(t, harness.start())

	tear(t, harness.eventLog(t, conversation))
	harness.chatted(t, conversation, 2, execution.EventTrackerActionApplied, map[string]any{
		"action_id": "t1.1",
		"turn":      1,
		"action": map[string]any{
			"action":      "create",
			"title":       "Conversation milestones reach Slack",
			"description": "the item's own words",
			"goal":        "Work the harness runs on its own is visible while it runs",
			"reason":      "the backlog moves invisibly today",
		},
		"work_item_id": "yoyodyne-ifd.114",
		"summary":      "admitted yoyodyne-ifd.114 to the backlog",
	})

	batch, err := harness.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v, want the pass to carry on past the torn line", err)
	}
	stream := conversationStream(conversation.ConversationID)
	var said []Delivery
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == stream && delivery.Posts() {
			said = append(said, delivery)
		}
	}
	if len(said) != 2 || said[0].Notification.Event.Kind != notify.KindLogLineSkipped || said[1].Notification.Event.Kind != notify.KindItemAdmitted {
		t.Fatalf("said %+v, want the torn line said and then the admission recorded after it", said)
	}
	body := harness.rendered(t, said[0])
	if !strings.Contains(body, "product manager's conversation log") {
		t.Fatalf("rendered %q, want the log named by the role whose conversation it is", body)
	}
	if strings.Contains(body, conversation.ConversationID) {
		t.Fatalf("rendered %q, which names the conversation by its identifier", body)
	}
	cursors = advanced(cursors, batch)
	harness.poll(t, cursors)
	if cursors.Streams[stream].Position != 3 {
		t.Fatalf("cursor = %#v, want the whole log read with the torn line counted", cursors.Streams[stream])
	}
}

// The amendment log advances by record rather than by proposal, so that a torn
// line — which says nothing about whether it was a proposal or a decision —
// holds a position the proposals behind it are counted past. A decision is read
// past in silence, and a sink that last ran on a build counting proposals is
// carried across to the same place: nothing already said is said again, and
// nothing since is lost.
func TestTheAmendmentLogAdvancesByRecordAndAnOldProposalCursorIsCarriedAcross(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.propose(t, "amendment-0123456789abcdef0123456789abcde0", moment)
	harness.propose(t, "amendment-0123456789abcdef0123456789abcde1", moment.Add(time.Minute))
	harness.decide(t, "amendment-0123456789abcdef0123456789abcde0")
	harness.propose(t, "amendment-0123456789abcdef0123456789abcde2", moment.Add(2*time.Minute))

	// A fresh sink says the three proposals and reads past the decision.
	cursors := harness.poll(t, harness.start(), notify.KindProposalRaised, notify.KindProposalRaised, notify.KindProposalRaised)
	if cursors.Streams[amendmentStream].Position != 4 {
		t.Fatalf("cursor = %#v, want every record counted, the decision included", cursors.Streams[amendmentStream])
	}

	// A sink whose last build said the first two proposals, counting proposals,
	// says only the third — and stands on the new stream afterwards, with the old
	// one no longer among the streams the pass reports, so it is dropped.
	old := harness.start()
	old.Streams[proposalStream] = Cursor{Position: 2}
	batch, err := harness.feed.Poll(context.Background(), old)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if _, kept := batch.Streams[proposalStream]; kept {
		t.Fatal("the pass still reports the stream counted by proposal, so its cursor would be carried forever")
	}
	carried := harness.poll(t, old, notify.KindProposalRaised)
	if carried.Streams[amendmentStream].Position != 4 {
		t.Fatalf("cursor = %#v, want the old count carried across to the record after the last proposal it said", carried.Streams[amendmentStream])
	}
}

// advanced is the cursors as the sink writes them once every delivery of one
// pass has been taken.
func advanced(cursors Cursors, batch Batch) Cursors {
	taken := Cursors{SchemaVersion: CursorsSchemaVersion, Since: cursors.Since, Streams: map[string]Cursor{}}
	for stream, cursor := range cursors.Streams {
		taken.Streams[stream] = cursor
	}
	for _, delivery := range batch.Deliveries {
		taken.Streams[delivery.Stream] = delivery.Cursor
	}
	return taken
}

// decide records the operator's decision on one proposal, which is the other
// kind of record the amendment log holds.
func (h *testHarness) decide(t *testing.T, id string) {
	t.Helper()
	records, err := h.amend.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	proposal, found := amendment.Find(records, id)
	if !found {
		t.Fatalf("no proposal %s to decide", id)
	}
	decision, err := proposal.Decide(amendment.VerdictApproved, amendment.DeciderOperator, "", moment.Add(time.Hour))
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if err := h.amend.Decide(decision); err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
}

// eventLog is where one conversation's events are, so a test can tear it.
func (h *testHarness) eventLog(t *testing.T, conversation runstate.Conversation) string {
	t.Helper()
	return filepath.Join(h.chats.Root(), conversation.ConversationID+".events.jsonl")
}
