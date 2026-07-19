package tui

import (
	"reflect"
	"testing"
	"time"
)

func TestKittyKeyboardProtocolQuery(t *testing.T) {
	// The query pushes flags 7, pops the current set, then sends DA as a sentinel.
	if KittyKeyboardProtocolQuery != "\x1b[>7u\x1b[?u\x1b[c" {
		t.Errorf("query=%q", KittyKeyboardProtocolQuery)
	}
}

func TestParseKeyboardProtocolNegotiationSequence(t *testing.T) {
	cases := []struct {
		in   string
		typ  KittyNegotiationType
		flags int
		ok   bool
	}{
		{"\x1b[?7u", NegotiationKittyFlags, 7, true},
		{"\x1b[?0u", NegotiationKittyFlags, 0, true},
		{"\x1b[?62;4;52c", NegotiationDeviceAttributes, 0, true},
		{"a", 0, 0, false},
		{"\x1b[A", 0, 0, false},
	}
	for _, c := range cases {
		got, ok := ParseKeyboardProtocolNegotiationSequence(c.in)
		if ok != c.ok || (ok && (got.Type != c.typ || got.Flags != c.flags)) {
			t.Errorf("Parse(%q)={type:%d flags:%d ok:%v} want {type:%d flags:%d ok:%v}",
				c.in, got.Type, got.Flags, ok, c.typ, c.flags, c.ok)
		}
	}
}

func TestNormalizeAppleTerminalInput(t *testing.T) {
	cases := []struct {
		data               string
		isApple, isShift   bool
		want               string
	}{
		{"\r", true, true, "\x1b[13;2u"},
		{"\r", true, false, "\r"},
		{"\r", false, true, "\r"},
		{"\x1b[13;2u", true, true, "\x1b[13;2u"}, // non-Return unchanged
		{"a", true, true, "a"},
	}
	for _, c := range cases {
		if got := NormalizeAppleTerminalInput(c.data, c.isApple, c.isShift); got != c.want {
			t.Errorf("NormalizeAppleTerminalInput(%q,%v,%v)=%q want %q", c.data, c.isApple, c.isShift, got, c.want)
		}
	}
}

// --- kittyNegotiator ---

type negotRecorder struct {
	writes     []string
	forwards   []string
	kittySets  []bool
}

func newNegot(r *negotRecorder) *kittyNegotiator {
	n := newKittyNegotiator(
		func(s string) { r.writes = append(r.writes, s) },
		func(s string) { r.forwards = append(r.forwards, s) },
		func(b bool) { r.kittySets = append(r.kittySets, b) },
	)
	n.fragTimeout = time.Hour // prevent the real fragment timer from firing mid-test
	return n
}

func TestNegotNonZeroKittyFlags(t *testing.T) {
	r := &negotRecorder{}
	n := newNegot(r)
	n.feed("\x1b[?7u")
	if !n.kittyActive || len(r.kittySets) != 1 || !r.kittySets[0] {
		t.Errorf("kitty should activate: active=%v sets=%v", n.kittyActive, r.kittySets)
	}
	if len(r.writes) != 0 {
		t.Errorf("no modifyOtherKeys expected, writes=%v", r.writes)
	}
}

func TestNegotZeroKittyFlagsFallback(t *testing.T) {
	r := &negotRecorder{}
	n := newNegot(r)
	n.feed("\x1b[?0u")
	if n.kittyActive {
		t.Error("kitty should not activate for zero flags")
	}
	if !reflect.DeepEqual(r.writes, []string{enableModifyOtherKeysSeq}) {
		t.Errorf("writes=%v", r.writes)
	}
}

func TestNegotDeviceAttributesFallback(t *testing.T) {
	r := &negotRecorder{}
	n := newNegot(r)
	n.feed("\x1b[?62;4;52c")
	if !reflect.DeepEqual(r.writes, []string{enableModifyOtherKeysSeq}) {
		t.Errorf("writes=%v", r.writes)
	}
}

func TestNegotForwardsNormalInput(t *testing.T) {
	r := &negotRecorder{}
	n := newNegot(r)
	n.feed("a")
	if !reflect.DeepEqual(r.forwards, []string{"a"}) || n.kittyActive {
		t.Errorf("forwards=%v kitty=%v", r.forwards, n.kittyActive)
	}
}

func TestNegotSplitKittyConfirmation(t *testing.T) {
	r := &negotRecorder{}
	n := newNegot(r)
	n.feed("\x1b[?7") // prefix → buffer, pending
	if len(r.forwards) != 0 || n.kittyActive {
		t.Errorf("after first fragment: forwards=%v kitty=%v", r.forwards, n.kittyActive)
	}
	n.feed("u") // completes "\x1b[?7u"
	if !n.kittyActive || len(r.forwards) != 0 {
		t.Errorf("after completion: kitty=%v forwards=%v", n.kittyActive, r.forwards)
	}
}

func TestNegotReplayBufferedCSIPrefix(t *testing.T) {
	r := &negotRecorder{}
	n := newNegot(r)
	n.feed("\x1b[") // prefix → buffer, pending
	if len(r.forwards) != 0 {
		t.Errorf("should buffer, forwards=%v", r.forwards)
	}
	n.onFragmentTimeout() // simulate the 150ms fragment timer firing
	if !reflect.DeepEqual(r.forwards, []string{"\x1b["}) {
		t.Errorf("replay=%v", r.forwards)
	}
}

func TestNegotKittyDisablesModOtherKeys(t *testing.T) {
	r := &negotRecorder{}
	n := newNegot(r)
	n.feed("\x1b[?0u") // enable modOtherKeys
	n.feed("\x1b[?7u") // kitty active → disable modOtherKeys
	want := []string{enableModifyOtherKeysSeq, disableModifyOtherKeysSeq}
	if !reflect.DeepEqual(r.writes, want) {
		t.Errorf("writes=%v want %v", r.writes, want)
	}
	if !n.kittyActive {
		t.Error("kitty should be active")
	}
}

func TestNegotFlushBufferThenForward(t *testing.T) {
	// buffer "\x1b[?" is a prefix; "x" makes combined "\x1b[?x" neither parse nor
	// prefix → flush buffer as input, then forward "x".
	r := &negotRecorder{}
	n := newNegot(r)
	n.feed("\x1b[?")
	n.feed("x")
	if !reflect.DeepEqual(r.forwards, []string{"\x1b[?", "x"}) {
		t.Errorf("forwards=%v", r.forwards)
	}
}
