package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// Event is the complete and frozen telemetry payload. Every field is always
// present (no omitempty): the JSON key set is exactly the ADR-005 D6 whitelist
// and no other data may be added. Incident content, cluster/namespace/alert
// names, logs, evidence bodies, file paths, hostnames, and model keys are never
// representable here.
type Event struct {
	InstallID     string `json:"install_id"`
	Event         string `json:"event"`
	Version       string `json:"version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Mode          string `json:"mode"`
	FaultClass    string `json:"fault_class"`
	DurationMS    int64  `json:"duration_ms"`
	EvidenceCount int    `json:"evidence_count"`
	Outcome       string `json:"outcome"`
}

// eventFieldNames is the frozen, complete payload whitelist (ADR-005 D6).
var eventFieldNames = []string{
	"install_id", "event", "version", "os", "arch",
	"mode", "fault_class", "duration_ms", "evidence_count", "outcome",
}

// requestTimeout bounds a single best-effort send. Delivery is never retried.
const requestTimeout = 2 * time.Second

// httpClient never follows redirects so a configured endpoint cannot bounce the
// pseudonymous install ID to another host.
var httpClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Record sends one anonymous event on a best-effort basis.
//
// It never returns an error and never writes to disk. A send happens only when
// telemetry is explicitly enabled, the environment does not force it off, an
// install ID and an endpoint exist, and the HTTP request completes within
// requestTimeout (2s). Every failure is silent; the caller's exit code, output,
// and files are never affected.
func Record(cfg Config, ev Event) {
	record(cfg, ev, requestTimeout)
}

// record is Record with an injectable timeout for deterministic tests.
func record(cfg Config, ev Event, timeout time.Duration) {
	if !cfg.Enabled || DisabledByEnv() {
		// The opt-in gate comes first: zero requests, zero writes.
		return
	}
	endpoint := effectiveEndpoint(cfg)
	if endpoint == "" || cfg.InstallID == "" {
		// Nothing to send to, or no pseudonymous ID: drop the event silently.
		return
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "kubectl-triage-telemetry")

	resp, err := httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	// Drain a bounded amount so the connection can be reused; the status code
	// is deliberately ignored because delivery is best-effort.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
}
