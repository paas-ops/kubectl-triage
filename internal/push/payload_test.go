package push

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/bundle"
	"github.com/paas-ops/kubectl-triage/internal/report"
)

func fixedConfig() Config {
	cfg := validConfig()
	cfg.PushID = "11111111-1111-4111-8111-111111111111"
	cfg.Now = func() time.Time {
		return time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)
	}
	return cfg
}

// testBundleDir scaffolds a bundle and writes a schema-valid report.json with
// two evidence items, mirroring the analyze output shape.
func testBundleDir(t *testing.T) (string, *report.Report) {
	t.Helper()
	dir := t.TempDir()
	if err := bundle.Scaffold(dir); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	b, err := bundle.Load(dir)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	rep := report.New(b.Incident, "test-cli")
	rep.GeneratedAt = "2026-10-12T09:25:00Z"
	rep.Evidence = []report.Evidence{
		{
			ID: "ev-001", Source: "k8s.events",
			ObservedAt: b.Incident.ObservedAt, CollectedAt: b.Incident.CollectedAt,
			Summary: "Warning OOMKilled", ContentRef: "k8s/events.json#items/0",
			SHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		{
			ID: "ev-002", Source: "logs",
			ObservedAt: b.Incident.ObservedAt, CollectedAt: b.Incident.CollectedAt,
			Summary: "日志摘录", ContentRef: "logs/app.log#L1",
			SHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		},
	}
	rep.Hypotheses = []report.Hypothesis{{
		Rank: 1, Statement: "容器内存不足", Confidence: "high",
		EvidenceIDs: []string{"ev-001"}, Contradictions: []report.Contradiction{},
	}}
	if err := rep.WriteJSON(filepath.Join(dir, "report.json")); err != nil {
		t.Fatalf("write report: %v", err)
	}
	return dir, rep
}

func TestBuildDefaultPayload(t *testing.T) {
	dir, rep := testBundleDir(t)
	cfg := fixedConfig()
	payload, result, err := Build(dir, cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if payload.ProtocolVersion != ProtocolVersion {
		t.Fatalf("protocol_version = %q", payload.ProtocolVersion)
	}
	if payload.PushID != cfg.PushID {
		t.Fatalf("push_id = %q, want injected %q", payload.PushID, cfg.PushID)
	}
	if payload.CreatedAt != "2026-10-12T09:30:00Z" {
		t.Fatalf("created_at = %q", payload.CreatedAt)
	}
	if payload.CLI.Version != "test-cli" {
		t.Fatalf("cli.version = %q", payload.CLI.Version)
	}
	if payload.ReportSHA256 != sha256Prefixed(payload.Report) {
		t.Fatalf("report_sha256 does not match report bytes")
	}
	if len(payload.Evidence) != len(rep.Evidence) {
		t.Fatalf("evidence refs = %d, want %d", len(payload.Evidence), len(rep.Evidence))
	}
	for i, ref := range payload.Evidence {
		want := rep.Evidence[i]
		if ref.ID != want.ID || ref.Source != want.Source || ref.ContentRef != want.ContentRef || ref.SHA256 != want.SHA256 {
			t.Fatalf("evidence[%d] = %+v, want fields from %+v", i, ref, want)
		}
	}
	if payload.Bundle.Included {
		t.Fatal("bundle must not be included by default")
	}
	if payload.Bundle.ContentBase64 != "" || payload.Bundle.SizeBytes != 0 {
		t.Fatalf("default bundle should be empty: %+v", payload.Bundle)
	}
	if result.EvidenceCount != len(rep.Evidence) || result.BundleIncluded {
		t.Fatalf("unexpected summary result: %+v", result)
	}

	data, err := payload.JSON()
	if err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if bytes.Contains(data, []byte(cfg.Token)) {
		t.Fatal("payload JSON must never contain the enterprise token")
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode payload json: %v", err)
	}
	for _, field := range []string{"protocol_version", "push_id", "created_at", "cli", "report", "report_sha256", "evidence", "bundle"} {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("payload is missing %q", field)
		}
	}
}

func TestBuildDeterministicIncludeEvidence(t *testing.T) {
	dir, _ := testBundleDir(t)
	cfg := fixedConfig()
	cfg.IncludeEvidence = true

	payload1, result1, err := Build(dir, cfg)
	if err != nil {
		t.Fatalf("build 1: %v", err)
	}
	payload2, _, err := Build(dir, cfg)
	if err != nil {
		t.Fatalf("build 2: %v", err)
	}
	first, _ := json.Marshal(payload1)
	second, _ := json.Marshal(payload2)
	if !bytes.Equal(first, second) {
		t.Fatal("repeated builds must produce identical payloads")
	}
	if !payload1.Bundle.Included {
		t.Fatal("bundle should be included")
	}
	if payload1.Bundle.ContentType != "application/gzip" {
		t.Fatalf("content_type = %q", payload1.Bundle.ContentType)
	}
	archive, err := base64.StdEncoding.DecodeString(payload1.Bundle.ContentBase64)
	if err != nil {
		t.Fatalf("decode content_base64: %v", err)
	}
	if int64(len(archive)) != payload1.Bundle.SizeBytes {
		t.Fatalf("size_bytes = %d, archive = %d", payload1.Bundle.SizeBytes, len(archive))
	}
	if payload1.Bundle.SHA256 != sha256Prefixed(archive) {
		t.Fatal("bundle sha256 does not match archive")
	}
	if result1.BundleSize != int64(len(archive)) || !result1.BundleIncluded {
		t.Fatalf("unexpected result: %+v", result1)
	}

	entries, order := decodeBundleEntries(t, archive)
	if len(order) != len(entries) {
		t.Fatalf("duplicate tar entries: order=%v entries=%d", order, len(entries))
	}
	if !sort.StringsAreSorted(order) {
		t.Fatalf("tar entries must be sorted by path: %v", order)
	}
	for _, want := range []string{"incident.yaml", "k8s/events.json", "logs/app.log", "metrics/memory.json"} {
		if _, ok := entries[want]; !ok {
			t.Fatalf("bundle is missing %q (got %v)", want, order)
		}
	}
	for _, excluded := range []string{"report.json", "report.md"} {
		if _, ok := entries[excluded]; ok {
			t.Fatalf("bundle must exclude %q", excluded)
		}
	}
	for name := range entries {
		if strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
			t.Fatalf("unsafe tar path %q", name)
		}
	}
}

func TestBuildBundleSizeLimit(t *testing.T) {
	dir, _ := testBundleDir(t)
	cfg := fixedConfig()
	cfg.IncludeEvidence = true
	cfg.MaxBundleBytes = 1
	_, _, err := Build(dir, cfg)
	if err == nil {
		t.Fatal("expected bundle size error")
	}
	var configErr *ConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("expected *ConfigError, got %T: %v", err, err)
	}
	if !strings.Contains(configErr.Reason, "max-bundle-bytes") {
		t.Fatalf("unexpected reason: %q", configErr.Reason)
	}
}

func TestBuildMissingReport(t *testing.T) {
	dir := t.TempDir()
	if err := bundle.Scaffold(dir); err != nil {
		t.Fatal(err)
	}
	_, _, err := Build(dir, fixedConfig())
	if err == nil {
		t.Fatal("expected missing report error")
	}
	if !strings.Contains(err.Error(), "report.json") || !strings.Contains(err.Error(), "analyze") {
		t.Fatalf("error should hint at running analyze: %v", err)
	}
}

func TestBuildInvalidReport(t *testing.T) {
	dir := t.TempDir()
	if err := bundle.Scaffold(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), []byte("{not valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Build(dir, fixedConfig())
	if err == nil {
		t.Fatal("expected invalid report error")
	}
	if !strings.Contains(err.Error(), "report.json") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildSchemaInvalidReport(t *testing.T) {
	dir := t.TempDir()
	if err := bundle.Scaffold(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Build(dir, fixedConfig()); err == nil {
		t.Fatal("expected schema validation error")
	}
}

func TestBuildSensitiveReportBlocked(t *testing.T) {
	dir, rep := testBundleDir(t)
	const secret = "password=supersecret123"
	rep.Summary = secret
	if err := rep.WriteJSON(filepath.Join(dir, "report.json")); err != nil {
		t.Fatal(err)
	}
	_, _, err := Build(dir, fixedConfig())
	if err == nil {
		t.Fatal("expected security error")
	}
	var securityErr *SecurityError
	if !errors.As(err, &securityErr) {
		t.Fatalf("expected *SecurityError, got %T: %v", err, err)
	}
	if strings.Contains(err.Error(), "supersecret123") {
		t.Fatal("security error must not echo the secret")
	}
}

func TestBuildSensitiveBundleBlocked(t *testing.T) {
	dir, _ := testBundleDir(t)
	if err := os.WriteFile(filepath.Join(dir, "logs", "leak.log"), []byte("password=supersecret123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Build(dir, fixedConfig())
	if err == nil {
		t.Fatal("expected security error")
	}
	var securityErr *SecurityError
	if !errors.As(err, &securityErr) {
		t.Fatalf("expected *SecurityError, got %T: %v", err, err)
	}
	if strings.Contains(err.Error(), "supersecret123") {
		t.Fatal("security error must not echo the secret")
	}
}

func TestBuildRejectsInvalidConfig(t *testing.T) {
	dir, _ := testBundleDir(t)
	cfg := fixedConfig()
	cfg.Endpoint = ""
	_, _, err := Build(dir, cfg)
	if err == nil {
		t.Fatal("expected config error")
	}
	var configErr *ConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("expected *ConfigError, got %T", err)
	}
}

func decodeBundleEntries(t *testing.T, archive []byte) (map[string][]byte, []string) {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()
	tw := tar.NewReader(gz)
	entries := map[string][]byte{}
	order := make([]string, 0, 8)
	for {
		header, err := tw.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		data, err := io.ReadAll(tw)
		if err != nil {
			t.Fatalf("tar read %s: %v", header.Name, err)
		}
		entries[header.Name] = data
		order = append(order, header.Name)
	}
	return entries, order
}
