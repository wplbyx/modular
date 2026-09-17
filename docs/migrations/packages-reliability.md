# packages reliability migration

This change preserves existing constructors and method signatures. The changes below correct behavior that callers may previously have depended on. No Go dependencies are added.

## Message delivery

- Kafka uses one fetch loop and bounded worker queues. A partition always uses one worker and processes messages in order; workers can share partitions, so a failed partition can exert backpressure on others. `Workers` limits concurrency, not partition count. Kafka consumer-group mode is required for committed restart positions.
- Kafka/Redis handler failure without a DLQ now retains and retries the message until cancellation. `MaxRetries` specifies when to move to a configured DLQ, not permission to discard messages. Backoff starts at the existing RetryBackoff, grows exponentially, uses jitter, and caps at 30 seconds.
- DLQ delivery must succeed before acknowledgment. Acknowledgment failures retry without rerunning a handler that already succeeded. Ambiguous broker outcomes can still duplicate delivery or DLQ entries; handlers must implement business idempotency.
- Redis first reads its own pending entries and also uses `XAUTOCLAIM` (Redis 6.2+) for idle entries. `WithStreamRecovery(interval, minIdle)` defaults to 30 seconds and 5 minutes. Choose minIdle above expected handler duration. Recovery does not make long-running handlers exclusive across instances.
- Redis `Block <= 0` is normalized to one second so a caller-owned connection cannot block consumption shutdown indefinitely on a permanent XREADGROUP wait. The injected Redis client is never closed by StreamClient.
- A Kafka consumer or Redis StreamClient permits one subscription during its lifetime. Create a separate instance for another subscription; repeated Subscribe and Subscribe after Close return errors. Unsubscribe cancels consumption.
- Kafka Producer resolves the default topic onto each message instead of specifying both Writer.Topic and Message.Topic, which kafka-go rejects.

## Disk storage

- Upload and multipart completion write a same-directory temporary file, check copy/close errors, then replace the destination. Failed reads and missing or invalid parts preserve an existing object. This is not a power-loss durability guarantee; rename semantics remain platform dependent.
- File operations use `os.Root`; paths and symlinks cannot escape the configured root. `GenKeyToFilePath` remains a path-formatting helper, not a safe substitute for Storage operations.
- Multipart IDs must be canonical UUIDs. A persisted manifest binds each session to its storage-root path and object key. Empty, malformed, foreign and mismatched sessions return errors. Do not construct a session manually.
- Pre-upgrade in-progress multipart sessions lack manifests and cannot be resumed. Complete them before upgrading, or restart the upload. Do not run blanket cleanup against the shared temporary directory.
- Part numbers must be positive and unique; supplied ETags are verified. Completion reads one part at a time. Legacy `MultipartTempDir` remains available for diagnostics but is not used to authorize operations.

## Redis ownership locks

- `IdempotentLock` provides execution mutual exclusion, not persisted result deduplication. Callback completion releases the lock, so a subsequent identical request can execute again.
- Renewal failure or ownership loss cancels the callback context immediately, and Run reports the cause. Explicit renewal checks use the same policy.
- Minimum TTL is 300ms; renewal runs every TTL/3 with an operation budget of TTL/3. Release uses an independent five-second budget.
- Callbacks must observe cancellation. A lock cannot forcibly stop a callback or fence writes to an independent database.

## Application, registry and protection

- Application owns the configured total shutdown budget. Each Close caller can stop waiting using its own context without canceling the shared shutdown procedure.
- Once stopping starts, readiness cannot become ready again. A late successful registration is deregistered with a separate bounded cleanup context; cleanup errors after Run has returned are logged.
- Run no longer waits forever for an Endpoint.Startup that ignores shutdown. If startup/setup or endpoint termination remains unresolved when the budget expires, resources that could still be in use are left open and the returned error identifies the unfinished phase. Arbitrary Go callbacks cannot be forcibly terminated.
- Consul rolls back successful transport registrations if a later registration fails. Resolver service names omit the URI path slash, IPv6 addresses are bracketed, and an empty discovery snapshot clears addresses. K8s watchers retain the latest snapshot and handle deletion tombstones.
- HTTP handler errors are rendered before protection/access accounting. HTTP/gRPC panic paths complete protection accounting once. Standalone circuit breakers ignore stale-generation results; saturated half-open admission returns ErrTooManyCalls.

## Caching

`WriteBehind.Set` means **cache update and queue admission**, not durable write completion. Full or closed queues reject before changing the cache. Already admitted setters may finish while Close is draining.

- Use `Close(ctx) error` for bounded draining and `FlushContext(ctx) error` for a completion watermark. Legacy Stop/Flush remain but cannot return write errors.
- Flush covers all tasks admitted before that call. Failures are sticky for the object's lifetime: a later Flush also reports an earlier failed write. A bounded summary retains the first failure and its sequence; `Stats().Failed` counts all failures.
- Writers are not automatically retried because their effects may be non-idempotent. Errors/panics become failed task results.
- Background writes preserve context values without inheriting request cancellation. `WithWriteBehindTimeout` defaults to 30 seconds. Close deadline expiry cancels cooperative writers.

`RefreshAhead` keeps one background refresh per key and defaults to eight concurrent refreshes. Entries unused for one TTL leave the refresh table. Failed cache writes do not advance successful-refresh time, and failures back off.

- `GetContext(ctx, key, loader)` passes a cancellable context to the loader.
- `NewRefreshAheadWithOptions` accepts `WithRefreshConcurrency` and `WithRefreshTimeout` (default 30 seconds).
- `Close(ctx)` stops scheduling, waits for refresh tasks, and cancels them on deadline. Legacy Get and Stop remain; a legacy callback that ignores cancellation can delay draining indefinitely.
- TTL <= 0 disables automatic refresh. Cache storage itself remains owned by the caller.

`Memoizer.PurgeExpired()` explicitly reclaims expired entries; no background goroutine is added. Expiry deletion rechecks the current entry under its write lock. WriteThrough remains source-write followed by cache-write and does **not** offer a cross-storage transaction.

## Configuration and telemetry

`NewSnapshotWatcher[T](interval, loaderOptions...)` reconstructs an independent loader for each poll. Run publishes a fresh validated object when settings change; the callback owns that object. Initial load failure returns an error; subsequent invalid updates go to `Errors()` without replacing the last published snapshot. The context stops polling, and callbacks must honor it. Options and captured Cobra flags must not be mutated concurrently. Remote backends without context-aware reads may finish a single outstanding read after cancellation.

Legacy Watch/WatchRemoteConfig are deprecated; they mutate a shared Viper instance and must not run concurrently with Load. SnapshotWatcher does not hot-reconfigure existing Resource instances automatically.

Telemetry adds the following configuration/Flags while preserving plaintext and always-sample defaults:

```yaml
Telemetry:
  Tracer: collector.example:4317
  Metric: collector.example:4317
  Logger: collector.example:4317
  UseTLS: true
  CAFile: ./collector-ca.pem
  CertFile: ""
  KeyFile: ""
  ServerName: collector.example
  Headers: [] # key=value; supply credentials through protected configuration
  Sampler: parentbased # always | never | parentbased | ratio
  SampleRatio: 0.1
```

All providers are constructed before publishing global references. Only one active telemetry Resource may own process providers; Close restores previous references if still owned. Public Tp/Mp/Lp fields are lifecycle diagnostics and must not be read concurrently with Setup/Close. CertFile and KeyFile are paired; TLS-related settings require UseTLS.

FTP and RabbitMQ config types remain for compatibility and are deprecated: no corresponding implementation is shipped here.

## Other runtime behavior

- Health Manager shares one in-flight execution per checker across overlapping probes. A checker that ignores cancellation occupies that slot until it returns, preventing unlimited duplicate calls.
- EventBus normal close drains ordered events. An expired close budget cancels handler context; pending canceled events are reflected in Stats.Canceled. No persistent delivery guarantee is added.
- GORM automatic ping is disabled; the explicit PingContext owns validation. Dialector initialization may still have its own network work and timeouts.
- SSE emits a `data:` field for every data line, rejects newlines in Event/ID and NUL in ID, unregisters on write failure and refuses connections after shutdown.
- HTTP client Config.MaxResponseBytes limits successful buffered helpers and Download; zero keeps the old unlimited behavior. Do still returns a stream owned by the caller. Exceeding the limit returns ErrResponseTooLarge.
- `PostMultipartStream(ctx, url, fields, filePaths)` streams local files without buffering or replay retries. `util.DoRequestWithLimit` adds an optional buffered-response cap to the legacy helper.
- Logging keeps the existing RingMPSC/sink architecture. Added tests cover dynamic detach; existing slow/full-sink tests remain the backpressure checks. Microbenchmarks measure enqueue/write overhead, not production throughput.
