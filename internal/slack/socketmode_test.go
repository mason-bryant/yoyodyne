package slack

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

// Slack redelivers anything it is not acknowledged for. The sink acts on none of
// it today, so the acknowledgment is the whole of what it owes the workspace:
// without it, one reply would arrive over and over for as long as the sink runs.
func TestEveryInboundEnvelopeIsAcknowledgedEvenThoughNothingActsOnIt(t *testing.T) {
	t.Parallel()

	received := make(chan socketEnvelope, 1)
	acknowledged := make(chan string, 1)
	server := startWebSocketServer(t, func(peer *serverSocket) {
		peer.writeText([]byte(`{"type":"hello","num_connections":1}`))
		peer.writeText([]byte(`{"type":"events_api","envelope_id":"env-1","payload":{"event":{"text":"look at this"}}}`))
		var ack struct {
			EnvelopeID string `json:"envelope_id"`
		}
		if err := json.Unmarshal(peer.readFrame().payload, &ack); err != nil {
			t.Errorf("Unmarshal() error = %v", err)
		}
		acknowledged <- ack.EnvelopeID
		peer.writeText([]byte(`{"type":"disconnect","reason":"refresh_requested"}`))
	})

	link := &connection{
		api:  connectionAPI(t, server.url),
		dial: server.dial,
		log:  func(string, ...any) {},
		handle: func(_ context.Context, envelope socketEnvelope) {
			received <- envelope
		},
		timeout: 2 * time.Second,
	}

	err := link.session(context.Background())
	// A refresh is Slack cycling the connection, which is routine: it has to
	// read as the connection ending rather than as something going wrong, or
	// every refresh would back the sink off as if the workspace were refusing.
	if !errors.Is(err, errConnectionClosed) {
		t.Fatalf("session() error = %v, want a routine disconnect reported as a close", err)
	}
	select {
	case id := <-acknowledged:
		if id != "env-1" {
			t.Fatalf("acknowledged %q, want the envelope Slack sent", id)
		}
	default:
		t.Fatal("the inbound envelope was never acknowledged")
	}
	select {
	case envelope := <-received:
		if envelope.Type != "events_api" {
			t.Fatalf("handler saw %q, want the message handed on unread", envelope.Type)
		}
	default:
		t.Fatal("the inbound message never reached the handler")
	}
}

// An app whose connection Slack has disabled is not a connection to keep
// reopening: the operator has to reinstall or re-enable it, and a sink that
// reconnected in a tight loop would say nothing useful about why.
func TestADisabledConnectionIsReportedRatherThanRetriedQuietly(t *testing.T) {
	t.Parallel()

	server := startWebSocketServer(t, func(peer *serverSocket) {
		peer.writeText([]byte(`{"type":"hello"}`))
		peer.writeText([]byte(`{"type":"disconnect","reason":"link_disabled"}`))
	})
	link := &connection{
		api:     connectionAPI(t, server.url),
		dial:    server.dial,
		log:     func(string, ...any) {},
		timeout: 2 * time.Second,
	}
	err := link.session(context.Background())
	if err == nil || errors.Is(err, errConnectionClosed) {
		t.Fatalf("session() error = %v, want a disabled app reported as a problem", err)
	}
}

// A message this client cannot read is Slack saying something newer than the
// code knows. Dropping a working connection over it would trade a message
// nobody needed for every message that came after it.
func TestAnUnreadableMessageDoesNotEndTheConnection(t *testing.T) {
	t.Parallel()

	server := startWebSocketServer(t, func(peer *serverSocket) {
		peer.writeText([]byte(`{"type":`))
		peer.writeText([]byte(`{"type":"disconnect","reason":"refresh_requested"}`))
	})
	var logged int
	link := &connection{
		api:     connectionAPI(t, server.url),
		dial:    server.dial,
		log:     func(string, ...any) { logged++ },
		timeout: 2 * time.Second,
	}
	if err := link.session(context.Background()); !errors.Is(err, errConnectionClosed) {
		t.Fatalf("session() error = %v, want the connection to survive to its ordinary end", err)
	}
	if logged == 0 {
		t.Fatal("a message that could not be read must be said out loud rather than swallowed")
	}
}

// connectionAPI is a client whose apps.connections.open points at the test's own
// pipe, which is what makes the connection exercisable without a network.
func connectionAPI(t *testing.T, url string) *API {
	t.Helper()
	return newTestAPI(t, func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, map[string]any{"ok": true, "url": url})
	})
}
