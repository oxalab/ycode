package utils 

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	src "ycode/packages/llm/src"
)

// ValidateToolCall: Finds a tool by name and validates the tool call arguments.
func ValidateToolCall(tools []src.Tool[any], toolCall src.ToolCall) (map[string]any, error){
	for _, t := range tools {
		if t.Name == toolCall.Name {
			return ValidateToolArguments(t, toolCall)
		}
	}
	return nil, fmt.Errorf("tool %q not found", toolCall.Name)
}

// ValidateToolArguments : validates and coerces tool call arguments.
func ValidateToolArguments(tool src.Tool[any], toolCall src.ToolCall) (map[string]any, error) {
	args := deepClone(toolCall.Arguments)
	coerceWithJSONSchema(args, tool.Parameters.(map[string]any))
	return args, nil
}

func deepClone(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

func coerceWithJSONSchema(value map[string]any, schema map[string]any) {
	properties, _ := schema["properties"].(map[string]any)
	if properties == nil {
		return 
	}
	for key, propSchema := range properties {
		val, ok := value[key]
		if !ok {
			continue
		}
		value[key] = coerceValue(val, propSchema.(map[string]any))
	}
}

func coerceValue(val any, schema map[string]any) any {
	types := schemaTypes(schema)
	// Try coercing to match schema type
	for _, t := range types {
		coerced := coercePrimitive(val, t)
		if coerced != val {
			return coerced
		}
	}

	// Recurse into nested objects
	if hasType(types, "object") {
		if m, ok := val.(map[string]any); ok {
			coerceWithJSONSchema(m, schema)
		}
	}

	// Recurse into arrays
	if hasType(types, "array") {
		if arr, ok := val.([]any); ok {
			if itemSchema, ok := schema["items"].(map[string]any); ok {
				for i, item := range arr {
					arr[i] = coerceValue(item, itemSchema)
				}
			}
		}
	}
	return val
}

func coercePrimitive(val any, typ string) any {
	switch typ {
		case "number":
			switch v := val.(type ) {
				case nil:
					return float64(0)
				case string:
					if strings.TrimSpace(v) != "" {
						if f, err := parseFloat(v); err == nil && math.Abs(f) <= math.MaxFloat64 {
							return f
						}
					}
				case bool:
					if v {
						return float64(1)
					}
					return float64(0)
			}
		case "integer":
			switch v := val.(type) {
				case nil:
					return 0
				case string:
					if strings.TrimSpace(v) != ""{
						if f, err := parseFloat(v); err == nil && f == math.Trunc(f) {
							return int(f)
						}
					}
				case bool:
					if v {
						return 1
					}
					return 0
			}
		case "boolean":
			switch v := val.(type) {
			case nil:
				return false
			case string:
				if v == "true"{
					return true
				}
				if v == "false" {
					return false
				}
			case float64:
				if v == 1 {
					return true
				} 
				if v == 0 {
					return false
				}
			case int:
				if v == 1 {
					return true
				} 
				if v == 0 {
					return false
				}
			}
		case "string":
			switch v := val.(type) {
				case nil:
					return ""
				case float64:
					return fmt.Sprintf("%v", v)
				case bool:
					return fmt.Sprintf("%v", v)
			}
		case "null":
			if val == "" || val == float64(0) || val == int(0) || val == false {
				return nil
			}
	}
	return val
}

func schemaTypes(schema map[string]any) []string {
	switch t := schema["type"].(type) {
		case string:
			return []string{t}
		case []any:
			var out []string
			for _, v := range t {
				if s, ok := v.(string); ok {
					out = append(out, s)
				}
			}
			return out
	}
	return nil
}

func hasType(types []string, target string) bool {
	for _, t := range types {
		if t == target {
			return true
		}
	}
	return false
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}