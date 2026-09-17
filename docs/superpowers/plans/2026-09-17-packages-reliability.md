# packages reliability implementation

Approved scope: preserve public entry points; repair incorrect behavior with regression tests.
Message failures remain unacknowledged and retry with cancellable backoff unless delivered to DLQ.

## Delivery checklist

- [x] Storage: confined paths, bound multipart sessions, atomic replacement.
- [x] Messaging: partition ordering, pending recovery, reliable acknowledgment and shutdown.
- [x] Redis lock: cancel on ownership loss, bounded renewal and release.
- [x] Transport/resilience: exactly-once completion, final error classification, breaker generations.
- [x] Application/registry: registration/close coordination, bounded waiting, discovery snapshots.
- [x] Caching: admission/drain/results, bounded refresh, conditional expiry cleanup.
- [x] Operations: GORM ping, health checks, eventbus cancellation, SSE, telemetry, config snapshots, streaming HTTP.
- [x] Logging tests/benchmarks, migration notes, complete build/test/vet/format verification.

Tests exercise public lifecycle and behavioral contracts. External services are mocked at SDK/network boundaries where practical; live integration results and race-toolchain limitations are reported separately.

Design constraints and migration decisions: [spec](../specs/2026-09-17-packages-reliability.md).

## Verification results (2026-09-17)

Implementation and available local checks are complete. Environmental gaps below remain unverified.

| Check | Result |
| --- | --- |
| Windows `go build ./...` | Passed (Go 1.26.5) |
| `go test -json -count=1 ./...` | Passed; nine live Redis tests skipped because localhost:6379 was unavailable |
| Critical concurrency/regression tests, 20 repetitions | Passed |
| `go vet ./...` | Passed |
| `gofmt -l .` and `git diff --check` | Clean |
| Linux amd64 cross-build, CGO disabled | Passed; Linux runtime behavior was not tested |
| Race detector | Blocked: installed Clang lacks Windows C headers (`stdlib.h`, `windows.h`, `errno.h`) |
| Live Kafka integration and broker restart | Not run; delivery behavior covered with SDK-boundary fakes |

The Windows symlink confinement regression passed. Cross-compilation does not validate Linux file-replacement behavior. A complete C toolchain and live brokers are still needed for race and integration validation.

Logging benchmark smoke checks used discard sinks on Windows (i5-13400): synchronous approximately 1768 ns/op, asynchronous approximately 1575 ns/op, both approximately 1484 B/op and 8 allocations/op, with zero drops. These results are not production throughput measurements.

Migration guidance: [packages reliability migration](../../migrations/packages-reliability.md). No dependency changes or commits were made.
