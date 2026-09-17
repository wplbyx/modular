# Audit workflow

Use for convention checks or release verification. Read [commands](../commands.md),
[layering](../layering.md), and [lifecycle](../lifecycle.md).

1. Run the installed skill CLI and inspect manifest file/region ownership.
2. Confirm architecture schema 2 declares one Application and an acyclic
   bounded-context module graph.
3. Confirm each module is rooted at `modules/<module>`, owns its infrastructure,
   and imports siblings only through declared provider `contract` packages.
4. Review both assembly levels: cmd owns shared Resources, cross-module
   connections, and at most one HTTP/gRPC server; module bootstrap assembles
   private objects using explicit dependencies. Constructor inputs/results
   are usable by cmd without internal types; constructors do not start work.
5. Confirm module data writes stay owned and any cross-module transaction has a
   clear initiating use case and private transaction interface.
   Apply the [contract review questions](../interface-design.md#contract-evidence-and-review)
   to business guarantees, critical collaboration flows, and their test evidence.
6. Run `verify --phase contract` or `verify --phase complete` for the current
   phase. Run `coverage` separately when a report is requested.
