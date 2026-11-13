package utils

import (
	"encoding/json"
	"strings"
	"testing"

	src "ycode/packages/llm/src"
)

func mustUnmarshalObject(t *testing.T, jsonText string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonText), &m); err != nil {
		t.Fatalf("bad test schema: %v\n%s", err, jsonText)
	}
	return m
}

func validateArgs(t *testing.T, schemaMap map[string]any, args map[string]any) (map[string]any, error) {
	t.Helper()
	tool := src.Tool[any]{Name: "read", Description: "read", Parameters: schemaMap}
	return ValidateToolArguments(tool, src.ToolCall{ID: "tc1", Name: "read", Arguments: args})
}

func TestValidationCoercesStringNumbers(t *testing.T) {
	s := mustUnmarshalObject(t, `{
		"type": "object",
		"properties": {
			"count": {"type": "number"},
			"index": {"type": "integer"},
			"flag":  {"type": "boolean"},
			"label": {"type": "string"}
		}
	}`)
	args := map[string]any{"count": "4.5", "index": "7", "flag": "true", "label": 3}
	got, err := validateArgs(t, s, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f, ok := got["count"].(float64); !ok || f != 4.5 {
		t.Fatalf("count = %#v, want 4.5", got["count"])
	}
	if f, ok := got["index"].(float64); !ok || f != 7 {
		t.Fatalf("index = %#v, want 7", got["index"])
	}
	if b, ok := got["flag"].(bool); !ok || !b {
		t.Fatalf("flag = %#v, want true", got["flag"])
	}
	if s2, ok := got["label"].(string); !ok || s2 != "3" {
		t.Fatalf("label = %#v, want \"3\"", got["label"])
	}
}

func TestValidationUnionCoercionAnyOf(t *testing.T) {
	s := mustUnmarshalObject(t, `{
		"type": "object",
		"properties": {
			"value": {"anyOf": [{"type": "number"}, {"type": "string"}]}
		}
	}`)
	got, err := validateArgs(t, s, map[string]any{"value": "42"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f, ok := got["value"].(float64); !ok || f != 42 {
		t.Fatalf("value = %#v, want 42 (first passing union branch wins)", got["value"])
	}
}

func TestValidationTupleItemsCoercion(t *testing.T) {
	s := mustUnmarshalObject(t, `{
		"type": "object",
		"properties": {
			"pair": {
				"type": "array",
				"items": [{"type": "number"}, {"type": "string"}]
			}
		}
	}`)
	got, err := validateArgs(t, s, map[string]any{"pair": []any{"1", 2}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	pair, ok := got["pair"].([]any)
	if !ok || len(pair) != 2 {
		t.Fatalf("pair = %#v", got["pair"])
	}
	if f, ok := pair[0].(float64); !ok || f != 1 {
		t.Fatalf("pair[0] = %#v, want 1", pair[0])
	}
	if str, ok := pair[1].(string); !ok || str != "2" {
		t.Fatalf("pair[1] = %#v, want \"2\"", pair[1])
	}
}

func TestValidationNestedObjectCoercionAndPath(t *testing.T) {
	s := mustUnmarshalObject(t, `{
		"type": "object",
		"properties": {
			"file": {
				"type": "object",
				"properties": {"line": {"type": "number"}}
			}
		}
	}`)
	got, err := validateArgs(t, s, map[string]any{"file": map[string]any{"line": "3"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	file := got["file"].(map[string]any)
	if f, ok := file["line"].(float64); !ok || f != 3 {
		t.Fatalf("nested line = %#v, want 3", file["line"])
	}

	// A nested failure reports the dotted path.
	_, err = validateArgs(t, s, map[string]any{"file": map[string]any{"line": "abc"}})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "  - file.line: must be number") {
		t.Fatalf("error missing nested path line: %v", err)
	}
}

func TestValidationRequiredMissing(t *testing.T) {
	s := mustUnmarshalObject(t, `{
		"type": "object",
		"properties": {"path": {"type": "string"}, "limit": {"type": "number"}},
		"required": ["path", "limit"]
	}`)
	_, err := validateArgs(t, s, map[string]any{})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "must have required properties path, limit") {
		t.Fatalf("required message wording diverges: %v", err)
	}
	// formatValidationPath: first required property appended to the object
	// path (root -> bare name).
	if !strings.Contains(err.Error(), "  - path: must have required properties path, limit") {
		t.Fatalf("required path formatting diverges: %v", err)
	}
}

func TestValidationExactFailureWording(t *testing.T) {
	// The reference message: header, "  - <path>: <message>" lines, blank
	// line, "Received arguments:" + original arguments (2-space JSON). An
	// object cannot coerce to a string, so this path genuinely fails.
	s := mustUnmarshalObject(t, `{
		"type": "object",
		"properties": {"path": {"type": "string"}}
	}`)
	tool := src.Tool[any]{Name: "read", Parameters: s}
	_, err := ValidateToolArguments(tool, src.ToolCall{
		ID: "tc1", Name: "read",
		Arguments: map[string]any{"path": map[string]any{"x": 1}},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	want := "Validation failed for tool \"read\":\n" +
		"  - path: must be string\n" +
		"\n" +
		"Received arguments:\n" +
		"{\n  \"path\": {\n    \"x\": 1\n  }\n}"
	if err.Error() != want {
		t.Fatalf("exact wording diverges:\n got: %q\nwant: %q", err.Error(), want)
	}
}

func TestValidationTypeAndEnumWording(t *testing.T) {
	s := mustUnmarshalObject(t, `{
		"type": "object",
		"properties": {
			"mode": {"enum": ["a", "b"]},
			"count": {"type": ["number", "string"]}
		}
	}`)
	_, err := validateArgs(t, s, map[string]any{"mode": "x"})
	if err == nil || !strings.Contains(err.Error(), "must be equal to one of the allowed values") {
		t.Fatalf("enum wording diverges: %v", err)
	}
	// true coerces to 1 (number branch wins); only an object defeats both
	// branches and triggers the multi-type message.
	_, err = validateArgs(t, s, map[string]any{"count": map[string]any{"deep": 1}})
	if err == nil || !strings.Contains(err.Error(), "must be either number or string") {
		t.Fatalf("multi-type wording diverges: %v", err)
	}
	if _, err := validateArgs(t, s, map[string]any{"count": true}); err != nil {
		t.Fatalf("true should coerce through the number branch: %v", err)
	}
}

func TestValidationAdditionalProperties(t *testing.T) {
	s := mustUnmarshalObject(t, `{
		"type": "object",
		"properties": {"a": {"type": "number"}},
		"additionalProperties": false
	}`)
	_, err := validateArgs(t, s, map[string]any{"a": 1, "extra": "x"})
	if err == nil || !strings.Contains(err.Error(), "must not have additional properties") {
		t.Fatalf("additionalProperties wording diverges: %v", err)
	}
	if _, err := validateArgs(t, s, map[string]any{"a": 1}); err != nil {
		t.Fatalf("defined property should pass: %v", err)
	}
}

func TestValidationStringAndArrayConstraints(t *testing.T) {
	s := mustUnmarshalObject(t, `{
		"type": "object",
		"properties": {
			"name": {"type": "string", "minLength": 3, "maxLength": 5},
			"tags": {"type": "array", "minItems": 1, "maxItems": 2, "uniqueItems": true}
		}
	}`)
	_, err := validateArgs(t, s, map[string]any{"name": "ab"})
	if err == nil || !strings.Contains(err.Error(), "must not have fewer than 3 characters") {
		t.Fatalf("minLength wording diverges: %v", err)
	}
	_, err = validateArgs(t, s, map[string]any{"name": "toolong"})
	if err == nil || !strings.Contains(err.Error(), "must not have more than 5 characters") {
		t.Fatalf("maxLength wording diverges: %v", err)
	}
	_, err = validateArgs(t, s, map[string]any{"tags": []any{1, 1}})
	if err == nil || !strings.Contains(err.Error(), "must not have duplicate items") {
		t.Fatalf("uniqueItems wording diverges: %v", err)
	}
	_, err = validateArgs(t, s, map[string]any{"tags": []any{}})
	if err == nil || !strings.Contains(err.Error(), "must not have fewer than 1 items") {
		t.Fatalf("minItems wording diverges: %v", err)
	}
}

func TestValidationNumericConstraints(t *testing.T) {
	s := mustUnmarshalObject(t, `{
		"type": "object",
		"properties": {"n": {"type": "number", "minimum": 1, "maximum": 10, "multipleOf": 2}}
	}`)
	if _, err := validateArgs(t, s, map[string]any{"n": 4}); err != nil {
		t.Fatalf("4 within range and multiple of 2 should pass: %v", err)
	}
	_, err := validateArgs(t, s, map[string]any{"n": 0.5})
	if err == nil || !strings.Contains(err.Error(), "must be >= 1") {
		t.Fatalf("minimum wording diverges: %v", err)
	}
	_, err = validateArgs(t, s, map[string]any{"n": 20})
	if err == nil || !strings.Contains(err.Error(), "must be <= 10") {
		t.Fatalf("maximum wording diverges: %v", err)
	}
	_, err = validateArgs(t, s, map[string]any{"n": 5})
	if err == nil || !strings.Contains(err.Error(), "must be multiple of 2") {
		t.Fatalf("multipleOf wording diverges: %v", err)
	}
}

func TestValidationNilParametersSkips(t *testing.T) {
	tool := src.Tool[any]{Name: "noparams", Parameters: nil}
	got, err := ValidateToolArguments(tool, src.ToolCall{ID: "t", Name: "noparams", Arguments: map[string]any{"x": 1}})
	if err != nil {
		t.Fatalf("nil parameters must skip validation: %v", err)
	}
	if _, ok := got["x"]; !ok {
		t.Fatalf("arguments must pass through: %#v", got)
	}
}

func TestValidateToolCallNotFoundWording(t *testing.T) {
	_, err := ValidateToolCall(nil, src.ToolCall{ID: "t", Name: "nope"})
	if err == nil || err.Error() != "Tool \"nope\" not found" {
		t.Fatalf("wording = %v, want Tool \"nope\" not found", err)
	}
}

func TestValidationOriginalArgumentsReportedUncoerced(t *testing.T) {
	// The "Received arguments" block shows the ORIGINAL arguments, not the
	// coerced ones.
	s := mustUnmarshalObject(t, `{
		"type": "object",
		"properties": {"n": {"type": "number", "minimum": 100}}
	}`)
	_, err := validateArgs(t, s, map[string]any{"n": "5"})
	if err == nil {
		t.Fatal("expected validation error")
	}
	// Received block shows the original string "5" (uncoerced), and the only
	// error is the post-coercion range violation.
	if !strings.Contains(err.Error(), "\"n\": \"5\"") || !strings.Contains(err.Error(), "must be >= 100") {
		t.Fatalf("expected the original (uncoerced) arguments and the range error: %v", err)
	}
	if strings.Contains(err.Error(), "must be number") {
		t.Fatalf("the coerced value must not produce a type error: %v", err)
	}
}
