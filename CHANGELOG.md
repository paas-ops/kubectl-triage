# Changelog

## Unreleased — M3.1 distribution hardening

- Homebrew tap sync: the tap repository now runs a `sync-formula` workflow (daily schedule and
  manual dispatch) that pulls the latest formula from releases using its own token, so no
  cross-repository PAT is required.
- Release verification: a manual `verify-release` workflow re-checks sha256 sums, the keyless
  cosign certificate identity, SBOM coverage and the GHCR image signature for any published tag;
  the release workflow also verifies its own signatures before finishing.
- Evaluation set contract: `docs/eval-contract.md` documents the shared layout and JSON contract
  for the formal 100-case benchmark used by the enterprise edition.

## v0.3.0 — 2026-10-30

Beta distribution: signed release artifacts, container image, docs site and opt-in telemetry.

- Release engineering: GoReleaser v2.15.4 builds darwin/linux x amd64/arm64 archives with
  sha256 checksums, cosign keyless signatures, CycloneDX SBOMs and a generated Homebrew
  formula; a `release` workflow runs on tag pushes; CI gains `release-check` and
  `docker-build` jobs.
- Container image: `ghcr.io/paas-ops/kubectl-triage:v0.3.0` and `:latest`, distroless and
  non-root, signed by digest.
- Homebrew: formula is published as a Release asset and synced to the new
  `paas-ops/homebrew-tap` tap (`brew tap paas-ops/homebrew-tap && brew install kubectl-triage`).
- Docs: GitHub Pages static site, install/homebrew/quickstart/security/pilot guides and a
  pilot-request issue template.
- Telemetry: opt-in anonymous events, disabled by default; fixed 10-field whitelist, no
  incident content, cluster, namespace, paths or keys; `telemetry status|enable|disable`,
  bounded 2s best-effort delivery.
- No report schema changes; offline behavior unchanged.

## v0.2.0 — 2026-10-30

Optional model providers and the local evaluation harness.

- Optional model mode: OpenAI-compatible and Ollama providers (`--model`, `--model-url`,
  `--model-name`), temperature 0, JSON-only output, API key read from the environment only.
- Budget, timeout and rounds: `--model-timeout` (60s), `--model-budget-usd` (1.0),
  `--model-max-rounds` (3); transient retries; budget/round exhaustion degrades.
- Safe degradation: any runtime model failure keeps the offline report with `degraded: true`
  and exit code 3; configuration errors exit 1; `--offline` and `--model` stay mutually exclusive.
- Output validation: report schema + evidence citation closure + URL/command neutralisation;
  prompt-injection and exfiltration tests (stub provider only, redirects disabled).
- Local eval: `kubectl-triage eval <cases-dir>` with Top1/Top3/missing/injection/degraded
  scoring, stable `--json` contract and `--now` replay; 12/12 golden.
- Docs: ADR-004, docs/model.md, docs/eval.md. No schema changes and no new dependencies.

## v0.1.0 — 2026-10-30

First milestone release: the M1 offline MVP.

- `import alertmanager|kubectl|prometheus|loki` with deterministic canonical output and
  import-time credential scanning (exit code 4, nothing written on rejection).
- Strict `incident.yaml` / `report.json` JSON Schema validation (embedded schemas, no external dependency).
- `analyze` evidence normalization: `content_ref`, separate observed/collected timestamps,
  stale (>15m) and clock-skew marks, `--max-evidence` trimming listed in report limitations.
- Five deterministic fault classes (OOMKilled, CrashLoopBackOff, ImagePullBackOff, Pending,
  release regression) with evidence citations, contradictions and confidence layering (rule-v0.2).
- `report <dir>` re-renders an existing report without re-analysis.
- `--redact strict|standard|off`; `--offline` vs `--model` mutual exclusion; exit codes 0/1/2/3/4.
- 12 golden incident bundles (five classes x2 + missing + injection) with determinism,
  schema and prompt-injection assertions.
- Clock-skew semantics: only an observation timestamp later than collection (>9s) is
  flagged as skew; normal collection lag is reported separately (>=60s) and never as skew.
- `go install ...@v0.1.0` binaries now report the module version via build metadata.
- CI: gofmt, build, vet, tests and darwin/linux amd64/arm64 cross-builds.

## v0.0.1 — 2026-10-30

Initial repository skeleton.

- `init <dir>` creates a minimal incident bundle.
- `validate <dir>` checks required metadata, RFC3339 timestamps and common credential patterns.
- `analyze <dir> --offline` produces an evidence-linked Markdown/JSON report for
  OOMKilled, ImagePullBackOff, CrashLoopBackOff and Pending signatures.
- Apache-2.0 license, CI (build/test/vet), contribution and security docs.

Not yet implemented: importers, model providers, local eval, enterprise `push`.
