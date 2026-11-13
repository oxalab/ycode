package utils

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	src "ycode/packages/llm/src"
)

// =============================================================================
// Tool argument validation (port of the reference utils/validation.ts)
// =============================================================================
//
// The reference compiles each tool's schema with TypeBox (Compile + Check +
// Errors) after a conversion pass, and throws a formatted message on failure:
//
//	Validation failed for tool "<name>":
//	  - <path>: <message>
//
//	Received arguments:
//	<original arguments, JSON with 2-space indent>
//
// Go has no TypeBox, so this file ports the observable behavior over plain
// JSON Schema: a coercion pass (validation.ts coerceWithJsonSchema, including
// allOf/anyOf/oneof branch selection, additionalProperties schemas and tuple
// items) followed by a Check pass whose error messages mirror the TypeBox
// en_US locale (must be X / must have required properties a, b / ...), with
// paths formatted like formatValidationPath (instancePath JSON pointers
// flattened to dots; required errors append the first missing property; the
// root path renders as "root").
//
// Divergences (documented, none reachable from ycode's own tool schemas):
//   - $ref, format, not, if/then/else, patternProperties, propertyNames,
//     contains and dependencies are not validated (silently skipped).
//   - string lengths count UTF-8 runes where TypeBox counts UTF-16 units.
//   - a failed "type" check reports just the type error; structural
//     keywords (properties/required/items) are not evaluated for that value.

// ValidateToolCall finds a tool by name and validates the tool call arguments.
func ValidateToolCall(tools []src.Tool[any], toolCall src.ToolCall) (map[string]any, error) {
	for _, t := range tools {
		if t.Name == toolCall.Name {
			return ValidateToolArguments(t, toolCall)
		}
	}
	return nil, fmt.Errorf("Tool %q not found", toolCall.Name)
}

// ValidateToolArguments validates and coerces tool call arguments against the
// tool's JSON Schema, returning the validated (possibly coerced) arguments or
// the reference-formatted validation error.
func ValidateToolArguments(tool src.Tool[any], toolCall src.ToolCall) (map[string]any, error) {
	args := deepClone(toolCall.Arguments)

	schema, ok := tool.Parameters.(map[string]any)
	if !ok {
		if tool.Parameters == nil {
			// No schema to validate against (the reference would throw inside
			// Compile; ycode tools without parameters skip validation).
			return args, nil
		}
		return nil, fmt.Errorf("Invalid parameters schema for tool %q", tool.Name)
	}

	coerceWithJSONSchema(args, schema)

	if errs := checkJSONSchema(args, schema, ""); len(errs) == 0 {
		return args, nil
	} else {
		lines := make([]string, 0, len(errs))
		for _, e := range errs {
			lines = append(lines, fmt.Sprintf("  - %s: %s", formatValidationPath(e), e.message))
		}
		joined := strings.Join(lines, "\n")
		if joined == "" {
			joined = "Unknown validation error"
		}
		received, _ := json.MarshalIndent(toolCall.Arguments, "", "  ")
		return nil, fmt.Errorf("Validation failed for tool %q:\n%s\n\nReceived arguments:\n%s",
			toolCall.Name, joined, string(received))
	}
}

// =============================================================================
// Schema errors and path formatting
// =============================================================================

type schemaError struct {
	keyword            string
	instancePath       string // JSON-pointer style: "" for root, "/a/b" nested
	message            string
	requiredProperties []string // set for required-keyword errors
}

// formatValidationPath mirrors the reference: required errors append the first
// missing property to the object path; everything else renders as the dotted
// path, with the root rendered as "root".
func formatValidationPath(err schemaError) string {
	if err.keyword == "required" && len(err.requiredProperties) > 0 {
		basePath := pointerToDots(err.instancePath)
		if basePath != "" {
			return basePath + "." + err.requiredProperties[0]
		}
		return err.requiredProperties[0]
	}
	if path := pointerToDots(err.instancePath); path != "" {
		return path
	}
	return "root"
}

func pointerToDots(p string) string {
	p = strings.TrimPrefix(p, "/")
	return strings.ReplaceAll(p, "/", ".")
}

// =============================================================================
// Check pass (TypeBox Check + Errors with en_US messages)
// =============================================================================

func checkJSONSchema(value any, schema map[string]any, path string) []schemaError {
	if schema == nil {
		return nil
	}
	errs := []schemaError{}
	add := func(keyword, message string) {
		errs = append(errs, schemaError{keyword: keyword, instancePath: path, message: message})
	}

	// const / enum
	if constant, present := schema["const"]; present {
		if !reflect.DeepEqual(normalizeJSONValue(value), normalizeJSONValue(constant)) {
			add("const", "must be equal to constant")
		}
	}
	if enumValues, present := schemaList(schema["enum"]); present {
		found := false
		for _, ev := range enumValues {
			if reflect.DeepEqual(normalizeJSONValue(value), normalizeJSONValue(ev)) {
				found = true
				break
			}
		}
		if !found {
			add("enum", "must be equal to one of the allowed values")
		}
	}

	// type (TypeBox en_US: "must be X" / "must be either A or B"). A failed
	// type check short-circuits the structural keywords for this value.
	schemaTypes := schemaTypes(schema)
	if len(schemaTypes) > 0 {
		matched := false
		for _, t := range schemaTypes {
			if matchesJSONType(value, t) {
				matched = true
				break
			}
		}
		if !matched {
			if len(schemaTypes) == 1 {
				add("type", "must be "+schemaTypes[0])
			} else {
				add("type", "must be either "+strings.Join(schemaTypes, " or "))
			}
			return errs
		}
	}

	// Combined schemas
	if allOf, present := schemaList(schema["allOf"]); present {
		for _, sub := range allOf {
			errs = append(errs, checkJSONSchema(value, sub, path)...)
		}
	}
	if anyOf, present := schemaList(schema["anyOf"]); present {
		matched := false
		for _, sub := range anyOf {
			if len(checkJSONSchema(value, sub, path)) == 0 {
				matched = true
				break
			}
		}
		if !matched {
			add("anyOf", "must match a schema in anyOf")
		}
	}
	if oneOf, present := schemaList(schema["oneOf"]); present {
		matches := 0
		for _, sub := range oneOf {
			if len(checkJSONSchema(value, sub, path)) == 0 {
				matches++
			}
		}
		if matches != 1 {
			add("oneOf", "must match exactly one schema in oneOf")
		}
	}

	// Numeric constraints (TypeBox en_US: "must be {comparison} {limit}")
	if num, ok := asFloat(value); ok {
		if limit, ok := schemaNumber(schema, "minimum"); ok && num < limit {
			add("minimum", "must be >= "+formatNumber(limit))
		}
		if limit, ok := schemaNumber(schema, "maximum"); ok && num > limit {
			add("maximum", "must be <= "+formatNumber(limit))
		}
		if limit, ok := schemaNumber(schema, "exclusiveMinimum"); ok && num <= limit {
			add("exclusiveMinimum", "must be > "+formatNumber(limit))
		}
		if limit, ok := schemaNumber(schema, "exclusiveMaximum"); ok && num >= limit {
			add("exclusiveMaximum", "must be < "+formatNumber(limit))
		}
		if limit, ok := schemaNumber(schema, "multipleOf"); ok && limit > 0 && math.Mod(num, limit) != 0 {
			add("multipleOf", "must be multiple of "+formatNumber(limit))
		}
	}

	// String constraints
	if str, ok := value.(string); ok {
		if limit, ok := schemaInt(schema, "minLength"); ok && utf8.RuneCountInString(str) < limit {
			add("minLength", fmt.Sprintf("must not have fewer than %d characters", limit))
		}
		if limit, ok := schemaInt(schema, "maxLength"); ok && utf8.RuneCountInString(str) > limit {
			add("maxLength", fmt.Sprintf("must not have more than %d characters", limit))
		}
		if pattern, ok := schema["pattern"].(string); ok && pattern != "" {
			if re, err := regexp.Compile(pattern); err == nil && !re.MatchString(str) {
				add("pattern", fmt.Sprintf("must match pattern %q", pattern))
			}
		}
	}

	// Array constraints
	if arr, ok := value.([]any); ok {
		if limit, ok := schemaInt(schema, "minItems"); ok && len(arr) < limit {
			add("minItems", fmt.Sprintf("must not have fewer than %d items", limit))
		}
		if limit, ok := schemaInt(schema, "maxItems"); ok && len(arr) > limit {
			add("maxItems", fmt.Sprintf("must not have more than %d items", limit))
		}
		if unique, ok := schema["uniqueItems"].(bool); ok && unique && !allUnique(arr) {
			add("uniqueItems", "must not have duplicate items")
		}
		switch items := schema["items"].(type) {
		case map[string]any:
			for i, item := range arr {
				errs = append(errs, checkJSONSchema(item, items, path+"/"+strconv.Itoa(i))...)
			}
		case []any: // tuple items
			for i, item := range arr {
				if i >= len(items) {
					break
				}
				if sub, ok := items[i].(map[string]any); ok {
					errs = append(errs, checkJSONSchema(item, sub, path+"/"+strconv.Itoa(i))...)
				}
			}
		}
	}

	// Object constraints
	if obj, ok := value.(map[string]any); ok {
		// required (one error listing all missing properties, TypeBox style)
		var missing []string
		if required, ok := schema["required"].([]any); ok {
			for _, r := range required {
				name, ok := r.(string)
				if !ok {
					continue
				}
				if _, present := obj[name]; !present {
					missing = append(missing, name)
				}
			}
		}
		if len(missing) > 0 {
			errs = append(errs, schemaError{
				keyword:            "required",
				instancePath:       path,
				message:            "must have required properties " + strings.Join(missing, ", "),
				requiredProperties: missing,
			})
		}

		// properties
		if properties, ok := schema["properties"].(map[string]any); ok {
			for key, propSchema := range properties {
				val, present := obj[key]
				if !present {
					continue
				}
				if sub, ok := propSchema.(map[string]any); ok {
					errs = append(errs, checkJSONSchema(val, sub, path+"/"+key)...)
				}
			}
		}

		// additionalProperties (at most one object-level error, Ajv style)
		if ap, present := schema["additionalProperties"]; present {
			properties, _ := schema["properties"].(map[string]any)
			reported := false
			for key, val := range obj {
				if properties != nil {
					if _, defined := properties[key]; defined {
						continue
					}
				}
				switch rule := ap.(type) {
				case bool:
					if rule || reported {
						continue
					}
					add("additionalProperties", "must not have additional properties")
					reported = true
				case map[string]any:
					errs = append(errs, checkJSONSchema(val, rule, path+"/"+key)...)
				}
			}
		}
	}

	return errs
}

// =============================================================================
// Coercion pass (validation.ts coerceWithJsonSchema)
// =============================================================================

// coerceWithJSONSchema coerces the top-level arguments object in place.
func coerceWithJSONSchema(value map[string]any, schema map[string]any) {
	coerceValueInPlace(value, schema)
}

// coerceValueInPlace coerces a value per its schema and returns the (possibly
// replaced) value. Object and array coercions mutate in place; primitive
// coercions return a new value.
func coerceValueInPlace(value any, schema map[string]any) any {
	if allOf, present := schemaList(schema["allOf"]); present {
		for _, sub := range allOf {
			value = coerceValueInPlace(value, sub)
		}
	}
	if anyOf, present := schemaList(schema["anyOf"]); present {
		value = coerceWithUnionSchema(value, anyOf)
	}
	if oneOf, present := schemaList(schema["oneOf"]); present {
		value = coerceWithUnionSchema(value, oneOf)
	}

	schemaTypes := schemaTypes(schema)
	matchesUnionMember := false
	if len(schemaTypes) > 1 {
		for _, t := range schemaTypes {
			if matchesJSONType(value, t) {
				matchesUnionMember = true
				break
			}
		}
	}
	if len(schemaTypes) > 0 && !matchesUnionMember {
		for _, t := range schemaTypes {
			candidate := coercePrimitive(value, t)
			if !valuesIdentical(candidate, value) {
				value = candidate
				break
			}
		}
	}

	if hasType(schemaTypes, "object") {
		if obj, ok := value.(map[string]any); ok {
			applySchemaObjectCoercion(obj, schema)
		}
	}
	if hasType(schemaTypes, "array") {
		if arr, ok := value.([]any); ok {
			applySchemaArrayCoercion(arr, schema)
		}
	}
	return value
}

func applySchemaObjectCoercion(value map[string]any, schema map[string]any) {
	properties, _ := schema["properties"].(map[string]any)
	definedKeys := map[string]bool{}
	for key := range properties {
		definedKeys[key] = true
	}
	for key, propSchema := range properties {
		val, present := value[key]
		if !present {
			continue
		}
		if sub, ok := propSchema.(map[string]any); ok {
			value[key] = coerceValueInPlace(val, sub)
		}
	}
	if additional, ok := schema["additionalProperties"].(map[string]any); ok {
		for key, v := range value {
			if definedKeys[key] {
				continue
			}
			value[key] = coerceValueInPlace(v, additional)
		}
	}
}

func applySchemaArrayCoercion(value []any, schema map[string]any) {
	if items, ok := schema["items"].([]any); ok {
		for i := 0; i < len(value) && i < len(items); i++ {
			if sub, ok := items[i].(map[string]any); ok {
				value[i] = coerceValueInPlace(value[i], sub)
			}
		}
		return
	}
	if items, ok := schema["items"].(map[string]any); ok {
		for i := range value {
			value[i] = coerceValueInPlace(value[i], items)
		}
	}
}

// coerceWithUnionSchema tries each branch on a fresh clone; the first branch
// whose coerced candidate passes Check wins (none: keep the original).
func coerceWithUnionSchema(value any, schemas []map[string]any) any {
	for _, schema := range schemas {
		candidate := deepCloneValue(value)
		coerced := coerceValueInPlace(candidate, schema)
		if len(checkJSONSchema(coerced, schema, "")) == 0 {
			return coerced
		}
	}
	return value
}

// =============================================================================
// Primitives and helpers
// =============================================================================

// coercePrimitive mirrors validation.ts coercePrimitiveByType.
func coercePrimitive(val any, typ string) any {
	switch typ {
	case "number":
		switch v := val.(type) {
		case nil:
			return float64(0)
		case string:
			if strings.TrimSpace(v) != "" {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
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
			return float64(0)
		case string:
			if strings.TrimSpace(v) != "" {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f == math.Trunc(f) {
					return f
				}
			}
		case bool:
			if v {
				return float64(1)
			}
			return float64(0)
		}
	case "boolean":
		switch v := val.(type) {
		case nil:
			return false
		case string:
			if v == "true" {
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
			return formatNumber(v)
		case int:
			return strconv.Itoa(v)
		case bool:
			return strconv.FormatBool(v)
		}
	case "null":
		if val == "" || val == float64(0) || val == int(0) || val == false {
			return nil
		}
	}
	return val
}

func matchesJSONType(value any, typ string) bool {
	switch typ {
	case "number":
		switch value.(type) {
		case float64, int, int64:
			return true
		}
		return false
	case "integer":
		switch v := value.(type) {
		case float64:
			return v == math.Trunc(v)
		case int:
			return true
		case int64:
			return true
		}
		return false
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "null":
		return value == nil
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	}
	return false
}

func deepClone(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func deepCloneValue(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}

// normalizeJSONValue round-trips through JSON so comparisons see the same
// value space (numbers as float64) regardless of how the schema literal was
// written.
func normalizeJSONValue(v any) any {
	if b, err := json.Marshal(v); err == nil {
		var out any
		if err := json.Unmarshal(b, &out); err == nil {
			return out
		}
	}
	return v
}

// valuesIdentical mirrors the TS strict-equality check between a coercion
// candidate and the original value: reference identity for objects (TS !==),
// value equality for primitives. Comparing map/slice interface values with ==
// panics in Go, hence the pointer check.
func valuesIdentical(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch a.(type) {
	case map[string]any, []any:
		return reflect.TypeOf(a) == reflect.TypeOf(b) && reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
	}
	return a == b
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

func asFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	}
	return 0, false
}

func schemaNumber(schema map[string]any, key string) (float64, bool) {
	if v, ok := schema[key].(float64); ok {
		return v, true
	}
	if v, ok := schema[key].(int); ok {
		return float64(v), true
	}
	return 0, false
}

func schemaInt(schema map[string]any, key string) (int, bool) {
	if v, ok := schema[key].(float64); ok && v == math.Trunc(v) {
		return int(v), true
	}
	if v, ok := schema[key].(int); ok {
		return v, true
	}
	return 0, false
}

func schemaList(field any) ([]map[string]any, bool) {
	list, ok := field.([]any)
	if !ok {
		return nil, false
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out, true
}

// formatNumber renders a float the way JS String(number) does for typical
// schema literals (shortest round-trip representation).
func formatNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func allUnique(items []any) bool {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if reflect.DeepEqual(normalizeJSONValue(items[i]), normalizeJSONValue(items[j])) {
				return false
			}
		}
	}
	return true
}
