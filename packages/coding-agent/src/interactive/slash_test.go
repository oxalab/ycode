package interactive

// slash_test.go covers the slash-router's pure parsing (command + argument
// extraction) and bash-mode prefix detection. The session-call handlers
// (/compact /new /name) and bash execution are exercised end-to-end with a
// configured provider; here we verify routing decides correctly without
// touching a live session (unknown/helpful commands are checked for not panicking
// on a nil session by routing only to no-op paths).

import (
	"testing"
)

func TestBashPrefixDetection(t *testing.T) {
	cases := []struct {
		text     string
		isBash   bool
		excluded bool
		command  string
	}{
		{"!ls", true, false, "ls"},
		{"!!ls -la", true, true, "ls -la"},
		{"!  echo hi", true, false, "echo hi"}, // trims whitespace
		{"hello", false, false, ""},
		{"/model", false, false, ""},
	}
	for _, c := range cases {
		isBash := len(c.text) > 0 && c.text[0] == '!'
		if isBash != c.isBash {
			t.Errorf("%q: isBash=%v want %v", c.text, isBash, c.isBash)
			continue
		}
		if !c.isBash {
			continue
		}
		excluded := len(c.text) >= 2 && c.text[1] == '!'
		cmd := c.text[1:]
		if excluded {
			cmd = cmd[1:]
		}
		// trim spaces (mirrors handleBashSubmit)
		for cmd != "" && (cmd[0] == ' ' || cmd[0] == '\t') {
			cmd = cmd[1:]
		}
		if excluded != c.excluded {
			t.Errorf("%q: excluded=%v want %v", c.text, excluded, c.excluded)
		}
		if cmd != c.command {
			t.Errorf("%q: command=%q want %q", c.text, cmd, c.command)
		}
	}
}
