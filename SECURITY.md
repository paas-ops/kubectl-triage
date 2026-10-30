# Security policy

## Supported versions

Security fixes are provided for the latest minor release.

## Reporting a vulnerability

Do **not** open a public GitHub issue for a vulnerability.

Report privately to the maintainers via the security contact configured on the repository
(or the maintainer email in `go.mod`/GitHub profile). Include:

- affected version/commit;
- a minimal reproduction (no real credentials or customer data);
- impact and whether data could leave the local machine.

We aim to acknowledge within 3 business days and coordinate disclosure after a fix.

## Security boundaries

- The CLI is read-only: it must never call Kubernetes write APIs or `exec`.
- It must not read Secrets or upload evidence by default.
- Model/network access is opt-in; `--offline` must prevent all network calls.
- Reports must not echo credentials found in input; validation should reject them.
