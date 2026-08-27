package execution

import (
	"encoding/json"
	"fmt"
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

// A run's event log is appended to and never rewritten, so a repository that
// has been running holds logs at every version the harness has ever written.
// Refusing an older one would not upgrade it, it would lose it — and what is in
// those logs is the only record of what the harness has already done and already
// spent. A version this build does not know how to read is refused for the
// opposite reason: reading it would be guessing at a shape somebody added later.
func TestDecodeEventReadsEveryVersionThisHarnessHasWritten(t *testing.T) {
	t.Parallel()

	for version := MinReadableEventSchemaVersion; version <= EventSchemaVersion; version++ {
		event := Event{
			SchemaVersion: version,
			RunID:         "run-1",
			Sequence:      1,
			Timestamp:     time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
			Type:          EventRunCompleted,
			Source:        "claude-code",
		}
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		decoded, err := DecodeEvent(data)
		if err != nil {
			t.Fatalf("DecodeEvent() at schema version %d error = %v", version, err)
		}
		if decoded.SchemaVersion != version {
			t.Fatalf("decoded schema version = %d, want %d preserved rather than rewritten", decoded.SchemaVersion, version)
		}
	}

	ahead := fmt.Sprintf(`{"schema_version":%d,"run_id":"run-1","sequence":1,"timestamp":"2026-08-14T00:00:00Z","type":"run.completed","source":"claude-code"}`,
		EventSchemaVersion+1)
	if _, err := DecodeEvent([]byte(ahead)); err == nil {
		t.Fatal("DecodeEvent() accepted a schema version this build has never written")
	}
}

// The role on a terminal is what the phase split attributes money by, so the
// version it arrived at is a fact about attribution rather than bookkeeping: at
// or above it, a terminal with no role failed to say whose invocation it ended.
// Nothing may quietly move it, because moving it forward would make every log in
// between readable positionally again.
func TestTerminalRoleSchemaVersionIsOneThisHarnessCanWrite(t *testing.T) {
	t.Parallel()

	if TerminalRoleSchemaVersion < MinReadableEventSchemaVersion || TerminalRoleSchemaVersion > EventSchemaVersion {
		t.Fatalf("terminals name a role from schema version %d, which is outside the readable range %d..%d",
			TerminalRoleSchemaVersion, MinReadableEventSchemaVersion, EventSchemaVersion)
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
