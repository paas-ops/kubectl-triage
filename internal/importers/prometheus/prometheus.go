// Package prometheus imports Prometheus query_range responses as
// metrics/NAME.json.
//
// Only the stable envelope is kept (status, data.resultType, data.result); other
// response fields such as warnings are dropped. matrix, vector, scalar and string
// results are supported and rendered with a fixed, human-readable layout so the
// same response always produces the same file.
package prometheus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/paas-ops/kubectl-triage/internal/importers"
)

const (
	sourceName  = "prometheus"
	defaultName = "query"
	extension   = ".json"
)

func init() { importers.Register(importer{}) }

type importer struct{}

func (importer) Name() string        { return sourceName }
func (importer) DefaultName() string { return defaultName }

func (importer) Import(in importers.Input) (*importers.Output, error) { return Import(in) }

// Import normalizes a query_range response into metrics/NAME.json.
func Import(in importers.Input) (*importers.Output, error) {
	name, err := importers.ResolveName(in, defaultName, extension)
	if err != nil {
		return nil, fmt.Errorf("prometheus: %w", err)
	}
	status, resultType, result, err := parse(in.Data)
	if err != nil {
		return nil, err
	}
	data, err := marshalDocument(status, resultType, result)
	if err != nil {
		return nil, fmt.Errorf("prometheus: encode metrics: %w", err)
	}
	return importers.Single("metrics/"+name+extension, data), nil
}

// response is the subset of the HTTP API query_range response we consume.
type response struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
}

type matrixSeries struct {
	Metric map[string]string `json:"metric"`
	Values []sample          `json:"values"`
}

type vectorSeries struct {
	Metric map[string]string `json:"metric"`
	Value  sample            `json:"value"`
}

// sample is one [timestamp, value] point. Prometheus timestamps are JSON numbers
// and values are strings; both are kept verbatim for byte-stable output.
type sample struct {
	Timestamp json.Number
	Value     string
}

func (s *sample) UnmarshalJSON(data []byte) error {
	var pair []json.RawMessage
	if err := json.Unmarshal(data, &pair); err != nil {
		return err
	}
	if len(pair) != 2 {
		return fmt.Errorf("sample must be a [timestamp, value] pair")
	}
	var timestamp json.Number
	if err := json.Unmarshal(pair[0], &timestamp); err != nil {
		return fmt.Errorf("timestamp: %w", err)
	}
	if timestamp == "" {
		return fmt.Errorf("timestamp is empty")
	}
	if err := json.Unmarshal(pair[1], &s.Value); err != nil {
		return fmt.Errorf("value: %w", err)
	}
	s.Timestamp = timestamp
	return nil
}

func parse(data []byte) (string, string, any, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return "", "", nil, fmt.Errorf("prometheus: empty input")
	}
	var resp response
	if err := json.Unmarshal(trimmed, &resp); err != nil {
		return "", "", nil, fmt.Errorf("prometheus: parse query_range response: %w", err)
	}
	status := strings.TrimSpace(resp.Status)
	switch {
	case status == "":
		return "", "", nil, fmt.Errorf("prometheus: response has no status")
	case status != "success":
		return "", "", nil, fmt.Errorf("prometheus: query status is %q; fix the query before importing", status)
	}
	if len(resp.Data.Result) == 0 {
		return "", "", nil, fmt.Errorf("prometheus: response has no data.result")
	}
	resultType := strings.TrimSpace(resp.Data.ResultType)
	switch resultType {
	case "matrix":
		var series []matrixSeries
		if err := json.Unmarshal(resp.Data.Result, &series); err != nil {
			return "", "", nil, fmt.Errorf("prometheus: parse matrix result: %w", err)
		}
		return status, resultType, series, nil
	case "vector":
		var series []vectorSeries
		if err := json.Unmarshal(resp.Data.Result, &series); err != nil {
			return "", "", nil, fmt.Errorf("prometheus: parse vector result: %w", err)
		}
		return status, resultType, series, nil
	case "scalar", "string":
		var point sample
		if err := json.Unmarshal(resp.Data.Result, &point); err != nil {
			return "", "", nil, fmt.Errorf("prometheus: parse %s result: %w", resultType, err)
		}
		return status, resultType, point, nil
	default:
		return "", "", nil, fmt.Errorf("prometheus: unsupported resultType %q (want matrix|vector|scalar|string)", resultType)
	}
}

func marshalDocument(status, resultType string, result any) ([]byte, error) {
	statusJSON, err := json.Marshal(status)
	if err != nil {
		return nil, err
	}
	typeJSON, err := json.Marshal(resultType)
	if err != nil {
		return nil, err
	}
	rendered, err := renderResult(result)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("{\n  \"status\": ")
	b.WriteString(string(statusJSON))
	b.WriteString(",\n  \"data\": {\n    \"resultType\": ")
	b.WriteString(string(typeJSON))
	b.WriteString(",\n    \"result\": ")
	b.WriteString(rendered)
	b.WriteString("\n  }\n}\n")
	return []byte(b.String()), nil
}

// renderResult keeps sample arrays inline so metric files stay diff-friendly.
func renderResult(result any) (string, error) {
	switch r := result.(type) {
	case []matrixSeries:
		if len(r) == 0 {
			return "[]", nil
		}
		var b strings.Builder
		b.WriteString("[")
		for i := range r {
			if i > 0 {
				b.WriteString(",")
			}
			metric, err := marshalMetric(r[i].Metric)
			if err != nil {
				return "", err
			}
			values, err := renderSamples(r[i].Values)
			if err != nil {
				return "", err
			}
			b.WriteString("\n      {\n        \"metric\": ")
			b.WriteString(metric)
			b.WriteString(",\n        \"values\": ")
			b.WriteString(values)
			b.WriteString("\n      }")
		}
		b.WriteString("\n    ]")
		return b.String(), nil
	case []vectorSeries:
		if len(r) == 0 {
			return "[]", nil
		}
		var b strings.Builder
		b.WriteString("[")
		for i := range r {
			if i > 0 {
				b.WriteString(",")
			}
			metric, err := marshalMetric(r[i].Metric)
			if err != nil {
				return "", err
			}
			value, err := r[i].Value.render()
			if err != nil {
				return "", err
			}
			b.WriteString("\n      {\n        \"metric\": ")
			b.WriteString(metric)
			b.WriteString(",\n        \"value\": ")
			b.WriteString(value)
			b.WriteString("\n      }")
		}
		b.WriteString("\n    ]")
		return b.String(), nil
	case sample:
		return r.render()
	default:
		return "", fmt.Errorf("unsupported result type %T", result)
	}
}

func renderSamples(samples []sample) (string, error) {
	parts := make([]string, 0, len(samples))
	for _, point := range samples {
		rendered, err := point.render()
		if err != nil {
			return "", err
		}
		parts = append(parts, rendered)
	}
	return "[" + strings.Join(parts, ", ") + "]", nil
}

func (s sample) render() (string, error) {
	timestamp := s.Timestamp.String()
	if timestamp == "" || !json.Valid([]byte(timestamp)) {
		return "", fmt.Errorf("invalid sample timestamp %q", timestamp)
	}
	value, err := json.Marshal(s.Value)
	if err != nil {
		return "", err
	}
	return "[" + timestamp + ", " + string(value) + "]", nil
}

// marshalMetric renders label maps with sorted keys for deterministic output.
func marshalMetric(metric map[string]string) (string, error) {
	keys := make([]string, 0, len(metric))
	for key := range metric {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{")
	for i, key := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return "", err
		}
		encodedValue, err := json.Marshal(metric[key])
		if err != nil {
			return "", err
		}
		b.Write(encodedKey)
		b.WriteString(": ")
		b.Write(encodedValue)
	}
	b.WriteString("}")
	return b.String(), nil
}
