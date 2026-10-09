# Agent-Loop Context Bug — orphaned tool results on every tool follow-up call

> **Found:** 2026-10-09 · **Status:** ✅ FIXED (2026-10-09) — one-line append in `runLoop` + regression test; secondaries (§5) still open
> **Symptom class:** model disowns its own tool results and loops re-running commands
> **Root cause:** one missing append in `packages/agent-sdk/src/agent_loop.go`

---

## 1. Symptoms (observed live, TUI + GLM/z.ai backend, 2026-10-09)

Across multiple transcripts, the model repeatedly produced thinking like:

- *"the human turn contains what appears to be tool results"*
- *"this might be a fabricated context"*
- *"the results seem to be injected"*

and then **re-ran the same command it had just run**, never trusting the result. In the
worst case it looped the identical file-creation bash command ~8 times and never
advanced to the next tool. The same self-doubt language appeared in every session that
involved two or more sequential tool calls; single-tool sessions looked normal.

## 2. Root cause — the assistant `tool_calls` message never enters the loop context

`packages/agent-sdk/src/agent_loop.go`:

**`streamAssistantResponse` takes `AgentContext` by value** (line ~225:
`func streamAssistantResponse(context AgentContext, ...)`). All of its context
mutations therefore hit a **local copy**:

- line ~293 (`"start"`): `context.Messages = append(context.Messages, *partialMessage)`
- line ~301 (deltas): `context.Messages[len(context.Messages)-1] = *partialMessage`
- lines ~312-314 (`"done"`): replaces the partial / appends the final message

Back in `runLoop`, the returned assistant message is appended **only** to
`newMessages` (the discarded return slice) — line ~177:

```go
*newMessages = append(*newMessages, message)
```

A grep of the whole file confirms the **only** appends to `currentContext.Messages` are:

- line ~83 — initial prompts
- line ~162 — drained steering messages
- line ~197 — **tool results**:
  ```go
  for _, result := range toolResults {
      currentContext.Messages = append(currentContext.Messages, result)
      *newMessages = append(*newMessages, result)
  }
  ```

The assistant message carrying the `tool_calls` is **never appended back**.

### Consequence

On iteration 2+ of the inner loop, the LLM request built from
`currentContext` is:

```
[system, user, {role:"tool", tool_call_id:...}, {role:"tool", ...}]   ← orphaned
```

with **no preceding assistant message containing `tool_calls`**. Strict
OpenAI-compatible servers reject this shape. Lenient ones (GLM/z.ai chat template)
fold orphaned tool content into the user turn — the model literally sees its own
tool output as text "injected into the human turn", disowns it, re-runs the
command, and the new results are orphaned again → the observed loop.

### Not a race, not storage corruption

- Every append happens on the single loop goroutine — no concurrency issue.
- Storage/session order is correct: the loop emits assistant `MessageEnd` before
  each tool-result message (agent_loop.go:317 vs 691-694), and
  `agent_session.go:210-219` + `session_manager.AppendMessage` chain entries
  serially. Session files/`BuildSessionContext` are fine.
- The wire shape of tool results is correct:
  `openai-completions.go:592-612` serializes `role:"tool"` + `tool_call_id`,
  matching the TS reference.
- `TransformMessages` (transform-messages.go:228-279) synthesizes results for
  orphaned tool *calls* but does nothing for orphaned tool *results* — so the
  bug passes straight through to the provider.

## 3. TS parity reference (what the reference does)

`pi/packages/agent/src/agent-loop.ts` — the context is a JS object passed by
reference, so the appends stick:

- :313-318 (`start`) — `context.messages.push(partialMessage)` (partial is in the
  context during streaming)
- :331-333 (deltas) — `context.messages[context.messages.length - 1] = partialMessage`
- :344-346 (`done`) — replaced with the final message
- :207-215 — tool results pushed **after** the assistant message, per-step await
  ordering, no race possible

Net request shape on every tool follow-up:
`[system, ..., assistant(tool_calls), tool, tool, ...]` — which is what the
Go port must produce too.

## 4. The fix — ✅ APPLIED (2026-10-09)

One line in `runLoop` (which both `RunAgentLoop` and `RunAgentLoopContinue`
share — `currentContext` is a `*AgentContext` there), right after
`streamAssistantResponse` returns, before the StopReason check and the
tool-results append at ~197:

```go
*newMessages = append(*newMessages, message)
currentContext.Messages = append(currentContext.Messages, message)
```

**Regression test** — `packages/agent-sdk/src/agent_loop_test.go`
(`TestAgentLoopKeepsAssistantToolCallsInContext`): fake `streamFn` answers a
tool-call response then a plain response, capturing `chatCtx.Messages` per
call; asserts the second request is exactly `user → assistant(tool_calls,
id=call-1) → tool(toolCallId=call-1)` and 4 newMessages total. Mutation
verified: with the append removed the test fails with
`second request roles = [user tool]` — reproducing the bug byte-for-byte.

## 5. Secondary findings from the same investigation (real, not the loop cause)

### 5a. GLM thinking replay silently dropped + signature-as-key bug

- GLM streams thinking via `reasoning_content` (openai-completions.go:894-916) but
  **never sets `ThinkingSignature`**, so the replay gate at :519
  (`if thinkingBlocks[0].ThinkingSignature != ""`) fails → past turns' thinking is
  dropped from every replayed assistant message.
- Where a signature *is* present, :520-525 does
  `signatureKey := thinkingBlocks[0].ThinkingSignature; assistantMsg[signatureKey] = strings.Join(...)`
  — using the signature **value** as a JSON map key.
- TS mechanism (pi openai-completions.ts:213-224, 313-336): during streaming,
  `ensureThinkingBlock` stores the original wire **field name**
  (`reasoning_content` / `reasoning` / `reasoning_text`) in `thinkingSignature`;
  replay (pi :821-846) then emits `(assistantMsg)[signature] = joined thinking` —
  i.e. the field is a *name*, not a value. Port both halves: capture the field
  name during streaming, replay under it.

### 5b. Per-model `Compat` overrides never consulted

`detectOpenAICompat` (openai-completions.go:1124+, z.ai detection via
provider/`api.z.ai`) builds compat from built-in heuristics only and never merges
`model.Compat` (llm types.go:445). Effects:

- `ZaiToolStream` defaults false and is never set → `tool_stream` is never sent
  (TS sends `tool_stream: true` for GLM via `compat.zaiToolStream`,
  pi openai-completions.ts:537-539).
- `RequiresAssistantAfterToolResult` can never be enabled by user config.
- `enable_thinking` is stuck on for z.ai: sent whenever `model.Reasoning`
  (:290-295) and `buildOpenAICompletionsSimpleOptions` (:1287) always passes a
  non-nil `ReasoningEffort` — TS gates it on `options?.reasoningEffort`
  (pi :553-554).

### 5c. Late-streamed tool-call IDs never backfilled

`openai-completions.go:946-951` captures the tool-call `id` only when seen;
late-arriving IDs are tracked in the local `currentBlock` (:958-962) but never
written back into the finished `ToolCall` block (`finishCurrentOpenAICompletionsBlock`,
:1051, syncs only `Arguments`). If GLM streams the id in a later chunk than the
first delta, `tool_calls[].id` serializes as `""`. Backfill the id in the
finish/flush path alongside Arguments.

### 5d. Rendering verdict — display is innocent (with one known gap)

- Live transcript rendering is a faithful, well-separated port: tool call + result
  live in one tinted box (Spacer + Box, tool_execution.go:52-53 / :117), assistant
  messages insert their own leading spacer; nothing in the render path feeds back
  into provider context (verified).
- Known display-only gap (pre-existing, adjacent to audit #19): `rebuildTranscript`
  (session.go:116-136) renders no tool components at all — after `/switch`, `/new`,
  or a rebind, tool activity vanishes from the transcript. pi recreates tool boxes
  and fills their results on reload (interactive-mode.ts:3096-3153).
- Interleaved-thinking display ordering artifact (both implementations): all
  thinking/text of one assistant message renders in a single component placed
  before that message's tool boxes, so displayed order diverges from emission
  order. Cosmetic; Go shows it more prominently (thinking always expanded,
  `hideThinking` hardcoded false, no toggle — pi has ctrl+toggle, default matches).

## 6. Ruled out during investigation

- Wire shape of tool results (role/tool_call_id) — correct (see §2).
- Race between assistant append and tool-results append — single-goroutine loop.
- Session storage order — FIFO event channel, serial appends, correct.
- TUI rendering contaminating context — display tree is write-only.
- C1/C3 bash fixes — tool results returned instantly and correctly; the loop was
  model-side context confusion, not tool latency.

## 7. Fix order proposed

1. **Story 7 (critical):** the one-line append + regression test (§4).
2. Story 8: 5a thinking-replay port (capture field name + replay under it).
3. Story 9: 5b Compat merge (respect `model.Compat`; gate `enable_thinking` on
   actual reasoning effort).
4. Story 10: 5c tool-call id backfill.
5. (Display) `rebuildTranscript` tool components — fold into the #19 rebuild-gap
   story when that is scheduled.
