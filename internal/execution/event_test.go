package execution

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestEventRoundTrip(t *testing.T) {
	t.Parallel()

	want, err := NewEvent("run-123", 1, time.Date(2026, 8, 14, 12, 0, 0, 0, time.FixedZone("test", -7*60*60)), EventAgentMessage, "claude-code", map[string]string{"text": "done"})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	got, err := DecodeEvent(data)
	if err != nil {
		t.Fatalf("DecodeEvent() error = %v", err)
	}
	if got.RunID != want.RunID || got.Sequence != want.Sequence || got.Type != want.Type || got.Timestamp.Location() != time.UTC {
		t.Fatalf("DecodeEvent() = %#v, want %#v", got, want)
	}
}

func TestDecodeEventRejectsMalformedAndIncompleteEvents(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		`not-json`,
		`{}`,
		`{"schema_version":1,"run_id":"run-1","sequence":1,"timestamp":"2026-08-14T00:00:00Z","source":"test"}`,
	} {
		if _, err := DecodeEvent([]byte(input)); err == nil {
			t.Errorf("DecodeEvent(%q) error = nil", input)
		}
	}
}

func TestSequence(t *testing.T) {
	t.Parallel()

	sequence := NewSequence(8)
	if got := sequence.Next(); got != 9 {
		t.Fatalf("first Next() = %d, want 9", got)
	}
	if got := sequence.Next(); got != 10 {
		t.Fatalf("second Next() = %d, want 10", got)
	}
}

func TestNewEventRejectsUnencodablePayload(t *testing.T) {
	t.Parallel()

	_, err := NewEvent("run-1", 1, time.Now(), EventAgentMessage, "test", make(chan int))
	if err == nil || !strings.Contains(err.Error(), "encode event payload") {
		t.Fatalf("NewEvent() error = %v", err)
	}
}
