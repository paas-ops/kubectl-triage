# kubectl-triage Project Documentation

English | [简体中文](PROJECT-zh.md)

> **Version v0.5.0** · 2026-10-30 · Apache-2.0 · https://github.com/paas-ops/kubectl-triage
>
> This document is the consolidated project description and the entry point to the facts: positioning,
> architecture, data contracts, commands, security, quality, milestones and roadmap. Detailed rules are
> defined by the linked documents and ADRs.

## 1. Project Positioning

kubectl-triage is a **lightweight, offline-first Kubernetes incident triage CLI**: a developer takes a
redacted incident evidence bundle, runs a single command locally, and gets a **reproducible, traceable and
reviewable** Markdown/JSON diagnosis report — without a control plane, without uploading production
evidence, and without an account.

Product promises:

- **First local diagnosis in under 5 minutes**: no account, no login, no cloud dependency; `init`
  generates a runnable example;
- **Historical incidents can be reviewed**: bundles are archivable, shareable and replayable
  (golden + local eval);
- **Credibility comes from evidence**: every conclusion cites evidence and shows counter-evidence,
  missing-data notes and confidence tiers instead of model rhetoric;
- **Secure by default**: zero network, read-only, never reads Secrets; model and push are explicit
  opt-in.

## 2. Target Users and Typical Scenarios

| User | Scenario | Success criterion |
| --- | --- | --- |
| Application developer | Receives an alert and only has local `kubectl` output and a few log excerpts | Gets an evidence-backed root-cause draft without logging into a platform |
| On-call SRE | Post-incident review; needs to archive evidence and conclusions | A reviewable Markdown/JSON report |
| Platform engineer | Evaluates whether the tool is worth adopting | Replays and evaluates their own redacted historical incidents |
| Security/compliance | Production logs must not leave the domain | Runs offline; evidence hashes can be verified |
| Enterprise buyer | The team needs an incident library, evaluation and audit | Upgrades from the CLI pilot to the enterprise edition |

## 3. Open Source vs Enterprise Boundary

**The open-source CLI performs "one incident, locally, once" for free; the enterprise edition sells
"team, evaluation, audit and private deployment".**

| Capability | Open-source CLI (free) | Enterprise edition (CloudOps Agent) |
| --- | --- | --- |
| Single local diagnosis / offline rules / optional model / MD+JSON report | ✅ Unlimited | ✅ |
| Local eval (golden + formal set validation) | ✅ | ✅ (plus a team evaluation service) |
| Team incident library / search / ownership | ❌ | ✅ |
| Audit (who viewed what and when, model/version) | ❌ | ✅ |
| SSO/RBAC/multi-tenancy/private deployment/SLA | ❌ | ✅ |
| Enterprise push | ✅ Client (explicit opt-in) | ✅ Server (library/dedup/evaluation/audit) |

See [Enterprise boundary (Chinese)](enterprise.md) for the detailed boundary and FAQ; enterprise-side
G2–G5 do not live in this repository.

## 4. Core Concepts

| Concept | Description |
| --- | --- |
| **Incident bundle** | A directory: `incident.yaml` + evidence (k8s/metrics/logs/changes/alert.json); local and archivable |
| **Evidence** | Normalized entries: id / source / observed_at / collected_at / summary / content_ref / sha256 / stale |
| **Report** | `report.json` (schema_version 1.0, stable interface) + `report.md` rendered from it |
| **Hypothesis** | At most 3, ranked by confidence; each cites at least 1 valid evidence item; may carry counter-evidence and missing-data notes |
| **Evaluation set** | A case collection isomorphic to incident bundles + `expected/report.json`; golden and the formal 100 cases share the contract |
| **Enterprise push** | Explicitly configured client: sends only the report + evidence hashes by default, optionally a redacted evidence bundle; protocol v1.0 is public |

## 5. System Architecture

### 5.1 Modules

| Module | Responsibility |
| --- | --- |
| `cmd/kubectl-triage` | Program entry point; wires the CLI and version information (supports `go install @version` fallback) |
| `internal/cli` | cobra commands, flags, exit codes, output and human-friendly errors |
| `internal/bundle` | Bundle loading/validation/scaffolding, path-traversal protection, strict/standard/off redaction modes |
| `internal/redact` | Credential-pattern detection and `[REDACTED]` scrubbing (reused before import and before push) |
| `internal/evidence` | Evidence normalization: content_ref, timestamps, hashes, stale, clock skew, trimming |
| `internal/analyze` | Analysis orchestration: evidence extraction, rule engine, counter-evidence/confidence/missing data, report assembly |
| `internal/analyze/rule` | Deterministic rules for the five failure classes (rule-v0.2) |
| `internal/analyze/model` | Optional model: OpenAI-compatible/Ollama, budget/timeout/retry, output validation and neutralization |
| `internal/report` | Report structure, JSON serialization, Markdown rendering, schema validation |
| `internal/importers` | Alertmanager/kubectl/Prometheus/Loki importers (deterministic normalization) |
| `internal/eval` | Local evaluation: Top1/Top3/missing/injection/degraded scoring, set validation (`--validate`) |
| `internal/push` | Enterprise push client: protocol 1.0, idempotent retries, dry-run, security pre-flight |
| `internal/telemetry` | Opt-in anonymous telemetry: disabled by default, 10-field allowlist, best-effort |
| `pkg/schema` | Public JSON Schemas + zero-dependency subset validator + compatibility baseline tests |

### 5.2 Data Flow

```text
bundle directory ──► bundle(Load/Validate, redact) ──► evidence(normalize/trim)
                                              │
                                              ├─► rule engine (five failure classes) ──► report (offline)
                                              └─► model(optional) ── validate/neutralize ──► report (enhanced/degraded)
report ◄──────────────────────────────────────────┘
  ├─► report.md / report.json (local)
  ├─► eval (compare with expected: Top1/Top3/missing/injection/degraded)
  └─► push (explicit config; report + evidence hashes, optional evidence bundle)
```

## 6. Command Reference

| Command | Purpose | Key flags |
| --- | --- | --- |
| `version` | Version/build information | — |
| `init <dir>` | Generate a minimal runnable incident bundle | — |
| `import <source> <dir>` | Import alerts/events/metrics/logs | `--from`, `--name`, `--force` |
| `validate <dir>` | Schema, timestamp, redaction and evidence-integrity checks | `--redact strict|standard|off` |
| `analyze <dir>` | Rule analysis + report | `--offline`, `--model`, four model flags, `--format`, `--out`, `--max-evidence`, `--redact`, `--fail-on-missing` |
| `report <dir>` | Re-render an existing report (no re-analysis) | `--format`, `--out` |
| `eval <cases-dir>` | Local evaluation | `--json`, `--filter`, `--max-cases`, `--now`, `--validate`, `--min-cases`, `--compare`, model flags |
| `telemetry status|enable|disable` | Manage opt-in anonymous telemetry | — |
| `push <dir>` | Push the report to an enterprise control plane (explicit opt-in) | `--endpoint`, `--include-evidence`, `--max-bundle-bytes`, `--timeout`, `--dry-run`, `--out` |

### Exit Codes

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Input/configuration error (including `--offline` vs `--model` conflict, push configuration/authentication failure) |
| 2 | Insufficient evidence (`--fail-on-missing`) or evaluation has failing cases |
| 3 | Model/network failure, already degraded (report written with degraded=true) |
| 4 | Security check failed (sensitive content or unauthorized data detected) |

## 7. Data Contracts

### 7.1 Incident Bundle (input)

Required `incident.yaml` fields: `id, alertname, severity(critical|warning|info), cluster, namespace,
workload, observed_at, collected_at, source` (RFC3339 timestamps).

Optional evidence files (as needed): `alert.json`, `k8s/events.json`, `k8s/pods.yaml`,
`k8s/deployments.yaml`, `metrics/*.json`, `logs/*.log`,
`changes/rollout.json`, `runbook.md`, `notes.md`.

### 7.2 Report (output)

Top-level fields: `schema_version("1.0"), incident, generated_at, mode(offline|model), degraded, summary,
evidence[], hypotheses[≤3], missing_data[], recommended_checks[], suggested_fix, limitations[], versions{}`.

- Evidence fields: `id, source, observed_at, collected_at, summary, content_ref, query?, redaction?,
  sha256, stale, skew_seconds?`;
- `content_ref` conventions: whole file `<rel>`, JSON entry `<rel>#items/<n>`, log line
  `<rel>#L<n>`;
- Every conclusion must cite existing evidence; counter-evidence and supporting evidence must not share
  the same id; all suggestions are read-only and never generate executable scripts.

### 7.3 Versioning and Compatibility

- The report `schema_version` is fixed at `1.0`; v0.x allows breaking changes but requires a CHANGELOG +
  migration notes;
- Starting with v1.0, at least one major version is supported; new optional fields do not break old
  consumers (see the [schema contract (Chinese)](schema.md) and
  [ADR-007 (Chinese)](adr/ADR-007-v1.0准备.md));
- Compatibility is guarded by the `pkg/schema` baseline tests (`UPDATE_COMPAT=1` for explicit changes +
  review).

## 8. Analysis Engine

### 8.1 Five Failure-Class Rules (deterministic, rule-v0.2)

| Failure class | Typical signals | Default confidence |
| --- | --- | --- |
| OOMKilled | `OOMKilled`, `exitCode=137`, memory-limit related | high (≥2 independent sources and fresh) |
| CrashLoopBackOff | `CrashLoopBackOff`, `Back-off restarting` | mid |
| ImagePullBackOff | `ErrImagePull`, `pull access denied`, `manifest unknown` | high |
| Pending | `FailedScheduling`, insufficient resources/affinity | mid |
| Release regression | `changes/` records + ReplicaSet/image diff | mid/low |

Each hypothesis carries: evidence citations, counter-evidence (for example the image was pulled
successfully earlier, probe failures, a rollout earlier than the incident window), confidence tiers
(`high/mid/low`; stale evidence or counter-evidence lowers it), missing evidence and next steps, and
read-only remediation suggestions.

### 8.2 Optional Model (disabled by default)

- providers: OpenAI-compatible (`/v1/chat/completions`) / Ollama (`/api/chat`); temperature 0,
  JSON only;
- only the configured endpoint is contacted; redirects are disabled; evidence is treated as untrusted
  data; model output goes through schema validation, citation closure and text neutralization;
- budget/timeout/rounds are controlled (defaults $1 / 60s / 3 rounds); any runtime failure keeps the
  offline report and marks it `degraded`, exit code 3; configuration errors exit code 1. See
  [Model mode (Chinese)](model.md).

## 9. Security and Privacy

- **Offline by default**: no cluster or network access; model, push and telemetry are all explicit
  opt-in;
- **Read-only**: never reads Secrets, never parses kubeconfig, never executes `exec/apply/delete`; all
  remediation suggestions are read-only;
- **Input safety**: credential patterns are scanned before import/validation/push; reports never echo
  keys; out-of-scope/out-of-bounds evidence references are rejected;
- **Injection protection**: text in evidence that says "ignore instructions / execute commands /
  exfiltrate" is neither executed nor copied into reports, and model output is neutralized again;
- **Telemetry off by default**: only 10 allowlisted fields (version/OS/architecture/mode/duration/
  evidence count, etc.); cluster, namespace, logs, evidence bodies, paths and keys are never collected;
  `KUBECTL_TRIAGE_TELEMETRY=off` forces it off;
- **Supply chain**: releases provide sha256 + cosign keyless signatures + CycloneDX SBOMs; GHCR images
  are signed by digest.

See [Security and data flow (Chinese)](security.md) for details.

## 10. Distribution and Supply Chain

| Channel | Status |
| --- | --- |
| Source `go install github.com/paas-ops/kubectl-triage/cmd/kubectl-triage@vX.Y.Z` | ✅ |
| Release assets (darwin/linux × amd64/arm64 + windows/amd64) | ✅ Windows included since v0.5.0 |
| Docker: `ghcr.io/paas-ops/kubectl-triage:vX.Y.Z` (distroless/nonroot) | ✅ since v0.3.0 |
| Homebrew: `brew tap paas-ops/homebrew-tap && brew install kubectl-triage` | ✅ formula synced automatically |
| Documentation site: https://paas-ops.github.io/kubectl-triage/ | ✅ |
| Signature/SBOM verification | ✅ `checksums.txt` + `.sig/.pem` + `*.cdx.json`; `verify-release` can re-check |

Release pipeline: tag push → release workflow (GoReleaser v2.15.4 + cosign + SBOM + GHCR + release
self-verification) → `verify-release` re-check → tap auto-sync. See
[Install and verify (Chinese)](install.md) for installation and verification.

## 11. Quality Engineering

| Item | Value/practice (v0.5.0) |
| --- | --- |
| Go packages | 18 (16 with tests; the 2 untested packages are assembly layers) |
| Code size | ~19k lines of Go (~10k lines of tests) |
| Golden cases | 12 (five classes × 2 + missing + injection), strict comparison + determinism + non-intersecting counter-evidence |
| Evaluation | `eval` scoring (Top1/Top3/missing/injection/degraded) + `--validate` structure gate + `--compare` baseline regression gate |
| CI | 4 workflows: ci (test/cross-build/windows-test/release-check/docker-build), release, verify-release, pages |
| Contract gates | schema baseline tests, golden expected-report schema validation, release self-verification |
| Offline-default tests | push/model/telemetry all use stubs/httptest; no network access |

## 12. Milestones and Releases

| Milestone | Version | Deliverables | Verification |
| --- | --- | --- | --- |
| M0 inception/skeleton | — | Repository, license, schema v0, CLI skeleton, CI | Initial commit, CI green |
| M1 offline MVP | v0.1.0 | Contracts (schema/exit codes), four importers, redaction, five failure rules, 12 golden, reports | 13 package tests, 12/12 golden, CI 5/5 |
| M2 model/evaluation | v0.2.0 | OpenAI-compatible/Ollama, budget/timeout/degradation, injection protection, local eval | 15 package tests, model stub E2E, eval 12/12 |
| M3 beta distribution | v0.3.0 | GoReleaser artifacts, cosign/SBOM, GHCR, Homebrew, Pages site, opt-in telemetry | Release 12 assets, verify-release green |
| M3.1 hardening | — | Tap auto-sync (no PAT), release self-verification, evaluation-set contract | tap sync success, verify-release re-check |
| M4 enterprise bridge | v0.4.0 | Push client (protocol v1.0, idempotency/retry/dry-run/evidence bundle), enterprise boundary/pilot/metrics docs | E2E 8 scenarios, Release 12 assets, verify-release |
| M5 v1.0 preparation | v0.5.0 | Schema compatibility baseline, Windows amd64, eval --validate | 16 tested packages, windows-test, Release 14 assets |

> Complete ADRs: contract decisions (ADR-002), M1–M4 implementation decisions (ADR-003–006), v1.0
> preparation (ADR-007).

## 13. Metrics and Operations

The north star is not GitHub Stars but a four-stage funnel:

1. **Actual installs**: Release downloads / Homebrew analytics / GHCR pulls (deduplicated);
2. **Activation**: opt-in `analyze` event (first successful report generation);
3. **Repeat usage**: at least 2 successful diagnoses within 4 weeks after activation (deduplicated by
   install_id);
4. **Enterprise pilots**: ≥10 qualified applications, ≥3 entering a pilot, conversion/payment (the
   90-day commercial threshold).

Current status: the tooling is ready (telemetry client, pilot issue form, playbook, weekly report
template), but **real numbers depend on external release and operations** (as of v0.5.0, external
promotion has not started yet). Definitions and the weekly report template are in
[Metrics funnel (Chinese)](metrics.md).

## 14. Roadmap and Next Steps

| Priority | Item | Owner |
| --- | --- | --- |
| P0 | Build the formal 100-case blind set (redaction, contract validation, dual annotation, baseline snapshot) | Data/Evaluation |
| P0 | Windows real-machine smoke acceptance (zip/hashes/plugin/paths/CJK/CRLF) | Release/QA |
| P0 | v1.0 announcement condition review (evaluation + pilot + compatibility baseline) | Product owner |
| P1 | Pilot recruitment and 28-day playbook execution, weekly funnel report | GTM/Founder |
| P2 | CLI `--now`, model config deduplication, push Retry-After/sharding, Homebrew cask migration, docs-site link check in CI, Windows code signing, etc. | Maintainer |

See [Next steps (Chinese)](next-steps.md) for the complete list, acceptance criteria and rollback
process.

## 15. Project Structure

```text
kubectl-triage/
├── cmd/kubectl-triage/          # program entry point
├── internal/
│   ├── cli/                     # commands, flags, exit codes
│   ├── bundle/                  # bundle loading/validation/scaffolding/redaction
│   ├── redact/                  # credential detection and scrubbing
│   ├── evidence/                # evidence normalization/trimming/stale/skew
│   ├── analyze/                 # analysis orchestration + rule/ + model/
│   ├── report/                  # JSON/Markdown rendering
│   ├── importers/               # alertmanager/kubectl/prometheus/loki
│   ├── eval/                    # local evaluation and set validation
│   ├── push/                    # enterprise push client
│   └── telemetry/               # opt-in anonymous telemetry
├── pkg/schema/                  # public schemas + validator + compatibility baseline
├── testdata/golden/             # 12 golden incident bundles
├── examples/oom/                # 5-minute example
├── docs/                        # requirements/plan/ADRs/install/security/eval/protocol…
├── site/                        # static documentation site (GitHub Pages)
└── .github/workflows/           # ci / release / verify-release / pages
```

## 16. Documentation Index

> Most deep-dive guides are currently Chinese-only; links marked (Chinese) point to Chinese documents.

| Document | Content |
| --- | --- |
| [Requirements (Chinese)](需求文档.md) | Product positioning, CLI interaction, evidence spec, open-source/commercial boundary, metrics and acceptance |
| [Split plan (Chinese)](拆分计划.md) | Repository split, module structure, 12-week milestones, WBS, release and metrics |
| [ADR-002 (Chinese)](adr/ADR-002-契约口径.md) ~ [ADR-007 (Chinese)](adr/ADR-007-v1.0准备.md) | Frozen implementation decisions for each stage |
| [5-minute quickstart (Chinese)](quickstart.md) | Install, init, analyze, read the report, exit codes |
| [Install and verify (Chinese)](install.md) | Release assets, sha256/cosign/SBOM, Docker, Windows, Homebrew |
| [Security and data flow (Chinese)](security.md) | Boundary, permission inventory, data flow, telemetry privacy, supply chain |
| [Model mode (Chinese)](model.md) | Ollama/OpenAI-compatible, budget, degradation and security |
| [Local evaluation (Chinese)](eval.md) · [Evaluation-set contract (Chinese)](eval-contract.md) | eval usage, scoring, formal-set contract and --validate |
| [push protocol v1.0 (Chinese)](push-protocol.md) · [Enterprise boundary (Chinese)](enterprise.md) | Enterprise push protocol and open-source/commercial boundary |
| [Pilot playbook (Chinese)](pilot-playbook.md) · [Metrics funnel (Chinese)](metrics.md) | 28-day pilot operations and north-star metrics |
| [Schema contract (Chinese)](schema.md) | Report/incident fields and compatibility policy |
| [Next steps (Chinese)](next-steps.md) | v1.0 conditions, P0/P1/P2, release and rollback |

## 17. Contributing, License and Security Disclosure

- Contribution process: [CONTRIBUTING.md](../CONTRIBUTING.md) (DCO, no CLA required; never commit real
  customer data/credentials);
- License: Apache-2.0 ([LICENSE](../LICENSE));
- Security disclosure: do not open a public issue; report privately via [SECURITY.md](../SECURITY.md);
- Code of conduct: [CODE_OF_CONDUCT.md](../CODE_OF_CONDUCT.md).
