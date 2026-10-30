package analyze

import (
	"time"

	"github.com/paas-ops/kubectl-triage/internal/analyze/model"
)

// Options controls deterministic analysis behavior.
//
// MaxEvidence <= 0 falls back to evidence.DefaultMaxEvidence (200); a zero Now
// means "time.Now().UTC()" and is only overridden by tests and golden runs.
// Model is the optional model-provider configuration (ADR-004); empty Provider
// means offline rules only.
type Options struct {
	MaxEvidence int
	Now         time.Time
	Model       model.Config
}
