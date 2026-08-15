package cli

import (
	"testing"

	sdk "ycode/packages/agent-sdk/src"
)

func TestParseArgs_BasicFlags(t *testing.T) {
	a := ParseArgs([]string{"--provider", "openai", "--model", "gpt-4o", "-c", "-v"})
	if a.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", a.Provider)
	}
	if a.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", a.Model)
	}
	if !a.Continue {
		t.Error("Continue = false, want true")
	}
	if !a.Version {
		t.Error("Version = false, want true")
	}
}

func TestParseArgs_ModeValidation(t *testing.T) {
	cases := map[string]struct {
		input string
		want  Mode
		set   bool
	}{
		"text": {"text", ModeText, true},
		"json": {"json", ModeJSON, true},
		"rpc":  {"rpc", ModeRPC, true},
		"bogus": {"bogus", "", false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			a := ParseArgs([]string{"--mode", c.input})
			if c.set && a.Mode != c.want {
				t.Errorf("Mode = %q, want %q", a.Mode, c.want)
			}
			if !c.set && a.Mode != "" {
				t.Errorf("Mode = %q, want unset", a.Mode)
			}
		})
	}
}

func TestParseArgs_ThinkingValidation(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		a := ParseArgs([]string{"--thinking", "high"})
		if a.Thinking != sdk.ThinkingHigh {
			t.Errorf("Thinking = %q, want high", a.Thinking)
		}
		if len(a.Diagnostics) != 0 {
			t.Errorf("Diagnostics = %v, want none", a.Diagnostics)
		}
	})
	t.Run("invalid warns", func(t *testing.T) {
		a := ParseArgs([]string{"--thinking", "ultra"})
		if a.Thinking != "" {
			t.Errorf("Thinking = %q, want unset", a.Thinking)
		}
		if len(a.Diagnostics) != 1 || a.Diagnostics[0].Type != "warning" {
			t.Fatalf("Diagnostics = %v, want one warning", a.Diagnostics)
		}
	})
	if !IsValidThinkingLevel("medium") {
		t.Error("IsValidThinkingLevel(medium) = false, want true")
	}
	if IsValidThinkingLevel("nope") {
		t.Error("IsValidThinkingLevel(nope) = true, want false")
	}
}

func TestParseArgs_CSVFlags(t *testing.T) {
	a := ParseArgs([]string{"--tools", " read , grep ,, ls", "--models", "a,b,c"})
	wantTools := []string{"read", "grep", "ls"}
	if len(a.Tools) != len(wantTools) {
		t.Fatalf("Tools = %v, want %v", a.Tools, wantTools)
	}
	for i, w := range wantTools {
		if a.Tools[i] != w {
			t.Errorf("Tools[%d] = %q, want %q", i, a.Tools[i], w)
		}
	}
	// --models keeps empty-trimmed fields (no filter), matching the reference.
	if len(a.Models) != 3 {
		t.Errorf("Models = %v, want 3 entries", a.Models)
	}
}

func TestParseArgs_Repeatable(t *testing.T) {
	a := ParseArgs([]string{
		"--append-system-prompt", "one",
		"--append-system-prompt", "two",
		"-e", "ext1", "--extension", "ext2",
	})
	if len(a.AppendSystemPrompt) != 2 || a.AppendSystemPrompt[1] != "two" {
		t.Errorf("AppendSystemPrompt = %v, want [one two]", a.AppendSystemPrompt)
	}
	if len(a.Extensions) != 2 || a.Extensions[0] != "ext1" || a.Extensions[1] != "ext2" {
		t.Errorf("Extensions = %v, want [ext1 ext2]", a.Extensions)
	}
}

func TestParseArgs_FileArgs(t *testing.T) {
	a := ParseArgs([]string{"@prompt.md", "@image.png", "hello"})
	if len(a.FileArgs) != 2 || a.FileArgs[0] != "prompt.md" || a.FileArgs[1] != "image.png" {
		t.Errorf("FileArgs = %v, want [prompt.md image.png]", a.FileArgs)
	}
	if len(a.Messages) != 1 || a.Messages[0] != "hello" {
		t.Errorf("Messages = %v, want [hello]", a.Messages)
	}
}

func TestParseArgs_PrintConsumesNext(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string // expected captured messages
	}{
		{"plain", []string{"-p", "do something"}, []string{"do something"}},
		{"file-not-consumed", []string{"-p", "@file.md"}, nil},        // @ not consumed as message
		{"flag-not-consumed", []string{"-p", "--model"}, nil},         // -- not consumed
		{"triple-dash-consumed", []string{"-p", "---sentinel"}, []string{"---sentinel"}}, // --- IS consumed
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := ParseArgs(c.args)
			if !a.Print {
				t.Fatal("Print = false, want true")
			}
			if len(a.Messages) != len(c.want) {
				t.Fatalf("Messages = %v, want %v", a.Messages, c.want)
			}
			for i, w := range c.want {
				if a.Messages[i] != w {
					t.Errorf("Messages[%d] = %q, want %q", i, a.Messages[i], w)
				}
			}
		})
	}
}

func TestParseArgs_ListModels(t *testing.T) {
	t.Run("bare", func(t *testing.T) {
		a := ParseArgs([]string{"--list-models"})
		if !a.ListModels || a.ListModelsSearch != "" {
			t.Errorf("ListModels=%v Search=%q, want true/empty", a.ListModels, a.ListModelsSearch)
		}
	})
	t.Run("with search", func(t *testing.T) {
		a := ParseArgs([]string{"--list-models", "sonnet"})
		if !a.ListModels || a.ListModelsSearch != "sonnet" {
			t.Errorf("ListModels=%v Search=%q, want true/sonnet", a.ListModels, a.ListModelsSearch)
		}
	})
	t.Run("flag not search", func(t *testing.T) {
		a := ParseArgs([]string{"--list-models", "--verbose"})
		if !a.ListModels || a.ListModelsSearch != "" {
			t.Errorf("ListModels=%v Search=%q, want true/empty (next is a flag)", a.ListModels, a.ListModelsSearch)
		}
		if !a.Verbose {
			t.Error("Verbose should still be parsed after unconsumed --list-models")
		}
	})
}

func TestParseArgs_UnknownFlags(t *testing.T) {
	t.Run("long with value", func(t *testing.T) {
		a := ParseArgs([]string{"--plan", "draft"})
		if got := a.UnknownFlags["plan"]; got != "draft" {
			t.Errorf("UnknownFlags[plan] = %v, want draft", got)
		}
	})
	t.Run("equals form", func(t *testing.T) {
		a := ParseArgs([]string{"--plan=draft"})
		if got := a.UnknownFlags["plan"]; got != "draft" {
			t.Errorf("UnknownFlags[plan] = %v, want draft", got)
		}
	})
	t.Run("bare bool", func(t *testing.T) {
		a := ParseArgs([]string{"--plan"})
		if got, ok := a.UnknownFlags["plan"]; !ok || got != true {
			t.Errorf("UnknownFlags[plan] = %v (%v), want true", got, ok)
		}
	})
	t.Run("short unknown errors", func(t *testing.T) {
		a := ParseArgs([]string{"-z"})
		if len(a.Diagnostics) != 1 || a.Diagnostics[0].Type != "error" {
			t.Fatalf("Diagnostics = %v, want one error", a.Diagnostics)
		}
	})
}

func TestPadRight(t *testing.T) {
	if got := padRight("abc", 6); got != "abc   " {
		t.Errorf("padRight = %q, want %q", got, "abc   ")
	}
	if got := padRight("abcdef", 3); got != "abcdef" {
		t.Errorf("padRight = %q, want abcdef (no truncation)", got)
	}
}
