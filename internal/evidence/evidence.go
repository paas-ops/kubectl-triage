// Package evidence normalizes incident evidence: stable identifiers, timing,
// content references, hashes and deterministic trimming.
//
// The rules engine and the report layer only consume this normalized form so
// that repeated runs over the same bundle produce the same report.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// DefaultMaxEvidence is the evidence cap used when none is configured.
	DefaultMaxEvidence = 200
	// StaleAfter marks evidence as stale when it is older than this value relative
	// to the analysis reference time ("now").
	StaleAfter = 15 * time.Minute
	// MaxSkew is the largest tolerated positive clock skew: skew_seconds is set
	// only when observed_at is later than collected_at by more than MaxSkew.
	// Normal collection delay (collected_at at or after observed_at) is not skew.
	MaxSkew = 9 * time.Second
)

// Item is one normalized evidence entry. JSON tags match the frozen
// report.schema.json evidence object.
type Item struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	ObservedAt  string `json:"observed_at"`
	CollectedAt string `json:"collected_at"`
	Summary     string `json:"summary"`
	ContentRef  string `json:"content_ref"`
	Query       string `json:"query,omitempty"`
	Redaction   string `json:"redaction,omitempty"`
	SHA256      string `json:"sha256"`
	Stale       bool   `json:"stale"`
	SkewSeconds int    `json:"skew_seconds,omitempty"`
}

// NewID returns the stable evidence identifier for the n-th (1-based) entry:
// 1 -> "ev-001", 12 -> "ev-012".
func NewID(n int) string {
	return fmt.Sprintf("ev-%03d", n)
}

// Hash returns the prefixed SHA-256 of data: "sha256:<hex>".
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ParseTime parses an RFC3339 / RFC3339Nano timestamp and normalizes it to UTC.
func ParseTime(value string) (time.Time, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return time.Time{}, errors.New("empty timestamp")
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// FormatTime renders t as RFC3339 in UTC; this is the only timestamp format the
// analyzer writes into reports.
func FormatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// MarkTiming fills Stale and SkewSeconds from the item timestamps:
//
//   - stale when observed_at or collected_at is more than StaleAfter before now;
//   - skew_seconds is the signed observed_at minus collected_at, recorded only
//     when observed_at is later than collected_at by more than MaxSkew (a
//     positive clock skew). A normal collection delay (collected_at at or after
//     observed_at) is not clock skew and leaves SkewSeconds at 0.
//
// Unparseable timestamps are left to Validate; MarkTiming is tolerant so that
// the analyzer can still emit a partial report for degraded bundles.
func (it *Item) MarkTiming(now time.Time) {
	observed, observedErr := ParseTime(it.ObservedAt)
	collected, collectedErr := ParseTime(it.CollectedAt)

	it.Stale = false
	it.SkewSeconds = 0
	if observedErr == nil && now.Sub(observed) > StaleAfter {
		it.Stale = true
	}
	if collectedErr == nil && now.Sub(collected) > StaleAfter {
		it.Stale = true
	}
	if observedErr == nil && collectedErr == nil {
		skew := observed.Sub(collected)
		if skew > MaxSkew {
			it.SkewSeconds = int(skew / time.Second)
		}
	}
}
