package tools

// schema.go provides a tiny builder for JSON Schema objects used as tool
// parameter definitions. It mirrors the TypeBox schemas in the TS reference
// (reference/coding-agent/src/core/tools/*.ts), producing the same shape the
// providers expect: {"type":"object","properties":{...},"required":[...]} with
// per-property "description" strings.
//
// Each builtin tool builds its schema via these helpers in its
// Create*ToolDefinition function and assigns it to ToolDefinition.Parameters
// (json.RawMessage). tool_definition_wrapper.go then unwraps it into the
// map[string]any the SDK/provider layer ships to the LLM.

import "encoding/json"

// propType is the JSON Schema type of a property ("string", "number",
// "boolean", "integer", "array", "object").
type propType string

const (
	propString  propType = "string"
	propNumber  propType = "number"
	propInteger propType = "integer"
	propBoolean propType = "boolean"
	propArray   propType = "array"
	propObject  propType = "object"
)

// prop describes one object property.
type prop struct {
	typ         propType
	description string
	// array-only: element schema (items).
	items map[string]any
	// extra fields merged into the property (e.g. "enum").
	extra map[string]any
}

// objectSchema builds a JSON Schema object from the given properties. Keys in
// required are listed under "required". Property order follows props insertion
// order (Go maps are unordered, but providers don't rely on order — the TS
// TypeBox schemas are likewise consumed as objects).
//
// No "additionalProperties" is emitted: the reference's bare Type.Object
// calls (bash/read/write/ls/grep/find) emit none. Tools whose reference
// schema passes the option explicitly (edit, edit.ts:50) use objectSchemaStrict.
func objectSchema(props map[string]prop, required []string) json.RawMessage {
	return marshalSchema(buildObjectSchema(props, required, false))
}

// objectSchemaStrict is objectSchema plus a top-level
// "additionalProperties": false — the shape of the reference's edit schema,
// which passes the option to Type.Object explicitly (edit.ts:50).
func objectSchemaStrict(props map[string]prop, required []string) json.RawMessage {
	return marshalSchema(buildObjectSchema(props, required, true))
}

func buildObjectSchema(props map[string]prop, required []string, additionalProperties bool) map[string]any {
	properties := make(map[string]any, len(props))
	for name, p := range props {
		m := map[string]any{
			"type":        string(p.typ),
			"description": p.description,
		}
		for k, v := range p.extra {
			m[k] = v
		}
		if p.typ == propArray && p.items != nil {
			m["items"] = p.items
		}
		properties[name] = m
	}
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
		// "required" is omitted when empty (matches TypeBox, which only emits
		// required for non-Optional fields; an empty slice would serialize as
		// [] which some validators reject).
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	if additionalProperties {
		schema["additionalProperties"] = false
	}
	return schema
}

func marshalSchema(schema map[string]any) json.RawMessage {
	b, _ := json.Marshal(schema)
	return b
}

// arrayItems builds an "items" schema for an array property of objects. The
// nested "additionalProperties": false matches the reference's edit schema,
// which passes the option explicitly for the edits[] items (edit.ts:39) —
// arrayItems is used only by edit today.
func arrayItems(props map[string]prop, required []string) map[string]any {
	properties := make(map[string]any, len(props))
	for name, p := range props {
		m := map[string]any{
			"type":        string(p.typ),
			"description": p.description,
		}
		for k, v := range p.extra {
			m[k] = v
		}
		properties[name] = m
	}
	items := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		items["required"] = required
	}
	return items
}
