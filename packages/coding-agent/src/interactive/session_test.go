package interactive

// session_test.go covers the session selector's pure display helpers. The switch
// itself (AgentSessionRuntime.SwitchSession) + transcript rebuild need a live
// runtime + session file, exercised end-to-end with a configured provider.

import (
	"strings"
	"testing"
	"time"

	llm "ycode/packages/llm/src"
)

func TestUserMessageText(t *testing.T) {
	msg := llm.UserMessage{Content: []llm.ContentBlock{
		llm.TextContent{Text: "hello"},
		llm.TextContent{Text: "world"},
	}}
	if got := userMessageText(msg); got != "hello\nworld" {
		t.Errorf("userMessageText = %q, want hello\\nworld", got)
	}
}

func TestTruncateSessionLabel(t *testing.T) {
	if got := truncateSessionLabel(""); got != "(no messages)" {
		t.Errorf("empty label = %q, want (no messages)", got)
	}
	if got := truncateSessionLabel("short"); got != "short" {
		t.Errorf("short label = %q, want short", got)
	}
	got := truncateSessionLabel(strings.Repeat("a", 50))
	if !strings.HasSuffix(got, "...") {
		t.Errorf("long label not truncated: %q", got)
	}
	if len([]rune(got)) != 40 {
		t.Errorf("truncated label len = %d, want 40", len([]rune(got)))
	}
}

func TestRelativeTime(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{2 * 24 * time.Hour, "2d ago"},
	}
	for _, c := range cases {
		if got := relativeTime(time.Now().Add(-c.d)); got != c.want {
			t.Errorf("relativeTime(-%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
