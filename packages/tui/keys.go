package tui

// This file is a Go port of reference/tui/src/keys.ts — keyboard input handling
// for legacy escape sequences, the Kitty keyboard protocol (CSI-u), and xterm
// modifyOtherKeys. See packages/tui/mapping/04-keys.md.

import (
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
)

// KeyID is a key identifier string such as "enter", "ctrl+c", "shift+tab".
type KeyID string

// KeyEventType is press/repeat/release (Kitty flag 2).
type KeyEventType string

const (
	EventPress   KeyEventType = "press"
	EventRepeat  KeyEventType = "repeat"
	EventRelease KeyEventType = "release"
)

// ---------------------------------------------------------------------------
// Key helper — typed key identifiers. Use Key.Escape, Key.Enter, …, or
// Key.Ctrl("c"), Key.Shift("tab"), Key.CtrlShift("p"), ….
// ---------------------------------------------------------------------------

type keyHelper struct {
	Escape, Esc, Enter, Return, Tab, Space KeyID
	Backspace, Delete, Insert, Clear       KeyID
	Home, End, PageUp, PageDown            KeyID
	Up, Down, Left, Right                  KeyID
	F1, F2, F3, F4, F5, F6, F7, F8         KeyID
	F9, F10, F11, F12                      KeyID

	// Symbol keys
	Backtick, Hyphen, Equals                              KeyID
	LeftBracket, RightBracket, Backslash                  KeyID
	Semicolon, Quote, Comma, Period, Slash                KeyID
	Exclamation, At, Hash, Dollar, Percent, Caret         KeyID
	Ampersand, Asterisk, LeftParen, RightParen            KeyID
	Underscore, Plus, Pipe, Tilde                         KeyID
	LeftBrace, RightBrace, Colon, LessThan, GreaterThan   KeyID
	Question                                              KeyID
}

// Key is the typed key-identifier helper.
var Key = keyHelper{
	Escape: "escape", Esc: "esc", Enter: "enter", Return: "return", Tab: "tab", Space: "space",
	Backspace: "backspace", Delete: "delete", Insert: "insert", Clear: "clear",
	Home: "home", End: "end", PageUp: "pageUp", PageDown: "pageDown",
	Up: "up", Down: "down", Left: "left", Right: "right",
	F1: "f1", F2: "f2", F3: "f3", F4: "f4", F5: "f5", F6: "f6", F7: "f7", F8: "f8",
	F9: "f9", F10: "f10", F11: "f11", F12: "f12",
	Backtick: "`", Hyphen: "-", Equals: "=",
	LeftBracket: "[", RightBracket: "]", Backslash: `\`,
	Semicolon: ";", Quote: "'", Comma: ",", Period: ".", Slash: "/",
	Exclamation: "!", At: "@", Hash: "#", Dollar: "$", Percent: "%", Caret: "^",
	Ampersand: "&", Asterisk: "*", LeftParen: "(", RightParen: ")",
	Underscore: "_", Plus: "+", Pipe: "|", Tilde: "~",
	LeftBrace: "{", RightBrace: "}", Colon: ":", LessThan: "<", GreaterThan: ">",
	Question: "?",
}

func (keyHelper) Ctrl(k string) KeyID            { return KeyID("ctrl+" + k) }
func (keyHelper) Shift(k string) KeyID           { return KeyID("shift+" + k) }
func (keyHelper) Alt(k string) KeyID             { return KeyID("alt+" + k) }
func (keyHelper) Super(k string) KeyID           { return KeyID("super+" + k) }
func (keyHelper) CtrlShift(k string) KeyID       { return KeyID("ctrl+shift+" + k) }
func (keyHelper) ShiftCtrl(k string) KeyID       { return KeyID("shift+ctrl+" + k) }
func (keyHelper) CtrlAlt(k string) KeyID         { return KeyID("ctrl+alt+" + k) }
func (keyHelper) AltCtrl(k string) KeyID         { return KeyID("alt+ctrl+" + k) }
func (keyHelper) ShiftAlt(k string) KeyID        { return KeyID("shift+alt+" + k) }
func (keyHelper) AltShift(k string) KeyID        { return KeyID("alt+shift+" + k) }
func (keyHelper) CtrlSuper(k string) KeyID       { return KeyID("ctrl+super+" + k) }
func (keyHelper) SuperCtrl(k string) KeyID       { return KeyID("super+ctrl+" + k) }
func (keyHelper) ShiftSuper(k string) KeyID      { return KeyID("shift+super+" + k) }
func (keyHelper) SuperShift(k string) KeyID      { return KeyID("super+shift+" + k) }
func (keyHelper) AltSuper(k string) KeyID        { return KeyID("alt+super+" + k) }
func (keyHelper) SuperAlt(k string) KeyID        { return KeyID("super+alt+" + k) }
func (keyHelper) CtrlShiftAlt(k string) KeyID    { return KeyID("ctrl+shift+alt+" + k) }
func (keyHelper) CtrlShiftSuper(k string) KeyID  { return KeyID("ctrl+shift+super+" + k) }

// ---------------------------------------------------------------------------
// Global Kitty-protocol state (set by the terminal after negotiation)
// ---------------------------------------------------------------------------

var kittyProtocolActive atomic.Bool

// SetKittyProtocolActive sets the global Kitty keyboard protocol state.
func SetKittyProtocolActive(active bool) { kittyProtocolActive.Store(active) }

// IsKittyProtocolActive reports whether the Kitty keyboard protocol is active.
func IsKittyProtocolActive() bool { return kittyProtocolActive.Load() }

// ---------------------------------------------------------------------------
// Constants & tables
// ---------------------------------------------------------------------------

const (
	modShift = 1
	modAlt   = 2
	modCtrl  = 4
	modSuper = 8
	lockMask = 64 | 128 // Caps Lock + Num Lock
)

const (
	cpEscape    = 27
	cpTab       = 9
	cpEnter     = 13
	cpSpace     = 32
	cpBackspace = 127
	cpKpEnter   = 57414 // numpad Enter (Kitty)
)

const (
	arrowUp    = -1
	arrowDown  = -2
	arrowRight = -3
	arrowLeft  = -4
)

const (
	fnDelete    = -10
	fnInsert    = -11
	fnPageUp    = -12
	fnPageDown  = -13
	fnHome      = -14
	fnEnd       = -15
)

var kittyFunctionalKeyEquivalents = map[int]int{
	57399: '0', 57400: '1', 57401: '2', 57402: '3', 57403: '4', 57404: '5',
	57405: '6', 57406: '7', 57407: '8', 57408: '9', 57409: '.', 57410: '/',
	57411: '*', 57412: '-', 57413: '+', 57415: '=', 57416: ',', 57417: arrowLeft,
	57418: arrowRight, 57419: arrowUp, 57420: arrowDown, 57421: fnPageUp,
	57422: fnPageDown, 57423: fnHome, 57424: fnEnd, 57425: fnInsert, 57426: fnDelete,
}

var symbolKeys = map[rune]bool{
	'`': true, '-': true, '=': true, '[': true, ']': true, '\\': true, ';': true, '\'': true,
	',': true, '.': true, '/': true, '!': true, '@': true, '#': true, '$': true, '%': true,
	'^': true, '&': true, '*': true, '(': true, ')': true, '_': true, '+': true, '|': true,
	'~': true, '{': true, '}': true, ':': true, '<': true, '>': true, '?': true,
}

var legacyKeySequences = map[string][]string{
	"up":       {"\x1b[A", "\x1bOA"},
	"down":     {"\x1b[B", "\x1bOB"},
	"right":    {"\x1b[C", "\x1bOC"},
	"left":     {"\x1b[D", "\x1bOD"},
	"home":     {"\x1b[H", "\x1bOH", "\x1b[1~", "\x1b[7~"},
	"end":      {"\x1b[F", "\x1bOF", "\x1b[4~", "\x1b[8~"},
	"insert":   {"\x1b[2~"},
	"delete":   {"\x1b[3~"},
	"pageUp":   {"\x1b[5~", "\x1b[[5~"},
	"pageDown": {"\x1b[6~", "\x1b[[6~"},
	"clear":    {"\x1b[E", "\x1bOE"},
	"f1":       {"\x1bOP", "\x1b[11~", "\x1b[[A"},
	"f2":       {"\x1bOQ", "\x1b[12~", "\x1b[[B"},
	"f3":       {"\x1bOR", "\x1b[13~", "\x1b[[C"},
	"f4":       {"\x1bOS", "\x1b[14~", "\x1b[[D"},
	"f5":       {"\x1b[15~", "\x1b[[E"},
	"f6":       {"\x1b[17~"}, "f7": {"\x1b[18~"}, "f8": {"\x1b[19~"}, "f9": {"\x1b[20~"},
	"f10":      {"\x1b[21~"}, "f11": {"\x1b[23~"}, "f12": {"\x1b[24~"},
}

var legacyShiftSequences = map[string][]string{
	"up": {"\x1b[a"}, "down": {"\x1b[b"}, "right": {"\x1b[c"}, "left": {"\x1b[d"},
	"clear": {"\x1b[e"}, "insert": {"\x1b[2$"}, "delete": {"\x1b[3$"},
	"pageUp": {"\x1b[5$"}, "pageDown": {"\x1b[6$"}, "home": {"\x1b[7$"}, "end": {"\x1b[8$"},
}

var legacyCtrlSequences = map[string][]string{
	"up": {"\x1bOa"}, "down": {"\x1bOb"}, "right": {"\x1bOc"}, "left": {"\x1bOd"},
	"clear": {"\x1bOe"}, "insert": {"\x1b[2^"}, "delete": {"\x1b[3^"},
	"pageUp": {"\x1b[5^"}, "pageDown": {"\x1b[6^"}, "home": {"\x1b[7^"}, "end": {"\x1b[8^"},
}

var legacySequenceKeyIDs = map[string]KeyID{
	"\x1bOA": "up", "\x1bOB": "down", "\x1bOC": "right", "\x1bOD": "left",
	"\x1bOH": "home", "\x1bOF": "end",
	"\x1b[E": "clear", "\x1bOE": "clear", "\x1bOe": "ctrl+clear", "\x1b[e": "shift+clear",
	"\x1b[2~": "insert", "\x1b[2$": "shift+insert", "\x1b[2^": "ctrl+insert",
	"\x1b[3$": "shift+delete", "\x1b[3^": "ctrl+delete",
	"\x1b[[5~": "pageUp", "\x1b[[6~": "pageDown",
	"\x1b[a": "shift+up", "\x1b[b": "shift+down", "\x1b[c": "shift+right", "\x1b[d": "shift+left",
	"\x1bOa": "ctrl+up", "\x1bOb": "ctrl+down", "\x1bOc": "ctrl+right", "\x1bOd": "ctrl+left",
	"\x1b[5$": "shift+pageUp", "\x1b[6$": "shift+pageDown", "\x1b[7$": "shift+home", "\x1b[8$": "shift+end",
	"\x1b[5^": "ctrl+pageUp", "\x1b[6^": "ctrl+pageDown", "\x1b[7^": "ctrl+home", "\x1b[8^": "ctrl+end",
	"\x1bOP": "f1", "\x1bOQ": "f2", "\x1bOR": "f3", "\x1bOS": "f4",
	"\x1b[11~": "f1", "\x1b[12~": "f2", "\x1b[13~": "f3", "\x1b[14~": "f4",
	"\x1b[[A": "f1", "\x1b[[B": "f2", "\x1b[[C": "f3", "\x1b[[D": "f4", "\x1b[[E": "f5",
	"\x1b[15~": "f5", "\x1b[17~": "f6", "\x1b[18~": "f7", "\x1b[19~": "f8",
	"\x1b[20~": "f9", "\x1b[21~": "f10", "\x1b[23~": "f11", "\x1b[24~": "f12",
	"\x1bb": "alt+left", "\x1bf": "alt+right", "\x1bp": "alt+up", "\x1bn": "alt+down",
}

var (
	kittyCsiURe     = regexp.MustCompile(`^\x1b\[(\d+)(?::(\d*))?(?::(\d+))?(?:;(\d+))?(?::(\d+))?u$`)
	kittyArrowRe    = regexp.MustCompile(`^\x1b\[1;(\d+)(?::(\d+))?([ABCD])$`)
	kittyFuncRe     = regexp.MustCompile(`^\x1b\[(\d+)(?:;(\d+))?(?::(\d+))?~$`)
	kittyHomeEndRe  = regexp.MustCompile(`^\x1b\[1;(\d+)(?::(\d+))?([HF])$`)
	modifyOtherKeysRe = regexp.MustCompile(`^\x1b\[27;(\d+);(\d+)~$`)
)

const kittyPrintableAllowedModifiers = modShift | lockMask

// ---------------------------------------------------------------------------
// Event type / release / repeat detection
// ---------------------------------------------------------------------------

func parseEventType(s string) KeyEventType {
	if s == "" {
		return EventPress
	}
	switch s {
	case "2":
		return EventRepeat
	case "3":
		return EventRelease
	}
	return EventPress
}

// IsKeyRelease reports whether data is a Kitty key-release event (flag 2).
// Bracketed-paste content is never treated as a release.
func IsKeyRelease(data string) bool {
	if strings.Contains(data, "\x1b[200~") {
		return false
	}
	for _, suf := range []string{":3u", ":3~", ":3A", ":3B", ":3C", ":3D", ":3H", ":3F"} {
		if strings.Contains(data, suf) {
			return true
		}
	}
	return false
}

// IsKeyRepeat reports whether data is a Kitty key-repeat event (flag 2).
func IsKeyRepeat(data string) bool {
	if strings.Contains(data, "\x1b[200~") {
		return false
	}
	for _, suf := range []string{":2u", ":2~", ":2A", ":2B", ":2C", ":2D", ":2H", ":2F"} {
		if strings.Contains(data, suf) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Kitty protocol parsing
// ---------------------------------------------------------------------------

type parsedKitty struct {
	codepoint     int
	shiftedKey     *int
	baseLayoutKey  *int
	modifier       int
	eventType      KeyEventType
}

type parsedModifyOtherKeys struct {
	codepoint int
	modifier  int
}

func normalizeKittyFunctionalCodepoint(cp int) int {
	if v, ok := kittyFunctionalKeyEquivalents[cp]; ok {
		return v
	}
	return cp
}

func normalizeShiftedLetterIdentityCodepoint(cp, modifier int) int {
	effective := modifier & ^lockMask
	if (effective&modShift) != 0 && cp >= 65 && cp <= 90 {
		return cp + 32
	}
	return cp
}

func parseKittySequence(data string) (parsedKitty, bool) {
	if m := kittyCsiURe.FindStringSubmatch(data); m != nil {
		codepoint, _ := strconv.Atoi(m[1])
		var shifted, baseLayout *int
		if len(m[2]) > 0 {
			v, _ := strconv.Atoi(m[2])
			shifted = &v
		}
		if len(m[3]) > 0 {
			v, _ := strconv.Atoi(m[3])
			baseLayout = &v
		}
		modValue := 1
		if len(m[4]) > 0 {
			modValue, _ = strconv.Atoi(m[4])
		}
		ev := parseEventType(m[5])
		return parsedKitty{codepoint, shifted, baseLayout, modValue - 1, ev}, true
	}
	if m := kittyArrowRe.FindStringSubmatch(data); m != nil {
		modValue, _ := strconv.Atoi(m[1])
		ev := parseEventType(m[2])
		arrowCodes := map[byte]int{'A': arrowUp, 'B': arrowDown, 'C': arrowRight, 'D': arrowLeft}
		return parsedKitty{codepoint: arrowCodes[m[3][0]], modifier: modValue - 1, eventType: ev}, true
	}
	if m := kittyFuncRe.FindStringSubmatch(data); m != nil {
		keyNum, _ := strconv.Atoi(m[1])
		modValue := 1
		if len(m[2]) > 0 {
			modValue, _ = strconv.Atoi(m[2])
		}
		ev := parseEventType(m[3])
		funcCodes := map[int]int{2: fnInsert, 3: fnDelete, 5: fnPageUp, 6: fnPageDown, 7: fnHome, 8: fnEnd}
		if cp, ok := funcCodes[keyNum]; ok {
			return parsedKitty{codepoint: cp, modifier: modValue - 1, eventType: ev}, true
		}
	}
	if m := kittyHomeEndRe.FindStringSubmatch(data); m != nil {
		modValue, _ := strconv.Atoi(m[1])
		ev := parseEventType(m[2])
		cp := fnHome
		if m[3] == "F" {
			cp = fnEnd
		}
		return parsedKitty{codepoint: cp, modifier: modValue - 1, eventType: ev}, true
	}
	return parsedKitty{}, false
}

func matchesKittySequence(data string, expectedCodepoint, expectedModifier int) bool {
	p, ok := parseKittySequence(data)
	if !ok {
		return false
	}
	actualMod := p.modifier & ^lockMask
	expectedMod := expectedModifier & ^lockMask
	if actualMod != expectedMod {
		return false
	}

	normalized := normalizeShiftedLetterIdentityCodepoint(normalizeKittyFunctionalCodepoint(p.codepoint), p.modifier)
	normalizedExpected := normalizeShiftedLetterIdentityCodepoint(normalizeKittyFunctionalCodepoint(expectedCodepoint), expectedModifier)
	if normalized == normalizedExpected {
		return true
	}

	// Base-layout fallback for non-Latin keyboards (Cyrillic/Dvorak). Only fall
	// back when the codepoint is not itself a recognized Latin letter/symbol.
	if p.baseLayoutKey != nil && *p.baseLayoutKey == expectedCodepoint {
		cp := normalized
		isLatinLetter := cp >= 97 && cp <= 122
		isKnownSymbol := symbolKeys[rune(cp)]
		if !isLatinLetter && !isKnownSymbol {
			return true
		}
	}
	return false
}

func parseModifyOtherKeysSequence(data string) (parsedModifyOtherKeys, bool) {
	m := modifyOtherKeysRe.FindStringSubmatch(data)
	if m == nil {
		return parsedModifyOtherKeys{}, false
	}
	modValue, _ := strconv.Atoi(m[1])
	codepoint, _ := strconv.Atoi(m[2])
	return parsedModifyOtherKeys{codepoint, modValue - 1}, true
}

func matchesModifyOtherKeys(data string, expectedKeycode, expectedModifier int) bool {
	p, ok := parseModifyOtherKeysSequence(data)
	if !ok {
		return false
	}
	return p.codepoint == expectedKeycode && p.modifier == expectedModifier
}

func matchesPrintableModifyOtherKeys(data string, expectedKeycode, expectedModifier int) bool {
	if expectedModifier == 0 {
		return false
	}
	p, ok := parseModifyOtherKeysSequence(data)
	if !ok || p.modifier != expectedModifier {
		return false
	}
	return normalizeShiftedLetterIdentityCodepoint(p.codepoint, p.modifier) ==
		normalizeShiftedLetterIdentityCodepoint(expectedKeycode, expectedModifier)
}

func isWindowsTerminalSession() bool {
	wt := osGetenv("WT_SESSION")
	if wt == "" {
		return false
	}
	return osGetenv("SSH_CONNECTION") == "" && osGetenv("SSH_CLIENT") == "" && osGetenv("SSH_TTY") == ""
}

// IsWindowsTerminalSession reports a local Windows Terminal session.
func IsWindowsTerminalSession() bool { return isWindowsTerminalSession() }

func matchesRawBackspace(data string, expectedModifier int) bool {
	if data == "\x7f" {
		return expectedModifier == 0
	}
	if data != "\x08" {
		return false
	}
	if isWindowsTerminalSession() {
		return expectedModifier == modCtrl
	}
	return expectedModifier == 0
}

// rawCtrlChar returns the control character for a key (code & 0x1f), or "" if
// the key has no control form.
func rawCtrlChar(key string) (string, bool) {
	if len(key) == 0 {
		return "", false
	}
	c := key[0]
	switch {
	case (c >= 'a' && c <= 'z') || c == '[' || c == '\\' || c == ']' || c == '_':
		return string(rune(c) & 0x1f), true
	case c == '-':
		return string(rune(31)), true // same physical key as Ctrl+_
	}
	return "", false
}

func isDigitKey(key string) bool { return len(key) == 1 && key[0] >= '0' && key[0] <= '9' }

// ---------------------------------------------------------------------------
// Key identifier formatting / parsing
// ---------------------------------------------------------------------------

func formatKeyNameWithModifiers(keyName string, modifier int) (string, bool) {
	effective := modifier & ^lockMask
	supported := modShift | modCtrl | modAlt | modSuper
	if effective&^supported != 0 {
		return "", false
	}
	var mods []string
	if effective&modShift != 0 {
		mods = append(mods, "shift")
	}
	if effective&modCtrl != 0 {
		mods = append(mods, "ctrl")
	}
	if effective&modAlt != 0 {
		mods = append(mods, "alt")
	}
	if effective&modSuper != 0 {
		mods = append(mods, "super")
	}
	if len(mods) == 0 {
		return keyName, true
	}
	return strings.Join(mods, "+") + "+" + keyName, true
}

type parsedKeyID struct {
	key                    string
	ctrl, shift, alt, supr bool
}

func parseKeyId(keyID KeyID) (parsedKeyID, bool) {
	parts := strings.Split(strings.ToLower(string(keyID)), "+")
	key := parts[len(parts)-1]
	if key == "" {
		return parsedKeyID{}, false
	}
	out := parsedKeyID{key: key}
	for _, p := range parts {
		switch p {
		case "ctrl":
			out.ctrl = true
		case "shift":
			out.shift = true
		case "alt":
			out.alt = true
		case "super":
			out.supr = true
		}
	}
	return out, true
}

func matchesLegacySequence(data string, seqs []string) bool {
	for _, s := range seqs {
		if data == s {
			return true
		}
	}
	return false
}

func matchesLegacyModifierSequence(data, key string, modifier int) bool {
	if modifier == modShift {
		return matchesLegacySequence(data, legacyShiftSequences[key])
	}
	if modifier == modCtrl {
		return matchesLegacySequence(data, legacyCtrlSequences[key])
	}
	return false
}

// ---------------------------------------------------------------------------
// MatchesKey — the big switch
// ---------------------------------------------------------------------------

// MatchesKey reports whether input data matches a key identifier.
func MatchesKey(data string, keyID KeyID) bool {
	p, ok := parseKeyId(keyID)
	if !ok {
		return false
	}
	key, ctrl, shift, alt, supr := p.key, p.ctrl, p.shift, p.alt, p.supr
	modifier := 0
	if shift {
		modifier |= modShift
	}
	if alt {
		modifier |= modAlt
	}
	if ctrl {
		modifier |= modCtrl
	}
	if supr {
		modifier |= modSuper
	}

	switch key {
	case "escape", "esc":
		if modifier != 0 {
			return false
		}
		return data == "\x1b" ||
			matchesKittySequence(data, cpEscape, 0) ||
			matchesModifyOtherKeys(data, cpEscape, 0)

	case "space":
		if !kittyProtocolActive.Load() {
			if modifier == modCtrl && data == "\x00" {
				return true
			}
			if modifier == modAlt && data == "\x1b " {
				return true
			}
		}
		if modifier == 0 {
			return data == " " ||
				matchesKittySequence(data, cpSpace, 0) ||
				matchesModifyOtherKeys(data, cpSpace, 0)
		}
		return matchesKittySequence(data, cpSpace, modifier) ||
			matchesModifyOtherKeys(data, cpSpace, modifier)

	case "tab":
		if modifier == modShift {
			return data == "\x1b[Z" ||
				matchesKittySequence(data, cpTab, modShift) ||
				matchesModifyOtherKeys(data, cpTab, modShift)
		}
		if modifier == 0 {
			return data == "\t" || matchesKittySequence(data, cpTab, 0)
		}
		return matchesKittySequence(data, cpTab, modifier) ||
			matchesModifyOtherKeys(data, cpTab, modifier)

	case "enter", "return":
		switch {
		case modifier == modShift:
			if matchesKittySequence(data, cpEnter, modShift) || matchesKittySequence(data, cpKpEnter, modShift) {
				return true
			}
			if matchesModifyOtherKeys(data, cpEnter, modShift) {
				return true
			}
			if kittyProtocolActive.Load() {
				return data == "\x1b\r" || data == "\n"
			}
			return false
		case modifier == modAlt:
			if matchesKittySequence(data, cpEnter, modAlt) || matchesKittySequence(data, cpKpEnter, modAlt) {
				return true
			}
			if matchesModifyOtherKeys(data, cpEnter, modAlt) {
				return true
			}
			if !kittyProtocolActive.Load() {
				return data == "\x1b\r"
			}
			return false
		case modifier == 0:
			return data == "\r" ||
				(!kittyProtocolActive.Load() && data == "\n") ||
				data == "\x1bOM" ||
				matchesKittySequence(data, cpEnter, 0) ||
				matchesKittySequence(data, cpKpEnter, 0)
		}
		return matchesKittySequence(data, cpEnter, modifier) ||
			matchesKittySequence(data, cpKpEnter, modifier) ||
			matchesModifyOtherKeys(data, cpEnter, modifier)

	case "backspace":
		switch {
		case modifier == modAlt:
			if data == "\x1b\x7f" || data == "\x1b\b" {
				return true
			}
			return matchesKittySequence(data, cpBackspace, modAlt) ||
				matchesModifyOtherKeys(data, cpBackspace, modAlt)
		case modifier == modCtrl:
			if matchesRawBackspace(data, modCtrl) {
				return true
			}
			return matchesKittySequence(data, cpBackspace, modCtrl) ||
				matchesModifyOtherKeys(data, cpBackspace, modCtrl)
		case modifier == 0:
			return matchesRawBackspace(data, 0) ||
				matchesKittySequence(data, cpBackspace, 0) ||
				matchesModifyOtherKeys(data, cpBackspace, 0)
		}
		return matchesKittySequence(data, cpBackspace, modifier) ||
			matchesModifyOtherKeys(data, cpBackspace, modifier)

	case "insert":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["insert"]) ||
				matchesKittySequence(data, fnInsert, 0)
		}
		if matchesLegacyModifierSequence(data, "insert", modifier) {
			return true
		}
		return matchesKittySequence(data, fnInsert, modifier)

	case "delete":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["delete"]) ||
				matchesKittySequence(data, fnDelete, 0)
		}
		if matchesLegacyModifierSequence(data, "delete", modifier) {
			return true
		}
		return matchesKittySequence(data, fnDelete, modifier)

	case "clear":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["clear"])
		}
		return matchesLegacyModifierSequence(data, "clear", modifier)

	case "home":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["home"]) ||
				matchesKittySequence(data, fnHome, 0)
		}
		if matchesLegacyModifierSequence(data, "home", modifier) {
			return true
		}
		return matchesKittySequence(data, fnHome, modifier)

	case "end":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["end"]) ||
				matchesKittySequence(data, fnEnd, 0)
		}
		if matchesLegacyModifierSequence(data, "end", modifier) {
			return true
		}
		return matchesKittySequence(data, fnEnd, modifier)

	case "pageup":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["pageUp"]) ||
				matchesKittySequence(data, fnPageUp, 0)
		}
		if matchesLegacyModifierSequence(data, "pageUp", modifier) {
			return true
		}
		return matchesKittySequence(data, fnPageUp, modifier)

	case "pagedown":
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["pageDown"]) ||
				matchesKittySequence(data, fnPageDown, 0)
		}
		if matchesLegacyModifierSequence(data, "pageDown", modifier) {
			return true
		}
		return matchesKittySequence(data, fnPageDown, modifier)

	case "up":
		if modifier == modAlt {
			return data == "\x1bp" || matchesKittySequence(data, arrowUp, modAlt)
		}
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["up"]) ||
				matchesKittySequence(data, arrowUp, 0)
		}
		if matchesLegacyModifierSequence(data, "up", modifier) {
			return true
		}
		return matchesKittySequence(data, arrowUp, modifier)

	case "down":
		if modifier == modAlt {
			return data == "\x1bn" || matchesKittySequence(data, arrowDown, modAlt)
		}
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["down"]) ||
				matchesKittySequence(data, arrowDown, 0)
		}
		if matchesLegacyModifierSequence(data, "down", modifier) {
			return true
		}
		return matchesKittySequence(data, arrowDown, modifier)

	case "left":
		if modifier == modAlt {
			return data == "\x1b[1;3D" ||
				(!kittyProtocolActive.Load() && data == "\x1bB") ||
				data == "\x1bb" ||
				matchesKittySequence(data, arrowLeft, modAlt)
		}
		if modifier == modCtrl {
			return data == "\x1b[1;5D" ||
				matchesLegacyModifierSequence(data, "left", modCtrl) ||
				matchesKittySequence(data, arrowLeft, modCtrl)
		}
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["left"]) ||
				matchesKittySequence(data, arrowLeft, 0)
		}
		if matchesLegacyModifierSequence(data, "left", modifier) {
			return true
		}
		return matchesKittySequence(data, arrowLeft, modifier)

	case "right":
		if modifier == modAlt {
			return data == "\x1b[1;3C" ||
				(!kittyProtocolActive.Load() && data == "\x1bF") ||
				data == "\x1bf" ||
				matchesKittySequence(data, arrowRight, modAlt)
		}
		if modifier == modCtrl {
			return data == "\x1b[1;5C" ||
				matchesLegacyModifierSequence(data, "right", modCtrl) ||
				matchesKittySequence(data, arrowRight, modCtrl)
		}
		if modifier == 0 {
			return matchesLegacySequence(data, legacyKeySequences["right"]) ||
				matchesKittySequence(data, arrowRight, 0)
		}
		if matchesLegacyModifierSequence(data, "right", modifier) {
			return true
		}
		return matchesKittySequence(data, arrowRight, modifier)

	case "f1", "f2", "f3", "f4", "f5", "f6", "f7", "f8", "f9", "f10", "f11", "f12":
		if modifier != 0 {
			return false
		}
		return matchesLegacySequence(data, legacyKeySequences[key])
	}

	// Single letter / digit / symbol keys.
	if len(key) == 1 {
		r := rune(key[0])
		isLetter := r >= 'a' && r <= 'z'
		isDigit := isDigitKey(key)
		isSymbol := symbolKeys[r]
		if isLetter || isDigit || isSymbol {
			codepoint := int(r)
			rawCtrl, hasCtrl := rawCtrlChar(key)

			if modifier == modCtrl+modAlt && !kittyProtocolActive.Load() && hasCtrl {
				if data == "\x1b"+rawCtrl {
					return true
				}
			}
			if modifier == modAlt && !kittyProtocolActive.Load() && (isLetter || isDigit) {
				if data == "\x1b"+key {
					return true
				}
			}
			switch {
			case modifier == modCtrl:
				if hasCtrl && data == rawCtrl {
					return true
				}
				return matchesKittySequence(data, codepoint, modCtrl) ||
					matchesPrintableModifyOtherKeys(data, codepoint, modCtrl)
			case modifier == modShift+modCtrl:
				return matchesKittySequence(data, codepoint, modShift+modCtrl) ||
					matchesPrintableModifyOtherKeys(data, codepoint, modShift+modCtrl)
			case modifier == modShift:
				if isLetter && data == strings.ToUpper(key) {
					return true
				}
				return matchesKittySequence(data, codepoint, modShift) ||
					matchesPrintableModifyOtherKeys(data, codepoint, modShift)
			case modifier != 0:
				return matchesKittySequence(data, codepoint, modifier) ||
					matchesPrintableModifyOtherKeys(data, codepoint, modifier)
			default:
				return data == key || matchesKittySequence(data, codepoint, 0)
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// ParseKey
// ---------------------------------------------------------------------------

func formatParsedKey(codepoint, modifier int, baseLayoutKey *int) (string, bool) {
	normalized := normalizeKittyFunctionalCodepoint(codepoint)
	identity := normalizeShiftedLetterIdentityCodepoint(normalized, modifier)

	isLatinLetter := identity >= 97 && identity <= 122
	isDigit := identity >= 48 && identity <= 57
	isKnownSymbol := symbolKeys[rune(identity)]
	effective := identity
	if !(isLatinLetter || isDigit || isKnownSymbol) && baseLayoutKey != nil {
		effective = *baseLayoutKey
	}

	var keyName string
	switch {
	case effective == cpEscape:
		keyName = "escape"
	case effective == cpTab:
		keyName = "tab"
	case effective == cpEnter || effective == cpKpEnter:
		keyName = "enter"
	case effective == cpSpace:
		keyName = "space"
	case effective == cpBackspace:
		keyName = "backspace"
	case effective == fnDelete:
		keyName = "delete"
	case effective == fnInsert:
		keyName = "insert"
	case effective == fnHome:
		keyName = "home"
	case effective == fnEnd:
		keyName = "end"
	case effective == fnPageUp:
		keyName = "pageUp"
	case effective == fnPageDown:
		keyName = "pageDown"
	case effective == arrowUp:
		keyName = "up"
	case effective == arrowDown:
		keyName = "down"
	case effective == arrowLeft:
		keyName = "left"
	case effective == arrowRight:
		keyName = "right"
	case effective >= 48 && effective <= 57:
		keyName = string(rune(effective))
	case effective >= 97 && effective <= 122:
		keyName = string(rune(effective))
	case symbolKeys[rune(effective)]:
		keyName = string(rune(effective))
	}
	if keyName == "" {
		return "", false
	}
	return formatKeyNameWithModifiers(keyName, modifier)
}

// ParseKey parses input data into a key identifier.
func ParseKey(data string) (KeyID, bool) {
	if p, ok := parseKittySequence(data); ok {
		s, found := formatParsedKey(p.codepoint, p.modifier, p.baseLayoutKey)
		return KeyID(s), found
	}
	if p, ok := parseModifyOtherKeysSequence(data); ok {
		s, found := formatParsedKey(p.codepoint, p.modifier, nil)
		return KeyID(s), found
	}

	if kittyProtocolActive.Load() {
		if data == "\x1b\r" || data == "\n" {
			return "shift+enter", true
		}
	}
	if id, ok := legacySequenceKeyIDs[data]; ok {
		return id, true
	}

	switch {
	case data == "\x1b":
		return "escape", true
	case data == "\x1c":
		return `ctrl+\`, true
	case data == "\x1d":
		return "ctrl+]", true
	case data == "\x1f":
		return "ctrl+-", true
	case data == "\x1b\x1b":
		return "ctrl+alt+[", true
	case data == "\x1b\x1c":
		return `ctrl+alt+\`, true
	case data == "\x1b\x1d":
		return "ctrl+alt+]", true
	case data == "\x1b\x1f":
		return "ctrl+alt+-", true
	case data == "\t":
		return "tab", true
	case data == "\r" || (!kittyProtocolActive.Load() && data == "\n") || data == "\x1bOM":
		return "enter", true
	case data == "\x00":
		return "ctrl+space", true
	case data == " ":
		return "space", true
	case data == "\x7f":
		return "backspace", true
	case data == "\x08":
		if isWindowsTerminalSession() {
			return "ctrl+backspace", true
		}
		return "backspace", true
	case data == "\x1b[Z":
		return "shift+tab", true
	case !kittyProtocolActive.Load() && data == "\x1b\r":
		return "alt+enter", true
	case !kittyProtocolActive.Load() && data == "\x1b ":
		return "alt+space", true
	case data == "\x1b\x7f" || data == "\x1b\b":
		return "alt+backspace", true
	}
	if !kittyProtocolActive.Load() && data == "\x1bB" {
		return "alt+left", true
	}
	if !kittyProtocolActive.Load() && data == "\x1bF" {
		return "alt+right", true
	}
	if !kittyProtocolActive.Load() && len(data) == 2 && data[0] == '\x1b' {
		code := int(data[1])
		if code >= 1 && code <= 26 {
			return KeyID("ctrl+alt+" + string(rune(code+96))), true
		}
		if (code >= 97 && code <= 122) || (code >= 48 && code <= 57) {
			return KeyID("alt+" + string(rune(code))), true
		}
	}
	switch data {
	case "\x1b[A":
		return "up", true
	case "\x1b[B":
		return "down", true
	case "\x1b[C":
		return "right", true
	case "\x1b[D":
		return "left", true
	case "\x1b[H", "\x1bOH":
		return "home", true
	case "\x1b[F", "\x1bOF":
		return "end", true
	case "\x1b[3~":
		return "delete", true
	case "\x1b[5~":
		return "pageUp", true
	case "\x1b[6~":
		return "pageDown", true
	}

	// Raw Ctrl+letter.
	if len(data) == 1 {
		code := int(data[0])
		if code >= 1 && code <= 26 {
			return KeyID("ctrl+" + string(rune(code+96))), true
		}
		if code >= 32 && code <= 126 {
			return KeyID(data), true
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Kitty CSI-u / modifyOtherKeys printable decoding
// ---------------------------------------------------------------------------

// DecodeKittyPrintable decodes a Kitty CSI-u sequence into a printable char.
func DecodeKittyPrintable(data string) (string, bool) {
	m := kittyCsiURe.FindStringSubmatch(data)
	if m == nil {
		return "", false
	}
	codepoint, err := strconv.Atoi(m[1])
	if err != nil {
		return "", false
	}
	var shifted *int
	if len(m[2]) > 0 {
		v, _ := strconv.Atoi(m[2])
		shifted = &v
	}
	modValue := 1
	if len(m[4]) > 0 {
		modValue, _ = strconv.Atoi(m[4])
	}
	modifier := modValue - 1

	if modifier&^kittyPrintableAllowedModifiers != 0 {
		return "", false
	}
	if modifier&(modAlt|modCtrl) != 0 {
		return "", false
	}
	effective := codepoint
	if modifier&modShift != 0 && shifted != nil {
		effective = *shifted
	}
	effective = normalizeKittyFunctionalCodepoint(effective)
	if effective < 32 {
		return "", false
	}
	return string(rune(effective)), true
}

func decodeModifyOtherKeysPrintable(data string) (string, bool) {
	p, ok := parseModifyOtherKeysSequence(data)
	if !ok {
		return "", false
	}
	modifier := p.modifier & ^lockMask
	if modifier&^modShift != 0 {
		return "", false
	}
	if p.codepoint < 32 {
		return "", false
	}
	return string(rune(p.codepoint)), true
}

// DecodePrintableKey decodes a printable Kitty CSI-u or modifyOtherKeys sequence.
func DecodePrintableKey(data string) (string, bool) {
	if s, ok := DecodeKittyPrintable(data); ok {
		return s, true
	}
	return decodeModifyOtherKeysPrintable(data)
}
