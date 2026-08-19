package slack

// The websocket the Socket Mode connection runs over.
//
// It is written here rather than taken from a library because this repository
// depends on one module and nothing else, and a reporting channel is the last
// place to start widening the dependency surface: what the sink needs is a
// client handshake, masked frames out, unmasked frames in, and the two control
// frames that keep a connection honest. That is small enough to read in one
// sitting and small enough to test without a network.
//
// What it deliberately does not implement is everything else in RFC 6455:
// permessage-deflate, subprotocol negotiation, and continuation of interleaved
// messages beyond what Slack sends. A frame it does not understand closes the
// connection, and a closed connection is reconnected — which is the same
// recovery every other interruption here takes.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// websocketGUID is the constant RFC 6455 mixes into the key to prove the server
// understood the handshake rather than merely answering it.
const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	opcodeContinuation byte = 0x0
	opcodeText         byte = 0x1
	opcodeBinary       byte = 0x2
	opcodeClose        byte = 0x8
	opcodePing         byte = 0x9
	opcodePong         byte = 0xa
)

const (
	// maxFrameBytes bounds one frame's payload, and maxMessageBytes bounds a
	// message assembled from several. Both are far larger than anything Socket
	// Mode sends and exist so a peer that claims an enormous length is refused
	// rather than allocated for.
	maxFrameBytes   = 1 << 20
	maxMessageBytes = 4 << 20
	// handshakeTimeout bounds connecting. A handshake that has not finished by
	// then is one the sink retries.
	handshakeTimeout = 30 * time.Second
)

// errConnectionClosed reports the peer closing the connection in the orderly
// way. It is not a failure: Slack asks a Socket Mode client to reconnect
// routinely, so this is the ordinary end of a connection rather than the
// exceptional one.
var errConnectionClosed = errors.New("websocket closed by peer")

// dialFunc opens the transport a websocket runs over. It is injected so a test
// can hand back a pipe and exercise the real handshake and framing rather than
// a stand-in for them.
type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// websocketConn is one client connection. Reads happen on one goroutine and
// writes can happen on two — an acknowledgment and a pong — so writing is
// serialized and reading is not.
type websocketConn struct {
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex
	closed  bool
}

// dialWebSocket performs the client handshake and returns the open connection.
func dialWebSocket(ctx context.Context, rawURL string, dial dialFunc) (*websocketConn, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse websocket url: %w", err)
	}
	secure := false
	switch strings.ToLower(target.Scheme) {
	case "wss":
		secure = true
	case "ws":
	default:
		return nil, fmt.Errorf("websocket url scheme %q is not ws or wss", target.Scheme)
	}
	host := target.Host
	if target.Port() == "" {
		if secure {
			host = net.JoinHostPort(target.Hostname(), "443")
		} else {
			host = net.JoinHostPort(target.Hostname(), "80")
		}
	}

	if dial == nil {
		dial = defaultDial
	}
	handshakeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()

	conn, err := dial(handshakeCtx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", host, err)
	}
	if secure {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: target.Hostname()})
		if err := tlsConn.HandshakeContext(handshakeCtx); err != nil {
			conn.Close()
			return nil, fmt.Errorf("tls handshake with %s: %w", target.Hostname(), err)
		}
		conn = tlsConn
	}

	if deadline, ok := handshakeCtx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return nil, fmt.Errorf("set handshake deadline: %w", err)
		}
	}
	key, err := handshakeKey()
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := writeHandshake(conn, target, key); err != nil {
		conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	if err := readHandshake(reader, key); err != nil {
		conn.Close()
		return nil, err
	}
	// The handshake deadline must not outlive the handshake: the connection is
	// then idle for as long as nothing happens on it, which is normal.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("clear handshake deadline: %w", err)
	}
	return &websocketConn{conn: conn, reader: reader}, nil
}

func defaultDial(ctx context.Context, network, address string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, address)
}

func handshakeKey() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate websocket key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func writeHandshake(conn net.Conn, target *url.URL, key string) error {
	path := target.RequestURI()
	if path == "" {
		path = "/"
	}
	request := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + target.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		return fmt.Errorf("write websocket handshake: %w", err)
	}
	return nil
}

// readHandshake checks the server both accepted the upgrade and proved it
// understood the key. A 101 with the wrong accept header is something other
// than a websocket server answering, and treating it as one would leave the
// sink parsing arbitrary bytes as frames.
func readHandshake(reader *bufio.Reader, key string) error {
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		return fmt.Errorf("read websocket handshake: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSwitchingProtocols {
		return fmt.Errorf("websocket handshake refused: %s", response.Status)
	}
	if !strings.EqualFold(strings.TrimSpace(response.Header.Get("Upgrade")), "websocket") {
		return errors.New("websocket handshake did not upgrade")
	}
	if response.Header.Get("Sec-WebSocket-Accept") != acceptKey(key) {
		return errors.New("websocket handshake accept key does not match")
	}
	return nil
}

func acceptKey(key string) string {
	sum := sha1.Sum([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// ReadMessage returns the next data message, answering the control frames that
// arrive before it. A ping is ponged here rather than reported, because a
// caller that had to remember to answer pings is a caller whose connection dies
// the first time it forgets.
//
// The deadline bounds waiting for one message. Slack pings a Socket Mode
// connection regularly, so a connection that says nothing for longer than the
// deadline is a connection that has silently gone away, and reporting that is
// what gets it reopened.
func (w *websocketConn) ReadMessage(deadline time.Time) ([]byte, error) {
	if err := w.conn.SetReadDeadline(deadline); err != nil {
		return nil, fmt.Errorf("set read deadline: %w", err)
	}
	var assembled []byte
	var assembling bool
	for {
		frame, err := w.readFrame()
		if err != nil {
			return nil, err
		}
		switch frame.opcode {
		case opcodePing:
			if err := w.writeFrame(opcodePong, frame.payload); err != nil {
				return nil, err
			}
		case opcodePong:
			// Nothing to do: a pong is the peer answering, which the read
			// itself already recorded as the connection being alive.
		case opcodeClose:
			// The close is echoed so the peer sees an orderly shutdown, and a
			// failure to echo changes nothing: the connection is over either way.
			_ = w.writeFrame(opcodeClose, frame.payload)
			return nil, errConnectionClosed
		case opcodeText, opcodeBinary:
			if assembling {
				return nil, errors.New("websocket peer interleaved a new message inside a fragmented one")
			}
			if frame.fin {
				return frame.payload, nil
			}
			assembled = frame.payload
			assembling = true
		case opcodeContinuation:
			if !assembling {
				return nil, errors.New("websocket continuation frame with nothing to continue")
			}
			if len(assembled)+len(frame.payload) > maxMessageBytes {
				return nil, fmt.Errorf("websocket message exceeds %d bytes", maxMessageBytes)
			}
			assembled = append(assembled, frame.payload...)
			if frame.fin {
				return assembled, nil
			}
		default:
			return nil, fmt.Errorf("websocket opcode %#x is not supported", frame.opcode)
		}
	}
}

type frame struct {
	fin     bool
	opcode  byte
	payload []byte
}

func (w *websocketConn) readFrame() (frame, error) {
	var header [2]byte
	if _, err := io.ReadFull(w.reader, header[:]); err != nil {
		return frame{}, readError(err)
	}
	fin := header[0]&0x80 != 0
	if header[0]&0x70 != 0 {
		return frame{}, errors.New("websocket reserved bits are set; no extension was negotiated")
	}
	opcode := header[0] & 0x0f
	masked := header[1]&0x80 != 0
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(w.reader, extended[:]); err != nil {
			return frame{}, readError(err)
		}
		length = uint64(binary.BigEndian.Uint16(extended[:]))
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(w.reader, extended[:]); err != nil {
			return frame{}, readError(err)
		}
		length = binary.BigEndian.Uint64(extended[:])
	}
	if length > maxFrameBytes {
		return frame{}, fmt.Errorf("websocket frame claims %d bytes, limit is %d", length, maxFrameBytes)
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(w.reader, mask[:]); err != nil {
			return frame{}, readError(err)
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(w.reader, payload); err != nil {
		return frame{}, readError(err)
	}
	if masked {
		for index := range payload {
			payload[index] ^= mask[index%4]
		}
	}
	return frame{fin: fin, opcode: opcode, payload: payload}, nil
}

// readError names a peer that went away mid-frame as the connection ending,
// which is what it is. Anything else is reported as itself.
func readError(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return errConnectionClosed
	}
	return fmt.Errorf("read websocket frame: %w", err)
}

// WriteText sends one text message.
func (w *websocketConn) WriteText(payload []byte) error {
	return w.writeFrame(opcodeText, payload)
}

// writeFrame sends one masked frame. Every client frame is masked, which the
// specification requires and servers enforce.
func (w *websocketConn) writeFrame(opcode byte, payload []byte) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	if w.closed {
		return errConnectionClosed
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return fmt.Errorf("generate frame mask: %w", err)
	}

	header := make([]byte, 0, 14)
	header = append(header, 0x80|opcode)
	length := len(payload)
	switch {
	case length < 126:
		header = append(header, 0x80|byte(length))
	case length <= 0xffff:
		header = append(header, 0x80|126, byte(length>>8), byte(length))
	default:
		header = append(header, 0x80|127)
		var extended [8]byte
		binary.BigEndian.PutUint64(extended[:], uint64(length))
		header = append(header, extended[:]...)
	}
	header = append(header, mask[:]...)

	masked := make([]byte, length)
	for index := range payload {
		masked[index] = payload[index] ^ mask[index%4]
	}
	if err := w.conn.SetWriteDeadline(time.Now().Add(requestTimeout)); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
	if _, err := w.conn.Write(append(header, masked...)); err != nil {
		return fmt.Errorf("write websocket frame: %w", err)
	}
	return nil
}

// Close ends the connection, telling the peer first when it can. Closing twice
// is a no-op, so a caller can defer it beside the error path that already
// closed it.
func (w *websocketConn) Close() error {
	w.writeMu.Lock()
	if w.closed {
		w.writeMu.Unlock()
		return nil
	}
	w.closed = true
	w.writeMu.Unlock()
	return w.conn.Close()
}
