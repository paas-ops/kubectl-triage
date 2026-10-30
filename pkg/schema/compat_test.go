// Compatibility baseline test for the public incident/report JSON Schemas
// (ADR-007 D1/D2, split plan 5.3).
//
// What this test covers:
//   - Recursively extracts the compatibility-relevant shape of the two embedded
//     schemas: required field lists (sorted), property keys, type/$ref/format/
//     enum/const/bounds, array items and nested object requirements. The
//     summary is serialized as stable JSON (sorted keys, 2-space indent) and
//     compared byte-for-byte with testdata/compat-baseline.json.
//   - Asserts the schema_version const, the JSON Schema draft and the top-level
//     additionalProperties=false invariants directly.
//   - Validates every testdata/golden/*/expected/report.json and
//     examples/oom/report.json against the current embedded report schema.
//
// Schema change process (v1.0 compatibility promise, ADR-007 D1/D2):
//  1. Classify the change: adding an optional field is compatible; removing a
//     field, adding/tightening required, narrowing enum, or changing const or
//     field semantics is breaking and needs a major schema version bump.
//  2. Record the change, its classification and migration notes in CHANGELOG.md
//     and the relevant ADR.
//  3. Regenerate the baseline deliberately and review its diff (do not commit
//     an unreviewed baseline update):
//     UPDATE_COMPAT=1 GOCACHE=$PWD/.gocache go test -run TestCompatibilityBaseline ./pkg/schema
//  4. Run the full test suite; if the report shape changed, regenerate golden
//     expected reports and examples/oom/report.json.
//  5. Never edit testdata/compat-baseline.json by hand: it is generated.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const (
	// compatBaselinePath is package-relative (go test runs in the package dir).
	compatBaselinePath = "testdata/compat-baseline.json"
	// compatUpdateEnv switches the baseline test into write mode.
	compatUpdateEnv = "UPDATE_COMPAT"
	// compatDraftURL is the JSON Schema dialect both public schemas must use.
	compatDraftURL = "https://json-schema.org/draft/2020-12/schema"
)

// compatNode is the compatibility-relevant subset of one schema node. Field
// order defines the baseline JSON layout; maps and lists are sorted for stable
// output.
type compatNode struct {
	Type                 string                 `json:"type,omitempty"`
	Format               string                 `json:"format,omitempty"`
	Ref                  string                 `json:"$ref,omitempty"`
	Enum                 []any                  `json:"enum,omitempty"`
	Const                any                    `json:"const,omitempty"`
	MinLength            *int                   `json:"minLength,omitempty"`
	Minimum              *int                   `json:"minimum,omitempty"`
	MinItems             *int                   `json:"minItems,omitempty"`
	MaxItems             *int                   `json:"maxItems,omitempty"`
	Required             []string               `json:"required,omitempty"`
	AdditionalProperties any                    `json:"additionalProperties,omitempty"`
	Properties           map[string]*compatNode `json:"properties,omitempty"`
	Items                *compatNode            `json:"items,omitempty"`
}

// TestCompatibilityBaseline is the schema compatibility gate. Default mode is a
// strict byte comparison; UPDATE_COMPAT=1 rewrites the baseline.
func TestCompatibilityBaseline(t *testing.T) {
	computed := compatMarshal(t, compatBuildSummary(t))
	if again := compatMarshal(t, compatBuildSummary(t)); !bytes.Equal(computed, again) {
		t.Fatal("compatibility summary serialization is not deterministic")
	}
	if os.Getenv(compatUpdateEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(compatBaselinePath), 0o755); err != nil {
			t.Fatalf("create baseline directory: %v", err)
		}
		if err := os.WriteFile(compatBaselinePath, computed, 0o644); err != nil {
			t.Fatalf("write baseline: %v", err)
		}
		t.Logf("compatibility baseline updated: %s", compatBaselinePath)
		t.Logf("next: review the baseline diff, classify the schema change (optional field vs breaking), record it in CHANGELOG.md and the relevant ADR, then run the full test suite")
		return
	}

	baseline, err := os.ReadFile(compatBaselinePath)
	if err != nil {
		t.Fatalf("missing compatibility baseline %s: %v; run UPDATE_COMPAT=1 GOCACHE=$PWD/.gocache go test -run TestCompatibilityBaseline ./pkg/schema", compatBaselinePath, err)
	}
	if bytes.Equal(baseline, computed) {
		return
	}
	diff := compatDiff(t, baseline, computed)
	t.Fatalf("schema compatibility baseline changed (ADR-007 D1/D2):%s%s%sIf this change is intentional: classify it (optional field addition vs breaking), record it in CHANGELOG.md and the relevant ADR, then regenerate the baseline with UPDATE_COMPAT=1 and review the diff.", string([]byte{10}), diff, string([]byte{10}))
}

// TestCompatibilityDiffReportsContractChanges pins the guidance the gate emits
// when a schema contract changes (required / enum / const / properties).
func TestCompatibilityDiffReportsContractChanges(t *testing.T) {
	baseline := []byte(`{
  "report.schema.json": {
    "required": ["a", "b"],
    "properties": {
      "mode": {"enum": ["offline", "model"]},
      "schema_version": {"const": "1.0"}
    }
  }
}`)
	current := []byte(`{
  "report.schema.json": {
    "required": ["a", "c"],
    "properties": {
      "mode": {"enum": ["offline"]},
      "schema_version": {"const": "2.0"},
      "extra": {"type": "string"}
    }
  }
}`)
	diff := compatDiff(t, baseline, current)
	for _, want := range []string{
		"report.schema.json.required",
		"added [c]",
		"removed [b]",
		"report.schema.json.properties.mode.enum",
		"model",
		"report.schema.json.properties.schema_version.const",
		"1.0",
		"2.0",
		"report.schema.json.properties.extra: added in current schema",
	} {
		if !strings.Contains(diff, want) {
			t.Fatalf("compatibility diff missing %q:%s%s", want, string([]byte{10}), diff)
		}
	}
}

// TestCompatibilitySchemaInvariants pins the invariants that must never drift
// silently even if someone edits the summary extraction.
func TestCompatibilitySchemaInvariants(t *testing.T) {
	incident := compatLoadEmbedded(t, incidentSchemaName)
	report := compatLoadEmbedded(t, reportSchemaName)

	for _, tc := range []struct {
		name string
		doc  map[string]any
	}{
		{incidentSchemaName, incident},
		{reportSchemaName, report},
	} {
		if got, _ := tc.doc["$schema"].(string); got != compatDraftURL {
			t.Errorf("%s: $schema = %q, want %q", tc.name, got, compatDraftURL)
		}
		additional, ok := tc.doc["additionalProperties"].(bool)
		if !ok || additional {
			t.Errorf("%s: top-level additionalProperties = %v, want false", tc.name, tc.doc["additionalProperties"])
		}
	}

	properties, ok := report["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s: properties is not an object", reportSchemaName)
	}
	schemaVersion, ok := properties["schema_version"].(map[string]any)
	if !ok {
		t.Fatalf("%s: properties.schema_version is not an object", reportSchemaName)
	}
	constValue, ok := schemaVersion["const"].(string)
	if !ok || constValue != "1.0" {
		t.Fatalf("%s: schema_version const = %v, want string %q", reportSchemaName, schemaVersion["const"], "1.0")
	}
}

// TestCompatibilityFixtureReportsValidate keeps every checked-in report
// fixture valid under the current embedded schema.
func TestCompatibilityFixtureReportsValidate(t *testing.T) {
	root := filepath.Join("..", "..")

	goldenReports, err := filepath.Glob(filepath.Join(root, "testdata", "golden", "*", "expected", "report.json"))
	if err != nil {
		t.Fatalf("glob golden expected reports: %v", err)
	}
	sort.Strings(goldenReports)
	if len(goldenReports) != 12 {
		t.Fatalf("expected 12 golden expected reports, found %d: %v", len(goldenReports), goldenReports)
	}
	for _, path := range goldenReports {
		compatValidateReportFile(t, path)
	}
	compatValidateReportFile(t, filepath.Join(root, "examples", "oom", "report.json"))
}

func compatValidateReportFile(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("%s: read report: %v", path, err)
		return
	}
	if err := ValidateReport(data); err != nil {
		t.Errorf("%s: report does not satisfy the current schema: %v", path, err)
	}
}

// compatBuildSummary extracts both embedded schemas into their compatibility
// summary.
func compatBuildSummary(t *testing.T) map[string]*compatNode {
	t.Helper()
	return map[string]*compatNode{
		incidentSchemaName: compatSummarizeDoc(t, incidentSchemaName, compatLoadEmbedded(t, incidentSchemaName)),
		reportSchemaName:   compatSummarizeDoc(t, reportSchemaName, compatLoadEmbedded(t, reportSchemaName)),
	}
}

// compatLoadEmbedded decodes one embedded schema with json.Number so numeric
// bounds round-trip without float formatting drift.
func compatLoadEmbedded(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := schemaFiles.ReadFile(name)
	if err != nil {
		t.Fatalf("read embedded schema %s: %v", name, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil {
		t.Fatalf("decode embedded schema %s: %v", name, err)
	}
	return doc
}

func compatSummarizeDoc(t *testing.T, name string, doc map[string]any) *compatNode {
	t.Helper()
	return compatNodeFrom(t, name, "$", doc)
}

// compatNodeFrom recursively converts one schema object into a compatNode.
// Keywords outside the compatibility contract (title, description, $id,
// examples, ...) are intentionally ignored.
func compatNodeFrom(t *testing.T, schemaName, path string, schema map[string]any) *compatNode {
	t.Helper()
	node := &compatNode{}
	if value, ok := schema["type"].(string); ok {
		node.Type = value
	}
	if value, ok := schema["format"].(string); ok {
		node.Format = value
	}
	if value, ok := schema["$ref"].(string); ok {
		node.Ref = value
	}
	if value, ok := schema["const"]; ok {
		node.Const = value
	}
	if value, ok := schema["enum"].([]any); ok {
		node.Enum = compatSortedValues(value)
	}
	if value, ok := schema["required"].([]any); ok {
		node.Required = compatSortedStrings(t, schemaName, path+".required", value)
	}

	bounds := []struct {
		keyword string
		target  **int
	}{
		{"minLength", &node.MinLength},
		{"minimum", &node.Minimum},
		{"minItems", &node.MinItems},
		{"maxItems", &node.MaxItems},
	}
	for _, bound := range bounds {
		if value, ok := schema[bound.keyword]; ok {
			*bound.target = compatIntPtr(t, schemaName, path+"."+bound.keyword, value)
		}
	}

	switch additional := schema["additionalProperties"].(type) {
	case bool:
		node.AdditionalProperties = additional
	case map[string]any:
		node.AdditionalProperties = compatNodeFrom(t, schemaName, path+".additionalProperties", additional)
	case nil:
		// absent: additional properties default to allowed, not part of the gate
	default:
		t.Fatalf("%s: %s.additionalProperties has unsupported type %T", schemaName, path, additional)
	}

	if properties, ok := schema["properties"].(map[string]any); ok {
		node.Properties = make(map[string]*compatNode, len(properties))
		keys := make([]string, 0, len(properties))
		for key := range properties {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child, ok := properties[key].(map[string]any)
			if !ok {
				t.Fatalf("%s: %s.properties.%s is not an object", schemaName, path, key)
			}
			node.Properties[key] = compatNodeFrom(t, schemaName, path+".properties."+key, child)
		}
	}
	if items, ok := schema["items"].(map[string]any); ok {
		node.Items = compatNodeFrom(t, schemaName, path+".items", items)
	}
	return node
}

func compatSortedStrings(t *testing.T, schemaName, path string, values []any) []string {
	t.Helper()
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("%s: %s contains non-string value %v", schemaName, path, value)
		}
		out = append(out, text)
	}
	sort.Strings(out)
	return out
}

// compatSortedValues sorts enum values by their compact JSON encoding so the
// baseline is independent of declaration order.
func compatSortedValues(values []any) []any {
	out := make([]any, len(values))
	copy(out, values)
	sort.SliceStable(out, func(i, j int) bool {
		left, _ := json.Marshal(out[i])
		right, _ := json.Marshal(out[j])
		return bytes.Compare(left, right) < 0
	})
	return out
}

func compatIntPtr(t *testing.T, schemaName, path string, value any) *int {
	t.Helper()
	var number int
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			t.Fatalf("%s: %s is not an integer: %v", schemaName, path, err)
		}
		number = int(parsed)
	case float64:
		number = int(typed)
	case int:
		number = typed
	default:
		t.Fatalf("%s: %s has unsupported numeric type %T", schemaName, path, value)
	}
	return &number
}

func compatMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal compatibility summary: %v", err)
	}
	return append(data, byte(10))
}

// compatDiff renders a deterministic, human-readable list of compatibility
// differences between the committed baseline and the current schemas.
func compatDiff(t *testing.T, baseline, current []byte) string {
	t.Helper()
	var want, got any
	if err := json.Unmarshal(baseline, &want); err != nil {
		t.Fatalf("decode compatibility baseline: %v", err)
	}
	if err := json.Unmarshal(current, &got); err != nil {
		t.Fatalf("decode computed compatibility summary: %v", err)
	}
	var lines []string
	compatCompare("", want, got, &lines)
	if len(lines) == 0 {
		lines = append(lines, "(no structured difference found; raw bytes differ - check formatting or ordering)")
	}
	const maxLines = 60
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], fmt.Sprintf("... and %d more differences", len(lines)-maxLines))
	}
	return strings.Join(lines, string([]byte{10}))
}

func compatCompare(path string, want, got any, out *[]string) {
	wantMap, wantIsMap := want.(map[string]any)
	gotMap, gotIsMap := got.(map[string]any)
	if wantIsMap && gotIsMap {
		seen := make(map[string]bool, len(wantMap)+len(gotMap))
		for key := range wantMap {
			seen[key] = true
		}
		for key := range gotMap {
			seen[key] = true
		}
		for _, key := range compatSortedKeys(seen) {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			wantValue, wantOK := wantMap[key]
			gotValue, gotOK := gotMap[key]
			switch {
			case wantOK && !gotOK:
				*out = append(*out, childPath+": removed from current schema")
			case !wantOK && gotOK:
				*out = append(*out, childPath+": added in current schema")
			default:
				compatCompareKey(childPath, key, wantValue, gotValue, out)
			}
		}
		return
	}
	if !reflect.DeepEqual(want, got) {
		*out = append(*out, fmt.Sprintf("%s: baseline %s -> current %s", path, compatRender(want), compatRender(got)))
	}
}

// compatCompareKey highlights contract-critical keywords explicitly (required,
// enum, const) and recurses otherwise.
func compatCompareKey(path, key string, want, got any, out *[]string) {
	switch key {
	case "required":
		wantList, wantOK := want.([]any)
		gotList, gotOK := got.([]any)
		if wantOK && gotOK {
			added, removed := compatStringDelta(wantList, gotList)
			if len(added) > 0 || len(removed) > 0 {
				*out = append(*out, fmt.Sprintf("%s: added %v, removed %v (current vs baseline)", path, added, removed))
			}
			return
		}
	case "enum":
		wantList, wantOK := want.([]any)
		gotList, gotOK := got.([]any)
		if wantOK && gotOK {
			added, removed := compatValueDelta(wantList, gotList)
			if len(added) > 0 || len(removed) > 0 {
				*out = append(*out, fmt.Sprintf("%s: added %v, removed %v (current vs baseline)", path, added, removed))
			}
			return
		}
	case "const":
		if !reflect.DeepEqual(want, got) {
			*out = append(*out, fmt.Sprintf("%s: baseline const %s -> current const %s", path, compatRender(want), compatRender(got)))
		}
		return
	}
	compatCompare(path, want, got, out)
}

func compatStringDelta(want, got []any) (added, removed []string) {
	wantSet := make(map[string]bool, len(want))
	for _, value := range want {
		if text, ok := value.(string); ok {
			wantSet[text] = true
		}
	}
	gotSet := make(map[string]bool, len(got))
	for _, value := range got {
		if text, ok := value.(string); ok {
			gotSet[text] = true
		}
	}
	for text := range gotSet {
		if !wantSet[text] {
			added = append(added, text)
		}
	}
	for text := range wantSet {
		if !gotSet[text] {
			removed = append(removed, text)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func compatValueDelta(want, got []any) (added, removed []string) {
	wantSet := make(map[string]bool, len(want))
	for _, value := range want {
		wantSet[compatRender(value)] = true
	}
	gotSet := make(map[string]bool, len(got))
	for _, value := range got {
		gotSet[compatRender(value)] = true
	}
	for value := range gotSet {
		if !wantSet[value] {
			added = append(added, value)
		}
	}
	for value := range wantSet {
		if !gotSet[value] {
			removed = append(removed, value)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func compatSortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func compatRender(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}
