// Package alertmanager imports Alertmanager alert exports as the bundle's
// alert.json.
//
// It accepts the Alertmanager webhook payload, a bare alert array (as returned
// by /api/v2/alerts) and the /api/v1/alerts envelope. Only per-alert diagnostic
// fields are kept; webhook envelope fields that depend on receiver/grouping
// configuration (version, receiver, groupKey, externalURL, commonLabels,
// commonAnnotations, ...) are dropped because they are not incident evidence.
//
// The normalized file is deterministic: alerts are sorted by startsAt, then
// fingerprint, then labels, and timestamps are rendered as RFC3339Nano UTC.
package alertmanager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/importers"
)

const (
	sourceName = "alertmanager"
	fileName   = "alert.json"
)

func init() { importers.Register(importer{}) }

type importer struct{}

func (importer) Name() string        { return sourceName }
func (importer) DefaultName() string { return "" }

func (importer) Import(in importers.Input) (*importers.Output, error) { return Import(in) }

// Import normalizes one Alertmanager payload into alert.json.
func Import(in importers.Input) (*importers.Output, error) {
	alerts, err := parse(in.Data)
	if err != nil {
		return nil, err
	}
	doc := document{Status: overallStatus(alerts), Alerts: alerts}
	data, err := importers.MarshalJSON(doc)
	if err != nil {
		return nil, fmt.Errorf("alertmanager: encode %s: %w", fileName, err)
	}
	return importers.Single(fileName, data), nil
}

// document is the normalized alert.json layout.
type document struct {
	Status string  `json:"status,omitempty"`
	Alerts []alert `json:"alerts"`
}

// alert keeps only the per-alert fields that help diagnosis.
type alert struct {
	Status       string            `json:"status,omitempty"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	StartsAt     string            `json:"startsAt,omitempty"`
	EndsAt       string            `json:"endsAt,omitempty"`
	GeneratorURL string            `json:"generatorURL,omitempty"`
	Fingerprint  string            `json:"fingerprint,omitempty"`
}

// rawAlert mirrors the subset of Alertmanager alert JSON we consume.
type rawAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
}

// normalized carries sort keys alongside the rendered alert.
type normalized struct {
	alert
	starts time.Time
	canon  string
}

func parse(data []byte) ([]alert, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("alertmanager: empty input")
	}
	raws, err := decodeAlerts(trimmed)
	if err != nil {
		return nil, err
	}
	normalizedAlerts := make([]normalized, 0, len(raws))
	for i, raw := range raws {
		n, err := normalize(raw)
		if err != nil {
			return nil, fmt.Errorf("alertmanager: alert %d: %w", i, err)
		}
		normalizedAlerts = append(normalizedAlerts, n)
	}
	sort.SliceStable(normalizedAlerts, func(i, j int) bool {
		return less(normalizedAlerts[i], normalizedAlerts[j])
	})
	alerts := make([]alert, len(normalizedAlerts))
	for i := range normalizedAlerts {
		alerts[i] = normalizedAlerts[i].alert
	}
	return alerts, nil
}

func decodeAlerts(data []byte) ([]rawAlert, error) {
	switch data[0] {
	case '[':
		var list []rawAlert
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, fmt.Errorf("alertmanager: parse alert list: %w", err)
		}
		return list, nil
	case '{':
		var probe struct {
			Alerts json.RawMessage `json:"alerts"`
			Data   json.RawMessage `json:"data"`
			Labels json.RawMessage `json:"labels"`
		}
		if err := json.Unmarshal(data, &probe); err != nil {
			return nil, fmt.Errorf("alertmanager: parse input: %w", err)
		}
		switch {
		case probe.Alerts != nil:
			var list []rawAlert
			if err := json.Unmarshal(probe.Alerts, &list); err != nil {
				return nil, fmt.Errorf("alertmanager: parse webhook alerts: %w", err)
			}
			return list, nil
		case probe.Data != nil:
			var list []rawAlert
			if err := json.Unmarshal(probe.Data, &list); err != nil {
				return nil, fmt.Errorf("alertmanager: parse API data alerts: %w", err)
			}
			return list, nil
		case probe.Labels != nil:
			var one rawAlert
			if err := json.Unmarshal(data, &one); err != nil {
				return nil, fmt.Errorf("alertmanager: parse alert: %w", err)
			}
			return []rawAlert{one}, nil
		default:
			return nil, fmt.Errorf("alertmanager: unsupported JSON object: want a webhook payload with alerts[], an API envelope with data[], or a single alert")
		}
	default:
		return nil, fmt.Errorf("alertmanager: input is not JSON (want an object or array)")
	}
}

func normalize(raw rawAlert) (normalized, error) {
	status := strings.TrimSpace(raw.Status)
	if status != "" && status != "firing" && status != "resolved" {
		return normalized{}, fmt.Errorf("unsupported status %q (want firing|resolved)", status)
	}
	startsAt, starts, err := normalizeTime("startsAt", raw.StartsAt)
	if err != nil {
		return normalized{}, err
	}
	endsAt, _, err := normalizeTime("endsAt", raw.EndsAt)
	if err != nil {
		return normalized{}, err
	}
	labels := raw.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	canonLabels, err := json.Marshal(labels)
	if err != nil {
		return normalized{}, fmt.Errorf("encode labels: %w", err)
	}
	return normalized{
		alert: alert{
			Status:       status,
			Labels:       labels,
			Annotations:  raw.Annotations,
			StartsAt:     startsAt,
			EndsAt:       endsAt,
			GeneratorURL: strings.TrimSpace(raw.GeneratorURL),
			Fingerprint:  strings.TrimSpace(raw.Fingerprint),
		},
		starts: starts,
		canon:  string(canonLabels) + "\x00" + strings.TrimSpace(raw.Fingerprint),
	}, nil
}

func normalizeTime(field, value string) (string, time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%s is not RFC3339: %w", field, err)
	}
	t = t.UTC()
	return t.Format(time.RFC3339Nano), t, nil
}

func less(a, b normalized) bool {
	if !a.starts.Equal(b.starts) {
		return a.starts.Before(b.starts)
	}
	return a.canon < b.canon
}

// overallStatus summarizes the alert list the same way for every input format.
func overallStatus(alerts []alert) string {
	if len(alerts) == 0 {
		return ""
	}
	firing := false
	resolved := 0
	for _, a := range alerts {
		switch a.Status {
		case "firing":
			firing = true
		case "resolved":
			resolved++
		}
	}
	if firing {
		return "firing"
	}
	if resolved == len(alerts) {
		return "resolved"
	}
	return ""
}
