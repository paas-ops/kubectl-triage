# kubectl-triage

English | [简体中文](README-zh.md)

A lightweight, offline-first Kubernetes incident triage CLI. Developers run a traceable diagnosis over
historical incident data locally, without a control plane and without uploading evidence; the enterprise
edition then upgrades a single diagnosis into a team incident library, evaluation, audit and private
deployment.

> Current stage: **M5 v1.0 preparation (target release `v0.5.0`)**: `init` / `import` / `validate` /
> `analyze` / `report` / `eval` / `telemetry` / `push` are available, with deterministic rules for five
> failure classes, evidence citations/counter-evidence/missing-data notes, an optional model (disabled by
> default, automatic degradation on failure), local eval plus a formal evaluation-set validator
> (`eval --validate`), opt-in telemetry and an enterprise push client, as well as prebuilt, signed
> macOS / Linux / **Windows** artifacts, SBOMs, Docker and Homebrew distribution.
> The `report.json` `schema_version 1.0` has entered the compatibility-baseline stage (see
> [docs/schema.md](docs/schema.md) (Chinese)); announcing v1.0 still needs a formal 100-case blind set and
> pilot data — this stage is engineering preparation only. The enterprise incident library,
> evaluation-set hosting, audit and private deployment live in the CloudOps Agent commercial edition and
> are not part of this repository. Before formal evaluation, do not use results as the sole basis for
> production decisions.
>
> Documentation site (static, readable offline): **https://paas-ops.github.io/kubectl-triage/** ·
> Project page: **https://paas-ops.github.io/kubectl-triage/project.html** ·
> Site source [site/index.html](site/index.html) (no build step, no external dependencies).

## Quick Start

```bash
# Install from source (Go 1.24+; see the installation matrix for release binaries / Docker / Homebrew)
go install github.com/paas-ops/kubectl-triage/cmd/kubectl-triage@latest

# Generate an example incident bundle and run one local diagnosis
kubectl-triage init /tmp/triage-demo
kubectl-triage validate /tmp/triage-demo
kubectl-triage analyze /tmp/triage-demo

# It also works as a kubectl plugin
kubectl triage analyze /tmp/triage-demo
```

Output:

```text
/tmp/triage-demo/
├── incident.yaml
├── k8s/events.json
├── logs/app.log
├── metrics/memory.json
├── report.md
└── report.json
```

The complete 5-minute flow (including exit codes and FAQ) is in
[docs/quickstart.md](docs/quickstart.md) (Chinese).

## Installation Matrix

| Method | Command / artifact | Status |
| --- | --- | --- |
| Source (Go 1.24+) | `go install github.com/paas-ops/kubectl-triage/cmd/kubectl-triage@latest` | ✅ Available now |
| Release binaries | `kubectl-triage_<version>_<os>_<arch>.tar.gz` (darwin/linux × amd64/arm64) | ✅ Since `v0.3.0` |
| Windows binary | `kubectl-triage_<version>_windows_amd64.zip` (unzip to `kubectl-triage.exe`; put it on PATH to use as a kubectl plugin) | ✅ Since `v0.5.0` |
| Docker / GHCR | `docker run --rm ghcr.io/paas-ops/kubectl-triage:v0.3.0 version` | ✅ Since `v0.3.0` |
| Homebrew | `brew tap paas-ops/homebrew-tap && brew install kubectl-triage` | ✅ Tap exists (formula synced automatically) |

- Every release archive (tar.gz / Windows zip) ships with sha256 (`checksums.txt`), a cosign keyless
  signature (`.sig` / `.pem`) and a matching CycloneDX SBOM (`*.cdx.json`); verification steps are in
  [docs/install.md](docs/install.md) (Chinese).
- The Homebrew formula is currently published as a release asset; automatic tap pushes require
  `HOMEBREW_TAP_GITHUB_TOKEN`. Current status and bootstrap steps are in
  [docs/homebrew.md](docs/homebrew.md) (Chinese).
- Older versions without prebuilt artifacts (v0.1.0 / v0.2.0) should be installed from source.

## Current Capabilities

| Command | Status |
| --- | --- |
| `init <dir>` | ✅ Generates a minimal runnable incident bundle |
| `validate <dir>` | ✅ JSON Schema validation, RFC3339 timestamps, sensitive-content scanning (`--redact strict|standard|off`) |
| `analyze <dir>` | ✅ Deterministic analysis for five failure classes + Markdown/JSON reports (`--format`/`--out`/`--max-evidence`/`--fail-on-missing`, exit codes 0/1/2/3/4) |
| `report <dir>` | ✅ Re-renders from an existing `report.json` without re-analysis |
| `import alertmanager/kubectl/prometheus/loki` | ✅ Scans credentials during import; exit code 4 on a match |
| `--model` (Ollama / OpenAI-compatible) | ✅ Optional model mode, disabled by default; budget/timeout/rounds, degradation exit code 3 |
| `eval <dir>` | ✅ Local evaluation (Top1/Top3/missing/injection/degraded; `--json`, `--now` to replay golden); `--validate [--min-cases N]` validates only the set structure (no analysis, exit codes 0/1/2) |
| `telemetry status|enable|disable` | ✅ Opt-in anonymous telemetry, disabled by default; field allowlist, never collects incident content |
| Schema compatibility baseline | ✅ `pkg/schema` baseline tests (`UPDATE_COMPAT=1` for explicit updates); 12 golden and examples reports validated continuously (since `v0.5.0`) |
| Prebuilt / signed / SBOM / Docker / Homebrew | ✅ Generated by the release workflow on a `v0.3.0` tag; the Homebrew formula is synced automatically by the tap workflow |
| `push <dir>` (enterprise push, optional) | ✅ Explicit endpoint/token configuration; sends only the report and evidence hashes by default; `--dry-run`; exit codes 0/1/3/4 (since `v0.4.0`) |

## Enterprise Bridge: push (Optional)

`push` explicitly syncs a redacted local report to an enterprise control plane and is the only
enterprise-bridge capability on the OSS side (the receiving side and team capabilities are implemented
by the commercial edition):

```bash
export KUBECTL_TRIAGE_ENTERPRISE_TOKEN=...     # environment variable only, no flag

# Dry-run first: build the payload and summary without network access; --out can dump JSON for audit
kubectl-triage push ./incidents/2026-10-12-checkout-oom \
  --endpoint https://control.example.com --dry-run

# Real push (sends only the report and evidence hashes by default)
kubectl-triage push ./incidents/2026-10-12-checkout-oom \
  --endpoint https://control.example.com

# Explicitly include the redacted evidence bundle (deterministic tar.gz + base64)
kubectl-triage push ./incidents/2026-10-12-checkout-oom \
  --endpoint https://control.example.com --include-evidence
```

- Missing endpoint/token → exit code 1 with zero network requests; the endpoint must be `https`
  (plain http is allowed only for loopback local testing);
- Exit codes: `0` success; `1` configuration / authentication / rejected (4xx); `3` network / timeout /
  429 / 5xx (retries exhausted); `4` pre-push security check failed (e.g. the report contains a
  sensitive pattern);
- Request fields, idempotency, retries, evidence bundle and error codes are in
  [docs/push-protocol.md](docs/push-protocol.md) (Chinese); the OSS/enterprise boundary is in
  [docs/enterprise.md](docs/enterprise.md) (Chinese).

## Offline, Security and Telemetry

- **Offline by default**: no cluster or network access; model calls happen only with an explicit
  `--model` and only to the user-configured endpoint; `--offline` is a hard guarantee and is mutually
  exclusive with `--model` (non-none).
- **Read-only**: never reads Secrets, never parses `KUBECONFIG`, never executes `exec/apply/delete`; all
  remediation suggestions are read-only and never executed automatically.
- **Input safety**: common credential patterns are scanned during import/validation, and reports never
  echo keys; model output enters a report only after schema validation, citation closure and text
  neutralization.
- **Verifiable supply chain**: sha256 + cosign keyless signatures + CycloneDX SBOMs; container images are
  signed by digest and run as nonroot. The release pipeline self-verifies signatures at release time;
  `gh workflow run verify-release.yml` can re-check any published tag manually.
- **Telemetry off by default**: no data is sent; after an explicit `telemetry enable` only 10
  allowlisted fields are reported (version / OS / architecture / mode / duration / evidence count, etc.).
  Cluster names, namespaces, alert names, logs or evidence bodies, model keys and file paths are never
  collected; the endpoint is user-configured and events are dropped when it is unset.
  `KUBECTL_TRIAGE_TELEMETRY=off` forces telemetry off.

See [docs/security.md](docs/security.md) (Chinese) for the complete data-flow diagram, permission
inventory, telemetry field allowlist and vulnerability disclosure process.

## Documentation

- Documentation site: **https://paas-ops.github.io/kubectl-triage/** (static site, source
  [site/index.html](site/index.html))
- [Project documentation page (online)](https://paas-ops.github.io/kubectl-triage/project.html): the
  full project documentation generated from [docs/PROJECT.md](docs/PROJECT.md) (generator
  [scripts/generate_project_page.py](scripts/generate_project_page.py))
- [Project documentation (source)](docs/PROJECT.md): project overview (positioning, architecture,
  contracts, commands, security, quality, milestones, roadmap)
- Chinese documentation: [Project documentation (Chinese)](docs/PROJECT-zh.md) ·
  [README (Chinese)](README-zh.md) ·
  [Chinese documentation site](https://paas-ops.github.io/kubectl-triage/index-zh.html)
- [Next steps](docs/next-steps.md) (Chinese): v1.0 announcement conditions, P0/P1/P2 to-dos, release and
  rollback process
- [5-minute quickstart](docs/quickstart.md) (Chinese): install, init, validate, analyze, read the report,
  exit codes and FAQ
- [Install and verify](docs/install.md) (Chinese): release assets, sha256 / cosign / SBOM, Docker,
  go install, Homebrew
- [Homebrew status and tap bootstrap](docs/homebrew.md) (Chinese): formula asset,
  `HOMEBREW_TAP_GITHUB_TOKEN`, follow-up enablement
- [Security and data flow](docs/security.md) (Chinese): read-only/network boundary, data-flow diagram,
  telemetry privacy, permission inventory, vulnerability disclosure
- [Schema contract](docs/schema.md) (Chinese): incident/report fields, `schema_version` semantics,
  v0.x/v1.0 compatibility promises and breaking-change process
- [Model mode](docs/model.md) (Chinese): Ollama / OpenAI-compatible configuration, budget, degradation and
  security boundaries
- [Local evaluation](docs/eval.md) (Chinese): eval usage, scoring, JSON contract and CI examples
- [Evaluation-set contract](docs/eval-contract.md) (Chinese): directory/data contract for the formal
  100-case evaluation set and the schema shared with the enterprise edition
- [Push protocol 1.0](docs/push-protocol.md) (Chinese): public contract for enterprise control-plane
  server implementations (endpoint/idempotency/retries/evidence bundle/error codes)
- [Open source and enterprise](docs/enterprise.md) (Chinese): OSS vs enterprise capability boundary, push
  positioning, G2–G5 are not in this repository
- [Enterprise pilot](docs/pilot.md) (Chinese): pilot eligibility, 28-day process, acceptance criteria and
  application entry point
- [Pilot operations playbook](docs/pilot-playbook.md) (Chinese): 28-day timeline, acceptance criteria,
  data boundary and retrospective template
- [Metrics funnel and weekly report](docs/metrics.md) (Chinese): install → activation → repeat → pilot
  definitions, weekly report template and anti-vanity metrics
- [ADR-005 M3 implementation decisions](docs/adr/ADR-005-M3实现口径.md) (Chinese): release artifacts,
  signing/SBOM, Docker/Homebrew, docs site, telemetry
- [ADR-006 M4 implementation decisions](docs/adr/ADR-006-M4实现口径.md) (Chinese): push client and public
  protocol 1.0, idempotency/retries, evidence bundle
- [ADR-007 v1.0 preparation](docs/adr/ADR-007-v1.0准备.md) (Chinese): schema compatibility policy and
  baseline, Windows support, evaluation-set validator
- [ADR-002 contract decisions](docs/adr/ADR-002-契约口径.md) (Chinese) · [ADR-003 M1](docs/adr/ADR-003-M1实现口径.md) (Chinese) ·
  [ADR-004 M2](docs/adr/ADR-004-M2实现口径.md) (Chinese)
- [Requirements](docs/需求文档.md) (Chinese): product positioning, CLI interaction, evidence spec,
  open-source/commercial boundary, metrics and acceptance criteria
- [Split plan](docs/拆分计划.md) (Chinese): repository split, module structure, 12-week milestones, task
  breakdown, release and metrics plan
- [CHANGELOG](CHANGELOG.md): version changes and migration notes

> The `CloudOps-Agent/创业商业方案.md` referenced by the planning documents belongs to an external
> commercial repository and is not published with this repository.

## Enterprise Pilot

Validate it with your own redacted historical incidents: a team with ≥2 SREs/platform engineers, ≥3
clusters and ≥10 redacted incidents, accepting the 28-day pilot and acceptance criteria. Application
entry points:

- [Submit a pilot application](https://github.com/paas-ops/kubectl-triage/issues/new?template=pilot-request.yml)
  (form fields in [.github/ISSUE_TEMPLATE/pilot-request.yml](.github/ISSUE_TEMPLATE/pilot-request.yml))
- [Pilot guide](docs/pilot.md) (Chinese) · [Pilot operations playbook (28 days)](docs/pilot-playbook.md) (Chinese) ·
  [Metrics definitions](docs/metrics.md) (Chinese)
- Enterprise bridge: [Push protocol 1.0](docs/push-protocol.md) (Chinese) · [Open source and enterprise](docs/enterprise.md) (Chinese)

## Open Source and Commercial Boundary

**The open-source CLI performs "one incident, locally, once" for free; the enterprise edition sells
"team, evaluation, audit and private deployment".**

- Open source: Apache-2.0; unlimited single diagnoses, one incident bundle at a time; the enterprise
  bridge provides only the `push` client and public protocol 1.0;
- Enterprise edition (CloudOps Agent): team incident library, evaluation, audit, SSO/RBAC, private
  deployment and SLA; the push receiving side and G2–G5 enterprise capabilities are not in this repository
  (see [docs/enterprise.md](docs/enterprise.md) (Chinese) and
  [docs/push-protocol.md](docs/push-protocol.md) (Chinese));
- We do not charge by crippling single-incident diagnosis.

## North-Star Metrics

Not GitHub Stars, but:

1. **Actual installs**: deduplicated installs from Releases / Homebrew / container pulls
2. **Repeat usage**: the share of developers who diagnose again within 4 weeks of their first successful
   diagnosis
3. **Enterprise pilot requests**: qualified pilot applications coming from the CLI/documentation/eval
   content

Definitions, collection sources, the weekly report template and anti-vanity metrics are in
[docs/metrics.md](docs/metrics.md) (Chinese).

## Development

```bash
go test ./...
go vet ./...
go build ./...

# Preview the documentation site locally (pure static, no build step)
python3 -m http.server 8000 --directory site

# Regenerate the project documentation pages (docs/PROJECT.md / docs/PROJECT-zh.md → site/project.html / site/project-zh.html; --check detects staleness)
python3 scripts/generate_project_page.py
```

CI is in [.github/workflows/ci.yml](.github/workflows/ci.yml); the documentation site is deployed to
GitHub Pages by [.github/workflows/pages.yml](.github/workflows/pages.yml) when `site/**` changes
(release engineering files `.goreleaser.yaml`, `Dockerfile` and
`.github/workflows/release.yml` are triggered by a `v*` tag).

## License

Apache-2.0. See [LICENSE](LICENSE).
