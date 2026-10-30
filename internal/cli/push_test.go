package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/push"
	"github.com/paas-ops/kubectl-triage/internal/report"
)

func pushReadyDir(t *testing.T) string {
	t.Helper()
	dir := scaffoldDir(t)
	res := run(t, "analyze", dir, "--offline")
	requireCode(t, res, 0)
	return dir
}

func setPushEnv(t *testing.T, endpoint, token string) {
	t.Helper()
	t.Setenv(push.EndpointEnv, endpoint)
	t.Setenv(push.TokenEnv, token)
}

func writePushAccepted(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"accepted":         true,
		"protocol_version": "1.0",
		"incident_id":      "inc-123",
	})
}

func writePushRemoteError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
}

func decodePushPayload(t *testing.T, body []byte) push.Payload {
	t.Helper()
	var payload push.Payload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode push payload: %v", err)
	}
	return payload
}

func TestPushCommandSuccess(t *testing.T) {
	dir := pushReadyDir(t)
	var hits int32
	var mu sync.Mutex
	var headers http.Header
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		mu.Lock()
		headers = r.Header.Clone()
		body, _ = io.ReadAll(r.Body)
		mu.Unlock()
		writePushAccepted(w, http.StatusOK)
	}))
	defer server.Close()

	setPushEnv(t, "", "super-secret-token")
	res := run(t, "push", dir, "--endpoint", server.URL)
	requireCode(t, res, 0)
	if !strings.Contains(res.out, "推送成功") {
		t.Fatalf("stdout should report success: %q", res.out)
	}
	if strings.Contains(res.out, "super-secret-token") || strings.Contains(res.errString(), "super-secret-token") {
		t.Fatal("token must never be printed")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := headers.Get("Authorization"); got != "Bearer super-secret-token" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := headers.Get("User-Agent"); got != "kubectl-triage/test" {
		t.Fatalf("User-Agent = %q", got)
	}
	payload := decodePushPayload(t, body)
	if payload.ProtocolVersion != push.ProtocolVersion {
		t.Fatalf("protocol_version = %q", payload.ProtocolVersion)
	}
	if got := headers.Get("Idempotency-Key"); got != payload.PushID {
		t.Fatalf("Idempotency-Key %q differs from push_id %q", got, payload.PushID)
	}
	if payload.CLI.Version != "test" {
		t.Fatalf("cli.version = %q", payload.CLI.Version)
	}
	if len(payload.Evidence) == 0 {
		t.Fatal("expected evidence hash references")
	}
	if payload.Bundle.Included {
		t.Fatal("default push must not upload the evidence bundle")
	}
	if !strings.HasPrefix(payload.ReportSHA256, "sha256:") {
		t.Fatalf("report_sha256 = %q", payload.ReportSHA256)
	}
	if !strings.Contains(res.out, "推送成功") {
		t.Fatalf("unexpected stdout: %q", res.out)
	}
}

func (r runResult) errString() string {
	if r.err == nil {
		return ""
	}
	return r.err.Error()
}

func TestPushUsesEnvironmentEndpoint(t *testing.T) {
	dir := pushReadyDir(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writePushAccepted(w, http.StatusOK)
	}))
	defer server.Close()

	setPushEnv(t, server.URL, "token-env")
	requireCode(t, run(t, "push", dir), 0)
}

func TestPushConfigurationErrors(t *testing.T) {
	dir := pushReadyDir(t)

	// Missing token: no network request may happen.
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		writePushAccepted(w, http.StatusOK)
	}))
	defer server.Close()

	setPushEnv(t, server.URL, "")
	res := run(t, "push", dir)
	requireCode(t, res, 1)
	if !strings.Contains(res.errString(), "令牌") {
		t.Fatalf("expected token configuration error, got %v", res.err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("configuration error must not touch the network, got %d request(s)", got)
	}

	// Missing endpoint.
	setPushEnv(t, "", "token")
	res = run(t, "push", dir)
	requireCode(t, res, 1)
	if !strings.Contains(res.errString(), "端点") {
		t.Fatalf("expected endpoint configuration error, got %v", res.err)
	}

	// Non-loopback plain http is rejected before any request.
	setPushEnv(t, "", "token")
	res = run(t, "push", dir, "--endpoint", "http://enterprise.example.test")
	requireCode(t, res, 1)
	if !strings.Contains(res.errString(), "loopback") {
		t.Fatalf("expected loopback hint, got %v", res.err)
	}
}

func TestPushNonRetryableRejections(t *testing.T) {
	dir := pushReadyDir(t)
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var hits int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&hits, 1)
				if status == http.StatusUnauthorized {
					writePushRemoteError(w, status, "auth_failed", "invalid token super-secret-token")
					return
				}
				writePushRemoteError(w, status, "rejected", "request rejected")
			}))
			defer server.Close()

			setPushEnv(t, "", "super-secret-token")
			res := run(t, "push", dir, "--endpoint", server.URL)
			requireCode(t, res, 1)
			if got := atomic.LoadInt32(&hits); got != 1 {
				t.Fatalf("4xx must not be retried, got %d request(s)", got)
			}
			if strings.Contains(res.errString(), "super-secret-token") || strings.Contains(res.out, "super-secret-token") {
				t.Fatal("token must never be printed")
			}
		})
	}
}

func TestPushRetriesServerErrors(t *testing.T) {
	dir := pushReadyDir(t)
	var hits int32
	var mu sync.Mutex
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		if n < 3 {
			writePushRemoteError(w, http.StatusInternalServerError, "internal", "try again")
			return
		}
		writePushAccepted(w, http.StatusOK)
	}))
	defer server.Close()

	setPushEnv(t, "", "token")
	requireCode(t, run(t, "push", dir, "--endpoint", server.URL), 0)
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("request count = %d, want 3", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for i := 1; i < len(keys); i++ {
		if keys[i] != keys[0] {
			t.Fatalf("retries must reuse the Idempotency-Key: %v", keys)
		}
	}
}

func TestPushServerErrorsExhausted(t *testing.T) {
	dir := pushReadyDir(t)
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		writePushRemoteError(w, http.StatusBadGateway, "upstream", "boom")
	}))
	defer server.Close()

	setPushEnv(t, "", "token")
	requireCode(t, run(t, "push", dir, "--endpoint", server.URL), 3)
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("request count = %d, want 3", got)
	}
}

func TestPushTimeoutExitsThree(t *testing.T) {
	dir := pushReadyDir(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		writePushAccepted(w, http.StatusOK)
	}))
	defer server.Close()

	setPushEnv(t, "", "token")
	res := run(t, "push", dir, "--endpoint", server.URL, "--timeout", "100ms")
	requireCode(t, res, 3)
}

func TestPushDryRun(t *testing.T) {
	dir := pushReadyDir(t)
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		writePushAccepted(w, http.StatusOK)
	}))
	defer server.Close()

	outPath := filepath.Join(t.TempDir(), "payload.json")
	setPushEnv(t, "", "super-secret-token")
	res := run(t, "push", dir, "--dry-run", "--endpoint", server.URL, "--out", outPath)
	requireCode(t, res, 0)
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("dry-run must not touch the network, got %d request(s)", got)
	}
	for _, want := range []string{"推送摘要", "未联网", "协议版本", "报告 sha256", "证据条数"} {
		if !strings.Contains(res.out, want) {
			t.Fatalf("dry-run output missing %q: %q", want, res.out)
		}
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read --out payload: %v", err)
	}
	if strings.Contains(string(data), "super-secret-token") {
		t.Fatal("payload JSON must not contain the token")
	}
	var payload push.Payload
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode --out payload: %v", err)
	}
	if payload.ProtocolVersion != push.ProtocolVersion || payload.PushID == "" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.Bundle.Included {
		t.Fatal("dry-run default should not include the bundle")
	}
}

func TestPushIncludeEvidence(t *testing.T) {
	dir := pushReadyDir(t)

	runPush := func(extraArgs ...string) (push.Payload, http.Header) {
		t.Helper()
		var mu sync.Mutex
		var body []byte
		var headers http.Header
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			headers = r.Header.Clone()
			body, _ = io.ReadAll(r.Body)
			mu.Unlock()
			writePushAccepted(w, http.StatusOK)
		}))
		defer server.Close()
		args := append([]string{"push", dir, "--endpoint", server.URL}, extraArgs...)
		setPushEnv(t, "", "token")
		requireCode(t, run(t, args...), 0)
		mu.Lock()
		defer mu.Unlock()
		return decodePushPayload(t, body), headers
	}

	// Default: hash-only payload.
	payload, _ := runPush()
	if payload.Bundle.Included {
		t.Fatal("default push must not include the evidence bundle")
	}
	if payload.Bundle.ContentBase64 != "" || payload.Bundle.SizeBytes != 0 {
		t.Fatalf("default bundle should be empty: %+v", payload.Bundle)
	}

	// --include-evidence: deterministic tar.gz with sha/size and contents.
	payload, _ = runPush("--include-evidence")
	if !payload.Bundle.Included || payload.Bundle.ContentType != "application/gzip" {
		t.Fatalf("unexpected bundle reference: %+v", payload.Bundle)
	}
	archive, err := base64.StdEncoding.DecodeString(payload.Bundle.ContentBase64)
	if err != nil {
		t.Fatalf("decode bundle base64: %v", err)
	}
	if int64(len(archive)) != payload.Bundle.SizeBytes {
		t.Fatalf("size_bytes = %d, archive = %d", payload.Bundle.SizeBytes, len(archive))
	}
	if !strings.HasPrefix(payload.Bundle.SHA256, "sha256:") {
		t.Fatalf("bundle sha256 = %q", payload.Bundle.SHA256)
	}
	entries := decodePushBundle(t, archive)
	for _, want := range []string{"incident.yaml", "k8s/events.json", "logs/app.log", "metrics/memory.json"} {
		if _, ok := entries[want]; !ok {
			t.Fatalf("bundle missing %q", want)
		}
	}
	for _, excluded := range []string{"report.json", "report.md"} {
		if _, ok := entries[excluded]; ok {
			t.Fatalf("bundle must exclude %q", excluded)
		}
	}

	// Evidence hashes in the payload must match the local report.
	rep, err := report.Load(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatalf("load report: %v", err)
	}
	if len(payload.Evidence) != len(rep.Evidence) {
		t.Fatalf("payload evidence = %d, report = %d", len(payload.Evidence), len(rep.Evidence))
	}
	for i, ref := range payload.Evidence {
		if ref.ID != rep.Evidence[i].ID || ref.SHA256 != rep.Evidence[i].SHA256 {
			t.Fatalf("evidence[%d] mismatch: %+v vs %+v", i, ref, rep.Evidence[i])
		}
	}
}

func decodePushBundle(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gzip bundle: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	entries := map[string][]byte{}
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar bundle: %v", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read tar entry %s: %v", header.Name, err)
		}
		entries[header.Name] = data
	}
	return entries
}

func TestPushMissingReportExitsOne(t *testing.T) {
	dir := scaffoldDir(t)
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		writePushAccepted(w, http.StatusOK)
	}))
	defer server.Close()

	setPushEnv(t, "", "token")
	res := run(t, "push", dir, "--endpoint", server.URL)
	requireCode(t, res, 1)
	if !strings.Contains(res.errString(), "report.json") || !strings.Contains(res.errString(), "analyze") {
		t.Fatalf("error should hint at running analyze: %v", res.err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("missing report must not touch the network, got %d request(s)", got)
	}
}

func TestPushInvalidReportExitsOne(t *testing.T) {
	dir := scaffoldDir(t)
	writeFile(t, dir, "report.json", "{}")
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer server.Close()

	setPushEnv(t, "", "token")
	requireCode(t, run(t, "push", dir, "--endpoint", server.URL), 1)
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("invalid report must not touch the network, got %d request(s)", got)
	}
}

func TestPushSensitiveReportBlocked(t *testing.T) {
	dir := pushReadyDir(t)
	setPushEnv(t, "", "token")

	path := filepath.Join(dir, "report.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	const secret = "password=supersecret123"
	doc["summary"] = secret
	modified, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, modified, 0o644); err != nil {
		t.Fatal(err)
	}

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		writePushAccepted(w, http.StatusOK)
	}))
	defer server.Close()

	res := run(t, "push", dir, "--endpoint", server.URL)
	requireCode(t, res, 4)
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("sensitive report must not be sent, got %d request(s)", got)
	}
	if strings.Contains(res.errString(), secret) || strings.Contains(res.out, secret) {
		t.Fatal("security error must not echo the secret")
	}
}

func TestPushBundleSizeLimitExitsOne(t *testing.T) {
	dir := pushReadyDir(t)
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer server.Close()

	setPushEnv(t, "", "token")
	res := run(t, "push", dir, "--endpoint", server.URL, "--include-evidence", "--max-bundle-bytes", "10")
	requireCode(t, res, 1)
	if !strings.Contains(res.errString(), "max-bundle-bytes") {
		t.Fatalf("expected bundle size error, got %v", res.err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("oversized bundle must not touch the network, got %d request(s)", got)
	}
}

func TestPushSensitiveBundleBlocked(t *testing.T) {
	dir := pushReadyDir(t)
	writeFile(t, dir, "logs/leak.log", "password=supersecret123\n")
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		writePushAccepted(w, http.StatusOK)
	}))
	defer server.Close()

	setPushEnv(t, "", "super-secret-token")
	res := run(t, "push", dir, "--endpoint", server.URL)
	requireCode(t, res, 4)
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("sensitive bundle must not be sent, got %d request(s)", got)
	}
	if strings.Contains(res.errString(), "supersecret123") || strings.Contains(res.out, "supersecret123") {
		t.Fatal("security error must not echo the secret")
	}
}
