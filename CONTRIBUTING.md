# Contributing

Thanks for helping make incident triage more reproducible.

## Scope

The open-source CLI intentionally stays narrow:

- one incident bundle at a time;
- local/offline by default;
- no cluster writes, no Secret reads, no auto-remediation;
- evidence-linked reports, not a chat interface.

Team incident libraries, hosted evaluation, audit, SSO and private deployment belong to the
commercial CloudOps Agent product and are out of scope for this repository.

## Development

```bash
go test ./...
go vet ./...
go build ./...
```

Requirements: Go 1.24+.

## Pull requests

- Keep changes small and explain the user-visible behavior.
- Add or update golden fixtures under `testdata/` when changing analysis output.
- Never commit real customer data, credentials, kubeconfigs or unredacted logs.
- Use `git commit -s` (DCO). No CLA is required.

## Security

Please do not open a public issue for a vulnerability. See [SECURITY.md](SECURITY.md).
