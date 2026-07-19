# 05 — StdinBuffer (sequence splitter)

- **TS source:** [`stdin-buffer.ts`](../../reference/tui/src/stdin-buffer.ts) (434 LOC)
- **Go target:** `stdinbuffer.go`
- **Package:** `tui`
- **Based on OpenTUI** (MIT, Copyright 2025 opentui) — preserve attribution in
  file header.

Buffers raw stdin and emits **complete** escape sequences via a callback/channel.
Necessary because stdin chunks can split a single sequence across events (common for
mouse SGR, Kitty responses over SSH).

---

## API

| TS | Go |
|---|---|
| `class StdinBuffer extends EventEmitter` | `type StdinBuffer struct{...}` |
| `new StdinBuffer({timeout?})` (default 10ms) | `NewStdinBuffer(timeout time.Duration) *StdinBuffer` |
| `process(data string \| Buffer)` | `Process(data []byte)` or `ProcessString(s string)` |
| event `"data": (seq) => void` | `OnData(func(seq string))` or emit to a channel `Data chan<- string` |
| event `"paste": (content) => void` | `OnPaste(func(content string))` |
| `flush()` → string[] | `Flush() []string` |
| `clear()` | `Clear()` |
| `getBuffer()` | `Buffered() string` |
| `destroy()` | `Destroy()` |
| `type StdinBufferOptions` | struct field |
| `type StdinBufferEventMap` | (not needed in Go) |

**Design choice:** TS uses Node `EventEmitter`. Go: expose `OnData`/`OnPaste`
registrations, or feed a channel. The terminal adapter calls `Process`; consumers
register callbacks. Channel option is cleaner for the Bubble Tea adapter.

## Behavior to port (all pinned by tests)

1. **`isCompleteSequence(data) → "complete" | "incomplete" | "not-escape"`**
   - CSI (`ESC [`): complete when final byte is 0x40–0x7E (`@`–`~`).
   - Old-style mouse `ESC[M` needs ESC[M + 3 bytes (6 total).
   - SGR mouse `ESC[<B;X;Y[Mm]` validated by regex.
   - OSC (`ESC ]`) terminates on BEL (`\x07`) or ST (`ESC \`).
   - DCS (`ESC P`), APC (`ESC _`) terminate on ST.
   - SS3 (`ESC O`) = ESC O + 1 char.
   - ESC + single char = meta key (complete).
2. **`extractCompleteSequences(buf) → {sequences, remainder}`** — walk buffer,
   accumulate complete sequences, leave a remainder. **WezTerm regression:** if
   candidate is `\x1b\x1b` and next char starts a new escape (`[ ] O P _`), emit only
   the first ESC and restart from the second (Escape-key press as raw ESC + release
   as CSI-u).
3. **Bracketed paste:** on `\x1b[200~` enter paste mode, accumulate until
   `\x1b[201~`, emit the content via the `paste` event (no `data` events for paste
   bytes). Handles paste spanning chunks and bytes before/after the markers.
4. **Kitty printable dedup:** when a Kitty CSI-u printable sequence (e.g.
   `\x1b[224u` → `à`) is immediately followed by its raw char, drop the raw
   duplicate (`pendingKittyPrintableCodepoint` state). Cross-chunk dedup too.
   - `parseUnmodifiedKittyPrintableCodepoint` regex `/^\x1b\[(\d+)(?::\d*)?(?::\d+)?u$/`,
     cp ≥ 32.
5. **Timeout flush:** if buffer non-empty after processing, arm a `time.Timer`
   (default 10ms) that flushes the remainder.
6. **High-byte conversion:** single buffer byte > 127 → `ESC + (byte-128)`.

## Constants

| TS | Go |
|---|---|
| `ESC`, `BRACKETED_PASTE_START`, `BRACKETED_PASTE_END` | `const` |
| default timeout 10ms | `const defaultTimeout = 10 * time.Millisecond` |

## Tests to port
- **`stdin-buffer.test.ts` (458)** — full coverage: regular chars, complete/partial
  escape sequences, mixed content, Kitty protocol (press/release/batched, WezTerm
  ESC+ESC+CSI regression, printable dedup), mouse (SGR + old-style `ESC[M`),
  bracketed paste (chunks, unicode, newlines), flush/clear/destroy, timeout flush.
  Port as table-driven + sub-tests; the async timeout cases use `time.Sleep`.
