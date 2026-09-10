# Audit workflow

Use for convention checks, extraction review, or pre-merge verification. Read
[commands](../commands.md).

1. Run `python3 .modular/tool/modular.py self-check`.
2. Run `make scaffold-doctor` and record every issue without mutation.
3. Validate architecture assignments, dependency cycles, declared imports,
   contract-only cross-module access, and extraction blockers.
4. Confirm each Process has one identity/lifecycle and at most one HTTP/gRPC
   server, with Process-owned Resources and readiness.
5. Run `make contract-check` or `make verify` for the current phase.

Treat `.modular/profile.toml` as project-specific policy. Ownership conflicts,
stale common output, unfinished markers, and missing business tests are release
failures; warnings are not silently rewritten during an audit.
