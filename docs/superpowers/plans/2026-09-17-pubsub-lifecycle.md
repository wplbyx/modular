# Pub/Sub lifecycle reliability delivery

Approved scope: MQTT, Redis Channel, RocketMQ PushConsumer, SubscriberEndpoint,
regression tests and CI. Preserve existing public signatures; apply bounded
backpressure; stop admission and drain before canceling at the shutdown deadline.
SSE, Kafka integration expansion and installed scaffold synchronization are deferred.

## Implementation

- [x] MQTT manual ACK after business success, bounded workers/queue, retry/panic
  handling, payload copy, per-subscription QoS restoration and persistent-session
  deliveries arriving before handler registration.
- [x] RocketMQ delayed SDK construction/start after handler registration,
  FAILURE for missing/failed handlers, single SDK operation owner and late cleanup.
- [x] Redis Channel bounded workers and direct SDK receive with backpressure,
  failure accounting and caller-owned Redis client preservation.
- [x] Context-aware close, bounded caller waits, cooperative cancellation, drain
  tracking, single-use endpoint state and compatibility for legacy subscribers.
- [x] Statistics/options, migration documentation, Linux race CI and real-Broker
  integration configuration with mandatory environment variables.

## Verification

| Check | Result |
| --- | --- |
| Windows uncached full suite | 42 test-bearing packages passed; nine existing localhost Redis tests skipped |
| Focused lifecycle regressions, 20 repetitions | Passed |
| Windows build and vet | Passed |
| Scaffold tests and self-check | Five tests passed; self-check passed |
| Integration test compilation and YAML parsing | Passed |
| Formatting and diff whitespace | Clean |
| Full Linux race suite in WSL | Passed during implementation; final refinements were not rechecked after the user requested stopping WSL validation |
| MQTT/Redis real Broker integration | Initial integration run passed; final Redis receive-loop refinements were not rerun after environment failure |
| RocketMQ real Broker integration | First run failed because advertised Proxy port differed from the host mapping; configuration corrected, rerun blocked by Docker Desktop failure |

The user explicitly requested no further WSL testing because the environment is
faulty. Full final-state Broker and Linux race acceptance remains pending; the
configured CI jobs have not been executed remotely. No commits or pushes were made.

Docker created the isolated modular-pubsub-test stack before its engine failed.
Cleanup could not be completed while the daemon was unavailable; after recovery,
run `docker compose -f tests/integration/pubsub/compose.yaml down -v`.

Migration: [pubsub-lifecycle](../../migrations/pubsub-lifecycle.md).
Broker setup and test commands: [integration instructions](../../../tests/integration/pubsub/README.md).
