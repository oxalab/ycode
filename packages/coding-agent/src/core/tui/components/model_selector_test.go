package components

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "ycode/packages/coding-agent/src/core"
	"ycode/packages/coding-agent/src/core/tui/theme"
)

// newTestModelSelector builds a model selector over a registry backed by the
// given models.json content ("" skips the file). ANTHROPIC_API_KEY is set so
// built-in Anthropic models count as available.
func newTestModelSelector(t *testing.T, modelsJson string) *ModelSelectorComponent {
	t.Helper()

	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	var modelsPath string
	if modelsJson != "" {
		dir := t.TempDir()
		modelsPath = filepath.Join(dir, "models.json")
		if err := os.WriteFile(modelsPath, []byte(modelsJson), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		modelsPath = filepath.Join(t.TempDir(), "missing.json")
	}

	auth := core.NewInMemoryAuthStorage(nil)
	registry := core.NewModelRegistry(auth, modelsPath)

	theme.InitTheme("dark")
	return NewModelSelectorComponent(
		theme.GetTheme(), nil, nil, registry, nil,
		ModelSelectorCallbacks{},
	)
}

// Audit #21: the reference appends registry error lines even when the model
// list is non-empty (and then suppresses the Model Name footer); the Go port
// swallowed the load error entirely.
func TestModelSelectorShowsRegistryErrorWithModelsPresent(t *testing.T) {
	comp := newTestModelSelector(t, "{ this is not json")

	if comp.errorMessage == "" {
		t.Fatal("expected component to capture registry load error")
	}
	if comp.registryError() != comp.errorMessage {
		t.Fatalf("component error %q does not match registry error %q", comp.errorMessage, comp.registryError())
	}

	if len(comp.activeModels) == 0 {
		t.Fatal("expected built-in models to remain available despite registry error")
	}

	view := comp.View()
	// A distinctive chunk of the error message must appear in the render.
	snippet := "Failed to parse models.json"
	if !strings.Contains(view, snippet) {
		t.Fatalf("View() should show registry error %q, got:\n%s", snippet, view)
	}
	if strings.Contains(view, "Model Name:") {
		t.Fatal("View() should suppress Model Name footer while an error is shown")
	}
}

// Audit #21: with no error, a non-empty list shows the unconditional
// "Model Name: <name>" footer for the highlighted model.
func TestModelSelectorShowsModelNameFooter(t *testing.T) {
	comp := newTestModelSelector(t, "")

	if comp.errorMessage != "" {
		t.Fatalf("expected no registry error, got %q", comp.errorMessage)
	}
	if len(comp.activeModels) == 0 {
		t.Fatal("expected available models in registry")
	}

	view := comp.View()
	if !strings.Contains(view, "Model Name: ") {
		t.Fatalf("View() should include Model Name footer, got:\n%s", view)
	}

	// The footer must name the model under the cursor, not just any model.
	item, ok := comp.selector.SelectedItem()
	if !ok || item.Model == nil {
		t.Fatal("expected a selected item")
	}
	if !strings.Contains(view, "Model Name: "+item.Model.Name) {
		t.Fatalf("footer should name selected model %q, got:\n%s", item.Model.Name, view)
	}
}

// The generic Selector's footer hook must render even when the filtered list
// is empty (the TS reference shows error lines instead of "No matching
// models" in that case).
func TestSelectorFooterRendersOnEmptyList(t *testing.T) {
	theme.InitTheme("dark")
	s := NewSelector[int](
		nil, theme.GetTheme(),
		func(item int, query string) bool { return true },
		func(item int, selected bool) string { return "item" },
		nil, nil,
		SelectorOptions[int]{
			EmptyText: "No matching models",
			FooterFn: func(filtered []int, cursor int) []string {
				return []string{"footer-line"}
			},
		},
	)
	view := s.View()
	if !strings.Contains(view, "No matching models") {
		t.Fatalf("empty text missing, got:\n%s", view)
	}
	if !strings.Contains(view, "footer-line") {
		t.Fatalf("footer should render on empty list, got:\n%s", view)
	}
}
