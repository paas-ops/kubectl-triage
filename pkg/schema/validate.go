// Package schema validates incident metadata and report documents against the
// embedded public JSON Schemas under pkg/schema.
//
// The validator implements only the JSON Schema subset used by those two
// documents so the CLI stays dependency-free and fully offline. Supported
// keywords: $ref (local file), type, required, properties, additionalProperties,
// items, enum, const, minLength, minimum, maxItems, minItems and
// format=date-time. Unknown keywords are ignored, matching JSON Schema's
// extensibility rules for annotations.
package schema

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed incident.schema.json report.schema.json
var schemaFiles embed.FS

const (
	incidentSchemaName = "incident.schema.json"
	reportSchemaName   = "report.schema.json"
)

// ValidationError describes the first schema violation found in a document.
// Path uses a JSONPath-like notation rooted at "$".
type ValidationError struct {
	// Schema is the embedded schema file the document was validated against.
	Schema string
	// Path locates the offending value, for example "$.evidence[0].content_ref".
	Path string
	// Reason explains the violation in human-readable terms.
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.Schema, e.Path, e.Reason)
}

var (
	incidentSchema = mustLoadSchema(incidentSchemaName)
	reportSchema   = mustLoadSchema(reportSchemaName)
)

func mustLoadSchema(name string) map[string]any {
	data, err := schemaFiles.ReadFile(name)
	if err != nil {
		panic("schema: read embedded " + name + ": " + err.Error())
	}
	doc, err := decodeJSON(data)
	if err != nil {
		panic("schema: decode embedded " + name + ": " + err.Error())
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		panic("schema: embedded " + name + " is not a JSON object")
	}
	return obj
}

// ValidateIncident reports whether data is a valid incident metadata document,
// i.e. incident.yaml converted to JSON. The returned error is a
// *ValidationError when the document violates the schema.
func ValidateIncident(data []byte) error {
	return validateDocument(incidentSchemaName, incidentSchema, data)
}

// ValidateReport reports whether data is a valid report.json document.
// The returned error is a *ValidationError when the document violates the schema.
func ValidateReport(data []byte) error {
	return validateDocument(reportSchemaName, reportSchema, data)
}

func validateDocument(schemaName string, schema map[string]any, data []byte) error {
	instance, err := decodeJSON(data)
	if err != nil {
		return &ValidationError{Schema: schemaName, Path: "$", Reason: "document is not valid JSON: " + err.Error()}
	}
	return validateValue(schemaName, schema, "$", instance, 0)
}

// decodeJSON decodes exactly one JSON value, preserving number precision by
// using json.Number, and rejects trailing garbage.
func decodeJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("unexpected trailing JSON value")
		}
		return nil, err
	}
	return v, nil
}

// maxRefDepth bounds $ref expansion to keep malformed schemas from looping.
const maxRefDepth = 32

func validateValue(schemaName string, schema map[string]any, path string, v any, depth int) error {
	if depth > maxRefDepth {
		return &ValidationError{Schema: schemaName, Path: path, Reason: "schema $ref nesting exceeds limit"}
	}
	if schema == nil {
		return nil
	}

	if ref, ok := schema["$ref"].(string); ok {
		resolved, err := resolveRef(schemaName, ref)
		if err != nil {
			return &ValidationError{Schema: schemaName, Path: path, Reason: err.Error()}
		}
		return validateValue(schemaName, resolved, path, v, depth+1)
	}

	if rawType, ok := schema["type"]; ok {
		if err := checkType(schemaName, path, rawType, v); err != nil {
			return err
		}
	}

	if enum, ok := schema["enum"]; ok {
		values, ok := enum.([]any)
		if !ok {
			return &ValidationError{Schema: schemaName, Path: path, Reason: "schema enum is not an array"}
		}
		matched := false
		for _, candidate := range values {
			if jsonEqual(candidate, v) {
				matched = true
				break
			}
		}
		if !matched {
			return &ValidationError{Schema: schemaName, Path: path, Reason: fmt.Sprintf("value %s is not one of %s", renderValue(v), renderValues(values))}
		}
	}

	if c, ok := schema["const"]; ok && !jsonEqual(c, v) {
		return &ValidationError{Schema: schemaName, Path: path, Reason: fmt.Sprintf("value %s does not equal required constant %s", renderValue(v), renderValue(c))}
	}

	switch inst := v.(type) {
	case map[string]any:
		return validateObject(schemaName, schema, path, inst, depth)
	case []any:
		return validateArray(schemaName, schema, path, inst, depth)
	case string:
		return validateString(schemaName, schema, path, inst)
	case json.Number:
		return validateNumber(schemaName, schema, path, inst)
	}
	return nil
}

func checkType(schemaName, path string, rawType any, v any) error {
	allowed := map[string]bool{}
	switch t := rawType.(type) {
	case string:
		allowed[t] = true
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok {
				allowed[s] = true
			}
		}
	default:
		return &ValidationError{Schema: schemaName, Path: path, Reason: "schema type must be a string or array of strings"}
	}

	for name := range allowed {
		if matchesType(name, v) {
			return nil
		}
	}
	return &ValidationError{
		Schema: schemaName,
		Path:   path,
		Reason: fmt.Sprintf("value %s has type %s, want %s", renderValue(v), jsonTypeName(v), strings.Join(sortedKeys(allowed), "|")),
	}
}

func matchesType(name string, v any) bool {
	switch name {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	case "number":
		_, ok := v.(json.Number)
		return ok
	case "integer":
		n, ok := v.(json.Number)
		return ok && isInteger(n)
	default:
		return false
	}
}

func isInteger(n json.Number) bool {
	s := string(n)
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return false
	}
	return math.Trunc(f) == f
}

func jsonTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case json.Number:
		return "number"
	default:
		return reflect.TypeOf(v).String()
	}
}

func validateObject(schemaName string, schema map[string]any, path string, obj map[string]any, depth int) error {
	if required, ok := schema["required"]; ok {
		names, ok := required.([]any)
		if !ok {
			return &ValidationError{Schema: schemaName, Path: path, Reason: "schema required must be an array of strings"}
		}
		for _, item := range names {
			name, ok := item.(string)
			if !ok {
				continue
			}
			if _, present := obj[name]; !present {
				return &ValidationError{Schema: schemaName, Path: childPath(path, name), Reason: "required field is missing"}
			}
		}
	}

	properties, _ := schema["properties"].(map[string]any)
	additional, hasAdditional := schema["additionalProperties"]

	// Deterministic iteration matters for stable error messages.
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := obj[key]
		if propSchema, ok := properties[key].(map[string]any); ok {
			if err := validateValue(schemaName, propSchema, childPath(path, key), value, depth+1); err != nil {
				return err
			}
			continue
		}
		if !hasAdditional {
			continue
		}
		switch ap := additional.(type) {
		case bool:
			if !ap {
				return &ValidationError{Schema: schemaName, Path: childPath(path, key), Reason: "additional property is not allowed"}
			}
		case map[string]any:
			if err := validateValue(schemaName, ap, childPath(path, key), value, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateArray(schemaName string, schema map[string]any, path string, arr []any, depth int) error {
	if min, ok := schemaIntKeyword(schema, "minItems"); ok && len(arr) < min {
		return &ValidationError{Schema: schemaName, Path: path, Reason: fmt.Sprintf("array has %d items, minimum is %d", len(arr), min)}
	}
	if max, ok := schemaIntKeyword(schema, "maxItems"); ok && len(arr) > max {
		return &ValidationError{Schema: schemaName, Path: path, Reason: fmt.Sprintf("array has %d items, maximum is %d", len(arr), max)}
	}
	items, hasItems := schema["items"]
	if !hasItems {
		return nil
	}
	switch itemSchema := items.(type) {
	case map[string]any:
		for i, value := range arr {
			if err := validateValue(schemaName, itemSchema, indexPath(path, i), value, depth+1); err != nil {
				return err
			}
		}
	case []any:
		// Tuple validation: validate the declared positions only. The public
		// schemas do not use tuples, but supporting it keeps $ref targets usable.
		for i, value := range arr {
			if i >= len(itemSchema) {
				break
			}
			if s, ok := itemSchema[i].(map[string]any); ok {
				if err := validateValue(schemaName, s, indexPath(path, i), value, depth+1); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateString(schemaName string, schema map[string]any, path, value string) error {
	if raw, ok := schema["minLength"]; ok {
		if min, ok := numberInt(raw); ok {
			count := utf8.RuneCountInString(value)
			if count < min {
				return &ValidationError{Schema: schemaName, Path: path, Reason: fmt.Sprintf("string length %d is less than minimum %d", count, min)}
			}
		}
	}
	if format, ok := schema["format"].(string); ok && format == "date-time" {
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return &ValidationError{Schema: schemaName, Path: path, Reason: fmt.Sprintf("value %q is not an RFC3339 date-time", value)}
		}
	}
	return nil
}

func validateNumber(schemaName string, schema map[string]any, path string, value json.Number) error {
	raw, ok := schema["minimum"]
	if !ok {
		return nil
	}
	min, ok := numberFloat(raw)
	if !ok {
		return &ValidationError{Schema: schemaName, Path: path, Reason: "schema minimum must be a number"}
	}
	got, err := value.Float64()
	if err != nil {
		return &ValidationError{Schema: schemaName, Path: path, Reason: "value is not a valid number"}
	}
	if got < min {
		return &ValidationError{Schema: schemaName, Path: path, Reason: fmt.Sprintf("value %s is less than minimum %s", renderValue(value), renderValue(raw))}
	}
	return nil
}

func schemaIntKeyword(schema map[string]any, keyword string) (int, bool) {
	raw, ok := schema[keyword]
	if !ok {
		return 0, false
	}
	return numberInt(raw)
}

func numberInt(raw any) (int, bool) {
	f, ok := numberFloat(raw)
	if !ok {
		return 0, false
	}
	return int(f), true
}

func numberFloat(raw any) (float64, bool) {
	switch n := raw.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

func resolveRef(baseSchemaName, ref string) (map[string]any, error) {
	name, fragment, _ := strings.Cut(ref, "#")
	if name == "" {
		name = baseSchemaName
	}
	if strings.Contains(name, "..") || strings.Contains(name, "/") {
		return nil, fmt.Errorf("unsupported schema reference %q", ref)
	}
	var doc map[string]any
	switch name {
	case incidentSchemaName:
		doc = incidentSchema
	case reportSchemaName:
		doc = reportSchema
	default:
		data, err := schemaFiles.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("unknown schema reference %q", ref)
		}
		decoded, err := decodeJSON(data)
		if err != nil {
			return nil, fmt.Errorf("decode schema reference %q: %w", ref, err)
		}
		obj, ok := decoded.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("schema reference %q is not an object", ref)
		}
		doc = obj
	}
	if fragment == "" {
		return doc, nil
	}
	resolved, err := resolvePointer(doc, fragment)
	if err != nil {
		return nil, fmt.Errorf("schema reference %q: %w", ref, err)
	}
	return resolved, nil
}

func resolvePointer(doc map[string]any, fragment string) (map[string]any, error) {
	pointer, ok := strings.CutPrefix(fragment, "/")
	if !ok {
		return nil, fmt.Errorf("unsupported fragment %q", fragment)
	}
	var current any = doc
	for _, token := range strings.Split(pointer, "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("fragment %q does not point at an object", fragment)
		}
		current, ok = obj[token]
		if !ok {
			return nil, fmt.Errorf("fragment %q does not exist", fragment)
		}
	}
	resolved, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("fragment %q does not point at an object", fragment)
	}
	return resolved, nil
}

func jsonEqual(a, b any) bool {
	an, aIsNum := a.(json.Number)
	bn, bIsNum := b.(json.Number)
	if aIsNum && bIsNum {
		af, errA := an.Float64()
		bf, errB := bn.Float64()
		if errA == nil && errB == nil {
			return af == bf
		}
		return string(an) == string(bn)
	}
	return reflect.DeepEqual(a, b)
}

func childPath(parent, name string) string {
	if isIdentifier(name) {
		return parent + "." + name
	}
	return parent + "[" + strconv.Quote(name) + "]"
}

func indexPath(parent string, i int) string {
	return fmt.Sprintf("%s[%d]", parent, i)
}

func isIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// renderValue formats a scalar for error messages. Objects and arrays are
// reduced to their type name to avoid dumping potentially large content.
func renderValue(v any) string {
	switch value := v.(type) {
	case nil:
		return "null"
	case string:
		runes := []rune(value)
		if len(runes) > 64 {
			return strconv.Quote(string(runes[:64]) + "...")
		}
		return strconv.Quote(value)
	case json.Number:
		return string(value)
	case bool:
		return strconv.FormatBool(value)
	default:
		return jsonTypeName(v)
	}
}

func renderValues(values []any) string {
	rendered := make([]string, 0, len(values))
	for _, v := range values {
		rendered = append(rendered, renderValue(v))
	}
	return "[" + strings.Join(rendered, ", ") + "]"
}
