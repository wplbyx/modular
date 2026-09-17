# packages reliability contracts

The existing Endpoint/Resource, ManagedResource/Provider and modular monolith architecture remain intact.

- A successful asynchronous submission means admission, not persistence. Flush reports completion and bounded failure summaries for its admission watermark.
- Closing rejects new work and drains admitted work. Deadline expiry cancels cooperative callbacks; it cannot forcibly terminate arbitrary Go code.
- Message success or successful dead-letter delivery precedes acknowledgment. Failed messages retry; delivery remains at least once, so handlers own business idempotency.
- Registrations completed during shutdown are compensated; readiness never advances after stopping begins.
- Files are written completely before replacing an object. Multipart sessions are validated and bound to their storage root and key.
- Every admitted transport operation completes its protection accounting exactly once, including panic paths.
- Existing public signatures remain available. New context-aware APIs provide stronger lifecycle contracts; legacy wrappers document limitations.
- Retain the cyub RingMPSC implementation and avoid speculative performance architecture changes.

Implementation and validation progress: [plan](../plans/2026-09-17-packages-reliability.md).
