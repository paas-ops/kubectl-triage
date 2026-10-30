package telemetry

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// isolateEventEnv clears the environment knobs that influence Record.
func isolateEventEnv(t *testing.T) {
	t.Helper()
	t.Setenv(TelemetryEnv, "")
	t.Setenv(EndpointEnv, "")
}

type capturedRequest struct {
	method      string
	contentType string
	body        []byte
}

// newRecordingServer records every request body and replies with status.
func newRecordingServer(t *testing.T, status int) (*httptest.Server, <-chan capturedRequest) {
	t.Helper()
	requests := make(chan capturedRequest, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- capturedRequest{method: r.Method, contentType: r.Header.Get("Content-Type"), body: body}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, requests
}

// newCountingServer counts requests and replies with status.
func newCountingServer(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &count
}

func sampleEvent(t *testing.T) Event {
	t.Helper()
	id, err := newInstallID()
	if err != nil {
		t.Fatalf("newInstallID: %v", err)
	}
	return Event{
		InstallID:     id,
		Event:         "analyze",
		Version:       "9.9.9-test",
		OS:            "linux",
		Arch:          "amd64",
		Mode:          "offline",
		FaultClass:    "",
		DurationMS:    42,
		EvidenceCount: 3,
		Outcome:       "ok",
	}
}

func enabledConfig(t *testing.T, endpoint string) Config {
	t.Helper()
	return Config{Enabled: true, InstallID: sampleEvent(t).InstallID, Endpoint: endpoint}
}

func knownEventField(name string) bool {
	for _, field := range eventFieldNames {
		if field == name {
			return true
		}
	}
	return false
}

func TestEventJSONAlwaysContainsEveryWhitelistField(t *testing.T) {
	raw, err := json.Marshal(Event{})
	if err != nil {
		t.Fatalf("marshal empty event: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if len(payload) != len(eventFieldNames) {
		t.Fatalf("empty event marshals to %d fields, want %d: %s", len(payload), len(eventFieldNames), raw)
	}
	for _, name := range eventFieldNames {
		if _, ok := payload[name]; !ok {
			t.Fatalf("event JSON is missing whitelist field %q: %s", name, raw)
		}
	}
}

func TestEventPayloadCarriesExactlyTheWhitelist(t *testing.T) {
	isolateEventEnv(t)
	srv, requests := newRecordingServer(t, http.StatusNoContent)
	event := sampleEvent(t)
	Record(Config{Enabled: true, InstallID: event.InstallID, Endpoint: srv.URL}, event)

	var req capturedRequest
	select {
	case req = <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("expected one telemetry POST, got none")
	}
	if len(requests) != 0 {
		t.Fatalf("expected exactly one POST, still %d pending", len(requests))
	}
	if req.method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.method)
	}
	if req.contentType != "application/json" {
		t.Fatalf("content-type = %q, want application/json", req.contentType)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(req.body, &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if len(payload) != len(eventFieldNames) {
		t.Fatalf("payload has %d fields, want exactly %d: %s", len(payload), len(eventFieldNames), req.body)
	}
	for _, name := range eventFieldNames {
		if _, ok := payload[name]; !ok {
			t.Fatalf("payload missing whitelist field %q: %s", name, req.body)
		}
	}
	for name := range payload {
		if !knownEventField(name) {
			t.Fatalf("payload contains non-whitelisted field %q: %s", name, req.body)
		}
	}

	var got Event
	if err := json.Unmarshal(req.body, &got); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if got != event {
		t.Fatalf("round-tripped event = %+v, want %+v", got, event)
	}

	// No incident identifiers or credentials may ever appear in the payload.
	for _, forbidden := range []string{
		"cluster", "namespace", "alertname", "workload", "hostname",
		"kubeconfig", "api_key", "token", "secret", "password",
	} {
		if strings.Contains(string(req.body), forbidden) {
			t.Fatalf("payload must not contain %q: %s", forbidden, req.body)
		}
	}
}

func TestRecordSendsExactlyOnce(t *testing.T) {
	isolateEventEnv(t)
	srv, count := newCountingServer(t, http.StatusNoContent)
	event := sampleEvent(t)
	Record(Config{Enabled: true, InstallID: event.InstallID, Endpoint: srv.URL}, event)
	if got := count.Load(); got != 1 {
		t.Fatalf("request count = %d, want exactly 1", got)
	}
}

func TestRecordDisabledSendsNothingAndWritesNothing(t *testing.T) {
	isolateEventEnv(t)
	dir := filepath.Join(t.TempDir(), "telemetry-config")
	t.Setenv(ConfigDirEnv, dir)
	srv, count := newCountingServer(t, http.StatusNoContent)
	t.Setenv(EndpointEnv, srv.URL) // endpoint configured, telemetry still off
	event := sampleEvent(t)
	Record(Config{Enabled: false, InstallID: event.InstallID, Endpoint: srv.URL}, event)
	if got := count.Load(); got != 0 {
		t.Fatalf("disabled telemetry made %d request(s), want 0", got)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("disabled telemetry must not write a config: %v", err)
	}
}

func TestRecordEnvironmentOffOverridesEnabled(t *testing.T) {
	isolateEventEnv(t)
	srv, count := newCountingServer(t, http.StatusNoContent)
	t.Setenv(TelemetryEnv, "off")
	Record(enabledConfig(t, srv.URL), sampleEvent(t))
	if got := count.Load(); got != 0 {
		t.Fatalf("KUBECTL_TRIAGE_TELEMETRY=off made %d request(s), want 0", got)
	}
}

func TestRecordWithoutEndpointSendsNothing(t *testing.T) {
	isolateEventEnv(t)
	oldClient := httpClient
	var calls atomic.Int32
	httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("network must not be used")
	})}
	t.Cleanup(func() { httpClient = oldClient })

	event := sampleEvent(t)
	Record(Config{Enabled: true, InstallID: event.InstallID}, event)
	if got := calls.Load(); got != 0 {
		t.Fatalf("no endpoint made %d HTTP call(s), want 0", got)
	}

	// An unparsable endpoint must also be dropped before touching the network.
	Record(Config{Enabled: true, InstallID: event.InstallID, Endpoint: "://not-a-url"}, event)
	if got := calls.Load(); got != 0 {
		t.Fatalf("invalid endpoint made %d HTTP call(s), want 0", got)
	}
}

func TestRecordUsesEnvironmentEndpoint(t *testing.T) {
	isolateEventEnv(t)
	srv, count := newCountingServer(t, http.StatusNoContent)
	event := sampleEvent(t)
	t.Setenv(EndpointEnv, srv.URL)
	// A broken config endpoint must be overridden by the environment.
	Record(Config{Enabled: true, InstallID: event.InstallID, Endpoint: "http://127.0.0.1:1"}, event)
	if got := count.Load(); got != 1 {
		t.Fatalf("env endpoint request count = %d, want 1", got)
	}
}

func TestRecordDropsWithoutInstallID(t *testing.T) {
	isolateEventEnv(t)
	srv, count := newCountingServer(t, http.StatusNoContent)
	t.Setenv(EndpointEnv, srv.URL)
	Record(Config{Enabled: true, Endpoint: srv.URL}, sampleEvent(t))
	if got := count.Load(); got != 0 {
		t.Fatalf("missing install_id made %d request(s), want 0", got)
	}
}

func TestRecordIgnoresHTTPErrors(t *testing.T) {
	isolateEventEnv(t)
	srv, count := newCountingServer(t, http.StatusInternalServerError)
	Record(enabledConfig(t, srv.URL), sampleEvent(t))
	if got := count.Load(); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}
	// Reaching this line means the 500 was swallowed (best-effort delivery).
}

func TestRecordDoesNotFollowRedirects(t *testing.T) {
	isolateEventEnv(t)
	target, targetCount := newCountingServer(t, http.StatusNoContent)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/collect", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)

	Record(enabledConfig(t, redirector.URL), sampleEvent(t))
	if got := targetCount.Load(); got != 0 {
		t.Fatalf("redirect target received %d request(s), want 0", got)
	}
}

func TestRecordTimeoutIsSilentAndBounded(t *testing.T) {
	isolateEventEnv(t)
	if requestTimeout != 2*time.Second {
		t.Fatalf("documented send timeout = %v, want 2s", requestTimeout)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reading the body to EOF lets net/http start its background read, so a
		// client timeout cancels r.Context() instead of blocking Close forever.
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	start := time.Now()
	record(enabledConfig(t, srv.URL), sampleEvent(t), 50*time.Millisecond)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timed-out send took %v, want <= 1s", elapsed)
	}
}

// roundTripFunc lets tests observe HTTP calls without opening a socket.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
