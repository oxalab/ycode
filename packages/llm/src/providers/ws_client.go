package providers

/*
Minimal RFC 6455 WebSocket CLIENT used by the codex WebSocket transport
(openai-codex-responses.ts uses the runtime WebSocket constructor; Go has no
runtime WebSocket, so this is the port's stand-in). Scope is deliberately
exactly what the reference uses: no subprotocols, no permessage-deflate, no
keepalive — TS leaves ping/pong to the runtime and never negotiates
extensions, so this client auto-pongs, rejects RSV bits, and negotiates
nothing.
*/

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
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// WebSocket opcodes (RFC 6455 §5.2).
const (
	wsOpContinuation = 0x0
	wsOpText         = 0x1
	wsOpBinary       = 0x2
	wsOpClose        = 0x8
	wsOpPing         = 0x9
	wsOpPong         = 0xA
)

// wsCloseError is the port of TS WebSocketCloseError
// (openai-codex-responses.ts:748-760): message
// "WebSocket closed[ <code>][ <reason>]:"-style, with the 1009 special case
// synthesizing " message too big".
type wsCloseError struct {
	Code     int
	Reason   string
	WasClean bool
}

func (e *wsCloseError) Error() string {
	reason := e.Reason
	if e.Code == 1009 {
		reason = " message too big"
	}
	msg := "WebSocket closed"
	if e.Code != 0 {
		msg += fmt.Sprintf(" %d", e.Code)
	}
	if reason != "" {
		msg += " " + reason
	}
	return strings.TrimSpace(msg)
}

// wsConn is a connected WebSocket client. All reads are driven from one
// goroutine (nextMessage); writes are mutex-guarded so close/pong can race
// the message writer.
type wsConn struct {
	raw    net.Conn
	br     *bufio.Reader
	wmu    sync.Mutex
	closed bool
}

// wsDial opens a WebSocket connection, performing the RFC 6455 opening
// handshake with the given headers (the codex handshake headers ride here,
// including OpenAI-Beta). http:// and ws:// dial plain TCP (tests);
// https:// and wss:// dial TLS.
func wsDial(ctx context.Context, rawURL string, header http.Header) (*wsConn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid WebSocket URL: %w", err)
	}
	var secure bool
	switch u.Scheme {
	case "ws", "http":
		secure = false
	case "wss", "https":
		secure = true
	default:
		return nil, fmt.Errorf("unsupported WebSocket scheme: %s", u.Scheme)
	}
	host := u.Host
	if u.Port() == "" {
		if secure {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	dialer := &net.Dialer{}
	var conn net.Conn
	if secure {
		conn, err = dialer.DialContext(ctx, "tcp", host)
		if err == nil {
			tlsConn := tls.Client(conn, &tls.Config{ServerName: u.Hostname()})
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				conn.Close()
				return nil, err
			}
			conn = tlsConn
		}
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", host)
	}
	if err != nil {
		return nil, err
	}

	// Opening handshake (RFC 6455 §4.1).
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)

	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}

	var req strings.Builder
	req.WriteString("GET " + path + " HTTP/1.1\r\n")
	req.WriteString("Host: " + u.Host + "\r\n")
	req.WriteString("Upgrade: websocket\r\n")
	req.WriteString("Connection: Upgrade\r\n")
	req.WriteString("Sec-WebSocket-Key: " + key + "\r\n")
	req.WriteString("Sec-WebSocket-Version: 13\r\n")
	for name, values := range header {
		for _, value := range values {
			req.WriteString(name + ": " + value + "\r\n")
		}
	}
	req.WriteString("\r\n")

	if _, err := conn.Write([]byte(req.String())); err != nil {
		conn.Close()
		return nil, err
	}

	br := bufio.NewReader(conn)
	hreq := &http.Request{Method: "GET", URL: u, Header: header}
	resp, err := http.ReadResponse(br, hreq)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("WebSocket handshake failed: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		conn.Close()
		return nil, fmt.Errorf("WebSocket handshake rejected: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != computeWSAccept(key) {
		resp.Body.Close()
		conn.Close()
		return nil, errors.New("WebSocket handshake failed: invalid Sec-WebSocket-Accept")
	}

	return &wsConn{raw: conn, br: br}, nil
}

func computeWSAccept(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// nextMessage returns the next complete text message, handling control
// frames inline (auto-pong, close) and reassembling fragmented messages.
// It blocks until a message is available, the socket dies, or the peer
// closes — mirroring TS parseWebSocket, where a close before completion is
// surfaced as an error and close-after-completion ends the stream cleanly
// (the codex layer owns the sawCompletion distinction).
func (c *wsConn) nextMessage() (string, error) {
	var assembled []byte
	fragmented := false
	for {
		fin, opcode, payload, err := c.readFrame()
		if err != nil {
			return "", err
		}
		switch opcode {
		case wsOpText, wsOpBinary:
			assembled = append(assembled[:0], payload...)
			if fin {
				return string(assembled), nil
			}
			fragmented = true
		case wsOpContinuation:
			if !fragmented {
				return "", errors.New("WebSocket protocol error: unexpected continuation frame")
			}
			assembled = append(assembled, payload...)
			if fin {
				return string(assembled), nil
			}
		case wsOpClose:
			code, reason := parseWSPayloadClose(payload)
			c.writeCloseFrame(code, reason)
			return "", &wsCloseError{Code: code, Reason: reason, WasClean: true}
		case wsOpPing:
			c.writeControl(wsOpPong, payload)
		case wsOpPong:
			// Unsolicited pongs are ignored.
		default:
			return "", fmt.Errorf("WebSocket protocol error: unexpected opcode %d", opcode)
		}
	}
}

// readFrame reads one frame. Server frames are unmasked per RFC; a masked
// server frame is tolerated by unmasking.
func (c *wsConn) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	var hdr [2]byte
	if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
		return false, 0, nil, err
	}
	fin = hdr[0]&0x80 != 0
	if hdr[0]&0x70 != 0 {
		return false, 0, nil, errors.New("WebSocket protocol error: reserved bits set (no extensions negotiated)")
	}
	opcode = hdr[0] & 0x0F
	masked := hdr[1]&0x80 != 0
	length := uint64(hdr[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(c.br, maskKey[:]); err != nil {
			return false, 0, nil, err
		}
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}
	return fin, opcode, payload, nil
}

// WriteText sends one text message. Client frames MUST be masked.
func (c *wsConn) WriteText(text string) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.writeFrameLocked(wsOpText, true, []byte(text))
}

func (c *wsConn) writeCloseFrame(code int, reason string) {
	payload := make([]byte, 2, 2+len(reason))
	binary.BigEndian.PutUint16(payload, uint16(code))
	payload = append(payload, []byte(reason)...)
	_ = c.writeFrameLocked(wsOpClose, true, payload)
}

func (c *wsConn) writeControl(opcode byte, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.writeFrameLocked(opcode, true, payload)
}

// Close performs a closing handshake (best effort) and shuts the socket.
func (c *wsConn) Close(code int, reason string) {
	c.wmu.Lock()
	if !c.closed {
		c.closed = true
		c.writeCloseFrameLocked(code, reason)
	}
	c.wmu.Unlock()
	_ = c.raw.Close()
}

func (c *wsConn) writeCloseFrameLocked(code int, reason string) {
	payload := make([]byte, 2, 2+len(reason))
	binary.BigEndian.PutUint16(payload, uint16(code))
	payload = append(payload, []byte(reason)...)
	_ = c.writeFrameLocked(wsOpClose, true, payload)
}

func (c *wsConn) writeFrameLocked(opcode byte, fin bool, payload []byte) error {
	if c.closed {
		return errors.New("WebSocket connection closed")
	}
	var hdr []byte
	first := byte(opcode)
	if fin {
		first |= 0x80
	}
	hdr = append(hdr, first)
	length := len(payload)
	maskBit := byte(0x80)
	switch {
	case length < 126:
		hdr = append(hdr, maskBit|byte(length))
	case length <= 0xFFFF:
		hdr = append(hdr, maskBit|126)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(length))
		hdr = append(hdr, ext[:]...)
	default:
		hdr = append(hdr, maskBit|127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(length))
		hdr = append(hdr, ext[:]...)
	}
	if _, err := c.raw.Write(hdr); err != nil {
		return err
	}
	var maskKey [4]byte
	if _, err := rand.Read(maskKey[:]); err != nil {
		return err
	}
	if _, err := c.raw.Write(maskKey[:]); err != nil {
		return err
	}
	masked := make([]byte, length)
	for i := range payload {
		masked[i] = payload[i] ^ maskKey[i%4]
	}
	_, err := c.raw.Write(masked)
	return err
}

// isOpen reports whether the socket is still usable — the port of
// isWebSocketReusable (readyState OPEN or unknown).
func (c *wsConn) isOpen() bool {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return !c.closed
}

// watchContext closes the socket when ctx is cancelled, making blocking
// reads fail; abort semantics match TS (abort during handshake/stream closes
// the socket). The returned stop func detaches the watcher.
func (c *wsConn) watchContext(ctx context.Context) (stop func()) {
	if ctx == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			c.Close(1000, "aborted")
		case <-done:
		}
	}()
	return func() { close(done) }
}

// wsDeadlineWatchdog is not needed — context cancellation closes the socket
// (see watchContext); TS likewise has no read timeouts.

func parseWSPayloadClose(payload []byte) (int, string) {
	if len(payload) >= 2 {
		code := int(binary.BigEndian.Uint16(payload[:2]))
		return code, strings.TrimSpace(string(payload[2:]))
	}
	return 1005, ""
}
