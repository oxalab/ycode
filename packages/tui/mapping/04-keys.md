# 04 — Keys (keyboard input parsing)

- **TS source:** [`keys.ts`](../../reference/tui/src/keys.ts) (1400 LOC)
- **Go target:** `keys.go`
- **Package:** `tui`
- **Pure logic — no terminal I/O. Port 1:1, full test coverage exists.**

Supports three input dialects simultaneously: legacy escape sequences, Kitty
keyboard protocol (CSI-u), and xterm modifyOtherKeys. Plus a mode flag
(`kittyProtocolActive`) that disambiguates overlapping sequences.

---

## Public API

| TS export | Go |
|---|---|
| `Key` (object literal w/ special keys + `ctrl/shift/alt/super/ctrlShift/...` builders) | `var Key` struct or package of constructors — see below |
| `type KeyId` (union of base keys + modifier combos) | `type KeyID string` (Go can't express the literal union; document valid forms) |
| `matchesKey(data, keyId)` | `MatchesKey(data, keyID string) bool` |
| `parseKey(data)` | `ParseKey(data string) (KeyID, bool)` (TS returns `string \| undefined`) |
| `decodeKittyPrintable(data)` | `DecodeKittyPrintable(data string) (string, bool)` |
| `decodePrintableKey(data)` | `DecodePrintableKey(data string) (string, bool)` |
| `isKeyRelease(data)` | `IsKeyRelease(data string) bool` |
| `isKeyRepeat(data)` | `IsKeyRepeat(data string) bool` |
| `isKittyProtocolActive()` | `IsKittyProtocolActive() bool` |
| `setKittyProtocolActive(active)` | `SetKittyProtocolActive(bool)` — package-global (set by terminal) |
| `type KeyEventType` ("press"/"repeat"/"release") | `type KeyEventType string` |

### `Key` helper in Go
TS `Key` is an object with `escape: "escape"`, `ctrl("c")`, etc. In Go:

```go
// Key IDs
const KeyEscape KeyID = "escape"   // ... all specials
// Builder funcs
func Ctrl(k string) KeyID      { return KeyID("ctrl+"+k) }
func Shift(k string) KeyID     { return KeyID("shift+"+k) }
func Alt(k string) KeyID       { return KeyID("alt+"+k) }
func Super(k string) KeyID     { return KeyID("super+"+k) }
func CtrlShift(k string) KeyID { return KeyID("ctrl+shift+"+k) }
// ... all combined/triple modifiers
```

`KeyId` union is purely a TS compile-time safety net; Go uses `KeyID string` +
documents the grammar in a doc comment. The string format (`"ctrl+c"`,
`"shift+tab"`) is the wire contract — keep identical.

## Internal tables (port as Go vars)

| TS | Go |
|---|---|
| `MODIFIERS {shift:1, alt:2, ctrl:4, super:8}` | `const modShift, modAlt, modCtrl, modSuper = 1,2,4,8` |
| `LOCK_MASK = 64+128` | `const lockMask = 64 | 128` |
| `CODEPOINTS {escape:27,tab:9,enter:13,space:32,backspace:127,kpEnter:57414}` | `const cpEscape ...` |
| `ARROW_CODEPOINTS {up:-1,...}` | `var arrowCodepoints` |
| `FUNCTIONAL_CODEPOINTS {delete:-10,...}` | `var functionalCodepoints` |
| `KITTY_FUNCTIONAL_KEY_EQUIVALENTS` (Map, keypad→logical) | `var kittyFunctionalKeyEquivalents map[int]int` |
| `SYMBOL_KEYS` (Set) | `var symbolKeys map[rune]bool` |
| `LEGACY_KEY_SEQUENCES`, `LEGACY_SHIFT_SEQUENCES`, `LEGACY_CTRL_SEQUENCES`, `LEGACY_SEQUENCE_KEY_IDS` | `var legacyKey/Shift/Ctrl map[string][]string`, `var legacySequenceKeyIDs map[string]KeyID` |

## Internal functions (port verbatim)

| TS | Go | Notes |
|---|---|---|
| `normalizeKittyFunctionalCodepoint` | same | keypad → digit/symbol/arrow |
| `normalizeShiftedLetterIdentityCodepoint` | same | shift+A → 'a' for matching |
| `matchesLegacySequence` / `matchesLegacyModifierSequence` | same | |
| `parseKittySequence` | `parseKittySequence` | returns codepoint/shifted/base/mod/eventType |
| `matchesKittySequence` | same | includes base-layout fallback for non-Latin (Cyrillic/Dvorak) |
| `parseModifyOtherKeysSequence` / `matchesModifyOtherKeys` | same | `\x1b[27;mod;code~` |
| `isWindowsTerminalSession` | same | WT_SESSION + no SSH_* |
| `matchesRawBackspace` | same | 0x08 ambiguity (Ctrl+Backspace vs plain) |
| `rawCtrlChar` | same | `code & 0x1f` |
| `isDigitKey`, `matchesPrintableModifyOtherKeys` | same | |
| `formatKeyNameWithModifiers`, `parseKeyId` | same | |
| `formatParsedKey` | same | codepoint → "ctrl+c" etc., base-layout rules |
| `parseEventType` | same | |

## `matchesKey` — the big switch

Port the entire switch (`escape/space/tab/enter/backspace/insert/delete/clear/
home/end/pageUp/pageDown/up/down/left/right/f1..f12` + the single-char fallthrough)
**exactly**, including the `_kittyProtocolActive`-dependent branches (e.g. plain
`\n` = `enter` when kitty off, `shift+enter` when kitty on; `\x1b\r` = alt+enter off /
shift+enter on). Every branch is pinned by `keys.test.ts` — port that test file first
and use it as the spec.

## Tests to port
- **`keys.test.ts` (614)** — exhaustive; port as table-driven tests. This is the
  single most important test file for input correctness. Covers kitty alternate
  keys (Cyrillic/Dvorak), modifyOtherKeys, legacy, Windows-Terminal 0x08 over SSH,
  alt-prefixed sequences, keypad normalization, decode-printable.
