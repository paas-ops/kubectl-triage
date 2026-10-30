package push

import "context"

// Run builds the protocol payload from dir and pushes it to the configured
// enterprise endpoint. The whole run — bundle packing, HTTP retries and
// response parsing — is bounded by cfg.Timeout (ADR-006 D3).
//
// Push failures never mutate the local bundle or report: push only reads them.
func Run(ctx context.Context, dir string, cfg Config) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, cfg.timeout())
	defer cancel()

	payload, _, err := Build(dir, cfg)
	if err != nil {
		return nil, err
	}
	return Send(runCtx, cfg, payload)
}
