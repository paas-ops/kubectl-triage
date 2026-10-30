# Changelog

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
