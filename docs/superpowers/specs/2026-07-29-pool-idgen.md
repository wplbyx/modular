# Bounded Worker Pool and ID Generation

## Worker Pool Contract

`packages/pool` is an asynchronous `core.Resource`. Construction validates configuration, `Setup` starts execution, and `Close` rejects new submissions before draining accepted work within the supplied shutdown context.

The two overload policies are explicit:

- `Reject` uses ants nonblocking admission and returns `ErrOverloaded` when no worker is available.
- `Queue` accepts work into a bounded process-local queue and returns as soon as the task is admitted. A full queue returns `ErrOverloaded`; submitters never wait unboundedly.

Task errors and panics are asynchronous outcomes. They are logged through the injected logger and exposed by `Stats`; they are not returned from a successful `Submit`. Task contexts combine the submitting context with the pool lifecycle, so forced shutdown cancels cooperative tasks.

The pool limits background execution. It does not replace ingress rate limiting, adaptive transport protection, circuit breaking, or synchronous bulkhead isolation.

## Bulkhead Contract

`packages/resilience.Bulkhead` limits synchronous executions. `MaxConcurrentCalls` bounds running calls and `QueueSize` bounds waiting calls. A zero or full queue rejects immediately with `ErrBulkheadFull`. Waiting calls honor their context, `WaitTimeout`, and `Close`.

## ID Generation Contract

`packages/idgen.Generator` returns an opaque string `ID`. Business code must not parse the encoding or infer the active generation strategy.

UUIDv7 is the default distributed option because it has no coordination dependency. Snowflake is available when decimal positive-int64 compatibility is required. Its `Layout` explicitly fixes epoch, time unit, and timestamp/node/sequence bit counts; the allocation must total 63 bits.

The default Snowflake layout is a millisecond 41/10/12 split with the epoch `2020-01-01T00:00:00Z`. Clock rollback fails closed, sequence exhaustion waits for the next time unit within the caller context, and a lost node lease stops generation.

Changing an active Snowflake epoch or bit layout creates a new ID namespace. Old and new layouts must not generate into the same namespace during a rolling deployment.

## Topology Assembly

Both strategies are exposed as `core.ManagedResource[idgen.Generator]`, so repositories depend on `core.Provider[idgen.Generator]` in either topology.

- Single-process UUIDv7 and microservice UUIDv7 use identical assembly.
- A single-process Snowflake generator can use an externally unique static node number.
- A microservice Snowflake generator uses a deployment-specific `NodeLeaseProvider` adapter. The core module deliberately does not bind Redis, Consul, or Kubernetes.

`core.ServiceNode.ID`, request IDs, business IDs, and Snowflake node IDs remain separate concepts and are not migrated implicitly.
