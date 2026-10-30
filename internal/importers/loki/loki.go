// Package loki imports Loki query_range log responses as logs/NAME.log.
//
// Every entry becomes one line:
//
//	<RFC3339 UTC timestamp> <{label="value",...}> <log line>
//
// Entries are sorted by timestamp and then by their rendered stream labels, so
// the same response always produces the same file. Newlines inside a log line are
// escaped as literal \n to keep one entry per line.
package loki

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/paas-ops/kubectl-triage/internal/importers"
)

const (
	sourceName  = "loki"
	defaultName = "loki"
	extension   = ".log"
)

func init() { importers.Register(importer{}) }

type importer struct{}

func (importer) Name() string        { return sourceName }
func (importer) DefaultName() string { return defaultName }

func (importer) Import(in importers.Input) (*importers.Output, error) { return Import(in) }

// Import normalizes a Loki query_range log response into logs/NAME.log.
func Import(in importers.Input) (*importers.Output, error) {
	name, err := importers.ResolveName(in, defaultName, extension)
	if err != nil {
		return nil, fmt.Errorf("loki: %w", err)
	}
	entries, err := parse(in.Data)
	if err != nil {
		return nil, err
	}
	return importers.Single("logs/"+name+extension, marshalLines(entries)), nil
}

// response is the subset of the Loki HTTP API query_range response we consume.
type response struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string   `json:"resultType"`
		Result     []stream `json:"result"`
	} `json:"data"`
}

// stream is one label set with its log entries; values are [ns timestamp, line].
type stream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"`
}

// entry is one flattened log line with its sort keys.
type entry struct {
	at     int64
	labels string
	line   string
	seq    int
}

func parse(data []byte) ([]entry, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("loki: empty input")
	}
	var resp response
	if err := json.Unmarshal(trimmed, &resp); err != nil {
		return nil, fmt.Errorf("loki: parse query_range response: %w", err)
	}
	status := strings.TrimSpace(resp.Status)
	switch {
	case status == "":
		return nil, fmt.Errorf("loki: response has no status")
	case status != "success":
		return nil, fmt.Errorf("loki: query status is %q; fix the query before importing", status)
	}
	if resp.Data.ResultType != "streams" {
		return nil, fmt.Errorf("loki: unsupported resultType %q (want streams; use a log query, not a metric query)", resp.Data.ResultType)
	}
	var entries []entry
	seq := 0
	for i, s := range resp.Data.Result {
		labels := formatLabels(s.Stream)
		for j, pair := range s.Values {
			at, err := strconv.ParseInt(pair[0], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("loki: stream %d entry %d: invalid nanosecond timestamp %q", i, j, pair[0])
			}
			entries = append(entries, entry{
				at:     at,
				labels: labels,
				line:   escapeLine(pair[1]),
				seq:    seq,
			})
			seq++
		}
	}
	sort.SliceStable(entries, func(a, b int) bool {
		if entries[a].at != entries[b].at {
			return entries[a].at < entries[b].at
		}
		if entries[a].labels != entries[b].labels {
			return entries[a].labels < entries[b].labels
		}
		return entries[a].seq < entries[b].seq
	})
	return entries, nil
}

func marshalLines(entries []entry) []byte {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(time.Unix(0, e.at).UTC().Format(time.RFC3339Nano))
		b.WriteString(" ")
		b.WriteString(e.labels)
		b.WriteString(" ")
		b.WriteString(e.line)
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// formatLabels renders {key="value", ...} with sorted keys.
func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{")
	for i, key := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(strconv.Quote(labels[key]))
	}
	b.WriteString("}")
	return b.String()
}

// escapeLine keeps one log entry per output line; literal backslash-n sequences
// are intentionally left untouched so the original text is not corrupted.
func escapeLine(line string) string {
	line = strings.ReplaceAll(line, "\r\n", "\\n")
	line = strings.ReplaceAll(line, "\n", "\\n")
	line = strings.ReplaceAll(line, "\r", "\\r")
	return line
}
