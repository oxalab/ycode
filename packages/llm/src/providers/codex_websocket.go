package providers

/*
Codex WebSocket transport — port of the WS half of openai-codex-responses.ts.
The reference tries WebSocket FIRST under transport "auto" (the app default,
settings-manager.ts:659), falling back to SSE only after a transport failure;
SSE is the reference's own fallback, not its primary path.

Verified against pi/packages/ai/src/providers/openai-codex-responses.ts:
- one text frame per request: {"type":"response.create", ...requestBody} (:1202)
- sticky per-session SSE fallback after ONE pre-first-event transport failure (:688-707)
- session connection cache keyed by sessionId, 5-minute idle eviction (:852-937)
- cached-context delta via connection-scoped previous_response_id (:1095-1147)
- no ping/pong/keepalive; abort closes the socket ("Request was aborted")
*/

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	src "ycode/packages/llm/src"
)

const (
	codexOpenAIBetaWebsockets = "responses_websockets=2026-02-06"
	// SESSION_WEBSOCKET_CACHE_TTL_MS — var only so tests can shrink it.
	defaultCodexWSIdleTTL = 5 * time.Minute
)

var codexWSIdleTTL = defaultCodexWSIdleTTL

// =============================================================================
// Error classification (isCodexNonTransportError, :488-490)
// =============================================================================

// codexApiError = CodexApiError: the SERVER answered with an
// application-level error (provider "error"/"response.failed" events).
// Non-transport: rethrown without fallback.
type codexApiError struct {
	Message string
	Code    string
}

func (e *codexApiError) Error() string { return e.Message }
func (e *codexApiError) DiagnosticName() string {
	return "CodexApiError"
}
func (e *codexApiError) DiagnosticCode() any {
	if e.Code == "" {
		return nil
	}
	return e.Code
}

// codexProtocolError = CodexProtocolError: malformed protocol data
// (invalid WS/SSE JSON). Also non-transport.
type codexProtocolError struct {
	Message string
}

func (e *codexProtocolError) Error() string          { return e.Message }
func (e *codexProtocolError) DiagnosticName() string { return "CodexProtocolError" }

func isCodexNonTransportError(err error) bool {
	if err == nil {
		return false
	}
	var api *codexApiError
	var proto *codexProtocolError
	if errors.As(err, &api) || errors.As(err, &proto) {
		return true
	}
	return false
}

// =============================================================================
// URL + headers
// =============================================================================

// resolveCodexWebSocketURL ports resolveCodexWebSocketUrl (:439-444): the
// codex responses endpoint with the scheme swapped to ws/wss.
func resolveCodexWebSocketURL(baseURL string) string {
	endpoint := resolveCodexURL(baseURL)
	if strings.HasPrefix(endpoint, "https://") {
		return "wss://" + strings.TrimPrefix(endpoint, "https://")
	}
	if strings.HasPrefix(endpoint, "http://") {
		return "ws://" + strings.TrimPrefix(endpoint, "http://")
	}
	return endpoint
}

// codexCreateRequestID ports createCodexRequestId (:1290-1295): UUID when
// available, else a timestamped fallback.
func codexCreateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err == nil {
		b[6] = (b[6] & 0x0F) | 0x40
		b[8] = (b[8] & 0x3F) | 0x80
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	}
	return fmt.Sprintf("codex_%d_%s", time.Now().UnixMilli(), randText())
}

func randText() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)[:10]
}

// buildBaseCodexHeaders is shared by the SSE and WS header builders
// (:1297-1313). buildCodexSSEHeaders (openai-codex-responses.go) is the SSE
// specialization; this is the extraction both use.
func buildBaseCodexHeaders(modelHeaders, additionalHeaders map[string]string, accountId, token string) http.Header {
	headers := http.Header{}
	for k, v := range modelHeaders {
		headers.Set(k, v)
	}
	for k, v := range additionalHeaders {
		headers.Set(k, v)
	}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("chatgpt-account-id", accountId)
	headers.Set("originator", "pi")
	headers.Set("User-Agent", codexUserAgent())
	return headers
}

// buildCodexWebSocketHeaders ports buildWebSocketHeaders (:1335-1351): drops
// accept/content-type, sets the websockets beta, and stamps the request id
// into x-client-request-id + session_id. Note the request id is the
// sessionId when present (stable per session), else a fresh UUID — unlike
// SSE which omits both headers without a sessionId. The :797
// `delete wsHeaders["OpenAI-Beta"]` in the reference is a no-op under Fetch
// key-lowercasing, so the beta header rides the handshake here too.
func buildCodexWebSocketHeaders(modelHeaders, additionalHeaders map[string]string, accountId, token, requestID string) http.Header {
	headers := buildBaseCodexHeaders(modelHeaders, additionalHeaders, accountId, token)
	headers.Del("accept")
	headers.Del("content-type")
	headers.Del("OpenAI-Beta")
	headers.Set("OpenAI-Beta", codexOpenAIBetaWebsockets)
	headers.Set("x-client-request-id", requestID)
	headers.Set("session_id", requestID)
	return headers
}

// =============================================================================
// Session cache + sticky SSE fallback (:630-632, :852-937, :688-707)
// =============================================================================

// codexWSContinuation ports CachedWebSocketContinuationState.
type codexWSContinuation struct {
	lastRequestBody   map[string]interface{}
	lastResponseID    string
	lastResponseItems []interface{}
}

// codexWSCachedConn ports CachedWebSocketConnection.
type codexWSCachedConn struct {
	conn         *wsConn
	busy         bool
	idleTimer    *time.Timer
	continuation *codexWSContinuation
}

var codexWSSessionCache = map[string]*codexWSCachedConn{}
var codexWSSseFallbackSessions = map[string]bool{}
var codexWSMu sync.Mutex

// isCodexWSSSEFallbackActive ports isWebSocketSseFallbackActive.
func isCodexWSSSEFallbackActive(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	codexWSMu.Lock()
	defer codexWSMu.Unlock()
	return codexWSSseFallbackSessions[sessionID]
}

// recordCodexWSSSEFallback ports recordWebSocketSseFallback (stats counter
// only in the reference — no set add).
func recordCodexWSSSEFallback(sessionID string) {
	if sessionID == "" {
		return
	}
	// Reference only bumps debug stats here; nothing sticky changes.
}

// recordCodexWSFailure ports recordWebSocketFailure: ONE transport failure
// before the first event sticks this session to SSE permanently (no TTL).
func recordCodexWSFailure(sessionID string) {
	if sessionID == "" {
		return
	}
	codexWSMu.Lock()
	defer codexWSMu.Unlock()
	codexWSSseFallbackSessions[sessionID] = true
}

// closeOpenAICodexWebSocketSessions ports the teardown hook (:669-686):
// closes cached sockets (1000 "debug_close"), empty id = all sessions.
func closeOpenAICodexWebSocketSessions(sessionID string) {
	codexWSMu.Lock()
	defer codexWSMu.Unlock()
	for id, entry := range codexWSSessionCache {
		if sessionID == "" || id == sessionID {
			if entry.idleTimer != nil {
				entry.idleTimer.Stop()
			}
			entry.conn.Close(1000, "debug_close")
			delete(codexWSSessionCache, id)
		}
	}
}

// resetCodexWebSocketSessions combines resetOpenAICodexWebSocketDebugStats
// (:659-667 — clears the sticky fallback flag) with cache eviction.
func resetCodexWebSocketSessions(sessionID string) {
	codexWSMu.Lock()
	defer codexWSMu.Unlock()
	if sessionID == "" {
		codexWSSseFallbackSessions = map[string]bool{}
		for id, entry := range codexWSSessionCache {
			if entry.idleTimer != nil {
				entry.idleTimer.Stop()
			}
			entry.conn.Close(1000, "done")
			delete(codexWSSessionCache, id)
		}
		return
	}
	delete(codexWSSseFallbackSessions, sessionID)
	if entry, ok := codexWSSessionCache[sessionID]; ok {
		if entry.idleTimer != nil {
			entry.idleTimer.Stop()
		}
		entry.conn.Close(1000, "done")
		delete(codexWSSessionCache, sessionID)
	}
}

// codexWSAcquireResult carries the acquireWebSocket contract
// (:852-862): socket + optional cache entry + release(keep).
type codexWSAcquireResult struct {
	conn    *wsConn
	entry   *codexWSCachedConn
	reused  bool
	release func(keep bool)
}

// acquireCodexWebSocket ports acquireWebSocket (:852-937): without a
// sessionId every request opens a fresh connection that is always closed;
// with one, open connections are cached, reused when idle+open, and evicted
// on keep=false or a dead socket. A busy cached connection spawns a second,
// uncached one-shot connection.
func acquireCodexWebSocket(ctx context.Context, url string, headers http.Header, sessionID string) (*codexWSAcquireResult, error) {
	if sessionID == "" {
		conn, err := wsDial(ctx, url, headers)
		if err != nil {
			return nil, err
		}
		return &codexWSAcquireResult{
			conn:   conn,
			reused: false,
			release: func(keep bool) {
				conn.Close(1000, "done")
			},
		}, nil
	}

	codexWSMu.Lock()
	cached := codexWSSessionCache[sessionID]
	if cached != nil {
		if cached.idleTimer != nil {
			cached.idleTimer.Stop()
			cached.idleTimer = nil
		}
		if !cached.busy && cached.conn.isOpen() {
			cached.busy = true
			entry := cached
			codexWSMu.Unlock()
			return &codexWSAcquireResult{
				conn:   entry.conn,
				entry:  entry,
				reused: true,
				release: func(keep bool) {
					codexWSMu.Lock()
					defer codexWSMu.Unlock()
					if !keep || !entry.conn.isOpen() {
						entry.conn.Close(1000, "done")
						delete(codexWSSessionCache, sessionID)
						return
					}
					entry.busy = false
					entry.idleTimer = scheduleCodexWSExpiry(sessionID, entry)
				},
			}, nil
		}
		if cached.busy {
			codexWSMu.Unlock()
			conn, err := wsDial(ctx, url, headers)
			if err != nil {
				return nil, err
			}
			return &codexWSAcquireResult{
				conn:   conn,
				reused: false,
				release: func(keep bool) {
					conn.Close(1000, "done")
				},
			}, nil
		}
		if !cached.conn.isOpen() {
			cached.conn.Close(1000, "done")
			delete(codexWSSessionCache, sessionID)
		}
	}
	codexWSMu.Unlock()

	conn, err := wsDial(ctx, url, headers)
	if err != nil {
		return nil, err
	}
	entry := &codexWSCachedConn{conn: conn, busy: true}
	codexWSMu.Lock()
	codexWSSessionCache[sessionID] = entry
	codexWSMu.Unlock()
	return &codexWSAcquireResult{
		conn:   conn,
		entry:  entry,
		reused: false,
		release: func(keep bool) {
			codexWSMu.Lock()
			defer codexWSMu.Unlock()
			if !keep || !entry.conn.isOpen() {
				entry.conn.Close(1000, "done")
				if entry.idleTimer != nil {
					entry.idleTimer.Stop()
				}
				if codexWSSessionCache[sessionID] == entry {
					delete(codexWSSessionCache, sessionID)
				}
				return
			}
			entry.busy = false
			entry.idleTimer = scheduleCodexWSExpiry(sessionID, entry)
		},
	}, nil
}

// scheduleCodexWSExpiry ports scheduleSessionWebSocketExpiry (:779-788):
// idle eviction after the TTL; a timer firing while busy is a no-op and the
// next release re-arms a fresh timer. Must be called with codexWSMu held.
func scheduleCodexWSExpiry(sessionID string, entry *codexWSCachedConn) *time.Timer {
	ttl := codexWSIdleTTL
	return time.AfterFunc(ttl, func() {
		codexWSMu.Lock()
		defer codexWSMu.Unlock()
		if entry.busy {
			return
		}
		entry.conn.Close(1000, "idle_timeout")
		if codexWSSessionCache[sessionID] == entry {
			delete(codexWSSessionCache, sessionID)
		}
	})
}

// =============================================================================
// Cached-context delta (:1095-1147)
// =============================================================================

// codexWSRequestBodyWithoutInput ports requestBodyWithoutInput.
func codexWSRequestBodyWithoutInput(body map[string]interface{}) string {
	rest := make(map[string]interface{}, len(body))
	for k, v := range body {
		if k == "input" || k == "previous_response_id" {
			continue
		}
		rest[k] = v
	}
	encoded, _ := json.Marshal(rest)
	return string(encoded)
}

// codexWSResponseInputsEqual ports responseInputsEqual (JSON-string compare,
// order-significant).
func codexWSResponseInputsEqual(a, b []interface{}) bool {
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)
	return string(aJSON) == string(bJSON)
}

// codexWSRequestBodiesMatchExceptInput ports requestBodiesMatchExceptInput.
func codexWSRequestBodiesMatchExceptInput(a, b map[string]interface{}) bool {
	return codexWSRequestBodyWithoutInput(a) == codexWSRequestBodyWithoutInput(b)
}

// codexWSGetInputDelta ports getCachedWebSocketInputDelta: the new input
// must strictly extend the baseline (previous input + last turn's assistant
// items), else no delta (full context resend).
func codexWSGetInputDelta(body map[string]interface{}, continuation *codexWSContinuation) []interface{} {
	if !codexWSRequestBodiesMatchExceptInput(body, continuation.lastRequestBody) {
		return nil
	}
	currentInput, _ := body["input"].([]interface{})
	prevInput, _ := continuation.lastRequestBody["input"].([]interface{})
	baseline := make([]interface{}, 0, len(prevInput)+len(continuation.lastResponseItems))
	baseline = append(baseline, prevInput...)
	baseline = append(baseline, continuation.lastResponseItems...)
	if len(currentInput) < len(baseline) {
		return nil
	}
	if !codexWSResponseInputsEqual(currentInput[:len(baseline)], baseline) {
		return nil
	}
	return currentInput[len(baseline):]
}

// codexWSBuildCachedRequestBody ports buildCachedWebSocketRequestBody.
func codexWSBuildCachedRequestBody(entry *codexWSCachedConn, body map[string]interface{}) map[string]interface{} {
	continuation := entry.continuation
	if continuation == nil {
		return body
	}
	delta := codexWSGetInputDelta(body, continuation)
	if delta == nil || continuation.lastResponseID == "" {
		entry.continuation = nil
		return body
	}
	out := make(map[string]interface{}, len(body)+2)
	for k, v := range body {
		out[k] = v
	}
	out["previous_response_id"] = continuation.lastResponseID
	out["input"] = delta
	return out
}

// =============================================================================
// The WS stream (processWebSocketStream, :1166-1240)
// =============================================================================

// consumeCodexWebSocketStream reads WS frames, mapping each through
// mapCodexEvent into the shared responses processor via the same
// pipe+SSE-encoding seam the SSE path uses. The "start" event is pushed
// lazily, just before the first provider event reaches the processor
// (startWebSocketOutputOnFirstEvent, :1149-1164).
func consumeCodexWebSocketStream(ctx context.Context, conn *wsConn, ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, model src.Model[src.Api], options *OpenAICodexResponsesOptions, started *bool) error {
	pr, pw := io.Pipe()
	transformErr := make(chan error, 1)
	go func() {
		defer pw.Close()
		sawCompletion := false
		for {
			frame, err := conn.nextMessage()
			if err != nil {
				// Abort surfaces as "Request was aborted" (TS parseWebSocket
				// onAbort :1055-1059), not as the raw socket error.
				if ctx != nil && ctx.Err() != nil {
					transformErr <- fmt.Errorf("Request was aborted")
					return
				}
				// A socket close AFTER the terminal event is a clean end
				// (parseWebSocket onClose :1042-1047); before it, the close
				// error propagates as a transport failure.
				if sawCompletion {
					transformErr <- nil
				} else {
					transformErr <- err
				}
				return
			}
			var event map[string]interface{}
			if err := json.Unmarshal([]byte(frame), &event); err != nil {
				transformErr <- &codexProtocolError{Message: fmt.Sprintf("Invalid Codex WebSocket JSON: %v", err)}
				return
			}
			// Completion detection mirrors parseWebSocket (:1019-1022): any of
			// response.completed/done/incomplete marks the stream complete; a
			// socket close after that is a clean end.
			eventType, _ := event["type"].(string)
			if eventType == "response.completed" || eventType == "response.done" || eventType == "response.incomplete" {
				sawCompletion = true
			}
			if !*started {
				*started = true
				ch <- src.AssistantMessageEvent{Type: "start", Partial: output}
			}
			done, err := mapCodexEvent(event, pw, options)
			if err != nil {
				transformErr <- err
				return
			}
			if done {
				transformErr <- nil
				return
			}
		}
	}()

	processorOptions := &OpenAIResponsesOptions{ServiceTier: optionsServiceTier(options)}
	processErr := processOpenAIResponsesStream(pr, ch, output, model, processorOptions)
	transformResult := <-transformErr
	// Either side failing is fatal; prefer the processor's error when both
	// raced (it carries the drained partial state).
	if processErr != nil {
		return processErr
	}
	return transformResult
}

// processCodexWebSocketStream ports processWebSocketStream: acquire
// (reusing cached session sockets), optionally apply the cached-context
// delta, send ONE {"type":"response.create",...} frame, consume the event
// stream, and keep-or-close the socket. Returns the response id when the
// stream completed (for continuation capture) via output.ResponseID.
func processCodexWebSocketStream(model src.Model[src.Api], ch chan<- src.AssistantMessageEvent, output *src.AssistantMessage, headers http.Header, params interface{}, options *OpenAICodexResponsesOptions, requestID string, started *bool) error {
	ctx := codexContext(options)
	url := resolveCodexWebSocketURL(model.BaseURL)
	sessionID := optionsSessionID(options)

	acquired, err := acquireCodexWebSocket(ctx, url, headers, sessionID)
	if err != nil {
		return err
	}
	conn := acquired.conn
	keepConnection := true
	useCachedContext := options == nil || options.Transport == "" || options.Transport == src.TransportAuto || options.Transport == src.TransportWebsocketCached

	// Abort watchdog: closes the socket on cancellation, matching TS abort
	// semantics (connectWebSocket :830-836 / parseWebSocket onAbort).
	stopWatch := conn.watchContext(ctx)
	defer stopWatch()

	var body map[string]interface{}
	switch p := params.(type) {
	case map[string]interface{}:
		body = p
	default:
		acquired.release(false)
		return fmt.Errorf("onPayload must return an object for the WebSocket transport")
	}
	if useCachedContext && acquired.entry != nil {
		body = codexWSBuildCachedRequestBody(acquired.entry, body)
	}

	frame := make(map[string]interface{}, len(body)+1)
	frame["type"] = "response.create"
	for k, v := range body {
		frame[k] = v
	}
	frameJSON, err := json.Marshal(frame)
	if err != nil {
		acquired.release(false)
		return err
	}

	processorErr := func() error {
		if err := conn.WriteText(string(frameJSON)); err != nil {
			return err
		}
		return consumeCodexWebSocketStream(ctx, conn, ch, output, model, options, started)
	}()

	if processorErr != nil {
		if acquired.entry != nil {
			acquired.entry.continuation = nil
		}
		keepConnection = false
		acquired.release(keepConnection)
		return processorErr
	}

	if ctx != nil && ctx.Err() != nil {
		// Post-success abort: the stream completed but the caller cancelled —
		// TS :1219-1220 drops the connection (the caller then throws).
		keepConnection = false
	} else if useCachedContext && acquired.entry != nil && output.ResponseID != "" {
		// Continuation capture (:1221-1230): the finished assistant turn is
		// round-tripped back to ResponseInput items, minus function_call_output.
		responseItems := convertResponsesMessages(model, src.ChatContext{Messages: []src.Message{output}}, codexToolCallProviders, &ConvertResponsesMessagesOptions{IncludeSystemPrompt: false})
		filtered := make([]interface{}, 0, len(responseItems))
		for _, item := range responseItems {
			if m, ok := item.(map[string]interface{}); ok {
				if t, _ := m["type"].(string); t == "function_call_output" {
					continue
				}
			}
			filtered = append(filtered, item)
		}
		acquired.entry.continuation = &codexWSContinuation{
			lastRequestBody:   body,
			lastResponseID:    output.ResponseID,
			lastResponseItems: filtered,
		}
	}
	acquired.release(keepConnection)
	return nil
}
