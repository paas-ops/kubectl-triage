package analyze

import "time"

// Options controls deterministic analysis behavior.
//
// MaxEvidence <= 0 falls back to evidence.DefaultMaxEvidence (200); a zero Now
// means "time.Now().UTC()" and is only overridden by tests and golden runs.
type Options struct {
	MaxEvidence int
	Now         time.Time
}
