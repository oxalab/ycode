# Test inventory — `terminal.test.ts`

- **Source:** [`test/terminal.test.ts`](../../../../reference/tui/test/terminal.test.ts) (233 LOC)
- **Verifies:** [`src/terminal.ts`](../../../../reference/tui/src/terminal.ts) → [03-terminal.md](../03-terminal.md)
- **Harness:** mocks `process.stdout.write` + `process.stdin.on` via a `NegotiationHarness`; casts to call private `queryAndEnableKittyProtocol`; `mock.timers` for the split-response timeout cases. Env/property descriptor save-restore for dimension fallback.
- **Go target:** `terminal_test.go` — but note: much of this is Node-stdio-specific. If ProcessTerminal is backed by Bubble Tea ([03-terminal.md](../03-terminal.md)), the **negotiation parser** (`parseKeyboardProtocolNegotiationSequence`) and `normalizeAppleTerminalInput` are pure and port directly; the stdio-harness cases adapt to the chosen adapter.

## Groups

### `normalizeAppleTerminalInput` (4)
- Apple-Terminal `\r` + shift-held → `\x1b[13;2u` (CSI-u shift+enter); unchanged when no shift / non-Apple / non-Return input.

### Kitty keyboard protocol negotiation (7)
- **query sequence** written first: `\x1b[>7u\x1b[?u\x1b[c`; no modifyOtherKeys yet.
- **non-zero flags** (`\x1b[?7u`) → kitty active, input consumed, no modOtherKeys; `stop()` writes `\x1b[<u` exactly once.
- **zero flags** (`\x1b[?0u`) → modifyOtherKeys fallback (`\x1b[>4;2m` once; on stop `\x1b[>4;0m` once).
- **device-attributes** (`\x1b[?62;4;52c`) without kitty flags → modOtherKeys fallback.
- **normal input forwarded** while waiting for kitty response.
- **split kitty confirmation** (`\x1b[?7` then `u` across chunks within 150ms) → buffers, then activates.
- **replays buffered CSI-prefix** (`\x1b[`) when, after the 150ms fragment timeout, it isn't a kitty response (forwards the raw `\x1b[`).

### Dimensions (1)
- `columns`/`rows` fall back to `COLUMNS`/`LINES` env before the 80/24 default (when `stdout.columns` undefined).

## Port note
The negotiation logic is **response-driven, not timeout-driven** (the DA sentinel enables this) — a CHANGELOG-flagged fix ([15-appendix.md](../15-appendix.md)). The split-response 150ms buffer + replay path is the trickiest part; port `parseKeyboardProtocolNegotiationSequence` + the buffer/replay helpers, and test with the mock-timer equivalents (Go: fake clock or inject a clock interface).
