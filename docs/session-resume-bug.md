# Session Resume Bug — every persisted message is silently dropped on load

> **Found:** 2026-10-09 (exploration, user-reported: `/resume` shows an empty transcript) ·
> **Status:** ✅ FIXED (2026-10-09, story 8) — decoder + transcript rebuild + tests
> **Root cause:** `SessionEntry.Message` is a non-empty interface with no custom
> unmarshaler — `json.Unmarshal` fails on every message entry and both readers
> silently discard the failures.

---

## 1. Symptoms

Typing `/resume` (alias of `/switch`, slash.go:58-60) and picking a session lands
you in an empty transcript — no old messages render. Worse, the restored agent has
**no memory of the session either** (see §3): continuing the conversation after a
resume starts from zero context. `--continue` at startup is equally affected
(same code path, and `NewOrchestrator` never renders history at all —
orchestrator.go:100-102; `rebuildTranscript` has exactly one call site, slash.go:196).

## 2. Root cause — message entries cannot survive deserialization

`packages/coding-agent/src/core/compaction/types.go:38-45`:

```go
type SessionEntry struct {
	Type 	SessionEntryType `json:"type"`
	...
	Message 	llm.Message `json:"message,omitempty"`
}
```

`llm.Message` is a **non-empty marker interface** (`packages/llm/src/types.go:224-226`,
`type Message interface { IsMessage() }`), and a repo-wide grep finds **zero
`UnmarshalJSON` implementations**. Go's encoding/json cannot put a JSON object
into a non-empty interface, so every persisted
`{"type":"message","message":{...}}` line fails with (verified empirically with a
synthetic line):

```
json: cannot unmarshal object into Go struct field SessionEntry.message of type llm.Message
```

Both readers treat the error as "skip it":

- `GetEntries` — session_manager.go:973-980: `if json.Unmarshal(raw, entry) == nil { entries = append(...) }`
- `buildIndex` — session_manager.go:~844-847: `if json.Unmarshal(raw, entry) != nil { continue }`

Result: after `OpenSessionManager` loads the file (which it does correctly —
the JSONL data is on disk and marshaling works; only unmarshaling is broken),
**zero message entries survive** in `fileEntries`/`byID`. Model-thinking-level
and other non-message entries do decode, which is why the session looks
partially alive (name, model via `model_change` entries) but has no messages.

### Why the TS reference never hits this

JavaScript's `JSON.parse` is type-erased — pi's `SessionManager.open` /
`loadEntriesFromFile` (session-manager.ts:1280-1288, 438) decodes into plain
objects; the rich TS types are compile-time only and can never fail at runtime.
The Go port's typed decoding is a port-introduced defect class.

## 3. Consequences

1. **Empty transcript:** `rebuildTranscript` (session.go:116-136) iterates
   `GetEntries()`, skips everything that isn't `EntryTypeMessage` (nothing
   survives), clears the chat, renders nothing.
2. **LLM context lost:** `CreateAgentSession` (core/sdk.go:448-450) calls
   `BuildSessionContext()` → no messages → `hasExistingSession == false` →
   `agent.SetMessages(existingSession.Messages)` never runs. The model resumes
   with zero prior conversation. Model restore from the last assistant message
   (session_manager.go:513) also can't fire.
3. **Leaf/tree walk poisoned:** `leafID` ends at the last decodable
   non-message entry or empty → `BuildSessionContext` returns early
   (session_manager.go:473-476).

### Ruled out

- Session-file loading/ordering: `OpenSessionManager` reads the file
  synchronously before `SwitchSession` returns; `rebindSession` →
  `rebuildTranscript` runs after. Ordering is fine.
- The render loop itself: even with entries present, the switch only handles
  `llm.AssistantMessage` / `*llm.AssistantMessage` / `llm.UserMessage` — see §4.
- Live-session rendering: unaffected (components come from real-time events,
  never from `GetEntries`) — which is why the bug stayed invisible until a
  resume was actually attempted.

## 4. Full fix scope (story 8)

1. **Decode messages** — role-keyed decode of `SessionEntry.Message` into
   concrete types: `user` → `llm.UserMessage`, `assistant` →
   `llm.AssistantMessage`, `tool` → `llm.ToolResultMessage[any]`,
   `bashExecution` → the BashExecutionMessage shape (it implements
   `llm.Message` and is persisted). Either a custom `UnmarshalJSON` on
   `SessionEntry` or a shared decode helper used by both `GetEntries` and
   `buildIndex` (they must not diverge). Watch: content blocks are
   `{type:"text"|"thinking"|"toolCall"|...}` — the same style of typed decode
   is needed there (assistant `Content []llm.ContentBlock` is also an
   interface — verify how/whether block entries decode; likely needs the same
   treatment keyed on `type`).
2. **Context restore comes free** — the `agent.SetMessages` wiring already
   exists and starts working once entries decode.
3. **Rendering to pi parity** — pi's `renderSessionContext`
   (interactive-mode.ts:3096-3164) does much more than our two-case switch:
   - assistant messages → AssistantMessageComponent, then one
     ToolExecutionComponent per toolCall block (created with
     `setExpanded(toolOutputExpanded)`), aborted/error stopReasons get an
     error result immediately;
   - following `toolResult` messages are matched onto those components by
     `toolCallId` (`updateResult`) — unmatched ones stay pending;
   - user messages get a leading Spacer when chat is non-empty, plus editor
     history (`populateHistory`);
   - bashExecution / custom (display) / compactionSummary / branchSummary all
     render dedicated components.
   Extend `rebuildTranscript` to this behavior (the tool-components gap was
   already flagged #19-adjacent; it folds into this story).
4. **Tests** (none cover this path today):
   - Round-trip: append user/assistant/toolResult/bash entries through
     SessionManager, reload from disk, assert `GetEntries` and
     `BuildSessionContext` restore all messages with correct types and order.
   - `rebuildTranscript` render test over disk-loaded entries (components
     created, tool results matched, editor history fed).

## 5. Reference (pi) — what "correct" looks like

- `SessionManager.open` / `setSessionFile` (session-manager.ts:1280-1288,
  698-719): JSONL → plain objects, never fails on message shapes.
- `buildSessionContext` (session-manager.ts:315-422): leaf-to-root walk →
  flat message list (message / custom_message / branch_summary / compaction
  entries; header + settings entries consumed for state).
- `createAgentSession` (sdk.ts:214, 378-383): `agent.state.messages =
  existingSession.messages` when non-empty — same rendering/context pairing
  the Go port intended.
- `renderSessionContext` (interactive-mode.ts:3096-3164) +
  `addMessageToChat` (2998-3088): full transcript re-render incl. tool
  components and result matching.

---

## 6. Fix applied (2026-10-09, story 8)

- **Decoder** — new `core/session_decode.go`: `decodeSessionEntry(raw)` is the
  single decode path used by BOTH `GetEntries` and `buildIndex`. Role-keyed
  (`user`/`assistant`/`tool`) into `llm.UserMessage`/`llm.AssistantMessage`/
  `llm.ToolResultMessage[any]` via shadow-struct embedding (content arrays are
  `[]json.RawMessage` first, then decoded per block), plus
  `decodeContentBlocks` keyed on block `type` (text/thinking/toolCall/image;
  toolCall decodes as `*llm.ToolCall` to match the live provider shape;
  unknown block types skip forward-compatibly). Tagless core message structs
  (BashExecutionMessage etc. — capitalized keys, no role) are sniffed by
  distinguishing keys (`Command`/`CustomType`/`FromID`/`TokensBefore`).
  Lives in package core: compaction cannot import BashExecutionMessage
  (core→compaction→llm), and only these two sites unmarshal SessionEntry.
- **Transcript rebuild** — `rebuildTranscript` (interactive/session.go) now
  mirrors pi `renderSessionContext`: assistant messages render plus one
  `NewToolExecution` + `MarkStarted()` per toolCall block (aborted → "Operation
  aborted" error result inline; error → ErrorMessage), following toolResult
  messages match onto pending components by `toolCallID` via
  `UpdateResult(extractResultText(...), IsError, false)`, bash executions
  rebuild via `NewBashExecution`+`AppendOutput`+`SetComplete`, compaction
  entries render the summary card (collapsed, audit #23 behavior), user
  messages get a non-empty-guarded leading spacer + `editor.AddToHistory`
  (pi populateHistory). branchSummary/custom_message remain deferred (no Go
  component; context restore doesn't depend on them).
- **Context restore** — no code needed: `agent.SetMessages` wiring
  (sdk.go:448-450) works as soon as entries decode.
- **Tests** — `core/session_decode_test.go`
  (`TestSessionMessageRoundTrip` production append→reload→typed restore incl.
  thinking/toolCall blocks and BuildSessionContext; `TestDecodeLegacySessionLines`
  hand-written JSONL with legacy tagless bash shape + header filtering;
  `TestDecodeContentBlocks`) and `interactive/session_rebuild_test.go`
  (`TestRebuildTranscriptRendersDecodedEntries` — child inventory + rendered
  text + collapsed/expanded compaction card + editor history;
  `TestRebuildTranscriptAbortedToolCallGetsErrorResult`). Added the read-only
  `Editor.History()` accessor for assertions.
