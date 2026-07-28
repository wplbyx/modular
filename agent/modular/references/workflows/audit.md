# Audit workflow

Use this workflow for convention checks or pre-merge review.
Read [commands](../commands.md) before selecting the verification phase.

1. Run `python .modular/tool/modular.py self-check` for the installed/local
   skill package.
2. Run `make scaffold-doctor` and record every warning/error without mutation.
3. Run `make contract-check` for a contract-phase project or `make verify` for
   a business-complete project.
4. Check ownership conflicts, stale generated common files, cross-svc internal
   imports, forbidden placeholders, and business package test presence.
5. Treat `.modular/profile.toml` as project-specific policy. Do not promote a
   profile rule such as a domain test-file restriction into universal skill
   instructions.
