package slack

// The Socket Mode connection.
//
// Socket Mode rather than the Events API because the harness runs on operators'
// machines behind NAT: the Events API would demand a public HTTPS endpoint per
// operator, which is not a setup anybody would follow. It is also bidirectional
// on one connection, and that is what keeps the inbound half a feature rather
// than a transport redesign — the websocket that posts is the websocket replies
// will arrive on.
//
// Today nothing acts on what arrives. Every inbound envelope is acknowledged, so
// Slack stops redelivering it, and then handed to a handler the sink can be
// given. The default handler does nothing, which is the honest shape of one-way
// reporting: the connection is open, the messages are seen, and no reply steers
// anything until the inbound mapping exists and an operator has named themselves
// in the allow-list.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	// socketReadTimeout is how long a connection may say nothing before it is
	// treated as dead. Slack pings a Socket Mode connection every few tens of
	// seconds, so silence past this is a connection that has gone away without
	// saying so — the failure that otherwise leaves a sink connected to nothing
	// and reporting no problem.
	socketReadTimeout = 90 * time.Second
	// The reconnection backoff. It starts short because most disconnections are
	// Slack asking for a refresh, and grows because a workspace that is refusing
	// is not helped by being asked faster.
	minReconnectWait = time.Second
	maxReconnectWait = time.Minute
)

// socketEnvelope is one message from Slack over the connection. The fields are
// the union of what the message types carry; which of them are set is decided by
// the type.
type socketEnvelope struct {
	Type string `json:"type"`
	// EnvelopeID is present on anything Slack expects an acknowledgment for. An
	// unacknowledged envelope is redelivered, so this is answered whether or not
	// anything is done with the payload.
	EnvelopeID string `json:"envelope_id,omitempty"`
	// Payload is the event as the Events API would have delivered it. It is
	// carried unread: reading it is the inbound half's work.
	Payload json.RawMessage `json:"payload,omitempty"`
	// Reason is why Slack is disconnecting — a refresh it schedules routinely, a
	// warning before one, or an app whose connection has been disabled.
	Reason string `json:"reason,omitempty"`
	// NumConnections and DebugInfo arrive on the hello message and are read for
	// one thing only: a workspace holding more connections than this sink opened
	// is a second sink, which is what duplicate threads look like from here.
	NumConnections int `json:"num_connections,omitempty"`
}

const (
	socketHello        = "hello"
	socketDisconnect   = "disconnect"
	disconnectRefresh  = "refresh_requested"
	disconnectDisabled = "link_disabled"
)

// InboundHandler is what the sink does with a message that arrives on the
// connection. It exists as a seam rather than as behavior: the inbound half maps
// a thread reply onto the existing directive record, and until it does, a sink
// that quietly did something with replies would be a channel doing more than its
// documentation says.
type InboundHandler func(ctx context.Context, envelope socketEnvelope)

// connection keeps one Socket Mode connection open for as long as its context
// lives, reopening it whenever Slack or the network closes it.
type connection struct {
	api     *API
	dial    dialFunc
	handle  InboundHandler
	log     func(format string, args ...any)
	timeout time.Duration
}

// run holds the connection open until the context ends. It never returns a
// failure, because there is no failure here a run should learn about: a sink
// that cannot connect still posts, and reporting is observation rather than a
// gate.
func (c *connection) run(ctx context.Context) {
	wait := minReconnectWait
	for ctx.Err() == nil {
		err := c.session(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err == nil || errors.Is(err, errConnectionClosed):
			// An orderly close is Slack's routine refresh. Reconnecting
			// immediately is what it is asking for, so the backoff resets.
			wait = minReconnectWait
		case PermanentError(err):
			c.log("the Slack connection was refused and will stay refused until it is fixed: %v", err)
			wait = maxReconnectWait
		default:
			c.log("the Slack connection dropped; reconnecting in %s: %v", wait, err)
		}
		if !sleepUntil(ctx, wait) {
			return
		}
		if wait < maxReconnectWait {
			wait *= 2
			if wait > maxReconnectWait {
				wait = maxReconnectWait
			}
		}
	}
}

// session opens one connection and reads it until it ends.
func (c *connection) session(ctx context.Context) error {
	url, err := c.api.OpenConnection(ctx)
	if err != nil {
		return err
	}
	socket, err := dialWebSocket(ctx, url, c.dial)
	if err != nil {
		return err
	}
	defer socket.Close()
	// A read blocks until something arrives or the deadline passes, and neither
	// is the operator pressing Ctrl-C. Closing the socket when the context ends
	// is what turns that into an immediate stop rather than a wait of up to the
	// read timeout while the process looks hung.
	session, stopWatching := context.WithCancel(ctx)
	defer stopWatching()
	go func() {
		<-session.Done()
		socket.Close()
	}()

	timeout := c.timeout
	if timeout <= 0 {
		timeout = socketReadTimeout
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		raw, err := socket.ReadMessage(time.Now().Add(timeout))
		if err != nil {
			return err
		}
		var envelope socketEnvelope
		if err := json.Unmarshal(raw, &envelope); err != nil {
			// A message this client cannot read is Slack saying something newer
			// than this code knows. It is logged and skipped rather than taken
			// as a reason to drop a working connection.
			c.log("a Slack message could not be read and was skipped: %v", err)
			continue
		}
		if envelope.EnvelopeID != "" {
			if err := c.acknowledge(socket, envelope.EnvelopeID); err != nil {
				return err
			}
		}
		switch envelope.Type {
		case socketHello:
			c.log("connected to Slack over Socket Mode")
			if envelope.NumConnections > 1 {
				c.log("this workspace holds %d Socket Mode connections for this app; more than one sink per product opens more than one thread per work item", envelope.NumConnections)
			}
		case socketDisconnect:
			if envelope.Reason == disconnectDisabled {
				return fmt.Errorf("slack disabled this app's Socket Mode connection (%s)", envelope.Reason)
			}
			// A refresh is routine and expected: Slack cycles these
			// connections, and the caller reconnects without backing off.
			return errConnectionClosed
		default:
			if c.handle != nil {
				c.handle(ctx, envelope)
			}
		}
	}
}

// acknowledge answers an envelope so Slack stops redelivering it. It is done
// before anything else with the message, because an acknowledgment that waits
// for the work is an acknowledgment that arrives late when the work is slow.
func (c *connection) acknowledge(socket *websocketConn, envelopeID string) error {
	payload, err := json.Marshal(struct {
		EnvelopeID string `json:"envelope_id"`
	}{EnvelopeID: envelopeID})
	if err != nil {
		return fmt.Errorf("encode acknowledgment: %w", err)
	}
	if err := socket.WriteText(payload); err != nil {
		return fmt.Errorf("acknowledge %s: %w", envelopeID, err)
	}
	return nil
}

// sleepUntil waits, reporting whether the wait finished rather than the context
// ending.
func sleepUntil(ctx context.Context, wait time.Duration) bool {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
