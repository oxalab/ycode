package tools

// schema_test.go verifies that every builtin tool definition carries a
// non-empty Parameters JSON Schema with the expected properties + descriptions
// (matching the TS reference). This guards against the previous regression
// where Parameters was nil for all builtins and the LLM received empty
// input_schemas.

import (
	"encoding/json"
	"strings"
	"testing"

	"ycode/packages/coding-agent/src/core/extensions"
)

// allBuiltinDefinitions builds every builtin tool definition the way the
// runtime does (CreateAllToolDefinitions), returning name→definition.
func allBuiltinDefinitions(t *testing.T) map[ToolName]*extensions.ToolDefinition {
	t.Helper()
	defs := CreateAllToolDefinitions("", nil)
	if len(defs) != 7 {
		t.Fatalf("expected 7 builtin tools, got %d", len(defs))
	}
	return defs
}

// parseSchema unmarshals the Parameters RawMessage into a map.
func parseSchema(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("Parameters is empty")
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("Parameters is not valid JSON: %v", err)
	}
	return m
}

// requireProp asserts a property exists with the given type + a non-empty
// description, returning the property map.
func requireProp(t *testing.T, props map[string]any, name, typ string) map[string]any {
	t.Helper()
	p, ok := props[name].(map[string]any)
	if !ok {
		t.Errorf("property %q missing or not an object", name)
		return nil
	}
	if pt, _ := p["type"].(string); pt != typ {
		t.Errorf("property %q: type=%q want %q", name, pt, typ)
	}
	if d, _ := p["description"].(string); d == "" {
		t.Errorf("property %q: description is empty", name)
	}
	return p
}

// TestAllToolsHaveParameterSchemas ensures no builtin ships with an empty
// Parameters (the regression this file guards against).
func TestAllToolsHaveParameterSchemas(t *testing.T) {
	defs := allBuiltinDefinitions(t)
	for name, def := range defs {
		if len(def.Parameters) == 0 {
			t.Errorf("tool %q has empty Parameters — LLM would receive no input_schema", name)
			continue
		}
		schema := parseSchema(t, def.Parameters)
		if schema["type"] != "object" {
			t.Errorf("tool %q: schema type=%v want object", name, schema["type"])
		}
		props, _ := schema["properties"].(map[string]any)
		if len(props) == 0 {
			t.Errorf("tool %q: schema has no properties", name)
		}
		// Every property must carry a description.
		for propName, pv := range props {
			pm, ok := pv.(map[string]any)
			if !ok {
				t.Errorf("tool %q: property %q not an object", name, propName)
				continue
			}
			if d, _ := pm["description"].(string); d == "" {
				t.Errorf("tool %q: property %q has empty description", name, propName)
			}
		}
	}
}

// TestBashSchema checks the bash schema fields against the reference.
func TestBashSchema(t *testing.T) {
	def := CreateBashToolDefinition("", nil)
	schema := parseSchema(t, def.Parameters)
	props, _ := schema["properties"].(map[string]any)
	requireProp(t, props, "command", "string")
	requireProp(t, props, "timeout", "number")
	// command is required, timeout is optional.
	required, _ := schema["required"].([]any)
	if !contains(required, "command") {
		t.Errorf("bash: command should be required, got %v", required)
	}
	if contains(required, "timeout") {
		t.Errorf("bash: timeout should be optional, got required=%v", required)
	}
}

// TestReadSchema checks the read schema (and confirms the offset tag fix).
func TestReadSchema(t *testing.T) {
	def := CreateReadToolDefinition("", nil)
	schema := parseSchema(t, def.Parameters)
	props, _ := schema["properties"].(map[string]any)
	requireProp(t, props, "path", "string")
	requireProp(t, props, "offset", "integer")
	requireProp(t, props, "limit", "integer")
}

// TestEditSchema verifies the nested edits[] array schema.
func TestEditSchema(t *testing.T) {
	def := CreateEditToolDefinition("", nil)
	schema := parseSchema(t, def.Parameters)
	props, _ := schema["properties"].(map[string]any)
	requireProp(t, props, "path", "string")
	editsProp := requireProp(t, props, "edits", "array")
	items, ok := editsProp["items"].(map[string]any)
	if !ok {
		t.Fatalf("edit: edits[] has no items schema")
	}
	itemProps, _ := items["properties"].(map[string]any)
	requireProp(t, itemProps, "oldText", "string")
	requireProp(t, itemProps, "newText", "string")
}

// TestGrepSchema confirms the context parameter is present (guards the
// "content"→"context" JSON-tag fix).
func TestGrepSchema(t *testing.T) {
	def := CreateGrepToolDefinition("", nil)
	schema := parseSchema(t, def.Parameters)
	props, _ := schema["properties"].(map[string]any)
	requireProp(t, props, "pattern", "string")
	requireProp(t, props, "context", "integer") // was buggy "content"
	requireProp(t, props, "ignoreCase", "boolean")
	requireProp(t, props, "literal", "boolean")
}

// TestGrepContextJsonTag confirms the struct tag is now "context" not
// "content" (the bug that made input.Context never populate).
func TestGrepContextJsonTag(t *testing.T) {
	raw := `{"pattern":"foo","context":3}`
	var input GrepToolInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if input.Context == nil || *input.Context != 3 {
		t.Errorf("input.Context = %v, want 3 (json tag was 'content' before fix)", input.Context)
	}
}

// TestReadOffsetJsonTag confirms the struct tag is now "offset" not "offest".
func TestReadOffsetJsonTag(t *testing.T) {
	raw := `{"path":"x","offset":5}`
	var input ReadToolInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if input.Offset == nil || *input.Offset != 5 {
		t.Errorf("input.Offset = %v, want 5 (json tag was 'offest' before fix)", input.Offset)
	}
}

// TestDescriptionsMatchReference spot-checks that key descriptions are
// verbatim from the TS reference (not paraphrased).
func TestDescriptionsMatchReference(t *testing.T) {
	cases := []struct {
		tool    string
		prop    string
		wantSub string // a distinctive substring from the reference text
	}{
		{"bash", "command", "Bash command to execute"},
		{"read", "path", "Path to the file to read"},
		{"edit", "edits", "matched against the original file, not incrementally"},
		{"grep", "glob", "*.ts"},
		{"find", "pattern", "src/**/*.spec.ts"},
		{"ls", "limit", "500"},
	}
	defs := allBuiltinDefinitions(t)
	for _, c := range cases {
		var def *extensions.ToolDefinition
		for _, d := range defs {
			if d.Name == c.tool {
				def = d
				break
			}
		}
		if def == nil {
			t.Errorf("tool %q not found", c.tool)
			continue
		}
		schema := parseSchema(t, def.Parameters)
		props, _ := schema["properties"].(map[string]any)
		p, ok := props[c.prop].(map[string]any)
		if !ok {
			t.Errorf("tool %q: property %q missing", c.tool, c.prop)
			continue
		}
		desc, _ := p["description"].(string)
		if !strings.Contains(desc, c.wantSub) {
			t.Errorf("tool %q %q: description %q does not contain %q", c.tool, c.prop, desc, c.wantSub)
		}
	}
}

func contains(s []any, v string) bool {
	for _, x := range s {
		if xs, ok := x.(string); ok && xs == v {
			return true
		}
	}
	return false
}
