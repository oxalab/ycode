package tui

import "testing"

func withKitty(t *testing.T, active bool, fn func()) {
	t.Helper()
	prev := IsKittyProtocolActive()
	SetKittyProtocolActive(active)
	t.Cleanup(func() { SetKittyProtocolActive(prev) })
	fn()
}

func withMockEnv(t *testing.T, env map[string]string, fn func()) {
	t.Helper()
	prev := osGetenv
	osGetenv = func(key string) string {
		if v, ok := env[key]; ok {
			return v
		}
		return ""
	}
	t.Cleanup(func() { osGetenv = prev })
	fn()
}

// matchesKey assertion helper.
func mk(t *testing.T, data string, id KeyID, want bool) {
	t.Helper()
	if got := MatchesKey(data, id); got != want {
		t.Errorf("MatchesKey(%q, %q)=%v want %v", data, id, got, want)
	}
}

func pk(t *testing.T, data string, want KeyID) {
	t.Helper()
	got, ok := ParseKey(data)
	if !ok || got != want {
		t.Errorf("ParseKey(%q)=(%q,%v) want (%q,true)", data, got, ok, want)
	}
}

func pkFail(t *testing.T, data string) {
	t.Helper()
	if got, ok := ParseKey(data); ok {
		t.Errorf("ParseKey(%q)=(%q,true) want false", data, got)
	}
}

func TestMatchesKeyKittyAlternate(t *testing.T) {
	withKitty(t, true, func() {
		cases := []struct {
			data string
			id   KeyID
			want bool
		}{
			{"\x1b[1089::99;5u", "ctrl+c", true},
			{"\x1b[1074::100;5u", "ctrl+d", true},
			{"\x1b[1103::122;5u", "ctrl+z", true},
			{"\x1b[1079::112;6u", "ctrl+shift+p", true},
			{"\x1b[99;5u", "ctrl+c", true},
			{"\x1b[107;9u", "super+k", true},
			{"\x1b[13;9u", "super+enter", true},
			{"\x1b[107;13u", "ctrl+super+k", true},
			{"\x1b[107;14u", "ctrl+shift+super+k", true},
			{"\x1b[107;13u", "super+k", false},
			{"\x1b[49u", "1", true},
			{"\x1b[49;5u", "ctrl+1", true},
			{"\x1b[49;5u", "ctrl+2", false},
			{"\x1b[57400u", "1", true},
			{"\x1b[57410u", "/", true},
			{"\x1b[57417u", "left", true},
			{"\x1b[57426u", "delete", true},
			{"\x1b[99:67:99;2u", "shift+c", true},
			{"\x1b[1089::99;5:3u", "ctrl+c", true},
			{"\x1b[1089:1057:99;6:2u", "ctrl+shift+c", true},
			{"\x1b[107::118;5u", "ctrl+k", true},
			{"\x1b[107::118;5u", "ctrl+v", false},
			{"\x1b[47::91;5u", "ctrl+/", true},
			{"\x1b[47::91;5u", "ctrl+[", false},
			{"\x1b[1089::99;5u", "ctrl+d", false},
			{"\x1b[1089::99;5u", "ctrl+shift+c", false},
		}
		for _, c := range cases {
			mk(t, c.data, c.id, c.want)
		}
		// parseKey
		pk(t, "\x1b[107;9u", "super+k")
		pk(t, "\x1b[13;9u", "super+enter")
		pk(t, "\x1b[107;13u", "ctrl+super+k")
		pk(t, "\x1b[107;14u", "shift+ctrl+super+k")
		pk(t, "\x1b[49u", "1")
		pk(t, "\x1b[49;5u", "ctrl+1")
		pk(t, "\x1b[57399u", "0")
		pk(t, "\x1b[57409u", ".")
		pk(t, "\x1b[57413u", "+")
		pk(t, "\x1b[57416u", ",")
		pk(t, "\x1b[57417u", "left")
		pk(t, "\x1b[57420u", "down")
		pk(t, "\x1b[57425u", "insert")
		pk(t, "\x1b[57426u", "delete")
		pk(t, "\x1b[1089::99;5u", "ctrl+c")
		pk(t, "\x1b[107::118;5u", "ctrl+k")
		pk(t, "\x1b[47::91;5u", "ctrl+/")
		pk(t, "\x1b[99;5u", "ctrl+c")
		pk(t, "\x1b[69;2u", "shift+e")
		pkFail(t, "\x1b[99;17u")
	})
}

func TestMatchesKeyModifyOtherKeys(t *testing.T) {
	withKitty(t, false, func() {
		cases := []struct {
			data string
			id   KeyID
			want bool
		}{
			{"\x1b[27;5;99~", "ctrl+c", true},
			{"\x1b[27;5;100~", "ctrl+d", true},
			{"\x1b[27;5;122~", "ctrl+z", true},
			{"\x1b[27;5;13~", "ctrl+enter", true},
			{"\x1b[27;2;13~", "shift+enter", true},
			{"\x1b[27;3;13~", "alt+enter", true},
			{"\x1b[27;2;9~", "shift+tab", true},
			{"\x1b[27;5;9~", "ctrl+tab", true},
			{"\x1b[27;3;9~", "alt+tab", true},
			{"\x1b[27;1;127~", "backspace", true},
			{"\x1b[27;5;127~", "ctrl+backspace", true},
			{"\x1b[27;3;127~", "alt+backspace", true},
			{"\x1b[27;1;27~", "escape", true},
			{"\x1b[27;1;32~", "space", true},
			{"\x1b[27;5;32~", "ctrl+space", true},
			{"\x1b[27;5;47~", "ctrl+/", true},
			{"\x1b[27;5;49~", "ctrl+1", true},
			{"\x1b[27;2;49~", "shift+1", true},
			{"\x1b[27;2;69~", "shift+e", true},
			{"\x1b[27;6;69~", "ctrl+shift+e", true},
			{"\x1b[104;7u", "ctrl+alt+h", true},
			{"\x1b[27;7;104~", "ctrl+alt+h", true},
		}
		for _, c := range cases {
			mk(t, c.data, c.id, c.want)
		}
		pk(t, "\x1b[27;5;99~", "ctrl+c")
		pk(t, "\x1b[27;2;13~", "shift+enter")
		pk(t, "\x1b[27;3;13~", "alt+enter")
		pk(t, "\x1b[27;5;13~", "ctrl+enter")
		pk(t, "\x1b[27;2;9~", "shift+tab")
		pk(t, "\x1b[27;5;9~", "ctrl+tab")
		pk(t, "\x1b[27;3;9~", "alt+tab")
		pk(t, "\x1b[27;1;127~", "backspace")
		pk(t, "\x1b[27;5;127~", "ctrl+backspace")
		pk(t, "\x1b[27;1;27~", "escape")
		pk(t, "\x1b[27;1;32~", "space")
		pk(t, "\x1b[27;5;32~", "ctrl+space")
		pk(t, "\x1b[27;5;47~", "ctrl+/")
		pk(t, "\x1b[27;5;49~", "ctrl+1")
		pk(t, "\x1b[27;2;69~", "shift+e")
		pk(t, "\x1b[27;6;69~", "shift+ctrl+e")
		pk(t, "\x1b[104;7u", "ctrl+alt+h")
		pk(t, "\x1b[27;7;104~", "ctrl+alt+h")
	})
}

func TestMatchesKeyLegacy(t *testing.T) {
	withKitty(t, false, func() {
		cases := []struct {
			data string
			id   KeyID
			want bool
		}{
			{"\x03", "ctrl+c", true},
			{"\x04", "ctrl+d", true},
			{"\x1b", "escape", true},
			{"\n", "enter", true},
			{"\x00", "ctrl+space", true},
			{"\x1c", `ctrl+\`, true},
			{"\x1d", "ctrl+]", true},
			{"\x1f", "ctrl+_", true},
			{"\x1f", "ctrl+-", true},
			{"\x1b\x1b", "ctrl+alt+[", true},
			{"\x1b\x1c", `ctrl+alt+\`, true},
			{"\x1b\x1d", "ctrl+alt+]", true},
			{"\x1b\x1f", "ctrl+alt+_", true},
			{"\x1b\x1f", "ctrl+alt+-", true},
			{"\x1b[A", "up", true}, {"\x1b[B", "down", true},
			{"\x1b[C", "right", true}, {"\x1b[D", "left", true},
			{"\x1bOA", "up", true}, {"\x1bOH", "home", true}, {"\x1bOF", "end", true},
			{"\x1bOP", "f1", true}, {"\x1b[24~", "f12", true}, {"\x1b[E", "clear", true},
			{"\x1bp", "alt+up", true},
			{"\x1b[a", "shift+up", true}, {"\x1bOa", "ctrl+up", true},
			{"\x1b[2$", "shift+insert", true}, {"\x1b[2^", "ctrl+insert", true},
			{"\x1b[7$", "shift+home", true},
		}
		for _, c := range cases {
			mk(t, c.data, c.id, c.want)
		}
		mk(t, "\x1bp", "up", false)

		// alt-prefixed (kitty off)
		for _, c := range []struct{ data string; id KeyID }{
			{"\x1b ", "alt+space"}, {"\x1b\b", "alt+backspace"}, {"\x1b\x03", "ctrl+alt+c"},
			{"\x1bB", "alt+left"}, {"\x1bF", "alt+right"}, {"\x1ba", "alt+a"},
			{"\x1b1", "alt+1"}, {"\x1by", "alt+y"}, {"\x1bz", "alt+z"},
		} {
			mk(t, c.data, c.id, true)
		}
	})

	// alt-prefixed are NOT matched when kitty is active.
	withKitty(t, true, func() {
		mk(t, "\x1b ", "alt+space", false)
		mk(t, "\x1ba", "alt+a", false)
		mk(t, "\x1bB", "alt+left", false)
		mk(t, "\n", "enter", false)
		mk(t, "\n", "shift+enter", true)
		pk(t, "\n", "shift+enter")
	})
}

func TestRawBackspaceWindowsTerminal(t *testing.T) {
	t.Run("outside Windows Terminal", func(t *testing.T) {
		withMockEnv(t, nil, func() {
			mk(t, "\x7f", "backspace", true)
			mk(t, "\x7f", "ctrl+backspace", false)
			mk(t, "\x08", "backspace", true)
			mk(t, "\x08", "ctrl+backspace", false)
			mk(t, "\x08", "ctrl+h", true)
			pk(t, "\x7f", "backspace")
			pk(t, "\x08", "backspace")
		})
	})
	t.Run("local Windows Terminal", func(t *testing.T) {
		withMockEnv(t, map[string]string{"WT_SESSION": "s"}, func() {
			mk(t, "\x08", "ctrl+backspace", true)
			mk(t, "\x08", "backspace", false)
			mk(t, "\x08", "ctrl+h", true)
			pk(t, "\x08", "ctrl+backspace")
		})
	})
	t.Run("Windows Terminal over SSH", func(t *testing.T) {
		withMockEnv(t, map[string]string{
			"WT_SESSION": "s", "SSH_CONNECTION": "1 2 3 4", "SSH_CLIENT": "1 2 3", "SSH_TTY": "/dev/pts/1",
		}, func() {
			mk(t, "\x08", "ctrl+backspace", false)
			mk(t, "\x08", "backspace", true)
			mk(t, "\x08", "ctrl+h", true)
			pk(t, "\x08", "backspace")
		})
	})
}

func TestDecodeKittyPrintable(t *testing.T) {
	cases := []struct {
		data string
		want string
		ok   bool
	}{
		{"\x1b[57399u", "0", true}, {"\x1b[57400u", "1", true}, {"\x1b[57409u", ".", true},
		{"\x1b[57410u", "/", true}, {"\x1b[57411u", "*", true}, {"\x1b[57412u", "-", true},
		{"\x1b[57413u", "+", true}, {"\x1b[57415u", "=", true}, {"\x1b[57416u", ",", true},
		{"\x1b[57417u", "", false},
	}
	for _, c := range cases {
		got, ok := DecodeKittyPrintable(c.data)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("DecodeKittyPrintable(%q)=(%q,%v) want (%q,%v)", c.data, got, ok, c.want, c.ok)
		}
	}
}

func TestDecodePrintableKey(t *testing.T) {
	cases := []struct {
		data string
		want string
		ok   bool
	}{
		{"\x1b[27;2;69~", "E", true},
		{"\x1b[27;2;196~", "Ä", true},
		{"\x1b[27;2;32~", " ", true},
		{"\x1b[27;2;13~", "", false},
		{"\x1b[27;6;69~", "", false},
	}
	for _, c := range cases {
		got, ok := DecodePrintableKey(c.data)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("DecodePrintableKey(%q)=(%q,%v) want (%q,%v)", c.data, got, ok, c.want, c.ok)
		}
	}
}

func TestParseKeyLegacy(t *testing.T) {
	withKitty(t, false, func() {
		pk(t, "\x03", "ctrl+c")
		pk(t, "\x04", "ctrl+d")
		pk(t, "\x1b", "escape")
		pk(t, "\t", "tab")
		pk(t, "\r", "enter")
		pk(t, "\n", "enter")
		pk(t, "\x00", "ctrl+space")
		pk(t, " ", "space")
		pk(t, "1", "1")
		pk(t, "\x1b[A", "up")
		pk(t, "\x1b[B", "down")
		pk(t, "\x1b[C", "right")
		pk(t, "\x1b[D", "left")
		pk(t, "\x1bOH", "home")
		pk(t, "\x1bOF", "end")
		pk(t, "\x1bOP", "f1")
		pk(t, "\x1b[24~", "f12")
		pk(t, "\x1b[E", "clear")
		pk(t, "\x1b[2^", "ctrl+insert")
		pk(t, "\x1bp", "alt+up")
		pk(t, "\x1b[[5~", "pageUp")
		if !MatchesKey("1", "1") {
			t.Error(`MatchesKey("1","1") should be true`)
		}
	})
}
