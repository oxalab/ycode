package providers

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	src "ycode/packages/llm/src"
)

/*
Codex WebSocket transport tests (story 39). The server half is a hand-rolled
RFC 6455 responder on httptest + http.Hijacker: it answers WebSocket upgrade
requests with 101 and speaks frames, and falls through to a codex SSE
response for anything else — mirroring how the real fallback composes on one
endpoint.
*/

// -----------------------------------------------------------------------------
// Server-side frame helpers (server frames are unmasked; client frames masked)
// -----------------------------------------------------------------------------

type wsTestConn struct {
	conn net.Conn
	br   *bufio.Reader
}

func (c *wsTestConn) readFrame() (byte, []byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
		return 0, nil, err
	}
	opcode := hdr[0] & 0x0F
	masked := hdr[1]&0x80 != 0
	length := uint64(hdr[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(c.br, maskKey[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		if masked {
			payload[i] ^= maskKey[i%4]
		}
	}
	return opcode, payload, nil
}

func (c *wsTestConn) writeFrame(opcode byte, payload []byte) error {
	first := byte(opcode) | 0x80
	var hdr []byte
	hdr = append(hdr, first)
	switch {
	case len(payload) < 126:
		hdr = append(hdr, byte(len(payload)))
	case len(payload) <= 0xFFFF:
		hdr = append(hdr, 126)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(len(payload)))
		hdr = append(hdr, ext[:]...)
	default:
		hdr = append(hdr, 127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(len(payload)))
		hdr = append(hdr, ext[:]...)
	}
	if _, err := c.conn.Write(hdr); err != nil {
		return err
	}
	_, err := c.conn.Write(payload)
	return err
}

func (c *wsTestConn) writeTextJSON(event map[string]interface{}) error {
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return c.writeFrame(0x1, encoded)
}

// codexWSCompleted builds a response.completed event for the given response id.
func codexWSCompleted(id, text string) map[string]interface{} {
	return map[string]interface{}{
		"type": "response.completed",
		"response": map[string]interface{}{
			"id":     id,
			"status": "completed",
			"output": []interface{}{
				codexWSMessageItem(text),
			},
			"usage": map[string]interface{}{"input_tokens": 3, "output_tokens": 2, "total_tokens": 5},
		},
	}
}

// codexWSMessageItem builds the assistant message item exactly as the real
// wire emits it (output_item.added/done + completed.output all use this).
func codexWSMessageItem(text string) map[string]interface{} {
	return map[string]interface{}{
		"type": "message",
		"role": "assistant",
		"id":   "msg_0",
		"content": []interface{}{
			map[string]interface{}{"type": "output_text", "text": text},
		},
	}
}

// -----------------------------------------------------------------------------
// The dual-protocol test server
// -----------------------------------------------------------------------------

type wsTestServer struct {
	srv           *httptest.Server
	mu            sync.Mutex
	upgrades      int
	sseHits       int
	rejectUpgrade bool
	upgradeHeader http.Header
	script        func(c *wsTestConn, callIndex int)
	callIndex     int
	sseID         string
}

func newWSCodexServer(t *testing.T, script func(c *wsTestConn, callIndex int)) *wsTestServer {
	t.Helper()
	ts := &wsTestServer{script: script, sseID: "resp_sse"}
	ts.srv = httptest.NewServer(http.HandlerFunc(ts.handle))
	t.Cleanup(ts.srv.Close)
	return ts
}

func (ts *wsTestServer) handle(w http.ResponseWriter, r *http.Request) {
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		ts.mu.Lock()
		ts.upgrades++
		ts.upgradeHeader = r.Header.Clone()
		reject := ts.rejectUpgrade
		ts.callIndex++
		call := ts.callIndex
		ts.mu.Unlock()
		if reject {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("no websocket here"))
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		accept := computeWSAccept(r.Header.Get("Sec-WebSocket-Key"))
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
		if _, err := rw.Writer.WriteString(resp); err != nil {
			conn.Close()
			return
		}
		rw.Writer.Flush()
		c := &wsTestConn{conn: conn, br: rw.Reader}
		go ts.script(c, call)
		return
	}
	ts.mu.Lock()
	ts.sseHits++
	ts.mu.Unlock()
	// Codex SSE fallback responder (full event sequence, matching the wire).
	sse := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"" + ts.sseID + "\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"id\":\"msg_sse\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.content_part.added\",\"part\":{\"type\":\"output_text\",\"text\":\"\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"from-sse\"}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"id\":\"msg_sse\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"from-sse\"}]}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"" + ts.sseID + "\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte(sse))
}

func (ts *wsTestServer) stats() (upgrades, sseHits int) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.upgrades, ts.sseHits
}

// codexWSStreamOpts builds stream options for the WS tests: auto transport
// (the reference default) + the fabricated ChatGPT JWT + sessionId.
func codexWSStreamOpts(t *testing.T, sessionID string, mutate func(*OpenAICodexResponsesOptions)) *OpenAICodexResponsesOptions {
	opts := &OpenAICodexResponsesOptions{
		APIKey:    codexTestJWT(t, "acc_123"),
		SessionID: sessionID,
		Transport: src.TransportAuto,
		Context:   context.Background(),
	}
	if mutate != nil {
		mutate(opts)
	}
	return opts
}

// drainCodexStream is collectCodexStream without the SSE transport default
// (WS tests control the transport explicitly).
func drainCodexStream(m src.Model[src.Api], opts *OpenAICodexResponsesOptions) (*src.AssistantMessage, []string, error) {
	return drainCodexStreamCtx(m, src.ChatContext{}, opts)
}

func drainCodexStreamCtx(m src.Model[src.Api], ctx src.ChatContext, opts *OpenAICodexResponsesOptions) (*src.AssistantMessage, []string, error) {
	ch := StreamOpenAICodexResponses(m, ctx, opts)()
	var types []string
	var final *src.AssistantMessage
	var streamErr error
	for ev := range ch {
		types = append(types, ev.Type)
		switch ev.Type {
		case "error":
			final = ev.Error
			if final != nil {
				streamErr = fmt.Errorf("%s", final.ErrorMessage)
			}
		case "done":
			final = ev.Message
		}
	}
	return final, types, streamErr
}

// codexEchoScript: each request frame gets created → delta → completed.
// Records the raw request frames for body assertions.
func codexEchoScript(frames *[][]byte, mu *sync.Mutex) func(c *wsTestConn, callIndex int) {
	return func(c *wsTestConn, callIndex int) {
		for {
			opcode, payload, err := c.readFrame()
			if err != nil {
				return
			}
			if opcode == 0x8 { // client close
				_ = c.writeFrame(0x8, payload)
				return
			}
			if opcode != 0x1 {
				continue
			}
			mu.Lock()
			*frames = append(*frames, payload)
			mu.Unlock()
			id := fmt.Sprintf("resp_%d", callIndex)
			text := fmt.Sprintf("ws-reply-%d", callIndex)
			_ = c.writeTextJSON(map[string]interface{}{"type": "response.created", "response": map[string]interface{}{"id": id, "status": "in_progress"}})
			// The real wire scaffolds every delta with item/part added events —
			// the shared processor gates text accumulation on them.
			_ = c.writeTextJSON(map[string]interface{}{"type": "response.output_item.added", "item": map[string]interface{}{"type": "message", "role": "assistant", "id": "msg_0", "status": "in_progress"}})
			_ = c.writeTextJSON(map[string]interface{}{"type": "response.content_part.added", "part": map[string]interface{}{"type": "output_text", "text": ""}})
			_ = c.writeTextJSON(map[string]interface{}{"type": "response.output_text.delta", "delta": text})
			_ = c.writeTextJSON(map[string]interface{}{"type": "response.output_item.done", "item": codexWSMessageItem(text)})
			_ = c.writeTextJSON(codexWSCompleted(id, text))
		}
	}
}

func frameJSON(t *testing.T, frames [][]byte, index int) map[string]interface{} {
	t.Helper()
	if index >= len(frames) {
		t.Fatalf("expected at least %d request frames, got %d", index+1, len(frames))
	}
	var m map[string]interface{}
	if err := json.Unmarshal(frames[index], &m); err != nil {
		t.Fatalf("frame %d: %v", index, err)
	}
	return m
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

func TestCodexWSSuccessAndReuse(t *testing.T) {
	var mu sync.Mutex
	var frames [][]byte
	ts := newWSCodexServer(t, codexEchoScript(&frames, &mu))

	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = ts.srv.URL })
	opts := codexWSStreamOpts(t, "sess-ws-1", nil)
	ctx := src.ChatContext{Messages: []src.Message{&src.UserMessage{Role: src.RoleUser, Content: []src.ContentBlock{src.TextContent{Type: "text", Text: "hi"}}}}}

	final, types, err := drainCodexStreamCtx(m, ctx, opts)
	if err != nil {
		t.Fatalf("stream 1: %v", err)
	}
	if final == nil {
		t.Fatal("no final message")
	}
	if final.StopReason == src.StopReasonError || final.StopReason == src.StopReasonAborted {
		t.Fatalf("stop reason = %s, want a completing reason", final.StopReason)
	}
	hasStart, hasDone := false, false
	for _, ty := range types {
		if ty == "start" {
			hasStart = true
		}
		if ty == "done" {
			hasDone = true
		}
	}
	if !hasStart || !hasDone {
		t.Fatalf("event types = %v, want start and done", types)
	}

	// Body frame pin: one text message {"type":"response.create", ...body}.
	f1 := frameJSON(t, frames, 0)
	if f1["type"] != "response.create" {
		t.Fatalf("frame type = %v, want response.create", f1["type"])
	}
	if f1["model"] != "gpt-5.1" || f1["store"] != false || f1["stream"] != true {
		t.Fatalf("frame body fields = %v", f1)
	}
	if _, ok := f1["input"].([]interface{}); !ok {
		t.Fatalf("frame input missing: %v", f1)
	}

	// Second call on the SAME session reuses the socket: no new upgrade.
	final2, _, err := drainCodexStream(m, opts)
	if err != nil {
		t.Fatalf("stream 2: %v", err)
	}
	if final2 == nil {
		t.Fatal("no final message for stream 2")
	}
	if upgrades, _ := ts.stats(); upgrades != 1 {
		t.Fatalf("upgrades = %d after two calls, want 1 (connection reused)", upgrades)
	}
	// The second call sent its own request frame over the reused socket.
	f2 := frameJSON(t, frames, 1)
	if f2["type"] != "response.create" {
		t.Fatalf("frame 2 type = %v", f2["type"])
	}
}

func TestCodexWSCacheDeltaAndInvalidation(t *testing.T) {
	var mu sync.Mutex
	var frames [][]byte
	ts := newWSCodexServer(t, codexEchoScript(&frames, &mu))
	resetCodexWebSocketSessions("")

	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = ts.srv.URL })

	// Call 1: user "hi" → assistant "ws-reply-1" (server-scripted).
	ctx1 := src.ChatContext{Messages: []src.Message{&src.UserMessage{Role: src.RoleUser, Content: []src.ContentBlock{src.TextContent{Type: "text", Text: "hi"}}}}}
	opts1 := codexWSStreamOpts(t, "sess-delta", nil)
	if _, _, err := drainCodexStreamCtx(m, ctx1, opts1); err != nil {
		t.Fatalf("call 1: %v", err)
	}

	// Call 2: history extends call 1's input with the assistant reply + a new
	// user message → delta send (previous_response_id + only the new items).
	assistantMsg := &src.AssistantMessage{
		Role: src.RoleAssistant, Api: m.Api, Provider: m.Provider, Model: m.ID,
		Content: []src.ContentBlock{src.TextContent{Type: "text", Text: "ws-reply-1"}},
		Usage:   src.Usage{Cost: src.UsageCost{}}, StopReason: src.StopReasonStop,
	}
	ctx2 := src.ChatContext{Messages: []src.Message{
		&src.UserMessage{Role: src.RoleUser, Content: []src.ContentBlock{src.TextContent{Type: "text", Text: "hi"}}},
		assistantMsg,
		&src.UserMessage{Role: src.RoleUser, Content: []src.ContentBlock{src.TextContent{Type: "text", Text: "more"}}},
	}}
	opts2 := codexWSStreamOpts(t, "sess-delta", nil)
	if _, _, err := drainCodexStreamCtx(m, ctx2, opts2); err != nil {
		t.Fatalf("call 2: %v", err)
	}

	f2 := frameJSON(t, frames, 1)
	if f2["previous_response_id"] != "resp_1" {
		t.Fatalf("frame 2 previous_response_id = %v, want resp_1 (delta send)", f2["previous_response_id"])
	}
	delta, ok := f2["input"].([]interface{})
	if !ok || len(delta) != 1 {
		t.Fatalf("frame 2 input delta = %v, want exactly the 1 new item", f2["input"])
	}

	// Call 3: same history but a changed system prompt — a different body
	// field (instructions) invalidates the continuation → full resend.
	ctx3 := ctx2
	ctx3.SystemPrompt = "changed instructions invalidate the cache"
	opts3 := codexWSStreamOpts(t, "sess-delta", nil)
	if _, _, err := drainCodexStreamCtx(m, ctx3, opts3); err != nil {
		t.Fatalf("call 3: %v", err)
	}
	f3 := frameJSON(t, frames, 2)
	if _, has := f3["previous_response_id"]; has {
		t.Fatalf("frame 3 has previous_response_id %v, want full resend after body change", f3["previous_response_id"])
	}
	if input, ok := f3["input"].([]interface{}); !ok || len(input) != 3 {
		t.Fatalf("frame 3 input = %v, want full 3-item context", f3["input"])
	}
}

func TestCodexWSSseFallbackDiagnosticAndStickiness(t *testing.T) {
	ts := newWSCodexServer(t, func(c *wsTestConn, callIndex int) { _ = c })
	ts.rejectUpgrade = true

	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = ts.srv.URL })
	opts := codexWSStreamOpts(t, "sess-fallback", nil)

	final, types, err := drainCodexStream(m, opts)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	// Completed via the SSE fallback.
	if final == nil {
		t.Fatal("no final message")
	}
	joined := strings.Join(types, ",")
	if !strings.Contains(joined, "done") {
		t.Fatalf("event types = %v, want SSE completion", types)
	}
	if upgrades, sseHits := ts.stats(); upgrades != 1 || sseHits != 1 {
		t.Fatalf("upgrades=%d sseHits=%d, want 1 upgrade then 1 SSE", upgrades, sseHits)
	}

	// The diagnostic pin (TS :212-221): type provider_transport_failure with
	// all five detail fields.
	if len(final.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %d entries, want 1", len(final.Diagnostics))
	}
	diag := final.Diagnostics[0]
	if diag.Type != "provider_transport_failure" {
		t.Fatalf("diagnostic type = %q", diag.Type)
	}
	if diag.Error == nil || diag.Error.Message == "" {
		t.Fatalf("diagnostic error missing: %+v", diag.Error)
	}
	if got, _ := diag.Details["configuredTransport"].(string); got != "auto" {
		t.Fatalf("configuredTransport = %v", diag.Details["configuredTransport"])
	}
	if got, _ := diag.Details["fallbackTransport"].(string); got != "sse" {
		t.Fatalf("fallbackTransport = %v, want sse (nothing emitted)", diag.Details["fallbackTransport"])
	}
	if got, _ := diag.Details["eventsEmitted"].(bool); got != false {
		t.Fatalf("eventsEmitted = %v, want false", diag.Details["eventsEmitted"])
	}
	if got, _ := diag.Details["phase"].(string); got != "before_message_stream_start" {
		t.Fatalf("phase = %v", diag.Details["phase"])
	}
	if got, _ := diag.Details["requestBytes"].(int); got <= 0 {
		t.Fatalf("requestBytes = %v, want > 0", diag.Details["requestBytes"])
	}

	// Sticky: the second call goes straight to SSE — no second upgrade.
	final2, _, err := drainCodexStream(m, opts)
	if err != nil {
		t.Fatalf("stream 2: %v", err)
	}
	_ = final2
	if upgrades, sseHits := ts.stats(); upgrades != 1 || sseHits != 2 {
		t.Fatalf("after sticky: upgrades=%d sseHits=%d, want 1/2", upgrades, sseHits)
	}
	if len(final2.Diagnostics) != 0 {
		t.Fatalf("second call diagnostics = %d, want 0 (no WS attempt)", len(final2.Diagnostics))
	}

	// resetCodexWebSocketSessions clears the sticky flag.
	resetCodexWebSocketSessions("sess-fallback")
	final3, _, err := drainCodexStream(m, opts)
	if err != nil {
		t.Fatalf("stream 3: %v", err)
	}
	_ = final3
	if upgrades, sseHits := ts.stats(); upgrades != 2 || sseHits != 3 {
		t.Fatalf("after reset: upgrades=%d sseHits=%d, want 2/3", upgrades, sseHits)
	}
}

func TestCodexWSApiErrorNoFallback(t *testing.T) {
	ts := newWSCodexServer(t, func(c *wsTestConn, callIndex int) {
		_ = c.writeTextJSON(map[string]interface{}{"type": "error", "message": "codex auth failed", "code": "invalid_api_key"})
	})

	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = ts.srv.URL })
	opts := codexWSStreamOpts(t, "sess-apierr", nil)

	final, _, err := drainCodexStream(m, opts)
	if err == nil || !strings.Contains(err.Error(), "Codex error: codex auth failed") {
		t.Fatalf("err = %v, want the codex api error", err)
	}
	if final == nil || final.StopReason != src.StopReasonError {
		t.Fatalf("final = %+v, want stopReason error", final)
	}
	if upgrades, sseHits := ts.stats(); upgrades != 1 || sseHits != 0 {
		t.Fatalf("upgrades=%d sseHits=%d, want NO SSE fallback for api errors", upgrades, sseHits)
	}
	if len(final.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %d, want 0 (non-transport errors get none)", len(final.Diagnostics))
	}

	// Non-transport errors never stick: the next call retries WS.
	if _, _, err := drainCodexStream(m, opts); err == nil {
		t.Fatal("second call should fail the same way")
	}
	if upgrades, _ := ts.stats(); upgrades != 2 {
		t.Fatalf("upgrades = %d after retry, want 2 (api errors never stick)", upgrades)
	}
}

func TestCodexWSMidStreamCloseTerminal(t *testing.T) {
	ts := newWSCodexServer(t, func(c *wsTestConn, callIndex int) {
		// Deliver created (the first event → started=true), then close.
		_ = c.writeTextJSON(map[string]interface{}{"type": "response.created", "response": map[string]interface{}{"id": "resp_x", "status": "in_progress"}})
		_ = c.writeFrame(0x8, []byte{0x03, 0xE9}) // 1001 "going away"
	})

	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = ts.srv.URL })
	opts := codexWSStreamOpts(t, "sess-close", nil)

	// Mid-stream close (after the first event) is terminal: error event, NO fallback.
	final, _, err := drainCodexStream(m, opts)
	if err == nil {
		t.Fatal("want error")
	}
	if _, sseHits := ts.stats(); sseHits != 0 {
		t.Fatalf("sseHits = %d, want 0 (mid-stream close is terminal)", sseHits)
	}
	if len(final.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %d, want the transport-failure diagnostic", len(final.Diagnostics))
	}
	if got, _ := final.Diagnostics[0].Details["eventsEmitted"].(bool); got != true {
		t.Fatalf("eventsEmitted = %v, want true", final.Diagnostics[0].Details["eventsEmitted"])
	}
	if got, _ := final.Diagnostics[0].Details["fallbackTransport"]; got != nil {
		t.Fatalf("fallbackTransport = %v, want undefined after stream start", got)
	}
}

func TestCodexWSClosePreStartFallsBack(t *testing.T) {
	ts := newWSCodexServer(t, func(c *wsTestConn, callIndex int) {
		// Close immediately — no event ever reached the client.
		_ = c.writeFrame(0x8, []byte{0x03, 0xE8})
	})

	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = ts.srv.URL })
	opts := codexWSStreamOpts(t, "sess-close2", nil)

	final, _, err := drainCodexStream(m, opts)
	if err != nil {
		t.Fatalf("fallback should complete the stream: %v", err)
	}
	if upgrades, sseHits := ts.stats(); upgrades != 1 || sseHits != 1 {
		t.Fatalf("upgrades=%d sseHits=%d, want pre-start close → SSE fallback", upgrades, sseHits)
	}
	if len(final.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %d, want 1", len(final.Diagnostics))
	}
	if got, _ := final.Diagnostics[0].Details["eventsEmitted"].(bool); got != false {
		t.Fatalf("eventsEmitted = %v, want false", got)
	}
}

func TestCodexWSAbortMidStream(t *testing.T) {
	started := make(chan struct{})
	ts := newWSCodexServer(t, func(c *wsTestConn, callIndex int) {
		_ = c.writeTextJSON(map[string]interface{}{"type": "response.created", "response": map[string]interface{}{"id": "resp_a", "status": "in_progress"}})
		_ = c.writeTextJSON(map[string]interface{}{"type": "response.output_text.delta", "delta": "partial"})
		close(started)
		// Hold the socket open so the abort has to close it.
		time.Sleep(2 * time.Second)
	})

	ctx, cancel := context.WithCancel(context.Background())
	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = ts.srv.URL })
	opts := codexWSStreamOpts(t, "sess-abort", func(o *OpenAICodexResponsesOptions) { o.Context = ctx })

	go func() {
		<-started
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	final, _, err := drainCodexStream(m, opts)
	if err == nil || !strings.Contains(err.Error(), "Request was aborted") {
		t.Fatalf("err = %v, want Request was aborted", err)
	}
	if final == nil || final.StopReason != src.StopReasonAborted {
		t.Fatalf("final = %+v, want stopReason aborted", final)
	}
	if _, sseHits := ts.stats(); sseHits != 0 {
		t.Fatalf("sseHits = %d, want 0 (aborts never fall back)", sseHits)
	}
	cancel()
}

func TestCodexWSHeadersPin(t *testing.T) {
	// The reference has no read timeouts, so a silent server would hang the
	// stream forever — the script closes the socket after the handshake to
	// let the stream end.
	ts := newWSCodexServer(t, func(c *wsTestConn, callIndex int) {
		time.Sleep(100 * time.Millisecond)
		c.conn.Close()
	})

	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = ts.srv.URL })
	opts := codexWSStreamOpts(t, "sess-headers", nil)
	opts.Headers = map[string]string{"X-Custom": "yes"}

	_, _, _ = drainCodexStream(m, opts)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && ts.upgradeHeader == nil {
		time.Sleep(10 * time.Millisecond)
	}
	if ts.upgradeHeader == nil {
		t.Fatal("no upgrade request captured")
	}
	h := ts.upgradeHeader
	if got := h.Get("Authorization"); got != "Bearer "+opts.APIKey {
		t.Fatalf("Authorization = %q", got)
	}
	if got := h.Get("chatgpt-account-id"); got != "acc_123" {
		t.Fatalf("chatgpt-account-id = %q", got)
	}
	if got := h.Get("originator"); got != "pi" {
		t.Fatalf("originator = %q", got)
	}
	if !strings.HasPrefix(h.Get("User-Agent"), "pi (") {
		t.Fatalf("User-Agent = %q", h.Get("User-Agent"))
	}
	if got := h.Get("OpenAI-Beta"); got != "responses_websockets=2026-02-06" {
		t.Fatalf("OpenAI-Beta = %q, want responses_websockets=2026-02-06", got)
	}
	if got := h.Get("x-client-request-id"); got != "sess-headers" {
		t.Fatalf("x-client-request-id = %q, want the sessionId", got)
	}
	if got := h.Get("session_id"); got != "sess-headers" {
		t.Fatalf("session_id = %q, want the sessionId", got)
	}
	if h.Get("accept") != "" || h.Get("content-type") != "" {
		t.Fatalf("WS handshake must not carry accept/content-type: %v %v", h.Get("accept"), h.Get("content-type"))
	}
	if h.Get("X-Custom") != "yes" {
		t.Fatalf("model headers not forwarded: %q", h.Get("X-Custom"))
	}
}

func TestCodexWSIdleEviction(t *testing.T) {
	oldTTL := codexWSIdleTTL
	codexWSIdleTTL = 60 * time.Millisecond
	t.Cleanup(func() { codexWSIdleTTL = oldTTL })

	var mu sync.Mutex
	var frames [][]byte
	ts := newWSCodexServer(t, codexEchoScript(&frames, &mu))
	resetCodexWebSocketSessions("")

	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = ts.srv.URL })
	opts := codexWSStreamOpts(t, "sess-idle", nil)

	if _, _, err := drainCodexStream(m, opts); err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if upgrades, _ := ts.stats(); upgrades != 1 {
		t.Fatalf("upgrades = %d after call 1", upgrades)
	}
	time.Sleep(150 * time.Millisecond) // idle timer fires (60ms)
	if _, _, err := drainCodexStream(m, opts); err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if upgrades, _ := ts.stats(); upgrades != 2 {
		t.Fatalf("upgrades = %d after idle eviction, want 2 (fresh connection)", upgrades)
	}
}

func TestCodexWSNoSessionAlwaysFresh(t *testing.T) {
	var mu sync.Mutex
	var frames [][]byte
	ts := newWSCodexServer(t, codexEchoScript(&frames, &mu))
	resetCodexWebSocketSessions("")

	m := codexTestModel(func(m *src.Model[src.Api]) { m.BaseURL = ts.srv.URL })
	opts := codexWSStreamOpts(t, "", nil) // no sessionId

	if _, _, err := drainCodexStream(m, opts); err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if _, _, err := drainCodexStream(m, opts); err != nil {
		t.Fatalf("call 2: %v", err)
	}
	// Without a sessionId every request opens a fresh connection.
	if upgrades, _ := ts.stats(); upgrades != 2 {
		t.Fatalf("upgrades = %d, want 2 (no session → no reuse)", upgrades)
	}
}

func TestCodexWebSocketURLResolution(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "wss://chatgpt.com/backend-api/codex/responses"},
		{"https://example.com", "wss://example.com/codex/responses"},
		{"http://127.0.0.1:8080/codex", "ws://127.0.0.1:8080/codex/responses"},
		{"wss://already.example/ws", "wss://already.example/ws/codex/responses"},
		{"https://example.com/codex/responses", "wss://example.com/codex/responses"},
	}
	for _, c := range cases {
		if got := resolveCodexWebSocketURL(c.in); got != c.want {
			t.Fatalf("resolveCodexWebSocketURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCodexWSTransportPlumbing(t *testing.T) {
	// The converter forwards every Transport value including websocket-cached.
	so := &src.StreamOptions{Transport: src.TransportWebsocketCached, APIKey: "k", SessionID: "s1"}
	opts := convertStreamOptionsToOpenAICodexResponses(so)
	if opts.Transport != src.TransportWebsocketCached {
		t.Fatalf("converter Transport = %q, want websocket-cached", opts.Transport)
	}
	if opts.SessionID != "s1" {
		t.Fatalf("converter SessionID = %q", opts.SessionID)
	}
}

func TestDiagnosticsMarshalPin(t *testing.T) {
	msg := &src.AssistantMessage{
		Role:       src.RoleAssistant,
		StopReason: src.StopReasonStop,
	}
	src.AppendAssistantMessageDiagnostic(msg, src.CreateAssistantMessageDiagnostic(
		"provider_transport_failure",
		fmt.Errorf("WebSocket handshake rejected: HTTP 404"),
		map[string]any{"configuredTransport": "auto", "requestBytes": 42},
	))
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(data, &generic); err != nil {
		t.Fatal(err)
	}
	diags, ok := generic["diagnostics"].([]interface{})
	if !ok || len(diags) != 1 {
		t.Fatalf("marshaled diagnostics = %v", generic["diagnostics"])
	}
	d0 := diags[0].(map[string]interface{})
	if d0["type"] != "provider_transport_failure" {
		t.Fatalf("diagnostic type = %v", d0["type"])
	}
	errInfo := d0["error"].(map[string]interface{})
	if errInfo["message"] != "WebSocket handshake rejected: HTTP 404" {
		t.Fatalf("error message = %v", errInfo["message"])
	}
}
