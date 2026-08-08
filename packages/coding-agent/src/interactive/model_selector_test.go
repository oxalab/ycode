package interactive

// model_selector_test.go exercises the model selector's pure logic (sort,
// render, nav, confirm) without a full *core.ModelRegistry — fields are set
// directly (same package). The fuzzy filter itself is covered by the pi-tui
// package; here we verify our sort order, row rendering, and input routing.

import (
	"strings"
	"testing"

	"ycode/packages/tui"
	"ycode/packages/coding-agent/src/core/tui/theme"
	llm "ycode/packages/llm/src"
)

func TestModelSelectorSortCurrentFirst(t *testing.T) {
	ensureTheme()
	th := theme.GetTheme()
	m := &ModelSelector{th: th, current: llm.Model[llm.Api]{Provider: "openai", ID: "gpt-4"}, haveCurrent: true}
	in := []modelItem{
		{provider: "google", id: "gemini"},
		{provider: "anthropic", id: "claude"},
		{provider: "openai", id: "gpt-4"},
	}
	out := m.sortItems(in)
	if out[0].id != "gpt-4" {
		t.Errorf("current model not first: %+v", out[0])
	}
	// Remainder sorted by provider: anthropic < google.
	if out[1].provider != "anthropic" || out[2].provider != "google" {
		t.Errorf("provider order wrong: %s, %s", out[1].provider, out[2].provider)
	}
}

func TestModelSelectorRenderBadgesAndCurrent(t *testing.T) {
	ensureTheme()
	th := theme.GetTheme()
	m := &ModelSelector{
		th:         th,
		current:    llm.Model[llm.Api]{Provider: "openai", ID: "gpt-4"},
		haveCurrent: true,
	}
	m.listBox = &tui.Container{}
	m.active = []modelItem{
		{provider: "openai", id: "gpt-4", name: "GPT-4"},
		{provider: "anthropic", id: "claude", name: "Claude"},
	}
	m.all = m.active
	m.applyFilter("")

	joined := strings.Join(m.listBox.Render(80), "\n")
	if !strings.Contains(joined, "✓") {
		t.Errorf("current model not marked ✓:\n%s", joined)
	}
	if !strings.Contains(joined, "→ gpt-4") {
		t.Errorf("selected row missing → prefix:\n%s", joined)
	}
	if !strings.Contains(joined, "[openai]") {
		t.Errorf("provider badge missing:\n%s", joined)
	}
	if !strings.Contains(joined, "Model Name: GPT-4") {
		t.Errorf("model-name footer missing:\n%s", joined)
	}
}

func TestModelSelectorNavAndConfirm(t *testing.T) {
	ensureTheme()
	installAppKeybindings() // registers tui.select.* globally
	th := theme.GetTheme()

	var picked string
	m := &ModelSelector{
		th:      th,
		onSelect: func(model llm.Model[llm.Api]) { picked = model.ID },
	}
	m.listBox = &tui.Container{}
	m.active = []modelItem{
		{provider: "a", id: "m1", model: llm.Model[llm.Api]{Provider: "a", ID: "m1"}},
		{provider: "b", id: "m2", model: llm.Model[llm.Api]{Provider: "b", ID: "m2"}},
		{provider: "c", id: "m3", model: llm.Model[llm.Api]{Provider: "c", ID: "m3"}},
	}
	m.all = m.active
	m.applyFilter("")

	const down, enter = "\x1b[B", "\r" // raw terminal key data (arrow-down, enter/CR)
	m.HandleInput(down)
	m.HandleInput(down)
	if m.selected != 2 {
		t.Errorf("after 2×down, selected=%d want 2", m.selected)
	}
	// Wrap from bottom back to top.
	m.HandleInput(down)
	if m.selected != 0 {
		t.Errorf("after wrap, selected=%d want 0", m.selected)
	}
	// Confirm (enter) selects the highlighted model via onSelect. Go back to m3.
	m.HandleInput(down)
	m.HandleInput(down)
	m.HandleInput(enter)
	if picked != "m3" {
		t.Errorf("confirm picked %q, want m3", picked)
	}
}
