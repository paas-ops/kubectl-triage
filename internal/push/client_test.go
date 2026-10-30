package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fastRetries(t *testing.T) {
	t.Helper()
	old := retryBackoff
	retryBackoff = []time.Duration{time.Millisecond, 2 * time.Millisecond}
	t.Cleanup(func() { retryBackoff = old })
}

func testConfigForServer(server *httptest.Server) Config {
	cfg := fixedConfig()
	cfg.Endpoint = server.URL
	cfg.Token = "test-token"
	return cfg
}

func writeAccepted(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"accepted":         true,
		"protocol_version": "1.0",
		"incident_id":      "inc-123",
	})
}

func writeRemoteError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
}

func TestRunSuccess(t *testing.T) {
	fastRetries(t)
	dir, _ := testBundleDir(t)

	var hits int32
	var mu sync.Mutex
	var headers http.Header
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/push" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		mu.Lock()
		headers = r.Header.Clone()
		body, _ = io.ReadAll(r.Body)
		mu.Unlock()
		writeAccepted(w, http.StatusOK)
	}))
	defer server.Close()

	cfg := testConfigForServer(server)
	result, err := Run(context.Background(), dir, cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !result.Accepted || result.IncidentID != "inc-123" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.ProtocolVersion != ProtocolVersion {
		t.Fatalf("protocol_version = %q", result.ProtocolVersion)
	}
	if result.PushID != cfg.PushID {
		t.Fatalf("push_id = %q, want %q", result.PushID, cfg.PushID)
	}
	if result.EvidenceCount != 2 {
		t.Fatalf("evidence count = %d, want 2", result.EvidenceCount)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if got := headers.Get("Authorization"); got != "Bearer test-token" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := headers.Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := headers.Get("Idempotency-Key"); got != cfg.PushID {
		t.Fatalf("Idempotency-Key = %q, want %q", got, cfg.PushID)
	}
	if got := headers.Get("User-Agent"); got != "kubectl-triage/test-cli" {
		t.Fatalf("User-Agent = %q", got)
	}
	if strings.Contains(string(body), cfg.Token) {
		t.Fatal("request body must not contain the token")
	}
	var sent Payload
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if sent.ProtocolVersion != ProtocolVersion || sent.PushID != cfg.PushID || sent.CreatedAt != "2026-10-12T09:30:00Z" {
		t.Fatalf("unexpected payload: %+v", sent)
	}
	if sent.ReportSHA256 != sha256Prefixed(sent.Report) {
		t.Fatal("report_sha256 does not match the report object in the request")
	}
	if len(sent.Evidence) != 2 || sent.Bundle.Included {
		t.Fatalf("unexpected payload evidence/bundle: %+v / %+v", sent.Evidence, sent.Bundle)
	}
}

func TestRunNonRetryableRejections(t *testing.T) {
	fastRetries(t)
	dir, _ := testBundleDir(t)

	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity} {
		t.Run(fmt.Sprintf("HTTP%d", status), func(t *testing.T) {
			var hits int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&hits, 1)
				writeRemoteError(w, status, "rejected", "request rejected")
			}))
			defer server.Close()

			cfg := testConfigForServer(server)
			_, err := Run(context.Background(), dir, cfg)
			var remoteErr *RemoteError
			if !errors.As(err, &remoteErr) {
				t.Fatalf("expected *RemoteError, got %T: %v", err, err)
			}
			if remoteErr.StatusCode != status || remoteErr.Code != "rejected" {
				t.Fatalf("unexpected remote error: %+v", remoteErr)
			}
			if got := atomic.LoadInt32(&hits); got != 1 {
				t.Fatalf("4xx must not be retried, got %d request(s)", got)
			}
		})
	}
}

func TestRunRetriesServerErrorsWithStableID(t *testing.T) {
	fastRetries(t)
	dir, _ := testBundleDir(t)

	var hits int32
	var mu sync.Mutex
	var keys []string
	var pushIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		var payload Payload
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		pushIDs = append(pushIDs, payload.PushID)
		mu.Unlock()
		if n < 3 {
			writeRemoteError(w, http.StatusInternalServerError, "internal", "try again")
			return
		}
		writeAccepted(w, http.StatusOK)
	}))
	defer server.Close()

	cfg := testConfigForServer(server)
	result, err := Run(context.Background(), dir, cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !result.Accepted {
		t.Fatalf("expected accepted result, got %+v", result)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("request count = %d, want 3 (initial + 2 retries)", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for i, key := range keys {
		if key != cfg.PushID || pushIDs[i] != cfg.PushID {
			t.Fatalf("attempt %d reused a different push id: key=%q body=%q", i, key, pushIDs[i])
		}
	}
}

func TestRunRetriesTooManyRequests(t *testing.T) {
	fastRetries(t)
	dir, _ := testBundleDir(t)

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			writeRemoteError(w, http.StatusTooManyRequests, "rate_limited", "slow down")
			return
		}
		writeAccepted(w, http.StatusOK)
	}))
	defer server.Close()

	cfg := testConfigForServer(server)
	if _, err := Run(context.Background(), dir, cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("request count = %d, want 2", got)
	}
}

func TestRunExhaustsRetriesOnServerErrors(t *testing.T) {
	fastRetries(t)
	dir, _ := testBundleDir(t)

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		writeRemoteError(w, http.StatusInternalServerError, "internal", "still failing")
	}))
	defer server.Close()

	cfg := testConfigForServer(server)
	_, err := Run(context.Background(), dir, cfg)
	var networkErr *NetworkError
	if !errors.As(err, &networkErr) {
		t.Fatalf("expected *NetworkError, got %T: %v", err, err)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("request count = %d, want 3 (exhausted retries)", got)
	}
}

func TestRunTimeout(t *testing.T) {
	fastRetries(t)
	dir, _ := testBundleDir(t)

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		time.Sleep(300 * time.Millisecond)
		writeAccepted(w, http.StatusOK)
	}))
	defer server.Close()

	cfg := testConfigForServer(server)
	cfg.Timeout = 80 * time.Millisecond
	started := time.Now()
	_, err := Run(context.Background(), dir, cfg)
	var networkErr *NetworkError
	if !errors.As(err, &networkErr) {
		t.Fatalf("expected *NetworkError for timeout, got %T: %v", err, err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("timed-out run should not retry, got %d request(s)", got)
	}
}

func TestRunRejectsRedirects(t *testing.T) {
	fastRetries(t)
	dir, _ := testBundleDir(t)

	var hits int32
	var redirectHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/push" {
			atomic.AddInt32(&hits, 1)
			http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
			return
		}
		atomic.AddInt32(&redirectHits, 1)
		writeAccepted(w, http.StatusOK)
	}))
	defer server.Close()

	cfg := testConfigForServer(server)
	_, err := Run(context.Background(), dir, cfg)
	var networkErr *NetworkError
	if !errors.As(err, &networkErr) {
		t.Fatalf("expected *NetworkError, got %T: %v", err, err)
	}
	if got := atomic.LoadInt32(&redirectHits); got != 0 {
		t.Fatalf("redirect target was followed %d time(s)", got)
	}
}

func TestRunSanitizesTokenFromRemoteErrors(t *testing.T) {
	fastRetries(t)
	dir, _ := testBundleDir(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeRemoteError(w, http.StatusUnauthorized, "auth_failed", "invalid token test-token")
	}))
	defer server.Close()

	cfg := testConfigForServer(server)
	_, err := Run(context.Background(), dir, cfg)
	if err == nil {
		t.Fatal("expected remote error")
	}
	if strings.Contains(err.Error(), "test-token") {
		t.Fatalf("error must not echo the enterprise token: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("expected token to be sanitized: %v", err)
	}
}

func TestRunValidatesConfigBeforeNetwork(t *testing.T) {
	fastRetries(t)
	dir, _ := testBundleDir(t)
	cfg := fixedConfig()
	cfg.Endpoint = "http://not-loopback.example.test"
	_, err := Run(context.Background(), dir, cfg)
	var configErr *ConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("expected *ConfigError, got %T: %v", err, err)
	}
}

func TestSendRejectsNilPayload(t *testing.T) {
	cfg := fixedConfig()
	_, err := Send(context.Background(), cfg, nil)
	var configErr *ConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("expected *ConfigError, got %T: %v", err, err)
	}
}
