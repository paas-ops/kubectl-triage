# Changelog

## v0.0.1 — 2026-10-30

Initial repository skeleton.

- `init <dir>` creates a minimal incident bundle.
- `validate <dir>` checks required metadata, RFC3339 timestamps and common credential patterns.
- `analyze <dir> --offline` produces an evidence-linked Markdown/JSON report for
  OOMKilled, ImagePullBackOff, CrashLoopBackOff and Pending signatures.
- Apache-2.0 license, CI (build/test/vet), contribution and security docs.

Not yet implemented: importers, model providers, local eval, enterprise `push`.
