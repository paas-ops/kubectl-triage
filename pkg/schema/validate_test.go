package schema

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func incidentDoc() map[string]any {
	return map[string]any{
		"id":           "example-oom",
		"alertname":    "KubePodCrashLooping",
		"severity":     "critical",
		"cluster":      "local",
		"namespace":    "checkout",
		"workload":     "checkout-api",
		"observed_at":  "2026-10-12T09:14:00Z",
		"collected_at": "2026-10-12T09:20:00Z",
		"source":       "alertmanager",
	}
}

func evidenceDoc() map[string]any {
	return map[string]any{
		"id":           "ev-001",
		"source":       "k8s/events.json",
		"observed_at":  "2026-10-12T09:14:00Z",
		"collected_at": "2026-10-12T09:20:00Z",
		"summary":      "Pod checkout-api-7d9f OOMKilled, exitCode=137",
		"content_ref":  "k8s/events.json#items/3",
		"sha256":       "sha256:abc",
		"stale":        false,
	}
}

func hypothesisDoc() map[string]any {
	return map[string]any{
		"rank":           1,
		"statement":      "容器内存 limit 过低导致 OOMKilled",
		"confidence":     "high",
		"evidence_ids":   []any{"ev-001"},
		"contradictions": []any{map[string]any{"evidence_id": "ev-007", "note": "同期节点内存充足"}},
	}
}

func reportDoc() map[string]any {
	return map[string]any{
		"schema_version":     "1.0",
		"incident":           incidentDoc(),
		"generated_at":       "2026-10-12T09:25:00Z",
		"mode":               "offline",
		"degraded":           false,
		"summary":            "容器因内存不足被 OOMKilled",
		"evidence":           []any{evidenceDoc()},
		"hypotheses":         []any{hypothesisDoc()},
		"missing_data":       []any{},
		"recommended_checks": []any{"检查 memory limit"},
		"suggested_fix":      "只读建议：评估 memory limit。",
		"limitations":        []any{},
		"versions":           map[string]any{"cli": "test", "rules": "rule-v0.1", "model": "none"},
	}
}

func requireValidationError(t *testing.T, err error, wantSchema, wantPath, wantReason string) *ValidationError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a validation error for %s %s", wantSchema, wantPath)
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if ve.Schema != wantSchema {
		t.Fatalf("schema = %q, want %q", ve.Schema, wantSchema)
	}
	if ve.Path != wantPath {
		t.Fatalf("path = %q, want %q (reason: %s)", ve.Path, wantPath, ve.Reason)
	}
	if !strings.Contains(ve.Reason, wantReason) {
		t.Fatalf("reason = %q, want it to contain %q", ve.Reason, wantReason)
	}
	return ve
}

func TestValidateIncidentValid(t *testing.T) {
	if err := ValidateIncident(mustJSON(t, incidentDoc())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateIncidentMissingRequired(t *testing.T) {
	doc := incidentDoc()
	delete(doc, "workload")
	err := ValidateIncident(mustJSON(t, doc))
	requireValidationError(t, err, incidentSchemaName, "$.workload", "required field is missing")
}

func TestValidateIncidentEmptyID(t *testing.T) {
	doc := incidentDoc()
	doc["id"] = ""
	err := ValidateIncident(mustJSON(t, doc))
	requireValidationError(t, err, incidentSchemaName, "$.id", "minimum")
}

func TestValidateIncidentBadSeverity(t *testing.T) {
	doc := incidentDoc()
	doc["severity"] = "fatal"
	err := ValidateIncident(mustJSON(t, doc))
	requireValidationError(t, err, incidentSchemaName, "$.severity", "is not one of")
}

func TestValidateIncidentBadTimestamp(t *testing.T) {
	doc := incidentDoc()
	doc["observed_at"] = "2026-10-12 09:14"
	err := ValidateIncident(mustJSON(t, doc))
	requireValidationError(t, err, incidentSchemaName, "$.observed_at", "RFC3339")
}

func TestValidateIncidentAdditionalProperty(t *testing.T) {
	doc := incidentDoc()
	doc["owner"] = "alice"
	err := ValidateIncident(mustJSON(t, doc))
	requireValidationError(t, err, incidentSchemaName, "$.owner", "additional property is not allowed")
}

func TestValidateIncidentNotAnObject(t *testing.T) {
	err := ValidateIncident([]byte("[]"))
	requireValidationError(t, err, incidentSchemaName, "$", "want object")
}

func TestValidateIncidentInvalidJSON(t *testing.T) {
	err := ValidateIncident([]byte("{"))
	requireValidationError(t, err, incidentSchemaName, "$", "not valid JSON")
}

func TestValidateIncidentTrailingJSON(t *testing.T) {
	data := append(mustJSON(t, incidentDoc()), []byte(" {}")...)
	err := ValidateIncident(data)
	requireValidationError(t, err, incidentSchemaName, "$", "not valid JSON")
}

func TestValidateReportValid(t *testing.T) {
	if err := ValidateReport(mustJSON(t, reportDoc())); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateReportEmptyHypothesesAllowed(t *testing.T) {
	doc := reportDoc()
	doc["hypotheses"] = []any{}
	doc["evidence"] = []any{}
	doc["missing_data"] = []any{map[string]any{"source": "k8s.events", "reason": "缺少事件", "next_step": "导出 events.json"}}
	if err := ValidateReport(mustJSON(t, doc)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateReportEvidenceMissingContentRef(t *testing.T) {
	doc := reportDoc()
	delete(evidenceItem(doc), "content_ref")
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.evidence[0].content_ref", "required field is missing")
}

func TestValidateReportEvidenceMissingStale(t *testing.T) {
	doc := reportDoc()
	delete(evidenceItem(doc), "stale")
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.evidence[0].stale", "required field is missing")
}

func TestValidateReportEvidenceAdditionalProperty(t *testing.T) {
	doc := reportDoc()
	evidenceItem(doc)["raw"] = "unexpected payload"
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.evidence[0].raw", "additional property is not allowed")
}

func TestValidateReportNullContradictions(t *testing.T) {
	doc := reportDoc()
	hypothesisItem(doc)["contradictions"] = nil
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.hypotheses[0].contradictions", "want array")
}

func TestValidateReportNullHypotheses(t *testing.T) {
	doc := reportDoc()
	doc["hypotheses"] = nil
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.hypotheses", "want array")
}

func TestValidateReportTooManyHypotheses(t *testing.T) {
	doc := reportDoc()
	doc["hypotheses"] = []any{
		map[string]any{"rank": 1, "statement": "a", "confidence": "high", "evidence_ids": []any{"ev-001"}, "contradictions": []any{}},
		map[string]any{"rank": 2, "statement": "b", "confidence": "mid", "evidence_ids": []any{"ev-001"}, "contradictions": []any{}},
		map[string]any{"rank": 3, "statement": "c", "confidence": "low", "evidence_ids": []any{"ev-001"}, "contradictions": []any{}},
		map[string]any{"rank": 4, "statement": "d", "confidence": "low", "evidence_ids": []any{"ev-001"}, "contradictions": []any{}},
	}
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.hypotheses", "maximum is 3")
}

func TestValidateReportEmptyEvidenceIDs(t *testing.T) {
	doc := reportDoc()
	hypothesisItem(doc)["evidence_ids"] = []any{}
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.hypotheses[0].evidence_ids", "minimum is 1")
}

func TestValidateReportBadConfidence(t *testing.T) {
	doc := reportDoc()
	hypothesisItem(doc)["confidence"] = "certain"
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.hypotheses[0].confidence", "is not one of")
}

func TestValidateReportBadRank(t *testing.T) {
	doc := reportDoc()
	hypothesisItem(doc)["rank"] = 0
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.hypotheses[0].rank", "less than minimum")
}

func TestValidateReportNonIntegerRank(t *testing.T) {
	doc := reportDoc()
	hypothesisItem(doc)["rank"] = 1.5
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.hypotheses[0].rank", "want integer")
}

func TestValidateReportSchemaVersionConst(t *testing.T) {
	doc := reportDoc()
	doc["schema_version"] = "2.0"
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.schema_version", "does not equal required constant")
}

func TestValidateReportMissingSection(t *testing.T) {
	doc := reportDoc()
	delete(doc, "limitations")
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.limitations", "required field is missing")
}

func TestValidateReportBadGeneratedAt(t *testing.T) {
	doc := reportDoc()
	doc["generated_at"] = "2026-10-12"
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.generated_at", "RFC3339")
}

func TestValidateReportIncidentRef(t *testing.T) {
	doc := reportDoc()
	doc["incident"].(map[string]any)["namespace"] = ""
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.incident.namespace", "minimum")
}

func TestValidateReportIncidentRefMissingField(t *testing.T) {
	doc := reportDoc()
	delete(doc["incident"].(map[string]any), "cluster")
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.incident.cluster", "required field is missing")
}

func TestValidateReportVersionsValueType(t *testing.T) {
	doc := reportDoc()
	doc["versions"] = map[string]any{"cli": 1}
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.versions.cli", "want string")
}

func TestValidateReportAdditionalTopLevelProperty(t *testing.T) {
	doc := reportDoc()
	doc["extra"] = true
	err := ValidateReport(mustJSON(t, doc))
	requireValidationError(t, err, reportSchemaName, "$.extra", "additional property is not allowed")
}

func TestValidateReportInvalidJSON(t *testing.T) {
	err := ValidateReport([]byte("not json"))
	requireValidationError(t, err, reportSchemaName, "$", "not valid JSON")
}

func TestValidateReportNonObject(t *testing.T) {
	err := ValidateReport([]byte("\"report\""))
	requireValidationError(t, err, reportSchemaName, "$", "want object")
}

func TestValidateErrorFormat(t *testing.T) {
	doc := incidentDoc()
	doc["severity"] = "fatal"
	err := ValidateIncident(mustJSON(t, doc))
	if err == nil {
		t.Fatal("expected error")
	}
	message := err.Error()
	for _, want := range []string{incidentSchemaName, "$.severity", "is not one of"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not contain %q", message, want)
		}
	}
}

func evidenceItem(doc map[string]any) map[string]any {
	return doc["evidence"].([]any)[0].(map[string]any)
}

func hypothesisItem(doc map[string]any) map[string]any {
	return doc["hypotheses"].([]any)[0].(map[string]any)
}
