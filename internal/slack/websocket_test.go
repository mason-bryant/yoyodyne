package slack

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The handshake is the only thing that separates a websocket server from
// anything else answering on that address. A client that accepted a 101 without
// checking the key would go on to parse whatever arrived as frames.
func TestTheHandshakeIsCheckedRatherThanAssumed(t *testing.T) {
	t.Parallel()

	t.Run("a server that proves it understood the key is accepted", func(t *testing.T) {
		t.Parallel()
		server := startWebSocketServer(t, func(peer *serverSocket) {
			peer.writeText([]byte(`{"type":"hello"}`))
		})
		socket, err := dialWebSocket(context.Background(), server.url, server.dial)
		if err != nil {
			t.Fatalf("dialWebSocket() error = %v", err)
		}
		defer socket.Close()
		message, err := socket.ReadMessage(time.Now().Add(time.Second))
		if err != nil || string(message) != `{"type":"hello"}` {
			t.Fatalf("ReadMessage() = %q, %v", message, err)
		}
	})

	t.Run("a wrong accept key is refused", func(t *testing.T) {
		t.Parallel()
		server := startRawServer(t, func(conn net.Conn, reader *bufio.Reader) {
			readHandshakeRequest(t, reader)
			writeHandshakeResponse(conn, "not-the-accept-key")
		})
		if _, err := dialWebSocket(context.Background(), server.url, server.dial); err == nil {
			t.Fatal("dialWebSocket() = nil, want a server that did not prove itself refused")
		}
	})

	t.Run("anything other than an upgrade is refused", func(t *testing.T) {
		t.Parallel()
		server := startRawServer(t, func(conn net.Conn, reader *bufio.Reader) {
			readHandshakeRequest(t, reader)
			conn.Write([]byte("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n"))
		})
		if _, err := dialWebSocket(context.Background(), server.url, server.dial); err == nil {
			t.Fatal("dialWebSocket() = nil, want a refusal")
		}
	})
}

// A connection that does not answer pings is a connection the peer drops. The
// caller must not have to remember to answer them, because a caller that forgets
// has a sink that silently stops reporting.
func TestAPingIsAnsweredWithoutTheCallerAsking(t *testing.T) {
	t.Parallel()

	answered := make(chan []byte, 1)
	server := startWebSocketServer(t, func(peer *serverSocket) {
		peer.writeFrame(opcodePing, []byte("alive"))
		frame := peer.readFrame()
		if frame.opcode == opcodePong {
			answered <- frame.payload
		}
		peer.writeText([]byte(`{"type":"hello"}`))
	})

	socket, err := dialWebSocket(context.Background(), server.url, server.dial)
	if err != nil {
		t.Fatalf("dialWebSocket() error = %v", err)
	}
	defer socket.Close()
	message, err := socket.ReadMessage(time.Now().Add(2 * time.Second))
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if string(message) != `{"type":"hello"}` {
		t.Fatalf("ReadMessage() = %q, want the data message the ping preceded", message)
	}
	select {
	case payload := <-answered:
		if string(payload) != "alive" {
			t.Fatalf("pong payload = %q, want the ping's own payload", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("the ping went unanswered")
	}
}

// Slack may split a message across frames. A client that returned the first
// fragment would hand half a JSON document to the decoder.
func TestAFragmentedMessageArrivesWhole(t *testing.T) {
	t.Parallel()

	server := startWebSocketServer(t, func(peer *serverSocket) {
		peer.writeFragment(opcodeText, []byte(`{"type":`), false)
		peer.writeFragment(opcodeContinuation, []byte(`"hello"}`), true)
	})
	socket, err := dialWebSocket(context.Background(), server.url, server.dial)
	if err != nil {
		t.Fatalf("dialWebSocket() error = %v", err)
	}
	defer socket.Close()
	message, err := socket.ReadMessage(time.Now().Add(time.Second))
	if err != nil || string(message) != `{"type":"hello"}` {
		t.Fatalf("ReadMessage() = %q, %v, want the whole message", message, err)
	}
}

// Slack cycles Socket Mode connections routinely, so an orderly close is the
// ordinary end of one rather than a failure. It has to be reported as such, or
// every refresh would look like an outage in the sink's log.
func TestAnOrderlyCloseIsReportedAsTheConnectionEnding(t *testing.T) {
	t.Parallel()

	server := startWebSocketServer(t, func(peer *serverSocket) {
		peer.writeFrame(opcodeClose, nil)
		// The client echoes the close before it gives up, and reading it here
		// keeps the pipe from blocking that write.
		peer.readFrame()
	})
	socket, err := dialWebSocket(context.Background(), server.url, server.dial)
	if err != nil {
		t.Fatalf("dialWebSocket() error = %v", err)
	}
	defer socket.Close()
	if _, err := socket.ReadMessage(time.Now().Add(time.Second)); !errors.Is(err, errConnectionClosed) {
		t.Fatalf("ReadMessage() error = %v, want the connection reported as closed", err)
	}
}

// A peer that goes away without saying so is the failure that otherwise leaves a
// sink connected to nothing and reporting no problem.
func TestSilencePastTheDeadlineEndsTheRead(t *testing.T) {
	t.Parallel()

	server := startWebSocketServer(t, func(peer *serverSocket) {
		<-peer.done
	})
	socket, err := dialWebSocket(context.Background(), server.url, server.dial)
	if err != nil {
		t.Fatalf("dialWebSocket() error = %v", err)
	}
	defer socket.Close()
	if _, err := socket.ReadMessage(time.Now().Add(50 * time.Millisecond)); err == nil {
		t.Fatal("ReadMessage() = nil, want a silent connection to be given up on")
	}
}

// testServer is the other end of one connection plus the dial function that
// reaches it. It is an in-memory pipe rather than a listening socket, so the
// test exercises the real handshake and the real framing without a network.
type testServer struct {
	url  string
	dial dialFunc
	done chan struct{}
}

func startRawServer(t *testing.T, handle func(net.Conn, *bufio.Reader)) *testServer {
	t.Helper()
	client, peer := net.Pipe()
	server := &testServer{
		url:  "ws://slack.test/link",
		done: make(chan struct{}),
		dial: func(context.Context, string, string) (net.Conn, error) { return client, nil },
	}
	go func() {
		defer peer.Close()
		handle(peer, bufio.NewReader(peer))
		<-server.done
	}()
	t.Cleanup(func() {
		close(server.done)
		client.Close()
	})
	return server
}

// serverSocket is the peer end of one connection: it writes unmasked frames, as
// a server must, and reads the client's masked ones.
type serverSocket struct {
	conn   net.Conn
	reader *bufio.Reader
	done   chan struct{}
	t      *testing.T
}

func startWebSocketServer(t *testing.T, handle func(*serverSocket)) *testServer {
	t.Helper()
	var server *testServer
	server = startRawServer(t, func(conn net.Conn, reader *bufio.Reader) {
		key := readHandshakeRequest(t, reader)
		writeHandshakeResponse(conn, acceptKey(key))
		handle(&serverSocket{conn: conn, reader: reader, done: server.done, t: t})
	})
	return server
}

func readHandshakeRequest(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	request, err := http.ReadRequest(reader)
	if err != nil {
		t.Errorf("ReadRequest() error = %v", err)
		return ""
	}
	if !strings.EqualFold(request.Header.Get("Upgrade"), "websocket") {
		t.Errorf("Upgrade = %q, want the client to ask for a websocket", request.Header.Get("Upgrade"))
	}
	return request.Header.Get("Sec-WebSocket-Key")
}

func writeHandshakeResponse(conn net.Conn, accept string) {
	conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"))
}

func (s *serverSocket) writeText(payload []byte) { s.writeFrame(opcodeText, payload) }

func (s *serverSocket) writeFrame(opcode byte, payload []byte) {
	s.writeFragment(opcode, payload, true)
}

// writeFragment writes one unmasked frame, which is what a server sends.
func (s *serverSocket) writeFragment(opcode byte, payload []byte, fin bool) {
	header := []byte{opcode, 0}
	if fin {
		header[0] |= 0x80
	}
	if length := len(payload); length < 126 {
		header[1] = byte(length)
	} else {
		header[1] = 126
		var extended [2]byte
		binary.BigEndian.PutUint16(extended[:], uint16(length))
		header = append(header, extended[:]...)
	}
	if _, err := s.conn.Write(append(header, payload...)); err != nil {
		s.t.Errorf("server write error = %v", err)
	}
}

func (s *serverSocket) readFrame() frame {
	read := &websocketConn{conn: s.conn, reader: s.reader}
	if err := s.conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		s.t.Errorf("SetReadDeadline() error = %v", err)
	}
	decoded, err := read.readFrame()
	if err != nil {
		s.t.Errorf("server readFrame() error = %v", err)
	}
	return decoded
}
